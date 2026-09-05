//go:build windows

package runner

import "context"

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

// prepareSharedTree has nothing to do on Windows: the game process runs with
// the same identity as asa-server itself, so server-files and the instances
// directory are already writable by everyone who needs them.
func prepareSharedTree(root string) error { return nil }

// sharedTrees / sharedAccessStatus: same reason — there is no second identity
// to share anything with, so there are no shared trees and nothing to report.
func sharedTrees() []string { return nil }

func sharedAccessStatus() SharedAccessInfo { return SharedAccessInfo{} }

// runtimeHomeDir / runtimeUserName / RuntimeHomeDir / RuntimeUserName have no
// Windows counterpart at all, unlike everything above them. The no-ops in this
// file exist so that cross-platform callers (package main, svcmgr, installer)
// don't need build tags — but those four had exactly one caller each, in
// fixups_linux.go and service_linux.go, both already Linux-only. A no-op whose
// return value nobody reads buys nothing and advertises an API that means
// nothing here. Code that wants the runtime user's name on both platforms
// reads RuntimeUserStatus().Name.

func runtimeUserInfo() RuntimeUserInfo {
	return RuntimeUserInfo{Managed: false, Bypassed: false, Name: "", Ready: true}
}
