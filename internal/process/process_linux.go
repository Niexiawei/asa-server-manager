//go:build linux

package process

import (
	"asa-server/pkg/procx"
	"strings"
)

// isExpectedProcessPlatform checks pid's cmdline against expectedExecutables
// instead of its image name: under Wine, /proc/<pid>/exe points at wine's
// own binary (wine-preloader or Proton's wine64), not the Windows exe
// actually running inside it, so image-name matching (the Windows
// implementation) would always fail here. See
// docs/LINUX_COMPATIBILITY_PLAN.md §5.3 item 4.
func isExpectedProcessPlatform(pid uint32) bool {
	cmdline, err := procx.ProcessCmdline(pid)
	if err != nil {
		return false
	}
	lower := strings.ToLower(cmdline)
	for exe := range expectedExecutables {
		if strings.Contains(lower, exe) {
			return true
		}
	}
	return false
}
