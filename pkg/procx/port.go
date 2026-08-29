package procx

import (
	"fmt"

	gnet "github.com/shirou/gopsutil/v4/net"
)

// PIDByPort looks up the PID of the process with a socket bound to port.
//
// Checks both TCP and UDP sockets ("all") in one pass — ARK's game port is
// UDP, its RCON port is TCP, and callers care about either. This replaces
// the netstat-based text parsing previously duplicated in
// internal/process/process_windows.go and this package's own
// GetPIDByPort: gopsutil's net.Connections already walks
// GetExtendedTcpTable/UdpTable on Windows and /proc/net/* on Linux.
func PIDByPort(port int) (int, error) {
	conns, err := gnet.Connections("all")
	if err != nil {
		return 0, fmt.Errorf("failed to list connections: %w", err)
	}
	for _, c := range conns {
		if int(c.Laddr.Port) == port && c.Pid > 0 {
			return int(c.Pid), nil
		}
	}
	return 0, fmt.Errorf("no process found listening on port %d", port)
}

// PortInUse reports whether anything holds a socket bound to port.
//
// Unlike PIDByPort this does not require the socket to be attributable to a
// PID: gopsutil resolves the owning process by walking /proc/*/fd for the
// socket inode, which can come back empty for a process in another mount
// namespace — precisely the case for the ARK server running inside
// pressure-vessel's container. Callers that only need "is the server serving
// yet" must not depend on that attribution succeeding.
//
// Deliberately NOT implemented by trying to bind the port: a probe that binds
// would race the very process it is waiting for, and could steal the port out
// from under it between two of its own retries.
func PortInUse(port int) (bool, error) {
	conns, err := gnet.Connections("all")
	if err != nil {
		return false, fmt.Errorf("failed to list connections: %w", err)
	}
	for _, c := range conns {
		if int(c.Laddr.Port) == port {
			return true, nil
		}
	}
	return false, nil
}
