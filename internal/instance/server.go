package instance

import (
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/installer"
	"asa-server/internal/mirror"
	"asa-server/internal/plugindata"
	procpkg "asa-server/internal/process"
	"asa-server/internal/rconx"
	"asa-server/internal/runner"
	statepkg "asa-server/internal/state"
	"asa-server/pkg/console"
	"asa-server/pkg/fsutil"
	"asa-server/pkg/logger"
	"asa-server/pkg/netutil"
	"asa-server/pkg/procx"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ErrOperationNotAllowed is returned when an operation is not allowed in the current instance state
var ErrOperationNotAllowed = fmt.Errorf("operation not allowed in current state")

// GetGameLogFilePath returns the full path to the log file for a given instance
// v2: uses per-instance log directory under InstancesDir
func GetGameLogFilePath(instanceName string) (string, error) {
	logsDir := filepath.Join(cfgpkg.InstancesDir, instanceName, "Logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create logs directory: %w", err)
	}
	logPath := filepath.Join(logsDir, "ShooterGame.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		os.WriteFile(logPath, nil, 0644)
	}
	return logPath, nil
}

// GetAsaApiLogFilePath returns the full path to the AsaApiLoader console log for a given instance
func GetAsaApiLogFilePath(instanceName string) (string, error) {
	instanceDir := filepath.Join(cfgpkg.InstancesDir, instanceName)
	if err := os.MkdirAll(instanceDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create instance directory: %w", err)
	}
	logPath := filepath.Join(instanceDir, "arkAsaApi.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		os.WriteFile(logPath, nil, 0644)
	}
	return logPath, nil
}

// IsServerRunning checks if a server instance is running
// It checks by verifying if both the game port and RCON port are listening
// This uniquely identifies the specific server instance

// IsServerRunningByPID checks if a server instance is running by verifying if the saved PID process exists
// This method retrieves the PID from the instance directory and checks if the process is still active

type StartServerOptions struct {
	GameLogPathCallback          func(path string)
	GameInitializationSuccessful func()
	WaitServerCompleted          bool
	ParentCtx                    context.Context
	PidCallback                  func(pid int)
	OnRestartStartupComplete     func(instanceName string)
	RetryOnNetworkError          int           // serverUnreachable 错误重试次数，0 → 默认 3
	RetryInterval                time.Duration // 重试间隔，0 → 默认 5s
	StatePreset                  bool          // CAS 已由调用方完成，跳过内部 CAS
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

func WithRetryOnNetworkError(count int) StartServerOptionsFunc {
	return func(options *StartServerOptions) { options.RetryOnNetworkError = count }
}

func WithRetryInterval(d time.Duration) StartServerOptionsFunc {
	return func(options *StartServerOptions) { options.RetryInterval = d }
}

// WithStatePreset 表示调用方已完成 CAS，函数内部跳过重复的原子状态检查。
func WithStatePreset() StartServerOptionsFunc {
	return func(options *StartServerOptions) { options.StatePreset = true }
}

func isNetworkRetriableStartupError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "ApiError: Failed (serverUnreachable)")
}

// StartServer starts a server instance (public version with CAS check)
func StartServer(instanceName string, options ...StartServerOptionsFunc) error {
	tmpOpts := new(StartServerOptions)
	for _, o := range options {
		o(tmpOpts)
	}

	if !tmpOpts.StatePreset {
		// CAS: 原子检查状态并转换到 start_initialization
		ok, err := statepkg.CompareAndSwapInstanceState(instanceName,
			[]statepkg.InstanceStatus{statepkg.StatusStopped, statepkg.StatusStartFailed, statepkg.StatusStopFailed, statepkg.StatusRestartFailed, ""},
			statepkg.StatusStartStartInitialization)
		if err != nil {
			return fmt.Errorf("failed to check instance state: %w", err)
		}
		if !ok {
			return ErrOperationNotAllowed
		}
	}

	retryCount := tmpOpts.RetryOnNetworkError
	if retryCount == 0 {
		retryCount = 3
	}
	retryInterval := tmpOpts.RetryInterval
	if retryInterval == 0 {
		retryInterval = 5 * time.Second
	}

	var lastErr error
	for attempt := 0; attempt <= retryCount; attempt++ {
		if attempt > 0 {
			logger.Infof("服务器 %s 网络错误，第 %d/%d 次重试，等待 %v...", instanceName, attempt, retryCount, retryInterval)
			time.Sleep(retryInterval)
		}
		lastErr = startServerInternal(instanceName, options...)
		if lastErr == nil {
			return nil
		}
		if isNetworkRetriableStartupError(lastErr) && attempt < retryCount {
			logger.Warnf("服务器 %s 启动失败（网络错误），将重试 (%d/%d): %v", instanceName, attempt+1, retryCount, lastErr)
			continue
		}
		_ = statepkg.WriteInstanceState(instanceName, statepkg.StatusStopped, "")
		return lastErr
	}
	_ = statepkg.WriteInstanceState(instanceName, statepkg.StatusStopped, "")
	return lastErr
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
			_ = statepkg.WriteInstanceState(instanceName, statepkg.StatusStartFailed, startErr.Error())
		}
	}()

	// 更新期间 server-files 正在被增删，此时做镜像同步只会同步出残缺镜像
	if installer.IsUpdatingServerFiles() {
		err := fmt.Errorf("server files are being updated, cannot start instance %s", instanceName)
		logger.Warnf("%v", err)
		startErr = err
		return err
	}

	// Check for duplicate ports
	if err := cfgpkg.CheckForDuplicatePorts(); err != nil {
		logger.Errorf("Port conflicts detected: %v", err)
		startErr = err
		return err
	}

	config, err := cfgpkg.LoadInstanceConfig(instanceName)
	if err != nil {
		startErr = err
		return err
	}

	logger.Infof("Starting server for instance: %s", instanceName)

	// 同步实例镜像目录（增量）
	mirrorDir, err = mirror.SyncInstanceMirror(instanceName, config)
	if err != nil {
		wrappedErr := fmt.Errorf("failed to setup instance mirror: %w", err)
		startErr = wrappedErr
		return wrappedErr
	}

	// 校验镜像关键路径完整性，不完整时自动重建
	mirrorDir, err = mirror.VerifyAndRepairInstanceMirror(instanceName, config, mirrorDir)
	if err != nil {
		startErr = err
		return err
	}
	// 插件的配置与运行期数据必须在镜像同步与校验**之后**才能注入：
	// 放在之前会被同步的 MD5 回写覆盖掉。
	// 先 Rescue 再 Inject 的顺序不能颠倒 —— 上一轮若是崩溃退出，镜像里留着的
	// 才是最新数据，先抢救回实例目录，再拿实例目录那一份注入。
	plugindata.Rescue(instanceName, mirrorDir)
	plugindata.Inject(instanceName, mirrorDir)

	// The mirror was just (re)built as root; on Linux the game process runs
	// dropped to a dedicated non-root user, which must own this tree to write
	// saves/logs/dumps into it. No-op on Windows and when not managing a
	// dropped user. See docs/UMU_RUNTIME_USER_PLAN.md §5.2.
	if err := runner.ChownMirrorForRuntime(mirrorDir); err != nil {
		startErr = fmt.Errorf("为降权运行时用户准备实例镜像目录失败: %w", err)
		return startErr
	}

	// 镜像里的 Config / Logs / Save 是指回 instances/<name>/ 的软链，Mods 与
	// ModsUserData 则指回 server-files —— 上面那次 chown 用的是 Lchown，只改链接
	// 本身，目标目录仍归 root。所以链接目标必须单独按「root 与运行时用户共写」
	// 处理，否则游戏能看到目录却写不进去。Windows 上是空实现。
	// 见 docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md §3.6 / §4。
	if err := runner.PrepareSharedTree(filepath.Join(cfgpkg.InstancesDir, instanceName)); err != nil {
		startErr = fmt.Errorf("为降权运行时用户准备实例目录失败: %w", err)
		return startErr
	}

	exeWorkDir := filepath.Join(mirrorDir, "ShooterGame/Binaries/Win64")

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
		fmt.Sprintf("-RCONPort=%d", config.RCONPort),
		"-game",
		"-server",
		"-log",
	)

	// Handle BindDomain resolution and IP parameter injection
	if config.BindDomain != "" {
		// Resolve domain to IPv4 addresses
		if ipv4Addrs, err := netutil.ResolveDomainToIPv4(config.BindDomain); err == nil && len(ipv4Addrs) > 0 {
			// Use the first resolved IPv4 address
			ipv4Addr := ipv4Addrs[0]

			ipFound := false
			serverIpFound := false

			// Replace -ip and -serverip in-place within existing args
			for i, param := range args {
				if strings.HasPrefix(param, "-ip=") {
					args[i] = fmt.Sprintf("-ip=%s", ipv4Addr)
					ipFound = true
				} else if param == "-ip" && i+1 < len(args) {
					args[i+1] = ipv4Addr
					ipFound = true
				} else if strings.HasPrefix(param, "-serverip=") {
					args[i] = fmt.Sprintf("-serverip=%s", ipv4Addr)
					serverIpFound = true
				} else if param == "-serverip" && i+1 < len(args) {
					args[i+1] = ipv4Addr
					serverIpFound = true
				}
			}

			if !ipFound {
				args = append(args, fmt.Sprintf("-ip=%s", ipv4Addr))
			}
			if !serverIpFound {
				args = append(args, fmt.Sprintf("-serverip=%s", ipv4Addr))
			}
		} else {
			logger.Warnf("Failed to resolve domain %s to IPv4 address, ignoring BindDomain parameter", config.BindDomain)
		}
	}

	if config.ModIDs != "" {
		args = append(args, fmt.Sprintf("-mods=%s", config.ModIDs))
	}

	if config.ClusterID != "" {
		clusterDir := runner.GamePath(filepath.Join(cfgpkg.BaseDir, "clusters", config.ClusterID))
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
		if fsutil.FileExists(arkApiExe) {
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
		logger.Infof("Created log file: %s", gameLogPath)
	}

	// Fail fast with an actionable message if the Linux Wine/Proton runtime
	// isn't in place yet (no-op on Windows) — otherwise runner.Run reports a
	// low-level "umu-run not found" from deep in the launch path. See
	// docs/SETUP_FLOW_OPTIMIZATION_PLAN.md §3.5.
	if err := runner.CheckRuntime(); err != nil {
		startErr = fmt.Errorf("无法启动实例：%w", err)
		return startErr
	}

	// Second net for "game runs as a dedicated non-root user": re-check that
	// the runtime user still exists and can actually write the dirs it needs
	// (with the real-write deep probe forced on). No-op on Windows / when not
	// managing a dropped user. See docs/UMU_RUNTIME_USER_PLAN.md §4.4.
	if probs := runner.VerifyRuntimeAccessForLaunch(); len(probs) > 0 {
		startErr = fmt.Errorf("无法启动实例：降权运行时环境自检未通过：\n%s", formatRunnerProblems(probs))
		return startErr
	}

	// Launch arkExe (ArkAscendedServer.exe, or AsaApiLoader.exe wrapping it
	// with the same arguments — runner treats both identically, see
	// internal/runner's package doc). Windows: direct exec/pty, unchanged
	// from before. Linux: wrapped in umu-run — see
	// docs/LINUX_COMPATIBILITY_PLAN.md §5.1.
	//
	// context.Background(), not the local ctx: this launch must outlive
	// startServerInternal's own return. defer cancel() above fires as soon
	// as this function returns (which happens once startup is confirmed,
	// not when the server stops) — passing ctx here would have
	// exec.CommandContext kill the just-started server the moment this
	// function's own bookkeeping finishes.
	handle, err := runner.Run(context.Background(), arkExe, args, runner.Options{
		Dir: exeWorkDir,
		PTY: arkAsaApiRunning,
	})
	if err != nil {
		startErr = fmt.Errorf("failed to start server: %w", err)
		return startErr
	}

	// launcher_pid: on Windows this equals the game PID saved below; on
	// Linux it's umu-run's PID (== the process-group id for the whole
	// launch, since runner.Run sets Setsid there). Saved unconditionally so
	// a best-effort kill always has a value to fall back to even if the
	// game-PID resolution below fails.
	if err := procpkg.SaveLauncherPID(instanceName, handle.LauncherPID); err != nil {
		logger.Warnf("Failed to save launcher PID for instance %s: %v", instanceName, err)
	}

	// The launched process must keep running independently of this
	// function; reap it once it exits so Linux doesn't accumulate a zombie
	// waiting to be waited on (Windows doesn't need this, but calling Wait
	// unconditionally is harmless there too — previously only the PTY/AsaApi
	// branch did this).
	go func() {
		_ = handle.Wait()
		if handle.PTY != nil {
			_ = handle.PTY.Close()
		}
	}()

	if arkAsaApiRunning {
		// 将 PTY 输出清洗后落盘，每次启动清空
		// 打开失败只告警，不影响开服
		if apiLogPath, logErr := GetAsaApiLogFilePath(instanceName); logErr != nil {
			logger.Warnf("Failed to resolve AsaApi log path for instance %s: %v", instanceName, logErr)
		} else if apiLogFile, openErr := os.OpenFile(apiLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644); openErr != nil {
			logger.Warnf("Failed to open AsaApi log file %s: %v", apiLogPath, openErr)
		} else {
			// cleaner 独占该句柄，pty 关闭后 CleanScreenOutput 返回并释放
			// AsaApiLoader 用光标定位排版，必须走 CleanScreenOutput 而非 CleanConsoleOutput
			go func() {
				defer apiLogFile.Close()
				_ = console.CleanScreenOutput(handle.PTY, apiLogFile)
			}()
		}

		// 保存 AsaApiLoader（asaServerApi）进程 PID，供停止时先于游戏进程结束。
		// Linux 上这与 launcher_pid 是同一个值（进程组 leader），杀哪个都一样。
		if err := procpkg.SaveAsaServerApiPID(instanceName, handle.LauncherPID); err != nil {
			logger.Warnf("Failed to save AsaApiLoader PID for instance %s: %v", instanceName, err)
		}
	}

	// Resolve the real ArkAscendedServer.exe PID. Windows launching it
	// directly (no AsaApiLoader) already has the right answer in
	// handle.LauncherPID; every other case needs a poll — AsaApiLoader.exe
	// spawns it as a child (either platform), and Linux's umu-run wraps
	// every launch regardless of AsaApiLoader. See waitForGamePID's doc
	// comment and docs/LINUX_COMPATIBILITY_PLAN.md §5.3.
	if arkAsaApiRunning || !runner.LauncherIsDirect() {
		gamePID, err := waitForGamePID(ctx, config.SaveDir)
		if err != nil {
			startErr = fmt.Errorf("failed to start server: %w", err)
			return startErr
		}
		pid = int(gamePID)
	} else {
		pid = handle.LauncherPID
	}

	if err := procpkg.SaveInstancePID(instanceName, pid); err != nil {
		logger.Warnf("Failed to save PID for instance %s: %v", instanceName, err)
	}

	// 进程起来了就开始给插件数据库做在线快照：回收只在正常停止时执行，
	// 崩溃、断电、管理器被杀这些路径靠快照把最坏损失收窄到一个周期。
	plugindata.StartSnapshots(instanceName, mirrorDir, pluginSnapshotInterval(config))

	logger.Infof("Server started for instance: %s. It should be fully operational in approximately 60 seconds.", instanceName)
	logger.Infof("Game log file: %s", gameLogPath)

	// Monitor for mod information in a separate goroutine
	go MonitorAndExtractModInfo(ctx, gameLogPath, instanceName)

	go waitServerStartup(pid, gameLogPath, func(startup bool, err string) {
		if startup {
			if opts.OnRestartStartupComplete != nil {
				_ = statepkg.WriteInstanceState(instanceName, statepkg.StatusRestarted, "")
				opts.OnRestartStartupComplete(instanceName)
			}
			_ = statepkg.WriteInstanceState(instanceName, statepkg.StatusStarted, "")
			if opts.WaitServerCompleted {
				startupSuccess <- true
			}
		} else {
			_ = statepkg.WriteInstanceState(instanceName, statepkg.StatusStartFailed, err)
			initFailed <- fmt.Errorf("%s", err)
		}
	}, func() {
		_ = statepkg.WriteInstanceState(instanceName, statepkg.StatusStartStartInitializationSuccessful, "")
		// v2: 不调用 confReset（镜像目录独立，无需恢复原始 Config）
		_ = statepkg.WriteInstanceState(instanceName, statepkg.StatusStarting, "")
		initSuccessful <- true

		if opts.GameInitializationSuccessful != nil {
			opts.GameInitializationSuccessful()
		}
	})

	if ctx.Err() != nil {
		killGameServer(pid)
	}

	if opts.PidCallback != nil {
		opts.PidCallback(pid)
	}

	select {
	case err := <-initFailed:
		return err
	case <-initSuccessful:
	}

	if opts.WaitServerCompleted {
		<-startupSuccess
	}
	return nil
}

// StopServer stops a server instance (public version with CAS check)
func StopServer(instanceName string, options ...StartServerOptionsFunc) error {
	opts := new(StartServerOptions)
	for _, o := range options {
		o(opts)
	}

	if !opts.StatePreset {
		// CAS: 原子检查状态并转换到 stopping
		ok, err := statepkg.CompareAndSwapInstanceState(instanceName,
			[]statepkg.InstanceStatus{statepkg.StatusStarted},
			statepkg.StatusStopping)
		if err != nil {
			return fmt.Errorf("failed to check instance state: %w", err)
		}
		if !ok {
			return ErrOperationNotAllowed
		}
	}

	return stopServerInternal(instanceName)
}

// stopServerInternal stops a server instance (internal version without CAS, for RestartServer)
func stopServerInternal(instanceName string) error {
	var (
		pid         int
		gameLogPath string
		ctx         = context.Background()
	)
	// 使用一个变量来记录错误，以便在函数结束时检查是否需要记录失败状态
	var stopErr error

	// 使用 defer 来在函数退出时记录失败状态（如果存在错误）
	defer func() {
		if stopErr != nil {
			// 保留失败记录（历史）
			_ = statepkg.WriteInstanceState(instanceName, statepkg.StatusStopFailed, stopErr.Error())
			// 回滚到操作前状态：服务器仍在运行则为 started，已停止则为 stopped
			rollbackStatus := statepkg.StatusStopped
			if running, err := procpkg.IsServerRunning(instanceName); err == nil && running {
				rollbackStatus = statepkg.StatusStarted
			}
			_ = statepkg.WriteInstanceState(instanceName, rollbackStatus, "")
		}
	}()

	running, err := procpkg.IsServerRunning(instanceName)
	if err != nil || !running {
		logger.Warnf("Server for instance %s is not running.", instanceName)
		stopErr = fmt.Errorf("server for instance %s is not running", instanceName)
		return stopErr
	}

	logger.Infof("Stopping server for instance: %s", instanceName)

	// 先停快照：saveworld 与进程退出期间的磁盘 I/O 不该再被 VACUUM INTO 争用
	plugindata.StopSnapshots(instanceName)

	// Try graceful shutdown with RCON

	config, configErr := cfgpkg.LoadInstanceConfig(instanceName)
	if configErr != nil {
		stopErr = fmt.Errorf("failed to load instance config: %w", configErr)
		return stopErr
	}
	pid, err = procx.PIDByPort(config.Port)
	if err != nil {
		stopErr = fmt.Errorf("failed to find process PID: %w", err)
		return stopErr
	}

	logger.Infof("Stopping server for instance: %s pid: %d", instanceName, pid)

	gameLogPath, _ = GetGameLogFilePath(instanceName)

	if err := SaveWorldSafely(instanceName); err != nil {
		stopErr = fmt.Errorf("failed to save world safely: %w", err)
		return stopErr
	}

	response, err := rconx.Execute(ctx, instanceName, "DoExit")

	if err == nil && strings.Contains(response, "Exiting") {
		logger.Infof("Server instance %s reported 'Exiting...'. Awaiting shutdown...", instanceName)
	} else {
		if termErr := procx.Terminate(pid); termErr != nil {
			logger.Warnf("failed to kill process PID %d: %s", pid, termErr.Error())
			_ = procx.Kill(pid)
		}
	}

	// Wait for server to fully stop using waitServerStopped
	waitCtx, waitCancel := context.WithCancel(context.Background())
	defer waitCancel()

	stopped := make(chan struct{})
	go waitServerStopped(waitCtx, pid, gameLogPath,
		func() {
			logger.Infof("Server %s received closing request", instanceName)
		},
		func(complete bool) {
			close(stopped)
		})

	// Wait for complete stop or timeout
	select {
	case <-stopped:
		logger.Infof("Server for instance %s has exited.", instanceName)
	case <-time.After(5 * time.Minute):
		logger.Warnf("Process %d did not exit within 5min, force killing", pid)
		// Tree kill, not single-PID: this is the last-resort path
		// (docs/LINUX_COMPATIBILITY_PLAN.md §5.4's "强杀进程树" row). On
		// Linux, pid is a member of the umu-run/bwrap/wine process group —
		// a single-PID kill here would leave that whole tree orphaned,
		// holding the Wine prefix busy for the next start. On Windows this
		// is a harmless no-op difference from plain Kill (the game process
		// has no meaningful children).
		_ = procx.KillTree(pid)
		waitCancel() // 取消 waitServerStopped goroutine 的 context
	}

	// Cleanup
	if config.EnableAsaPlugin {
		if pid2, pidErr := procpkg.GetInstancePID(instanceName); pidErr == nil {
			_ = procx.Kill(pid2)
		}
	}

	// 进程已完全退出，此时才能安全地整组拷回 SQLite 文件 ——
	// 运行中拷会拷出主库与 -wal 互相撕裂的组合。
	plugindata.Reclaim(instanceName, mirror.InstanceMirrorDir(instanceName))

	_ = statepkg.WriteInstanceState(instanceName, statepkg.StatusStopped, "")
	return nil
}

// ForceStopServer 强制停止实例：杀死进程 + 重置状态 + 清理镜像
// v2: 不再需要 WaitForNoInitializing（每个实例的镜像独立）
func ForceStopServer(instanceName string) error {
	// 0. 停掉插件数据库快照；镜像里的插件数据由 CleanupInstanceMirror 内部的
	//    Rescue 抢救回实例目录，这里不必再单独回收
	plugindata.StopSnapshots(instanceName)
	// 1. 先停止 AsaApiLoader（asaServerApi）进程（best effort）
	if apiPid, pidErr := procpkg.GetAsaServerApiPID(instanceName); pidErr == nil && apiPid > 0 {
		killGameServer(apiPid)
	}
	// 2. 扫描进程命令行查找游戏进程（best effort，端口未监听时也能找到）
	cfg, err := cfgpkg.LoadInstanceConfig(instanceName)
	if err == nil {
		if pid, pidErr := findServerPIDBySaveDir(cfg.SaveDir); pidErr == nil && pid > 0 {
			killGameServer(pid)
		}
	}
	// 3. 尝试杀死已保存的游戏进程 PID（best effort）
	if pid2, pidErr := procpkg.GetInstancePID(instanceName); pidErr == nil && pid2 > 0 {
		killGameServer(pid2)
	}
	// 4. 尝试杀死已保存的启动器 PID（best effort，兜底）——Linux 上这是整棵
	//    umu-run/bwrap/wine/游戏进程树的 pgid leader，前三步因故都拿不到有效
	//    PID 时仍能保证杀干净；Windows 上与游戏 PID 同值，杀两遍无害。
	if launcherPid, pidErr := procpkg.GetLauncherPID(instanceName); pidErr == nil && launcherPid > 0 {
		killGameServer(launcherPid)
	}
	// 5. 重置状态为 stopped
	_ = statepkg.WriteInstanceState(instanceName, statepkg.StatusStopped, "")
	// 6. 清理镜像目录
	if err := mirror.CleanupInstanceMirror(instanceName); err != nil {
		logger.Warnf("Failed to cleanup instance mirror for %s: %v", instanceName, err)
	}
	return nil
}

func KillServer(instanceName string) error {

	cfg, err := cfgpkg.LoadInstanceConfig(instanceName)
	if err != nil {
		return err
	}
	pid, err := procx.PIDByPort(cfg.Port)
	if err != nil {
		return err
	}

	err = procx.Kill(pid)
	if err != nil {
		return err
	}
	_ = statepkg.WriteInstanceState(instanceName, statepkg.StatusStopped, "")
	return nil
}

// RestartServer restarts a server instance
func RestartServer(instanceName string, options ...StartServerOptionsFunc) error {
	opts := new(StartServerOptions)
	for _, o := range options {
		o(opts)
	}

	if !opts.StatePreset {
		// CAS: 原子检查状态并转换到 restarting
		ok, err := statepkg.CompareAndSwapInstanceState(instanceName,
			[]statepkg.InstanceStatus{statepkg.StatusStarted},
			statepkg.StatusRestarting)
		if err != nil {
			return fmt.Errorf("failed to check instance state: %w", err)
		}
		if !ok {
			return ErrOperationNotAllowed
		}
	}

	// 使用一个变量来记录错误，以便在函数结束时检查是否需要记录失败状态
	var restartErr error

	// 使用 defer 来在函数退出时记录失败状态（如果存在错误）
	defer func() {
		if restartErr != nil {
			// 保留失败记录（历史）
			_ = statepkg.WriteInstanceState(instanceName, statepkg.StatusRestartFailed, restartErr.Error())
			// 回滚到操作前状态：服务器仍在运行则为 started，已停止则为 stopped
			rollbackStatus := statepkg.StatusStopped
			if running, err := procpkg.IsServerRunning(instanceName); err == nil && running {
				rollbackStatus = statepkg.StatusStarted
			}
			_ = statepkg.WriteInstanceState(instanceName, rollbackStatus, "")
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

// GetRunningInstances returns a list of running instances
func GetRunningInstances() ([]string, error) {
	instances, err := cfgpkg.GetAvailableInstances()
	if err != nil {
		return nil, err
	}

	var running []string
	for _, instanceName := range instances {
		if isRunning, err := procpkg.IsServerRunning(instanceName); err == nil && isRunning {
			running = append(running, instanceName)
		}
	}

	return running, nil
}

// SaveInstancePID saves the PID of a running instance to its directory

// GetInstancePID retrieves the PID of a running instance from its directory

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
	baseConfigDir := filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer")
	instanceConfigDir := filepath.Join(cfgpkg.InstancesDir, instanceName, "Config")

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

	logger.Infof("Configuration files synced for instance '%s'", instanceName)
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

// formatRunnerProblems renders runner.Problem entries as an indented,
// fix-hinted block for an instance-start error message.
func formatRunnerProblems(problems []runner.Problem) string {
	var b strings.Builder
	for _, p := range problems {
		fmt.Fprintf(&b, "  - [%s] %s\n", p.Name, p.Detail)
		if p.Fix != "" {
			fmt.Fprintf(&b, "      修复：%s\n", p.Fix)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// pluginSnapshotInterval 把实例配置里的分钟数换算成快照周期。
// 0 表示用 plugindata 的默认值，负数表示关闭。
func pluginSnapshotInterval(cfg *cfgpkg.InstanceConfig) time.Duration {
	if cfg == nil || cfg.PluginSnapshotInterval == 0 {
		return 0
	}
	if cfg.PluginSnapshotInterval < 0 {
		return -1
	}
	return time.Duration(cfg.PluginSnapshotInterval) * time.Minute
}
