package webapi

import (
	"sync"

	"asa-server/internal/appconfig"
	"asa-server/pkg/logger"
	"asa-server/pkg/procnet"
	"asa-server/pkg/serverinfo"
)

// 实例级网络计量（eBPF）的接线点。
//
// 为什么在这里而不是 pkg/serverinfo 内部 lazy load：serverinfo 定义的是
// NetSource 接口，谁来实现由组合根决定——这样 serverinfo 不认识 eBPF，
// 也不必在 Windows 上背着一个恒返回 unsupported 的依赖。
// 配置项同理一路传参下来（appconfig → procnet.Options），pkg/ 不读配置。
//
// 失败是**常规路径**：Windows 没有实现、Linux 缺 BTF / 被容器策略挡下、
// 内核符号漂移都会走到这。结果只是实例的 net_io 恒为 null，
// 宿主机网络与其它所有指标照常。见 docs/RESOURCE_RATE_CHART_PLAN.md §2.2。
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
