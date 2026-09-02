package runner

import (
	"path/filepath"
	"strconv"
	"strings"
)

// prefix_mode "overlay": one shared, read-only lower prefix plus a private
// writable layer per instance, so every instance gets its own inode for
// WINEPREFIX — and therefore its own wineserver — without paying for a full
// prefix each. See docs/UMU_PREFIX_OVERLAY_PLAN.md.
//
// This file holds the parts with no platform in them (path layout, mountinfo
// parsing) so they can be unit-tested anywhere, following steamrt.go's split.
// The mounting itself is in overlay_linux.go.

// overlayDirName is fixed under BaseDir on purpose: linux.prefix_dir moves the
// *lower* prefix, and having one setting silently control two different
// layouts is harder to explain than having the writable layers always live in
// one known place. See docs/UMU_PREFIX_OVERLAY_PLAN.md §11.4.
const overlayDirName = "umu-prefix-overlay"

// lowerStampName records which lower the writable layer was built on, so a
// lower that has since been rebuilt (new Proton, reinstalled VC++) can be
// detected instead of being silently mixed with copy-ups from the old one.
const lowerStampName = ".lower-stamp"

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
// handed to Wine as WINEPREFIX. Under the copy fallback (mountOverlay could
// not mount) the very same path is a plain seeded directory instead — the
// path deliberately does not change, so nothing downstream has to know which
// of the two it got.
func overlayMergedDir(cfg Config, key string) string {
	return filepath.Join(overlayInstanceDir(cfg, key), "merged")
}

func overlayStampPath(cfg Config, key string) string {
	return filepath.Join(overlayInstanceDir(cfg, key), lowerStampName)
}

// mountOptionsSafe reports whether a path can appear in overlayfs's comma
// separated mount option string at all.
//
// The kernel parses "lowerdir=A,upperdir=B,workdir=C" by splitting on commas,
// and treats ':' as the separator between stacked lower layers, so a path
// containing either would not mean what it looks like — at best the mount
// fails, at worst it succeeds against a different directory. Instance names
// reach these paths, so this is checked rather than assumed; the caller falls
// back to the copy path with an explanation.
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
// escaping rules are the same but its ordering is not stable across kernels.
// The line shape is
//
//	36 35 98:0 / /mnt rw,noatime shared:1 - overlay overlay rw,lowerdir=…
//	                    ^mount point                ^fs type
//
// with a variable number of optional fields before the " - " separator, which
// is why the split is on that separator rather than on a fixed column index.
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
// tab, newline and backslash in mountinfo paths. A path with a space in it is
// unlikely here but silently mis-parsing one would make a live mount invisible
// to the reconcile pass, which then treats it as removable.
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
