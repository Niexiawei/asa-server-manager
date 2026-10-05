package stun

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"
)

// RFC 5769 的官方测试向量。
func vec(s string) []byte {
	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		panic(err)
	}
	return b
}

var (
	rfc5769Request = vec(`
		00 01 00 58 21 12 a4 42 b7 e7 a7 01 bc 34 d6 86 fa 87 df ae
		80 22 00 10 53 54 55 4e 20 74 65 73 74 20 63 6c 69 65 6e 74
		00 24 00 04 6e 00 01 ff
		80 29 00 08 93 2f f9 b1 51 26 3b 36
		00 06 00 09 65 76 74 6a 3a 68 36 76 59 20 20 20
		00 08 00 14 9a ea a7 0c bf d8 cb 56 78 1e f2 b5 b2 d3 f2 49 c1 b5 71 a2
		80 28 00 04 e5 7a 3b cf`)
	rfc5769IPv4Response = vec(`
		01 01 00 3c 21 12 a4 42 b7 e7 a7 01 bc 34 d6 86 fa 87 df ae
		80 22 00 0b 74 65 73 74 20 76 65 63 74 6f 72 20
		00 20 00 08 00 01 a1 47 e1 12 a6 43
		00 08 00 14 2b 91 f5 99 fd 9e 90 c3 8c 74 89 f9 2a f9 ba 53 f0 6b e7 d7
		80 28 00 04 c0 7d 4c 96`)
	rfc5769IPv6Response = vec(`
		01 01 00 48 21 12 a4 42 b7 e7 a7 01 bc 34 d6 86 fa 87 df ae
		80 22 00 0b 74 65 73 74 20 76 65 63 74 6f 72 20
		00 20 00 14 00 02 a1 47 01 13 a9 fa a5 d3 f1 79 bc 25 f4 b5 be d2 b9 d9
		00 08 00 14 a3 82 95 4e 4b e6 7b f1 17 84 c9 7c 82 92 c2 75 bf e3 ed 41
		80 28 00 04 c8 fb 0b 4c`)
	rfc5769TxID = TxID(vec(`b7 e7 a7 01 bc 34 d6 86 fa 87 df ae`))
)

func TestRFC5769Request(t *testing.T) {
	m, err := Parse(rfc5769Request)
	if err != nil {
		t.Fatal(err)
	}
	if !m.HasFingerprint || m.Class() != ClassRequest || m.Method() != MethodBinding || m.TxID != rfc5769TxID {
		t.Fatalf("解析结果不对：%+v", m)
	}
	if v, _ := m.Get(AttrSoftware); string(v) != "STUN test client" {
		t.Fatalf("SOFTWARE = %q", v)
	}
	if v, _ := m.Get(AttrUsername); string(v) != "evtj:h6vY" {
		t.Fatalf("USERNAME = %q（填充不该算进值里）", v)
	}

	// 交给服务端：PRIORITY / USERNAME / MESSAGE-INTEGRITY 都在「必须理解」区间，本服务一个都不认识。
	s := &server{opts: (&ServerOptions{}).withDefaults()}
	s.limiter = newLimiter(s.opts)
	resp := s.handle(rfc5769Request, &net.UDPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 32853})
	_, err = ParseBindingResponse(resp, rfc5769TxID)
	var er *ErrorResponse
	if !errors.As(err, &er) || er.Code != 420 {
		t.Fatalf("应得到 420，得到 %v", err)
	}
	want := []uint16{AttrPriority, AttrUsername, AttrMessageIntegrity}
	if !slices.Equal(er.Unknown, want) {
		t.Fatalf("UNKNOWN-ATTRIBUTES = %04X，应为 %04X", er.Unknown, want)
	}
}

func TestRFC5769Responses(t *testing.T) {
	for _, c := range []struct {
		name string
		pkt  []byte
		want string
	}{
		{"IPv4", rfc5769IPv4Response, "192.0.2.1:32853"},
		{"IPv6", rfc5769IPv6Response, "[2001:db8:1234:5678:11:2233:4455:6677]:32853"},
	} {
		got, err := ParseBindingResponse(c.pkt, rfc5769TxID)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != netip.MustParseAddrPort(c.want) {
			t.Fatalf("%s: 得到 %s，应为 %s", c.name, got, c.want)
		}
	}
	if _, err := ParseBindingResponse(rfc5769IPv4Response, NewTxID()); !errors.Is(err, ErrTxMismatch) {
		t.Fatalf("事务 ID 不同应报 ErrTxMismatch，得到 %v", err)
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	for _, ap := range []string{"192.0.2.1:32853", "[2001:db8::1]:3478", "[::ffff:10.0.0.1]:9"} {
		addr := netip.MustParseAddrPort(ap)
		tx := NewTxID()
		pkt := (&Message{Type: TypeBindingSuccess, TxID: tx, Attrs: []Attr{
			{Type: AttrXORMappedAddress, Value: EncodeXORMappedAddress(addr, tx)},
		}}).Encode(true)
		m, err := Parse(pkt)
		if err != nil || !m.HasFingerprint {
			t.Fatalf("%s: 自己编的报文解析失败：%v", ap, err)
		}
		got, err := ParseBindingResponse(pkt, tx)
		if err != nil {
			t.Fatal(err)
		}
		want := netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
		if got != want {
			t.Fatalf("得到 %s，应为 %s", got, want)
		}
		if strings.HasPrefix(ap, "[::ffff:") && !got.Addr().Is4() {
			t.Fatal("IPv4-mapped 地址应编码成 IPv4 族")
		}
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	_, req := NewBindingRequest()
	good := append([]byte(nil), rfc5769IPv4Response...)
	badFP := append([]byte(nil), good...)
	badFP[len(badFP)-1] ^= 0xFF
	badCookie := append([]byte(nil), req...)
	badCookie[4] ^= 0xFF
	badLen := append([]byte(nil), req...)
	badLen[3] = 4
	topBits := append([]byte(nil), req...)
	topBits[0] |= 0x80
	for name, pkt := range map[string][]byte{
		"空":             nil,
		"截断":            req[:19],
		"长度不符":          badLen,
		"cookie 错":      badCookie,
		"首 2 位非 0":      topBits,
		"FINGERPRINT 错": badFP,
		"属性越界":          append(append([]byte(nil), vec("00 01 00 04 21 12 a4 42 00 00 00 00 00 00 00 00 00 00 00 00")...), vec("00 01 00 08")...),
	} {
		if _, err := Parse(pkt); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: err = %v，应为 ErrMalformed", name, err)
		}
	}
}

// startServer 在回环上起一个 STUN 服务，返回地址与计数器。
func startServer(t *testing.T, network, addr string, opts ServerOptions) (net.Addr, *Stats) {
	t.Helper()
	pc, err := net.ListenPacket(network, addr)
	if err != nil {
		t.Skipf("无法监听 %s %s：%v", network, addr, err)
	}
	st := &Stats{}
	opts.Stats = st
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, pc, opts) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("Serve 返回 %v，应为 context.Canceled", err)
		}
		pc.Close()
	})
	return pc.LocalAddr(), st
}

func TestServeLoopback(t *testing.T) {
	for _, c := range []struct{ network, listen, dial string }{
		{"udp4", "127.0.0.1:0", "127.0.0.1:0"},
		{"udp6", "[::1]:0", "[::1]:0"},
	} {
		srvAddr, st := startServer(t, c.network, c.listen, ServerOptions{})
		cli, err := net.ListenPacket(c.network, c.dial)
		if err != nil {
			t.Skipf("%s: %v", c.network, err)
		}
		defer cli.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		got, err := Query(ctx, cli, srvAddr)
		cancel()
		if err != nil {
			t.Fatalf("%s: %v", c.network, err)
		}
		want := cli.LocalAddr().(*net.UDPAddr).AddrPort()
		if got != netip.AddrPortFrom(want.Addr().Unmap(), want.Port()) {
			t.Fatalf("%s: 反射地址 %s，应为本机地址 %s", c.network, got, want)
		}
		if st.Success.Load() != 1 {
			t.Fatalf("%s: Success = %d", c.network, st.Success.Load())
		}
	}
}

// 双栈 socket（":0"）收到的 IPv4 来源是 ::ffff:127.0.0.1，回给客户端的必须是 IPv4 族。
func TestDualStackAnswersIPv4Family(t *testing.T) {
	srvAddr, _ := startServer(t, "udp", ":0", ServerOptions{})
	port := srvAddr.(*net.UDPAddr).Port
	cli, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := Query(ctx, cli, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Addr().Is4() || got.Addr().String() != "127.0.0.1" {
		t.Fatalf("得到 %s，应为 IPv4 的 127.0.0.1", got)
	}
}

// 不合法的包、非 request、TURN 方法：一律不回。
func TestServeSilentlyDrops(t *testing.T) {
	srvAddr, st := startServer(t, "udp4", "127.0.0.1:0", ServerOptions{})
	cli, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	tx := NewTxID()
	allocate := (&Message{Type: 0x0003, TxID: tx}).Encode(true) // TURN Allocate request
	indication := (&Message{Type: 0x0011, TxID: tx}).Encode(true)
	_, req := NewBindingRequest()
	truncated := req[:len(req)-3]
	for _, pkt := range [][]byte{[]byte("hello"), truncated, rfc5769IPv4Response, allocate, indication} {
		if _, err := cli.WriteTo(pkt, srvAddr); err != nil {
			t.Fatal(err)
		}
	}
	cli.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, _, err := cli.ReadFrom(make([]byte, 1500)); err == nil {
		t.Fatalf("不该收到任何回包，收到 %d 字节", n)
	}
	if got := st.DropMalformed.Load(); got != 5 {
		t.Fatalf("DropMalformed = %d，应为 5", got)
	}
}

func TestChangeRequestGets420(t *testing.T) {
	srvAddr, _ := startServer(t, "udp4", "127.0.0.1:0", ServerOptions{})
	cli, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	tx := NewTxID()
	req := (&Message{Type: TypeBindingRequest, TxID: tx, Attrs: []Attr{
		{Type: AttrChangeRequest, Value: []byte{0, 0, 0, 6}},
		{Type: AttrSoftware, Value: []byte("probe")}, // 可选区间，不该被列进 UNKNOWN-ATTRIBUTES
	}}).Encode(false)
	cli.WriteTo(req, srvAddr)
	cli.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, _, err := cli.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ParseBindingResponse(buf[:n], tx)
	var er *ErrorResponse
	if !errors.As(err, &er) || er.Code != 420 || !slices.Equal(er.Unknown, []uint16{AttrChangeRequest}) {
		t.Fatalf("应得到 420 + [CHANGE-REQUEST]，得到 %v", err)
	}
}

func TestRateLimit(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	o := (&ServerOptions{
		RatePerSource: 1, BurstPerSource: 3, RateGlobal: 1000, MaxSources: 2,
		now: func() time.Time { return now },
	}).withDefaults()
	l := newLimiter(o)

	a := netip.MustParseAddr("2001:db8:1:1::1")
	sameSlash64 := netip.MustParseAddr("2001:db8:1:1::ffff")
	other64 := netip.MustParseAddr("2001:db8:1:2::1")

	for i := 0; i < 3; i++ {
		if !l.allow(a) {
			t.Fatalf("突发内第 %d 次应放行", i+1)
		}
	}
	if l.allow(a) {
		t.Fatal("超出突发应丢弃")
	}
	if l.allow(sameSlash64) {
		t.Fatal("同一 /64 内换地址应共享桶")
	}
	if !l.allow(other64) {
		t.Fatal("/64 之外的来源不受影响")
	}
	// 来源表已满（MaxSources=2）：新来源只受全局桶约束，不会被单独限流。
	full := netip.MustParseAddr("198.51.100.7")
	for i := 0; i < 10; i++ {
		if !l.allow(full) {
			t.Fatal("来源表满后新来源应只走全局桶")
		}
	}
	if len(l.sources) != 2 {
		t.Fatalf("来源表应保持 2 条，实际 %d", len(l.sources))
	}
	// 一秒后补回一个令牌。
	now = now.Add(time.Second)
	if !l.allow(a) {
		t.Fatal("令牌应按速率补回")
	}
	// 闲置超过 SourceIdle 后被淘汰。
	now = now.Add(2 * time.Minute)
	l.allow(full)
	if _, ok := l.sources[sourceKey(a)]; ok {
		t.Fatal("闲置来源应被淘汰")
	}
}

func TestGlobalRateLimit(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := newLimiter((&ServerOptions{RateGlobal: 5, now: func() time.Time { return now }}).withDefaults())
	allowed := 0
	for i := 0; i < 20; i++ {
		if l.allow(netip.AddrFrom4([4]byte{10, 0, byte(i), 1})) {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("全局桶应只放行 5 个，放行了 %d", allowed)
	}
}

func TestRateLimitWarnThrottled(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := newLimiter((&ServerOptions{now: func() time.Time { return now }}).withDefaults())
	var warns int
	warnf := func(string, ...any) { warns++ }
	a := netip.MustParseAddr("203.0.113.9")
	l.warn(warnf, a)
	l.warn(warnf, a)
	now = now.Add(30 * time.Second)
	l.warn(warnf, a)
	if warns != 1 {
		t.Fatalf("一分钟内只应提示一次，提示了 %d 次", warns)
	}
	now = now.Add(31 * time.Second)
	l.warn(warnf, a)
	if warns != 2 {
		t.Fatalf("一分钟后应再提示，共 %d 次", warns)
	}
}

func TestClassifyMapping(t *testing.T) {
	p := netip.MustParseAddrPort
	local := []netip.AddrPort{p("192.168.1.10:5000"), p("[2001:db8::10]:5000")}
	cases := []struct {
		a, b netip.AddrPort
		want Mapping
	}{
		{p("192.168.1.10:5000"), p("192.168.1.10:5000"), NoNAT},
		{p("[2001:db8::10]:5000"), netip.AddrPort{}, NoNAT},
		{p("203.0.113.1:40000"), p("203.0.113.1:40000"), EndpointIndependent},
		{p("203.0.113.1:40000"), p("203.0.113.1:40001"), EndpointDependent},
		{p("203.0.113.1:40000"), netip.AddrPort{}, MappingUnknown},
		{netip.AddrPort{}, p("203.0.113.1:40000"), MappingUnknown},
	}
	for _, c := range cases {
		if got := ClassifyMapping(local, c.a, c.b); got != c.want {
			t.Errorf("ClassifyMapping(%s, %s) = %v，应为 %v", c.a, c.b, got, c.want)
		}
	}
}

func FuzzParse(f *testing.F) {
	_, req := NewBindingRequest()
	for _, seed := range [][]byte{rfc5769Request, rfc5769IPv4Response, rfc5769IPv6Response, req} {
		f.Add(seed)
	}
	s := &server{opts: (&ServerOptions{RateGlobal: 1e9, RatePerSource: 1e9, BurstPerSource: 1e9}).withDefaults()}
	s.limiter = newLimiter(s.opts)
	from := &net.UDPAddr{IP: net.IPv4(198, 51, 100, 1), Port: 1}
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := Parse(b)
		if err == nil {
			for _, a := range m.Attrs {
				_, _ = DecodeXORMappedAddress(a.Value, m.TxID)
				_, _, _ = DecodeErrorCode(a.Value)
			}
		}
		if resp := s.handle(b, from); resp != nil {
			if _, err := Parse(resp); err != nil {
				t.Fatalf("服务端回了一个自己都解析不了的包：%v", err)
			}
		}
	})
}
