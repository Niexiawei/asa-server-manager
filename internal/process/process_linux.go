//go:build linux

package process

import "errors"

// errNotImplemented 标记「编译期存根」——Linux 运行时支持要到
// docs/LINUX_COMPATIBILITY_PLAN.md P1（端口→PID 切 gopsutil）才落地，
// 这里先保证依赖方能编译，调用即报错而不是编译失败。
var errNotImplemented = errors.New("process: not implemented on linux yet")

// IsServerRunning checks if a server instance is running by verifying its game
// port is listening. Stub pending the P1 gopsutil-based port->PID lookup that
// replaces both this and pkg/winproc.GetPIDByPort.
func IsServerRunning(instanceName string) (bool, error) {
	return false, errNotImplemented
}
