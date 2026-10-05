package mesh

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
)

// 能力列表，随 Register 与 Hello 交换。打洞是 "punch.v1"（P6）。
const (
	CapRelay  = "relay.v1"
	CapDirect = "direct.v1"
	CapPair   = "pair.v1"
	CapHTTP   = "http.v1"
)

// Capabilities 返回本程序声明的能力。
func Capabilities() []string { return []string{CapRelay, CapDirect, CapPair, CapHTTP} }

const (
	// maxUnpairedConns：未授权身份同时持有的入站连接上限。它们能完成握手（否则没法配对），
	// 但只该用来配对——不能借此占满本机资源。
	maxUnpairedConns = 4
	// connIdle：入站连接空闲多久关闭。对全部入站连接生效（§12 P3-5）：已授权的对端被关掉
	// 空闲连接后，客户端下次请求透明重拨，代价只是一次握手；远程面板打开时 SSE 一直在流，不会空闲。
	connIdle = 2 * time.Minute

	// 配对的限流（§12 P3-3）：按调用者，邀请错误 pairFailMax 次 / pairFailWindow 后拒绝；全局每分钟 pairGlobalPerMin 次。
	pairFailMax      = 5
	pairFailWindow   = 10 * time.Minute
	pairGlobalPerMin = 30
)

// unpairedMethods 是未授权身份能调用的方法。
var unpairedMethods = map[string]bool{
	meshpb.Peer_Hello_FullMethodName: true,
	meshpb.Peer_Pair_FullMethodName:  true,
}

// Authorizer 回答「本机授予这个节点什么角色」，未授权为 ""。由 peers.json（PeerStore）实现。
type Authorizer interface {
	Grant(id meshid.ID) string
}

type noPairs struct{}

func (noPairs) Grant(meshid.ID) string { return "" }

// methodAllowed 是拦截器的放行规则：已授权的身份放行一切（角色由 B 的 HTTP 鉴权中间件细分），
// 未授权的只能调 unpairedMethods。
func methodAllowed(auth Authorizer, id meshid.ID, fullMethod string) bool {
	return auth.Grant(id) != "" || unpairedMethods[fullMethod]
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

// peerAddrFromContext 是调用者的来源地址（直连是 TCP 地址，中转是 relay:<短 ID>），只用于审计与显示。
func peerAddrFromContext(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return p.Addr.String()
	}
	return ""
}

func authUnary(auth Authorizer) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		id, ok := peerIDFromContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "无法识别调用者")
		}
		if !methodAllowed(auth, id, info.FullMethod) {
			return nil, status.Error(codes.PermissionDenied, "本机没有授权你的节点")
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
			return status.Error(codes.PermissionDenied, "本机没有授权你的节点")
		}
		return h(srv, ss)
	}
}

// roleToProto / roleFromProto 在本包的角色字符串与 proto 枚举之间转换。
func roleToProto(role string) meshpb.Role {
	switch role {
	case RoleAdmin:
		return meshpb.Role_ROLE_ADMIN
	case RoleOperator:
		return meshpb.Role_ROLE_OPERATOR
	}
	return meshpb.Role_ROLE_NONE
}

func roleFromProto(r meshpb.Role) string {
	switch r {
	case meshpb.Role_ROLE_ADMIN:
		return RoleAdmin
	case meshpb.Role_ROLE_OPERATOR:
		return RoleOperator
	}
	return ""
}

// peerService 实现 asamesh.v1.Peer。
type peerService struct {
	meshpb.UnimplementedPeerServer
	version string
	label   func() string
	store   *PeerStore
	tunnel  *tunnelServer

	limitMu    sync.Mutex
	failures   map[meshid.ID][]time.Time
	pairGlobal *rate.Limiter
}

func newPeerService(version string, label func() string, store *PeerStore, tunnel *tunnelServer) *peerService {
	return &peerService{
		version: version, label: label, store: store, tunnel: tunnel,
		failures:   map[meshid.ID][]time.Time{},
		pairGlobal: rate.NewLimiter(rate.Every(time.Minute/pairGlobalPerMin), pairGlobalPerMin),
	}
}

func (s *peerService) Hello(ctx context.Context, _ *meshpb.HelloRequest) (*meshpb.HelloResponse, error) {
	resp := &meshpb.HelloResponse{
		Version:      s.version,
		Capabilities: Capabilities(),
	}
	// 对未授权的身份只回版本与能力，不回备注名等任何本机信息。
	if id, ok := peerIDFromContext(ctx); ok {
		if role := s.store.Grant(id); role != "" {
			resp.GrantedRole = roleToProto(role)
			resp.Label = s.label()
		}
	}
	return resp, nil
}

// pairBlocked 判断 id 是否因邀请错误过多被暂时拒绝。
func (s *peerService) pairBlocked(id meshid.ID, now time.Time) bool {
	s.limitMu.Lock()
	defer s.limitMu.Unlock()
	keep := s.failures[id][:0]
	for _, t := range s.failures[id] {
		if now.Sub(t) < pairFailWindow {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(s.failures, id)
	} else {
		s.failures[id] = keep
	}
	return len(keep) >= pairFailMax
}

func (s *peerService) pairFailed(id meshid.ID, now time.Time) {
	s.limitMu.Lock()
	s.failures[id] = append(s.failures[id], now)
	s.limitMu.Unlock()
}

// Pair 实现配对（§12 P3-3）。
func (s *peerService) Pair(ctx context.Context, req *meshpb.PairRequest) (*meshpb.PairResponse, error) {
	id, ok := peerIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "无法识别调用者")
	}
	now := time.Now()
	if s.pairBlocked(id, now) || !s.pairGlobal.Allow() {
		return nil, status.Error(codes.ResourceExhausted, "配对尝试过于频繁，请稍后再试")
	}
	addr := peerAddrFromContext(ctx)

	if req.GetInviteId() != "" || len(req.GetInviteSecret()) > 0 {
		role, err := s.store.RedeemInvite(id, req.GetInviteId(), req.GetInviteSecret(), req.GetLabel())
		if errors.Is(err, ErrInviteInvalid) {
			s.pairFailed(id, now)
			logger.Warnf("[mesh] 节点 %s（来自 %s）出示的邀请码无效", id.Short(), addr)
			return nil, status.Error(codes.PermissionDenied, ErrInviteInvalid.Error())
		}
		if err != nil {
			return nil, status.Error(codes.Internal, "记录配对失败")
		}
		logger.Infof("[mesh] 节点 %s（%s，来自 %s）用邀请码完成配对，授予 %s", id.Short(), req.GetLabel(), addr, role)
		return &meshpb.PairResponse{Status: meshpb.PairStatus_PAIR_STATUS_PAIRED, GrantedRole: roleToProto(role), Label: s.label()}, nil
	}

	if role := s.store.Grant(id); role != "" {
		return &meshpb.PairResponse{Status: meshpb.PairStatus_PAIR_STATUS_PAIRED, GrantedRole: roleToProto(role), Label: s.label()}, nil
	}
	err := s.store.AddRequest(RequestRecord{NodeID: id, Label: req.GetLabel(), Version: req.GetVersion(), Addr: addr})
	if errors.Is(err, ErrTooManyPending) {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "记录申请失败")
	}
	logger.Infof("[mesh] 节点 %s（%s，来自 %s）申请配对，等待管理员批准", id.Short(), req.GetLabel(), addr)
	return &meshpb.PairResponse{Status: meshpb.PairStatus_PAIR_STATUS_PENDING}, nil
}

// HTTP 实现隧道（tunnel_server.go）。
func (s *peerService) HTTP(stream meshpb.Peer_HTTPServer) error {
	return s.tunnel.serve(stream)
}

// connRegistry 按节点 ID 登记入站连接：授权被撤销或角色变了时，把那个节点的连接全部断开，
// 其上的隧道流（SSE、WebSocket）随之结束（§12 P3-5）。
type connRegistry struct {
	mu    sync.Mutex
	conns map[meshid.ID]map[*trackedConn]struct{}
}

func newConnRegistry() *connRegistry {
	return &connRegistry{conns: map[meshid.ID]map[*trackedConn]struct{}{}}
}

func (r *connRegistry) add(c *trackedConn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	set := r.conns[c.id]
	if set == nil {
		set = map[*trackedConn]struct{}{}
		r.conns[c.id] = set
	}
	set[c] = struct{}{}
}

func (r *connRegistry) remove(c *trackedConn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if set := r.conns[c.id]; set != nil {
		delete(set, c)
		if len(set) == 0 {
			delete(r.conns, c.id)
		}
	}
}

// closeID 断开 id 的全部入站连接，返回断开的条数。
func (r *connRegistry) closeID(id meshid.ID) int {
	r.mu.Lock()
	list := make([]*trackedConn, 0, len(r.conns[id]))
	for c := range r.conns[id] {
		list = append(list, c)
	}
	r.mu.Unlock()
	for _, c := range list {
		c.Close()
	}
	return len(list)
}

type trackedConn struct {
	net.Conn
	id      meshid.ID
	reg     *connRegistry
	release func()
	once    sync.Once
}

func (c *trackedConn) Close() error {
	c.once.Do(func() {
		c.reg.remove(c)
		if c.release != nil {
			c.release()
		}
	})
	return c.Conn.Close()
}

// limitedCreds 在 TLS 握手之后、交给 gRPC 之前：按身份登记连接（撤销时用），并限制未授权的入站连接数。
// 握手之前不知道对方是谁，所以只能在这里做。
type limitedCreds struct {
	credentials.TransportCredentials
	auth Authorizer
	reg  *connRegistry
	sem  chan struct{}
}

var errTooManyUnpaired = errors.New("未授权身份的入站连接过多")

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
	tc := &trackedConn{Conn: conn, id: id, reg: c.reg}
	if c.auth.Grant(id) == "" {
		select {
		case c.sem <- struct{}{}:
			tc.release = func() { <-c.sem }
		default:
			conn.Close()
			return nil, nil, errTooManyUnpaired
		}
	}
	c.reg.add(tc)
	return tc, info, nil
}

func (c *limitedCreds) Clone() credentials.TransportCredentials {
	return &limitedCreds{TransportCredentials: c.TransportCredentials.Clone(), auth: c.auth, reg: c.reg, sem: c.sem}
}

// newPeerServer 建 Peer gRPC 服务器：要求客户端证书但接受任何公钥，由拦截器按节点 ID 放行。
func newPeerServer(serverCreds credentials.TransportCredentials, auth Authorizer, reg *connRegistry, svc meshpb.PeerServer) *grpc.Server {
	creds := &limitedCreds{TransportCredentials: serverCreds, auth: auth, reg: reg, sem: make(chan struct{}, maxUnpairedConns)}
	gs := grpc.NewServer(
		grpc.Creds(creds),
		grpc.ChainUnaryInterceptor(authUnary(auth)),
		grpc.ChainStreamInterceptor(authStream(auth)),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}),
		grpc.KeepaliveParams(keepalive.ServerParameters{MaxConnectionIdle: connIdle}),
	)
	meshpb.RegisterPeerServer(gs, svc)
	return gs
}
