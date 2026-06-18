package actions

import (
	"asa-server/asaserver"
	"asa-server/logger"
	"context"
	"os"

	"github.com/urfave/cli/v3"
)

func ActionUpdate(ctx context.Context, cmd *cli.Command) error {
	logger.GetLogger().Info("Installing/updating base server...")

	stdoutFmt := os.Stdout
	// Download and extract SteamCMD
	if err := asaserver.DownloadAndExtractSteamCmd(ctx, stdoutFmt); err != nil {
		return err
	}

	// Download and update ARK server
	if err := asaserver.DownloadAndUpdateArkServer(ctx, stdoutFmt); err != nil {
		return err
	}

	// Get force-server flag
	forceServer := cmd.Bool("force-server")

	// Verify server installation by running it to generate config files
	if err := asaserver.VerifyServerInstallation(ctx, forceServer); err != nil {
		return err
	}

	logger.GetLogger().Info("Base server installation/update completed.")
	return nil
}
