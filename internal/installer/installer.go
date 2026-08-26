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
func VerifyServerInstallation(ctx context.Context, force bool) error {
	// 本函数会从 server-files 拉起一个服务端进程，且不指定 -Port（占默认 7777），
	// 与运行中的实例既撞端口又共用文件，必须同样拦下
	if err := beginServerFilesUpdate(); err != nil {
		return err
	}
	defer endServerFilesUpdate()

	configDir := filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer")

	// Check if configuration directory already exists
	if _, err := os.Stat(configDir); err == nil && !force {
		logger.Info("Server configuration directory already exists. Skipping initial verification.")
		return nil
	}

	if force {
		if _, err := os.Stat(configDir); err == nil {
			logger.Info("Force verification enabled. Re-running server verification...")
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
		logger.Info("First installation detected. Running server to generate configuration files...")
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
	logger.Infof("Running server verification on port %d...", port)

	// Start the server to generate config files. On Windows this is a plain
	// exec of arkExe; on Linux runner.Run wraps it in umu-run (Wine/Proton) —
	// see docs/LINUX_COMPATIBILITY_PLAN.md §5.1/§5.5. Either way,
	// handle.LauncherPID is what procx.KillTree needs below: the actual game
	// PID on Windows, umu-run's PID (== the whole launch's process group
	// leader) on Linux.
	handle, err := runner.Run(ctx, arkExe, []string{
		"TheIsland_WP?listen",
		fmt.Sprintf("-Port=%d", port),
		"-NoBattlEye",
		"-crossplay",
		"-server",
		"-log",
		"-nosteamclient",
		"-game",
	}, runner.Options{})
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

	// Wait for the config directory to appear rather than a fixed sleep:
	// Wine cold starts are considerably slower than native Windows, and a
	// fixed 60s used to be both too short under Wine and needlessly long on
	// Windows once the directory has actually appeared. Capped at 180s per
	// docs/LINUX_COMPATIBILITY_PLAN.md §5.5.
	waitErr := waitForConfigDir(ctx, configDir, 180*time.Second)

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
		return fmt.Errorf("server verification failed: %w", waitErr)
	}

	logger.Info("Server verification completed. Configuration files generated.")
	return nil
}

// waitForConfigDir polls for configDir to appear, up to timeout or ctx
// cancellation, whichever comes first.
func waitForConfigDir(ctx context.Context, configDir string, timeout time.Duration) error {
	if _, err := os.Stat(configDir); err == nil {
		return nil
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out after %s waiting for %s to appear", timeout, configDir)
		case <-ticker.C:
			if _, err := os.Stat(configDir); err == nil {
				return nil
			}
		}
	}
}
