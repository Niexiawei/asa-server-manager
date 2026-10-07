package mesh

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/meshid"
	"asa-server/pkg/upnp"
	"asa-server/pkg/upnp/upnptest"
)

func fakeDiscover(t *testing.T, g *upnptest.IGD) func(context.Context) []*upnp.Gateway {
	return func(ctx context.Context) []*upnp.Gateway {
		gws, err := upnp.GatewaysAt(ctx, g.Location(), upnptest.LoopbackIP)
		if err != nil {
			t.Errorf("读假网关失败：%v", err)
		}
		return gws
	}
}

func testID(t *testing.T) meshid.ID {
	t.Helper()
	_, id, err := meshid.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// startMapper 起一个 portMapper，返回它与停止函数（停止 = 取消 + close，即 mesh Stop 的顺序）。
func startMapper(t *testing.T, pm *portMapper) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go pm.run(ctx)
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); pm.close() }) }
	t.Cleanup(stop)
	return stop
}

func waitUPnP(t *testing.T, pm *portMapper, state string) UPnPStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := pm.status()
		if st.State == state {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("UPnP 状态没有变成 %s：%+v", state, st)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

var bothPorts = []portReq{{upnp.TCP, 19194}, {upnp.UDP, 19195}}

// 映射 → 宣称 → 停止时删除。
func TestPortMapperMapsAndDeletes(t *testing.T) {
	g := upnptest.Start(t, upnptest.Options{})
	id := testID(t)
	changes := 0
	var mu sync.Mutex
	pm := newPortMapper(id, bothPorts, fakeDiscover(t, g), nil, func() { mu.Lock(); changes++; mu.Unlock() })
	stop := startMapper(t, pm)

	st := waitUPnP(t, pm, upnpMapped)
	if st.ExternalIP != "203.0.113.7" || len(st.Mappings) != 2 || st.Gateway != "Fake IGD" {
		t.Fatalf("状态不对：%+v", st)
	}
	if got := pm.mapped(upnp.TCP); len(got) != 1 || got[0].String() != "203.0.113.7:19194" {
		t.Fatalf("TCP 外部地址不对：%v", got)
	}
	if got := pm.mapped(upnp.UDP); len(got) != 1 || got[0].String() != "203.0.113.7:19195" {
		t.Fatalf("UDP 外部地址不对：%v", got)
	}
	m := g.Mappings()[upnptest.Key{Protocol: "TCP", ExternalPort: 19194}]
	if m.Lease != 3600 || !strings.Contains(m.Description, id.Short()) || m.InternalPort != 19194 {
		t.Fatalf("网关上的映射不对：%+v", m)
	}
	mu.Lock()
	if changes == 0 {
		t.Fatal("映射生效时应通知重新上报候选")
	}
	mu.Unlock()

	stop()
	if n := len(g.Mappings()); n != 0 {
		t.Fatalf("停止时应删除全部映射，还剩 %d 条", n)
	}
	if st := pm.status(); st.State != upnpStopped || len(pm.mapped(upnp.TCP)) != 0 {
		t.Fatalf("停止后不该再宣称映射：%+v", st)
	}
}

// 端口被同一内网的另一台机器占了：换到确定性序列里的端口；同一身份重来一次还是那个端口。
func TestPortMapperConflictDeterministic(t *testing.T) {
	g := upnptest.Start(t, upnptest.Options{})
	g.Reserve("TCP", 19194)
	id := testID(t)
	pm := newPortMapper(id, []portReq{{upnp.TCP, 19194}}, fakeDiscover(t, g), nil, nil)
	stop := startMapper(t, pm)
	if st := waitUPnP(t, pm, upnpMapped); len(st.Mappings) != 1 {
		t.Fatalf("状态不对：%+v", st)
	}
	first := pm.mapped(upnp.TCP)
	if len(first) != 1 || first[0].Port() == 19194 {
		t.Fatalf("被占时应换端口：%v", first)
	}
	stop()

	pm2 := newPortMapper(id, []portReq{{upnp.TCP, 19194}}, fakeDiscover(t, g), nil, nil)
	startMapper(t, pm2)
	waitUPnP(t, pm2, upnpMapped)
	if again := pm2.mapped(upnp.TCP); len(again) != 1 || again[0] != first[0] {
		t.Fatalf("同一身份应选到同一个外部端口（崩溃残留会被续约而不是越积越多）：%v vs %v", again, first)
	}
}

// 只支持永久映射：租期 0，停止时照样删掉（不然每次都留一条）。
func TestPortMapperPermanentOnly(t *testing.T) {
	g := upnptest.Start(t, upnptest.Options{})
	g.SetPermanentOnly(true)
	pm := newPortMapper(testID(t), bothPorts, fakeDiscover(t, g), nil, nil)
	stop := startMapper(t, pm)
	st := waitUPnP(t, pm, upnpMapped)
	for _, m := range st.Mappings {
		if !m.Permanent {
			t.Fatalf("应标为永久：%+v", st)
		}
	}
	stop()
	if n := len(g.Mappings()); n != 0 {
		t.Fatalf("永久映射也要在停止时删除，还剩 %d 条", n)
	}
}

// 上级还有 NAT：WAN 是私网 / CGNAT，或与 STUN 看到的出口不同 ⇒ 不映射、不宣称；出口对上之后恢复。
func TestPortMapperDoubleNAT(t *testing.T) {
	g := upnptest.Start(t, upnptest.Options{WANIP: "100.64.1.2"})
	pm := newPortMapper(testID(t), bothPorts, fakeDiscover(t, g), nil, nil)
	startMapper(t, pm)
	st := waitUPnP(t, pm, upnpDoubleNAT)
	if !strings.Contains(st.Error, "100.64.1.2") || len(g.Mappings()) != 0 || len(pm.mapped(upnp.TCP)) != 0 {
		t.Fatalf("CGNAT 的 WAN 地址应判为上级还有 NAT：%+v %v", st, g.Mappings())
	}

	g2 := upnptest.Start(t, upnptest.Options{})
	var mu sync.Mutex
	srflx := []netip.Addr{netip.MustParseAddr("198.51.100.1")}
	pm2 := newPortMapper(testID(t), bothPorts, fakeDiscover(t, g2), func() []netip.Addr {
		mu.Lock()
		defer mu.Unlock()
		return srflx
	}, nil)
	startMapper(t, pm2)
	if st := waitUPnP(t, pm2, upnpDoubleNAT); !strings.Contains(st.Error, "198.51.100.1") {
		t.Fatalf("WAN 与 STUN 出口不同应判为上级还有 NAT：%+v", st)
	}
	mu.Lock()
	srflx = []netip.Addr{netip.MustParseAddr("203.0.113.7")}
	mu.Unlock()
	pm2.kick()
	waitUPnP(t, pm2, upnpMapped)
}

// 路由器重启丢了映射：下一轮（这里用 kick 代替 30 分钟）按同样的外部端口重建。
func TestPortMapperRecreatesAfterRouterReboot(t *testing.T) {
	g := upnptest.Start(t, upnptest.Options{})
	pm := newPortMapper(testID(t), bothPorts, fakeDiscover(t, g), nil, nil)
	startMapper(t, pm)
	waitUPnP(t, pm, upnpMapped)
	g.Clear()
	pm.kick()
	deadline := time.Now().Add(5 * time.Second)
	for len(g.Mappings()) != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("路由器丢了映射后应重建：%v", g.Mappings())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, ok := g.Mappings()[upnptest.Key{Protocol: "UDP", ExternalPort: 19195}]; !ok {
		t.Fatalf("应按原外部端口重建：%v", g.Mappings())
	}
}

// 路由器拒绝映射（606 等非冲突错误）：不乱换端口，状态带出错误码。
func TestPortMapperDenied(t *testing.T) {
	g := upnptest.Start(t, upnptest.Options{})
	g.SetDenyAll(606)
	pm := newPortMapper(testID(t), bothPorts, fakeDiscover(t, g), nil, nil)
	startMapper(t, pm)
	if st := waitUPnP(t, pm, upnpError); !strings.Contains(st.Error, "606") {
		t.Fatalf("应带出路由器的错误码：%+v", st)
	}
	if n := len(g.Actions()); n > 1+len(bothPorts) {
		t.Fatalf("非冲突错误不该换端口重试（共 %d 次调用）", n)
	}
}

// 端到端：B 只有 UPnP 映射出来的地址（不上报网卡地址），A 第一次拨号就直连成功，不走中转。
func TestUPnPMappedTCPDirect(t *testing.T) {
	c := newTestCoord(t)
	g := upnptest.Start(t, upnptest.Options{WANIP: "127.0.0.1"})
	a := newManager(t, c, "a")
	b := newManager(t, c, "b", func(o *Options) {
		o.ListenAddr = "127.0.0.1:0"
		o.localCandidates = func(int) []*meshpb.Candidate { return nil }
		o.upnpDiscover = fakeDiscover(t, g)
		o.upnpAllowNonPublic = true
	})
	waitConnected(t, a, b)
	deadline := time.Now().Add(5 * time.Second)
	for b.Status().UPnP.State != upnpMapped {
		if time.Now().After(deadline) {
			t.Fatalf("B 没有映射成功：%+v", b.Status().UPnP)
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, port, _ := net.SplitHostPort(listenAddr(t, b))
	want := "127.0.0.1:" + port
	for !slices.Contains(b.Status().Candidates, want) {
		if time.Now().After(deadline) {
			t.Fatalf("映射出来的地址应进上报候选：%v", b.Status().Candidates)
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitCandidatesOnCoord(t, a, nodeID(t, b), want)
	if res := helloEventually(t, a, nodeID(t, b)); res.Path != PathPublic {
		t.Fatalf("应经 UPnP 映射的地址直连：%+v", res)
	}
	if p, _ := strconv.Atoi(port); g.Mappings()[upnptest.Key{Protocol: "TCP", ExternalPort: p}].InternalPort != p {
		t.Fatalf("假网关上应有 B 的 TCP 映射：%v", g.Mappings())
	}
}

// waitCandidatesOnCoord 等到 A 经 Resolve 能看到 B 的这个候选（上报是异步的）。
func waitCandidatesOnCoord(t *testing.T, a *Manager, b meshid.ID, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		a.mu.Lock()
		coord := a.coord
		a.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		res, err := coord.client.Resolve(ctx, &meshpb.ResolveRequest{NodeId: b.Compact()})
		cancel()
		if err == nil {
			for _, c := range res.GetCandidates() {
				if c.GetAddr() == want && c.GetKind() == meshpb.CandidateKind_CANDIDATE_KIND_MAPPED {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("协调节点上看不到 B 的映射候选 %s：%v %v", want, res.GetCandidates(), err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestUPnPVerdict(t *testing.T) {
	for ip, ok := range map[string]bool{
		"203.0.113.7": true, "0.0.0.0": false, "192.168.1.1": false, "100.64.1.2": false, "10.0.0.1": false,
	} {
		got, reason := UPnPVerdict(net.ParseIP(ip))
		if got != ok || (!ok && reason == "") {
			t.Errorf("%s：得到 %v（%s），期望 %v", ip, got, reason, ok)
		}
	}
	if _, reason := UPnPVerdict(net.ParseIP("0.0.0.0")); !strings.Contains(reason, "0.0.0.0") {
		t.Errorf("0.0.0.0 的原因要说清楚：%s", reason)
	}
}
