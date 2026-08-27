package authapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"asa-server/internal/appconfig"
	"asa-server/internal/auth"

	"github.com/gin-gonic/gin"
)

// setupEnv 写一份配置并初始化鉴权，返回 baseDir
func setupEnv(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, appconfig.ConfigFileName), []byte(yaml), 0o644); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	t.Setenv("ASA_CFG", dir)
	if _, err := appconfig.Load(); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if appconfig.Get().Auth.Enabled {
		m, err := auth.Initialize(dir)
		if err != nil {
			t.Fatalf("初始化鉴权失败: %v", err)
		}
		t.Cleanup(func() { m.Close() })
	}
	return dir
}

func newTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware())

	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.GET("/index.html", func(c *gin.Context) { c.String(200, "<html>") })
	r.GET("/api/instances", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	// 模拟一个 SSE 端点：正常情况下才会写出 event-stream 头
	r.GET("/api/logs/stream", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.String(200, "data: hello\n\n")
	})
	// 模拟 WebSocket 端点：正常情况下才会返回 101
	r.GET("/api/ws/events", func(c *gin.Context) { c.Status(http.StatusSwitchingProtocols) })
	return r
}

func do(r *gin.Engine, method, path string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "203.0.113.9:12345" // 默认当作公网来源
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// 鉴权关闭时行为必须和引入鉴权之前完全一致，而且**根本不打开** auth.db
func TestAuthDisabledPassesEverything(t *testing.T) {
	dir := setupEnv(t, "auth:\n  enabled: false\n")
	r := newTestRouter()

	for _, path := range []string{"/api/instances", "/health", "/index.html", "/api/logs/stream"} {
		if got := do(r, "GET", path, nil).Code; got != 200 {
			t.Errorf("%s 应放行，实际 %d", path, got)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "database_file", "auth.db")); !os.IsNotExist(err) {
		t.Error("鉴权关闭时不应创建 auth.db")
	}
}

func TestExemptAndStaticPaths(t *testing.T) {
	setupEnv(t, authEnabledYAML)
	seedAdmin(t)
	r := newTestRouter()

	// 静态资源必须放行，否则未登录时连登录页都打不开
	if got := do(r, "GET", "/index.html", nil).Code; got != 200 {
		t.Errorf("静态资源应放行，实际 %d", got)
	}
	if got := do(r, "GET", "/health", nil).Code; got != 200 {
		t.Errorf("/health 应放行，实际 %d", got)
	}
	// 业务接口必须拦下
	if got := do(r, "GET", "/api/instances", nil).Code; got != 401 {
		t.Errorf("未登录访问业务接口应返回 401，实际 %d", got)
	}
}

func TestSetupRequiredWhenNoUsers(t *testing.T) {
	setupEnv(t, authEnabledYAML)
	r := newTestRouter()

	w := do(r, "GET", "/api/instances", nil)
	if w.Code != 401 {
		t.Fatalf("零用户状态应返回 401，实际 %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), codeSetupReq) {
		t.Errorf("响应应带 setup_required 标识供前端跳转，实际 %s", w.Body.String())
	}
}

// SSE 鉴权失败绝不能写出 200 + text/event-stream：
// 那样浏览器的 EventSource 会每 3 秒无限重连，而且 JS 关不掉它。
func TestSSERejectionIsFatalToBrowser(t *testing.T) {
	setupEnv(t, authEnabledYAML)
	seedAdmin(t)
	r := newTestRouter()

	w := do(r, "GET", "/api/logs/stream", func(req *http.Request) {
		req.Header.Set("Accept", "text/event-stream")
	})
	if w.Code != 401 {
		t.Errorf("未登录的 SSE 请求应返回 401，实际 %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); strings.Contains(ct, "text/event-stream") {
		t.Errorf("拒绝响应的 Content-Type 不得是 text/event-stream（会触发浏览器无限重连），实际 %q", ct)
	}
	if strings.Contains(w.Body.String(), "data:") {
		t.Error("不应写出任何 SSE 数据体")
	}
}

// WebSocket 必须在 Upgrade 之前拒绝，不能"先升级再关闭"
func TestWebSocketRejectedBeforeUpgrade(t *testing.T) {
	setupEnv(t, authEnabledYAML)
	seedAdmin(t)
	r := newTestRouter()

	w := do(r, "GET", "/api/ws/events", func(req *http.Request) {
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "Upgrade")
	})
	if w.Code == http.StatusSwitchingProtocols {
		t.Fatal("未登录的 WebSocket 请求绝不能升级成功")
	}
	if w.Code != 401 {
		t.Errorf("应返回 401，实际 %d", w.Code)
	}
}

// ---- lan_bypass：整套鉴权里最容易出安全事故的地方 ----

const lanBypassYAML = `
auth:
  enabled: true
  lan_bypass:
    enabled: true
    deny_if_forwarded: true
`

func TestLANBypassAllowsLocalDirect(t *testing.T) {
	setupEnv(t, lanBypassYAML)
	seedAdmin(t)
	r := newTestRouter()

	w := do(r, "GET", "/api/instances", func(req *http.Request) {
		req.RemoteAddr = "127.0.0.1:54321"
	})
	if w.Code != 200 {
		t.Errorf("本机直连且无反代头时应放行，实际 %d", w.Code)
	}
}

// ★ 核心用例。本项目的典型部署是反代跑在同一台机器上转发到 127.0.0.1，
// 此时公网请求的 RemoteAddr 也是 127.0.0.1。如果按"源 IP 是内网就放行"
// 的朴素实现，开启 lan_bypass 就等于鉴权对公网彻底失效。
// 区分两者的唯一信号就是反代设置的 X-Forwarded-For。
func TestLANBypassDeniesForwardedRequests(t *testing.T) {
	setupEnv(t, lanBypassYAML)
	seedAdmin(t)
	r := newTestRouter()

	for _, header := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded", "X-Forwarded-Host"} {
		w := do(r, "GET", "/api/instances", func(req *http.Request) {
			req.RemoteAddr = "127.0.0.1:54321" // 本机反代转发过来
			req.Header.Set(header, "203.0.113.7")
		})
		if w.Code != 401 {
			t.Errorf("带 %s 的请求必须拒绝（否则公网可绕过鉴权），实际 %d", header, w.Code)
		}
	}
}

func TestLANBypassDeniesPublicIP(t *testing.T) {
	setupEnv(t, lanBypassYAML)
	seedAdmin(t)
	r := newTestRouter()

	w := do(r, "GET", "/api/instances", func(req *http.Request) {
		req.RemoteAddr = "203.0.113.9:12345"
	})
	if w.Code != 401 {
		t.Errorf("公网来源应拒绝，实际 %d", w.Code)
	}
}

func TestLANBypassDisabledByDefault(t *testing.T) {
	setupEnv(t, authEnabledYAML)
	seedAdmin(t)
	r := newTestRouter()

	w := do(r, "GET", "/api/instances", func(req *http.Request) {
		req.RemoteAddr = "127.0.0.1:54321"
	})
	if w.Code != 401 {
		t.Errorf("未开启 lan_bypass 时本机也要登录，实际 %d", w.Code)
	}
}

// ---- 会话 ----

func TestValidSessionPasses(t *testing.T) {
	setupEnv(t, authEnabledYAML)
	u := seedAdmin(t)
	m := auth.GetManager()
	tok, _, err := m.IssueSession(u, auth.StageFull, []string{auth.AMRPassword}, appconfig.Get().Auth.Session.TTL)
	if err != nil {
		t.Fatalf("IssueSession: %v", err)
	}
	r := newTestRouter()

	w := do(r, "GET", "/api/instances", func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: appconfig.Get().Auth.Session.CookieName, Value: tok})
	})
	if w.Code != 200 {
		t.Errorf("持有效会话应放行，实际 %d: %s", w.Code, w.Body.String())
	}
}

// pre-auth 令牌只能用于两步验证的第二步，绝不能当作完整会话凭证
func TestPreAuthTokenRejectedAsSession(t *testing.T) {
	setupEnv(t, authEnabledYAML)
	u := seedAdmin(t)
	m := auth.GetManager()
	tok, _, err := m.IssueSession(u, auth.StagePre, nil, appconfig.Get().Auth.Session.TTL)
	if err != nil {
		t.Fatalf("IssueSession: %v", err)
	}
	r := newTestRouter()

	w := do(r, "GET", "/api/instances", func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: appconfig.Get().Auth.Session.CookieName, Value: tok})
	})
	if w.Code != 401 {
		t.Errorf("pre-auth 令牌不得通过鉴权，实际 %d", w.Code)
	}
}

func TestGarbageTokenRejected(t *testing.T) {
	setupEnv(t, authEnabledYAML)
	seedAdmin(t)
	r := newTestRouter()

	for _, tok := range []string{"garbage", "a.b", "", strings.Repeat("x", 500)} {
		w := do(r, "GET", "/api/instances", func(req *http.Request) {
			req.AddCookie(&http.Cookie{Name: appconfig.Get().Auth.Session.CookieName, Value: tok})
		})
		if w.Code != 401 {
			t.Errorf("非法令牌 %q 应返回 401，实际 %d", tok, w.Code)
		}
	}
}

// ---- 本机限定接口 ----

func TestIsLoopbackRequest(t *testing.T) {
	cases := []struct {
		name   string
		remote string
		header string
		want   bool
	}{
		{"本机直连", "127.0.0.1:1234", "", true},
		{"IPv6 本机", "[::1]:1234", "", true},
		{"公网", "203.0.113.9:1234", "", false},
		{"内网非本机", "192.168.1.5:1234", "", false},
		{"本机但带 XFF（反代转发）", "127.0.0.1:1234", "X-Forwarded-For", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/api/auth/reload", nil)
			ctx.Request.RemoteAddr = c.remote
			if c.header != "" {
				ctx.Request.Header.Set(c.header, "203.0.113.7")
			}
			if got := IsLoopbackRequest(ctx); got != c.want {
				t.Errorf("IsLoopbackRequest = %v，期望 %v", got, c.want)
			}
		})
	}
}

// ---- 测试夹具 ----

const authEnabledYAML = "auth:\n  enabled: true\n"

func seedAdmin(t *testing.T) *auth.User {
	t.Helper()
	m := auth.GetManager()
	if m == nil {
		t.Fatal("鉴权管理器未初始化")
	}
	u, err := m.CreateUser(context.Background(), "admin", "correct-horse", auth.RoleAdmin, "test")
	if err != nil {
		t.Fatalf("创建测试用户失败: %v", err)
	}
	return u
}
