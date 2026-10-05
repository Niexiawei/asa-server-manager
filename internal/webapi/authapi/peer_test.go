package authapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"asa-server/internal/auth"
	"asa-server/internal/mesh"
)

// 管理器互控隧道请求的鉴权（docs/REMOTE_MANAGER_MESH_PLAN.md §6.4、§12 P3-6）。安全用例，作为回归守卫。

// peerRouter 模拟 B 侧：一个前置中间件把 PeerIdentity 放进 context（隧道服务端的做法），
// 之后是真实的鉴权中间件与 /api/auth/*、/api/users/* 路由。
func peerRouter(pi *mesh.PeerIdentity) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if pi != nil {
		r.Use(func(c *gin.Context) {
			c.Request = c.Request.WithContext(mesh.WithPeerIdentity(c.Request.Context(), *pi))
		})
	}
	r.Use(Middleware())
	NewHandler().RegisterRouter(r)
	r.GET("/api/instances", func(c *gin.Context) { c.JSON(200, gin.H{"actor": ActorName(c)}) })
	r.POST("/api/instances", func(c *gin.Context) { c.JSON(200, gin.H{"actor": ActorName(c)}) })
	r.DELETE("/api/arkapi/packages/:token", RequireAdmin(), func(c *gin.Context) { c.Status(404) })
	r.GET("/api/mesh/status", func(c *gin.Context) { c.Status(200) })
	r.GET("/api/peers/:id/fwd/*path", func(c *gin.Context) { c.Status(200) })
	return r
}

func peerID(role string) *mesh.PeerIdentity {
	return &mesh.PeerIdentity{NodeID: "ABCDEFGH-IJKL", Label: "机房-2", Role: role, RemoteUser: "alice", Addr: "relay:ABCDEFGH"}
}

func codeOf(t *testing.T, body []byte) string {
	t.Helper()
	var v struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &v)
	return v.Code
}

func TestPeerForbiddenPath(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/users":                    true,
		"/api/users/bob/role":           true,
		"/api/auth/audit":               true,
		"/api/auth":                     true,
		"/api/auth/state":               false,
		"/api/mesh/status":              true,
		"/api/peers/x/fwd/api/users":    true,
		"/api/./users":                  true,
		"/api/instances/../users":       true,
		"/api/instances":                false,
		"/api/server/a/start":           false,
		"/api/authority-is-not-auth":    false,
		"/api/meshy-but-not-mesh/thing": false,
	} {
		if got := PeerForbiddenPath(path); got != want {
			t.Errorf("PeerForbiddenPath(%q) = %v，应为 %v", path, got, want)
		}
	}
}

func checkPeerTable(t *testing.T, r *gin.Engine, role string) {
	t.Helper()
	cases := []struct {
		method, path string
		code         int
		errCode      string
	}{
		{"GET", "/api/instances", 200, ""},
		{"GET", "/api/users", 403, codePeerForbidden},
		{"GET", "/api/auth/audit", 403, codePeerForbidden},
		{"GET", "/api/mesh/status", 403, codePeerForbidden},
		{"GET", "/api/peers/x/fwd/api/instances", 403, codePeerForbidden},
	}
	if role == mesh.RoleAdmin {
		cases = append(cases, struct {
			method, path string
			code         int
			errCode      string
		}{"DELETE", "/api/arkapi/packages/none", 404, ""})
	} else {
		cases = append(cases, struct {
			method, path string
			code         int
			errCode      string
		}{"DELETE", "/api/arkapi/packages/none", 403, codeForbidden})
	}
	for _, c := range cases {
		w := do(r, c.method, c.path, nil)
		if w.Code != c.code || (c.errCode != "" && codeOf(t, w.Body.Bytes()) != c.errCode) {
			t.Errorf("[%s] %s %s = %d %s，应为 %d %s", role, c.method, c.path, w.Code, w.Body.String(), c.code, c.errCode)
		}
	}
}

// B 关着鉴权：远程授权照样生效——operator 调不了管理员接口，禁区一律 403。
func TestPeerAuthDisabled(t *testing.T) {
	setupEnv(t, "auth:\n  enabled: false\n")
	checkPeerTable(t, peerRouter(peerID(mesh.RoleOperator)), mesh.RoleOperator)
	checkPeerTable(t, peerRouter(peerID(mesh.RoleAdmin)), mesh.RoleAdmin)

	// 对照：不是隧道请求时，鉴权关闭 = 一切放行（行为不变）。
	if w := do(peerRouter(nil), "DELETE", "/api/arkapi/packages/none", nil); w.Code != 404 {
		t.Fatalf("鉴权关闭时本机请求应放行，实际 %d", w.Code)
	}
}

// B 开着鉴权、甚至 lan_bypass 开到最宽：隧道请求不需要 Cookie，也不吃 lan_bypass，只认授予的角色。
func TestPeerAuthEnabledIgnoresBypass(t *testing.T) {
	setupEnv(t, `
auth:
  enabled: true
  lan_bypass:
    enabled: true
    deny_if_forwarded: true
    networks: ["0.0.0.0/0", "::/0"]
`)
	seedAdmin(t)
	r := peerRouter(peerID(mesh.RoleOperator))
	checkPeerTable(t, r, mesh.RoleOperator)
	w := do(r, "DELETE", "/api/arkapi/packages/none", func(req *http.Request) { req.RemoteAddr = "127.0.0.1:1" })
	if w.Code != 403 {
		t.Fatalf("lan_bypass 不该对隧道请求生效，实际 %d", w.Code)
	}
	checkPeerTable(t, peerRouter(peerID(mesh.RoleAdmin)), mesh.RoleAdmin)

	// /api/auth/state：远程上下文恒为已登录，并标明 peer。
	w = do(r, "GET", "/api/auth/state", nil)
	var st stateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || w.Code != 200 || !st.Authenticated || !st.Peer {
		t.Fatalf("/api/auth/state 对隧道请求应回 authenticated+peer：%d %s", w.Code, w.Body.String())
	}

	// ActorName 与审计：写操作进 auth 审计表，操作者是 peer:<备注名>/<用户>。
	w = do(r, "POST", "/api/instances", nil)
	if w.Code != 200 || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("POST 应放行：%d", w.Code)
	}
	var got struct{ Actor string }
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Actor != "peer:机房-2/alice" {
		t.Fatalf("ActorName = %q", got.Actor)
	}
	entries, err := auth.QueryAudit(context.Background(), auth.GetManager().DB(), auth.AuditFilter{Event: auth.EventPeerRequest, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	// 最新一条是这次 POST；前面 checkPeerTable 里被 RequireAdmin 挡掉的 DELETE 也各有一条（尝试同样要留痕）。
	if len(entries) == 0 || entries[0].Username != "peer:机房-2/alice" || entries[0].Detail != "POST /api/instances → 200" ||
		entries[0].ClientIP != "relay:ABCDEFGH" {
		t.Fatalf("审计记录不对：%+v", entries)
	}
	n := len(entries)
	// GET 不进审计。
	do(r, "GET", "/api/instances", nil)
	entries, _ = auth.QueryAudit(context.Background(), auth.GetManager().DB(), auth.AuditFilter{Event: auth.EventPeerRequest, Limit: 10})
	if len(entries) != n {
		t.Fatalf("GET 不该写审计：%d → %d 条", n, len(entries))
	}
}

// 隧道之外的请求伪造任何请求头都变不成隧道请求：身份只来自 context。
func TestPeerIdentityNotFromHeaders(t *testing.T) {
	setupEnv(t, "auth:\n  enabled: true\n")
	seedAdmin(t)
	r := peerRouter(nil)
	w := do(r, "GET", "/api/instances", func(req *http.Request) {
		req.Header.Set("X-Mesh-Peer", "ABCDEFGH")
		req.Header.Set("X-Peer-Role", "admin")
	})
	if w.Code != 401 {
		t.Fatalf("没有登录的本机请求应 401，实际 %d", w.Code)
	}
}
