package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// 角色只有两种。这是单机管理面板，不是多租户 SaaS——
// 细粒度 RBAC 的复杂度远超它能带来的收益。
const (
	RoleAdmin    = "admin"    // 可管理用户，可做一切操作
	RoleOperator = "operator" // 可操作服务器，不能管用户
)

var (
	ErrUserNotFound  = errors.New("用户不存在")
	ErrUserExists    = errors.New("用户名已被占用")
	ErrUserDisabled  = errors.New("账户已被禁用")
	ErrInvalidRole   = errors.New("角色无效")
	ErrLastAdmin     = errors.New("系统必须保留至少一个启用状态的管理员")
	ErrInvalidName   = errors.New("用户名格式不合法")
	ErrSelfOperation = errors.New("不能对自己执行该操作")
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,32}$`)

// User 是一个登录账户。
//
// PasswordHash 恒非空：任何账户都必须能用密码登录。
type User struct {
	ID             int64
	Username       string
	PasswordHash   string
	Role           string
	SessionVersion int
	TOTPEnabled    bool
	TOTPSecret     string
	TOTPLastStep   int64
	Disabled       bool
	CreatedAt      time.Time
	LastLoginAt    time.Time
}

// IsAdmin 判断是否为管理员
func (u *User) IsAdmin() bool { return u != nil && u.Role == RoleAdmin }

// queryer 让同一份 SQL 既能直接在 *sql.DB 上跑，也能在事务里跑。
// 不变量（比如"至少保留一个管理员"）必须和它保护的写操作在同一个事务里，
// 否则并发下两个请求会各自认为"还有另一个管理员"，把最后一个也删掉。
type queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const userColumns = `id, username, password_hash, role, session_version,
	totp_enabled, totp_secret, totp_last_step, disabled, created_at, last_login_at`

// ValidateUsername 校验用户名格式
func ValidateUsername(name string) error {
	if !usernamePattern.MatchString(name) {
		return fmt.Errorf("%w：只允许字母、数字、下划线和连字符，长度 3-32", ErrInvalidName)
	}
	return nil
}

// ValidateRole 校验角色取值
func ValidateRole(role string) error {
	if role != RoleAdmin && role != RoleOperator {
		return fmt.Errorf("%w：只能是 %s 或 %s", ErrInvalidRole, RoleAdmin, RoleOperator)
	}
	return nil
}

// CreateUser 创建用户。passwordHash 必须已经算好且非空。
func CreateUser(ctx context.Context, q queryer, username, passwordHash, role string) (*User, error) {
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := ValidateRole(role); err != nil {
		return nil, err
	}
	if passwordHash == "" {
		return nil, ErrPasswordEmpty
	}

	now := time.Now().Unix()
	res, err := q.ExecContext(ctx,
		`INSERT INTO users(username, username_lower, password_hash, role, created_at)
		 VALUES(?, ?, ?, ?, ?)`,
		username, strings.ToLower(username), passwordHash, role, now)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrUserExists
		}
		return nil, fmt.Errorf("创建用户失败: %w", err)
	}
	id, _ := res.LastInsertId()

	return &User{
		ID:             id,
		Username:       username,
		PasswordHash:   passwordHash,
		Role:           role,
		SessionVersion: 1,
		CreatedAt:      time.Unix(now, 0),
	}, nil
}

// GetUser 按用户名查找（大小写不敏感）
func GetUser(ctx context.Context, q queryer, username string) (*User, error) {
	row := q.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE username_lower = ?`,
		strings.ToLower(strings.TrimSpace(username)))
	return scanUser(row)
}

// ListUsers 返回全部用户，按用户名排序
func ListUsers(ctx context.Context, q queryer) ([]*User, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY username_lower`)
	if err != nil {
		return nil, fmt.Errorf("查询用户列表失败: %w", err)
	}
	defer rows.Close()

	var out []*User
	for rows.Next() {
		u, err := scanUserRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountUsers 返回用户总数。为 0 时系统处于"零用户"状态，需要走首次引导。
func CountUsers(ctx context.Context, q queryer) (int, error) {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("统计用户数失败: %w", err)
	}
	return n, nil
}

// countActiveAdmins 统计启用状态的管理员数量。
// 只在事务内调用，用于守住"至少一个管理员"的不变量。
func countActiveAdmins(ctx context.Context, q queryer, excludeID int64) (int, error) {
	var n int
	err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE role = ? AND disabled = 0 AND id != ?`,
		RoleAdmin, excludeID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计管理员数量失败: %w", err)
	}
	return n, nil
}

// SetPassword 重置密码。
//
// 三件事必须在同一个事务里完成，缺一不可：
//  1. 写入新哈希
//  2. session_version++ —— 否则改完密码，旧密码签发的令牌还能继续用
//  3. 清除该用户的登录失败锁定 —— 管理员重置密码后还让人被锁着不合直觉
func SetPassword(ctx context.Context, db *sql.DB, username, passwordHash string) error {
	if passwordHash == "" {
		return ErrPasswordEmpty
	}
	return inTx(ctx, db, func(tx *sql.Tx) error {
		u, err := GetUser(ctx, tx, username)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET password_hash = ?, session_version = session_version + 1 WHERE id = ?`,
			passwordHash, u.ID); err != nil {
			return fmt.Errorf("更新密码失败: %w", err)
		}
		return clearFailuresTx(ctx, tx, u.Username)
	})
}

// BumpSessionVersion 让该用户所有已签发的令牌立即失效（登出全部设备）
func BumpSessionVersion(ctx context.Context, q queryer, username string) error {
	res, err := q.ExecContext(ctx,
		`UPDATE users SET session_version = session_version + 1 WHERE username_lower = ?`,
		strings.ToLower(username))
	if err != nil {
		return fmt.Errorf("更新会话版本失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SetRole 修改角色。降级最后一个管理员会被拒绝。
func SetRole(ctx context.Context, db *sql.DB, username, role string) error {
	if err := ValidateRole(role); err != nil {
		return err
	}
	return inTx(ctx, db, func(tx *sql.Tx) error {
		u, err := GetUser(ctx, tx, username)
		if err != nil {
			return err
		}
		if u.Role == RoleAdmin && role != RoleAdmin {
			if err := requireAnotherAdmin(ctx, tx, u.ID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, role, u.ID)
		return err
	})
}

// SetDisabled 启用/禁用账户。禁用最后一个管理员会被拒绝。
func SetDisabled(ctx context.Context, db *sql.DB, username string, disabled bool) error {
	return inTx(ctx, db, func(tx *sql.Tx) error {
		u, err := GetUser(ctx, tx, username)
		if err != nil {
			return err
		}
		if disabled && u.Role == RoleAdmin {
			if err := requireAnotherAdmin(ctx, tx, u.ID); err != nil {
				return err
			}
		}
		// 禁用同时踢掉已有会话，否则被禁用的人在令牌过期前还能继续操作
		if disabled {
			if _, err := tx.ExecContext(ctx,
				`UPDATE users SET disabled = 1, session_version = session_version + 1 WHERE id = ?`,
				u.ID); err != nil {
				return err
			}
			return nil
		}
		_, err = tx.ExecContext(ctx, `UPDATE users SET disabled = 0 WHERE id = ?`, u.ID)
		return err
	})
}

// DeleteUser 删除账户。删除最后一个管理员会被拒绝。
// 关联的恢复码由外键级联删除。
func DeleteUser(ctx context.Context, db *sql.DB, username string) error {
	return inTx(ctx, db, func(tx *sql.Tx) error {
		u, err := GetUser(ctx, tx, username)
		if err != nil {
			return err
		}
		if u.Role == RoleAdmin {
			if err := requireAnotherAdmin(ctx, tx, u.ID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, u.ID); err != nil {
			return fmt.Errorf("删除用户失败: %w", err)
		}
		return nil
	})
}

// TouchLastLogin 记录最后登录时间
func TouchLastLogin(ctx context.Context, q queryer, username string) error {
	_, err := q.ExecContext(ctx,
		`UPDATE users SET last_login_at = ? WHERE username_lower = ?`,
		time.Now().Unix(), strings.ToLower(username))
	return err
}

func requireAnotherAdmin(ctx context.Context, q queryer, excludeID int64) error {
	n, err := countActiveAdmins(ctx, q, excludeID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}

func scanUser(row *sql.Row) (*User, error) {
	var u User
	var createdAt, lastLogin int64
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.SessionVersion,
		&u.TOTPEnabled, &u.TOTPSecret, &u.TOTPLastStep, &u.Disabled, &createdAt, &lastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取用户失败: %w", err)
	}
	u.CreatedAt = time.Unix(createdAt, 0)
	if lastLogin > 0 {
		u.LastLoginAt = time.Unix(lastLogin, 0)
	}
	return &u, nil
}

func scanUserRows(rows *sql.Rows) (*User, error) {
	var u User
	var createdAt, lastLogin int64
	err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.SessionVersion,
		&u.TOTPEnabled, &u.TOTPSecret, &u.TOTPLastStep, &u.Disabled, &createdAt, &lastLogin)
	if err != nil {
		return nil, fmt.Errorf("读取用户失败: %w", err)
	}
	u.CreatedAt = time.Unix(createdAt, 0)
	if lastLogin > 0 {
		u.LastLoginAt = time.Unix(lastLogin, 0)
	}
	return &u, nil
}

func inTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // 已提交时是 no-op
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func isUniqueViolation(err error) bool {
	// modernc 的驱动不暴露结构化错误码，只能按消息判断。
	// UNIQUE 约束是这里唯一可能触发的约束冲突，误判风险可以接受。
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE")
}
