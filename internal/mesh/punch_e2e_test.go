package mesh

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"asa-server/pkg/meshid"
	"asa-server/pkg/stun"
)

// startSTUN 在回环上起两个 STUN 端口（协调节点的「两个 UDP 端口」），返回地址。
func startSTUN(t *testing.T) []string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var addrs []string
	for range 2 {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { pc.Close() })
		go stun.Serve(ctx, pc, stun.ServerOptions{RatePerSource: 1000, BurstPerSource: 1000})
		addrs = append(addrs, pc.LocalAddr().String())
	}
	return addrs
}

func withCoordSTUN(addrs []string) func(*testCoord) {
	return func(c *testCoord) { c.stunAddrs = addrs }
}

// withPunch 打开打洞：UDP 绑在回环随机端口，本机 UDP 候选就是它（真实枚举会过滤掉回环地址）。
func withPunch(o *Options) {
	o.UDPListenAddr = "127.0.0.1:0"
	o.localUDP = func(port int) []netip.AddrPort {
		return []netip.AddrPort{netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(port))}
	}
	o.punchMinInterval = 200 * time.Millisecond
}

// coneFilter 模拟「端口受限锥形 NAT」的入站过滤：只放行本 socket 发过包的对端地址。
// 双方都套上它时，只有探测确实在「开洞」，对方的包才进得来。dropAll 模拟 UDP 被封。
type coneFilter struct {
	net.PacketConn
	mu      sync.Mutex
	allowed map[netip.AddrPort]bool
	dropAll atomic.Bool
}

func (f *coneFilter) WriteTo(b []byte, addr net.Addr) (int, error) {
	if ap, ok := addrPortOf(addr); ok {
		f.mu.Lock()
		f.allowed[ap] = true
		f.mu.Unlock()
	}
	return f.PacketConn.WriteTo(b, addr)
}

func (f *coneFilter) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		n, addr, err := f.PacketConn.ReadFrom(b)
		if err != nil {
			return n, addr, err
		}
		ap, _ := addrPortOf(addr)
		f.mu.Lock()
		ok := f.allowed[ap]
		f.mu.Unlock()
		if ok && !f.dropAll.Load() {
			return n, addr, nil
		}
	}
}

func withCone(slot **coneFilter, dropAll bool) func(*Options) {
	return func(o *Options) {
		o.wrapPacketConn = func(pc net.PacketConn) net.PacketConn {
			f := &coneFilter{PacketConn: pc, allowed: map[netip.AddrPort]bool{}}
			f.dropAll.Store(dropAll)
			*slot = f
			return f
		}
	}
}

// punchPair 建 A、B（都不监听 TCP ⇒ 直连必败），B 以 operator 授权 A。
func punchPair(t *testing.T, aOpts, bOpts []func(*Options)) *pairedPair {
	t.Helper()
	c := newTestCoord(t, withCoordSTUN(startSTUN(t)))
	p := &pairedPair{c: c, blocked: make(chan struct{}, 4)}
	p.a = newManager(t, c, "a", aOpts...)
	p.b = newManager(t, c, "b", bOpts...)
	waitConnected(t, p.a, p.b)
	p.aid, p.bid = nodeID(t, p.a), nodeID(t, p.b)
	inv, _, err := p.b.CreateInvite(InviteOptions{Role: RoleOperator}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res := pairEventually(t, p.a, inv); res.Status != "paired" {
		t.Fatalf("配对结果不对：%+v", res)
	}
	return p
}

// waitPath 一直发 Hello，直到 A 到 B 的路径是 want。
func waitPath(t *testing.T, a *Manager, b meshid.ID, want PathKind, within time.Duration) *HelloResult {
	t.Helper()
	deadline := time.Now().Add(within)
	var last *HelloResult
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		res, err := a.Hello(ctx, b)
		cancel()
		if err == nil {
			last = res
			if res.Path == want {
				return res
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s 内没有走上 %s：最后一次 %+v", within, want, last)
	return nil
}

func lastPunch(t *testing.T, a *Manager, b meshid.ID) *PunchRecord {
	t.Helper()
	views, err := a.Peers()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.NodeID == b {
			return v.LastPunch
		}
	}
	return nil
}

func waitPunchRecord(t *testing.T, a *Manager, b meshid.ID, within time.Duration) *PunchRecord {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if r := lastPunch(t, a, b); r != nil {
			return r
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s 内没有打洞记录", within)
	return nil
}

// ① TCP 直连不通 ⇒ 先中转，几秒内升级为打洞；地址发现与状态也在这里一并验。
func TestPunchUpgradesRelay(t *testing.T) {
	p := punchPair(t, []func(*Options){withPunch}, []func(*Options){withPunch})

	res := waitPath(t, p.a, p.bid, PathPunched, 15*time.Second)
	if res.Version != "b" {
		t.Fatalf("回答必须来自 B：%+v", res)
	}
	r := lastPunch(t, p.a, p.bid)
	if r == nil || !r.OK || r.Addr == "" {
		t.Fatalf("打洞记录不对：%+v", r)
	}

	st := p.a.Status().Punch
	if !st.Active || st.UDPAddr == "" {
		t.Fatalf("打洞状态不对：%+v", st)
	}
	deadline := time.Now().Add(5 * time.Second)
	for st.Mapping != "none" || len(st.Srflx) != 1 || st.Srflx[0] != st.UDPAddr {
		if time.Now().After(deadline) {
			t.Fatalf("回环上应判为无 NAT、反射地址就是本机 UDP 地址：%+v", st)
		}
		time.Sleep(50 * time.Millisecond)
		st = p.a.Status().Punch
	}
}

// ② 两侧都套「端口受限锥形 NAT」的过滤：只有双方都在探测，洞才开得出来。
func TestPunchThroughConeFilters(t *testing.T) {
	var fa, fb *coneFilter
	p := punchPair(t, []func(*Options){withPunch, withCone(&fa, false)}, []func(*Options){withPunch, withCone(&fb, false)})
	waitPath(t, p.a, p.bid, PathPunched, 15*time.Second)
}

// ③ B 的入站 UDP 全被挡住：留在中转、记下原因，不出现快速重试。B 的探测照样发得出去，
// 所以 A 会看到「地址可达」并尝试 QUIC，失败在握手——单向 UDP 的真实样子。
func TestPunchBlockedStaysRelay(t *testing.T) {
	var fb *coneFilter
	slowRetry := func(o *Options) { o.punchMinInterval = time.Hour }
	p := punchPair(t, []func(*Options){withPunch, slowRetry}, []func(*Options){withPunch, withCone(&fb, true)})

	if res := helloEventually(t, p.a, p.bid); res.Path != PathRelay {
		t.Fatalf("打洞前应走中转：%+v", res)
	}
	r := waitPunchRecord(t, p.a, p.bid, probeWindow+5*time.Second)
	if r.OK || (r.Reason != punchReasonTimeout && !strings.HasPrefix(r.Reason, "QUIC 握手失败")) {
		t.Fatalf("应记探测超时或 QUIC 握手失败：%+v", r)
	}
	time.Sleep(time.Second)
	if r2 := lastPunch(t, p.a, p.bid); r2 == nil || !r2.At.Equal(r.At) {
		t.Fatalf("最小间隔内不该再打洞：%+v → %+v", r, r2)
	}
	if res := helloEventually(t, p.a, p.bid); res.Path != PathRelay {
		t.Fatalf("打不通时应留在中转：%+v", res)
	}
}

// ④ B 关了打洞：FailedPrecondition ⇒ 记原因，留在中转。
func TestPunchPeerDisabled(t *testing.T) {
	p := punchPair(t, []func(*Options){withPunch}, nil)
	helloEventually(t, p.a, p.bid)
	r := waitPunchRecord(t, p.a, p.bid, 10*time.Second)
	if r.OK || r.Reason != punchReasonDisabled {
		t.Fatalf("应记「对方关闭了打洞」：%+v", r)
	}
	if st := p.b.Status().Punch; st.Active || st.UDPAddr != "" {
		t.Fatalf("关了打洞的一方不该开 UDP：%+v", st)
	}
	if res := helloEventually(t, p.a, p.bid); res.Path != PathRelay {
		t.Fatalf("应留在中转：%+v", res)
	}
}

// ⑤ 打洞路径上撤销授权：B 断开 A 的打洞连接（P3-5 的语义在新路径上成立）。
func TestPunchRevokeClosesConn(t *testing.T) {
	p := punchPair(t, []func(*Options){withPunch}, []func(*Options){withPunch})
	waitPath(t, p.a, p.bid, PathPunched, 15*time.Second)
	if n := inboundUDPConns(p.b, p.aid); n == 0 {
		t.Fatal("B 上应有一条来自 A 的打洞入站连接")
	}
	if err := OpenPeerStore(p.b.Dir()).Revoke(p.aid); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for inboundUDPConns(p.b, p.aid) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("撤销后 B 应断开 A 的打洞连接")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// inboundUDPConns 数 m 上来自 id、走 UDP（打洞）的入站连接。
func inboundUDPConns(m *Manager, id meshid.ID) int {
	m.mu.Lock()
	reg := m.reg
	m.mu.Unlock()
	reg.mu.Lock()
	defer reg.mu.Unlock()
	n := 0
	for c := range reg.conns[id] {
		if _, ok := c.RemoteAddr().(*net.UDPAddr); ok {
			n++
		}
	}
	return n
}

// ⑥ 没有打洞会话的身份直接对 B 的 UDP 端口建 QUIC：被准入拒绝。
func TestPunchQUICAdmission(t *testing.T) {
	c := newTestCoord(t, withCoordSTUN(startSTUN(t)))
	b := newManager(t, c, "b", withPunch)
	waitConnected(t, b)
	cert, _, err := meshid.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tlsConf := meshid.ClientConfig(cert, nodeID(t, b))
	tlsConf.NextProtos = []string{quicALPN}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	qc, err := quic.DialAddr(ctx, b.Status().Punch.UDPAddr, tlsConf, quicConfig())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-qc.Context().Done():
	case <-ctx.Done():
		t.Fatal("没有打洞会话的 QUIC 连接应被关闭")
	}
	var ae *quic.ApplicationError
	if err := context.Cause(qc.Context()); !errors.As(err, &ae) || ae.ErrorCode != quicRefused {
		t.Fatalf("应以 quicRefused 关闭：%v", err)
	}
}

// ⑦ 打洞连接断了：回到中转，随后再次打通（受最小间隔约束）。
func TestPunchReconnectsAfterDrop(t *testing.T) {
	p := punchPair(t, []func(*Options){withPunch}, []func(*Options){withPunch})
	waitPath(t, p.a, p.bid, PathPunched, 15*time.Second)
	p.a.mu.Lock()
	old := p.a.peers[p.bid]
	p.a.mu.Unlock()

	p.b.mu.Lock()
	reg := p.b.reg
	p.b.mu.Unlock()
	reg.closeID(p.aid)

	deadline := time.Now().Add(20 * time.Second)
	for {
		res := waitPath(t, p.a, p.bid, PathPunched, 20*time.Second)
		p.a.mu.Lock()
		cur := p.a.peers[p.bid]
		p.a.mu.Unlock()
		if cur != old && res.Path == PathPunched {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("打洞连接断开后没有重新打通")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if r := lastPunch(t, p.a, p.bid); r == nil || !r.OK || !strings.Contains(r.Addr, "127.0.0.1") {
		t.Fatalf("重新打通后的记录不对：%+v", r)
	}
}

// ⑧ B 重启（Stop 时正常关闭 QUIC 连接）：A 立即察觉、几秒内经中转或重新打洞恢复，
// 而不是等 QUIC 空闲超时（45 秒）——那段时间里 A 到 B 的请求会全部失败。
func TestPunchPeerRestartRecoversQuickly(t *testing.T) {
	p := punchPair(t, []func(*Options){withPunch}, []func(*Options){withPunch})
	waitPath(t, p.a, p.bid, PathPunched, 15*time.Second)
	if err := p.b.Reload(); err != nil {
		t.Fatal(err)
	}
	waitConnected(t, p.b)
	start := time.Now()
	res := helloEventually(t, p.a, p.bid)
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("B 重启后 A 用了 %s 才恢复（应远小于 QUIC 空闲超时 %s）：%+v", took, quicIdle, res)
	}
}
