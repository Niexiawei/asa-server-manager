//go:build linux

package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/aymanbagabas/go-pty"
)

// run wraps exePath in umu-run so the Windows PE executes under the pinned
// GE-Proton build. Treats every exe identically — see the package doc
// comment on why AsaApiLoader.exe gets no special handling here.
func run(ctx context.Context, exePath string, args []string, opt Options) (*Handle, error) {
	bin, launchArgs, env, err := umuCommandLine(exePath, args, opt)
	if err != nil {
		return nil, err
	}

	// Drop the umu-run child (and everything bwrap/wine spawns below it) to
	// the dedicated non-root user when asa-server runs as root — see
	// docs/UMU_RUNTIME_USER_PLAN.md. cred is nil (no drop) when euid != 0
	// or umu_run_as_root=true.
	cred, home, err := resolveRuntimeCredential(getConfig())
	if err != nil {
		return nil, err
	}
	if cred != nil {
		env = runtimeEnv(env, home, runtimeUserName(getConfig()))
	}

	if opt.PTY {
		return runPTY(ctx, bin, launchArgs, env, cred, opt)
	}

	cmd := exec.CommandContext(ctx, bin, launchArgs...)
	cmd.Dir = opt.Dir
	cmd.Env = env
	cmd.Stdin = nil
	// Setsid: umu-run execs through bwrap -> wine -> the actual game
	// process, a whole tree. Giving it its own session/process group is
	// what lets a later kill(-pgid) reach all of it instead of orphaning
	// bwrap/wineserver — see docs/LINUX_COMPATIBILITY_PLAN.md §5.4/§5.6
	// risk 9. It's also what decouples the launch from this program's own
	// controlling terminal, matching Windows's HideWindow intent.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Credential: cred}

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
// preconditions umuCommandLine enforces, factored out so business-layer
// callers can probe readiness up front. Error text is end-user facing.
func checkRuntime() error {
	cfg := getConfig()

	bin := umuRunPath(cfg)
	if fi, err := os.Stat(bin); err != nil || fi.Mode()&0111 == 0 {
		return fmt.Errorf("Wine/Proton 运行时尚未初始化：缺少 umu-run（%s）。请运行 asa-server setup 完成环境准备", bin)
	}
	proton := protonPath(cfg)
	if fi, err := os.Stat(filepath.Join(proton, "proton")); err != nil || fi.IsDir() {
		return fmt.Errorf("Wine/Proton 运行时尚未初始化：缺少 %s（%s）。请运行 asa-server setup 完成环境准备", cfg.ProtonVersion, proton)
	}
	prefix := prefixDir(cfg, "")
	if _, err := os.Stat(filepath.Join(prefix, "system.reg")); err != nil {
		return fmt.Errorf("Wine 前缀尚未初始化：%s。请运行 asa-server setup 完成环境准备", prefix)
	}
	return nil
}

// umuCommandLine builds the umu-run invocation for exePath/args, matching
// scripts/ark_instance_manager.sh's proven env var set exactly (notably: no
// PROTON_VERB — the reference script doesn't set it, and umu-run's default
// is already correct for running a game exe).
func umuCommandLine(exePath string, args []string, opt Options) (bin string, launchArgs []string, env []string, err error) {
	if err := checkRuntime(); err != nil {
		return "", nil, nil, err
	}
	cfg := getConfig()

	// Run the umu-launcher zipapp under an explicitly resolved interpreter
	// rather than its "#!/usr/bin/env python3" shebang — the system default
	// may be older than the 3.10 umu needs. See docs/UMU_PYTHON_DISCOVERY_PLAN.md.
	py, err := umuInterpreter()
	if err != nil {
		return "", nil, nil, err
	}

	bin = py.Path
	proton := protonPath(cfg)

	// checkRuntime validated the default shared prefix; a per-instance launch
	// (Options.PrefixKey set under PrefixMode "per-instance") uses a distinct
	// directory that still has to exist.
	prefix := prefixDir(cfg, opt.PrefixKey)
	if _, statErr := os.Stat(prefix); statErr != nil {
		return "", nil, nil, fmt.Errorf("runner: Wine prefix not found at %s (call EnsureRuntime first): %w", prefix, statErr)
	}

	// argv: <python> <umu-run> <exe> <exe args...>
	launchArgs = append([]string{umuRunPath(cfg), exePath}, args...)

	baseEnv := opt.Env
	if baseEnv == nil {
		baseEnv = os.Environ()
	}
	env = append(append([]string{}, baseEnv...),
		"WINEPREFIX="+prefix,
		"GAMEID="+cfg.GameID,
		"PROTONPATH="+proton,
		// Regular launches keep the runtime pinned; only the one-time
		// warmPrefix() call in umu_linux.go omits this, on purpose.
		"UMU_RUNTIME_UPDATE=0",
	)
	return bin, launchArgs, env, nil
}

// gamePath: Wine maps its Z: drive to /, so a host path such as
// /home/x/asa becomes Z:\home\x\asa on the launched exe's command line.
func gamePath(hostPath string) string {
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		abs = hostPath
	}
	return "Z:" + strings.ReplaceAll(abs, "/", `\`)
}

// launcherIsDirect: umu-run is an OS-level wrapper — Handle.LauncherPID is
// umu-run's own PID, not the Windows exe's, which is Wine's problem to
// eventually launch as some descendant process.
func launcherIsDirect() bool { return false }
