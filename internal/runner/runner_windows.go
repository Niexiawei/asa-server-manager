//go:build windows

package runner

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/aymanbagabas/go-pty"
)

// run is the Windows implementation: exePath is launched directly, exactly
// as internal/instance/server.go does today (this package isn't wired into
// that call site yet — see docs/LINUX_COMPATIBILITY_PLAN.md P4).
func run(ctx context.Context, exePath string, args []string, opt Options) (*Handle, error) {
	if opt.PTY {
		return runPTY(ctx, exePath, args, opt)
	}

	cmd := exec.CommandContext(ctx, exePath, args...)
	cmd.Dir = opt.Dir
	if opt.Env != nil {
		cmd.Env = opt.Env
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Handle{
		LauncherPID: cmd.Process.Pid,
		Process:     cmd.Process,
		Wait:        cmd.Wait,
	}, nil
}

func runPTY(ctx context.Context, exePath string, args []string, opt Options) (*Handle, error) {
	pp, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("failed to open pty: %w", err)
	}
	w, h := ptySize(opt)
	_ = pp.Resize(w, h)

	c := pp.CommandContext(ctx, exePath, args...)
	c.Dir = opt.Dir
	if opt.Env != nil {
		c.Env = opt.Env
	}
	if err := c.Start(); err != nil {
		_ = pp.Close()
		return nil, err
	}
	return &Handle{
		LauncherPID: c.Process.Pid,
		PTY:         pp,
		Wait:        c.Wait,
	}, nil
}

// gamePath is identity on Windows — paths are already in the form the exe
// expects.
func gamePath(hostPath string) string { return hostPath }

// launcherIsDirect: exec.Command launches exePath itself, no OS-level
// wrapper — Handle.LauncherPID is always exePath's own PID.
func launcherIsDirect() bool { return true }

// ensureRuntime is a no-op on Windows: there is no Wine/Proton runtime to
// download or warm.
func ensureRuntime(ctx context.Context, progress io.Writer) error { return nil }

// preflight has nothing to check on Windows.
func preflight() []Problem { return nil }
