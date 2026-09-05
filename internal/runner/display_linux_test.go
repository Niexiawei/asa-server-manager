//go:build linux

package runner

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"asa-server/pkg/xvfb"
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
		if got := xvfb.SocketPath(tt.display); got != tt.want {
			t.Errorf("xvfb.SocketPath(%q) = %q, want %q（%s）", tt.display, got, tt.want, tt.why)
		}
	}
}

// TestX11DisplayUsableRejectsDeadDisplay: 光有 DISPLAY 变量不算数 —— 实测
// DISPLAY=:99（无人监听）与完全不设一样失败。远程形式无法本地判断，放行。
func TestX11DisplayUsableRejectsDeadDisplay(t *testing.T) {
	if xvfb.DisplayUsable(":99999") {
		t.Error("xvfb.DisplayUsable(\":99999\") = true, want false — 没有这个 socket")
	}
	if !xvfb.DisplayUsable("remote.host:0") {
		t.Error("xvfb.DisplayUsable(remote) = false, want true — 远程显示交给调用方去试")
	}
}

// TestPlanDisplayIgnoresDeadDisplay: DISPLAY 指向一个不存在的显示时不能采用它，
// 必须继续往后找。这是「有变量 ≠ 有服务」那条实测结论的回归测试。
func TestPlanDisplayIgnoresDeadDisplay(t *testing.T) {
	t.Setenv("DISPLAY", ":99999")

	plans, blocked := planDisplay(getConfig())
	for _, p := range plans {
		if p.Kind == displayEnv {
			t.Errorf("planDisplay adopted a DISPLAY with no listener: %+v", p)
		}
	}
	// 这台机器上 Xvfb / 现成 X 服务的有无决定 blocked 是不是空，两种都合法 ——
	// 断言的是「没把死的 DISPLAY 当成活的」，以及成功时必然指明了一种手段。
	if blocked == "" && len(plans) == 0 {
		t.Error("planDisplay reported success with an empty chain")
	}
	for _, p := range plans {
		if p.Kind == displayNone || p.How == "" {
			t.Errorf("plan without a kind or explanation: %+v", p)
		}
	}
}

// TestPlanDisplayChainOrder: 候选链的顺序是**点名的 > 自己管的 > 捡来的 > 扫出来的**。
// 这是本次改动的核心断言：自管 Xvfb 是唯一由我们启动、监控、随我们退出的显示，
// 排在一个从环境里捡来的变量后面没有道理（docs/ALWAYS_MANAGED_XVFB_DISPLAY_PLAN.md §1.1）。
func TestPlanDisplayChainOrder(t *testing.T) {
	rank := map[displayKind]int{
		displayConfigured: 0,
		displayManaged:    1,
		displayEnv:        2,
		displayExisting:   3,
	}
	plans, _ := planDisplay(getConfig())
	for i := 1; i < len(plans); i++ {
		if rank[plans[i-1].Kind] >= rank[plans[i].Kind] {
			t.Errorf("候选链顺序错了：%s 排在 %s 前面\n完整链：%+v",
				plans[i-1].Kind, plans[i].Kind, plans)
		}
	}
}

// TestPlanDisplayPrefersManagedOverEnv: 有 Xvfb 可用时，环境变量捡来的显示不许
// 排在自管 Xvfb 前面。这条正是改序要保证的东西。
//
// 无法构造一个真的 DISPLAY，所以断言写成「只要自管那一档在链里，它就必须排在
// env/existing 之前」——不依赖这台机器上恰好有没有 X 服务。
func TestPlanDisplayPrefersManagedOverEnv(t *testing.T) {
	plans, _ := planDisplay(getConfig())
	seenBorrowed := false
	for _, p := range plans {
		switch p.Kind {
		case displayEnv, displayExisting:
			seenBorrowed = true
		case displayManaged:
			if seenBorrowed {
				t.Errorf("自管 Xvfb 排在了捡来/扫出来的显示后面：%+v", plans)
			}
		}
	}
}

// TestPlanDisplayConfiguredWinsOverManaged: linux.display 是逃生舱，不能被这次
// 改序吃掉 —— 「我就是想用宿主那个显示」必须仍然有办法表达。
//
// 这里只能验证到「配置项被读进了解析器」：真正入链还要过一次握手，而单测环境里
// 没有真显示。所以断言分两种情况写。
func TestPlanDisplayConfiguredWinsOverManaged(t *testing.T) {
	cfg := getConfig()
	cfg.Display = ":99997" // 不存在，必然握手失败
	plans, _ := planDisplay(cfg)
	for _, p := range plans {
		if p.Kind == displayConfigured {
			t.Errorf("planDisplay 采纳了一个握不上手的 linux.display：%+v", p)
		}
	}
	// 反过来：任何一个真的入链的 configured 候选都必须在最前面（由
	// TestPlanDisplayChainOrder 的 rank 表钉住），这里只补一条 —— 配置为空时
	// 绝不会凭空冒出 configured 这一档。
	cfg.Display = ""
	plans, _ = planDisplay(cfg)
	for _, p := range plans {
		if p.Kind == displayConfigured {
			t.Errorf("linux.display 为空却出现了 configured 候选：%+v", p)
		}
	}
}

// TestPlanDisplayBlockedOnlyWhenChainEmpty: 不变量 —— blocked 为空 ⇔ 链非空。
// preflight / DisplayStatus 都建立在这条上：它们只问 planDisplay，而启动路径沿着
// 同一条链走，两边不能对「这台机器能不能拿到显示」给出不同答案。
func TestPlanDisplayBlockedOnlyWhenChainEmpty(t *testing.T) {
	plans, blocked := planDisplay(getConfig())
	if (blocked == "") != (len(plans) > 0) {
		t.Errorf("blocked = %q 但链长 %d —— 二者必须互为充要条件", blocked, len(plans))
	}
}

// TestDisplayStatusMatchesPlan: 诊断视图与真正的启动判断必须是同一个答案，
// 否则 verify-arkapi 会报「✔ 显示就绪」而启动照样被拒。
func TestDisplayStatusMatchesPlan(t *testing.T) {
	plans, blocked := planDisplay(getConfig())
	info := displayStatus()

	if info.Available != (blocked == "") {
		t.Errorf("DisplayStatus().Available = %v, but planDisplay blocked = %q", info.Available, blocked)
	}
	if info.Blocked != blocked {
		t.Errorf("DisplayStatus().Blocked = %q, want %q", info.Blocked, blocked)
	}
	// 报的必须是**头一档**（下一次启动真正会先用的那个），其余进 Fallbacks。
	if len(plans) > 0 && plans[0].Kind != displayManaged && info.How != plans[0].How {
		t.Errorf("DisplayStatus().How = %q, want the chain head %q", info.How, plans[0].How)
	}
	if want := len(plans) - min(1, len(plans)); len(info.Fallbacks) != want {
		t.Errorf("DisplayStatus().Fallbacks 有 %d 条，链长 %d，want %d 条",
			len(info.Fallbacks), len(plans), want)
	}
}

// TestDisplayStatusStartsNothing: 诊断视图不许有副作用。自管那一档的「拿到显示」
// 意味着真的 fork 一个 X 服务端，被 GET /api/system/preflight 问一句就起一个是不行的。
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
	if got := xvfbUnavailableReason(xvfb.ErrNoBinary, ""); !strings.Contains(got, "sudo apt install xvfb") {
		t.Errorf("xvfb.ErrNoBinary → %q, want the install hint", got)
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

// TestX11SocketDirStateExplainsRefusal: 拒绝时必须给出可执行的原因，
// 而且不能再拿 o+w 当判据 —— 那条判据会把「属主是运行时用户的 0755 目录」判成
// 不可写，而目录恰恰是上一轮那个降权 Xvfb 自己建的（第一次成功毒死后续每一次）。
func TestX11SocketDirStateExplainsRefusal(t *testing.T) {
	st := xvfbManager().SocketDirState()
	if st.Writable && st.Why != "" {
		t.Errorf("SocketDirState = %+v；可写就不该带原因", st)
	}
	if !st.Writable && st.Why == "" {
		t.Errorf("SocketDirState = %+v；不可写必须说明原因", st)
	}
	if st.Writable && st.Fixable {
		t.Errorf("SocketDirState = %+v；已经可写就没有「待修」这回事", st)
	}
	if !st.Writable && !strings.Contains(st.Why, xvfb.SocketDir) && !strings.Contains(st.Why, "/tmp") {
		t.Errorf("refusal reason %q names neither %s nor /tmp", st.Why, xvfb.SocketDir)
	}
}

// TestX11SocketDirStateFixableNeedsRootAndConsent: 「只读挂载但我们修得好」这一档
// 有两个前提，缺一不可 —— 必须是 root（remount 要 CAP_SYS_ADMIN），
// 且 AllowX11Remount 没被关掉。关掉时不但不能 Fixable，还要在原因里说清楚
// 是**配置**挡住的，否则用户会去查一个根本不存在的权限问题。
func TestX11SocketDirStateFixableNeedsRootAndConsent(t *testing.T) {
	st := xvfb.New(xvfb.Config{AllowX11Remount: false}).SocketDirState()
	if st.Fixable {
		t.Errorf("AllowX11Remount=false 却报 Fixable：%+v", st)
	}
	if !st.Writable && !strings.Contains(st.Why, "AllowX11Remount") &&
		strings.Contains(st.Why, "只读挂载") {
		t.Errorf("只读挂载 + 开关关掉时，原因里必须点名 AllowX11Remount：%q", st.Why)
	}
	if os.Geteuid() != 0 {
		if st := xvfb.New(xvfb.Config{AllowX11Remount: true}).SocketDirState(); st.Fixable {
			t.Errorf("非 root 却报 Fixable（remount 需要 CAP_SYS_ADMIN）：%+v", st)
		}
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

// TestFirstLineCompressesMultilineError: 回退原因要缀进 How，而 Xvfb 的失败会带上
// xvfb.log 的末尾若干行 —— 整段塞进一行文案里没法看。完整原文仍在 error 与日志里。
func TestFirstLineCompressesMultilineError(t *testing.T) {
	if got := firstLine("Xvfb 启动失败：缺字体\n/root/xvfb.log 的末尾输出：\nFatal server error"); got != "Xvfb 启动失败：缺字体" {
		t.Errorf("firstLine = %q, want only the first line", got)
	}
	if got := firstLine(strings.Repeat("x", 500)); len(got) > 200 {
		t.Errorf("firstLine 没有截断超长单行：%d 字符", len(got))
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

// TestPlanDisplayNoPhantomXvfbNote: 自管 Xvfb **在链里**时，不许在别的候选后面缀
// 一句「起不了 Xvfb」。判据一旦写成「头一档不是 managed」，linux.display 点名且
// Xvfb 一切正常的机器就会得到一句空原因的假故障 —— 一个凭空造出来的排障线索，
// 比没有线索更贵（同 XVFB 方案 §12.2 那段 Xalia 噪音的教训）。
func TestPlanDisplayNoPhantomXvfbNote(t *testing.T) {
	plans, blocked := planDisplay(getConfig())
	if blocked != "" || len(plans) == 0 {
		return
	}
	if !containsKind(plans, displayManaged) {
		return // 自管那一档真的不可用，缀原因是对的
	}
	for _, p := range plans {
		if strings.Contains(p.How, "起不了 Xvfb") || strings.Contains(p.How, "本机没有 Xvfb") {
			t.Errorf("自管 Xvfb 在链里，却有候选声称它不可用：%q", p.How)
		}
	}
}
