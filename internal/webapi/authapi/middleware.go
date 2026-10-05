// Package authapi 是鉴权的 HTTP 接入层：中间件 + /api/auth/* 与 /api/users/* 路由。
// 领域逻辑在 asa-server/auth，本包只负责把它接到 gin 上。
package authapi

import (
	"fmt"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"asa-server/internal/appconfig"
	"asa-server/internal/auth"
	"asa-server/internal/mesh"
	"asa-server/pkg/logger"

	"github.com/gin-gonic/gin"
)

const (
	ctxUserKey    = "auth.user"
	ctxClaimsKey  = "auth.claims"
	ctxBypassKey  = "auth.bypassed"
	ctxPeerKey    = "auth.peer"
	codeUnauth    = "unauthorized"
	codeSetupReq  = "setup_required"
	codeForbidden = "forbidden"
	// codePeerForbidden：远程禁区（§6.3）。前端据此提示「远程访问不允许」，而不是「没有权限」。
	codePeerForbidden = "peer_forbidden"
)

// peerForbiddenPrefixes 是远程禁区（docs/REMOTE_MANAGER_MESH_PLAN.md §6.3）：无论授予什么角色，
// 经隧道一律 403——远程不能改本机的账号、不能改本机的配对关系、不能借本机再跳到第三台（禁止多跳）。
var peerForbiddenPrefixes = []string{"/api/users", "/api/auth", "/api/mesh", "/api/peers"}

// peerAllowedExceptions 是禁区里对隧道请求开放的路径：前端在远程上下文里也会问登录态。
var peerAllowedExceptions = map[string]bool{"/api/auth/state": true}

// PeerForbiddenPath 判断 p 是否在远程禁区里。按原样与 path.Clean 之后各判一次，
// 不让 /api/./users 这类写法绕过。
func PeerForbiddenPath(p string) bool {
	for _, cand := range []string{p, path.Clean("/" + p)} {
		if peerAllowedExceptions[cand] {
			continue
		}
		for _, pre := range peerForbiddenPrefixes {
			if cand == pre || strings.HasPrefix(cand, pre+"/") {
				return true
			}
		}
	}
	return false
}

// 这些路径在鉴权开启时依然放行。
//
// 静态资源（非 /api 前缀）也一律放行，这是刻意的：SPA 的 JS/CSS 里没有数据，
// 数据全在 API。给静态资源加鉴权没有任何安全收益，只会让未登录时连登录页
// 都打不开。
var exemptPaths = map[string]bool{
	"/health":              true,
	"/api/auth/state":      true,
	"/api/auth/login":      true,
	"/api/auth/login/totp": true,
	"/api/auth/logout":     true,
	"/api/auth/setup":      true,
	"/api/auth/reload":     true,
}

// Middleware 是鉴权闸门，必须挂在所有业务路由之前。
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := appconfig.Get()
		// 隧道请求**先于** auth.enabled 的短路被识别（§6.4）：远程授权在任何情况下都必须生效，
		// lan_bypass 对它永不生效（根本走不到那一步）。身份来自 context，不来自任何请求头。
		if pi, ok := mesh.PeerIdentityFrom(c.Request.Context()); ok {
			handlePeer(c, pi, cfg.Auth.Enabled)
			return
		}
		if !cfg.Auth.Enabled {
			c.Next()
			return
		}

		// 把来源信息挂到 context 上，让下游领域方法写审计时能带上"从哪来"，
		// 而不必在每个方法签名里传 HTTP 细节。豁免路径（登录、setup）也需要，
		// 所以放在最前面。
		c.Request = c.Request.WithContext(auth.WithAuditSource(c.Request.Context(), auth.AuditSource{
			ClientIP:  clientIPOrRemote(c),
			UserAgent: c.Request.UserAgent(),
		}))

		path := c.Request.URL.Path
		if !strings.HasPrefix(path, "/api") || exemptPaths[path] {
			c.Next()
			return
		}

		m := auth.GetManager()
		if m == nil {
			// 鉴权开着但没初始化成功，这时候放行等于完全没有鉴权。
			reject(c, http.StatusServiceUnavailable, codeUnauth, "鉴权模块未就绪")
			return
		}
		// 零用户状态：除首次引导外一律拒绝，前端据此跳到 /setup
		if m.UserCount() == 0 {
			reject(c, http.StatusUnauthorized, codeSetupReq, "尚未创建管理员账号")
			return
		}

		if shouldBypass(c, &cfg.Auth) {
			c.Set(ctxBypassKey, true)
			c.Next()
			return
		}

		tok, err := c.Cookie(cfg.Auth.Session.CookieName)
		if err != nil || tok == "" {
			recordAuthFailure(c)
			reject(c, http.StatusUnauthorized, codeUnauth, "未登录")
			return
		}
		user, claims, err := m.VerifySession(tok)
		if err != nil {
			recordAuthFailure(c)
			clearSessionCookie(c)
			reject(c, http.StatusUnauthorized, codeUnauth, "会话已失效，请重新登录")
			return
		}

		// 滑动续期：让活跃用户的到期时间自然错开。固定 TTL 会让所有客户端
		// 在同一时刻掉线、同时开始重连，形成惊群。
		if auth.ShouldRenew(claims, cfg.Auth.Session.TTL, time.Now()) {
			if newTok, _, err := m.IssueSession(user, auth.StageFull, claims.AMR, cfg.Auth.Session.TTL); err == nil {
				setSessionCookie(c, newTok)
			}
		}

		c.Set(ctxUserKey, user)
		c.Set(ctxClaimsKey, claims)
		c.Next()
	}
}

// handlePeer 处理经管理器互控隧道进来的请求：远程禁区 → 按授予的角色合成用户 → 放行 → 审计。
func handlePeer(c *gin.Context, pi mesh.PeerIdentity, authEnabled bool) {
	if PeerForbiddenPath(c.Request.URL.Path) {
		reject(c, http.StatusForbidden, codePeerForbidden, "远程访问不允许使用此接口")
		return
	}
	role := auth.RoleOperator
	if pi.Role == mesh.RoleAdmin {
		role = auth.RoleAdmin
	}
	user := &auth.User{Username: pi.ActorName(), Role: role}
	c.Set(ctxUserKey, user)
	c.Set(ctxPeerKey, pi)
	short := pi.NodeID
	if len(short) > 9 {
		short = short[:9]
	}
	c.Request = c.Request.WithContext(auth.WithAuditSource(c.Request.Context(), auth.AuditSource{
		ClientIP:  pi.Addr,
		UserAgent: "asa-mesh/" + short,
	}))
	c.Next()

	switch c.Request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return
	}
	// B 开着鉴权时，写操作另进 auth 的审计表；关着时只有 mesh 自己那行日志（auth.db 根本没打开）。
	if !authEnabled {
		return
	}
	if m := auth.GetManager(); m != nil {
		m.Audit(c.Request.Context(), auth.AuditEntry{
			Event:    auth.EventPeerRequest,
			Username: user.Username,
			Actor:    user.Username,
			Detail:   fmt.Sprintf("%s %s → %d", c.Request.Method, c.Request.URL.Path, c.Writer.Status()),
		})
	}
}

// PeerIdentity 返回隧道请求的对端身份；不是隧道请求时 ok 为 false。
func PeerIdentity(c *gin.Context) (mesh.PeerIdentity, bool) {
	if v, ok := c.Get(ctxPeerKey); ok {
		if pi, ok := v.(mesh.PeerIdentity); ok {
			return pi, true
		}
	}
	return mesh.PeerIdentityFrom(c.Request.Context())
}

// IsPeer 表示该请求来自管理器互控隧道。
func IsPeer(c *gin.Context) bool {
	_, ok := PeerIdentity(c)
	return ok
}

// RequireAdmin 挂在只允许管理员访问的路由上
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 隧道请求只看授予的角色，与 auth.enabled 无关（§6.4）：B 关着鉴权时，
		// 授予 operator 的对端照样调不了管理员接口。
		if pi, ok := PeerIdentity(c); ok {
			if pi.Role != mesh.RoleAdmin {
				reject(c, http.StatusForbidden, codeForbidden, "需要管理员权限（对方只授予了本机操作员角色）")
				return
			}
			c.Next()
			return
		}
		if !appconfig.Get().Auth.Enabled {
			c.Next()
			return
		}
		// 内网免鉴权的请求没有具体用户身份，视同管理员——
		// 能从内网直连本机的人本来就等价于有物理访问权
		if Bypassed(c) {
			c.Next()
			return
		}
		u := CurrentUser(c)
		if u == nil || !u.IsAdmin() {
			reject(c, http.StatusForbidden, codeForbidden, "需要管理员权限")
			return
		}
		c.Next()
	}
}

// CurrentUser 返回当前请求的登录用户，未登录或走内网免鉴权时为 nil
func CurrentUser(c *gin.Context) *auth.User {
	if v, ok := c.Get(ctxUserKey); ok {
		if u, ok := v.(*auth.User); ok {
			return u
		}
	}
	return nil
}

// CurrentClaims 返回当前会话令牌的载荷
func CurrentClaims(c *gin.Context) *auth.Claims {
	if v, ok := c.Get(ctxClaimsKey); ok {
		if cl, ok := v.(*auth.Claims); ok {
			return cl
		}
	}
	return nil
}

// Bypassed 表示该请求走的是内网免鉴权
func Bypassed(c *gin.Context) bool {
	v, ok := c.Get(ctxBypassKey)
	return ok && v == true
}

// IsAuthenticated 供 WebSocket handler 做纵深防御用。
// 隧道请求恒为 true：撤销由 mesh 主动断开隧道流保证，不依赖这里 60 秒一次的复查。
func IsAuthenticated(c *gin.Context) bool {
	if IsPeer(c) {
		return true
	}
	if !appconfig.Get().Auth.Enabled {
		return true
	}
	return Bypassed(c) || CurrentUser(c) != nil
}

// ActorName 返回用于审计的操作者标识
func ActorName(c *gin.Context) string {
	if u := CurrentUser(c); u != nil {
		return u.Username
	}
	if Bypassed(c) {
		return "lan-bypass"
	}
	return ""
}

// shouldBypass 判断是否命中内网免鉴权。
//
// ⚠️ 这个函数是整套鉴权里最容易出安全事故的地方。本项目的典型部署是反代
// （frpc / Nginx）跑在同一台机器上转发到 127.0.0.1，此时公网请求和本机请求的
// RemoteAddr 完全一样。按"源 IP 是内网就放行"的朴素实现，开启 lan_bypass
// 就等于鉴权对公网彻底失效。
func shouldBypass(c *gin.Context, cfg *appconfig.AuthConfig) bool {
	lb := cfg.LANBypass
	if !lb.Enabled {
		return false
	}

	// 规则 1：任何反代痕迹一律不放行。
	// 反代设置 XFF 是把"本机反代转发来的公网请求"识别出来的唯一信号。
	if lb.DenyIfForwarded {
		for _, h := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded", "X-Forwarded-Host"} {
			if c.GetHeader(h) != "" {
				return false
			}
		}
	}

	// 规则 2：用 RemoteAddr，不用 c.ClientIP()。
	// ClientIP() 在可信代理场景下返回的是 XFF 里的值，语义是"最终客户端"；
	// 这里要判断的是"这个 TCP 连接从哪来"。
	ip := auth.HostIP(c.Request.RemoteAddr)
	if ip == nil {
		return false
	}

	// 规则 3：落在配置的内网网段内
	return auth.IsInNetworks(ip, lb.Networks)
}

// reject 按链路类型返回拒绝响应。
//
// 三种链路必须区别对待，否则会造成重连风暴：
//   - WebSocket：在 Upgrade 之前返回 401，绝不"先升级再关闭"
//   - SSE：必须在写出任何响应体之前返回非 200。一旦响应头是
//     200 + text/event-stream，浏览器 EventSource 会每 3 秒无限重连，
//     而且 JS 关不掉它
//   - 普通 REST：正常返回 JSON
func reject(c *gin.Context, status int, code, msg string) {
	if isWebSocketUpgrade(c.Request) {
		c.Header("Connection", "close")
	}
	c.AbortWithStatusJSON(status, gin.H{"error": msg, "code": code})
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// IsLoopbackRequest 判断请求是否来自本机且不经过任何反代。
// /api/auth/setup 和 /api/auth/reload 靠它把自己限制在本机。
func IsLoopbackRequest(c *gin.Context) bool {
	for _, h := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded", "X-Forwarded-Host"} {
		if c.GetHeader(h) != "" {
			return false
		}
	}
	ip := auth.HostIP(c.Request.RemoteAddr)
	return ip != nil && ip.IsLoopback()
}

// ---- 鉴权失败限速 + 日志降噪 ----

// 这个限速器针对的是"会话失效后客户端疯狂重连"造成的流量，
// 用内存计数即可——重启清零无所谓，它防的是流量不是爆破。
// 登录接口的密码爆破防护是另一套，落在数据库里（auth.login_failures）。
type failCounter struct {
	mu     sync.Mutex
	counts map[string]*ipFailures
}

type ipFailures struct {
	count       int
	windowStart time.Time
	lastLogged  time.Time
	sinceLogged int
}

const (
	authFailWindow    = time.Minute
	authFailThreshold = 20
)

var authFails = &failCounter{counts: map[string]*ipFailures{}}

func recordAuthFailure(c *gin.Context) {
	ip := c.ClientIP()
	if ip == "" {
		return
	}
	now := time.Now()

	authFails.mu.Lock()
	defer authFails.mu.Unlock()

	f, ok := authFails.counts[ip]
	if !ok || now.Sub(f.windowStart) > authFailWindow {
		f = &ipFailures{windowStart: now, lastLogged: now}
		authFails.counts[ip] = f
		// 顺手清掉长期不活跃的条目，免得这张表无限增长
		if len(authFails.counts) > 1000 {
			for k, v := range authFails.counts {
				if now.Sub(v.windowStart) > 5*authFailWindow {
					delete(authFails.counts, k)
				}
			}
		}
	}
	f.count++
	f.sinceLogged++

	// 日志按分钟聚合。一次重连风暴能产生几万条失败，
	// 一条一条写会把 asaServer.log 刷爆，真正的错误反而被冲掉。
	if now.Sub(f.lastLogged) >= authFailWindow {
		logger.Warnf("[鉴权] IP %s 过去 1 分钟鉴权失败 %d 次", ip, f.sinceLogged)
		f.lastLogged = now
		f.sinceLogged = 0
	}
}

// TooManyAuthFailures 判断该 IP 是否已经超出鉴权失败频率上限
func TooManyAuthFailures(ip string) bool {
	authFails.mu.Lock()
	defer authFails.mu.Unlock()
	f, ok := authFails.counts[ip]
	if !ok || time.Since(f.windowStart) > authFailWindow {
		return false
	}
	return f.count > authFailThreshold
}

// ---- Cookie ----

func setSessionCookie(c *gin.Context, token string) {
	cfg := appconfig.Get()
	s := cfg.Auth.Session
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     s.CookieName,
		Value:    token,
		Path:     s.CookiePath,
		MaxAge:   int(s.TTL.Seconds()),
		HttpOnly: true, // 前端 JS 读不到令牌，XSS 也偷不走
		Secure:   cfg.Server.TLS.Enabled,
		SameSite: s.SameSiteMode(),
	})
}

func clearSessionCookie(c *gin.Context) {
	s := appconfig.Get().Auth.Session
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     s.CookieName,
		Value:    "",
		Path:     s.CookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   appconfig.Get().Server.TLS.Enabled,
		SameSite: s.SameSiteMode(),
	})
}

// clientIPOrRemote 取审计与限流用的来源 IP
func clientIPOrRemote(c *gin.Context) string {
	if ip := c.ClientIP(); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		return c.Request.RemoteAddr
	}
	return host
}
