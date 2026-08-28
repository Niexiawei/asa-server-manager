//go:build windows

package runner

import (
	"context"
	"os"
)

// Windows has no "run the game as a dedicated non-root user" story: the
// service runs as LocalSystem and privilege separation for the child would be
// a different API (CreateProcessAsUser + a restricted token), out of scope —
// see docs/UMU_RUNTIME_USER_PLAN.md §2 non-goals. Every entry point below is
// a no-op so package main / svcmgr / installer don't need build tags.

func ensureRuntimeUser(ctx context.Context) error { return nil }

func verifyRuntimeAccess(forceDeep bool) []Problem { return nil }

func runtimeUserProblems() []Problem { return nil }

func chownMirrorForRuntime(mirrorDir string) error { return nil }

func chownTreeForRuntime(root string) error { return nil }

func runtimeHomeDir(cfg Config) string {
	h, _ := os.UserHomeDir()
	return h
}

func runtimeUserName(cfg Config) string { return "" }

// RuntimeUserName has no meaning on Windows.
func RuntimeUserName() string { return "" }

func runtimeUserInfo() RuntimeUserInfo {
	return RuntimeUserInfo{Managed: false, Bypassed: false, Name: "", Ready: true}
}
