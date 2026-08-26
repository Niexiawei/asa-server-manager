//go:build windows

package process

import (
	"asa-server/pkg/procx"
	"path/filepath"
	"strings"
)

// isExpectedProcessPlatform checks pid's image (executable file) name
// against expectedExecutables — this is exactly what isExpectedProcess did
// before docs/LINUX_COMPATIBILITY_PLAN.md P4 split it by platform; Windows
// behavior is unchanged.
func isExpectedProcessPlatform(pid uint32) bool {
	imagePath, err := procx.ProcessImageName(pid)
	if err != nil {
		return false
	}
	return expectedExecutables[strings.ToLower(filepath.Base(imagePath))]
}
