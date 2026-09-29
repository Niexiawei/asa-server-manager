//go:build linux

// Package vcrt is the umuruntime plugin that provides win32.msvcrt: the
// Microsoft VC++ 2015-2022 runtime inside a Wine prefix. The mechanism —
// downloading the installer, writing the DLL overrides, running the
// installer, judging the result — is asa-server/pkg/vcredist; this package
// only decides when it runs, on which prefix, and how its results map onto
// the plugin contract. See docs/UMU_RUNTIME_PLUGIN_PLAN.md §4.4 and
// docs/ARKAPI_LINUX_VCREDIST_PLAN.md.
//
// It needs win32.gui **softly**: the DLL overrides (the load-bearing half)
// need no display, only Microsoft's installer does. Without one the plugin
// still writes the overrides and reports the installer as skipped
// (umuruntime.Degraded), which is the normal path on a headless host.
//
// Like pkg/vcredist, it never names the application: every "what to tell
// the user" decision is returned as a type (vcredist.Result.Skip,
// *vcredist.AutoDownloadDisabledError, the OnUnverifiedDownload hook).
package vcrt

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"asa-server/pkg/umuruntime"
	"asa-server/pkg/vcredist"
)

// Name is this plugin's name in a umuruntime.Host.
const Name = "vcrt"

// Config configures the plugin.
type Config struct {
	// Managed: the prefixes belong to the host (a "custom" runtime's
	// prefixes are the operator's own). Unmanaged means inspect only.
	Managed bool
	// Install: install the runtime at all (linux.install_vcredist). false
	// means inspect only — Satisfied and InspectPrefix still read the disk.
	Install bool

	// Dir, URL, SHA256 and AutoDownload are pkg/vcredist's: where the
	// installer is kept, where it comes from, how it is verified, and
	// whether it may be downloaded.
	Dir          string
	URL          string
	SHA256       string
	AutoDownload bool

	// OnUnverifiedDownload is told, before the download starts, that no
	// checksum is available for url. logf is the running operation's.
	OnUnverifiedDownload func(url string, logf func(string, ...any))
}

// Plugin provides umuruntime.CapMSVCRT. One per host; configuration changes
// go through Reconfigure. It holds no other state (pkg/vcredist's Installer
// is built fresh for every provisioning).
type Plugin struct {
	cfg atomic.Pointer[Config]
}

// New returns a Plugin for cfg.
func New(cfg Config) *Plugin {
	p := &Plugin{}
	p.cfg.Store(&cfg)
	return p
}

// Reconfigure replaces the live Config.
func (p *Plugin) Reconfigure(cfg Config) { p.cfg.Store(&cfg) }

func (p *Plugin) config() Config { return *p.cfg.Load() }

var (
	_ umuruntime.PrefixProvisioner = (*Plugin)(nil)
	_ umuruntime.PrefixInspector   = (*Plugin)(nil)
)

func (p *Plugin) Name() string { return Name }

func (p *Plugin) Provides() []umuruntime.Capability {
	return []umuruntime.Capability{umuruntime.CapMSVCRT}
}

func (p *Plugin) Needs() []umuruntime.Need {
	return []umuruntime.Need{{Cap: umuruntime.CapGUI, Phase: umuruntime.PhaseProvision, Soft: true}}
}

func (p *Plugin) active() bool {
	c := p.config()
	return c.Managed && c.Install
}

// Pending: the DLL overrides are missing. Deliberately not "the native
// runtime is missing" — the installer can never succeed on a headless host,
// so that trigger would rerun a regedit container on every start. The
// overrides are the load-bearing half and the half that works headless.
func (p *Plugin) Pending(prefix string) bool {
	return p.active() && !vcredist.OverridesApplied(prefix)
}

// Satisfied: the native runtime is in system32. Never Definitive — the
// judgement is a PE header heuristic, and ARK ships native copies of most of
// the runtime next to its exe, so a "no" here often still runs. Blocking a
// launch on it is exactly what docs/LINUX_COMPATIBILITY_PLAN.md §1 goal 5
// rules out.
func (p *Plugin) Satisfied(prefix string) umuruntime.Readiness {
	ok := vcredist.InstalledIn(prefix)
	detail := "system32 里的 " + vcredist.ProbeDLL + " 是微软原生版本"
	if !ok {
		detail = "system32 里的 " + vcredist.ProbeDLL + " 不是微软原生版本"
	}
	return umuruntime.Readiness{OK: ok, Definitive: false, Detail: detail}
}

// Provision runs pkg/vcredist's two steps (overrides, then the installer)
// on pc.Prefix. A skipped installer is Degraded with the skip's cause, never
// an error: the overrides are in place and ordinary launches are fine.
func (p *Plugin) Provision(ctx context.Context, pc *umuruntime.ProvisionContext) (umuruntime.Outcome, error) {
	if !p.active() {
		return umuruntime.Outcome{Kind: umuruntime.Skipped}, nil
	}
	cfg := p.config()

	inst := vcredist.New(vcredist.Config{
		Dir:          cfg.Dir,
		URL:          cfg.URL,
		SHA256:       cfg.SHA256,
		AutoDownload: cfg.AutoDownload,
		Umu:          pc.Umu,
		ChownPath:    pc.ChownPath,
		// The installer (not the overrides) needs a display — the same
		// capability, from the same providers, as a launch that needs one.
		// "This host has none" and "it has one but this attempt failed" map
		// onto pkg/vcredist's two skip reasons.
		AcquireDisplay: func() ([]string, string, error) {
			lease, err := pc.Acquire(ctx, umuruntime.CapGUI)
			var (
				unavailable *umuruntime.CapabilityUnavailableError
				failed      *umuruntime.AcquireError
			)
			switch {
			case errors.As(err, &unavailable):
				return nil, "", fmt.Errorf("%w: %s", vcredist.ErrNoDisplay, unavailable.Why)
			case errors.As(err, &failed):
				return nil, "", failed.Err
			case err != nil:
				return nil, "", err
			}
			return lease.Apply(nil), lease.Describe(), nil
		},
		OnUnverifiedDownload: func(url string) {
			if cfg.OnUnverifiedDownload != nil {
				cfg.OnUnverifiedDownload(url, pc.Logf)
			}
		},
	})

	res, err := inst.Ensure(ctx, pc.Prefix, pc.Logf)
	if err != nil {
		return umuruntime.Outcome{Detail: res}, err
	}
	switch res.Skip {
	case vcredist.SkipNoDisplay, vcredist.SkipDisplayUnavailable:
		return umuruntime.Outcome{Kind: umuruntime.Degraded, Cause: res.SkipCause, Detail: res}, nil
	case vcredist.SkipAlreadyInstalled:
		return umuruntime.Outcome{Kind: umuruntime.AlreadySatisfied, Detail: res}, nil
	}
	return umuruntime.Outcome{Kind: umuruntime.Done, Detail: res}, nil
}

// InspectPrefix is vcredist.Inspect for ic.Prefix / ic.ExeDir, plus the
// three fields pkg/vcredist leaves to its caller: whether the prefix is ours
// (Managed) and which display the installer would use, or why none
// (asked through ic.Probe — read-only, it never starts an X server).
// Ready is Satisfied's answer.
func (p *Plugin) InspectPrefix(ic umuruntime.InspectContext) umuruntime.Status {
	info := vcredist.Inspect(ic.Prefix, ic.ExeDir)
	info.Managed = p.config().Managed
	if ic.Probe != nil {
		if ok, detail := ic.Probe(umuruntime.CapGUI); ok {
			info.InstallerDisplay = detail
		} else {
			info.InstallerBlocked = detail
		}
	}
	sat := p.Satisfied(ic.Prefix)
	return umuruntime.Status{Ready: sat.OK, Detail: sat.Detail, Data: info}
}
