package meshcoord

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"asa-server/internal/mesh/meshpb"
)

func TestFirstJoinRequiresSecret(t *testing.T) {
	c := startCoord(t, ServerOptions{STUNAddrs: []string{"coord.example:3478", "coord.example:3479"}})
	n := c.newNode(t)

	if _, err := n.register(t, DefaultNetworkID, ""); codeOf(err) != codes.PermissionDenied {
		t.Fatalf("无密钥首次接入应被拒，得到 %v", err)
	}
	if _, err := n.register(t, DefaultNetworkID, "wrong"); codeOf(err) != codes.PermissionDenied {
		t.Fatalf("错误密钥应被拒，得到 %v", err)
	}
	if _, err := n.register(t, "no-such-network", c.secret); codeOf(err) != codes.PermissionDenied {
		t.Fatalf("不存在的网络应被拒，得到 %v", err)
	}
	s, err := n.register(t, DefaultNetworkID, c.secret)
	if err != nil {
		t.Fatal(err)
	}
	if s.registered.GetNodeId() != n.id.Compact() {
		t.Fatal("Registered 应回显按证书算出的节点 ID")
	}
	if !slices.Equal(s.registered.GetStunAddrs(), []string{"coord.example:3478", "coord.example:3479"}) {
		t.Fatalf("stun_addrs = %v", s.registered.GetStunAddrs())
	}
	if s.registered.GetObservedAddr() == "" {
		t.Fatal("Registered 应带出口地址")
	}
	s.cancel()

	// 已是成员：此后只凭证书，不需要密钥。
	if _, err := n.register(t, DefaultNetworkID, ""); err != nil {
		t.Fatalf("成员再次登记不该需要密钥：%v", err)
	}
}

func TestBannedNode(t *testing.T) {
	c := startCoord(t, ServerOptions{BanSweepInterval: 50 * time.Millisecond})
	n := c.newNode(t)
	s := n.mustRegister(t, c)

	// 运行中被拉黑（node ban 是另一个进程直接写库）：复查后会话被断开。
	if err := c.store.SetBanned(DefaultNetworkID, n.id, true); err != nil {
		t.Fatal(err)
	}
	m, err := s.stream.Recv()
	if err != nil || m.GetKicked() == nil {
		t.Fatalf("应先收到 Kicked，得到 %v / %v", m, err)
	}
	if err := recvErr(s.stream.Recv); codeOf(err) != codes.Aborted {
		t.Fatalf("被踢后流应以 Aborted 结束，得到 %v", err)
	}
	if _, err := n.register(t, DefaultNetworkID, c.secret); codeOf(err) != codes.PermissionDenied {
		t.Fatalf("被拉黑的节点即使带对密钥也应被拒，得到 %v", err)
	}

	// 预先拉黑一个从未接入的节点。
	m2 := c.newNode(t)
	if err := c.store.SetBanned(DefaultNetworkID, m2.id, true); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.register(t, DefaultNetworkID, c.secret); codeOf(err) != codes.PermissionDenied {
		t.Fatalf("预先拉黑的节点应被拒，得到 %v", err)
	}
}

// 安全用例：网络只能来自调用者自己的 Session，跨网络的节点一律查不到（§8.4.5）。
func TestCrossNetworkIsolation(t *testing.T) {
	c := startCoord(t, ServerOptions{})
	other, err := c.store.CreateNetwork("other", "另一个网络")
	if err != nil {
		t.Fatal(err)
	}
	a := c.newNode(t)
	b := c.newNode(t)
	a.mustRegister(t, c)
	if _, err := b.register(t, "other", other.Secret); err != nil {
		t.Fatal(err)
	}
	ghost := c.newNode(t) // 从未登记

	ctx := ctxTimeout(t, 5*time.Second)
	_, errOther := a.client.Resolve(ctx, &meshpb.ResolveRequest{NodeId: b.id.Compact()})
	_, errGhost := a.client.Resolve(ctx, &meshpb.ResolveRequest{NodeId: ghost.id.Compact()})
	if codeOf(errOther) != codes.NotFound || codeOf(errGhost) != codes.NotFound {
		t.Fatalf("跨网络与不存在都应是 NotFound：%v / %v", errOther, errGhost)
	}
	if errOther.Error() != errGhost.Error() {
		t.Fatalf("两种情况的错误必须完全相同，不能让调用者区分：%q vs %q", errOther, errGhost)
	}
	if _, err := a.client.OpenRelay(ctx, &meshpb.OpenRelayRequest{TargetNodeId: b.id.Compact()}); codeOf(err) != codes.NotFound {
		t.Fatalf("跨网络 OpenRelay 应是 NotFound，得到 %v", err)
	}
	// 同网络能查到。
	a2 := c.newNode(t)
	a2.mustRegister(t, c)
	resp, err := a.client.Resolve(ctx, &meshpb.ResolveRequest{NodeId: a2.id.String()})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetVersion() != "test" || resp.GetPeerObservedIp() != "127.0.0.1" || resp.GetSelfObservedIp() != "127.0.0.1" {
		t.Fatalf("Resolve 结果不对：%v", resp)
	}
}

func TestResolveRequiresOnlineSession(t *testing.T) {
	c := startCoord(t, ServerOptions{})
	a := c.newNode(t)
	b := c.newNode(t)
	b.mustRegister(t, c)
	ctx := ctxTimeout(t, 5*time.Second)
	if _, err := a.client.Resolve(ctx, &meshpb.ResolveRequest{NodeId: b.id.Compact()}); codeOf(err) != codes.FailedPrecondition {
		t.Fatalf("没有在线会话的调用者应被拒，得到 %v", err)
	}
	if _, err := a.client.OpenRelay(ctx, &meshpb.OpenRelayRequest{TargetNodeId: b.id.Compact()}); codeOf(err) != codes.FailedPrecondition {
		t.Fatalf("没有在线会话的调用者应被拒，得到 %v", err)
	}
}

func TestSecondSessionKicksFirst(t *testing.T) {
	c := startCoord(t, ServerOptions{})
	cert, _ := newIdentity(t)
	first := c.dial(t, cert)
	s1 := first.mustRegister(t, c)

	second := c.dial(t, cert) // 同一把私钥，另一条连接（另一台机器）
	second.mustRegister(t, c)

	m, err := s1.stream.Recv()
	if err != nil || m.GetKicked() == nil {
		t.Fatalf("旧会话应收到 Kicked，得到 %v / %v", m, err)
	}
	if err := recvErr(s1.stream.Recv); codeOf(err) != codes.Aborted {
		t.Fatalf("旧会话应以 Aborted 结束，得到 %v", err)
	}
	if n, _ := c.srv.OnlineCount(); n != 1 {
		t.Fatalf("在线节点应只剩 1 个，实际 %d", n)
	}
}

// openRelay 让 a 发起到 b 的中转，返回双方的凭据。
func openRelay(t *testing.T, a *node, sb *clientSession, b *node) (sessionID string, tokenA, tokenB []byte) {
	t.Helper()
	resp, err := a.client.OpenRelay(ctxTimeout(t, 5*time.Second), &meshpb.OpenRelayRequest{TargetNodeId: b.id.Compact()})
	if err != nil {
		t.Fatal(err)
	}
	ir := sb.nextIncomingRelay(t)
	if ir.GetSessionId() != resp.GetSessionId() || ir.GetFromNodeId() != a.id.Compact() {
		t.Fatalf("IncomingRelay 不对：%v", ir)
	}
	return resp.GetSessionId(), resp.GetToken(), ir.GetToken()
}

func TestRelayTokensAreBoundAndOneTime(t *testing.T) {
	c := startCoord(t, ServerOptions{RelayJoinTimeout: 300 * time.Millisecond})
	a, b, x := c.newNode(t), c.newNode(t), c.newNode(t)
	a.mustRegister(t, c)
	sb := b.mustRegister(t, c)
	x.mustRegister(t, c)

	sid, tokenA, tokenB := openRelay(t, a, sb, b)

	// 第三方拿到 A 的 token 也用不了（凭据绑定节点）。
	if err := recvErr(x.joinRelay(t, sid, tokenA).Recv); codeOf(err) != codes.PermissionDenied {
		t.Fatalf("第三方用别人的 token 应被拒，得到 %v", err)
	}
	// A 拿 B 的 token 也不行。
	if err := recvErr(a.joinRelay(t, sid, tokenB).Recv); codeOf(err) != codes.PermissionDenied {
		t.Fatalf("A 用 B 的 token 应被拒，得到 %v", err)
	}
	ra := a.joinRelay(t, sid, tokenA)
	// 一次性：A 再 Join 一次被拒。
	time.Sleep(50 * time.Millisecond)
	if err := recvErr(a.joinRelay(t, sid, tokenA).Recv); codeOf(err) != codes.PermissionDenied {
		t.Fatalf("同一 token 第二次使用应被拒，得到 %v", err)
	}
	// B 一直不来：期限到了，A 那一侧以 PermissionDenied 结束，迟到的 B 也被拒。
	if err := recvErr(ra.Recv); codeOf(err) != codes.PermissionDenied {
		t.Fatalf("配对期限到了应被拒，得到 %v", err)
	}
	if err := recvErr(b.joinRelay(t, sid, tokenB).Recv); codeOf(err) != codes.PermissionDenied {
		t.Fatalf("过期后 Join 应被拒，得到 %v", err)
	}
	if _, relays := c.srv.OnlineCount(); relays != 0 {
		t.Fatalf("过期的中转会话应被清理，剩 %d", relays)
	}
}

func TestRelayIntegrity(t *testing.T) {
	c := startCoord(t, ServerOptions{})
	a, b := c.newNode(t), c.newNode(t)
	a.mustRegister(t, c)
	sb := b.mustRegister(t, c)
	sid, tokenA, tokenB := openRelay(t, a, sb, b)
	ra := a.joinRelay(t, sid, tokenA)
	rb := b.joinRelay(t, sid, tokenB)

	const size = 8 << 20
	pump := func(src []byte, send func(*meshpb.RelayFrame) error, closeSend func() error) error {
		for off := 0; off < len(src); off += 32 << 10 {
			if err := send(&meshpb.RelayFrame{Msg: &meshpb.RelayFrame_Data{Data: src[off:min(off+32<<10, len(src))]}}); err != nil {
				return err
			}
		}
		return nil
	}
	drain := func(recv func() (*meshpb.RelayFrame, error), n int) ([]byte, error) {
		var buf bytes.Buffer
		for buf.Len() < n {
			f, err := recv()
			if err != nil {
				return buf.Bytes(), err
			}
			buf.Write(f.GetData())
		}
		return buf.Bytes(), nil
	}

	dataA := make([]byte, size)
	dataB := make([]byte, size)
	rand.Read(dataA)
	rand.Read(dataB)
	var wg sync.WaitGroup
	var gotAtB, gotAtA []byte
	var errs [4]error
	wg.Add(4)
	go func() { defer wg.Done(); errs[0] = pump(dataA, ra.Send, ra.CloseSend) }()
	go func() { defer wg.Done(); errs[1] = pump(dataB, rb.Send, rb.CloseSend) }()
	go func() { defer wg.Done(); gotAtB, errs[2] = drain(rb.Recv, size) }()
	go func() { defer wg.Done(); gotAtA, errs[3] = drain(ra.Recv, size) }()
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	if sha256.Sum256(gotAtB) != sha256.Sum256(dataA) || sha256.Sum256(gotAtA) != sha256.Sum256(dataB) {
		t.Fatal("中转后的数据不一致")
	}

	// 一侧结束，另一侧也随之结束。
	ra.CloseSend()
	if err := recvErr(rb.Recv); err != io.EOF {
		t.Fatalf("A 结束后 B 应读到 EOF，得到 %v", err)
	}
}

func TestRelayConcurrencyLimit(t *testing.T) {
	c := startCoord(t, ServerOptions{Limits: LimitsConfig{MaxRelaysPerNode: 2}})
	a, b := c.newNode(t), c.newNode(t)
	a.mustRegister(t, c)
	sb := b.mustRegister(t, c)
	openRelay(t, a, sb, b)
	openRelay(t, a, sb, b)
	_, err := a.client.OpenRelay(ctxTimeout(t, 5*time.Second), &meshpb.OpenRelayRequest{TargetNodeId: b.id.Compact()})
	if codeOf(err) != codes.ResourceExhausted {
		t.Fatalf("超过每节点中转上限应是 ResourceExhausted，得到 %v", err)
	}
}

func TestRelayIdleTimeout(t *testing.T) {
	c := startCoord(t, ServerOptions{Limits: LimitsConfig{RelayIdleTimeout: 200 * time.Millisecond}})
	a, b := c.newNode(t), c.newNode(t)
	a.mustRegister(t, c)
	sb := b.mustRegister(t, c)
	sid, tokenA, tokenB := openRelay(t, a, sb, b)
	ra := a.joinRelay(t, sid, tokenA)
	b.joinRelay(t, sid, tokenB)
	start := time.Now()
	if err := recvErr(ra.Recv); err != io.EOF {
		t.Fatalf("空闲超时后流应正常结束，得到 %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("空闲超时没有及时生效")
	}
}

func TestOpenRelayToSelfRejected(t *testing.T) {
	c := startCoord(t, ServerOptions{})
	a := c.newNode(t)
	a.mustRegister(t, c)
	_, err := a.client.OpenRelay(ctxTimeout(t, 5*time.Second), &meshpb.OpenRelayRequest{TargetNodeId: a.id.Compact()})
	if codeOf(err) != codes.InvalidArgument {
		t.Fatalf("中转到自己应被拒，得到 %v", err)
	}
}
