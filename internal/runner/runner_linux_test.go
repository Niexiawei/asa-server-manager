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

// The regression this guards is docs/UMU_PREFIX_INIT_TROUBLESHOOTING.md: a
// root login shell's DBUS_SESSION_BUS_ADDRESS leaked into the dropped child,
// pressure-vessel tried to bind /run/user/0/bus into the container, and bwrap
// killed the launch before Wine started.
func TestInheritedEnv_DropsSessionScopedVariables(t *testing.T) {
	dropped := map[string]string{
		"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/0/bus",
		"XDG_RUNTIME_DIR":          "/run/user/0",
		"SESSION_MANAGER":          "local/host:@/tmp/.ICE-unix/1234",
		"XAUTHORITY":               "/root/.Xauthority",
		"DISPLAY":                  ":0",
		"WAYLAND_DISPLAY":          "wayland-0",
		"PULSE_SERVER":             "unix:/run/user/0/pulse/native",
		"SSH_AUTH_SOCK":            "/tmp/ssh-abc/agent.1",
		"JOURNAL_STREAM":           "8:12345",
	}
	kept := map[string]string{
		"PATH":        "/usr/bin",
		"LANG":        "C.UTF-8",
		"LC_ALL":      "C.UTF-8",
		"HTTPS_PROXY": "http://127.0.0.1:8080",
		"no_proxy":    "localhost",
		"UMU_LOG":     "debug",
		"PROTON_LOG":  "1",
		"WINEDEBUG":   "-all",
		"HOME":        "/root", // rewritten later by runtimeEnv, but must survive
	}
	for k, v := range dropped {
		t.Setenv(k, v)
	}
	for k, v := range kept {
		t.Setenv(k, v)
	}

	got := map[string]bool{}
	for _, kv := range inheritedEnv() {
		k, _, _ := strings.Cut(kv, "=")
		got[k] = true
	}

	for k := range dropped {
		if got[k] {
			t.Errorf("%s must not reach the launched process", k)
		}
	}
	for k := range kept {
		if !got[k] {
			t.Errorf("%s should have been passed through", k)
		}
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

func TestCheckRuntime_MessagesPointAtSetup(t *testing.T) {
	Configure(Config{Runtime: "umu", ProtonVersion: "GE-Proton10-34", BaseDir: t.TempDir()})
	t.Cleanup(func() { Configure(defaultConfig()) })

	err := CheckRuntime()
	if err == nil || !strings.Contains(err.Error(), "asa-server setup") {
		t.Fatalf("want a 'run asa-server setup' hint, got %v", err)
	}
}
