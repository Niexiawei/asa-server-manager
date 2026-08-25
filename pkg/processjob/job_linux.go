//go:build linux

package processjob

import (
	"context"
	"errors"
	"os/exec"
)

// errNotImplemented 标记「编译期存根」。真正的 Linux 进程树管理（setsid 进程组 +
// kill(-pgid)，见 docs/LINUX_COMPATIBILITY_PLAN.md §3.2 的 pkg/proctree 改名计划）
// 要到 P1 才落地，这里先保证依赖方能编译。
var errNotImplemented = errors.New("processjob: not implemented on linux yet")

// ProcessJob is the Linux-side placeholder for the Windows Job Object handle.
type ProcessJob struct {
	cmd *exec.Cmd
}

// Start starts cmd. On Linux this stub always fails; use setsid-based process
// group management once pkg/proctree lands (P1).
func Start(ctx context.Context, cmd *exec.Cmd) (*ProcessJob, error) {
	return nil, errNotImplemented
}

// Close is a no-op stub; safe to call on a nil receiver.
func (p *ProcessJob) Close() {}

// Wait is a no-op stub; safe to call on a nil receiver.
func (p *ProcessJob) Wait() error {
	if p == nil || p.cmd == nil {
		return nil
	}
	return p.cmd.Wait()
}
