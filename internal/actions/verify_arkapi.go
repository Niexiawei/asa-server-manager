package actions

import (
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/installer"
	"asa-server/internal/runner"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/urfave/cli/v3"
)

// VerifyArkApiCommand 是 `asa-server verify-arkapi`：ArkApi（AsaApiLoader.exe）这条
// 启动链路的专用诊断，与 `verify` 的关系是「同一条路的加长版」——
// verify 验的是 ArkAscendedServer.exe 能不能被拉起来，这条验的是 AsaApiLoader.exe
// 能不能把它**注入着**拉起来。
//
// 分两段：先把前置条件逐条列出来（ArkApi 装没装、Wine/Proton 运行时、VC++ 运行时
// 与 DLL override 的现状），再真的拉起一次。前一段是纯本地读文件，随时可跑；
// --check-only 就停在这里。
//
// 为什么值得单独一条命令：ArkApi 在 Wine 下能否工作没有任何官方保证
// （docs/LINUX_COMPATIBILITY_PLAN.md §6 风险 11），失败时的现象往往是「服务端起不来，
// 日志戛然而止」。把可检查的前置条件全部摊开，能把「玄学」缩小成「还剩哪一项没验」。
func VerifyArkApiCommand() *cli.Command {
	return &cli.Command{
		Name: "verify-arkapi",
		// 用户手上可能已经按下划线的写法记住了，两种都收。
		Aliases: []string{"verify_arkapi"},
		Usage: "诊断 ArkApi 能否在本机运行：先列出前置条件，再用 AsaApiLoader.exe 真拉起一次" +
			"（Linux 上还会检查 Wine prefix 里的 VC++ 运行时与 DLL override）",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "check-only",
				Usage: "只做静态检查并打印诊断，不真的拉起服务端（不占端口、不动 server-files）",
			},
			&cli.BoolFlag{
				Name: "install-vcredist",
				Usage: "检查前先把缺失的 VC++ 运行时装进 Wine prefix（约 24MB 下载）。" +
					"等价于 setup 里的那一步，但不会连带重跑 SteamCMD",
			},
		},
		Action: ActionVerifyArkApi,
	}
}

func ActionVerifyArkApi(ctx context.Context, cmd *cli.Command) error {
	fmt.Println("=== ArkApi 运行诊断 ===")

	if cmd.Bool("install-vcredist") {
		fmt.Println()
		fmt.Println("正在准备 VC++ 运行时...")
		if err := runner.EnsurePrefixVCRedist(ctx, "", os.Stdout); err != nil {
			// 不直接失败：下面的诊断会把现状原原本本列出来，比这里一句错误有用得多。
			fmt.Printf("VC++ 运行时安装失败: %v\n", err)
		}
	}

	ready := printArkApiPrerequisites()

	if cmd.Bool("check-only") {
		fmt.Println("\n--check-only 已指定，不进行实际启动。")
		if !ready {
			return fmt.Errorf("前置条件未满足，见上面的检查结果")
		}
		return nil
	}
	if !ready {
		return fmt.Errorf("前置条件未满足，已跳过启动验证；补齐后重跑本命令")
	}

	fmt.Println()
	fmt.Println("正在进行实际启动验证（会占用一个随机空闲端口，几分钟后自行结束）...")
	if err := installer.VerifyArkApiInstallation(ctx, os.Stdout); err != nil {
		return err
	}
	return nil
}

// printArkApiPrerequisites 打印全部前置条件，返回「能不能继续做启动验证」。
func printArkApiPrerequisites() bool {
	ok := true

	fmt.Println()
	fmt.Println("[1] ArkApi 安装状态")
	if installer.ArkApiInstalled() {
		fmt.Printf("  ✔ AsaApiLoader.exe: %s\n", installer.AsaApiLoaderPath())
	} else {
		fmt.Printf("  ✘ 没有找到 %s\n", installer.AsaApiLoaderPath())
		fmt.Println("      ArkApi 需要自行下载安装到 server-files 的 ShooterGame/Binaries/Win64/ 下，本程序不代为下载")
		ok = false
	}

	win64 := filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame", "Binaries", "Win64")
	pluginsDir := filepath.Join(win64, "ArkApi", "Plugins")
	if fi, err := os.Stat(pluginsDir); err == nil && fi.IsDir() {
		entries, _ := os.ReadDir(pluginsDir)
		fmt.Printf("  ✔ 插件目录: %s（%d 个条目）\n", pluginsDir, len(entries))
	} else {
		// 不算失败：没装插件也能验证加载器本身。
		fmt.Printf("  · 插件目录不存在（%s）—— 只验证加载器本身\n", pluginsDir)
	}

	if runtime.GOOS != "linux" {
		fmt.Println()
		fmt.Println("[2] 运行时环境")
		fmt.Println("  · Windows：VC++ 运行时是系统级组件，按 ArkApi 官方要求自行安装即可，无需 Wine 相关检查")
		return ok
	}

	fmt.Println()
	fmt.Println("[2] Wine/Proton 运行时")
	if err := runner.CheckRuntime(); err != nil {
		fmt.Printf("  ✘ %v\n", err)
		ok = false
	} else {
		fmt.Println("  ✔ umu-run / GE-Proton / Wine 前缀均已就绪")
	}

	// 排在 VC++ 前面，因为它比 VC++ 更硬：VC++ 判据是启发式的、缺了也常常能跑，
	// 而没有显示 AsaApiLoader.exe 一定起不来，且失败时毫无线索
	// （退出码 3，零输出）。见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §9。
	fmt.Println()
	fmt.Println("[3] 图形显示（AsaApiLoader.exe 会创建 Win32 窗口，Wine 下必需）")
	if d := runner.DisplayStatus(); d.Available {
		fmt.Printf("  ✔ %s\n", d.How)
	} else {
		fmt.Printf("  ✘ %s\n", d.Blocked)
		fmt.Println("      没有显示时加载器会以退出码 3 静默退出，连自己的 logs/ 都不会建")
		ok = false
	}

	fmt.Println()
	fmt.Println("[4] VC++ 运行时与 DLL override（ArkApi 的前置依赖）")
	if !printVCRedistStatus(win64) {
		// 缺 VC++ 不阻断启动验证：判据本身是启发式的，而且游戏目录里可能已经带着
		// 原生 DLL。让用户看到现状，然后照样试 —— 与实例启动侧的立场一致
		// （docs/ARKAPI_LINUX_VCREDIST_PLAN.md §3.6）。
		fmt.Println("      仍会继续尝试启动：这些判据是启发式的，最终结论以实际启动结果为准")
	}
	return ok
}

// printVCRedistStatus 打印 prefix 的 VC++ 现状，返回「看起来没问题」。
func printVCRedistStatus(gameDir string) bool {
	info := runner.VCRedistStatus("", gameDir)
	if !info.Managed {
		fmt.Println("  · linux.runtime 不是 umu，prefix 由用户自管，跳过检查")
		return true
	}

	fmt.Printf("  Wine 前缀: %s\n", info.Prefix)

	// override 是承重项：游戏自己在 exe 同目录带着原生 DLL，Windows 的搜索顺序里
	// 应用目录优先于 system32，唯一挡路的是 Wine 默认偏好自己的内建实现。
	overridesOK := info.OverridesSet == info.WantOverrides
	if overridesOK {
		fmt.Printf("  ✔ DLL override: %d/%d 条已写入 prefix 注册表（native,builtin）\n",
			info.OverridesSet, info.WantOverrides)
	} else {
		fmt.Printf("  ✘ DLL override: 只有 %d/%d 条写入了 prefix 注册表\n",
			info.OverridesSet, info.WantOverrides)
		fmt.Println("      这是关键项：没有它，Wine 会优先加载自己的内建实现，")
		fmt.Println("      游戏目录里那些原生 DLL 形同虚设。跑 asa-server setup 会写入")
	}

	if info.Installed {
		fmt.Printf("  ✔ system32 里的 %s 是微软原生版本\n", info.ProbeDLL)
	} else {
		fmt.Printf("  · system32 里的 %s 仍是 Wine 自带的（VC++ 运行时未装进 prefix）\n", info.ProbeDLL)
		if info.InstallerBlocked != "" {
			// 与 [3] 同一个原因、同一个 resolveDisplay —— 装了 xvfb 两条一起解决。
			fmt.Printf("      装不了的原因：%s\n", info.InstallerBlocked)
			fmt.Println("      这一项本身通常不影响 ArkApi（游戏自带的原生 DLL 加上上面的 override 一般够用），")
			fmt.Println("      但缺显示会让 ArkApi 根本起不来 —— 见上面的 [3]。装好 xvfb 后重跑")
			fmt.Println("      asa-server verify-arkapi --install-vcredist 可以把两件事一起补上")
		} else {
			fmt.Printf("      可用的显示：%s；跑 asa-server verify-arkapi --install-vcredist 安装\n",
				info.InstallerDisplay)
		}
	}
	if info.RegistryVersion != "" {
		fmt.Printf("  · prefix 注册表报告的 VC++ 版本: %s"+
			"（仅供参考——GE-Proton 会在全新 prefix 里预置这个键，不能当作已安装的证据）\n",
			info.RegistryVersion)
	}

	fmt.Println("  各 DLL 出处（游戏目录的优先级高于 system32）：")
	fmt.Printf("    %-26s %-10s %s\n", "DLL", "system32", "游戏目录")
	for _, d := range info.DLLs {
		fmt.Printf("    %-26s %-10s %s\n", d.Name, dllOriginText(d.InSystem32), dllOriginText(d.InGameDir))
	}
	// 判据是 override，不是 system32 —— 后者在无头机上本来就装不上。
	return overridesOK
}

func dllOriginText(o runner.DLLOrigin) string {
	switch o {
	case runner.DLLNative:
		return "原生"
	case runner.DLLWine:
		return "Wine 内建"
	case runner.DLLMissing:
		return "缺失"
	}
	return "-"
}
