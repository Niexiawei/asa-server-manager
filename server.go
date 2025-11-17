package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorcon/rcon"
)

// logMappingMutex protects the instance to log file mapping
var logMappingMutex sync.RWMutex

// instanceLogMapping stores the mapping of instance names to their log file paths
var instanceLogMapping = make(map[string]string)

// InitializeLogMapping loads log mappings from persistent storage
func InitializeLogMapping() error {
	mappings, err := LoadLogMappingFromFile()
	if err != nil {
		return fmt.Errorf("failed to load log mapping from file: %w", err)
	}

	logMappingMutex.Lock()
	instanceLogMapping = mappings
	logMappingMutex.Unlock()

	if len(mappings) > 0 {
		fmt.Printf("📂 Loaded %d instance log mappings from persistent storage\n", len(mappings))
	}

	return nil
}

// PersistLogMapping saves the current log mappings to storage
func PersistLogMapping() error {
	logMappingMutex.RLock()
	mappingsCopy := make(map[string]string)
	for k, v := range instanceLogMapping {
		mappingsCopy[k] = v
	}
	logMappingMutex.RUnlock()

	return SaveLogMappingToFile(mappingsCopy)
}

// RemoveInstanceLogMapping removes the log mapping for an instance
func RemoveInstanceLogMapping(instanceName string) error {
	logMappingMutex.Lock()
	delete(instanceLogMapping, instanceName)
	logMappingMutex.Unlock()

	// Persist the updated mappings to file
	return PersistLogMapping()
}

// BackupAndRemoveInstanceLogFile backs up the log file for an instance and removes the original
func BackupAndRemoveInstanceLogFile(instanceName string) error {
	// Get the log file path from mapping
	logFilePath, exists := GetInstanceLogFile(instanceName)
	if !exists {
		fmt.Printf("⚠️  Log file for instance %s does not exist: %s\n", instanceName, logFilePath)
		return nil
	}

	// Check if log file exists
	if _, err := os.Stat(logFilePath); os.IsNotExist(err) {
		fmt.Printf("⚠️  Log file for instance %s does not exist: %s\n", instanceName, logFilePath)
		return nil
	}

	// Create logs backup directory
	logsBackupDir := filepath.Join(BaseDir, "logs-backup")
	if err := os.MkdirAll(logsBackupDir, 0755); err != nil {
		return fmt.Errorf("failed to create logs backup directory: %w", err)
	}

	// Generate backup file name with instance name and timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	logFileName := filepath.Base(logFilePath)
	backupFileName := fmt.Sprintf("%s_%s_%s", instanceName, timestamp, logFileName)
	backupFilePath := filepath.Join(logsBackupDir, backupFileName)

	// Copy log file to backup location
	srcFile, err := os.Open(logFilePath)
	if err != nil {
		return fmt.Errorf("failed to open log file for backup: %w", err)
	}
	defer srcFile.Close()

	backupFile, err := os.Create(backupFilePath)
	if err != nil {
		return fmt.Errorf("failed to create backup file: %w", err)
	}
	defer backupFile.Close()

	if _, err := io.Copy(backupFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy log file to backup: %w", err)
	}

	fmt.Printf("📋 Backed up log file to: %s\n", backupFilePath)

	// Remove the original log file
	if err := os.Remove(logFilePath); err != nil {
		return fmt.Errorf("failed to remove original log file: %w", err)
	}

	fmt.Printf("🗑 Removed original log file: %s\n", logFilePath)

	return nil
}

// GetGameLogFileName returns the log file name for a given instance based on running order
// The naming convention is: ShooterGame.log for the first instance, ShooterGame_2.log, ShooterGame_3.log, etc.
// It finds the first available (non-existent) log file number
func GetGameLogFileName(instanceName string) (string, error) {
	logsDir := filepath.Join(ServerFilesDir, "ShooterGame/Saved/Logs")

	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Find the first available log file number (starting from 1)
	// First, check if ShooterGame.log (number 1) is available
	logFilePath := filepath.Join(logsDir, "ShooterGame.log")
	if _, err := os.Stat(logFilePath); os.IsNotExist(err) {
		return "ShooterGame.log", nil
	}

	// If ShooterGame.log exists, find the first available numbered file
	// Check ShooterGame_2.log, ShooterGame_3.log, etc.
	for i := 2; i <= 999; i++ {
		logFileName := fmt.Sprintf("ShooterGame_%d.log", i)
		logFilePath := filepath.Join(logsDir, logFileName)
		if _, err := os.Stat(logFilePath); os.IsNotExist(err) {
			return logFileName, nil
		}
	}

	// Fallback (should never happen in practice)
	return "", fmt.Errorf("could not find available log file number for instance %s", instanceName)
}

// GetGameLogFilePath returns the full path to the log file for a given instance
func GetGameLogFilePath(instanceName string) (string, error) {
	logsDir := filepath.Join(ServerFilesDir, "ShooterGame/Saved/Logs")

	// Check if we have a cached mapping
	logMappingMutex.RLock()
	if logPath, exists := instanceLogMapping[instanceName]; exists {
		logMappingMutex.RUnlock()
		return logPath, nil
	}
	logMappingMutex.RUnlock()

	// Get the log file name and create the mapping
	logFileName, err := GetGameLogFileName(instanceName)
	if err != nil {
		return "", err
	}

	logPath := filepath.Join(logsDir, logFileName)

	// Store the mapping
	logMappingMutex.Lock()
	instanceLogMapping[instanceName] = logPath
	logMappingMutex.Unlock()

	return logPath, nil
}

// SetInstanceLogFile manually sets the log file path for an instance
// This is useful when you want to explicitly map an instance to a log file
func SetInstanceLogFile(instanceName, logFilePath string) {
	logMappingMutex.Lock()
	instanceLogMapping[instanceName] = logFilePath
	logMappingMutex.Unlock()
}

// GetInstanceLogFile retrieves the log file path for an instance from the mapping
func GetInstanceLogFile(instanceName string) (string, bool) {
	logMappingMutex.RLock()
	logPath, exists := instanceLogMapping[instanceName]
	logMappingMutex.RUnlock()
	return logPath, exists
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

	// Check if both the game port and RCON port are in the output
	gamePortStr := fmt.Sprintf(":%d", config.Port)
	rconPortStr := fmt.Sprintf(":%d", config.RCONPort)

	// Both ports must be present in the netstat output for the server to be considered running
	hasGamePort := strings.Contains(netstatOutput, gamePortStr)
	hasRCONPort := strings.Contains(netstatOutput, rconPortStr)

	if !hasGamePort || !hasRCONPort {
		// Debug: Print which ports are missing
		if !hasGamePort {
			fmt.Printf("⚠️  Game port :%d not found\n", config.Port)
		}
		if !hasRCONPort {
			fmt.Printf("⚠️  RCON port :%d not found\n", config.RCONPort)
		}
		return false, nil
	}

	return true, nil
}

// copyDir copies a directory recursively
func copyDir(src, dst string) error {
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
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			srcFile, err := os.Open(srcPath)
			if err != nil {
				return err
			}
			defer srcFile.Close()

			dstFile, err := os.Create(dstPath)
			if err != nil {
				return err
			}
			defer dstFile.Close()

			if _, err := io.Copy(dstFile, srcFile); err != nil {
				return err
			}
		}
	}

	return nil
}

// setupInstanceConfig sets up the instance configuration directory with proper symlinks or junctions
func setupInstanceConfig(instanceName string) error {
	instanceConfigDir := filepath.Join(InstancesDir, instanceName, "Config")
	baseConfigDir := filepath.Join(ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer")
	baseConfigDirBackup := baseConfigDir + ".bak"

	// 1. If instance Config directory doesn't exist, copy from base server config
	if _, err := os.Stat(instanceConfigDir); os.IsNotExist(err) {
		fmt.Printf("📋 Copying base server configuration to instance '%s'...\n", instanceName)
		if err := copyDir(baseConfigDir, instanceConfigDir); err != nil {
			return fmt.Errorf("failed to copy config directory: %w", err)
		}
	}

	// 2. Backup the original Config directory if not already backed up
	fileInfo, err := os.Lstat(baseConfigDir)
	if err == nil {
		// If it's not a symlink and backup doesn't exist, back it up
		isSymlink := (fileInfo.Mode() & os.ModeSymlink) != 0
		_, backupErr := os.Stat(baseConfigDirBackup)
		if !isSymlink && os.IsNotExist(backupErr) {
			fmt.Printf("💾 Backing up original configuration directory...\n")
			if err := os.Rename(baseConfigDir, baseConfigDirBackup); err != nil {
				return fmt.Errorf("failed to backup original config directory: %w", err)
			}
		}
	}

	// 3. Remove the symlink/directory and create a junction to instance config
	if err := os.RemoveAll(baseConfigDir); err != nil {
		return fmt.Errorf("failed to remove base config directory: %w", err)
	}

	// Create junction from base config to instance config (no admin required on Windows)
	absInstanceConfigDir, err := filepath.Abs(instanceConfigDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of instance config: %w", err)
	}

	// Try to create as junction using mklink command (works without admin on Windows for NTFS)
	// This is more reliable than os.Symlink which requires admin privileges
	cmd := exec.Command("cmd", "/c", "mklink", "/J", baseConfigDir, absInstanceConfigDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create directory junction: %w", err)
	}

	return nil
}

// StartServer starts a server instance
func StartServer(instanceName string) error {
	if running, err := IsServerRunning(instanceName); err == nil && running {
		fmt.Printf("⚠️  Server for instance %s is already running.\n", instanceName)
		return nil
	}

	// Check for duplicate ports
	if err := CheckForDuplicatePorts(); err != nil {
		fmt.Printf("❌ Port conflicts detected: %v\n", err)
		return err
	}

	config, err := LoadInstanceConfig(instanceName)
	if err != nil {
		return err
	}

	fmt.Printf("🚀 Starting server for instance: %s\n", instanceName)

	// Setup instance configuration directory and symlinks
	if err := setupInstanceConfig(instanceName); err != nil {
		return err
	}

	// Ensure per-instance save directory exists
	saveDir := filepath.Join(ServerFilesDir, "ShooterGame/Saved/SavedArks", config.SaveDir)

	if err := os.MkdirAll(saveDir, 0755); err != nil {
		return fmt.Errorf("failed to create save directory: %w", err)
	}

	// Build the command
	mapParam := fmt.Sprintf("%s?listen?SessionName=%s?ServerPassword=%s?RCONEnabled=True?ServerAdminPassword=%s?AltSaveDirectoryName=%s",
		config.MapName,
		config.ServerName,
		config.ServerPassword,
		config.ServerAdminPassword,
		config.SaveDir,
	)

	// Direct execution of ArkAscendedServer.exe on Windows
	arkExe := filepath.Join(ServerFilesDir, "ShooterGame/Binaries/Win64/ArkAscendedServer.exe")

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

	if config.ModIDs != "" {
		args = append(args, fmt.Sprintf("-mods=%s", config.ModIDs))
	}

	if config.ClusterID != "" {
		clusterDir := filepath.Join(BaseDir, "clusters", config.ClusterID)
		args = append(args,
			fmt.Sprintf("-ClusterDirOverride=%s", clusterDir),
			fmt.Sprintf("-ClusterId=%s", config.ClusterID),
		)
	}

	cmd := exec.Command(arkExe, args...)

	// Get the game log file path and establish mapping
	gameLogPath, err := GetGameLogFilePath(instanceName)
	if err != nil {
		return fmt.Errorf("failed to get game log file path: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	fmt.Printf("✅ Server started for instance: %s. It should be fully operational in approximately 60 seconds.\n", instanceName)
	time.Sleep(60 * time.Second)

	fmt.Printf("📝 Game log file: %s\n", gameLogPath)

	// Persist the log mapping for future restarts
	if err := PersistLogMapping(); err != nil {
		fmt.Printf("⚠️  Warning: Failed to persist log mapping: %v\n", err)
	}

	return nil
}

// GetPIDByPort finds the PID of the process listening on a specific port
func GetPIDByPort(port int) (int, error) {
	cmd := exec.Command("netstat", "-ano")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to execute netstat: %w", err)
	}

	netstatOutput := string(output)
	portStr := fmt.Sprintf(":%d", port)

	// Split output into lines and search for the port
	lines := strings.Split(netstatOutput, "\n")
	for _, line := range lines {
		if strings.Contains(line, portStr) {
			// The last field in the line is the PID
			fields := strings.Fields(line)
			if len(fields) > 0 {
				pid, err := strconv.Atoi(fields[len(fields)-1])
				if err == nil && pid > 0 {
					return pid, nil
				}
			}
		}
	}

	return 0, fmt.Errorf("no process found listening on port %d", port)
}

// StopServer stops a server instance
func StopServer(instanceName string) error {
	running, err := IsServerRunning(instanceName)
	if err != nil || !running {
		fmt.Printf("⚠️  Server for instance %s is not running.\n", instanceName)
		return nil
	}

	fmt.Printf("🛑 Stopping server for instance: %s\n", instanceName)

	// Try graceful shutdown with RCON
	response, err := SendRCONCommand(instanceName, "DoExit")
	fmt.Println("rcon err:", err)
	if err == nil && strings.Contains(response, "Exiting") {
		fmt.Printf("✅ Server instance %s reported 'Exiting...'. Awaiting shutdown (can take up to 2 minutes)...\n", instanceName)

		// Wait for process to finish with timeout
		timeout := time.Now().Add(2 * time.Minute)
		for time.Now().Before(timeout) {
			if running, _ := IsServerRunning(instanceName); !running {
				fmt.Printf("✅ Server for instance %s has exited.\n", instanceName)

				// Backup and remove the log file
				if err := BackupAndRemoveInstanceLogFile(instanceName); err != nil {
					fmt.Printf("⚠️  Warning: Failed to backup log file for instance %s: %v\n", instanceName, err)
				}

				// Remove the log mapping for this instance
				if err := RemoveInstanceLogMapping(instanceName); err != nil {
					fmt.Printf("⚠️  Warning: Failed to remove log mapping for instance %s: %v\n", instanceName, err)
				}

				return nil
			}
			time.Sleep(2 * time.Second)
		}

		fmt.Printf("⚠️  Server didn't shut down within timeout. Forcing kill...\n")
	}

	// Force kill if graceful shutdown didn't work
	// Get the PID using the game port
	config, err := LoadInstanceConfig(instanceName)
	if err != nil {
		return fmt.Errorf("failed to load instance config: %w", err)
	}

	pid, err := GetPIDByPort(config.Port)
	if err != nil {
		return fmt.Errorf("failed to find process PID: %w", err)
	}

	fmt.Printf("🔡 Found process PID: %d for instance '%s' on port :%d\n", pid, instanceName, config.Port)

	// Kill the specific process by PID
	if err := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid), "/F").Run(); err != nil {
		return fmt.Errorf("failed to kill process PID %d: %w", pid, err)
	}

	fmt.Printf("✅ Server for instance %s has been stopped.\n", instanceName)
	time.Sleep(10 * time.Second)

	// Backup and remove the log file
	if err := BackupAndRemoveInstanceLogFile(instanceName); err != nil {
		fmt.Printf("⚠️  Warning: Failed to backup log file for instance %s: %v\n", instanceName, err)
	}

	// Remove the log mapping for this instance
	if err := RemoveInstanceLogMapping(instanceName); err != nil {
		fmt.Printf("⚠️  Warning: Failed to remove log mapping for instance %s: %v\n", instanceName, err)
	}

	return nil
}

// RestartServer restarts a server instance
func RestartServer(instanceName string) error {
	if err := StopServer(instanceName); err != nil {
		return err
	}

	time.Sleep(30 * time.Second)

	return StartServer(instanceName)
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
	fmt.Printf("📡 Connecting to RCON server at %s...\n", rconAddr)
	fmt.Printf("   Instance: %s\n", instanceName)
	fmt.Printf("   RCON Port: %d\n", config.RCONPort)

	var client interface {
		Execute(string) (string, error)
		Close() error
	}
	var connectErr error

	// Try to connect with timeout and retry
	for attempt := 1; attempt <= 3; attempt++ {
		client, connectErr = rcon.Dial(rconAddr, config.ServerAdminPassword)
		if connectErr == nil {
			fmt.Printf("✅ Connected to RCON server\n")
			break
		}

		fmt.Printf("⚠️  Attempt %d failed: %v\n", attempt, connectErr)
		if attempt < 3 {
			fmt.Println("   Retrying in 2 seconds...")
			time.Sleep(2 * time.Second)
		}
	}

	if connectErr != nil {
		fmt.Printf("\n❌ RCON Connection failed (password: '%s')\n", config.ServerAdminPassword)
		fmt.Println("\nTroubleshooting tips:")
		fmt.Println("  1. Verify ServerAdminPassword in instance_config.ini")
		fmt.Println("  2. Check that RCON port is correct: " + rconAddr)
		fmt.Println("  3. Wait 60+ seconds after server start for RCON to be ready")
		fmt.Println("  4. Check server log for 'RCON password' or 'authentication' errors")
		return "", fmt.Errorf("failed to connect to RCON server at %s: %w", rconAddr, connectErr)
	}
	defer client.Close()

	// Send command
	fmt.Printf("📡 Sending RCON command '%s' to %s\n", command, rconAddr)
	response, err := client.Execute(command)
	if err != nil {
		return "", fmt.Errorf("RCON command execution failed: %w", err)
	}

	fmt.Printf("✅ RCON response: %s\n", response)
	return response, nil
}

// StartAllInstances starts all instances with delay between each
func StartAllInstances() error {
	instances, err := GetAvailableInstances()
	if err != nil {
		return err
	}

	fmt.Println("🚀 Starting all server instances...")

	for _, instanceName := range instances {
		running, err := IsServerRunning(instanceName)
		if err == nil && running {
			fmt.Printf("⚠️  Instance %s is already running. Skipping...\n", instanceName)
			continue
		}

		if err := StartServer(instanceName); err != nil {
			fmt.Printf("❌ Failed to start instance %s: %v\n", instanceName, err)
			continue
		}

		fmt.Println("⏳ Waiting 30 seconds before starting the next instance...")
		time.Sleep(30 * time.Second)
	}

	fmt.Println("✅ All instances have been processed.")
	return nil
}

// StopAllInstances stops all instances
func StopAllInstances() error {
	instances, err := GetAvailableInstances()
	if err != nil {
		return err
	}

	fmt.Println("🛑 Stopping all server instances...")

	for _, instanceName := range instances {
		running, err := IsServerRunning(instanceName)
		if err == nil && !running {
			fmt.Printf("⚠️  Instance %s is not running. Skipping...\n", instanceName)
			continue
		}

		if err := StopServer(instanceName); err != nil {
			fmt.Printf("❌ Failed to stop instance %s: %v\n", instanceName, err)
		}
	}

	fmt.Println("✅ All instances have been stopped.")
	return nil
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
