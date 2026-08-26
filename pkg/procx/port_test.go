package procx

import (
	"net"
	"os"
	"testing"
)

// TestPIDByPort_TCP opens a real TCP listener on an ephemeral port and
// verifies PIDByPort resolves it back to this test process's own PID —
// exercising the actual OS connection table (gopsutil's net.Connections)
// instead of parsing captured tool output, on whichever platform runs it.
func TestPIDByPort_TCP(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open TCP listener: %v", err)
	}
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port
	pid, err := PIDByPort(port)
	if err != nil {
		t.Fatalf("PIDByPort(%d) failed: %v", port, err)
	}
	if pid != os.Getpid() {
		t.Errorf("PIDByPort(%d) = %d, want own pid %d", port, pid, os.Getpid())
	}
}

// TestPIDByPort_UDP covers the socket kind ARK's game port actually uses —
// RCON is TCP, but the connection kind passed to gopsutil ("all") must catch
// both, per docs/LINUX_COMPATIBILITY_PLAN.md §5.2.
func TestPIDByPort_UDP(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open UDP socket: %v", err)
	}
	defer conn.Close()

	port := conn.LocalAddr().(*net.UDPAddr).Port
	pid, err := PIDByPort(port)
	if err != nil {
		t.Fatalf("PIDByPort(%d) failed: %v", port, err)
	}
	if pid != os.Getpid() {
		t.Errorf("PIDByPort(%d) = %d, want own pid %d", port, pid, os.Getpid())
	}
}

// TestPIDByPort_NotFound asserts the "nothing listening" path returns an
// error rather than a zero/garbage PID.
func TestPIDByPort_NotFound(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open TCP listener: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close() // free the port immediately so nothing is bound to it

	if pid, err := PIDByPort(port); err == nil {
		t.Errorf("PIDByPort(%d) = %d, nil; want an error for an unbound port", port, pid)
	}
}
