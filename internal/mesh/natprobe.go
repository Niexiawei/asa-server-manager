package mesh

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"time"

	"asa-server/pkg/logger"
	"asa-server/pkg/stun"
)

// 地址发现与 NAT 判型（§5.6.3 第 1 步、§12 P6-2）：经打洞用的那个 socket 向协调节点的两个 STUN 端口
// 各问一次，得到「服务器反射地址」，再用 stun.ClassifyMapping 判断映射是否与目的无关。

const (
	// natRefresh：定期重测的间隔。拿到 Registered、网卡地址变化时另外立即测一次。
	natRefresh = 5 * time.Minute
	// natProbeTimeout：一轮测试的上限（两个端口，各最多重传三次）。
	natProbeTimeout = 10 * time.Second
)

// natState 是最近一次测试的结果。测不到（UDP 被封、STUN 没开）时 mapping 为 MappingUnknown、
// 没有 srflx——照样尝试打洞：host 候选在 IPv6 下经常就够。
type natState struct {
	mapping   stun.Mapping
	srflx     []netip.AddrPort
	checkedAt time.Time
	err       string
}

func (p *puncher) status() PunchStatus {
	p.mu.Lock()
	nat := p.nat
	p.mu.Unlock()
	st := PunchStatus{Active: true, UDPAddr: p.mux.localAddr(), Mapping: mappingName(nat.mapping),
		Srflx: []string{}, CheckedAt: nat.checkedAt, Error: nat.err}
	if p.bindErr != nil {
		st.UDPError = p.bindErr.Error()
	}
	for _, ap := range nat.srflx {
		st.Srflx = append(st.Srflx, ap.String())
	}
	return st
}

// kick 请求立即重测一次（非阻塞，合并重复请求）。
func (p *puncher) kick() {
	select {
	case p.trigger <- struct{}{}:
	default:
	}
}

func (p *puncher) discoverLoop(ctx context.Context) {
	t := time.NewTicker(natRefresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.trigger:
		case <-t.C:
		}
		dctx, cancel := context.WithTimeout(ctx, natProbeTimeout)
		p.discover(dctx)
		cancel()
	}
}

// discover 测一轮并记下结果。只有结论变了才记 INFO，免得每 5 分钟刷一条。
func (p *puncher) discover(ctx context.Context) {
	servers := resolveSTUN(ctx, p.stunList())
	st := natState{checkedAt: time.Now()}
	var via [2]netip.AddrPort
	var errs []error
	for i, srv := range servers {
		if i == len(via) {
			break
		}
		ap, err := p.mux.stunQuery(ctx, srv)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		via[i] = ap
		if !slices.Contains(st.srflx, ap) {
			st.srflx = append(st.srflx, ap)
		}
	}
	switch {
	case len(servers) == 0:
		st.err = "协调节点没有开 STUN"
	case len(errs) > 0:
		st.err = errors.Join(errs...).Error()
	}
	st.mapping = stun.ClassifyMapping(p.hostUDP(p.mux.port), via[0], via[1])

	p.mu.Lock()
	prev := p.nat
	p.nat = st
	p.mu.Unlock()
	if prev.mapping != st.mapping || !slices.Equal(prev.srflx, st.srflx) {
		logger.Infof("[mesh] NAT 判型：%s，反射地址 %v", st.mapping, st.srflx)
	}
	if st.err != "" && st.err != prev.err {
		logger.Warnf("[mesh] STUN 地址发现不完整：%s", st.err)
	}
}

// resolveSTUN 把协调节点下发的 host:port 解析成地址（主机名由管理器自己解析，§12 P1-8）。
func resolveSTUN(ctx context.Context, list []string) []netip.AddrPort {
	var out []netip.AddrPort
	for _, s := range list {
		host, port, err := net.SplitHostPort(s)
		if err != nil {
			continue
		}
		pn, err := net.LookupPort("udp", port)
		if err != nil {
			continue
		}
		var addr netip.Addr
		if a, err := netip.ParseAddr(host); err == nil {
			addr = a
		} else {
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(ips) == 0 {
				continue
			}
			addr = ips[0]
			for _, ip := range ips {
				if ip.Unmap().Is4() { // 优先 IPv4：IPv6 不需要反射地址（全局 IPv6 本身就是公网地址）
					addr = ip
					break
				}
			}
		}
		out = append(out, netip.AddrPortFrom(addr.Unmap(), uint16(pn)))
	}
	return out
}
