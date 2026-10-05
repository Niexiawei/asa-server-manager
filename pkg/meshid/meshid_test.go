package meshid

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newIdentity(t *testing.T) (tls.Certificate, ID) {
	t.Helper()
	key, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := SelfSignedCert(key, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return cert, FromCert(cert.Leaf)
}

func TestIDRoundTrip(t *testing.T) {
	_, id := newIdentity(t)

	s := id.String()
	if got := strings.Count(s, "-"); got != 12 {
		t.Fatalf("String() 应分 13 组，得到 %q", s)
	}
	for _, in := range []string{
		s,
		strings.ToLower(s),
		id.Compact(),
		strings.ReplaceAll(s, "-", " "),
		" " + strings.ToLower(id.Compact()) + "\n",
	} {
		got, err := ParseID(in)
		if err != nil {
			t.Fatalf("ParseID(%q): %v", in, err)
		}
		if got != id {
			t.Fatalf("ParseID(%q) 往返不一致", in)
		}
	}
	if len(id.Short()) != 8 || !strings.HasPrefix(id.Compact(), id.Short()) {
		t.Fatalf("Short() = %q", id.Short())
	}
}

func TestParseIDRejects(t *testing.T) {
	_, id := newIdentity(t)
	for _, in := range []string{"", "ABC", id.Compact()[:51], id.Compact() + "A", strings.Replace(id.Compact(), id.Compact()[:1], "1", 1)} {
		if _, err := ParseID(in); !errors.Is(err, ErrInvalidID) {
			t.Errorf("ParseID(%q) err = %v，应为 ErrInvalidID", in, err)
		}
	}
}

func TestIDJSON(t *testing.T) {
	_, id := newIdentity(t)
	raw, err := json.Marshal(struct{ ID ID }{id})
	if err != nil {
		t.Fatal(err)
	}
	var back struct{ ID ID }
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.ID != id {
		t.Fatal("JSON 往返不一致")
	}
}

func TestLoadOrCreateStableAcrossResign(t *testing.T) {
	dir := t.TempDir()
	cert1, id1, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 证书被删、被写坏，都只重签证书，ID 不变。
	for _, damage := range []func(){
		func() { os.Remove(filepath.Join(dir, CertFileName)) },
		func() { os.WriteFile(filepath.Join(dir, CertFileName), []byte("garbage"), 0o644) },
	} {
		damage()
		cert2, id2, err := LoadOrCreate(dir)
		if err != nil {
			t.Fatal(err)
		}
		if id2 != id1 {
			t.Fatal("重签证书后 ID 变了")
		}
		if string(cert2.Certificate[0]) == string(cert1.Certificate[0]) {
			t.Fatal("证书应当被重签")
		}
	}
	// 证书完好时原样读回。
	cert3, _, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	cert4, _, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(cert3.Certificate[0]) != string(cert4.Certificate[0]) {
		t.Fatal("完好的证书不该被重签")
	}

	if err := Reset(dir); err != nil {
		t.Fatal(err)
	}
	_, id5, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if id5 == id1 {
		t.Fatal("Reset 之后应生成新身份")
	}
}

func TestLoadOrCreateRefusesUnreadableKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, KeyFileName), []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreate(dir); err == nil {
		t.Fatal("私钥损坏时必须报错，不能悄悄换一把")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, KeyFileName))
	if string(raw) != "not a key" {
		t.Fatal("损坏的私钥文件被覆盖了")
	}
}

func TestKeyFilePermission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 的文件权限靠 ACL，不看 mode")
	}
	dir := t.TempDir()
	if _, _, err := LoadOrCreate(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, KeyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("node.key 权限 = %o，应为 600", perm)
	}
}

// handshake 在回环 TCP 上跑一次 TLS 握手，返回双方各自的错误。
//
// 不用 net.Pipe：它没有缓冲，TLS 1.3 的客户端发完 Finished 就认为握手完成，
// 服务端随后拒绝时写出的告警没人读，会永远阻塞。
func handshake(t *testing.T, client, server *tls.Config) (clientErr, serverErr error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		s, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer s.Close()
		srv := tls.Server(s, server)
		_ = srv.SetDeadline(time.Now().Add(5 * time.Second))
		done <- srv.Handshake()
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cli := tls.Client(c, client)
	_ = cli.SetDeadline(time.Now().Add(5 * time.Second))
	clientErr = cli.Handshake()
	serverErr = <-done
	return
}

func TestPinnedHandshake(t *testing.T) {
	aCert, aID := newIdentity(t)
	bCert, bID := newIdentity(t)
	_, otherID := newIdentity(t)

	if ce, se := handshake(t, ClientConfig(aCert, bID), ServerConfig(bCert, aID)); ce != nil || se != nil {
		t.Fatalf("双方钉对了公钥，握手应成功：client=%v server=%v", ce, se)
	}
	if ce, se := handshake(t, ClientConfig(aCert, bID), AnyClientServerConfig(bCert)); ce != nil || se != nil {
		t.Fatalf("AnyClientServerConfig 应接受任意客户端：client=%v server=%v", ce, se)
	}

	// 客户端钉错了服务端：拨到「同 IP 的别的机器」时就是这种情况。
	ce, _ := handshake(t, ClientConfig(aCert, otherID), AnyClientServerConfig(bCert))
	var pm *PinMismatchError
	if !errors.As(ce, &pm) || pm.Want != otherID || pm.Got != bID {
		t.Fatalf("钉错服务端应得到 PinMismatchError，得到 %v", ce)
	}

	// 服务端只接受 otherID，A 来连被拒。
	_, se := handshake(t, ClientConfig(aCert, bID), ServerConfig(bCert, otherID))
	if !errors.As(se, &pm) || pm.Got != aID {
		t.Fatalf("服务端应拒绝非期望的客户端，得到 %v", se)
	}
}

func TestServerRequiresClientCert(t *testing.T) {
	bCert, bID := newIdentity(t)
	noCert := ClientConfig(tls.Certificate{}, bID)
	noCert.Certificates = nil
	_, se := handshake(t, noCert, AnyClientServerConfig(bCert))
	if se == nil {
		t.Fatal("客户端不出示证书时服务端必须拒绝")
	}
}
