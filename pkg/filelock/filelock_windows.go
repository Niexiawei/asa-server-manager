//go:build windows

package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockFile locks the file's first byte. The byte range is arbitrary but
// must be the same for every holder; the file's content is never used.
func tryLockFile(f *os.File, mode Mode) (unlock func(), err error) {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if mode == Exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(h, flags, 0, 1, 0, ol); err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(h, 0, 1, 0, ol) }, nil
}
