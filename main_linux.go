//go:build linux

package main

import (
	"asa-server/internal/actions"
	"asa-server/internal/webapi"
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

var platformCommands = []*cli.Command{
	actions.PermsCommand(),
	actions.PrefixCommand(),
}

func Commands() []*cli.Command {
	return append(
		commonCommands,
		platformCommands...,
	)
}

// runDefaultAction is what a no-argument invocation does on Linux: there's
// no GUI to fall back to, so it's equivalent to `asa-server api`
// (docs/LINUX_COMPATIBILITY_PLAN.md §5.9). It runs the same base-environment
// gate as the explicit `api` subcommand (docs/SETUP_FLOW_OPTIMIZATION_PLAN.md
// §3.3) — a bare `asa-server` on a fresh box should point at `setup`, not
// silently start an API server no instance can use.
func runDefaultAction(ctx context.Context) error {
	enforceRuntimeUserGate()
	if err := actions.VerifyEnvironmentReady(); err != nil {
		return fmt.Errorf("%w\n\n（如确需继续启动，运行 asa-server api --skip-env-check）", err)
	}
	return webapi.ActionAPI(ctx, nil)
}
