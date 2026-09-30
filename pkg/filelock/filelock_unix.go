//go:build !windows

package filelock

import (
	"errors"
	"os"
	"syscall"
)

func tryLockFile(f *os.File, mode Mode) (unlock func(), err error) {
	how := syscall.LOCK_SH
	if mode == Exclusive {
		how = syscall.LOCK_EX
	}
	fd := int(f.Fd())
	if err := syscall.Flock(fd, how|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() { _ = syscall.Flock(fd, syscall.LOCK_UN) }, nil
}
