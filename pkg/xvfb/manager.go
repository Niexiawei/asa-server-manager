//go:build linux

package xvfb

import (
	"sync"
	"sync/atomic"
	"syscall"
)

// Config configures a Manager. It carries only mechanism-level fields —
// nothing here knows about ASA, instances, or config.yaml section names.
type Config struct {
	// Bin overrides Xvfb's path (e.g. linux.xvfb_bin). Empty means "search
	// PATH, then a few well-known fallback locations".
	Bin string
	// Screen is Xvfb's -screen argument (e.g. linux.xvfb_screen). Empty
	// means DefaultScreen.
	Screen string
	// StatePath is where the last-started display/pid is persisted so a
	// restart can adopt it instead of starting a second one. Empty disables
	// persistence and adoption.
	StatePath string
	// AllowX11Remount permits remounting a read-only SocketDir (WSLg mounts
	// it that way) read-write. false refuses the remount and reports why.
	AllowX11Remount bool

	// HomeDir returns the HOME Xvfb should run with (its log path and the
	// child's HOME env). Called fresh on every use — the account it
	// resolves to may not exist yet at Reconfigure time.
	HomeDir func() string
	// ChildIDs returns the uid/gid Xvfb (and the Wine process it serves)
	// runs as, and whether a drop-privileges identity is in effect at all.
	// Read-only: must not create an account as a side effect. Called fresh
	// on every use.
	ChildIDs func() (uid, gid uint32, managed bool)
	// Credential resolves (creating the account if needed) the credential
	// to spawn Xvfb under. Called fresh on every spawn.
	Credential func() (*syscall.Credential, error)
}

func (c Config) childIDs() (uid, gid uint32, managed bool) {
	if c.ChildIDs == nil {
		return 0, 0, false
	}
	return c.ChildIDs()
}

func (c Config) homeDir() string {
	if c.HomeDir == nil {
		return ""
	}
	return c.HomeDir()
}

func (c Config) credential() (*syscall.Credential, error) {
	if c.Credential == nil {
		return nil, nil
	}
	return c.Credential()
}

// Info is a read-only snapshot of the currently self-managed display, safe
// to expose to a status/preflight API. Running == false means either
// nothing has been started yet, or nothing needs to be (Manager is only
// ever consulted, never spawns anything, to produce this).
type Info struct {
	Running bool
	Display string
	PID     int
}

// SocketDirState is the result of probing whether Xvfb could actually
// publish a socket in SocketDir right now.
type SocketDirState struct {
	// Writable: Xvfb can publish a socket without any help.
	Writable bool
	// Fixable: not writable right now, but Acquire can make it so (a
	// read-only remount, only possible as root with AllowX11Remount).
	Fixable bool
	// Why explains an unwritable/unfixable state, meant to be shown
	// verbatim to a user.
	Why string
}

// Manager owns one self-managed Xvfb server. The zero-value like state
// (spawn-loop channel, current display, adopt-tried flag, remount flag) all
// live on the instance rather than as package-level globals, so that "there
// is exactly one Xvfb for this process" is a property of holding exactly one
// Manager (see internal/runner's single package-level instance), not of
// pkg/xvfb enforcing it via its own singletons.
//
// Config is held behind an atomic pointer rather than protected by mu:
// Status() and other read-only callers (a preflight endpoint, a status
// page) must never block behind Acquire(), which can take up to ~10s on a
// cold start.
type Manager struct {
	cfg atomic.Pointer[Config]

	// mu serializes "start or replace the singleton Xvfb". current is
	// atomic so read-only callers don't need mu at all.
	mu         sync.Mutex
	current    atomic.Pointer[managedXvfb]
	adoptTried bool // guarded by mu; orphan adoption is tried at most once

	// remounted remembers whether Reconfigure...no, Acquire remounted
	// SocketDir read-write, so Stop can restore it. See remountSocketDirRW.
	remounted atomic.Bool

	// spawnOnce/spawnReqs dispatch every process spawn to one dedicated,
	// never-exiting OS thread — see spawnLoop's doc comment for why this
	// is load-bearing, not an optimization.
	spawnOnce sync.Once
	spawnReqs chan *spawnReq
}

// New returns a Manager for cfg. Constructing one starts nothing; the first
// real work happens on the first Acquire.
func New(cfg Config) *Manager {
	m := &Manager{}
	m.cfg.Store(&cfg)
	return m
}

// Reconfigure updates the live Config. Deliberately not a new Manager: this
// instance may be mid-way through owning a running Xvfb process (the spawn
// thread, the watchdog goroutine, the current display) that a fresh Manager
// would know nothing about and would leak. The next Acquire/Status/Stop
// call picks up the new Config; nothing currently running is disturbed.
func (m *Manager) Reconfigure(cfg Config) {
	m.cfg.Store(&cfg)
}

func (m *Manager) config() Config {
	if c := m.cfg.Load(); c != nil {
		return *c
	}
	return Config{}
}
