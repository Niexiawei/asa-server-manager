//go:build linux

// Package umu manages the umu-launcher/GE-Proton runtime a Wine/Proton game
// server needs on Linux: downloading umu-run and a pinned GE-Proton build,
// resolving the Python interpreter that runs the umu-launcher zipapp,
// warming a Wine prefix (wineboot --init) to a verified-usable state, and
// detecting whether a wineserver process still holds a given prefix open.
//
// It does not know about ASA, instances, or which Wine prefix belongs to
// which instance — the caller supplies an explicit prefix directory to
// WarmPrefix, and drop-privileges/chown decisions are injected via Config's
// callbacks (they come from asa-server/pkg/sysuser, which this package does
// not import).
//
// The package is Linux-only in its entirety: umu/Wine/Proton have no
// Windows equivalent, so callers simply don't construct a Runtime there
// (mirrors asa-server/pkg/sysuser and asa-server/pkg/xvfb).
package umu

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"

	"asa-server/pkg/pyfinder"
)

// Config configures a Runtime. All fields are mechanism-level; PrefixDir/
// PrefixMode/business notions of "which instance" live one layer up.
type Config struct {
	// BaseDir roots umu-launcher/, proton/, and (via HomeDir) the umu
	// download cache.
	BaseDir string
	// UmuVersion is the pinned umu-launcher release tag to install.
	UmuVersion string
	// ProtonVersion is the pinned GE-Proton release tag to install and run.
	ProtonVersion string
	// GameID is passed to umu-run as GAMEID.
	GameID string
	// AutoDownload gates whether EnsureUmu/EnsureGEProton may fetch anything.
	AutoDownload bool
	// SteamRTPrefetch enables the Steam Linux Runtime prefetch optimization
	// (asa-server/pkg/steamrt) before wineboot runs.
	SteamRTPrefetch bool
	// PythonBin overrides interpreter auto-discovery (asa-server/pkg/pyfinder).
	PythonBin string

	// HomeDir returns the current runtime-user home (for the umu cache dir
	// and the child's HOME env). Called fresh on every use.
	HomeDir func() string
	// ChildIDs returns the uid/gid the dropped child runs as, and whether a
	// drop is in effect. Read-only. Called fresh on every use.
	ChildIDs func() (uid, gid uint32, managed bool)
	// Credential resolves (creating the account if needed) the credential
	// to run wineboot under. Called fresh on every warm.
	Credential func() (*syscall.Credential, error)
	// ChownPath chowns a single path (non-recursive) to the runtime user.
	ChownPath func(path string) error
	// UserName is the runtime user's account name, for the dropped child's
	// USER/LOGNAME env (cosmetic, but real processes and tools do read it).
	UserName func() string
}

func (c Config) homeDir() string {
	if c.HomeDir == nil {
		return ""
	}
	return c.HomeDir()
}

func (c Config) childIDs() (uid, gid uint32, managed bool) {
	if c.ChildIDs == nil {
		return 0, 0, false
	}
	return c.ChildIDs()
}

func (c Config) credential() (*syscall.Credential, error) {
	if c.Credential == nil {
		return nil, nil
	}
	return c.Credential()
}

func (c Config) chownPath(path string) error {
	if c.ChownPath == nil {
		return nil
	}
	return c.ChownPath(path)
}

func (c Config) userName() string {
	if c.UserName == nil {
		return ""
	}
	return c.UserName()
}

// ProtonNoXalia turns off Xalia, the accessibility/gamepad UI overlay
// GE-Proton starts alongside every launch. Its script defaults
// PROTON_USE_XALIA to "1" but honours a value supplied from outside, which
// is how this works — GE-Proton10-34's proton, L2093.
//
// A dedicated server has no screen and nobody sitting at one, so the overlay
// has nothing to do in *any* launch. And without a display it doesn't
// merely idle, it **fails**: SDL finds no video driver and Xalia dies with
//
//	System.PlatformNotSupportedException: Video driver  not supported
//
// harmless to the program actually being launched, but noise that mimics a
// fault costs more than the process it comes from. Applied at every umu/
// Proton command line this package (and its caller) builds.
const ProtonNoXalia = "PROTON_USE_XALIA=0"

// Runtime manages one umu/GE-Proton installation. Config is held behind an
// atomic pointer, refreshed on every call, matching the pattern used by
// pkg/xvfb.Manager and internal/runner's sysUserFor: cheap to call before
// every use, so the caller's own Configure() needs no special hook for it.
type Runtime struct {
	cfg atomic.Pointer[Config]

	// mu serializes first-time initialization (two callers racing to
	// download umu/GE-Proton, or to warm the same prefix, on a fresh
	// install) — see docs/LINUX_COMPATIBILITY_PLAN.md §6 risk 6. Callers
	// needing per-prefix (not whole-Runtime) locking do it themselves.
	mu sync.Mutex

	py *pyfinder.Resolver
}

// New returns a Runtime for cfg.
func New(cfg Config) *Runtime {
	r := &Runtime{py: pyfinder.New()}
	r.cfg.Store(&cfg)
	return r
}

// Reconfigure updates the live Config. Not a new Runtime: doing so would
// discard py's resolved-interpreter cache for no reason, since nothing here
// holds state that a Config change should invalidate.
func (r *Runtime) Reconfigure(cfg Config) {
	r.cfg.Store(&cfg)
}

func (r *Runtime) config() Config {
	if c := r.cfg.Load(); c != nil {
		return *c
	}
	return Config{}
}

// Dir is umu-launcher's install directory.
func (r *Runtime) Dir() string { return filepath.Join(r.config().BaseDir, "umu-launcher") }

// RunPath is the umu-run entry point.
func (r *Runtime) RunPath() string { return filepath.Join(r.Dir(), "umu-run") }

// ProtonBaseDir holds every installed GE-Proton build.
func (r *Runtime) ProtonBaseDir() string { return filepath.Join(r.config().BaseDir, "proton") }

// ProtonPath is the pinned GE-Proton build's directory.
func (r *Runtime) ProtonPath() string {
	return filepath.Join(r.ProtonBaseDir(), r.config().ProtonVersion)
}

// Interpreter resolves the Python interpreter umu-run should execute under
// (asa-server/pkg/pyfinder), using Config.PythonBin as an explicit override.
func (r *Runtime) Interpreter() (pyfinder.Info, error) {
	return r.py.Resolve(r.config().PythonBin)
}
