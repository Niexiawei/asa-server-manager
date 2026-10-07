package mesh

import (
	"context"
	"crypto/tls"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
)

// 打洞路径上的 QUIC（§5.6.3 第 5～7 步、§12 P6-5）。发起方 Dial、应答方 Listen；
// 每条 QUIC 连接只开一条流，包成 net.Conn 之后照旧跑端到端 TLS + gRPC（TLS 套 TLS，上层零差别）。

const (
	quicALPN = "asa-mesh/1"
	// quicKeepAlive / quicIdle：NAT 的 UDP 映射空闲超时常见 30 秒起，15 秒一次保活保住映射。
	quicKeepAlive = 15 * time.Second
	quicIdle      = 45 * time.Second
	// quicHandshake：探测已经证明地址可达，握手不该慢。
	quicHandshake = 5 * time.Second
	// quicStreamWait：应答方准入后等发起方开流（它紧接着就发内层 TLS 的 ClientHello）。
	quicStreamWait = 10 * time.Second
)

// quicRefused 是准入失败时给对方的应用错误码。
const quicRefused quic.ApplicationErrorCode = 1

func quicConfig() *quic.Config {
	return &quic.Config{
		HandshakeIdleTimeout:  quicHandshake,
		MaxIdleTimeout:        quicIdle,
		KeepAlivePeriod:       quicKeepAlive,
		MaxIncomingStreams:    1,
		MaxIncomingUniStreams: -1,
	}
}

func quicServerTLS(cert tls.Certificate) *tls.Config {
	cfg := meshid.AnyClientServerConfig(cert)
	cfg.NextProtos = []string{quicALPN}
	return cfg
}

// quicConn 把一条 QUIC 流包成 net.Conn。连接上只有这一条流，生命周期一致：Close 关整条连接。
type quicConn struct {
	*quic.Stream
	conn *quic.Conn
	once sync.Once
}

func (q *quicConn) LocalAddr() net.Addr  { return q.conn.LocalAddr() }
func (q *quicConn) RemoteAddr() net.Addr { return q.conn.RemoteAddr() }

func (q *quicConn) Close() error {
	q.once.Do(func() {
		q.Stream.CancelRead(0)
		q.Stream.Close()
		q.conn.CloseWithError(0, "")
	})
	return nil
}

// dialQUIC 是发起方：钉对方公钥建 QUIC → 开流 → 内层端到端 mTLS（与直连、中转完全相同）。
func (p *puncher) dialQUIC(ctx context.Context, addr netip.AddrPort, peer meshid.ID) (*pathConn, error) {
	ctx, cancel := context.WithTimeout(ctx, quicHandshake+2*time.Second)
	defer cancel()
	tlsConf := meshid.ClientConfig(p.cert, peer)
	tlsConf.NextProtos = []string{quicALPN}
	qc, err := p.mux.tr.Dial(ctx, net.UDPAddrFromAddrPort(addr), tlsConf, quicConfig())
	if err != nil {
		return nil, err
	}
	p.track(qc)
	st, err := qc.OpenStreamSync(ctx)
	if err != nil {
		qc.CloseWithError(0, "")
		return nil, err
	}
	tc, err := clientTLS(ctx, &quicConn{Stream: st, conn: qc}, p.cert, peer)
	if err != nil {
		return nil, err
	}
	return &pathConn{Conn: tc, kind: PathPunched}, nil
}

// acceptLoop 是应答方：接受 QUIC 连接，准入后交给 Peer gRPC 服务。
func (p *puncher) acceptLoop(ctx context.Context) {
	for {
		qc, err := p.ln.Accept(ctx)
		if err != nil {
			return
		}
		go p.handleInbound(ctx, qc)
	}
}

// handleInbound 做准入：UDP 端口对全网可见，只接受**有打洞会话的发起方**——内层 TLS 与拦截器本来也会挡，
// 这里是提前、更省地挡。
func (p *puncher) handleInbound(ctx context.Context, qc *quic.Conn) {
	id, err := meshid.PeerID(qc.ConnectionState().TLS)
	if err != nil || !p.admit(id) {
		qc.CloseWithError(quicRefused, "no punch session")
		logger.Debugf("[mesh] 拒绝来自 %s 的 QUIC 连接：没有对应的打洞会话", qc.RemoteAddr())
		return
	}
	actx, cancel := context.WithTimeout(ctx, quicStreamWait)
	st, err := qc.AcceptStream(actx)
	cancel()
	if err != nil {
		qc.CloseWithError(quicRefused, "no stream")
		return
	}
	p.track(qc)
	logger.Infof("[mesh] 接受来自 %s 的打洞连接（%s）", id.Short(), qc.RemoteAddr())
	p.deliver(&quicConn{Stream: st, conn: qc})
}
