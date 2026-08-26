//go:build linux

package installer

import (
	cfgpkg "asa-server/internal/config"
	"fmt"
	"os"
	"path/filepath"
)

// applyLinuxFixups applies the three ASA-on-Wine 10 compatibility fixes
// scripts/ark_instance_manager.sh's install_base_server() runs after every
// SteamCMD validate. All three are idempotent by design (see each
// function), matching the "not an optimization, decides whether it starts
// at all" framing in docs/LINUX_COMPATIBILITY_PLAN.md §5.5.
func applyLinuxFixups() error {
	pluginsDir := filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame", "Plugins")
	if err := disableSentryPluginAt(pluginsDir); err != nil {
		return fmt.Errorf("failed to disable Sentry plugin: %w", err)
	}

	win64Dir := filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame", "Binaries", "Win64")
	if err := writeSteamAppIDAt(win64Dir); err != nil {
		return fmt.Errorf("failed to write steam_appid.txt: %w", err)
	}

	if err := symlinkSteamSDK(); err != nil {
		return fmt.Errorf("failed to symlink Steam SDK: %w", err)
	}
	return nil
}

// symlinkSteamSDK links SteamCMD's bundled steamclient.so into
// $HOME/.steam/sdk32 and sdk64. Wine's lsteamclient.dll dlopen()s these
// exact paths — resolved via the running user's $HOME, independent of
// WINEPREFIX — to bridge to the native Steam SDK; without them the server
// crashes inside FSteamServerInstanceHandler.
func symlinkSteamSDK() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to resolve $HOME: %w", err)
	}

	pairs := []struct{ src, sdkDir string }{
		{filepath.Join(cfgpkg.SteamCmdDir, "linux32", "steamclient.so"), filepath.Join(home, ".steam", "sdk32")},
		{filepath.Join(cfgpkg.SteamCmdDir, "linux64", "steamclient.so"), filepath.Join(home, ".steam", "sdk64")},
	}
	for _, p := range pairs {
		if _, err := os.Stat(p.src); err != nil {
			continue // SteamCMD hasn't been run yet, or doesn't ship this arch's .so
		}
		if err := os.MkdirAll(p.sdkDir, 0755); err != nil {
			return err
		}
		linkPath := filepath.Join(p.sdkDir, "steamclient.so")
		// -f semantics: refresh a stale link left by an old BaseDir.
		os.Remove(linkPath)
		if err := os.Symlink(p.src, linkPath); err != nil {
			return err
		}
	}
	return nil
}
