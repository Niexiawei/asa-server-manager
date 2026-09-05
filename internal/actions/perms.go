package actions

import (
	"context"
	"fmt"
	"time"

	"asa-server/internal/runner"

	"github.com/urfave/cli/v3"
)

// PermsCommand 管理「asa-server 与降权游戏进程共享写权限」的那几棵目录树。
//
// 这条命令只服务一种场景：**带外变更** —— 管理员用 SFTP / scp 直接往
// server-files 里传了 mod 包或 ArkApi 插件。程序自己创建的目录不归它管，
// 那些在实例启动流程里已经自动处理（见 internal/instance/server.go 里
// runner.Run 之前那段循环，以及 docs/ACL_PERMISSION_HARDENING_PLAN.md §3.2）。
//
// 这条命令只在 `main_linux.go` 的 `platformCommands` 里注册，Windows 上不存在
// —— 那里游戏与 asa-server 同一身份，没有"共享"可言。所以本文件里不需要、也不该
// 有 `runtime.GOOS` 判断（`prefix.go` 曾经有过两段，两边都到不了）。
func PermsCommand() *cli.Command {
	return &cli.Command{
		Name:  "perms",
		Usage: "查看/修复 server-files 与 instances 的共享写权限（Linux 降权运行时）",
		Commands: []*cli.Command{
			{
				Name:   "status",
				Usage:  "只读报告：运行时用户、ACL 可用性、各目录树当前状态",
				Action: actionPermsStatus,
			},
			{
				Name: "fix",
				Usage: "重新施加共享写权限（以 root 上传过 mod / 插件后用）；" +
					"有 ACL 时用组+setgid+默认 ACL，否则退回 chown",
				Action: actionPermsFix,
			},
		},
	}
}

// 两个子命令都**不**调 VerifyEnvironmentReady：那个检查要求 SteamCMD 与服务端
// 本体都已安装，而权限诊断恰恰经常发生在环境没装好的时候（安装过程本身就可能
// 因为权限而失败）。BaseDir 与 runner 配置由 main() 在 CLI 分发前统一装配，
// 这两条命令需要的前提仅此而已。
func actionPermsStatus(ctx context.Context, cmd *cli.Command) error {
	info := runner.SharedAccessStatus()
	if !info.Managed {
		fmt.Println("当前不涉及降权运行：游戏进程与 asa-server 使用同一身份，无需共享写权限处理。")
		fmt.Println("（非 root 启动、或 linux.umu_run_as_root=true 时就是这种情况。）")
		return nil
	}

	fmt.Printf("运行时用户：%s (uid=%d gid=%d，属组 %s)\n", info.User, info.UID, info.GID, info.Group)
	switch info.Model() {
	case "acl":
		fmt.Printf("ACL 支持：  可用 (%s)\n", info.ACLTool)
		fmt.Println("权限模型：  方案 B（组 + setgid + 默认 ACL）—— 新文件在创建瞬间即继承，无需事后修复")
	default:
		fmt.Printf("ACL 支持：  不可用（%s）\n", info.ACLError)
		fmt.Println("权限模型：  方案 A（chown 兜底）—— 能用，但以 root 新建的文件游戏写不了，")
		fmt.Println("            需要重启 asa-server、重跑 update，或执行 asa-server perms fix")
	}
	fmt.Println()

	allGood := true
	for _, t := range info.Trees {
		fmt.Printf("  %s\n", t.Path)
		if !t.Exists {
			fmt.Println("    （不存在，尚未创建）")
			continue
		}
		fmt.Printf("    属组/权限位  %s\n", checkMark(t.Prepared))
		if info.ACLTool != "" {
			fmt.Printf("    默认 ACL     %s  (default:group:%s:rwx)\n", checkMark(t.DefaultACL), info.Group)
			if !t.Prepared || !t.DefaultACL {
				allGood = false
			}
		} else if !t.Prepared {
			allGood = false
		}
	}

	fmt.Println()
	if allGood {
		fmt.Println("结论：全部就绪。")
	} else {
		fmt.Println("结论：有条目未就绪，执行 asa-server perms fix 修复。")
	}
	return nil
}

func actionPermsFix(ctx context.Context, cmd *cli.Command) error {
	info := runner.SharedAccessStatus()
	if !info.Managed {
		fmt.Println("当前不涉及降权运行，无需修复。")
		return nil
	}

	trees := runner.SharedTrees()
	if len(trees) == 0 {
		fmt.Println("没有需要处理的目录树（server-files / instances 尚未创建）。")
		return nil
	}

	if info.ACLTool == "" {
		fmt.Printf("提示：POSIX ACL 不可用（%s），本次只能按方案 A（chown）修复。\n", info.ACLError)
		fmt.Println("      装上 acl 包后重跑本命令即可升级到方案 B，之后带外上传无需再手动修复。")
		fmt.Println()
	}

	for _, tree := range trees {
		// server-files 有约 5 万个条目，遍历要几秒；先打印再动手，
		// 否则用户会以为卡住了。
		fmt.Printf("处理 %s ... ", tree)
		start := time.Now()
		if err := runner.PrepareSharedTree(tree); err != nil {
			fmt.Println("失败")
			return fmt.Errorf("处理 %s 失败: %w", tree, err)
		}
		fmt.Printf("完成（%s）\n", time.Since(start).Round(time.Millisecond))
	}

	fmt.Println("\n共享写权限已重新施加。用 asa-server perms status 复查。")
	return nil
}

func checkMark(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}
