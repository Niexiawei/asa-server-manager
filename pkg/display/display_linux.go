//go:build linux

package display

import (
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"asa-server/pkg/logger"
	"asa-server/pkg/xvfb"
)

// Config is what this package needs from the host application config.
type Config struct {
	// Display 是 `linux.display` 点名的显示号（":0"）。空 = 没点名。
	Display string
}

// Resolver 回答「这台机器怎么给 Wine 进程一个显示」。
//
// 它持有的 *xvfb.Manager 由调用方传入而**不是自己 New 的**：xvfb.Manager 里跑着一个
// LockOSThread 且永不返回的 spawn-loop goroutine，「进程内只有一个自管显示」这条不变量
// 靠「组合根只持有一份 Manager」保证。见 pkg/xvfb.Manager 的注释与
// docs/RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md §4.3。
type Resolver struct {
	cfg  atomic.Pointer[Config]
	xvfb *xvfb.Manager
}

// New returns a Resolver for cfg. Constructing one starts nothing.
func New(cfg Config, mgr *xvfb.Manager) *Resolver {
	r := &Resolver{xvfb: mgr}
	r.cfg.Store(&cfg)
	return r
}

// Reconfigure updates the live Config. Cheap (an atomic pointer store), so the
// caller can refresh before every use instead of hooking its own Configure().
// The injected *xvfb.Manager is untouched — it has its own Reconfigure, and
// the caller owns it.
func (r *Resolver) Reconfigure(cfg Config) { r.cfg.Store(&cfg) }

func (r *Resolver) config() Config {
	if c := r.cfg.Load(); c != nil {
		return *c
	}
	return Config{}
}

// Kind 是「显示从哪来」的四种答案，常量的声明顺序就是候选顺序：
// **点名的 > 自己管的 > 捡来的 > 扫出来的**（见 Resolver.Plan）。
type Kind string

const (
	KindNone Kind = ""
	// KindConfigured：linux.display 点名的。唯一表达了「请用这个显示」的一档。
	KindConfigured Kind = "configured"
	// KindManaged：自管的 Xvfb（asa-server/pkg/xvfb）。
	KindManaged Kind = "managed"
	// KindEnv：从环境变量 DISPLAY 捡来的。没有人表达过意图 —— 桌面终端、
	// su -、WSLg 都会顺手把它塞进来。
	KindEnv Kind = "env"
	// KindExisting：扫 SocketDir 扫出来的、系统里已在跑的 X 服务。
	KindExisting Kind = "existing"
)

// Plan 是**只读**的判断结果：这台机器打算怎么给 Wine 进程提供显示。
//
// 与 Target 分开是必须的，不是洁癖：preflight、状态接口、
// `verify-arkapi --check-only` 都要问「能不能拿到显示」，而自管 Xvfb 那一档的
// 「拿到」意味着**真的 fork 一个 X 服务端**。合在一个函数里，`GET /api/system/preflight`
// 会顺手起一个 X 服务。Resolver.Plan 只做判断，acquire 才动手。
type Plan struct {
	Kind    Kind
	Display string // Kind 为 env/existing 时的 ":0"
	How     string // 人类可读的说明
}

// Plan 返回按优先级排好的**候选链**，一条都不成立时第二个返回值说明原因。
// **无副作用。**
//
// **vc_redist 安装与 ArkApi 启动共用这一个函数**：两者需要显示的原因是同一个
// （Wine 的 winex11.drv），分成两套判断只会让它们慢慢漂开。
//
// # 顺序：点名的 > 自己管的 > 捡来的 > 扫出来的
//
//  1. `linux.display` **点名**的显示。唯一一档表达了明确意图，所以排第一 ——
//     它也是「我就是想用宿主那个显示」的逃生舱（调试时想看见游戏窗口，配它）。
//     后台服务进程通常**没有** DISPLAY（真机 /proc/<pid>/environ 里只有 HOME=/root），
//     这也是把机器上现成的 X 服务告诉它的唯一办法。
//  2. **自管 Xvfb**，要求 SocketDir 可写（或我们改得动）。它是这条链上唯一**由我们
//     启动、由我们监控、随我们退出**的显示：不依赖任何桌面会话，用户注销、桌面
//     重启、WSLg 重启都不会把游戏带走；而且它是进程内单例，天然保证所有调用方
//     拿到同一个显示。判据是 **Xvfb 这个服务端二进制**，不是 Debian 那个 xvfb-run
//     脚本 —— 后者 Fedora/RHEL/Arch 压根不提供，拿它当判据会把能用的机器挡在门外
//     （docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md §1）。
//  3. 环境变量 `DISPLAY` **捡来**的显示。它以前排在第 1 位，但没有人表达过「请用这个」
//     的意思：从桌面终端启动、从 su - 继承、WSLg 自动导出都会带上它，而它的生命周期
//     不归我们管。降到自管 Xvfb 之后（docs/ALWAYS_MANAGED_XVFB_DISPLAY_PLAN.md §1.1）。
//  4. 扫 SocketDir 扫出来的、握手能过的 X 服务。兜底。
//
// 后两档保留而不是删掉，是**故意的**：一台没有 Xvfb、或 SocketDir 死活写不了的
// 机器（WSL 是其中一种）仍然要能跑起 ArkApi，而不是拿到一句「本机没有可用的 X 显示」。
//
// # 为什么是「链」而不是「一个答案」
//
// 第 2 档从「只服务无头机」升到「几乎所有机器」之后，它的 acquire 是**可能失败的**
// （缺字体、/tmp 满了、SELinux 拦了）。若仍只返回一个答案，一台本来靠 :0 跑得好好的
// 机器会因为 Xvfb 起不来而启动失败 —— 纯粹的回归。返回整条链，让 Acquire 依次
// 尝试，既堵住这个回归，又不破坏「Plan 只读 / acquire 才动手」这条分界：
// **链非空 ⇔ 这台机器有合理把握拿到显示**，preflight 与 Status 问的还是它。
//
// **不传 XAUTHORITY**。理由与 umu.InheritedEnv 的白名单同源：它经常指向 /run/user/0
// 下面的路径，而 pressure-vessel 会老老实实地去 bind 环境变量点名的每一个路径，
// 降权之后那次 bind 直接让整个容器起不来（DBUS_SESSION_BUS_ADDRESS 那次就是这么坑了
// 一整晚，见 umu.InheritedEnv 的注释）。所以四条路都只认**不需要 cookie 就能握手**的
// 显示，自管的那个 Xvfb 也因此不带 -auth。
func (r *Resolver) Plan() ([]Plan, string) {
	cfg := r.config()
	var plans []Plan

	// ① 点名的
	if p, ok := namedPlan(KindConfigured, cfg.Display,
		"配置指定的 X 显示 "+cfg.Display); ok {
		plans = append(plans, p)
	}

	// ② 自管 Xvfb
	_, xvfbErr := r.xvfb.BinaryPath()
	dir := r.xvfb.SocketDirState()
	if xvfbErr == nil && (dir.Writable || dir.Fixable) {
		how := "自管 Xvfb 虚拟显示"
		if !dir.Writable {
			// 只读挂载但我们改得动。说出来，别让「拉起 Xvfb 之前顺手 remount 了
			// 一个系统目录」这件事只有读代码的人才知道。
			how += "（需先把 " + xvfb.SocketDir + " 重新挂载为可写：" + dir.Why + "）"
		}
		plans = append(plans, Plan{Kind: KindManaged, How: how})
	}

	// ③ 捡来的
	if envDisplay := os.Getenv("DISPLAY"); !containsDisplay(plans, envDisplay) {
		if p, ok := namedPlan(KindEnv, envDisplay,
			"宿主的 X 显示 "+envDisplay+"（来自 DISPLAY 环境变量）"); ok {
			plans = append(plans, p)
		}
	}

	// ④ 扫出来的
	if d := firstUsableX11Display(); d != "" && !containsDisplay(plans, d) {
		plans = append(plans, Plan{
			Kind:    KindExisting,
			Display: d,
			How:     "系统里已在运行的 X 显示 " + d,
		})
	}

	if len(plans) == 0 {
		return nil, "本机没有可用的 X 显示：" +
			xvfbUnavailableReason(xvfbErr, dir.Why) +
			"；系统里也没有现成的、不需要 cookie 就能连的 X 服务"
	}
	// 自管那一档**根本没进链**时，把原因挂在头一档的说明后面：用户看到的是
	// 「用了宿主的 :0」，他有权知道为什么不是那个本该优先的自管显示。
	//
	// 判据必须是「managed 不在链里」而不是「头一档不是 managed」—— 后者在
	// linux.display 点名且 Xvfb 一切正常时也成立，会缀上一句
	// 「起不了 Xvfb：」的空原因，凭空造出一个不存在的故障。
	if !containsKind(plans, KindManaged) {
		plans[0].How += xvfbUnavailableNote(xvfbErr, dir.Why)
	}
	return plans, ""
}

// namedPlan 把一个点名的显示号变成候选，握手过不去就不算数。
//
// 两种「有名字却用不了」都要挡掉，都是实测过的：
//   - socket 文件不在 —— DISPLAY=:99（无人监听）与完全不设一样失败；
//   - socket 在但握不上手 —— 多半是它要 xauth cookie，而我们刻意不传 XAUTHORITY。
func namedPlan(kind Kind, display, how string) (Plan, bool) {
	if display == "" || !x11SocketExists(display) || !xvfb.DisplayUsable(display) {
		return Plan{}, false
	}
	return Plan{Kind: kind, Display: display, How: how}, true
}

// x11SocketExists 校验 DISPLAY 指向的本地 X 服务的 socket 文件是不是真的在。
// 这只是握手前的快速筛子；能不能连由 xvfb.DisplayUsable 说了算。
func x11SocketExists(display string) bool {
	return xvfb.SocketPath(display) != "" || !xvfb.IsLocalDisplay(display)
}

// containsKind 报告链里有没有这一档。
func containsKind(plans []Plan, kind Kind) bool {
	for _, p := range plans {
		if p.Kind == kind {
			return true
		}
	}
	return false
}

// containsDisplay 报告链里是否已经有这个显示号，避免同一个 :0 以两种身份重复入链。
func containsDisplay(plans []Plan, display string) bool {
	if display == "" {
		return true // 空值当作「已经有了」，调用方因此不必先判空
	}
	for _, p := range plans {
		if p.Display == display {
			return true
		}
	}
	return false
}

// xvfbUnavailableReason 说明「自管 Xvfb」这一档为什么走不通。
//
// 三种原因必须分清楚。以前不管哪一种都归到同一句「本机没有可用的 X 显示，也没有
// Xvfb —— 请安装 Xvfb」，于是一台 Xvfb 装在 /usr/bin/Xvfb、真正的问题是 SocketDir
// 权限不对的机器，得到的唯一指引是去装一个它早就装好了的包。判断得对却说不清，
// 等于没判断。
func xvfbUnavailableReason(xvfbErr error, dirWhy string) string {
	switch {
	case errors.Is(xvfbErr, xvfb.ErrNoBinary):
		return "本机没有 Xvfb —— 请" + xvfb.InstallHint
	case xvfbErr != nil:
		// linux.xvfb_bin 指错了。原文（哪个路径、不存在还是不可执行）比任何转述都有用。
		return xvfbErr.Error()
	default:
		return dirWhy
	}
}

// xvfbUnavailableNote 是同一件事的短版本，挂在「用了现成显示」的说明后面当尾注。
func xvfbUnavailableNote(xvfbErr error, dirWhy string) string {
	switch {
	case errors.Is(xvfbErr, xvfb.ErrNoBinary):
		return "（本机没有 Xvfb）"
	case xvfbErr != nil:
		return "（Xvfb 不可用：" + xvfbErr.Error() + "）"
	default:
		return "（起不了 Xvfb：" + dirWhy + "）"
	}
}

// acquire 把计划变成一个真的能用的显示，必要时**拉起 Xvfb**。只有启动路径该调它。
func (r *Resolver) acquire(p Plan) (Target, error) {
	switch p.Kind {
	case KindConfigured, KindEnv, KindExisting:
		return Target{Env: []string{"DISPLAY=" + p.Display}, How: p.How}, nil
	case KindManaged:
		display, err := r.xvfb.Acquire()
		if err != nil {
			return Target{}, err
		}
		return Target{
			Env: []string{"DISPLAY=" + display},
			How: "自管 Xvfb 虚拟显示 " + display,
		}, nil
	}
	return Target{}, errors.New("本机没有可用的图形显示")
}

// Acquire 是启动路径的唯一入口：先判断，再沿候选链动手。
// blocked 非空 = 这台机器压根没有显示可用（调用方给自己的上下文文案）；
// err 非空 = 链上每一档都试过且都失败了，错误是**头一档**的（它才是本该用的那个），
// 里面带着 xvfb.log 的现场。
//
// 回退必须**大声**：日志一条 WARN，并且把原因缀进 How ——「本来该用自管 Xvfb，
// 结果用了宿主的 :0」是排障时第一个要知道的事，静默回退等于把它藏起来。
func (r *Resolver) Acquire() (target Target, blocked string, err error) {
	plans, blocked := r.Plan()
	if blocked != "" {
		return Target{}, blocked, nil
	}

	var (
		firstErr error
		fellBack []string
	)
	for _, p := range plans {
		t, aerr := r.acquire(p)
		if aerr == nil {
			if len(fellBack) > 0 {
				t.How += "（已回退：" + strings.Join(fellBack, "；") + "）"
			}
			return t, "", nil
		}
		if firstErr == nil {
			firstErr = aerr
		}
		logger.Warnf("display: 显示候选「%s」拿不到（%s），尝试下一档", p.How, firstLine(aerr.Error()))
		fellBack = append(fellBack, p.How+" 用不了："+firstLine(aerr.Error()))
	}
	return Target{}, "", firstErr
}

// firstLine 把一条可能很长的错误（Xvfb 的失败会带上 xvfb.log 的末尾若干行）压成一行，
// 好缀进 How 里。完整原文仍然在 Acquire 返回的 error 与日志里。
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	const max = 160
	if len(line) > max {
		return line[:max] + "…"
	}
	return line
}

// Stop 关掉本进程拉起的那个 Xvfb（如果有）。幂等；从未需要过显示、或显示是从别的
// 进程认领来的（那个不归我们杀），都是空操作。
func (r *Resolver) Stop() { r.xvfb.Stop() }

// Status 报告本机的显示现状。**只读**：自管那一档只报告「打算这么做」以及
// 「现在起来没有」，绝不因为被问一句就拉起一个 X 服务端。
//
// 报告的是候选链的**头一档** —— 那才是下一次启动真正会用的那个。其余各档进
// Fallbacks，让「万一头一档拿不到会发生什么」在诊断视图里也是可见的。
func (r *Resolver) Status() Info {
	plans, blocked := r.Plan()
	var p Plan
	if len(plans) > 0 {
		p = plans[0]
	}
	info := Info{
		Available: blocked == "",
		How:       p.How,
		Blocked:   blocked,
		Display:   p.Display,
	}
	for _, f := range plans[min(1, len(plans)):] {
		info.Fallbacks = append(info.Fallbacks, f.How)
	}
	if p.Kind == KindManaged {
		info.Managed = true
		if status := r.xvfb.Status(); status.Running {
			info.Display = status.Display
			info.How = "自管 Xvfb 虚拟显示 " + status.Display
		} else {
			info.How = "自管 Xvfb 虚拟显示（尚未启动，将在需要时拉起）"
		}
	}
	return info
}

// --- 探测 -----------------------------------------------------------------------

// firstUsableX11Display 扫 SocketDir，返回第一个握手能过的显示号。
// 显示号按数值升序，好让结果稳定（同一台机器每次选中同一个）。
func firstUsableX11Display() string {
	entries, err := os.ReadDir(xvfb.SocketDir)
	if err != nil {
		return ""
	}
	var nums []int
	for _, e := range entries {
		n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "X"))
		if err != nil || !strings.HasPrefix(e.Name(), "X") {
			continue
		}
		nums = append(nums, n)
	}
	sort.Ints(nums)
	for _, n := range nums {
		d := ":" + strconv.Itoa(n)
		if xvfb.DisplayUsable(d) {
			return d
		}
	}
	return ""
}
