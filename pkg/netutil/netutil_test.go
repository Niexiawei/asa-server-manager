package netutil

import (
	"fmt"
	"net"
	"testing"
)

func TestFreeUDPPort(t *testing.T) {
	port, err := FreeUDPPort()
	if err != nil {
		t.Fatalf("FreeUDPPort() error = %v", err)
	}

	if port <= 0 || port > 65535 {
		t.Errorf("端口 %d 超出合法范围", port)
	}

	// 兑现「空闲」这个承诺：返回的端口必须真的能被 UDP 绑定
	conn, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("FreeUDPPort() 返回的端口 %d 无法绑定: %v", port, err)
	}
	conn.Close()
}

// 内核不会立刻复用刚释放的临时端口，因此连续两次调用应拿到不同端口。
// 若这条失败，说明同一个端口会被连发两次，并发调用方就会撞车。
func TestFreeUDPPortNotRepeated(t *testing.T) {
	first, err := FreeUDPPort()
	if err != nil {
		t.Fatalf("FreeUDPPort() error = %v", err)
	}
	second, err := FreeUDPPort()
	if err != nil {
		t.Fatalf("FreeUDPPort() error = %v", err)
	}

	if first == second {
		t.Errorf("连续两次返回了同一个端口 %d", first)
	}
}

func TestResolveDomainToIPv4(t *testing.T) {
	ips, err := ResolveDomainToIPv4("asa.nicoi.cn")
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Println(ips)
}
