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
	// Log receives the launched process's stdout and stderr. nil discards
	// both, which was the only behaviour before — and on Linux that meant
	// every diagnostic umu-run, pressure-vessel and Wine produce went to
	// /dev/null, so a launch that died inside the container left nothing to
	// read (docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md §3.5 c).
	// scripts/ark_instance_manager.sh redirects both to a file for exactly
	// this reason. Ignored when PTY is set — there the PTY *is* the stream,
	// and the caller already owns it through Handle.PTY.
	Log io.Writer
	// PrefixKey selects which Wine prefix to use under Config.PrefixMode
	// "per-instance" (see docs/LINUX_COMPATIBILITY_PLAN.md §6 risk 6).
	// Empty always means the default shared prefix, including under
	// "per-instance" mode. Ignored on Windows.
	PrefixKey string
	// NeedsDisplay marks an exe that creates Win32 windows and therefore
	// cannot run under Wine without an X display, however headless the
	// workload looks. Set it for AsaApiLoader.exe (ArkApi) and nothing else:
	// ArkAscendedServer.exe itself boots fine with no display, and wrapping
	// every launch in xvfb-run would add a process and a failure mode for no
	// gain. Ignored on Windows, which always has a window station.
	//
	// On Linux the launch either inherits the host's DISPLAY or gets its own
	// xvfb-run virtual display; when neither exists Run fails fast with an
	// actionable error rather than letting the loader die silently.
	// See internal/runner/display_linux.go.
	NeedsDisplay bool
}

// Handle is a running launch.
//
// On Windows, LauncherPID is the game PID. On Linux it is umu-run's PID —
// umu-run execs into bwrap/wine before the real game process exists, so
// LauncherPID is NOT the game's PID there, and resolving the real one is a
// separate step (see internal/instance's waitForGamePID).
//
// LauncherPID is also NOT a usable process-group handle on Linux, despite Run
// setting Setsid: pressure-vessel starts a new session on the way into the
// container, and so does Wine for the game itself, so the launcher's process
// group ends up containing nothing but the launcher. Anything that wants the
// whole launch has to go through the parent/child tree instead — which is
// what procx.KillTree/TerminateTree do. See
// docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md §2.
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

// SharesWinePrefix reports whether instances all land in the same Wine prefix,
// and therefore in the same wineserver — one Wine session shared by every
// running instance. Always false on Windows (no prefix exists) and under
// prefix_mode "per-instance"; true under "shared", the default.
//
// Two consequences follow from it, both enforced in internal/instance:
//
//  1. Launches must be serialized. Proton's launch path assumes a prefix is
//     touched by one launch at a time — see launchgate.go. The gate lives
//     there rather than here because it is held until the instance reaches
//     start_initialization_successful, which runner.Run returns long before.
//
//  2. At most one ArkApi instance can run. AsaApiLoader.exe needs an X display
//     and Wine initializes its display subsystem once per session, so a second
//     loader joining an existing session hangs before it ever execs — measured
//     2026-08-31, see docs/UMU_PREFIX_PER_INSTANCE_PLAN.md §2.2.
func SharesWinePrefix() bool { return sharesWinePrefix() }

// EnsurePrefixVCRedist makes sure the Microsoft VC++ runtime that ArkApi's
// AsaApiLoader.exe depends on is installed into a Wine prefix — Wine and
// GE-Proton ship only their own implementations of those DLLs. No-op on
// Windows, where the runtime is a system-level component.
//
// prefixKey has the same meaning as Options.PrefixKey: empty selects the
// default shared prefix. progress receives human-readable status lines, the
// same shape EnsureRuntime uses. Idempotent — a prefix that already has it
// costs a couple of local file reads.
//
// See docs/ARKAPI_LINUX_VCREDIST_PLAN.md.
func EnsurePrefixVCRedist(ctx context.Context, prefixKey string, progress io.Writer) error {
	return ensurePrefixVCRedist(ctx, prefixKey, progress)
}

// PrefixHasVCRedist reports whether a Wine prefix already carries the native
// Microsoft VC++ runtime. Read-only and offline, so callers on a hot path
// (instance start) can use it for diagnostics. Always true on Windows.
//
// Deliberately NOT a launch gate: the judgement is a heuristic over registry
// text and a PE header marker, and blocking a start on a possibly-wrong check
// is exactly what docs/LINUX_COMPATIBILITY_PLAN.md §1 goal 5 rules out — the
// program doesn't get to decide ArkApi is unusable on the user's behalf.
func PrefixHasVCRedist(prefixKey string) bool {
	return prefixHasVCRedist(prefixKey)
}

// DLLOrigin says where a DLL in a Wine prefix came from.
type DLLOrigin string

const (
	DLLMissing DLLOrigin = "missing"
	DLLWine    DLLOrigin = "wine"   // Wine 自己的占位/内建 PE
	DLLNative  DLLOrigin = "native" // 微软原生
)

// VCRedistDLLInfo is one runtime DLL's origin, in the prefix and next to the game.
//
// Both columns matter: Windows resolves a DLL from the **application directory
// first**, and ARK ships native copies of most of the VC++ runtime right next
// to ArkAscendedServer.exe — so what Wine ends up loading is decided by the
// DllOverrides setting, not only by what is in system32.
type VCRedistDLLInfo struct {
	Name       string
	InSystem32 DLLOrigin
	InGameDir  DLLOrigin // empty when no game dir was supplied
}

// VCRedistInfo is the read-only view of a prefix's VC++ runtime state, for
// `asa-server verify-arkapi`.
type VCRedistInfo struct {
	Managed  bool   // Linux && runtime == "umu"
	Prefix   string
	ProbeDLL string
	// Installed is the single judgement "the native runtime is in system32",
	// decided by ProbeDLL's PE header. RegistryVersion is NOT part of it —
	// GE-Proton pre-fakes the standard detection key in a brand-new prefix
	// (see internal/runner/vcredist.go), so it is diagnostic text only.
	Installed       bool
	RegistryVersion string
	// OverridesSet/WantOverrides describe the DllOverrides entries in the
	// prefix — the load-bearing half of this whole thing. ARK ships native
	// copies of most of the runtime next to its exe, and the override is what
	// makes Wine prefer them over its own builtins.
	OverridesSet  int
	WantOverrides int
	// InstallerDisplay / InstallerBlocked: Microsoft's redist installer refuses
	// to run under Wine without a reachable X display (exit 203), even with
	// /quiet. On a headless host — this project's main deployment shape —
	// Installed stays false by design and only the overrides apply.
	InstallerDisplay string
	InstallerBlocked string
	DLLs             []VCRedistDLLInfo
}

// VCRedistStatus summarises a prefix's VC++ runtime state. Read-only, offline.
// gameDir is the directory holding the game exe (empty skips that column).
// On Windows: {Managed: false} with everything else zero.
func VCRedistStatus(prefixKey, gameDir string) VCRedistInfo {
	return vcRedistStatus(prefixKey, gameDir)
}

// DisplayInfo is how (and whether) this host can give a Wine process an X
// display — the precondition Options.NeedsDisplay depends on. On Windows it is
// always available: there is a real window station.
type DisplayInfo struct {
	Available bool   `json:"available"`
	How       string `json:"how"`     // "宿主的 X 显示 :0" / "xvfb-run（虚拟显示）"
	Blocked   string `json:"blocked"` // why not, when Available is false
}

// DisplayStatus reports the host's display situation. Read-only and offline —
// one getenv, one stat and a PATH lookup.
func DisplayStatus() DisplayInfo { return displayStatus() }

// Preflight runs host dependency checks. Always empty on Windows.
func Preflight() []Problem {
	return preflight()
}

// Problem is one failed Preflight check.
type Problem struct {
	Name   string // short id, e.g. "glibc32"
	Detail string // human-readable description of what's missing/wrong
	Fix    string // suggested remediation command, if any ("" when there isn't one)
	// Warning marks an advisory rather than a blocker: the thing still works,
	// just in a degraded or less convenient form. Consumers must treat the two
	// differently — `asa-server setup` refuses to continue on a blocker but not
	// on an advisory, and the preflight API reports healthy when only
	// advisories are present.
	//
	// Without this distinction every check is a hard stop, which is how
	// "the acl package isn't installed" once became a reason `setup` would not
	// run at all — see docs/ACL_PERMISSION_HARDENING_PLAN.md §1.
	Warning bool
}

// Blockers returns the subset of problems that must stop whatever is being
// attempted; Advisories returns the rest.
func Blockers(problems []Problem) []Problem { return filterProblems(problems, false) }

// Advisories returns the subset of problems that are merely recommendations.
func Advisories(problems []Problem) []Problem { return filterProblems(problems, true) }

func filterProblems(problems []Problem, warning bool) []Problem {
	var out []Problem
	for _, p := range problems {
		if p.Warning == warning {
			out = append(out, p)
		}
	}
	return out
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

// SharedAccessInfo is the diagnostic view of the shared-write regime, for
// `asa-server perms status`. On Windows (and whenever privileges aren't being
// dropped) Managed is false and everything else is empty: the game runs as the
// same identity as asa-server, so there is nothing to share.
type SharedAccessInfo struct {
	Managed bool   // Linux && euid==0 && !RunAsRoot
	User    string // runtime user name
	UID     int
	GID     int
	Group   string // primary group name, the one the ACL entries name
	// ACLTool is the resolved setfacl path, "" when POSIX ACLs are unusable.
	// ACLError says why in that case.
	ACLTool  string
	ACLError string
	Trees    []TreeAccessInfo
}

// TreeAccessInfo is one shared tree's current state.
type TreeAccessInfo struct {
	Path   string
	Exists bool
	// Prepared is the sampled ownership/mode check (group, g+rw, setgid on
	// dirs). DefaultACL is whether the tree root carries the inheritable
	// entry — the part that makes files created later by root writable, and
	// the part Prepared deliberately cannot see (see sharedAccessNeeded).
	Prepared   bool
	DefaultACL bool
}

// Model names the regime in force: "acl" (group + setgid + default ACL),
// "chown" (the degraded fallback), or "n/a" when privileges aren't dropped.
func (i SharedAccessInfo) Model() string {
	switch {
	case !i.Managed:
		return "n/a"
	case i.ACLTool != "":
		return "acl"
	default:
		return "chown"
	}
}

// SharedAccessStatus reports the current shared-write state without changing
// anything. Read-only.
func SharedAccessStatus() SharedAccessInfo { return sharedAccessStatus() }

// SharedTrees lists the directory trees that both this process and the dropped
// runtime user must be able to write. Empty on Windows / when not dropping.
func SharedTrees() []string { return sharedTrees() }

// PrepareSharedTree makes a directory tree writable by BOTH this process
// (root) and the dropped runtime user, without transferring ownership: group +
// setgid + a POSIX default ACL, so files created later by *either* side stay
// writable by the other. Used for server-files and the instances directory,
// which SteamCMD, admin uploads and the game process all write to.
//
// No-op on Windows and whenever privileges aren't being dropped. Idempotent,
// and degrades to a plain recursive chown when the filesystem has no ACL
// support. See internal/runner/sharedaccess_linux.go.
func PrepareSharedTree(root string) error { return prepareSharedTree(root) }

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
	// SteamRTPrefetch: before umu initializes, download the Steam Linux
	// Runtime archive with pkg/download and drop it into umu's own download
	// cache, so umu's internal (retry-less, proxy-deaf) urllib3 fetch of
	// 150~190 MB turns into a 1 MiB resume. Config default is true; false
	// restores the plain "let umu download it" behaviour for troubleshooting.
	// Failing to prefetch is never fatal. See docs/STEAMRT_PREFETCH_PLAN.md.
	SteamRTPrefetch bool
	// InstallVCRedist: 在 Wine prefix 里装微软 VC++ 运行时。ArkApi 的
	// AsaApiLoader.exe 依赖它，而 Wine/GE-Proton 的 prefix 里只有 Wine 自己的同名
	// 实现。配置默认 true；false 完全不下载不安装，启用 ArkApi 的实例启动时只多一条
	// 告警（不阻断）。见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md。
	InstallVCRedist bool
	// VCRedistURL 留空 = 微软官方短链。最终下载地址的路径里自带文件 sha256，会被
	// 自动提取用于校验；自建镜像若没有那一段，用 VCRedistSHA256 显式指定。
	VCRedistURL    string
	VCRedistSHA256 string
	// WineDLLOverrides 原样追加到游戏进程的 WINEDLLOVERRIDES。留空 = 不设。
	// VC++ 那 11 个 DLL 的 override 已在安装时写进 prefix 注册表，不必在这里重复；
	// 这一项是排障用的逃生舱。
	WineDLLOverrides string
	GameID           string
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
		Runtime:         "umu",
		UmuVersion:      defaultUmuVersion,
		ProtonVersion:   defaultProtonVersion,
		PrefixMode:      "shared",
		AutoDownload:    true,
		SteamRTPrefetch: true,
		InstallVCRedist: true,
		GameID:          defaultGameID,
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
