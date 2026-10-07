package mesh

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/meshid"
	"asa-server/pkg/stun"
)

func TestProbeEncodeVerify(t *testing.T) {
	key := make([]byte, punchKeyLen)
	key[0] = 1
	sid := [punchSIDLen]byte{9, 8, 7}
	b := encodeProbe(key, sid, probeTypeProbe, 42)
	if len(b) != probeLen || !isProbe(b) || b[0]&0xC0 != 0 {
		t.Fatalf("探测包格式不对：% x", b)
	}
	if typ, ok := verifyProbe(key, sid, b); !ok || typ != probeTypeProbe {
		t.Fatal("合法的探测包应通过校验")
	}
	if typ, ok := verifyProbe(key, sid, encodeProbe(key, sid, probeTypeAck, 0)); !ok || typ != probeTypeAck {
		t.Fatal("合法的 ack 应通过校验")
	}

	wrongKey := append([]byte(nil), key...)
	wrongKey[0] = 2
	tampered := append([]byte(nil), b...)
	tampered[20] ^= 1
	badType := encodeProbe(key, sid, 7, 0)
	badMagic := append([]byte(nil), b...)
	badMagic[0] = 0x2B
	for name, c := range map[string]struct {
		key []byte
		sid [punchSIDLen]byte
		b   []byte
	}{
		"错 key":   {wrongKey, sid, b},
		"错会话 ID":  {key, [punchSIDLen]byte{1}, b},
		"被篡改":     {key, sid, tampered},
		"截断":      {key, sid, b[:probeLen-1]},
		"未知类型":    {key, sid, badType},
		"首字节不对":   {key, sid, badMagic},
		"空包":      {key, sid, nil},
	} {
		if _, ok := verifyProbe(c.key, c.sid, c.b); ok {
			t.Errorf("%s：应拒绝", name)
		}
	}
}

func TestOrderPunchCandidates(t *testing.T) {
	ap := netip.MustParseAddrPort
	host := []netip.AddrPort{
		ap("192.168.1.5:19194"), ap("10.0.0.2:19194"),
		ap("[2408:1234::5]:19194"), ap("[fd00::5]:19194"),
	}
	srflx := []netip.AddrPort{ap("203.0.113.9:40000"), ap("203.0.113.9:40000")}
	mapped := []netip.AddrPort{ap("198.51.100.4:19194")}
	got := orderPunchCandidates(host, srflx, mapped)
	want := []string{"198.51.100.4:19194", "203.0.113.9:40000", "[2408:1234::5]:19194", "192.168.1.5:19194", "10.0.0.2:19194", "[fd00::5]:19194"}
	if len(got) != len(want) {
		t.Fatalf("候选个数不对：%v", got)
	}
	for i, c := range got {
		if c.GetAddr() != want[i] || c.GetTransport() != meshpb.Transport_TRANSPORT_UDP {
			t.Fatalf("第 %d 个应是 %s（UDP），得到 %v", i, want[i], got)
		}
	}
	if got[0].GetKind() != meshpb.CandidateKind_CANDIDATE_KIND_MAPPED || got[1].GetKind() != meshpb.CandidateKind_CANDIDATE_KIND_SRFLX ||
		got[2].GetKind() != meshpb.CandidateKind_CANDIDATE_KIND_HOST {
		t.Fatalf("种类不对：%v", got)
	}

	var many []netip.AddrPort
	for i := range 20 {
		many = append(many, netip.AddrPortFrom(netip.AddrFrom4([4]byte{10, 0, 0, byte(i + 1)}), 1))
	}
	if n := len(orderPunchCandidates(many, nil, nil)); n != punchMaxCands {
		t.Fatalf("应截断到 %d 个，得到 %d", punchMaxCands, n)
	}
}

func TestParsePunchTargets(t *testing.T) {
	udp := func(a string) *meshpb.Candidate {
		return &meshpb.Candidate{Transport: meshpb.Transport_TRANSPORT_UDP, Addr: a}
	}
	in := []*meshpb.Candidate{
		{Transport: meshpb.Transport_TRANSPORT_TCP, Addr: "1.2.3.4:1"},
		udp("not-an-addr"), udp("0.0.0.0:5"), udp("1.2.3.4:0"), udp("224.0.0.1:5"),
		udp("1.2.3.4:5"), udp("[::ffff:1.2.3.4]:5"), udp("[2001:db8::1]:6"),
	}
	got := parsePunchTargets(in)
	if len(got) != 2 || got[0].String() != "1.2.3.4:5" || got[1].String() != "[2001:db8::1]:6" {
		t.Fatalf("只该留下合法的 UDP 单播地址（去重、IPv4 映射还原）：%v", got)
	}
	var many []*meshpb.Candidate
	for i := range 20 {
		many = append(many, udp(netip.AddrPortFrom(netip.AddrFrom4([4]byte{10, 0, 0, byte(i + 1)}), 1).String()))
	}
	if n := len(parsePunchTargets(many)); n != punchMaxCands {
		t.Fatalf("对方的候选应截断到 %d 个，得到 %d", punchMaxCands, n)
	}
}

func openTestMux(t *testing.T) *udpMux {
	t.Helper()
	u, bindErr, err := openUDP("127.0.0.1:0", nil)
	if err != nil || bindErr != nil {
		t.Fatal(err, bindErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go u.run(ctx)
	t.Cleanup(func() { cancel(); u.close() })
	return u
}

func muxAddr(u *udpMux) netip.AddrPort {
	return netip.MustParseAddrPort(u.localAddr())
}

// 分发器：探测按会话 ID 送达，未知会话与垃圾丢弃，STUN 回包按事务 ID 回到查询；等待者不读时分发器不阻塞。
func TestUDPMuxDispatch(t *testing.T) {
	u := openTestMux(t)
	key := make([]byte, punchKeyLen)
	sid := [punchSIDLen]byte{1}
	s := newPunchSession(u, sid, key, meshid.ID{})
	u.addSession(s)

	other, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	to := net.UDPAddrFromAddrPort(muxAddr(u))
	other.WriteTo([]byte("garbage"), to)
	other.WriteTo(encodeProbe(key, [punchSIDLen]byte{2}, probeTypeProbe, 0), to) // 未知会话
	other.WriteTo(encodeProbe(key, sid, probeTypeProbe, 1), to)
	select {
	case p := <-s.in:
		if p.from.String() != other.LocalAddr().String() {
			t.Fatalf("来源地址不对：%v", p.from)
		}
		if _, ok := verifyProbe(key, sid, p.b); !ok {
			t.Fatal("送达的应是那个合法探测")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("探测没有送达会话")
	}
	select {
	case p := <-s.in:
		t.Fatalf("不该送达别的包：% x", p.b)
	case <-time.After(200 * time.Millisecond):
	}

	// 没人读的会话：投递不阻塞。
	done := make(chan struct{})
	go func() {
		for range 500 {
			u.dispatch(encodeProbe(key, sid, probeTypeProbe, 0), other.LocalAddr())
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("等待者不读时分发器被阻塞")
	}

	// STUN：经本 socket 问到的反射地址就是它自己。
	srv := startSTUN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := u.stunQuery(ctx, netip.MustParseAddrPort(srv[0]))
	if err != nil || got != muxAddr(u) {
		t.Fatalf("STUN 查询结果不对：%v %v（期望 %v）", got, err, muxAddr(u))
	}
	if m := stun.ClassifyMapping([]netip.AddrPort{muxAddr(u)}, got, got); m != stun.NoNAT {
		t.Fatalf("回环上应判为无 NAT：%v", m)
	}
}

// 会话：收到有效探测回 ack 给来源地址，并把来源记为已验证。
func TestPunchSessionAcks(t *testing.T) {
	u := openTestMux(t)
	key := make([]byte, punchKeyLen)
	sid := [punchSIDLen]byte{3}
	s := newPunchSession(u, sid, key, meshid.ID{})
	u.addSession(s)
	defer s.close()
	go s.run(time.Second)

	other, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	other.WriteTo(encodeProbe(key, sid, probeTypeProbe, 0), net.UDPAddrFromAddrPort(muxAddr(u)))
	select {
	case ap := <-s.verified:
		if ap.String() != other.LocalAddr().String() {
			t.Fatalf("已验证地址不对：%v", ap)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("有效探测没有让来源地址进入已验证")
	}
	other.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 100)
	n, _, err := other.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if typ, ok := verifyProbe(key, sid, buf[:n]); !ok || typ != probeTypeAck {
		t.Fatalf("应回一个 ack：% x", buf[:n])
	}
}
