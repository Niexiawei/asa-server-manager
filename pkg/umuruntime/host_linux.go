//go:build linux

package umuruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
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
	// OnOutcome receives every prefix provisioner's result during Ensure and
	// EnsurePrefix — including an Optional plugin's failure, which does not
	// fail the operation and would otherwise go unseen. It is where the
	// application puts its own words on them. logf is the operation's
	// progress sink. nil = results are dropped.
	OnOutcome func(o Outcome, logf func(string, ...any))
}

// manages reports whether the prefixes are this host's to modify: under
// Runtime "custom" the operator built them, and no provisioner touches them.
func (c Config) manages() bool { return c.Runtime != "custom" }

func (c Config) umuConfig() umu.Config {
	u := c.Umu
	u.HomeDir = c.Identity.HomeDir
	u.ChildIDs = c.Identity.ChildIDs
	u.Credential = c.Identity.credentialOnly()
	u.ChownPath = c.Identity.ChownPath
	u.UserName = c.Identity.UserName
	return u
}

// prefixConfig is the wineprefix.Config for cfg, with the provisioning hooks
// pointing back at this host's plugins (unless the prefixes aren't ours).
func (h *Host) prefixConfig(cfg Config) wineprefix.Config {
	p := cfg.Prefix
	p.ChownPath = cfg.Identity.ChownPath
	p.Provision, p.Pending, p.ProvisionFingerprint = nil, nil, nil
	if cfg.manages() {
		p.Provision = func(ctx context.Context, key, prefix string, logf func(string, ...any)) error {
			return h.provision(ctx, key, prefix, logf, false, nil)
		}
		p.Pending = h.pending
		p.ProvisionFingerprint = h.provisionFingerprint
	}
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
	h.prefixes = wineprefix.New(h.prefixConfig(cfg), h.umu)
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
	h.prefixes.Reconfigure(h.prefixConfig(cfg))
}

func (h *Host) config() Config { return *h.cfg.Load() }

// Umu is the host's umu/GE-Proton runtime.
func (h *Host) Umu() *umu.Runtime { return h.umu }

// Prefixes is the host's Wine prefix manager.
func (h *Host) Prefixes() *wineprefix.Manager { return h.prefixes }

// Graph is the host's validated plugin graph.
func (h *Host) Graph() *Graph { return h.graph }

// Ensure downloads/verifies umu-launcher and the pinned GE-Proton build,
// warms the shared Wine prefix and runs every prefix provisioner on it (in
// dependency order, inside the prefix's write window). Mirrors
// scripts/ark_instance_manager.sh's install_base_server() umu/Proton section,
// the verified reference this sequence is copied from.
//
// A missing piece under Runtime "custom" is a *NotReadyError; a download
// needed with auto download off is ErrAutoDownloadDisabled. A Required
// provisioner's failure fails Ensure; an Optional one's goes to OnOutcome.
func (h *Host) Ensure(ctx context.Context, logf func(string, ...any)) error {
	h.ensureMu.Lock()
	defer h.ensureMu.Unlock()

	cfg := h.config()
	if logf == nil {
		logf = func(string, ...any) {}
	}

	// ensureMu only serializes this process; another asa-server process (a
	// `setup` from a terminal next to the running service) needs the file
	// lock. See runtimeLockFile.
	ctx, unlock, err := h.Lock(ctx, logf)
	if err != nil {
		return err
	}
	defer unlock()

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
	//
	// overlay 模式下先问「有没有事要做」，没有就根本不打开写窗口：打开窗口会先卸载
	// 所有空闲的可写层，而本函数每次 API 启动都跑——以前是「先卸载、再判断」，
	// 挂载跨重启存活的设计因此名存实亡（docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md §3.2）。
	// 其余模式没有可写层可卸，照旧走完下面的预热与补装（例如显示后来可用了，
	// VC++ 插件会借这一趟补装原生运行时）。
	if cfg.Prefix.PrefixMode == "overlay" && !h.prefixes.LowerNeedsWork() {
		logf("共享 Wine 前缀已是最新，无需修改")
		return nil
	}
	doneWrite, err := h.prefixes.PrepareSharedWrite("环境准备 EnsureRuntime")
	if err != nil {
		return err
	}
	defer doneWrite()

	lower := h.prefixes.Dir("")
	if err := h.umu.WarmPrefix(ctx, lower, logf, prefetched.Variant != ""); err != nil {
		return fmt.Errorf("failed to prepare Wine prefix: %w", err)
	}

	// Provisioning needs the prefix initialized first, and still has to be
	// inside the write window opened above. Optional failures (the VC++
	// runtime — most users never enable ArkApi) are reported through
	// OnOutcome and do not fail the environment; see
	// docs/ARKAPI_LINUX_VCREDIST_PLAN.md §3.2.
	return h.provision(ctx, "", lower, logf, false, nil)
}

// Provision explicitly (re)runs the provisioners of caps — all of them when
// caps is empty — on the prefix identified by key. It is the entry point for
// "install it now" requests (`verify-arkapi --install-vcredist`), and unlike
// Ensure/EnsurePrefix it returns every plugin's failure regardless of
// criticality: the caller asked for exactly this. Non-failure outcomes still
// go to OnOutcome.
//
// When key resolves to the shared prefix, the write guard
// (wineprefix.Manager.PrepareSharedWrite) is taken here, first. That guard
// used to be each caller's job, and the fourth caller forgot it (the P0 in
// docs/UMU_PREFIX_PLAN.md) — writing a live overlay lowerdir is undefined
// behaviour that surfaces on the *instances*.
//
// A no-op under Runtime "custom": those prefixes aren't ours to modify.
func (h *Host) Provision(ctx context.Context, key string, logf func(string, ...any), caps ...Capability) error {
	if !h.config().manages() {
		return nil
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	ctx, unlock, err := h.Lock(ctx, logf)
	if err != nil {
		return err
	}
	defer unlock()

	prefix := h.prefixes.Dir(key)
	if prefix == h.prefixes.Dir("") {
		done, err := h.prefixes.PrepareSharedWrite("Provision " + capsString(caps))
		if err != nil {
			return err
		}
		defer done()
	}
	return h.provision(ctx, key, prefix, logf, true, caps)
}

func capsString(caps []Capability) string {
	if len(caps) == 0 {
		return "(all)"
	}
	s := string(caps[0])
	for _, c := range caps[1:] {
		s += "," + string(c)
	}
	return s
}

// provision runs the provisioners of caps (all when caps is empty) on prefix,
// providers first. explicit selects Provision's error policy (every failure
// is returned) over the implicit one (only Required failures are returned;
// the rest go to OnOutcome).
func (h *Host) provision(ctx context.Context, key, prefix string, logf func(string, ...any), explicit bool, caps []Capability) error {
	cfg := h.config()
	var errs []error
	for _, r := range h.graph.Order() {
		pp, ok := r.Plugin.(PrefixProvisioner)
		if !ok || !providesAny(r.Plugin, caps) {
			continue
		}
		o := h.provisionOne(ctx, pp, key, prefix, logf)
		if o.Kind == Failed && (explicit || r.Criticality == Required) {
			errs = append(errs, o.Cause)
			continue
		}
		if cfg.OnOutcome != nil {
			cfg.OnOutcome(o, logf)
		}
	}
	return errors.Join(errs...)
}

func (h *Host) provisionOne(ctx context.Context, pp PrefixProvisioner, key, prefix string, logf func(string, ...any)) Outcome {
	o := Outcome{Plugin: pp.Name(), Key: key}
	for _, n := range pp.Needs() {
		if n.Phase != PhaseProvision || n.Soft {
			continue
		}
		if ok, why := h.available(n.Cap, prefix); !ok {
			o.Kind, o.Cause = Skipped, &CapabilityUnavailableError{Cap: n.Cap, Why: why}
			return o
		}
	}

	res, err := pp.Provision(ctx, &ProvisionContext{
		Key:       key,
		Prefix:    prefix,
		Umu:       h.umu,
		ChownPath: h.config().Identity.ChownPath,
		Logf:      logf,
		Acquire:   h.Acquire,
	})
	if err != nil {
		o.Kind, o.Cause = Failed, err
		return o
	}
	o.Kind, o.Cause, o.Detail = res.Kind, res.Cause, res.Detail
	return o
}

func providesAny(p Plugin, caps []Capability) bool {
	if len(caps) == 0 {
		return true
	}
	for _, have := range p.Provides() {
		for _, want := range caps {
			if have == want {
				return true
			}
		}
	}
	return false
}

// provisionFingerprint is wineprefix's ProvisionFingerprint hook: every
// provisioner's Fingerprint, as "name=fingerprint" parts in name order (so
// registration order doesn't matter), ";"-separated.
func (h *Host) provisionFingerprint(prefix string) string {
	var parts []string
	for _, r := range h.graph.Order() {
		if pp, ok := r.Plugin.(PrefixProvisioner); ok {
			parts = append(parts, pp.Name()+"="+pp.Fingerprint(prefix))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

// pending is wineprefix's Pending hook: does any provisioner have work left
// in prefix?
func (h *Host) pending(prefix string) bool {
	for _, r := range h.graph.Order() {
		if pp, ok := r.Plugin.(PrefixProvisioner); ok && pp.Pending(prefix) {
			return true
		}
	}
	return false
}

// available answers, read-only, whether c could be had for a launch in
// prefix: an EnvProvider that Probes ok, or a PrefixProvisioner Satisfied
// there. detail is what would be used, or the first provider's reason why
// not.
func (h *Host) available(c Capability, prefix string) (bool, string) {
	provs := h.graph.Providers(c)
	if len(provs) == 0 {
		return false, fmt.Sprintf("no plugin provides %s", c)
	}
	var first string
	for i, r := range provs {
		var (
			ok     bool
			detail string
		)
		switch p := r.Plugin.(type) {
		case EnvProvider:
			ok, detail = p.Probe()
		case PrefixProvisioner:
			rd := p.Satisfied(prefix)
			ok, detail = rd.OK, rd.Detail
		default:
			ok = true
		}
		if ok {
			return true, detail
		}
		if i == 0 {
			first = detail
		}
	}
	return false, first
}

// Probe answers, read-only, whether c could be acquired, or is satisfied in
// the shared prefix, and what would be used (or why not). Never starts
// anything.
func (h *Host) Probe(c Capability) (bool, string) { return h.available(c, h.prefixes.Dir("")) }

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

// Status reports every plugin's state, in dependency order, as seen from the
// shared prefix. Read-only. A PrefixInspector is asked about the shared
// prefix; otherwise a Statuser reports; a plugin implementing neither is
// reported as ready.
func (h *Host) Status() []PluginStatus {
	var out []PluginStatus
	for _, r := range h.graph.Order() {
		out = append(out, h.statusOf(r.Plugin, h.prefixes.Dir(""), ""))
	}
	return out
}

// CapabilityStatus reports the providers of c as seen from the prefix
// identified by key, for an executable in exeDir (may be empty). Read-only.
// Empty when nothing provides c.
func (h *Host) CapabilityStatus(c Capability, key, exeDir string) []PluginStatus {
	prefix := h.prefixes.Dir(key)
	var out []PluginStatus
	for _, r := range h.graph.Providers(c) {
		out = append(out, h.statusOf(r.Plugin, prefix, exeDir))
	}
	return out
}

func (h *Host) statusOf(p Plugin, prefix, exeDir string) PluginStatus {
	ps := PluginStatus{
		Name:     p.Name(),
		Provides: p.Provides(),
		Needs:    p.Needs(),
		Status:   Status{Ready: true},
	}
	switch s := p.(type) {
	case PrefixInspector:
		ps.Status = s.InspectPrefix(InspectContext{
			Prefix: prefix,
			ExeDir: exeDir,
			Probe:  func(c Capability) (bool, string) { return h.available(c, prefix) },
		})
	case Statuser:
		ps.Status = s.Report()
	}
	return ps
}

// CheckNeeds reports which of caps are not available for a launch in the
// prefix identified by key. Read-only — a display is probed, never started.
// Readiness.Definitive tells a caller whether to refuse the launch or just
// warn: a missing provider or an unobtainable display is a fact; the VC++
// check is a heuristic.
func (h *Host) CheckNeeds(key string, caps []Capability) []Unmet {
	prefix := h.prefixes.Dir(key)
	var out []Unmet
	seen := map[Capability]bool{}
	for _, c := range caps {
		if seen[c] {
			continue
		}
		seen[c] = true
		provs := h.graph.Providers(c)
		if len(provs) == 0 {
			out = append(out, Unmet{Cap: c, Readiness: Readiness{Definitive: true,
				Detail: fmt.Sprintf("no plugin provides %s", c)}})
			continue
		}
		if ok, _ := h.available(c, prefix); ok {
			continue
		}
		first := provs[0].Plugin
		u := Unmet{Cap: c, Plugin: first.Name(), Readiness: Readiness{Definitive: true}}
		switch p := first.(type) {
		case EnvProvider:
			_, u.Readiness.Detail = p.Probe()
		case PrefixProvisioner:
			u.Readiness = p.Satisfied(prefix)
		}
		out = append(out, u)
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
