package auth

import (
	"net"
	"testing"

	"asa-server/internal/appconfig"
)

func TestIsInNetworks(t *testing.T) {
	nets := appconfig.DefaultPrivateNetworks

	private := []string{
		"127.0.0.1", "127.5.5.5",
		"10.0.0.1", "10.255.255.254",
		"172.16.0.1", "172.31.255.254",
		"192.168.1.1", "192.168.255.254",
		"169.254.1.1",
		"::1",
		"fc00::1", "fd12:3456::1",
		"fe80::1",
	}
	for _, s := range private {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("测试数据有误，无法解析 %q", s)
		}
		if !IsInNetworks(ip, nets) {
			t.Errorf("%s 应被判定为内网", s)
		}
	}

	public := []string{
		"8.8.8.8", "1.1.1.1",
		"172.32.0.1",  // 刚好在 172.16/12 之外
		"172.15.0.1",  // 刚好在 172.16/12 之前
		"192.169.0.1", // 刚好在 192.168/16 之外
		"11.0.0.1",
		"2001:4860:4860::8888",
		"2400:cb00::1",
	}
	for _, s := range public {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("测试数据有误，无法解析 %q", s)
		}
		if IsInNetworks(ip, nets) {
			t.Errorf("%s 不应被判定为内网", s)
		}
	}
}

// IPv4-mapped IPv6 是这里最容易出错的地方：::ffff:8.8.8.8 长得像
// 一串前缀全零的 IPv6，但它语义上就是公网的 8.8.8.8，绝不能放行。
func TestIsInNetworksIPv4Mapped(t *testing.T) {
	nets := appconfig.DefaultPrivateNetworks

	if IsInNetworks(net.ParseIP("::ffff:8.8.8.8"), nets) {
		t.Error("::ffff:8.8.8.8 是公网地址，不得被判定为内网")
	}
	// 反过来，映射过来的内网地址仍应被认成内网
	if !IsInNetworks(net.ParseIP("::ffff:192.168.1.1"), nets) {
		t.Error("::ffff:192.168.1.1 语义上就是 192.168.1.1，应判定为内网")
	}
}

func TestIsInNetworksRejectsBadConfig(t *testing.T) {
	// 配置在 appconfig.Validate 阶段就该被拦下。万一有调用方绕过校验，
	// 这里必须判定为"不是内网"——免鉴权比误拦截危险得多。
	if IsInNetworks(net.ParseIP("127.0.0.1"), []string{"这不是网段"}) {
		t.Error("网段配置非法时必须判定为非内网（fail closed）")
	}
}

func TestParseNetworksAcceptsBareIP(t *testing.T) {
	set, err := ParseNetworks([]string{"192.168.1.5", "::1"})
	if err != nil {
		t.Fatalf("ParseNetworks: %v", err)
	}
	if !set.Contains(net.ParseIP("192.168.1.5")) {
		t.Error("裸 IP 应被补成单主机网段并命中自身")
	}
	if set.Contains(net.ParseIP("192.168.1.6")) {
		t.Error("裸 IP 补成的网段不应命中邻居地址")
	}
	if !set.Contains(net.ParseIP("::1")) {
		t.Error("::1 应命中")
	}
}

func TestHostIP(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:54321":  "127.0.0.1",
		"[::1]:54321":      "::1",
		"192.168.1.1:8080": "192.168.1.1",
		"127.0.0.1":        "127.0.0.1", // 少数场景下没有端口
	}
	for in, want := range cases {
		got := HostIP(in)
		if got == nil {
			t.Errorf("HostIP(%q) 返回 nil，期望 %s", in, want)
			continue
		}
		if !got.Equal(net.ParseIP(want)) {
			t.Errorf("HostIP(%q) = %v，期望 %s", in, got, want)
		}
	}
	if HostIP("not-an-address") != nil {
		t.Error("无法解析的地址应返回 nil")
	}
}
