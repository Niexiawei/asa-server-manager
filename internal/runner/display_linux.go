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
// 见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §9。

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
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

// displayTarget 描述「怎么给一个 Wine 进程提供图形显示」：要么把一个已经验证过能连
// 的显示传下去，要么用 xvfb-run 现开一个虚拟显示。
type displayTarget struct {
	Env     []string // 追加到进程环境，如 DISPLAY=:0
	Wrapper []string // 命令前缀，如 [xvfb-run -a -e ... -f ...]
	How     string   // 人类可读的说明
}

// resolveDisplay 决定怎么给进程提供显示，拿不到时第二个返回值说明原因。
//
// **vc_redist 安装与 ArkApi 启动共用这一个函数**：两者需要显示的原因是同一个
// （Wine 的 winex11.drv），分成两套判断只会让它们慢慢漂开。
//
// 三条路，按「越靠前越可控」排列，每一条都是**验证过**的而不是猜的：
//
//  1. 显式的 DISPLAY —— 运维明确指定，且真的握手成功。
//  2. xvfb-run，**但要求 /tmp/.X11-unix 可写**。这条是无头服务器（本项目的主力
//     部署形态）的正路：自带显示、自带 auth、不依赖任何桌面会话，进程树归我们，
//     用户注销也不会把游戏带走。
//  3. 系统里已经跑着的、握手能过的 X 显示。这条是 ① 的补丁：服务进程通常**没有**
//     DISPLAY 环境变量（真机上 /proc/<pid>/environ 里只有 HOME=/root），但机器上
//     可能确实有一个能用的 X 服务。
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
// 起不来 —— 又回到那个「零输出」的失败模式。更糟的是 xvfb-run 在 Xvfb 启动失败时
// **仍然会照常执行命令**，所以光看它的退出码发现不了。既然这个前提可以直接测出来，
// 就不该赌。
//
// **不传 XAUTHORITY**（xvfb-run 自己设的那份除外）。理由与 inheritedEnv 的白名单
// 同源：它经常指向 /run/user/0 下面的路径，而 pressure-vessel 会老老实实地去 bind
// 环境变量点名的每一个路径，降权之后那次 bind 直接让整个容器起不来
// （DBUS_SESSION_BUS_ADDRESS 那次就是这么坑了一整晚，见 inheritedEnv 的注释）。
// 所以 ①③ 只认**不需要 cookie 就能握手**的显示，需要 auth 的场景请走 ②。
func resolveDisplay(cfg Config) (displayTarget, string) {
	if d := os.Getenv("DISPLAY"); d != "" {
		switch {
		case !x11SocketExists(d):
			// 有变量没服务：实测 DISPLAY=:99（无人监听）与不设一样失败，
			// 所以这里不能认它，继续往下找。
		case !x11DisplayUsable(d):
			// socket 在但握不上手，最常见的原因是它要 xauth cookie 而我们
			// 刻意不传 XAUTHORITY。同样继续往下找 —— 有 xvfb 兜底。
		default:
			return displayTarget{Env: []string{"DISPLAY=" + d}, How: "宿主的 X 显示 " + d}, ""
		}
	}

	xvfbRun, xvfbErr := exec.LookPath("xvfb-run")
	if xvfbErr == nil && x11SocketDirWritable() {
		return displayTarget{Wrapper: xvfbRunArgs(cfg, xvfbRun), How: "xvfb-run（虚拟显示）"}, ""
	}

	if d := firstUsableX11Display(); d != "" {
		return displayTarget{
			Env: []string{"DISPLAY=" + d},
			How: "系统里已在运行的 X 显示 " + d + "（本机 " + x11SocketDir + " 只读，起不了 Xvfb）",
		}, ""
	}

	if xvfbErr != nil {
		return displayTarget{}, "本机没有可用的 X 显示，也没有 xvfb-run —— 请" + xvfbInstallHint
	}
	return displayTarget{}, fmt.Sprintf(
		"本机没有可用的 X 显示：%s 不可写（只读挂载？WSLg 就是这么挂的），"+
			"Xvfb 无法在那里发布 socket，pressure-vessel 也就没法把显示带进容器；"+
			"而系统里也没有现成的、不需要 cookie 就能连的 X 服务", x11SocketDir)
}

// xvfbRunArgs 拼 xvfb-run 的命令前缀。
//
// 两个默认值必须改掉，都不是调优而是纠错：
//
//   - `-e`：默认把 Xvfb 的输出扔进 /dev/null。而 xvfb-run 在 Xvfb 起不来时**照样
//     执行命令**，于是失败现场只剩「游戏连不上显示」这一个二手症状。落到文件里，
//     排障时至少有第一手证据。
//   - `-f`：默认写 `./.Xauthority`，也就是**游戏工作目录**（实例镜像的 Win64）。
//     往游戏目录里丢文件是副作用，而且那目录不一定可写。挪到运行时用户的 HOME。
//
// 两个路径都放运行时 HOME 下：降权后的 xvfb-run 就是以那个身份跑的，属主天然正确。
func xvfbRunArgs(cfg Config, xvfbRun string) []string {
	home := runtimeHomeDir(cfg)
	return []string{
		xvfbRun,
		"-a", // 自动挑一个没被占用的显示号，多实例并发启动时不会互相撞车
		"-e", filepath.Join(home, "xvfb.log"),
		"-f", filepath.Join(home, ".Xauthority-xvfb"),
	}
}

// wrap 把显示施加到一条命令上，返回新的 bin/argv/env。
//
// env 的追加放在最后是刻意的：runtimeEnv 会剥掉 XDG_*，DISPLAY 不在其列，但顺序上
// 排最后就不会被将来新增的过滤逻辑吃掉（exec 取同名变量的最后一个）。
func (d displayTarget) wrap(bin string, argv, env []string) (string, []string, []string) {
	outEnv := append(append([]string{}, env...), d.Env...)
	if len(d.Wrapper) == 0 {
		return bin, argv, outEnv
	}
	// argv: [xvfb-run …] <bin> <原 argv...>
	outArgs := append([]string{}, d.Wrapper[1:]...)
	outArgs = append(outArgs, bin)
	outArgs = append(outArgs, argv...)
	return d.Wrapper[0], outArgs, outEnv
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
// （见 resolveDisplay 的注释），所以一个需要 cookie 的显示对我们就是不可用的 ——
// 而它的 socket 文件明明在。拿文件存在当判据，会挑中一个连不上的显示，然后又变成
// 那个「加载器零输出退出」的谜题。握手一次就能把猜测换成事实，代价是几微秒。
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

// x11SocketDirWritable 报告 Xvfb 能不能在 /tmp/.X11-unix 里发布一个**文件** socket。
//
// 目录不存在时 X 服务器会自己建（需要 /tmp 可写）。存在时有两道关，缺一不可：
//
//   - syscall.Access 的 W_OK —— root 会绕过权限位，但**绕不过只读挂载**
//     （返回 EROFS），而只读挂载正是 WSLg 那个坑；
//   - 权限位里的 o+w —— 我们会降权到非 root 用户去跑 Xvfb，root 能写不代表它能写。
//     X 的约定就是 1777。
func x11SocketDirWritable() bool {
	fi, err := os.Stat(x11SocketDir)
	if err != nil {
		return syscall.Access("/tmp", writeOK) == nil
	}
	if !fi.IsDir() {
		return false
	}
	if err := syscall.Access(x11SocketDir, writeOK); err != nil {
		return false
	}
	return fi.Mode().Perm()&0o002 != 0
}

// writeOK is access(2)'s W_OK. Spelled out rather than pulled from x/sys/unix
// so this file keeps to the standard library.
const writeOK = 0x2

// displayStatus 是 runner.DisplayStatus 的实现。
func displayStatus() DisplayInfo {
	d, blocked := resolveDisplay(getConfig())
	return DisplayInfo{
		Available: blocked == "",
		How:       d.How,
		Blocked:   blocked,
	}
}
