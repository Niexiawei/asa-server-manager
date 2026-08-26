//go:build windows

package installer

// applyLinuxFixups has nothing to do on Windows — the Sentry/steam_appid/SDK
// symlink issues are all specific to running ArkAscendedServer.exe under
// Wine (see docs/LINUX_COMPATIBILITY_PLAN.md §5.5).
func applyLinuxFixups() error { return nil }
