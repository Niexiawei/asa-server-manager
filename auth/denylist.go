package auth

import (
	"context"
	"fmt"
	"time"
)

// token_denylist 支持"只登出当前这台设备"。
//
// 全设备登出走 users.session_version++，不经过这里——那条路径不需要记录任何
// 单个令牌，代价是 O(1)。denylist 只在单设备登出时才写入，所以表通常是空的。

// DenyToken 把一个 jti 吊销到它自然过期为止
func DenyToken(ctx context.Context, q queryer, jti string, expiresAt time.Time) error {
	if jti == "" {
		return nil
	}
	_, err := q.ExecContext(ctx,
		`INSERT INTO token_denylist(jti, expires_at) VALUES(?, ?)
		 ON CONFLICT(jti) DO UPDATE SET expires_at = excluded.expires_at`,
		jti, expiresAt.Unix())
	if err != nil {
		return fmt.Errorf("吊销令牌失败: %w", err)
	}
	return nil
}

// LoadDenylist 读出所有未过期的吊销记录，供内存副本使用
func LoadDenylist(ctx context.Context, q queryer, now time.Time) (map[string]time.Time, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT jti, expires_at FROM token_denylist WHERE expires_at > ?`, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("读取吊销列表失败: %w", err)
	}
	defer rows.Close()

	out := map[string]time.Time{}
	for rows.Next() {
		var jti string
		var exp int64
		if err := rows.Scan(&jti, &exp); err != nil {
			return nil, fmt.Errorf("读取吊销列表失败: %w", err)
		}
		out[jti] = time.Unix(exp, 0)
	}
	return out, rows.Err()
}

// PurgeExpiredTokens 清掉已经自然过期的吊销记录，返回删除行数。
// 令牌过期之后再留着它的吊销记录没有意义。
func PurgeExpiredTokens(ctx context.Context, q queryer, now time.Time) (int64, error) {
	res, err := q.ExecContext(ctx, `DELETE FROM token_denylist WHERE expires_at <= ?`, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("清理过期吊销记录失败: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
