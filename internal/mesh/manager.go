package mesh

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
	"asa-server/pkg/meshjoin"
	"asa-server/pkg/stun"
)

// AppVersion 是本程序的版本，随 Register 与 Hello 交换。由 main 在启动时设置。
var AppVersion = "dev"

// Options 是 New 的参数。
type Options struct {
	// Dir 是 {BaseDir}/mesh。
	Dir     string
	Version string
	// BackoffMin 是协调节点重连退避的起点，默认 1 秒（测试会调小）。
	BackoffMin time.Duration
	// UpgradeMin / UpgradeMax 是「中转 → 直连」升级重试的间隔，默认 1 分钟起、翻倍到 10 分钟。
	UpgradeMin, UpgradeMax time.Duration
	// CandidateInterval 是重新枚举本机候选地址的间隔，默认 60 秒。
	CandidateInterval time.Duration
	// ListenAddr 覆盖 Peer 端口的监听地址（测试用 127.0.0.1:0）；空 = ":<peer_port>"。
	ListenAddr string
	// UDPListenAddr 覆盖打洞用 UDP 端口的绑定地址（测试用 127.0.0.1:0）；空 = ":<udp_port>"。
	UDPListenAddr string

	// 以下只给包内测试用。
	wrapListener    func(net.Listener) net.Listener
	localCandidates func(listenPort int) []*meshpb.Candidate
	// wrapPacketConn 包住打洞用的 UDP socket（模拟 NAT 的过滤）。
	wrapPacketConn func(net.PacketConn) net.PacketConn
	// localUDP 替换本机 UDP 候选的枚举（真实枚举会过滤掉回环地址）。
	localUDP func(port int) []netip.AddrPort
	// punchMinInterval 是同一对端两次打洞的最小间隔，默认 30 秒。
	punchMinInterval time.Duration
}

// Manager 是管理器互控的运行时。照 frpmanage 的形态：包级单例 + 可直接 New（测试用）。
type Manager struct {
	opts  Options
	store *PeerStore

	mu          sync.Mutex
	running     bool
	ctx         context.Context
	cancel      context.CancelFunc
	cfg         *Config
	id          meshid.ID
	cert        tls.Certificate
	coord       *coordClient
	peerSrv     *grpc.Server
	relayLn     *injectListener
	punch       *puncher
	punchPeers  map[meshid.ID]*punchPeer
	listenAddr  string
	listenErr   error
	candidates  func() []*meshpb.Candidate
	reg         *connRegistry
	tunnel      *tunnelServer
	direct      *directProvider
	providers   []pathProvider
	peers       map[meshid.ID]*peerHandle
	lastPath    map[meshid.ID]PathKind
	hellos      map[meshid.ID]helloRecord
	httpHandler http.Handler
	startErr    error
	punchErr    error
}

var errCannotDialSelf = errors.New("不能连接自己")

// New 建一个 Manager。不碰磁盘、不联网——这些都在 Start 里。
func New(opts Options) *Manager {
	if opts.Version == "" {
		opts.Version = AppVersion
	}
	if opts.BackoffMin <= 0 {
		opts.BackoffMin = time.Second
	}
	if opts.UpgradeMin <= 0 {
		opts.UpgradeMin = time.Minute
	}
	if opts.UpgradeMax <= 0 {
		opts.UpgradeMax = 10 * time.Minute
	}
	if opts.CandidateInterval <= 0 {
		opts.CandidateInterval = time.Minute
	}
	if opts.punchMinInterval <= 0 {
		opts.punchMinInterval = defaultPunchMinInterval
	}
	m := &Manager{opts: opts, store: OpenPeerStore(opts.Dir)}
	m.store.OnGrantsChanged(m.onGrantsChanged)
	return m
}

var globalManager *Manager

// Initialize 建立包级单例。只记下目录，零副作用。
func Initialize(baseDir string) *Manager {
	globalManager = New(Options{Dir: Dir(baseDir), Version: AppVersion})
	return globalManager
}

// GetGlobalManager 返回包级单例（未 Initialize 时为 nil）。
func GetGlobalManager() *Manager { return globalManager }

// SetGlobalManagerForTest 替换包级单例并返回还原函数。只给其他包的测试用（webapi/meshapi 的端到端用例）。
func SetGlobalManagerForTest(m *Manager) (restore func()) {
	old := globalManager
	globalManager = m
	return func() { globalManager = old }
}

// Dir 返回 {BaseDir}/mesh。
func (m *Manager) Dir() string { return m.opts.Dir }

// Store 返回配对与授权表。不要求在运行（页面在 mesh 停用时也能管理配对）。
func (m *Manager) Store() *PeerStore { return m.store }

// SetHTTPHandler 注入隧道请求的处理者（组合根传入 Gin engine，避免 mesh → webapi 成环）。
func (m *Manager) SetHTTPHandler(h http.Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.httpHandler = h
	if m.tunnel != nil {
		m.tunnel.setHandler(h)
	}
}

// Start 读取配置、监听 Peer 端口、接入协调节点（若配置了）。未配置时返回 ErrNotConfigured，
// 不发起任何连接、不监听任何端口。
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return errors.New("管理器互控已在运行")
	}
	cfg, err := LoadConfig(m.opts.Dir)
	if err != nil {
		m.startErr = err
		return err
	}
	cert, id, err := meshid.LoadOrCreate(m.opts.Dir)
	if err != nil {
		m.startErr = err
		return fmt.Errorf("加载本机身份: %w", err)
	}
	if err := m.store.Refresh(true); err != nil {
		m.startErr = err
		return fmt.Errorf("读取 %s: %w", PeersFileName, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	reg := newConnRegistry()
	tunnel := &tunnelServer{store: m.store}
	tunnel.setHandler(m.httpHandler)
	svc := newPeerService(m.opts.Version, m.label, m.store, tunnel)
	peerSrv := newPeerServer(credentials.NewTLS(meshid.AnyClientServerConfig(cert)), m.store, reg, svc)
	relayLn := newInjectListener()
	go serveListener(peerSrv, relayLn, "中转 / 打洞")

	// Peer 端口（D7：默认监听）。绑不上不让整个 mesh 起不来：记下来、退化为只走中转（§12 P2-3）。
	listenPort, listenAddr := 0, ""
	var listenErr error
	if !cfg.NoListen {
		addr := m.opts.ListenAddr
		if addr == "" {
			addr = ":" + strconv.Itoa(cfg.ListenPort())
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			listenErr = fmt.Errorf("监听 Peer 端口 %s 失败：%w（只能经中转被连接）", addr, err)
			logger.Warnf("[mesh] %v", listenErr)
		} else {
			if m.opts.wrapListener != nil {
				ln = m.opts.wrapListener(ln)
			}
			listenAddr = ln.Addr().String()
			listenPort = ln.Addr().(*net.TCPAddr).Port
			go serveListener(peerSrv, ln, "Peer 端口")
		}
	}
	candidates := func() []*meshpb.Candidate {
		return buildCandidates(systemInterfaces(), listenPort, cfg.PublicAddrs)
	}
	if m.opts.localCandidates != nil {
		hook := m.opts.localCandidates
		candidates = func() []*meshpb.Candidate { return hook(listenPort) }
	}

	var coord *coordClient
	if cfg.Coordinator != nil {
		coord, err = newCoordClient(*cfg.Coordinator, cert, id, m.opts.Version, cfg.Label, m.opts.BackoffMin,
			func(c net.Conn) { relayLn.deliver(c) }, candidates, m.opts.CandidateInterval)
		if err != nil {
			cancel()
			peerSrv.Stop()
			m.startErr = err
			return err
		}
	}
	// 打洞（§12 P6）：要协调节点（信令走中转、地址发现靠它的 STUN）。UDP 绑不上不致命，失败才放弃打洞。
	var punch *puncher
	var punchErr error
	if coord != nil && !cfg.NoPunch {
		punch, punchErr = m.startPuncher(ctx, cfg, cert, id, coord, relayLn)
		if punchErr != nil {
			logger.Warnf("[mesh] 打洞不可用：%v", punchErr)
		}
		svc.punch.Store(punch)
	}
	if coord != nil {
		go coord.run(ctx)
	}
	direct := &directProvider{cert: cert, coord: coord, store: m.store, stagger: happyStagger, budget: happyBudget}
	providers := []pathProvider{direct}
	if coord != nil {
		providers = append(providers, relayProvider{c: coord, cert: cert})
	}
	go m.refreshLoop(ctx)

	m.cfg, m.id, m.cert = cfg, id, cert
	m.ctx, m.cancel, m.coord, m.peerSrv, m.relayLn = ctx, cancel, coord, peerSrv, relayLn
	m.listenAddr, m.listenErr, m.candidates = listenAddr, listenErr, candidates
	m.reg, m.tunnel, m.direct, m.providers = reg, tunnel, direct, providers
	m.punch, m.punchErr, m.punchPeers = punch, punchErr, map[meshid.ID]*punchPeer{}
	m.peers = map[meshid.ID]*peerHandle{}
	m.lastPath = map[meshid.ID]PathKind{}
	m.hellos = map[meshid.ID]helloRecord{}
	m.running = true
	m.startErr = nil
	where := "无协调节点模式"
	if coord != nil {
		where = "协调节点 " + cfg.Coordinator.Addr
	}
	listen := "不监听 Peer 端口"
	if listenAddr != "" {
		listen = "Peer 端口 " + listenAddr
	}
	logger.Infof("[mesh] 已启动：本机 %s，%s，%s", id.Short(), where, listen)
	return nil
}

// startPuncher 绑 UDP、建 QUIC 监听、起分发 / 接受 / 地址发现三个循环，并让协调节点的登记与网卡变化触发重测 NAT。
func (m *Manager) startPuncher(ctx context.Context, cfg *Config, cert tls.Certificate, id meshid.ID,
	coord *coordClient, inject *injectListener) (*puncher, error) {
	addr := m.opts.UDPListenAddr
	if addr == "" {
		addr = ":" + strconv.Itoa(cfg.UDPListenPort())
	}
	mux, bindErr, err := openUDP(addr, m.opts.wrapPacketConn)
	if err != nil {
		return nil, err
	}
	if bindErr != nil {
		logger.Warnf("[mesh] %v", bindErr)
	}
	p, err := newPuncher(mux, bindErr, cert, id, func(c net.Conn) { inject.deliver(c) },
		func() []string { return coord.snapshot().STUNAddrs }, m.opts.localUDP)
	if err != nil {
		mux.close()
		return nil, err
	}
	coord.onNetChange = p.kick
	p.start(ctx)
	return p, nil
}

func serveListener(gs *grpc.Server, ln net.Listener, what string) {
	if err := gs.Serve(ln); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		logger.Warnf("[mesh] Peer 服务（%s）退出: %v", what, err)
	}
}

// refreshLoop 定期检查 peers.json 是否被别的进程（CLI）改过：撤销要在几秒内生效，
// 不能等到下一次有 RPC 进来才发现。
func (m *Manager) refreshLoop(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := m.store.Refresh(true); err != nil {
				logger.Debugf("[mesh] 重新读取 %s 失败: %v", PeersFileName, err)
			}
		}
	}
}

// onGrantsChanged 在授权被撤销或降级时：立即取消该对端的全部隧道流（SSE、WebSocket 不会在撤销后
// 继续活着，§12 P3-5），1 秒后再断开它的入站连接——留这一点时间让正在返回的一问一答（例如
// 恰好是这次 Pair 导致了降级）送达。新的 RPC 由拦截器按新的授权判断，不依赖断开。
func (m *Manager) onGrantsChanged(ids []meshid.ID) {
	m.mu.Lock()
	reg, tunnel := m.reg, m.tunnel
	m.mu.Unlock()
	if reg == nil {
		return
	}
	for _, id := range ids {
		n := tunnel.cancelID(id)
		logger.Infof("[mesh] 对 %s 的授权已撤销或降级，结束它的 %d 条隧道流", id.Short(), n)
		time.AfterFunc(revokeGrace, func() { reg.closeID(id) })
	}
}

// revokeGrace 是授权撤销后断开入站连接前的宽限。
const revokeGrace = time.Second

// Stop 断开协调节点、关闭 Peer 服务与所有对端连接。未在运行时是空操作。
func (m *Manager) Stop() error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	cancel, peers, peerSrv, relayLn, coord, punch := m.cancel, m.peers, m.peerSrv, m.relayLn, m.coord, m.punch
	m.running = false
	m.punch, m.punchErr, m.punchPeers = nil, nil, nil
	m.peers, m.coord, m.peerSrv, m.relayLn, m.lastPath, m.reg, m.tunnel = nil, nil, nil, nil, nil, nil, nil
	m.direct, m.providers = nil, nil
	m.mu.Unlock()

	// 锁外关闭：对端连接的 dialer 回调要拿 m.mu，持锁关闭可能与它互等。
	cancel()
	for _, h := range peers {
		h.forceClose()
	}
	if punch != nil {
		punch.close()
	}
	peerSrv.Stop()
	relayLn.Close()
	var err error
	if coord != nil {
		err = coord.close()
	}
	logger.Infof("[mesh] 已停止")
	return err
}

// Running 报告 mesh 是否在运行。
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// Reload 重新加载配置：Stop 再 Start（页面改配置后热应用）。未配置时返回 ErrNotConfigured，此时已是停止状态。
func (m *Manager) Reload() error {
	if err := m.Stop(); err != nil {
		logger.Warnf("[mesh] 停止时出错: %v", err)
	}
	return m.Start()
}

func (m *Manager) label() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg == nil {
		return ""
	}
	return m.cfg.Label
}

// ControlRole 返回本机上能使用远程控制的最低角色（D5）。读不到配置时按最严格的 admin。
func (m *Manager) ControlRole() string {
	m.mu.Lock()
	if m.running {
		defer m.mu.Unlock()
		return m.cfg.EffectiveControlRole()
	}
	m.mu.Unlock()
	if cfg, err := readConfig(m.opts.Dir); err == nil {
		return cfg.EffectiveControlRole()
	}
	return RoleAdmin
}

// Status 是 GET /api/mesh/status 的内容。
type Status struct {
	Configured    bool        `json:"configured"`
	Enabled       bool        `json:"enabled"`
	Running       bool        `json:"running"`
	NodeID        string      `json:"node_id"`
	Label         string      `json:"label"`
	Version       string      `json:"version"`
	Coordinator   string      `json:"coordinator,omitempty"`
	CoordinatorID string      `json:"coordinator_id,omitempty"` // 协调节点证书指纹（连接时钉住它）
	NetworkID     string      `json:"network_id,omitempty"`
	Connected     bool        `json:"connected"`
	Since         time.Time   `json:"since,omitzero"`
	Observed      string      `json:"observed_addr,omitempty"`
	STUNAddrs     []string    `json:"stun_addrs"`
	PeerPort      int         `json:"peer_port"`
	NoListen      bool        `json:"no_listen"`
	PublicAddrs   []string    `json:"public_addrs"`
	ListenAddr    string      `json:"listen_addr,omitempty"`
	ListenError   string      `json:"listen_error,omitempty"`
	Candidates    []string    `json:"candidates"`
	ControlRole   string      `json:"control_role"`
	UDPPort       int         `json:"udp_port"` // 配置值，0 = 同 Peer 端口
	NoPunch       bool        `json:"no_punch"`
	Punch         PunchStatus `json:"punch"`
	LastError     string      `json:"last_error,omitempty"`
	LastErrorAt   time.Time   `json:"last_error_at,omitzero"`
}

// PunchStatus 是状态里的打洞一节（§12 P6-7）。只在运行中且打洞可用时 Active。
type PunchStatus struct {
	Active    bool      `json:"active"`
	UDPAddr   string    `json:"udp_addr,omitempty"`
	UDPError  string    `json:"udp_error,omitempty"`
	Mapping   string    `json:"mapping"` // none / easy / hard / unknown
	Srflx     []string  `json:"srflx"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
	Error     string    `json:"error,omitempty"`
}

func (st *Status) fillConfig(cfg *Config) {
	st.Label = cfg.Label
	st.Enabled = cfg.Enabled
	st.PeerPort = cfg.ListenPort()
	st.NoListen = cfg.NoListen
	st.PublicAddrs = append([]string{}, cfg.PublicAddrs...)
	st.ControlRole = cfg.EffectiveControlRole()
	st.UDPPort = cfg.UDPPort
	st.NoPunch = cfg.NoPunch
	if cfg.Coordinator != nil {
		st.Coordinator = cfg.Coordinator.Addr
		st.CoordinatorID = cfg.Coordinator.ID.String()
		st.NetworkID = cfg.Coordinator.NetworkID
	}
}

// Status 返回当前状态。只读：本机还没有身份时 NodeID 为空，不会因此生成身份。
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{Running: m.running, STUNAddrs: []string{}, Candidates: []string{}, PublicAddrs: []string{},
		PeerPort: DefaultPeerPort, ControlRole: RoleAdmin, Version: m.opts.Version,
		Punch: PunchStatus{Mapping: mappingName(stun.MappingUnknown), Srflx: []string{}}}
	if m.running {
		st.Configured = true
		st.NodeID = m.id.String()
		st.fillConfig(m.cfg)
		st.ListenAddr = m.listenAddr
		if m.listenErr != nil {
			st.ListenError = m.listenErr.Error()
		}
		st.Candidates = candidateAddrs(m.candidates())
		if m.coord != nil {
			cs := m.coord.snapshot()
			st.Connected, st.Since, st.Observed = cs.Connected, cs.Since, cs.ObservedAddr
			if cs.STUNAddrs != nil {
				st.STUNAddrs = cs.STUNAddrs
			}
			st.LastError, st.LastErrorAt = cs.LastError, cs.LastErrorAt
		}
		if m.punch != nil {
			st.Punch = m.punch.status()
		} else if m.punchErr != nil {
			st.Punch.Error = m.punchErr.Error()
		}
		return st
	}
	if id, ok := ExistingIdentity(m.opts.Dir); ok {
		st.NodeID = id.String()
	}
	if cfg, err := readConfig(m.opts.Dir); cfg != nil && (err == nil || errors.Is(err, ErrNotConfigured)) {
		st.fillConfig(cfg)
		st.Configured = err == nil && (cfg.Enabled || cfg.Coordinator != nil)
	}
	if m.startErr != nil && !errors.Is(m.startErr, ErrNotConfigured) {
		st.LastError = m.startErr.Error()
	}
	return st
}

// HelloResult 是一次 Hello 的结果。
type HelloResult struct {
	NodeID       string    `json:"node_id"`
	Path         PathKind  `json:"path"`
	LatencyMS    int64     `json:"latency_ms"`
	Version      string    `json:"version"`
	Capabilities []string  `json:"capabilities"`
	GrantedRole  string    `json:"granted_role"`
	Label        string    `json:"label,omitempty"`
	At           time.Time `json:"at"`
}

// helloRecord 是最近一次 Hello 的结果或错误（页面的在线状态取自它）。
type helloRecord struct {
	res *HelloResult
	err string
	at  time.Time
}

// ErrNotRunning 表示管理器互控没有在运行。
var ErrNotRunning = errors.New("管理器互控未运行")

// Hello 对 peer 发一次 Hello，返回路径类型、耗时、对方版本与授予本机的角色。
// 对已知对端（peers.json 里有记录或对方授权了本机）顺手把回答记进出站缓存。
func (m *Manager) Hello(ctx context.Context, peer meshid.ID) (*HelloResult, error) {
	h, release, err := m.acquire(peer)
	if err != nil {
		return nil, err
	}
	defer release()
	start := time.Now()
	resp, err := meshpb.NewPeerClient(h.cc).Hello(ctx, &meshpb.HelloRequest{Version: m.opts.Version, Capabilities: Capabilities()})
	if err != nil {
		m.recordHello(peer, nil, err)
		return nil, err
	}
	res := &HelloResult{
		NodeID:       peer.String(),
		Path:         h.currentKind(),
		LatencyMS:    time.Since(start).Milliseconds(),
		Version:      resp.GetVersion(),
		Capabilities: resp.GetCapabilities(),
		GrantedRole:  resp.GetGrantedRole().String(),
		Label:        resp.GetLabel(),
		At:           time.Now(),
	}
	m.recordHello(peer, res, nil)
	role := roleFromProto(resp.GetGrantedRole())
	if _, known := m.store.Peer(peer); known || role != "" {
		if err := m.store.RecordRemote(peer, role, resp.GetLabel(), resp.GetVersion(), nil); err != nil {
			logger.Warnf("[mesh] 记录 %s 的回答失败: %v", peer.Short(), err)
		}
	}
	if role != "" {
		m.wakeUpgrade(peer)
	}
	return res, nil
}

func (m *Manager) recordHello(peer meshid.ID, res *HelloResult, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hellos == nil {
		return
	}
	r := helloRecord{res: res, at: time.Now()}
	if err != nil {
		r.err = err.Error()
	}
	m.hellos[peer] = r
}

// PairResult 是本机向对端发起配对的结果。
type PairResult struct {
	NodeID      string `json:"node_id"`
	Status      string `json:"status"` // paired / pending
	GrantedRole string `json:"granted_role,omitempty"`
	Label       string `json:"label,omitempty"`
}

// PairWithInvite 用对方的邀请码配对（A 侧，§12 P3-3）。邀请码带直连地址时先记下，
// 无协调节点也能拨通。
func (m *Manager) PairWithInvite(ctx context.Context, invite string) (*PairResult, error) {
	inv, err := meshjoin.ParseInvite(invite)
	if err != nil {
		return nil, err
	}
	if len(inv.Addrs) > 0 {
		if err := m.store.RecordRemote(inv.Node, "", "", "", inv.Addrs); err != nil {
			return nil, err
		}
	}
	return m.pair(ctx, inv.Node, &meshpb.PairRequest{InviteId: inv.ID, InviteSecret: inv.Secret})
}

// RequestPair 向对方提交配对申请（申请-批准）。addrs 是可选的对方直连地址。
func (m *Manager) RequestPair(ctx context.Context, peer meshid.ID, addrs []string) (*PairResult, error) {
	for _, a := range addrs {
		if _, _, err := net.SplitHostPort(a); err != nil {
			return nil, fmt.Errorf("直连地址 %q 不是 host:port", a)
		}
	}
	if len(addrs) > 0 {
		if err := m.store.RecordRemote(peer, "", "", "", addrs); err != nil {
			return nil, err
		}
	}
	return m.pair(ctx, peer, &meshpb.PairRequest{})
}

func (m *Manager) pair(ctx context.Context, peer meshid.ID, req *meshpb.PairRequest) (*PairResult, error) {
	h, release, err := m.acquire(peer)
	if err != nil {
		return nil, err
	}
	defer release()
	req.Label, req.Version = m.label(), m.opts.Version
	resp, err := meshpb.NewPeerClient(h.cc).Pair(ctx, req)
	if err != nil {
		return nil, err
	}
	res := &PairResult{NodeID: peer.String(), Label: resp.GetLabel()}
	role := roleFromProto(resp.GetGrantedRole())
	switch resp.GetStatus() {
	case meshpb.PairStatus_PAIR_STATUS_PAIRED:
		res.Status, res.GrantedRole = "paired", role
		logger.Infof("[mesh] 已与 %s（%s）配对，对方授予本机 %s", peer.Short(), resp.GetLabel(), role)
	default:
		res.Status = "pending"
		logger.Infof("[mesh] 已向 %s 提交配对申请，等待对方批准", peer.Short())
	}
	if err := m.store.RecordRemote(peer, role, resp.GetLabel(), "", nil); err != nil {
		return nil, err
	}
	if role != "" {
		m.wakeUpgrade(peer)
	}
	return res, nil
}

// LocalAddrs 返回本机当前上报的直连地址（生成带直连地址的邀请码用）。未运行时为空。
func (m *Manager) LocalAddrs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running {
		return nil
	}
	return candidateAddrs(m.candidates())
}

// CreateInvite 生成邀请码。withLocalAddrs 为 true 时附带本机当前的直连地址。
func (m *Manager) CreateInvite(opts InviteOptions, withLocalAddrs bool) (string, InviteRecord, error) {
	id, err := LoadIdentity(m.opts.Dir)
	if err != nil {
		return "", InviteRecord{}, err
	}
	if withLocalAddrs {
		for _, a := range m.LocalAddrs() {
			if !slices.Contains(opts.Addrs, a) {
				opts.Addrs = append(opts.Addrs, a)
			}
		}
	}
	return m.store.CreateInvite(id, opts)
}

// PeerView 是 GET /api/mesh/peers 的一行：存储的记录 + 运行时状态。
type PeerView struct {
	PeerRecord
	DisplayName string       `json:"display_name"`
	ShortID     string       `json:"short_id"`
	Path        PathKind     `json:"path,omitempty"`
	LastHello   *HelloResult `json:"last_hello,omitempty"`
	LastError   string       `json:"last_error,omitempty"`
	LastCheckAt time.Time    `json:"last_check_at,omitzero"`
	LastPunch   *PunchRecord `json:"last_punch,omitempty"`
}

// Peers 返回所有对端（含运行时状态）。
func (m *Manager) Peers() ([]PeerView, error) {
	d, err := m.store.Snapshot()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PeerView, 0, len(d.Peers))
	for _, p := range d.Peers {
		v := PeerView{PeerRecord: p, DisplayName: p.DisplayLabel(), ShortID: p.NodeID.Short()}
		if p.Addrs == nil {
			v.Addrs = []string{}
		}
		if h := m.peers[p.NodeID]; h != nil {
			v.Path = h.currentKind()
		}
		if r, ok := m.hellos[p.NodeID]; ok {
			v.LastHello, v.LastError, v.LastCheckAt = r.res, r.err, r.at
		}
		if pp := m.punchPeers[p.NodeID]; pp != nil && !pp.record.At.IsZero() {
			rec := pp.record
			v.LastPunch = &rec
		}
		out = append(out, v)
	}
	return out, nil
}
