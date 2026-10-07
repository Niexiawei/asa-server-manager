package mesh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"asa-server/pkg/stun"
)

// udpMux 是打洞用的 UDP socket（§12 P6-1）。**一个** socket 承载 STUN、探测与 QUIC：
// 打洞在 NAT 上开出来的映射属于这个 socket，之后的 QUIC 必须从同一个 socket 发出，否则映射对不上。
//
// QUIC 包由 quic.Transport 自己处理；其余的包（首字节高两位为 0）经 ReadNonQUICPacket 交给 run，
// 再按内容分给等它的人：STUN 回包按事务 ID，探测包按会话 ID。
type udpMux struct {
	pc   net.PacketConn
	tr   *quic.Transport
	port int

	mu       sync.Mutex
	stunWait map[stun.TxID]chan []byte
	sessions map[[punchSIDLen]byte]*punchSession
}

// openUDP 绑定 addr；绑不上（端口被占）时退到系统分配的端口——打洞照样能用（反射地址是 STUN 问出来的），
// 返回的 bindErr 只进状态。wrap 是测试钩子（模拟 NAT 的过滤）。
func openUDP(addr string, wrap func(net.PacketConn) net.PacketConn) (u *udpMux, bindErr error, err error) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		bindErr = fmt.Errorf("绑定 UDP %s 失败：%w（已改用随机端口）", addr, err)
		host, _, _ := net.SplitHostPort(addr)
		if pc, err = net.ListenPacket("udp", net.JoinHostPort(host, "0")); err != nil {
			return nil, bindErr, err
		}
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	if wrap != nil {
		pc = wrap(pc)
	}
	return &udpMux{
		pc:       pc,
		tr:       &quic.Transport{Conn: pc},
		port:     port,
		stunWait: map[stun.TxID]chan []byte{},
		sessions: map[[punchSIDLen]byte]*punchSession{},
	}, bindErr, nil
}

// close 关掉 Transport（顺带关掉其上所有 QUIC 连接）与 socket。Transport 不关它没有创建的 socket。
func (u *udpMux) close() {
	u.tr.Close()
	u.pc.Close()
}

func (u *udpMux) localAddr() string { return u.pc.LocalAddr().String() }

// run 分发非 QUIC 包，直到 ctx 结束或 Transport 关闭。
//
// ⚠️ quic-go 的非 QUIC 包队列只有 32 个、满了就丢（maxQueuedNonQUICPackets），这里绝不能阻塞：
// 投递一律非阻塞，等待者自己带缓冲。
func (u *udpMux) run(ctx context.Context) {
	buf := make([]byte, 1500)
	for {
		n, from, err := u.tr.ReadNonQUICPacket(ctx, buf)
		if err != nil {
			return
		}
		u.dispatch(buf[:n], from)
	}
}

func (u *udpMux) dispatch(b []byte, from net.Addr) {
	switch {
	case isProbe(b):
		var sid [punchSIDLen]byte
		copy(sid[:], b[2:2+punchSIDLen])
		u.mu.Lock()
		s := u.sessions[sid]
		u.mu.Unlock()
		if s != nil {
			if ap, ok := addrPortOf(from); ok {
				s.deliver(append([]byte(nil), b...), ap)
			}
		}
	case stun.IsMessage(b):
		m, err := stun.Parse(b)
		if err != nil {
			return
		}
		u.mu.Lock()
		ch := u.stunWait[m.TxID]
		u.mu.Unlock()
		if ch != nil {
			select {
			case ch <- append([]byte(nil), b...):
			default: // 重传带来的重复回包
			}
		}
	}
}

func (u *udpMux) addSession(s *punchSession) {
	u.mu.Lock()
	u.sessions[s.id] = s
	u.mu.Unlock()
}

func (u *udpMux) removeSession(s *punchSession) {
	u.mu.Lock()
	if u.sessions[s.id] == s {
		delete(u.sessions, s.id)
	}
	u.mu.Unlock()
}

func (u *udpMux) writeTo(b []byte, to netip.AddrPort) {
	// 发不出去（例如没有 IPv6 路由）就算了：探测本来就是「多发几个，有一个到就行」。
	_, _ = u.tr.WriteTo(b, net.UDPAddrFromAddrPort(to))
}

// stunRetransmits 同 stun.Query：RTO 500ms 起翻倍，共发三次（RFC 5389 §7.2.1 的简化）。
var stunRetransmits = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}

var errSTUNTimeout = errors.New("STUN 无应答")

// stunQuery 经本 socket 问一次 server，返回 server 看到的本机地址。不能直接用 stun.Query：
// 它要独占读 socket，而这里的读属于 quic.Transport。
func (u *udpMux) stunQuery(ctx context.Context, server netip.AddrPort) (netip.AddrPort, error) {
	tx, req := stun.NewBindingRequest()
	ch := make(chan []byte, 1)
	u.mu.Lock()
	u.stunWait[tx] = ch
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		delete(u.stunWait, tx)
		u.mu.Unlock()
	}()
	for _, wait := range stunRetransmits {
		if _, err := u.tr.WriteTo(req, net.UDPAddrFromAddrPort(server)); err != nil {
			return netip.AddrPort{}, err
		}
		t := time.NewTimer(wait)
		select {
		case b := <-ch:
			t.Stop()
			ap, err := stun.ParseBindingResponse(b, tx)
			return unmapAddrPort(ap), err
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return netip.AddrPort{}, ctx.Err()
		}
	}
	return netip.AddrPort{}, errSTUNTimeout
}

func addrPortOf(a net.Addr) (netip.AddrPort, bool) {
	ua, ok := a.(*net.UDPAddr)
	if !ok {
		return netip.AddrPort{}, false
	}
	return unmapAddrPort(ua.AddrPort()), true
}

// unmapAddrPort 把双栈 socket 上的 ::ffff:a.b.c.d 还原成 IPv4，同一个对端只有一种写法。
func unmapAddrPort(ap netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
}
