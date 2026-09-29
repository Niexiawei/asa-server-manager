package main

import "strings"

// startupMode 决定 main 在 CLI 解析之前那段启动引导的副作用。
//
// 配置加载与建数据目录必须发生在构建 CLI 之前（全局 flag 的 Value 取自配置，
// 「flag > 文件 > 默认值」才由 cli 库天然保证），所以只能先偷看参数判断本次要跑
// 什么命令。见 docs/SETUP_FLOW_OPTIMIZATION_PLAN.md Part 2 §P2-3.2。
type startupMode struct {
	// autoGenerate：三级查找都没有 config.yaml 时，Load 是否在 exe 同级写一份模板。
	autoGenerate bool
	// ensureDirs：是否在 BaseDir 下建数据子目录并把文件日志切过去。
	ensureDirs bool
	// deferDirsIfMissing：配置缺失时不建数据目录（只设目录变量），等命令自己生成配置、
	// 选定数据目录后经 bootstrap.Reload 再建。setup 与 Windows GUI 用：否则 exe 旁边会
	// 先冒出一堆空目录，而用户接下来选的数据目录多半在别处。
	deferDirsIfMissing bool
	// readOnly：命令只读或只写 config.yaml 本身（config 子命令、帮助、版本）。
	// 配置有错时不在启动引导里报错 / 中止，交给命令自己处理——`config validate`
	// 的全部意义就是报告配置错误，不能在它开口之前就被 log.Fatal 截住。
	readOnly bool
}

var (
	// defaultStartup 是旧行为：缺配置就生成模板、建数据目录。api / 服务模式 /
	// 其余全部命令都走它（§10.7 不变量 3：api 不得要求预先存在的 config.yaml）。
	defaultStartup = startupMode{autoGenerate: true, ensureDirs: true}
	// readOnlyStartup 完全不碰磁盘（除了命令自己要写的 config.yaml）。
	readOnlyStartup = startupMode{readOnly: true}
	// firstRunStartup 给自己处理配置缺失的入口（setup、Windows GUI）：不自动生成，
	// 配置已存在时照常建目录，缺失时推迟。
	firstRunStartup = startupMode{ensureDirs: true, deferDirsIfMissing: true}
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
var firstRunCommands = map[string]bool{
	"setup": true,
	"gui":   true, // 仅 Windows 注册
}

// readOnlyCommands 是顶层命令名里不该有启动副作用的那些。
var readOnlyCommands = map[string]bool{
	"config": true,
	"help":   true, // urfave/cli 自动加的 help 子命令
	"h":      true,
}

// startupModeFor 根据目标平台与命令行参数（os.Args，含程序名）决定启动引导的副作用。
func startupModeFor(goos string, args []string) startupMode {
	if len(args) <= 1 {
		// 无参：Windows 进 GUI（首次启动向导自己处理配置缺失），Linux 起 api（旧行为）。
		if goos == "windows" {
			return firstRunStartup
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
		for _, later := range rest[i+1:] {
			if later == "--" {
				break
			}
			switch later {
			case "--help", "-h", "-help":
				return readOnlyStartup
			}
		}
		if firstRunCommands[arg] {
			return firstRunStartup
		}
		return defaultStartup
	}
	return defaultStartup
}
