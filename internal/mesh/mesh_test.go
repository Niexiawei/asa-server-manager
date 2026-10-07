package mesh

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"asa-server/internal/mesh/meshpb"
	"asa-server/internal/meshcoord"
	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
	"asa-server/pkg/meshjoin"
)

func TestMain(m *testing.M) {
	cleanup := logger.InitTempForTest()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// testCoord 是进程内的协调节点，可以在同一地址上停掉再起（模拟协调节点重启）。
type testCoord struct {
	t     *testing.T
	addr  string
	cert  tls.Certificate
	id    meshid.ID
	store *meshcoord.Store
	blob  string
	gs    *grpc.Server
	srv   *meshcoord.Server
	// stunAddrs 是下发给管理器的 STUN 地址；默认是一个没人监听的假地址。
	stunAddrs []string
}

func newTestCoord(t *testing.T, opts ...func(*testCoord)) *testCoord {
	t.Helper()
	store, err := meshcoord.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	n, _, err := store.EnsureDefaultNetwork()
	if err != nil {
		t.Fatal(err)
	}
	cert, id, err := meshid.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := &testCoord{t: t, addr: "127.0.0.1:0", cert: cert, id: id, store: store, stunAddrs: []string{"127.0.0.1:3478"}}
	for _, f := range opts {
		f(c)
	}
	c.start()
	c.blob, err = meshjoin.JoinBlob{Addr: c.addr, Coordinator: id, NetworkID: n.ID, NetworkSecret: n.Secret}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.stop)
	return c
}

func (c *testCoord) start() {
	c.t.Helper()
	var lis net.Listener
	var err error
	// 重启时同一端口可能还没完全释放，稍等重试。
	for i := 0; i < 50; i++ {
		if lis, err = net.Listen("tcp", c.addr); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		c.t.Fatal(err)
	}
	c.addr = lis.Addr().String()
	c.srv = meshcoord.NewServer(meshcoord.ServerOptions{Store: c.store, STUNAddrs: c.stunAddrs})
	c.gs = meshcoord.NewGRPCServer(c.cert, c.srv)
	go c.gs.Serve(lis)
}

func (c *testCoord) stop() {
	if c.gs != nil {
		c.gs.Stop()
		c.srv.Close()
		c.gs = nil
	}
}

// newManager 建一个接入 c 的管理器（c 为 nil = 无协调节点模式）。默认不监听 Peer 端口，
// 于是对端之间只能走中转；要直连的用例传 withListen。
func newManager(t *testing.T, c *testCoord, version string, opts ...func(*Options)) *Manager {
	t.Helper()
	dir := t.TempDir()
	if c != nil {
		if _, err := Join(dir, c.blob); err != nil {
			t.Fatal(err)
		}
	} else if _, err := SetEnabled(dir, true); err != nil {
		t.Fatal(err)
	}
	o := Options{Dir: dir, Version: version, BackoffMin: 50 * time.Millisecond}
	for _, f := range opts {
		f(&o)
	}
	if o.ListenAddr == "" {
		noListen := true
		if _, err := UpdateConfig(dir, ConfigPatch{NoListen: &noListen}); err != nil {
			t.Fatal(err)
		}
	}
	// 打洞同理：默认关（不绑 UDP），要打洞的用例传 withPunch。
	if o.UDPListenAddr == "" {
		noPunch := true
		if _, err := UpdateConfig(dir, ConfigPatch{NoPunch: &noPunch}); err != nil {
			t.Fatal(err)
		}
	}
	// UPnP 同理：默认关——测试绝不能去动开发机所在路由器的端口映射。要的用例传 withFakeUPnP。
	if o.upnpDiscover == nil {
		noUPnP := true
		if _, err := UpdateConfig(dir, ConfigPatch{NoUPnP: &noUPnP}); err != nil {
			t.Fatal(err)
		}
	}
	m := New(o)
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop() })
	return m
}

func waitConnected(t *testing.T, ms ...*Manager) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for _, m := range ms {
		for !m.Status().Connected {
			if time.Now().After(deadline) {
				t.Fatalf("管理器没有登记上协调节点：%+v", m.Status())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// helloEventually 重试 Hello 直到成功：协调节点刚重启时，旧的对端连接要先失败一次才会重新选路。
func helloEventually(t *testing.T, from *Manager, to meshid.ID) *HelloResult {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		res, err := from.Hello(ctx, to)
		cancel()
		if err == nil {
			return res
		}
		if time.Now().After(deadline) {
			t.Fatalf("Hello %s 一直失败：%v", to.Short(), err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func nodeID(t *testing.T, m *Manager) meshid.ID {
	t.Helper()
	id, err := meshid.ParseID(m.Status().NodeID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// P1 的目标闭环：两台管理器各自只能外联协调节点，A 经中转调到 B 的 Hello。
func TestHelloOverRelay(t *testing.T) {
	c := newTestCoord(t)
	a := newManager(t, c, "1.0-a")
	b := newManager(t, c, "1.0-b")
	waitConnected(t, a, b)

	st := a.Status()
	if len(st.STUNAddrs) != 1 || st.STUNAddrs[0] != "127.0.0.1:3478" {
		t.Fatalf("Status 应带协调节点下发的 stun_addrs：%+v", st)
	}

	res := helloEventually(t, a, nodeID(t, b))
	if res.Path != PathRelay || res.Version != "1.0-b" || res.GrantedRole != meshpb.Role_ROLE_NONE.String() {
		t.Fatalf("Hello 结果不对：%+v", res)
	}
	back := helloEventually(t, b, nodeID(t, a))
	if back.Version != "1.0-a" {
		t.Fatalf("反向 Hello 结果不对：%+v", back)
	}

	// 协调节点重启：双方自动重新登记，Hello 恢复。
	c.stop()
	deadline := time.Now().Add(10 * time.Second)
	for a.Status().Connected || b.Status().Connected {
		if time.Now().After(deadline) {
			t.Fatal("协调节点停掉后状态应变为断开")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if a.Status().LastError == "" {
		t.Fatal("断开后应记下最近的错误")
	}
	c.start()
	waitConnected(t, a, b)
	if res := helloEventually(t, a, nodeID(t, b)); res.Version != "1.0-b" {
		t.Fatalf("重启后 Hello 结果不对：%+v", res)
	}
}

func TestStartNotConfiguredHasNoSideEffects(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mesh")
	m := New(Options{Dir: dir})
	if err := m.Start(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("未配置应返回 ErrNotConfigured，得到 %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("未配置时不该建出 mesh 目录")
	}
	st := m.Status()
	if st.Configured || st.Running || st.NodeID != "" || st.LastError != "" {
		t.Fatalf("未配置时的状态不对：%+v", st)
	}
	if _, err := m.Hello(context.Background(), meshid.ID{1}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("未运行时 Hello 应返回 ErrNotRunning，得到 %v", err)
	}
}

func TestJoinLeave(t *testing.T) {
	c := newTestCoord(t)
	dir := t.TempDir()
	if _, err := Join(dir, "garbage"); !errors.Is(err, meshjoin.ErrPrefix) {
		t.Fatalf("坏的 join blob 应被拒，得到 %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ConfigFileName)); !os.IsNotExist(err) {
		t.Fatal("join blob 无效时不该写配置")
	}
	cc, err := Join(dir, c.blob)
	if err != nil {
		t.Fatal(err)
	}
	if cc.ID != c.id || cc.Addr != c.addr {
		t.Fatalf("配置不对：%+v", cc)
	}
	cfg, err := LoadConfig(dir)
	if err != nil || !cfg.Enabled {
		t.Fatalf("Join 后应已启用：%+v %v", cfg, err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Join(dir, ConfigFileName))
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("config.json 含网络密钥，权限应为 600，实际 %o", info.Mode().Perm())
		}
	}
	if err := Leave(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(dir); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Leave 后应为未配置，得到 %v", err)
	}
}

// SetCoordinator 只写协调节点，不碰启用开关（页面上协调节点只是一项配置）；Join（CLI）仍会顺带启用。
func TestSetCoordinatorKeepsEnabled(t *testing.T) {
	c := newTestCoord(t)
	for _, enabled := range []bool{false, true} {
		dir := t.TempDir()
		if _, err := SetEnabled(dir, enabled); err != nil {
			t.Fatal(err)
		}
		cc, err := SetCoordinator(dir, c.blob)
		if err != nil {
			t.Fatal(err)
		}
		if cc.ID != c.id || cc.Addr != c.addr {
			t.Fatalf("配置不对：%+v", cc)
		}
		cfg, err := readConfig(dir)
		if err != nil && !errors.Is(err, ErrNotConfigured) {
			t.Fatal(err)
		}
		if cfg.Enabled != enabled || cfg.Coordinator == nil {
			t.Fatalf("SetCoordinator 不该改启用开关（原为 %v）：%+v", enabled, cfg)
		}
	}
	if _, err := SetCoordinator(t.TempDir(), "garbage"); !errors.Is(err, meshjoin.ErrPrefix) {
		t.Fatalf("坏的 join blob 应被拒，得到 %v", err)
	}
}

type fakeAuth map[meshid.ID]string

func (f fakeAuth) Grant(id meshid.ID) string { return f[id] }

// 拦截器的放行表：未授权的身份只能调 Hello 与 Pair。
func TestMethodAllowTable(t *testing.T) {
	stranger, friend := meshid.ID{1}, meshid.ID{2}
	auth := fakeAuth{friend: RoleOperator}
	cases := []struct {
		id     meshid.ID
		method string
		want   bool
	}{
		{stranger, meshpb.Peer_Hello_FullMethodName, true},
		{stranger, meshpb.Peer_Pair_FullMethodName, true},
		{stranger, meshpb.Peer_HTTP_FullMethodName, false},
		{stranger, "/asamesh.v1.Peer/Anything", false},
		{friend, "/asamesh.v1.Peer/HTTP", true},
	}
	for _, c := range cases {
		if got := methodAllowed(auth, c.id, c.method); got != c.want {
			t.Errorf("methodAllowed(%s, %s) = %v，应为 %v", c.id.Short(), c.method, got, c.want)
		}
	}
}

func TestInterceptorRejects(t *testing.T) {
	key, _ := meshid.Generate()
	cert, _ := meshid.SelfSignedCert(key, time.Hour)
	ctx := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{cert.Leaf},
	}}})
	called := false
	h := func(context.Context, any) (any, error) { called = true; return nil, nil }

	_, err := authUnary(noPairs{})(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/asamesh.v1.Peer/HTTP"}, h)
	if status.Code(err) != codes.PermissionDenied || called {
		t.Fatalf("未配对身份调非放行方法应被拒，得到 %v", err)
	}
	if _, err := authUnary(noPairs{})(ctx, nil, &grpc.UnaryServerInfo{FullMethod: meshpb.Peer_Hello_FullMethodName}, h); err != nil || !called {
		t.Fatalf("Hello 应放行：%v", err)
	}
	if _, err := authUnary(noPairs{})(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: meshpb.Peer_Hello_FullMethodName}, h); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("没有 TLS 身份应是 Unauthenticated，得到 %v", err)
	}
}
