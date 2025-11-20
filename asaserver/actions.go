package asaserver

import (
	"asa-server/logger"
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"
)

// Action functions for commands

func ActionUpdate(ctx context.Context, cmd *cli.Command) error {
	logger.GetLogger().Info("Installing/updating base server...")

	stdoutFmt := os.Stdout
	// Download and extract SteamCMD
	if err := DownloadAndExtractSteamCmd(stdoutFmt); err != nil {
		return err
	}

	// Download and update ARK server
	if err := DownloadAndUpdateArkServer(stdoutFmt); err != nil {
		return err
	}

	// Get force-server flag
	forceServer := cmd.Bool("force-server")

	// Verify server installation by running it to generate config files
	if err := VerifyServerInstallation(forceServer); err != nil {
		return err
	}

	logger.GetLogger().Info("Base server installation/update completed.")
	return nil
}

func ActionList(ctx context.Context, cmd *cli.Command) error {
	instances, err := GetAvailableInstances()
	if err != nil {
		return err
	}

	if len(instances) == 0 {
		logger.GetLogger().Warnf("No instances found in '%s'.", InstancesDir)
		return nil
	}

	logger.GetLogger().Info("Available instances:")
	for _, inst := range instances {
		running, _ := IsServerRunning(inst)
		status := "OFFLINE"
		if running {
			status = "ONLINE"
		}
		logger.GetLogger().Infof("  %s %s", status, inst)
	}

	return nil
}

func ActionCreate(ctx context.Context, cmd *cli.Command) error {
	fmt.Print("Enter the name for the new instance: ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("failed to read input")
	}

	instanceName := strings.TrimSpace(scanner.Text())
	if instanceName == "" {
		logger.GetLogger().Warn("Instance name cannot be empty.")
		return nil
	}

	// Check if instance already exists
	if _, err := os.Stat(filepath.Join(InstancesDir, instanceName)); err == nil {
		logger.GetLogger().Warnf("Instance '%s' already exists.", instanceName)
		return nil
	}

	// Create instance directory
	if err := os.MkdirAll(filepath.Join(InstancesDir, instanceName, "Config"), 0755); err != nil {
		return fmt.Errorf("failed to create instance directory: %w", err)
	}

	// Copy base server configuration files to instance Config directory
	baseConfigDir := filepath.Join(ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer")
	instanceConfigDir := filepath.Join(InstancesDir, instanceName, "Config")
	if _, err := os.Stat(baseConfigDir); err == nil {
		logger.GetLogger().Infof("Copying base server configuration files to instance '%s'...", instanceName)
		if err := CopyDir(baseConfigDir, instanceConfigDir); err != nil {
			logger.GetLogger().Warnf("Failed to copy base server configuration: %v", err)
			// Continue anyway as this is not critical
		}
	} else {
		logger.GetLogger().Warnf("Base server configuration directory not found at %s", baseConfigDir)
	}

	// Create default configuration
	config := CreateDefaultInstanceConfig(instanceName)
	if err := SaveInstanceConfig(instanceName, config); err != nil {
		return err
	}

	// Create empty Game.ini if not already copied from base server
	gameIniPath := filepath.Join(InstancesDir, instanceName, "Config", "Game.ini")
	if _, err := os.Stat(gameIniPath); os.IsNotExist(err) {
		if err := os.WriteFile(gameIniPath, []byte(""), 0644); err != nil {
			return fmt.Errorf("failed to create Game.ini: %w", err)
		}
	}

	logger.GetLogger().Infof("Instance '%s' created successfully.", instanceName)
	logger.GetLogger().Infof("Configuration file: %s", filepath.Join(InstancesDir, instanceName, "instance_config.ini"))

	return nil
}

func ActionManage(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	instanceName := ""
	if args.Len() > 0 {
		instanceName = args.Get(0)
	}

	if instanceName == "" {
		instanceName = selectInstance()
		if instanceName == "" {
			return fmt.Errorf("no instance selected")
		}
	}

	return manageInstanceMenu(instanceName)
}

func ActionStart(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() < 1 {
		return fmt.Errorf("instance name required")
	}

	instanceName := args.Get(0)
	return StartServer(instanceName)
}

func ActionStop(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() < 1 {
		return fmt.Errorf("instance name required")
	}

	instanceName := args.Get(0)
	return StopServer(instanceName)
}

func ActionRestart(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() < 1 {
		return fmt.Errorf("instance name required")
	}

	instanceName := args.Get(0)
	return RestartServer(instanceName)
}

func ActionStatus(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() == 0 {
		// Show status of all instances
		instances, err := GetAvailableInstances()
		if err != nil {
			return err
		}

		if len(instances) == 0 {
			logger.GetLogger().Warn("No instances found.")
			return nil
		}

		logger.GetLogger().Info("Checking running instances...")
		runningCount := 0
		for _, instanceName := range instances {
			running, err := IsServerRunning(instanceName)
			if err == nil && running {
				logger.GetLogger().Infof("  %s is running", instanceName)
				runningCount++
			} else {
				logger.GetLogger().Infof("  %s is not running", instanceName)
			}
		}

		if runningCount == 0 {
			logger.GetLogger().Warn("No instances are currently running.")
		} else {
			logger.GetLogger().Infof("Total running instances: %d", runningCount)
		}
		return nil
	}

	instanceName := args.Get(0)
	running, err := IsServerRunning(instanceName)
	if err != nil {
		return err
	}

	if running {
		logger.GetLogger().Infof("Server for instance %s is running.", instanceName)
	} else {
		logger.GetLogger().Infof("Server for instance %s is not running.", instanceName)
	}

	return nil
}

func ActionRCON(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() < 2 {
		return fmt.Errorf("instance name and command required")
	}

	instanceName := args.Get(0)
	command := args.Get(1)

	return actionRCONImpl(instanceName, command)
}

func actionRCONImpl(instanceName string, command string) error {
	response, err := SendRCONCommand(instanceName, command)
	if err != nil {
		return err
	}

	logger.GetLogger().Infof("Response: %s", response)
	return nil
}

func ActionDelete(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	var instanceName string
	if args.Len() > 0 {
		instanceName = args.Get(0)
	} else {
		instanceName = selectInstance()
		if instanceName == "" {
			return fmt.Errorf("no instance selected")
		}
	}

	logger.GetLogger().Warnf("WARNING: This will permanently delete instance '%s' and all its data.", instanceName)
	fmt.Print("Type 'CONFIRM' to delete the instance, or 'cancel' to abort: ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("failed to read input")
	}

	response := strings.TrimSpace(scanner.Text())
	if response != "CONFIRM" {
		logger.GetLogger().Info("Deletion cancelled.")
		return nil
	}

	// Stop instance if running
	if running, _ := IsServerRunning(instanceName); running {
		logger.GetLogger().Infof("Stopping instance '%s'...", instanceName)
		if err := StopServer(instanceName); err != nil {
			logger.GetLogger().Errorf("Error stopping server: %v", err)
		}
	}

	// Delete instance directory
	instanceDir := filepath.Join(InstancesDir, instanceName)
	if err := os.RemoveAll(instanceDir); err != nil {
		return fmt.Errorf("failed to delete instance directory: %w", err)
	}

	// Delete save directories
	savePath := filepath.Join(ServerFilesDir, "ShooterGame/Saved", instanceName)
	os.RemoveAll(savePath)

	savedArksPath := filepath.Join(ServerFilesDir, "ShooterGame/Saved/SavedArks", instanceName)
	os.RemoveAll(savedArksPath)

	logger.GetLogger().Infof("Instance '%s' has been deleted.", instanceName)
	return nil
}

func ActionRename(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	var instanceName string
	if args.Len() > 0 {
		instanceName = args.Get(0)
	} else {
		instanceName = selectInstance()
		if instanceName == "" {
			return fmt.Errorf("no instance selected")
		}
	}

	fmt.Print("Enter the new name for the instance: ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("failed to read input")
	}

	newName := strings.TrimSpace(scanner.Text())
	if newName == "" {
		logger.GetLogger().Warn("Instance name cannot be empty.")
		return nil
	}

	if newName == instanceName {
		logger.GetLogger().Warn("New name is the same as current name.")
		return nil
	}

	// Stop instance if running
	if running, _ := IsServerRunning(instanceName); running {
		logger.GetLogger().Infof("Stopping instance '%s' before renaming...", instanceName)
		if err := StopServer(instanceName); err != nil {
			return fmt.Errorf("failed to stop server: %w", err)
		}
	}

	// Rename instance directory
	oldPath := filepath.Join(InstancesDir, instanceName)
	newPath := filepath.Join(InstancesDir, newName)

	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("failed to rename instance directory: %w", err)
	}

	// Rename save directories
	oldSavePath := filepath.Join(ServerFilesDir, "ShooterGame/Saved", instanceName)
	newSavePath := filepath.Join(ServerFilesDir, "ShooterGame/Saved", newName)
	os.Rename(oldSavePath, newSavePath)

	oldArksPath := filepath.Join(ServerFilesDir, "ShooterGame/Saved/SavedArks", instanceName)
	newArksPath := filepath.Join(ServerFilesDir, "ShooterGame/Saved/SavedArks", newName)
	os.Rename(oldArksPath, newArksPath)

	// Update SaveDir in configuration
	config, err := LoadInstanceConfig(newName)
	if err == nil {
		config.SaveDir = newName
		SaveInstanceConfig(newName, config)
	}

	logger.GetLogger().Infof("Instance renamed from '%s' to '%s'.", instanceName, newName)
	return nil
}

func ActionBackup(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() < 2 {
		return fmt.Errorf("instance name and world folder required")
	}

	instanceName := args.Get(0)
	worldFolder := args.Get(1)

	return BackupInstanceWorld(instanceName, worldFolder)
}

func ActionRestore(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() < 1 {
		return fmt.Errorf("instance name required")
	}

	instanceName := args.Get(0)

	backups, err := GetAvailableBackups()
	if err != nil {
		return err
	}

	if len(backups) == 0 {
		logger.GetLogger().Warn("No backups found.")
		return nil
	}

	logger.GetLogger().Info("Available backups:")
	for i, backup := range backups {
		logger.GetLogger().Infof("  %d) %s", i+1, filepath.Base(backup))
	}

	fmt.Print("Select a backup (number): ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("failed to read input")
	}

	choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if err != nil || choice < 1 || choice > len(backups) {
		logger.GetLogger().Warn("Invalid selection.")
		return nil
	}

	selectedBackup := backups[choice-1]

	logger.GetLogger().Warn("WARNING: Restoring this backup may overwrite existing worlds.")
	fmt.Print("Type 'CONFIRM' to proceed or 'cancel' to abort: ")

	if !scanner.Scan() {
		return fmt.Errorf("failed to read input")
	}

	response := strings.TrimSpace(scanner.Text())
	if response != "CONFIRM" {
		logger.GetLogger().Info("Restore cancelled.")
		return nil
	}

	return RestoreBackupToInstance(instanceName, selectedBackup)
}

func ActionStartAll(ctx context.Context, cmd *cli.Command) error {
	return StartAllInstances()
}

func ActionStopAll(ctx context.Context, cmd *cli.Command) error {
	return StopAllInstances()
}

func ActionViewGameIni(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	var instanceName string
	if args.Len() > 0 {
		instanceName = args.Get(0)
	} else {
		instanceName = selectInstance()
		if instanceName == "" {
			return fmt.Errorf("no instance selected")
		}
	}

	content, err := GetGameIniContent(instanceName)
	if err != nil {
		return err
	}

	logger.GetLogger().Infof("Game.ini for instance '%s':", instanceName)
	logger.GetLogger().Info(content)

	return nil
}

func ActionViewGameUserSettings(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	var instanceName string
	if args.Len() > 0 {
		instanceName = args.Get(0)
	} else {
		instanceName = selectInstance()
		if instanceName == "" {
			return fmt.Errorf("no instance selected")
		}
	}

	content, err := GetGameUserSettingsContent(instanceName)
	if err != nil {
		return err
	}

	logger.GetLogger().Infof("GameUserSettings.ini for instance '%s':", instanceName)
	logger.GetLogger().Info(content)

	return nil
}

func ActionConfigRestart(ctx context.Context, cmd *cli.Command) error {
	logger.GetLogger().Warn("Restart manager configuration is not yet implemented.")
	logger.GetLogger().Info("You can manually edit the restart configuration file when ready.")
	return nil
}

// Helper functions

func selectInstance() string {
	instances, err := GetAvailableInstances()
	if err != nil {
		logger.GetLogger().Errorf("Error getting instances: %v", err)
		return ""
	}

	if len(instances) == 0 {
		logger.GetLogger().Warn("No instances found.")
		return ""
	}

	logger.GetLogger().Info("Available instances:")
	for i, inst := range instances {
		logger.GetLogger().Infof("  %d) %s", i+1, inst)
	}

	fmt.Print("Select an instance (number): ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return ""
	}

	choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if err != nil || choice < 1 || choice > len(instances) {
		logger.GetLogger().Warn("Invalid selection.")
		return ""
	}

	return instances[choice-1]
}

func manageInstanceMenu(instanceName string) error {
	for {
		fmt.Printf("Managing Instance: %s \n", instanceName)
		fmt.Printf("Options: \n")
		fmt.Printf("  1) Start Server \n")
		fmt.Printf("  2) Stop Server \n")
		fmt.Printf("  3) Restart Server \n")
		fmt.Printf("  4) Check Status \n")
		fmt.Printf("  5) Send RCON Command \n")
		fmt.Printf("  6) Backup World \n")
		fmt.Printf("  7) Restore Backup \n")
		fmt.Printf("  8) View Live Logs \n")
		fmt.Printf("  9) Edit Configuration \n")
		fmt.Printf("  10) Change Instance Name \n")
		fmt.Printf("  0) Back to Main Menu \n")

		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			break
		}

		choice := strings.TrimSpace(scanner.Text())
		switch choice {
		case "1":
			if err := StartServer(instanceName); err != nil {
				logger.GetLogger().Errorf("Error starting server: %v", err)
			}
		case "2":
			if err := StopServer(instanceName); err != nil {
				logger.GetLogger().Errorf("Error stopping server: %v", err)
			}
		case "3":
			if err := RestartServer(instanceName); err != nil {
				logger.GetLogger().Errorf("Error restarting server: %v", err)
			}
		case "4":
			running, err := IsServerRunning(instanceName)
			if err != nil {
				logger.GetLogger().Errorf("Error checking server status: %v", err)
			} else if running {
				logger.GetLogger().Infof("Server for instance %s is running.", instanceName)
			} else {
				logger.GetLogger().Infof("Server for instance %s is not running.", instanceName)
			}
		case "5":
			fmt.Print("Enter RCON command: ")
			if scanner.Scan() {
				command := strings.TrimSpace(scanner.Text())
				if err := actionRCONImpl(instanceName, command); err != nil {
					logger.GetLogger().Errorf("Error sending RCON command: %v", err)
				}
			}
		case "6":
			fmt.Print("Enter world folder name: ")
			if scanner.Scan() {
				worldFolder := strings.TrimSpace(scanner.Text())
				if err := BackupInstanceWorld(instanceName, worldFolder); err != nil {
					logger.GetLogger().Errorf("Error backing up world: %v", err)
				}
			}
		case "7":
			// Simulate restore action with local backup selection
			backups, err := GetAvailableBackups()
			if err != nil {
				logger.GetLogger().Errorf("Error retrieving backups: %v", err)
			} else if len(backups) > 0 {
				logger.GetLogger().Info("Available backups:")
				for i, backup := range backups {
					logger.GetLogger().Infof("  %d) %s", i+1, filepath.Base(backup))
				}
				fmt.Print("Select a backup (number): ")
				if scanner.Scan() {
					choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
					if err != nil {
						logger.GetLogger().Errorf("Invalid backup number: %v", err)
					} else if choice > 0 && choice <= len(backups) {
						if err := RestoreBackupToInstance(instanceName, backups[choice-1]); err != nil {
							logger.GetLogger().Errorf("Error restoring backup: %v", err)
						}
					} else {
						logger.GetLogger().Warn("Invalid backup selection.")
					}
				}
			} else {
				logger.GetLogger().Warn("No backups available.")
			}
		case "8":
			if err := viewInstanceLogs(instanceName); err != nil {
				logger.GetLogger().Errorf("Error viewing logs: %v", err)
			}
		case "9":
			if err := editInstanceConfigFile(instanceName); err != nil {
				logger.GetLogger().Errorf("Error editing configuration: %v", err)
			}
		case "10":
			fmt.Print("Enter the new name for the instance: ")
			if scanner.Scan() {
				newName := strings.TrimSpace(scanner.Text())
				switch newName {
				case "":
					logger.GetLogger().Warn("Instance name cannot be empty.")
				case instanceName:
					logger.GetLogger().Warn("New name is the same as the old name.")
				default:
					// Perform rename
					if running, _ := IsServerRunning(instanceName); running {
						logger.GetLogger().Info("Stopping server before rename...")
						if err := StopServer(instanceName); err != nil {
							logger.GetLogger().Errorf("Error stopping server: %v", err)
						}
					}
					oldPath := filepath.Join(InstancesDir, instanceName)
					newPath := filepath.Join(InstancesDir, newName)
					if err := os.Rename(oldPath, newPath); err != nil {
						logger.GetLogger().Errorf("Error renaming instance directory: %v", err)
					} else {
						config, err := LoadInstanceConfig(newName)
						if err != nil {
							logger.GetLogger().Errorf("Error loading instance config: %v", err)
						} else {
							config.SaveDir = newName
							if err := SaveInstanceConfig(newName, config); err != nil {
								logger.GetLogger().Errorf("Error saving instance config: %v", err)
							} else {
								logger.GetLogger().Infof("Instance renamed to '%s'.", newName)
								return nil
							}
						}
					}
				}
			}
		case "0":
			return nil
		default:
			logger.GetLogger().Warn("Invalid option.")
		}
	}

	return nil
}

func viewInstanceLogs(instanceName string) error {
	// Get the log file path for the instance
	logPath, exists := GetInstanceLogFile(instanceName)
	if !exists {
		// Try to get the log path if not in mapping
		var err error
		logPath, err = GetGameLogFilePath(instanceName)
		if err != nil {
			return fmt.Errorf("failed to get log file path: %w", err)
		}
	}

	// Check if log file exists
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		logger.GetLogger().Warnf("Log file not found: %s", logPath)
		logger.GetLogger().Info("Tip: Start the server first to generate log files.")
		return nil
	}

	logger.GetLogger().Infof("Viewing live logs for instance '%s'", instanceName)
	logger.GetLogger().Infof("Log file: %s", logPath)
	logger.GetLogger().Info("Press Ctrl+C to stop viewing logs...")

	// Start tailing the log file in real-time
	stopMonitoring := TailLogFile(logPath, func(line string) {
		fmt.Println(line)
	})

	// Wait for user to press Ctrl+C
	// Create a channel to handle interrupts
	var input string
	scanner := bufio.NewScanner(os.Stdin)
	for {
		// Check if there's input to stop (user presses Ctrl+C is handled by OS)
		if !scanner.Scan() {
			break
		}
		input = scanner.Text()
		if input != "" {
			break
		}
	}

	// Stop monitoring
	stopMonitoring()
	logger.GetLogger().Info("Log viewing stopped.")
	return nil
}

func editInstanceConfigFile(instanceName string) error {
	configPath := filepath.Join(InstancesDir, instanceName, "instance_config.ini")

	// Check if config file exists
	if _, err := os.Stat(configPath); err != nil {
		return fmt.Errorf("config file not found: %s", configPath)
	}

	logger.GetLogger().Infof("Opening configuration file: %s", configPath)

	// Open the file with Notepad on Windows
	cmd := exec.Command("notepad.exe", configPath)

	// Run the command and wait for it to complete
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to open notepad: %w", err)
	}

	logger.GetLogger().Info("Configuration file editing completed.")
	return nil
}
