package actions

import (
	"asa-server/asaserver"
	"asa-server/backup"
	"asa-server/logger"
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
func ActionBackup(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() < 1 {
		return fmt.Errorf("instance name required")
	}

	instanceName := args.Get(0)

	return backup.BackupInstanceWorld(instanceName)
}

func ActionStartAll(ctx context.Context, cmd *cli.Command) error {
	return asaserver.StartAllInstances()
}

func ActionStopAll(ctx context.Context, cmd *cli.Command) error {
	return asaserver.StopAllInstances()
}
