package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 登录失败计数落在数据库里，不放内存。
//
// 放内存的话，攻击者只要等一次服务重启（更新、崩溃恢复、手动重启，在 Windows
// 服务上都是常态）失败计数就清零了，锁定机制等于不存在。这是选择 SQLite
// 而不是 JSON 文件的核心理由之一。
const (
	ScopeIP   = "ip"
	ScopeUser = "user"
)

// ErrLockedOut 表示当前主体处于锁定期
var ErrLockedOut = errors.New("登录尝试过于频繁，账户已被临时锁定")

// LimitParams 是限流参数，由调用方从配置里取，保持本层可独立测试
type LimitParams struct {
	MaxFailures int
	Window      time.Duration
	Lockout     time.Duration
}

// LockStatus 描述某个主体当前的锁定情况
type LockStatus struct {
	Locked    bool
	Until     time.Time
	Remaining time.Duration
	FailCount int
}

// CheckLock 查询锁定状态。已过期的锁定视为未锁定。
func CheckLock(ctx context.Context, q queryer, scope, key string, now time.Time) (LockStatus, error) {
	key = normalizeKey(scope, key)

	var failCount int
	var lockedUntil int64
	err := q.QueryRowContext(ctx,
		`SELECT fail_count, locked_until FROM login_failures WHERE scope = ? AND key = ?`,
		scope, key).Scan(&failCount, &lockedUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return LockStatus{}, nil
	}
	if err != nil {
		return LockStatus{}, fmt.Errorf("查询登录失败计数失败: %w", err)
	}

	if lockedUntil > now.Unix() {
		until := time.Unix(lockedUntil, 0)
		return LockStatus{
			Locked:    true,
			Until:     until,
			Remaining: until.Sub(now),
			FailCount: failCount,
		}, nil
	}
	return LockStatus{FailCount: failCount}, nil
}

// RecordFailure 记一次失败并返回记完之后的锁定状态。
func RecordFailure(ctx context.Context, db *sql.DB, scope, key string, p LimitParams, now time.Time) (LockStatus, error) {
	key = normalizeKey(scope, key)

	var status LockStatus
	err := inTx(ctx, db, func(tx *sql.Tx) error {
		var failCount int
		var firstFail, lockedUntil int64
		err := tx.QueryRowContext(ctx,
			`SELECT fail_count, first_fail, locked_until FROM login_failures WHERE scope = ? AND key = ?`,
			scope, key).Scan(&failCount, &firstFail, &lockedUntil)

		switch {
		case errors.Is(err, sql.ErrNoRows):
			failCount, firstFail, lockedUntil = 0, now.Unix(), 0
		case err != nil:
			return fmt.Errorf("查询登录失败计数失败: %w", err)
		}

		// 统计窗口滑过，或上一轮锁定已经结束 —— 两种情况都从头开始计数，
		// 免得用户被"上个月那几次失败"一直拖累。
		windowExpired := now.Unix() >= firstFail+int64(p.Window.Seconds())
		lockExpired := lockedUntil != 0 && now.Unix() >= lockedUntil
		if windowExpired || lockExpired {
			failCount, firstFail, lockedUntil = 0, now.Unix(), 0
		}

		failCount++
		if failCount >= p.MaxFailures {
			lockedUntil = now.Add(p.Lockout).Unix()
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO login_failures(scope, key, fail_count, first_fail, locked_until)
			 VALUES(?, ?, ?, ?, ?)
			 ON CONFLICT(scope, key) DO UPDATE SET
			   fail_count = excluded.fail_count,
			   first_fail = excluded.first_fail,
			   locked_until = excluded.locked_until`,
			scope, key, failCount, firstFail, lockedUntil); err != nil {
			return fmt.Errorf("写入登录失败计数失败: %w", err)
		}

		status.FailCount = failCount
		if lockedUntil > now.Unix() {
			until := time.Unix(lockedUntil, 0)
			status.Locked = true
			status.Until = until
			status.Remaining = until.Sub(now)
		}
		return nil
	})
	return status, err
}

// ClearFailures 清除某个主体的失败计数与锁定。
// 登录成功、管理员解锁、重置密码时调用。
func ClearFailures(ctx context.Context, q queryer, scope, key string) error {
	_, err := q.ExecContext(ctx,
		`DELETE FROM login_failures WHERE scope = ? AND key = ?`,
		scope, normalizeKey(scope, key))
	if err != nil {
		return fmt.Errorf("清除登录失败计数失败: %w", err)
	}
	return nil
}

func clearFailuresTx(ctx context.Context, tx *sql.Tx, username string) error {
	return ClearFailures(ctx, tx, ScopeUser, username)
}

// ClearScopeFailures 清空某一维度的全部锁定，返回清掉的条数。
//
// 供本机 CLI 救援使用：单管理员的服务器上，用户维度和 IP 维度总是同时被锁
// （所有失败尝试都来自同一台机器），只解开用户维度等于没解。
func ClearScopeFailures(ctx context.Context, q queryer, scope string) (int64, error) {
	res, err := q.ExecContext(ctx, `DELETE FROM login_failures WHERE scope = ?`, scope)
	if err != nil {
		return 0, fmt.Errorf("清除登录失败计数失败: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// normalizeKey 让用户名的大小写不影响计数归属，否则换个大小写就能绕过锁定。
func normalizeKey(scope, key string) string {
	if scope == ScopeUser {
		return strings.ToLower(strings.TrimSpace(key))
	}
	return strings.TrimSpace(key)
}
