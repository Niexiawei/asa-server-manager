//go:build linux

package xvfb

// 自管 Xvfb —— 本项目在 Linux 上提供虚拟显示的方式。
//
// **为什么不是 `xvfb-run`**：那是 Debian 打包时自带的一个 shell 脚本，不是 X.Org 的
// 组件。Fedora / RHEL / Arch 只给 `Xvfb` 服务端二进制，于是「有没有 xvfb-run」这个
// 判据会把那些**明明能开虚拟显示**的机器判成不能。判据必须落在能力上：机器上
// 有没有 `Xvfb`、它起不起得来、起来之后握不握得上手——三件事都能直接测出来。
//
// 自管顺带治好了 xvfb-run 的三个毛病：
//
//   - Xvfb 起不来时它**照样执行命令**，于是失败现场只剩「游戏连不上显示」这个二手
//     症状。这里改成「握手过不了就直接让启动失败」，并把 Xvfb 自己的错误原文交给用户。
//   - 它默认把 Xvfb 的输出丢 /dev/null。
//   - 它默认把 auth 文件写进**游戏工作目录**。
//
// 见 docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md。
//
// # 生命周期：跟着宿主进程走
//
// 一个 Manager 一个单例（多个调用方共用一个显示），用前握手做健康检查，
// 死了由看门狗补起来，**宿主进程退出时一起退出**。
//
// 三层保证，从软到硬：
//
//  1. **显式停**：Stop()，由调用方在自己的退出路径上调。确定性的那一层，还能留下日志。
//  2. **Pdeathsig=SIGTERM**：宿主进程被 SIGKILL、panic、OOM 时没人能执行第 1 层，
//     由内核代劳。**这要求 fork 它的那个 OS 线程活得和进程一样久** —— Linux 的
//     parent-death signal 跟的是创建它的**线程**，而 Go 的 M 空闲时会退出，届时
//     Xvfb 会被莫名其妙杀掉。因此所有 Xvfb 都由 spawnLoop 那个
//     runtime.LockOSThread 且永不返回的 goroutine 来 fork（见那里的注释）。
//  3. **认领**：万一前两层都没生效（或者同机上另一个进程已经起过一个），
//     下一次启动通过 Config.StatePath 把它认回来而不是再起一个。
//     **认领来的不归我们杀**（Stop 对它是空操作）—— 它是别人的进程，
//     杀了会把那个进程正在服务的调用方弄死。
import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	// defaultScreen：一块屏幕、24 位色（帧缓冲约 5MB）。可由 Config.Screen 覆盖。
	defaultScreen = "1280x1024x24"
	// displayFD 是 -displayfd 用的文件描述符号。Go 的 cmd.ExtraFiles[0] 固定落在 fd 3。
	displayFD = 3
	// startTimeout：从 fork 到显示可握手。本地 X 服务起来是百毫秒级，给足十秒
	// 纯粹是让慢机器/冷缓存不至于误判。
	startTimeout = 10 * time.Second
	probeGap     = 50 * time.Millisecond
	stopTimeout  = 3 * time.Second
	// logMaxBytes：xvfb.log 超过这个大小就在下次启动时截断重来。它只在排障时
	// 被读，没有保留历史的价值，但不能让它无限长。
	logMaxBytes = 1 << 20
	// 老 X server（< 1.13）没有 -displayfd，回退成自己挑号时从这里开始试。
	// 100 往上是约定俗成的「虚拟显示」区间，不容易撞上真实桌面会话的 :0/:1。
	firstDisplay = 100
	displayTries = 10
	// watchInterval 是看门狗盯**认领来的**那种孤儿的间隔 —— 它不是本进程的子进程，
	// 没有 Wait 通道可等，只能定期看一眼。自己 fork 的那种是事件驱动的，不轮询。
	watchInterval = 30 * time.Second
	// x11ProbeTimeout 与本包主文件里的 ProbeTimeout 是同一件事，此处不重复定义。

	// socketDirMode 是 X 的约定：1777 —— 全局可写，加 sticky 位保证谁的 socket 只有
	// 谁删得掉。root 起的 X 服务端建出来就是这个。
	writeOK = 0x2 // access(2) 的 W_OK
)

const socketDirMode = os.ModeSticky | 0o777

// restartBackoff 是看门狗补起 Xvfb 的重试节奏。有限次数：连着几次都起不来说明
// 是环境问题（字体没了、/tmp 满了），无限重试只会刷屏 —— 下一次真正需要显示的
// 调用会再试一次，并把原因原原本本报给用户。
var restartBackoff = []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second}

// ErrNoBinary：本机找不到 Xvfb 可执行文件。
var ErrNoBinary = errors.New("Xvfb not found")

// errNoDisplayFD：这个 Xvfb 不认识 -displayfd（X server < 1.13），换老办法。
var errNoDisplayFD = errors.New("Xvfb does not support -displayfd")

// noSuchID is a uid/gid that matches nothing on any Linux system (it is
// (uid_t)-1, the "no change" sentinel of setuid/chown, never a real
// account). ChildIDs hands it back when the runtime user doesn't exist yet.
const noSuchID = ^uint32(0)

// managedXvfb 是一个进程拉起（或从上一轮认领）的 Xvfb 服务端。
type managedXvfb struct {
	display string // ":100"
	pid     int
	log     string // xvfb.log 路径，失败诊断用

	// cmd/exited 只有自己 fork 出来的那种才有；认领来的孤儿两者都是零值。
	// waitErr 在 close(exited) **之前**写、只在收到 close 之后读，happens-before 成立。
	cmd     *exec.Cmd
	exited  chan struct{}
	waitErr error
	// logStart 是本次启动前 xvfb.log 的长度。日志是追加写的，诊断时只该看这次
	// 启动之后的内容，否则会把上几次的错误当成这次的。
	logStart int64
	// intentional 标记「是我们自己停的」，看门狗见到它就不再补起 —— 否则
	// 宿主进程关停时会跟自己抢着重启一个马上又要被杀掉的 X 服务。
	intentional atomic.Bool
	// adopted 标记「这是别的进程起的」。它不归我们杀，也没有 cmd/exited。
	adopted bool
}

// --- 发现 -----------------------------------------------------------------------

// extraPaths 是 PATH 之外再兜的一层：systemd 服务的 PATH 可能被裁剪过，而 Xvfb
// 的位置在各发行版是固定的几个。
var extraPaths = []string{
	"/usr/bin/Xvfb",
	"/usr/X11R6/bin/Xvfb",
	"/usr/local/bin/Xvfb",
}

// binaryPath 解析出 Xvfb 的路径：Config.Bin 优先，其次 PATH，最后兜底路径。
func binaryPath(cfg Config) (string, error) {
	if cfg.Bin != "" {
		if !isExecutableFile(cfg.Bin) {
			return "", fmt.Errorf("配置的 Xvfb 路径 %s 不存在或不可执行", cfg.Bin)
		}
		return cfg.Bin, nil
	}
	if p, err := exec.LookPath("Xvfb"); err == nil {
		return p, nil
	}
	for _, p := range extraPaths {
		if isExecutableFile(p) {
			return p, nil
		}
	}
	return "", ErrNoBinary
}

// BinaryPath resolves Xvfb's path the same way Acquire will: Config.Bin
// first, then PATH, then a few well-known fallback locations. Read-only.
func (m *Manager) BinaryPath() (string, error) {
	return binaryPath(m.config())
}

func isExecutableFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Mode().Perm()&0o111 != 0
}

// --- 命令行 ---------------------------------------------------------------------

func screen(cfg Config) string {
	if cfg.Screen != "" {
		return cfg.Screen
	}
	return defaultScreen
}

// args 是首选形态：让 X 服务端**自己挑一个空闲显示号**并把号码写回 fd。
//
// 这是唯一没有 TOCTOU 的挑号方式——自己扫 /tmp/.X11-unix/X<n> 再启动，两个调用方并发
// 时会撞车。注意用了 -displayfd 就**不能再给显示号位置参数**，两者互斥。
//
// 其余四个参数：
//   - -screen 0 <WxHxD>：一块屏幕就够。
//   - -nolisten tcp：只经 /tmp/.X11-unix 的 unix socket 暴露，不开网络监听。
//   - -noreset：最后一个客户端断开时不重置服务端。Wine 短暂断开重连的间隙不该
//     把显示状态清掉。
//   - -ac：显式关闭访问控制。**不传 -auth 是有意的**：本包刻意不传 XAUTHORITY，
//     而「无认证握手能过」正是判断显示可用与否的唯一手段——带上 cookie 自己就
//     探测不到自己了。
func args(cfg Config, fd int) []string {
	return append([]string{"-displayfd", strconv.Itoa(fd)}, commonArgs(cfg)...)
}

// argsForDisplay 是 -displayfd 不被支持时的老形态：显示号写在命令行第一位。
func argsForDisplay(cfg Config, display string) []string {
	return append([]string{display}, commonArgs(cfg)...)
}

func commonArgs(cfg Config) []string {
	return []string{
		"-screen", "0", screen(cfg),
		"-nolisten", "tcp",
		"-noreset",
		"-ac",
	}
}

// --- 单例 -----------------------------------------------------------------------

// Acquire returns a display that's currently usable — spawning, or adopting
// an orphaned one, if necessary. Blocking; a cold start can take up to
// ~10s.
func (m *Manager) Acquire() (string, error) {
	x, err := m.ensure()
	if err != nil {
		return "", err
	}
	return x.display, nil
}

// ensure 返回一个**当下确实能连**的自管显示，必要时现拉起一个。
func (m *Manager) ensure() (*managedXvfb, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg := m.config()
	bin, binErr := binaryPath(cfg)

	if cur := m.current.Load(); cur != nil {
		if DisplayUsable(cur.display) {
			return cur, nil
		}
		// 中途死了（被 OOM、被人 kill、自己崩了）。收尸后重开，这一档因此是可自愈的。
		cur.stop()
		m.current.Store(nil)
	}

	if binErr != nil {
		return nil, binErr
	}

	if !m.adoptTried {
		m.adoptTried = true
		if x := adopt(cfg); x != nil {
			m.current.Store(x)
			go m.watch(x)
			return x, nil
		}
	}

	// 起 Xvfb 之前先把 SocketDir 备齐 —— 这是 acquire 这一侧的动作，只读的判断
	// 侧（SocketDirState）碰不得。
	if err := m.ensureSocketDir(cfg); err != nil {
		return nil, err
	}

	x, err := m.start(cfg, bin)
	if err != nil {
		return nil, err
	}
	m.current.Store(x)
	writeState(cfg, x)
	go m.watch(x)
	return x, nil
}

// Stop shuts down the Xvfb this Manager started, if any. Idempotent, a
// no-op if nothing was ever started, and a no-op for a display adopted from
// another process (that one is not this Manager's to kill).
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	x := m.current.Load()
	if x == nil {
		return
	}
	// 先置位再停：看门狗醒来时必须能看出「这是计划内的」，否则它会在关停途中
	// 抢着补起一个马上又要被杀的 X 服务。
	x.intentional.Store(true)
	m.current.Store(nil)
	m.restoreSocketDirRO()
	if x.adopted {
		return
	}
	x.stop()
}

// watch 是 Xvfb 的看门狗：它一死就补起一个。
//
// 它**救不了正在跑的那个调用方** —— 那个进程的 X 连接已经断了，新起的还是
// 另一个显示号。看门狗的价值有两条：① 把「显示莫名其妙没了」变成日志里
// 一句「Xvfb 于某时退出，原因是…」；② 让下一次调用不必现等一个冷启动的 X 服务。
func (m *Manager) watch(x *managedXvfb) {
	x.waitExit()
	if x.intentional.Load() || m.current.Load() != x {
		return // 我们自己停的，或者已经被换掉了 —— 都不该由这里插手
	}

	for range restartBackoff {
		time.Sleep(restartBackoff[0])
		if x.intentional.Load() || m.current.Load() != x {
			return
		}
		if _, err := m.ensure(); err == nil {
			return // ensure 会为新的那个另起一只看门狗
		}
	}
}

// waitExit 等到这个 Xvfb 真的没了。自己 fork 的那种等 Wait 通道（事件驱动、零轮询）；
// 认领来的孤儿不是本进程的子进程，Wait 不到，只能定期看一眼。
func (x *managedXvfb) waitExit() {
	if x.exited != nil {
		<-x.exited
		return
	}
	for processIsXvfb(x.pid) {
		time.Sleep(watchInterval)
	}
}

// Status reports the currently self-managed display, if any — read-only and
// offline. Diagnostic callers use this; it never spawns anything just
// because it was asked.
func (m *Manager) Status() Info {
	x := m.current.Load()
	if x == nil {
		return Info{}
	}
	return Info{Running: true, Display: x.display, PID: x.pid}
}

// --- socket 目录 ----------------------------------------------------------------

// ensureSocketDir 保证 SocketDir 存在，且**将要跑 Xvfb 的那个身份**写得进去。
//
// 为什么需要它：非 root 的 X 服务端建不出 1777（chmod 那一步会失败），落到 umask 022
// 就是 0755，属主是那个降权用户。于是「降权 Xvfb 第一次启动」会留下一个只有它自己
// 写得进去的目录 —— 换个运行时用户、或者切换到以 root 运行游戏，下一次就建不出
// socket 了。这里就是替 X 服务端把约定补上。
//
// **只在需要时动手**：目录缺了才建，存在但那个身份写不进去才 chmod，非 root 时
// 直接返回：我们既建不出 1777 也 chmod 不动别人的目录。
//
// 归属：它属于 acquire 那一侧（会改文件系统），只由 ensure 在真要拉起 Xvfb 之前
// 调用；SocketDirState 一律碰不到它。
func (m *Manager) ensureSocketDir(cfg Config) error {
	if os.Geteuid() != 0 {
		return nil
	}
	if err := m.remountSocketDirRW(cfg); err != nil {
		return err
	}
	fi, err := os.Stat(SocketDir)
	switch {
	case os.IsNotExist(err):
		if mkErr := os.Mkdir(SocketDir, socketDirMode); mkErr != nil && !os.IsExist(mkErr) {
			return fmt.Errorf("创建 %s 失败: %w", SocketDir, mkErr)
		}
		// Mkdir 的 mode 会被 umask 削掉，sticky 位也未必留得住，显式再来一次。
		if chErr := os.Chmod(SocketDir, socketDirMode); chErr != nil {
			return fmt.Errorf("把 %s 设为 1777 失败: %w", SocketDir, chErr)
		}
		return nil
	case err != nil:
		return fmt.Errorf("检查 %s 失败: %w", SocketDir, err)
	case !fi.IsDir():
		return fmt.Errorf("%s 存在但不是目录", SocketDir)
	}

	uid, gid, managed := cfg.childIDs()
	if !managed || statWritableBy(fi, uid, gid) {
		return nil // Xvfb 以本进程身份跑，或者那个身份本来就写得进去
	}
	if err := os.Chmod(SocketDir, socketDirMode); err != nil {
		return fmt.Errorf("把 %s 改为 1777 失败: %w", SocketDir, err)
	}
	return nil
}

// remountSocketDirRW 把只读挂载的 SocketDir 重新挂载为可写。
//
// # 这是 WSL 上唯一能让自管 Xvfb 成立的一步
//
// X 的 socket 路径写死在 xtrans 里，没有任何环境变量能改，所以「让 Xvfb 把 socket
// 建到别处」这条路不存在，只能让那个目录可写。而 WSLg 恰恰把它挂成只读 tmpfs
// （`none on /tmp/.X11-unix type tmpfs (ro,relatime)`），于是在 WSL 上自管 Xvfb
// 从来轮不到，必然落到 WSLg 自己的 :0。见 docs/ALWAYS_MANAGED_XVFB_DISPLAY_PLAN.md §4。
//
// # 为什么是 remount 而不是盖一层新 tmpfs
//
// remount 只改这个挂载点的读写属性，**不遮挡任何已经存在的 socket** —— WSLg 自己的
// X0 原样保留。盖一层新 tmpfs 则会连 X0 一起遮掉，除非再单独把它 bind 回来。
//
// # 判据是能力不是发行版
//
// 触发条件是**上一步 access(2) 返回了 EROFS**，不是「这台机器是不是 WSL」。
//
// 目录本来就可写时是**空操作**。
func (m *Manager) remountSocketDirRW(cfg Config) error {
	aerr := syscall.Access(SocketDir, writeOK)
	if aerr == nil || !errors.Is(aerr, syscall.EROFS) {
		// 已经可写，或者不是只读挂载的问题（目录不存在、或别的错误）。后者交给下游
		// 按老路处理：目录缺了会被建出来，别的错误会在那里被如实报出。
		return nil
	}
	if !cfg.AllowX11Remount {
		return fmt.Errorf("%s 是只读挂载（WSLg 就是这么挂的），Xvfb 建不出 socket；"+
			"AllowX11Remount 为 false，不尝试把它重新挂载为可写。"+
			"可点名一个现成的 X 显示", SocketDir)
	}
	if err := syscall.Mount("", SocketDir, "", syscall.MS_REMOUNT|syscall.MS_BIND, ""); err != nil {
		return fmt.Errorf("%s 是只读挂载（WSLg 就是这么挂的），Xvfb 建不出 socket；"+
			"把它重新挂载为可写也失败了（%v）。可点名一个现成的 X 显示"+
			"（WSLg 的是 :0），或关闭 AllowX11Remount 以省掉这次尝试",
			SocketDir, err)
	}
	m.remounted.Store(true)
	return nil
}

// restoreSocketDirRO 把 remountSocketDirRW 改过的挂载点还原回只读。
//
// 只在我们真的改过时才动手。安全性来自一个事实：还原之后**已经建好的 socket
// 照常能连** —— 连一个已存在的 unix socket 不需要对它所在的目录有写权限，只有
// 新建才需要。best-effort：还原失败不返回错误，下一次调用会再 remount 一次。
func (m *Manager) restoreSocketDirRO() {
	if !m.remounted.CompareAndSwap(true, false) {
		return
	}
	_ = syscall.Mount("", SocketDir, "", syscall.MS_REMOUNT|syscall.MS_BIND|syscall.MS_RDONLY, "")
}

// statWritableBy 按 POSIX 的顺序算某个 uid/gid 对这个目录的写权限：属主位优先，
// 其次属组位，都不沾边才看 other 位。**属主匹配时属组位不参与**，哪怕它更宽松 ——
// 内核就是这么判的，用 o+w 一位当近似正是上一版翻车的地方。
//
// 不考虑附加组：这个包只认单一 gid。
func statWritableBy(fi os.FileInfo, uid, gid uint32) bool {
	perm := fi.Mode().Perm()
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return perm&0o002 != 0
	}
	switch {
	case st.Uid == uid:
		return perm&0o200 != 0
	case st.Gid == gid:
		return perm&0o020 != 0
	}
	return perm&0o002 != 0
}

// SocketDirState reports whether Xvfb could actually publish a socket in
// SocketDir right now — read-only, safe to call against a live process.
//
// # 这里曾经有一条 o+w 判据，它把自己毒死了
//
// 原来的判据想用「目录是不是 world-writable」模拟「降权之后的 Xvfb 写不写得进去」，
// 在一台降权 Xvfb 自己建过目录（0755，属主是它自己）的机器上翻车：属主明明写得进去，
// o+w 却判它不行；更糟的是目录不存在时只看 /tmp、返回可写，第一次成功就把目录建成
// 0755 —— 毒死了后续每一次。
//
// # 现在的判据：本进程的写入能力，加上「改不改得动它」
//
//   - access(2) 的 W_OK。root 绕得过权限位，但绕不过**只读挂载**（返回 EROFS）。
//   - 目录不存在时看 /tmp —— X 服务端会自己建，我们也会先替它建好。
//
// 降权那一档不在这里猜：真要起 Xvfb 之前由 ensureSocketDir 把它扶正到 1777。
// 判断与动作因此各归各位。
func (m *Manager) SocketDirState() SocketDirState {
	cfg := m.config()
	fi, err := os.Stat(SocketDir)
	if err != nil {
		if aerr := syscall.Access("/tmp", writeOK); aerr != nil {
			return SocketDirState{Why: fmt.Sprintf("/tmp 不可写（%v），Xvfb 建不出 %s", aerr, SocketDir)}
		}
		return SocketDirState{Writable: true}
	}
	if !fi.IsDir() {
		return SocketDirState{Why: SocketDir + " 存在但不是目录"}
	}

	aerr := syscall.Access(SocketDir, writeOK)
	if aerr == nil {
		return SocketDirState{Writable: true}
	}
	if errors.Is(aerr, syscall.EROFS) {
		why := SocketDir + " 是只读挂载（WSLg 就是这么挂的），Xvfb 建不出文件 socket"
		if os.Geteuid() == 0 && cfg.AllowX11Remount {
			return SocketDirState{Fixable: true, Why: why}
		}
		if !cfg.AllowX11Remount {
			return SocketDirState{Why: why + "；AllowX11Remount 为 false，不尝试把它重新挂载为可写"}
		}
		return SocketDirState{Why: why + "；重新挂载它需要 root"}
	}
	return SocketDirState{Why: fmt.Sprintf("%s 不可写（%v；当前权限 %04o）—— 本进程身份没有写权限",
		SocketDir, aerr, fi.Mode().Perm())}
}

// --- 启动 -----------------------------------------------------------------------

func (m *Manager) start(cfg Config, bin string) (*managedXvfb, error) {
	x, err := m.startDisplayFD(cfg, bin)
	if err == nil {
		return x, nil
	}
	if !errors.Is(err, errNoDisplayFD) {
		return nil, err
	}
	return m.startScanned(cfg, bin)
}

func (m *Manager) startDisplayFD(cfg Config, bin string) (*managedXvfb, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("创建 displayfd 管道失败: %w", err)
	}
	defer r.Close()

	x, startErr := m.spawn(cfg, bin, args(cfg, displayFD), w)
	// 父进程手里这一份写端必须立刻关掉，否则子进程死了 Read 也等不到 EOF。
	_ = w.Close()
	if startErr != nil {
		return nil, startErr
	}

	num, err := readDisplayFD(r, startTimeout)
	if err != nil {
		tail := x.logTail()
		x.stop()
		if rejectedDisplayFD(tail) {
			return nil, errNoDisplayFD
		}
		return nil, startError(x, tail, fmt.Errorf("没有读到 Xvfb 写回的显示号: %w", err))
	}
	x.display = ":" + num

	if !x.waitReady(startTimeout) {
		tail := x.logTail()
		x.stop()
		return nil, startError(x, tail,
			fmt.Errorf("显示 %s 在 %s 内没有变成可连接", x.display, startTimeout))
	}
	return x, nil
}

// startScanned 是老 X server 的回退路径：自己挑号，撞车了换一个再试。
func (m *Manager) startScanned(cfg Config, bin string) (*managedXvfb, error) {
	var lastErr error
	for n := firstDisplay; n < firstDisplay+displayTries; n++ {
		display := ":" + strconv.Itoa(n)
		if displayTaken(n) {
			continue
		}
		x, err := m.spawn(cfg, bin, argsForDisplay(cfg, display), nil)
		if err != nil {
			return nil, err
		}
		x.display = display
		if x.waitReady(startTimeout) {
			return x, nil
		}
		tail := x.logTail()
		x.stop()
		lastErr = startError(x, tail, fmt.Errorf("显示 %s 没有起来", display))
		// 号被别人抢了才值得换一个重试；缺字体那类错误重试十次也是十次一样的失败。
		if !displayInUse(tail) {
			return nil, lastErr
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("从 :%d 起连续 %d 个显示号都已被占用", firstDisplay, displayTries)
	}
	return nil, lastErr
}

// spawn fork 出 Xvfb 并接管它的输出，不负责判断显示有没有真的起来。
func (m *Manager) spawn(cfg Config, bin string, args []string, extra *os.File) (*managedXvfb, error) {
	logPath := logPath(cfg)
	logFile, err := openLog(logPath)
	if err != nil {
		return nil, err
	}
	// 子进程拿到的是自己那一份 fd，父进程这一份用完即关。
	defer logFile.Close()
	logStart, _ := logFile.Seek(0, io.SeekEnd)

	// 与游戏进程同一个身份：socket 与 lock 文件的属主才对得上，也没理由让一个常驻的、
	// 无认证的 X 服务端跑在 root 下。它能不能在 SocketDir 里写，由 ensureSocketDir
	// 在上一步实际保证（而不是靠猜权限位）。
	cred, err := cfg.credential()
	if err != nil {
		return nil, fmt.Errorf("解析运行时用户失败: %w", err)
	}

	cmd := exec.Command(bin, args...)
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Env = env(cfg)
	if extra != nil {
		cmd.ExtraFiles = []*os.File{extra} // → 子进程的 fd 3
	}
	// Setsid：Xvfb 是常驻服务，不该挂在宿主进程的控制终端上，也不该被某次启动的
	// KillTree 顺手收走。
	//
	// Pdeathsig=SIGTERM：宿主进程被 SIGKILL / panic / OOM 时没人能执行显式停止，
	// 由内核代劳。用 SIGTERM 而不是 SIGKILL，好让 X 服务端自己把
	// SocketDir/X<n> 与 /tmp/.X<n>-lock 清干净 —— 留下 lock 文件会让回退路径的
	// 挑号逻辑白白跳过那个号。
	//
	// Go 把 Pdeathsig 设在切换 Credential **之后**（setuid 会清掉这个设置），
	// 并且会复查父进程是否已经先一步死了，所以降权与它可以并存。
	cmd.SysProcAttr = sysProcAttr(cred)

	// 必须由那个永不退出的专用线程来 fork —— 见 spawnLoop。
	if err := m.spawnStart(cmd); err != nil {
		return nil, fmt.Errorf("启动 %s 失败: %w", bin, err)
	}

	x := &managedXvfb{
		pid:      cmd.Process.Pid,
		log:      logPath,
		cmd:      cmd,
		exited:   make(chan struct{}),
		logStart: logStart,
	}
	// 必须有人 Wait，否则 Xvfb 死掉之后会留一个僵尸挂在宿主进程名下。
	go func() {
		x.waitErr = cmd.Wait()
		close(x.exited)
	}()
	return x, nil
}

// --- 专用 fork 线程 ---------------------------------------------------------------
//
// Linux 的 parent-death signal（PR_SET_PDEATHSIG）跟的是**创建子进程的那个线程**，
// 不是进程：那个线程一退出，子进程立刻收到信号。而 Go 的调度器会在 M 空闲时把它
// 回收掉 —— 于是「随手 fork 一个带 Pdeathsig 的进程」在 Go 里是个定时炸弹：
// 某个与 Xvfb 毫无关系的时刻，某个线程退出，正在服务的 X 服务端被无缘无故杀掉。
//
// 解法是给 fork 这件事一个**永不退出的专属线程**：下面这个 goroutine
// runtime.LockOSThread 之后永远不 Unlock、永远不 return，于是它绑定的那个 OS 线程
// 活得和进程一样久。所有 Xvfb 都由它 fork，Pdeathsig 的语义就从「某个线程死了」
// 收敛成了「宿主进程死了」—— 这正是我们要的那个保证。

type spawnReq struct {
	cmd  *exec.Cmd
	done chan error
}

// spawnStart 把 cmd.Start() 派给专用线程执行，同步等它的结果。
func (m *Manager) spawnStart(cmd *exec.Cmd) error {
	m.spawnOnce.Do(func() {
		m.spawnReqs = make(chan *spawnReq)
		go spawnLoop(m.spawnReqs)
	})
	req := &spawnReq{cmd: cmd, done: make(chan error, 1)}
	m.spawnReqs <- req
	return <-req.done
}

// spawnLoop 永不返回，理由见上面那段。别给它加退出条件。
func spawnLoop(reqs chan *spawnReq) {
	runtime.LockOSThread()
	for req := range reqs {
		req.done <- req.cmd.Start()
	}
}

// sysProcAttr 是自管 Xvfb 的进程属性。单独抽出来是为了能被单测钉住：
// Pdeathsig 是「Xvfb 跟着宿主进程一起走」这条保证里**唯一不依赖代码被执行到**
// 的那一层（宿主进程被 SIGKILL 时谁都跑不了，只剩内核）。
func sysProcAttr(cred *syscall.Credential) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setsid:     true,
		Credential: cred,
		Pdeathsig:  syscall.SIGTERM,
	}
}

// env：Xvfb 不进任何容器，没有环境白名单那类顾虑，但也没有理由把整个
// os.Environ() 传下去。给最小集合。
func env(cfg Config) []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	return []string{"HOME=" + cfg.homeDir(), "PATH=" + path}
}

// stop 停掉这个 Xvfb。两种调用场景：起失败/已死的收尸，以及显式停止（Manager.Stop）。
//
// **认领来的直接不管**：那是另一个进程 fork 的，它自己的 Pdeathsig 与退出路径会
// 负责；我们越俎代庖会把对方正在服务的调用方弄死。
func (x *managedXvfb) stop() {
	if x == nil || x.pid <= 0 || x.adopted {
		return
	}
	if x.cmd == nil || x.cmd.Process == nil {
		return
	}
	_ = x.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-x.exited:
	case <-time.After(stopTimeout):
		_ = x.cmd.Process.Kill()
	}
}

// --- 就绪判定与诊断 --------------------------------------------------------------

// waitReady 轮询到显示真的能握手为止。
//
// 这一步是自管相对 xvfb-run 最大的收益：xvfb-run 在 Xvfb 起不来时照跑命令，
// 我们则在显示没就绪时直接让启动失败。判据复用 DisplayUsable 的无认证握手。
//
// 进程已经退出就立刻收手，不等满超时：缺字体那类失败是**秒退**的，
// 干等十秒只是让用户多盯十秒同一个结论。
func (x *managedXvfb) waitReady(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if DisplayUsable(x.display) {
			return true
		}
		if x.dead() || time.Now().After(deadline) {
			return false
		}
		time.Sleep(probeGap)
	}
}

// unusableReason 解释「握手过不去」的原因：进程死了就报它的退出状态，
// 还活着就如实说 —— 后者是更值得警惕的一种（多半是 X socket 被人删了）。
func (x *managedXvfb) unusableReason() string {
	if !x.dead() {
		return "进程还在，但显示连不上了"
	}
	if x.waitErr != nil {
		return "进程已退出: " + x.waitErr.Error()
	}
	return "进程已退出"
}

// dead 报告进程是否已经退出（认领来的孤儿没有 exited 通道，只能靠握手判断）。
func (x *managedXvfb) dead() bool {
	if x.exited == nil {
		return false
	}
	select {
	case <-x.exited:
		return true
	default:
		return false
	}
}

// readDisplayFD 读 X 服务端经 -displayfd 写回来的显示号。
func readDisplayFD(r *os.File, timeout time.Duration) (string, error) {
	_ = r.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 32)
	n, err := r.Read(buf)
	if n == 0 {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return "", err
	}
	return parseDisplayFD(string(buf[:n]))
}

// parseDisplayFD 把 "100\n" 变成 "100"。
func parseDisplayFD(s string) (string, error) {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("Xvfb 写回的显示号是空的")
	}
	if _, err := strconv.Atoi(line); err != nil {
		return "", fmt.Errorf("Xvfb 写回的显示号 %q 不是数字", line)
	}
	return line, nil
}

// rejectedDisplayFD 判断这次失败是不是「这个 X server 不认识 -displayfd」。
// 老 X server（< 1.13）会打印用法并退出，而不是给出别的什么线索。
func rejectedDisplayFD(logTail string) bool {
	l := strings.ToLower(logTail)
	if !strings.Contains(l, "displayfd") {
		return false
	}
	for _, m := range []string{"unrecognized", "unknown option", "invalid option", "usage:"} {
		if strings.Contains(l, m) {
			return true
		}
	}
	return false
}

// displayInUse 判断失败是不是「这个显示号被别人占了」——只有这一种值得换号重试。
func displayInUse(logTail string) bool {
	l := strings.ToLower(logTail)
	return strings.Contains(l, "server is already active") ||
		strings.Contains(l, "already active for display")
}

// failureHint 把已知的失败模式翻译成能照做的一句话。
//
// 字体那条是最值得单独认的：最小化安装的发行版常常没有基础字体，Xvfb 会直接
// fatal 退出。这个问题在 xvfb-run 时代同样存在，只是被 /dev/null 吞掉了。
func failureHint(logTail string) string {
	l := strings.ToLower(logTail)
	switch {
	case strings.Contains(l, "could not open default font"), strings.Contains(l, "could not open font"):
		return FontHint
	case strings.Contains(l, "cannot establish any listening sockets"),
		strings.Contains(l, "read-only file system"):
		return "Xvfb 无法在 " + SocketDir + " 建 socket（只读挂载或权限不足）"
	case strings.Contains(l, "server is already active"):
		return "该显示号已被占用"
	}
	return ""
}

func startError(x *managedXvfb, tail string, cause error) error {
	msg := fmt.Sprintf("Xvfb 启动失败：%v", cause)
	if hint := failureHint(tail); hint != "" {
		msg += "。" + hint
	}
	if tail != "" {
		msg += fmt.Sprintf("\n%s 的末尾输出：\n%s", x.log, tail)
	}
	return errors.New(msg)
}

// --- 日志 -----------------------------------------------------------------------

// logPath：落运行时用户的 HOME，与 xvfb-run 时代的 -e 指向同一个文件名。
func logPath(cfg Config) string {
	return filepath.Join(cfg.homeDir(), "xvfb.log")
}

// openLog 以追加方式打开日志；父进程持有 fd 再交给子进程，所以文件属主是谁
// 都不影响降权后的 Xvfb 写入。超过上限就截断重来（这个文件没有保留历史的价值）。
func openLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建 %s 的目录失败: %w", path, err)
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > logMaxBytes {
		_ = os.Truncate(path, 0)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("打开 %s 失败: %w", path, err)
	}
	return f, nil
}

// logTail 只读**本次启动之后**写进去的内容：日志是追加的，把上几次的错误当成
// 这次的会把排障带到沟里。
func (x *managedXvfb) logTail() string {
	const maxTail = 4 << 10
	f, err := os.Open(x.log)
	if err != nil {
		return ""
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	from := x.logStart
	if size := fi.Size(); size-from > maxTail {
		from = size - maxTail
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// --- 孤儿认领 -------------------------------------------------------------------

// state 是 Config.StatePath 的内容：上一次是谁、在哪个显示上。
type state struct {
	Display string `json:"display"`
	PID     int    `json:"pid"`
	Started string `json:"started"`
}

// adopt 认领上一轮留下的 Xvfb。
//
// 存在的理由：宿主进程退出时不杀 Xvfb，所以每次重启都会多一个——除非能把上一个
// 认回来。三道关：记录里的 pid 还活着、它确实是 Xvfb、那个显示握手能过。三条都
// 满足才认，认错了最坏也只是用了别人的 Xvfb（照样能用）。
func adopt(cfg Config) *managedXvfb {
	st, err := readState(cfg)
	if err != nil || st.PID <= 0 || st.Display == "" {
		return nil
	}
	if !processIsXvfb(st.PID) || !DisplayUsable(st.Display) {
		return nil
	}
	return &managedXvfb{display: st.Display, pid: st.PID, log: logPath(cfg), adopted: true}
}

func readState(cfg Config) (state, error) {
	var st state
	if cfg.StatePath == "" {
		return st, errors.New("no state path configured")
	}
	data, err := os.ReadFile(cfg.StatePath)
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(data, &st)
	return st, err
}

func writeState(cfg Config, x *managedXvfb) {
	if cfg.StatePath == "" {
		return
	}
	data, err := json.Marshal(state{
		Display: x.display,
		PID:     x.pid,
		Started: time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	_ = os.WriteFile(cfg.StatePath, data, 0o644)
}

// processIsXvfb 判断 pid 还活着且确实是个 Xvfb（防 pid 复用认错人）。
func processIsXvfb(pid int) bool {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == "Xvfb"
}

// displayTaken：socket 或 lock 文件在，就当这个号已经被占了。
// 只有 -displayfd 用不了的回退路径才需要它。
func displayTaken(n int) bool {
	num := strconv.Itoa(n)
	return pathExists(filepath.Join(SocketDir, "X"+num)) ||
		pathExists(filepath.Join("/tmp", ".X"+num+"-lock"))
}


// atomicBoolImpl is sync/atomic.Bool. Defined via import below.
