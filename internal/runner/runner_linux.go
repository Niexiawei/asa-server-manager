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

	"asa-server/pkg/logger"

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

	// AsaApiLoader.exe creates real Win32 windows, so under Wine it needs an X
	// display even though the workload is a headless game server: without one
	// CreateWindow fails and the loader exits with code 3 having written
	// nothing at all — no console output, not even its own logs/ directory
	// (measured 2026-08-30, see display_linux.go). Fail fast with something
	// actionable instead of reporting a "started" instance that is already
	// dead. Applied after runtimeEnv on purpose — see displayTarget.wrap.
	if opt.NeedsDisplay {
		disp, blocked := resolveDisplay(getConfig())
		if blocked != "" {
			return nil, fmt.Errorf("无法启动 %s：它需要图形显示，但%s。"+
				"AsaApiLoader.exe（ArkApi）在 Wine 下没有显示会静默退出，"+
				"所以这里提前拒绝，而不是让实例假装启动成功",
				filepath.Base(exePath), blocked)
		}
		logger.Infof("runner: %s 需要图形显示，本次使用 %s", filepath.Base(exePath), disp.How)
		bin, launchArgs, env = disp.wrap(bin, launchArgs, env)
	}

	if opt.PTY {
		return runPTY(ctx, bin, launchArgs, env, cred, opt)
	}

	cmd := exec.CommandContext(ctx, bin, launchArgs...)
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

// sharesWinePrefix: every mode except "per-instance" puts all instances in one
// prefix. Tested that way round on purpose — an unconfigured (zero-value)
// Config must still be treated as sharing, since that's the riskier default
// and prefixDir treats anything that isn't "per-instance" as shared too.
func sharesWinePrefix() bool { return getConfig().PrefixMode != "per-instance" }

// umuCommandLine builds the umu-run invocation for exePath/args, matching
// scripts/ark_instance_manager.sh's proven env var set exactly — including
// PROTON_VERB=run, which that script exports on start_server()'s very first
// line (L884), far away from the `env WINEPREFIX=... GAMEID=...` line that
// actually launches the game. See PROTON_VERB's comment below for what
// omitting it cost us.
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
		baseEnv = inheritedEnv()
	}
	env = append(append([]string{}, baseEnv...),
		"WINEPREFIX="+prefix,
		"GAMEID="+cfg.GameID,
		"PROTONPATH="+proton,
		// Regular launches keep the runtime pinned; only the one-time
		// warmPrefix() call in umu_linux.go omits this, on purpose.
		"UMU_RUNTIME_UPDATE=0",
		// PROTON_VERB=run, NOT umu's default "waitforexitandrun".
		//
		// waitforexitandrun runs `wineserver -w` before exec'ing the game —
		// Steam's way of making a relaunch wait for the previous session to
		// die. It assumes one game per prefix. Under prefix_mode "shared" all
		// instances share one prefix, so a second instance parked forever in
		// `wineserver -w` waiting for the first to exit: the game was never
		// exec'd, and the only symptom upstack was waitForGamePID's
		// "游戏进程在 3m0s 内没有出现".
		//
		// The reference script has always set this (start_server(), L884);
		// our earlier claim that it "doesn't set it" came from diffing only
		// the launch command line and missing the export above it.
		// See docs/UMU_PREFIX_PER_INSTANCE_PLAN.md §2-§4.
		"PROTON_VERB=run",
	)
	// Operator escape hatch. The VC++ override set ArkApi needs is already
	// written into the prefix registry at install time (see
	// docs/ARKAPI_LINUX_VCREDIST_PLAN.md §2.4), so this is for one-off
	// troubleshooting rather than normal operation. Appended last so it wins
	// over anything inheritedEnv let through — exec keeps the last occurrence.
	if cfg.WineDLLOverrides != "" {
		env = append(env, "WINEDLLOVERRIDES="+cfg.WineDLLOverrides)
	}
	return bin, launchArgs, env, nil
}

// inheritedEnv is os.Environ() filtered down to the variables a launched game
// process has any business seeing.
//
// It is a whitelist on purpose. The child is normally re-credentialed to a
// dedicated non-root user, while asa-server is often started from a root login
// shell — and such a shell exports a pile of variables naming root-private
// sockets under /run/user/0. pressure-vessel dutifully tries to bind whatever
// they name into the container, so a single leaked variable kills the launch
// before Wine ever starts:
//
//	bwrap: Can't find source path /run/user/0/bus: Permission denied
//
// That one came from DBUS_SESSION_BUS_ADDRESS. A denylist cannot win this
// game — XDG_* was already being stripped (see runtimeEnv) and D-Bus still got
// through, costing an entire evening of "setup says it succeeded but nothing
// works". See docs/UMU_PREFIX_INIT_TROUBLESHOOTING.md.
func inheritedEnv() []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, kv := range src {
		if k, _, ok := strings.Cut(kv, "="); ok && launchEnvAllowed(k) {
			out = append(out, kv)
		}
	}
	return out
}

func launchEnvAllowed(key string) bool {
	switch key {
	// HOME/USER/LOGNAME are rewritten by runtimeEnv when dropping privileges,
	// but must survive when we aren't (umu keeps its runtime cache under HOME).
	case "PATH", "TERM", "TZ", "HOME", "USER", "LOGNAME":
		return true
	case "LANG":
		return true
	}
	switch {
	case strings.HasPrefix(key, "LC_"):
		return true
	// umu-launcher downloads the Steam Linux Runtime with its own HTTP client
	// (urllib3), which honours these and nothing else — config.yaml's
	// download.http_proxy does not reach it.
	case strings.HasSuffix(key, "_PROXY"), strings.HasSuffix(key, "_proxy"):
		return true
	// Deliberate operator tuning of the Wine/Proton/umu stack (UMU_LOG,
	// PROTON_LOG, WINEDEBUG, ...). The ones we set ourselves are appended
	// after this and win, since exec keeps the last occurrence of a key.
	case strings.HasPrefix(key, "UMU_"), strings.HasPrefix(key, "PROTON_"), strings.HasPrefix(key, "WINE"):
		return true
	}
	return false
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
