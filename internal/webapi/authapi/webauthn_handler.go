package authapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"asa-server/appconfig"
	"asa-server/auth"
	"asa-server/logger"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/webauthn"
)

const ceremonyCookieName = "asa_wa_ceremony"

// RegisterWebAuthnRoutes 挂载 /api/auth/webauthn/*。
// login/* 在豁免清单里（未登录才要用），register/* 需要已登录。
func (h *Handler) registerWebAuthnRoutes(g *gin.RouterGroup) {
	w := g.Group("/webauthn")
	{
		w.POST("/register/begin", h.waRegisterBegin)
		w.POST("/register/finish", h.waRegisterFinish)
		w.POST("/login/begin", h.waLoginBegin)
		w.POST("/login/finish", h.waLoginFinish)
		w.GET("/credentials", h.waListCredentials)
		w.PUT("/credentials/:id", h.waRenameCredential)
		w.DELETE("/credentials/:id", h.waDeleteCredential)
	}
}

// requireWebAuthn 做域名闸门检查。
// 不可用时返回 409 而不是 4xx 通用错误，前端据此刷新可用性状态并隐藏入口。
func requireWebAuthn(c *gin.Context) (string, bool) {
	av := webAuthnAvailability(c)
	if !av.Available {
		c.JSON(http.StatusConflict, gin.H{
			"error":  "当前访问地址不支持 Passkey，请使用密码登录",
			"code":   "webauthn_unavailable",
			"reason": av.Reason,
		})
		return "", false
	}
	return av.RPID, true
}

func (h *Handler) waRegisterBegin(c *gin.Context) {
	m, u, ok := requireUser(c)
	if !ok {
		return
	}
	rpID, ok := requireWebAuthn(c)
	if !ok {
		return
	}
	wa, err := auth.InstanceFor(rpID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	wu, err := m.NewWebAuthnUser(c.Request.Context(), u, rpID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	opts, session, err := wa.BeginRegistration(wu,
		// 排除已注册的凭证，避免同一个认证器重复注册
		webauthn.WithExclusions(wu.Descriptors()),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "发起注册失败: " + err.Error()})
		return
	}

	id, err := auth.NewCeremony(rpID, u.ID, session)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	setCeremonyCookie(c, id)
	c.JSON(http.StatusOK, opts)
}

type registerFinishRequest struct {
	Name       string          `json:"name"`
	Credential json.RawMessage `json:"credential"`
}

func (h *Handler) waRegisterFinish(c *gin.Context) {
	m, u, ok := requireUser(c)
	if !ok {
		return
	}
	rpID, ok := requireWebAuthn(c)
	if !ok {
		return
	}

	var req registerFinishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}
	session, _, err := takeCeremony(c, rpID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	wa, err := auth.InstanceFor(rpID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	wu, err := m.NewWebAuthnUser(c.Request.Context(), u, rpID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	cred, err := wa.FinishRegistration(wu, *session, credentialRequest(c, req.Credential))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "注册失败: " + err.Error()})
		return
	}

	if err := m.SaveCredential(c.Request.Context(), u.ID, rpID, req.Name, cred); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, auth.ErrCredentialExists) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	m.Audit(c.Request.Context(), auth.AuditEntry{
		Event: auth.EventCredAdd, Username: u.Username, Actor: u.Username,
		Detail: "RP ID " + rpID,
	})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type loginBeginRequest struct {
	Username string `json:"username"`
}

func (h *Handler) waLoginBegin(c *gin.Context) {
	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪"})
		return
	}
	rpID, ok := requireWebAuthn(c)
	if !ok {
		return
	}
	wa, err := auth.InstanceFor(rpID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var req loginBeginRequest
	_ = c.ShouldBindJSON(&req)

	var opts any
	var session *webauthn.SessionData

	if req.Username == "" {
		// discoverable 流程：不需要用户名，浏览器从认证器里挑
		assertion, s, err := wa.BeginDiscoverableLogin()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		opts, session = assertion, s
	} else {
		u, found := m.Lookup(req.Username)
		if !found || u.Disabled {
			// 不能直接返回 404 —— 那是用户名枚举漏洞。
			// 走 discoverable 流程返回一个正常的 challenge，
			// 让失败发生在 finish 阶段，与真实失败无法区分。
			assertion, s, err := wa.BeginDiscoverableLogin()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			opts, session = assertion, s
		} else {
			wu, err := m.NewWebAuthnUser(c.Request.Context(), u, rpID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			assertion, s, err := wa.BeginLogin(wu)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			opts, session = assertion, s
		}
	}

	id, err := auth.NewCeremony(rpID, 0, session)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	setCeremonyCookie(c, id)
	c.JSON(http.StatusOK, opts)
}

type loginFinishRequest struct {
	Credential json.RawMessage `json:"credential"`
}

func (h *Handler) waLoginFinish(c *gin.Context) {
	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪"})
		return
	}
	rpID, ok := requireWebAuthn(c)
	if !ok {
		return
	}
	var req loginFinishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}
	session, _, err := takeCeremony(c, rpID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	wa, err := auth.InstanceFor(rpID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	var matched *auth.User

	cred, err := wa.FinishDiscoverableLogin(
		func(rawID, userHandle []byte) (webauthn.User, error) {
			u, err := m.FindUserByHandle(ctx, userHandle)
			if err != nil {
				return nil, err
			}
			if u.Disabled {
				return nil, auth.ErrUserDisabled
			}
			matched = u
			return m.NewWebAuthnUser(ctx, u, rpID)
		},
		*session, credentialRequest(c, req.Credential))

	if err != nil || matched == nil {
		m.Audit(ctx, auth.AuditEntry{
			Event: auth.EventWebAuthnFail, ClientIP: clientIPOrRemote(c),
			UserAgent: c.Request.UserAgent(), Detail: "断言校验失败",
		})
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Passkey 验证失败，请使用密码登录", "code": codeUnauth,
		})
		return
	}

	// 克隆检测。注意很多认证器（尤其云端同步的 passkey）按设计恒返回 0，
	// 把 0/0 当成异常会让这些用户直接登不上。
	stored := m.SignCountFor(rpID, cred.ID)
	if auth.ShouldTreatAsClone(stored, cred.Authenticator.SignCount, cred.Authenticator.CloneWarning) {
		mode := appconfig.Get().Auth.WebAuthn.CloneDetection
		logger.GetLogger().Warnf("[鉴权] 用户 %s 的认证器签名计数异常（可能被克隆），处理策略: %s",
			matched.Username, mode)
		m.Audit(ctx, auth.AuditEntry{
			Event: auth.EventWebAuthnClone, Username: matched.Username,
			ClientIP: clientIPOrRemote(c), Detail: "签名计数未前进",
		})
		if mode == "disable_credential" {
			_ = m.DeleteCredentialByCredID(ctx, rpID, cred.ID)
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "该认证器已被停用（检测到异常），请使用密码登录后重新注册",
			})
			return
		}
	}

	_ = m.UpdateCredentialUsage(ctx, rpID, cred.ID, cred.Authenticator.SignCount,
		cred.Authenticator.CloneWarning)

	// 经过用户验证（PIN / 生物识别）的 Passkey 本身已经是两个因素：
	// 持有认证器 + 知道 PIN 或生物特征。此时不该再要求 TOTP。
	// 注意 UserVerified 必须读**本次断言**的结果，不能读注册时存的值——
	// 同一个认证器这次有没有做 UV 是逐次变化的。
	cfg := appconfig.Get().Auth
	amr := []string{auth.AMRWebAuthn}
	uvSatisfies := cred.Flags.UserVerified && cfg.WebAuthn.Satisfies2FA
	if cred.Flags.UserVerified {
		amr = append(amr, auth.AMRUserVfy)
	}

	if matched.TOTPEnabled && cfg.TOTP.Enabled && !uvSatisfies {
		tok, _, err := m.IssueSession(matched, auth.StagePre, amr, preAuthTTL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "签发令牌失败"})
			return
		}
		setPreAuthCookie(c, tok)
		c.JSON(http.StatusOK, gin.H{"totp_required": true})
		return
	}

	tok, _, err := m.IssueSession(matched, auth.StageFull, amr, cfg.Session.TTL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "签发令牌失败"})
		return
	}
	clearPreAuthCookie(c)
	setSessionCookie(c, tok)
	m.Audit(ctx, auth.AuditEntry{
		Event: auth.EventWebAuthnOK, Username: matched.Username, Actor: matched.Username,
		ClientIP: clientIPOrRemote(c), UserAgent: c.Request.UserAgent(),
		Detail: "Passkey 登录",
	})
	m.FinishWebAuthnLogin(ctx, matched)
	c.JSON(http.StatusOK, gin.H{"user": toUserView(matched)})
}

// ---- 凭证管理 ----

func (h *Handler) waListCredentials(c *gin.Context) {
	m, u, ok := requireUser(c)
	if !ok {
		return
	}
	// 不按 RP ID 过滤：Profile 页要把其它域名下的凭证也列出来，
	// 标注"当前域名下不可用"并允许删除
	creds, err := auth.ListCredentials(c.Request.Context(), m.DB(), u.ID, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"credentials": creds, "count": len(creds)})
}

type renameCredentialRequest struct {
	Name string `json:"name"`
}

func (h *Handler) waRenameCredential(c *gin.Context) {
	m, u, ok := requireUser(c)
	if !ok {
		return
	}
	id := queryIntParam(c, "id")
	var req renameCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}
	if err := m.RenameCredential(c.Request.Context(), u.ID, id, req.Name); err != nil {
		c.JSON(credErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) waDeleteCredential(c *gin.Context) {
	m, u, ok := requireUser(c)
	if !ok {
		return
	}
	if err := m.DeleteCredential(c.Request.Context(), u.ID, queryIntParam(c, "id")); err != nil {
		c.JSON(credErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	m.Audit(c.Request.Context(), auth.AuditEntry{
		Event: auth.EventCredDelete, Username: u.Username, Actor: u.Username,
	})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func credErrStatus(err error) int {
	if errors.Is(err, auth.ErrCredentialNotFound) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

// ---- 仪式 Cookie ----

func setCeremonyCookie(c *gin.Context, id string) {
	cfg := appconfig.Get()
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     ceremonyCookieName,
		Value:    id,
		Path:     cfg.Auth.Session.CookiePath,
		MaxAge:   300,
		HttpOnly: true,
		Secure:   cfg.Server.TLS.Enabled,
		SameSite: cfg.Auth.Session.SameSiteMode(),
	})
}

func takeCeremony(c *gin.Context, rpID string) (*webauthn.SessionData, int64, error) {
	id, err := c.Cookie(ceremonyCookieName)
	if err != nil || id == "" {
		return nil, 0, auth.ErrCeremonyExpired
	}
	// 取完即删（在 auth.TakeCeremony 里做），并校验 rpID 与当前请求一致，
	// 防止在 A 域名发起的仪式被拿到 B 域名去完成
	return auth.TakeCeremonyWithID(id, rpID)
}

// credentialRequest 把 JSON 里的 credential 字段包成一个 http.Request，
// 因为 go-webauthn 的 Finish* 接口只接受 *http.Request。
func credentialRequest(c *gin.Context, raw json.RawMessage) *http.Request {
	r := c.Request.Clone(c.Request.Context())
	r.Body = io.NopCloser(bytes.NewReader(raw))
	r.ContentLength = int64(len(raw))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func queryIntParam(c *gin.Context, name string) int64 {
	var n int64
	for _, ch := range c.Param(name) {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int64(ch-'0')
	}
	return n
}
