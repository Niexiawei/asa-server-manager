//go:build windows

package main

import (
	"asa-server/internal/gui"
	"context"

	"github.com/urfave/cli/v3"
)

var platformCommands = []*cli.Command{
	{
		Name:   "gui",
		Usage:  "Start GUI mode",
		Action: actionGUI,
	},
}

func Commands() []*cli.Command {
	return append(
		commonCommands,
		platformCommands...,
	)
}

// actionGUI starts the GUI application
func actionGUI(ctx context.Context, cmd *cli.Command) error {
	guiApp := gui.NewGUIApp()
	guiApp.Run()
	return nil
}

// runDefaultAction is what a no-argument invocation does on Windows: launch
// the GUI, exactly as before the Linux no-args path was added.
func runDefaultAction(ctx context.Context) error {
	return actionGUI(ctx, nil)
}
