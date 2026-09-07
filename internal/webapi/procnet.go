package webapi

import (
	"sync"

	"asa-server/internal/appconfig"
	"asa-server/pkg/logger"
	"asa-server/pkg/procnet"
	"asa-server/pkg/serverinfo"
)

// 实例级网络计量的接线点（Linux 走 eBPF、Windows 走 ETW，由 pkg/procnet 门面分派）。
//
// 为什么在这里而不是 pkg/serverinfo 内部 lazy load：serverinfo 定义的是
// NetSource 接口，谁来实现由组合根决定——这样 serverinfo 既不认识 eBPF 也不认识 ETW。
// 配置项同理一路传参下来（appconfig → procnet.Options），pkg/ 不读配置。
//
// 失败是**常规路径**：Windows 上普通用户没有 ETW 实时会话权限、Linux 缺 BTF /
// 被容器策略挡下、内核符号漂移都会走到这。结果只是实例的 net_io 为 null，
// 宿主机网络与其它所有指标照常。见 docs/RESOURCE_RATE_CHART_PLAN.md §2.2
// 与 docs/NETMON_CLI_AND_ETW_WIRING_PLAN.md。
//
// ⚠️ Windows 上 ETW 会话是**独占**的：`asa-server netmon etw` 这类诊断命令一旦
// 抢走会话，这里的 collector 会开始返回 ok=false（字段回到 null）且不会自愈，
// 要重启本进程。所以那条命令默认拒绝在会话已存在时启动。
var (
	procNetMu sync.Mutex
	procNet   *procnet.Collector
)

func startProcNet() {
	procNetMu.Lock()
	defer procNetMu.Unlock()
	if procNet != nil {
		return
	}

	c, err := procnet.Load(procnet.Options{BTFPath: appconfig.Get().Linux.EBPFBTFPath})
	if err != nil {
		logger.Infof("实例级网络监控未启用（该字段将为 null）: %v", err)
		return
	}
	procNet = c
	serverinfo.SetNetSource(c)
	logger.Infof("实例级网络监控已启用：%s", c.Describe())
}

// stopProcNet 卸载 BPF 探针并释放 map。必须先撤下 NetSource 再 Close，
// 否则采样器可能正拿着一个已经关掉的 map 在读。
func stopProcNet() {
	procNetMu.Lock()
	defer procNetMu.Unlock()
	if procNet == nil {
		return
	}
	serverinfo.SetNetSource(nil)
	if err := procNet.Close(); err != nil {
		logger.Warnf("卸载实例级网络监控出错: %v", err)
	}
	procNet = nil
}
