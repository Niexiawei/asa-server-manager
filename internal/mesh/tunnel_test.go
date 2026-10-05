package mesh

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"asa-server/pkg/meshid"
	"asa-server/pkg/meshjoin"
)

// echoView 是 /echo 的回答：B 侧 handler 看到的请求。
type echoView struct {
	Method     string              `json:"method"`
	Path       string              `json:"path"`
	Query      string              `json:"query"`
	RemoteAddr string              `json:"remote_addr"`
	Host       string              `json:"host"`
	Headers    map[string][]string `json:"headers"`
	Peer       *PeerIdentity       `json:"peer"`
}

// testEngine 是 B 侧的最小 Gin engine。handlerCtxDone 在 /block 的 handler 看到 ctx 结束时收到一个值。
func testEngine(handlerCtxDone chan<- struct{}) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Any("/echo/*rest", func(c *gin.Context) {
		v := echoView{Method: c.Request.Method, Path: c.Request.URL.Path, Query: c.Request.URL.RawQuery,
			RemoteAddr: c.Request.RemoteAddr, Host: c.Request.Host, Headers: c.Request.Header}
		if pi, ok := PeerIdentityFrom(c.Request.Context()); ok {
			v.Peer = &pi
		}
		c.JSON(http.StatusOK, v)
	})
	r.POST("/upload", func(c *gin.Context) {
		h := sha256.New()
		n, err := io.Copy(h, c.Request.Body)
		if err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.String(http.StatusOK, "%d %s", n, hex.EncodeToString(h.Sum(nil)))
	})
	r.GET("/download", func(c *gin.Context) {
		n, _ := strconv.Atoi(c.Query("n"))
		c.Header("Content-Type", "application/octet-stream")
		c.Status(http.StatusOK)
		_, _ = io.Copy(c.Writer, io.LimitReader(detRand{}, int64(n)))
	})
	r.GET("/sse", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		i := 0
		c.Stream(func(w io.Writer) bool {
			fmt.Fprintf(w, "data: %d\n\n", i)
			i++
			if i >= 3 {
				return false
			}
			time.Sleep(300 * time.Millisecond)
			return true
		})
	})
	r.GET("/block", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Status(http.StatusOK)
		fmt.Fprint(c.Writer, "data: hi\n\n")
		c.Writer.Flush()
		<-c.Request.Context().Done()
		if handlerCtxDone != nil {
			handlerCtxDone <- struct{}{}
		}
	})
	r.GET("/unauthorized", func(c *gin.Context) {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "unauthorized"})
	})
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}
	r.GET("/ws", func(c *gin.Context) {
		conn, err := up.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(mt, append([]byte("echo:"), msg...)); err != nil {
				return
			}
		}
	})
	return r
}

// detRand 是确定性的「随机」字节流（下载内容可复算）。
type detRand struct{}

func (detRand) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(i*31 + 7)
	}
	return len(p), nil
}

type pairedPair struct {
	c       *testCoord
	a, b    *Manager
	aid     meshid.ID
	bid     meshid.ID
	blocked chan struct{}
}

// newPaired 建 A、B 并让 B 以 role 授权 A（邀请码）。B 挂上 testEngine。
func newPaired(t *testing.T, role string, opts ...func(*Options)) *pairedPair {
	t.Helper()
	c := newTestCoord(t)
	p := &pairedPair{c: c, blocked: make(chan struct{}, 4)}
	p.a = newManager(t, c, "a", opts...)
	p.b = newManager(t, c, "b", opts...)
	p.b.SetHTTPHandler(testEngine(p.blocked))
	waitConnected(t, p.a, p.b)
	p.aid, p.bid = nodeID(t, p.a), nodeID(t, p.b)
	inv, _, err := p.b.CreateInvite(InviteOptions{Role: role, Note: "A 机"}, false)
	if err != nil {
		t.Fatal(err)
	}
	res := pairEventually(t, p.a, inv)
	if res.Status != "paired" || res.GrantedRole != role {
		t.Fatalf("配对结果不对：%+v", res)
	}
	return p
}

func pairEventually(t *testing.T, a *Manager, inv string) *PairResult {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		res, err := a.PairWithInvite(ctx, inv)
		cancel()
		if err == nil {
			return res
		}
		if status.Code(err) == codes.PermissionDenied || time.Now().After(deadline) {
			t.Fatalf("配对失败：%v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// proxy 是 A 侧的转发入口（meshapi 的最小复刻）：ReverseProxy + 隧道 RoundTripper。
func (p *pairedPair) proxy(t *testing.T, user string) *httptest.Server {
	t.Helper()
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL = &url.URL{Scheme: "http", Host: tunnelHost, Path: pr.In.URL.Path, RawQuery: pr.In.URL.RawQuery}
			pr.Out.Host = tunnelHost
		},
		Transport: p.a.RoundTripper(p.bid, user),
	}
	srv := httptest.NewServer(rp)
	t.Cleanup(srv.Close)
	return srv
}

func TestPairInviteOneShot(t *testing.T) {
	p := newPaired(t, RoleOperator)
	res := helloEventually(t, p.a, p.bid)
	if res.GrantedRole != "ROLE_OPERATOR" {
		t.Fatalf("Hello 应显示授予的角色：%+v", res)
	}
	rec, ok := p.b.Store().Peer(p.aid)
	if !ok || rec.GrantedRole != RoleOperator || rec.Label != "A 机" {
		t.Fatalf("B 侧记录不对：%+v", rec)
	}
	if out, ok := p.a.Store().Peer(p.bid); !ok || out.RemoteRole != RoleOperator {
		t.Fatalf("A 侧应记下对方授予的角色：%+v", out)
	}
	d, _ := p.b.Store().Snapshot()
	if len(d.Invites) != 0 {
		t.Fatalf("邀请码用过就该删掉：%+v", d.Invites)
	}

	// 同一个邀请码给第三台用：失败。
	inv, _, err := p.b.CreateInvite(InviteOptions{Role: RoleAdmin}, false)
	if err != nil {
		t.Fatal(err)
	}
	pairEventually(t, p.a, inv) // A 用掉
	c3 := newManager(t, p.c, "c")
	waitConnected(t, c3)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c3.PairWithInvite(ctx, inv); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("用过的邀请码应被拒，得到 %v", err)
	}
	if role := p.b.Store().Grant(p.aid); role != RoleAdmin {
		t.Fatalf("已配对的身份用新邀请再配一次应改成新角色，得到 %q", role)
	}
}

func TestPairInviteExpiredAndRateLimited(t *testing.T) {
	c := newTestCoord(t)
	a := newManager(t, c, "a")
	b := newManager(t, c, "b")
	waitConnected(t, a, b)
	bid := nodeID(t, b)

	inv, rec, err := b.CreateInvite(InviteOptions{Role: RoleOperator, TTL: time.Millisecond}, false)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if !time.Now().After(rec.ExpiresAt) {
		t.Fatal("邀请应已过期")
	}
	helloEventually(t, a, bid) // 先把连接建好
	ctx := context.Background()
	if _, err := a.PairWithInvite(ctx, inv); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("过期邀请应被拒，得到 %v", err)
	}
	// 再错 4 次（共 5 次）之后，连正确的邀请码也先被限流挡住。
	for range pairFailMax - 1 {
		_, _ = a.PairWithInvite(ctx, inv)
	}
	good, _, err := b.CreateInvite(InviteOptions{Role: RoleOperator}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.PairWithInvite(ctx, good); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("错误次数超限后应 ResourceExhausted，得到 %v", err)
	}
}

func TestPairRequestApprove(t *testing.T) {
	c := newTestCoord(t)
	a := newManager(t, c, "a")
	b := newManager(t, c, "b")
	waitConnected(t, a, b)
	aid, bid := nodeID(t, a), nodeID(t, b)
	helloEventually(t, a, bid)

	res, err := a.RequestPair(context.Background(), bid, nil)
	if err != nil || res.Status != "pending" {
		t.Fatalf("申请应进入待批准：%+v %v", res, err)
	}
	d, _ := b.Store().Snapshot()
	if len(d.Requests) != 1 || d.Requests[0].NodeID != aid || d.Requests[0].Version != "a" {
		t.Fatalf("B 应记下待批准申请：%+v", d.Requests)
	}
	if err := b.Store().ApproveRequest(aid, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if res := helloEventually(t, a, bid); res.GrantedRole != "ROLE_ADMIN" {
		t.Fatalf("批准后 Hello 应显示 admin：%+v", res)
	}
	if res, err := a.RequestPair(context.Background(), bid, nil); err != nil || res.Status != "paired" {
		t.Fatalf("已授权后再申请应直接 paired：%+v %v", res, err)
	}
}

func TestPendingRequestLimit(t *testing.T) {
	s := OpenPeerStore(t.TempDir())
	for i := range MaxPendingRequests {
		if err := s.AddRequest(RequestRecord{NodeID: meshid.ID{byte(i + 1)}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddRequest(RequestRecord{NodeID: meshid.ID{0xFF}}); !errors.Is(err, ErrTooManyPending) {
		t.Fatalf("申请满了应拒绝，得到 %v", err)
	}
	// 同一节点再申请只替换，不占新位置。
	if err := s.AddRequest(RequestRecord{NodeID: meshid.ID{1}, Label: "again"}); err != nil {
		t.Fatal(err)
	}
}

func TestInviteSecretNotStored(t *testing.T) {
	dir := t.TempDir()
	s := OpenPeerStore(dir)
	key, _ := meshid.Generate()
	self, _ := meshid.FromPublicKey(key.Public())
	inv, _, err := s.CreateInvite(self, InviteOptions{Role: RoleOperator})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := meshjoin.ParseInvite(inv)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, PeersFileName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(hex.EncodeToString(parsed.Secret))) ||
		bytes.Contains(raw, []byte(base64.RawURLEncoding.EncodeToString(parsed.Secret))) {
		t.Fatal("peers.json 里不该有邀请密钥原文")
	}
}

func TestTunnelRoundTrip(t *testing.T) {
	p := newPaired(t, RoleAdmin, withListen)
	srv := p.proxy(t, "alice")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/echo/a%2Fb?x=1&y=2", nil)
	req.Header.Set("Cookie", "asa_session=stolen")
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	req.Header.Set("X-Custom", "kept")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var v echoView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || v.Query != "x=1&y=2" || v.Host != tunnelHost {
		t.Fatalf("请求没有原样到达：%d %+v", resp.StatusCode, v)
	}
	for _, h := range []string{"Cookie", "Authorization", "X-Forwarded-For"} {
		if _, ok := v.Headers[h]; ok {
			t.Errorf("B 侧不该看到 %s：%v", h, v.Headers[h])
		}
	}
	if v.Headers["X-Custom"] == nil {
		t.Error("普通请求头应原样转发")
	}
	if v.RemoteAddr != TunnelRemoteAddr || strings.HasPrefix(v.RemoteAddr, "127.") {
		t.Errorf("RemoteAddr 必须是固定的丢弃地址：%s", v.RemoteAddr)
	}
	if v.Peer == nil || v.Peer.Role != RoleAdmin || v.Peer.RemoteUser != "alice" || v.Peer.NodeID != p.aid.String() {
		t.Fatalf("PeerIdentity 不对：%+v", v.Peer)
	}
	if v.Peer.ActorName() != "peer:A 机/alice" {
		t.Errorf("ActorName = %s", v.Peer.ActorName())
	}

	// 8 MiB 上传与下载。
	const n = 8 << 20
	body := make([]byte, n)
	_, _ = rand.Read(body)
	sum := sha256.Sum256(body)
	resp, err = http.Post(srv.URL+"/upload", "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if want := fmt.Sprintf("%d %s", n, hex.EncodeToString(sum[:])); string(got) != want {
		t.Fatalf("上传内容不一致：%s", got)
	}
	resp, err = http.Get(srv.URL + "/download?n=" + strconv.Itoa(n))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	m, _ := io.Copy(h, resp.Body)
	resp.Body.Close()
	wantH := sha256.New()
	_, _ = io.Copy(wantH, io.LimitReader(detRand{}, n))
	if m != n || !bytes.Equal(h.Sum(nil), wantH.Sum(nil)) {
		t.Fatalf("下载内容不一致：%d 字节", m)
	}
}

func TestTunnelSSEStreamsImmediately(t *testing.T) {
	p := newPaired(t, RoleOperator)
	srv := p.proxy(t, "")
	start := time.Now()
	resp, err := http.Get(srv.URL + "/sse")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	line, err := br.ReadString('\n')
	if err != nil || line != "data: 0\n" {
		t.Fatalf("第一条事件不对：%q %v", line, err)
	}
	if d := time.Since(start); d > 450*time.Millisecond {
		t.Fatalf("第一条事件应立即到达，不该攒到最后：%s", d)
	}
	rest, _ := io.ReadAll(br)
	if !strings.Contains(string(rest), "data: 2") {
		t.Fatalf("后续事件缺失：%q", rest)
	}
}

func TestTunnelWebSocket(t *testing.T) {
	p := newPaired(t, RoleOperator)
	srv := p.proxy(t, "")
	ws, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("WebSocket 经隧道升级失败：%v（%v）", err, resp)
	}
	defer ws.Close()
	for i := range 3 {
		msg := fmt.Sprintf("hello-%d", i)
		if err := ws.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
			t.Fatal(err)
		}
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, got, err := ws.ReadMessage()
		if err != nil || string(got) != "echo:"+msg {
			t.Fatalf("回声不对：%q %v", got, err)
		}
	}
}

func TestTunnelClientCancelReachesHandler(t *testing.T) {
	p := newPaired(t, RoleOperator)
	srv := p.proxy(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/block", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = bufio.NewReader(resp.Body).ReadString('\n')
	cancel()
	resp.Body.Close()
	select {
	case <-p.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("A 断开后 B 的 handler 应收到取消")
	}
}

func TestTunnelUnpairedDenied(t *testing.T) {
	c := newTestCoord(t)
	a := newManager(t, c, "a")
	b := newManager(t, c, "b")
	b.SetHTTPHandler(testEngine(nil))
	waitConnected(t, a, b)
	bid := nodeID(t, b)
	helloEventually(t, a, bid)
	req, _ := http.NewRequest(http.MethodGet, "http://"+tunnelHost+"/echo/x", nil)
	_, err := a.RoundTripper(bid, "").RoundTrip(req)
	if !errors.Is(err, ErrPeerNotPaired) {
		t.Fatalf("未授权的身份走隧道应得到 ErrPeerNotPaired，得到 %v", err)
	}
}

// 撤销立即生效：在途的长流被断开，之后的请求被拒。撤销来自另一个 PeerStore 实例（等同于 CLI 进程直接写文件）。
func TestRevokeCutsLiveStreams(t *testing.T) {
	p := newPaired(t, RoleOperator)
	srv := p.proxy(t, "")
	resp, err := http.Get(srv.URL + "/block")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	if _, err := br.ReadString('\n'); err != nil {
		t.Fatal(err)
	}

	cli := OpenPeerStore(p.b.Dir())
	if err := cli.Revoke(p.aid); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(br)
		ended <- err
	}()
	select {
	case <-ended:
	case <-time.After(8 * time.Second):
		t.Fatal("撤销后在途的流应在几秒内结束")
	}
	select {
	case <-p.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("撤销后 B 的 handler 应收到取消")
	}
	req, _ := http.NewRequest(http.MethodGet, "http://"+tunnelHost+"/echo/x", nil)
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, err := p.a.RoundTripper(p.bid, "").RoundTrip(req)
		if errors.Is(err, ErrPeerNotPaired) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("撤销后的请求应被拒，得到 %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if res := helloEventually(t, p.a, p.bid); res.GrantedRole != "ROLE_NONE" {
		t.Fatalf("撤销后 Hello 应显示 ROLE_NONE：%+v", res)
	}
}
