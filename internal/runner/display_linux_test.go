//go:build linux

package runner

import (
	"errors"
	"strings"
	"testing"

	"asa-server/pkg/umuruntime"
	"asa-server/pkg/umuruntime/plugins/xdisplay"
)

// 候选链本身的用例在 asa-server/pkg/umuruntime/plugins/xdisplay（Plan 顺序、blocked 文案、Target 的
// 追加语义等）。这里只剩「组合根把它接对了没有」以及 preflight 那一侧的断言 ——
// checkDisplay 属于 internal/runner，xdisplay 不认识本程序的文案。

// TestDisplayStatusStartsNothing: 诊断视图不许有副作用。自管那一档的「拿到显示」
// 意味着真的 fork 一个 X 服务端，被 GET /api/system/preflight 问一句就起一个是不行的。
//
// xdisplay 里有同名不变量的用例；这一条是**接线**的版本：它走的是真正被 API
// 调用的那两个入口（displayStatus/checkDisplay）和进程唯一的那个 displayRes。
func TestDisplayStatusStartsNothing(t *testing.T) {
	before := displayFor(getConfig()).XvfbStatus()
	_ = displayStatus()
	_ = checkDisplay()
	if displayFor(getConfig()).XvfbStatus() != before {
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

// TestDisplayProblemDetailCarriesRealReason: Detail 必须带上候选链算出来的那句原因。
// 以前它是一句写死的话，于是一台**装好了** Xvfb、只是 /tmp/.X11-unix 权限不对的
// 机器，得到的唯一指引是「请安装 Xvfb」——判断得对却说不清，等于没判断。
func TestDisplayProblemDetailCarriesRealReason(t *testing.T) {
	_, blocked := planDisplay()
	p := checkDisplay()
	if p == nil {
		return
	}
	if !strings.Contains(p.Detail, blocked) {
		t.Errorf("checkDisplay Detail = %q, want it to contain the resolver reason %q", p.Detail, blocked)
	}
}

// TestCheckDisplayAgreesWithPlan: preflight 与启动路径必须是同一个判断。
// 它们分家过一次 —— preflight 只看 xvfb-run 在不在，而 WSLg 上 xvfb-run 装了也没用
// （/tmp/.X11-unix 只读），于是自检通过、启动照样死。
func TestCheckDisplayAgreesWithPlan(t *testing.T) {
	_, blocked := planDisplay()
	if got := checkDisplay(); (got == nil) != (blocked == "") {
		t.Errorf("checkDisplay() = %+v but the resolver blocked = %q", got, blocked)
	}
}

// TestDisplayStatusMatchesResolver: 组合根不许在转发的路上把答案改掉 ——
// runner.DisplayStatus() 就是 xdisplay 的 Status()，一个字段都不加工。
func TestDisplayStatusMatchesResolver(t *testing.T) {
	if got, want := displayStatus(), displayFor(getConfig()).Status(); got.Available != want.Available ||
		got.Blocked != want.Blocked || got.How != want.How {
		t.Errorf("displayStatus() = %+v, resolver said %+v", got, want)
	}
}

// TestDisplayPluginRegistered: 组合根把显示插件挂上了宿主，并且只挂了一份——
// 「进程内只有一个自管显示」靠的就是这一份。
func TestDisplayPluginRegistered(t *testing.T) {
	var found int
	for _, st := range hostFor(getConfig()).Status() {
		if st.Name == xdisplay.Name {
			found++
			if len(st.Provides) != 1 || st.Provides[0] != umuruntime.CapGUI {
				t.Errorf("xdisplay provides %v, want [%s]", st.Provides, umuruntime.CapGUI)
			}
		}
	}
	if found != 1 {
		t.Fatalf("xdisplay registered %d times, want exactly once", found)
	}
	if r, ok := runtimeHost.Graph().Lookup(xdisplay.Name); !ok || r.Plugin != umuruntime.Plugin(displayRes) {
		t.Error("the registered display plugin is not displayRes — a second resolver would mean a second Xvfb manager")
	}
}

func TestLaunchNeeds(t *testing.T) {
	if got := launchNeeds(Options{}); len(got) != 0 {
		t.Errorf("launchNeeds(no display) = %v, want none", got)
	}
	if got := launchNeeds(Options{NeedsDisplay: true}); len(got) != 1 || got[0] != umuruntime.CapGUI {
		t.Errorf("launchNeeds(NeedsDisplay) = %v, want [%s]", got, umuruntime.CapGUI)
	}
}

// TestDescribeLaunchErrorKeepsWording: 插件化之后，两种「拿不到显示」给用户的话必须与
// 之前逐字相同——「这台机器不行」要说原因并解释为什么提前拒绝，「这次没成功」要带上
// 原始错误（里面有 xvfb.log 的现场）。
func TestDescribeLaunchErrorKeepsWording(t *testing.T) {
	exe := "/srv/ShooterGame/Binaries/Win64/AsaApiLoader.exe"

	got := describeLaunchError(exe, &umuruntime.CapabilityUnavailableError{Cap: umuruntime.CapGUI, Why: "本机没有可用的图形显示"})
	if want := "无法启动 AsaApiLoader.exe：它需要图形显示，但本机没有可用的图形显示。" +
		"AsaApiLoader.exe（ArkApi）在 Wine 下没有显示会静默退出，" +
		"所以这里提前拒绝，而不是让实例假装启动成功"; got.Error() != want {
		t.Errorf("unavailable:\n got %q\nwant %q", got, want)
	}

	boom := errors.New("Xvfb 启动失败：缺字体")
	got = describeLaunchError(exe, &umuruntime.AcquireError{Cap: umuruntime.CapGUI, Plugin: xdisplay.Name, Err: boom})
	if want := "无法启动 AsaApiLoader.exe：拿不到图形显示。Xvfb 启动失败：缺字体"; got.Error() != want || !errors.Is(got, boom) {
		t.Errorf("acquire failure:\n got %q\nwant %q (wrapping the cause)", got, want)
	}

	other := errors.New("别的错误")
	if got := describeLaunchError(exe, other); got != other {
		t.Errorf("unrelated error rewritten: %v", got)
	}
}
