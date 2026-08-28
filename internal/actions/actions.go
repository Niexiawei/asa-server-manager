package actions

import (
	"asa-server/internal/installer"
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
