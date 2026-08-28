package actions

import (
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/runner"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withEmptyBaseDir(t *testing.T) {
	t.Helper()
	base := t.TempDir()
	oldSteam, oldServer := cfgpkg.SteamCmdDir, cfgpkg.ServerFilesDir
	cfgpkg.SteamCmdDir = filepath.Join(base, "steamcmd")
	cfgpkg.ServerFilesDir = filepath.Join(base, "server-files")
	t.Cleanup(func() {
		cfgpkg.SteamCmdDir, cfgpkg.ServerFilesDir = oldSteam, oldServer
	})
}

func TestVerifyEnvironmentReady_ReportsMissingAndPointsAtSetup(t *testing.T) {
	withEmptyBaseDir(t)

	err := VerifyEnvironmentReady()
	if err == nil {
		t.Fatal("expected an error when nothing is installed")
	}
	msg := err.Error()
	for _, want := range []string{"SteamCMD 未安装", "ARK 服务端本体未安装", "asa-server setup"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q:\n%s", want, msg)
		}
	}
}

func TestVerifyEnvironmentReady_NilWhenEverythingPresent(t *testing.T) {
	if err := runner.CheckRuntime(); err != nil {
		t.Skipf("runtime not ready on this host (%v); the all-present path needs it too", err)
	}
	withEmptyBaseDir(t)

	mk := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	// steamCmdBinaryName is unexported in installer; both platform values end
	// in a name we can reconstruct from the OS. Simplest: create both.
	mk(filepath.Join(cfgpkg.SteamCmdDir, "steamcmd.exe"))
	mk(filepath.Join(cfgpkg.SteamCmdDir, "steamcmd.sh"))
	mk(filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Binaries/Win64/ArkAscendedServer.exe"))
	if err := os.MkdirAll(filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := VerifyEnvironmentReady(); err != nil {
		t.Fatalf("expected nil once everything is present, got: %v", err)
	}
}
