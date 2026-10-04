package netutil

import (
	"fmt"
	"net"
	"slices"
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

// 三个解析函数按地址族筛选。输入用字面 IP：net.LookupIP 对字面 IP 直接返回，
// 不发任何 DNS 查询，断网、DNS 受限的机器上结果也一样。以前这里解析的是一个真实
// 公网域名、只打印不断言（docs/TEST_ENV_COUPLING_PLAN.md T4）。
func TestResolveDomainFiltersByFamily(t *testing.T) {
	cases := []struct {
		in             string
		all, ipv4, ip6 []string
	}{
		{"127.0.0.1", []string{"127.0.0.1"}, []string{"127.0.0.1"}, nil},
		{"::1", []string{"::1"}, nil, []string{"::1"}},
		// IPv4 映射的 IPv6 地址按 IPv4 处理（To4 非空）。
		{"::ffff:192.0.2.1", []string{"192.0.2.1"}, []string{"192.0.2.1"}, nil},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			check := func(name string, got []string, err error, want []string) {
				t.Helper()
				if err != nil {
					t.Fatalf("%s(%q): %v", name, c.in, err)
				}
				if !slices.Equal(got, want) {
					t.Errorf("%s(%q) = %v，期望 %v", name, c.in, got, want)
				}
			}
			got, err := ResolveDomainToIP(c.in)
			check("ResolveDomainToIP", got, err, c.all)
			got, err = ResolveDomainToIPv4(c.in)
			check("ResolveDomainToIPv4", got, err, c.ipv4)
			got, err = ResolveDomainToIPv6(c.in)
			check("ResolveDomainToIPv6", got, err, c.ip6)
		})
	}
}
