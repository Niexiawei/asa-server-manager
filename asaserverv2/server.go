package asaserverv2

import (
	"asa-server/asaserver"
	"asa-server/common"
	"asa-server/logger"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aymanbagabas/go-pty"
)

// StartServer 启动服务器实例
// v2 版本：使用 per-instance 镜像目录替代 setupInstanceConfig
func StartServer(instanceName string, options ...asaserver.StartServerOptionsFunc) error {
	opts := new(asaserver.StartServerOptions)
	opts.WaitServerCompleted = false
	opts.ParentCtx = context.Background()
	for _, o := range options {
		o(opts)
	}
	_ = asaserver.WriteInstanceState(instanceName, asaserver.StatusStartStartInitialization, "")
	if !asaserver.TryLockServerActions() {
		return asaserver.ErrServerActionsLocked
	}

	ctx, cancel := context.WithCancel(opts.ParentCtx)
	defer cancel()

	var (
		startupSuccess = make(chan bool, 1)
		initSuccessful = make(chan bool, 1)
		pid            int
		mirrorDir      string
	)

	defer func() {
		close(startupSuccess)
		close(initSuccessful)
	}()

	var startErr error

	defer func() {
		if startErr != nil {
			_ = asaserver.WriteInstanceState(instanceName, asaserver.StatusStartFailed, startErr.Error())
			// 启动失败时清理镜像
			if mirrorDir != "" {
				if err := CleanupInstanceMirror(instanceName); err != nil {
					logger.GetLogger().Warnf("Failed to cleanup mirror after start failure: %v", err)
				}
			}
		}
	}()

	// 检查端口冲突
	if err := asaserver.CheckForDuplicatePorts(); err != nil {
		asaserver.UnlockServerActions()
		logger.GetLogger().Errorf("Port conflicts detected: %v", err)
		startErr = err
		return err
	}

	config, err := asaserver.LoadInstanceConfig(instanceName)
	if err != nil {
		asaserver.UnlockServerActions()
		startErr = err
		return err
	}

	logger.GetLogger().Infof("Starting server for instance: %s (v2)", instanceName)

	// 创建实例镜像目录
	mirrorDir, err = SetupInstanceMirror(instanceName, config)
	if err != nil {
		asaserver.UnlockServerActions()
		startErr = fmt.Errorf("failed to setup instance mirror: %w", err)
		return startErr
	}

	// 构建命令行参数
	mapParam := fmt.Sprintf("%s?listen?SessionName=%s?ServerPassword=%s?RCONEnabled=True?ServerAdminPassword=%s?AltSaveDirectoryName=%s",
		config.MapName,
		quotifyIfNeeded(config.ServerName),
		quotifyIfNeeded(config.ServerPassword),
		quotifyIfNeeded(config.ServerAdminPassword),
		config.SaveDir,
	)

	// 使用镜像目录中的 exe
	arkExe := filepath.Join(mirrorDir, "ShooterGame/Binaries/Win64/ArkAscendedServer.exe")

	args := []string{
		mapParam,
	}

	if config.CustomStartParameters != "" {
		args = append(args, strings.Fields(config.CustomStartParameters)...)
	}

	args = append(args,
		fmt.Sprintf("-WinLiveMaxPlayers=%d", config.MaxPlayers),
		fmt.Sprintf("-Port=%d", config.Port),
		fmt.Sprintf("-QueryPort=%d", config.QueryPort),
		fmt.Sprintf("-RCONPort=%d", config.RCONPort),
		"-game",
		"-server",
		"-log",
	)

	// Handle BindDomain resolution
	if config.BindDomain != "" {
		if ipv4Addrs, err := common.ResolveDomainToIPv4(config.BindDomain); err == nil && len(ipv4Addrs) > 0 {
			ipv4Addr := ipv4Addrs[0]
			customParams := strings.Fields(config.CustomStartParameters)
			ipFound := false
			serverIpFound := false

			for i, param := range customParams {
				if strings.HasPrefix(param, "-ip=") {
					customParams[i] = fmt.Sprintf("-ip=%s", ipv4Addr)
					ipFound = true
				} else if param == "-ip" && i+1 < len(customParams) {
					customParams[i+1] = ipv4Addr
					ipFound = true
				} else if strings.HasPrefix(param, "-serverip=") {
					customParams[i] = fmt.Sprintf("-serverip=%s", ipv4Addr)
					serverIpFound = true
				} else if param == "-serverip" && i+1 < len(customParams) {
					customParams[i+1] = ipv4Addr
					serverIpFound = true
				}
			}

			if ipFound || serverIpFound {
				args = []string{mapParam}
				args = append(args, customParams...)
			} else {
				args = append(args, fmt.Sprintf("-ip=%s", ipv4Addr))
				args = append(args, fmt.Sprintf("-serverip=%s", ipv4Addr))
			}
		} else {
			logger.GetLogger().Warnf("Failed to resolve domain %s to IPv4 address, ignoring BindDomain parameter", config.BindDomain)
		}
	}

	if config.ModIDs != "" {
		args = append(args, fmt.Sprintf("-mods=%s", config.ModIDs))
	}

	if config.ClusterID != "" {
		clusterDir := filepath.Join(asaserver.BaseDir, "clusters", config.ClusterID)
		if strings.Contains(config.CustomStartParameters, "-ClusterDirOverride") {
			args = append(args, fmt.Sprintf("-ClusterId=%s", config.ClusterID))
		} else {
			args = append(args,
				fmt.Sprintf("-ClusterDirOverride=%s", clusterDir),
				fmt.Sprintf("-ClusterId=%s", config.ClusterID),
			)
		}
	}

	var arkAsaApiRunning bool

	if config.EnableAsaPlugin {
		arkApiExe := filepath.Join(mirrorDir, "ShooterGame/Binaries/Win64/AsaApiLoader.exe")
		if asaserver.FileExists(arkApiExe) {
			arkExe = arkApiExe
			arkAsaApiRunning = true
		}
	}

	// 日志路径使用实例本地目录
	gameLogPath, err := GetGameLogFilePath(instanceName)
	if err != nil {
		asaserver.UnlockServerActions()
		startErr = fmt.Errorf("failed to get game log file path: %w", err)
		return startErr
	}

	if opts.GameLogPathCallback != nil {
		opts.GameLogPathCallback(gameLogPath)
	}

	// 确保日志文件存在
	if _, err := os.Stat(gameLogPath); os.IsNotExist(err) {
		if err := os.WriteFile(gameLogPath, []byte(""), 0644); err != nil {
			return fmt.Errorf("failed to create log file %s: %w", gameLogPath, err)
		}
		logger.GetLogger().Infof("Created log file: %s", gameLogPath)
	}

	// 设置进程工作目录为镜像内的 exe 目录
	exeWorkDir := filepath.Join(mirrorDir, "ShooterGame/Binaries/Win64")

	if arkAsaApiRunning {
		logWriter := &logWriter{
			fn: func(msg string) {
				msg = strings.TrimRight(msg, "\n\r")
				if msg != "" {
					if strings.Contains(msg, "Info/GameAnalytics") {
						return
					}
					logger.GetArkApiLogger().Infof("[%s][AsaApiLoader] %s", instanceName, msg)
				}
			},
		}

		pp, err := pty.New()
		if err != nil {
			startErr = fmt.Errorf("failed to create pty: %w", err)
			return startErr
		}
		c := pp.Command(arkExe, args...)
		c.Dir = exeWorkDir
		if err := c.Start(); err != nil {
			startErr = fmt.Errorf("failed to start server: %w", err)
			return startErr
		}
		go arkApiCleanConsoleOutput(pp, logWriter)
		logger.GetLogger().Infof("[%s] Redirecting AsaApiLoader output to logger", instanceName)

		_pid, err := asaserver.WaitArkApiRunServer(ctx, config.QueryPort)
		if err != nil {
			startErr = fmt.Errorf("failed to start server: %w", err)
			return startErr
		}
		pid = int(_pid)
	} else {
		cmd := exec.Command(arkExe, args...)
		cmd.Dir = exeWorkDir
		if err := cmd.Start(); err != nil {
			startErr = fmt.Errorf("failed to start server: %w", err)
			return startErr
		}
		pid = cmd.Process.Pid
	}

	if err := asaserver.SaveInstancePID(instanceName, pid); err != nil {
		logger.GetLogger().Warnf("Failed to save PID for instance %s: %v", instanceName, err)
	}

	_ = asaserver.WriteInstanceState(instanceName, asaserver.StatusStarting, "")

	logger.GetLogger().Infof("Server started for instance: %s (v2). It should be fully operational in approximately 60 seconds.", instanceName)
	logger.GetLogger().Infof("Game log file: %s", gameLogPath)

	if ctx.Err() != nil {
		asaserver.UnlockServerActions()
		killGameServer(pid)
	}

	if opts.PidCallback != nil {
		opts.PidCallback(pid)
	}

	// Monitor for mod information
	go asaserver.MonitorAndExtractModInfo(ctx, gameLogPath, instanceName)

	go waitServerStartup(pid, gameLogPath, func(startup bool, err string) {
		if startup {
			_ = asaserver.WriteInstanceState(instanceName, asaserver.StatusStarted, "")
		} else {
			_ = asaserver.WriteInstanceState(instanceName, asaserver.StatusStartFailed, err)
		}
	}, func() {
		_ = asaserver.WriteInstanceState(instanceName, asaserver.StatusStartStartInitializationSuccessful, "")
		// v2: 不调用 confReset（镜像目录独立，无需恢复原始 Config）
		asaserver.UnlockServerActions()
		initSuccessful <- true

		if opts.GameInitializationSuccessful != nil {
			opts.GameInitializationSuccessful()
		}
	})

	if opts.WaitServerCompleted {
		WaitServerCompletedCtx, WaitServerCompletedCancel := context.WithCancel(ctx)
		defer WaitServerCompletedCancel()

		go func() {
			if exited := asaserver.WaitGamePidExit(WaitServerCompletedCtx, pid); exited {
				WaitServerCompletedCancel()
			}
		}()

		asaserver.TailLogFileWithLinesContext(WaitServerCompletedCtx, gameLogPath, 0, func(line string) {
			if strings.Contains(line, "Server has completed startup and is now advertising for join") {
				startupSuccess <- true
			}
		})

		select {
		case <-WaitServerCompletedCtx.Done():
			asaserver.UnlockServerActions()
			startErr = fmt.Errorf("start game server exited")
			return startErr
		case <-startupSuccess:
			return nil
		}
	} else {
		<-initSuccessful
	}

	return nil
}

// StopServer 停止服务器实例
func StopServer(instanceName string) error {
	var (
		pid         int
		gameLogPath string
	)
	var stopErr error

	defer func() {
		if stopErr != nil {
			_ = asaserver.WriteInstanceState(instanceName, asaserver.StatusStopFailed, stopErr.Error())
			rollbackStatus := asaserver.StatusStopped
			if running, err := asaserver.IsServerRunning(instanceName); err == nil && running {
				rollbackStatus = asaserver.StatusStarted
			}
			_ = asaserver.WriteInstanceState(instanceName, rollbackStatus, "")
		}
	}()

	if !asaserver.TryLockServerActions() {
		return asaserver.ErrServerActionsLocked
	}
	_ = asaserver.WriteInstanceState(instanceName, asaserver.StatusStopping, "")

	running, err := asaserver.IsServerRunning(instanceName)
	if err != nil || !running {
		asaserver.UnlockServerActions()
		logger.GetLogger().Warnf("Server for instance %s is not running.", instanceName)
		stopErr = fmt.Errorf("server for instance %s is not running", instanceName)
		return stopErr
	}

	logger.GetLogger().Infof("Stopping server for instance: %s (v2)", instanceName)

	config, configErr := asaserver.LoadInstanceConfig(instanceName)
	if configErr != nil {
		asaserver.UnlockServerActions()
		stopErr = fmt.Errorf("failed to load instance config: %w", configErr)
		return stopErr
	}
	pid, err = asaserver.GetPIDByPort(config.Port)
	if err != nil {
		asaserver.UnlockServerActions()
		stopErr = fmt.Errorf("failed to find process PID: %w", err)
		return stopErr
	}

	logger.GetLogger().Infof("Stopping server for instance: %s pid: %d", instanceName, pid)

	// 日志路径使用实例本地目录
	gameLogPath, _ = GetGameLogFilePath(instanceName)

	if err := asaserver.SaveWorldSafely(instanceName); err != nil {
		asaserver.UnlockServerActions()
		stopErr = fmt.Errorf("failed to save world safely: %w", err)
		return stopErr
	}

	response, err := asaserver.SendRCONCommand(instanceName, "DoExit")

	if err == nil && strings.Contains(response, "Exiting") {
		logger.GetLogger().Infof("Server instance %s reported 'Exiting...'. Awaiting shutdown...", instanceName)
	} else {
		if taskkillErr := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid)).Run(); taskkillErr != nil {
			logger.GetLogger().Warnf("failed to kill process PID %d: %s", pid, taskkillErr.Error())
			_ = exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid)).Run()
		}
	}

	// Wait for server to fully stop
	waitCtx, waitCancel := context.WithCancel(context.Background())
	defer waitCancel()

	stopped := make(chan struct{})
	go waitServerStopped(waitCtx, pid, gameLogPath,
		func() {
			logger.GetLogger().Infof("Server %s received closing request", instanceName)
		},
		func(complete bool) {
			asaserver.UnlockServerActions()
			close(stopped)
		})

	// Wait for complete stop or timeout
	select {
	case <-stopped:
		logger.GetLogger().Infof("Server for instance %s has exited.", instanceName)
	case <-time.After(5 * time.Minute):
		logger.GetLogger().Warnf("Process %d did not exit within 5min, force killing", pid)
		_ = exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid)).Run()
		waitCancel()
		asaserver.UnlockServerActions()
	}

	// Cleanup AsaApiLoader process if applicable
	if config.EnableAsaPlugin {
		if pid2, pidErr := asaserver.GetInstancePID(instanceName); pidErr == nil {
			_ = exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid2)).Run()
		}
	}

	_ = asaserver.WriteInstanceState(instanceName, asaserver.StatusStopped, "")

	// 清理镜像目录
	if err := CleanupInstanceMirror(instanceName); err != nil {
		logger.GetLogger().Warnf("Failed to cleanup instance mirror for %s: %v", instanceName, err)
	}

	return nil
}

// KillServer 强制杀死服务器实例
func KillServer(instanceName string) error {
	cfg, err := asaserver.LoadInstanceConfig(instanceName)
	if err != nil {
		return err
	}
	pid, err := asaserver.GetPIDByPort(cfg.Port)
	if err != nil {
		return err
	}

	err = exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid)).Run()
	if err != nil {
		return err
	}
	_ = asaserver.WriteInstanceState(instanceName, asaserver.StatusStopped, "")

	// 清理镜像目录
	if err := CleanupInstanceMirror(instanceName); err != nil {
		logger.GetLogger().Warnf("Failed to cleanup instance mirror for %s: %v", instanceName, err)
	}

	return nil
}

// RestartServer 重启服务器实例
func RestartServer(instanceName string) error {
	var restartErr error

	defer func() {
		if restartErr != nil {
			_ = asaserver.WriteInstanceState(instanceName, asaserver.StatusRestartFailed, restartErr.Error())
			rollbackStatus := asaserver.StatusStopped
			if running, err := asaserver.IsServerRunning(instanceName); err == nil && running {
				rollbackStatus = asaserver.StatusStarted
			}
			_ = asaserver.WriteInstanceState(instanceName, rollbackStatus, "")
		}
	}()

	_ = asaserver.WriteInstanceState(instanceName, asaserver.StatusRestarting, "")

	if err := StopServer(instanceName); err != nil {
		restartErr = err
		return err
	}
	time.Sleep(10 * time.Second)

	err := StartServer(instanceName)
	if err != nil {
		restartErr = err
		return err
	}
	return nil
}

// StartAllInstances 启动所有实例
func StartAllInstances() error {
	instances, err := asaserver.GetAvailableInstances()
	if err != nil {
		return err
	}

	fmt.Println("Starting all server instances...")

	for _, instanceName := range instances {
		running, err := asaserver.IsServerRunning(instanceName)
		if err == nil && running {
			logger.GetLogger().Warnf("Instance %s is already running. Skipping...", instanceName)
			continue
		}

		if err := StartServer(instanceName); err != nil {
			logger.GetLogger().Errorf("Failed to start instance %s: %v", instanceName, err)
			continue
		}

		logger.GetLogger().Info("Waiting 30 seconds before starting the next instance...")
		time.Sleep(30 * time.Second)
	}

	logger.GetLogger().Info("All instances have been processed.")
	return nil
}

// StopAllInstances 停止所有实例
func StopAllInstances() error {
	instances, err := asaserver.GetAvailableInstances()
	if err != nil {
		return err
	}
	for _, instanceName := range instances {
		running, err := asaserver.IsServerRunning(instanceName)
		if err == nil && !running {
			logger.GetLogger().Warnf("Instance %s is not running. Skipping...", instanceName)
			continue
		}

		if err := StopServer(instanceName); err != nil {
			logger.GetLogger().Errorf("Failed to stop instance %s: %v", instanceName, err)
		}
	}

	logger.GetLogger().Info("All instances have been stopped.")
	return nil
}

// GetRunningInstances 返回正在运行的实例列表
func GetRunningInstances() ([]string, error) {
	instances, err := asaserver.GetAvailableInstances()
	if err != nil {
		return nil, err
	}

	var running []string
	for _, instanceName := range instances {
		if isRunning, err := asaserver.IsServerRunning(instanceName); err == nil && isRunning {
			running = append(running, instanceName)
		}
	}

	return running, nil
}

// GetGameLogFilePath 返回实例的游戏日志文件路径
// v2 版本：使用实例本地 Logs 目录，不需要日志计数器
func GetGameLogFilePath(instanceName string) (string, error) {
	logsDir := filepath.Join(asaserver.InstancesDir, instanceName, "Logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create logs directory: %w", err)
	}
	return filepath.Join(logsDir, "ShooterGame.log"), nil
}

// GetGameLogFileName 返回实例的游戏日志文件名和完整路径
func GetGameLogFileName(instanceName string) (string, string, error) {
	logPath, err := GetGameLogFilePath(instanceName)
	if err != nil {
		return "", "", err
	}
	return logPath, "ShooterGame.log", nil
}

// quotifyIfNeeded 包装可能包含特殊字符的参数
func quotifyIfNeeded(value string) string {
	if value == "" {
		return value
	}
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, " ", "-")
	return fmt.Sprintf("\"%s\"", value)
}

// SyncGameConfigToInstance 同步游戏配置到实例
func SyncGameConfigToInstance(instanceName string) error {
	baseConfigDir := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer")
	instanceConfigDir := filepath.Join(asaserver.InstancesDir, instanceName, "Config")

	if err := os.MkdirAll(instanceConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create instance config directory: %w", err)
	}

	gameIniSourcePath := filepath.Join(baseConfigDir, "Game.ini")
	gameIniDestPath := filepath.Join(instanceConfigDir, "Game.ini")
	if err := syncConfigFile(gameIniSourcePath, gameIniDestPath); err != nil {
		return fmt.Errorf("failed to sync Game.ini: %w", err)
	}

	gameUserSettingsSourcePath := filepath.Join(baseConfigDir, "GameUserSettings.ini")
	gameUserSettingsDestPath := filepath.Join(instanceConfigDir, "GameUserSettings.ini")
	if err := syncConfigFile(gameUserSettingsSourcePath, gameUserSettingsDestPath); err != nil {
		return fmt.Errorf("failed to sync GameUserSettings.ini: %w", err)
	}

	logger.GetLogger().Infof("Configuration files synced for instance '%s'", instanceName)
	return nil
}

// syncConfigFile 同步单个配置文件
func syncConfigFile(sourcePath, destPath string) error {
	if _, err := os.Stat(sourcePath); os.IsNotExist(err) {
		return nil
	}

	srcFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to open source config file: %w", err)
	}
	defer srcFile.Close()

	destFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create destination config file: %w", err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy config file: %w", err)
	}

	return nil
}

