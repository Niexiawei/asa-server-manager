package main

import (
	"errors"
	"fmt"
	"strings"

	"asa-server/internal/appconfig"
)

// startupMode 决定 main 在 CLI 解析之前那段启动引导的副作用。
//
// 配置加载与建数据目录必须发生在构建 CLI 之前（全局 flag 的 Value 取自配置，
// 「flag > 文件 > 默认值」才由 cli 库天然保证），所以只能先偷看参数判断本次要跑
// 什么命令。见 docs/SETUP_FLOW_OPTIMIZATION_PLAN.md Part 2 §P2-3.2。
//
// 除 readOnly 外，所有入口都要求一份有效的 config.yaml；缺配置时只有
// deferDirsIfMissing 的入口（setup、GUI）自己去生成，其余拒绝启动，见
// startupConfigBlocks 与 docs/APPCONFIG_BASEDIR_PLAN.md Part 2 P2-3 第 6 条。
type startupMode struct {
	// ensureDirs：是否在 BaseDir 下建数据子目录并把文件日志切过去。
	ensureDirs bool
	// deferDirsIfMissing：配置缺失时由命令自己生成配置（setup 的交互流程、GUI 的首次
	// 设置向导），这期间不建数据目录（只设目录变量），等选定数据目录后经
	// bootstrap.Reload 再建——否则 exe 旁边会先冒出一堆空目录，而用户接下来选的数据
	// 目录多半在别处。
	deferDirsIfMissing bool
	// readOnly：命令只读或只写 config.yaml 本身（config 子命令、帮助、版本），或者是
	// 配置坏了也必须能用的维护命令（maintenanceCommands）。不检查配置、不建目录：
	// `config validate` 的全部意义就是报告配置错误，不能在它开口之前就被截住；
	// 配置坏了也得能把服务停掉、卸掉。
	readOnly bool
	// gui：Windows GUI。没有控制台，启动失败要弹窗说明，否则等于闪退。
	gui bool
	// service：作为 OS 服务运行。stderr 没人看，启动失败要写进日志。
	service bool
}

var (
	// defaultStartup：配置必须存在且有效，然后建数据目录。api 与其余命令都走它。
	defaultStartup = startupMode{ensureDirs: true}
	// serviceStartup 与 defaultStartup 相同，只是失败时的告知渠道不同。
	serviceStartup = startupMode{ensureDirs: true, service: true}
	// readOnlyStartup 完全不碰磁盘（除了命令自己要写的 config.yaml）。
	readOnlyStartup = startupMode{readOnly: true}
	// firstRunStartup 给自己处理配置缺失的入口（setup）：配置已存在时照常建目录，
	// 缺失时推迟。配置存在但无效时照样拒绝启动。
	firstRunStartup = startupMode{ensureDirs: true, deferDirsIfMissing: true}
	// guiStartup 是 GUI 版的 firstRunStartup。
	guiStartup = startupMode{ensureDirs: true, deferDirsIfMissing: true, gui: true}
)

// globalValueFlags 是根命令上**带取值**的全局 flag（见 main.go 的 app.Flags）：
// 以 `--name value` 形式出现时要连同后面那个参数一起跳过，否则会把值当成命令名。
// 增删全局 flag 时同步这里；漏登记的最坏后果是识别不出命令、回到 defaultStartup，
// 不会让命令失败。
var globalValueFlags = map[string]bool{
	"api-port":        true,
	"port":            true,
	"cert-file":       true,
	"key-file":        true,
	"tls-domains":     true,
	"trusted-proxies": true,
}

// firstRunCommands 是自己处理「配置缺失」的顶层命令。
var firstRunCommands = map[string]startupMode{
	"setup": firstRunStartup,
	"gui":   guiStartup, // 仅 Windows 注册
}

// readOnlyCommands 是顶层命令名里不该有启动副作用的那些。
var readOnlyCommands = map[string]bool{
	"config": true,
	"help":   true, // urfave/cli 自动加的 help 子命令
	"h":      true,
}

// maintenanceCommands 是配置坏了也必须能用的二级命令（顶层命令 → 子命令）：
// 停服务、卸服务、卸证书。它们不读写数据目录里的业务数据，按 readOnly 处理。
// service start 也在这里：它只是叫 SCM / systemd 去启动服务，服务进程自己会按
// 服务模式再校验一次配置。
var maintenanceCommands = map[string]map[string]bool{
	"service": {"remove": true, "stop": true, "start": true},
	"cert":    {"uninstall": true},
}

// startupModeFor 根据目标平台与命令行参数（os.Args，含程序名）决定启动引导的副作用。
func startupModeFor(goos string, args []string) startupMode {
	if len(args) <= 1 {
		// 无参：Windows 进 GUI（首次启动向导自己处理配置缺失），Linux 起 api。
		if goos == "windows" {
			return guiStartup
		}
		return defaultStartup
	}
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		if arg == "--" {
			break
		}
		if strings.HasPrefix(arg, "-") {
			name, _, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
			switch name {
			case "help", "h", "version", "v":
				// 任何位置出现帮助 / 版本 flag，命令都只打印不执行（含子命令的 --help）。
				return readOnlyStartup
			}
			if globalValueFlags[name] && !inline {
				i++ // 跳过它的值
			}
			continue
		}
		// 第一个非 flag 参数就是顶层命令名。子命令后面还可能出现 --help，
		// 继续扫一遍剩余参数。这里只认帮助、不认 -v：子命令自己的 -v 可能另有含义。
		if readOnlyCommands[arg] {
			return readOnlyStartup
		}
		later := rest[i+1:]
		for _, a := range later {
			if a == "--" {
				break
			}
			switch a {
			case "--help", "-h", "-help":
				return readOnlyStartup
			}
		}
		if subs := maintenanceCommands[arg]; subs != nil && subs[firstNonFlag(later)] {
			return readOnlyStartup
		}
		if mode, ok := firstRunCommands[arg]; ok {
			return mode
		}
		return defaultStartup
	}
	return defaultStartup
}

// firstNonFlag 返回 args 里第一个不以 - 开头的参数（子命令名），没有时返回空串。
func firstNonFlag(args []string) string {
	for _, a := range args {
		if a == "--" {
			return ""
		}
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// startupConfigBlocks 报告这次启动是否因为配置文件而不能继续：readOnly 从不拦；
// 配置存在但无效时一律拦；缺配置时只有自己会生成配置的入口（setup、GUI）放行。
func startupConfigBlocks(mode startupMode, missing bool, err error) bool {
	if mode.readOnly {
		return false
	}
	if errors.Is(err, appconfig.ErrConfigInvalid) {
		return true
	}
	return missing && !mode.deferDirsIfMissing
}

// startupConfigMessage 是拒绝启动时给用户看的话：缺配置时列出查找过的位置并给出
// 生成配置的命令，配置无效时给出文件路径与错误原文。
func startupConfigMessage(missing bool, path string, err error, dirs appconfig.SearchDirs) string {
	var b strings.Builder
	if missing {
		b.WriteString("未找到配置文件 config.yaml，程序不会启动。已查找（按顺序，找到即停）：\n")
		show := func(n int, label, dir string) {
			if dir == "" {
				dir = "（未设置）"
			}
			fmt.Fprintf(&b, "  %d. %s  %s\n", n, label, dir)
		}
		show(1, "环境变量 ASA_CFG", dirs.ASACfg)
		show(2, "程序所在目录    ", dirs.ExeDir)
		show(3, "系统配置目录    ", dirs.SystemDir)
		b.WriteString("请先运行 asa-server config init 生成配置（只生成配置，不下载任何东西），" +
			"或运行 asa-server setup（生成配置并安装运行环境）。")
		return b.String()
	}
	fmt.Fprintf(&b, "配置文件 %s 无法通过校验，程序不会启动：\n  %v\n", path, err)
	b.WriteString("修正后重试；可运行 asa-server config validate 复查。")
	return b.String()
}
