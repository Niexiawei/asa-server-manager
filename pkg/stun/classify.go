package stun

import "net/netip"

// Mapping 是 NAT 的映射行为（RFC 4787 的术语）。
type Mapping int

const (
	// MappingUnknown：信息不足（只问到一个端口，或有一次没问到）。
	MappingUnknown Mapping = iota
	// NoNAT：反射地址就是本机地址，没有经过 NAT。
	NoNAT
	// EndpointIndependent：映射与目的端口无关（NAT1～NAT3），可以打洞。
	EndpointIndependent
	// EndpointDependent：映射随目的地变化（对称型，俗称 NAT4），打洞多半不通。
	EndpointDependent
)

func (m Mapping) String() string {
	switch m {
	case NoNAT:
		return "无 NAT（公网地址直达）"
	case EndpointIndependent:
		return "映射与目的无关（易打洞，NAT1～NAT3）"
	case EndpointDependent:
		return "映射随目的变化（难打洞，对称型 / NAT4）"
	default:
		return "未知"
	}
}

// ClassifyMapping 比对从同一个本地 socket 向协调节点两个 STUN 端口问到的反射地址
// （docs/REMOTE_MANAGER_MESH_PLAN.md §5.6.3 第 1 步）。local 是本机这个 socket 的
// 全部可能地址（各网卡 IP + 端口），用来认出「没有 NAT」。
//
// 只有一个 IP 时分辨不出「只随目的 IP 变」的 NAT，会误判成易打洞——代价只是 P6 多试一次失败。
func ClassifyMapping(local []netip.AddrPort, viaA, viaB netip.AddrPort) Mapping {
	if !viaA.IsValid() {
		return MappingUnknown
	}
	a := unmap(viaA)
	for _, l := range local {
		if unmap(l) == a {
			return NoNAT
		}
	}
	if !viaB.IsValid() {
		return MappingUnknown
	}
	if a == unmap(viaB) {
		return EndpointIndependent
	}
	return EndpointDependent
}

func unmap(ap netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
}
