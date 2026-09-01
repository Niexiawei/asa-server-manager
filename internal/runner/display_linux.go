//go:build linux

package runner

// Wine 侧的「图形显示」是本项目在 Linux 上的一个**硬依赖**，尽管跑的是无头服务端。
//
// 两处需要它，成因相同：Wine 的 winex11.drv 连不上 X 服务时，`CreateWindow` 一律失败
// （`err:winediag:nodrv_CreateWindow ... The explorer process failed to start.`），
// 于是任何要开窗口的 Windows 程序都会在打出第一行日志之前就死掉。
//
//  1. **AsaApiLoader.exe（ArkApi）**——真机实测（WSL2 + GE-Proton10-34 + umu 1.4.4，
//     2026-08-30）：不给显示时 5 秒后退出码 3，**一个字都不打**，Win64/logs/ 连目录
//     都不会建；补上一个能用的显示，同一条命令就能加载 ArkApi、下载 offsets cache、
//     加载插件并拉起 ArkAscendedServer.exe。日志里 `oleacc:find_class_data unhandled
//     window class: L"Static"` 说明它是带真窗口的程序，不是纯控制台程序。
//  2. **vc_redist.x64.exe**——WiX Burn 引导器即使带 /quiet 也要初始化 UI 子系统，
//     连不上 X 就以 203 退出（见 vcRedistExitNoDisplay）。
//
// ArkAscendedServer.exe 本身**不需要**显示（同一台机器上 42 秒就开始监听），所以
// 只有上面两条路径会去解析显示，普通实例启动一如既往地不碰它。
//
// 见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §9 与 docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md。

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// x11SocketDir 是 X 服务发布本地 socket 的**唯一**位置 —— 路径写死在 xtrans 里，
// 不受任何环境变量影响。整个文件里对它的两次检查（能不能写、里面有没有现成的
// socket）都源自这一点。
const x11SocketDir = "/tmp/.X11-unix"

// x11ProbeTimeout 是探测一个显示能不能连的超时。本地 unix socket 的握手是微秒级，
// 给足一秒纯粹是防对端半死不活地吊着。
const x11ProbeTimeout = time.Second

// displayKind 是「显示从哪来」的三种答案。
type displayKind string

const (
	displayNone     displayKind = ""
	displayEnv      displayKind = "env"      // 配置或环境变量点名的现成显示
	displayManaged  displayKind = "managed"  // 我们自己拉起的 Xvfb（xvfb_linux.go）
	displayExisting displayKind = "existing" // 扫出来的、系统里已在跑的 X 服务
)

// displayPlan 是**只读**的判断结果：这台机器打算怎么给 Wine 进程提供显示。
//
// 与 displayTarget 分开是必须的，不是洁癖：preflight、DisplayStatus、
// `verify-arkapi --check-only` 都要问「能不能拿到显示」，而自管 Xvfb 那一档的
// 「拿到」意味着**真的 fork 一个 X 服务端**。合在一个函数里，`GET /api/system/preflight`
// 会顺手起一个 X 服务。planDisplay 只做判断，acquire 才动手。
type displayPlan struct {
	Kind    displayKind
	Display string // Kind 为 env/existing 时的 ":0"
	XvfbBin string // Kind 为 managed 时解析好的 Xvfb 路径
	How     string // 人类可读的说明
}

// displayTarget 是**已经拿到手**的显示：把它施加到一条命令的环境上即可。
type displayTarget struct {
	Env []string // 追加到进程环境，如 DISPLAY=:0
	How string   // 人类可读的说明
}

// planDisplay 决定怎么给进程提供显示，拿不到时第二个返回值说明原因。**无副作用。**
//
// **vc_redist 安装与 ArkApi 启动共用这一个函数**：两者需要显示的原因是同一个
// （Wine 的 winex11.drv），分成两套判断只会让它们慢慢漂开。
//
// 三条路，按「越靠前越可控」排列，每一条都是**验证过**的而不是猜的：
//
//  1. 点名的显示 —— `linux.display` 配置项或 DISPLAY 环境变量，且真的握手成功。
//     配置项是为后台服务准备的：服务进程通常**没有** DISPLAY（真机
//     /proc/<pid>/environ 里只有 HOME=/root），而机器上可能确实有个能用的 X 服务。
//  2. 自管 Xvfb，**但要求 /tmp/.X11-unix 可写**。这条是无头服务器（本项目的主力
//     部署形态）的正路：自带显示、不依赖任何桌面会话，进程归我们管，用户注销也不会
//     把游戏带走。判据是 **Xvfb 这个服务端二进制**，不是 Debian 那个 xvfb-run 脚本
//     —— 后者 Fedora/RHEL/Arch 压根不提供，拿它当判据会把能用的机器挡在门外
//     （docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md §1）。
//  3. 系统里已经跑着的、握手能过的 X 显示。这条兜住 ① 没配、② 用不了的情况。
//
// 为什么 ② 要检查 /tmp/.X11-unix 可写 —— 这是 2026-08-30 真机踩出来的：WSLg 把
// 这个目录挂成 **只读 tmpfs**（`none on /tmp/.X11-unix type tmpfs (ro,relatime)`），
// 于是 `Xvfb :100` 建不出文件 socket，只剩一个抽象 socket，pressure-vessel 只能报
//
//	W: X11 socket /tmp/.X11-unix/X100 does not exist in filesystem,
//	   trying to use abstract socket instead.
//
// 然后容器里的程序连不上，GE-Proton 的 xalia 抛
// `PlatformNotSupportedException: Video driver not supported`，AsaApiLoader 同样
// 起不来 —— 又回到那个「零输出」的失败模式。既然这个前提可以直接测出来，就不该赌。
//
// **不传 XAUTHORITY**。理由与 inheritedEnv 的白名单同源：它经常指向 /run/user/0
// 下面的路径，而 pressure-vessel 会老老实实地去 bind 环境变量点名的每一个路径，
// 降权之后那次 bind 直接让整个容器起不来（DBUS_SESSION_BUS_ADDRESS 那次就是这么坑了
// 一整晚，见 inheritedEnv 的注释）。所以三条路都只认**不需要 cookie 就能握手**的显示，
// 自管的那个 Xvfb 也因此不带 -auth（见 xvfbArgs）。
func planDisplay(cfg Config) (displayPlan, string) {
	if d, src := configuredDisplay(cfg); d != "" {
		switch {
		case !x11SocketExists(d):
			// 有变量没服务：实测 DISPLAY=:99（无人监听）与不设一样失败，
			// 所以这里不能认它，继续往下找。
		case !x11DisplayUsable(d):
			// socket 在但握不上手，最常见的原因是它要 xauth cookie 而我们
			// 刻意不传 XAUTHORITY。同样继续往下找 —— 有 Xvfb 兜底。
		default:
			return displayPlan{Kind: displayEnv, Display: d, How: src + "的 X 显示 " + d}, ""
		}
	}

	xvfbBin, xvfbErr := xvfbBinary(cfg)
	dirOK, dirWhy := x11SocketDirWritable()
	if xvfbErr == nil && dirOK {
		return displayPlan{Kind: displayManaged, XvfbBin: xvfbBin, How: "自管 Xvfb 虚拟显示"}, ""
	}

	if d := firstUsableX11Display(); d != "" {
		return displayPlan{
			Kind:    displayExisting,
			Display: d,
			How:     "系统里已在运行的 X 显示 " + d + xvfbUnavailableNote(xvfbErr, dirWhy),
		}, ""
	}

	return displayPlan{}, "本机没有可用的 X 显示：" +
		xvfbUnavailableReason(xvfbErr, dirWhy) +
		"；系统里也没有现成的、不需要 cookie 就能连的 X 服务"
}

// configuredDisplay 取显式点名的显示：配置优先于环境变量。
func configuredDisplay(cfg Config) (display, source string) {
	if cfg.Display != "" {
		return cfg.Display, "配置指定"
	}
	if d := os.Getenv("DISPLAY"); d != "" {
		return d, "宿主"
	}
	return "", ""
}

// xvfbUnavailableReason 说明「自管 Xvfb」这一档为什么走不通。
//
// 三种原因必须分清楚。以前不管哪一种都归到同一句「本机没有可用的 X 显示，也没有
// Xvfb —— 请安装 Xvfb」，于是 2026-08-31 那台 AlmaLinux（Xvfb 装在 /usr/bin/Xvfb、
// 真正的问题是 /tmp/.X11-unix 权限不对）得到的唯一指引，是去装一个它早就装好了的包。
// 判断得对却说不清，等于没判断。
func xvfbUnavailableReason(xvfbErr error, dirWhy string) string {
	switch {
	case errors.Is(xvfbErr, errNoXvfb):
		return "本机没有 Xvfb —— 请" + xvfbInstallHint
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
	case errors.Is(xvfbErr, errNoXvfb):
		return "（本机没有 Xvfb）"
	case xvfbErr != nil:
		return "（Xvfb 不可用：" + xvfbErr.Error() + "）"
	default:
		return "（起不了 Xvfb：" + dirWhy + "）"
	}
}

// acquire 把计划变成一个真的能用的显示，必要时**拉起 Xvfb**。只有启动路径该调它。
func (p displayPlan) acquire(cfg Config) (displayTarget, error) {
	switch p.Kind {
	case displayEnv, displayExisting:
		return displayTarget{Env: []string{"DISPLAY=" + p.Display}, How: p.How}, nil
	case displayManaged:
		x, err := ensureXvfb(cfg, p.XvfbBin)
		if err != nil {
			return displayTarget{}, err
		}
		return displayTarget{
			Env: []string{"DISPLAY=" + x.display},
			How: "自管 Xvfb 虚拟显示 " + x.display,
		}, nil
	}
	return displayTarget{}, errors.New("本机没有可用的图形显示")
}

// acquireDisplay 是启动路径的唯一入口：先判断，再动手。
// blocked 非空 = 这台机器压根没有显示可用（调用方给自己的上下文文案）；
// err 非空 = 有能力但这次没拿到（Xvfb 起不来），错误里带着 xvfb.log 的现场。
func acquireDisplay(cfg Config) (target displayTarget, blocked string, err error) {
	p, blocked := planDisplay(cfg)
	if blocked != "" {
		return displayTarget{}, blocked, nil
	}
	target, err = p.acquire(cfg)
	return target, "", err
}

// applyTo 把显示追加到一条命令的环境上。
//
// 追加在最后是刻意的：runtimeEnv 会剥掉 XDG_*，DISPLAY 不在其列，但顺序上排最后就
// 不会被将来新增的过滤逻辑吃掉（exec 取同名变量的最后一个）。返回新切片而不是就地
// append —— 「解析一次显示、拿它包两条命令」不能污染第一条。
func (d displayTarget) applyTo(env []string) []string {
	return append(append([]string{}, env...), d.Env...)
}

// --- 探测 -----------------------------------------------------------------------

// x11SocketExists 校验 DISPLAY 指向的本地 X 服务的 socket 文件是不是真的在。
// 这只是握手前的快速筛子；能不能连由 x11DisplayUsable 说了算。
func x11SocketExists(display string) bool {
	return x11SocketPath(display) != "" || !isLocalDisplay(display)
}

// x11SocketPath 把 ":0" / ":0.0" 这样的本地 DISPLAY 换算成 socket 文件路径，
// 文件不存在或不是本地形式时返回 ""。
func x11SocketPath(display string) string {
	if !isLocalDisplay(display) {
		return ""
	}
	num := strings.TrimPrefix(display, ":")
	if i := strings.Index(num, "."); i >= 0 {
		num = num[:i] // 去掉 screen 号，socket 只按 display 号命名
	}
	if num == "" {
		return ""
	}
	if _, err := strconv.Atoi(num); err != nil {
		return ""
	}
	path := filepath.Join(x11SocketDir, "X"+num)
	if !pathExists(path) {
		return ""
	}
	return path
}

// isLocalDisplay 区分 ":0" 这样的本地显示与 "host:0" / 路径形式的远程显示。
// 只有本地形式能靠 socket 与握手判断，远程的交给调用方自己去试。
func isLocalDisplay(display string) bool {
	return strings.HasPrefix(display, ":")
}

// x11DisplayUsable 真的连一次 X 服务并走一遍**无认证**的连接握手。
//
// 为什么要做到这一步而不是止于「socket 文件在不在」：本项目刻意不传 XAUTHORITY
// （见 planDisplay 的注释），所以一个需要 cookie 的显示对我们就是不可用的 ——
// 而它的 socket 文件明明在。拿文件存在当判据，会挑中一个连不上的显示，然后又变成
// 那个「加载器零输出退出」的谜题。握手一次就能把猜测换成事实，代价是几微秒。
//
// 自管 Xvfb 的就绪判定（managedXvfb.waitReady）用的也是这个函数 —— 起没起来与能不能用
// 必须是同一个判据。
//
// 协议（X11 §连接建立）：客户端先发 12 字节的 setup 请求，服务端回的第一个字节
// 0=Failed / 1=Success / 2=Authenticate。我们只看这一个字节，不解析后面的
// 服务端信息，也不需要任何 X 库。
func x11DisplayUsable(display string) bool {
	if !isLocalDisplay(display) {
		return true // 远程显示无法本地判断，放行让调用方去试
	}
	path := x11SocketPath(display)
	if path == "" {
		return false
	}
	conn, err := net.DialTimeout("unix", path, x11ProbeTimeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(x11ProbeTimeout))

	// 'l' = 小端；protocol-major=11, minor=0；auth 名与 auth 数据长度都是 0。
	req := []byte{'l', 0, 11, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if _, err := conn.Write(req); err != nil {
		return false
	}
	resp := make([]byte, 8)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return false
	}
	return resp[0] == 1
}

// firstUsableX11Display 扫 /tmp/.X11-unix，返回第一个握手能过的显示号。
// 显示号按数值升序，好让结果稳定（同一台机器每次选中同一个）。
func firstUsableX11Display() string {
	entries, err := os.ReadDir(x11SocketDir)
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
		if x11DisplayUsable(d) {
			return d
		}
	}
	return ""
}

// x11SocketDirWritable 报告 Xvfb 能不能在 /tmp/.X11-unix 里发布一个**文件** socket，
// 不能时第二个返回值说明原因（会原样出现在用户看到的文案里，所以要具体）。
//
// # 这里曾经有一条 o+w 判据，它把自己毒死了
//
// 原来的最后一行是 `fi.Mode().Perm()&0o002 != 0`，想用「目录是不是 world-writable」
// 模拟「降权之后的 Xvfb 写不写得进去」。2026-08-31 真机（AlmaLinux）上翻车：
//
//	drwxr-xr-x. 2 asa-umu-runtime asa-umu-runtime /tmp/.X11-unix
//
// 这个目录是**上一轮那个降权 Xvfb 自己建的** —— 非 root 的 X 服务端建不出 1777
// （chmod 不动），落到 umask 022 就是 0755，属主正是它。于是：
//
//   - 那个用户明明是属主、rwx 俱全、写得进去，o+w 这一条却判它不行；
//   - 更糟的是目录**不存在**时这个函数只看 /tmp，返回 true。第一次启动因此成功，
//     而它一成功就把目录建成了 0755 —— **第一次成功把后续每一次都毒死了**。
//     Xvfb 装在 /usr/bin/Xvfb，preflight 却一口咬定「本机没有可用的 X 显示」。
//
// # 现在的判据：本进程的写入能力，加上「我们改不改得动它」
//
//   - access(2) 的 W_OK。root 绕得过权限位，但绕不过**只读挂载**（返回 EROFS），
//     而只读挂载正是 WSLg 那个坑；asa-server 不以 root 运行时，它就是完整答案。
//   - 目录不存在时看 /tmp —— X 服务端会自己建，我们也会先替它建好。
//
// 降权那一档不在这里猜了。runtimeUserManaged 蕴含 euid == 0，也就是说凡是要降权的
// 场合，**这个目录的权限我们都改得动**：真要起 Xvfb 之前由 ensureX11SocketDir 把它
// 扶正到 X 的约定 1777。判断与动作因此各归各位 —— planDisplay 只读，acquire 才动手。
func x11SocketDirWritable() (bool, string) {
	fi, err := os.Stat(x11SocketDir)
	if err != nil {
		if aerr := syscall.Access("/tmp", writeOK); aerr != nil {
			return false, fmt.Sprintf("/tmp 不可写（%v），Xvfb 建不出 %s", aerr, x11SocketDir)
		}
		return true, ""
	}
	if !fi.IsDir() {
		return false, x11SocketDir + " 存在但不是目录"
	}
	if aerr := syscall.Access(x11SocketDir, writeOK); aerr != nil {
		return false, fmt.Sprintf("%s 不可写（%v；当前权限 %04o）—— 只读挂载（WSLg 就是这么挂的）"+
			"或本进程身份没有写权限，Xvfb 无法在那里发布 socket，"+
			"pressure-vessel 也就没法把显示带进容器", x11SocketDir, aerr, fi.Mode().Perm())
	}
	return true, ""
}

// writeOK is access(2)'s W_OK. Spelled out rather than pulled from x/sys/unix
// so this file keeps to the standard library.
const writeOK = 0x2

// stopManagedDisplay 是 runner.StopManagedDisplay 的实现。
func stopManagedDisplay() { stopManagedXvfb() }

// displayStatus 是 runner.DisplayStatus 的实现。**只读**：自管那一档只报告
// 「打算这么做」以及「现在起来没有」，绝不因为被问一句就拉起一个 X 服务端。
func displayStatus() DisplayInfo {
	cfg := getConfig()
	p, blocked := planDisplay(cfg)
	info := DisplayInfo{
		Available: blocked == "",
		How:       p.How,
		Blocked:   blocked,
		Display:   p.Display,
	}
	if p.Kind == displayManaged {
		info.Managed = true
		if x := currentManagedXvfb(); x != nil {
			info.Display = x.display
			info.How = "自管 Xvfb 虚拟显示 " + x.display
		} else {
			info.How = "自管 Xvfb 虚拟显示（尚未启动，将在需要时拉起）"
		}
	}
	return info
}
