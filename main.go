package main

import (
	"asa-server/internal/actions"
	"asa-server/internal/appconfig"
	"asa-server/internal/certmgr"
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/runner"
	"asa-server/internal/svcmgr"
	"asa-server/internal/webapi"
	"asa-server/pkg/download"
	"asa-server/pkg/logger"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kardianos/service"
	"github.com/urfave/cli/v3"
)

// isRunningAsService checks if running as an OS-managed service (Windows SCM
// or systemd/sysvinit on Linux — kardianos's Interactive() already abstracts
// the platform difference).
func isRunningAsService() (bool, error) {
	isInteractive := service.Interactive()
	// If running as a service, Interactive() returns false
	// If not running as a service (interactive mode), Interactive() returns true
	return !isInteractive, nil
}

func main() {
	// Check if running as an OS service and run service
	isService, err := isRunningAsService()

	if err != nil {
		log.Fatal(err)
	}

	// 应用配置必须在构建 CLI、建目录、初始化日志之前加载：BaseDir 本身现在就是从
	// config.yaml 解析出来的（见 docs/LINUX_COMPATIBILITY_PLAN.md §10.3/§10.5 G1），
	// 顺序倒过来会用错误的目录建目录/写日志。下面每个 flag 的 Value 也直接取自配置，
	// 于是「命令行 > 配置文件 > 默认值」的优先级由 cli 库天然保证，不需要在 Action
	// 里再判断 IsSet 然后手工合并。
	appCfg := loadAppConfig()

	if err := cfgpkg.EnsureDirectories(cfgpkg.BaseDir); err != nil {
		log.Fatal(err)
	}

	logger.InitLoggerWithBaseDir(cfgpkg.BaseDir)

	applyAppConfig(appCfg)

	app := &cli.Command{
		Name:    "asa-manager",
		Usage:   "ARK Server Ascended Instance Management Tool",
		Version: "1.0.0",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:        "api-port",
				Aliases:     []string{"port"},
				Usage:       "http server port",
				DefaultText: strconv.Itoa(appCfg.Server.Port),
				Value:       appCfg.Server.Port,
				Destination: &webapi.ApiServerPort,
			},
			&cli.BoolFlag{
				Name:        "tls",
				Usage:       "Serve over HTTPS (required for browsers to negotiate HTTP/2)",
				Value:       appCfg.Server.TLS.Enabled,
				Destination: &webapi.EnableTLS,
			},
			&cli.BoolFlag{
				Name:        "tls-trust",
				Usage:       "Install the local CA into the system trust store (Windows Root store / Linux ca-certificates|ca-trust)",
				Value:       appCfg.Server.TLS.TrustLocalCA,
				Destination: &webapi.TrustLocalCA,
			},
			&cli.StringFlag{
				Name:        "cert-file",
				Usage:       "Use an existing certificate instead of the local CA (requires --key-file)",
				Value:       appCfg.Server.TLS.CertFile,
				Destination: &webapi.TLSCertFile,
			},
			&cli.StringFlag{
				Name:        "key-file",
				Usage:       "Private key matching --cert-file",
				Value:       appCfg.Server.TLS.KeyFile,
				Destination: &webapi.TLSKeyFile,
			},
			&cli.StringFlag{
				Name:        "tls-domains",
				Usage:       "Extra domains to include in the certificate SAN, comma separated",
				Value:       strings.Join(appCfg.Server.TLS.Domains, ","),
				Destination: &webapi.TLSDomains,
			},
			&cli.StringFlag{
				Name:        "trusted-proxies",
				Usage:       "Proxies allowed to set X-Forwarded-For, comma separated (empty = trust none)",
				Value:       strings.Join(appCfg.Server.TrustedProxies, ","),
				Destination: &webapi.TrustedProxies,
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
				Name:   "api",
				Usage:  "Start HTTP API server",
				Action: webapi.ActionAPI,
			},

			{
				Name:  "service",
				Usage: "Manage the OS service (Windows service / systemd on Linux)",
				Commands: []*cli.Command{
					{
						Name:   "install",
						Usage:  "Install as an OS service",
						Action: svcmgr.ActionServiceInstall,
					},
					{
						Name:   "remove",
						Usage:  "Remove the OS service",
						Action: svcmgr.ActionServiceRemove,
					},
					{
						Name:   "start",
						Usage:  "Start the OS service",
						Action: svcmgr.ActionServiceStart,
					},
					{
						Name:   "stop",
						Usage:  "Stop the OS service",
						Action: svcmgr.ActionServiceStop,
					},
				},
			},
			{
				Name:   "gui",
				Usage:  "Start GUI mode",
				Action: actionGUI,
			},
			actions.SetupCommand(),
			certmgr.Command(),
			actions.AuthDBCommand(),
			actions.AuthUserCommand(),
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

	// Check if running as an OS service and run service
	if isService {
		svcmgr.RunService()
		return
	}

	// No arguments: Windows defaults to GUI, Linux (no GUI, see
	// docs/LINUX_COMPATIBILITY_PLAN.md §5.9) defaults to the API server.
	if len(os.Args) == 1 {
		if err := runDefaultAction(context.Background()); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}

// loadAppConfig 读取 {BaseDir}/config.yaml。
//
// 加载失败不阻断启动：记 ERROR 后用默认配置继续。默认配置里 auth.enabled 是 false，
// 所以配置写坏的最坏后果是"没有鉴权"，而不是"所有人都登不进来"——
// 对一个本机管理面板来说，后者才是真正的灾难。
func loadAppConfig() *appconfig.Config {
	// Load 不接收任何目录参数——查找规则（ASA_CFG > exe 同级 > 系统固定目录）与
	// BaseDir 取值优先级（basedir 字段 > ASA_BASEDIR > config.yaml 所在目录）全部
	// 内置在它自己的算法里，见 docs/APPCONFIG_BASEDIR_PLAN.md。
	baseDir, err := appconfig.Load()
	// 即使加载出错，appconfig.Load 也总会给出一个可用的兜底 BaseDir，后面建目录/
	// 写日志可以放心使用。
	cfgpkg.BaseDir = baseDir
	if err == nil {
		return appconfig.Get()
	}

	// 配置里明确写了要开鉴权，却又有错 —— 这时候绝不能"用默认值继续跑"：
	// 默认值 auth.enabled 是 false，一个拼写错误就会让服务静默地不带鉴权启动。
	// 配置错误应该表现为"起不来"，不该表现为"安全防护悄悄消失了"。
	if errors.Is(err, appconfig.ErrAuthConfigInvalid) {
		logger.Errorf("%v", err)
		log.Fatalf("配置有误且已启用鉴权，服务不会以无鉴权状态启动。\n"+
			"请修正 %s 后重试。\n%v", filepath.Join(cfgpkg.BaseDir, appconfig.ConfigFileName), err)
	}

	msg := fmt.Sprintf("加载 %s 失败，将使用默认配置继续启动: %v", appconfig.ConfigFileName, err)
	logger.Error(msg)
	return appconfig.Get()
}

// applyAppConfig 把配置写进 webapi 的包级变量。
//
// 看起来和下面 flag 的 Value 重复，其实不是：作为 Windows 服务运行时
// app.Run() 根本不会执行（RunService 在那之前就 return 了），flag 的 Destination
// 永远不会被写入。而「装成 Windows 服务」正是本项目的主要部署方式——
// 少了这一步，config.yaml 在最常见的场景下会被静默忽略。
//
// 交互式运行时这里的赋值随后会被 flag 解析覆盖成同样的值（未显式传参）
// 或命令行指定的值（显式传参），两条路径结果都正确。
func applyAppConfig(cfg *appconfig.Config) {
	webapi.ApiServerPort = cfg.Server.Port
	webapi.EnableTLS = cfg.Server.TLS.Enabled
	webapi.TrustLocalCA = cfg.Server.TLS.TrustLocalCA
	webapi.TLSCertFile = cfg.Server.TLS.CertFile
	webapi.TLSKeyFile = cfg.Server.TLS.KeyFile
	webapi.TLSDomains = strings.Join(cfg.Server.TLS.Domains, ",")
	webapi.TrustedProxies = strings.Join(cfg.Server.TrustedProxies, ",")

	download.Configure(download.Config{
		GithubProxy: cfg.Download.GithubProxy,
		HTTPProxy:   cfg.Download.HTTPProxy,
		Timeout:     cfg.Download.Timeout,
		Retries:     cfg.Download.Retries,
	})

	runner.Configure(runner.Config{
		Runtime:       cfg.Linux.Runtime,
		UmuVersion:    cfg.Linux.UmuVersion,
		ProtonVersion: cfg.Linux.ProtonVersion,
		PrefixMode:    cfg.Linux.PrefixMode,
		PrefixDir:     cfg.Linux.PrefixDir,
		AutoDownload:  cfg.Linux.AutoDownload,
		GameID:        cfg.Linux.GameID,
		BaseDir:       cfgpkg.BaseDir,
	})
}
