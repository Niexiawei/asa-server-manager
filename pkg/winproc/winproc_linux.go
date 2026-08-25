//go:build linux

package winproc

import "errors"

// errNotImplemented 标记「编译期存根」——真正的 /proc 实现要到
// docs/LINUX_COMPATIBILITY_PLAN.md P1（pkg/winproc 改名 pkg/procx）才落地，
// 这里先保证依赖方能编译，调用即报错而不是编译失败。
var errNotImplemented = errors.New("winproc: not implemented on linux yet")

// IsProcessExited reports whether the process with the given PID has exited.
func IsProcessExited(pid uint32) (bool, error) {
	return false, errNotImplemented
}

// ProcessImageName returns the full path of the executable behind pid.
func ProcessImageName(pid uint32) (string, error) {
	return "", errNotImplemented
}

// GetPIDByPort looks up the PID of the process listening on port.
func GetPIDByPort(port int) (int, error) {
	return 0, errNotImplemented
}

// RunAsAdmin re-launches the current executable with elevated privileges.
// Linux has no equivalent concept; junction creation (this program's one
// historical reason to elevate) needs no privilege here either.
func RunAsAdmin(args string) error {
	return errNotImplemented
}

// QueryProcess searches processes by name and optional command-line substring.
func QueryProcess(name, commandLine string) ([]Win32Process, error) {
	return nil, errNotImplemented
}
