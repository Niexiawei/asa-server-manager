// Package sysuser manages a dedicated non-root system user a privileged
// parent process can drop a child to, and the ownership handoff that comes
// with it (creating the account, resolving its exec credential, chowning
// paths to it, self-checking that the drop actually works end to end). It
// does not know what the child is, or which directories matter and why —
// the caller (a Wine/Proton game-server launcher, here) supplies the paths
// to chown and to self-check.
//
// See docs/UMU_RUNTIME_USER_PLAN.md for the original motivation: the parent
// keeps running as root, only the game process tree gets dropped.
package sysuser

// Config configures a Manager. All fields are optional; a zero Config
// resolves to "manage a user named Name, uid/gid picked by the system's
// user-admin tool".
type Config struct {
	// Name overrides the account name. Empty uses Name's default.
	Name string
	// UID/GID pin the account to specific numeric ids when creating it, and
	// are cross-checked against an already-existing account of the same
	// name. Zero means "let the system pick".
	UID, GID int
	// RunAsRoot disables the drop entirely: Managed() is always false and
	// every other method becomes a no-op. Mirrors a user's explicit opt-in
	// to running the child as root.
	RunAsRoot bool
	// HomeFallback is the home directory to use when the account doesn't
	// exist yet and hasn't been created (or its HomeDir can't be resolved).
	HomeFallback string
	// DeepProbe makes Problems' self-check actually write a probe file as
	// the dropped user, catching a permission failure a stat-only check
	// can't see (SELinux, ACLs, a noexec/ro mount).
	DeepProbe bool
}

// DefaultName is the account name used when Config.Name is empty.
const DefaultName = "asa-umu-runtime"

// Info is the drop-privileges state, safe to expose to a status/preflight API.
type Info struct {
	Managed  bool   // this process is root and manages a dropped user
	Bypassed bool   // this process is root but RunAsRoot opted out of the drop
	Name     string
	Ready    bool // Managed == false, or the self-check found no Problems
}

// AccessCheck names the paths Problems should verify, all owned by the
// caller's business layout — sysuser only knows they need to be readable/
// writable/probeable by the dropped user, not what they are.
type AccessCheck struct {
	// OwnershipDirs are walked (sampled) for entries not owned by the
	// managed user.
	OwnershipDirs []string
	// TraversableDir must have the "world can pass through" bit set (o+x) —
	// typically the launcher's base data directory.
	TraversableDir string
	// ReadableEntry must be world-readable+executable — typically the Wine
	// runtime's entry point.
	ReadableEntry string
	// ProbeDir is where DeepProbe (if enabled) writes and removes a small
	// file as the dropped user.
	ProbeDir string
}

// Manager manages one dedicated system user. Its methods are platform-split
// (sysuser_linux.go has the real implementation; sysuser_windows.go is all
// no-ops — see docs/UMU_RUNTIME_USER_PLAN.md §2 non-goals for why Windows
// has no equivalent story).
type Manager struct {
	cfg Config
}

// New returns a Manager for cfg. Constructing one does not touch the system;
// nothing happens until a method is called.
func New(cfg Config) *Manager {
	return &Manager{cfg: cfg}
}

func (c Config) userName() string {
	if c.Name != "" {
		return c.Name
	}
	return DefaultName
}
