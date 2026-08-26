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
	"strings"
	"time"
)

// ErrChecksumMismatch is returned (wrapped) when Options.Checksum is set and
// the downloaded content doesn't match.
var ErrChecksumMismatch = errors.New("download: checksum mismatch")

// Options describes a single download.
type Options struct {
	URL      string
	Dest     string // final path; a same-directory .part file is used while downloading
	Checksum string // optional, "sha256:<hex>" or "sha512:<hex>"; empty skips verification
	Resume   bool   // continue a previous .part file via a Range request instead of restarting
	Progress func(done, total int64)
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
	openFlag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if opt.Resume {
		if fi, err := os.Stat(partPath); err == nil {
			startOffset = fi.Size()
			openFlag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if startOffset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startOffset))
	}

	resp, err := httpClient.Load().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Server ignored (or we didn't send) Range — restart from scratch.
		startOffset = 0
		openFlag = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	case http.StatusPartialContent:
		// Resuming; openFlag/startOffset already set above.
	default:
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.OpenFile(partPath, openFlag, 0o644)
	if err != nil {
		return err
	}

	total := startOffset + resp.ContentLength
	written := startOffset
	if opt.Progress != nil {
		opt.Progress(written, total)
	}

	_, copyErr := io.Copy(out, &progressReader{r: resp.Body, done: &written, total: total, cb: opt.Progress})
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}

	if opt.Checksum != "" {
		if err := verifyChecksum(partPath, opt.Checksum); err != nil {
			os.Remove(partPath)
			return err
		}
	}

	return os.Rename(partPath, opt.Dest)
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
