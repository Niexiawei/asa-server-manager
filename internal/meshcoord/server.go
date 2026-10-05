package meshcoord

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
)

const (
	defaultRelayJoinTimeout = 30 * time.Second
	defaultBanSweep         = 30 * time.Second
	sessionQueue            = 16
)

// errNotFound 是 Resolve / OpenRelay 对「不存在、不在同一网络、不在线」的统一回答——
// 不让调用者区分「别的网络有这个节点」（§12 P1-1）。
var errNotFound = status.Error(codes.NotFound, "节点不在线")

// ServerOptions 是 Server 的参数。
type ServerOptions struct {
	Store     *Store
	STUNAddrs []string
	Limits    LimitsConfig

	// RelayJoinTimeout：OpenRelay 之后双方都 Join 的期限。默认 30 秒。
	RelayJoinTimeout time.Duration
	// BanSweepInterval：在线会话按拉黑表复查的间隔（node ban 是另一个进程写的库）。默认 30 秒。
	BanSweepInterval time.Duration
}

// Server 实现 asamesh.v1.Coordinator。
type Server struct {
	meshpb.UnimplementedCoordinatorServer

	opts ServerOptions

	mu         sync.Mutex
	online     map[meshid.ID]*session
	relays     map[string]*relay
	relayCount map[meshid.ID]int
	limiters   map[meshid.ID]*rate.Limiter

	stop     chan struct{}
	stopOnce sync.Once
}

// NewServer 建一个协调节点服务。调用方负责 Close。
func NewServer(opts ServerOptions) *Server {
	if opts.RelayJoinTimeout <= 0 {
		opts.RelayJoinTimeout = defaultRelayJoinTimeout
	}
	if opts.BanSweepInterval <= 0 {
		opts.BanSweepInterval = defaultBanSweep
	}
	if opts.Limits.MaxRelaysPerNode <= 0 {
		opts.Limits.MaxRelaysPerNode = defaultMaxRelaysPerNode
	}
	if opts.Limits.RelayIdleTimeout <= 0 {
		opts.Limits.RelayIdleTimeout = defaultRelayIdleTimeout
	}
	s := &Server{
		opts:       opts,
		online:     map[meshid.ID]*session{},
		relays:     map[string]*relay{},
		relayCount: map[meshid.ID]int{},
		limiters:   map[meshid.ID]*rate.Limiter{},
		stop:       make(chan struct{}),
	}
	go s.banSweepLoop()
	return s
}

// Close 停止后台任务。在线会话由 gRPC 服务器的 Stop 结束。
func (s *Server) Close() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// session 是一条在线的 Session 长连接。
type session struct {
	id       meshid.ID
	network  string
	label    string
	version  string
	caps     []string
	observed netip.AddrPort

	mu         sync.Mutex
	candidates []*meshpb.Candidate

	out      chan *meshpb.CoordMessage
	kicked   chan struct{}
	kickOnce sync.Once
	reason   string
}

func (ss *session) kick(reason string) {
	ss.kickOnce.Do(func() {
		ss.reason = reason
		close(ss.kicked)
	})
}

// push 把一条消息放进会话的发送队列；队列满（对端不读）时返回 false。
func (ss *session) push(m *meshpb.CoordMessage) bool {
	select {
	case ss.out <- m:
		return true
	case <-ss.kicked:
		return false
	default:
		return false
	}
}

// callerID 从 TLS 握手里取出调用者的节点 ID。
func callerID(ctx context.Context) (meshid.ID, netip.AddrPort, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return meshid.ID{}, netip.AddrPort{}, status.Error(codes.Unauthenticated, "无法识别调用者")
	}
	ti, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return meshid.ID{}, netip.AddrPort{}, status.Error(codes.Unauthenticated, "需要 TLS 客户端证书")
	}
	id, err := meshid.PeerID(ti.State)
	if err != nil {
		return meshid.ID{}, netip.AddrPort{}, status.Error(codes.Unauthenticated, err.Error())
	}
	var observed netip.AddrPort
	if ta, ok := p.Addr.(*net.TCPAddr); ok {
		ap := ta.AddrPort()
		observed = netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
	}
	return id, observed, nil
}

// Session 实现登记长连接。
func (s *Server) Session(stream meshpb.Coordinator_SessionServer) error {
	ctx := stream.Context()
	id, observed, err := callerID(ctx)
	if err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	reg := first.GetRegister()
	if reg == nil {
		return status.Error(codes.InvalidArgument, "Session 的首帧必须是 Register")
	}
	if err := s.authorize(id, observed, reg); err != nil {
		return err
	}

	ss := &session{
		id:         id,
		network:    reg.GetNetworkId(),
		label:      reg.GetLabel(),
		version:    reg.GetVersion(),
		caps:       append([]string(nil), reg.GetCapabilities()...),
		observed:   observed,
		candidates: cloneCandidates(reg.GetCandidates()),
		out:        make(chan *meshpb.CoordMessage, sessionQueue),
		kicked:     make(chan struct{}),
	}
	s.mu.Lock()
	if old := s.online[id]; old != nil {
		// 同一把私钥同时出现在两个地方：要么是配置被拷到了另一台机器，要么私钥泄露了。
		logger.Warnf("[coord] 节点 %s 有新会话登记（来自 %s），踢掉旧会话（来自 %s）——同一身份出现在两处",
			id.Short(), observed, old.observed)
		old.kick("同一节点 ID 有新会话登记")
	}
	s.online[id] = ss
	s.mu.Unlock()
	logger.Infof("[coord] 节点 %s（%s，%s）上线，来自 %s", id.Short(), ss.label, ss.version, observed)

	defer func() {
		s.mu.Lock()
		if s.online[id] == ss {
			delete(s.online, id)
		}
		s.mu.Unlock()
		_ = s.opts.Store.Touch(ss.network, id, time.Now())
		logger.Infof("[coord] 节点 %s 下线", id.Short())
	}()

	ss.out <- &meshpb.CoordMessage{Msg: &meshpb.CoordMessage_Registered{Registered: &meshpb.Registered{
		NodeId:           id.Compact(),
		ObservedAddr:     observed.String(),
		ServerTimeUnixMs: time.Now().UnixMilli(),
		StunAddrs:        s.opts.STUNAddrs,
	}}}

	recvErr := make(chan error, 1)
	go func() {
		for {
			m, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			s.handleNodeMessage(ss, m)
		}
	}()

	for {
		select {
		case m := <-ss.out:
			if err := stream.Send(m); err != nil {
				return err
			}
		case <-ss.kicked:
			_ = stream.Send(&meshpb.CoordMessage{Msg: &meshpb.CoordMessage_Kicked{Kicked: &meshpb.Kicked{Reason: ss.reason}}})
			return status.Error(codes.Aborted, ss.reason)
		case err := <-recvErr:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// authorize 决定这次登记能否通过：被拉黑的拒绝；已是该网络成员的直接通过；
// 否则核对网络密钥（常数时间比较）后入表。拒绝时对外只说「被拒绝」，细节进本地日志。
func (s *Server) authorize(id meshid.ID, observed netip.AddrPort, reg *meshpb.Register) error {
	denied := status.Error(codes.PermissionDenied, "接入被拒绝：网络或密钥不正确，或本节点已被拉黑")
	network := reg.GetNetworkId()
	n, err := s.opts.Store.Network(network)
	if errors.Is(err, ErrNotFound) {
		logger.Warnf("[coord] 拒绝 %s（来自 %s）：网络 %q 不存在", id.Short(), observed, network)
		return denied
	}
	if err != nil {
		return status.Error(codes.Internal, "读取网络失败")
	}
	rec, err := s.opts.Store.Node(network, id)
	switch {
	case err == nil && rec.Banned:
		logger.Warnf("[coord] 拒绝 %s（来自 %s）：已被拉黑", id.Short(), observed)
		return denied
	case err == nil && !rec.FirstSeen.IsZero():
		// 已是成员：此后只凭证书，忽略请求里的密钥。
	case err == nil || errors.Is(err, ErrNotFound):
		if subtle.ConstantTimeCompare([]byte(reg.GetNetworkSecret()), []byte(n.Secret)) != 1 {
			logger.Warnf("[coord] 拒绝 %s（来自 %s）：网络 %q 的密钥不正确", id.Short(), observed, network)
			return denied
		}
		logger.Infof("[coord] 节点 %s 首次接入网络 %q", id.Short(), network)
	default:
		return status.Error(codes.Internal, "读取节点记录失败")
	}
	if err := s.opts.Store.RecordJoin(network, id, reg.GetLabel(), reg.GetVersion(), time.Now()); err != nil {
		return status.Error(codes.Internal, "记录登记失败")
	}
	return nil
}

func (s *Server) handleNodeMessage(ss *session, m *meshpb.NodeMessage) {
	switch msg := m.GetMsg().(type) {
	case *meshpb.NodeMessage_Candidates:
		ss.mu.Lock()
		ss.candidates = cloneCandidates(msg.Candidates.GetCandidates())
		ss.mu.Unlock()
	case *meshpb.NodeMessage_Register:
		logger.Warnf("[coord] 节点 %s 在 Session 中途又发了 Register，忽略", ss.id.Short())
	default:
		// PunchOffer / PunchAnswer 是 P6 的；没声明 punch.v1 的协调节点直接忽略。
	}
}

// callerSession 要求调用者此刻有在线会话，网络取自那个会话（§8.4.5 的硬规则：
// 永远不读请求参数里的网络）。
func (s *Server) callerSession(ctx context.Context) (*session, error) {
	id, _, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.online[id]
	if ss == nil {
		return nil, status.Error(codes.FailedPrecondition, "调用者没有在线会话，请先建立 Session")
	}
	return ss, nil
}

// lookupLocked 在调用者的网络里找 target。调用方持有 s.mu。
func (s *Server) lookupLocked(caller *session, target string) (*session, error) {
	id, err := meshid.ParseID(target)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "节点 ID 格式不正确")
	}
	t := s.online[id]
	if t == nil || t.network != caller.network {
		return nil, errNotFound
	}
	return t, nil
}

// Resolve 实现查询。
func (s *Server) Resolve(ctx context.Context, req *meshpb.ResolveRequest) (*meshpb.ResolveResponse, error) {
	caller, err := s.callerSession(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	t, err := s.lookupLocked(caller, req.GetNodeId())
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	cands := cloneCandidates(t.candidates)
	t.mu.Unlock()
	return &meshpb.ResolveResponse{
		Version:        t.version,
		Capabilities:   t.caps,
		Candidates:     cands,
		PeerObservedIp: addrString(t.observed),
		SelfObservedIp: addrString(caller.observed),
	}, nil
}

func addrString(ap netip.AddrPort) string {
	if !ap.IsValid() {
		return ""
	}
	return ap.Addr().String()
}

func cloneCandidates(in []*meshpb.Candidate) []*meshpb.Candidate {
	out := make([]*meshpb.Candidate, 0, len(in))
	for _, c := range in {
		out = append(out, proto.Clone(c).(*meshpb.Candidate))
	}
	return out
}

// banSweepLoop 定期按拉黑表复查在线会话：asa-coordinator node ban 是另一个进程，
// 直接写库，本进程只能自己去看。
func (s *Server) banSweepLoop() {
	t := time.NewTicker(s.opts.BanSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.sweepBans()
		}
	}
}

func (s *Server) sweepBans() {
	byNet := map[string][]*session{}
	s.mu.Lock()
	for _, ss := range s.online {
		byNet[ss.network] = append(byNet[ss.network], ss)
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for network, list := range byNet {
		ids := make([]meshid.ID, len(list))
		for i, ss := range list {
			ids[i] = ss.id
		}
		banned, err := s.opts.Store.BannedAmong(ctx, network, ids)
		if err != nil {
			logger.Warnf("[coord] 复查拉黑表失败: %v", err)
			continue
		}
		for _, ss := range list {
			if banned[ss.id] {
				logger.Warnf("[coord] 节点 %s 已被拉黑，断开其会话", ss.id.Short())
				ss.kick("本节点已被协调节点管理员拉黑")
			}
		}
	}
}

// OnlineCount 返回在线节点数（状态日志用）。
func (s *Server) OnlineCount() (nodes, relays int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.online), len(s.relays)
}
