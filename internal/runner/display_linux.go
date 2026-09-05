//go:build linux

package runner

// 「显示从哪来」的候选链业务规则在 asa-server/pkg/display，自管虚拟显示的机制
// （拉起/看门狗/认领/socket 目录 remount）在 asa-server/pkg/xvfb。本文件只是把
// 两者接到 runner.Config 上的组合根胶水。
//
// 为什么本项目在 Linux 上把「图形显示」当硬依赖（尽管跑的是无头服务端），见
// pkg/display 的包注释、docs/ARKAPI_LINUX_VCREDIST_PLAN.md §9 与
// docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md：ArkAscendedServer.exe 本身**不需要**显示，
// 只有 AsaApiLoader.exe（ArkApi）与 vc_redist.x64.exe 这两条路径要。

import (
	"asa-server/pkg/display"
)

// displayRes 是本进程唯一一个显示解析器。它持有的是 xvfbMgr —— 同一个
// *xvfb.Manager 实例，不是新建的：「进程内只有一个自管显示」这条不变量落地为
// 「组合根只持有一份 Manager」，见 xvfb_linux.go 与
// docs/RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md §4.3。
var displayRes = display.New(display.Config{}, xvfbMgr)

// displayResolver 用当下的 runner.Config 刷新解析器并返回它。
//
// 先调 xvfbManager()：它刷新的是同一个 *xvfb.Manager，而 displayRes 持有的正是那个
// 指针 —— 少这一步，显示解析会拿着上一次 Configure 时的 Xvfb 配置去判断。
func displayResolver() *display.Resolver {
	xvfbManager()
	displayRes.Reconfigure(display.Config{Display: getConfig().Display})
	return displayRes
}

// planDisplay 是只读的候选链判断：**绝不**拉起 X 服务端。preflight、
// DisplayStatus、`verify-arkapi --check-only` 只许问它。
func planDisplay() ([]display.Plan, string) { return displayResolver().Plan() }

// acquireDisplay 是启动路径的唯一入口：先判断，再沿候选链动手（必要时拉起 Xvfb）。
func acquireDisplay() (display.Target, string, error) { return displayResolver().Acquire() }

// stopManagedDisplay 是 runner.StopManagedDisplay 的实现。
func stopManagedDisplay() { displayResolver().Stop() }

// displayStatus 是 runner.DisplayStatus 的实现。
func displayStatus() DisplayInfo { return displayResolver().Status() }
