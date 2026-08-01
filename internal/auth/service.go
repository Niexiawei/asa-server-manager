package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"asa-server/internal/appconfig"

	"golang.org/x/crypto/bcrypt"
)

// 本文件是"业务动作"层：把数据库写入、内存副本刷新、审计记录三件事
// 绑在一起。所有会改动用户状态的路径都应该走这里，而不是直接调
// user.go 里的函数——否则内存副本会和数据库悄悄分叉。

// ErrInvalidCredentials 是密码错和用户不存在共用的错误。
// 两者必须返回同一个错误，否则攻击者可以据此枚举出哪些用户名真实存在。
var ErrInvalidCredentials = errors.New("用户名或密码错误")

// dummyHash 用于"用户不存在"时消耗掉与真实校验相当的时间。
//
// 没有它的话，不存在的用户会立刻返回，而存在的用户要等一次 bcrypt，
// 攻击者靠响应时间就能把用户名枚举出来。
//
// 必须是真实计算出来的哈希：写死一个字符串万一格式不对，bcrypt 会立即
// 报错返回，时间差反而更明显——那就把防护写成了漏洞。
var dummyHash = func() string {
	h, err := bcrypt.GenerateFromPassword([]byte("no-such-user"), bcrypt.DefaultCost)
	if err != nil {
		return ""
	}
	return string(h)
}()

// LoginResult 描述一次密码认证的结果
type LoginResult struct {
	User *User
	// TOTPRequired 为 true 时密码已经通过，但还需要第二步验证
	TOTPRequired bool
}

// AuthenticatePassword 校验用户名密码，并处理失败限流。
//
// 调用方拿到 ErrLockedOut 时应把剩余时间展示给用户；拿到 ErrInvalidCredentials
// 时**不要**区分"用户不存在"和"密码错误"。
func (m *Manager) AuthenticatePassword(ctx context.Context, username, password, clientIP, userAgent string) (*LoginResult, error) {
	now := time.Now()
	params := LimitParamsFromConfig()

	// 两个维度都要查：按 IP 挡住扫全站用户名的攻击，
	// 按用户名挡住从多个 IP 打同一个账户的攻击。
	for _, s := range []struct{ scope, key string }{
		{ScopeIP, clientIP},
		{ScopeUser, username},
	} {
		if s.key == "" {
			continue
		}
		st, err := CheckLock(ctx, m.db, s.scope, s.key, now)
		if err != nil {
			return nil, err
		}
		if st.Locked {
			return nil, fmt.Errorf("%w：请在 %d 分钟后重试",
				ErrLockedOut, int(st.Remaining.Minutes())+1)
		}
	}

	u, found := m.Lookup(username)
	hash := dummyHash
	if found {
		hash = u.PasswordHash
	}
	ok := VerifyPassword(hash, password)

	if !found || !ok || u.Disabled {
		m.recordLoginFailure(ctx, username, clientIP, userAgent, params, now)
		if found && u.Disabled && ok {
			return nil, ErrUserDisabled
		}
		return nil, ErrInvalidCredentials
	}

	if err := ClearFailures(ctx, m.db, ScopeUser, username); err != nil {
		return nil, err
	}
	if err := ClearFailures(ctx, m.db, ScopeIP, clientIP); err != nil {
		return nil, err
	}

	cfg := appconfig.Get().Auth
	needTOTP := cfg.TOTP.Enabled && u.TOTPEnabled
	if !needTOTP {
		m.finishLogin(ctx, u, clientIP, userAgent, "密码登录")
	}
	return &LoginResult{User: u, TOTPRequired: needTOTP}, nil
}

// CompleteTOTP 校验两步验证的第二步。code 可以是 6 位验证码，也可以是恢复码。
// 返回本次登录用到的认证手段，供写进令牌的 amr 字段。
func (m *Manager) CompleteTOTP(ctx context.Context, u *User, code, clientIP, userAgent string) ([]string, error) {
	if !u.TOTPEnabled {
		return nil, ErrTOTPNotEnabled
	}
	params := LimitParamsFromConfig()
	now := time.Now()

	if LooksLikeRecoveryCode(code) {
		if err := ConsumeRecoveryCode(ctx, m.db, u.Username, code); err != nil {
			m.recordTOTPFailure(ctx, u.Username, clientIP, userAgent, params, now, "恢复码无效")
			return nil, err
		}
		m.finishLogin(ctx, u, clientIP, userAgent, "使用恢复码登录")
		return []string{AMRPassword, AMRRecovery}, nil
	}

	cfg := appconfig.Get().Auth.TOTP
	step, err := ValidateTOTP(u.TOTPSecret, code, cfg.Skew, u.TOTPLastStep, now)
	if err != nil {
		m.recordTOTPFailure(ctx, u.Username, clientIP, userAgent, params, now, err.Error())
		return nil, err
	}
	if err := RecordTOTPStep(ctx, m.db, u.Username, step); err != nil {
		return nil, err
	}
	if err := m.Reload(ctx); err != nil {
		return nil, err
	}

	_ = ClearFailures(ctx, m.db, ScopeUser, u.Username)
	_ = ClearFailures(ctx, m.db, ScopeIP, clientIP)
	m.finishLogin(ctx, u, clientIP, userAgent, "两步验证登录")
	return []string{AMRPassword, AMRTOTP}, nil
}

func (m *Manager) finishLogin(ctx context.Context, u *User, clientIP, userAgent, detail string) {
	_ = TouchLastLogin(ctx, m.db, u.Username)
	m.Audit(ctx, AuditEntry{
		Event: EventLoginOK, Username: u.Username, Actor: u.Username,
		ClientIP: clientIP, UserAgent: userAgent, Detail: detail,
	})
	_ = m.Reload(ctx)
}

func (m *Manager) recordLoginFailure(ctx context.Context, username, clientIP, userAgent string, p LimitParams, now time.Time) {
	if clientIP != "" {
		_, _ = RecordFailure(ctx, m.db, ScopeIP, clientIP, p, now)
	}
	if username != "" {
		_, _ = RecordFailure(ctx, m.db, ScopeUser, username, p, now)
	}
	m.Audit(ctx, AuditEntry{
		Event: EventLoginFail, Username: username,
		ClientIP: clientIP, UserAgent: userAgent, Detail: "密码校验失败",
	})
}

func (m *Manager) recordTOTPFailure(ctx context.Context, username, clientIP, userAgent string, p LimitParams, now time.Time, detail string) {
	if clientIP != "" {
		_, _ = RecordFailure(ctx, m.db, ScopeIP, clientIP, p, now)
	}
	if username != "" {
		_, _ = RecordFailure(ctx, m.db, ScopeUser, username, p, now)
	}
	m.Audit(ctx, AuditEntry{
		Event: EventTOTPFail, Username: username,
		ClientIP: clientIP, UserAgent: userAgent, Detail: detail,
	})
}

// ---- 用户管理（写库 + 刷新内存副本 + 审计，三件事绑在一起）----

// CreateUser 创建账户。password 为明文，会在这里完成校验和哈希。
func (m *Manager) CreateUser(ctx context.Context, username, password, role, actor string) (*User, error) {
	cfg := appconfig.Get().Auth.Password
	if err := ValidatePasswordStrength(password, cfg.MinLength); err != nil {
		return nil, err
	}
	hash, err := HashPassword(password, cfg.BcryptCost)
	if err != nil {
		return nil, err
	}
	u, err := CreateUser(ctx, m.db, username, hash, role)
	if err != nil {
		return nil, err
	}
	if err := m.Reload(ctx); err != nil {
		return nil, err
	}
	m.Audit(ctx, AuditEntry{
		Event: EventUserCreate, Username: username, Actor: actor,
		Detail: "角色 " + role,
	})
	return u, nil
}

// ChangePassword 修改密码。会踢掉该用户所有已登录设备，并解除锁定。
func (m *Manager) ChangePassword(ctx context.Context, username, newPassword, actor, event string) error {
	cfg := appconfig.Get().Auth.Password
	if err := ValidatePasswordStrength(newPassword, cfg.MinLength); err != nil {
		return err
	}
	hash, err := HashPassword(newPassword, cfg.BcryptCost)
	if err != nil {
		return err
	}
	if err := SetPassword(ctx, m.db, username, hash); err != nil {
		return err
	}
	if err := m.Reload(ctx); err != nil {
		return err
	}
	m.Audit(ctx, AuditEntry{Event: event, Username: username, Actor: actor})
	return nil
}

// SetRole 修改角色
func (m *Manager) SetRole(ctx context.Context, username, role, actor string) error {
	if err := SetRole(ctx, m.db, username, role); err != nil {
		return err
	}
	if err := m.Reload(ctx); err != nil {
		return err
	}
	m.Audit(ctx, AuditEntry{
		Event: EventUserUpdate, Username: username, Actor: actor,
		Detail: "角色改为 " + role,
	})
	return nil
}

// SetDisabled 启用/禁用账户
func (m *Manager) SetDisabled(ctx context.Context, username string, disabled bool, actor string) error {
	if err := SetDisabled(ctx, m.db, username, disabled); err != nil {
		return err
	}
	if err := m.Reload(ctx); err != nil {
		return err
	}
	detail := "启用账户"
	if disabled {
		detail = "禁用账户"
	}
	m.Audit(ctx, AuditEntry{Event: EventUserUpdate, Username: username, Actor: actor, Detail: detail})
	return nil
}

// DeleteUser 删除账户
func (m *Manager) DeleteUser(ctx context.Context, username, actor string) error {
	if err := DeleteUser(ctx, m.db, username); err != nil {
		return err
	}
	if err := m.Reload(ctx); err != nil {
		return err
	}
	m.Audit(ctx, AuditEntry{Event: EventUserDelete, Username: username, Actor: actor})
	return nil
}

// Unlock 清除该用户的登录失败锁定
func (m *Manager) Unlock(ctx context.Context, username, actor string) error {
	if err := ClearFailures(ctx, m.db, ScopeUser, username); err != nil {
		return err
	}
	m.Audit(ctx, AuditEntry{Event: EventUserUnlock, Username: username, Actor: actor})
	return nil
}

// ResetTOTP 管理员解绑某用户的两步验证（用户丢手机时的救援路径）
func (m *Manager) ResetTOTP(ctx context.Context, username, actor string) error {
	if err := DisableTOTP(ctx, m.db, username); err != nil {
		return err
	}
	if err := m.Reload(ctx); err != nil {
		return err
	}
	m.Audit(ctx, AuditEntry{Event: EventTOTPReset, Username: username, Actor: actor})
	return nil
}

// BindTOTP 在用户确认扫码成功后写入密钥，并生成一批恢复码（明文只返回这一次）
func (m *Manager) BindTOTP(ctx context.Context, username, secret string) ([]string, error) {
	if err := EnableTOTP(ctx, m.db, username, secret); err != nil {
		return nil, err
	}
	codes, err := GenerateRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	// 恢复码是高熵随机串，不需要和用户密码同等的哈希成本
	if err := ReplaceRecoveryCodes(ctx, m.db, username, codes, 10); err != nil {
		return nil, err
	}
	if err := m.Reload(ctx); err != nil {
		return nil, err
	}
	m.Audit(ctx, AuditEntry{Event: EventTOTPBind, Username: username, Actor: username})
	return codes, nil
}

// RegenerateRecoveryCodes 重新生成恢复码，旧的全部作废
func (m *Manager) RegenerateRecoveryCodes(ctx context.Context, username string) ([]string, error) {
	codes, err := GenerateRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	if err := ReplaceRecoveryCodes(ctx, m.db, username, codes, 10); err != nil {
		return nil, err
	}
	return codes, nil
}
