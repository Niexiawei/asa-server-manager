package actions

import (
	"asa-server/asaserver"
	"asa-server/backup"
	"asa-server/logger"
	"asa-server/tui"
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/urfave/cli/v3"
)

// Action functions for commands

func ActionUpdate(ctx context.Context, cmd *cli.Command) error {
	logger.GetLogger().Info("Installing/updating base server...")

	stdoutFmt := os.Stdout
	// Download and extract SteamCMD
	if err := asaserver.DownloadAndExtractSteamCmd(stdoutFmt); err != nil {
		return err
	}

	// Download and update ARK server
	if err := asaserver.DownloadAndUpdateArkServer(stdoutFmt); err != nil {
		return err
	}

	// Get force-server flag
	forceServer := cmd.Bool("force-server")

	// Verify server installation by running it to generate config files
	if err := asaserver.VerifyServerInstallation(forceServer); err != nil {
		return err
	}

	logger.GetLogger().Info("Base server installation/update completed.")
	return nil
}

func ActionList(ctx context.Context, cmd *cli.Command) error {
	instances, err := asaserver.GetAvailableInstances()
	if err != nil {
		return err
	}

	if len(instances) == 0 {
		logger.GetLogger().Warnf("No instances found in '%s'.", asaserver.InstancesDir)
		return nil
	}

	logger.GetLogger().Info("Available instances:")
	for _, inst := range instances {
		running, _ := asaserver.IsServerRunning(inst)
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
	if _, err := os.Stat(filepath.Join(asaserver.InstancesDir, instanceName)); err == nil {
		logger.GetLogger().Warnf("Instance '%s' already exists.", instanceName)
		return nil
	}

	// Create instance directory
	if err := os.MkdirAll(filepath.Join(asaserver.InstancesDir, instanceName, "Config"), 0755); err != nil {
		return fmt.Errorf("failed to create instance directory: %w", err)
	}

	// Copy base server configuration files to instance Config directory
	baseConfigDir := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer")
	instanceConfigDir := filepath.Join(asaserver.InstancesDir, instanceName, "Config")
	if _, err := os.Stat(baseConfigDir); err == nil {
		logger.GetLogger().Infof("Copying base server configuration files to instance '%s'...", instanceName)
		if err := asaserver.CopyDir(baseConfigDir, instanceConfigDir); err != nil {
			logger.GetLogger().Warnf("Failed to copy base server configuration: %v", err)
			// Continue anyway as this is not critical
		}
	} else {
		logger.GetLogger().Warnf("Base server configuration directory not found at %s", baseConfigDir)
	}

	// Create default configuration
	config := asaserver.CreateDefaultInstanceConfig(instanceName)
	if err := asaserver.SaveInstanceConfig(instanceName, config); err != nil {
		return err
	}

	// Create empty Game.ini if not already copied from base server
	gameIniPath := filepath.Join(asaserver.InstancesDir, instanceName, "Config", "Game.ini")
	if _, err := os.Stat(gameIniPath); os.IsNotExist(err) {
		if err := os.WriteFile(gameIniPath, []byte(""), 0644); err != nil {
			return fmt.Errorf("failed to create Game.ini: %w", err)
		}
	}

	logger.GetLogger().Infof("Instance '%s' created successfully.", instanceName)
	logger.GetLogger().Infof("Configuration file: %s", filepath.Join(asaserver.InstancesDir, instanceName, "instance_config.ini"))

	return nil
}

func ActionStart(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() < 1 {
		return fmt.Errorf("instance name required")
	}

	instanceName := args.Get(0)
	return asaserver.StartServer(instanceName)
}

func ActionStop(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() < 1 {
		return fmt.Errorf("instance name required")
	}

	instanceName := args.Get(0)
	return asaserver.StopServer(instanceName)
}

func ActionRestart(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() < 1 {
		return fmt.Errorf("instance name required")
	}

	instanceName := args.Get(0)
	return asaserver.RestartServer(instanceName)
}

func ActionStatus(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() == 0 {
		// Show status of all instances
		instances, err := asaserver.GetAvailableInstances()
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
			running, err := asaserver.IsServerRunning(instanceName)
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
	running, err := asaserver.IsServerRunning(instanceName)
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
	response, err := asaserver.SendRCONCommand(instanceName, command)
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
	if running, _ := asaserver.IsServerRunning(instanceName); running {
		logger.GetLogger().Infof("Stopping instance '%s'...", instanceName)
		if err := asaserver.StopServer(instanceName); err != nil {
			logger.GetLogger().Errorf("Error stopping server: %v", err)
		}
	}

	// Delete instance directory
	instanceDir := filepath.Join(asaserver.InstancesDir, instanceName)
	if err := os.RemoveAll(instanceDir); err != nil {
		return fmt.Errorf("failed to delete instance directory: %w", err)
	}

	// Delete save directories
	savePath := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved", instanceName)
	os.RemoveAll(savePath)

	savedArksPath := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved/SavedArks", instanceName)
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
	if running, _ := asaserver.IsServerRunning(instanceName); running {
		logger.GetLogger().Infof("Stopping instance '%s' before renaming...", instanceName)
		if err := asaserver.StopServer(instanceName); err != nil {
			return fmt.Errorf("failed to stop server: %w", err)
		}
	}

	// Rename instance directory
	oldPath := filepath.Join(asaserver.InstancesDir, instanceName)
	newPath := filepath.Join(asaserver.InstancesDir, newName)

	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("failed to rename instance directory: %w", err)
	}

	// Rename save directories
	oldSavePath := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved", instanceName)
	newSavePath := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved", newName)
	os.Rename(oldSavePath, newSavePath)

	oldArksPath := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved/SavedArks", instanceName)
	newArksPath := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved/SavedArks", newName)
	os.Rename(oldArksPath, newArksPath)

	// Update SaveDir in configuration
	config, err := asaserver.LoadInstanceConfig(newName)
	if err == nil {
		config.SaveDir = newName
		asaserver.SaveInstanceConfig(newName, config)
	}

	logger.GetLogger().Infof("Instance renamed from '%s' to '%s'.", instanceName, newName)
	return nil
}

func ActionBackup(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() < 1 {
		return fmt.Errorf("instance name required")
	}

	instanceName := args.Get(0)

	return backup.BackupInstanceWorld(instanceName)
}

func ActionRestore(ctx context.Context, cmd *cli.Command) error {
	// ... existing code ...
	args := cmd.Args()
	if args.Len() < 1 {
		return fmt.Errorf("instance name required")
	}
	instanceName := args.Get(0)

	if args.Len() < 2 {
		return fmt.Errorf("backup file path required")
	}
	backupFile := args.Get(1)

	// Check if backup file exists
	if _, err := os.Stat(backupFile); err != nil {
		return fmt.Errorf("backup file not found: %s", backupFile)
	}

	// Get restore options from flags
	restoreWorldfile := cmd.Bool("worldfile")
	restoreInstanceConfig := cmd.Bool("instance-config")
	restoreGameConfig := cmd.Bool("game-config")

	// If no options specified, restore all
	if !restoreWorldfile && !restoreInstanceConfig && !restoreGameConfig {
		restoreWorldfile = true
		restoreInstanceConfig = true
		restoreGameConfig = true
	}

	// Build restore options description
	components := []string{}
	// ... existing code ...
	if restoreWorldfile {
		components = append(components, "worldfile (世界文件/SaveDir)")
	}
	if restoreInstanceConfig {
		components = append(components, "instance_config.ini (实例配置)")
	}
	if restoreGameConfig {
		components = append(components, "Config (游戏配置)")
	}

	// Display confirmation message
	logger.GetLogger().Warnf("确认要从备份 \"%s\" 恢复到实例 \"%s\" 吗？", filepath.Base(backupFile), instanceName)
	logger.GetLogger().Info("\n将恢复的内容：")
	for i, comp := range components {
		logger.GetLogger().Infof("  %d. %s", i+1, comp)
	}
	logger.GetLogger().Warn("\n此操作不可撤销，请谨慎操作！")
	fmt.Print("\n请输入 'yes' 确认恢复或 'no' 取消: ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("failed to read input")
	}

	confirm := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if confirm != "yes" {
		logger.GetLogger().Info("恢复已取消")
		return nil
	}

	// Build restore option functions
	var optFuncs []backup.RestoreOptionFunc
	if restoreWorldfile {
		optFuncs = append(optFuncs, backup.WithRestoreWorldfile())
	}
	if restoreInstanceConfig {
		optFuncs = append(optFuncs, backup.WithRestoreInstanceConfig())
	}
	if restoreGameConfig {
		optFuncs = append(optFuncs, backup.WithRestoreGameConfig())
	}

	return backup.RestoreBackupToInstance(instanceName, backupFile, optFuncs...)
}

func ActionStartAll(ctx context.Context, cmd *cli.Command) error {
	return asaserver.StartAllInstances()
}

func ActionStopAll(ctx context.Context, cmd *cli.Command) error {
	return asaserver.StopAllInstances()
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

	content, err := asaserver.GetGameIniContent(instanceName)
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

	content, err := asaserver.GetGameUserSettingsContent(instanceName)
	if err != nil {
		return err
	}

	logger.GetLogger().Infof("GameUserSettings.ini for instance '%s':", instanceName)
	logger.GetLogger().Info(content)

	return nil
}

func ActionSyncGameConfig(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	var instanceNames []string

	// Get instance names from arguments
	if args.Len() > 0 {
		for i := 0; i < args.Len(); i++ {
			instanceName := args.Get(i)
			if instanceName != "" {
				instanceNames = append(instanceNames, instanceName)
			}
		}
	}

	// If no instances provided, show error
	if len(instanceNames) == 0 {
		return fmt.Errorf("at least one instance name required")
	}

	// Sync config for each instance
	var failedInstances []string
	for _, instanceName := range instanceNames {
		logger.GetLogger().Infof("Syncing game configuration for instance '%s'...", instanceName)
		if err := asaserver.SyncGameConfigToInstance(instanceName); err != nil {
			logger.GetLogger().Errorf("Failed to sync config for instance '%s': %v", instanceName, err)
			failedInstances = append(failedInstances, instanceName)
			continue
		}
		logger.GetLogger().Infof("Successfully synced game configuration for instance '%s'", instanceName)
	}

	// Report results
	if len(failedInstances) > 0 {
		return fmt.Errorf("failed to sync configuration for instances: %v", failedInstances)
	}

	logger.GetLogger().Infof("All %d instances synced successfully", len(instanceNames))
	return nil
}

func ActionManage(ctx context.Context, cmd *cli.Command) error {
	// 使用 bubbletea TUI 替代旧的交互方式
	model := tui.NewModel()
	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// 注意: selectInstance, manageInstanceMenu, viewInstanceLogs, editInstanceConfigFile 函数已迁移到 tui/models.go
// 保留此处作为向后兼容（如果其他地方还在使用）

// Helper functions
func selectInstance() string {
	instances, err := asaserver.GetAvailableInstances()
	if err != nil {
		fmt.Printf("Error getting instances: %v \n", err)
		return ""
	}

	if len(instances) == 0 {
		fmt.Println("No instances found.")
		return ""
	}

	fmt.Println("Available instances:")
	for i, inst := range instances {
		fmt.Printf("  %d) %s \n", i+1, inst)
	}

	fmt.Print("Select an instance (number): ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return ""
	}

	choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if err != nil || choice < 1 || choice > len(instances) {
		fmt.Println("Invalid selection.")
		return ""
	}
	return instances[choice-1]
}

// 此函数已迁移到 tui/models.go
// 保留向后兼容的实现
func manageInstanceMenu(instanceName string) error {
	logger.GetLogger().Warn("manageInstanceMenu 已弃用，请使用 TUI 模式")
	return nil
}

func viewInstanceLogs(instanceName string) error {
	// Get the log file path for the instance
	logPath, exists := asaserver.GetInstanceLogFile(instanceName)
	if !exists {
		// Try to get the log path if not in mapping
		var err error
		logPath, err = asaserver.GetGameLogFilePath(instanceName)
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
	stopMonitoring := asaserver.TailLogFile(logPath, func(line string) {
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
	configPath := filepath.Join(asaserver.InstancesDir, instanceName, "instance_config.ini")

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
