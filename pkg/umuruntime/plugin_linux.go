//go:build linux

package umuruntime

import (
	"context"

	"asa-server/pkg/umu"
)

// PrefixProvisioner provides a capability by installing something into a Wine
// prefix (the VC++ runtime; later perhaps d3dcompiler, dotnet, fonts).
//
// The Host runs Provision on the shared prefix during Ensure, and — through
// pkg/wineprefix's Provision/Pending hooks — on every per-instance prefix
// when it is created or found wanting. Provisioning the shared prefix always
// happens inside its write window (wineprefix.Manager.PrepareSharedWrite);
// plugins never have to remember that.
type PrefixProvisioner interface {
	Plugin
	// Pending reports whether Provision still has work worth doing in
	// prefix. Must be cheap and offline: it runs on every instance start.
	// It is NOT "is the capability satisfied" — the VC++ installer can never
	// succeed on a headless host, and using that as the trigger would rerun
	// it on every start.
	Pending(prefix string) bool
	// Satisfied reports whether the capability is usable in prefix.
	// Read-only, offline.
	Satisfied(prefix string) Readiness
	// Provision does the work. A non-nil error is a failure; everything
	// else (including "skipped part of it, and here is why") is an Outcome.
	// The Host fills in Outcome.Plugin and Outcome.Key.
	Provision(ctx context.Context, pc *ProvisionContext) (Outcome, error)
}

// ProvisionContext is everything a provisioner may use.
type ProvisionContext struct {
	// Key is the prefix's key ("" = the shared one); Prefix its directory.
	Key    string
	Prefix string
	// Umu runs executables inside the prefix (umu.Runtime.RunInPrefix).
	Umu *umu.Runtime
	// ChownPath hands a path to the runtime identity; nil = no-op.
	ChownPath func(string) error
	// Logf receives progress lines; never nil.
	Logf func(string, ...any)
	// Acquire gets a lease on a capability the plugin needs while
	// provisioning (see Host.Acquire for the error contract: a machine that
	// simply can't provide it is ErrCapabilityUnavailable).
	Acquire func(ctx context.Context, c Capability) (Lease, error)
}
