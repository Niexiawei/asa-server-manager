package stun

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// ServerOptions 是 Serve 的参数。零值字段取默认值。
type ServerOptions struct {
	// RatePerSource / BurstPerSource：每个来源（IPv4 /32、IPv6 /64）的令牌桶。默认 5 次/秒、突发 10。
	RatePerSource  float64
	BurstPerSource int
	// RateGlobal：全局令牌桶（突发 = 1 秒的量）。默认 2000 次/秒。
	RateGlobal float64
	// MaxSources：来源表上限，满了新来源只受全局桶约束。默认 65536。
	MaxSources int
	// SourceIdle：来源多久不活动就从表里淘汰。默认 1 分钟。
	SourceIdle time.Duration

	// Stats 非 nil 时累加计数。
	Stats *Stats
	// Warnf 非 nil 时报告限流（首次发生时一条，之后每分钟最多一条）。
	Warnf func(format string, args ...any)

	now func() time.Time // 测试注入
}

func (o *ServerOptions) withDefaults() ServerOptions {
	r := *o
	if r.RatePerSource <= 0 {
		r.RatePerSource = 5
	}
	if r.BurstPerSource <= 0 {
		r.BurstPerSource = 10
	}
	if r.RateGlobal <= 0 {
		r.RateGlobal = 2000
	}
	if r.MaxSources <= 0 {
		r.MaxSources = 65536
	}
	if r.SourceIdle <= 0 {
		r.SourceIdle = time.Minute
	}
	if r.now == nil {
		r.now = time.Now
	}
	return r
}

// Stats 是服务端计数器，可并发读取。
type Stats struct {
	Received         atomic.Uint64 // 收到的包
	Success          atomic.Uint64 // 回了 Binding success
	UnknownAttribute atomic.Uint64 // 回了 420
	DropMalformed    atomic.Uint64 // 不合法 / 不是 Binding request，静默丢弃
	DropRateLimited  atomic.Uint64 // 超出限流，静默丢弃
}

// Snapshot 是 Stats 某一时刻的值。
type Snapshot struct {
	Received, Success, UnknownAttribute, DropMalformed, DropRateLimited uint64
}

// Snapshot 读取当前计数。
func (s *Stats) Snapshot() Snapshot {
	return Snapshot{
		Received:         s.Received.Load(),
		Success:          s.Success.Load(),
		UnknownAttribute: s.UnknownAttribute.Load(),
		DropMalformed:    s.DropMalformed.Load(),
		DropRateLimited:  s.DropRateLimited.Load(),
	}
}

// maxPacket：STUN 报文没有理由超过 MTU，读缓冲取一个以太网 MTU 之上的整数。
const maxPacket = 1500

// Serve 在 pc 上提供 STUN Binding 服务，直到 ctx 结束或 pc 被关闭。无会话状态：
// 读一个包、就地回写。ctx 结束时返回 ctx.Err()；不关闭 pc（归调用方）。
//
// 规则（docs/REMOTE_MANAGER_MESH_PLAN.md §12「P1-8」）：
//   - 不合法报文、非 request、非 Binding 方法、超出限流：静默丢弃，不给伪造来源的垃圾包任何回包。
//   - Binding request 不带「必须理解」属性：回 XOR-MAPPED-ADDRESS + FINGERPRINT。
//   - 带「必须理解」属性（本服务一个都不认识）：回 420 + UNKNOWN-ATTRIBUTES（RFC 5389 §7.3.1）。
//   - 不回 MAPPED-ADDRESS / SOFTWARE / OTHER-ADDRESS：省放大量，也不在未认证端口上报版本。
func Serve(ctx context.Context, pc net.PacketConn, opts ServerOptions) error {
	o := opts.withDefaults()
	s := &server{opts: o, limiter: newLimiter(o)}

	stop := context.AfterFunc(ctx, func() { _ = pc.SetReadDeadline(time.Now()) })
	defer stop()

	buf := make([]byte, maxPacket)
	consecutiveErrs := 0
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			// 其余错误是瞬态的：例如 Windows 上向不可达端口回过包后，下一次 ReadFrom 会收到
			// WSAECONNRESET（ICMP 端口不可达）。不能因此停止服务；持续出错时稍微退让，别空转。
			consecutiveErrs++
			if consecutiveErrs > 100 {
				time.Sleep(10 * time.Millisecond)
			}
			continue
		}
		consecutiveErrs = 0
		resp := s.handle(buf[:n], from)
		if resp != nil {
			_, _ = pc.WriteTo(resp, from)
		}
	}
}

type server struct {
	opts    ServerOptions
	limiter *limiter
}

// handle 处理一个包，返回要回写的字节（nil = 不回）。
func (s *server) handle(pkt []byte, from net.Addr) []byte {
	st := s.opts.Stats
	if st != nil {
		st.Received.Add(1)
	}
	src, ok := addrPortOf(from)
	if !ok {
		return nil
	}
	if !s.limiter.allow(src.Addr()) {
		if st != nil {
			st.DropRateLimited.Add(1)
		}
		s.limiter.warn(s.opts.Warnf, src.Addr())
		return nil
	}
	m, err := Parse(pkt)
	if err != nil || m.Class() != ClassRequest || m.Method() != MethodBinding {
		if st != nil {
			st.DropMalformed.Add(1)
		}
		return nil
	}

	var unknown []uint16
	for _, a := range m.Attrs {
		if comprehensionRequired(a.Type) && !containsType(unknown, a.Type) {
			unknown = append(unknown, a.Type)
		}
	}
	if len(unknown) > 0 {
		if st != nil {
			st.UnknownAttribute.Add(1)
		}
		resp := &Message{Type: TypeBindingError, TxID: m.TxID, Attrs: []Attr{
			{Type: AttrErrorCode, Value: EncodeErrorCode(420, "Unknown Attribute")},
			{Type: AttrUnknownAttributes, Value: EncodeUnknownAttributes(unknown)},
		}}
		return resp.Encode(true)
	}

	if st != nil {
		st.Success.Add(1)
	}
	resp := &Message{Type: TypeBindingSuccess, TxID: m.TxID, Attrs: []Attr{
		{Type: AttrXORMappedAddress, Value: EncodeXORMappedAddress(src, m.TxID)},
	}}
	return resp.Encode(true)
}

func containsType(list []uint16, t uint16) bool {
	for _, x := range list {
		if x == t {
			return true
		}
	}
	return false
}

func addrPortOf(a net.Addr) (netip.AddrPort, bool) {
	switch v := a.(type) {
	case *net.UDPAddr:
		ap := v.AddrPort()
		return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()), ap.IsValid()
	default:
		ap, err := netip.ParseAddrPort(a.String())
		if err != nil {
			return netip.AddrPort{}, false
		}
		return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()), true
	}
}

// ---- 限流 ----

type bucket struct {
	tokens float64
	last   time.Time
}

func (b *bucket) take(now time.Time, rate float64, burst float64) bool {
	b.tokens = min(burst, b.tokens+now.Sub(b.last).Seconds()*rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type limiter struct {
	opts ServerOptions

	mu        sync.Mutex
	global    bucket
	sources   map[netip.Prefix]*bucket
	lastSweep time.Time

	lastWarn time.Time
	warned   bool
}

func newLimiter(o ServerOptions) *limiter {
	now := o.now()
	return &limiter{
		opts:      o,
		global:    bucket{tokens: o.RateGlobal, last: now},
		sources:   make(map[netip.Prefix]*bucket),
		lastSweep: now,
	}
}

// sourceKey：IPv4 按 /32，IPv6 按 /64——一条家宽 IPv6 前缀里换地址是零成本的，按单地址限流等于没限。
func sourceKey(a netip.Addr) netip.Prefix {
	a = a.Unmap()
	bits := 32
	if a.Is6() {
		bits = 64
	}
	p, _ := a.Prefix(bits)
	return p
}

func (l *limiter) allow(a netip.Addr) bool {
	now := l.opts.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastSweep) >= l.opts.SourceIdle {
		for k, b := range l.sources {
			if now.Sub(b.last) >= l.opts.SourceIdle {
				delete(l.sources, k)
			}
		}
		l.lastSweep = now
	}

	key := sourceKey(a)
	b := l.sources[key]
	if b == nil && len(l.sources) < l.opts.MaxSources {
		b = &bucket{tokens: float64(l.opts.BurstPerSource), last: now}
		l.sources[key] = b
	}
	if b != nil && !b.take(now, l.opts.RatePerSource, float64(l.opts.BurstPerSource)) {
		return false
	}
	return l.global.take(now, l.opts.RateGlobal, l.opts.RateGlobal)
}

func (l *limiter) warn(warnf func(string, ...any), a netip.Addr) {
	if warnf == nil {
		return
	}
	now := l.opts.now()
	l.mu.Lock()
	if l.warned && now.Sub(l.lastWarn) < time.Minute {
		l.mu.Unlock()
		return
	}
	l.warned = true
	l.lastWarn = now
	l.mu.Unlock()
	warnf("STUN 限流：来自 %s 的请求超出速率，已丢弃（每分钟最多提示一次）", sourceKey(a))
}
