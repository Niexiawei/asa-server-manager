//go:build linux

package runner

import (
	"path/filepath"
	"reflect"
	"strings"
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

// TestX11SocketPathParsing: DISPLAY 的解析必须只认本地形式，并且落到真实文件上。
func TestX11SocketPathParsing(t *testing.T) {
	tests := []struct {
		display string
		want    string
		why     string
	}{
		{"", "", "空值"},
		{"99999", "", "没有冒号，不是本地形式"},
		{":99999", "", "本地形式但 socket 文件不存在"},
		{":", "", "冒号后没有显示号"},
		{":abc", "", "显示号不是数字"},
		{"remote.host:0", "", "远程显示没有本地 socket"},
	}
	for _, tt := range tests {
		if got := x11SocketPath(tt.display); got != tt.want {
			t.Errorf("x11SocketPath(%q) = %q, want %q（%s）", tt.display, got, tt.want, tt.why)
		}
	}
}

// TestX11DisplayUsableRejectsDeadDisplay: 光有 DISPLAY 变量不算数 —— 实测
// DISPLAY=:99（无人监听）与完全不设一样失败。远程形式无法本地判断，放行。
func TestX11DisplayUsableRejectsDeadDisplay(t *testing.T) {
	if x11DisplayUsable(":99999") {
		t.Error("x11DisplayUsable(\":99999\") = true, want false — 没有这个 socket")
	}
	if !x11DisplayUsable("remote.host:0") {
		t.Error("x11DisplayUsable(remote) = false, want true — 远程显示交给调用方去试")
	}
}

// TestResolveDisplayIgnoresDeadDisplay: DISPLAY 指向一个不存在的显示时不能采用它，
// 必须继续往后找。这是「有变量 ≠ 有服务」那条实测结论的回归测试。
func TestResolveDisplayIgnoresDeadDisplay(t *testing.T) {
	t.Setenv("DISPLAY", ":99999")

	d, blocked := resolveDisplay(getConfig())
	for _, kv := range d.Env {
		if kv == "DISPLAY=:99999" {
			t.Errorf("resolveDisplay adopted a DISPLAY with no listener: %v", d.Env)
		}
	}
	// 这台机器上 xvfb-run / 现成 X 服务的有无决定 blocked 是不是空，两种都合法 ——
	// 断言的是「没把死的 DISPLAY 当成活的」，以及成功时必然带着一种手段。
	if blocked == "" && len(d.Env) == 0 && len(d.Wrapper) == 0 {
		t.Errorf("resolveDisplay reported success with neither Env nor Wrapper: %+v", d)
	}
}

// TestXvfbRunArgsOverrideBadDefaults: xvfb-run 的两个默认值必须被覆盖 ——
// `-e` 默认丢进 /dev/null（Xvfb 起不来时现场全无，而它仍会照常执行命令），
// `-f` 默认写 ./.Xauthority（也就是游戏工作目录）。
func TestXvfbRunArgsOverrideBadDefaults(t *testing.T) {
	args := xvfbRunArgs(Config{BaseDir: "/srv/asa"}, "/usr/bin/xvfb-run")

	if args[0] != "/usr/bin/xvfb-run" {
		t.Fatalf("args[0] = %q, want the resolved xvfb-run path", args[0])
	}
	joined := strings.Join(args, " ")
	for _, flag := range []string{"-a", "-e", "-f"} {
		if !strings.Contains(joined, " "+flag+" ") && !strings.HasSuffix(joined, " "+flag) {
			t.Errorf("xvfbRunArgs is missing %s: %v", flag, args)
		}
	}
	for i, a := range args {
		if (a == "-e" || a == "-f") && (i+1 >= len(args) || !filepath.IsAbs(args[i+1])) {
			t.Errorf("%s must be followed by an absolute path, got %v", a, args)
		}
	}
	if strings.Contains(joined, "/dev/null") {
		t.Error("xvfbRunArgs still sends Xvfb's output to /dev/null")
	}
}

// TestDisplayStatusMatchesResolve: 诊断视图与真正的启动判断必须是同一个答案，
// 否则 verify-arkapi 会报「✔ 显示就绪」而启动照样被拒。
func TestDisplayStatusMatchesResolve(t *testing.T) {
	_, blocked := resolveDisplay(getConfig())
	info := displayStatus()

	if info.Available != (blocked == "") {
		t.Errorf("DisplayStatus().Available = %v, but resolveDisplay blocked = %q", info.Available, blocked)
	}
	if info.Blocked != blocked {
		t.Errorf("DisplayStatus().Blocked = %q, want %q", info.Blocked, blocked)
	}
}

// TestDisplayProblemIsBlocking: 显示是阻断级依赖，不是建议级。没有它 ArkApi 与
// vc_redist 安装器都彻底没有第二条路可走（对比 posix-acl 有 chown 兜底，
// 见 docs/ACL_PERMISSION_HARDENING_PLAN.md §1）。
func TestDisplayProblemIsBlocking(t *testing.T) {
	p := checkDisplay()
	if p == nil {
		return // 这台机器能拿到显示，没有可断言的问题对象
	}
	if p.Warning {
		t.Error("checkDisplay returned an advisory; it must be a blocker")
	}
	if p.Name != "x11-display" || p.Fix == "" {
		t.Errorf("checkDisplay problem = %+v, want name \"x11-display\" and a non-empty Fix", p)
	}
}

// TestCheckDisplayAgreesWithResolve: preflight 与启动路径必须是同一个判断。
// 它们分家过一次 —— preflight 只看 xvfb-run 在不在，而 WSLg 上 xvfb-run 装了也没用
// （/tmp/.X11-unix 只读），于是自检通过、启动照样死。
func TestCheckDisplayAgreesWithResolve(t *testing.T) {
	_, blocked := resolveDisplay(getConfig())
	if got := checkDisplay(); (got == nil) != (blocked == "") {
		t.Errorf("checkDisplay() = %+v but resolveDisplay blocked = %q", got, blocked)
	}
}
