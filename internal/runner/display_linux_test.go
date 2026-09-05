//go:build linux

package runner

import (
	"strings"
	"testing"
)

// 候选链本身的用例在 asa-server/pkg/display（Plan 顺序、blocked 文案、Target 的
// 追加语义等）。这里只剩「组合根把它接对了没有」以及 preflight 那一侧的断言 ——
// checkDisplay 属于 internal/runner，pkg/display 不认识 Problem。

// TestDisplayStatusStartsNothing: 诊断视图不许有副作用。自管那一档的「拿到显示」
// 意味着真的 fork 一个 X 服务端，被 GET /api/system/preflight 问一句就起一个是不行的。
//
// pkg/display 里有同名不变量的用例；这一条是**接线**的版本：它走的是真正被 API
// 调用的那两个入口（displayStatus/checkDisplay）和进程唯一的那个 xvfbMgr。
func TestDisplayStatusStartsNothing(t *testing.T) {
	before := xvfbManager().Status()
	_ = displayStatus()
	_ = checkDisplay()
	if xvfbManager().Status() != before {
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
// runner.DisplayStatus() 就是 pkg/display 的 Status()，一个字段都不加工。
func TestDisplayStatusMatchesResolver(t *testing.T) {
	if got, want := displayStatus(), displayResolver().Status(); got.Available != want.Available ||
		got.Blocked != want.Blocked || got.How != want.How {
		t.Errorf("displayStatus() = %+v, resolver said %+v", got, want)
	}
}
