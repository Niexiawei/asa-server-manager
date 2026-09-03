package runner

import (
	"context"
	"io"
	"strings"
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
	InUse bool
	// SizeBytes is what this prefix costs on disk **exclusively**. For an
	// overlay layer that is its upper directory, not the merge view: the whole
	// point of the mode is that the bulk is shared, and measuring the merge
	// would report the shared lower once per instance.
	SizeBytes int64
	// Overlay marks a prefix_mode "overlay" writable layer rather than a
	// standalone prefix. Its content is the lower plus this layer's copy-ups.
	Overlay bool
	// Mounted distinguishes overlay's two shapes, which occupy the same path:
	// a real overlayfs mount, or the copy the fallback seeded when overlayfs
	// was unavailable. Meaningless unless Overlay is set.
	Mounted bool
	// Current means the prefix_mode in force right now would use this exact
	// path for this key — i.e. it is live, not a leftover from a past mode.
	//
	// Switching modes strands the previous mode's directory: an instance that
	// ran under "per-instance" and now runs under "overlay" still has its
	// ~700MB umu-prefix-<name> sitting there, owned by an instance that very
	// much still exists. "Does the instance exist" therefore can't answer
	// "is this reclaimable" — this can.
	Current bool
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

// PrepareSharedPrefixWrite makes it safe to modify the shared Wine prefix,
// returning an end-user-readable error when it currently isn't. Always nil on
// Windows and outside prefix_mode "overlay".
//
// Callers are the operations that write the shared prefix while instances may
// be running: EnsureRuntime (wineboot, the Proton version reconcile, the VC++
// registry write) and the two verify commands, which launch a real wineserver
// against it. Under "overlay" those writes go to a directory that live mounts
// use as their lowerdir, which overlayfs documents as undefined behaviour —
// and the symptoms would land on the *instances*, not on the command that
// caused them. See docs/UMU_PREFIX_OVERLAY_PLAN.md §6.1/§12.4.
//
// Not a pure check: it unmounts writable layers nothing is using, because
// those mounts outlive their instances by design and "stop the instances"
// would otherwise never be enough to clear them. See
// prepareSharedPrefixWrite.
//
// op names the operation for the log ("asa-server verify" and friends); the
// returned closure marks the end of the window in which the shared prefix may
// be written, and must be called (defer is the natural shape). It is never nil
// when err is nil, and it is a no-op outside prefix_mode "overlay".
func PrepareSharedPrefixWrite(op string) (func(), error) { return prepareSharedPrefixWrite(op) }

// ReconcilePrefixes cleans up prefix state a crash could have left behind.
// Cheap (one /proc read plus a stat per layer), read-only unless something is
// actually broken, and a no-op on Windows and outside prefix_mode "overlay".
//
// Call it once at startup, before anything launches. It deliberately does NOT
// unmount layers whose instance simply isn't running: overlay mounts live in
// the host mount namespace and are meant to survive restarts, so "mounted but
// idle" is the normal resting state, not garbage.
func ReconcilePrefixes() { reconcilePrefixes() }

// wineprefixValueUnder reports whether a live wineserver's WINEPREFIX value
// refers to the prefix directory `prefix` — either that directory itself or
// something inside it.
//
// The comparison has to be on **path** boundaries, not on string boundaries.
// All our prefixes are siblings that share a name stem
// ("<BaseDir>/umu-prefix", "<BaseDir>/umu-prefix-<instance>"), so a plain
// strings.HasPrefix answers "is the shared prefix in use?" with "yes" whenever
// *any* per-instance prefix is in use:
//
//	value                            prefix              plain HasPrefix  correct
//	<BaseDir>/umu-prefix/pfx/        <BaseDir>/umu-prefix      true          true
//	<BaseDir>/umu-prefix-jibian/pfx/ <BaseDir>/umu-prefix      true         false
//	<BaseDir>/umu-prefix-A/pfx/      <BaseDir>/umu-prefix-AB  false         false
//
// That was the behaviour until 2026-09-01. The symptoms were mild enough to go
// unnoticed — `prefix status` reporting the shared prefix as in use, `prefix
// gc` and RemoveInstancePrefix refusing prefixes nothing holds, and the 90s
// drain in warmPrefix waiting out its full deadline — but every one of them is
// a false "something is still running", which is the direction that makes
// cleanup impossible rather than unsafe. See
// docs/UMU_PREFIX_OVERLAY_PLAN.md §12.2.
//
// Both sides are compared as-written: symlinks are not resolved (we cannot
// resolve another process's view reliably) and neither side is made absolute,
// which is fine because both come from the same layout code.
func wineprefixValueUnder(value, prefix string) bool {
	v := strings.TrimRight(value, "/")
	want := strings.TrimRight(prefix, "/")
	if v == "" || want == "" {
		return false
	}
	return v == want || strings.HasPrefix(v, want+"/")
}
