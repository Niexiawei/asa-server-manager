package authapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"asa-server/internal/appconfig"
	"asa-server/internal/auth"
	"asa-server/internal/logger"

	"github.com/gin-gonic/gin"
)

// preAuthTTL 是两步验证中间态令牌的有效期。
// 短一点：它只需要撑过用户掏出手机看验证码的这段时间。
const preAuthTTL = 5 * time.Minute

// Handler 承载 /api/auth/* 与 /api/users/* 路由
type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) RegisterRouter(r *gin.Engine) {
	a := r.Group("/api/auth")
	{
		a.GET("/state", h.state)
		a.POST("/login", h.login)
		a.POST("/login/totp", h.loginTOTP)
		a.POST("/logout", h.logout)
		a.POST("/logout-all", h.logoutAll)
		a.POST("/password", h.changePassword)
		a.POST("/setup", h.setup)
		a.POST("/reload", h.reload)

		a.POST("/totp/setup", h.totpSetup)
		a.POST("/totp/confirm", h.totpConfirm)
		a.POST("/totp/disable", h.totpDisable)
		a.POST("/totp/recovery/regenerate", h.regenerateRecovery)

		a.GET("/audit", RequireAdmin(), h.audit)

		h.registerWebAuthnRoutes(a)
	}

	u := r.Group("/api/users", RequireAdmin())
	{
		u.GET("", h.listUsers)
		u.POST("", h.createUser)
		u.PUT("/:username", h.updateUser)
		u.DELETE("/:username", h.deleteUser)
		u.POST("/:username/password", h.resetPassword)
		u.POST("/:username/totp/reset", h.resetTOTP)
		u.POST("/:username/webauthn/reset", h.resetWebAuthn)
		u.POST("/:username/unlock", h.unlockUser)
	}
}

// ---- 状态 ----

type stateResponse struct {
	AuthEnabled       bool      `json:"auth_enabled"`
	Authenticated     bool      `json:"authenticated"`
	Bypassed          bool      `json:"bypassed"`
	SetupRequired     bool      `json:"setup_required"`
	SetupAllowed      bool      `json:"setup_allowed"`
	User              *userView `json:"user,omitempty"`
	TOTPEnabledGlobal bool      `json:"totp_enabled_global"`
	TOTPRequired      bool      `json:"totp_required"`
	// 密码登录恒可用，所以没有对应字段。WebAuthn 是补充，不可用时静默退回密码。
	WebAuthnAvailable bool   `json:"webauthn_available"`
	WebAuthnReason    string `json:"webauthn_reason,omitempty"`
	WebAuthnRPID      string `json:"webauthn_rp_id,omitempty"`
}

type userView struct {
	Username    string `json:"username"`
	Role        string `json:"role"`
	TOTPEnabled bool   `json:"totp_enabled"`
	Disabled    bool   `json:"disabled"`
	LastLoginAt string `json:"last_login_at,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

func toUserView(u *auth.User) *userView {
	if u == nil {
		return nil
	}
	v := &userView{
		Username:    u.Username,
		Role:        u.Role,
		TOTPEnabled: u.TOTPEnabled,
		Disabled:    u.Disabled,
		CreatedAt:   u.CreatedAt.Format(time.RFC3339),
	}
	if !u.LastLoginAt.IsZero() {
		v.LastLoginAt = u.LastLoginAt.Format(time.RFC3339)
	}
	return v
}

// state 是前端进入应用时问的第一个问题：要不要登录、我是谁、有哪些登录方式可用。
// 未登录时返回 200 + authenticated:false，而不是 401 —— 否则前端拿不到
// "是否开启了鉴权"这个信息。
func (h *Handler) state(c *gin.Context) {
	cfg := appconfig.Get()
	resp := stateResponse{
		AuthEnabled:       cfg.Auth.Enabled,
		TOTPEnabledGlobal: cfg.Auth.TOTP.Enabled,
		TOTPRequired:      cfg.Auth.TOTP.Required,
	}
	if !cfg.Auth.Enabled {
		resp.Authenticated = true // 没开鉴权，前端按已登录处理
		c.JSON(http.StatusOK, resp)
		return
	}

	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪", "code": codeUnauth})
		return
	}

	if m.UserCount() == 0 {
		resp.SetupRequired = true
		// 只有本机才能创建第一个管理员，防止服务恰好暴露在公网时被人抢注
		resp.SetupAllowed = IsLoopbackRequest(c)
		c.JSON(http.StatusOK, resp)
		return
	}

	av := webAuthnAvailability(c)
	resp.WebAuthnAvailable = av.Available
	resp.WebAuthnReason = av.Reason
	resp.WebAuthnRPID = av.RPID

	if Bypassed(c) {
		resp.Bypassed = true
		resp.Authenticated = true
		c.JSON(http.StatusOK, resp)
		return
	}
	// 这个接口在豁免清单里，中间件不会填 user，得自己校验一次
	if tok, err := c.Cookie(cfg.Auth.Session.CookieName); err == nil && tok != "" {
		if u, _, err := m.VerifySession(tok); err == nil {
			resp.Authenticated = true
			resp.User = toUserView(u)
		}
	}
	c.JSON(http.StatusOK, resp)
}

// ---- 登录 ----

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *Handler) login(c *gin.Context) {
	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪"})
		return
	}
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}

	ip := clientIPOrRemote(c)
	res, err := m.AuthenticatePassword(c.Request.Context(), req.Username, req.Password, ip, c.Request.UserAgent())
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, auth.ErrLockedOut) {
			status = http.StatusTooManyRequests
		}
		c.JSON(status, gin.H{"error": err.Error(), "code": codeUnauth})
		return
	}

	cfg := appconfig.Get().Auth
	if res.TOTPRequired {
		tok, _, err := m.IssueSession(res.User, auth.StagePre, []string{auth.AMRPassword}, preAuthTTL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "签发令牌失败"})
			return
		}
		setPreAuthCookie(c, tok)
		c.JSON(http.StatusOK, gin.H{"totp_required": true})
		return
	}

	tok, _, err := m.IssueSession(res.User, auth.StageFull, []string{auth.AMRPassword}, cfg.Session.TTL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "签发令牌失败"})
		return
	}
	clearPreAuthCookie(c)
	setSessionCookie(c, tok)
	c.JSON(http.StatusOK, gin.H{"user": toUserView(res.User)})
}

type totpRequest struct {
	Code string `json:"code"`
}

func (h *Handler) loginTOTP(c *gin.Context) {
	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪"})
		return
	}
	preTok, err := c.Cookie(preAuthCookieName())
	if err != nil || preTok == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先完成密码登录", "code": codeUnauth})
		return
	}
	// 必须用 VerifyPreAuth 而不是 VerifySession：只要有一处把 pre-auth 令牌
	// 当成完整凭证接受，两步验证就形同虚设
	u, _, err := m.VerifyPreAuth(preTok)
	if err != nil {
		clearPreAuthCookie(c)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "验证已超时，请重新登录", "code": codeUnauth})
		return
	}

	var req totpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}

	ip := clientIPOrRemote(c)
	amr, err := m.CompleteTOTP(c.Request.Context(), u, req.Code, ip, c.Request.UserAgent())
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, auth.ErrLockedOut) {
			status = http.StatusTooManyRequests
		}
		c.JSON(status, gin.H{"error": err.Error(), "code": codeUnauth})
		return
	}

	// 重新取一次：CompleteTOTP 可能更新了 totp_last_step
	if fresh, ok := m.Lookup(u.Username); ok {
		u = fresh
	}
	tok, _, err := m.IssueSession(u, auth.StageFull, amr, appconfig.Get().Auth.Session.TTL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "签发令牌失败"})
		return
	}
	clearPreAuthCookie(c)
	setSessionCookie(c, tok)
	c.JSON(http.StatusOK, gin.H{"user": toUserView(u)})
}

// logout 是幂等的：未登录时也返回 200，前端不用为"登出失败"写分支
func (h *Handler) logout(c *gin.Context) {
	m := auth.GetManager()
	if m != nil {
		if tok, err := c.Cookie(appconfig.Get().Auth.Session.CookieName); err == nil && tok != "" {
			if u, claims, err := m.VerifySession(tok); err == nil {
				_ = m.RevokeToken(c.Request.Context(), claims)
				m.Audit(c.Request.Context(), auth.AuditEntry{
					Event: auth.EventLogout, Username: u.Username, Actor: u.Username,
					ClientIP: clientIPOrRemote(c), UserAgent: c.Request.UserAgent(),
				})
			}
		}
	}
	clearSessionCookie(c)
	clearPreAuthCookie(c)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) logoutAll(c *gin.Context) {
	m, u, ok := requireUser(c)
	if !ok {
		return
	}
	if err := m.RevokeAllSessions(c.Request.Context(), u.Username); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	m.Audit(c.Request.Context(), auth.AuditEntry{
		Event: auth.EventLogoutAll, Username: u.Username, Actor: u.Username,
		ClientIP: clientIPOrRemote(c),
	})
	clearSessionCookie(c)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func (h *Handler) changePassword(c *gin.Context) {
	m, u, ok := requireUser(c)
	if !ok {
		return
	}
	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}
	if !auth.VerifyPassword(u.PasswordHash, req.OldPassword) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "当前密码不正确"})
		return
	}
	if err := m.ChangePassword(c.Request.Context(), u.Username, req.NewPassword,
		u.Username, auth.EventPasswordChange); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 改密码会 bump session_version，把所有设备都踢掉——包括当前这台。
	// 给当前设备重新签一张，否则用户改完密码就被自己挤下线了。
	fresh, _ := m.Lookup(u.Username)
	if tok, _, err := m.IssueSession(fresh, auth.StageFull, []string{auth.AMRPassword},
		appconfig.Get().Auth.Session.TTL); err == nil {
		setSessionCookie(c, tok)
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ---- 首次引导 ----

type setupRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// setup 创建第一个管理员，只在零用户状态且请求来自本机时可用。
func (h *Handler) setup(c *gin.Context) {
	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪"})
		return
	}
	if m.UserCount() > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "管理员账号已存在"})
		return
	}
	// 服务可能已经暴露在公网上，不能让人从外面抢注第一个管理员
	if !IsLoopbackRequest(c) {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "首次创建管理员只能在服务器本机操作，请在本机浏览器打开面板",
		})
		return
	}

	var req setupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}
	u, err := m.CreateUser(c.Request.Context(), req.Username, req.Password, auth.RoleAdmin, "setup")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tok, _, err := m.IssueSession(u, auth.StageFull, []string{auth.AMRPassword},
		appconfig.Get().Auth.Session.TTL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "签发令牌失败"})
		return
	}
	setSessionCookie(c, tok)
	c.JSON(http.StatusOK, gin.H{"user": toUserView(u)})
}

// reload 让运行中的服务重新加载内存副本。
//
// CLI 在本机改完 auth.db 之后调用它，这样忘记密码、丢失两步验证设备的用户
// 不必等服务重启就能登录。只接受本机且不带反代头的请求。
func (h *Handler) reload(c *gin.Context) {
	if !IsLoopbackRequest(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "该接口只接受本机调用"})
		return
	}
	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪"})
		return
	}
	if err := m.Reload(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	logger.GetLogger().Info("[鉴权] 已按本机请求重新加载用户数据")
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ---- 审计 ----

func (h *Handler) audit(c *gin.Context) {
	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪"})
		return
	}
	f := auth.AuditFilter{
		Username: c.Query("user"),
		Event:    c.Query("event"),
		Limit:    queryInt(c, "limit", 100),
		Offset:   queryInt(c, "offset", 0),
	}
	if s := c.Query("since"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			f.Since = t
		}
	}
	entries, err := auth.QueryAudit(c.Request.Context(), m.DB(), f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries, "count": len(entries)})
}

// ---- 内部工具 ----

// requireUser 取出当前登录用户。内网免鉴权的请求没有具体身份，
// 无法执行"改自己的密码"这类操作，需要单独提示。
func requireUser(c *gin.Context) (*auth.Manager, *auth.User, bool) {
	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪"})
		return nil, nil, false
	}
	u := CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "该操作需要登录后进行（当前请求走的是内网免鉴权，没有具体账户身份）",
			"code":  codeUnauth,
		})
		return nil, nil, false
	}
	return m, u, true
}

func preAuthCookieName() string {
	return appconfig.Get().Auth.Session.CookieName + "_pre"
}

func setPreAuthCookie(c *gin.Context, token string) {
	cfg := appconfig.Get()
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     preAuthCookieName(),
		Value:    token,
		Path:     cfg.Auth.Session.CookiePath,
		MaxAge:   int(preAuthTTL.Seconds()),
		HttpOnly: true,
		Secure:   cfg.Server.TLS.Enabled,
		SameSite: cfg.Auth.Session.SameSiteMode(),
	})
}

func clearPreAuthCookie(c *gin.Context) {
	cfg := appconfig.Get()
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     preAuthCookieName(),
		Value:    "",
		Path:     cfg.Auth.Session.CookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.Server.TLS.Enabled,
		SameSite: cfg.Auth.Session.SameSiteMode(),
	})
}

func queryInt(c *gin.Context, key string, def int) int {
	n, err := strconv.Atoi(c.Query(key))
	if err != nil {
		return def
	}
	return n
}
