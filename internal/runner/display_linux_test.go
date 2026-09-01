//go:build linux

package runner

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestDisplayApplyToAppendsLast: 显示以环境变量的形式追加，且必须排在最后 ——
// exec 取同名变量的最后一个，排最后就不会被将来新增的过滤逻辑吃掉。
func TestDisplayApplyToAppendsLast(t *testing.T) {
	d := displayTarget{Env: []string{"DISPLAY=:0"}}
	env := d.applyTo([]string{"PATH=/bin", "HOME=/root"})

	if want := []string{"PATH=/bin", "HOME=/root", "DISPLAY=:0"}; !reflect.DeepEqual(env, want) {
		t.Errorf("applyTo = %v, want %v", env, want)
	}
}

// TestDisplayApplyToDoesNotAliasCaller: applyTo 必须返回新切片。就地 append 会让
// 「解析一次显示、拿它包两条命令」悄悄污染第一条 —— runInPrefix 正是这么用的。
func TestDisplayApplyToDoesNotAliasCaller(t *testing.T) {
	env := make([]string, 1, 8) // 有富余容量，就地 append 不会重新分配
	env[0] = "PATH=/bin"

	d := displayTarget{Env: []string{"DISPLAY=:0"}}
	got := d.applyTo(env)

	if len(env) != 1 || env[0] != "PATH=/bin" {
		t.Errorf("caller env was mutated: %v", env)
	}
	if len(got) != 2 {
		t.Errorf("applyTo = %v, want the caller's env plus DISPLAY", got)
	}
}

// TestDisplayApplyToZeroValue: 空 displayTarget 是恒等变换，调用方不必先判空。
func TestDisplayApplyToZeroValue(t *testing.T) {
	if got := (displayTarget{}).applyTo([]string{"K=V"}); !reflect.DeepEqual(got, []string{"K=V"}) {
		t.Errorf("zero displayTarget changed the env: %v", got)
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

// TestPlanDisplayIgnoresDeadDisplay: DISPLAY 指向一个不存在的显示时不能采用它，
// 必须继续往后找。这是「有变量 ≠ 有服务」那条实测结论的回归测试。
func TestPlanDisplayIgnoresDeadDisplay(t *testing.T) {
	t.Setenv("DISPLAY", ":99999")

	p, blocked := planDisplay(getConfig())
	if p.Kind == displayEnv {
		t.Errorf("planDisplay adopted a DISPLAY with no listener: %+v", p)
	}
	// 这台机器上 Xvfb / 现成 X 服务的有无决定 blocked 是不是空，两种都合法 ——
	// 断言的是「没把死的 DISPLAY 当成活的」，以及成功时必然指明了一种手段。
	if blocked == "" && p.Kind == displayNone {
		t.Errorf("planDisplay reported success with no kind: %+v", p)
	}
	if blocked == "" && p.How == "" {
		t.Errorf("planDisplay reported success with no explanation: %+v", p)
	}
}

// TestPlanDisplayPrefersConfiguredOverEnv: linux.display 优先于环境变量。
// 两者都指向死显示时应该一起被跳过 —— 这里断言的是「配置被读到了」。
func TestPlanDisplayPrefersConfiguredOverEnv(t *testing.T) {
	t.Setenv("DISPLAY", ":99998")

	d, src := configuredDisplay(Config{Display: ":99997"})
	if d != ":99997" || src != "配置指定" {
		t.Errorf("configuredDisplay = (%q, %q), want the configured value to win", d, src)
	}
	if d, src := configuredDisplay(Config{}); d != ":99998" || src != "宿主" {
		t.Errorf("configuredDisplay = (%q, %q), want the DISPLAY env var as fallback", d, src)
	}
}

// TestDisplayStatusMatchesPlan: 诊断视图与真正的启动判断必须是同一个答案，
// 否则 verify-arkapi 会报「✔ 显示就绪」而启动照样被拒。
func TestDisplayStatusMatchesPlan(t *testing.T) {
	_, blocked := planDisplay(getConfig())
	info := displayStatus()

	if info.Available != (blocked == "") {
		t.Errorf("DisplayStatus().Available = %v, but planDisplay blocked = %q", info.Available, blocked)
	}
	if info.Blocked != blocked {
		t.Errorf("DisplayStatus().Blocked = %q, want %q", info.Blocked, blocked)
	}
}

// TestDisplayStatusStartsNothing: 诊断视图不许有副作用。自管那一档的「拿到显示」
// 意味着真的 fork 一个 X 服务端，被 GET /api/system/preflight 问一句就起一个是不行的。
func TestDisplayStatusStartsNothing(t *testing.T) {
	before := currentManagedXvfb()
	_ = displayStatus()
	_ = checkDisplay()
	if currentManagedXvfb() != before {
		t.Error("displayStatus/checkDisplay started an X server as a side effect")
	}
}

// TestDisplayProblemIsAdvisory: 显示是**建议级**，不是阻断级。它只对 ArkApi 实例
// 是硬依赖，而 ArkApi 是每实例可选的；ArkAscendedServer.exe 本身不需要显示。
// 曾经把它做成阻断级，结果一台永远用不到 ArkApi 的无头机连 setup 都跑不完
// （2026-08-31 AlmaLinux 真机，见 docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md §11）。
func TestDisplayProblemIsAdvisory(t *testing.T) {
	p := checkDisplay()
	if p == nil {
		return // 这台机器能拿到显示，没有可断言的问题对象
	}
	if !p.Warning {
		t.Error("checkDisplay returned a blocker; it must be an advisory — 缺显示只影响 ArkApi，不该拦住安装")
	}
	if p.Name != "x11-display" || p.Fix == "" {
		t.Errorf("checkDisplay problem = %+v, want name \"x11-display\" and a non-empty Fix", p)
	}
}

// TestDisplayProblemDetailCarriesRealReason: Detail 必须带上 planDisplay 算出来的
// 那句原因。以前它是一句写死的话，于是一台**装好了** Xvfb、只是 /tmp/.X11-unix
// 权限不对的机器，得到的唯一指引是「请安装 Xvfb」——判断得对却说不清，等于没判断。
func TestDisplayProblemDetailCarriesRealReason(t *testing.T) {
	_, blocked := planDisplay(getConfig())
	p := checkDisplay()
	if p == nil {
		return
	}
	if !strings.Contains(p.Detail, blocked) {
		t.Errorf("checkDisplay Detail = %q, want it to contain planDisplay's reason %q", p.Detail, blocked)
	}
}

// TestXvfbUnavailableReasonDistinguishesCauses: 「没装 Xvfb」「xvfb_bin 指错了」
// 「socket 目录不可写」是三件不同的事，指引也不同。只有第一种才该让用户去装包。
func TestXvfbUnavailableReasonDistinguishesCauses(t *testing.T) {
	if got := xvfbUnavailableReason(errNoXvfb, ""); !strings.Contains(got, "sudo apt install xvfb") {
		t.Errorf("errNoXvfb → %q, want the install hint", got)
	}
	badBin := errors.New("linux.xvfb_bin 指向的 /nope/Xvfb 不存在或不可执行")
	if got := xvfbUnavailableReason(badBin, ""); !strings.Contains(got, "/nope/Xvfb") {
		t.Errorf("bad xvfb_bin → %q, want the original error text", got)
	}
	if got := xvfbUnavailableReason(badBin, ""); strings.Contains(got, "sudo apt install") {
		t.Errorf("bad xvfb_bin → %q, must NOT tell the user to install a package they already have", got)
	}
	dirWhy := "/tmp/.X11-unix 不可写（当前权限 0755）"
	if got := xvfbUnavailableReason(nil, dirWhy); got != dirWhy {
		t.Errorf("dir problem → %q, want %q verbatim", got, dirWhy)
	}
	if got := xvfbUnavailableReason(nil, dirWhy); strings.Contains(got, "sudo apt install") {
		t.Errorf("dir problem → %q, must NOT be reported as a missing package", got)
	}
}

// TestX11SocketDirWritableExplainsRefusal: 拒绝时必须给出可执行的原因，
// 而且不能再拿 o+w 当判据 —— 那条判据会把「属主是运行时用户的 0755 目录」判成
// 不可写，而目录恰恰是上一轮那个降权 Xvfb 自己建的（第一次成功毒死后续每一次）。
func TestX11SocketDirWritableExplainsRefusal(t *testing.T) {
	ok, why := x11SocketDirWritable()
	if ok != (why == "") {
		t.Errorf("x11SocketDirWritable = (%v, %q); 拒绝必须带原因，通过必须不带", ok, why)
	}
	if !ok && !strings.Contains(why, x11SocketDir) && !strings.Contains(why, "/tmp") {
		t.Errorf("refusal reason %q names neither %s nor /tmp", why, x11SocketDir)
	}
}

// TestCheckDisplayAgreesWithPlan: preflight 与启动路径必须是同一个判断。
// 它们分家过一次 —— preflight 只看 xvfb-run 在不在，而 WSLg 上 xvfb-run 装了也没用
// （/tmp/.X11-unix 只读），于是自检通过、启动照样死。
func TestCheckDisplayAgreesWithPlan(t *testing.T) {
	_, blocked := planDisplay(getConfig())
	if got := checkDisplay(); (got == nil) != (blocked == "") {
		t.Errorf("checkDisplay() = %+v but planDisplay blocked = %q", got, blocked)
	}
}

// TestDisplayBlockedMessageNamesXvfbNotXvfbRun: 拿不到显示时给的提示必须指向
// **Xvfb**。指向 xvfb-run 会把 Fedora/RHEL/Arch 用户带到一个他们装不上的包
// （那是 Debian 的脚本），正是这次改动要修的错。
func TestDisplayBlockedMessageNamesXvfbNotXvfbRun(t *testing.T) {
	_, blocked := planDisplay(getConfig())
	if blocked == "" {
		return // 这台机器有显示，没有可断言的文案
	}
	if strings.Contains(blocked, "xvfb-run") {
		t.Errorf("blocked message still points at xvfb-run: %q", blocked)
	}
}
