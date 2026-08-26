package installer

import (
	"os"
	"path/filepath"
	"strings"
)

// ApplyLinuxFixups applies the ASA-on-Wine compatibility fixes documented in
// docs/LINUX_COMPATIBILITY_PLAN.md §5.5 (disabling the Sentry crashpad
// plugin, writing steam_appid.txt, symlinking the Steam SDK). No-op on
// Windows. Idempotent — safe to call after every server-files update and
// before every server launch.
func ApplyLinuxFixups() error {
	return applyLinuxFixups()
}

// arkDedicatedServerAppID is ARK: Survival Ascended's *dedicated server*
// Steam AppID. It is not the game's own AppID (2399830) — an older install
// could have written that one, which is wrong for this purpose.
const arkDedicatedServerAppID = "2430930"

// disableSentryPluginAt renames pluginsDir/sentry to pluginsDir/sentry.disabled.
// ASA's bundled sentry-native crashpad backend reads StackLimit/StackBase
// from Wine's TEB; Wine 10 returns huge values there, so crashpad tries to
// dump gigabytes of stack and the engine never gets past sentry_init().
// Renaming makes sentry_init() fail cleanly with "invalid handler_path"
// instead, and the engine continues normally.
//
// No build constraint — pure path logic (os.Stat/RemoveAll/Rename, nothing
// platform-specific), kept unconstrained so it's unit-tested on any host
// even though only the Linux fixup pipeline calls it today.
func disableSentryPluginAt(pluginsDir string) error {
	sentryDir := filepath.Join(pluginsDir, "sentry")
	disabledDir := filepath.Join(pluginsDir, "sentry.disabled")

	if fi, err := os.Stat(sentryDir); err != nil || !fi.IsDir() {
		return nil // not present: already disabled, or not downloaded yet
	}
	// A stale sentry.disabled can be left over if `validate` re-downloaded
	// the plugin while an earlier run's disabled copy was still there.
	if err := os.RemoveAll(disabledDir); err != nil {
		return err
	}
	return os.Rename(sentryDir, disabledDir)
}

// writeSteamAppIDAt writes the dedicated-server AppID to
// win64Dir/steam_appid.txt. lsteamclient.dll reads this to identify itself
// to the Steam SDK without a running Steam client.
//
// Compares content rather than just checking existence: an install could
// have the game's own AppID written there instead of the server's, which
// would be silently wrong rather than merely missing.
//
// No build constraint, same rationale as disableSentryPluginAt.
func writeSteamAppIDAt(win64Dir string) error {
	if fi, err := os.Stat(win64Dir); err != nil || !fi.IsDir() {
		return nil // server not installed yet
	}

	appIDPath := filepath.Join(win64Dir, "steam_appid.txt")
	current, _ := os.ReadFile(appIDPath)
	if strings.TrimSpace(string(current)) == arkDedicatedServerAppID {
		return nil
	}
	return os.WriteFile(appIDPath, []byte(arkDedicatedServerAppID), 0644)
}
