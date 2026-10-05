package mesh

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/meshid"
)

// withListen 让管理器在 127.0.0.1 的随机端口上监听，并把它作为唯一的 HOST 候选上报
// （真实枚举会过滤掉回环地址）。
func withListen(o *Options) {
	o.ListenAddr = "127.0.0.1:0"
	o.localCandidates = func(port int) []*meshpb.Candidate {
		if port == 0 {
			return nil
		}
		return []*meshpb.Candidate{{
			Transport: meshpb.Transport_TRANSPORT_TCP,
			Kind:      meshpb.CandidateKind_CANDIDATE_KIND_HOST,
			Addr:      net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		}}
	}
}

func fastUpgrade(o *Options) {
	o.UpgradeMin = 100 * time.Millisecond
	o.UpgradeMax = 400 * time.Millisecond
}

// gateListener 可以「拔网线」：关上时 Accept 到的连接立即断开（对端看到的是握手失败）。
type gateListener struct {
	net.Listener
	blocked atomic.Bool
}

func (l *gateListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.blocked.Load() {
			c.Close()
			continue
		}
		return c, nil
	}
}

func withGate(g **gateListener, blocked bool) func(*Options) {
	return func(o *Options) {
		o.wrapListener = func(ln net.Listener) net.Listener {
			gl := &gateListener{Listener: ln}
			gl.blocked.Store(blocked)
			*g = gl
			return gl
		}
	}
}

func listenAddr(t *testing.T, m *Manager) string {
	t.Helper()
	a := m.Status().ListenAddr
	if a == "" {
		t.Fatalf("管理器没有在监听：%+v", m.Status())
	}
	return a
}

// P2 验收：同一内网走直连。
func TestDirectLAN(t *testing.T) {
	c := newTestCoord(t)
	a := newManager(t, c, "a", withListen)
	b := newManager(t, c, "b", withListen)
	waitConnected(t, a, b)
	waitCandidates(t, c, b)

	res := helloEventually(t, a, nodeID(t, b))
	if res.Path != PathLAN || res.Version != "b" {
		t.Fatalf("同一内网应走直连：%+v", res)
	}
}

// waitCandidates 等到 b 的候选在协调节点上可见（登记带着候选，通常立即可见）。
func waitCandidates(t *testing.T, c *testCoord, b *Manager) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(b.Status().Candidates) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("b 没有候选地址")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 无协调节点模式：只按手填地址直连。
func TestNoCoordinatorMode(t *testing.T) {
	a := newManager(t, nil, "a", withListen)
	b := newManager(t, nil, "b", withListen)
	if st := a.Status(); st.Coordinator != "" || st.Connected || !st.Running {
		t.Fatalf("无协调节点模式的状态不对：%+v", st)
	}
	bid := nodeID(t, b)
	if _, err := a.Hello(context.Background(), bid); err == nil {
		t.Fatal("没有手填地址、没有协调节点时 Hello 应失败")
	}
	addrs := []string{listenAddr(t, b)}
	if err := a.Store().SetPeer(bid, PeerPatch{Addrs: &addrs}); err != nil {
		t.Fatal(err)
	}
	res := helloEventually(t, a, bid)
	if res.Path != PathLAN || res.Version != "b" {
		t.Fatalf("应按手填地址直连：%+v", res)
	}
}

// 拨到「同一地址的别的机器」：钉公钥让握手失败、落到中转，而不是把请求发给错的人。
func TestWrongMachineFallsBackToRelay(t *testing.T) {
	c := newTestCoord(t)
	a := newManager(t, c, "a")
	b := newManager(t, c, "b")                    // 不监听：只剩手填的那个（错误的）地址
	impostor := newManager(t, c, "c", withListen) // 冒充者
	waitConnected(t, a, b, impostor)

	bid := nodeID(t, b)
	addrs := []string{listenAddr(t, impostor)}
	if err := a.Store().SetPeer(bid, PeerPatch{Addrs: &addrs}); err != nil {
		t.Fatal(err)
	}
	res := helloEventually(t, a, bid)
	if res.Path != PathRelay || res.Version != "b" {
		t.Fatalf("身份不符的直连必须失败并落到中转，回答必须来自 B：%+v", res)
	}
}

// 断开 → 中转 → 恢复 → 升级为直连；升级时旧连接上的在途使用者不被打断。
func TestRelayUpgradesToDirect(t *testing.T) {
	c := newTestCoord(t)
	var gate *gateListener
	a := newManager(t, c, "a", fastUpgrade)
	b := newManager(t, c, "b", withListen, withGate(&gate, true))
	waitConnected(t, a, b)
	waitCandidates(t, c, b)
	bid := nodeID(t, b)

	if res := helloEventually(t, a, bid); res.Path != PathRelay {
		t.Fatalf("直连被挡住时应走中转：%+v", res)
	}
	// 一个「在途流」：持有旧（中转）连接的引用。
	old, release, err := a.acquire(bid)
	if err != nil {
		t.Fatal(err)
	}
	gate.blocked.Store(false)

	deadline := time.Now().Add(10 * time.Second)
	for {
		a.mu.Lock()
		cur := a.peers[bid]
		a.mu.Unlock()
		if cur != old {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("恢复连通后没有升级为直连")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if res := helloEventually(t, a, bid); res.Path != PathLAN {
		t.Fatalf("升级后新请求应走直连：%+v", res)
	}
	old.mu.Lock()
	closedEarly := old.closed
	old.mu.Unlock()
	if closedEarly {
		t.Fatal("旧连接还有人在用，不该被关闭")
	}
	release()
	old.mu.Lock()
	closedAfter := old.closed
	old.mu.Unlock()
	if !closedAfter {
		t.Fatal("旧连接没人用了应立即关闭")
	}
}

// Peer 端口被占不致命：mesh 照常启动，中转照常可用。
func TestListenFailureNotFatal(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	c := newTestCoord(t)
	a := newManager(t, c, "a")
	b := newManager(t, c, "b", func(o *Options) { o.ListenAddr = occupied.Addr().String() })
	waitConnected(t, a, b)
	st := b.Status()
	if st.ListenError == "" || st.ListenAddr != "" {
		t.Fatalf("端口被占应记下 listen_error：%+v", st)
	}
	if res := helloEventually(t, a, nodeID(t, b)); res.Path != PathRelay {
		t.Fatalf("监听失败时应能经中转被连接：%+v", res)
	}
}

func fakeTLSConn(t *testing.T) (*tls.Conn, net.Conn) {
	t.Helper()
	c1, c2 := net.Pipe()
	return tls.Client(c1, &tls.Config{}), c2
}

func TestRaceDialStaggers(t *testing.T) {
	targets := []dialTarget{{"black-hole:1", PathLAN}, {"good:1", PathLAN}}
	start := time.Now()
	var losers []net.Conn
	var mu sync.Mutex
	c, won, err := raceDial(context.Background(), targets, 100*time.Millisecond, func(ctx context.Context, tg dialTarget) (*tls.Conn, error) {
		if tg.addr == "black-hole:1" {
			<-ctx.Done() // 不回 SYN：一直挂到被取消
			return nil, ctx.Err()
		}
		tc, other := fakeTLSConn(t)
		mu.Lock()
		losers = append(losers, other)
		mu.Unlock()
		return tc, nil
	})
	if err != nil || won.addr != "good:1" {
		t.Fatalf("第二个候选应胜出：%v %+v", err, won)
	}
	c.Close()
	if d := time.Since(start); d < 100*time.Millisecond || d > time.Second {
		t.Fatalf("第二个候选应在错开间隔之后发起：%s", d)
	}
}

func TestRaceDialFailureStartsNextImmediately(t *testing.T) {
	targets := []dialTarget{{"refused:1", PathLAN}, {"good:1", PathPublic}}
	start := time.Now()
	c, won, err := raceDial(context.Background(), targets, time.Second, func(ctx context.Context, tg dialTarget) (*tls.Conn, error) {
		if tg.addr == "refused:1" {
			return nil, errors.New("connection refused")
		}
		tc, _ := fakeTLSConn(t)
		return tc, nil
	})
	if err != nil || won.kind != PathPublic {
		t.Fatalf("应由第二个候选胜出：%v %+v", err, won)
	}
	c.Close()
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("前一个失败时应立即发起下一个，不必等错开间隔：%s", d)
	}

	_, _, err = raceDial(context.Background(), targets[:1], time.Second, func(context.Context, dialTarget) (*tls.Conn, error) {
		return nil, errors.New("connection refused")
	})
	if err == nil {
		t.Fatal("全部失败时应返回错误")
	}
}

func TestPlanDirect(t *testing.T) {
	cand := func(kind meshpb.CandidateKind, tr meshpb.Transport, addr string) *meshpb.Candidate {
		return &meshpb.Candidate{Kind: kind, Transport: tr, Addr: addr}
	}
	host, conf, srflx := meshpb.CandidateKind_CANDIDATE_KIND_HOST, meshpb.CandidateKind_CANDIDATE_KIND_CONFIGURED, meshpb.CandidateKind_CANDIDATE_KIND_SRFLX
	tcp, udp := meshpb.Transport_TRANSPORT_TCP, meshpb.Transport_TRANSPORT_UDP
	cands := []*meshpb.Candidate{
		cand(host, tcp, "192.168.1.20:19194"),
		cand(host, tcp, "203.0.113.5:19194"),
		cand(conf, tcp, "b.example.com:19194"),
		cand(srflx, udp, "198.51.100.1:5000"), // P6 的 UDP 候选，直连不用
		cand(host, udp, "192.168.1.20:19194"),
	}
	manual := []string{"10.0.0.9:19194", "192.168.1.20:19194"} // 第二个与协调节点给的重复

	got := planDirect(cands, manual, true)
	want := []dialTarget{
		{"10.0.0.9:19194", PathLAN}, {"192.168.1.20:19194", PathLAN},
		{"203.0.113.5:19194", PathPublic}, {"b.example.com:19194", PathPublic},
	}
	if !equalTargets(got, want) {
		t.Fatalf("出口相同时 lan 在前、手填在前：\n got %+v\nwant %+v", got, want)
	}
	got = planDirect(cands, manual, false)
	want = append(want[2:4:4], want[0], want[1])
	if !equalTargets(got, want) {
		t.Fatalf("出口不同时 public 在前：\n got %+v\nwant %+v", got, want)
	}
}

func equalTargets(a, b []dialTarget) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestHostAddrsFilter(t *testing.T) {
	ip := netip.MustParseAddr
	ifs := []ifaceAddrs{
		{name: "以太网", up: true, addrs: []netip.Addr{ip("fe80::1"), ip("2001:db8::10"), ip("192.168.1.20"), ip("169.254.3.4")}},
		{name: "WLAN", up: true, addrs: []netip.Addr{ip("192.168.1.20"), ip("fd00::5")}}, // 重复的地址只报一次
		{name: "Loopback Pseudo-Interface 1", up: true, loop: true, addrs: []netip.Addr{ip("127.0.0.1")}},
		{name: "docker0", up: true, addrs: []netip.Addr{ip("172.17.0.1")}},
		{name: "br-1a2b3c", up: true, addrs: []netip.Addr{ip("172.18.0.1")}},
		{name: "veth12ab", up: true, addrs: []netip.Addr{ip("172.17.0.5")}},
		{name: "vEthernet (WSL (Hyper-V firewall))", up: true, addrs: []netip.Addr{ip("172.24.16.1")}},
		{name: "vEthernet (Default Switch)", up: true, addrs: []netip.Addr{ip("172.30.0.1")}},
		{name: "tailscale0", up: true, addrs: []netip.Addr{ip("100.101.102.103")}},
		{name: "eth1", up: false, addrs: []netip.Addr{ip("10.9.9.9")}},
	}
	got := hostAddrs(ifs)
	want := []netip.Addr{ip("192.168.1.20"), ip("100.101.102.103"), ip("2001:db8::10"), ip("fd00::5")}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}

	cs := buildCandidates(ifs, 0, []string{"b.example.com:19194"})
	if len(cs) != 1 || cs[0].GetKind() != meshpb.CandidateKind_CANDIDATE_KIND_CONFIGURED {
		t.Fatalf("不监听时只报手填的公网地址：%v", cs)
	}
	cs = buildCandidates(ifs, 19194, nil)
	if len(cs) != len(want) || cs[0].GetAddr() != "192.168.1.20:19194" || cs[2].GetAddr() != "[2001:db8::10]:19194" {
		t.Fatalf("HOST 候选不对：%v", cs)
	}
	if classify("100.101.102.103:1") != PathLAN || classify("8.8.8.8:1") != PathPublic || classify("x.example:1") != PathPublic {
		t.Fatal("地址归类不对")
	}
	_ = meshid.ID{}
}
