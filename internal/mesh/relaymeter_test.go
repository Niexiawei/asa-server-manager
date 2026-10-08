package mesh

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"asa-server/internal/meshcoord"
	"asa-server/pkg/meshid"
)

func peerView(t *testing.T, m *Manager, id meshid.ID) PeerView {
	t.Helper()
	views, err := m.Peers()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.NodeID == id {
			return v
		}
	}
	t.Fatalf("对端列表里没有 %s", id.Short())
	return PeerView{}
}

// 管理器按对端计的中转字节（密文，TLS 之下）与协调节点库里记的完全一致：A 发出的 = B 收到的 = 协调节点
// 记的 A → B，反方向同理；Status 的合计等于各对端之和（P7-4、P7-5）。
func TestRelayBytesMatchCoordinator(t *testing.T) {
	p := newPaired(t, RoleOperator)
	if res := helloEventually(t, p.a, p.bid); res.Path != PathRelay {
		t.Fatalf("应经中转：%+v", res)
	}
	// 再走一次隧道请求，让字节数大一些。
	helloEventually(t, p.b, p.aid)

	var last string
	deadline := time.Now().Add(15 * time.Second)
	for {
		av, bv := peerView(t, p.a, p.bid).RelayBytes, peerView(t, p.b, p.aid).RelayBytes
		var ab, ba int64 // 协调节点记的 A → B、B → A
		list, err := p.c.store.ListRelaySessions(meshcoord.RelayFilter{})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range list {
			switch {
			case r.From == p.aid && r.To == p.bid:
				ab, ba = ab+r.BytesAB, ba+r.BytesBA
			case r.From == p.bid && r.To == p.aid:
				ab, ba = ab+r.BytesBA, ba+r.BytesAB
			}
		}
		st := p.a.Status().Relay
		ok := av.Sent > 0 && av.Received > 0 && av.Sent == bv.Received && av.Received == bv.Sent &&
			av.Sent == ab && av.Received == ba && st.RelayBytes == av && !st.Since.IsZero()
		if ok {
			return
		}
		last = "A 对 B " + fmtRB(av) + "；B 对 A " + fmtRB(bv) + "；协调节点 A→B " + itoa(ab) + " B→A " + itoa(ba) + "；A 合计 " + fmtRB(st.RelayBytes)
		if time.Now().After(deadline) {
			t.Fatalf("中转字节数对不上：%s", last)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func fmtRB(b RelayBytes) string { return "发 " + itoa(b.Sent) + " 收 " + itoa(b.Received) }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// 直连不计入中转字节。
func TestRelayBytesNotCountedOnDirect(t *testing.T) {
	p := newPaired(t, RoleOperator, withListen)
	waitCandidates(t, p.c, p.b)
	if res := waitPath(t, p.a, p.bid, PathLAN, 20*time.Second); res == nil {
		t.Fatal("应走内网直连")
	}
	before := peerView(t, p.a, p.bid).RelayBytes
	for range 5 {
		if res := helloEventually(t, p.a, p.bid); res.Path != PathLAN {
			t.Fatalf("应保持直连：%+v", res)
		}
	}
	if after := peerView(t, p.a, p.bid).RelayBytes; after != before {
		t.Fatalf("直连上的流量不应计入中转：%+v → %+v", before, after)
	}
}

// mesh 重启后从零开始计。
func TestRelayBytesResetOnRestart(t *testing.T) {
	p := newPaired(t, RoleOperator)
	helloEventually(t, p.a, p.bid)
	if v := peerView(t, p.a, p.bid).RelayBytes; v.Sent == 0 {
		t.Fatalf("经中转之后应有字节数：%+v", v)
	}
	if err := p.a.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := p.a.Start(); err != nil {
		t.Fatal(err)
	}
	if v := peerView(t, p.a, p.bid).RelayBytes; v != (RelayBytes{}) {
		t.Fatalf("重启后应从零开始：%+v", v)
	}
	if st := p.a.Status().Relay; st.RelayBytes != (RelayBytes{}) {
		t.Fatalf("重启后合计应从零开始：%+v", st)
	}
}

// 每日中转额度用完（P7-3）：正在用的中转连接不切断；断开后新的中转被拒，对端行的 last_error 是人话、不带 gRPC 前缀。
func TestRelayDailyQuotaShownOnPeer(t *testing.T) {
	c := newTestCoord(t, func(c *testCoord) { c.limits.RelayDailyMiB = 1 })
	p := newPairedOn(t, c, RoleOperator)
	srv := p.proxy(t, "")
	// 一次下载跨过额度：在途会话不切断，整个下载完成。
	const n = 2 << 20
	resp, err := http.Get(srv.URL + "/download?n=" + strconv.Itoa(n))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if got != n {
		t.Fatalf("跨过额度的下载应完整完成，只收到 %d 字节", got)
	}

	// B 重启，A 到 B 的中转断开；再连就要新的中转，被拒。
	if err := p.b.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := p.b.Start(); err != nil {
		t.Fatal(err)
	}
	waitConnected(t, p.b)
	deadline := time.Now().Add(15 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, err := p.a.Hello(ctx, p.bid)
		cancel()
		if err != nil && strings.Contains(err.Error(), "今日中转额度已用完") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("额度用完后新的中转应被拒，最后一次：%v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	le := peerView(t, p.a, p.bid).LastError
	if !strings.Contains(le, "中转：本机今日中转额度已用完") || strings.Contains(le, "rpc error") {
		t.Fatalf("对端行应显示协调节点的原话：%q", le)
	}
}
