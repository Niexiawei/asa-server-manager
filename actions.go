package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"
)

// Action functions for commands

func actionUpdate(ctx context.Context, cmd *cli.Command) error {
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

func actionList(ctx context.Context, cmd *cli.Command) error {
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

func actionCreate(ctx context.Context, cmd *cli.Command) error {
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

	// Create default configuration
	config := CreateDefaultInstanceConfig(instanceName)
	if err := SaveInstanceConfig(instanceName, config); err != nil {
		return err
	}

	// Create empty Game.ini
	gameIniPath := filepath.Join(InstancesDir, instanceName, "Config", "Game.ini")
	if err := os.WriteFile(gameIniPath, []byte(""), 0644); err != nil {
		return fmt.Errorf("failed to create Game.ini: %w", err)
	}

	fmt.Printf("✅ Instance '%s' created successfully.\n", instanceName)
	fmt.Printf("📝 Configuration file: %s\n", filepath.Join(InstancesDir, instanceName, "instance_config.ini"))

	return nil
}

func actionManage(ctx context.Context, cmd *cli.Command) error {
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

func actionStart(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() < 1 {
		return fmt.Errorf("instance name required")
	}

	instanceName := args.Get(0)
	return StartServer(instanceName)
}

func actionStop(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() < 1 {
		return fmt.Errorf("instance name required")
	}

	instanceName := args.Get(0)
	return StopServer(instanceName)
}

func actionRestart(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() < 1 {
		return fmt.Errorf("instance name required")
	}

	instanceName := args.Get(0)
	return RestartServer(instanceName)
}

func actionStatus(ctx context.Context, cmd *cli.Command) error {
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

func actionRCON(ctx context.Context, cmd *cli.Command) error {
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

func actionDelete(ctx context.Context, cmd *cli.Command) error {
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

func actionRename(ctx context.Context, cmd *cli.Command) error {
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

func actionBackup(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() < 2 {
		return fmt.Errorf("instance name and world folder required")
	}

	instanceName := args.Get(0)
	worldFolder := args.Get(1)

	return BackupInstanceWorld(instanceName, worldFolder)
}

func actionRestore(ctx context.Context, cmd *cli.Command) error {
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

func actionStartAll(ctx context.Context, cmd *cli.Command) error {
	return StartAllInstances()
}

func actionStopAll(ctx context.Context, cmd *cli.Command) error {
	return StopAllInstances()
}

func actionConfigRestart(ctx context.Context, cmd *cli.Command) error {
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
		fmt.Println("  8) Edit Configuration")
		fmt.Println("  9) Change Instance Name")
		fmt.Println("  0) Back to Main Menu")

		fmt.Print("Select an option: ")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			break
		}

		choice := strings.TrimSpace(scanner.Text())
		switch choice {
		case "1":
			StartServer(instanceName)
		case "2":
			StopServer(instanceName)
		case "3":
			RestartServer(instanceName)
		case "4":
			running, _ := IsServerRunning(instanceName)
			if running {
				fmt.Printf("✅ Server for instance %s is running.\n", instanceName)
			} else {
				fmt.Printf("❌ Server for instance %s is not running.\n", instanceName)
			}
		case "5":
			fmt.Print("Enter RCON command: ")
			if scanner.Scan() {
				command := strings.TrimSpace(scanner.Text())
				actionRCONImpl(instanceName, command)
			}
		case "6":
			fmt.Print("Enter world folder name: ")
			if scanner.Scan() {
				worldFolder := strings.TrimSpace(scanner.Text())
				BackupInstanceWorld(instanceName, worldFolder)
			}
		case "7":
			// Simulate restore action with local backup selection
			backups, _ := GetAvailableBackups()
			if len(backups) > 0 {
				fmt.Println("📋 Available backups:")
				for i, backup := range backups {
					fmt.Printf("  %d) %s\n", i+1, filepath.Base(backup))
				}
				fmt.Print("Select a backup (number): ")
				if scanner.Scan() {
					choice, _ := strconv.Atoi(strings.TrimSpace(scanner.Text()))
					if choice > 0 && choice <= len(backups) {
						RestoreBackupToInstance(instanceName, backups[choice-1])
					}
				}
			}
		case "8":
			editInstanceConfigFile(instanceName)
		case "9":
			fmt.Print("Enter the new name for the instance: ")
			if scanner.Scan() {
				newName := strings.TrimSpace(scanner.Text())
				if newName != "" {
					// Perform rename
					if running, _ := IsServerRunning(instanceName); running {
						StopServer(instanceName)
					}
					oldPath := filepath.Join(InstancesDir, instanceName)
					newPath := filepath.Join(InstancesDir, newName)
					if err := os.Rename(oldPath, newPath); err == nil {
						config, _ := LoadInstanceConfig(newName)
						config.SaveDir = newName
						SaveInstanceConfig(newName, config)
						fmt.Printf("✅ Instance renamed to '%s'.\n", newName)
						return nil
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

func editInstanceConfigFile(instanceName string) error {
	configPath := filepath.Join(InstancesDir, instanceName, "instance_config.ini")

	fmt.Printf("📝 Edit configuration: %s\n", configPath)
	fmt.Println("Opening config file... (Note: This requires a text editor)")
	fmt.Printf("Path: %s\n", configPath)

	return nil
}
