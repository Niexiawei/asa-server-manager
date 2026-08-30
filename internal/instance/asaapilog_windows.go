//go:build windows

package instance

import (
	"asa-server/pkg/console"
	"asa-server/pkg/logger"
	"io"
	"os"
	"time"
)

// startAsaApiLogging 把这次 ArkApi 启动的输出接到实例的 arkAsaApi.log。
//
// Windows 上 PTY 里跑的**就是** AsaApiLoader.exe 本体，控制台里流的就是它的业务输出，
// 所以直接落盘即可 —— 与本函数存在之前的行为逐字相同。mirrorDir / launchedAt / done
// 是 Linux 那边转抄 ArkApi 文件日志才需要的，这里用不上。
//
// 为什么不顺手也在 Windows 上改成读 ArkApi 的文件日志：这条路今天是好的，
// 而 Windows 是本项目已经交付的平台，不为对称性去动一个没坏的东西。
// 见 docs/ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md §1.4。
func startAsaApiLogging(instanceName, mirrorDir string, ptyStream io.Reader, launchedAt time.Time, done <-chan struct{}) {
	logPath, err := GetAsaApiLogFilePath(instanceName)
	if err != nil {
		logger.Warnf("Failed to resolve AsaApi log path for instance %s: %v", instanceName, err)
		return
	}
	// 每次启动清空。
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		logger.Warnf("Failed to open AsaApi log file %s: %v", logPath, err)
		return
	}
	// cleaner 独占该句柄，pty 关闭后 CleanScreenOutput 返回并释放。
	// AsaApiLoader 用光标定位排版，必须走 CleanScreenOutput 而非 CleanConsoleOutput。
	go func() {
		defer f.Close()
		_ = console.CleanScreenOutput(ptyStream, f)
	}()
}
