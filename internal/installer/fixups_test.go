package installer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDisableSentryPluginAt_RenamesSentryDir(t *testing.T) {
	pluginsDir := t.TempDir()
	sentryDir := filepath.Join(pluginsDir, "sentry")
	if err := os.MkdirAll(filepath.Join(sentryDir, "sub"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	marker := filepath.Join(sentryDir, "sub", "marker.txt")
	if err := os.WriteFile(marker, []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := disableSentryPluginAt(pluginsDir); err != nil {
		t.Fatalf("disableSentryPluginAt: %v", err)
	}

	if _, err := os.Stat(sentryDir); err == nil {
		t.Error("expected sentry dir to no longer exist")
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "sentry.disabled", "sub", "marker.txt")); err != nil {
		t.Errorf("expected renamed content to survive at sentry.disabled/sub/marker.txt: %v", err)
	}
}

func TestDisableSentryPluginAt_OverwritesStaleDisabled(t *testing.T) {
	pluginsDir := t.TempDir()
	sentryDir := filepath.Join(pluginsDir, "sentry")
	staleDisabled := filepath.Join(pluginsDir, "sentry.disabled")
	if err := os.MkdirAll(sentryDir, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.MkdirAll(staleDisabled, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staleDisabled, "old.txt"), []byte("stale"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := disableSentryPluginAt(pluginsDir); err != nil {
		t.Fatalf("disableSentryPluginAt: %v", err)
	}

	if _, err := os.Stat(filepath.Join(staleDisabled, "old.txt")); err == nil {
		t.Error("expected the stale sentry.disabled to have been replaced, but its old content is still there")
	}
}

func TestDisableSentryPluginAt_NoopWhenAbsent(t *testing.T) {
	pluginsDir := t.TempDir()
	if err := disableSentryPluginAt(pluginsDir); err != nil {
		t.Fatalf("expected no error when sentry dir is absent, got %v", err)
	}
}

func TestWriteSteamAppIDAt_WritesCorrectAppID(t *testing.T) {
	win64Dir := t.TempDir()

	if err := writeSteamAppIDAt(win64Dir); err != nil {
		t.Fatalf("writeSteamAppIDAt: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(win64Dir, "steam_appid.txt"))
	if err != nil {
		t.Fatalf("expected steam_appid.txt to be written: %v", err)
	}
	if string(got) != arkDedicatedServerAppID {
		t.Errorf("steam_appid.txt = %q, want %q", got, arkDedicatedServerAppID)
	}
}

// TestWriteSteamAppIDAt_CorrectsWrongAppID covers the real bug this fixup
// exists for: an older install (or a mistaken manual edit) leaving the
// game's own AppID (2399830) instead of the dedicated server's (2430930).
func TestWriteSteamAppIDAt_CorrectsWrongAppID(t *testing.T) {
	win64Dir := t.TempDir()
	appIDPath := filepath.Join(win64Dir, "steam_appid.txt")
	if err := os.WriteFile(appIDPath, []byte("2399830"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := writeSteamAppIDAt(win64Dir); err != nil {
		t.Fatalf("writeSteamAppIDAt: %v", err)
	}

	got, _ := os.ReadFile(appIDPath)
	if string(got) != arkDedicatedServerAppID {
		t.Errorf("steam_appid.txt = %q, want it corrected to %q", got, arkDedicatedServerAppID)
	}
}

func TestWriteSteamAppIDAt_NoopWhenDirMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if err := writeSteamAppIDAt(missing); err != nil {
		t.Fatalf("expected no error when win64Dir is absent, got %v", err)
	}
}
