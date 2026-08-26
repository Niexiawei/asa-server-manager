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
