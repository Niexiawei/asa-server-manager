package streamconn_test

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"asa-server/pkg/meshid"
	"asa-server/pkg/streamconn"
)

// 证明 streamconn 对上层是合格的 net.Conn：外层一条 gRPC 双向流（模拟协调节点中转的
// Relay 流），流上包成 net.Conn，再在其上跑端到端 TLS（钉公钥）+ 另一个 gRPC 服务——
// 正是管理器之间经中转的那条路径的形状（docs/REMOTE_MANAGER_MESH_PLAN.md §5.4、§5.5）。

const pipeMethod = "/streamconn.test.Pipe/Open"

var pipeDesc = grpc.ServiceDesc{
	ServiceName: "streamconn.test.Pipe",
	HandlerType: (*any)(nil),
	Streams: []grpc.StreamDesc{{
		StreamName:    "Open",
		ServerStreams: true,
		ClientStreams: true,
		Handler: func(srv any, stream grpc.ServerStream) error {
			return srv.(*pipeServer).open(stream)
		},
	}},
}

type pipeServer struct{ ln *chanListener }

func (p *pipeServer) open(stream grpc.ServerStream) error {
	c := streamconn.New(streamconn.Options{
		Recv: func() ([]byte, error) {
			var m wrapperspb.BytesValue
			if err := stream.RecvMsg(&m); err != nil {
				return nil, err
			}
			return m.Value, nil
		},
		Send: func(b []byte) error { return stream.SendMsg(wrapperspb.Bytes(b)) },
	})
	if !p.ln.deliver(c) {
		return nil
	}
	select {
	case <-c.Done():
	case <-stream.Context().Done():
		c.Close()
	}
	return nil
}

// chanListener 是由外部喂连接的 net.Listener。
type chanListener struct {
	ch        chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
}

func newChanListener() *chanListener {
	return &chanListener{ch: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *chanListener) deliver(c net.Conn) bool {
	select {
	case l.ch <- c:
		return true
	case <-l.closed:
		c.Close()
		return false
	}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return streamconn.Addr("relay") }

func dialPipe(outer *grpc.ClientConn) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		// 流的生命周期要独立于拨号 ctx：拨号完成后 ctx 会被取消，连接还要继续用。
		streamCtx, cancel := context.WithCancel(context.Background())
		stream, err := outer.NewStream(streamCtx, &pipeDesc.Streams[0], pipeMethod)
		if err != nil {
			cancel()
			return nil, err
		}
		return streamconn.New(streamconn.Options{
			Recv: func() ([]byte, error) {
				var m wrapperspb.BytesValue
				if err := stream.RecvMsg(&m); err != nil {
					return nil, err
				}
				return m.Value, nil
			},
			Send:      func(b []byte) error { return stream.SendMsg(wrapperspb.Bytes(b)) },
			CloseSend: stream.CloseSend,
			Abort:     cancel,
		}), nil
	}
}

func identity(t *testing.T) (tls.Certificate, meshid.ID) {
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

func TestTLSAndGRPCOverStreamConn(t *testing.T) {
	// 外层：明文 gRPC，TCP 回环。
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	relayLn := newChanListener()
	outerSrv := grpc.NewServer()
	outerSrv.RegisterService(&pipeDesc, &pipeServer{ln: relayLn})
	go outerSrv.Serve(tcp)
	t.Cleanup(outerSrv.Stop)

	// 内层：TLS（钉公钥）+ gRPC health，Serve 在由中转流喂连接的 listener 上。
	srvCert, srvID := identity(t)
	cliCert, cliID := identity(t)
	var seenPeer meshid.ID
	var seenMu sync.Mutex
	innerSrv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(meshid.AnyClientServerConfig(srvCert))),
		grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			if p, ok := peer.FromContext(ctx); ok {
				if ti, ok := p.AuthInfo.(credentials.TLSInfo); ok {
					if id, err := meshid.PeerID(ti.State); err == nil {
						seenMu.Lock()
						seenPeer = id
						seenMu.Unlock()
					}
				}
			}
			return h(ctx, req)
		}),
	)
	healthpb.RegisterHealthServer(innerSrv, health.NewServer())
	go innerSrv.Serve(relayLn)
	t.Cleanup(innerSrv.Stop)

	outer, err := grpc.NewClient(tcp.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { outer.Close() })

	inner, err := grpc.NewClient("passthrough:///peer",
		grpc.WithContextDialer(dialPipe(outer)),
		grpc.WithTransportCredentials(credentials.NewTLS(meshid.ClientConfig(cliCert, srvID))),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { inner.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := healthpb.NewHealthClient(inner).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("status = %v", resp.Status)
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	if seenPeer != cliID {
		t.Fatal("内层服务端应能从 TLS 握手里认出客户端的节点 ID")
	}

	// 钉错了服务端身份：握手失败，调用不会被送到别人那里。
	_, otherID := identity(t)
	wrong, err := grpc.NewClient("passthrough:///peer",
		grpc.WithContextDialer(dialPipe(outer)),
		grpc.WithTransportCredentials(credentials.NewTLS(meshid.ClientConfig(cliCert, otherID))),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.Close()
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shortCancel()
	if _, err := healthpb.NewHealthClient(wrong).Check(shortCtx, &healthpb.HealthCheckRequest{}); err == nil {
		t.Fatal("钉错公钥时调用必须失败")
	}
}
