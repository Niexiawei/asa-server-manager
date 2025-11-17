package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gorcon/rcon"
)

// IsServerRunning checks if a server instance is running
// It checks by verifying if both the game port and RCON port are listening
// This uniquely identifies the specific server instance
func IsServerRunning(instanceName string) (bool, error) {
	// Load the instance configuration to get the ports
	config, err := LoadInstanceConfig(instanceName)
	if err != nil {
		return false, err
	}

	// Use Windows netstat with findstr to check both ports in one command
	// This is more efficient than checking separately
	portPattern := fmt.Sprintf("/R \"%d|%d\"", config.Port, config.RCONPort)
	cmd := exec.Command("cmd", "/C", fmt.Sprintf("netstat -ano | findstr %s", portPattern))

	// Hide the cmd window on Windows
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	output, err := cmd.Output()
	if err != nil {
		return false, nil
	}

	netstatOutput := string(output)

	// Check if both the game port and RCON port are in the output
	gamePortStr := fmt.Sprintf(":%d", config.Port)
	rconPortStr := fmt.Sprintf(":%d", config.RCONPort)

	// Both ports must be present in the netstat output for the server to be considered running
	hasGamePort := strings.Contains(netstatOutput, gamePortStr)
	hasRCONPort := strings.Contains(netstatOutput, rconPortStr)

	return hasGamePort && hasRCONPort, nil
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

	// Create Config directory if it doesn't exist
	configDir := filepath.Join(InstancesDir, instanceName, "Config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
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

	logFile := filepath.Join(InstancesDir, instanceName, "server.log")
	file, err := os.Create(logFile)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer file.Close()

	cmd.Stdout = file
	cmd.Stderr = file

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	fmt.Printf("✅ Server started for instance: %s. It should be fully operational in approximately 60 seconds.\n", instanceName)
	return nil
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
	if err == nil && strings.Contains(response, "Exiting") {
		fmt.Printf("✅ Server instance %s reported 'Exiting...'. Awaiting shutdown (can take up to 2 minutes)...\n", instanceName)

		// Wait for process to finish with timeout
		timeout := time.Now().Add(2 * time.Minute)
		for time.Now().Before(timeout) {
			if running, _ := IsServerRunning(instanceName); !running {
				fmt.Printf("✅ Server for instance %s has exited.\n", instanceName)
				return nil
			}
			time.Sleep(2 * time.Second)
		}

		fmt.Printf("⚠️  Server didn't shut down within timeout. Forcing kill...\n")
	}

	// Force kill if graceful shutdown didn't work
	exec.Command("taskkill", "/IM", "ArkAscendedServer.exe", "/F").Run()

	fmt.Printf("✅ Server for instance %s has been stopped.\n", instanceName)
	return nil
}

// RestartServer restarts a server instance
func RestartServer(instanceName string) error {
	if err := StopServer(instanceName); err != nil {
		return err
	}

	time.Sleep(5 * time.Second)

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

	// Connect to RCON server
	rconAddr := fmt.Sprintf("localhost:%d", config.RCONPort)
	client, err := rcon.Dial(rconAddr, config.ServerAdminPassword)
	if err != nil {
		return "", fmt.Errorf("failed to connect to RCON server at %s: %w", rconAddr, err)
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
