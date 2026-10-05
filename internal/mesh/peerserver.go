package mesh

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/meshid"
)

// CapRelay 是 P1 声明的能力。打洞是 "punch.v1"（P6）。
const CapRelay = "relay.v1"

const (
	// maxUnpairedConns：未配对身份同时持有的入站连接上限。它们能完成握手（否则没法配对），
	// 但只该用来配对——不能借此占满本机资源。
	maxUnpairedConns = 4
	// unpairedIdle：入站连接空闲多久关闭。P1 还没有配对，所以对全部入站连接生效；
	// P3 有了配对后可以只对未配对身份生效。
	unpairedIdle = 2 * time.Minute
)

// unpairedMethods 是未配对身份能调用的方法。Pair（P3）加进来时也只加在这里。
var unpairedMethods = map[string]bool{
	meshpb.Peer_Hello_FullMethodName: true,
}

// Authorizer 回答「这个节点 ID 是否已配对」。P1 没有配对表，恒为 false；P3 由 peers.json 实现。
type Authorizer interface {
	Paired(id meshid.ID) bool
}

type noPairs struct{}

func (noPairs) Paired(meshid.ID) bool { return false }

// methodAllowed 是拦截器的放行规则：已配对的身份放行一切（P3 再按角色细分），
// 未配对的只能调 unpairedMethods。
func methodAllowed(auth Authorizer, id meshid.ID, fullMethod string) bool {
	return auth.Paired(id) || unpairedMethods[fullMethod]
}

// peerIDFromContext 取出调用者的节点 ID。
func peerIDFromContext(ctx context.Context) (meshid.ID, bool) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return meshid.ID{}, false
	}
	ti, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return meshid.ID{}, false
	}
	id, err := meshid.PeerID(ti.State)
	return id, err == nil
}

func authUnary(auth Authorizer) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		id, ok := peerIDFromContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "无法识别调用者")
		}
		if !methodAllowed(auth, id, info.FullMethod) {
			return nil, status.Error(codes.PermissionDenied, "未配对的身份只能调用 Hello")
		}
		return h(ctx, req)
	}
}

func authStream(auth Authorizer) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) error {
		id, ok := peerIDFromContext(ss.Context())
		if !ok {
			return status.Error(codes.Unauthenticated, "无法识别调用者")
		}
		if !methodAllowed(auth, id, info.FullMethod) {
			return status.Error(codes.PermissionDenied, "未配对的身份只能调用 Hello")
		}
		return h(srv, ss)
	}
}

// peerService 实现 asamesh.v1.Peer。
type peerService struct {
	meshpb.UnimplementedPeerServer
	version string
	label   func() string
	auth    Authorizer
}

func (s *peerService) Hello(ctx context.Context, _ *meshpb.HelloRequest) (*meshpb.HelloResponse, error) {
	resp := &meshpb.HelloResponse{
		Version:      s.version,
		Capabilities: []string{CapRelay},
		GrantedRole:  meshpb.Role_ROLE_NONE,
	}
	// 对未配对的身份只回版本与能力，不回备注名等任何本机信息。
	if id, ok := peerIDFromContext(ctx); ok && s.auth.Paired(id) {
		resp.Label = s.label()
	}
	return resp, nil
}

// limitedCreds 在 TLS 握手之后、交给 gRPC 之前，按身份限制未配对的入站连接数。
// 握手之前不知道对方是谁，所以只能在这里做。
type limitedCreds struct {
	credentials.TransportCredentials
	auth Authorizer
	sem  chan struct{}
}

var errTooManyUnpaired = errors.New("未配对身份的入站连接过多")

func (c *limitedCreds) ServerHandshake(raw net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn, info, err := c.TransportCredentials.ServerHandshake(raw)
	if err != nil {
		return nil, nil, err
	}
	ti, ok := info.(credentials.TLSInfo)
	if !ok {
		conn.Close()
		return nil, nil, errors.New("需要 TLS")
	}
	id, err := meshid.PeerID(ti.State)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if c.auth.Paired(id) {
		return conn, info, nil
	}
	select {
	case c.sem <- struct{}{}:
	default:
		conn.Close()
		return nil, nil, errTooManyUnpaired
	}
	return &releaseConn{Conn: conn, release: func() { <-c.sem }}, info, nil
}

func (c *limitedCreds) Clone() credentials.TransportCredentials {
	return &limitedCreds{TransportCredentials: c.TransportCredentials.Clone(), auth: c.auth, sem: c.sem}
}

type releaseConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *releaseConn) Close() error {
	c.once.Do(c.release)
	return c.Conn.Close()
}

// newPeerServer 建 Peer gRPC 服务器：要求客户端证书但接受任何公钥，由拦截器按节点 ID 放行。
func newPeerServer(serverCreds credentials.TransportCredentials, svc *peerService) *grpc.Server {
	creds := &limitedCreds{TransportCredentials: serverCreds, auth: svc.auth, sem: make(chan struct{}, maxUnpairedConns)}
	gs := grpc.NewServer(
		grpc.Creds(creds),
		grpc.ChainUnaryInterceptor(authUnary(svc.auth)),
		grpc.ChainStreamInterceptor(authStream(svc.auth)),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}),
		grpc.KeepaliveParams(keepalive.ServerParameters{MaxConnectionIdle: unpairedIdle}),
	)
	meshpb.RegisterPeerServer(gs, svc)
	return gs
}
