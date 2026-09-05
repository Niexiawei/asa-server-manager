//go:build linux

package umu

import (
	"slices"
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

// hasEnv 报告 env 里有没有某个 KEY=（不比较值）。
func hasEnv(env []string, key string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") {
			return true
		}
	}
	return false
}

// TestRunEnv_ZeroOptionsIsWarmPrefixShape: WarmPrefix 传的是零值 RunOptions，
// 它**必须**既不带 UMU_RUNTIME_UPDATE 也不带 PROTON_VERB ——
//   - 前者：wineboot 那一次是唯一必须被允许去拉运行时的调用，关掉更新检查会让全新
//     机器上的第一次 setup 拿不到 Steam Linux Runtime；
//   - 后者：它要的正是 umu 默认的 waitforexitandrun。
//
// 这两条是 runInPrefix 下沉进本包时唯一有行为风险的地方（见
// docs/RUNNER_INSTANCE_PACKAGE_SPLIT_TODO.md §4），所以单独钉住。
func TestRunEnv_ZeroOptionsIsWarmPrefixShape(t *testing.T) {
	r := New(Config{GameID: "umu-0"})
	env := r.runEnv("/tmp/pfx", RunOptions{})

	if hasEnv(env, "UMU_RUNTIME_UPDATE") {
		t.Error("零值 RunOptions 带上了 UMU_RUNTIME_UPDATE，wineboot 将无法拉取缺失的运行时")
	}
	if hasEnv(env, "PROTON_VERB") {
		t.Error("零值 RunOptions 带上了 PROTON_VERB，wineboot 需要 umu 的默认 verb")
	}
	for _, want := range []string{"WINEPREFIX", "GAMEID", "PROTONPATH", "PROTON_USE_XALIA"} {
		if !hasEnv(env, want) {
			t.Errorf("runEnv 少了 %s：%v", want, env)
		}
	}
}

// TestRunEnv_VCRedistShape: vcredist 那两次必须两样都带 —— PROTON_VERB=run 尤其
// 关键，umu 默认的 waitforexitandrun 会先跑 `wineserver -w`，共享 prefix 上只要有
// 实例在跑就永不返回。
func TestRunEnv_VCRedistShape(t *testing.T) {
	r := New(Config{GameID: "umu-0"})
	env := r.runEnv("/tmp/pfx", RunOptions{NoRuntimeUpdate: true, Verb: "run"})

	if !slices.Contains(env, "UMU_RUNTIME_UPDATE=0") {
		t.Errorf("NoRuntimeUpdate 没生效：%v", env)
	}
	if !slices.Contains(env, "PROTON_VERB=run") {
		t.Errorf("Verb 没生效：%v", env)
	}
}

// TestRunEnv_DoesNotAliasInheritedEnv: runEnv 连着调两次，第二次不能看见第一次
// 追加的东西。InheritedEnv 若返回一个有富余容量的切片，就地 append 会让「解析一次
// 环境、跑两条命令」串味 —— vcredist 正是连着跑 regedit 与安装器两条。
func TestRunEnv_DoesNotAliasInheritedEnv(t *testing.T) {
	r := New(Config{GameID: "umu-0"})
	_ = r.runEnv("/tmp/a", RunOptions{NoRuntimeUpdate: true, Verb: "run"})
	second := r.runEnv("/tmp/b", RunOptions{})

	if hasEnv(second, "UMU_RUNTIME_UPDATE") || hasEnv(second, "PROTON_VERB") {
		t.Errorf("第二次 runEnv 看见了第一次追加的变量：%v", second)
	}
	if !slices.Contains(second, "WINEPREFIX=/tmp/b") {
		t.Errorf("第二次 runEnv 的 WINEPREFIX 不对：%v", second)
	}
}
