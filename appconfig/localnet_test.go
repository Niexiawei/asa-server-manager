package appconfig

import (
	"net"
	"testing"
)

func TestLooksVirtual(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"Ethernet", false},
		{"Wi-Fi", false},
		{"以太网", false},
		{"Local Area Connection", false},
		{"vEthernet (WSL)", true},
		{"vEthernet (Default Switch)", true},
		{"Docker Desktop Virtual Ethernet Adapter", true},
		{"TAP-Windows Adapter V9", true},
		{"Tailscale", true},
		{"ZeroTier One [4bd77786]", true},
		{"Microsoft Wi-Fi Direct Virtual Adapter", true},
		{"WireGuard Tunnel", true},
		{"VMware Network Adapter VMnet8", true},
		{"VirtualBox Host-Only Network", true},
	}
	for _, c := range cases {
		t.Log(c)
		if got := looksVirtual(c.name); got != c.want {
			t.Errorf("looksVirtual(%q) = %v，期望 %v", c.name, got, c.want)
		}
	}
}

// 不假设测试机的具体网络拓扑，只断言探测结果格式正确、不 panic。
func TestDetectLocalPrivateSubnetsReturnsValidCIDRs(t *testing.T) {
	subnets := detectLocalPrivateSubnets()
	for _, s := range subnets {
		if _, _, err := net.ParseCIDR(s); err != nil {
			t.Errorf("detectLocalPrivateSubnets() 返回了非法 CIDR %q: %v", s, err)
		}
	}
}
