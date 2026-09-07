// Package procnet 按进程（tgid）统计网络收发字节。
//
// 存在的理由：gopsutil 给不了这个量——`Process.NetIOCounters` 在 Windows 上未实现，
// 在 Linux 上读的是网络 namespace 级（等价于整机）。要按进程精确计量只能在内核侧下钩子。
//
// 本包是**统一门面**，两个平台各有一套机理完全不同的实现：
//
//   - Linux/amd64：用 github.com/cilium/ebpf 加载一组 kprobe/kretprobe，
//     按 `bpf_get_current_pid_tgid() >> 32` 聚合到 BPF hash map（procnet_linux.go）。
//   - Windows：委托 pkg/winnetetw，走 ETW 的 Microsoft-Windows-Kernel-Network
//     provider（procnet_windows.go 只是转发壳，见 docs/WINNET_ETW_PLAN.md）。
//
// 其余平台（linux/arm64 不出 eBPF 产物等）返回 ErrUnsupported。
// 调用方不需要知道有几种实现，也不需要 build tag——这正是门面存在的理由。
//
// 本包**不认识实例、PID 文件等领域概念**，只认 PID；被跟踪进程的集合由调用方通过
// Bytes 的调用隐式给出（问到谁就跟踪谁，一段时间没人问就自动淘汰）。
// 上层接线见 pkg/serverinfo 的 NetSource 与 internal/webapi/actions.go。
//
// 设计与取舍详见 docs/RESOURCE_RATE_CHART_PLAN.md §2.2。
package procnet

import "errors"

// BPF 对象（bpf/procnet_amd64.o）**已编译好提交进仓库**，常规 go build 不需要
// clang/llvm——只有改了 bpf/procnet.c 或 bpf/bpf_min.h 才要重跑这两条。
// llvm-strip -g 去掉 DWARF、保留 .BTF（18KB → 11KB）。
//
// 这两行**必须留在这个没有 build tag 的文件里**：go generate 也遵守 build tag，
// 写进 procnet_linux.go 的话，在 Windows 开发机上连指令都扫不到——而 .o 恰恰
// 就是在那台机器上生成的（clang -target bpf 跨平台可用，bpf2go 则不行，见
// docs/RESOURCE_RATE_CHART_PLAN.md §11.2 决策 25）。
//
// 另外两个等价入口，参数必须与这里保持一致：
//   - 根目录 Makefile 的 `make bpf`（有依赖判断，只在源比产物新时才编；
//     可用 CLANG= / LLVM_STRIP= 指定工具链）
//   - CI：.github/workflows/bpf.yml 在 .c/.h 变更时自动重新生成并提交回来
//
// **两个产物**：基础版 + `-DPROCNET_NS_PID` 的命名空间感知版。后者用
// bpf_get_ns_current_pid_tgid（内核 5.7+）把 tgid 换算到调用方的 PID namespace；
// 5.4 上加载不了，所以不能只留它一个（procnet_linux.go 先试后退）。
//
//go:generate clang -O2 -g -Wall -Werror -target bpf -mcpu=v1 -c bpf/procnet.c -o bpf/procnet_amd64.o
//go:generate llvm-strip -g bpf/procnet_amd64.o
//go:generate clang -O2 -g -Wall -Werror -target bpf -mcpu=v1 -DPROCNET_NS_PID -c bpf/procnet.c -o bpf/procnet_ns_amd64.o
//go:generate llvm-strip -g bpf/procnet_ns_amd64.o

// ErrUnsupported 表示当前平台/内核下压根没有实现（linux/arm64 等）。
// 有实现但这次没起来（缺权限、缺 BTF、被容器策略挡下）返回的是具体原因，不是它。
//
// 无论哪种，调用方都应当把实例级网络字段整体置 null，而不是当作故障——
// 宿主机网络与其它所有指标都不受影响。
var ErrUnsupported = errors.New("procnet: 当前平台或内核不支持按进程网络计量")

// Options 是 Load 的可选参数。
type Options struct {
	// BTFPath 外部 BTF 的位置，对应 appconfig 的 linux.ebpf_btf_path，留空 =
	// 交给 cilium/ebpf 去读内核自带的 /sys/kernel/btf/vmlinux。
	//
	// 取值支持两种形态：
	//   - 单个 BTF 文件（.btf，或 btfhub 那种 .btf.tar.xz）
	//   - btfhub-archive 的本地副本目录：按 uname -r + /etc/os-release 自动拼路径
	//
	// 当前这版 BPF 程序只读 pt_regs 的寄存器参数、不访问任何内核结构体字段，
	// 因此**没有 CO-RE 重定位、加载时并不需要目标机 BTF**；这个选项是为将来
	// 需要按 socket 取地址/端口之类要读内核结构体的扩展预留的。配错了只降级，不阻断。
	BTFPath string

	// Diagnostics 打开之后，实现可以做一些**只有排障才值得的**额外工作，
	// 代价是常驻开销——服务进程一律传 false，只有 `asa-server netmon` 传 true。
	//
	// Linux：开启内核的 BPF 运行统计（BPF_ENABLE_STATS，需 5.8+），于是
	// Describe() 能报出每个探针**被命中了多少次**。这一个数把
	// 「探针根本没触发」与「触发了但 tgid 对不上、流量没算进目标」分开——
	// 两者在外部表现完全一样（曲线恒零），没有它只能靠猜。
	// 统计是全局开关，会给**机器上所有** BPF 程序加一点每次执行的计时开销，
	// 所以不能在服务里默认开。
	//
	// Windows：当前无额外行为（ETW 侧的等价信息本来就在 Describe 里）。
	Diagnostics bool
}
