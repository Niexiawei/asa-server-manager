//go:build linux

package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestDisplayWrapEnvOnly: 拿到宿主 DISPLAY 时只加环境变量，命令本身不动。
func TestDisplayWrapEnvOnly(t *testing.T) {
	d := displayTarget{Env: []string{"DISPLAY=:0"}}
	bin, argv, env := d.wrap("/usr/bin/python3", []string{"umu-run", "game.exe"}, []string{"PATH=/bin"})

	if bin != "/usr/bin/python3" {
		t.Errorf("bin = %q, want the original binary", bin)
	}
	if want := []string{"umu-run", "game.exe"}; !reflect.DeepEqual(argv, want) {
		t.Errorf("argv = %v, want %v", argv, want)
	}
	if want := []string{"PATH=/bin", "DISPLAY=:0"}; !reflect.DeepEqual(env, want) {
		t.Errorf("env = %v, want DISPLAY appended last", env)
	}
}

// TestDisplayWrapXvfb: xvfb-run 变成新的 bin，原 bin 降为它的第一个参数。
// 顺序错了会变成「用 python3 跑 -a」这种从现象几乎反推不出来的失败。
func TestDisplayWrapXvfb(t *testing.T) {
	d := displayTarget{Wrapper: []string{"/usr/bin/xvfb-run", "-a"}}
	bin, argv, env := d.wrap("/usr/bin/python3", []string{"umu-run", "game.exe"}, []string{"PATH=/bin"})

	if bin != "/usr/bin/xvfb-run" {
		t.Errorf("bin = %q, want the xvfb-run wrapper", bin)
	}
	want := []string{"-a", "/usr/bin/python3", "umu-run", "game.exe"}
	if !reflect.DeepEqual(argv, want) {
		t.Errorf("argv = %v, want %v", argv, want)
	}
	if got := []string{"PATH=/bin"}; !reflect.DeepEqual(env, got) {
		t.Errorf("env = %v, want it untouched when the target carries no Env", env)
	}
}

// TestDisplayWrapDoesNotAliasCaller: wrap 必须返回新切片。就地 append 会让
// 「解析一次显示、拿它包两条命令」悄悄污染第一条 —— runInPrefix 正是这么用的。
func TestDisplayWrapDoesNotAliasCaller(t *testing.T) {
	argv := make([]string, 1, 8) // 有富余容量，就地 append 不会重新分配
	argv[0] = "umu-run"
	env := make([]string, 1, 8)
	env[0] = "PATH=/bin"

	d := displayTarget{Env: []string{"DISPLAY=:0"}, Wrapper: []string{"/usr/bin/xvfb-run", "-a"}}
	_, _, _ = d.wrap("/usr/bin/python3", argv, env)

	if len(argv) != 1 || argv[0] != "umu-run" {
		t.Errorf("caller argv was mutated: %v", argv)
	}
	if len(env) != 1 || env[0] != "PATH=/bin" {
		t.Errorf("caller env was mutated: %v", env)
	}
}

// TestDisplayWrapZeroValue: 空 displayTarget 是恒等变换，调用方不必先判空。
func TestDisplayWrapZeroValue(t *testing.T) {
	bin, argv, env := displayTarget{}.wrap("bin", []string{"a"}, []string{"K=V"})
	if bin != "bin" || !reflect.DeepEqual(argv, []string{"a"}) || !reflect.DeepEqual(env, []string{"K=V"}) {
		t.Errorf("zero displayTarget changed the command: %q %v %v", bin, argv, env)
	}
}

// TestX11SocketExists: 光有 DISPLAY 变量不算数 —— 实测 DISPLAY=:99（无人监听）
// 与完全不设一样失败，所以判据必须落到 socket 上。
func TestX11SocketExists(t *testing.T) {
	// 借 /tmp/.X11-unix 的真实路径没法在测试里造，改为只验证解析逻辑对
	// 「不可能存在的显示号」的判断，以及非本地形式的放行。
	tests := []struct {
		display string
		want    bool
		why     string
	}{
		{"", false, "空值"},
		{"99999", false, "没有冒号，不是合法 DISPLAY"},
		{":99999", false, "本地形式但 socket 不存在"},
		{":", false, "冒号后没有显示号"},
		{"remote.host:0", true, "远程显示无法本地判断，放行让调用方去试"},
		{"/tmp/some.socket:0", true, "抽象/路径形式同样放行"},
	}
	for _, tt := range tests {
		if got := x11SocketExists(tt.display); got != tt.want {
			t.Errorf("x11SocketExists(%q) = %v, want %v（%s）", tt.display, got, tt.want, tt.why)
		}
	}
}

// TestResolveDisplayPrefersHostDisplay: DISPLAY 指向的 socket 真的在时优先用它，
// 不去多起一个 Xvfb。用一个假的 /tmp/.X11-unix 条目是做不到的（路径是硬编码的），
// 所以这里只钉「变量存在但 socket 不在 → 不采用」这一半。
func TestResolveDisplayIgnoresDeadDisplay(t *testing.T) {
	t.Setenv("DISPLAY", ":99999")

	d, blocked := resolveDisplay()
	if len(d.Env) > 0 {
		t.Errorf("resolveDisplay adopted a DISPLAY with no listener: %v", d.Env)
	}
	// 这台机器上有没有 xvfb-run 决定了 blocked 是不是空，两种都合法 ——
	// 断言的是「没把死的 DISPLAY 当成活的」。
	if blocked == "" && len(d.Wrapper) == 0 {
		t.Errorf("resolveDisplay reported success with neither Env nor Wrapper: %+v", d)
	}
}

// TestDisplayStatusMatchesResolve: 诊断视图与真正的启动判断必须是同一个答案，
// 否则 verify-arkapi 会报「✔ 显示就绪」而启动照样被拒。
func TestDisplayStatusMatchesResolve(t *testing.T) {
	_, blocked := resolveDisplay()
	info := displayStatus()

	if info.Available != (blocked == "") {
		t.Errorf("DisplayStatus().Available = %v, but resolveDisplay blocked = %q", info.Available, blocked)
	}
	if info.Blocked != blocked {
		t.Errorf("DisplayStatus().Blocked = %q, want %q", info.Blocked, blocked)
	}
}

// TestXvfbProblemIsBlocking: xvfb 是阻断级依赖，不是建议级。没有它 ArkApi 与
// vc_redist 安装器都彻底没有第二条路可走（对比 posix-acl 有 chown 兜底，
// 见 docs/ACL_PERMISSION_HARDENING_PLAN.md §1）。
func TestXvfbProblemIsBlocking(t *testing.T) {
	p := checkXvfb()
	if p == nil {
		// 这台机器装了 xvfb-run，检查通过 —— 没有可断言的问题对象。
		if _, err := os.Stat(filepath.Join("/usr/bin", "xvfb-run")); err != nil {
			t.Log("checkXvfb passed; xvfb-run resolved from PATH outside /usr/bin")
		}
		return
	}
	if p.Warning {
		t.Error("checkXvfb returned an advisory; it must be a blocker")
	}
	if p.Name != "xvfb" || p.Fix == "" {
		t.Errorf("checkXvfb problem = %+v, want name \"xvfb\" and a non-empty Fix", p)
	}
}
