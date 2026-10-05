package meshcoord

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
)

func TestMain(m *testing.M) {
	cleanup := logger.InitTempForTest()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// coord 是进程内的一个协调节点。
type coord struct {
	addr   string
	id     meshid.ID
	store  *Store
	srv    *Server
	secret string
}

func startCoord(t *testing.T, opts ServerOptions) *coord {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	n, _, err := store.EnsureDefaultNetwork()
	if err != nil {
		t.Fatal(err)
	}
	cert, id := newIdentity(t)
	opts.Store = store
	srv := NewServer(opts)
	gs := NewGRPCServer(cert, srv)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go gs.Serve(lis)
	t.Cleanup(func() { gs.Stop(); srv.Close() })
	return &coord{addr: lis.Addr().String(), id: id, store: store, srv: srv, secret: n.Secret}
}

func newIdentity(t *testing.T) (tls.Certificate, meshid.ID) {
	t.Helper()
	key, err := meshid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := meshid.SelfSignedCert(key, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return cert, meshid.FromCert(cert.Leaf)
}

// node 是一个连到协调节点的管理器（只用原始 gRPC 客户端，不经 internal/mesh）。
type node struct {
	id     meshid.ID
	cert   tls.Certificate
	client meshpb.CoordinatorClient
}

func (c *coord) dial(t *testing.T, cert tls.Certificate) *node {
	t.Helper()
	cc, err := grpc.NewClient(c.addr, grpc.WithTransportCredentials(credentials.NewTLS(meshid.ClientConfig(cert, c.id))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cc.Close() })
	return &node{id: meshid.FromCert(cert.Leaf), cert: cert, client: meshpb.NewCoordinatorClient(cc)}
}

func (c *coord) newNode(t *testing.T) *node {
	t.Helper()
	cert, _ := newIdentity(t)
	return c.dial(t, cert)
}

// clientSession 是一条已登记的 Session。
type clientSession struct {
	stream     meshpb.Coordinator_SessionClient
	cancel     context.CancelFunc
	registered *meshpb.Registered
}

// register 开 Session 并发送 Register；成功时返回会话，失败时返回 gRPC 错误。
func (n *node) register(t *testing.T, network, secret string) (*clientSession, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream, err := n.client.Session(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	err = stream.Send(&meshpb.NodeMessage{Msg: &meshpb.NodeMessage_Register{Register: &meshpb.Register{
		NetworkId: network, NetworkSecret: secret, Version: "test", Capabilities: []string{"relay.v1"}, Label: "t",
	}}})
	if err != nil {
		cancel()
		return nil, err
	}
	m, err := stream.Recv()
	if err != nil {
		cancel()
		return nil, err
	}
	reg := m.GetRegistered()
	if reg == nil {
		cancel()
		t.Fatalf("首条回复应是 Registered，得到 %v", m)
	}
	return &clientSession{stream: stream, cancel: cancel, registered: reg}, nil
}

func (n *node) mustRegister(t *testing.T, c *coord) *clientSession {
	t.Helper()
	s, err := n.register(t, DefaultNetworkID, c.secret)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// nextIncomingRelay 读下一条 IncomingRelay。
func (s *clientSession) nextIncomingRelay(t *testing.T) *meshpb.IncomingRelay {
	t.Helper()
	m, err := s.stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	ir := m.GetIncomingRelay()
	if ir == nil {
		t.Fatalf("应收到 IncomingRelay，得到 %v", m)
	}
	return ir
}

func (n *node) joinRelay(t *testing.T, sessionID string, token []byte) meshpb.Coordinator_RelayClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rs, err := n.client.Relay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.Send(&meshpb.RelayFrame{Msg: &meshpb.RelayFrame_Join{Join: &meshpb.RelayJoin{SessionId: sessionID, Token: token}}}); err != nil {
		t.Fatal(err)
	}
	return rs
}

func codeOf(err error) codes.Code {
	return status.Code(err)
}

// recvErr 读到流结束，返回结束原因（正常结束为 io.EOF）。
func recvErr[T any](recv func() (T, error)) error {
	for {
		if _, err := recv(); err != nil {
			if errors.Is(err, io.EOF) {
				return io.EOF
			}
			return err
		}
	}
}

func ctxTimeout(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}
