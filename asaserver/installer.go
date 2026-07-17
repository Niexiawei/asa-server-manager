package asaserver

import cfgpkg "asa-server/config"

import (
	"archive/zip"
	"asa-server/logger"
	"asa-server/pkg/console"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aymanbagabas/go-pty"
)

// DownloadAndExtractSteamCmd downloads and extracts SteamCMD to the steamcmd folder
// outputCallback is an optional callback for streaming console output (implements os.Writer interface)
func DownloadAndExtractSteamCmd(ctx context.Context, outputCallback ...io.Writer) error {
	// Get the output writer if provided
	var outputWriter io.Writer
	if len(outputCallback) > 0 && outputCallback[0] != nil {
		outputWriter = outputCallback[0]
	}

	// Check if SteamCMD is already installed and initialized
	steamCmdExe := filepath.Join(cfgpkg.SteamCmdDir, "steamcmd.exe")
	if _, err := os.Stat(steamCmdExe); err == nil {
		logMsg := "SteamCMD already installed."
		logger.GetLogger().Info(logMsg)
		if outputWriter != nil {
			outputWriter.Write([]byte(logMsg + "\n"))
		}
		if err := initializeSteamCmd(ctx, outputWriter); err != nil {
			return fmt.Errorf("failed to initialize SteamCMD: %w", err)
		}

		return nil
	}

	logMsg := "Downloading SteamCMD..."
	logger.GetLogger().Info(logMsg)
	if outputWriter != nil {
		outputWriter.Write([]byte(logMsg + "\n"))
	}

	// Download the SteamCMD zip file
	zipPath := filepath.Join(cfgpkg.SteamCmdDir, "steamcmd.zip")
	if err := downloadFile(cfgpkg.SteamCmdURL, zipPath); err != nil {
		return fmt.Errorf("failed to download SteamCMD: %w", err)
	}

	logMsg = "Extracting SteamCMD..."
	logger.GetLogger().Info(logMsg)
	if outputWriter != nil {
		outputWriter.Write([]byte(logMsg + "\n"))
	}

	// Extract the zip file
	if err := extractZip(zipPath, cfgpkg.SteamCmdDir); err != nil {
		return fmt.Errorf("failed to extract SteamCMD: %w", err)
	}

	// Remove the zip file after extraction
	if err := os.Remove(zipPath); err != nil {
		warnMsg := fmt.Sprintf("Warning: failed to remove zip file: %v", err)
		logger.GetLogger().Warnf(warnMsg)
		if outputWriter != nil {
			outputWriter.Write([]byte(warnMsg + "\n"))
		}
	}

	// Initialize SteamCMD by running it once
	logMsg = "Initializing SteamCMD..."
	logger.GetLogger().Info(logMsg)
	if outputWriter != nil {
		outputWriter.Write([]byte(logMsg + "\n"))
	}

	if err := initializeSteamCmd(ctx, outputWriter); err != nil {
		return fmt.Errorf("failed to initialize SteamCMD: %w", err)
	}

	logMsg = "SteamCMD installed successfully."
	logger.GetLogger().Info(logMsg)
	if outputWriter != nil {
		outputWriter.Write([]byte(logMsg + "\n"))
	}
	return nil
}

// downloadFile downloads a file from the given URL and saves it to the given path
func downloadFile(url string, filepath string) error {
	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Make HTTP GET request
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check HTTP response status
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	logger.GetLogger().Infof("Downloaded: %s (%d bytes)", filepath, resp.ContentLength)
	return nil
}

// extractZip extracts a zip file to the given directory
func extractZip(zipPath string, destDir string) error {
	// Open the zip file
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	// Extract each file in the zip
	for _, file := range reader.File {
		// Construct the full path to the extracted file
		fpath := filepath.Join(destDir, file.Name)

		// Zip Slip protection: ensure the path is within destDir
		if !strings.HasPrefix(filepath.Clean(fpath), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path in zip: %s", file.Name)
		}

		// Create directories if needed
		if file.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		// Create parent directories
		if err = os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		// Open the file in the zip
		infile, err := file.Open()
		if err != nil {
			return err
		}

		// Create the output file
		outfile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			infile.Close()
			return err
		}

		// Copy contents
		_, err = io.Copy(outfile, infile)
		infile.Close()
		outfile.Close()

		if err != nil {
			return err
		}
	}

	return nil
}

// initializeSteamCmd runs SteamCMD to initialize it
// outputWriter is an optional io.Writer for streaming console output
// This hides the cmd window and redirects output via the callback
func initializeSteamCmd(ctx context.Context, outputWriter ...io.Writer) error {
	steamCmdExe := filepath.Join(cfgpkg.SteamCmdDir, "steamcmd.exe")

	// Redirect stdout and stderr based on callback
	var writer io.Writer
	if len(outputWriter) > 0 && outputWriter[0] != nil {
		writer = outputWriter[0]
	}

	pp, err := pty.New()
	if err != nil {
		return fmt.Errorf("failed to open pty: %w", err)
	}
	defer pp.Close()

	// Create command with +quit argument to exit immediately after initialization
	cmd := pp.Command(steamCmdExe, "+quit")

	// Run SteamCMD
	logMsg := "Running SteamCMD initialization/updating..."
	logger.GetLogger().Info(logMsg)
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
	logger.GetLogger().Info(logMsg)
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

	steamCmdExe := filepath.Join(cfgpkg.SteamCmdDir, "steamcmd.exe")

	// Check if steamcmd.exe exists
	if _, err := os.Stat(steamCmdExe); err != nil {
		return fmt.Errorf("SteamCMD not found. Please run 'update' command first")
	}

	logMsg := "Installing/updating ARK server..."
	logger.GetLogger().Info(logMsg)
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

	CleanConsoleOutput := func(r io.Reader, w io.Writer) error {
		// 匹配 ANSI 转义序列以及上面提到的控制符
		// 包括 ESC [ ? ... h/l/m 等序列
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

	// Start SteamCMD with context cancellation support
	logMsg = "Running SteamCMD update..."
	logger.GetLogger().Info(logMsg)
	if outputWriter != nil {
		outputWriter.Write([]byte(logMsg + "\n"))
	}
	defer pp.Close()

	if outputWriter != nil {
		go CleanConsoleOutput(pp, outputWriter)
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

	logMsg = "ARK server installation/update completed successfully."
	logger.GetLogger().Info(logMsg)
	if outputWriter != nil {
		outputWriter.Write([]byte(logMsg + "\n"))
	}
	return nil
}

// VerifyServerInstallation checks if server configuration directory exists
// If not, it runs the server to generate initial configuration files
// force parameter: if true, will re-run server verification even if config exists
func VerifyServerInstallation(ctx context.Context, force bool) error {
	configDir := filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer")

	// Check if configuration directory already exists
	if _, err := os.Stat(configDir); err == nil && !force {
		logger.GetLogger().Info("Server configuration directory already exists. Skipping initial verification.")
		return nil
	}

	if force {
		if _, err := os.Stat(configDir); err == nil {
			logger.GetLogger().Info("Force verification enabled. Re-running server verification...")
		}
	}

	arkExe := filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Binaries/Win64/ArkAscendedServer.exe")

	// Check if ArkAscendedServer.exe exists
	if _, err := os.Stat(arkExe); err != nil {
		return fmt.Errorf("ArkAscendedServer.exe not found. Please run 'update' command first")
	}

	if !force {
		logger.GetLogger().Info("First installation detected. Running server to generate configuration files...")
	}

	logger.GetLogger().Info("This may take 60 seconds...")

	// Get the logs directory path
	logsDir := filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Saved/Logs")

	// Start the server to generate config files
	cmd := exec.Command(
		arkExe,
		"TheIsland_WP?listen",
		"-NoBattlEye",
		"-crossplay",
		"-server",
		"-log",
		"-nosteamclient",
		"-game",
	)

	// Do NOT redirect stdout/stderr to avoid process issues
	// Instead, we'll monitor the log file for output

	// Start the process in the background
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start server for verification: %w", err)
	}
	pid := cmd.Process.Pid
	logger.GetLogger().Infof("Server process started (PID: %d). Monitoring log file...", pid)

	logFilePath, err := FindLatestLogFile(logsDir)
	if err != nil {
		logger.GetLogger().Warnf("Warning: could not find log file initially - %v", err)
		// Continue anyway, will wait for manual log generation
	} else {
		logger.GetLogger().Infof("Monitoring log file: %s", filepath.Base(logFilePath))
	}

	// Wait for server to generate config files (with context cancellation)
	select {
	case <-time.After(60 * time.Second):
	case <-ctx.Done():
		logger.GetLogger().Info("Stopping server for verification (cancelled)...")
		exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid), "/F").Run()
		return ctx.Err()
	}
	// Kill the server process

	logger.GetLogger().Info("Stopping server for verification...")
	exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid), "/F").Run()

	// Wait a moment for process to clean up
	time.Sleep(2 * time.Second)

	logger.GetLogger().Info("Server verification completed. Configuration files generated.")
	return nil
}
