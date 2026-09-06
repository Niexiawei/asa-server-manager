//go:build windows

package procnet

// Windows 上按进程的网络计量没有 eBPF 这条路：eBPF-for-Windows 不覆盖这类计量，
// 要做只能走 ETW 的 Microsoft-Windows-Kernel-Network provider。
// 那是一个独立事项，本方案不做（docs/RESOURCE_RATE_CHART_PLAN.md §2.2）。
//
// 于是实例级 net_io 在 Windows 上恒为 null——**这是设计，不是故障**：
// 宿主机网络（net.IOCounters）与其它所有指标都照常。

// Collector 在 Windows 上是个空壳，只为让调用方无需 build tag 就能编译。
type Collector struct{}

// Load 恒返回 ErrUnsupported。
func Load(Options) (*Collector, error) { return nil, ErrUnsupported }

// Bytes 恒返回 ok=false。
func (c *Collector) Bytes(int32) (rx, tx uint64, ok bool) { return 0, 0, false }

// Describe 恒为空。
func (c *Collector) Describe() string { return "" }

// Close 无事可做。
func (c *Collector) Close() error { return nil }
