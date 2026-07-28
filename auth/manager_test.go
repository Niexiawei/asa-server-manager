package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	m, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

func TestManagerInitializeMigratesAndLoads(t *testing.T) {
	m := testManager(t)

	if m.UserCount() != 0 {
		t.Errorf("全新环境应为零用户，实际 %d", m.UserCount())
	}
	v, err := CurrentVersion(m.DB())
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if v != LatestVersion() {
		t.Errorf("Initialize 应自动迁移到最新版本，实际 %d", v)
	}
	if GetManager() != m {
		t.Error("Initialize 应设置全局实例")
	}
}

func TestSessionRoundTrip(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()

	u, err := m.CreateUser(ctx, "admin", "correct-horse", RoleAdmin, ActorCLI)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	tok, claims, err := m.IssueSession(u, StageFull, []string{AMRPassword}, time.Hour)
	if err != nil {
		t.Fatalf("IssueSession: %v", err)
	}

	got, gotClaims, err := m.VerifySession(tok)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if got.Username != "admin" {
		t.Errorf("用户名不匹配: %q", got.Username)
	}
	if gotClaims.JTI != claims.JTI {
		t.Error("jti 不匹配")
	}
}

// 改密码后旧令牌必须立刻失效，否则密码泄露后改密码起不到任何作用。
func TestSessionRevokedByPasswordChange(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()

	u, _ := m.CreateUser(ctx, "admin", "correct-horse", RoleAdmin, ActorCLI)
	tok, _, _ := m.IssueSession(u, StageFull, nil, time.Hour)

	if _, _, err := m.VerifySession(tok); err != nil {
		t.Fatalf("前置条件失败: %v", err)
	}

	if err := m.ChangePassword(ctx, "admin", "a-brand-new-password", "admin", EventPasswordChange); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, _, err := m.VerifySession(tok); !errors.Is(err, ErrSessionRevoked) {
		t.Errorf("改密码后旧令牌应返回 ErrSessionRevoked，实际 %v", err)
	}
}

func TestRevokeAllSessions(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()

	u, _ := m.CreateUser(ctx, "admin", "correct-horse", RoleAdmin, ActorCLI)
	tok1, _, _ := m.IssueSession(u, StageFull, nil, time.Hour)
	tok2, _, _ := m.IssueSession(u, StageFull, nil, time.Hour)

	if err := m.RevokeAllSessions(ctx, "admin"); err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}
	for i, tok := range []string{tok1, tok2} {
		if _, _, err := m.VerifySession(tok); !errors.Is(err, ErrSessionRevoked) {
			t.Errorf("令牌 %d 应失效，实际 %v", i+1, err)
		}
	}
}

// 单设备登出只作废这一个令牌，同一账号在其他设备上的会话必须保留。
func TestRevokeSingleToken(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()

	u, _ := m.CreateUser(ctx, "admin", "correct-horse", RoleAdmin, ActorCLI)
	tok1, claims1, _ := m.IssueSession(u, StageFull, nil, time.Hour)
	tok2, _, _ := m.IssueSession(u, StageFull, nil, time.Hour)

	if err := m.RevokeToken(ctx, claims1); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if _, _, err := m.VerifySession(tok1); !errors.Is(err, ErrSessionRevoked) {
		t.Errorf("被吊销的令牌应失效，实际 %v", err)
	}
	if _, _, err := m.VerifySession(tok2); err != nil {
		t.Errorf("其他设备的会话不应受影响，实际 %v", err)
	}
}

func TestDisabledUserCannotUseSession(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()

	if _, err := m.CreateUser(ctx, "admin", "correct-horse", RoleAdmin, ActorCLI); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	u, _ := m.CreateUser(ctx, "bob", "correct-horse", RoleOperator, ActorCLI)
	tok, _, _ := m.IssueSession(u, StageFull, nil, time.Hour)

	if err := m.SetDisabled(ctx, "bob", true, "admin"); err != nil {
		t.Fatalf("SetDisabled: %v", err)
	}
	// 禁用会同时 bump session_version，所以这里报哪个错都算对，
	// 关键是绝不能校验通过
	if _, _, err := m.VerifySession(tok); err == nil {
		t.Error("被禁用账户的令牌必须失效")
	}
}

func TestAuthenticatePassword(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()

	if _, err := m.CreateUser(ctx, "admin", "correct-horse", RoleAdmin, ActorCLI); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	res, err := m.AuthenticatePassword(ctx, "admin", "correct-horse", "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("正确密码应通过: %v", err)
	}
	if res.TOTPRequired {
		t.Error("未绑定两步验证时不应要求第二步")
	}

	// 用户不存在和密码错误必须返回同一个错误，否则用户名可以被枚举
	_, errWrong := m.AuthenticatePassword(ctx, "admin", "wrong", "127.0.0.1", "test")
	_, errNoUser := m.AuthenticatePassword(ctx, "ghost", "whatever", "127.0.0.2", "test")
	if !errors.Is(errWrong, ErrInvalidCredentials) {
		t.Errorf("密码错误应返回 ErrInvalidCredentials，实际 %v", errWrong)
	}
	if !errors.Is(errNoUser, ErrInvalidCredentials) {
		t.Errorf("用户不存在也应返回 ErrInvalidCredentials（防用户名枚举），实际 %v", errNoUser)
	}
	if errWrong.Error() != errNoUser.Error() {
		t.Errorf("两种失败的错误信息必须完全一致，否则可据此枚举用户名：%q vs %q",
			errWrong, errNoUser)
	}
}

func TestAuthenticateLocksOut(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()

	if _, err := m.CreateUser(ctx, "admin", "correct-horse", RoleAdmin, ActorCLI); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	p := LimitParamsFromConfig()
	for range p.MaxFailures {
		if _, err := m.AuthenticatePassword(ctx, "admin", "wrong", "10.1.2.3", "test"); err == nil {
			t.Fatal("错误密码不应通过")
		}
	}

	// 锁定之后，即使密码正确也要拒绝
	_, err := m.AuthenticatePassword(ctx, "admin", "correct-horse", "10.1.2.3", "test")
	if !errors.Is(err, ErrLockedOut) {
		t.Errorf("达到失败阈值后应返回 ErrLockedOut，实际 %v", err)
	}

	// 管理员解锁后恢复
	if err := m.Unlock(ctx, "admin", ActorCLI); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if err := ClearFailures(ctx, m.DB(), ScopeIP, "10.1.2.3"); err != nil {
		t.Fatalf("ClearFailures: %v", err)
	}
	if _, err := m.AuthenticatePassword(ctx, "admin", "correct-horse", "10.1.2.3", "test"); err != nil {
		t.Errorf("解锁后应能正常登录，实际 %v", err)
	}
}

func TestAuthenticateWithTOTP(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()

	if _, err := m.CreateUser(ctx, "admin", "correct-horse", RoleAdmin, ActorCLI); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	key, _ := GenerateTOTPKey("test", "admin")
	if _, err := m.BindTOTP(ctx, "admin", key.Secret()); err != nil {
		t.Fatalf("BindTOTP: %v", err)
	}

	res, err := m.AuthenticatePassword(ctx, "admin", "correct-horse", "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("AuthenticatePassword: %v", err)
	}
	if !res.TOTPRequired {
		t.Fatal("绑定两步验证后必须要求第二步")
	}

	amr, err := m.CompleteTOTP(ctx, res.User, codeAt(t, key.Secret(), time.Now()), "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("CompleteTOTP: %v", err)
	}
	if len(amr) != 2 || amr[1] != AMRTOTP {
		t.Errorf("amr 应记录密码+两步验证，实际 %v", amr)
	}
}

func TestReloadPicksUpExternalChanges(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()

	if _, err := m.CreateUser(ctx, "admin", "correct-horse", RoleAdmin, ActorCLI); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// 模拟 CLI 在另一个进程里直接改库
	if _, err := m.DB().ExecContext(ctx,
		`INSERT INTO users(username, username_lower, password_hash, role, created_at)
		 VALUES('cli-made','cli-made','x','operator',1)`); err != nil {
		t.Fatalf("直接插入失败: %v", err)
	}

	if _, ok := m.Lookup("cli-made"); ok {
		t.Error("Reload 之前内存副本不该看到外部改动")
	}
	if err := m.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, ok := m.Lookup("cli-made"); !ok {
		t.Error("Reload 之后应看到外部改动 —— 这是 /api/auth/reload 的意义")
	}
}

func TestHousekeeping(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()

	// 过期的吊销记录应被清理
	if err := DenyToken(ctx, m.DB(), "old-jti", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("DenyToken: %v", err)
	}
	if err := DenyToken(ctx, m.DB(), "live-jti", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("DenyToken: %v", err)
	}
	if err := m.Housekeeping(ctx); err != nil {
		t.Fatalf("Housekeeping: %v", err)
	}

	deny, err := LoadDenylist(ctx, m.DB(), time.Now())
	if err != nil {
		t.Fatalf("LoadDenylist: %v", err)
	}
	if _, ok := deny["old-jti"]; ok {
		t.Error("已过期的吊销记录应被清掉")
	}
	if _, ok := deny["live-jti"]; !ok {
		t.Error("未过期的吊销记录应保留")
	}
}

func TestAuditTrim(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()

	for i := range 50 {
		if err := WriteAudit(ctx, m.DB(), AuditEntry{
			Event: EventLoginFail, Username: "bob", ClientIP: "1.2.3.4",
			Detail: string(rune('a' + i%26)),
		}); err != nil {
			t.Fatalf("WriteAudit: %v", err)
		}
	}

	n, err := TrimAudit(ctx, m.DB(), 10)
	if err != nil {
		t.Fatalf("TrimAudit: %v", err)
	}
	if n != 40 {
		t.Errorf("应删除 40 条，实际 %d", n)
	}

	entries, err := QueryAudit(ctx, m.DB(), AuditFilter{Limit: 100})
	if err != nil {
		t.Fatalf("QueryAudit: %v", err)
	}
	if len(entries) != 10 {
		t.Errorf("裁剪后应剩 10 条，实际 %d", len(entries))
	}
	// 保留的必须是最新的那些
	if entries[0].Timestamp.IsZero() {
		t.Error("时间戳未正确读回")
	}
}

func TestQueryAuditFilters(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()

	for _, e := range []AuditEntry{
		{Event: EventLoginOK, Username: "alice"},
		{Event: EventLoginFail, Username: "alice"},
		{Event: EventLoginOK, Username: "bob"},
	} {
		if err := WriteAudit(ctx, m.DB(), e); err != nil {
			t.Fatalf("WriteAudit: %v", err)
		}
	}

	got, err := QueryAudit(ctx, m.DB(), AuditFilter{Username: "alice"})
	if err != nil {
		t.Fatalf("QueryAudit: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("按用户名过滤应得 2 条，实际 %d", len(got))
	}

	got, err = QueryAudit(ctx, m.DB(), AuditFilter{Event: EventLoginOK})
	if err != nil {
		t.Fatalf("QueryAudit: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("按事件过滤应得 2 条，实际 %d", len(got))
	}
}
