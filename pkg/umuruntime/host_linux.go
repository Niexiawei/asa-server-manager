//go:build linux

package umuruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"syscall"

	"asa-server/pkg/problem"
	"asa-server/pkg/umu"
	"asa-server/pkg/wineprefix"
)

// NotReadyError is asa-server/pkg/umu's "a runtime piece is missing" error,
// re-exported so callers of this package need not import pkg/umu to match it.
type NotReadyError = umu.NotReadyError

// Identity is how the host reaches the (possibly dropped-privileges) account
// that Wine processes run as. One value, handed to every mechanism that needs
// it — the same five callbacks used to be written out separately for
// pkg/umu and pkg/xvfb. Each is called fresh on every use: the account may
// not exist yet when the Host is configured. Any of them may be nil, which
// means "no separate identity" (run as this process).
type Identity struct {
	HomeDir  func() string
	ChildIDs func() (uid, gid uint32, managed bool)
	// Credential resolves (creating the account if needed) the credential
	// to run under, and that account's home. nil credential = no drop.
	Credential func() (cred *syscall.Credential, home string, err error)
	ChownPath  func(path string) error
	UserName   func() string
}

func (id Identity) credentialOnly() func() (*syscall.Credential, error) {
	if id.Credential == nil {
		return nil
	}
	return func() (*syscall.Credential, error) {
		cred, _, err := id.Credential()
		return cred, err
	}
}

func (id Identity) userName() string {
	if id.UserName == nil {
		return ""
	}
	return id.UserName()
}

// Config configures a Host.
type Config struct {
	// Runtime: "umu" downloads and manages umu-launcher/GE-Proton; "custom"
	// means the operator provides them and Ensure only verifies.
	Runtime string
	// WineDLLOverrides is appended verbatim to every launch's
	// WINEDLLOVERRIDES. An operator escape hatch; empty sets nothing.
	WineDLLOverrides string

	// Umu and Prefix configure the two mechanisms. Their identity-related
	// fields (umu.Config's HomeDir/ChildIDs/Credential/ChownPath/UserName,
	// wineprefix.Config's ChownPath) are **overwritten** from Identity —
	// set them there, not here.
	Umu    umu.Config
	Prefix wineprefix.Config

	Identity Identity

	// BeforeEnsure runs first in Ensure (creating the dropped-privileges
	// account, which warming the prefix below runs as). Its error is
	// returned as is.
	BeforeEnsure func(ctx context.Context) error
	// AfterWarm runs after Ensure has warmed the shared prefix, still inside
	// the shared-prefix write window. Its failures are the hook's own to
	// report: Ensure does not fail on them.
	//
	// Transitional: the VC++ runtime is wired through it until it becomes a
	// PrefixProvisioner plugin (docs/UMU_RUNTIME_PLUGIN_PLAN.md phase 3).
	AfterWarm func(ctx context.Context, logf func(string, ...any))
}

func (c Config) umuConfig() umu.Config {
	u := c.Umu
	u.HomeDir = c.Identity.HomeDir
	u.ChildIDs = c.Identity.ChildIDs
	u.Credential = c.Identity.credentialOnly()
	u.ChownPath = c.Identity.ChownPath
	u.UserName = c.Identity.UserName
	return u
}

func (c Config) prefixConfig() wineprefix.Config {
	p := c.Prefix
	p.ChownPath = c.Identity.ChownPath
	return p
}

// Host runs Windows executables under umu/GE-Proton and hosts the plugins
// they may need.
//
// There should be one per process: it owns the process's *umu.Runtime and
// *wineprefix.Manager (per-prefix locks, creation slots) and, through its
// plugins, things like the managed Xvfb. Configuration changes go through
// Reconfigure, never a second New.
type Host struct {
	cfg      atomic.Pointer[Config]
	graph    *Graph
	umu      *umu.Runtime
	prefixes *wineprefix.Manager

	// ensureMu serializes Ensure: two instances starting at once on a fresh
	// install would otherwise race on the same downloads and prefix warm-up.
	ensureMu sync.Mutex
}

// New validates the plugin graph (see Resolve) and builds a Host. It starts
// nothing.
func New(cfg Config, plugins ...Registered) (*Host, error) {
	g, err := Resolve(plugins)
	if err != nil {
		return nil, err
	}
	h := &Host{graph: g}
	h.cfg.Store(&cfg)
	h.umu = umu.New(cfg.umuConfig())
	h.prefixes = wineprefix.New(cfg.prefixConfig(), h.umu)
	return h, nil
}

// MustNew is New for a composition root whose plugin set is fixed at compile
// time, where a graph error is a programming error.
func MustNew(cfg Config, plugins ...Registered) *Host {
	h, err := New(cfg, plugins...)
	if err != nil {
		panic(err)
	}
	return h
}

// Reconfigure replaces the live Config and pushes it down to the mechanisms.
// Cheap (atomic stores), so callers may refresh before every use. Plugins
// are configured by whoever constructed them.
func (h *Host) Reconfigure(cfg Config) {
	h.cfg.Store(&cfg)
	h.umu.Reconfigure(cfg.umuConfig())
	h.prefixes.Reconfigure(cfg.prefixConfig())
}

func (h *Host) config() Config { return *h.cfg.Load() }

// Umu is the host's umu/GE-Proton runtime.
func (h *Host) Umu() *umu.Runtime { return h.umu }

// Prefixes is the host's Wine prefix manager.
func (h *Host) Prefixes() *wineprefix.Manager { return h.prefixes }

// Graph is the host's validated plugin graph.
func (h *Host) Graph() *Graph { return h.graph }

// Ensure downloads/verifies umu-launcher and the pinned GE-Proton build and
// warms the shared Wine prefix, then runs AfterWarm. Mirrors
// scripts/ark_instance_manager.sh's install_base_server() umu/Proton section,
// the verified reference this sequence is copied from.
//
// A missing piece under Runtime "custom" is a *NotReadyError; a download
// needed with auto download off is ErrAutoDownloadDisabled.
func (h *Host) Ensure(ctx context.Context, logf func(string, ...any)) error {
	h.ensureMu.Lock()
	defer h.ensureMu.Unlock()

	cfg := h.config()
	if logf == nil {
		logf = func(string, ...any) {}
	}

	// The dropped-privileges account comes first: WarmPrefix below runs
	// wineboot as that user, and it must be able to write the prefix and
	// its own HOME. See docs/UMU_RUNTIME_USER_PLAN.md §3.2.
	if cfg.BeforeEnsure != nil {
		if err := cfg.BeforeEnsure(ctx); err != nil {
			return err
		}
	}

	if cfg.Runtime == "custom" {
		logf("linux runtime mode is \"custom\": skipping umu/GE-Proton download, verifying the pre-configured runtime instead")
		if err := h.umu.CheckRuntime(); err != nil {
			return err
		}
		return h.prefixes.CheckSharedReady()
	}
	if !cfg.Umu.AutoDownload {
		return ErrAutoDownloadDisabled
	}

	if err := h.umu.EnsureUmu(ctx, logf); err != nil {
		return fmt.Errorf("failed to install umu-launcher: %w", err)
	}
	if err := h.umu.EnsureGEProton(ctx, logf); err != nil {
		return fmt.Errorf("failed to install %s: %w", cfg.Umu.ProtonVersion, err)
	}

	// WarmPrefix below runs `umu-run wineboot --init`, which would let umu go
	// fetch the Steam Linux Runtime itself via its own unthrottled client.
	// Prefetch it first via pkg/download so wineboot only has to resume the
	// last sliver. Failure only degrades — the reference behaviour (umu
	// downloads it) — and must never block setup. See
	// docs/STEAMRT_PREFETCH_PLAN.md.
	prefetched, err := h.umu.PrefetchSteamRuntime(ctx, logf)
	if err != nil {
		logf("Steam Linux Runtime 预下载失败（%v），改由 umu 自行下载", err)
	}

	// 从这里往下才开始动共享前缀本身。overlay 模式下它可能正被若干实例的可写层
	// 当 lowerdir 引用着 —— 这时改它是未定义行为。
	//
	// 判据是「还有没有事要做」而不是「有没有挂载」：本函数在每次 API 启动时都会
	// 后台跑一遍，而挂载是**故意**跨重启存活的（停实例不卸载），一见挂载就报错
	// 等于第一个实例起过之后永远起不来。见 wineprefix.Manager.LowerNeedsWork。
	doneWrite, err := h.prefixes.PrepareSharedWrite("环境准备 EnsureRuntime")
	if err != nil {
		if h.prefixes.LowerNeedsWork() {
			return err
		}
		logf("共享 Wine 前缀已是最新，跳过重建（当前有实例的可写层挂在它上面）")
		return nil
	}
	defer doneWrite()

	if err := h.umu.WarmPrefix(ctx, h.prefixes.Dir(""), logf, prefetched.Variant != ""); err != nil {
		return fmt.Errorf("failed to prepare Wine prefix: %w", err)
	}

	if cfg.AfterWarm != nil {
		cfg.AfterWarm(ctx, logf)
	}
	return nil
}

// Check reports, with local filesystem checks only, whether umu-run, the
// pinned GE-Proton build and the shared Wine prefix are all in place — the
// preconditions every launch enforces. A failure is a *NotReadyError.
func (h *Host) Check() error {
	if err := h.umu.CheckRuntime(); err != nil {
		return err
	}
	return h.prefixes.CheckSharedReady()
}

// LaunchSpec describes one launch for Command.
type LaunchSpec struct {
	// PrefixKey selects the Wine prefix (see wineprefix.Manager.Dir); "" is
	// the shared one.
	PrefixKey string
	// Env replaces the inherited environment; nil means umu.InheritedEnv().
	Env []string
	// Needs lists the capabilities the executable needs at launch. Those
	// provided by an EnvProvider are acquired and applied to the
	// environment; a capability no plugin provides fails the launch.
	Needs []Capability
}

// AcquiredLease is one capability acquired for a Command.
type AcquiredLease struct {
	Cap    Capability
	Plugin string
	Lease  Lease
}

// Command is a fully built launch: run Path with Args and Env, as Credential.
// Executing it (plain or on a PTY) is the caller's business.
type Command struct {
	Path string
	Args []string
	Env  []string
	// Credential is non-nil when the launch must drop privileges.
	Credential *syscall.Credential
	// Leases are the capabilities acquired for this launch, in Needs order.
	Leases []AcquiredLease
}

// Command builds the umu-run invocation for exe/args — argv
// `<python> <umu-run> <exe> <args...>` — and its environment, matching
// scripts/ark_instance_manager.sh's proven variable set exactly.
//
// The environment is layered in this order, and the order is load-bearing:
//
//	base (LaunchSpec.Env or umu.InheritedEnv)
//	→ WINEPREFIX / GAMEID / PROTONPATH / UMU_RUNTIME_UPDATE=0 / PROTON_VERB=run / PROTON_USE_XALIA=0
//	→ WINEDLLOVERRIDES (Config.WineDLLOverrides, when set)
//	→ the dropped user's HOME/USER/LOGNAME rewrite (only when dropping)
//	→ each lease's Apply, in Needs order
//
// exec keeps the last occurrence of a key, so ours win over inherited
// values; and leases go after the user rewrite so a future filter there
// cannot eat DISPLAY (see the display plugin's Target.Apply).
func (h *Host) Command(ctx context.Context, exe string, args []string, spec LaunchSpec) (*Command, error) {
	if err := h.Check(); err != nil {
		return nil, err
	}
	cfg := h.config()

	// Run the umu-launcher zipapp under an explicitly resolved interpreter
	// rather than its "#!/usr/bin/env python3" shebang — the system default
	// may be older than the 3.10 umu needs. See docs/UMU_PYTHON_DISCOVERY_PLAN.md.
	py, err := h.umu.Interpreter()
	if err != nil {
		return nil, err
	}

	// Check validated the shared prefix; a per-instance/overlay launch uses
	// a distinct directory that still has to exist.
	prefix := h.prefixes.Dir(spec.PrefixKey)
	if _, statErr := os.Stat(prefix); statErr != nil {
		return nil, fmt.Errorf("umuruntime: Wine prefix not found at %s (call EnsurePrefix first): %w", prefix, statErr)
	}

	base := spec.Env
	if base == nil {
		base = umu.InheritedEnv()
	}
	env := append(append([]string{}, base...),
		"WINEPREFIX="+prefix,
		"GAMEID="+cfg.Umu.GameID,
		"PROTONPATH="+h.umu.ProtonPath(),
		// Regular launches keep the runtime pinned; only WarmPrefix's
		// one-time wineboot omits this, on purpose.
		"UMU_RUNTIME_UPDATE=0",
		// PROTON_VERB=run, NOT umu's default "waitforexitandrun".
		//
		// waitforexitandrun runs `wineserver -w` before exec'ing the game —
		// Steam's way of making a relaunch wait for the previous session to
		// die. It assumes one game per prefix. With a shared prefix a second
		// instance parked forever in `wineserver -w` waiting for the first to
		// exit: the game was never exec'd, and the only symptom upstack was
		// "游戏进程在 3m0s 内没有出现".
		//
		// The reference script has always set this (start_server(), L884);
		// an earlier claim that it "doesn't set it" came from diffing only
		// the launch command line and missing the export above it.
		// See docs/UMU_PREFIX_PLAN.md Part 1 §2-§4.
		"PROTON_VERB=run",
		// No accessibility overlay on a headless server — see umu.ProtonNoXalia.
		umu.ProtonNoXalia,
	)
	// Operator escape hatch, appended last so it wins over anything
	// umu.InheritedEnv let through.
	if cfg.WineDLLOverrides != "" {
		env = append(env, "WINEDLLOVERRIDES="+cfg.WineDLLOverrides)
	}

	// Drop the umu-run child (and everything bwrap/wine spawns below it) to
	// the dedicated account when there is one. See docs/UMU_RUNTIME_USER_PLAN.md.
	var cred *syscall.Credential
	if cfg.Identity.Credential != nil {
		var home string
		if cred, home, err = cfg.Identity.Credential(); err != nil {
			return nil, err
		}
		if cred != nil {
			env = umu.RuntimeEnv(env, home, cfg.Identity.userName())
		}
	}

	cmd := &Command{
		Path:       py.Path,
		Args:       append([]string{h.umu.RunPath(), exe}, args...),
		Credential: cred,
	}
	seen := map[Capability]bool{}
	for _, c := range spec.Needs {
		if seen[c] {
			continue
		}
		seen[c] = true
		if !h.envProvided(c) {
			if len(h.graph.Providers(c)) == 0 {
				releaseAll(cmd.Leases)
				return nil, &CapabilityUnavailableError{Cap: c}
			}
			// Provided, but not by acquiring anything at launch (a prefix
			// provisioner): nothing to add to the environment.
			continue
		}
		lease, plugin, err := h.acquire(ctx, c)
		if err != nil {
			releaseAll(cmd.Leases)
			return nil, err
		}
		env = lease.Apply(env)
		cmd.Leases = append(cmd.Leases, AcquiredLease{Cap: c, Plugin: plugin, Lease: lease})
	}
	cmd.Env = env
	return cmd, nil
}

func releaseAll(leases []AcquiredLease) {
	for _, l := range leases {
		l.Lease.Release()
	}
}

func (h *Host) envProvided(c Capability) bool {
	for _, r := range h.graph.Providers(c) {
		if _, ok := r.Plugin.(EnvProvider); ok {
			return true
		}
	}
	return false
}

// Acquire gets a lease on c from the first provider that can give one.
//
// Providers are tried in registration order. One reporting
// ErrCapabilityUnavailable is skipped; if all do, the first one's
// *CapabilityUnavailableError is returned. A provider failing any other way
// stops the search with an *AcquireError: it should have worked, and silently
// trying something else would hide why it didn't. (A provider that falls
// back internally — the display plugin's candidate chain — does so before
// returning.)
func (h *Host) Acquire(ctx context.Context, c Capability) (Lease, error) {
	lease, _, err := h.acquire(ctx, c)
	return lease, err
}

func (h *Host) acquire(ctx context.Context, c Capability) (Lease, string, error) {
	var unavailable *CapabilityUnavailableError
	for _, r := range h.graph.Providers(c) {
		ep, ok := r.Plugin.(EnvProvider)
		if !ok {
			continue
		}
		name := r.Plugin.Name()
		lease, err := ep.Acquire(ctx)
		if err == nil {
			return lease, name, nil
		}
		if errors.Is(err, ErrCapabilityUnavailable) {
			if unavailable == nil && !errors.As(err, &unavailable) {
				unavailable = &CapabilityUnavailableError{Cap: c, Why: err.Error()}
			}
			continue
		}
		return nil, name, &AcquireError{Cap: c, Plugin: name, Err: err}
	}
	if unavailable != nil {
		return nil, "", unavailable
	}
	return nil, "", &CapabilityUnavailableError{Cap: c}
}

// Preflight collects every plugin's self-check problems, in dependency
// order. Read-only.
func (h *Host) Preflight() []problem.Problem {
	var out []problem.Problem
	for _, r := range h.graph.Order() {
		if p, ok := r.Plugin.(Preflighter); ok {
			out = append(out, p.Preflight()...)
		}
	}
	return out
}

// Status reports every plugin's state, in dependency order. Read-only. A
// plugin that doesn't implement Statuser is reported as ready.
func (h *Host) Status() []PluginStatus {
	var out []PluginStatus
	for _, r := range h.graph.Order() {
		ps := PluginStatus{
			Name:     r.Plugin.Name(),
			Provides: r.Plugin.Provides(),
			Needs:    r.Plugin.Needs(),
			Status:   Status{Ready: true},
		}
		if s, ok := r.Plugin.(Statuser); ok {
			ps.Status = s.Report()
		}
		out = append(out, ps)
	}
	return out
}

// Close closes every plugin that holds process-lifetime resources, consumers
// before providers. Idempotent as long as the plugins' Close are.
func (h *Host) Close() {
	order := h.graph.Order()
	for i := len(order) - 1; i >= 0; i-- {
		if c, ok := order[i].Plugin.(Closer); ok {
			c.Close()
		}
	}
}
