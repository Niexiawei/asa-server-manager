package runner

import "fmt"

// DescribeUnmet says, in this program's words, what an unmet capability
// means for the exe that needed it. It completes a sentence of the form
// "实例 X 启用了 ArkApi，但…": the runtime plugins only report the mechanism
// (Unmet.Readiness.Detail — "no Xvfb here"), and what that breaks is ours to
// say. See docs/UMU_RUNTIME_PLUGIN_PLAN.md §6.
func DescribeUnmet(u Unmet) string {
	switch u.Cap {
	case CapGUI:
		// 缺显示是事实（Definitive）：AsaApiLoader.exe 会创建真正的 Win32 窗口，
		// Wine 连不上 X 服务时 CreateWindow 直接失败，加载器退出码 3 且**什么都不打**
		// （连自己的 logs/ 目录都不建）。见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §9。
		return u.Readiness.Detail + "；AsaApiLoader.exe 在 Wine 下没有图形显示会静默退出"
	case CapMSVCRT:
		// 启发式（不 Definitive）：判据是 PE 头标记，而游戏在 exe 旁边自带了大部分
		// 原生 DLL，所以「没检测到」常常照样能跑。见 ARKAPI_LINUX_VCREDIST_PLAN §3.6。
		return "在 Wine 前缀里没有检测到微软 VC++ 运行时，AsaApiLoader.exe 可能起不来。" +
			"执行 asa-server setup 会自动安装；或确认 config.yaml 的 linux.install_vcredist 没有被关掉。"
	}
	return fmt.Sprintf("缺少运行时能力 %s（%s）", u.Cap, u.Readiness.Detail)
}
