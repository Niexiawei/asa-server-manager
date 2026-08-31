package runner

import (
	"context"
	"io"
)

// PrefixInfo is one Wine prefix directory found on disk.
//
// runner deliberately does NOT know what an "instance" is, so it can't say
// whether a prefix is an orphan — it only reports the key it was created for.
// Cross-referencing keys against real instances is the caller's job (see
// internal/actions/prefix.go).
type PrefixInfo struct {
	// Key is "" for the shared prefix, else the instance name it belongs to.
	Key  string
	Path string
	// Initialized mirrors the "this prefix is usable" judgement the launch
	// path uses (system.reg + drive_c/windows/system32 both present).
	Initialized bool
	// ProtonVersion is the .created-by-proton marker, "" when absent.
	ProtonVersion string
	// InUse means a wineserver still has this prefix open — deleting it would
	// pull the rug out from under a running instance.
	InUse     bool
	SizeBytes int64
}

// PrefixKeyFor maps an instance to the Options.PrefixKey it should launch
// with. Empty means the shared prefix, which is what every caller gets on
// Windows and under prefix_mode "shared".
//
// This is the single place that turns "which mode are we in" into "which
// prefix does this instance use" — callers pass the result straight through to
// Options.PrefixKey, EnsurePrefix and PrefixHasVCRedist without repeating the
// mode check. See docs/UMU_PREFIX_PER_INSTANCE_PLAN.md §9.
func PrefixKeyFor(instanceName string) string { return prefixKeyFor(instanceName) }

// EnsurePrefix makes sure the Wine prefix identified by prefixKey exists and
// is usable, creating it if this is the instance's first launch under
// prefix_mode "per-instance". No-op on Windows.
//
// It never downloads umu/GE-Proton/the Steam Linux Runtime — those are global,
// shared, and remain EnsureRuntime's job; a missing one is reported as "run
// asa-server setup" rather than silently fetched on a start path. An empty
// prefixKey therefore only verifies the shared prefix, it never rebuilds it.
//
// progress receives human-readable status lines (nil is fine — they still go
// to the log). Creating a second prefix costs one `wineboot --init`, on the
// order of a minute; a prefix that already exists costs a couple of stats.
func EnsurePrefix(ctx context.Context, prefixKey string, progress io.Writer) error {
	return ensurePrefix(ctx, prefixKey, progress)
}

// RemoveInstancePrefix deletes the per-instance Wine prefix belonging to
// instanceName, if one exists. No-op on Windows, and a no-op when that
// instance never got its own prefix.
//
// Deliberately independent of the current prefix_mode: a prefix left behind by
// a past stint in per-instance mode still has to be cleanable after switching
// back to shared. Refuses while a wineserver still holds the prefix.
//
// There is no rename counterpart on purpose. A Wine prefix embeds its own
// absolute path in several places, so moving one is not reliably safe, and it
// holds no user data (saves live in instances/<name>/Save) — deleting it and
// letting the next launch rebuild costs about a minute and cannot corrupt
// anything. See docs/UMU_PREFIX_PER_INSTANCE_PLAN.md §9.2.
func RemoveInstancePrefix(instanceName string) error {
	return removeInstancePrefix(instanceName)
}

// PrefixStatus lists every Wine prefix directory under BaseDir — the shared
// one plus any per-instance ones. Read-only and offline. Empty on Windows.
func PrefixStatus() []PrefixInfo { return prefixStatus() }
