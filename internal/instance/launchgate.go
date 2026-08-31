package instance

import (
	"context"
	"fmt"
	"sync"

	cfgpkg "asa-server/internal/config"
	procpkg "asa-server/internal/process"
	"asa-server/internal/runner"
	"asa-server/pkg/logger"
)

// launchGate 是共享 Wine prefix 下的启动闸门。
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
// 逐字相同。
type launchGateState struct {
	sem chan struct{} // 容量 1 的信号量：写入 = 持有，读出 = 释放
	mu  sync.Mutex
	who string // 当前持有者，只用于把等待日志写得有主语
}

var launchGate = &launchGateState{sem: make(chan struct{}, 1)}

func (g *launchGateState) holder() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.who == "" {
		return "（未知）"
	}
	return g.who
}

func (g *launchGateState) setHolder(name string) {
	g.mu.Lock()
	g.who = name
	g.mu.Unlock()
}

// acquireLaunchGate 阻塞到本次启动可以进入启动路径为止。
//
// 返回的释放函数是幂等的：调用方在初始化成功后显式调一次放行下一台，
// 同时用 defer 兜住所有早退路径，两次调用不会互相打架。
func acquireLaunchGate(ctx context.Context, instanceName string) (release func(), err error) {
	if !runner.SharesWinePrefix() {
		return func() {}, nil
	}

	select {
	case launchGate.sem <- struct{}{}:
	default:
		// 没抢到才打日志，也才需要说明在等谁——否则常见路径上会多出一条
		// 每次启动都出现、却什么也没解释的噪音。
		logger.Infof("实例 %s 正在等待实例 %s 初始化完成后再启动"+
			"（linux.prefix_mode=shared，多实例共用一个 Wine prefix，需串行启动）",
			instanceName, launchGate.holder())
		select {
		case launchGate.sem <- struct{}{}:
		case <-ctx.Done():
			return func() {}, fmt.Errorf("等待共享 Wine prefix 启动闸门时被取消: %w", ctx.Err())
		}
		logger.Infof("实例 %s 已获得共享 Wine prefix 的启动许可，开始启动", instanceName)
	}

	launchGate.setHolder(instanceName)

	var once sync.Once
	return func() {
		once.Do(func() {
			launchGate.setHolder("")
			<-launchGate.sem
		})
	}, nil
}

// conflictingArkApiInstance 找出「已经在跑、并且也启用了 ArkApi」的实例，
// 空字符串表示没有冲突。只在共享 Wine prefix 下有意义。
//
// 为什么这是个硬冲突：一个 prefix = 一个 wineserver = 一个 Wine 会话，而 Wine 的
// 显示子系统（winex11.drv / explorer 桌面）**每个会话只初始化一次**。第一个
// AsaApiLoader.exe 起来时会话就绑定在它那个 X 显示上了；第二个加载器带着自己的
// DISPLAY 加入同一个会话，会在 umu.exe 之后、exec 出加载器之前**静默挂住**——
// 不报错、不退出、什么都不打，一直到 waitForGamePID 三分钟超时为止
// （2026-08-31 真机实测，见 docs/UMU_PREFIX_PER_INSTANCE_PLAN.md §2.2）。
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
