// Package filelock is an advisory, cross-process lock on a file: flock(2) on
// Linux, LockFileEx on Windows. The kernel releases it when the holding
// process dies, so there is no "is this lock stale" judgement to get wrong —
// the failure mode of lock files created with O_EXCL.
//
// Locks belong to an **open file**, not to a process: two TryLock calls on
// the same path from one process conflict exactly as two processes would.
// That is what makes one mechanism serve both "another asa-server process"
// and "another goroutine in this one" — and it also means a caller that
// already holds a lock and asks for it again waits for itself. Callers that
// can nest must track that themselves (see pkg/umuruntime's Host.Lock).
package filelock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// ErrLocked means the lock is held by another open file — another process,
// or another holder in this process.
var ErrLocked = errors.New("filelock: held by another holder")

// Mode selects a shared (many holders, no exclusive one) or an exclusive lock.
type Mode int

const (
	Shared Mode = iota
	Exclusive
)

// TryLock takes the lock on path without waiting, creating the file (and its
// directory) if needed. ErrLocked when it is held in a conflicting mode. The
// returned func releases the lock and closes the file; it is idempotent.
func TryLock(path string, mode Mode) (release func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	unlock, err := tryLockFile(f, mode)
	if err != nil {
		f.Close()
		return nil, err
	}
	done := false
	return func() {
		if done {
			return
		}
		done = true
		unlock()
		f.Close()
	}, nil
}

// Lock waits for the lock, retrying every poll until ctx ends. waiting, when
// non-nil, is called after each failed attempt with the time spent so far —
// for a caller that wants to say, once, that it is waiting.
func Lock(ctx context.Context, path string, mode Mode, poll time.Duration,
	waiting func(elapsed time.Duration)) (release func(), err error) {

	start := time.Now()
	for {
		release, err := TryLock(path, mode)
		if err == nil {
			return release, nil
		}
		if !errors.Is(err, ErrLocked) {
			return nil, err
		}
		if waiting != nil {
			waiting(time.Since(start))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(poll):
		}
	}
}
