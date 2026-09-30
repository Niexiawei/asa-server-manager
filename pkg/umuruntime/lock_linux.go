//go:build linux

package umuruntime

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"asa-server/pkg/filelock"
)

// runtimeLockFile is the cross-process lock for "preparing or writing the
// shared runtime": downloading umu/GE-Proton, warming and provisioning the
// shared prefix, running a verification launch against it.
//
// The in-process mutex (Host.ensureMu) does nothing for the case that
// actually happens: `asa-server setup` from a terminal while the service is
// up, both warming the same WINEPREFIX — two wineboots in one prefix, or one
// process moving the prefix aside (a Proton version bump) while the other is
// still writing into it. Two concurrent downloads of the same archive also
// append to the same .part file. See
// docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md §4.2 D3.
const runtimeLockFile = ".umu-runtime.lock"

const (
	lockPollEvery   = 500 * time.Millisecond
	lockAnnounceAge = 2 * time.Second
)

// heldLockKey marks, in a context, which runtime lock the call chain carrying
// that context already holds (the value is the lock file's path).
type heldLockKey struct{}

// Lock takes the cross-process runtime lock and returns a context that
// records it, waiting for another holder (logging once, after a short while,
// that it is waiting). ctx cancels the wait; the returned func releases the
// lock.
//
// # Nesting
//
// The underlying file lock belongs to an open file, not to a process, so a
// chain that already holds it and asks again would wait for itself forever
// (pkg/filelock). Lock therefore recognises its own holder through the
// context: when ctx already carries this lock it returns at once with a
// no-op release. Pass the returned context down — that is all nesting needs.
// Concurrent callers with their own contexts still queue for the lock, which
// is the point of it.
//
// A no-op when no BaseDir is configured.
func (h *Host) Lock(ctx context.Context, logf func(string, ...any)) (context.Context, func(), error) {
	base := h.config().Umu.BaseDir
	if base == "" {
		return ctx, func() {}, nil
	}
	path := filepath.Join(base, runtimeLockFile)
	if held, _ := ctx.Value(heldLockKey{}).(string); held == path {
		return ctx, func() {}, nil
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}

	announced := false
	release, err := filelock.Lock(ctx, path, filelock.Exclusive, lockPollEvery, func(elapsed time.Duration) {
		if !announced && elapsed >= lockAnnounceAge {
			logf("另一个 asa-server 进程正在准备 Wine/Proton 运行时（%s），等待它完成……", path)
			announced = true
		}
	})
	if err != nil {
		return ctx, nil, fmt.Errorf("获取运行时锁 %s 失败: %w", path, err)
	}
	return context.WithValue(ctx, heldLockKey{}, path), release, nil
}
