//go:build windows

package plugindata

// warnIfPluginsPathCaseMismatch is a no-op on Windows: NTFS is
// case-insensitive, so pluginsRelPath's hardcoded casing can never
// silently mismatch what's actually on disk there.
func warnIfPluginsPathCaseMismatch(mirrorDir string) {}
