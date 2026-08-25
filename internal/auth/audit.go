package auth

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// 审计事件类型。一个刻意往公网暴露的管理面板，
// "谁、何时、从哪个 IP 登录或失败" 是必须能查到的。
const (
	EventLoginOK        = "login_ok"
	EventLoginFail      = "login_fail"
	EventTOTPFail       = "totp_fail"
	EventLogout         = "logout"
	EventLogoutAll      = "logout_all"
	EventPasswordChange = "passwd_change"
	EventPasswordReset  = "passwd_reset"
	EventUserCreate     = "user_create"
	EventUserDelete     = "user_delete"
	EventUserUpdate     = "user_update"
	EventUserUnlock     = "user_unlock"
	EventTOTPBind       = "totp_bind"
	EventTOTPReset      = "totp_reset"
	EventCredAdd        = "cred_add"
	EventCredDelete     = "cred_delete"
)

// ActorCLI 表示操作来自本机命令行而不是 HTTP 请求
const ActorCLI = "cli"

// AuditSource 是请求来源信息。
//
// 它通过 context 传递而不是加到每个方法的参数里：审计需要来源 IP，
// 但"创建用户""重置密码"这些领域方法本身不关心 HTTP 细节，
// 为了记日志给它们全都加两个参数会污染整条调用链。
type AuditSource struct {
	ClientIP  string
	UserAgent string
}

type auditSourceKey struct{}

// WithAuditSource 把来源信息挂到 context 上，由 HTTP 中间件在请求入口调用
func WithAuditSource(ctx context.Context, src AuditSource) context.Context {
	return context.WithValue(ctx, auditSourceKey{}, src)
}

// AuditSourceFrom 取出来源信息，没有则返回零值
func AuditSourceFrom(ctx context.Context) AuditSource {
	if v, ok := ctx.Value(auditSourceKey{}).(AuditSource); ok {
		return v
	}
	return AuditSource{}
}

// AuditEntry 是一条审计记录
type AuditEntry struct {
	ID        int64     `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Event     string    `json:"event"`
	Username  string    `json:"username"`
	Actor     string    `json:"actor"`
	ClientIP  string    `json:"client_ip"`
	UserAgent string    `json:"user_agent"`
	Detail    string    `json:"detail"`
}

// WriteAudit 写一条审计记录。
//
// 刻意不返回错误给业务流程：审计写失败不应该让用户登不上去。
// 调用方拿到 error 只用于记日志。
func WriteAudit(ctx context.Context, q queryer, e AuditEntry) error {
	ts := e.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	_, err := q.ExecContext(ctx,
		`INSERT INTO audit_log(ts, event, username, actor, client_ip, user_agent, detail)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		ts.Unix(), e.Event, e.Username, e.Actor, e.ClientIP, truncate(e.UserAgent, 512), truncate(e.Detail, 1024))
	if err != nil {
		return fmt.Errorf("写入审计日志失败: %w", err)
	}
	return nil
}

// AuditFilter 是审计日志查询条件
type AuditFilter struct {
	Username string
	Event    string
	Since    time.Time
	Limit    int
	Offset   int
}

// QueryAudit 分页查询审计日志，按时间倒序
func QueryAudit(ctx context.Context, q queryer, f AuditFilter) ([]AuditEntry, error) {
	var where []string
	var args []any

	if f.Username != "" {
		where = append(where, "username = ?")
		args = append(args, f.Username)
	}
	if f.Event != "" {
		where = append(where, "event = ?")
		args = append(args, f.Event)
	}
	if !f.Since.IsZero() {
		where = append(where, "ts >= ?")
		args = append(args, f.Since.Unix())
	}

	sqlStr := `SELECT id, ts, event, username, actor, client_ip, user_agent, detail FROM audit_log`
	if len(where) > 0 {
		sqlStr += " WHERE " + strings.Join(where, " AND ")
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	sqlStr += " ORDER BY ts DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, max(f.Offset, 0))

	rows, err := q.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("查询审计日志失败: %w", err)
	}
	defer rows.Close()

	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var ts int64
		if err := rows.Scan(&e.ID, &ts, &e.Event, &e.Username, &e.Actor,
			&e.ClientIP, &e.UserAgent, &e.Detail); err != nil {
			return nil, fmt.Errorf("读取审计日志失败: %w", err)
		}
		e.Timestamp = time.Unix(ts, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

// TrimAudit 把审计日志裁剪到最多 maxRows 条，返回删除的行数。
//
// 用滚动窗口而不是按时间保留：一次密码爆破就能刷进上万条，
// 按条数封顶才能保证文件大小可控。
func TrimAudit(ctx context.Context, db *sql.DB, maxRows int) (int64, error) {
	if maxRows <= 0 {
		return 0, nil
	}
	res, err := db.ExecContext(ctx,
		`DELETE FROM audit_log WHERE id NOT IN (
			SELECT id FROM audit_log ORDER BY id DESC LIMIT ?
		)`, maxRows)
	if err != nil {
		return 0, fmt.Errorf("裁剪审计日志失败: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
