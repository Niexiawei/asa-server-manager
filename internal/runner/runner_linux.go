//go:build linux

package runner

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"

	"asa-server/pkg/logger"
	"asa-server/pkg/umu"
	"asa-server/pkg/umuruntime"

	"github.com/aymanbagabas/go-pty"
)

// run wraps exePath in umu-run so the Windows PE executes under the pinned
// GE-Proton build. Treats every exe identically — see the package doc
// comment on why AsaApiLoader.exe gets no special handling here.
func run(ctx context.Context, exePath string, args []string, opt Options) (*Handle, error) {
	c, err := umuCommandLine(ctx, exePath, args, opt)
	if err != nil {
		return nil, err
	}
	env := c.Env

	// AsaApiLoader.exe creates real Win32 windows, so under Wine it needs an X
	// display even though the workload is a headless game server: without one
	// CreateWindow fails and the loader exits with code 3 having written
	// nothing at all — no console output, not even its own logs/ directory
	// (measured 2026-08-30, see the xdisplay package doc). Fail fast with something
	// actionable instead of reporting a "started" instance that is already
	// dead. Applied after the runtime-user env rewrite on purpose — see
	// display.Target.Apply.
	if opt.NeedsDisplay {
		disp, blocked, dispErr := acquireDisplay()
		switch {
		case blocked != "":
			// 这台机器压根没有显示能力。
			return nil, fmt.Errorf("无法启动 %s：它需要图形显示，但%s。"+
				"AsaApiLoader.exe（ArkApi）在 Wine 下没有显示会静默退出，"+
				"所以这里提前拒绝，而不是让实例假装启动成功",
				filepath.Base(exePath), blocked)
		case dispErr != nil:
			// 有能力但这次没拿到（多半是 Xvfb 起不来）。这条失败面是自管 Xvfb
			// 才有的 —— xvfb-run 在 Xvfb 起不来时照跑命令，于是同样的故障以前是
			// 静默的，只能从「加载器零输出退出」反推。
			return nil, fmt.Errorf("无法启动 %s：拿不到图形显示。%w",
				filepath.Base(exePath), dispErr)
		}
		logger.Infof("runner: %s 需要图形显示，本次使用 %s", filepath.Base(exePath), disp.How)
		env = disp.Apply(env)
	}

	if opt.PTY {
		return runPTY(ctx, c.Path, c.Args, env, c.Credential, opt)
	}

	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Dir = opt.Dir
	cmd.Env = env
	cmd.Stdin = nil
	if opt.Log != nil {
		cmd.Stdout, cmd.Stderr = opt.Log, opt.Log
	}
	// Setsid: umu-run execs through bwrap -> wine -> the actual game
	// process, a whole tree. Giving it its own session/process group is
	// what lets a later kill(-pgid) reach all of it instead of orphaning
	// bwrap/wineserver — see docs/LINUX_COMPATIBILITY_PLAN.md §5.4/§5.6
	// risk 9. It's also what decouples the launch from this program's own
	// controlling terminal, matching Windows's HideWindow intent.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Credential: c.Credential}

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Handle{
		LauncherPID: cmd.Process.Pid,
		Process:     cmd.Process,
		Wait:        cmd.Wait,
	}, nil
}

func runPTY(ctx context.Context, bin string, args, env []string, cred *syscall.Credential, opt Options) (*Handle, error) {
	pp, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("failed to open pty: %w", err)
	}
	w, h := ptySize(opt)
	_ = pp.Resize(w, h)

	// The slave pts is created owned by this (root) process; the dropped
	// child needs to own it to open it as its controlling terminal.
	// See docs/UMU_RUNTIME_USER_PLAN.md §9 risk 1 — this path (AsaApiLoader
	// under a dropped user) is still unverified on real hardware.
	if cred != nil {
		if up, ok := pp.(pty.UnixPty); ok {
			_ = up.Slave().Chown(int(cred.Uid), int(cred.Gid))
		}
	}

	c := pp.CommandContext(ctx, bin, args...)
	c.Dir = opt.Dir
	c.Env = env
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Credential: cred}
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

// checkRuntime verifies umu-run, the pinned GE-Proton build and the shared
// Wine prefix are all present, with no network access — the same
// preconditions every launch enforces, factored out so business-layer
// callers can probe readiness up front. Error text is end-user facing.
func checkRuntime() error { return withSetupHint(hostFor(getConfig()).Check()) }

// sharesWinePrefix asks wineprefix.Manager.SharesPrefix, which derives the
// answer from Dir: would two different instances land in the same prefix
// directory?
//
// It used to be a hand-written mode check (`PrefixMode != "per-instance"`),
// which was correct with exactly two modes and silently wrong the moment a
// third arrived — "overlay" isolates prefixes too, and a stale check would
// have serialized its launches and rejected its second ArkApi instance, with
// no error anywhere to explain either. Deriving the answer from Dir makes
// drift impossible: whatever Dir decides IS the sharing model.
//
// The failure direction is also the safe one. An unconfigured (zero-value)
// Config, or an unrecognized mode string, falls through Dir to the one
// shared prefix — so this returns true, and the caller gets the launch gate
// and the ArkApi exclusion. Over-serializing costs time; under-serializing
// costs a three-minute hang and an orphaned process tree.
func sharesWinePrefix() bool { return hostFor(getConfig()).Prefixes().SharesPrefix() }

// umuCommandLine builds the umu-run invocation for exePath/args — argv, the
// environment scripts/ark_instance_manager.sh proved (including
// PROTON_VERB=run), and the credential to drop to. See
// umuruntime.Host.Command for the layering.
func umuCommandLine(ctx context.Context, exePath string, args []string, opt Options) (*umuruntime.Command, error) {
	c, err := hostFor(getConfig()).Command(ctx, exePath, args, umuruntime.LaunchSpec{
		PrefixKey: opt.PrefixKey,
		Env:       opt.Env,
	})
	return c, withSetupHint(err)
}

// gamePath is the platform seam for runner.GamePath (runner_windows.go has
// the identity version), so it stays a local name even though the mechanism
// itself now lives in asa-server/pkg/umu.
func gamePath(hostPath string) string { return umu.GamePath(hostPath) }

// launcherIsDirect: umu-run is an OS-level wrapper — Handle.LauncherPID is
// umu-run's own PID, not the Windows exe's, which is Wine's problem to
// eventually launch as some descendant process.
func launcherIsDirect() bool { return false }
