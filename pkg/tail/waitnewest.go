package tail

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// WaitNewest blocks until dir contains a (non-directory) entry whose name
// satisfies match and whose ModTime is not before notBefore, polling every
// poll interval, or until ctx is done — whichever comes first. A missing dir
// is treated the same as "nothing matches yet", not an error: the directory
// commonly does not exist until whatever creates it has run at least once.
//
// notBefore exists because the directory can hold files from earlier runs:
// without a lower bound on ModTime, a stale leftover would be picked up and
// mistaken for the current run's output.
//
// On cancellation WaitNewest takes one last look before giving up, since the
// file can land in the same instant the caller stops waiting — a caller
// combining a "give up" signal with ctx cancellation should not lose a file
// that arrived right at that boundary.
func WaitNewest(ctx context.Context, dir string, notBefore time.Time, match func(name string) bool, poll time.Duration) (string, error) {
	for {
		if path, ok := newestMatch(dir, notBefore, match); ok {
			return path, nil
		}
		select {
		case <-ctx.Done():
			if path, ok := newestMatch(dir, notBefore, match); ok {
				return path, nil
			}
			return "", ctx.Err()
		case <-time.After(poll):
		}
	}
}

func newestMatch(dir string, notBefore time.Time, match func(name string) bool) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}

	var (
		newestPath string
		newestAt   time.Time
	)
	for _, e := range entries {
		if e.IsDir() || !match(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		// Before 而非 !After：mtime 与 notBefore 相等时算数。
		if info.ModTime().Before(notBefore) {
			continue
		}
		if newestPath == "" || info.ModTime().After(newestAt) {
			// filepath.Base 收敛：文件名是调用方约定的格式，不是我们生成的。
			newestPath, newestAt = filepath.Join(dir, filepath.Base(e.Name())), info.ModTime()
		}
	}
	return newestPath, newestPath != ""
}
