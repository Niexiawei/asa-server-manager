package actions

import (
	"asa-server/internal/appconfig"
	"asa-server/internal/bootstrap"
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/runner"
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
				Name: "basedir",
				Usage: "数据目录（仅在还没有 config.yaml 时使用）。交互模式下留空会提示输入；非交互模式下必须提供，" +
					"或者先用 asa-server config init 生成并编辑配置",
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

	baseDir, created, err := resolveSetupBaseDir(flagBaseDir, nonInteractive, stdPrompter(), stdinIsTerminal())
	if err != nil {
		return err
	}

	// 刚生成的配置用户可能在停顿处改过（umu_python_bin、prefix 模式……），自检用的是
	// 启动时的默认值——再跑一次，避免「自检用默认值通过、安装用新值失败」。
	if created && runtime.GOOS == "linux" {
		fmt.Println("配置已更新，重新执行宿主依赖自检：")
		if err := runLinuxPreflight(ignorePreflight); err != nil {
			return err
		}
	}

	// 数据目录、日志与运行时配置此时都已就位：沿用已有配置时由 main.go 启动时应用，
	// 新选数据目录时由 resolveSetupBaseDir 里的 bootstrap.Reload 应用——两条路径都经
	// bootstrap.Apply，runner.Config 字段给齐由那一处保证（整体覆盖，漏一项就是清空，
	// 见 docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md §11）。

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

// resolveSetupBaseDir 决定这次引导用哪个 BaseDir，created 报告这次是否新生成了配置。
//
//   - 已有 config.yaml（`config init` 过、或旧部署）：沿用 main.go 启动时解析出的
//     BaseDir，不重新问、不重新写文件，见 docs/LINUX_COMPATIBILITY_PLAN.md §10.4
//     「任一级已有 config.yaml 就维持现状」。
//   - 没有：先生成配置（与 `config init` 同一段逻辑），**停下来让用户改**，改好回车后
//     bootstrap.Reload 重新加载并重新应用（含下载代理），然后才开始几百 MB / 几十 GB
//     的下载。这是 Part 2 的核心诉求：影响下载与安装本身的配置，要在下载之前就能改。
//     见 docs/SETUP_FLOW_OPTIMIZATION_PLAN.md Part 2 §P2-3.4。
//
// tty 决定要不要问语言、要不要停顿等回车：从管道喂数据目录（`echo /data | asa-server setup`）
// 的老用法里没人会按回车，第一行也不能被语言问题吃掉。
func resolveSetupBaseDir(flagBaseDir string, nonInteractive bool, p *prompter, tty bool) (string, bool, error) {
	if !appconfig.ConfigMissing() {
		fmt.Fprintf(p.out, "检测到已有配置 %s，沿用当前数据目录: %s\n", appconfig.ConfigPath(), cfgpkg.BaseDir)
		return cfgpkg.BaseDir, false, nil
	}

	if nonInteractive && flagBaseDir == "" {
		return "", false, fmt.Errorf("还没有配置文件。非交互模式下请先运行 asa-server config init 并按需编辑，" +
			"或通过 --basedir 指定数据目录")
	}

	fmt.Fprintln(p.out, "还没有配置文件，先生成一份（也可以 Ctrl+C 退出，改用 asa-server config init 单独生成）。")
	res, err := runConfigInit(configInitOptions{
		BaseDir:     flagBaseDir,
		Interactive: !nonInteractive,
		AskLang:     !nonInteractive && tty,
	}, p)
	if err != nil {
		return "", false, err
	}

	pause := !nonInteractive && tty
	printConfigInitSummary(p.out, res, pause)
	if pause {
		fmt.Fprintln(p.out)
		if _, err := p.Line("现在可以用编辑器修改上面这份配置，改好后回到这里按回车继续（直接回车 = 使用默认值）..."); err != nil {
			return "", false, err
		}
	}

	for {
		baseDir, _, rerr := bootstrap.Reload()
		if rerr == nil {
			fmt.Fprintf(p.out, "已应用配置，数据目录: %s\n", baseDir)
			return baseDir, true, nil
		}
		if !pause {
			return "", false, rerr
		}
		fmt.Fprintf(p.out, "\n%v\n", rerr)
		ans, err := p.Line("请修正后按回车重试（输入 q 放弃）: ")
		if err != nil {
			return "", false, err
		}
		if strings.EqualFold(ans, "q") {
			return "", false, fmt.Errorf("已放弃；配置文件保留在 %s，修正后重跑 asa-server setup", res.Path)
		}
	}
}
