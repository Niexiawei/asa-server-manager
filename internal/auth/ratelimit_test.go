package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func testParams() LimitParams {
	return LimitParams{MaxFailures: 3, Window: 15 * time.Minute, Lockout: 15 * time.Minute}
}

func TestRecordFailureLocksAfterThreshold(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	p := testParams()
	now := time.Now()

	for i := 1; i < p.MaxFailures; i++ {
		st, err := RecordFailure(ctx, db, ScopeUser, "bob", p, now)
		if err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
		if st.Locked {
			t.Fatalf("第 %d 次失败不应触发锁定（阈值 %d）", i, p.MaxFailures)
		}
	}

	st, err := RecordFailure(ctx, db, ScopeUser, "bob", p, now)
	if err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	if !st.Locked {
		t.Fatalf("第 %d 次失败应触发锁定", p.MaxFailures)
	}
	if st.Remaining <= 0 || st.Remaining > p.Lockout {
		t.Errorf("剩余锁定时长异常: %v", st.Remaining)
	}
}

// 这是选择 SQLite 而不是 JSON/内存的核心理由：如果攻击者只要等一次
// 服务重启（更新、崩溃恢复、手动重启在 Windows 服务上都是常态）
// 失败计数就清零，锁定机制等于不存在。
func TestLockoutSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	ctx := context.Background()
	p := testParams()
	now := time.Now()

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for range p.MaxFailures {
		if _, err := RecordFailure(ctx, db, ScopeUser, "bob", p, now); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
	}
	if st, _ := CheckLock(ctx, db, ScopeUser, "bob", now); !st.Locked {
		t.Fatal("前置条件失败：应已锁定")
	}
	db.Close()

	// 模拟服务重启
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("重新 Open: %v", err)
	}
	defer db2.Close()

	st, err := CheckLock(ctx, db2, ScopeUser, "bob", now)
	if err != nil {
		t.Fatalf("CheckLock: %v", err)
	}
	if !st.Locked {
		t.Error("重启后锁定必须仍然生效，否则重启就是免费的暴力破解重置")
	}
}

func TestLockExpires(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	p := testParams()
	now := time.Now()

	for range p.MaxFailures {
		if _, err := RecordFailure(ctx, db, ScopeUser, "bob", p, now); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
	}

	later := now.Add(p.Lockout + time.Second)
	st, err := CheckLock(ctx, db, ScopeUser, "bob", later)
	if err != nil {
		t.Fatalf("CheckLock: %v", err)
	}
	if st.Locked {
		t.Error("锁定期结束后应自动解锁")
	}

	// 解锁之后重新开始计数，不该被上一轮的失败次数拖累
	st, err = RecordFailure(ctx, db, ScopeUser, "bob", p, later)
	if err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	if st.Locked {
		t.Error("解锁后的第一次失败不应立刻再次锁定")
	}
	if st.FailCount != 1 {
		t.Errorf("解锁后计数应重新从 1 开始，实际 %d", st.FailCount)
	}
}

func TestWindowExpiryResetsCount(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	p := testParams()
	now := time.Now()

	if _, err := RecordFailure(ctx, db, ScopeUser, "bob", p, now); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	// 统计窗口滑过之后，早先那次失败不该再算数
	st, err := RecordFailure(ctx, db, ScopeUser, "bob", p, now.Add(p.Window+time.Minute))
	if err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	if st.FailCount != 1 {
		t.Errorf("窗口滑过后计数应重置为 1，实际 %d", st.FailCount)
	}
}

func TestScopesAreIndependent(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	p := testParams()
	now := time.Now()

	for range p.MaxFailures {
		if _, err := RecordFailure(ctx, db, ScopeUser, "bob", p, now); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
	}

	if st, _ := CheckLock(ctx, db, ScopeUser, "alice", now); st.Locked {
		t.Error("锁定 bob 不该影响 alice")
	}
	if st, _ := CheckLock(ctx, db, ScopeIP, "bob", now); st.Locked {
		t.Error("user 维度的锁定不该串到 ip 维度")
	}
}

// 换个大小写就能绕过锁定的话，限流就没意义了
func TestUserScopeIsCaseInsensitive(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	p := testParams()
	now := time.Now()

	for range p.MaxFailures {
		if _, err := RecordFailure(ctx, db, ScopeUser, "Bob", p, now); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
	}
	st, err := CheckLock(ctx, db, ScopeUser, "BOB", now)
	if err != nil {
		t.Fatalf("CheckLock: %v", err)
	}
	if !st.Locked {
		t.Error("用户名大小写不同不应绕过锁定")
	}
}

func TestClearFailures(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	p := testParams()
	now := time.Now()

	for range p.MaxFailures {
		if _, err := RecordFailure(ctx, db, ScopeUser, "bob", p, now); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
	}
	if err := ClearFailures(ctx, db, ScopeUser, "bob"); err != nil {
		t.Fatalf("ClearFailures: %v", err)
	}
	if st, _ := CheckLock(ctx, db, ScopeUser, "bob", now); st.Locked {
		t.Error("ClearFailures 之后不应还处于锁定状态")
	}
}
