// Package procx provides cross-platform process primitives: liveness checks,
// image-name lookup, process-by-name/cmdline queries, port->PID resolution,
// and graceful/forceful termination. Windows implementations (including
// Terminate/Kill/TerminateTree/KillTree) live in procx_windows.go /
// wmi_windows.go (Win32 API + WMI); Linux implementations live in
// procx_linux.go (/proc scanning + signals). port.go carries PIDByPort,
// shared verbatim across both platforms via gopsutil.
package procx

import (
	"context"
	"time"
)

// Win32Process is a subset of Win32_Process WMI properties, returned by
// QueryProcess. 字段名必须与 WMI 属性名一致：wmi.Query 靠反射按名字回填。
type Win32Process struct {
	Name      string
	ProcessId uint32
	// CommandLine 在 WMI 里可能是 NULL（权限不足或系统进程），
	// 此时 wmi 库跳过该字段，保持零值空串。
	CommandLine string
}

// ProcessCmdline returns the full command line of the process with the
// given pid, in the same flattened (NUL/argument-separators-as-spaces)
// shape QueryProcess's CommandLine field uses. Windows: WMI lookup by
// ProcessId. Linux: /proc/<pid>/cmdline.
func ProcessCmdline(pid uint32) (string, error) {
	return processCmdline(pid)
}

// WaitProcessExit blocks until the process with the given pid has exited or ctx is done.
// It polls process liveness every interval. Returns true once the process has exited,
// false if ctx was cancelled first.
//
// NOTE: previously this existed as two同名 functions with different poll intervals
// (asaserver 500ms for the ARK game process, common 2s for syncthing). They are unified
// here with an explicit interval parameter so callers keep their original cadence.
func WaitProcessExit(ctx context.Context, pid int, interval time.Duration) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(interval):
			if exited, _ := IsProcessExited(uint32(pid)); exited {
				return true
			}
		}
	}
}
