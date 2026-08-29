package installer

import (
	cfgpkg "asa-server/internal/config"
	procpkg "asa-server/internal/process"
	"asa-server/internal/runner"
	"asa-server/pkg/console"
	"asa-server/pkg/download"
	"asa-server/pkg/logger"
	"asa-server/pkg/netutil"
	"asa-server/pkg/procx"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aymanbagabas/go-pty"
)

// server-files 的改写与实例运行互斥，本包是这条约束的唯一权威：
// 既拦「有实例在跑就不许更新」，也对外发布「正在更新」供启动侧查询。
//
// 实例镜像目录里除 ShooterGame/Binaries/Win64 外全是指回 server-files 的
// junction / 文件符号链接，更新会就地替换运行中进程正在映射的文件，直接崩服。
var (
	updateMu       sync.Mutex
	updateInFlight bool
)

// beginServerFilesUpdate 在同一把锁下确认没有实例在跑，并标记「更新中」。
// 检查与置位必须原子，否则两个并发的更新请求可能同时通过检查。
// 成功返回后调用方必须 defer endServerFilesUpdate()。
func beginServerFilesUpdate() error {
	updateMu.Lock()
	defer updateMu.Unlock()

	if alive := procpkg.ListAliveInstances(); len(alive) > 0 {
		return fmt.Errorf(
			"cannot update server files: instance(s) still running: %s; stop them first",
			strings.Join(alive, ", "),
		)
	}

	updateInFlight = true
	return nil
}

// endServerFilesUpdate 清除「更新中」标记。
func endServerFilesUpdate() {
	updateMu.Lock()
	defer updateMu.Unlock()
	updateInFlight = false
}

// IsUpdatingServerFiles 报告 server-files 是否正在被改写。
// 实例启动前据此拒绝：更新期间源目录正在增删，此时做镜像同步只会同步出残缺镜像。
func IsUpdatingServerFiles() bool {
	updateMu.Lock()
	defer updateMu.Unlock()
	return updateInFlight
}

// findLatestLogFile finds the latest log file (ShooterGame.log or ShooterGame_N.log).
// When multiple servers run, logs are named ShooterGame.log, ShooterGame_2.log, etc.
func findLatestLogFile(logsDir string) (string, error) {
	files, err := os.ReadDir(logsDir)
	if err != nil {
		return "", err
	}

	var logFiles []string
	for _, file := range files {
		if !file.IsDir() && strings.HasPrefix(file.Name(), "ShooterGame") && strings.HasSuffix(file.Name(), ".log") {
			logFiles = append(logFiles, file.Name())
		}
	}

	if len(logFiles) == 0 {
		return "", fmt.Errorf("no ShooterGame log files found")
	}

	var latestLog string
	for _, log := range logFiles {
		if latestLog == "" || log > latestLog {
			latestLog = log
		}
	}

	return filepath.Join(logsDir, latestLog), nil
}

// DownloadAndExtractSteamCmd downloads and extracts SteamCMD to the steamcmd folder
// outputCallback is an optional callback for streaming console output (implements os.Writer interface)
func DownloadAndExtractSteamCmd(ctx context.Context, outputCallback ...io.Writer) error {
	// Get the output writer if provided
	var outputWriter io.Writer
	if len(outputCallback) > 0 && outputCallback[0] != nil {
		outputWriter = outputCallback[0]
	}

	// Check if SteamCMD is already installed and initialized
	steamCmdExe := filepath.Join(cfgpkg.SteamCmdDir, steamCmdBinaryName)
	if _, err := os.Stat(steamCmdExe); err == nil {
		logMsg := "SteamCMD already installed."
		logger.Info(logMsg)
		if outputWriter != nil {
			outputWriter.Write([]byte(logMsg + "\n"))
		}
		if err := initializeSteamCmd(ctx, outputWriter); err != nil {
			return fmt.Errorf("failed to initialize SteamCMD: %w", err)
		}

		return nil
	}

	logMsg := "Downloading SteamCMD..."
	logger.Info(logMsg)
	if outputWriter != nil {
		outputWriter.Write([]byte(logMsg + "\n"))
	}

	// Download the SteamCMD archive
	archivePath := filepath.Join(cfgpkg.SteamCmdDir, "steamcmd_download."+steamCmdArchiveExt)
	if err := download.Fetch(ctx, download.Options{URL: steamCmdURL, Dest: archivePath, Resume: true}); err != nil {
		return fmt.Errorf("failed to download SteamCMD: %w", err)
	}
	if fi, err := os.Stat(archivePath); err == nil {
		logger.Infof("Downloaded: %s (%d bytes)", archivePath, fi.Size())
	}

	logMsg = "Extracting SteamCMD..."
	logger.Info(logMsg)
	if outputWriter != nil {
		outputWriter.Write([]byte(logMsg + "\n"))
	}

	// Extract the archive (steamcmd.zip on Windows, steamcmd_linux.tar.gz on Linux)
	if err := extractSteamCmdArchive(archivePath, cfgpkg.SteamCmdDir); err != nil {
		return fmt.Errorf("failed to extract SteamCMD: %w", err)
	}

	// Remove the archive after extraction
	if err := os.Remove(archivePath); err != nil {
		warnMsg := fmt.Sprintf("Warning: failed to remove downloaded archive: %v", err)
		logger.Warnf(warnMsg)
		if outputWriter != nil {
			outputWriter.Write([]byte(warnMsg + "\n"))
		}
	}

	// Initialize SteamCMD by running it once
	logMsg = "Initializing SteamCMD..."
	logger.Info(logMsg)
	if outputWriter != nil {
		outputWriter.Write([]byte(logMsg + "\n"))
	}

	if err := initializeSteamCmd(ctx, outputWriter); err != nil {
		return fmt.Errorf("failed to initialize SteamCMD: %w", err)
	}

	logMsg = "SteamCMD installed successfully."
	logger.Info(logMsg)
	if outputWriter != nil {
		outputWriter.Write([]byte(logMsg + "\n"))
	}
	return nil
}

// initializeSteamCmd runs SteamCMD to initialize it
// outputWriter is an optional io.Writer for streaming console output
// This hides the cmd window and redirects output via the callback
func initializeSteamCmd(ctx context.Context, outputWriter ...io.Writer) error {
	steamCmdExe := filepath.Join(cfgpkg.SteamCmdDir, steamCmdBinaryName)

	// Redirect stdout and stderr based on callback
	var writer io.Writer
	if len(outputWriter) > 0 && outputWriter[0] != nil {
		writer = outputWriter[0]
	}

	pp, err := pty.New()
	if err != nil {
		return fmt.Errorf("failed to open pty: %w", err)
	}
	pp.Resize(1920, 1080)
	defer pp.Close()

	// Create command with +quit argument to exit immediately after initialization
	cmd := pp.Command(steamCmdExe, "+quit")
	// Run SteamCMD
	logMsg := "Running SteamCMD initialization/updating..."
	logger.Info(logMsg)
	if writer != nil {
		writer.Write([]byte(logMsg + "\n"))
	}

	if writer != nil {
		go console.CleanConsoleOutput(pp, writer)
	}

	// Start and wait with context cancellation support
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start SteamCMD: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("SteamCMD initialization/updating failed: %w", err)
		}
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return ctx.Err()
	}

	logMsg = "SteamCMD initialized/updating successfully."
	logger.Info(logMsg)
	if writer != nil {
		writer.Write([]byte(logMsg + "\n"))
	}
	return nil
}

// DownloadAndUpdateArkServer downloads and updates the ARK server files using SteamCMD
// outputCallback is an optional callback for streaming console output (implements os.Writer interface)
func DownloadAndUpdateArkServer(ctx context.Context, outputCallback ...io.Writer) error {
	// Get the output writer if provided
	var outputWriter io.Writer
	if len(outputCallback) > 0 && outputCallback[0] != nil {
		outputWriter = outputCallback[0]
	}

	// 在动任何文件之前拦下：SteamCMD 会就地替换运行中实例正在映射的文件
	if err := beginServerFilesUpdate(); err != nil {
		return err
	}
	defer endServerFilesUpdate()

	// No-op on Windows. On Linux this guarantees umu/GE-Proton are in place
	// before VerifyServerInstallation tries to launch the server through
	// runner.Run further down — normally already done by the background
	// warm-up at API server startup, but not guaranteed (fresh install,
	// startup warm-up still in flight, auto_download disabled, ...).
	if err := runner.EnsureRuntime(ctx, outputWriter); err != nil {
		return fmt.Errorf("Linux runtime (umu/GE-Proton) not ready: %w", err)
	}

	steamCmdExe := filepath.Join(cfgpkg.SteamCmdDir, steamCmdBinaryName)

	// Check if SteamCMD's binary exists
	if _, err := os.Stat(steamCmdExe); err != nil {
		return fmt.Errorf("SteamCMD not found. Please run 'update' command first")
	}

	logMsg := "Installing/updating ARK server..."
	logger.Info(logMsg)
	if outputWriter != nil {
		outputWriter.Write([]byte(logMsg + "\n"))
	}

	// Create server-files directory if it doesn't exist
	if err := os.MkdirAll(cfgpkg.ServerFilesDir, 0755); err != nil {
		return fmt.Errorf("failed to create server-files directory: %w", err)
	}
	pp, err := pty.New()
	if err != nil {
		return fmt.Errorf("failed to open pty: %w", err)
	}
	pp.Resize(1920, 1080)

	if outputWriter != nil {
		outputWriter.Write([]byte(fmt.Sprintf("install to dir: %s", cfgpkg.ServerFilesDir)))
	}

	// Run SteamCMD with arguments to install/update ARK server
	// App ID 2430930 is ARK: Survival Ascended
	cmd := pp.Command(
		steamCmdExe,
		"+force_install_dir", cfgpkg.ServerFilesDir,
		"+login", "anonymous",
		"+app_update", "2430930", "validate",
		"+quit",
	)

	// Start SteamCMD with context cancellation support
	logMsg = "Running SteamCMD update..."
	logger.Info(logMsg)
	if outputWriter != nil {
		outputWriter.Write([]byte(logMsg + "\n"))
	}
	defer pp.Close()

	if outputWriter != nil {
		go console.CleanConsoleOutput(pp, outputWriter)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start SteamCMD: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("SteamCMD update failed: %w", err)
		}
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return ctx.Err()
	}

	// Re-apply the ASA-on-Wine fixups every time: `validate` just re-downloaded
	// the Sentry plugin this fixed a moment ago (see disableSentryPlugin's own
	// comment for why that specifically needs to happen after every update,
	// not just once at install time). No-op on Windows.
	if err := ApplyLinuxFixups(); err != nil {
		logger.Warnf("Failed to apply Linux compatibility fixups: %v", err)
	}

	// SteamCMD just ran as this process (root), so everything it wrote or
	// replaced is root-owned. Hand the tree back to the shared-access regime
	// so the dropped game process can still write saves, ModsUserData and
	// crash dumps into it. Unconditional — this is the authoritative pass the
	// sampled one in reconcileRuntimeOwnership defers to. No-op on Windows.
	if err := runner.PrepareSharedTree(cfgpkg.ServerFilesDir); err != nil {
		logger.Warnf("Failed to prepare %s for the runtime user: %v", cfgpkg.ServerFilesDir, err)
	}

	logMsg = "ARK server installation/update completed successfully."
	logger.Info(logMsg)
	if outputWriter != nil {
		outputWriter.Write([]byte(logMsg + "\n"))
	}
	return nil
}

// VerifyServerInstallation checks if server configuration directory exists
// If not, it runs the server to generate initial configuration files
// force parameter: if true, will re-run server verification even if config exists
// outputCallback is an optional writer for streaming progress (same shape as
// DownloadAndExtractSteamCmd / DownloadAndUpdateArkServer).
func VerifyServerInstallation(ctx context.Context, force bool, outputCallback ...io.Writer) error {
	var outputWriter io.Writer
	if len(outputCallback) > 0 && outputCallback[0] != nil {
		outputWriter = outputCallback[0]
	}
	emit := func(msg string) {
		logger.Info(msg)
		if outputWriter != nil {
			_, _ = outputWriter.Write([]byte(msg + "\n"))
		}
	}

	// 本函数会从 server-files 拉起一个服务端进程，且不指定 -Port（占默认 7777），
	// 与运行中的实例既撞端口又共用文件，必须同样拦下
	if err := beginServerFilesUpdate(); err != nil {
		return err
	}
	defer endServerFilesUpdate()

	configDir := filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer")

	// Check if configuration directory already exists
	if _, err := os.Stat(configDir); err == nil && !force {
		emit("Server configuration directory already exists. Skipping initial verification.")
		return nil
	}

	if force {
		if _, err := os.Stat(configDir); err == nil {
			emit("Force verification enabled. Re-running server verification...")
		}
	}

	arkExe := filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Binaries/Win64/ArkAscendedServer.exe")

	// Check if ArkAscendedServer.exe exists
	if _, err := os.Stat(arkExe); err != nil {
		return fmt.Errorf("ArkAscendedServer.exe not found. Please run 'update' command first")
	}

	// Apply Linux compatibility fixups (Sentry plugin, steam_appid.txt, Steam
	// SDK symlinks) before the very first launch too — a no-op on Windows.
	// Best-effort: a failure here shouldn't block verification outright, the
	// launch below will just fail with a clearer symptom if it mattered.
	if err := ApplyLinuxFixups(); err != nil {
		logger.Warnf("Failed to apply Linux compatibility fixups: %v", err)
	}

	if !force {
		emit("First installation detected. Running server to generate configuration files...")
	}

	// Get the logs directory path
	logsDir := filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Saved/Logs")

	// 挑一个空闲端口，避免和占用 ARK 默认 7777 的其他程序撞车。
	// 拿不到就退回 7777（原有行为），不因此让整个验证流程失败。
	port, err := netutil.FreeUDPPort()
	if err != nil {
		logger.Warnf("Failed to get a free UDP port, falling back to 7777: %v", err)
		port = 7777
	}
	emit(fmt.Sprintf("Running server verification on port %d — waiting for it to finish booting and start "+
		"serving (this can take several minutes on a first run)...", port))

	// The verification launch runs straight out of server-files, with no
	// per-instance mirror in front of it — so on Linux the dropped runtime
	// user needs write access to this tree directly. Without it the game
	// cannot create ShooterGame/Saved at all and the wait below is guaranteed
	// to time out after 180s with nothing to show for it. No-op on Windows.
	// See docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md §3.6.
	if err := os.MkdirAll(filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Saved"), 0o755); err != nil {
		return fmt.Errorf("failed to create Saved directory: %w", err)
	}
	if err := runner.PrepareSharedTree(cfgpkg.ServerFilesDir); err != nil {
		logger.Warnf("Failed to prepare %s for the runtime user: %v", cfgpkg.ServerFilesDir, err)
	}

	// Capture the launch's stdout/stderr. On Linux everything umu-run,
	// pressure-vessel and Wine have to say about a failed start goes here and
	// nowhere else — without it a container-side failure is completely silent
	// (docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md §3.5 c).
	var launchLog io.Writer
	_ = os.MkdirAll(filepath.Dir(verifyLaunchLogPath()), 0o755)
	if f, logErr := os.OpenFile(verifyLaunchLogPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); logErr != nil {
		logger.Warnf("Failed to open verification launch log: %v", logErr)
	} else {
		defer f.Close()
		launchLog = f
		emit("Launch output: " + verifyLaunchLogPath())
	}

	// Start the server to generate config files. On Windows this is a plain
	// exec of arkExe; on Linux runner.Run wraps it in umu-run (Wine/Proton) —
	// see docs/LINUX_COMPATIBILITY_PLAN.md §5.1/§5.5. handle.LauncherPID is
	// the actual game PID on Windows and umu-run's PID on Linux; procx.KillTree
	// below walks the parent/child tree from it, which reaches the container
	// contents on both.
	handle, err := runner.Run(ctx, arkExe, []string{
		"TheIsland_WP?listen",
		fmt.Sprintf("-Port=%d", port),
		"-NoBattlEye",
		"-crossplay",
		"-server",
		"-log",
		"-nosteamclient",
		"-game",
	}, runner.Options{Log: launchLog})
	if err != nil {
		return fmt.Errorf("failed to start server for verification: %w", err)
	}
	logger.Infof("Server process started (launcher PID: %d). Monitoring log file...", handle.LauncherPID)

	logFilePath, err := findLatestLogFile(logsDir)
	if err != nil {
		logger.Warnf("Warning: could not find log file initially - %v", err)
		// Continue anyway, will wait for manual log generation
	} else {
		logger.Infof("Monitoring log file: %s", filepath.Base(logFilePath))
	}

	// Wait until the server is genuinely up — see waitForVerificationServer
	// on why "the config directory exists" is not that.
	waitErr := waitForVerificationServer(ctx, configDir, port, handle.LauncherPID, verifyStartupTimeout, emit)

	if ctx.Err() != nil {
		logger.Info("Stopping server for verification (cancelled)...")
		_ = procx.KillTree(handle.LauncherPID)
		return ctx.Err()
	}

	logger.Info("Stopping server for verification...")
	_ = procx.KillTree(handle.LauncherPID)

	// Wait a moment for process to clean up
	time.Sleep(2 * time.Second)

	if waitErr != nil {
		reportVerificationFailure(logsDir, emit)
		return fmt.Errorf("server verification failed: %w", waitErr)
	}

	emit("Server verification completed: the server booted and served on its port.")
	return nil
}

// verifyStartupTimeout is the whole budget for "launched" to become
// "serving". The previous 180s covered only the first milestone (the config
// directory, which lands within a minute or so on a cold Wine start —
// docs/LINUX_COMPATIBILITY_PLAN.md §5.5); actually loading the map and
// opening the port takes another 30-90s on top, and more on a first run that
// is still warming caches. Five minutes leaves headroom for the slow path
// without letting a genuinely hung server hold the process forever.
const verifyStartupTimeout = 5 * time.Minute

// verifyLaunchLogPath is where the verification launch's stdout/stderr goes.
// Truncated on every run — it describes one launch, and the interesting case
// is always the most recent one.
func verifyLaunchLogPath() string {
	return filepath.Join(cfgpkg.BaseDir, "logs", "verify-launch.log")
}

// Probes waitForVerificationServer polls through. Package-level so tests can
// drive the wait loop without a real process or a real socket.
var (
	portInUse = procx.PortInUse

	processExited = func(pid int) bool {
		exited, err := procx.IsProcessExited(uint32(pid))
		return err == nil && exited
	}

	// verifyPollInterval paces the wait loop. Every probe costs a full
	// gopsutil connection walk, so 2s is deliberate — the wait runs for
	// minutes. Tests shrink it.
	verifyPollInterval = 2 * time.Second
)

// waitForVerificationServer blocks until the freshly launched server is
// actually serving — its game port is bound — or until it gives up.
//
// The configuration directory appearing is NOT that signal, and treating it as
// one is what this replaced. It shows up within seconds of engine init, long
// before the world is loaded and long before anything is listening; and on a
// re-run (force=true) it is already there from last time, so the wait returned
// about two seconds after launch and declared verification successful while
// the server was still booting — or, in the case this was written for, while
// it was hung on an unwritable directory and was never going to listen at all.
// See docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md §3.6.
//
// The config directory is still tracked, but only as a progress milestone and
// to make the failure message say which of the two stages was not reached.
//
// launcherPID is watched so a launch that dies outright fails immediately with
// what actually happened, instead of being reported as a timeout minutes
// later — the same early bail-out scripts/ark_instance_manager.sh does with
// `kill -0 "$init_pid"`.
func waitForVerificationServer(
	ctx context.Context,
	configDir string,
	port, launcherPID int,
	timeout time.Duration,
	emit func(string),
) error {
	started := time.Now()
	deadline := started.Add(timeout)

	_, statErr := os.Stat(configDir)
	configSeen := statErr == nil

	ticker := time.NewTicker(verifyPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		if !configSeen {
			if _, err := os.Stat(configDir); err == nil {
				configSeen = true
				emit(fmt.Sprintf("Configuration files generated after %s. Waiting for the server to open port %d...",
					elapsed(started), port))
			}
		}

		if inUse, err := portInUse(port); err != nil {
			logger.Warnf("Failed to check whether port %d is bound: %v", port, err)
		} else if inUse {
			emit(fmt.Sprintf("Server is listening on port %d after %s.", port, elapsed(started)))
			return nil
		}

		// Dead process: waiting out the remaining timeout would only turn a
		// precise failure into a vague one.
		if processExited(launcherPID) {
			return fmt.Errorf("server process exited after %s without ever listening on port %d",
				elapsed(started), port)
		}

		if time.Now().After(deadline) {
			if !configSeen {
				return fmt.Errorf("timed out after %s: %s was never created — the server did not get "+
					"far enough to write its own configuration", timeout, configDir)
			}
			return fmt.Errorf("timed out after %s: configuration was generated but the server never "+
				"started listening on port %d", timeout, port)
		}
	}
}

func elapsed(since time.Time) time.Duration {
	return time.Since(since).Round(time.Second)
}

// reportVerificationFailure emits the tail of both logs a failed verification
// leaves behind, mirroring what scripts/ark_instance_manager.sh prints when
// the initial server start doesn't come up. Without this the caller is told
// "it didn't start" and has to go find the files by hand — which, when the
// launch output was being discarded entirely, was not even possible.
func reportVerificationFailure(logsDir string, emit func(string)) {
	if tail := tailLines(verifyLaunchLogPath(), 20); tail != "" {
		emit(fmt.Sprintf("--- last lines of %s ---\n%s", verifyLaunchLogPath(), tail))
	}
	gameLog, err := findLatestLogFile(logsDir)
	if err != nil {
		emit(fmt.Sprintf("(no ShooterGame log was produced in %s)", logsDir))
		return
	}
	if tail := tailLines(gameLog, 30); tail != "" {
		emit(fmt.Sprintf("--- last lines of %s ---\n%s", gameLog, tail))
	}
}

// tailLines returns the last n lines of a file, or "" if it can't be read.
// The logs involved are small enough (a boot's worth of output) to read whole.
func tailLines(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
