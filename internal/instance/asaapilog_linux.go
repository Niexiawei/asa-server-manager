//go:build linux

package instance

import (
	"asa-server/pkg/console"
	"asa-server/pkg/iox"
	"asa-server/pkg/logger"
	"asa-server/pkg/tail"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ArkApi（AsaApiLoader.exe）的业务日志**不走控制台**，只写文件：
//
//	<游戏 exe 目录>/logs/ArkApi_<wine 侧 PID>_<YYYY-MM-DD_HH-MM>.log
//
// 实例场景下「游戏 exe 目录」是镜像里的 ShooterGame/Binaries/Win64/，每次启动一个
// 新文件，轮转由 ArkApi 自己按 config.json 的 DeleteOldLogs 处理。
//
// 这件事在 Linux 上很要命：这里 PTY 里跑的是 umu-run 整条包装链，不是加载器本体，
// 所以实例的 arkAsaApi.log 收到的全是 umu/pressure-vessel/Proton 的噪声，
// ArkApi 的内容一行都没有。而且「把噪声过滤掉」是行不通的 —— 过滤完是空的。
// 见 docs/ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md §1。
//
// 「目录里找最新匹配文件，且要等它出现」的机制部分在 asa-server/pkg/tail
// （WaitNewest）；「持续转抄」的机制部分在 asa-server/pkg/iox（Relay）。本文件
// 只留 ArkApi 日志自己的命名规则与调用胶水。

// arkApiLogDirRel 是 ArkApi 日志目录相对游戏根目录的位置。
const arkApiLogDirRel = "ShooterGame/Binaries/Win64/logs"

// ArkApi 日志的文件名形状。用宽松的前后缀匹配而不是精确解析 PID 与时间戳：
// 上游改了格式时我们只会挑错文件，而不是一个都挑不到。
const (
	arkApiLogPrefix = "ArkApi_"
	arkApiLogSuffix = ".log"
)

// ArkApi 日志转抄协程的节奏。
const (
	// arkApiLogPollInterval 是「日志文件出现了没有」的轮询间隔，也是文件读到 EOF
	// 之后的重试间隔。ArkApi 的写入是低频的（每条日志一行），1 秒的延迟对面板足够，
	// 而更密的轮询只会在一次几十分钟的开服过程里空转几万次。
	arkApiLogPollInterval = time.Second
)

// arkApiLogAppearTimeout 是等文件**出现**的上限。加载器要先下载 offsets cache 才会
// 开始写日志，真机上是几十秒；给到 5 分钟之后仍然没有，基本可以判定 ArkApi 没被
// 加载 —— 此时写一行说明并退出，而不是留一个永远在转的协程。
//
// 它只约束「等出现」，**绝不**约束之后的转抄：转抄要跟到启动链结束（实例停止）
// 为止，可能是几天。两者曾共用一个带这个超时的 ctx，结果每个 ArkApi 实例开服
// 5 分钟后插件日志就不再更新（docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md §6.2）。
// 变量而非常量只为测试能缩短它。
var arkApiLogAppearTimeout = 5 * time.Minute

// arkApiLogDir 返回某个实例镜像里的 ArkApi 日志目录。
func arkApiLogDir(mirrorDir string) string {
	return filepath.Join(mirrorDir, filepath.FromSlash(arkApiLogDirRel))
}

func isArkApiLogName(name string) bool {
	return strings.HasPrefix(name, arkApiLogPrefix) && strings.HasSuffix(name, arkApiLogSuffix)
}

// startAsaApiLogging 把这次 ArkApi 启动的输出接到实例目录里。Linux 版本要做两件事，
// 因为这里的 PTY 和 Windows 的 PTY 装的**不是同一样东西**：
//
//  1. PTY（umu-run → pressure-vessel → Proton → Wine 整条链的输出）→ launcher.log。
//     这份是排障用的，「加载器退出码 3、零输出」那次全靠它。
//  2. ArkApi 自己的文件日志 → arkAsaApi.log。加载器**不往控制台写**业务日志，
//     所以第 1 份里一行 ArkApi 的内容都没有；不做这一步，插件日志面板看到的就是
//     一屏 umu 噪声。
//
// 这样 API 层不需要知道平台差异：arkAsaApi.log 在两个平台上装的都是「ArkApi 的输出」。
// 见 docs/ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md §1.4（方案 C）。
//
// ptyStream 的关闭权归这里（server.go 的 Wait 协程不再关它），见 drainLauncherOutput。
func startAsaApiLogging(instanceName, mirrorDir string, ptyStream io.ReadCloser, launchedAt time.Time, done <-chan struct{}) {
	var dst io.Writer // nil = 没有地方落盘，只排空
	if launcherPath, err := GetLauncherLogFilePath(instanceName); err != nil {
		logger.Warnf("Failed to resolve launcher log path for instance %s: %v; 启动链输出已丢弃", instanceName, err)
	} else if f, err := os.OpenFile(launcherPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644); err != nil {
		logger.Warnf("Failed to open launcher log file %s: %v; 启动链输出已丢弃", launcherPath, err)
	} else {
		dst = f
	}

	go func() {
		if f, ok := dst.(*os.File); ok {
			defer f.Close()
		}
		drainLauncherOutput(ptyStream, dst, done, launcherDrainGrace)
	}()

	go copyArkApiLog(instanceName, mirrorDir, launchedAt, done)
}

// launcherDrainGrace 是启动链退出之后，等 PTY 里剩下的输出被读完的上限。
const launcherDrainGrace = 5 * time.Second

// drainLauncherOutput 把 PTY 的输出写进 dst（nil 时直接丢弃），读完后关闭 PTY。
//
// 关闭权在读取方，是因为启动链退出的那一刻 PTY 主端的内核缓冲里可能还有没读走的
// 输出：以前 Wait 协程一看到 launcher 退出就关 PTY，秒退的加载器最后几行 —— 恰恰是
// 「退出码 3、零输出」排障时最需要的 —— 就这么丢了。由读取方关，它会先读到
// 从端全部关闭后的 EIO，缓冲读尽才收手。
//
// 但不能只靠 EIO：umu/Wine 链里可能有比 launcher 活得更久、继承了从端的进程
// （例如共享 prefix 下的 wineserver），等它们全部退出可能要很久。所以启动链退出后
// 至多再等 grace，到点照旧关 —— 与以前一样有界，只是多给了缓冲一个读完的机会。
//
// dst 为 nil 时也必须有人读：没人读的 PTY 写满缓冲后，启动链往终端写会阻塞。
func drainLauncherOutput(ptyStream io.ReadCloser, dst io.Writer, done <-chan struct{}, grace time.Duration) {
	var once sync.Once
	closePTY := func() { once.Do(func() { _ = ptyStream.Close() }) }

	drained := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-drained:
			return
		}
		select {
		case <-drained:
		case <-time.After(grace):
		}
		closePTY()
	}()
	defer func() {
		close(drained)
		closePTY()
	}()

	if dst == nil {
		_, _ = io.Copy(io.Discard, ptyStream)
		return
	}
	_ = console.CleanScreenOutput(ptyStream, dst)
}

// copyArkApiLog 把本次启动产生的 ArkApi 日志持续转抄进实例的 arkAsaApi.log，
// 直到启动链结束（done 关闭）。
//
// 为什么是转抄而不是让 API 直接 tail 那个文件：ArkApi 的日志文件名每次启动都变
// （ArkApi_<pid>_<时间>.log），API 层要跟着解析文件名、处理「还没生成」、处理镜像重建，
// 复杂度全压在 HTTP 处理器上。转抄把这些收在启动路径里一次解决，API 层一行不用改。
func copyArkApiLog(instanceName, mirrorDir string, launchedAt time.Time, done <-chan struct{}) {
	dstPath, err := GetAsaApiLogFilePath(instanceName)
	if err != nil {
		logger.Warnf("Failed to resolve AsaApi log path for instance %s: %v", instanceName, err)
		return
	}
	// 每次启动清空，与 Windows 侧一致。
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		logger.Warnf("Failed to open AsaApi log file %s: %v", dstPath, err)
		return
	}
	defer dst.Close()

	relayArkApiLog(dst, arkApiLogDir(mirrorDir), launchedAt, done, arkApiLogAppearTimeout,
		func(msg string) {
			logger.Warnf("ArkApi log relay for instance %s stopped: %s", instanceName, msg)
		})
}

// relayArkApiLog 是 copyArkApiLog 去掉「打开目标文件」之后的主体：等 dir 里出现本次
// 启动的 ArkApi 日志（至多 appearTimeout），然后把它持续转抄进 dst，直到 done 关闭。
//
// 两段用**两个**取消信号：等出现的 ctx 带超时，转抄的 ctx 只随 done 结束。
func relayArkApiLog(dst io.Writer, dir string, launchedAt time.Time, done <-chan struct{},
	appearTimeout time.Duration, warn func(string)) {

	note(dst, "正在等待 ArkApi 日志出现（%s）；启动链本身的输出在同目录的 launcher.log", dir)

	// 等出现：done 关闭与超时二者先到者为准。ctx.Err() 的两种取值
	// （Canceled/DeadlineExceeded）恰好够区分下面的措辞。
	appearCtx, appearCancel := context.WithTimeout(context.Background(), appearTimeout)
	defer appearCancel()
	// 转抄：只随 done 结束。
	relayCtx, relayCancel := context.WithCancel(context.Background())
	defer relayCancel()
	go func() {
		select {
		case <-done:
			appearCancel()
			relayCancel()
		case <-relayCtx.Done():
		}
	}()

	srcPath, err := tail.WaitNewest(appearCtx, dir, launchedAt, isArkApiLogName, arkApiLogPollInterval)
	if err != nil {
		// 说清楚而不是留一个空文件 —— 「静默」正是这个问题最初难查的原因。
		reason := "启动链已结束，仍未生成 ArkApi 日志"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = fmt.Sprintf("等待超过 %s", appearTimeout)
		}
		note(dst, "未能找到本次启动的 ArkApi 日志：%s", reason)
		note(dst, "多半意味着 ArkApi 没有被加载。请看 launcher.log，或跑 asa-server verify-arkapi")
		return
	}
	note(dst, "ArkApi 日志：%s", srcPath)

	src, err := os.Open(srcPath)
	if err != nil {
		note(dst, "打开 ArkApi 日志失败：%v", err)
		return
	}
	defer src.Close()

	var stopped string
	iox.Relay(relayCtx, src, dst, arkApiLogPollInterval, func(msg string) {
		stopped = msg
		warn(msg)
	})
	// 结束时留一行说明：面板上的日志不再更新时，用户应当知道是为什么。
	if stopped != "" {
		note(dst, "转抄中断：%s", stopped)
	} else {
		note(dst, "启动链已结束，停止转抄")
	}
}

// note 往转抄目标里写一行 asa-server 自己的说明，与 ArkApi 的行区分开。
func note(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, "[asa-server] %s\n", fmt.Sprintf(format, args...))
}
