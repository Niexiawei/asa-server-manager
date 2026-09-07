package serverinfo

import "sync/atomic"

// NetSource 提供**按进程**的累计收发字节。
//
// gopsutil 给不了这个数：Process.NetIOCounters 在 Windows 上未实现，在 Linux 上
// 读的是网络 namespace 级（等价于整机）。实现在 pkg/procnet（Linux 走 eBPF，
// Windows 走 ETW，见 docs/RESOURCE_RATE_CHART_PLAN.md §2.2 与
// docs/WINNET_ETW_PLAN.md），由上层注入进来——采样器不认识这两种机制，
// 也不依赖它们；没注入时实例网络字段恒为 null。
//
// 返回的是**累计值**，速率由采样器按 Δt 自己算（与磁盘、网络的处理一致）。
type NetSource interface {
	Bytes(pid int32) (rx, tx uint64, ok bool)
}

var netSource atomic.Pointer[NetSource]

// SetNetSource 注入按进程网络计量的实现；传 nil 表示撤下（实例网络字段回到 null）。
func SetNetSource(src NetSource) {
	if src == nil {
		netSource.Store(nil)
		return
	}
	netSource.Store(&src)
}
