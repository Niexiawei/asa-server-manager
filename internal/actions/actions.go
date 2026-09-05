package actions

import (
	"asa-server/internal/installer"
	"asa-server/internal/instance"
	statepkg "asa-server/internal/state"
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

func ActionUpdate(ctx context.Context, cmd *cli.Command) error {
	fmt.Println("Installing/updating base server...")

	stdoutFmt := os.Stdout
	// Download and extract SteamCMD
	if err := installer.DownloadAndExtractSteamCmd(ctx, stdoutFmt); err != nil {
		return err
	}

	// Download and update ARK server
	if err := installer.DownloadAndUpdateArkServer(ctx, stdoutFmt); err != nil {
		return err
	}

	// Get force-server flag
	forceServer := cmd.Bool("force-server")

	// Verify server installation by running it to generate config files
	if err := installer.VerifyServerInstallation(ctx, forceServer, stdoutFmt); err != nil {
		return err
	}

	// 更新必然换掉 exe，也就换掉 ArkApi offsets cache 的哈希。趁用户还在等，把新
	// 版本的缓存备好，省掉「更新后第一次启动」那一次看起来像卡住的等待。永不致命。
	instance.PrefetchArkApiCacheAfterUpdate(ctx, stdoutFmt)

	fmt.Println("Base server installation/update completed.")
	return nil
}

// ActionStateClear 清空状态数据库
func ActionStateClear(ctx context.Context, cmd *cli.Command) error {
	fmt.Println("Clearing state database...")
	if err := statepkg.ClearStateDatabase(); err != nil {
		return fmt.Errorf("failed to clear state database: %w", err)
	}
	fmt.Println("State database cleared successfully.")
	return nil
}
