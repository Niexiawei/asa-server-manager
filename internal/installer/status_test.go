package installer

import (
	cfgpkg "asa-server/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func withBaseDir(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	oldSteam, oldServer := cfgpkg.SteamCmdDir, cfgpkg.ServerFilesDir
	cfgpkg.SteamCmdDir = filepath.Join(base, "steamcmd")
	cfgpkg.ServerFilesDir = filepath.Join(base, "server-files")
	t.Cleanup(func() {
		cfgpkg.SteamCmdDir, cfgpkg.ServerFilesDir = oldSteam, oldServer
	})
	return base
}

func TestCheckInstalled_EmptyBaseDir(t *testing.T) {
	withBaseDir(t)

	st := CheckInstalled()
	if st.SteamCmdReady || st.ServerBinaryReady || st.ServerConfigReady || st.Ready() {
		t.Fatalf("expected all-false on an empty BaseDir, got %+v", st)
	}
}

func TestCheckInstalled_BecomesReadyAsFilesAppear(t *testing.T) {
	withBaseDir(t)
	mustCreate := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	mustCreate(filepath.Join(cfgpkg.SteamCmdDir, steamCmdBinaryName))
	if st := CheckInstalled(); !st.SteamCmdReady || st.Ready() {
		t.Fatalf("after SteamCMD only: %+v", st)
	}

	mustCreate(filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Binaries/Win64/ArkAscendedServer.exe"))
	if st := CheckInstalled(); !st.ServerBinaryReady || st.Ready() {
		t.Fatalf("after server binary: %+v", st)
	}

	if err := os.MkdirAll(filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if st := CheckInstalled(); !st.Ready() {
		t.Fatalf("expected Ready() once all three present: %+v", st)
	}
}
