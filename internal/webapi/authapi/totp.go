package authapi

import (
	"net/http"
	"sync"
	"time"

	"asa-server/internal/appconfig"
	"asa-server/internal/auth"

	"github.com/gin-gonic/gin"
)

// 绑定两步验证时生成的密钥先不落库，暂存在内存里等用户确认。
//
// 如果一生成就写进数据库，用户扫码失败（换了手机、扫错了、中途关掉页面）
// 却已经被强制开启两步验证，下次登录就把自己锁在外面了。
type pendingTOTP struct {
	secret  string
	expires time.Time
}

var pendingTOTPs sync.Map // key: 小写用户名 -> *pendingTOTP

const totpSetupTTL = 5 * time.Minute

func (h *Handler) totpSetup(c *gin.Context) {
	_, u, ok := requireUser(c)
	if !ok {
		return
	}
	if !appconfig.Get().Auth.TOTP.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "两步验证功能未启用"})
		return
	}

	cfg := appconfig.Get().Auth.TOTP
	key, err := auth.GenerateTOTPKey(cfg.Issuer, u.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	qr, err := auth.TOTPQRCodeBase64(key, 256)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	pendingTOTPs.Store(lower(u.Username), &pendingTOTP{
		secret:  key.Secret(),
		expires: time.Now().Add(totpSetupTTL),
	})

	c.JSON(http.StatusOK, gin.H{
		"secret":         key.Secret(),
		"otpauth_url":    key.URL(),
		"qr_png_base64":  qr,
		"expires_in_sec": int(totpSetupTTL.Seconds()),
	})
}

func (h *Handler) totpConfirm(c *gin.Context) {
	m, u, ok := requireUser(c)
	if !ok {
		return
	}
	var req totpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}

	v, found := pendingTOTPs.Load(lower(u.Username))
	if !found {
		c.JSON(http.StatusBadRequest, gin.H{"error": "绑定流程已过期，请重新开始"})
		return
	}
	p := v.(*pendingTOTP)
	if time.Now().After(p.expires) {
		pendingTOTPs.Delete(lower(u.Username))
		c.JSON(http.StatusBadRequest, gin.H{"error": "绑定流程已过期，请重新开始"})
		return
	}

	skew := appconfig.Get().Auth.TOTP.Skew
	if _, err := auth.ValidateTOTP(p.secret, req.Code, skew, 0, time.Now()); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "验证码不正确。若反复失败，请检查服务器系统时间是否准确",
		})
		return
	}

	codes, err := m.BindTOTP(c.Request.Context(), u.Username, p.secret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	pendingTOTPs.Delete(lower(u.Username))

	// 恢复码明文只在这一次返回，之后数据库里只有哈希
	c.JSON(http.StatusOK, gin.H{
		"success":        true,
		"recovery_codes": codes,
		"message":        "两步验证已启用。请妥善保存恢复码 —— 它们不会再次显示。",
	})
}

type totpDisableRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

func (h *Handler) totpDisable(c *gin.Context) {
	m, u, ok := requireUser(c)
	if !ok {
		return
	}
	if !u.TOTPEnabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "尚未启用两步验证"})
		return
	}
	var req totpDisableRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}
	// 解绑要同时验证密码和验证码：只验其一的话，一个被临时接管的会话
	// 就足以把二次验证摘掉
	if !auth.VerifyPassword(u.PasswordHash, req.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "密码不正确"})
		return
	}
	skew := appconfig.Get().Auth.TOTP.Skew
	if _, err := auth.ValidateTOTP(u.TOTPSecret, req.Code, skew, u.TOTPLastStep, time.Now()); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "验证码不正确"})
		return
	}

	if err := m.ResetTOTP(c.Request.Context(), u.Username, u.Username); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "已解绑两步验证"})
}

func (h *Handler) regenerateRecovery(c *gin.Context) {
	m, u, ok := requireUser(c)
	if !ok {
		return
	}
	if !u.TOTPEnabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "尚未启用两步验证"})
		return
	}
	codes, err := m.RegenerateRecoveryCodes(c.Request.Context(), u.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"recovery_codes": codes,
		"message":        "旧的恢复码已全部作废。请妥善保存新的恢复码。",
	})
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if 'A' <= b[i] && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
