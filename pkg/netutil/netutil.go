// Package netutil provides DNS resolution and port helpers with no domain dependencies.
package netutil

import (
	"fmt"
	"net"
)

// FreeUDPPort 向系统申请一个当前空闲的 UDP 端口号。
//
// 绑定 :0 让内核从临时端口范围里分配一个没人占用的端口，随即关闭并返回该端口号。
// ARK 的 -Port 是 UDP 游戏端口，因此只查 UDP。
//
// 注意：关闭与调用方真正绑定之间存在理论上的抢占窗口，这是本模式的固有限制。
func FreeUDPPort() (int, error) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to acquire a free UDP port: %w", err)
	}
	defer conn.Close()

	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected local address type %T", conn.LocalAddr())
	}

	return addr.Port, nil
}

// ResolveDomainToIP resolves a domain name to its IP addresses.
func ResolveDomainToIP(domain string) ([]string, error) {
	ips, err := net.LookupIP(domain)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve domain %s: %w", domain, err)
	}

	var ipStrings []string
	for _, ip := range ips {
		ipStrings = append(ipStrings, ip.String())
	}

	return ipStrings, nil
}

// ResolveDomainToIPv4 resolves a domain name to its IPv4 addresses only.
func ResolveDomainToIPv4(domain string) ([]string, error) {
	ips, err := net.LookupIP(domain)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve domain %s: %w", domain, err)
	}

	var ipStrings []string
	for _, ip := range ips {
		// Check if it's an IPv4 address
		if ip.To4() != nil {
			ipStrings = append(ipStrings, ip.String())
		}
	}

	return ipStrings, nil
}

// ResolveDomainToIPv6 resolves a domain name to its IPv6 addresses only.
func ResolveDomainToIPv6(domain string) ([]string, error) {
	ips, err := net.LookupIP(domain)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve domain %s: %w", domain, err)
	}

	var ipStrings []string
	for _, ip := range ips {
		// Check if it's an IPv6 address (and not IPv4)
		if ip.To4() == nil && ip.To16() != nil {
			ipStrings = append(ipStrings, ip.String())
		}
	}

	return ipStrings, nil
}
