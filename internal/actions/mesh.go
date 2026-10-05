package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	cfgpkg "asa-server/internal/config"
	"asa-server/internal/mesh"
	"asa-server/pkg/meshid"
)

// MeshCommand 是管理器互控的 CLI（docs/REMOTE_MANAGER_MESH_PLAN.md §10.3、§12 P1-7、P2-6、P3-9）。
//
// 这些命令**不连协调节点、不连对端**：CLI 进程与正在运行的服务共用同一个身份，一连就会把服务的
// 会话踢掉（协调节点的单会话规则）。所以 join / leave / enable / disable 只写配置，生效要重启服务
// （页面上的同名操作会热应用）；peers.json 的改动（invite / revoke / approve）服务会自动重新加载，不用重启。
func MeshCommand() *cli.Command {
	return &cli.Command{
		Name:  "mesh",
		Usage: "管理器互控：本机身份、协调节点接入、配对与授权",
		Commands: []*cli.Command{
			{Name: "id", Usage: "打印本机节点 ID（首次运行时生成身份）", Action: actionMeshID},
			{
				Name:      "join",
				Usage:     "用协调节点给出的接入串接入（写入配置，重启服务后生效）",
				ArgsUsage: "<join blob>",
				Action:    actionMeshJoin,
			},
			{Name: "leave", Usage: "断开与协调节点的接入并停用（保留本机身份与配对，重启服务后生效）", Action: actionMeshLeave},
			{Name: "enable", Usage: "启用（没有协调节点时 = 无协调节点模式，只按手填地址直连；重启服务后生效）", Action: actionMeshEnable(true)},
			{Name: "disable", Usage: "停用（重启服务后生效）", Action: actionMeshEnable(false)},
			{Name: "status", Usage: "查看本地配置（只读本地文件，不连协调节点）", Action: actionMeshStatus},
			{Name: "peers", Usage: "列出对端、待批准申请与未用的邀请码", Action: actionMeshPeers},
			{
				Name:  "invite",
				Usage: "生成配对邀请码（整串只显示这一次）",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "role", Value: mesh.RoleOperator, Usage: "授予对方的角色：admin 或 operator"},
					&cli.DurationFlag{Name: "ttl", Value: mesh.DefaultInviteTTL, Usage: "有效期（最长 24h）"},
					&cli.StringSliceFlag{Name: "addr", Usage: "附带本机的直连地址 host:port（可多次）；带上它时对方无需协调节点也能配对"},
					&cli.StringFlag{Name: "note", Usage: "备注（对方配对后作为它的备注名）"},
				},
				Action: actionMeshInvite,
			},
			{
				Name:      "revoke",
				Usage:     "撤销对某个对端的授权（运行中的服务几秒内断开它）",
				ArgsUsage: "<节点 ID>",
				Action:    actionMeshRevoke,
			},
			{
				Name:      "approve",
				Usage:     "批准一条待批准的配对申请",
				ArgsUsage: "<节点 ID>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "role", Value: mesh.RoleOperator, Usage: "授予的角色：admin 或 operator"},
				},
				Action: actionMeshApprove,
			},
		},
	}
}

func meshDir() string { return mesh.Dir(cfgpkg.BaseDir) }

func actionMeshID(context.Context, *cli.Command) error {
	id, err := mesh.LoadIdentity(meshDir())
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

func actionMeshJoin(_ context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() != 1 {
		return cli.Exit("用法：asa-server mesh join <join blob>（整串用引号包起来）", 2)
	}
	cc, err := mesh.Join(meshDir(), cmd.Args().First())
	if err != nil {
		return err
	}
	id, err := mesh.LoadIdentity(meshDir())
	if err != nil {
		return err
	}
	fmt.Printf("已写入配置：协调节点 %s（身份 %s），网络 %s\n", cc.Addr, cc.ID.Short(), cc.NetworkID)
	fmt.Printf("本机节点 ID：%s\n", id)
	fmt.Println("重启 asa-server 服务（或 api）后生效；页面上的「接入」会立即生效。")
	return nil
}

func actionMeshLeave(context.Context, *cli.Command) error {
	if err := mesh.Leave(meshDir()); err != nil {
		return err
	}
	fmt.Println("已断开协调节点接入并停用（本机身份与配对保留）。重启 asa-server 服务（或 api）后生效。")
	fmt.Println("只想去掉协调节点、继续用直连：再运行 asa-server mesh enable。")
	return nil
}

func actionMeshEnable(on bool) cli.ActionFunc {
	return func(context.Context, *cli.Command) error {
		cfg, err := mesh.SetEnabled(meshDir(), on)
		if err != nil {
			return err
		}
		switch {
		case !on:
			fmt.Println("已停用。重启 asa-server 服务（或 api）后生效。")
		case cfg.Coordinator == nil:
			fmt.Println("已启用（无协调节点模式：只按对端的手填地址直连）。重启 asa-server 服务（或 api）后生效。")
		default:
			fmt.Println("已启用。重启 asa-server 服务（或 api）后生效。")
		}
		return nil
	}
}

func actionMeshStatus(context.Context, *cli.Command) error {
	dir := meshDir()
	if id, ok := mesh.ExistingIdentity(dir); ok {
		fmt.Printf("本机节点 ID：%s\n", id)
	} else {
		fmt.Println("本机节点 ID：尚未生成（运行 asa-server mesh id 或 mesh join 时生成）")
	}
	cfg, err := mesh.LoadConfig(dir)
	if err != nil && !errors.Is(err, mesh.ErrNotConfigured) {
		return err
	}
	state := "已启用"
	if err != nil {
		state = "未启用"
	}
	fmt.Printf("状态：      %s\n", state)
	if cfg != nil && cfg.Coordinator != nil {
		fmt.Printf("协调节点：  %s（身份 %s），网络 %s\n", cfg.Coordinator.Addr, cfg.Coordinator.ID.Short(), cfg.Coordinator.NetworkID)
	} else {
		fmt.Println("协调节点：  未接入（asa-server mesh join <接入串>；或 mesh enable 进入无协调节点模式）")
	}
	if cfg != nil {
		if cfg.NoListen {
			fmt.Println("Peer 端口： 不监听（只能经中转被连接）")
		} else {
			fmt.Printf("Peer 端口： %d\n", cfg.ListenPort())
		}
		if len(cfg.PublicAddrs) > 0 {
			fmt.Printf("公网地址：  %s\n", strings.Join(cfg.PublicAddrs, ", "))
		}
		fmt.Printf("远程控制：  本机 %s 及以上可用\n", cfg.EffectiveControlRole())
	}
	fmt.Println("（实时连接状态见 GET /api/mesh/status；本命令不连协调节点。）")
	return nil
}

func actionMeshPeers(context.Context, *cli.Command) error {
	d, err := mesh.OpenPeerStore(meshDir()).Snapshot()
	if err != nil {
		return err
	}
	if len(d.Peers) == 0 {
		fmt.Println("对端：无")
	}
	for _, p := range d.Peers {
		granted := "未授权"
		if p.GrantedRole != "" {
			granted = "授予 " + p.GrantedRole
		}
		remote := "对方未授权本机"
		if p.RemoteRole != "" {
			remote = "对方授予本机 " + p.RemoteRole
		}
		fmt.Printf("%s  %-16s  %s；%s", p.NodeID, p.DisplayLabel(), granted, remote)
		if len(p.Addrs) > 0 {
			fmt.Printf("；直连地址 %s", strings.Join(p.Addrs, ", "))
		}
		fmt.Println()
	}
	if len(d.Requests) > 0 {
		fmt.Println("待批准申请：")
		for _, r := range d.Requests {
			fmt.Printf("  %s  %s（%s，来自 %s，%s）\n", r.NodeID, r.Label, r.Version, r.Addr, r.RequestedAt.Format(time.DateTime))
		}
		fmt.Println("  批准：asa-server mesh approve <节点 ID> --role operator|admin")
	}
	now := time.Now()
	for _, v := range d.Invites {
		if now.Before(v.ExpiresAt) {
			fmt.Printf("未用邀请码：%s（%s，%s 到期）%s\n", v.ID, v.Role, v.ExpiresAt.Format(time.DateTime), v.Note)
		}
	}
	return nil
}

func actionMeshInvite(_ context.Context, cmd *cli.Command) error {
	dir := meshDir()
	id, err := mesh.LoadIdentity(dir)
	if err != nil {
		return err
	}
	inv, rec, err := mesh.OpenPeerStore(dir).CreateInvite(id, mesh.InviteOptions{
		Role: cmd.String("role"), TTL: cmd.Duration("ttl"), Addrs: cmd.StringSlice("addr"), Note: cmd.String("note"),
	})
	if err != nil {
		return err
	}
	// 邀请码只打到标准输出、不进日志：它是一次性的授权凭据。
	fmt.Println(inv)
	fmt.Fprintf(cmd.Root().ErrWriter, "（角色 %s，%s 到期；整串只显示这一次。对方在页面「远程管理器」里粘贴，或调用 POST /api/mesh/pair）\n",
		rec.Role, rec.ExpiresAt.Format(time.DateTime))
	return nil
}

func parseNodeArg(cmd *cli.Command) (meshid.ID, error) {
	if cmd.Args().Len() != 1 {
		return meshid.ID{}, cli.Exit("需要一个节点 ID", 2)
	}
	return meshid.ParseID(cmd.Args().First())
}

func actionMeshRevoke(_ context.Context, cmd *cli.Command) error {
	id, err := parseNodeArg(cmd)
	if err != nil {
		return err
	}
	if err := mesh.OpenPeerStore(meshDir()).Revoke(id); err != nil {
		return err
	}
	fmt.Printf("已撤销对 %s 的授权。运行中的服务会在几秒内断开它的连接。\n", id.Short())
	return nil
}

func actionMeshApprove(_ context.Context, cmd *cli.Command) error {
	id, err := parseNodeArg(cmd)
	if err != nil {
		return err
	}
	if err := mesh.OpenPeerStore(meshDir()).ApproveRequest(id, cmd.String("role")); err != nil {
		return err
	}
	fmt.Printf("已批准 %s，授予 %s。\n", id.Short(), cmd.String("role"))
	return nil
}
