//go:build windows

package plugindata

import (
	"path/filepath"
	"strings"
)

// pathCompareKey folds a cleaned path to a comparable form. NTFS is
// case-insensitive, so folding case here matches how Windows itself treats
// the two paths as the same file.
func pathCompareKey(cleaned string) string {
	return strings.ToLower(filepath.ToSlash(cleaned))
}
