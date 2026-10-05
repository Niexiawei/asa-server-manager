package mesh

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
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
}

// Manager 是管理器互控的运行时。照 frpmanage 的形态：包级单例 + 可直接 New（测试用）。
type Manager struct {
	opts Options

	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	cfg      *Config
	id       meshid.ID
	cert     tls.Certificate
	coord    *coordClient
	peerSrv  *grpc.Server
	relayLn  *relayListener
	peers    map[meshid.ID]*grpc.ClientConn
	lastPath map[meshid.ID]PathKind
	startErr error
}

// New 建一个 Manager。不碰磁盘、不联网——这些都在 Start 里。
func New(opts Options) *Manager {
	if opts.Version == "" {
		opts.Version = AppVersion
	}
	if opts.BackoffMin <= 0 {
		opts.BackoffMin = time.Second
	}
	return &Manager{opts: opts}
}

var globalManager *Manager

// Initialize 建立包级单例。只记下目录，零副作用。
func Initialize(baseDir string) *Manager {
	globalManager = New(Options{Dir: Dir(baseDir), Version: AppVersion})
	return globalManager
}

// GetGlobalManager 返回包级单例（未 Initialize 时为 nil）。
func GetGlobalManager() *Manager { return globalManager }

// Dir 返回 {BaseDir}/mesh。
func (m *Manager) Dir() string { return m.opts.Dir }

// Start 读取配置并接入协调节点。未配置时返回 ErrNotConfigured，不发起任何连接、不监听任何端口。
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

	ctx, cancel := context.WithCancel(context.Background())
	relayLn := newRelayListener()
	coord, err := newCoordClient(*cfg.Coordinator, cert, id, m.opts.Version, cfg.Label, m.opts.BackoffMin,
		func(c net.Conn) { relayLn.deliver(c) })
	if err != nil {
		cancel()
		m.startErr = err
		return err
	}
	svc := &peerService{version: m.opts.Version, label: m.label, auth: noPairs{}}
	peerSrv := newPeerServer(credentials.NewTLS(meshid.AnyClientServerConfig(cert)), svc)
	go func() {
		if err := peerSrv.Serve(relayLn); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			logger.Warnf("[mesh] Peer 服务退出: %v", err)
		}
	}()
	go coord.run(ctx)

	m.cfg, m.id, m.cert = cfg, id, cert
	m.cancel, m.coord, m.peerSrv, m.relayLn = cancel, coord, peerSrv, relayLn
	m.peers = map[meshid.ID]*grpc.ClientConn{}
	m.lastPath = map[meshid.ID]PathKind{}
	m.running = true
	m.startErr = nil
	logger.Infof("[mesh] 已启动：本机 %s，协调节点 %s", id.Short(), cfg.Coordinator.Addr)
	return nil
}

// Stop 断开协调节点、关闭 Peer 服务与所有对端连接。未在运行时是空操作。
func (m *Manager) Stop() error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	cancel, peers, peerSrv, relayLn, coord := m.cancel, m.peers, m.peerSrv, m.relayLn, m.coord
	m.running = false
	m.peers, m.coord, m.peerSrv, m.relayLn, m.lastPath = nil, nil, nil, nil, nil
	m.mu.Unlock()

	// 锁外关闭：对端连接的 dialer 回调要拿 m.mu，持锁关闭可能与它互等。
	cancel()
	for _, cc := range peers {
		cc.Close()
	}
	peerSrv.Stop()
	relayLn.Close()
	err := coord.close()
	logger.Infof("[mesh] 已停止")
	return err
}

func (m *Manager) label() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg == nil {
		return ""
	}
	return m.cfg.Label
}

// Status 是 GET /api/mesh/status 的内容。
type Status struct {
	Configured  bool      `json:"configured"`
	Running     bool      `json:"running"`
	NodeID      string    `json:"node_id"`
	Coordinator string    `json:"coordinator,omitempty"`
	NetworkID   string    `json:"network_id,omitempty"`
	Connected   bool      `json:"connected"`
	Since       time.Time `json:"since,omitzero"`
	Observed    string    `json:"observed_addr,omitempty"`
	STUNAddrs   []string  `json:"stun_addrs"`
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitzero"`
}

// Status 返回当前状态。只读：本机还没有身份时 NodeID 为空，不会因此生成身份。
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{Running: m.running, STUNAddrs: []string{}}
	if m.running {
		st.Configured = true
		st.NodeID = m.id.String()
		st.Coordinator = m.cfg.Coordinator.Addr
		st.NetworkID = m.cfg.Coordinator.NetworkID
		cs := m.coord.snapshot()
		st.Connected, st.Since, st.Observed = cs.Connected, cs.Since, cs.ObservedAddr
		if cs.STUNAddrs != nil {
			st.STUNAddrs = cs.STUNAddrs
		}
		st.LastError, st.LastErrorAt = cs.LastError, cs.LastErrorAt
		return st
	}
	if id, ok := ExistingIdentity(m.opts.Dir); ok {
		st.NodeID = id.String()
	}
	if cfg, err := LoadConfig(m.opts.Dir); err == nil {
		st.Configured = true
		st.Coordinator = cfg.Coordinator.Addr
		st.NetworkID = cfg.Coordinator.NetworkID
	}
	if m.startErr != nil && !errors.Is(m.startErr, ErrNotConfigured) {
		st.LastError = m.startErr.Error()
	}
	return st
}

// HelloResult 是一次 Hello 的结果。
type HelloResult struct {
	NodeID       string   `json:"node_id"`
	Path         PathKind `json:"path"`
	LatencyMS    int64    `json:"latency_ms"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
	GrantedRole  string   `json:"granted_role"`
}

// ErrNotRunning 表示管理器互控没有在运行。
var ErrNotRunning = errors.New("管理器互控未运行")

// Hello 对 peer 发一次 Hello，返回路径类型、耗时与对方版本。P1 验收与排障用。
func (m *Manager) Hello(ctx context.Context, peer meshid.ID) (*HelloResult, error) {
	cc, err := m.peerConn(peer)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	resp, err := meshpb.NewPeerClient(cc).Hello(ctx, &meshpb.HelloRequest{Version: m.opts.Version, Capabilities: []string{CapRelay}})
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	path := m.lastPath[peer]
	m.mu.Unlock()
	return &HelloResult{
		NodeID:       peer.String(),
		Path:         path,
		LatencyMS:    time.Since(start).Milliseconds(),
		Version:      resp.GetVersion(),
		Capabilities: resp.GetCapabilities(),
		GrantedRole:  resp.GetGrantedRole().String(),
	}, nil
}

// peerConn 返回到 peer 的 gRPC 连接（每个对端一个，懒建）。gRPC 断线重连时会再次调用
// dialer，于是「重新选路」自动发生。
func (m *Manager) peerConn(peer meshid.ID) (*grpc.ClientConn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running {
		return nil, ErrNotRunning
	}
	if peer == m.id {
		return nil, errors.New("不能连接自己")
	}
	if cc := m.peers[peer]; cc != nil {
		return cc, nil
	}
	providers := []pathProvider{relayProvider{c: m.coord}}
	cc, err := grpc.NewClient("passthrough:///"+peer.Compact(),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			pc, err := dialPeer(ctx, providers, peer)
			if err != nil {
				return nil, err
			}
			m.mu.Lock()
			if m.lastPath != nil {
				m.lastPath[peer] = pc.kind
			}
			m.mu.Unlock()
			return pc, nil
		}),
		// 端到端 mTLS：只接受公钥是 peer 的对端。路径从哪来（中转、以后的直连 / 打洞）都一样。
		grpc.WithTransportCredentials(credentials.NewTLS(meshid.ClientConfig(m.cert, peer))),
	)
	if err != nil {
		return nil, err
	}
	m.peers[peer] = cc
	return cc, nil
}
