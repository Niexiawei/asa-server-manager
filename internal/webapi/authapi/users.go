package authapi

import (
	"errors"
	"net/http"

	"asa-server/auth"

	"github.com/gin-gonic/gin"
)

// 本文件是管理员对其他账户的操作。所有路由都挂了 RequireAdmin()。

func (h *Handler) listUsers(c *gin.Context) {
	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪"})
		return
	}
	users := m.Users()
	// 只返回视图对象：密码哈希、TOTP 密钥、公钥一概不出网
	out := make([]*userView, 0, len(users))
	for _, u := range users {
		out = append(out, toUserView(u))
	}
	c.JSON(http.StatusOK, gin.H{"users": out, "count": len(out)})
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (h *Handler) createUser(c *gin.Context) {
	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪"})
		return
	}
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}
	if req.Role == "" {
		req.Role = auth.RoleOperator
	}
	// 密码必填：WebAuthn 只是补充，每个账户都必须能用密码登录，
	// 否则用户换个访问入口（IP、未配置的域名）就把自己锁在门外了
	if req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "必须设置密码"})
		return
	}

	u, err := m.CreateUser(c.Request.Context(), req.Username, req.Password, req.Role, ActorName(c))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, auth.ErrUserExists) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": toUserView(u)})
}

type updateUserRequest struct {
	Role     *string `json:"role,omitempty"`
	Disabled *bool   `json:"disabled,omitempty"`
}

func (h *Handler) updateUser(c *gin.Context) {
	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪"})
		return
	}
	name := c.Param("username")
	var req updateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}

	actor := ActorName(c)
	if req.Role != nil {
		if err := m.SetRole(c.Request.Context(), name, *req.Role, actor); err != nil {
			c.JSON(userErrStatus(err), gin.H{"error": err.Error()})
			return
		}
	}
	if req.Disabled != nil {
		// 禁用自己会立刻把自己踢下线，几乎肯定是误操作
		if *req.Disabled && isSelf(c, name) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "不能禁用当前登录的账户"})
			return
		}
		if err := m.SetDisabled(c.Request.Context(), name, *req.Disabled, actor); err != nil {
			c.JSON(userErrStatus(err), gin.H{"error": err.Error()})
			return
		}
	}

	u, _ := m.Lookup(name)
	c.JSON(http.StatusOK, gin.H{"user": toUserView(u)})
}

func (h *Handler) deleteUser(c *gin.Context) {
	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪"})
		return
	}
	name := c.Param("username")
	if isSelf(c, name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不能删除当前登录的账户"})
		return
	}
	if err := m.DeleteUser(c.Request.Context(), name, ActorName(c)); err != nil {
		c.JSON(userErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type resetPasswordRequest struct {
	Password string `json:"password"`
}

func (h *Handler) resetPassword(c *gin.Context) {
	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪"})
		return
	}
	var req resetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}
	name := c.Param("username")
	if err := m.ChangePassword(c.Request.Context(), name, req.Password,
		ActorName(c), auth.EventPasswordReset); err != nil {
		c.JSON(userErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "密码已重置，该用户所有设备已被登出"})
}

func (h *Handler) resetTOTP(c *gin.Context) {
	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪"})
		return
	}
	if err := m.ResetTOTP(c.Request.Context(), c.Param("username"), ActorName(c)); err != nil {
		c.JSON(userErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "已解绑两步验证"})
}

// resetWebAuthn 清空某用户的全部 Passkey。
// 因为密码始终可用，这个操作没有把人锁在门外的风险。
func (h *Handler) resetWebAuthn(c *gin.Context) {
	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪"})
		return
	}
	if err := m.DeleteAllCredentials(c.Request.Context(), c.Param("username"), ActorName(c)); err != nil {
		c.JSON(userErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "已清空该用户的全部 Passkey"})
}

func (h *Handler) unlockUser(c *gin.Context) {
	m := auth.GetManager()
	if m == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "鉴权模块未就绪"})
		return
	}
	if err := m.Unlock(c.Request.Context(), c.Param("username"), ActorName(c)); err != nil {
		c.JSON(userErrStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "已解除登录锁定"})
}

func isSelf(c *gin.Context, username string) bool {
	u := CurrentUser(c)
	return u != nil && equalFold(u.Username, username)
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func userErrStatus(err error) int {
	switch {
	case errors.Is(err, auth.ErrUserNotFound):
		return http.StatusNotFound
	case errors.Is(err, auth.ErrUserExists):
		return http.StatusConflict
	case errors.Is(err, auth.ErrLastAdmin):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}
