package mesh

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
)

// PathKind 是到对端的路径类型，决定优先级与页面显示（§5.3）。
type PathKind string

const (
	PathLAN     PathKind = "lan"
	PathPublic  PathKind = "public"
	PathPunched PathKind = "punched" // P6
	PathRelay   PathKind = "relay"
)

const (
	// happyStagger / happyBudget：Happy Eyeballs 的错开间隔与整体上限（§5.3）。
	happyStagger = 250 * time.Millisecond
	happyBudget  = 2 * time.Second
	// resolveTimeout：问协调节点「对端的候选地址」的上限。查不到不阻断，仍按手填地址直连。
	resolveTimeout = 1500 * time.Millisecond
)

// pathProvider 是一种到达对端的方式。返回的连接**已完成端到端 mTLS**（§12 P2-1）且公钥是 peer，
// 上层（gRPC）不知道它从哪来——以后加打洞、反向直连都是新增一个实现，不改上层。
type pathProvider interface {
	Dial(ctx context.Context, peer meshid.ID) (*pathConn, error)
}

// pathConn 是一条已认证的连接，记下它走的是哪条路径。
type pathConn struct {
	*tls.Conn
	kind PathKind
}

// dialPeer 按优先级依次尝试各路径：直连（Happy Eyeballs）在前，中转兜底。
func dialPeer(ctx context.Context, providers []pathProvider, peer meshid.ID) (*pathConn, error) {
	var errs []error
	for _, p := range providers {
		c, err := p.Dial(ctx, peer)
		if err == nil {
			if len(errs) > 0 {
				// 前面的路径（直连）失败、后面的（中转）成功：用户拿到了连接，但值得知道为什么没走直连——
				// 典型原因是对方的 Peer 端口被防火墙挡住，或候选地址指向了别的机器（钉公钥不符）。
				logger.Infof("[mesh] 到 %s 改走%s：%v", peer.Short(), c.kind, errors.Join(errs...))
			}
			return c, nil
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			break
		}
	}
	if len(errs) == 0 {
		return nil, errors.New("没有可用的路径（没有直连地址，也没有接入协调节点）")
	}
	return nil, errors.Join(errs...)
}

// clientTLS 在 raw 上完成到 peer 的端到端 mTLS。失败时关闭 raw。
func clientTLS(ctx context.Context, raw net.Conn, own tls.Certificate, peer meshid.ID) (*tls.Conn, error) {
	cfg := meshid.ClientConfig(own, peer)
	// grpc-go 服务端默认强制 ALPN：不带 h2 的客户端会被拒。
	cfg.NextProtos = []string{"h2"}
	tc := tls.Client(raw, cfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return tc, nil
}

// dialTarget 是一个直连候选。
type dialTarget struct {
	addr string
	kind PathKind
}

// classify 按地址归类：私网 / CGNAT ⇒ lan，其余（公网、域名）⇒ public。
func classify(addr string) PathKind {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return PathPublic
	}
	if a, err := netip.ParseAddr(host); err == nil && isLANAddr(a) {
		return PathLAN
	}
	return PathPublic
}

// planDirect 把协调节点给的候选与手填地址排成拨号顺序（§12 P2-4）：
// 出口 IP 相同 ⇒ lan 在前；否则 public 在前、lan 在后（仍然尝试：两边可能在同一个 VPN / 组网里）。
// 同一组里手填地址在前（那是用户明确说过的）。只要 TCP 的 HOST / CONFIGURED 候选，
// SRFLX / MAPPED 是 P6 打洞用的 UDP 地址。
func planDirect(cands []*meshpb.Candidate, manual []string, sameNAT bool) []dialTarget {
	seen := map[string]bool{}
	var lan, public []dialTarget
	add := func(addr string, kind PathKind) {
		if addr == "" || seen[addr] {
			return
		}
		seen[addr] = true
		if kind == PathLAN {
			lan = append(lan, dialTarget{addr, kind})
		} else {
			public = append(public, dialTarget{addr, kind})
		}
	}
	for _, a := range manual {
		add(a, classify(a))
	}
	for _, c := range cands {
		if c.GetTransport() != meshpb.Transport_TRANSPORT_TCP {
			continue
		}
		switch c.GetKind() {
		case meshpb.CandidateKind_CANDIDATE_KIND_HOST:
			add(c.GetAddr(), classify(c.GetAddr()))
		case meshpb.CandidateKind_CANDIDATE_KIND_CONFIGURED:
			add(c.GetAddr(), PathPublic)
		}
	}
	if sameNAT {
		return append(lan, public...)
	}
	return append(public, lan...)
}

// raceDial 是 Happy Eyeballs（RFC 8305 的简化）：按顺序发起，每 stagger 加一个，
// 前一个失败则立即发起下一个；第一个成功的胜出，其余取消，落选但已成功的连接关闭。
func raceDial(ctx context.Context, targets []dialTarget, stagger time.Duration,
	dial func(context.Context, dialTarget) (*tls.Conn, error)) (*tls.Conn, dialTarget, error) {

	if len(targets) == 0 {
		return nil, dialTarget{}, errors.New("没有直连候选")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		c   *tls.Conn
		t   dialTarget
		err error
	}
	results := make(chan result, len(targets))
	next, pending := 0, 0
	launch := func() {
		t := targets[next]
		next++
		pending++
		go func() {
			c, err := dial(ctx, t)
			results <- result{c, t, err}
		}()
	}
	// drain 在返回后收拾还在路上的拨号：成功了的也要关掉。
	drain := func(n int) {
		go func() {
			for range n {
				if r := <-results; r.c != nil {
					r.c.Close()
				}
			}
		}()
	}

	launch()
	timer := time.NewTimer(stagger)
	defer timer.Stop()
	var errs []error
	for pending > 0 {
		var tick <-chan time.Time
		if next < len(targets) {
			tick = timer.C
		}
		select {
		case r := <-results:
			pending--
			if r.err == nil {
				cancel()
				drain(pending)
				return r.c, r.t, nil
			}
			errs = append(errs, fmt.Errorf("%s: %w", r.t.addr, r.err))
			if next < len(targets) {
				launch()
				timer.Reset(stagger)
			}
		case <-tick:
			launch()
			timer.Reset(stagger)
		case <-ctx.Done():
			drain(pending)
			errs = append(errs, ctx.Err())
			return nil, dialTarget{}, errors.Join(errs...)
		}
	}
	return nil, dialTarget{}, errors.Join(errs...)
}

// directProvider 是直连路径：候选 = 协调节点给的 ∪ 手填的，Happy Eyeballs 拨号，每条都完成端到端 mTLS。
type directProvider struct {
	cert    tls.Certificate
	coord   *coordClient // 可为 nil（无协调节点模式）
	store   *PeerStore
	stagger time.Duration
	budget  time.Duration
}

func (p *directProvider) targets(ctx context.Context, peer meshid.ID) []dialTarget {
	var cands []*meshpb.Candidate
	sameNAT := false
	if p.coord != nil {
		rctx, cancel := context.WithTimeout(ctx, resolveTimeout)
		res, err := p.coord.client.Resolve(rctx, &meshpb.ResolveRequest{NodeId: peer.Compact()})
		cancel()
		if err == nil {
			cands = res.GetCandidates()
			sameNAT = res.GetSelfObservedIp() != "" && res.GetSelfObservedIp() == res.GetPeerObservedIp()
		}
	}
	var manual []string
	if rec, ok := p.store.Peer(peer); ok {
		manual = rec.Addrs
	}
	return planDirect(cands, manual, sameNAT)
}

func (p *directProvider) Dial(ctx context.Context, peer meshid.ID) (*pathConn, error) {
	targets := p.targets(ctx, peer)
	if len(targets) == 0 {
		return nil, errors.New("直连：没有候选地址")
	}
	ctx, cancel := context.WithTimeout(ctx, p.budget)
	defer cancel()
	var d net.Dialer
	c, t, err := raceDial(ctx, targets, p.stagger, func(ctx context.Context, t dialTarget) (*tls.Conn, error) {
		raw, err := d.DialContext(ctx, "tcp", t.addr)
		if err != nil {
			return nil, err
		}
		return clientTLS(ctx, raw, p.cert, peer)
	})
	if err != nil {
		return nil, fmt.Errorf("直连：%w", err)
	}
	return &pathConn{Conn: c, kind: t.kind}, nil
}

// relayProvider 是中转路径：OpenRelay → Relay 流 → streamconn → 端到端 mTLS。
type relayProvider struct {
	c    *coordClient
	cert tls.Certificate
}

func (p relayProvider) Dial(ctx context.Context, peer meshid.ID) (*pathConn, error) {
	resp, err := p.c.client.OpenRelay(ctx, &meshpb.OpenRelayRequest{TargetNodeId: peer.Compact()})
	if err != nil {
		return nil, fmt.Errorf("中转：%w", err)
	}
	raw, err := p.c.openRelayStream(resp.GetSessionId(), resp.GetToken(), peer.Short())
	if err != nil {
		return nil, fmt.Errorf("中转：%w", err)
	}
	tc, err := clientTLS(ctx, raw, p.cert, peer)
	if err != nil {
		return nil, fmt.Errorf("中转：%w", err)
	}
	return &pathConn{Conn: tc, kind: PathRelay}, nil
}
