//go:build linux

package main

import (
	"context"
	"errors"

	"github.com/urfave/cli/v3"
)

// actionGUI has no Linux equivalent — the desktop GUI is Fyne-based and
// stays Windows-only (docs/LINUX_COMPATIBILITY_PLAN.md §5.9). Unreachable
// today since main() still refuses to run on non-Windows at startup, but
// needed so the "gui" CLI command and the no-args path type-check here too.
func actionGUI(ctx context.Context, cmd *cli.Command) error {
	return errors.New("GUI 仅在 Windows 上可用，请使用 asa-server api")
}
