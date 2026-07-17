package main

import (
	"asa-server/actions"
	"asa-server/asaserver"
	cfgpkg "asa-server/config"
	"asa-server/gui"
	"asa-server/logger"
	"asa-server/pkg/winproc"
	"asa-server/webapi"
	"asa-server/winservice"
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"slices"
	"strings"

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

	// Check if running as Windows service and run service
	isService, err := isWindowsService()

	if err != nil {
		log.Fatal(err)
	}

	if err := cfgpkg.EnsureDirectories(); err != nil {
		log.Fatal(err)
	}

	logger.InitLoggerWithBaseDir(cfgpkg.BaseDir)

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
				Name:  "update",
				Usage: "Install or update the base server",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "force-server",
						Usage: "Force re-run server verification even if config exists",
					},
				},
				Action: actions.ActionUpdate,
			},
			{
				Name:  "api",
				Usage: "Start HTTP API server",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "no-admin",
						Usage: "Skip administrator elevation, run as current user (more disk usage)",
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
			{
				Name:   "gui",
				Usage:  "Start GUI mode",
				Action: actionGUI,
			},
			{
				Name:  "state",
				Usage: "State database management",
				Commands: []*cli.Command{
					{
						Name:   "clear",
						Usage:  "Clear all state history data (required after key format change)",
						Action: actions.ActionStateClear,
					},
				},
			},
		},
	}

	// Check if running as Windows service and run service
	if isService {
		logger.SetLogMode(logger.ServicesMode)
		winservice.RunService()
		return
	}

	// 如果是 api 子命令且未指定 --no-admin，静默提权
	if isApiCommand(os.Args) && !hasArgFlag("--no-admin") {
		ensureAdminElevation()
	}

	// If no arguments provided, start GUI mode
	if len(os.Args) == 1 {
		actionGUI(context.Background(), nil)
		return
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}

// actionGUI starts the GUI application
func actionGUI(ctx context.Context, cmd *cli.Command) error {
	guiApp := gui.NewGUIApp()
	guiApp.Run()
	return nil
}

// isApiCommand 检查 os.Args 是否包含 api 子命令
func isApiCommand(args []string) bool {
	for _, arg := range args[1:] { // 跳过程序名
		// 跳过标志（以 - 开头）
		if strings.HasPrefix(arg, "-") {
			continue
		}
		// 第一个非标志参数即为子命令
		return arg == "api"
	}
	return false
}

// ensureAdminElevation 静默检测管理员权限并提权
func ensureAdminElevation() {
	if asaserver.IsElevated() {
		return // 已是管理员，无需提权
	}

	// 构建参数并提权重启
	argStr := buildElevatedArgs()

	if err := winproc.RunAsAdmin(argStr); err != nil {
		logger.GetStdout().Warnf("管理员提权失败: %v", err)
		logger.GetStdout().Warnf("[警告] 将以非管理员模式继续运行，镜像启动将使用文件复制模式，占用更多磁盘空间")
		os.Exit(1)
	}

	// 提权成功，退出当前低权限进程
	os.Exit(0)
}

// quoteArg 对包含空格或特殊字符的参数加引号
func quoteArg(arg string) string {
	if strings.ContainsAny(arg, " \t\"") {
		return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
	}
	return arg
}

// hasArgFlag 扫描 os.Args[1:] 检查是否包含指定标志
// 用于在 app.Run() 之前检测 CLI 标志（因此时 CLI 尚未解析）
func hasArgFlag(flag string) bool {
	return slices.Contains(os.Args[1:], flag)
}

// buildElevatedArgs 构建提权重启的参数串
// 格式: "api [其他原始参数]"（跳过 api 本身避免重复）
func buildElevatedArgs() string {
	var otherArgs []string
	for _, arg := range os.Args[1:] {
		if arg == "api" {
			continue // 跳过子命令，后面手动加回
		}
		otherArgs = append(otherArgs, quoteArg(arg))
	}
	return "api " + strings.Join(otherArgs, " ")
}
