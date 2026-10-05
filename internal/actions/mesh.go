package actions

import (
	"context"
	"errors"
	"fmt"

	"github.com/urfave/cli/v3"

	cfgpkg "asa-server/internal/config"
	"asa-server/internal/mesh"
)

// MeshCommand 是管理器互控的最小 CLI（docs/REMOTE_MANAGER_MESH_PLAN.md §12 P1-7）。
//
// 这些命令**不连协调节点**：CLI 进程与正在运行的服务共用同一个身份，一连就会把服务的会话
// 踢掉（协调节点的单会话规则）。所以 join / leave 只写配置，生效要重启服务；status 只读本地文件。
func MeshCommand() *cli.Command {
	return &cli.Command{
		Name:  "mesh",
		Usage: "管理器互控：本机身份与协调节点接入",
		Commands: []*cli.Command{
			{
				Name:   "id",
				Usage:  "打印本机节点 ID（首次运行时生成身份）",
				Action: actionMeshID,
			},
			{
				Name:      "join",
				Usage:     "用协调节点给出的接入串接入（写入配置，重启服务后生效）",
				ArgsUsage: "<join blob>",
				Action:    actionMeshJoin,
			},
			{
				Name:   "leave",
				Usage:  "断开与协调节点的接入（保留本机身份，重启服务后生效）",
				Action: actionMeshLeave,
			},
			{
				Name:   "status",
				Usage:  "查看本地配置（只读本地文件，不连协调节点）",
				Action: actionMeshStatus,
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
	fmt.Println("重启 asa-server 服务（或 api）后生效。")
	return nil
}

func actionMeshLeave(context.Context, *cli.Command) error {
	if err := mesh.Leave(meshDir()); err != nil {
		return err
	}
	fmt.Println("已断开协调节点接入（本机身份保留）。重启 asa-server 服务（或 api）后生效。")
	return nil
}

func actionMeshStatus(context.Context, *cli.Command) error {
	dir := meshDir()
	if id, ok := mesh.ExistingIdentity(dir); ok {
		fmt.Printf("本机节点 ID：%s\n", id)
	} else {
		fmt.Println("本机节点 ID：尚未生成（运行 asa-server mesh id 或 mesh join 时生成）")
	}
	cfg, err := mesh.LoadConfig(dir)
	switch {
	case err == nil:
		fmt.Printf("协调节点：  %s（身份 %s）\n网络：      %s\n状态：      已启用\n",
			cfg.Coordinator.Addr, cfg.Coordinator.ID.Short(), cfg.Coordinator.NetworkID)
	case errors.Is(err, mesh.ErrNotConfigured):
		fmt.Println("协调节点：  未接入（asa-server mesh join <接入串>）")
	default:
		return err
	}
	fmt.Println("（实时连接状态见 GET /api/mesh/status；本命令不连协调节点。）")
	return nil
}
