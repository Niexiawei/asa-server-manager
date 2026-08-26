//go:build linux

package proctree

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"syscall"
)

// ProcessJob is the Linux-side process-group handle. Setsid makes cmd its
// own session and process-group leader (pgid == its own pid), so Close() can
// reach the whole tree via kill(-pgid) — the setsid+kill(-pgid) equivalent of
// the Windows Job Object's KILL_ON_JOB_CLOSE guarantee (see
// docs/LINUX_COMPATIBILITY_PLAN.md §3.2/§5.4).
type ProcessJob struct {
	cmd  *exec.Cmd
	pgid int
	once sync.Once
}

// Start starts cmd in its own session (setsid) and process group, then
// returns a handle whose Close() kills the whole group.
func Start(ctx context.Context, cmd *exec.Cmd) (*ProcessJob, error) {
	if cmd == nil {
		return nil, errors.New("proctree: cmd is nil")
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	pj := &ProcessJob{
		cmd:  cmd,
		pgid: cmd.Process.Pid, // Setsid: leader's pgid equals its own pid
	}

	if ctx != nil {
		go func() {
			<-ctx.Done()
			pj.Close()
		}()
	}

	return pj, nil
}

// Close force-terminates every process in the group. Safe to call multiple
// times and on a nil receiver.
func (p *ProcessJob) Close() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		if p.pgid > 1 {
			_ = syscall.Kill(-p.pgid, syscall.SIGKILL)
		}
	})
}

// Wait waits for the root process to exit.
func (p *ProcessJob) Wait() error {
	if p == nil || p.cmd == nil {
		return nil
	}
	return p.cmd.Wait()
}
