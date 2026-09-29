// Package wineprefix manages Wine prefix directories for a Wine/Proton game
// server on Linux: which directory a given key's prefix lives in under
// each isolation mode ("shared" | "per-instance" | "overlay"), creating and
// warming one on first use, listing what's on disk, and safely reclaiming
// it later.
//
// It does not know about ASA or instances — key is an opaque identifier the
// caller derives (an instance name, or "" for the shared prefix). It depends
// on asa-server/pkg/umu for the actual "warm a Wine prefix"/"is a wineserver
// still using it" mechanism. Anything installed *into* a prefix afterwards
// (the VC++ runtime, today) is the caller's: it is injected as the generic
// Provision/Pending hooks, so this package knows "call this after creating a
// prefix", never what gets installed.
package wineprefix

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
)

// Config configures a Manager. All fields are mechanism-level.
type Config struct {
	// BaseDir roots the shared prefix (unless PrefixDir overrides it) and
	// the overlay writable-layer directory.
	BaseDir string
	// PrefixDir overrides the shared/lower prefix's directory. "" means
	// {BaseDir}/umu-prefix.
	PrefixDir string
	// PrefixMode selects isolation: "shared" (default/unrecognized),
	// "per-instance", or "overlay".
	PrefixMode string
	// ProtonVersion is compared against a prefix's own marker to detect a
	// Proton-generation bump that requires a rebuild.
	ProtonVersion string
	// ChownPath chowns a single path (non-recursive) to the runtime user.
	ChownPath func(path string) error

	// Provision installs whatever the caller layers on top of a bare prefix
	// (asa-server/pkg/umuruntime's prefix-provisioner plugins). EnsurePrefix
	// calls it after creating a prefix, and on an existing one whenever
	// Pending says there is work left. key is the prefix's key (for wording),
	// prefix its directory. An error fails EnsurePrefix — the caller decides
	// which of its failures are worth that, and swallows the rest. nil = none.
	Provision func(ctx context.Context, key, prefix string, logf func(string, ...any)) error
	// Pending reports, cheaply and offline, whether Provision still has work
	// to do in prefix. It gates EnsurePrefix's fast path and LowerNeedsWork.
	// nil = never.
	Pending func(prefix string) bool
	// ProvisionFingerprint summarises, cheaply and offline, the state of
	// whatever Provision layered onto prefix. It becomes part of the lower
	// stamp an overlay writable layer is built against (see lowerStampName),
	// so provisioning the lower later — the VC++ runtime installed once a
	// display became available — invalidates the layers built before it.
	// Must not contain ';' or a newline. nil or "" = the Proton marker alone.
	ProvisionFingerprint func(prefix string) string
}

func (c Config) chownPath(path string) error {
	if c.ChownPath == nil {
		return nil
	}
	return c.ChownPath(path)
}

func (c Config) pending(prefix string) bool {
	return c.Pending != nil && c.Pending(prefix)
}

func (c Config) provision(ctx context.Context, key, prefix string, logf func(string, ...any)) error {
	if c.Provision == nil {
		return nil
	}
	return c.Provision(ctx, key, prefix, logf)
}

// Info is one Wine prefix directory found on disk.
type Info struct {
	// Key is "" for the shared prefix, else the key it was created for.
	Key  string
	Path string
	// Initialized mirrors the "this prefix is usable" judgement the launch
	// path uses (system.reg + drive_c/windows/system32 both present).
	Initialized bool
	// ProtonVersion is the .created-by-proton marker, "" when absent.
	ProtonVersion string
	// InUse means a wineserver still has this prefix open.
	InUse bool
	// SizeBytes is what this prefix costs on disk **exclusively**. For an
	// overlay layer that is its upper directory, not the merge view.
	SizeBytes int64
	// Overlay marks a prefix_mode "overlay" writable layer rather than a
	// standalone prefix.
	Overlay bool
	// Mounted distinguishes overlay's two shapes, which occupy the same
	// path: a real overlayfs mount, or the copy the fallback seeded when
	// overlayfs was unavailable. Meaningless unless Overlay is set.
	Mounted bool
	// Current means the mode in force right now would use this exact path
	// for this key — i.e. it is live, not a leftover from a past mode.
	Current bool
}

// --- overlay path layout (portable: no platform-specific API) ---

// overlayDirName is fixed under BaseDir on purpose: PrefixDir moves the
// *lower* prefix, and having one setting silently control two different
// layouts is harder to explain than having the writable layers always live
// in one known place.
const overlayDirName = "umu-prefix-overlay"

// lowerStampName records which lower the writable layer was built on, so a
// lower that has since changed (new Proton, a component provisioned into it
// later) is detected instead of being silently mixed with copy-ups from the
// old one — an upper that copied up system.reg or system32 before the change
// would otherwise shadow it for good.
//
// Its content is composeLowerStamp's: the lower's Proton marker, then one
// ";"-separated ProvisionFingerprint part. Until 2026-09 it was the Proton
// marker alone, which could not see a VC++ install at all
// (docs/UMU_PREFIX_PLAN.md §8.1); layers stamped that way no longer match
// and are rebuilt once, which loses nothing — the upper holds no user data.
const lowerStampName = ".lower-stamp"

// composeLowerStamp builds a .lower-stamp value. protonMarker comes first so
// that stampProtonVersion can always recover it.
func composeLowerStamp(protonMarker, provisionFingerprint string) string {
	if provisionFingerprint == "" {
		return protonMarker
	}
	return protonMarker + ";" + provisionFingerprint
}

// stampProtonVersion is the Proton marker part of a .lower-stamp value — what
// Info.ProtonVersion reports for an overlay layer. Old, marker-only stamps
// come back unchanged.
func stampProtonVersion(stamp string) string {
	v, _, _ := strings.Cut(stamp, ";")
	return v
}

func overlayRoot(cfg Config) string { return filepath.Join(cfg.BaseDir, overlayDirName) }

func overlayInstanceDir(cfg Config, key string) string {
	return filepath.Join(overlayRoot(cfg), key)
}

func overlayUpperDir(cfg Config, key string) string {
	return filepath.Join(overlayInstanceDir(cfg, key), "upper")
}

func overlayWorkDir(cfg Config, key string) string {
	return filepath.Join(overlayInstanceDir(cfg, key), "work")
}

// overlayMergedDir is the mount point, i.e. the directory that is actually
// handed to Wine as WINEPREFIX. Under the copy fallback (mounting failed)
// the very same path is a plain seeded directory instead — the path
// deliberately does not change, so nothing downstream has to know which of
// the two it got.
func overlayMergedDir(cfg Config, key string) string {
	return filepath.Join(overlayInstanceDir(cfg, key), "merged")
}

func overlayStampPath(cfg Config, key string) string {
	return filepath.Join(overlayInstanceDir(cfg, key), lowerStampName)
}

// mountOptionsSafe reports whether a path can appear in overlayfs's comma
// separated mount option string at all.
//
// The kernel parses "lowerdir=A,upperdir=B,workdir=C" by splitting on
// commas, and treats ':' as the separator between stacked lower layers, so a
// path containing either would not mean what it looks like — at best the
// mount fails, at worst it succeeds against a different directory. Instance
// names reach these paths, so this is checked rather than assumed; the
// caller falls back to the copy path with an explanation.
func mountOptionsSafe(paths ...string) bool {
	for _, p := range paths {
		if strings.ContainsAny(p, ",:\\\n") {
			return false
		}
	}
	return true
}

// parseOverlayMounts returns the mount points of every currently mounted
// overlay filesystem, read out of /proc/self/mountinfo content.
//
// mountinfo rather than /proc/mounts: the latter's first field is the mount
// *source*, which for overlay is the useless literal "overlay", and its
// escaping rules are the same but its ordering is not stable across
// kernels. The line shape is
//
//	36 35 98:0 / /mnt rw,noatime shared:1 - overlay overlay rw,lowerdir=…
//	                    ^mount point                ^fs type
//
// with a variable number of optional fields before the " - " separator,
// which is why the split is on that separator rather than on a fixed
// column index.
func parseOverlayMounts(mountinfo string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(mountinfo, "\n") {
		sep := strings.Index(line, " - ")
		if sep < 0 {
			continue
		}
		left := strings.Fields(line[:sep])
		right := strings.Fields(line[sep+3:])
		if len(left) < 5 || len(right) < 1 {
			continue
		}
		if right[0] != "overlay" {
			continue
		}
		out[unescapeMountPath(left[4])] = true
	}
	return out
}

// unescapeMountPath undoes the octal escaping the kernel applies to space,
// tab, newline and backslash in mountinfo paths. A path with a space in it
// is unlikely here but silently mis-parsing one would make a live mount
// invisible to the reconcile pass, which then treats it as removable.
func unescapeMountPath(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// overlayKeyFromMerged maps ".../umu-prefix-overlay/<key>/merged" back to
// <key>, and reports false for any path that isn't one of ours.
func overlayKeyFromMerged(root, mountPoint string) (string, bool) {
	rel, err := filepath.Rel(root, mountPoint)
	if err != nil {
		return "", false
	}
	dir, base := filepath.Split(filepath.Clean(rel))
	key := strings.TrimSuffix(dir, string(filepath.Separator))
	if base != "merged" || key == "" || strings.Contains(key, string(filepath.Separator)) || strings.HasPrefix(key, "..") {
		return "", false
	}
	return key, true
}
