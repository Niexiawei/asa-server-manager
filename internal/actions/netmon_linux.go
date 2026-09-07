//go:build linux

package actions

import (
	"context"
	"strings"

	"asa-server/internal/appconfig"
	"asa-server/pkg/procnet"

	"github.com/urfave/cli/v3"
)

// Linux 上的按进程网络计量走 eBPF，入口就是 procnet.Load——那边没有第二条路，
// 所以这条命令直接用门面，不像 ETW 侧要绕开委托层。
//
// 没有 TCP/UDP 分项：eBPF 侧六个探针是**分协议挂**的，Describe() 已经告诉你
// 哪几个挂上了；挂上了就必然计数，不存在「挂上了但事件不来」的中间态。
// 要拿分项就得改 BPF 源、改 map value 布局、重新生成 .o，代价与收益不成比例。
// 见 docs/NETMON_CLI_AND_ETW_WIRING_PLAN.md §2.8。
func netmonPlatformCommands() []*cli.Command {
	return []*cli.Command{
		{
			Name:  "ebpf",
			Usage: "用 eBPF（kprobe/kretprobe）观察一个进程的收发字节",
			Flags: append(netmonFlags(), &cli.StringFlag{
				Name:  "btf",
				Usage: "外部 BTF 路径（单文件或 btfhub 目录）；不传则取 linux.ebpf_btf_path",
			}),
			Action: actionNetmonEBPF,
		},
	}
}

func actionNetmonEBPF(ctx context.Context, cmd *cli.Command) error {
	btf := strings.TrimSpace(cmd.String("btf"))
	if btf == "" {
		btf = appconfig.Get().Linux.EBPFBTFPath
	}
	return runNetmon(ctx, cmd, func() (netCollector, error) {
		// Diagnostics 打开内核的 BPF 运行统计：Describe 会多报每个探针的命中次数。
		// 这一个数把「探针根本没触发」与「触发了但 tgid 对不上」分开——
		// 两者的外部表现都是曲线恒零。诊断命令才开，服务进程不开（全局开销）。
		return procnet.Load(procnet.Options{BTFPath: btf, Diagnostics: true})
	})
}
