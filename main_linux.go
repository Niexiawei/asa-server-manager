//go:build linux

package main

import (
	"asa-server/internal/actions"
	"asa-server/internal/webapi"
	"context"
	"errors"
	"fmt"

	"github.com/urfave/cli/v3"
)

// actionGUI has no Linux equivalent — the desktop GUI is Fyne-based and
// stays Windows-only (docs/LINUX_COMPATIBILITY_PLAN.md §5.9).
func actionGUI(ctx context.Context, cmd *cli.Command) error {
	return errors.New("GUI 仅在 Windows 上可用，请使用 asa-server api")
}

// runDefaultAction is what a no-argument invocation does on Linux: there's
// no GUI to fall back to, so it's equivalent to `asa-server api`
// (docs/LINUX_COMPATIBILITY_PLAN.md §5.9). It runs the same base-environment
// gate as the explicit `api` subcommand (docs/SETUP_FLOW_OPTIMIZATION_PLAN.md
// §3.3) — a bare `asa-server` on a fresh box should point at `setup`, not
// silently start an API server no instance can use.
func runDefaultAction(ctx context.Context) error {
	if err := actions.VerifyEnvironmentReady(); err != nil {
		return fmt.Errorf("%w\n\n（如确需继续启动，运行 asa-server api --skip-env-check）", err)
	}
	return webapi.ActionAPI(ctx, nil)
}
