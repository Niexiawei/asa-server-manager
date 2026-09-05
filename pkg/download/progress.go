package download

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ProgressLogger throttles an Options.Progress callback into human-readable
// percentage lines: at most one line per 5% (or 2 seconds when the total size
// is unknown), plus the final line. label prefixes each line (e.g. the file
// being fetched) and logf receives the formatted message.
//
// Kept separate from the byte-level Progress callback itself so a caller with
// several concurrent/sequential downloads can get one throttled logger per
// label without fighting over shared throttle state.
func ProgressLogger(label string, logf func(string, ...any)) func(done, total int64) {
	var (
		lastAt  time.Time
		lastPct = -1
	)
	return func(done, total int64) {
		pct := 0
		if total > 0 {
			pct = int(done * 100 / total)
		}
		final := total > 0 && done >= total
		now := time.Now()
		if !final && pct < lastPct+5 && now.Sub(lastAt) < 2*time.Second {
			return
		}
		lastAt, lastPct = now, pct
		if total > 0 {
			logf("  %s: %d%% (%.1f/%.1f MiB)", label, pct, mib(done), mib(total))
		} else {
			logf("  %s: %.1f MiB", label, mib(done))
		}
	}
}

func mib(n int64) float64 { return float64(n) / (1 << 20) }

// ResolveFinalURL follows redirects with a HEAD request and returns the URL
// the response actually landed on — useful when a stable "latest" URL
// redirects to a versioned one that embeds information the caller wants
// (e.g. a checksum in the path).
func ResolveFinalURL(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := Client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HEAD %s 返回 %s", rawURL, resp.Status)
	}
	if resp.Request == nil || resp.Request.URL == nil {
		return "", fmt.Errorf("HEAD %s 没有返回最终地址", rawURL)
	}
	return resp.Request.URL.String(), nil
}
