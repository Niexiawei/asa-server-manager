package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"

	"github.com/urfave/cli/v3"
)

func main() {
	// 检查操作系统，仅允许 Windows
	if runtime.GOOS != "windows" {
		fmt.Printf("❌ 错误：此工具仅支持 Windows 系统运行。\n")
		fmt.Printf("   当前系统：%s\n", runtime.GOOS)
		os.Exit(1)
	}

	if err := ensureDirectories(); err != nil {
		log.Fatal(err)
	}

	// Initialize log mapping from persistent storage
	if err := InitializeLogMapping(); err != nil {
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
				Action: actionUpdate,
			},
			{
				Name:   "list",
				Usage:  "List all available instances",
				Action: actionList,
			},
			{
				Name:   "create",
				Usage:  "Create a new instance",
				Action: actionCreate,
			},
			{
				Name:      "manage",
				Usage:     "Manage instance interactively",
				ArgsUsage: "[instance_name]",
				Action:    actionManage,
			},
			{
				Name:      "start",
				Usage:     "Start a server instance",
				ArgsUsage: "<instance_name>",
				Action:    actionStart,
			},
			{
				Name:      "stop",
				Usage:     "Stop a server instance",
				ArgsUsage: "<instance_name>",
				Action:    actionStop,
			},
			{
				Name:      "restart",
				Usage:     "Restart a server instance",
				ArgsUsage: "<instance_name>",
				Action:    actionRestart,
			},
			{
				Name:      "status",
				Usage:     "Check server status",
				ArgsUsage: "[instance_name]",
				Action:    actionStatus,
			},
			{
				Name:      "rcon",
				Usage:     "Send RCON command to server",
				ArgsUsage: "<instance_name> <command>",
				Action:    actionRCON,
			},
			{
				Name:      "delete",
				Usage:     "Delete an instance",
				ArgsUsage: "<instance_name>",
				Action:    actionDelete,
			},
			{
				Name:      "rename",
				Usage:     "Rename an instance",
				ArgsUsage: "<instance_name>",
				Action:    actionRename,
			},
			{
				Name:      "backup",
				Usage:     "Create a backup of an instance world",
				ArgsUsage: "<instance_name> <world_folder>",
				Action:    actionBackup,
			},
			{
				Name:      "restore",
				Usage:     "Restore a backup to an instance",
				ArgsUsage: "<instance_name>",
				Action:    actionRestore,
			},
			{
				Name:   "start-all",
				Usage:  "Start all instances",
				Action: actionStartAll,
			},
			{
				Name:   "stop-all",
				Usage:  "Stop all instances",
				Action: actionStopAll,
			},
			{
				Name:   "config-restart",
				Usage:  "Configure restart manager",
				Action: actionConfigRestart,
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
				Action: actionAPI,
			},
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
