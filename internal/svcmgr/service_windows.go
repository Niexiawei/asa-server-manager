//go:build windows

package svcmgr

import "github.com/kardianos/service"

// configurePlatform is a no-op on Windows: the SCM already runs the service
// as LocalSystem with no working-directory/environment quirks to work around,
// so behavior here is exactly what it was before the winservice->svcmgr
// rename (docs/LINUX_COMPATIBILITY_PLAN.md §5.8).
func configurePlatform(cfg *service.Config) {}

// warnBeforeInstall is a no-op on Windows: LocalSystem is the normal,
// expected way to run a Windows service, unlike root on Linux.
func warnBeforeInstall() {}

// finalizePlatform is a no-op on Windows: kardianos writes EnvVars into the
// service's registry key as REG_MULTI_SZ entries, where spaces in values need
// no quoting.
func finalizePlatform(cfg *service.Config) error { return nil }
