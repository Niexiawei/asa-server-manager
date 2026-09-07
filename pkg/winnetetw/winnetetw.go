//go:build windows

// Package winnetetw 用 ETW（Microsoft-Windows-Kernel-Network provider）统计
// **按进程**的网络收发字节，是 Linux 上 pkg/procnet（eBPF）的 Windows 对应实现。
//
// 整个包只在 Windows 上编译（每个文件都带 //go:build windows——Go 没有包级
// build tag，必须逐文件标注）。跨平台概念（ErrUnsupported、门面）全部留在
// pkg/procnet：后续期由 procnet_windows.go 委托本包，internal/webapi 的组合根
// 一行不改。见 docs/WINNET_ETW_PLAN.md。
//
// 数据语义与 pkg/procnet 逐条对齐：
//   - Bytes 返回**累计值**，速率由调用方（pkg/serverinfo 采样器）按 Δt 差分；
//   - tracked-set：首次问到某 PID 才开始为其计数（返回 0 基线），30s 没人问就淘汰；
//   - 计数器只增不减，PID 复用后差分依然正确（采样器按 CreateTime 重建 prev）。
//
// 计数口径（防误读，详见方案 §4.9）：size 是 Windows 网络栈层面的进程数据量，
// 不等于网卡 wire bytes；回环流量计入；代理/VPN/TUN 环境下与网卡流量有系统性差值。
//
// 权限：实时消费 Kernel-Network 事件需要管理员或 Performance Log Users 组。
// Load 失败一律返回 error（不定义 ErrUnsupported——本包没有「别的平台」），
// 调用方按「失败即降级」处理：实例级网络字段为 null，其余指标不受影响。
//
// 依赖仅 golang.org/x/sys/windows 与标准库；ETW/TDH 的 API 声明见 etw_syscall.go。
package winnetetw

// Options 是 Load 的可选参数。当前无可选项，为将来留位
// （如 ETW buffer 数量/大小/flush 间隔——先写死在 session 参数里，
// 压测发现问题再加，不加无消费者的配置面）。
type Options struct{}

// netKind 由 Event ID 唯一确定的「协议 + 方向」组合。
// ETW 只在这两个维度上有意义，地址族（v4/v6）对计数没有影响，合并。
type netKind uint8

const (
	kindTCP_RX netKind = iota
	kindTCP_TX
	kindUDP_RX
	kindUDP_TX
)

// kernelNetworkEvents 是本包启用的全部 8 个 Event ID（方案 §4.3）。
// 这个过滤会同时下发到 EnableTraceEx2（内核侧，事件产生点之前削减数据量）
// 和 callback（防御性二次过滤）。Kernel-Network provider 的事件远不止这 8 个，
// 不过滤的话整机的每条连接活动都会灌进 consumer。
//
// ARK 的游戏流量是 UDP，UDP 两路（42/43/58/59）必须启用——只挂 TCP 会得到
// 一条恒零的实例网络曲线。
var kernelNetworkEvents = map[uint16]netKind{
	10: kindTCP_TX, // TCP IPv4 send
	11: kindTCP_RX, // TCP IPv4 recv
	26: kindTCP_TX, // TCP IPv6 send
	27: kindTCP_RX, // TCP IPv6 recv
	42: kindUDP_TX, // UDP IPv4 send
	43: kindUDP_RX, // UDP IPv4 recv
	58: kindUDP_TX, // UDP IPv6 send
	59: kindUDP_RX, // UDP IPv6 recv
}

// classifyEvent 是 callback 热路径上的第一道过滤。未登记的 Event ID
// 直接丢弃（正常情况下 EnableTraceEx2 已经挡掉了，这里只是防御）。
func classifyEvent(id uint16) (netKind, bool) {
	k, ok := kernelNetworkEvents[id]
	return k, ok
}
