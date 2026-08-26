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

	if opt.PTY {
		return runPTY(ctx, bin, launchArgs, env, opt)
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Handle{
		LauncherPID: cmd.Process.Pid,
		Process:     cmd.Process,
		Wait:        cmd.Wait,
	}, nil
}

func runPTY(ctx context.Context, bin string, args, env []string, opt Options) (*Handle, error) {
	pp, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("failed to open pty: %w", err)
	}
	w, h := ptySize(opt)
	_ = pp.Resize(w, h)

	c := pp.CommandContext(ctx, bin, args...)
	c.Dir = opt.Dir
	c.Env = env
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
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

// umuCommandLine builds the umu-run invocation for exePath/args, matching
// scripts/ark_instance_manager.sh's proven env var set exactly (notably: no
// PROTON_VERB — the reference script doesn't set it, and umu-run's default
// is already correct for running a game exe).
func umuCommandLine(exePath string, args []string, opt Options) (bin string, launchArgs []string, env []string, err error) {
	cfg := getConfig()

	bin = umuRunPath(cfg)
	if _, statErr := os.Stat(bin); statErr != nil {
		return "", nil, nil, fmt.Errorf("runner: umu-run not found at %s (call EnsureRuntime first): %w", bin, statErr)
	}
	proton := protonPath(cfg)
	if fi, statErr := os.Stat(filepath.Join(proton, "proton")); statErr != nil || fi.IsDir() {
		return "", nil, nil, fmt.Errorf("runner: %s not found at %s (call EnsureRuntime first)", cfg.ProtonVersion, proton)
	}

	prefix := prefixDir(cfg, opt.PrefixKey)
	if _, statErr := os.Stat(prefix); statErr != nil {
		return "", nil, nil, fmt.Errorf("runner: Wine prefix not found at %s (call EnsureRuntime first): %w", prefix, statErr)
	}

	launchArgs = append([]string{exePath}, args...)

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
