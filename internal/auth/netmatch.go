package auth

import (
	"fmt"
	"net"
	"strings"
	"sync"
)

// NetworkSet 是一组已解析的网段，用于内网判定。
type NetworkSet struct {
	nets []*net.IPNet
}

// ParseNetworks 解析 CIDR 列表。裸 IP 会被补成单主机网段。
func ParseNetworks(cidrs []string) (*NetworkSet, error) {
	set := &NetworkSet{nets: make([]*net.IPNet, 0, len(cidrs))}
	for i, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			ip := net.ParseIP(c)
			if ip == nil {
				return nil, fmt.Errorf("networks[%d]: %q 不是合法的 CIDR 或 IP", i, c)
			}
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			_, n, err = net.ParseCIDR(fmt.Sprintf("%s/%d", ip.String(), bits))
			if err != nil {
				return nil, fmt.Errorf("networks[%d]: %q 无法转换为网段", i, c)
			}
		}
		set.nets = append(set.nets, n)
	}
	return set, nil
}

// Contains 判断 ip 是否落在集合中的任一网段内。
func (s *NetworkSet) Contains(ip net.IP) bool {
	if s == nil || ip == nil {
		return false
	}
	// IPv4-mapped IPv6（::ffff:a.b.c.d）要按其 IPv4 语义判定。
	// net.IPNet.Contains 内部会做这个归一化，但显式写出来更清楚：
	// ::ffff:8.8.8.8 是公网地址，绝不能因为"看起来像 IPv6 前缀全零"而被当成内网。
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range s.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// 中间件每个请求都要做内网判定，而配置里的网段是字符串。
// 按内容缓存已解析的结果，避免每次都重新 ParseCIDR。
var (
	netCacheMu sync.RWMutex
	netCache   = map[string]*NetworkSet{}
)

// IsInNetworks 判断 ip 是否属于 cidrs 描述的网段集合。
// 解析结果按 cidrs 内容缓存，可以安全地在请求热路径上调用。
func IsInNetworks(ip net.IP, cidrs []string) bool {
	key := strings.Join(cidrs, "\x00")

	netCacheMu.RLock()
	set, ok := netCache[key]
	netCacheMu.RUnlock()

	if !ok {
		var err error
		set, err = ParseNetworks(cidrs)
		if err != nil {
			// 配置在 appconfig.Validate 阶段已经校验过，走到这里说明调用方
			// 绕过了校验。宁可判定为"不是内网"——免鉴权比误拦截危险得多。
			return false
		}
		netCacheMu.Lock()
		netCache[key] = set
		netCacheMu.Unlock()
	}
	return set.Contains(ip)
}

// HostIP 从 "host:port" 形式的地址里取出 IP。
// 用于解析 http.Request.RemoteAddr。
func HostIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr // 少数情况下没有端口
	}
	return net.ParseIP(strings.Trim(host, "[]"))
}
