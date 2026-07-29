package appconfig

import (
	"net"
	"strings"
)

// detectLocalPrivateSubnets 枚举本机「看起来是物理网卡」的接口，
// 返回其私有/链路本地地址所在的子网 CIDR。
//
// 只在用户显式开启 auth.lan_bypass.auto_detect_local_subnets 时调用
// （默认 false），结果是对 Networks 的补充，不替换已有内容。
func detectLocalPrivateSubnets() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil // 探测失败不阻断启动，最多是少加几条
	}
	var out []string
	seen := make(map[string]bool)
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		if looksVirtual(ifi.Name) {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP
			// 公网地址一律不信任——物理网卡也可能直连公网。
			if !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
				continue
			}
			network := &net.IPNet{IP: ip.Mask(ipnet.Mask), Mask: ipnet.Mask}
			cidr := network.String()
			if !seen[cidr] {
				seen[cidr] = true
				out = append(out, cidr)
			}
		}
	}
	return out
}

// virtualAdapterKeywords 是虚拟/隧道适配器名称的关键字黑名单（小写匹配）。
//
// 没有 WMI 的 PhysicalAdapter 属性可用（会打破 appconfig 只依赖标准库的约束），
// 用名称关键字兜底：宁可把物理网卡误判成虚拟网卡漏掉，也不能把 Docker/WSL2
// 的容器子网当成本机 LAN 纳入信任。
var virtualAdapterKeywords = []string{
	"virtual", "vethernet", "veth", "docker", "wsl", "hyper-v", "hyperv",
	"vmware", "virtualbox", "vbox", "tap-windows", "tap ", "tun",
	"ppp", "tailscale", "zerotier", "wireguard", "npcap",
	"isatap", "teredo", "bluetooth", "vpn",
}

func looksVirtual(name string) bool {
	name = strings.ToLower(name)
	for _, kw := range virtualAdapterKeywords {
		if strings.Contains(name, kw) {
			return true
		}
	}
	return false
}
