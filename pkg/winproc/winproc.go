// Package winproc provides Windows process/window primitives (and, on
// Linux, compile-time stubs for the same signatures — see winproc_linux.go).
package winproc

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
