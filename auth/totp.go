package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"image/png"
	"math/big"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	totpPeriod    = 30
	totpDigits    = otp.DigitsSix
	totpAlgorithm = otp.AlgorithmSHA1 // 兼容所有主流验证器 App，不要改
)

var (
	ErrTOTPInvalid    = errors.New("验证码无效")
	ErrTOTPReused     = errors.New("该验证码已被使用过")
	ErrTOTPNotEnabled = errors.New("该账户未启用两步验证")
	ErrRecoveryUsed   = errors.New("恢复码无效或已被使用")
)

// GenerateTOTPKey 生成一个新的 TOTP 密钥。
// 返回的 key 不应立刻落库——要等用户提交一个有效验证码证明他确实扫上了，
// 否则用户扫码失败却已经被强制开启两步验证，就把自己锁在外面了。
func GenerateTOTPKey(issuer, account string) (*otp.Key, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: account,
		Period:      totpPeriod,
		Digits:      totpDigits,
		Algorithm:   totpAlgorithm,
	})
	if err != nil {
		return nil, fmt.Errorf("生成两步验证密钥失败: %w", err)
	}
	return key, nil
}

// TOTPQRCodeBase64 把 otpauth URL 渲染成 PNG 并做 base64 编码。
//
// 二维码在后端生成：前端不用引入 QR 库，密钥也不必以文本形式渲染进 DOM。
func TOTPQRCodeBase64(key *otp.Key, size int) (string, error) {
	if size <= 0 {
		size = 256
	}
	img, err := key.Image(size, size)
	if err != nil {
		return "", fmt.Errorf("生成二维码失败: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("编码二维码失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// ValidateTOTP 校验验证码并返回它对应的时间步。
//
// 返回的 step 必须由调用方写回 users.totp_last_step：TOTP 在一个 30 秒窗口内
// 反复有效，不记录用过的步就等于允许重放——攻击者在肩窥或中间人场景下
// 抓到一次验证码就能再用一次。
func ValidateTOTP(secret, code string, skew uint, lastStep int64, now time.Time) (int64, error) {
	code = strings.TrimSpace(code)
	if secret == "" {
		return 0, ErrTOTPNotEnabled
	}
	if code == "" {
		return 0, ErrTOTPInvalid
	}

	opts := totp.ValidateOpts{
		Period:    totpPeriod,
		Skew:      0, // 逐个时间步自己比对，这样才知道命中的是哪一步
		Digits:    totpDigits,
		Algorithm: totpAlgorithm,
	}

	// 允许 ±skew 个窗口，应对服务器与手机之间的时钟偏差
	for delta := -int64(skew); delta <= int64(skew); delta++ {
		t := now.Add(time.Duration(delta) * totpPeriod * time.Second)
		want, err := totp.GenerateCodeCustom(secret, t, opts)
		if err != nil {
			return 0, fmt.Errorf("计算验证码失败: %w", err)
		}
		if subtleEqual(want, code) {
			step := t.UTC().Unix() / totpPeriod
			if step <= lastStep {
				return 0, ErrTOTPReused
			}
			return step, nil
		}
	}
	return 0, ErrTOTPInvalid
}

// EnableTOTP 在用户确认扫码成功后写入密钥
func EnableTOTP(ctx context.Context, q queryer, username, secret string) error {
	res, err := q.ExecContext(ctx,
		`UPDATE users SET totp_enabled = 1, totp_secret = ?, totp_last_step = 0
		 WHERE username_lower = ?`,
		secret, strings.ToLower(username))
	if err != nil {
		return fmt.Errorf("启用两步验证失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// DisableTOTP 解绑两步验证，同时清掉全部恢复码
func DisableTOTP(ctx context.Context, db *sql.DB, username string) error {
	return inTx(ctx, db, func(tx *sql.Tx) error {
		u, err := GetUser(ctx, tx, username)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET totp_enabled = 0, totp_secret = '', totp_last_step = 0 WHERE id = ?`,
			u.ID); err != nil {
			return fmt.Errorf("解绑两步验证失败: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, u.ID); err != nil {
			return fmt.Errorf("清除恢复码失败: %w", err)
		}
		return nil
	})
}

// RecordTOTPStep 记录本次用掉的时间步，防止同一验证码被重放
func RecordTOTPStep(ctx context.Context, q queryer, username string, step int64) error {
	_, err := q.ExecContext(ctx,
		`UPDATE users SET totp_last_step = ? WHERE username_lower = ? AND totp_last_step < ?`,
		step, strings.ToLower(username), step)
	return err
}

// ---- 恢复码 ----

// 恢复码字母表排除了容易看错的字符。这些码要被人抄在纸上再敲回浏览器。
const recoveryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// RecoveryCodeCount 是每次生成的恢复码数量
const RecoveryCodeCount = 10

// GenerateRecoveryCodes 生成一批 XXXX-XXXX-XXXX 格式的恢复码（明文）
func GenerateRecoveryCodes(n int) ([]string, error) {
	if n <= 0 {
		n = RecoveryCodeCount
	}
	out := make([]string, 0, n)
	max := big.NewInt(int64(len(recoveryAlphabet)))
	for range n {
		var b strings.Builder
		for g := range 3 {
			if g > 0 {
				b.WriteByte('-')
			}
			for range 4 {
				idx, err := rand.Int(rand.Reader, max)
				if err != nil {
					return nil, fmt.Errorf("生成恢复码失败: %w", err)
				}
				b.WriteByte(recoveryAlphabet[idx.Int64()])
			}
		}
		out = append(out, b.String())
	}
	return out, nil
}

// ReplaceRecoveryCodes 用新的一批恢复码替换旧的，存的是哈希。
// 明文只在生成的那一次返回给用户，之后无法再取出。
func ReplaceRecoveryCodes(ctx context.Context, db *sql.DB, username string, codes []string, cost int) error {
	return inTx(ctx, db, func(tx *sql.Tx) error {
		u, err := GetUser(ctx, tx, username)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, u.ID); err != nil {
			return fmt.Errorf("清除旧恢复码失败: %w", err)
		}
		for _, c := range codes {
			h, err := HashPassword(NormalizeRecoveryCode(c), cost)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO recovery_codes(user_id, code_hash) VALUES(?, ?)`, u.ID, h); err != nil {
				return fmt.Errorf("写入恢复码失败: %w", err)
			}
		}
		return nil
	})
}

// ConsumeRecoveryCode 校验并用掉一个恢复码。
//
// 恢复码是高熵随机串，只能逐个比对哈希（哈希本身不可反查）。
// 未使用的码最多 10 个，这个开销可以接受，而且登录限流已经限制了尝试次数。
func ConsumeRecoveryCode(ctx context.Context, db *sql.DB, username, code string) error {
	code = NormalizeRecoveryCode(code)
	if code == "" {
		return ErrRecoveryUsed
	}
	return inTx(ctx, db, func(tx *sql.Tx) error {
		u, err := GetUser(ctx, tx, username)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT id, code_hash FROM recovery_codes WHERE user_id = ? AND used_at = 0`, u.ID)
		if err != nil {
			return fmt.Errorf("查询恢复码失败: %w", err)
		}

		var matchID int64
		for rows.Next() {
			var id int64
			var hash string
			if err := rows.Scan(&id, &hash); err != nil {
				rows.Close()
				return fmt.Errorf("读取恢复码失败: %w", err)
			}
			if matchID == 0 && VerifyPassword(hash, code) {
				matchID = id
				// 不 break：继续读完剩下的行，让耗时不随"第几个码匹配"而变化
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("读取恢复码失败: %w", err)
		}
		if matchID == 0 {
			return ErrRecoveryUsed
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE recovery_codes SET used_at = ? WHERE id = ? AND used_at = 0`,
			time.Now().Unix(), matchID); err != nil {
			return fmt.Errorf("标记恢复码已使用失败: %w", err)
		}
		return nil
	})
}

// CountUnusedRecoveryCodes 返回还剩多少个可用的恢复码，用于提醒用户重新生成
func CountUnusedRecoveryCodes(ctx context.Context, q queryer, username string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM recovery_codes rc
		 JOIN users u ON u.id = rc.user_id
		 WHERE u.username_lower = ? AND rc.used_at = 0`,
		strings.ToLower(username)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计恢复码失败: %w", err)
	}
	return n, nil
}

// NormalizeRecoveryCode 统一恢复码格式，用户抄写时的大小写和连字符差异不该导致失败
func NormalizeRecoveryCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		if r != '-' && r != ' ' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// LooksLikeRecoveryCode 区分用户输的是 6 位 TOTP 还是恢复码
func LooksLikeRecoveryCode(code string) bool {
	return len(NormalizeRecoveryCode(code)) == 12
}

func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range len(a) {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
