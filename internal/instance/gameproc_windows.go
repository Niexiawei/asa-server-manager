//go:build windows

package instance

import "asa-server/pkg/procx"

// queryGameProcesses finds the running ArkAscendedServer.exe.
//
// Windows keeps the image-name filter it has always used: the exe runs
// directly, so Win32_Process.Name really is "ArkAscendedServer.exe" and the
// WMI query narrows the scan before the command-line LIKE ever runs. The
// Linux build needs an entirely different rule (see gameproc_linux.go), which
// is the only reason this is a platform-split function at all.
func queryGameProcesses(cmdlineMarker string) ([]procx.Win32Process, error) {
	return procx.QueryProcess(arkExeName, cmdlineMarker)
}
