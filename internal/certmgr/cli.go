package certmgr

import (
	"asa-server/pkg/procx"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/urfave/cli/v3"
)

// ActionCertStatus 打印本地 CA 与服务器证书的现状
func ActionCertStatus(ctx context.Context, cmd *cli.Command) error {
	fmt.Printf("证书目录: %s\n\n", CertsDir())

	caCert, caDER, err := readCertPEM(caCertPath())
	if err != nil {
		fmt.Println("本地 CA: 尚未生成（首次以 --tls 启动 API 服务器时自动创建）")
		return nil
	}

	fingerprint := Fingerprint(caDER)
	fmt.Println("本地 CA")
	fmt.Printf("  主题:   %s\n", caCert.Subject.CommonName)
	fmt.Printf("  指纹:   %s\n", fingerprint)
	fmt.Printf("  有效期: %s ~ %s\n",
		caCert.NotBefore.Format("2006-01-02"), caCert.NotAfter.Format("2006-01-02"))

	if loc, ok := findTrusted(fingerprint); ok {
		fmt.Printf("  受信任: 是（%s\\Root）\n", loc)
	} else {
		fmt.Println("  受信任: 否 —— 浏览器会提示证书警告，执行 `asa-server cert install` 安装")
	}

	leaf, _, err := readCertPEM(leafCertPath())
	if err != nil {
		fmt.Println("\n服务器证书: 尚未签发")
		return nil
	}
	fmt.Println("\n服务器证书")
	fmt.Printf("  有效期: %s ~ %s\n",
		leaf.NotBefore.Format("2006-01-02"), leaf.NotAfter.Format("2006-01-02"))
	fmt.Printf("  SAN:    %s\n", sanKey(leaf.DNSNames, leaf.IPAddresses))
	if time.Until(leaf.NotAfter) <= renewBefore {
		fmt.Println("  提示:   即将过期，下次启动会自动重新签发")
	}

	return nil
}

// ActionCertInstall 手动把本地 CA 装进受信任根存储。
//
// 未提权时先尝试 CurrentUser；用户显式要求装到 LocalMachine（--machine）才请求提权重启，
// 免得一个查看性质的命令动不动就弹 UAC。
func ActionCertInstall(ctx context.Context, cmd *cli.Command) error {
	// CA 不存在就顺手建一个：生成是幂等的，没必要逼用户先去起一次 API 服务器
	if err := os.MkdirAll(CertsDir(), 0700); err != nil {
		return fmt.Errorf("创建证书目录失败: %w", err)
	}
	if _, err := ensureCA(); err != nil {
		return err
	}

	if cmd.Bool("machine") && !IsElevated() {
		fmt.Println("安装到 LocalMachine 需要管理员权限，正在请求提权...")
		if err := procx.RunAsAdmin("cert install --machine"); err != nil {
			return fmt.Errorf("提权失败: %w", err)
		}
		os.Exit(0)
	}

	if err := TrustCA(); err != nil {
		return fmt.Errorf("安装本地 CA 失败: %w", err)
	}
	fmt.Println("本地 CA 已安装到受信任根存储")
	return nil
}

// ActionCertUninstall 从受信任根存储移除本地 CA
func ActionCertUninstall(ctx context.Context, cmd *cli.Command) error {
	if err := UntrustCA(); err != nil {
		return fmt.Errorf("移除本地 CA 失败: %w", err)
	}
	fmt.Println("本地 CA 已从受信任根存储移除")
	return nil
}

// Command 返回 `asa-server cert` 子命令树
func Command() *cli.Command {
	return &cli.Command{
		Name:  "cert",
		Usage: "Manage the local HTTPS certificate authority",
		Commands: []*cli.Command{
			{
				Name:   "status",
				Usage:  "Show local CA / server certificate status",
				Action: ActionCertStatus,
			},
			{
				Name:  "install",
				Usage: "Install the local CA into the Windows trusted root store",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "machine",
						Usage: "Install for all users (LocalMachine, requires administrator)",
					},
				},
				Action: ActionCertInstall,
			},
			{
				Name:   "uninstall",
				Usage:  "Remove the local CA from the Windows trusted root store",
				Action: ActionCertUninstall,
			},
		},
	}
}
