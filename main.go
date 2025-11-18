package main

import (
	"asa-server/asaserver"
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
		fmt.Printf("❌ 错误：此工具仅支持 Windows 系统运行。\n")
		fmt.Printf("   当前系统：%s\n", runtime.GOOS)
		os.Exit(1)
	}

	if err := asaserver.EnsureDirectories(); err != nil {
		log.Fatal(err)
	}

	// Initialize log mapping from persistent storage
	if err := asaserver.InitializeLogMapping(); err != nil {
		log.Fatal(err)
	}

	app := &cli.Command{
		Name:    "asa-manager",
		Usage:   "ARK Server Ascended Instance Management Tool",
		Version: "1.0.0",
		Commands: []*cli.Command{
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
				Name:      "manage",
				Usage:     "Manage instance interactively",
				ArgsUsage: "[instance_name]",
				Action:    asaserver.ActionManage,
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
				ArgsUsage: "<instance_name>",
				Action:    asaserver.ActionRestore,
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
				Name:   "config-restart",
				Usage:  "Configure restart manager",
				Action: asaserver.ActionConfigRestart,
			},
			{
				Name:  "api",
				Usage: "Start HTTP API server",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "port",
						Value: 8080,
						Usage: "HTTP server port",
					},
				},
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
		winservice.RunService(false)
		return
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
