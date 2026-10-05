// asa-coordinator 是管理器互控的协调节点：在线登记、交换候选地址、字节中转、STUN。
// 独立部署在公网（通常是一台小 VPS），与 asa-server 同一个 Go 模块、互不依赖配置。
// 见 docs/REMOTE_MANAGER_MESH_PLAN.md §8、§12 P1-5。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	"asa-server/internal/meshcoord"
)

// version 由构建时注入：-ldflags "-X main.version=x.y.z"。
var version = "dev"

// exitConfigUnusable 是 sysexits.h 的 EX_CONFIG，与 asa-server 一致：配置缺失或无效，重试不会好。
const exitConfigUnusable = 78

func main() {
	app := &cli.Command{
		Name:    "asa-coordinator",
		Usage:   "ASA Server Manager 的协调节点（管理器互控的牵线与中转）",
		Version: version,
		Flags:   []cli.Flag{configFlag()},
		// 不带子命令 = run，方便直接双击 / 写进最简单的启动脚本。
		Action: actionRun,
		Commands: []*cli.Command{
			{
				Name:   "run",
				Usage:  "运行协调节点（默认动作）",
				Flags:  []cli.Flag{configFlag()},
				Action: actionRun,
			},
			configCommand(),
			{
				Name:  "join-blob",
				Usage: "打印接入串（含网络密钥，请只发给要接入的管理器）",
				Flags: []cli.Flag{
					configFlag(),
					&cli.StringFlag{Name: "network", Value: meshcoord.DefaultNetworkID, Usage: "网络 ID"},
				},
				Action: actionJoinBlob,
			},
			nodeCommand(),
			serviceCommand(),
			stunCommand(),
		},
	}
	if err := app.Run(context.Background(), os.Args); err != nil {
		var exitErr cli.ExitCoder
		if errors.As(err, &exitErr) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func configFlag() cli.Flag {
	return &cli.StringFlag{
		Name:    "config",
		Aliases: []string{"c"},
		Usage:   "配置文件路径（默认依次查找：程序所在目录、/etc/asa-coordinator/）",
	}
}

// loadConfig 按查找顺序加载并校验配置；失败时以退出码 78 结束。警告打到 stderr。
func loadConfig(cmd *cli.Command) (*meshcoord.Config, error) {
	cfg, warnings, err := meshcoord.LocateAndLoad(cmd.String("config"))
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "警告：", w)
	}
	if err != nil {
		return nil, cli.Exit(err.Error(), exitConfigUnusable)
	}
	return cfg, nil
}

func actionJoinBlob(_ context.Context, cmd *cli.Command) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	blob, err := meshcoord.JoinBlob(cfg, cmd.String("network"))
	if err != nil {
		return err
	}
	fmt.Println(blob)
	fmt.Fprintln(os.Stderr, "\n在每台管理器上运行：asa-server mesh join <上面这一行>")
	return nil
}
