package instance

import (
	"context"
	"fmt"

	cfgpkg "asa-server/internal/config"
	"asa-server/internal/installer"
	procpkg "asa-server/internal/process"
	"asa-server/internal/runner"
	"asa-server/pkg/logger"
	"asa-server/pkg/resourcegate"
)

// sharedPrefixGate 是共享 Wine prefix 下的启动闸门，容量 1。
//
// 为什么需要它：`prefix_mode: shared` 时所有实例跑在同一个 Wine prefix、同一个
// wineserver 上，而 Proton 的启动路径（prefix setup + protonfixes）假设一个
// prefix 同时只被一次启动触碰。批量启动本来就是串行的
// （internal/batchmanage/manager.go 阶段二），但单实例启动 API 不是——用户在
// 面板上先点 A、不等它好就点 B，两条启动流程会真并发。这把闸门补的正是这个缺口。
//
// 放行判据是「上一台到达 start_initialization_successful」，不是固定时长：
// 大地图初始化远超 30 秒，小地图十几秒就好了，按时间等要么不够要么白等。
// 上一台**失败**同样放行——失败是那一台的事，不该连坐后面的（见
// docs/UMU_PREFIX_PER_INSTANCE_PLAN.md §8）。
//
// per-instance 模式与 Windows 上没有这个约束，闸门整段短路，时序与加它之前
// 逐字相同。信号量+持有者机制本身不认识"实例""prefix"这些概念，见
// asa-server/pkg/resourcegate；判断要不要用这把闸门（SharesWinePrefix）
// 与等待日志的措辞，是本文件唯一留下的业务规则。
var sharedPrefixGate = resourcegate.New(1)

// acquireLaunchGate 阻塞到本次启动可以进入启动路径为止。
//
// 返回的释放函数是幂等的：调用方在初始化成功后显式调一次放行下一台，
// 同时用 defer 兜住所有早退路径，两次调用不会互相打架。
func acquireLaunchGate(ctx context.Context, instanceName string) (release func(), err error) {
	if !runner.SharesWinePrefix() {
		return func() {}, nil
	}

	// 只有确实要等才打日志——否则常见的「无人竞争」路径上会多出一条每次启动都
	// 出现、却什么也没解释的噪音。
	waited := sharedPrefixGate.Holder() != ""
	if waited {
		logger.Infof("实例 %s 正在等待实例 %s 初始化完成后再启动"+
			"（linux.prefix_mode=shared，多实例共用一个 Wine prefix，需串行启动）",
			instanceName, sharedPrefixGate.Holder())
	}

	release, err = sharedPrefixGate.Acquire(ctx, instanceName)
	if err != nil {
		return func() {}, fmt.Errorf("等待共享 Wine prefix 启动闸门时被取消: %w", err)
	}

	if waited {
		logger.Infof("实例 %s 已获得共享 Wine prefix 的启动许可，开始启动", instanceName)
	}
	return release, nil
}

// conflictingArkApiInstance 找出「已经在跑、并且也启用了 ArkApi」的实例，
// 空字符串表示没有冲突。只在共享 Wine prefix 下有意义。
//
// 为什么这是个硬冲突：一个 prefix = 一个 wineserver = 一个 Wine 会话，而同一个
// 会话里起第二个 AsaApiLoader.exe，它会在 umu.exe 之后、exec 出加载器之前**挂住**
// ——不退出、不报错，一直到 waitForGamePID 三分钟超时被清掉。
//
// **这一条是观测，不是推断，而且复测过两轮**：
//
//   - 2026-08-31 首次实测（每个实例各自 fork 一个 Xvfb、显示号互不相同）。
//   - 2026-09-01 在自管 Xvfb 下复测，**全部实例同一个 DISPLAY=:0**，三轮、
//     两次对调先后顺序（jibian↔meijue），后起的那个每次都止步于 umu.exe、
//     每次都跑满 3 分钟被清。见 docs/SHARED_PREFIX_MULTI_ARKAPI_PLAN.md §5。
//
// 复测的意义在于：当初判死这条路的实验里两个实例带着两个不同的显示，而现在它们
// 必然带同一个。**统一显示并不能解决问题**，所以卡点确实在 Wine 会话这一层。
//
// **卡点在哪，2026-09-01 的 WINEDEBUG=+x11drv,+win,+explorer 复测已经钉死**——
// 不在显示，也不在窗口创建，而是更靠后：
//
//   - 后起那条链**不去建 desktop，而是加入先来那个的**：它的窗口父级就是 A 的
//     explorer 建的桌面窗口/Message 窗口，没有 "started explorer"。
//   - 它的 x11drv **完整初始化了两次，零错误**。
//   - 它一路走到 **Wine conhost 为 umu.exe 建出控制台窗口**（WineConsoleClass +
//     对应的 X 窗口），**建成功了**。
//   - 然后 umu.exe 停在 futex_waitv，**从此不再 exec 目标 exe**；conhost 那个窗口
//     还在一秒一次地重绘、光标还在闪，直到超时被清。
//
// 所以旧注释里那句「Wine 的显示子系统每会话只初始化一次，第二个加载器在创建窗口
// 这一步静默挂住」**三处都错**：显示子系统没坏、窗口建出来了、它也不"静默"
// （41KB 日志且还在涨，只是没人接过它的 stderr 看）。
//
// ⚠️ **umu.exe 在等哪个同步对象，至今未知。** 这一行是有意留空的：上一次有人在
// 这里填了个听起来合理的推断，然后它被抄进四个地方当事实用了三个月。要填就拿
// 观测填 —— 下一步该做的对照写在 docs/SHARED_PREFIX_MULTI_ARKAPI_PLAN.md §12.4。
//
// 只有 ArkApi 会撞：ArkAscendedServer.exe 根本不碰显示，所以共享 prefix 下
// 多个纯 ARK 实例是正常可用的——参考脚本 ark_instance_manager.sh 一直如此，
// 它压根不支持 ArkApi，这也是它从没暴露过这个问题的原因。
func conflictingArkApiInstance(self string) string {
	// 只有共享 Wine 会话时才存在这个冲突。Windows 与 per-instance 下必须直接放行 ——
	// 漏了这一条就会把**本来完全合法**的第二个 ArkApi 实例拦下来，而且拦得理直气壮：
	// 报错信息还会建议用户去改一个他已经改好了的配置项。
	if !runner.SharesWinePrefix() {
		return ""
	}

	names, err := cfgpkg.GetAvailableInstances()
	if err != nil {
		// 读不到实例列表不该拦住启动：这个检查是为了给出好的错误信息，
		// 它自己失败时应当让位给原本的启动流程。
		logger.Warnf("检查 ArkApi 实例冲突时无法列出实例：%v", err)
		return ""
	}
	for _, name := range names {
		if name == self {
			continue
		}
		if running, _ := procpkg.IsServerRunning(name); !running {
			continue
		}
		cfg, err := cfgpkg.LoadInstanceConfig(name)
		if err != nil || cfg == nil || !cfg.EnableAsaPlugin {
			continue
		}
		return name
	}
	return ""
}

// arkApiConflictError 是那条冲突的唯一措辞。两个调用点（HTTP 层的 PrecheckStart
// 与启动路径里的 startServerInternal）必须给出逐字相同的话：同一个原因在弹窗里
// 和日志里长得不一样，只会让人以为遇上了两个问题。
func arkApiConflictError(self, other string) error {
	return fmt.Errorf("无法启动实例 %s：它与正在运行的实例 %s 都启用了 ArkApi，"+
		"而当前 linux.prefix_mode=shared 让所有实例共用同一个 Wine 会话——"+
		"该模式下同时只能有一个 ArkApi 实例（第二个会卡在加载器启动前，直到超时）。"+
		"把 config.yaml 的 linux.prefix_mode 改成 per-instance 即可同时运行"+
		"（每实例独立 Wine 前缀，首次启动多花约一分钟创建）；"+
		"或者先停掉实例 %s", self, other, other)
}

// PrecheckStart 在真正开始启动**之前**跑一遍那些「不试也知道会失败」的检查，
// 让 HTTP 层能把原因直接写进应答里。返回 nil 不代表启动会成功。
//
// 为什么需要它：`POST /api/server/:name/start` 是异步的 —— CAS 一成功就
// 200「正在启动」，真正的失败发生在后台协程里。那条失败只落两个地方：系统日志，
// 以及一条转瞬即逝的 start_failed 状态事件（StartServer 收尾时紧跟着写
// stopped，把它盖掉）。于是用户点了启动、收到「正在启动」，然后**什么提示也
// 没有**，实例悄悄回到已停止。共享 prefix 下起第二个 ArkApi 实例正是这个场景：
// 原因是完全确定的、也是可操作的（改 prefix_mode 或先停另一台），却只有翻日志
// 才看得到。
//
// 只收三类都满足的检查：**确定性**（不含启发式，不会误拦）、**无副作用**
// （不起进程、不建目录——所以这里不问 DisplayStatus 之外的任何东西，也不碰
// acquireDisplay）、**启动路径上同样会拦**（这里放行、那里拦下，才是权威顺序）。
func PrecheckStart(instanceName string) error {
	// 自己不跑 ArkApi 就与这条冲突无关。判据取 server-files 里的加载器而不是
	// 镜像里的那份：此刻镜像可能还没同步出来（首次启动），而镜像本来就是从
	// server-files 复制的。启动路径上那份判据（镜像里的 exe）仍然是权威的。
	cfg, err := cfgpkg.LoadInstanceConfig(instanceName)
	if err != nil || cfg == nil || !cfg.EnableAsaPlugin || !installer.ArkApiInstalled() {
		return nil
	}
	if other := conflictingArkApiInstance(instanceName); other != "" {
		return arkApiConflictError(instanceName, other)
	}
	return nil
}
