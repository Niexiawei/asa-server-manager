package mesh

import (
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	"asa-server/internal/mesh/meshpb"
)

// skipInterfacePrefixes 是不上报的虚拟网卡：容器与虚拟机的网桥。它们的地址在别的机器上
// 不可达，拨过去只会浪费 Happy Eyeballs 的时间；更糟的是不同机器上常常有**相同**的
// 172.17.0.1，拨到的是对方自己的 docker0（钉公钥会挡住，但白白多一次握手）。
var skipInterfacePrefixes = []string{
	"docker", "br-", "veth", "virbr", "cni", "flannel",
	"vEthernet (WSL",            // Hyper-V 给 WSL 的那张
	"vEthernet (Default Switch", // Hyper-V 默认交换机（NAT）
}

func skipInterface(name string) bool {
	for _, p := range skipInterfacePrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// cgnat 是运营商级 NAT 的共享地址段（RFC 6598）。Tailscale 等组网软件也用它，
// 对本功能来说它和私网地址一样：只在「同一张网」里可达。
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// usableHostAddr 判断一个网卡地址值不值得上报：排除回环、链路本地、未指定、组播；
// IPv6 只要全局单播与 ULA（fc00::/7）。
func usableHostAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsUnspecified() || a.IsMulticast() ||
		a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() {
		return false
	}
	if a.Is6() {
		return a.IsGlobalUnicast() || a.IsPrivate()
	}
	return true
}

// isLANAddr 判断地址是不是「只在同一张网里可达」的：RFC 1918 / ULA / CGNAT，以及回环
// （上报的候选里没有回环，但手填的 127.0.0.1——同一台机器上跑两个管理器——也算内网）。
func isLANAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsPrivate() || a.IsLoopback() || cgnat.Contains(a)
}

// ifaceAddrs 是一张网卡的名字与地址，从 net.Interfaces 里摘出来，方便单测（不依赖本机网卡）。
type ifaceAddrs struct {
	name  string
	up    bool
	loop  bool
	addrs []netip.Addr
}

func systemInterfaces() []ifaceAddrs {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]ifaceAddrs, 0, len(ifs))
	for _, ifc := range ifs {
		ia := ifaceAddrs{name: ifc.Name, up: ifc.Flags&net.FlagUp != 0, loop: ifc.Flags&net.FlagLoopback != 0}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if pn, ok := a.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(pn.IP); ok {
					ia.addrs = append(ia.addrs, ip.Unmap())
				}
			}
		}
		out = append(out, ia)
	}
	return out
}

// hostAddrs 从网卡列表里挑出要上报的地址（去重、稳定排序：IPv4 在前）。
func hostAddrs(ifs []ifaceAddrs) []netip.Addr {
	var out []netip.Addr
	for _, ifc := range ifs {
		if !ifc.up || ifc.loop || skipInterface(ifc.name) {
			continue
		}
		for _, a := range ifc.addrs {
			if usableHostAddr(a) && !slices.Contains(out, a) {
				out = append(out, a)
			}
		}
	}
	slices.SortStableFunc(out, func(x, y netip.Addr) int {
		switch {
		case x.Is4() && !y.Is4():
			return -1
		case !x.Is4() && y.Is4():
			return 1
		}
		return 0
	})
	return out
}

// buildCandidates 组装本机上报给协调节点的候选：各网卡地址 + 监听端口（HOST，只在监听时），
// 加上手填的公网地址（CONFIGURED）。
func buildCandidates(ifs []ifaceAddrs, listenPort int, public []string) []*meshpb.Candidate {
	var out []*meshpb.Candidate
	if listenPort > 0 {
		for _, a := range hostAddrs(ifs) {
			out = append(out, &meshpb.Candidate{
				Transport: meshpb.Transport_TRANSPORT_TCP,
				Kind:      meshpb.CandidateKind_CANDIDATE_KIND_HOST,
				Addr:      net.JoinHostPort(a.String(), strconv.Itoa(listenPort)),
			})
		}
	}
	for _, p := range public {
		out = append(out, &meshpb.Candidate{
			Transport: meshpb.Transport_TRANSPORT_TCP,
			Kind:      meshpb.CandidateKind_CANDIDATE_KIND_CONFIGURED,
			Addr:      p,
		})
	}
	return out
}

func sameCandidates(a, b []*meshpb.Candidate) bool {
	return slices.EqualFunc(a, b, func(x, y *meshpb.Candidate) bool { return proto.Equal(x, y) })
}

func candidateAddrs(cs []*meshpb.Candidate) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.GetAddr())
	}
	return out
}
