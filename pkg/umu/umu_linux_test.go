//go:build linux

package umu

import (
	"strings"
	"testing"
)

// 这三种前缀是同一个名字词干下的兄弟目录，所以「谁持有哪个前缀」只能按路径边界
// 比，不能按字符串前缀比。表里前两行是真实场景（umu 导出的是 "<prefix>/pfx/"），
// 后面几行是曾经会误判成 true 的情形。见 docs/UMU_PREFIX_OVERLAY_PLAN.md §12.2。
func TestPrefixValueUnder(t *testing.T) {
	const (
		shared   = "/opt/asa/umu-prefix"
		perInst  = "/opt/asa/umu-prefix-jibian"
		overlayA = "/opt/asa/umu-prefix-overlay/A/merged"
	)

	cases := []struct {
		name   string
		value  string // 某个活着的 wineserver 的 WINEPREFIX
		prefix string // 在问的那个前缀目录
		want   bool
	}{
		{"共享前缀自己在跑", shared + "/pfx/", shared, true},
		{"每实例前缀自己在跑", perInst + "/pfx/", perInst, true},
		{"值就是前缀目录本身", shared, shared, true},
		{"前缀带尾斜杠", shared + "/pfx/", shared + "/", true},

		{"每实例前缀不算共享前缀在跑", perInst + "/pfx/", shared, false},
		{"overlay 挂载点不算底层在跑", overlayA + "/pfx/", shared, false},
		{"名字是另一个前缀的字符串前缀", "/opt/asa/umu-prefix-AB/pfx/", "/opt/asa/umu-prefix-A", false},
		{"反过来也不成立", shared + "/pfx/", perInst, false},

		{"空值", "", shared, false},
		{"空前缀由调用方处理，这里一律不算", shared + "/pfx/", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PrefixValueUnder(c.value, c.prefix); got != c.want {
				t.Errorf("PrefixValueUnder(%q, %q) = %v, want %v", c.value, c.prefix, got, c.want)
			}
		})
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
		"HOME":        "/root", // rewritten later by RuntimeEnv, but must survive
	}
	for k, v := range dropped {
		t.Setenv(k, v)
	}
	for k, v := range kept {
		t.Setenv(k, v)
	}

	got := map[string]bool{}
	for _, kv := range InheritedEnv() {
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

func TestRuntimeEnv_RewritesHomeAndStripsXDG(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"HOME=/root",
		"USER=root",
		"LOGNAME=root",
		"XDG_RUNTIME_DIR=/run/user/0",
		"XDG_CACHE_HOME=/root/.cache",
		"WINEPREFIX=/x",
	}
	got := RuntimeEnv(base, "/var/lib/asa-umu-runtime", "asa-umu-runtime")

	for _, kv := range got {
		if strings.HasPrefix(kv, "XDG_") {
			t.Errorf("XDG_ var survived: %q", kv)
		}
		if kv == "HOME=/root" || kv == "USER=root" || kv == "LOGNAME=root" {
			t.Errorf("root identity var survived: %q", kv)
		}
	}
	for _, want := range []string{
		"HOME=/var/lib/asa-umu-runtime",
		"USER=asa-umu-runtime",
		"LOGNAME=asa-umu-runtime",
		"PATH=/usr/bin",
		"WINEPREFIX=/x",
	} {
		found := false
		for _, kv := range got {
			if kv == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %q in %v", want, got)
		}
	}
}

func TestRuntimeEnv_NoHomeIsPassthrough(t *testing.T) {
	base := []string{"HOME=/root", "PATH=/usr/bin"}
	got := RuntimeEnv(base, "", "x")
	if len(got) != len(base) {
		t.Fatalf("empty home should pass env through unchanged: got %v", got)
	}
	for i := range base {
		if got[i] != base[i] {
			t.Fatalf("empty home should pass env through unchanged: got %v", got)
		}
	}
}

func TestPrefixMarker_RoundTrips(t *testing.T) {
	prefix := t.TempDir()

	if got := PrefixMarker(prefix); got != "" {
		t.Errorf("标记不存在时应返回空串，got %q", got)
	}
	noopChown := func(string) error { return nil }
	if err := writePrefixMarker(prefix, "GE-Proton10-34", noopChown); err != nil {
		t.Fatalf("writePrefixMarker: %v", err)
	}
	if got := PrefixMarker(prefix); got != "GE-Proton10-34" {
		t.Errorf("PrefixMarker = %q, want GE-Proton10-34", got)
	}
}
