package mesh

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
	"asa-server/pkg/streamconn"
)

const (
	// keepaliveTime：家用路由器 NAT 表的空闲超时常见 60～300 秒，20 秒一次心跳保住映射。
	// 协调节点的 EnforcementPolicy.MinTime 是 10 秒，必须低于这里，否则会被以 too_many_pings 踢掉。
	keepaliveTime = 20 * time.Second

	backoffMax = time.Minute
	// stableSession：一次会话活过这么久，断开后退避从头算。被踢（Kicked）不算——
	// 否则两台拿着同一把私钥的机器会以 1 秒的节奏互踢。
	stableSession = 30 * time.Second
)

var errKicked = errors.New("被协调节点踢下线")

// coordState 是协调节点连接的可观察状态。
type coordState struct {
	Connected    bool
	Since        time.Time
	ObservedAddr string
	STUNAddrs    []string
	LastError    string
	LastErrorAt  time.Time
}

// coordClient 维护到协调节点的 Session 长连接，并提供中转路径。
type coordClient struct {
	cfg        CoordinatorConfig
	self       meshid.ID
	version    string
	label      string
	backoffMin time.Duration

	conn   *grpc.ClientConn
	client meshpb.CoordinatorClient

	// onIncoming 处理「有人经中转连你」：拿到包好的连接后交给 Peer 服务。
	onIncoming func(net.Conn)

	mu    sync.Mutex
	state coordState
}

func newCoordClient(cfg CoordinatorConfig, cert tls.Certificate, self meshid.ID, version, label string,
	backoffMin time.Duration, onIncoming func(net.Conn)) (*coordClient, error) {
	conn, err := grpc.NewClient(cfg.Addr,
		grpc.WithTransportCredentials(credentials.NewTLS(meshid.ClientConfig(cert, cfg.ID))),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: keepaliveTime, Timeout: 10 * time.Second, PermitWithoutStream: true}),
	)
	if err != nil {
		return nil, err
	}
	return &coordClient{
		cfg: cfg, self: self, version: version, label: label, backoffMin: backoffMin,
		conn: conn, client: meshpb.NewCoordinatorClient(conn), onIncoming: onIncoming,
	}, nil
}

func (c *coordClient) close() error { return c.conn.Close() }

func (c *coordClient) snapshot() coordState {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state
	s.STUNAddrs = append([]string(nil), s.STUNAddrs...)
	return s
}

func (c *coordClient) setError(err error) {
	c.mu.Lock()
	c.state.Connected = false
	c.state.LastError = err.Error()
	c.state.LastErrorAt = time.Now()
	c.mu.Unlock()
}

// run 保持 Session 在线直到 ctx 结束：断开后按 1 秒起、翻倍、上限 60 秒的退避重连。
func (c *coordClient) run(ctx context.Context) {
	backoff := c.backoffMin
	for {
		start := time.Now()
		err := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		c.setError(err)
		switch {
		case errors.Is(err, errKicked):
			logger.Errorf("[mesh] %v", err)
		case time.Since(start) >= stableSession:
			backoff = c.backoffMin
			logger.Warnf("[mesh] 与协调节点 %s 的连接断开：%v", c.cfg.Addr, err)
		default:
			// 连不上时只在退避到顶之前记，免得断网的机器每分钟刷一条。
			if backoff < backoffMax {
				logger.Warnf("[mesh] 连接协调节点 %s 失败：%v（%s 后重试）", c.cfg.Addr, err, backoff)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, backoffMax)
	}
}

// session 跑一次 Session，直到它结束。
func (c *coordClient) session(ctx context.Context) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := c.client.Session(sctx)
	if err != nil {
		return err
	}
	err = stream.Send(&meshpb.NodeMessage{Msg: &meshpb.NodeMessage_Register{Register: &meshpb.Register{
		NetworkId:     c.cfg.NetworkID,
		NetworkSecret: c.cfg.NetworkSecret,
		Version:       c.version,
		Capabilities:  []string{CapRelay},
		Label:         c.label,
	}}})
	if err != nil {
		return err
	}
	for {
		m, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("协调节点关闭了会话")
			}
			return err
		}
		switch msg := m.GetMsg().(type) {
		case *meshpb.CoordMessage_Registered:
			r := msg.Registered
			if r.GetNodeId() != c.self.Compact() {
				// 协调节点按 TLS 证书算出的 ID 与本机不符：中间有东西在替我们握手。
				return fmt.Errorf("协调节点回显的节点 ID 与本机不符（%s）", r.GetNodeId())
			}
			c.mu.Lock()
			c.state = coordState{Connected: true, Since: time.Now(), ObservedAddr: r.GetObservedAddr(),
				STUNAddrs: r.GetStunAddrs(), LastError: c.state.LastError, LastErrorAt: c.state.LastErrorAt}
			c.mu.Unlock()
			logger.Infof("[mesh] 已登记到协调节点 %s，出口地址 %s", c.cfg.Addr, r.GetObservedAddr())
		case *meshpb.CoordMessage_IncomingRelay:
			go c.acceptRelay(ctx, msg.IncomingRelay)
		case *meshpb.CoordMessage_Kicked:
			return fmt.Errorf("%w：%s（同一身份可能在另一台机器上运行）", errKicked, msg.Kicked.GetReason())
		}
	}
}

// openRelayStream 开一条 Relay 流并 Join，包成 net.Conn。流的生命周期独立于调用方的 ctx
// （拨号 ctx 在拨号完成后就会被取消，连接还要继续用）。
func (c *coordClient) openRelayStream(sessionID string, token []byte, remote string) (net.Conn, error) {
	ctx, cancel := context.WithCancel(context.Background())
	rs, err := c.client.Relay(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	if err := rs.Send(&meshpb.RelayFrame{Msg: &meshpb.RelayFrame_Join{Join: &meshpb.RelayJoin{SessionId: sessionID, Token: token}}}); err != nil {
		cancel()
		return nil, err
	}
	return streamconn.New(streamconn.Options{
		Recv: func() ([]byte, error) {
			for {
				f, err := rs.Recv()
				if err != nil {
					return nil, err
				}
				if d := f.GetData(); len(d) > 0 {
					return d, nil
				}
			}
		},
		Send: func(b []byte) error {
			return rs.Send(&meshpb.RelayFrame{Msg: &meshpb.RelayFrame_Data{Data: b}})
		},
		CloseSend:  rs.CloseSend,
		Abort:      cancel,
		RemoteAddr: streamconn.Addr("relay:" + remote),
	}), nil
}

func (c *coordClient) acceptRelay(ctx context.Context, ir *meshpb.IncomingRelay) {
	from := ir.GetFromNodeId()
	if id, err := meshid.ParseID(from); err == nil {
		from = id.Short()
	}
	conn, err := c.openRelayStream(ir.GetSessionId(), ir.GetToken(), from)
	if err != nil {
		logger.Warnf("[mesh] 接受来自 %s 的中转失败：%v", from, err)
		return
	}
	if ctx.Err() != nil {
		conn.Close()
		return
	}
	c.onIncoming(conn)
}

// relayProvider 是中转路径。
type relayProvider struct{ c *coordClient }

func (relayProvider) Kind() PathKind { return PathRelay }

func (p relayProvider) Dial(ctx context.Context, peer meshid.ID) (net.Conn, error) {
	resp, err := p.c.client.OpenRelay(ctx, &meshpb.OpenRelayRequest{TargetNodeId: peer.Compact()})
	if err != nil {
		return nil, err
	}
	return p.c.openRelayStream(resp.GetSessionId(), resp.GetToken(), peer.Short())
}
