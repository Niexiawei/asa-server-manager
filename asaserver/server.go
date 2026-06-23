package asaserver

import (
	"asa-server/common"
	"asa-server/logger"
	"asa-server/win32api"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aymanbagabas/go-pty"
	"github.com/gorcon/rcon"
)

// ErrOperationNotAllowed is returned when an operation is not allowed in the current instance state
var ErrOperationNotAllowed = fmt.Errorf("operation not allowed in current state")

// LogWriter is a custom writer that forwards output to logger
type LogWriter struct {
	loggerFn func(string)
}

// Write implements the io.Writer interface
func (lw *LogWriter) Write(p []byte) (n int, err error) {
	if lw.loggerFn != nil {
		lw.loggerFn(string(p))
	}
	return len(p), nil
}

// removeANSIEscapes removes ANSI escape sequences from a string
func removeANSIEscapes(s string) string {
	// This regex pattern matches ANSI escape sequences
	// Including color codes, cursor movement, and other control sequences
	var result strings.Builder
	i := 0
	for i < len(s) {
		// Check if this is the start of an escape sequence
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Skip the escape sequence
			i += 2
			// Skip until we find a letter or other terminator
			for i < len(s) {
				c := s[i]
				if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '@' {
					i++
					break
				}
				i++
			}
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return strings.TrimSpace(result.String())
}

// GetGameLogFilePath returns the full path to the log file for a given instance
// v2: uses per-instance log directory under InstancesDir
func GetGameLogFilePath(instanceName string) (string, error) {
	logsDir := filepath.Join(InstancesDir, instanceName, "Logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create logs directory: %w", err)
	}
	logPath := filepath.Join(logsDir, "ShooterGame.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		os.WriteFile(logPath, nil, 0644)
	}
	return logPath, nil
}

// IsServerRunning checks if a server instance is running
// It checks by verifying if both the game port and RCON port are listening
// This uniquely identifies the specific server instance
func IsServerRunning(instanceName string) (bool, error) {
	// Load the instance configuration to get the ports
	config, err := LoadInstanceConfig(instanceName)

	if err != nil {
		return false, err
	}

	// Build the netstat command with port filtering for efficiency
	// Check for both game port and RCON port in a single netstat query
	cmd := exec.Command("netstat", "-ano")
	// Hide the cmd window on Windows
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to execute netstat: %w", err)
	}

	netstatOutput := string(output)

	lines := strings.Split(netstatOutput, "\n")

	portStr := fmt.Sprintf(":%d", config.Port)

	for _, line := range lines {
		if strings.Contains(line, portStr) {
			// The last field in the line is the PID
			fields := strings.Fields(line)
			if len(fields) > 2 {
				if !strings.Contains(fields[1], portStr) {
					continue
				}
				pid, err := strconv.Atoi(fields[len(fields)-1])
				if err == nil && pid > 0 {
					return true, nil
				}
			}
		}
	}
	//logger.GetLogger().Warnf("Game port :%d not found", config.Port)
	return false, nil
}

// IsServerRunningByPID checks if a server instance is running by verifying if the saved PID process exists
// This method retrieves the PID from the instance directory and checks if the process is still active
func IsServerRunningByPID(instanceName string) (bool, error) {
	// Retrieve the saved PID for the instance
	pid, err := GetInstancePID(instanceName)
	if err != nil {
		return false, fmt.Errorf("failed to get instance PID: %w", err)
	}

	// Check if the process with this PID exists and is running
	exited, err := win32api.IsProcessExited(uint32(pid))
	if err != nil {
		return false, fmt.Errorf("failed to check process status: %w", err)
	}

	// If process has exited, it's not running
	if exited {
		return false, nil
	}

	return true, nil
}

// CopyDir copies a directory recursively
func CopyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := CopyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

type StartServerOptions struct {
	GameLogPathCallback          func(path string)
	GameInitializationSuccessful func()
	WaitServerCompleted          bool
	ParentCtx                    context.Context
	PidCallback                  func(pid int)
	OnRestartStartupComplete     func(instanceName string)
}

type StartServerOptionsFunc func(options *StartServerOptions)

func WithGameLogPathCallback(callback func(path string)) StartServerOptionsFunc {
	return func(options *StartServerOptions) {
		options.GameLogPathCallback = callback
	}
}
func WithGameInitializationSuccessfulCallback(callback func()) StartServerOptionsFunc {
	return func(options *StartServerOptions) {
		options.GameInitializationSuccessful = callback
	}
}

func WithWaitServerCompleted() StartServerOptionsFunc {
	return func(options *StartServerOptions) {
		options.WaitServerCompleted = true
	}
}
func WithCtx(ctx context.Context) StartServerOptionsFunc {
	return func(options *StartServerOptions) {
		options.ParentCtx = ctx
	}
}

func WithPidCallback(callback func(pid int)) StartServerOptionsFunc {
	return func(options *StartServerOptions) {
		options.PidCallback = callback
	}
}

// WithRestartStartupCompletion marks restart startup success: writes restarted history,
// invokes callback, then the existing started write runs in waitServerStartup.
func WithRestartStartupCompletion(callback func(instanceName string)) StartServerOptionsFunc {
	return func(options *StartServerOptions) {
		options.OnRestartStartupComplete = callback
	}
}

// StartServer starts a server instance (public version with CAS check)
func StartServer(instanceName string, options ...StartServerOptionsFunc) error {
	// CAS: 原子检查状态并转换到 start_initialization
	ok, err := CompareAndSwapInstanceState(instanceName,
		[]InstanceStatus{StatusStopped, StatusStartFailed, StatusStopFailed, StatusRestartFailed, ""},
		StatusStartStartInitialization)
	if err != nil {
		return fmt.Errorf("failed to check instance state: %w", err)
	}
	if !ok {
		return ErrOperationNotAllowed
	}

	return startServerInternal(instanceName, options...)
}

// startServerInternal starts a server instance (internal version without CAS, for RestartServer)
// v2: uses per-instance mirror directory instead of shared junction
func startServerInternal(instanceName string, options ...StartServerOptionsFunc) error {
	opts := new(StartServerOptions)
	opts.WaitServerCompleted = false
	opts.ParentCtx = context.Background()
	for _, o := range options {
		o(opts)
	}

	ctx, cancel := context.WithCancel(opts.ParentCtx)
	defer cancel()

	var (
		startupSuccess = make(chan bool, 1)
		initSuccessful = make(chan bool, 1)
		initFailed     = make(chan error, 1)
		pid            int
		mirrorDir      string
	)

	// Buffered channels will be garbage collected when no longer referenced.
	var startErr error

	defer func() {
		if startErr != nil {
			_ = WriteInstanceState(instanceName, StatusStartFailed, startErr.Error())
			// 启动失败时清理镜像
			if mirrorDir != "" {
				if err := CleanupInstanceMirror(instanceName); err != nil {
					logger.GetLogger().Warnf("Failed to cleanup mirror after start failure: %v", err)
				}
			}
		}
	}()

	// Check for duplicate ports
	if err := CheckForDuplicatePorts(); err != nil {
		logger.GetLogger().Errorf("Port conflicts detected: %v", err)
		startErr = err
		return err
	}

	config, err := LoadInstanceConfig(instanceName)
	if err != nil {
		startErr = err
		return err
	}

	logger.GetLogger().Infof("Starting server for instance: %s", instanceName)

	// 同步实例镜像目录（增量）
	mirrorDir, err = SyncInstanceMirror(instanceName, config)
	if err != nil {
		wrappedErr := fmt.Errorf("failed to setup instance mirror: %w", err)
		startErr = wrappedErr
		return wrappedErr
	}

	// Build the command
	// Quote parameters that may contain special characters to prevent parsing issues
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

	// Handle BindDomain resolution and IP parameter injection
	if config.BindDomain != "" {
		// Resolve domain to IPv4 addresses
		if ipv4Addrs, err := common.ResolveDomainToIPv4(config.BindDomain); err == nil && len(ipv4Addrs) > 0 {
			// Use the first resolved IPv4 address
			ipv4Addr := ipv4Addrs[0]

			// Check if -ip or -serverip are already in CustomStartParameters
			customParams := strings.Fields(config.CustomStartParameters)
			ipFound := false
			serverIpFound := false

			// Look for existing -ip and -serverip parameters to replace them
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

			// If we found and replaced parameters in CustomStartParameters, rebuild args
			if ipFound || serverIpFound {
				// Rebuild args with mapParam and modified custom parameters
				args = []string{mapParam}
				args = append(args, customParams...)
			} else {
				// Add the parameters if they weren't found in CustomStartParameters
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
		clusterDir := filepath.Join(BaseDir, "clusters", config.ClusterID)
		if strings.Contains(config.CustomStartParameters, "-ClusterDirOverride") {
			args = append(args,
				fmt.Sprintf("-ClusterId=%s", config.ClusterID),
			)
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
		if FileExists(arkApiExe) {
			arkExe = arkApiExe
			arkAsaApiRunning = true
		}
	}

	// Get the game log file path (v2: per-instance directory)
	gameLogPath, err := GetGameLogFilePath(instanceName)
	if err != nil {
		startErr = fmt.Errorf("failed to get game log file path: %w", err)
		return startErr
	}

	if opts.GameLogPathCallback != nil {
		opts.GameLogPathCallback(gameLogPath)
	}

	// Create the log file if it doesn't exist
	if _, err := os.Stat(gameLogPath); os.IsNotExist(err) {
		// Create the log file with empty content
		if err := os.WriteFile(gameLogPath, []byte(""), 0644); err != nil {
			return fmt.Errorf("failed to create log file %s: %w", gameLogPath, err)
		}
		logger.GetLogger().Infof("Created log file: %s", gameLogPath)
	}

	// 设置进程工作目录为镜像内的 exe 目录
	exeWorkDir := filepath.Join(mirrorDir, "ShooterGame/Binaries/Win64")

	if arkAsaApiRunning {
		logWriter := &LogWriter{
			loggerFn: func(msg string) {
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
		defer pp.Close()
		c := pp.Command(arkExe, args...)
		c.Dir = exeWorkDir
		if err := c.Start(); err != nil {
			startErr = fmt.Errorf("failed to start server: %w", err)
			return startErr
		}
		go arkApiCleanConsoleOutput(pp, logWriter)
		logger.GetLogger().Infof("[%s] Redirecting AsaApiLoader output to logger", instanceName)

		_pid, err := WaitArkApiRunServer(ctx, config.QueryPort)
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
		// Save the PID to the instance directory
		pid = cmd.Process.Pid
	}

	if err := SaveInstancePID(instanceName, pid); err != nil {
		logger.GetLogger().Warnf("Failed to save PID for instance %s: %v", instanceName, err)
	}

	logger.GetLogger().Infof("Server started for instance: %s. It should be fully operational in approximately 60 seconds.", instanceName)
	logger.GetLogger().Infof("Game log file: %s", gameLogPath)

	if ctx.Err() != nil {
		killGameServer(pid)
	}

	if opts.PidCallback != nil {
		opts.PidCallback(pid)
	}

	// Monitor for mod information in a separate goroutine
	go MonitorAndExtractModInfo(ctx, gameLogPath, instanceName)

	go waitServerStartup(pid, gameLogPath, func(startup bool, err string) {
		if startup {
			if opts.OnRestartStartupComplete != nil {
				_ = WriteInstanceState(instanceName, StatusRestarted, "")
				opts.OnRestartStartupComplete(instanceName)
			}
			_ = WriteInstanceState(instanceName, StatusStarted, "")
			if opts.WaitServerCompleted {
				startupSuccess <- true
			}
		} else {
			_ = WriteInstanceState(instanceName, StatusStartFailed, err)
			initFailed <- fmt.Errorf("%s", err)
		}
	}, func() {
		_ = WriteInstanceState(instanceName, StatusStartStartInitializationSuccessful, "")
		// v2: 不调用 confReset（镜像目录独立，无需恢复原始 Config）
		_ = WriteInstanceState(instanceName, StatusStarting, "")
		initSuccessful <- true

		if opts.GameInitializationSuccessful != nil {
			opts.GameInitializationSuccessful()
		}
	})

	if opts.WaitServerCompleted {
		WaitServerCompletedCtx, WaitServerCompletedCancel := context.WithCancel(ctx)
		defer WaitServerCompletedCancel()

		go func() {
			if exited := WaitGamePidExit(WaitServerCompletedCtx, pid); exited {
				WaitServerCompletedCancel()
			}
		}()

		TailLogFileWithLinesContext(WaitServerCompletedCtx, gameLogPath, 0, func(line string) {
			// Check for successful startup message
			if strings.Contains(line, "Server has completed startup and is now advertising for join") {
				startupSuccess <- true
			}
		})

		select {
		case <-WaitServerCompletedCtx.Done():
			startErr = fmt.Errorf("start game server exited")
			return startErr
		case <-startupSuccess:
			return nil
		}
	} else {
		select {
		case err := <-initFailed:
			return err
		case <-initSuccessful:
		}
	}

	return nil
}

// StopServer stops a server instance (public version with CAS check)
func StopServer(instanceName string) error {
	// CAS: 原子检查状态并转换到 stopping
	ok, err := CompareAndSwapInstanceState(instanceName,
		[]InstanceStatus{StatusStarted},
		StatusStopping)
	if err != nil {
		return fmt.Errorf("failed to check instance state: %w", err)
	}
	if !ok {
		return ErrOperationNotAllowed
	}

	return stopServerInternal(instanceName)
}

// stopServerInternal stops a server instance (internal version without CAS, for RestartServer)
func stopServerInternal(instanceName string) error {
	var (
		pid         int
		gameLogPath string
	)
	// 使用一个变量来记录错误，以便在函数结束时检查是否需要记录失败状态
	var stopErr error

	// 使用 defer 来在函数退出时记录失败状态（如果存在错误）
	defer func() {
		if stopErr != nil {
			// 保留失败记录（历史）
			_ = WriteInstanceState(instanceName, StatusStopFailed, stopErr.Error())
			// 回滚到操作前状态：服务器仍在运行则为 started，已停止则为 stopped
			rollbackStatus := StatusStopped
			if running, err := IsServerRunning(instanceName); err == nil && running {
				rollbackStatus = StatusStarted
			}
			_ = WriteInstanceState(instanceName, rollbackStatus, "")
		}
	}()

	running, err := IsServerRunning(instanceName)
	if err != nil || !running {
		logger.GetLogger().Warnf("Server for instance %s is not running.", instanceName)
		stopErr = fmt.Errorf("server for instance %s is not running", instanceName)
		return stopErr
	}

	logger.GetLogger().Infof("Stopping server for instance: %s", instanceName)
	// Try graceful shutdown with RCON

	config, configErr := LoadInstanceConfig(instanceName)
	if configErr != nil {
		stopErr = fmt.Errorf("failed to load instance config: %w", configErr)
		return stopErr
	}
	pid, err = GetPIDByPort(config.Port)
	if err != nil {
		stopErr = fmt.Errorf("failed to find process PID: %w", err)
		return stopErr
	}

	logger.GetLogger().Infof("Stopping server for instance: %s pid: %d", instanceName, pid)

	gameLogPath, _ = GetGameLogFilePath(instanceName)

	if err := SaveWorldSafely(instanceName); err != nil {
		stopErr = fmt.Errorf("failed to save world safely: %w", err)
		return stopErr
	}

	response, err := SendRCONCommand(instanceName, "DoExit")

	if err == nil && strings.Contains(response, "Exiting") {
		logger.GetLogger().Infof("Server instance %s reported 'Exiting...'. Awaiting shutdown...", instanceName)
	} else {
		if taskkillErr := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid)).Run(); taskkillErr != nil {
			logger.GetLogger().Warnf("failed to kill process PID %d: %s", pid, taskkillErr.Error())
			_ = exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid)).Run()
		}
	}

	// Wait for server to fully stop using waitServerStopped
	waitCtx, waitCancel := context.WithCancel(context.Background())
	defer waitCancel()

	stopped := make(chan struct{})
	go waitServerStopped(waitCtx, pid, gameLogPath,
		func() {
			logger.GetLogger().Infof("Server %s received closing request", instanceName)
		},
		func(complete bool) {
			close(stopped)
		})

	// Wait for complete stop or timeout
	select {
	case <-stopped:
		logger.GetLogger().Infof("Server for instance %s has exited.", instanceName)
	case <-time.After(5 * time.Minute):
		logger.GetLogger().Warnf("Process %d did not exit within 5min, force killing", pid)
		_ = exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid)).Run()
		waitCancel() // 取消 waitServerStopped goroutine 的 context
	}

	// Cleanup
	if config.EnableAsaPlugin {
		if pid2, pidErr := GetInstancePID(instanceName); pidErr == nil {
			_ = exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid2)).Run()
		}
	}

	_ = WriteInstanceState(instanceName, StatusStopped, "")
	return nil
}

// ForceStopServer 强制停止实例：杀死进程 + 重置状态 + 清理镜像
// v2: 不再需要 WaitForNoInitializing（每个实例的镜像独立）
func ForceStopServer(instanceName string) error {
	// 1. 通过 WMI 查找进程（best effort，端口未监听时也能找到）
	cfg, err := LoadInstanceConfig(instanceName)
	if err == nil {
		if pid, pidErr := findServerPIDByPort(cfg.Port); pidErr == nil && pid > 0 {
			killGameServer(pid)
		}
	}
	// 2. 尝试杀死已保存的 PID（插件进程，best effort）
	if pid2, pidErr := GetInstancePID(instanceName); pidErr == nil && pid2 > 0 {
		killGameServer(pid2)
	}
	// 3. 重置状态为 stopped
	_ = WriteInstanceState(instanceName, StatusStopped, "")
	// 4. 清理镜像目录
	if err := CleanupInstanceMirror(instanceName); err != nil {
		logger.GetLogger().Warnf("Failed to cleanup instance mirror for %s: %v", instanceName, err)
	}
	return nil
}

func KillServer(instanceName string) error {

	cfg, err := LoadInstanceConfig(instanceName)
	if err != nil {
		return err
	}
	pid, err := GetPIDByPort(cfg.Port)
	if err != nil {
		return err
	}

	err = exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid)).Run()
	if err != nil {
		return err
	}
	_ = WriteInstanceState(instanceName, StatusStopped, "")
	return nil
}

// RestartServer restarts a server instance
func RestartServer(instanceName string, options ...StartServerOptionsFunc) error {
	// CAS: 原子检查状态并转换到 restarting
	ok, err := CompareAndSwapInstanceState(instanceName,
		[]InstanceStatus{StatusStarted},
		StatusRestarting)
	if err != nil {
		return fmt.Errorf("failed to check instance state: %w", err)
	}
	if !ok {
		return ErrOperationNotAllowed
	}

	// 使用一个变量来记录错误，以便在函数结束时检查是否需要记录失败状态
	var restartErr error

	// 使用 defer 来在函数退出时记录失败状态（如果存在错误）
	defer func() {
		if restartErr != nil {
			// 保留失败记录（历史）
			_ = WriteInstanceState(instanceName, StatusRestartFailed, restartErr.Error())
			// 回滚到操作前状态：服务器仍在运行则为 started，已停止则为 stopped
			rollbackStatus := StatusStopped
			if running, err := IsServerRunning(instanceName); err == nil && running {
				rollbackStatus = StatusStarted
			}
			_ = WriteInstanceState(instanceName, rollbackStatus, "")
		}
	}()

	// 使用内部版本（跳过 CAS），因为 RestartServer 已经做了 CAS
	if err := stopServerInternal(instanceName); err != nil {
		restartErr = err
		return err
	}
	// v2: 不再需要 time.Sleep（镜像独立，不需要等待 junction 释放）

	startOpts := append([]StartServerOptionsFunc{WithWaitServerCompleted()}, options...)
	if err := StartServer(instanceName, startOpts...); err != nil {
		restartErr = err
		return err
	}
	return nil
}

// SendRCONCommand sends an RCON command to a server using gorcon/rcon library
func SendRCONCommand(instanceName string, command string) (string, error) {
	running, err := IsServerRunning(instanceName)
	if err != nil || !running {
		return "", fmt.Errorf("server for instance %s is not running", instanceName)
	}

	config, err := LoadInstanceConfig(instanceName)
	if err != nil {
		return "", err
	}

	// Validate RCON password is not empty
	if config.ServerAdminPassword == "" {
		return "", fmt.Errorf("RCON password is empty for instance %s. Please set ServerAdminPassword in config", instanceName)
	}

	// Connect to RCON server with retry logic
	rconAddr := fmt.Sprintf("localhost:%d", config.RCONPort)
	logger.GetLogger().Infof("Instance: %s Connecting to RCON server at %s...", instanceName, rconAddr)

	var client *rcon.Conn
	var connectErr error

	// Try to connect with timeout and retry
	for attempt := 1; attempt <= 3; attempt++ {
		client, connectErr = rcon.Dial(rconAddr, config.ServerAdminPassword)
		if connectErr == nil {
			logger.GetLogger().Info("Connected to RCON server")
			break
		}

		logger.GetLogger().Warnf("Attempt %d failed: %v", attempt, connectErr)
		if attempt < 3 {
			logger.GetLogger().Info("   Retrying in 2 seconds...")
			time.Sleep(2 * time.Second)
		}
	}

	if connectErr != nil {
		logger.GetLogger().Errorf("RCON connection to %s failed after 3 attempts: %v", rconAddr, connectErr)
		return "", fmt.Errorf("failed to connect to RCON server at %s: %w", rconAddr, connectErr)
	}

	defer client.Close()
	// Send command
	logger.GetLogger().Infof("Sending RCON command '%s' to %s", command, rconAddr)
	response, err := client.Execute(command)
	if err != nil {
		return "", fmt.Errorf("RCON command execution failed: %w", err)
	}

	logger.GetLogger().Infof("RCON response: %s", response)
	return response, nil
}

// GetRunningInstances returns a list of running instances
func GetRunningInstances() ([]string, error) {
	instances, err := GetAvailableInstances()
	if err != nil {
		return nil, err
	}

	var running []string
	for _, instanceName := range instances {
		if isRunning, err := IsServerRunning(instanceName); err == nil && isRunning {
			running = append(running, instanceName)
		}
	}

	return running, nil
}

// SaveInstancePID saves the PID of a running instance to its directory
func SaveInstancePID(instanceName string, pid int) error {
	instanceDir := filepath.Join(InstancesDir, instanceName)
	if err := os.MkdirAll(instanceDir, 0755); err != nil {
		return fmt.Errorf("failed to create instance directory: %w", err)
	}

	pidFile := filepath.Join(instanceDir, "pid")
	return os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0644)
}

// GetInstancePID retrieves the PID of a running instance from its directory
func GetInstancePID(instanceName string) (int, error) {
	instanceDir := filepath.Join(InstancesDir, instanceName)
	pidFile := filepath.Join(instanceDir, "pid")

	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("failed to parse PID: %w", err)
	}

	return pid, nil
}

// quotifyIfNeeded wraps a string in double quotes if it contains special characters
// that could interfere with command-line parameter parsing
func quotifyIfNeeded(value string) string {
	if value == "" {
		return value
	}
	// Replace spaces with dashes, converting multiple consecutive spaces to a single dash
	value = strings.TrimSpace(value)
	value = regexp.MustCompile(`\s+`).ReplaceAllString(value, "-")
	// Check if the value contains any special characters that need quoting
	// ARK server uses ? as parameter separator, so we need to quote values containing special characters
	return fmt.Sprintf("\"%s\"", value)
}

// SyncGameConfigToInstance reads Game.ini and GameUserSettings.ini from the base server directory
// and merges them with the instance's config files, only adding entries that don't exist in the instance files
func SyncGameConfigToInstance(instanceName string) error {
	baseConfigDir := filepath.Join(ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer")
	instanceConfigDir := filepath.Join(InstancesDir, instanceName, "Config")

	// Ensure instance config directory exists
	if err := os.MkdirAll(instanceConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create instance config directory: %w", err)
	}

	// Process Game.ini
	gameIniSourcePath := filepath.Join(baseConfigDir, "Game.ini")
	gameIniDestPath := filepath.Join(instanceConfigDir, "Game.ini")

	if err := syncConfigFile(gameIniSourcePath, gameIniDestPath); err != nil {
		return fmt.Errorf("failed to sync Game.ini: %w", err)
	}

	// Process GameUserSettings.ini
	gameUserSettingsSourcePath := filepath.Join(baseConfigDir, "GameUserSettings.ini")
	gameUserSettingsDestPath := filepath.Join(instanceConfigDir, "GameUserSettings.ini")

	if err := syncConfigFile(gameUserSettingsSourcePath, gameUserSettingsDestPath); err != nil {
		return fmt.Errorf("failed to sync GameUserSettings.ini: %w", err)
	}

	logger.GetLogger().Infof("Configuration files synced for instance '%s'", instanceName)
	return nil
}

// syncConfigFile synchronizes a single INI config file
// Copies source config file directly to destination using io.Copy
func syncConfigFile(sourcePath, destPath string) error {
	// Check if source file exists
	if _, err := os.Stat(sourcePath); os.IsNotExist(err) {
		return nil // Source doesn't exist, nothing to sync
	}

	// Open source file
	srcFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to open source config file: %w", err)
	}
	defer srcFile.Close()

	// Create or truncate destination file
	destFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create destination config file: %w", err)
	}
	defer destFile.Close()

	// Copy file content directly
	if _, err := io.Copy(destFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy config file: %w", err)
	}

	return nil
}
