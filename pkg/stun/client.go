package stun

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

var (
	// ErrTxMismatch：回包的事务 ID 不是我们发出的那个（迟到的旧回包、别人的包）。
	ErrTxMismatch = errors.New("stun: 事务 ID 不匹配")
	// ErrNoAddress：成功回包里没有地址属性。
	ErrNoAddress = errors.New("stun: 回包里没有映射地址")
)

// ErrorResponse 是服务端回的 Binding error。
type ErrorResponse struct {
	Code    int
	Reason  string
	Unknown []uint16 // 420 时服务端不认识的属性
}

func (e *ErrorResponse) Error() string {
	return fmt.Sprintf("stun: 服务端返回错误 %d %s", e.Code, e.Reason)
}

// NewBindingRequest 生成一条 Binding 请求（带 FINGERPRINT）。
//
// 请求与解析拆成两半而不是只给一个 Query：P6 的管理器在同一个 UDP socket 上跑 QUIC，
// 回包要经 quic.Transport.ReadNonQUICPacket 取出，客户端不能自己去读 socket。
func NewBindingRequest() (TxID, []byte) {
	tx := NewTxID()
	return tx, (&Message{Type: TypeBindingRequest, TxID: tx}).Encode(true)
}

// ParseBindingResponse 解析 tx 对应的回包，返回服务端看到的我们的地址。
func ParseBindingResponse(b []byte, tx TxID) (netip.AddrPort, error) {
	m, err := Parse(b)
	if err != nil {
		return netip.AddrPort{}, err
	}
	if m.TxID != tx {
		return netip.AddrPort{}, ErrTxMismatch
	}
	if m.Method() != MethodBinding {
		return netip.AddrPort{}, fmt.Errorf("%w：不是 Binding 回包", ErrMalformed)
	}
	switch m.Class() {
	case ClassSuccess:
		if v, ok := m.Get(AttrXORMappedAddress); ok {
			return DecodeXORMappedAddress(v, tx)
		}
		if v, ok := m.Get(AttrMappedAddress); ok {
			return DecodeMappedAddress(v)
		}
		return netip.AddrPort{}, ErrNoAddress
	case ClassError:
		e := &ErrorResponse{}
		if v, ok := m.Get(AttrErrorCode); ok {
			e.Code, e.Reason, _ = DecodeErrorCode(v)
		}
		if v, ok := m.Get(AttrUnknownAttributes); ok {
			e.Unknown = DecodeUnknownAttributes(v)
		}
		return netip.AddrPort{}, e
	default:
		return netip.AddrPort{}, fmt.Errorf("%w：不是回包", ErrMalformed)
	}
}

// retransmits 是 Query 的重传间隔（RFC 5389 §7.2.1 的简化：RTO 500ms 起翻倍，共发三次）。
var retransmits = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}

// Query 在普通 UDP socket 上问一次 server：发请求、等回包，按 retransmits 重传。
// 期间会改动 pc 的读 deadline，返回前恢复为零值；不要与别的读者共用 pc。
func Query(ctx context.Context, pc net.PacketConn, server net.Addr) (netip.AddrPort, error) {
	tx, req := NewBindingRequest()
	defer pc.SetReadDeadline(time.Time{})

	buf := make([]byte, maxPacket)
	lastErr := error(context.DeadlineExceeded)
	for _, rto := range retransmits {
		if err := ctx.Err(); err != nil {
			return netip.AddrPort{}, err
		}
		if _, err := pc.WriteTo(req, server); err != nil {
			return netip.AddrPort{}, err
		}
		until := time.Now().Add(rto)
		if d, ok := ctx.Deadline(); ok && d.Before(until) {
			until = d
		}
		_ = pc.SetReadDeadline(until)
		for {
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					break // 这一轮超时，重传
				}
				if errors.Is(err, net.ErrClosed) {
					return netip.AddrPort{}, err
				}
				// Windows 上 ICMP 端口不可达会表现为一次 ReadFrom 错误：记下，继续等这一轮。
				lastErr = err
				continue
			}
			ap, err := ParseBindingResponse(buf[:n], tx)
			if errors.Is(err, ErrTxMismatch) || errors.Is(err, ErrMalformed) {
				continue // 不是给这次请求的，接着等
			}
			if err != nil {
				return netip.AddrPort{}, err
			}
			return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()), nil
		}
	}
	if ctx.Err() != nil {
		return netip.AddrPort{}, ctx.Err()
	}
	return netip.AddrPort{}, fmt.Errorf("stun: %s 无应答: %w", server, lastErr)
}
