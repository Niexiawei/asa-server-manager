// Package display answers one question: how does a Wine process on this host
// get an X display?
//
// Wine 侧的「图形显示」是本项目在 Linux 上的一个**硬依赖**，尽管跑的是无头服务端。
// Wine 的 winex11.drv 连不上 X 服务时，`CreateWindow` 一律失败
// （`err:winediag:nodrv_CreateWindow ... The explorer process failed to start.`），
// 于是任何要开窗口的 Windows 程序都会在打出第一行日志之前就死掉。真机实测（WSL2 +
// GE-Proton10-34 + umu 1.4.4，2026-08-30）：不给显示时程序 5 秒后退出、**一个字都不打**，
// 连自己的 logs/ 目录都不会建；补上一个能用的显示，同一条命令就跑通。
// 见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §9 与 docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md。
//
// 本包只回答「显示从哪来」这个候选链问题；自管虚拟显示的机制本身（拉起/看门狗/
// 认领/socket 目录 remount）在 asa-server/pkg/xvfb，由调用方构造好一个
// *xvfb.Manager 注入进来 —— 「进程内只有一个自管显示」这条不变量落地为
// 「组合根只持有一份 *xvfb.Manager」，不是由本包或 pkg/xvfb 自建单例。
//
// 本文件**不带 build tag**：Info 要被 HTTP API 层在任何平台上引用（Windows 上
// internal/runner 直接返回一个「总是可用」的 Info，压根不构造 Resolver），
// 而 Target 的追加语义是纯切片操作，可以在任何平台上单测。
package display

// Info is how (and whether) this host can give a Wine process an X display.
// On Windows the caller reports it as always available: there is a real
// window station.
type Info struct {
	Available bool   `json:"available"`
	How       string `json:"how"`     // "宿主的 X 显示 :0" / "自管 Xvfb 虚拟显示 :100"
	Blocked   string `json:"blocked"` // why not, when Available is false
	// Managed is true when the display comes from the Xvfb this program starts
	// and owns, rather than one that was already running.
	Managed bool `json:"managed"`
	// Display is the ":N" that would be (or already is) used. Empty for a
	// managed display that hasn't been started yet — reporting must not start
	// one, see Resolver.Status.
	Display string `json:"display"`
	// Fallbacks are the remaining candidates, in order, that a launch would try
	// if How's own acquisition failed. Diagnostic only: the resolver returns a
	// chain rather than a single answer so that a host whose Xvfb won't start
	// (no fonts, full /tmp) still launches on whatever display it does have,
	// instead of regressing to "cannot start". Empty means the head is all
	// there is. See Resolver.Plan.
	Fallbacks []string `json:"fallbacks,omitempty"`
}

// Target 是**已经拿到手**的显示：把它施加到一条命令的环境上即可。
type Target struct {
	Env []string // 追加到进程环境，如 DISPLAY=:0
	How string   // 人类可读的说明
}

// Apply 把显示追加到一条命令的环境上。
//
// 追加在最后是刻意的：运行时用户的环境改写会剥掉 XDG_*，DISPLAY 不在其列，但顺序上
// 排最后就不会被将来新增的过滤逻辑吃掉（exec 取同名变量的最后一个）。返回新切片而
// 不是就地 append —— 「解析一次显示、拿它包两条命令」不能污染第一条。
func (t Target) Apply(env []string) []string {
	return append(append([]string{}, env...), t.Env...)
}
