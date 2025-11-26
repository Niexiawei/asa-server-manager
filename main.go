package main

import (
	"asa-server/asaserver"
	"asa-server/logger"
	"asa-server/webapi"
	"asa-server/winservice"
	"context"
	"fmt"
	"log"
	"os"
	"runtime"

	"github.com/kardianos/service"
	"github.com/urfave/cli/v3"
)

// isWindowsService checks if running as a Windows service
func isWindowsService() (bool, error) {
	isInteractive := service.Interactive()
	// If running as a service, Interactive() returns false
	// If not running as a service (interactive mode), Interactive() returns true
	return !isInteractive, nil
}

func main() {
	// 检查操作系统，仅允许 Windows
	if runtime.GOOS != "windows" {
		fmt.Printf("Error: This tool only supports Windows systems.\n")
		fmt.Printf("   Current system: %s\n", runtime.GOOS)
		os.Exit(1)
	}

	if err := asaserver.EnsureDirectories(); err != nil {
		log.Fatal(err)
	}

	logger.InitLoggerWithBaseDir(asaserver.BaseDir)
	// Initialize log mapping from persistent storage
	if err := asaserver.InitializeLogMapping(); err != nil {
		log.Fatal(err)
	}

	app := &cli.Command{
		Name:    "asa-manager",
		Usage:   "ARK Server Ascended Instance Management Tool",
		Version: "1.0.0",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:        "api-port",
				Aliases:     []string{"port"},
				Usage:       "http server port",
				DefaultText: "19193",
				Value:       19193,
				Destination: &webapi.ApiServerPort,
			},
		},
		Commands: []*cli.Command{
			{
				Name:      "manage",
				Usage:     "Manage instance interactively",
				ArgsUsage: "[instance_name]",
				Action:    asaserver.ActionManage,
			},
			{
				Name:  "update",
				Usage: "Install or update the base server",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "force-server",
						Usage: "Force re-run server verification even if config exists",
					},
				},
				Action: asaserver.ActionUpdate,
			},
			{
				Name:   "list",
				Usage:  "List all available instances",
				Action: asaserver.ActionList,
			},
			{
				Name:   "create",
				Usage:  "Create a new instance",
				Action: asaserver.ActionCreate,
			},
			{
				Name:      "start",
				Usage:     "Start a server instance",
				ArgsUsage: "<instance_name>",
				Action:    asaserver.ActionStart,
			},
			{
				Name:      "stop",
				Usage:     "Stop a server instance",
				ArgsUsage: "<instance_name>",
				Action:    asaserver.ActionStop,
			},
			{
				Name:      "restart",
				Usage:     "Restart a server instance",
				ArgsUsage: "<instance_name>",
				Action:    asaserver.ActionRestart,
			},
			{
				Name:      "status",
				Usage:     "Check server status",
				ArgsUsage: "[instance_name]",
				Action:    asaserver.ActionStatus,
			},
			{
				Name:      "rcon",
				Usage:     "Send RCON command to server",
				ArgsUsage: "<instance_name> <command>",
				Action:    asaserver.ActionRCON,
			},
			{
				Name:      "delete",
				Usage:     "Delete an instance",
				ArgsUsage: "<instance_name>",
				Action:    asaserver.ActionDelete,
			},
			{
				Name:      "rename",
				Usage:     "Rename an instance",
				ArgsUsage: "<instance_name>",
				Action:    asaserver.ActionRename,
			},
			{
				Name:      "backup",
				Usage:     "Create a backup of an instance world",
				ArgsUsage: "<instance_name> <world_folder>",
				Action:    asaserver.ActionBackup,
			},
			{
				Name:      "restore",
				Usage:     "Restore a backup to an instance",
				ArgsUsage: "<instance_name> <backup_file>",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "worldfile",
						Usage: "Restore worldfile (SaveDir)",
					},
					&cli.BoolFlag{
						Name:  "instance-config",
						Usage: "Restore instance_config.ini",
					},
					&cli.BoolFlag{
						Name:  "game-config",
						Usage: "Restore game config files (Config directory)",
					},
				},
				Action: asaserver.ActionRestore,
			},
			{
				Name:   "start-all",
				Usage:  "Start all instances",
				Action: asaserver.ActionStartAll,
			},
			{
				Name:   "stop-all",
				Usage:  "Stop all instances",
				Action: asaserver.ActionStopAll,
			},
			{
				Name:      "view-game",
				Usage:     "View Game.ini configuration file for an instance",
				ArgsUsage: "[instance_name]",
				Action:    asaserver.ActionViewGameIni,
			},
			{
				Name:      "view-game-user-settings",
				Usage:     "View GameUserSettings.ini configuration file for an instance",
				ArgsUsage: "[instance_name]",
				Action:    asaserver.ActionViewGameUserSettings,
			},
			{
				Name:      "sync-config",
				Usage:     "Synchronize game config files from base server to instances",
				ArgsUsage: "<instance_name> [instance_name2] [...]",
				Action:    asaserver.ActionSyncGameConfig,
			},
			{
				Name:   "api",
				Usage:  "Start HTTP API server",
				Action: webapi.ActionAPI,
			},
			{
				Name:  "service",
				Usage: "Manage Windows service",
				Commands: []*cli.Command{
					{
						Name:   "install",
						Usage:  "Install as Windows service",
						Action: winservice.ActionServiceInstall,
					},
					{
						Name:   "remove",
						Usage:  "Remove Windows service",
						Action: winservice.ActionServiceRemove,
					},
					{
						Name:   "start",
						Usage:  "Start Windows service",
						Action: winservice.ActionServiceStart,
					},
					{
						Name:   "stop",
						Usage:  "Stop Windows service",
						Action: winservice.ActionServiceStop,
					},
				},
			},
		},
	}

	// Check if running as Windows service and run service
	isService, err := isWindowsService()

	if err != nil {
		log.Fatal(err)
	}

	if isService {
		logger.SetLogMode(logger.ServicesMode)
		winservice.RunService(false)
		return
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
