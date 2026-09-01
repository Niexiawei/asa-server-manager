package actions

import (
	"asa-server/internal/appconfig"
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/runner"
	"asa-server/pkg/logger"
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/urfave/cli/v3"
)

// SetupCommand 是 `asa-server setup`：两平台通用的首次引导入口，交互 + 非交互两种
// 模式，串联 BaseDir 选择 → （Linux）umu/GE-Proton 运行时 → SteamCMD → ARK 本体。
// Windows 上不涉及 Wine/Proton（无 Preflight、无 EnsureRuntime），其余步骤相同；
// 双击运行的 Windows 用户走 GUI 引导面板（internal/gui/setup_progress.go），CLI 这条
// 主要给无头 / 脚本化安装。见 docs/SETUP_FLOW_OPTIMIZATION_PLAN.md §3.2。
//
// 不拆 _windows.go/_linux.go：下面的逻辑本身在两平台都能编译，只有 runtime.GOOS
// 分支圈住的 Preflight/EnsureRuntime 是 Linux 专属，不涉及任何平台专属 API。
func SetupCommand() *cli.Command {
	return &cli.Command{
		Name: "setup",
		Usage: "首次引导：BaseDir → （Linux）umu/GE-Proton 运行时 → SteamCMD → ARK 本体" +
			"（Windows 双击运行时 GUI 里也有同样的引导）",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name: "non-interactive",
				Usage: "非交互模式：不提示任何输入，缺少必要参数直接报错退出" +
					"（配合 --basedir 用于脚本/systemd 场景）",
			},
			&cli.StringFlag{
				Name:  "basedir",
				Usage: "数据目录。交互模式下留空会提示输入；非交互模式下必须提供，除非 config.yaml 已经配置过",
			},
			&cli.BoolFlag{
				Name: "ignore-preflight",
				Usage: "（Linux）宿主依赖自检不通过时仍继续。默认自检不通过会中止 setup，" +
					"因为缺 32 位 glibc / python3 等会让后续下载安装必然失败",
			},
		},
		Action: ActionSetup,
	}
}

func ActionSetup(ctx context.Context, cmd *cli.Command) error {
	nonInteractive := cmd.Bool("non-interactive")
	ignorePreflight := cmd.Bool("ignore-preflight")
	flagBaseDir := strings.TrimSpace(cmd.String("basedir"))

	fmt.Println("=== ASA Server Manager 首次引导 ===")

	if runtime.GOOS == "linux" {
		if err := runLinuxPreflight(ignorePreflight); err != nil {
			return err
		}
	}

	baseDir, err := resolveSetupBaseDir(flagBaseDir, nonInteractive)
	if err != nil {
		return err
	}

	if baseDir != cfgpkg.BaseDir {
		fmt.Printf("正在应用新的数据目录: %s\n", baseDir)
		cfgpkg.BaseDir = baseDir
		if err := cfgpkg.EnsureDirectories(baseDir); err != nil {
			return fmt.Errorf("创建数据目录失败: %w", err)
		}
		logger.InitLoggerWithBaseDir(baseDir)
	}

	// runner.Configure **整体覆盖**（runner.go 的 current.Store），所以这里必须把
	// linux.* 全给齐 —— 漏一项就等于在 setup 中途把它悄悄清空。曾经漏掉的正是
	// display/xvfb_bin/xvfb_screen 与 umu_runtime_*：preflight 跑在这一行之前，
	// 用的是 main.go 灌进去的完整配置，过了自检之后却换成一份残缺的，
	// 同一条命令里前后两种行为。见 docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md §11。
	appCfg := appconfig.Get()
	runner.Configure(runner.Config{
		Runtime:          appCfg.Linux.Runtime,
		UmuVersion:       appCfg.Linux.UmuVersion,
		ProtonVersion:    appCfg.Linux.ProtonVersion,
		PrefixMode:       appCfg.Linux.PrefixMode,
		PrefixDir:        appCfg.Linux.PrefixDir,
		PythonBin:        appCfg.Linux.UmuPythonBin,
		AutoDownload:     appCfg.Linux.AutoDownload,
		SteamRTPrefetch:  appCfg.Linux.SteamRTPrefetch,
		InstallVCRedist:  appCfg.Linux.InstallVCRedist,
		VCRedistURL:      appCfg.Linux.VCRedistURL,
		VCRedistSHA256:   appCfg.Linux.VCRedistSHA256,
		WineDLLOverrides: appCfg.Linux.WineDLLOverrides,
		Display:          appCfg.Linux.Display,
		XvfbBin:          appCfg.Linux.XvfbBin,
		XvfbScreen:       appCfg.Linux.XvfbScreen,
		AllowX11Remount:  appCfg.Linux.AllowX11Remount,
		GameID:           appCfg.Linux.GameID,
		BaseDir:          baseDir,
		RuntimeUser:      appCfg.Linux.UmuRuntimeUser,
		RuntimeUID:       appCfg.Linux.UmuRuntimeUID,
		RuntimeGID:       appCfg.Linux.UmuRuntimeGID,
		RunAsRoot:        appCfg.Linux.UmuRunAsRoot,
		RuntimeDeepProbe: appCfg.Linux.UmuRuntimeDeepProbe,
	})

	if runtime.GOOS == "linux" {
		fmt.Println("正在准备 umu/GE-Proton 运行时（首次运行需要下载，可能需要几分钟）...")
		if err := runner.EnsureRuntime(ctx, os.Stdout); err != nil {
			return fmt.Errorf("准备 Wine/Proton 运行时失败: %w", err)
		}
	}

	fmt.Println("正在下载 / 更新 SteamCMD 与 ARK 服务端本体（体积较大，请耐心等待）...")
	if err := InstallBaseEnvironment(ctx, os.Stdout); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("=== 引导完成 ===")
	fmt.Printf("数据目录: %s\n", baseDir)
	printPostSetupTips()
	return nil
}

// runLinuxPreflight 跑宿主依赖自检。与 `asa-server api` 启动路径（那里只打日志告警、
// 不阻断，见 docs/LINUX_COMPATIBILITY_PLAN.md §4.2）不同：在 setup 语境下用户已明确
// 表达"我要初始化环境"，缺 32 位 glibc（SteamCMD 是 32 位 ELF）或 python3（umu 需要）
// 还继续只会白下载几百 MB，所以默认不通过就中止。--ignore-preflight 是逃生舱，
// 供某些非主流发行版上检查误报时使用。
//
// 只有**阻断项**参与中止判定。建议项（Problem.Warning）照常打印但不拦路 ——
// 它们描述的是"能用但降级"，把它们当成缺依赖会让一台完全可用的机器装不上，
// 见 docs/ACL_PERMISSION_HARDENING_PLAN.md §1。
func runLinuxPreflight(ignore bool) error {
	problems := runner.Preflight()
	blockers, advisories := runner.Blockers(problems), runner.Advisories(problems)

	// 建议项先打印再判定：即使下面因为阻断项中止，用户也已经看到了完整清单。
	if len(blockers) == 0 {
		if len(advisories) == 0 {
			fmt.Println("宿主依赖自检：通过")
		} else {
			fmt.Printf("宿主依赖自检：通过（%d 项建议）\n", len(advisories))
			printProblems(advisories, "建议")
		}
		return nil
	}

	fmt.Println("宿主运行时依赖不满足，setup 无法继续。请按下面的建议手动安装后重试：")
	printProblems(blockers, "修复")
	if len(advisories) > 0 {
		fmt.Printf("另有 %d 项建议（不阻断 setup）：\n", len(advisories))
		printProblems(advisories, "建议")
	}

	if ignore {
		fmt.Println("--ignore-preflight 已指定，忽略上述问题继续。")
		return nil
	}
	return fmt.Errorf("宿主依赖缺失，已中止；补齐后重跑 asa-server setup（或加 --ignore-preflight 强行继续）")
}

func printProblems(problems []runner.Problem, fixLabel string) {
	for _, p := range problems {
		if p.Fix != "" {
			fmt.Printf("  - [%s] %s\n      %s：%s\n", p.Name, p.Detail, fixLabel, p.Fix)
		} else {
			fmt.Printf("  - [%s] %s\n", p.Name, p.Detail)
		}
	}
}

func printPostSetupTips() {
	// 降级到方案 A 时在这里再提一次装 acl。自检阶段那条建议排在几百 MB 下载日志
	// 之前，setup 跑完几分钟后早被刷走了；末尾这一屏才是用户真正会看到的。
	// 见 docs/ACL_PERMISSION_HARDENING_PLAN.md §4.1。
	if info := runner.SharedAccessStatus(); info.Managed && info.Model() == "chown" {
		fmt.Println()
		fmt.Println("⚠ 当前系统没有可用的 POSIX ACL，权限走的是 chown 兜底方案：")
		fmt.Println("  之后以 root 上传的 ArkApi 插件 / mod 文件，游戏进程会写不了，")
		fmt.Println("  需要重启 asa-server 或执行 asa-server perms fix 才能生效。建议：")
		fmt.Println("    apt install acl && systemctl restart asa-server   # Debian/Ubuntu")
		fmt.Println("    （Fedora: dnf install acl；Arch: pacman -S acl）")
		fmt.Println()
	}

	fmt.Println("接下来可以：")
	if runtime.GOOS == "linux" {
		fmt.Println("  asa-server service install    # 安装为 systemd 服务")
		fmt.Println("  asa-server cert install       # 安装本地 HTTPS 证书（需要 sudo）")
		fmt.Println("  asa-server perms status       # 查看共享写权限现状（排查插件/mod 写不了时用）")
	} else {
		fmt.Println("  asa-server service install    # 安装为 Windows 服务（需要管理员）")
		fmt.Println("  或直接双击 asa-server.exe 使用 GUI")
	}
	fmt.Println("  asa-server user add           # 创建管理员账号（如需开启鉴权）")
	fmt.Println("  asa-server api                # 直接前台启动，验证一下也行")
}

// resolveSetupBaseDir 决定这次引导用哪个 BaseDir：
//   - WasConfigAutoGenerated() 为 false：main.go 启动时那次 Load 读到的是已经存在
//     的 config.yaml（可能是重复运行 setup，或者手动配置过），直接沿用当前解析出
//     的 BaseDir，不重新问、不重新写文件——避免覆盖用户已经调好的设置，见
//     docs/LINUX_COMPATIBILITY_PLAN.md §10.4「任一级已有 config.yaml 就维持现状」。
//   - 为 true：真正的全新安装，需要选一个数据目录并写回 basedir 字段。
func resolveSetupBaseDir(flagBaseDir string, nonInteractive bool) (string, error) {
	if !appconfig.WasConfigAutoGenerated() {
		fmt.Printf("检测到已有配置，沿用当前数据目录: %s\n", cfgpkg.BaseDir)
		return cfgpkg.BaseDir, nil
	}

	chosen := flagBaseDir
	if chosen == "" {
		if nonInteractive {
			return "", fmt.Errorf("非交互模式下必须通过 --basedir 指定数据目录")
		}
		var err error
		chosen, err = promptBaseDir()
		if err != nil {
			return "", err
		}
	}

	for {
		if err := appconfig.ValidateBaseDir(chosen); err != nil {
			if nonInteractive {
				return "", err
			}
			fmt.Println(err)
			var promptErr error
			chosen, promptErr = promptBaseDir()
			if promptErr != nil {
				return "", promptErr
			}
			continue
		}
		break
	}

	if err := appconfig.WriteInitialConfig(chosen); err != nil {
		return "", fmt.Errorf("写入 config.yaml 失败: %w", err)
	}
	baseDir, err := appconfig.Load()
	if err != nil {
		return "", fmt.Errorf("重新加载配置失败: %w", err)
	}
	return baseDir, nil
}

func promptBaseDir() (string, error) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("请输入数据目录的绝对路径（ARK 服务端本体 + 存档将保存在这里，建议预留至少 30GB 空间）: ")
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("读取输入失败: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("数据目录不能为空")
	}
	return line, nil
}
