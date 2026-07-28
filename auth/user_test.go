package auth

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func mustCreate(t *testing.T, db *sql.DB, name, role string) *User {
	t.Helper()
	hash, err := HashPassword("correct-horse", 4) // 测试里用最低成本，快
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	u, err := CreateUser(context.Background(), db, name, hash, role)
	if err != nil {
		t.Fatalf("CreateUser(%s): %v", name, err)
	}
	return u
}

func TestCreateAndGetUser(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	mustCreate(t, db, "Admin", RoleAdmin)

	// 查找必须大小写不敏感
	for _, q := range []string{"Admin", "admin", "ADMIN", " admin "} {
		u, err := GetUser(ctx, db, q)
		if err != nil {
			t.Errorf("GetUser(%q): %v", q, err)
			continue
		}
		if u.Username != "Admin" {
			t.Errorf("GetUser(%q) 返回用户名 %q，期望保留原始大小写 Admin", q, u.Username)
		}
	}

	if _, err := GetUser(ctx, db, "nobody"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("查询不存在的用户应返回 ErrUserNotFound，实际 %v", err)
	}
}

func TestCreateUserRejectsDuplicate(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	mustCreate(t, db, "admin", RoleAdmin)

	hash, _ := HashPassword("x", 4)
	// 大小写不同也算重复
	if _, err := CreateUser(ctx, db, "ADMIN", hash, RoleOperator); !errors.Is(err, ErrUserExists) {
		t.Errorf("重名（忽略大小写）应返回 ErrUserExists，实际 %v", err)
	}
}

func TestCreateUserValidatesInput(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	hash, _ := HashPassword("x", 4)

	for _, name := range []string{"ab", "有中文", "with space", "toolong-" + string(make([]byte, 40))} {
		if _, err := CreateUser(ctx, db, name, hash, RoleAdmin); !errors.Is(err, ErrInvalidName) {
			t.Errorf("用户名 %q 应被拒绝，实际 %v", name, err)
		}
	}
	if _, err := CreateUser(ctx, db, "valid", hash, "superuser"); !errors.Is(err, ErrInvalidRole) {
		t.Error("非法角色应被拒绝")
	}
	// 每个账户恒有密码，空哈希任何路径都不接受
	if _, err := CreateUser(ctx, db, "valid", "", RoleAdmin); !errors.Is(err, ErrPasswordEmpty) {
		t.Errorf("空密码哈希应被拒绝，实际 %v", err)
	}
}

// 系统必须始终保留至少一个可用的管理员，否则谁也管不了用户了。
// 三条路径（删除、禁用、降级）都要守住。
func TestLastAdminIsProtected(t *testing.T) {
	ctx := context.Background()

	t.Run("不能删除", func(t *testing.T) {
		db := migratedDB(t)
		mustCreate(t, db, "admin", RoleAdmin)
		mustCreate(t, db, "bob", RoleOperator)
		if err := DeleteUser(ctx, db, "admin"); !errors.Is(err, ErrLastAdmin) {
			t.Errorf("应返回 ErrLastAdmin，实际 %v", err)
		}
	})

	t.Run("不能禁用", func(t *testing.T) {
		db := migratedDB(t)
		mustCreate(t, db, "admin", RoleAdmin)
		if err := SetDisabled(ctx, db, "admin", true); !errors.Is(err, ErrLastAdmin) {
			t.Errorf("应返回 ErrLastAdmin，实际 %v", err)
		}
	})

	t.Run("不能降级", func(t *testing.T) {
		db := migratedDB(t)
		mustCreate(t, db, "admin", RoleAdmin)
		if err := SetRole(ctx, db, "admin", RoleOperator); !errors.Is(err, ErrLastAdmin) {
			t.Errorf("应返回 ErrLastAdmin，实际 %v", err)
		}
	})

	t.Run("还有别的管理员时可以", func(t *testing.T) {
		db := migratedDB(t)
		mustCreate(t, db, "admin", RoleAdmin)
		mustCreate(t, db, "admin2", RoleAdmin)
		if err := DeleteUser(ctx, db, "admin"); err != nil {
			t.Errorf("存在第二个管理员时应允许删除，实际 %v", err)
		}
	})

	// 被禁用的管理员不算数：只剩一个"启用的管理员"时仍要保护
	t.Run("被禁用的管理员不计入", func(t *testing.T) {
		db := migratedDB(t)
		mustCreate(t, db, "admin", RoleAdmin)
		mustCreate(t, db, "admin2", RoleAdmin)
		if err := SetDisabled(ctx, db, "admin2", true); err != nil {
			t.Fatalf("禁用第二个管理员失败: %v", err)
		}
		if err := DeleteUser(ctx, db, "admin"); !errors.Is(err, ErrLastAdmin) {
			t.Errorf("被禁用的管理员不该被算作可用管理员，实际 %v", err)
		}
	})
}

// 改密码必须让旧令牌立刻失效，否则密码泄露后改密码起不到任何作用。
func TestSetPasswordBumpsVersionAndClearsLock(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	mustCreate(t, db, "bob", RoleOperator)

	p := testParams()
	for range p.MaxFailures {
		if _, err := RecordFailure(ctx, db, ScopeUser, "bob", p, time.Now()); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
	}

	before, _ := GetUser(ctx, db, "bob")
	newHash, _ := HashPassword("brand-new-password", 4)
	if err := SetPassword(ctx, db, "bob", newHash); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	after, _ := GetUser(ctx, db, "bob")
	if after.SessionVersion != before.SessionVersion+1 {
		t.Errorf("改密码后 session_version 应 +1（%d -> %d）", before.SessionVersion, after.SessionVersion)
	}
	if after.PasswordHash != newHash {
		t.Error("密码哈希未更新")
	}
	if st, _ := CheckLock(ctx, db, ScopeUser, "bob", time.Now()); st.Locked {
		t.Error("重置密码应顺带解除锁定")
	}
}

func TestSetDisabledRevokesSessions(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	mustCreate(t, db, "admin", RoleAdmin)
	mustCreate(t, db, "bob", RoleOperator)

	before, _ := GetUser(ctx, db, "bob")
	if err := SetDisabled(ctx, db, "bob", true); err != nil {
		t.Fatalf("SetDisabled: %v", err)
	}
	after, _ := GetUser(ctx, db, "bob")

	if !after.Disabled {
		t.Error("账户应处于禁用状态")
	}
	// 否则被禁用的人在令牌自然过期前还能继续操作
	if after.SessionVersion != before.SessionVersion+1 {
		t.Error("禁用账户应同时踢掉其已有会话")
	}
}

func TestCountUsers(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	n, err := CountUsers(ctx, db)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 0 {
		t.Errorf("全新数据库用户数应为 0，实际 %d", n)
	}

	mustCreate(t, db, "admin", RoleAdmin)
	if n, _ = CountUsers(ctx, db); n != 1 {
		t.Errorf("应有 1 个用户，实际 %d", n)
	}
}

func TestPasswordHashAndVerify(t *testing.T) {
	h, err := HashPassword("correct-horse-battery", 4)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !VerifyPassword(h, "correct-horse-battery") {
		t.Error("正确密码应校验通过")
	}
	if VerifyPassword(h, "wrong") {
		t.Error("错误密码不应通过")
	}
	if VerifyPassword("", "x") || VerifyPassword(h, "") {
		t.Error("空哈希或空密码一律不通过")
	}
	if _, err := HashPassword("", 4); !errors.Is(err, ErrPasswordEmpty) {
		t.Error("空密码不应被哈希")
	}
}

// bcrypt 只取前 72 字节。静默截断会导致两个不同的长密码都能登进同一个账户，
// 所以必须直接拒绝而不是截断。
func TestPasswordLengthLimits(t *testing.T) {
	long := make([]byte, 100)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := HashPassword(string(long), 4); err == nil {
		t.Error("超过 bcrypt 72 字节上限的密码应被拒绝，而不是静默截断")
	}
	if err := ValidatePasswordStrength(string(long), 8); err == nil {
		t.Error("强度校验也应拦下超长密码")
	}

	if err := ValidatePasswordStrength("short", 8); !errors.Is(err, ErrPasswordTooShort) {
		t.Error("过短密码应被拒绝")
	}
	// 按字符数而非字节数计，否则中文口令会被误判为过短
	if err := ValidatePasswordStrength("中文口令测试八字", 8); err != nil {
		t.Errorf("8 个中文字符应满足 min_length=8，实际 %v", err)
	}
}

func TestGeneratePassword(t *testing.T) {
	seen := map[string]bool{}
	for range 20 {
		p, err := GeneratePassword(20)
		if err != nil {
			t.Fatalf("GeneratePassword: %v", err)
		}
		if len(p) != 20 {
			t.Errorf("长度应为 20，实际 %d", len(p))
		}
		if seen[p] {
			t.Error("生成了重复密码")
		}
		seen[p] = true
		// 排除易混淆字符，否则用户从终端抄到浏览器时容易抄错
		for _, bad := range []string{"0", "O", "1", "l", "I"} {
			if contains(p, bad) {
				t.Errorf("密码 %q 含易混淆字符 %q", p, bad)
			}
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
