// Package runner abstracts "launch a Windows ARK server exe" across
// platforms. On Windows it's a thin wrapper over exec.Command/go-pty — the
// same thing instance/server.go does today. On Linux it wraps umu-run
// (umu-launcher + a pinned GE-Proton build) so the same Windows PE runs
// under Wine, and downloads/warms that runtime on demand.
//
// Run treats every exe identically. In particular it does not special-case
// ArkAscendedServer.exe vs AsaApiLoader.exe (the ArkApi loader): both are
// "a Windows exe that needs to run", and AsaApiLoader.exe is itself just a
// wrapper that re-launches the real server with the same arguments (see
// internal/instance/server.go's arkExe swap). Linux support for ArkApi is a
// user-facing on/off switch identical to Windows's, not a platform
// capability gate — see docs/LINUX_COMPATIBILITY_PLAN.md §1/§5.12.
package runner

import (
	"context"
	"io"
	"os"
	"sync/atomic"

	"github.com/aymanbagabas/go-pty"
)

// Options describes a single exe launch, platform-agnostic.
type Options struct {
	Dir  string   // working directory (host path)
	Env  []string // extra/replacement environment; nil means "inherit os.Environ()"
	PTY  bool     // AsaApiLoader.exe and SteamCMD need a real terminal; ArkAscendedServer.exe doesn't
	PTYW int      // PTY width; 0 uses the default (1920)
	PTYH int      // PTY height; 0 uses the default (1080)
	// PrefixKey selects which Wine prefix to use under Config.PrefixMode
	// "per-instance" (see docs/LINUX_COMPATIBILITY_PLAN.md §6 risk 6).
	// Empty always means the default shared prefix, including under
	// "per-instance" mode. Ignored on Windows.
	PrefixKey string
}

// Handle is a running launch.
//
// On Windows, LauncherPID is the game PID. On Linux it is umu-run's PID —
// umu-run execs into bwrap/wine before the real game process exists, so
// LauncherPID is NOT the game's PID there. It is, however, the process
// group id (Run sets Setsid), which is what Close-by-tree operations need;
// resolving the actual game PID is a separate step (procx.QueryProcess) not
// yet wired up — see docs/LINUX_COMPATIBILITY_PLAN.md §5.3, landing in P4.
type Handle struct {
	LauncherPID int
	Process     *os.Process // set outside PTY mode
	PTY         pty.Pty     // set only when Options.PTY was true
	Wait        func() error
}

// Run launches exePath (a Windows PE) with args. exePath and any path-like
// argument the caller builds must already be in the form the target
// platform expects — callers pass filesystem paths through GamePath first.
func Run(ctx context.Context, exePath string, args []string, opt Options) (*Handle, error) {
	return run(ctx, exePath, args, opt)
}

// GamePath converts a host filesystem path into the form the launched exe
// should be given on the command line. Windows: identity. Linux: Wine's Z:
// drive mapping (/a/b -> Z:\a\b).
func GamePath(hostPath string) string {
	return gamePath(hostPath)
}

// LauncherIsDirect reports whether Handle.LauncherPID from Run is the PID of
// the exe that was actually passed in (true), or an OS-level wrapper's PID
// (false — Linux's umu-run). True on Windows, false on Linux.
//
// This is independent of app-level wrapping: an exe that is itself a
// supervisor spawning a child process (ARK's AsaApiLoader.exe, on either
// platform) still needs its own child resolved separately even when
// LauncherIsDirect is true — that's a fact about the specific exe, which
// this package has no way to know, not about the platform's process model.
// Callers combine both: "do I need to resolve the real game PID instead of
// trusting Handle.LauncherPID?" is `isWrapperExe || !runner.LauncherIsDirect()`.
func LauncherIsDirect() bool {
	return launcherIsDirect()
}

// EnsureRuntime makes sure the launch runtime is ready to use. No-op on
// Windows. On Linux it downloads/verifies umu + the pinned GE-Proton build
// and warms the Wine prefix if they aren't already in place. progress
// receives human-readable status lines — the same shape installer's SSE
// writers already consume (io.Writer, not a structured callback).
func EnsureRuntime(ctx context.Context, progress io.Writer) error {
	return ensureRuntime(ctx, progress)
}

// CheckRuntime reports whether the launch runtime is ready, using only local
// filesystem checks — it never touches the network. Windows always returns
// nil. On Linux it verifies umu-run, the pinned GE-Proton build and the
// shared Wine prefix are all in place (the same three preconditions a real
// launch enforces), so callers — instance start, `service install`,
// `asa-server api` startup — can fail fast with a "run asa-server setup"
// message instead of a deep launch error. The returned error text is written
// for end users and is safe to display verbatim.
func CheckRuntime() error {
	return checkRuntime()
}

// Preflight runs host dependency checks. Always empty on Windows.
func Preflight() []Problem {
	return preflight()
}

// Problem is one failed Preflight check.
type Problem struct {
	Name   string // short id, e.g. "glibc32"
	Detail string // human-readable description of what's missing/wrong
	Fix    string // suggested remediation command, if any ("" when there isn't one)
}

// RuntimeUserInfo summarises the drop-privileges state for the preflight API
// (docs/UMU_RUNTIME_USER_PLAN.md §4.3). On Windows: always {Ready:true}.
type RuntimeUserInfo struct {
	Managed  bool   `json:"managed"`  // Linux && euid==0 && !RunAsRoot
	Bypassed bool   `json:"bypassed"` // Linux && euid==0 && RunAsRoot
	Name     string `json:"name"`
	Ready    bool   `json:"ready"`
}

// EnsureRuntimeUser makes sure the dedicated non-root account the game
// process is dropped to exists, and that the runtime-artifact subtrees it
// writes to are owned by it. No-op on Windows, and on Linux unless
// euid==0 && !RunAsRoot. Called synchronously from package main's startup
// gate — a returned error means asa-server must not continue.
func EnsureRuntimeUser(ctx context.Context) error { return ensureRuntimeUser(ctx) }

// VerifyRuntimeAccess re-checks (read-only, sampled) that the runtime user
// still exists and still has access to the directories it needs. Non-empty
// result at startup => asa-server refuses to start. No-op on Windows.
func VerifyRuntimeAccess() []Problem { return verifyRuntimeAccess(false) }

// VerifyRuntimeAccessForLaunch is VerifyRuntimeAccess with the real-write
// deep probe forced on — used as the per-instance start gate.
func VerifyRuntimeAccessForLaunch() []Problem { return verifyRuntimeAccess(true) }

// RuntimeHomeDir is the HOME the dropped child sees (umu's Steam Linux
// Runtime cache + lsteamclient's ~/.steam/sdk{32,64} live under it). On
// Windows / when not managing a user: this process's own home.
func RuntimeHomeDir() string { return runtimeHomeDir(getConfig()) }

// ChownMirrorForRuntime hands a freshly (re)built per-instance mirror dir to
// the runtime user. No-op unless managing a dropped user.
func ChownMirrorForRuntime(mirrorDir string) error { return chownMirrorForRuntime(mirrorDir) }

// ChownTreeForRuntime chowns an arbitrary path recursively to the runtime
// user (installer fixups use it for ~/.steam). No-op unless managing one.
func ChownTreeForRuntime(root string) error { return chownTreeForRuntime(root) }

// RuntimeUserStatus is the RuntimeUserInfo for the current config/euid.
func RuntimeUserStatus() RuntimeUserInfo { return runtimeUserInfo() }

// RuntimeUserProblems is the drop-privileges self-check result, for the
// preflight API's diagnostics. Empty on Windows / when not managing a user.
// Not part of Preflight() — see preflight_linux.go's runtimeUserProblems.
func RuntimeUserProblems() []Problem { return runtimeUserProblems() }

// RuntimePythonInfo reports which Python interpreter umu-run runs under
// (Linux only — umu-run is a zipapp). On Windows it is always {Resolved:true}
// with empty fields: there is no Python in the launch path.
type RuntimePythonInfo struct {
	Resolved bool   `json:"resolved"`
	Path     string `json:"path"`
	Version  string `json:"version"` // "3.14" etc.
	Source   string `json:"source"`  // "config" | "auto" | ""
}

// RuntimePython resolves (and memoises) the interpreter used to execute the
// umu-launcher zipapp. Empty {Resolved:true} on Windows.
func RuntimePython() RuntimePythonInfo { return runtimePython() }

// Config is the Linux runtime configuration described in
// docs/LINUX_COMPATIBILITY_PLAN.md §7 (the `linux:` config.yaml section).
// Windows builds accept and ignore it via Configure so main.go's
// applyAppConfig doesn't need a build tag of its own.
type Config struct {
	// Runtime selects where the Proton build comes from: "umu" downloads
	// and manages it automatically; "custom" expects the user to have set
	// up PrefixDir/a PROTONPATH themselves and EnsureRuntime becomes a
	// pure preflight check with no downloading.
	Runtime string
	// UmuVersion / ProtonVersion are exact release tags, never "latest" —
	// see docs/LINUX_COMPATIBILITY_PLAN.md §4.3 on why resolving "latest"
	// through api.github.com is something this program specifically avoids.
	UmuVersion    string
	ProtonVersion string
	// PrefixMode: "shared" (default, one Wine prefix for every instance) or
	// "per-instance" (more isolation, more disk).
	PrefixMode string
	// PrefixDir overrides the default {BaseDir}/umu-prefix location.
	PrefixDir string
	// PythonBin selects the interpreter that runs the umu-launcher zipapp
	// (Linux only). Empty = auto-detect a system python3 (python3,
	// python3.10 … python3.N, highest wins). Non-empty = use exactly this
	// one, with no auto-detect fallback: a bare name resolved via PATH
	// ("python3.14"), an absolute path, or a venv/pyenv interpreter path
	// ("/opt/asa-venv/bin/python", "~/.pyenv/versions/3.14.0/bin/python" —
	// "~" is expanded). Must be Python >= 3.10. See
	// docs/UMU_PYTHON_DISCOVERY_PLAN.md.
	PythonBin string
	// AutoDownload false means EnsureRuntime never touches the network —
	// missing runtime pieces are reported as Preflight problems instead.
	AutoDownload bool
	GameID       string
	// RuntimeUser is the dedicated non-root account the game instance's
	// umu/wine process tree is dropped to when asa-server itself runs as
	// root. Empty = "asa-umu-runtime". See docs/UMU_RUNTIME_USER_PLAN.md.
	RuntimeUser string
	// RuntimeUID / RuntimeGID pin the account's numeric ids (0 = let
	// useradd -r pick). Pinning keeps ownership stable when BaseDir is
	// carried to another host — see that doc's §9 risk 2.
	RuntimeUID int
	RuntimeGID int
	// RunAsRoot: true means run the game process as root on purpose and
	// skip the whole drop-privileges path + its startup self-check. The
	// only bypass for "asa-server refuses to start when the runtime user
	// can't be established" (that doc's §2 / §4.3).
	RunAsRoot bool
	// RuntimeDeepProbe: at asa-server startup, additionally fork a dropped
	// child that really writes a probe file (catches SELinux/ACL/mount
	// issues a stat check misses). Always forced on at instance-launch time.
	RuntimeDeepProbe bool
	// BaseDir anchors every relative path this package manages
	// ({BaseDir}/umu-launcher, {BaseDir}/proton, the default prefix dir).
	// runner has no dependency on the config package (would create an
	// import cycle risk with config -> ... -> runner); the caller
	// (main.go's applyAppConfig, which already knows BaseDir) supplies it.
	BaseDir string
}

const (
	defaultUmuVersion    = "1.4.4"
	defaultProtonVersion = "GE-Proton10-34"
	defaultGameID        = "umu-default"
)

func defaultConfig() Config {
	return Config{
		Runtime:       "umu",
		UmuVersion:    defaultUmuVersion,
		ProtonVersion: defaultProtonVersion,
		PrefixMode:    "shared",
		AutoDownload:  true,
		GameID:        defaultGameID,
	}
}

var current atomic.Pointer[Config]

func init() {
	def := defaultConfig()
	current.Store(&def)
}

// Configure sets the active Linux runtime configuration. Safe to call from
// both platforms' startup path — see the Config doc comment.
func Configure(cfg Config) {
	current.Store(&cfg)
}

// getConfig returns the active config, filling in any zero-valued fields
// from defaultConfig() so a caller that only sets BaseDir doesn't have to
// also repeat every default.
func getConfig() Config {
	cfg := *current.Load()
	def := defaultConfig()
	if cfg.Runtime == "" {
		cfg.Runtime = def.Runtime
	}
	if cfg.UmuVersion == "" {
		cfg.UmuVersion = def.UmuVersion
	}
	if cfg.ProtonVersion == "" {
		cfg.ProtonVersion = def.ProtonVersion
	}
	if cfg.PrefixMode == "" {
		cfg.PrefixMode = def.PrefixMode
	}
	if cfg.GameID == "" {
		cfg.GameID = def.GameID
	}
	return cfg
}

// ptySize applies the same 1920x1080 default the current Windows code
// hardcodes at internal/instance/server.go's pp.Resize(1920, 1080) call.
func ptySize(opt Options) (width, height int) {
	width, height = opt.PTYW, opt.PTYH
	if width <= 0 || height <= 0 {
		return 1920, 1080
	}
	return width, height
}
