package asaserver

import (
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
	fmt.Println("📦 Installing/updating base server...")

	// Download and extract SteamCMD
	if err := DownloadAndExtractSteamCmd(); err != nil {
		return err
	}

	// Download and update ARK server
	if err := DownloadAndUpdateArkServer(); err != nil {
		return err
	}

	// Get force-server flag
	forceServer := cmd.Bool("force-server")

	// Verify server installation by running it to generate config files
	if err := VerifyServerInstallation(forceServer); err != nil {
		return err
	}

	fmt.Println("✅ Base server installation/update completed.")
	return nil
}

func ActionList(ctx context.Context, cmd *cli.Command) error {
	instances, err := GetAvailableInstances()
	if err != nil {
		return err
	}

	if len(instances) == 0 {
		fmt.Printf("❌ No instances found in '%s'.\n", InstancesDir)
		return nil
	}

	fmt.Println("📋 Available instances:")
	for _, inst := range instances {
		running, _ := IsServerRunning(inst)
		status := "❌"
		if running {
			status = "✅"
		}
		fmt.Printf("  %s %s\n", status, inst)
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
		fmt.Println("❌ Instance name cannot be empty.")
		return nil
	}

	// Check if instance already exists
	if _, err := os.Stat(filepath.Join(InstancesDir, instanceName)); err == nil {
		fmt.Printf("❌ Instance '%s' already exists.\n", instanceName)
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
		fmt.Printf("📋 Copying base server configuration files to instance '%s'...\n", instanceName)
		if err := CopyDir(baseConfigDir, instanceConfigDir); err != nil {
			fmt.Printf("⚠️  Warning: Failed to copy base server configuration: %v\n", err)
			// Continue anyway as this is not critical
		}
	} else {
		fmt.Printf("⚠️  Base server configuration directory not found at %s\n", baseConfigDir)
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

	fmt.Printf("✅ Instance '%s' created successfully.\n", instanceName)
	fmt.Printf("📝 Configuration file: %s\n", filepath.Join(InstancesDir, instanceName, "instance_config.ini"))

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
			fmt.Println("❌ No instances found.")
			return nil
		}

		fmt.Println("🔍 Checking running instances...")
		runningCount := 0
		for _, instanceName := range instances {
			running, err := IsServerRunning(instanceName)
			if err == nil && running {
				fmt.Printf("  ✅ %s is running\n", instanceName)
				runningCount++
			} else {
				fmt.Printf("  ❌ %s is not running\n", instanceName)
			}
		}

		if runningCount == 0 {
			fmt.Println("❌ No instances are currently running.")
		} else {
			fmt.Printf("✅ Total running instances: %d\n", runningCount)
		}
		return nil
	}

	instanceName := args.Get(0)
	running, err := IsServerRunning(instanceName)
	if err != nil {
		return err
	}

	if running {
		fmt.Printf("✅ Server for instance %s is running.\n", instanceName)
	} else {
		fmt.Printf("❌ Server for instance %s is not running.\n", instanceName)
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

	fmt.Printf("📡 Response: %s\n", response)
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

	fmt.Printf("⚠️  WARNING: This will permanently delete instance '%s' and all its data.\n", instanceName)
	fmt.Print("Type 'CONFIRM' to delete the instance, or 'cancel' to abort: ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("failed to read input")
	}

	response := strings.TrimSpace(scanner.Text())
	if response != "CONFIRM" {
		fmt.Println("❌ Deletion cancelled.")
		return nil
	}

	// Stop instance if running
	if running, _ := IsServerRunning(instanceName); running {
		fmt.Printf("🛑 Stopping instance '%s'...\n", instanceName)
		if err := StopServer(instanceName); err != nil {
			fmt.Printf("⚠️  Error stopping server: %v\n", err)
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

	fmt.Printf("✅ Instance '%s' has been deleted.\n", instanceName)
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
		fmt.Println("❌ Instance name cannot be empty.")
		return nil
	}

	if newName == instanceName {
		fmt.Println("⚠️  New name is the same as current name.")
		return nil
	}

	// Stop instance if running
	if running, _ := IsServerRunning(instanceName); running {
		fmt.Printf("🛑 Stopping instance '%s' before renaming...\n", instanceName)
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

	fmt.Printf("✅ Instance renamed from '%s' to '%s'.\n", instanceName, newName)
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
		fmt.Println("❌ No backups found.")
		return nil
	}

	fmt.Println("📋 Available backups:")
	for i, backup := range backups {
		fmt.Printf("  %d) %s\n", i+1, filepath.Base(backup))
	}

	fmt.Print("Select a backup (number): ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("failed to read input")
	}

	choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if err != nil || choice < 1 || choice > len(backups) {
		fmt.Println("❌ Invalid selection.")
		return nil
	}

	selectedBackup := backups[choice-1]

	fmt.Printf("⚠️  WARNING: Restoring this backup may overwrite existing worlds.\n")
	fmt.Print("Type 'CONFIRM' to proceed or 'cancel' to abort: ")

	if !scanner.Scan() {
		return fmt.Errorf("failed to read input")
	}

	response := strings.TrimSpace(scanner.Text())
	if response != "CONFIRM" {
		fmt.Println("❌ Restore cancelled.")
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

	fmt.Printf("\n📄 Game.ini for instance '%s':\n", instanceName)
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println(content)
	fmt.Println(strings.Repeat("=", 80))

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

	fmt.Printf("\n📄 GameUserSettings.ini for instance '%s':\n", instanceName)
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println(content)
	fmt.Println(strings.Repeat("=", 80))

	return nil
}

func ActionConfigRestart(ctx context.Context, cmd *cli.Command) error {
	fmt.Println("⚠️  Restart manager configuration is not yet implemented.")
	fmt.Println("📝 You can manually edit the restart configuration file when ready.")
	return nil
}

// Helper functions

func selectInstance() string {
	instances, err := GetAvailableInstances()
	if err != nil {
		fmt.Printf("❌ Error getting instances: %v\n", err)
		return ""
	}

	if len(instances) == 0 {
		fmt.Println("❌ No instances found.")
		return ""
	}

	fmt.Println("📋 Available instances:")
	for i, inst := range instances {
		fmt.Printf("  %d) %s\n", i+1, inst)
	}

	fmt.Print("Select an instance (number): ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return ""
	}

	choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if err != nil || choice < 1 || choice > len(instances) {
		fmt.Println("❌ Invalid selection.")
		return ""
	}

	return instances[choice-1]
}

func manageInstanceMenu(instanceName string) error {
	for {
		fmt.Printf("\n🎮 Managing Instance: %s\n", instanceName)
		fmt.Println("Options:")
		fmt.Println("  1) Start Server")
		fmt.Println("  2) Stop Server")
		fmt.Println("  3) Restart Server")
		fmt.Println("  4) Check Status")
		fmt.Println("  5) Send RCON Command")
		fmt.Println("  6) Backup World")
		fmt.Println("  7) Restore Backup")
		fmt.Println("  8) View Live Logs")
		fmt.Println("  9) Edit Configuration")
		fmt.Println("  10) Change Instance Name")
		fmt.Println("  0) Back to Main Menu")

		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			break
		}

		choice := strings.TrimSpace(scanner.Text())
		switch choice {
		case "1":
			if err := StartServer(instanceName); err != nil {
				fmt.Printf("❌ Error starting server: %v\n", err)
			}
		case "2":
			if err := StopServer(instanceName); err != nil {
				fmt.Printf("❌ Error stopping server: %v\n", err)
			}
		case "3":
			if err := RestartServer(instanceName); err != nil {
				fmt.Printf("❌ Error restarting server: %v\n", err)
			}
		case "4":
			running, err := IsServerRunning(instanceName)
			if err != nil {
				fmt.Printf("❌ Error checking server status: %v\n", err)
			} else if running {
				fmt.Printf("✅ Server for instance %s is running.\n", instanceName)
			} else {
				fmt.Printf("❌ Server for instance %s is not running.\n", instanceName)
			}
		case "5":
			fmt.Print("Enter RCON command: ")
			if scanner.Scan() {
				command := strings.TrimSpace(scanner.Text())
				if err := actionRCONImpl(instanceName, command); err != nil {
					fmt.Printf("❌ Error sending RCON command: %v\n", err)
				}
			}
		case "6":
			fmt.Print("Enter world folder name: ")
			if scanner.Scan() {
				worldFolder := strings.TrimSpace(scanner.Text())
				if err := BackupInstanceWorld(instanceName, worldFolder); err != nil {
					fmt.Printf("❌ Error backing up world: %v\n", err)
				}
			}
		case "7":
			// Simulate restore action with local backup selection
			backups, err := GetAvailableBackups()
			if err != nil {
				fmt.Printf("❌ Error retrieving backups: %v\n", err)
			} else if len(backups) > 0 {
				fmt.Println("📋 Available backups:")
				for i, backup := range backups {
					fmt.Printf("  %d) %s\n", i+1, filepath.Base(backup))
				}
				fmt.Print("Select a backup (number): ")
				if scanner.Scan() {
					choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
					if err != nil {
						fmt.Printf("❌ Invalid backup number: %v\n", err)
					} else if choice > 0 && choice <= len(backups) {
						if err := RestoreBackupToInstance(instanceName, backups[choice-1]); err != nil {
							fmt.Printf("❌ Error restoring backup: %v\n", err)
						}
					} else {
						fmt.Println("❌ Invalid backup selection.")
					}
				}
			} else {
				fmt.Println("⚠️  No backups available.")
			}
		case "8":
			if err := viewInstanceLogs(instanceName); err != nil {
				fmt.Printf("❌ Error viewing logs: %v\n", err)
			}
		case "9":
			if err := editInstanceConfigFile(instanceName); err != nil {
				fmt.Printf("❌ Error editing configuration: %v\n", err)
			}
		case "10":
			fmt.Print("Enter the new name for the instance: ")
			if scanner.Scan() {
				newName := strings.TrimSpace(scanner.Text())
				switch newName {
				case "":
					fmt.Println("❌ Instance name cannot be empty.")
				case instanceName:
					fmt.Println("⚠️  New name is the same as the old name.")
				default:
					// Perform rename
					if running, _ := IsServerRunning(instanceName); running {
						fmt.Println("⏸️  Stopping server before rename...")
						if err := StopServer(instanceName); err != nil {
							fmt.Printf("❌ Error stopping server: %v\n", err)
						}
					}
					oldPath := filepath.Join(InstancesDir, instanceName)
					newPath := filepath.Join(InstancesDir, newName)
					if err := os.Rename(oldPath, newPath); err != nil {
						fmt.Printf("❌ Error renaming instance directory: %v\n", err)
					} else {
						config, err := LoadInstanceConfig(newName)
						if err != nil {
							fmt.Printf("❌ Error loading instance config: %v\n", err)
						} else {
							config.SaveDir = newName
							if err := SaveInstanceConfig(newName, config); err != nil {
								fmt.Printf("❌ Error saving instance config: %v\n", err)
							} else {
								fmt.Printf("✅ Instance renamed to '%s'.\n", newName)
								return nil
							}
						}
					}
				}
			}
		case "0":
			return nil
		default:
			fmt.Println("❌ Invalid option.")
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
		fmt.Printf("⚠️  Log file not found: %s\n", logPath)
		fmt.Println("Tip: Start the server first to generate log files.")
		return nil
	}

	fmt.Printf("📄 Viewing live logs for instance '%s'\n", instanceName)
	fmt.Printf("📝 Log file: %s\n", logPath)
	fmt.Println("Press Ctrl+C to stop viewing logs...")
	fmt.Println(strings.Repeat("=", 80))

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
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println("✅ Log viewing stopped.")
	return nil
}

func editInstanceConfigFile(instanceName string) error {
	configPath := filepath.Join(InstancesDir, instanceName, "instance_config.ini")

	// Check if config file exists
	if _, err := os.Stat(configPath); err != nil {
		return fmt.Errorf("config file not found: %s", configPath)
	}

	fmt.Printf("📝 Opening configuration file: %s\n", configPath)

	// Open the file with Notepad on Windows
	cmd := exec.Command("notepad.exe", configPath)

	// Run the command and wait for it to complete
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to open notepad: %w", err)
	}

	fmt.Println("✅ Configuration file editing completed.")
	return nil
}
