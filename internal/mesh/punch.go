package mesh

import (
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
	"asa-server/pkg/stun"
)

// 打洞（§5.6、§12 P6）。信令走中转路径上的 Peer.Punch（协调节点零改动），之后双方从同一个 UDP socket
// 互发带 HMAC 的探测包，收到对方的有效探测 ⇒ 这个地址可达 ⇒ 发起方在上面建 QUIC。

const (
	probeMagic     = 0x2A // 首字节高两位为 0：quic-go 把它当非 QUIC 包交给 ReadNonQUICPacket
	probeTypeProbe = 1
	probeTypeAck   = 2
	punchSIDLen    = 16
	punchKeyLen    = 32
	probeMACLen    = 16
	probeLen       = 2 + punchSIDLen + 8 + probeMACLen // 42

	// punchMaxCands：每侧候选上限。探测流量因此有上界：8 × 20 包/秒 × 5 秒。
	punchMaxCands = 8
	probeInterval = 50 * time.Millisecond
	probeWindow   = 5 * time.Second
	// punchGrace：探测窗口结束后，应答方的会话再留这么久，等发起方的 QUIC 握手完成。
	punchGrace = 10 * time.Second
	// punchSignalTimeout：等中转连接就绪并完成 Punch 调用的上限。
	punchSignalTimeout = 8 * time.Second
	// defaultPunchMinInterval：同一对端两次打洞的最小间隔，防止「打通—断—打通」的快速循环。
	defaultPunchMinInterval = 30 * time.Second
)

// 打洞失败的原因（进 PeerView.LastPunch，页面原样显示）。
const (
	punchReasonOldPeer   = "对方版本不支持打洞"
	punchReasonDisabled  = "对方关闭了打洞"
	punchReasonBothHard  = "双方都是对称型 NAT，只能中转"
	punchReasonNoCands   = "对方没有可用的 UDP 地址"
	punchReasonTimeout   = "探测超时（UDP 不通）"
	punchReasonNoLocal   = "本机没有可用的 UDP 地址"
	errPunchDisabledText = "本机未开启打洞"
)

// encodeProbe 生成一个探测包：magic | type | 会话 ID | 序号 | HMAC-SHA256(key, 前 26 字节) 截断。
// HMAC 只防「别人伪造探测把选路引到错误地址」；身份认证靠之后的 QUIC 握手钉公钥。
func encodeProbe(key []byte, sid [punchSIDLen]byte, typ byte, seq uint64) []byte {
	b := make([]byte, probeLen)
	b[0], b[1] = probeMagic, typ
	copy(b[2:], sid[:])
	binary.BigEndian.PutUint64(b[2+punchSIDLen:], seq)
	mac := hmac.New(sha256.New, key)
	mac.Write(b[:probeLen-probeMACLen])
	copy(b[probeLen-probeMACLen:], mac.Sum(nil))
	return b
}

func isProbe(b []byte) bool { return len(b) == probeLen && b[0] == probeMagic }

// verifyProbe 校验探测包属于会话 sid 且 HMAC 正确，返回类型。
func verifyProbe(key []byte, sid [punchSIDLen]byte, b []byte) (byte, bool) {
	if !isProbe(b) || [punchSIDLen]byte(b[2:2+punchSIDLen]) != sid {
		return 0, false
	}
	typ := b[1]
	if typ != probeTypeProbe && typ != probeTypeAck {
		return 0, false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(b[:probeLen-probeMACLen])
	if !hmac.Equal(mac.Sum(nil)[:probeMACLen], b[probeLen-probeMACLen:]) {
		return 0, false
	}
	return typ, true
}

type inPacket struct {
	b    []byte
	from netip.AddrPort
}

// punchSession 是一次打洞的探测过程（两侧各一个）。一个 goroutine（run）既定时发探测、又处理收到的包。
type punchSession struct {
	id      [punchSIDLen]byte
	key     []byte
	peer    meshid.ID
	mux     *udpMux
	targets []netip.AddrPort

	in       chan inPacket
	verified chan netip.AddrPort
	start    chan []netip.AddrPort // 发起方拿到对方候选后才开始探测
	stop     chan struct{}
	stopOnce sync.Once
}

func newPunchSession(mux *udpMux, id [punchSIDLen]byte, key []byte, peer meshid.ID) *punchSession {
	return &punchSession{
		id: id, key: key, peer: peer, mux: mux,
		in:       make(chan inPacket, 64),
		verified: make(chan netip.AddrPort, punchMaxCands*2),
		start:    make(chan []netip.AddrPort, 1),
		stop:     make(chan struct{}),
	}
}

func (s *punchSession) deliver(b []byte, from netip.AddrPort) {
	select {
	case s.in <- inPacket{b, from}:
	default:
	}
}

func (s *punchSession) close() {
	s.stopOnce.Do(func() {
		close(s.stop)
		s.mux.removeSession(s)
	})
}

// run 先等目标（start），之后每 probeInterval 向每个目标发一个探测，持续 window；
// 窗口结束后不再主动探测，但继续回 ack，直到 close。收到有效探测 ⇒ 回 ack 给**来源地址**
// （经过 NAT 时它与候选表里的地址不同）；有效的探测或 ack 都让来源地址进 verified。
func (s *punchSession) run(window time.Duration) {
	var (
		tick    *time.Ticker
		tickC   <-chan time.Time
		endC    <-chan time.Time
		seq     uint64
		targets []netip.AddrPort
		seen    = map[netip.AddrPort]bool{}
	)
	sendAll := func() {
		for _, t := range targets {
			s.mux.writeTo(encodeProbe(s.key, s.id, probeTypeProbe, seq), t)
		}
		seq++
	}
	defer func() {
		if tick != nil {
			tick.Stop()
		}
	}()
	for {
		select {
		case <-s.stop:
			return
		case targets = <-s.start:
			tick = time.NewTicker(probeInterval)
			tickC = tick.C
			end := time.NewTimer(window)
			defer end.Stop()
			endC = end.C
			sendAll()
		case <-tickC:
			sendAll()
		case <-endC:
			tick.Stop()
			tickC, endC = nil, nil
		case p := <-s.in:
			typ, ok := verifyProbe(s.key, s.id, p.b)
			if !ok {
				continue
			}
			if typ == probeTypeProbe {
				s.mux.writeTo(encodeProbe(s.key, s.id, probeTypeAck, 0), p.from)
			}
			if !seen[p.from] {
				seen[p.from] = true
				select {
				case s.verified <- p.from:
				default:
				}
			}
		}
	}
}

// candidate 种类在本包内的简写，供排序。
func udpCandidate(kind meshpb.CandidateKind, ap netip.AddrPort) *meshpb.Candidate {
	return &meshpb.Candidate{Transport: meshpb.Transport_TRANSPORT_UDP, Kind: kind, Addr: ap.String()}
}

// orderPunchCandidates 排出本机发给对方的 UDP 候选并截断到 punchMaxCands：
// 反射地址在前（跨 NAT 最可能通），然后全局 IPv6（国内家宽常有，本身就是公网地址），
// 最后私网地址（同一内网时 TCP 直连通常已经赢了）。
func orderPunchCandidates(host []netip.AddrPort, srflx []netip.AddrPort) []*meshpb.Candidate {
	type c struct {
		ap   netip.AddrPort
		kind meshpb.CandidateKind
		rank int
	}
	var list []c
	seen := map[netip.AddrPort]bool{}
	add := func(ap netip.AddrPort, kind meshpb.CandidateKind, rank int) {
		ap = unmapAddrPort(ap)
		if !ap.IsValid() || ap.Port() == 0 || seen[ap] {
			return
		}
		seen[ap] = true
		list = append(list, c{ap, kind, rank})
	}
	for _, ap := range srflx {
		add(ap, meshpb.CandidateKind_CANDIDATE_KIND_SRFLX, 0)
	}
	for _, ap := range host {
		rank := 2
		if a := ap.Addr().Unmap(); a.Is6() && !isLANAddr(a) {
			rank = 1
		}
		add(ap, meshpb.CandidateKind_CANDIDATE_KIND_HOST, rank)
	}
	slices.SortStableFunc(list, func(x, y c) int { return cmp.Compare(x.rank, y.rank) })
	if len(list) > punchMaxCands {
		list = list[:punchMaxCands]
	}
	out := make([]*meshpb.Candidate, 0, len(list))
	for _, x := range list {
		out = append(out, udpCandidate(x.kind, x.ap))
	}
	return out
}

// parsePunchTargets 取出对方候选里能拨的 UDP 地址（去重、截断）。来自对端，不可信：只认合法的单播地址。
func parsePunchTargets(cands []*meshpb.Candidate) []netip.AddrPort {
	var out []netip.AddrPort
	for _, c := range cands {
		if c.GetTransport() != meshpb.Transport_TRANSPORT_UDP {
			continue
		}
		ap, err := netip.ParseAddrPort(c.GetAddr())
		if err != nil {
			continue
		}
		ap = unmapAddrPort(ap)
		a := ap.Addr()
		if ap.Port() == 0 || a.IsUnspecified() || a.IsMulticast() || slices.Contains(out, ap) {
			continue
		}
		out = append(out, ap)
		if len(out) == punchMaxCands {
			break
		}
	}
	return out
}

func mappingToProto(m stun.Mapping) meshpb.NATMapping {
	switch m {
	case stun.NoNAT:
		return meshpb.NATMapping_NAT_MAPPING_NONE
	case stun.EndpointIndependent:
		return meshpb.NATMapping_NAT_MAPPING_EASY
	case stun.EndpointDependent:
		return meshpb.NATMapping_NAT_MAPPING_HARD
	}
	return meshpb.NATMapping_NAT_MAPPING_UNKNOWN
}

// mappingName 是状态接口里的取值（文案在前端）。
func mappingName(m stun.Mapping) string {
	switch m {
	case stun.NoNAT:
		return "none"
	case stun.EndpointIndependent:
		return "easy"
	case stun.EndpointDependent:
		return "hard"
	}
	return "unknown"
}

func randomArray[T ~[16]byte]() T {
	var b T
	_, _ = rand.Read(b[:])
	return b
}

// puncher 是本机的打洞能力：UDP socket、NAT 判型、两侧的会话、QUIC 的监听与拨号。
type puncher struct {
	mux      *udpMux
	cert     tls.Certificate
	self     meshid.ID
	bindErr  error
	deliver  func(net.Conn) // 准入的入站连接交给 Peer gRPC 服务
	stunList func() []string
	hostUDP  func(port int) []netip.AddrPort
	ln       *quic.Listener
	trigger  chan struct{}

	mu      sync.Mutex
	nat     natState
	inbound map[meshid.ID]*punchSession // 应答方：按发起方 ID，至多一个
	conns   map[*quic.Conn]struct{}     // 两个方向的全部 QUIC 连接，close 时逐条正常关闭
}

func newPuncher(mux *udpMux, bindErr error, cert tls.Certificate, self meshid.ID, deliver func(net.Conn),
	stunList func() []string, hostUDP func(port int) []netip.AddrPort) (*puncher, error) {
	ln, err := mux.tr.Listen(quicServerTLS(cert), quicConfig())
	if err != nil {
		return nil, err
	}
	if hostUDP == nil {
		hostUDP = func(port int) []netip.AddrPort {
			var out []netip.AddrPort
			for _, a := range hostAddrs(systemInterfaces()) {
				out = append(out, netip.AddrPortFrom(a, uint16(port)))
			}
			return out
		}
	}
	return &puncher{
		mux: mux, cert: cert, self: self, bindErr: bindErr, deliver: deliver, stunList: stunList, hostUDP: hostUDP,
		ln: ln, trigger: make(chan struct{}, 1), inbound: map[meshid.ID]*punchSession{},
		conns: map[*quic.Conn]struct{}{},
	}, nil
}

// start 起分发、QUIC 接受与地址发现三个循环，ctx 结束时退出。
func (p *puncher) start(ctx context.Context) {
	go p.mux.run(ctx)
	go p.acceptLoop(ctx)
	go p.discoverLoop(ctx)
}

// close 先逐条正常关闭 QUIC 连接（发出 CONNECTION_CLOSE），再关 Transport。
// ⚠️ 顺序承重：Transport.Close 直接丢弃连接、**不通知对方**，对方要等 quicIdle（45 秒）才发现，
// 这段时间里它到本机的请求全部失败、也不会回落到中转。
func (p *puncher) close() {
	p.mu.Lock()
	conns := make([]*quic.Conn, 0, len(p.conns))
	for c := range p.conns {
		conns = append(conns, c)
	}
	for id, s := range p.inbound {
		s.close()
		delete(p.inbound, id)
	}
	p.mu.Unlock()
	var wg sync.WaitGroup
	for _, c := range conns {
		wg.Go(func() { c.CloseWithError(0, "shutting down") })
	}
	wg.Wait()
	p.ln.Close()
	p.mux.close()
}

// track 登记一条 QUIC 连接，连接结束时自动注销。
func (p *puncher) track(c *quic.Conn) {
	p.mu.Lock()
	p.conns[c] = struct{}{}
	p.mu.Unlock()
	go func() {
		<-c.Context().Done()
		p.mu.Lock()
		delete(p.conns, c)
		p.mu.Unlock()
	}()
}

func (p *puncher) candidates() []*meshpb.Candidate {
	p.mu.Lock()
	srflx := slices.Clone(p.nat.srflx)
	p.mu.Unlock()
	return orderPunchCandidates(p.hostUDP(p.mux.port), srflx)
}

func (p *puncher) mapping() stun.Mapping {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.nat.mapping
}

// answer 是应答方（B）处理 Peer.Punch：登记会话、**先**开始探测再返回自己的候选——
// 发起方收到响应才开始探测，两边相差半个中转 RTT，远小于探测窗口。
func (p *puncher) answer(caller meshid.ID, req *meshpb.PunchRequest) (*meshpb.PunchResponse, error) {
	if len(req.GetSessionId()) != punchSIDLen || len(req.GetKey()) != punchKeyLen {
		return nil, status.Error(codes.InvalidArgument, "会话 ID 或密钥长度不对")
	}
	targets := parsePunchTargets(req.GetCandidates())
	resp := &meshpb.PunchResponse{Candidates: p.candidates(), Mapping: mappingToProto(p.mapping())}
	if len(targets) == 0 {
		return resp, nil
	}
	s := newPunchSession(p.mux, [punchSIDLen]byte(req.GetSessionId()), slices.Clone(req.GetKey()), caller)
	p.mux.addSession(s)
	p.mu.Lock()
	if old := p.inbound[caller]; old != nil {
		old.close()
	}
	p.inbound[caller] = s
	p.mu.Unlock()
	go s.run(probeWindow)
	s.start <- targets
	time.AfterFunc(probeWindow+punchGrace, func() { p.dropInbound(caller, s) })
	logger.Debugf("[mesh] %s 请求打洞，对方候选 %v", caller.Short(), targets)
	return resp, nil
}

func (p *puncher) dropInbound(id meshid.ID, s *punchSession) {
	p.mu.Lock()
	if p.inbound[id] == s {
		delete(p.inbound, id)
	}
	p.mu.Unlock()
	s.close()
}

// admit 是 QUIC 入站连接的准入：必须有一个发起方是 id 的未作废会话。准入即结束那个会话。
func (p *puncher) admit(id meshid.ID) bool {
	p.mu.Lock()
	s := p.inbound[id]
	delete(p.inbound, id)
	p.mu.Unlock()
	if s == nil {
		return false
	}
	s.close()
	return true
}

// punchResult 是发起方一次打洞的结果。
type punchResult struct {
	conn        *pathConn
	addr        netip.AddrPort
	reason      string
	peerMapping meshpb.NATMapping
	// later 为 true：这个对端暂时不必再试（旧版本 / 关了打洞），按升级上限的间隔再看。
	later bool
}

// dial 是发起方（A）的一次打洞：经 client（中转路径上的 Peer 客户端）发 Punch，探测，拿第一个已验证的地址建 QUIC。
func (p *puncher) dial(ctx context.Context, peer meshid.ID, client meshpb.PeerClient) punchResult {
	local := p.candidates()
	if len(local) == 0 {
		return punchResult{reason: punchReasonNoLocal}
	}
	sid := randomArray[[punchSIDLen]byte]()
	key := make([]byte, punchKeyLen)
	_, _ = rand.Read(key)
	// 先登记会话再发信令：B 收到请求就开始探测，它的包可能比响应先到。
	s := newPunchSession(p.mux, sid, key, peer)
	p.mux.addSession(s)
	defer s.close()
	go s.run(probeWindow)

	sctx, cancel := context.WithTimeout(ctx, punchSignalTimeout)
	resp, err := client.Punch(sctx, &meshpb.PunchRequest{SessionId: sid[:], Key: key, Candidates: local,
		Mapping: mappingToProto(p.mapping())}, grpc.WaitForReady(true))
	cancel()
	if err != nil {
		switch status.Code(err) {
		case codes.Unimplemented:
			return punchResult{reason: punchReasonOldPeer, later: true}
		case codes.FailedPrecondition:
			return punchResult{reason: punchReasonDisabled, later: true}
		}
		return punchResult{reason: "信令失败：" + err.Error()}
	}
	res := punchResult{peerMapping: resp.GetMapping()}
	targets := parsePunchTargets(resp.GetCandidates())
	if len(targets) == 0 {
		res.reason = punchReasonNoCands
		return res
	}
	s.start <- targets

	deadline := time.NewTimer(probeWindow)
	defer deadline.Stop()
	var errs []error
	for {
		select {
		case <-ctx.Done():
			res.reason = ctx.Err().Error()
			return res
		case <-deadline.C:
			res.reason = punchReasonTimeout
			if len(errs) > 0 {
				res.reason = "QUIC 握手失败：" + errors.Join(errs...).Error()
			}
			return res
		case addr := <-s.verified:
			pc, err := p.dialQUIC(ctx, addr, peer)
			if err == nil {
				res.conn, res.addr = pc, addr
				return res
			}
			errs = append(errs, fmt.Errorf("%s: %w", addr, err))
		}
	}
}
