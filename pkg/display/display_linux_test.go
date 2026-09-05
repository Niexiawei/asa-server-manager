//go:build linux

package display

import (
	"errors"
	"os"
	"strings"
	"testing"

	"asa-server/pkg/xvfb"
)

// testResolver 建一个带自己的 *xvfb.Manager 的解析器。构造 Manager 不启动任何东西
// （第一次 Acquire 才动手），而这些用例只问 Plan/Status，所以没有副作用。
func testResolver(cfg Config) *Resolver {
	return New(cfg, xvfb.New(xvfb.Config{}))
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

// TestPlanIgnoresDeadDisplay: DISPLAY 指向一个不存在的显示时不能采用它，
// 必须继续往后找。这是「有变量 ≠ 有服务」那条实测结论的回归测试。
func TestPlanIgnoresDeadDisplay(t *testing.T) {
	t.Setenv("DISPLAY", ":99999")

	plans, blocked := testResolver(Config{}).Plan()
	for _, p := range plans {
		if p.Kind == KindEnv {
			t.Errorf("Plan adopted a DISPLAY with no listener: %+v", p)
		}
	}
	// 这台机器上 Xvfb / 现成 X 服务的有无决定 blocked 是不是空，两种都合法 ——
	// 断言的是「没把死的 DISPLAY 当成活的」，以及成功时必然指明了一种手段。
	if blocked == "" && len(plans) == 0 {
		t.Error("Plan reported success with an empty chain")
	}
	for _, p := range plans {
		if p.Kind == KindNone || p.How == "" {
			t.Errorf("plan without a kind or explanation: %+v", p)
		}
	}
}

// TestPlanChainOrder: 候选链的顺序是**点名的 > 自己管的 > 捡来的 > 扫出来的**。
// 自管 Xvfb 是唯一由我们启动、监控、随我们退出的显示，排在一个从环境里捡来的
// 变量后面没有道理（docs/ALWAYS_MANAGED_XVFB_DISPLAY_PLAN.md §1.1）。
func TestPlanChainOrder(t *testing.T) {
	rank := map[Kind]int{
		KindConfigured: 0,
		KindManaged:    1,
		KindEnv:        2,
		KindExisting:   3,
	}
	plans, _ := testResolver(Config{}).Plan()
	for i := 1; i < len(plans); i++ {
		if rank[plans[i-1].Kind] >= rank[plans[i].Kind] {
			t.Errorf("候选链顺序错了：%s 排在 %s 前面\n完整链：%+v",
				plans[i-1].Kind, plans[i].Kind, plans)
		}
	}
}

// TestPlanPrefersManagedOverEnv: 有 Xvfb 可用时，环境变量捡来的显示不许排在自管
// Xvfb 前面。
//
// 无法构造一个真的 DISPLAY，所以断言写成「只要自管那一档在链里，它就必须排在
// env/existing 之前」——不依赖这台机器上恰好有没有 X 服务。
func TestPlanPrefersManagedOverEnv(t *testing.T) {
	plans, _ := testResolver(Config{}).Plan()
	seenBorrowed := false
	for _, p := range plans {
		switch p.Kind {
		case KindEnv, KindExisting:
			seenBorrowed = true
		case KindManaged:
			if seenBorrowed {
				t.Errorf("自管 Xvfb 排在了捡来/扫出来的显示后面：%+v", plans)
			}
		}
	}
}

// TestPlanConfiguredWinsOverManaged: linux.display 是逃生舱 —— 「我就是想用宿主
// 那个显示」必须仍然有办法表达。
//
// 这里只能验证到「配置项被读进了解析器」：真正入链还要过一次握手，而单测环境里
// 没有真显示。所以断言分两种情况写。
func TestPlanConfiguredWinsOverManaged(t *testing.T) {
	plans, _ := testResolver(Config{Display: ":99997"}).Plan() // 不存在，必然握手失败
	for _, p := range plans {
		if p.Kind == KindConfigured {
			t.Errorf("Plan 采纳了一个握不上手的 linux.display：%+v", p)
		}
	}
	// 反过来：任何一个真的入链的 configured 候选都必须在最前面（由
	// TestPlanChainOrder 的 rank 表钉住），这里只补一条 —— 配置为空时
	// 绝不会凭空冒出 configured 这一档。
	plans, _ = testResolver(Config{}).Plan()
	for _, p := range plans {
		if p.Kind == KindConfigured {
			t.Errorf("linux.display 为空却出现了 configured 候选：%+v", p)
		}
	}
}

// TestPlanBlockedOnlyWhenChainEmpty: 不变量 —— blocked 为空 ⇔ 链非空。
// preflight / Status 都建立在这条上：它们只问 Plan，而启动路径沿着同一条链走，
// 两边不能对「这台机器能不能拿到显示」给出不同答案。
func TestPlanBlockedOnlyWhenChainEmpty(t *testing.T) {
	plans, blocked := testResolver(Config{}).Plan()
	if (blocked == "") != (len(plans) > 0) {
		t.Errorf("blocked = %q 但链长 %d —— 二者必须互为充要条件", blocked, len(plans))
	}
}

// TestStatusMatchesPlan: 诊断视图与真正的启动判断必须是同一个答案，
// 否则 verify-arkapi 会报「✔ 显示就绪」而启动照样被拒。
func TestStatusMatchesPlan(t *testing.T) {
	r := testResolver(Config{})
	plans, blocked := r.Plan()
	info := r.Status()

	if info.Available != (blocked == "") {
		t.Errorf("Status().Available = %v, but Plan blocked = %q", info.Available, blocked)
	}
	if info.Blocked != blocked {
		t.Errorf("Status().Blocked = %q, want %q", info.Blocked, blocked)
	}
	// 报的必须是**头一档**（下一次启动真正会先用的那个），其余进 Fallbacks。
	if len(plans) > 0 && plans[0].Kind != KindManaged && info.How != plans[0].How {
		t.Errorf("Status().How = %q, want the chain head %q", info.How, plans[0].How)
	}
	if want := len(plans) - min(1, len(plans)); len(info.Fallbacks) != want {
		t.Errorf("Status().Fallbacks 有 %d 条，链长 %d，want %d 条",
			len(info.Fallbacks), len(plans), want)
	}
}

// TestStatusStartsNothing: 诊断视图不许有副作用。自管那一档的「拿到显示」意味着
// 真的 fork 一个 X 服务端，被 GET /api/system/preflight 问一句就起一个是不行的。
// 这是本包最承重的不变量 —— Plan 只读、acquire 才动手那条分界的落地检验。
func TestStatusStartsNothing(t *testing.T) {
	mgr := xvfb.New(xvfb.Config{})
	r := New(Config{}, mgr)

	before := mgr.Status()
	_ = r.Status()
	_, _ = r.Plan()
	if mgr.Status() != before {
		t.Error("Status/Plan started an X server as a side effect")
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
	st := xvfb.New(xvfb.Config{}).SocketDirState()
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

// TestBlockedMessageNamesXvfbNotXvfbRun: 拿不到显示时给的提示必须指向 **Xvfb**。
// 指向 xvfb-run 会把 Fedora/RHEL/Arch 用户带到一个他们装不上的包（那是 Debian 的
// 脚本），见 docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md §1。
func TestBlockedMessageNamesXvfbNotXvfbRun(t *testing.T) {
	_, blocked := testResolver(Config{}).Plan()
	if blocked == "" {
		return // 这台机器有显示，没有可断言的文案
	}
	if strings.Contains(blocked, "xvfb-run") {
		t.Errorf("blocked message still points at xvfb-run: %q", blocked)
	}
}

// TestPlanNoPhantomXvfbNote: 自管 Xvfb **在链里**时，不许在别的候选后面缀一句
// 「起不了 Xvfb」。判据一旦写成「头一档不是 managed」，linux.display 点名且
// Xvfb 一切正常的机器就会得到一句空原因的假故障 —— 一个凭空造出来的排障线索，
// 比没有线索更贵（同 XVFB 方案 §12.2 那段 Xalia 噪音的教训）。
func TestPlanNoPhantomXvfbNote(t *testing.T) {
	plans, blocked := testResolver(Config{}).Plan()
	if blocked != "" || len(plans) == 0 {
		return
	}
	if !containsKind(plans, KindManaged) {
		return // 自管那一档真的不可用，缀原因是对的
	}
	for _, p := range plans {
		if strings.Contains(p.How, "起不了 Xvfb") || strings.Contains(p.How, "本机没有 Xvfb") {
			t.Errorf("自管 Xvfb 在链里，却有候选声称它不可用：%q", p.How)
		}
	}
}
