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

// PROTON_VERB=run is what makes concurrent instances possible at all. umu's
// default verb, waitforexitandrun, runs `wineserver -w` before exec'ing the
// game; with a shared prefix that means instance B parks forever waiting for
// instance A to exit and its game process is never launched — observed on real
// hardware 2026-08-30 as "游戏进程在 3m0s 内没有出现". The reference script has
// always set it (start_server(), L884).
// See docs/UMU_PREFIX_PER_INSTANCE_PLAN.md.
func TestUmuCommandLine_PinsProtonVerbToRun(t *testing.T) {
	base := t.TempDir()
	Configure(Config{
		Runtime:       "umu",
		ProtonVersion: "GE-Proton10-34",
		PrefixMode:    "shared",
		GameID:        "umu-default",
		BaseDir:       base,
	})
	t.Cleanup(func() { Configure(defaultConfig()) })

	mustWrite := func(p string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), mode); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	mustWrite(filepath.Join(base, "umu-launcher", "umu-run"), 0o755)
	mustWrite(filepath.Join(base, "proton", "GE-Proton10-34", "proton"), 0o644)
	mustWrite(filepath.Join(base, "umu-prefix", "system.reg"), 0o644)

	// An operator (or a stale shell) exporting the broken value must not win:
	// launchEnvAllowed lets PROTON_* through, and exec keeps the LAST
	// occurrence of a key, so ours has to be appended after the inherited one.
	t.Setenv("PROTON_VERB", "waitforexitandrun")

	_, _, env, err := umuCommandLine(filepath.Join(base, "ArkAscendedServer.exe"), nil, Options{})
	if err != nil {
		t.Skipf("umuCommandLine unavailable in this environment: %v", err)
	}

	last := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PROTON_VERB="); ok {
			last = v
		}
	}
	if last != "run" {
		t.Fatalf("effective PROTON_VERB = %q, want \"run\" (waitforexitandrun deadlocks the 2nd instance on a shared prefix)", last)
	}
}

// TestUmuCommandLine_DisablesXalia: 无障碍覆盖层在服务端上没有用处，而在没有显示的
// 启动里（普通实例就是这样）它每次必崩一次，往 launcher.log 里塞一段看着像故障的
// .NET 栈。同样要能压过外面导出的值 —— launchEnvAllowed 放行 PROTON_*，
// 所以我们这一份必须排在继承来的那一份后面。见 protonNoXalia。
func TestUmuCommandLine_DisablesXalia(t *testing.T) {
	base := t.TempDir()
	Configure(Config{
		Runtime:       "umu",
		ProtonVersion: "GE-Proton10-34",
		PrefixMode:    "shared",
		GameID:        "umu-default",
		BaseDir:       base,
	})
	t.Cleanup(func() { Configure(defaultConfig()) })

	for _, p := range []string{
		filepath.Join(base, "umu-launcher", "umu-run"),
		filepath.Join(base, "proton", "GE-Proton10-34", "proton"),
		filepath.Join(base, "umu-prefix", "system.reg"),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	t.Setenv("PROTON_USE_XALIA", "1")

	_, _, env, err := umuCommandLine(filepath.Join(base, "ArkAscendedServer.exe"), nil, Options{})
	if err != nil {
		t.Skipf("umuCommandLine unavailable in this environment: %v", err)
	}

	last := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PROTON_USE_XALIA="); ok {
			last = v
		}
	}
	if last != "0" {
		t.Fatalf("effective PROTON_USE_XALIA = %q, want \"0\" —— 服务端不需要无障碍覆盖层，"+
			"而没有显示时它必崩并留下误导性的栈", last)
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
