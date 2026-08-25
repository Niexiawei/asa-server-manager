//go:build windows

package main

import (
	"asa-server/internal/gui"
	"context"

	"github.com/urfave/cli/v3"
)

// actionGUI starts the GUI application
func actionGUI(ctx context.Context, cmd *cli.Command) error {
	guiApp := gui.NewGUIApp()
	guiApp.Run()
	return nil
}
