package actions

import (
	"asa-server/internal/installer"
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

// VerifyCommand 是 `asa-server verify`：单独重跑「拉起一次服务端生成默认配置」这一步，
// 强制执行（force=true），配置目录已存在也照跑。
//
// `update` 的最后一步做的是同一件事，但前面压着一整轮 SteamCMD 下载/校验。把它拆出来
// 在 Linux 上尤其有用：那条路径要经过 umu-run → bwrap → Proton → Wine，是整条链路上
// 最容易出问题的一段，而这条命令是「这台机器到底能不能把 ArkAscendedServer.exe 拉起来」
// 最便宜的答案——几十秒，不碰网络。
//
// 注意它会真的启动一个服务端进程（占一个随机空闲端口，几分钟后自行结束），
// 内部与 update 共用同一把 server-files 锁：有实例在跑时会被拒绝，不会打架。
func VerifyCommand() *cli.Command {
	return &cli.Command{
		Name: "verify",
		Usage: "拉起一次 ARK 服务端，验证启动链路并重新生成默认配置" +
			"（等价于 update 的最后一步，可单独重跑；Linux 上用来验证 Wine/Proton）",
		Action: ActionVerify,
	}
}

func ActionVerify(ctx context.Context, cmd *cli.Command) error {
	fmt.Println("正在强制重跑服务端启动验证（首次运行可能需要几分钟）...")

	if err := installer.VerifyServerInstallation(ctx, true, os.Stdout); err != nil {
		return fmt.Errorf("服务端启动验证失败: %w", err)
	}

	fmt.Println("服务端启动验证通过。")
	return nil
}
