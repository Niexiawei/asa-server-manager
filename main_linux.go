//go:build linux

package main

import (
	"asa-server/internal/webapi"
	"context"
	"errors"

	"github.com/urfave/cli/v3"
)

// actionGUI has no Linux equivalent — the desktop GUI is Fyne-based and
// stays Windows-only (docs/LINUX_COMPATIBILITY_PLAN.md §5.9).
func actionGUI(ctx context.Context, cmd *cli.Command) error {
	return errors.New("GUI 仅在 Windows 上可用，请使用 asa-server api")
}

// runDefaultAction is what a no-argument invocation does on Linux: there's
// no GUI to fall back to, so it's equivalent to `asa-server api`
// (docs/LINUX_COMPATIBILITY_PLAN.md §5.9).
func runDefaultAction(ctx context.Context) error {
	return webapi.ActionAPI(ctx, nil)
}
