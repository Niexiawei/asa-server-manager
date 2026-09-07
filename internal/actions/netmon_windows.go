//go:build windows

package actions

import (
	"context"
	"fmt"

	"asa-server/pkg/winnetetw"

	"github.com/urfave/cli/v3"
)

// Windows 上的按进程网络计量走 ETW（pkg/winnetetw）。
//
// ⚠️ 这里**直连 winnetetw，不经 pkg/procnet 门面**：命令的用途是判断
// 「机制本身行不行」，直连之后一旦失败就能确定问题在 ETW 层，而不是委托层。
// 委托层统共四个转发方法、没有逻辑，绕过它不损失覆盖面。
func netmonPlatformCommands() []*cli.Command {
	return []*cli.Command{
		{
			Name:  "etw",
			Usage: "用 ETW（Microsoft-Windows-Kernel-Network）观察一个进程的收发字节",
			Flags: append(netmonFlags(), &cli.BoolFlag{
				Name:  "force",
				Usage: "已有 ETW 会话在跑时也强行启动（会把对方的监控打掉）",
			}),
			Action: actionNetmonETW,
		},
	}
}

func actionNetmonETW(ctx context.Context, cmd *cli.Command) error {
	// ⚠️ 独占性护栏。session 名固定，同机同时只能有一个消费进程；第二个 Load
	// 会把第一个的会话停掉，**而且对方要重启才能恢复**（撤下 NetSource 之后
	// 不会自己再 Load 回来）。接线之后这就是实打实的脚枪：管理员在服务跑着的
	// 时候敲一次诊断命令，线上的实例网络监控就没了。
	// 见 docs/NETMON_CLI_AND_ETW_WIRING_PLAN.md §2.6。
	if !cmd.Bool("force") {
		active, err := winnetetw.SessionActive()
		switch {
		case err != nil:
			// 问不出来（多半是没权限）——照旧往下走，Load 会给出准确的错误
			fmt.Printf("[提示] 无法查询已有 ETW 会话状态: %v\n", err)
		case active:
			return cli.Exit(
				"已有 ETW 会话 AsaServerProcNet 在运行，多半是 asa-server 服务或 api 进程。\n"+
					"继续会把它的实例级网络监控打掉，且对方要重启才能恢复。\n"+
					"确认要抢占请加 --force。", exitLoadFailed)
		}
	}

	return runNetmon(ctx, cmd, func() (netCollector, error) {
		c, err := winnetetw.Load(winnetetw.Options{})
		if err != nil {
			return nil, err
		}
		return etwCollector{c}, nil
	})
}

// etwCollector 把 winnetetw 的 ProtoBytes 摊成 netmon.go 那个与平台无关的
// 五返回值形状——那个文件不能 import winnetetw（它没有 build tag）。
type etwCollector struct{ *winnetetw.Collector }

func (c etwCollector) BytesByProtocol(pid int32) (tcpRx, tcpTx, udpRx, udpTx uint64, ok bool) {
	v, ok := c.Collector.BytesByProtocol(pid)
	return v.TCPRx, v.TCPTx, v.UDPRx, v.UDPTx, ok
}
