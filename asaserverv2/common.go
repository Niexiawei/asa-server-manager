package asaserverv2

import (
	"asa-server/asaserver"
	"asa-server/logger"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// logWriter 是一个自定义 writer，将输出转发到 logger
type logWriter struct {
	fn func(string)
}

// Write 实现 io.Writer 接口
func (lw *logWriter) Write(p []byte) (n int, err error) {
	if lw.fn != nil {
		lw.fn(string(p))
	}
	return len(p), nil
}

type waitServerStartupFunc func(startup bool, err string)

// waitServerStartup 监控服务器启动过程
// v2 版本：不调用 confReset（镜像目录独立，不需要恢复原始 Config）
func waitServerStartup(pid int, gameLogPath string, callback waitServerStartupFunc, successfullyCallback func()) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	latestLogLine := ""
	var (
		startup = make(chan struct{}) // 无缓冲，使用 close() 通知
	)

	go func() {
		if exited := asaserver.WaitGamePidExit(ctx, pid); exited {
			callback(false, latestLogLine)
			close(startup) // 进程退出时解除阻塞
		}
	}()

	asaserver.TailLogFileWithLinesContext(ctx, gameLogPath, 0, func(line string) {
		latestLogLine = line
		// Check for successful startup message
		if strings.Contains(line, "Server has completed startup and is now advertising for join") {
			callback(true, "")
			close(startup)
			cancel()
		}
		if strings.Contains(line, "Initialize Primal Game Data Override") {
			successfullyCallback()
		}
	})
	<-startup
}

// arkApiCleanConsoleOutput 清理 AsaApiLoader 的控制台输出
// 移除 ANSI 转义序列和控制字符
func arkApiCleanConsoleOutput(r io.Reader, w io.Writer) error {
	// 匹配 ANSI 转义序列
	ansiRegexp := regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)
	// 去除其它 C0 控制字符（保留换行符 \n）
	ctrlRegexp := regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]`)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		// 去掉 ANSI / 控制字符
		line = ansiRegexp.ReplaceAll(line, []byte{})
		line = ctrlRegexp.ReplaceAll(line, []byte{})

		// 输出，保证每行以换行符结尾
		line = bytes.TrimRight(line, " \t")
		if _, err := w.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// waitServerStoppedFunc 等待服务器停止的回调函数类型
type waitServerStoppedFunc func(stopComplete bool)

// waitServerStopped 监控服务器关闭过程
// 1. "Closing by request" -> closingCallback
// 2. PID exit -> stoppedCallback(true)
func waitServerStopped(ctx context.Context, pid int, gameLogPath string,
	closingCallback func(),
	stoppedCallback waitServerStoppedFunc,
) {
	// 监控进程退出
	go func() {
		if exited := asaserver.WaitGamePidExit(ctx, pid); exited {
			stoppedCallback(true)
		}
	}()

	// 如果没有日志文件路径，仅依赖 PID 监控
	if gameLogPath == "" {
		return
	}

	asaserver.TailLogFileWithLinesContext(ctx, gameLogPath, 0, func(line string) {
		if strings.Contains(line, "Closing by request") || strings.Contains(line, "closing by request") {
			if closingCallback != nil {
				closingCallback()
			}
		}
		if strings.Contains(line, "Log file closed") {
			logger.GetLogger().Infof("Server log file closed for pid %d", pid)
		}
	})
}

// killGameServer 强制杀死游戏服务器进程
func killGameServer(pid int) {
	if pid <= 0 {
		return
	}
	logger.GetLogger().Infof("Force killing game server process PID: %d", pid)
	if err := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid)).Run(); err != nil {
		logger.GetLogger().Warnf("failed to kill process PID %d: %s", pid, err.Error())
		_ = exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid)).Run()
	}
}

// findServerPIDByPort 通过端口查找进程 PID
func findServerPIDByPort(port int) (int, error) {
	return asaserver.GetPIDByPort(port)
}

// savePathReplacement 存档路径替换规则
// 游戏引擎在磁盘上使用的目录名可能与配置中的 MapName 不同
var savePathReplacement = map[string]string{
	"BobsMissions_WP": "BobsMissions",
}

// SaveWorldSafely 安全保存世界存档
// v2 版本：使用实例本地 Save 目录（通过 junction 指向 instances/<name>/Save/）
func SaveWorldSafely(instanceName string) error {
	config, err := asaserver.LoadInstanceConfig(instanceName)
	if err != nil {
		return fmt.Errorf("failed to load instance config: %w", err)
	}

	startTime := time.Now()

	response, err := asaserver.SendRCONCommand(instanceName, "saveworld")
	if err != nil {
		return fmt.Errorf("failed to send command: %w", err)
	}

	if strings.Contains(response, "World Saved") {
		logger.GetLogger().Infof("Server instance %s is saving world...", instanceName)
	} else {
		logger.GetLogger().Errorf("server instance %s is saving world error: %v", instanceName, err)
		return fmt.Errorf("server instance %s is saving world error: %w", instanceName, err)
	}

	if config.MapName == "BobsMissions_WP" {
		return nil
	}

	// v2: 存档路径使用实例本地 Save 目录
	saveDirPath := filepath.Join(asaserver.InstancesDir, instanceName, "Save", config.MapName)

	args := make([]string, 0, len(savePathReplacement)*2)
	for k, v := range savePathReplacement {
		args = append(args, k, v)
	}
	r := strings.NewReplacer(args...)
	saveDirPath = r.Replace(saveDirPath)
	saveFilePath := filepath.Join(saveDirPath, config.MapName+".ark")

	// 确保存档目录存在
	if err := os.MkdirAll(saveDirPath, 0755); err != nil {
		return fmt.Errorf("failed to create save directory: %w", err)
	}

	maxWait := 300 * time.Second
	checkInterval := 1 * time.Second
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), maxWait)
	defer cancel()

	for {
		select {
		case <-ticker.C:
			fileInfo, err := os.Stat(saveFilePath)
			if err == nil {
				fileModTime := fileInfo.ModTime()
				if fileModTime.After(startTime) || fileModTime.Equal(startTime) {
					diffMilli := fileModTime.UnixMilli() - startTime.UnixMilli()
					logger.GetLogger().Infof("World saved successfully for instance %s. Save file: %s. saved Milliseconds %d",
						instanceName, saveFilePath, diffMilli)
					return nil
				}
			} else {
				logger.GetLogger().Warnf("Save file not found or error checking: %v", err)
			}
			logger.GetLogger().Infof("The world archive has not been uploaded yet: %s", instanceName)
		case <-ctx.Done():
			logger.GetLogger().Errorf("timeout waiting for world save to complete for instance %s. Save file: %s", instanceName, saveFilePath)
			return fmt.Errorf("timeout waiting for world save to complete for instance %s. Save file: %s", instanceName, saveFilePath)
		}
	}
}
