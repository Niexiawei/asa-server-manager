package webapi

import (
	"asa-server/internal/certmgr"
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/logger"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	logger.InitLoggerWithBaseDir(os.TempDir())
	os.Exit(m.Run())
}

// startTLSServer 用与生产完全相同的 Protocols / HTTP2Config / 本地 CA 证书起一个监听器，
// 返回地址与信任该 CA 的根证书池
func startTLSServer(t *testing.T, handler http.Handler) (string, *x509.CertPool) {
	t.Helper()

	original := cfgpkg.BaseDir
	cfgpkg.BaseDir = t.TempDir()
	t.Cleanup(func() { cfgpkg.BaseDir = original })

	tlsConfig, err := certmgr.EnsureTLSConfig(certmgr.Options{Trust: false})
	if err != nil {
		t.Fatalf("准备 TLS 配置失败: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}

	srv := &http.Server{
		Handler:   handler,
		Protocols: protocolsH1H2(),
		HTTP2:     http2Config(),
		TLSConfig: tlsConfig,
	}
	go srv.ServeTLS(ln, "", "")
	t.Cleanup(func() { _ = srv.Close() })

	caPEM, err := os.ReadFile(filepath.Join(certmgr.CertsDir(), "ca.crt"))
	if err != nil {
		t.Fatalf("读取本地 CA 失败: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("本地 CA 无法加入根证书池")
	}

	return "https://" + ln.Addr().String(), pool
}

func newClient(pool *x509.CertPool, protocols *http.Protocols) *http.Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
		Protocols:       protocols,
	}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

func h1Only() *http.Protocols {
	p := new(http.Protocols)
	p.SetHTTP1(true)
	return p
}

// 探针把「协议版本」与「ResponseWriter 是否可劫持」一起报出来，
// 一个请求同时验证两件事
func probeHandler(w http.ResponseWriter, r *http.Request) {
	_, hijackable := w.(http.Hijacker)
	fmt.Fprintf(w, "%s hijackable=%v", r.Proto, hijackable)
}

func get(t *testing.T, client *http.Client, url string) string {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	return string(body)
}

// TestALPNNegotiatesHTTP2 是本次 HTTP/2 改造的核心验收：浏览器只在 TLS 上经 ALPN
// 协商 h2，协商不上就退回 HTTP/1.1 的每源 6 条连接限制，常驻 SSE 会把 REST 饿死。
func TestALPNNegotiatesHTTP2(t *testing.T) {
	url, pool := startTLSServer(t, http.HandlerFunc(probeHandler))

	got := get(t, newClient(pool, protocolsH1H2()), url)
	if want := "HTTP/2.0 hijackable=false"; got != want {
		t.Fatalf("ALPN 未协商到 HTTP/2：got %q, want %q", got, want)
	}
}

// TestHTTP1RemainsAvailableForWebSocket 守住 protocolsH1H2 注释里的那条红线：
// 关掉 HTTP/1.1 会让所有 WebSocket 挂在 "response does not implement http.Hijacker"。
// 反向代理（Caddy/nginx）的 ALPN 也只报 http/1.1，同样依赖这条路径。
func TestHTTP1RemainsAvailableForWebSocket(t *testing.T) {
	url, pool := startTLSServer(t, http.HandlerFunc(probeHandler))

	got := get(t, newClient(pool, h1Only()), url)
	if want := "HTTP/1.1 hijackable=true"; got != want {
		t.Fatalf("HTTP/1.1 升级路径已损坏：got %q, want %q", got, want)
	}
}

func TestParseTrustedProxies(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"默认回环", "127.0.0.1,::1", []string{"127.0.0.1", "::1"}},
		{"容忍空格", " 127.0.0.1 , 10.0.0.5 ", []string{"127.0.0.1", "10.0.0.5"}},
		// nil 让 gin 谁都不信任，ClientIP() 一律取真实 RemoteAddr。
		// 这正是「没配代理」时该有的行为——gin 的默认值是信任所有代理。
		{"空串表示谁都不信", "", nil},
		{"只有分隔符", " , ", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTrustedProxies(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}
