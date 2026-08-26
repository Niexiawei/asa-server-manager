//go:build linux

package plugindata

import "path/filepath"

// pathCompareKey folds a cleaned path to a comparable form. Case is
// deliberately NOT folded here: most Linux filesystems (ext4/xfs/btrfs) are
// case-sensitive, so `/a/DB` and `/a/db` are genuinely different paths —
// folding them together would misjudge a real external DbPathOverride as
// pointing inside the instance directory. See
// docs/LINUX_COMPATIBILITY_PLAN.md §5.12 table item 2.
func pathCompareKey(cleaned string) string {
	return filepath.ToSlash(cleaned)
}
