//go:build linux

package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRuntime_ReportsEachMissingPieceThenPasses(t *testing.T) {
	base := t.TempDir()
	Configure(Config{
		Runtime:       "umu",
		ProtonVersion: "GE-Proton10-34",
		PrefixMode:    "shared",
		GameID:        "umu-default",
		BaseDir:       base,
	})
	t.Cleanup(func() {
		def := defaultConfig()
		Configure(def)
	})

	mustWrite := func(p string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), mode); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	// 1. nothing present -> complains about umu-run
	if err := CheckRuntime(); err == nil || !strings.Contains(err.Error(), "umu-run") {
		t.Fatalf("want umu-run error, got %v", err)
	}

	// 2. umu-run present (executable) -> complains about GE-Proton
	mustWrite(filepath.Join(base, "umu-launcher", "umu-run"), 0o755)
	if err := CheckRuntime(); err == nil || !strings.Contains(err.Error(), "GE-Proton10-34") {
		t.Fatalf("want GE-Proton error, got %v", err)
	}

	// 3. proton present -> complains about the Wine prefix
	mustWrite(filepath.Join(base, "proton", "GE-Proton10-34", "proton"), 0o644)
	if err := CheckRuntime(); err == nil || !strings.Contains(err.Error(), "前缀") {
		t.Fatalf("want Wine prefix error, got %v", err)
	}

	// 4. prefix present -> passes
	mustWrite(filepath.Join(base, "umu-prefix", "system.reg"), 0o644)
	if err := CheckRuntime(); err != nil {
		t.Fatalf("want nil once all three present, got %v", err)
	}
}

func TestCheckRuntime_MessagesPointAtSetup(t *testing.T) {
	Configure(Config{Runtime: "umu", ProtonVersion: "GE-Proton10-34", BaseDir: t.TempDir()})
	t.Cleanup(func() { Configure(defaultConfig()) })

	err := CheckRuntime()
	if err == nil || !strings.Contains(err.Error(), "asa-server setup") {
		t.Fatalf("want a 'run asa-server setup' hint, got %v", err)
	}
}
