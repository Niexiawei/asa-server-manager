package meshapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"asa-server/internal/appconfig"
	"asa-server/internal/auth"
	"asa-server/internal/mesh"
	"asa-server/internal/meshcoord"
	"asa-server/internal/realtime"
	"asa-server/internal/webapi/authapi"
	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
	"asa-server/pkg/meshjoin"
)

// A、B 两台管理器经真实的隧道互通，A 侧走真实的 /api/peers/:id/fwd 入口（§12 P3-7、P3-10）。

func TestMain(m *testing.M) {
	appconfig.UnsetEnvForTest()
	cleanup := logger.InitTempForTest()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func setupConfig(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, appconfig.ConfigFileName), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASA_CFG", dir)
	if _, err := appconfig.Load(); err != nil {
		t.Fatal(err)
	}
	if appconfig.Get().Auth.Enabled {
		m, err := auth.Initialize(dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { m.Close() })
	}
	return dir
}

func startCoord(t *testing.T) string {
	t.Helper()
	store, err := meshcoord.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	n, _, err := store.EnsureDefaultNetwork()
	if err != nil {
		t.Fatal(err)
	}
	cert, id, err := meshid.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := meshcoord.NewServer(meshcoord.ServerOptions{Store: store})
	gs := meshcoord.NewGRPCServer(cert, srv)
	go gs.Serve(lis)
	t.Cleanup(func() { gs.Stop(); srv.Close() })
	blob, err := meshjoin.JoinBlob{Addr: lis.Addr().String(), Coordinator: id, NetworkID: n.ID, NetworkSecret: n.Secret}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func startManager(t *testing.T, blob string) *mesh.Manager {
	t.Helper()
	dir := t.TempDir()
	if _, err := mesh.Join(dir, blob); err != nil {
		t.Fatal(err)
	}
	noListen := true
	if _, err := mesh.UpdateConfig(dir, mesh.ConfigPatch{NoListen: &noListen}); err != nil {
		t.Fatal(err)
	}
	m := mesh.New(mesh.Options{Dir: dir, BackoffMin: 50 * time.Millisecond})
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop() })
	return m
}

func waitUp(t *testing.T, ms ...*mesh.Manager) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for _, m := range ms {
		for !m.Status().Connected {
			if time.Now().After(deadline) {
				t.Fatal("没有登记上协调节点")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// engineB 是 B 侧：真实的鉴权中间件 + 几个代表性的路由。
func engineB() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(authapi.Middleware())
	authapi.NewHandler().RegisterRouter(r)
	NewHandler().RegisterRouter(r) // B 自己的 /api/mesh、/api/peers：经隧道访问必须是 403
	r.GET("/api/instances", func(c *gin.Context) {
		c.JSON(200, gin.H{"actor": authapi.ActorName(c), "cookie": c.GetHeader("Cookie"), "xff": c.GetHeader("X-Forwarded-For")})
	})
	r.GET("/api/set-cookie", func(c *gin.Context) {
		c.SetCookie("evil", "1", 60, "/", "", false, true)
		c.String(200, "ok")
	})
	r.GET("/api/needs-login", func(c *gin.Context) { c.JSON(401, gin.H{"code": "unauthorized"}) })
	r.DELETE("/api/arkapi/packages/:token", authapi.RequireAdmin(), func(c *gin.Context) { c.Status(404) })
	r.GET("/api/ws/events", func(c *gin.Context) {
		conn, err := realtime.WSUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte("hello from B"))
		_, _, _ = conn.ReadMessage()
	})
	return r
}

type fixture struct {
	a, b *mesh.Manager
	bid  meshid.ID
	srvA *httptest.Server
}

func newFixture(t *testing.T, role string) *fixture {
	t.Helper()
	blob := startCoord(t)
	f := &fixture{a: startManager(t, blob), b: startManager(t, blob)}
	f.b.SetHTTPHandler(engineB())
	waitUp(t, f.a, f.b)
	f.bid = meshid.MustParseID(f.b.Status().NodeID)
	inv, _, err := f.b.CreateInvite(mesh.InviteOptions{Role: role, Note: "A"}, false)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err = f.a.PairWithInvite(ctx, inv)
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Cleanup(mesh.SetGlobalManagerForTest(f.a))
	gin.SetMode(gin.TestMode)
	ea := gin.New()
	ea.Use(authapi.Middleware())
	NewHandler().RegisterRouter(ea)
	f.srvA = httptest.NewServer(ea)
	t.Cleanup(f.srvA.Close)
	return f
}

func (f *fixture) fwd(path string) string {
	return f.srvA.URL + "/api/peers/" + f.bid.String() + "/fwd" + path
}

func doReq(t *testing.T, method, url string, mutate func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	if mutate != nil {
		mutate(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, body
}

func errCode(body []byte) string {
	var v struct{ Code string }
	_ = json.Unmarshal(body, &v)
	return v.Code
}

func TestForwardSecurity(t *testing.T) {
	setupConfig(t, "auth:\n  enabled: false\n")
	f := newFixture(t, mesh.RoleOperator)

	// 普通请求：A 的 Cookie / XFF 不会到 B，B 看到的操作者是 peer:A/-（A 关鉴权时没有用户名）。
	resp, body := doReq(t, "GET", f.fwd("/api/instances"), func(r *http.Request) {
		r.Header.Set("Cookie", "asa_session=secret")
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
	})
	var seen struct{ Actor, Cookie, XFF string }
	_ = json.Unmarshal(body, &seen)
	if resp.StatusCode != 200 || seen.Cookie != "" || seen.XFF != "" || seen.Actor != "peer:A/-" {
		t.Fatalf("转发结果不对：%d %s", resp.StatusCode, body)
	}

	// B 关着鉴权 + 授予 operator：管理员接口 403。
	if resp, body := doReq(t, "DELETE", f.fwd("/api/arkapi/packages/none"), nil); resp.StatusCode != 403 || errCode(body) != "forbidden" {
		t.Fatalf("operator 调管理员接口应 403：%d %s", resp.StatusCode, body)
	}
	// 远程禁区与禁止多跳。
	for _, p := range []string{"/api/users", "/api/auth/audit", "/api/mesh/status", "/api/peers/" + f.bid.String() + "/fwd/api/instances"} {
		if resp, body := doReq(t, "GET", f.fwd(p), nil); resp.StatusCode != 403 || errCode(body) != "peer_forbidden" {
			t.Errorf("%s 经隧道应 403 peer_forbidden：%d %s", p, resp.StatusCode, body)
		}
	}
	// B 的 Set-Cookie 不透传；B 的 401 被改成 403，A 的前端不会因此登出本机。
	if resp, _ := doReq(t, "GET", f.fwd("/api/set-cookie"), nil); resp.Header.Get("Set-Cookie") != "" {
		t.Fatalf("Set-Cookie 不该透传：%v", resp.Header)
	}
	if resp, body := doReq(t, "GET", f.fwd("/api/needs-login"), nil); resp.StatusCode != 403 || errCode(body) != "peer_unauthorized" {
		t.Fatalf("B 的 401 应改写为 403 peer_unauthorized：%d %s", resp.StatusCode, body)
	}

	// WebSocket：同源经 A 升级成功；跨源在 A 侧就被拒。
	wsURL := "ws" + strings.TrimPrefix(f.fwd("/api/ws/events"), "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": {f.srvA.URL}})
	if err != nil {
		t.Fatalf("同源 WebSocket 应能经 A 升级：%v", err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, msg, err := ws.ReadMessage(); err != nil || string(msg) != "hello from B" {
		t.Fatalf("WebSocket 消息不对：%q %v", msg, err)
	}
	ws.Close()
	_, resp, err = websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": {"https://evil.example"}})
	if err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatalf("跨源 WebSocket 应在 A 侧被拒：%v %v", err, resp)
	}

	// 撤销之后：403 peer_not_paired。
	if err := f.b.Store().Revoke(meshid.MustParseID(f.a.Status().NodeID)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, body := doReq(t, "GET", f.fwd("/api/instances"), nil)
		if resp.StatusCode == 403 && errCode(body) == "peer_not_paired" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("撤销后应 403 peer_not_paired：%d %s", resp.StatusCode, body)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestForwardAdminGrant(t *testing.T) {
	setupConfig(t, "auth:\n  enabled: false\n")
	f := newFixture(t, mesh.RoleAdmin)
	if resp, body := doReq(t, "DELETE", f.fwd("/api/arkapi/packages/none"), nil); resp.StatusCode != 404 {
		t.Fatalf("admin 授权应能调管理员接口（这里的 404 来自 B 的 handler）：%d %s", resp.StatusCode, body)
	}
	// 未知节点：连不上 ⇒ 502 peer_unreachable（不是 401，不会把本机登出）。
	other := strings.Replace(f.fwd("/api/instances"), f.bid.String(), meshid.ID{1, 2, 3}.String(), 1)
	if resp, body := doReq(t, "GET", other, nil); resp.StatusCode != 502 || errCode(body) != "peer_unreachable" {
		t.Fatalf("连不上的对端应 502 peer_unreachable：%d %s", resp.StatusCode, body)
	}
}

// A 开着鉴权：默认只有管理员能用远程控制（D5），control_role=operator 后操作员也能用。
func TestControlRoleGate(t *testing.T) {
	setupConfig(t, "auth:\n  enabled: true\n")
	am := auth.GetManager()
	ctx := context.Background()
	op, err := am.CreateUser(ctx, "oper", "correct-horse-battery", auth.RoleOperator, auth.ActorCLI)
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := am.IssueSession(op, auth.StageFull, []string{"pwd"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := mesh.SetEnabled(dir, true); err != nil {
		t.Fatal(err)
	}
	m := mesh.New(mesh.Options{Dir: dir})
	t.Cleanup(mesh.SetGlobalManagerForTest(m))

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(authapi.Middleware())
	NewHandler().RegisterRouter(r)
	cookie := &http.Cookie{Name: appconfig.Get().Auth.Session.CookieName, Value: tok}
	// 用真实的 HTTP 服务而不是 ResponseRecorder：ReverseProxy 要 CloseNotify，Recorder 没有。
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	call := func(method, path string) int {
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(nil))
		req.AddCookie(cookie)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	fwd := "/api/peers/" + meshid.ID{9}.String() + "/fwd/api/instances"
	if code := call("GET", fwd); code != 403 {
		t.Fatalf("默认只有管理员能用远程控制，operator 应 403，实际 %d", code)
	}
	if code := call("GET", "/api/mesh/peers"); code != 403 {
		t.Fatalf("operator 默认看不到对端列表，实际 %d", code)
	}
	role := mesh.RoleOperator
	if _, err := mesh.UpdateConfig(dir, mesh.ConfigPatch{ControlRole: &role}); err != nil {
		t.Fatal(err)
	}
	if code := call("GET", fwd); code == 403 || code == 401 {
		t.Fatalf("control_role=operator 后 operator 应能通过 A 的闸门（mesh 没在运行，期望 503），实际 %d", code)
	}
	if code := call("GET", "/api/mesh/peers"); code != 200 {
		t.Fatalf("control_role=operator 后 operator 应能看对端列表，实际 %d", code)
	}
	if code := call("PUT", "/api/mesh/config"); code != 403 {
		t.Fatalf("改配置仍然只能管理员，实际 %d", code)
	}
}

func TestForwardPath(t *testing.T) {
	for in, want := range map[string]string{
		"/api/peers/X/fwd/api/instances":  "/api/instances",
		"/api/peers/X/fwd/api/a%2Fb/c":    "/api/a%2Fb/c",
		"/api/peers/X/fwd/":               "/",
		"/api/peers/X/fwd":                "/",
		"/api/peers/X/fwd/api/x?y=1#frag": "/api/x?y=1#frag",
	} {
		if got := forwardPath(in); got != want {
			t.Errorf("forwardPath(%q) = %q，应为 %q", in, got, want)
		}
	}
}
