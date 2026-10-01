// Package download is the single downloader every large-file fetch in this
// program should go through (SteamCMD today; umu/GE-Proton/Syncthing once
// the Linux runtime lands — see docs/LINUX_COMPATIBILITY_PLAN.md §5.13).
// Centralizing it means proxy configuration, retry, and checksum
// verification are written once instead of once per call site.
package download

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrChecksumMismatch is returned (wrapped) when Options.Checksum is set and
// the downloaded content doesn't match.
var ErrChecksumMismatch = errors.New("download: checksum mismatch")

// ErrTooLarge is returned (wrapped) when Options.MaxBytes is set and the file
// is — or turns out while streaming to be — larger than that. Fetch does not
// retry it: the same server will send the same oversized body again.
var ErrTooLarge = errors.New("download: file exceeds MaxBytes")

// Options describes a single download.
type Options struct {
	URL      string
	Dest     string // final path; a same-directory .part file is used while downloading
	Checksum string // optional, "sha256:<hex>" or "sha512:<hex>"; empty skips verification
	Resume   bool   // continue a previous .part file via a Range request instead of restarting
	Progress func(done, total int64)
	// MaxBytes caps the size of the finished file (including any resumed
	// prefix). 0 means no limit. A declared length over the cap is refused
	// before anything is written; a body that runs past it — a server sending
	// more than it announced — is cut off and its .part deleted, so a
	// misbehaving mirror cannot fill the disk.
	MaxBytes int64
}

// Fetch downloads a URL to Options.Dest, retrying on failure per the
// package's configured Config.Retries. GitHub asset URLs are transparently
// rewritten to go through the configured proxy (see Configure).
func Fetch(ctx context.Context, opt Options) error {
	if opt.URL == "" {
		return errors.New("download: URL is required")
	}
	if opt.Dest == "" {
		return errors.New("download: Dest is required")
	}

	cfg := current.Load()
	url := rewriteGithubURL(opt.URL, cfg.GithubProxy)

	var lastErr error
	for attempt := 0; attempt < cfg.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		if err := fetchOnce(ctx, url, opt); err != nil {
			if errors.Is(err, ErrTooLarge) {
				return fmt.Errorf("download %s: %w", opt.URL, err)
			}
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("download %s: %w", opt.URL, lastErr)
}

func backoff(attempt int) time.Duration {
	return time.Duration(attempt) * 2 * time.Second
}

func fetchOnce(ctx context.Context, url string, opt Options) error {
	if err := os.MkdirAll(filepath.Dir(opt.Dest), 0o755); err != nil {
		return err
	}
	partPath := opt.Dest + ".part"

	var startOffset int64
	if opt.Resume {
		if fi, err := os.Stat(partPath); err == nil {
			startOffset = fi.Size()
		}
	}
	if opt.MaxBytes > 0 && startOffset > opt.MaxBytes {
		// A .part already past the cap can only be garbage; start over.
		os.Remove(partPath)
		startOffset = 0
	}

	resp, startOffset, total, err := get(ctx, url, partPath, startOffset)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if opt.MaxBytes > 0 && total > opt.MaxBytes {
		os.Remove(partPath)
		return fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, total, opt.MaxBytes)
	}

	openFlag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if startOffset > 0 {
		openFlag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	out, err := os.OpenFile(partPath, openFlag, 0o644)
	if err != nil {
		return err
	}

	written := startOffset
	if opt.Progress != nil {
		opt.Progress(written, total)
	}

	var body io.Reader = resp.Body
	if opt.MaxBytes > 0 {
		// One byte past the cap is enough to tell "exactly at the limit" from
		// "over it" without reading the rest of an oversized body.
		body = io.LimitReader(body, opt.MaxBytes-startOffset+1)
	}
	_, copyErr := io.Copy(out, &progressReader{r: body, done: &written, total: total, cb: opt.Progress})
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if opt.MaxBytes > 0 && written > opt.MaxBytes {
		os.Remove(partPath)
		return fmt.Errorf("%w: body ran past %d bytes", ErrTooLarge, opt.MaxBytes)
	}

	if opt.Checksum != "" {
		if err := verifyChecksum(partPath, opt.Checksum); err != nil {
			os.Remove(partPath)
			return err
		}
	}

	return os.Rename(partPath, opt.Dest)
}

// get issues the GET, asking to resume at offset when it is positive. It
// returns the response together with the offset its body actually starts at
// (0 when the server sent the whole file) and the full file size, or -1 when
// the server didn't say.
//
// A 206 is only trusted when its Content-Range starts exactly at offset:
// appending a range that starts anywhere else would splice bytes at the wrong
// position with nothing downstream to notice (most callers have no checksum).
// Such a response — or one without a parseable Content-Range — drops the .part
// and asks once more for the whole file, within the same attempt.
func get(ctx context.Context, url, partPath string, offset int64) (*http.Response, int64, int64, error) {
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, 0, 0, err
		}
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}

		resp, err := httpClient.Load().Do(req)
		if err != nil {
			return nil, 0, 0, err
		}

		switch resp.StatusCode {
		case http.StatusOK:
			// Server ignored (or we didn't send) Range — restart from scratch.
			return resp, 0, resp.ContentLength, nil
		case http.StatusPartialContent:
			if offset > 0 {
				if start, size, ok := parseContentRange(resp.Header.Get("Content-Range")); ok && start == offset {
					if size < 0 && resp.ContentLength >= 0 {
						size = offset + resp.ContentLength
					}
					return resp, offset, size, nil
				}
			}
			resp.Body.Close()
			if offset == 0 {
				return nil, 0, 0, fmt.Errorf("bad status: %s for a request without Range", resp.Status)
			}
			os.Remove(partPath)
			offset = 0
		default:
			resp.Body.Close()
			return nil, 0, 0, fmt.Errorf("bad status: %s", resp.Status)
		}
	}
}

// parseContentRange parses "bytes <start>-<end>/<size>". size is -1 when the
// server sent "*".
func parseContentRange(v string) (start, size int64, ok bool) {
	unit, spec, found := strings.Cut(strings.TrimSpace(v), " ")
	if !found || unit != "bytes" {
		return 0, 0, false
	}
	rng, sizeStr, found := strings.Cut(spec, "/")
	if !found {
		return 0, 0, false
	}
	startStr, endStr, found := strings.Cut(rng, "-")
	if !found {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 {
		return 0, 0, false
	}
	end, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil || end < start {
		return 0, 0, false
	}
	if sizeStr == "*" {
		return start, -1, true
	}
	size, err = strconv.ParseInt(sizeStr, 10, 64)
	if err != nil || size <= end {
		return 0, 0, false
	}
	return start, size, true
}

type progressReader struct {
	r     io.Reader
	done  *int64
	total int64
	cb    func(done, total int64)
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		*p.done += int64(n)
		if p.cb != nil {
			p.cb(*p.done, p.total)
		}
	}
	return n, err
}

func verifyChecksum(path, checksum string) error {
	algo, want, ok := strings.Cut(checksum, ":")
	if !ok {
		return fmt.Errorf("download: invalid checksum %q, want \"algo:hex\"", checksum)
	}

	var h hash.Hash
	switch algo {
	case "sha256":
		h = sha256.New()
	case "sha512":
		// GE-Proton's release page only publishes a .sha512sum companion
		// file (no sha256) — see docs/LINUX_COMPATIBILITY_PLAN.md §5.13/§4.3.
		h = sha512.New()
	default:
		return fmt.Errorf("download: unsupported checksum algorithm %q", algo)
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("%w: want %s got %s", ErrChecksumMismatch, want, got)
	}
	return nil
}
