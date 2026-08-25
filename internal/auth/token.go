package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 会话令牌是自签的紧凑格式，不引入 JWT 库：
//
//	base64url(payload) + "." + base64url(HMAC-SHA256(secret, base64url(payload)))
//
// 算法写死在代码里，从根上避开 JWT 那类 "alg" 混淆漏洞——攻击者没有任何
// 字段可以用来影响校验方式。

const (
	// StageFull 是完成全部认证步骤后签发的正式会话令牌
	StageFull = "full"
	// StagePre 是密码通过、但还差第二步（TOTP）时签发的中间令牌。
	// 它绝不能通过 ParseSessionToken —— 否则第二步形同虚设。
	StagePre = "pre"
)

// 认证手段标记，记进令牌的 amr 字段，便于前端展示与审计
const (
	AMRPassword = "pwd"
	AMRTOTP     = "totp"
	AMRRecovery = "recovery"
)

var (
	ErrTokenMalformed = errors.New("令牌格式错误")
	ErrTokenSignature = errors.New("令牌签名无效")
	ErrTokenExpired   = errors.New("令牌已过期")
	ErrTokenStage     = errors.New("令牌类型不匹配")
)

// Claims 是令牌载荷。字段名刻意用短名，令牌要放进 Cookie，越短越好。
type Claims struct {
	Username  string   `json:"u"`
	Version   int      `json:"v"`   // 对应 users.session_version，用于全设备吊销
	JTI       string   `json:"jti"` // 单设备登出时进 denylist
	IssuedAt  int64    `json:"iat"`
	ExpiresAt int64    `json:"exp"`
	Stage     string   `json:"stage"`
	AMR       []string `json:"amr,omitempty"`
}

// Expired 判断令牌是否已过期
func (c *Claims) Expired(now time.Time) bool {
	return now.Unix() >= c.ExpiresAt
}

// RemainingLifetime 返回剩余有效期，用于判断要不要滑动续期
func (c *Claims) RemainingLifetime(now time.Time) time.Duration {
	return time.Duration(c.ExpiresAt-now.Unix()) * time.Second
}

// SecretPath 返回签名密钥的存放路径
func SecretPath(baseDir string) string {
	return filepath.Join(baseDir, "auth", "secret.key")
}

// LoadOrCreateSecret 读取签名密钥，不存在则生成一份 32 字节的随机密钥。
//
// 删掉这个文件等于让所有人立即登出——这也是最后的应急手段之一。
func LoadOrCreateSecret(baseDir string) ([]byte, error) {
	path := SecretPath(baseDir)

	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) < 32 {
			return nil, fmt.Errorf("签名密钥 %s 长度异常（%d 字节），请删除该文件让程序重新生成", path, len(data))
		}
		return data, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取签名密钥失败: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建密钥目录失败: %w", err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("生成签名密钥失败: %w", err)
	}
	if err := os.WriteFile(path, secret, 0o600); err != nil {
		return nil, fmt.Errorf("写入签名密钥失败: %w", err)
	}
	return secret, nil
}

// NewJTI 生成令牌唯一标识
func NewJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成 jti 失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SignToken 用 secret 签发令牌
func SignToken(secret []byte, c Claims) (string, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("序列化令牌载荷失败: %w", err)
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + sign(secret, body), nil
}

// ParseToken 校验签名与有效期，返回载荷。
//
// 它**不**检查 Stage、session_version 或 denylist —— 那些需要访问用户状态，
// 由 Manager.VerifySession 负责。分开是为了让这一层保持纯函数、好测试。
func ParseToken(secret []byte, token string) (*Claims, error) {
	body, sig, found := strings.Cut(token, ".")
	if !found || body == "" || sig == "" {
		return nil, ErrTokenMalformed
	}
	// 定长比较，避免通过响应时间泄露签名前缀
	if subtle.ConstantTimeCompare([]byte(sig), []byte(sign(secret, body))) != 1 {
		return nil, ErrTokenSignature
	}

	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, ErrTokenMalformed
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, ErrTokenMalformed
	}
	if c.Username == "" || c.ExpiresAt == 0 {
		return nil, ErrTokenMalformed
	}
	if c.Expired(time.Now()) {
		return nil, ErrTokenExpired
	}
	return &c, nil
}

// ParseTokenWithStage 在 ParseToken 之上额外要求令牌属于指定阶段。
//
// 会话校验必须用 StageFull，两步验证的第二步必须用 StagePre。
// 两者绝不能共用一个校验函数，否则 pre-auth 令牌就等价于完整凭证了。
func ParseTokenWithStage(secret []byte, token, stage string) (*Claims, error) {
	c, err := ParseToken(secret, token)
	if err != nil {
		return nil, err
	}
	if c.Stage != stage {
		return nil, fmt.Errorf("%w：需要 %s，实际 %s", ErrTokenStage, stage, c.Stage)
	}
	return c, nil
}

func sign(secret []byte, body string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
