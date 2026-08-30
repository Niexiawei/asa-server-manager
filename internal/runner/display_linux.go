//go:build linux

package runner

// Wine 侧的「图形显示」是本项目在 Linux 上的一个**硬依赖**，尽管跑的是无头服务端。
//
// 两处需要它，成因相同：Wine 的 winex11.drv 连不上 X 服务时，`CreateWindow` 一律失败
// （`err:winediag:nodrv_CreateWindow ... The explorer process failed to start.`），
// 于是任何要开窗口的 Windows 程序都会在打出第一行日志之前就死掉。
//
//  1. **AsaApiLoader.exe（ArkApi）**——真机实测（WSL2 + GE-Proton10-34 + umu 1.4.4，
//     2026-08-30）：不给 DISPLAY 时 5 秒后退出码 3，**一个字都不打**，Win64/logs/
//     连目录都不会建；只补一个 `DISPLAY=:0`，同一条命令就能加载 ArkApi、下载 offsets
//     cache、加载插件并拉起 ArkAscendedServer.exe。日志里 `oleacc:find_class_data
//     unhandled window class: L"Static"` 说明它是带真窗口的程序，不是纯控制台程序。
//  2. **vc_redist.x64.exe**——WiX Burn 引导器即使带 /quiet 也要初始化 UI 子系统，
//     连不上 X 就以 203 退出（见 vcRedistExitNoDisplay）。
//
// ArkAscendedServer.exe 本身**不需要**显示（同一台机器上 42 秒就开始监听），所以
// 只有上面两条路径会去解析显示，普通实例启动一如既往地不碰它。
//
// 见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §2.6 与 §9。

import (
	"os"
	"os/exec"
	"strings"
)

// displayTarget 描述「怎么给一个 Wine 进程提供图形显示」：要么把宿主已有的 DISPLAY
// 传下去，要么用 xvfb-run 现开一个虚拟显示。
type displayTarget struct {
	Env     []string // 追加到进程环境，如 DISPLAY=:0
	Wrapper []string // 命令前缀，如 [xvfb-run -a]
	How     string   // 人类可读的说明
}

// resolveDisplay 决定怎么给进程提供显示，拿不到时第二个返回值说明原因。
//
// **vc_redist 安装与 ArkApi 启动共用这一个函数**：两者需要显示的原因是同一个
// （Wine 的 winex11.drv），分成两套判断只会让它们慢慢漂开。
//
// 顺序上宿主已有的 DISPLAY 优先：那是真机上验证过的路径，而且不用额外起进程。
// 拿不到才退到 xvfb-run —— 无头服务器（本项目的主力部署形态）走的就是这一条，
// 所以 xvfb 在 preflight 里是**阻断级**依赖，见 checkXvfb。
//
// **不传 XAUTHORITY**，哪怕宿主设了。理由与 inheritedEnv 的白名单同源：它经常指向
// /run/user/0 下面的路径，而 pressure-vessel 会老老实实地去 bind 环境变量点名的
// 每一个路径，降权之后那次 bind 直接让整个容器起不来（DBUS_SESSION_BUS_ADDRESS
// 那次就是这么坑了一整晚，见 inheritedEnv 的注释）。真需要 xauth 的显示请让它走
// xvfb-run —— 那是自带 cookie、自成一体的一条路。
func resolveDisplay() (displayTarget, string) {
	if d := os.Getenv("DISPLAY"); d != "" && x11SocketExists(d) {
		return displayTarget{Env: []string{"DISPLAY=" + d}, How: "宿主的 X 显示 " + d}, ""
	}
	if p, err := exec.LookPath("xvfb-run"); err == nil {
		// -a: 自动挑一个没被占用的显示号，多实例并发启动时不会互相撞车。
		return displayTarget{Wrapper: []string{p, "-a"}, How: "xvfb-run（虚拟显示）"}, ""
	}
	return displayTarget{}, "本机没有可用的 X 显示，也没有 xvfb-run —— 请" + xvfbInstallHint
}

// wrap 把显示施加到一条命令上，返回新的 bin/argv/env。
//
// env 的追加放在最后是刻意的：runtimeEnv 会剥掉 XDG_*，DISPLAY 不在其列，但顺序上
// 排最后就不会被将来新增的过滤逻辑吃掉（exec 取同名变量的最后一个）。
func (d displayTarget) wrap(bin string, argv, env []string) (string, []string, []string) {
	outEnv := append(append([]string{}, env...), d.Env...)
	if len(d.Wrapper) == 0 {
		return bin, argv, outEnv
	}
	// argv: [xvfb-run -a] <bin> <原 argv...>
	outArgs := append([]string{}, d.Wrapper[1:]...)
	outArgs = append(outArgs, bin)
	outArgs = append(outArgs, argv...)
	return d.Wrapper[0], outArgs, outEnv
}

// x11SocketExists 校验 DISPLAY 指向的本地 X 服务是不是真的在。
// 光有环境变量不算数：实测 DISPLAY=:99（无人监听）与不设一样失败。
func x11SocketExists(display string) bool {
	// ":0"、":0.0"、"host:0" 都可能；只有本地形式能靠 socket 判断。
	d := display
	if i := strings.LastIndex(d, ":"); i >= 0 {
		d = d[i+1:]
	} else {
		return false
	}
	if i := strings.Index(d, "."); i >= 0 {
		d = d[:i]
	}
	if d == "" {
		return false
	}
	if strings.HasPrefix(display, "/") || !strings.HasPrefix(display, ":") {
		// 远程或抽象形式，无法本地判断 —— 交给调用方自己去试。
		return true
	}
	return pathExists("/tmp/.X11-unix/X" + d)
}

// displayStatus 是 runner.DisplayStatus 的实现。
func displayStatus() DisplayInfo {
	d, blocked := resolveDisplay()
	return DisplayInfo{
		Available: blocked == "",
		How:       d.How,
		Blocked:   blocked,
	}
}
