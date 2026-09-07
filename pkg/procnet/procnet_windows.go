//go:build windows

package procnet

import "asa-server/pkg/winnetetw"

// Windows 上按进程的网络计量走 ETW（pkg/winnetetw，
// Microsoft-Windows-Kernel-Network provider），与 Linux 侧 eBPF 的语义逐条对齐：
// Bytes 返回累计值、首问登记 0 基线、30s 没人问就淘汰。
// 实现细节见 docs/WINNET_ETW_PLAN.md，接线决策见
// docs/NETMON_CLI_AND_ETW_WIRING_PLAN.md。
//
// 本文件只做转发，不复制任何逻辑——两个平台的行为差异应该只存在于
// winnetetw 与 procnet_linux.go 内部。

// Collector 是 winnetetw.Collector 的门面壳。
//
// 包一层而不是 `type Collector = winnetetw.Collector`：类型边界要握在 procnet
// 手里。别名会让调用方 import procnet 却拿到 winnetetw 的类型，fmt 打印、
// 错误文案、将来给 procnet 加平台公共方法都会失控。
// internal/webapi 与 pkg/serverinfo 永远只见 procnet.Collector。
type Collector struct {
	etw *winnetetw.Collector
}

// Load 建立 ETW 会话。失败返回 error（权限不足等，winnetetw 已经翻成可行动的中文），
// 调用方按「失败即降级」处理：实例的 net_io 为 null，宿主机网络与其它指标照常。
//
// ⚠️ 普通用户下失败是**常规路径**，不是故障：实时消费 Kernel-Network 事件需要
// 管理员或 Performance Log Users 组。Windows 服务（LocalSystem）与管理员终端
// 起的 api 都满足；双击 GUI、普通终端跑 api 则会降级。
//
// opts.BTFPath 是 Linux 概念，Windows 侧没有对应物，忽略之——组合根无条件传参，
// 不需要在调用侧做平台判断。
func Load(opts Options) (*Collector, error) {
	c, err := winnetetw.Load(winnetetw.Options{})
	if err != nil {
		return nil, err
	}
	return &Collector{etw: c}, nil
}

// Bytes 返回该 PID 的累计收发字节（自其被登记进跟踪集合起算），
// 速率由调用方按 Δt 差分。会话已终止或已关闭时返回 ok=false，
// 调用方据此把该字段置 null（而不是画一条恒 0 的线）。
func (c *Collector) Bytes(pid int32) (rx, tx uint64, ok bool) {
	if c == nil || c.etw == nil {
		return 0, 0, false
	}
	return c.etw.Bytes(pid)
}

// Describe 返回一行可读的运行状态（会话名、事件数、丢事件计数等），供上层记日志。
func (c *Collector) Describe() string {
	if c == nil || c.etw == nil {
		return ""
	}
	return c.etw.Describe()
}

// Close 停止 ETW 会话（CloseTrace → 等 ProcessTrace 退出 → ControlTraceW STOP，
// 并复核会话真的销毁了）。必须在进程退出前调用，避免残留 session。
func (c *Collector) Close() error {
	if c == nil || c.etw == nil {
		return nil
	}
	return c.etw.Close()
}
