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
	// arkApiLogAppearTimeout 是等文件出现的上限。加载器要先下载 offsets cache 才会
	// 开始写日志，真机上是几十秒；给到 5 分钟之后仍然没有，基本可以判定 ArkApi 没被
	// 加载 —— 此时写一行说明并退出，而不是留一个永远在转的协程。
	arkApiLogAppearTimeout = 5 * time.Minute
)

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
func startAsaApiLogging(instanceName, mirrorDir string, ptyStream io.Reader, launchedAt time.Time, done <-chan struct{}) {
	if launcherPath, err := GetLauncherLogFilePath(instanceName); err != nil {
		logger.Warnf("Failed to resolve launcher log path for instance %s: %v", instanceName, err)
	} else if f, err := os.OpenFile(launcherPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644); err != nil {
		logger.Warnf("Failed to open launcher log file %s: %v", launcherPath, err)
	} else {
		// cleaner 独占该句柄，pty 关闭后 CleanScreenOutput 返回并释放。
		go func() {
			defer f.Close()
			_ = console.CleanScreenOutput(ptyStream, f)
		}()
	}

	go copyArkApiLog(instanceName, mirrorDir, launchedAt, done)
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

	dir := arkApiLogDir(mirrorDir)
	note(dst, "正在等待 ArkApi 日志出现（%s）；启动链本身的输出在同目录的 launcher.log", dir)

	// done 关闭与「等待超时」合并成一个 ctx：WaitNewest 只需要认识一种取消信号，
	// 而 ctx.Err() 的两种取值（Canceled/DeadlineExceeded）恰好够区分下面的措辞。
	ctx, cancel := context.WithTimeout(context.Background(), arkApiLogAppearTimeout)
	defer cancel()
	go func() {
		select {
		case <-done:
			cancel()
		case <-ctx.Done():
		}
	}()

	srcPath, err := tail.WaitNewest(ctx, dir, launchedAt, isArkApiLogName, arkApiLogPollInterval)
	if err != nil {
		// 说清楚而不是留一个空文件 —— 「静默」正是这个问题最初难查的原因。
		reason := "启动链已结束，仍未生成 ArkApi 日志"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = fmt.Sprintf("等待超过 %s", arkApiLogAppearTimeout)
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

	iox.Relay(ctx, src, dst, arkApiLogPollInterval, func(msg string) {
		logger.Warnf("ArkApi log relay for instance %s stopped: %s", instanceName, msg)
	})
}

// note 往转抄目标里写一行 asa-server 自己的说明，与 ArkApi 的行区分开。
func note(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, "[asa-server] %s\n", fmt.Sprintf(format, args...))
}
