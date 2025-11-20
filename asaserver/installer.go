package asaserver

import (
	"archive/zip"
	"asa-server/logger"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// DownloadAndExtractSteamCmd downloads and extracts SteamCMD to the steamcmd folder
// outputCallback is an optional callback for streaming console output (implements os.Writer interface)
func DownloadAndExtractSteamCmd(outputCallback ...io.Writer) error {
	// Get the output writer if provided
	var outputWriter io.Writer
	if len(outputCallback) > 0 && outputCallback[0] != nil {
		outputWriter = outputCallback[0]
	}

	// Check if SteamCMD is already installed and initialized
	steamCmdExe := filepath.Join(SteamCmdDir, "steamcmd.exe")
	if _, err := os.Stat(steamCmdExe); err == nil {
		logMsg := "SteamCMD already installed."
		logger.GetLogger().Info(logMsg)
		if outputWriter != nil {
			outputWriter.Write([]byte(logMsg + "\n"))
		}
		return nil
	}

	logMsg := "Downloading SteamCMD..."
	logger.GetLogger().Info(logMsg)
	if outputWriter != nil {
		outputWriter.Write([]byte(logMsg + "\n"))
	}

	// Download the SteamCMD zip file
	zipPath := filepath.Join(SteamCmdDir, "steamcmd.zip")
	if err := downloadFile(SteamCmdURL, zipPath); err != nil {
		return fmt.Errorf("failed to download SteamCMD: %w", err)
	}

	logMsg = "Extracting SteamCMD..."
	logger.GetLogger().Info(logMsg)
	if outputWriter != nil {
		outputWriter.Write([]byte(logMsg + "\n"))
	}

	// Extract the zip file
	if err := extractZip(zipPath, SteamCmdDir); err != nil {
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
	if err := initializeSteamCmd(outputWriter); err != nil {
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
func initializeSteamCmd(outputWriter ...io.Writer) error {
	steamCmdExe := filepath.Join(SteamCmdDir, "steamcmd.exe")

	// Create command with +quit argument to exit immediately after initialization
	cmd := exec.Command(steamCmdExe, "+quit")

	// Redirect stdout and stderr based on callback
	var writer io.Writer
	if len(outputWriter) > 0 && outputWriter[0] != nil {
		writer = outputWriter[0]
	}
	cmd.Stdout = writer
	cmd.Stderr = writer

	// Windows specific: hide the cmd window
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	// Run SteamCMD
	logMsg := "Running SteamCMD initialization..."
	logger.GetLogger().Info(logMsg)
	if writer != nil {
		writer.Write([]byte(logMsg + "\n"))
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SteamCMD initialization failed: %w", err)
	}

	logMsg = "SteamCMD initialized successfully."
	logger.GetLogger().Info(logMsg)
	if writer != nil {
		writer.Write([]byte(logMsg + "\n"))
	}
	return nil
}

// DownloadAndUpdateArkServer downloads and updates the ARK server files using SteamCMD
// outputCallback is an optional callback for streaming console output (implements os.Writer interface)
func DownloadAndUpdateArkServer(outputCallback ...io.Writer) error {
	// Get the output writer if provided
	var outputWriter io.Writer
	if len(outputCallback) > 0 && outputCallback[0] != nil {
		outputWriter = outputCallback[0]
	}

	steamCmdExe := filepath.Join(SteamCmdDir, "steamcmd.exe")

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
	if err := os.MkdirAll(ServerFilesDir, 0755); err != nil {
		return fmt.Errorf("failed to create server-files directory: %w", err)
	}

	// Run SteamCMD with arguments to install/update ARK server
	// App ID 2430930 is ARK: Survival Ascended
	cmd := exec.Command(
		steamCmdExe,
		"+force_install_dir", ServerFilesDir,
		"+login", "anonymous",
		"+app_update", "2430930", "validate",
		"+quit",
	)

	// Redirect stdout and stderr via callback
	cmd.Stdout = outputWriter
	cmd.Stderr = outputWriter

	// Windows specific: hide the cmd window
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	// Run SteamCMD
	logMsg = "Running SteamCMD update..."
	logger.GetLogger().Info(logMsg)
	if outputWriter != nil {
		outputWriter.Write([]byte(logMsg + "\n"))
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SteamCMD update failed: %w", err)
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
func VerifyServerInstallation(force bool) error {
	configDir := filepath.Join(ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer")

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

	arkExe := filepath.Join(ServerFilesDir, "ShooterGame/Binaries/Win64/ArkAscendedServer.exe")

	// Check if ArkAscendedServer.exe exists
	if _, err := os.Stat(arkExe); err != nil {
		return fmt.Errorf("ArkAscendedServer.exe not found. Please run 'update' command first")
	}

	if !force {
		logger.GetLogger().Info("First installation detected. Running server to generate configuration files...")
	}

	logger.GetLogger().Info("This may take 60 seconds...")

	// Get the logs directory path
	logsDir := filepath.Join(ServerFilesDir, "ShooterGame/Saved/Logs")

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
	var stopMonitoring func()
	if err != nil {
		logger.GetLogger().Warnf("Warning: could not find log file initially - %v", err)
		// Continue anyway, will wait for manual log generation
	} else {
		logger.GetLogger().Infof("Monitoring log file: %s", filepath.Base(logFilePath))
		// Start tailing the log file asynchronously
		stopMonitoring = TailLogFile(logFilePath, func(line string) {
			fmt.Println(line)
		})
	}

	// Wait for server to generate config files
	time.Sleep(60 * time.Second)

	// Stop monitoring the log file
	if stopMonitoring != nil {
		stopMonitoring()
	}

	// Kill the server process
	logger.GetLogger().Info("Stopping server for verification...")
	exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid), "/F").Run()

	// Wait a moment for process to clean up
	time.Sleep(2 * time.Second)

	logger.GetLogger().Info("Server verification completed. Configuration files generated.")
	return nil
}
