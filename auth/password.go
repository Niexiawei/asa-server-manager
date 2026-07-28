package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

var (
	// ErrPasswordTooShort 表示密码长度不满足配置要求
	ErrPasswordTooShort = errors.New("密码长度不足")
	// ErrPasswordEmpty 表示密码为空。每个账户恒有密码，空密码任何路径都不接受。
	ErrPasswordEmpty = errors.New("密码不得为空")
)

// bcrypt 只用密码的前 72 字节。超长密码会被静默截断，
// 表现是"两个不同的长密码都能登录同一个账户"——直接拒绝比截断安全。
const maxPasswordBytes = 72

// HashPassword 用 bcrypt 计算密码哈希。
func HashPassword(password string, cost int) (string, error) {
	if password == "" {
		return "", ErrPasswordEmpty
	}
	if len(password) > maxPasswordBytes {
		return "", fmt.Errorf("密码过长（bcrypt 上限 %d 字节，当前 %d 字节）", maxPasswordBytes, len(password))
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", fmt.Errorf("计算密码哈希失败: %w", err)
	}
	return string(h), nil
}

// VerifyPassword 校验密码。返回 false 表示不匹配（不是错误）。
func VerifyPassword(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// ValidatePasswordStrength 检查密码是否满足配置的最小长度。
//
// 只查长度，不强制"大小写+数字+符号"那套组合规则——那类规则会把用户推向
// "Password1!" 这种可预测的模式，实际强度还不如一句长口令。
func ValidatePasswordStrength(password string, minLength int) error {
	if password == "" {
		return ErrPasswordEmpty
	}
	// 按字符数而不是字节数计，否则中文口令会被高估
	if utf8.RuneCountInString(password) < minLength {
		return fmt.Errorf("%w：至少需要 %d 个字符", ErrPasswordTooShort, minLength)
	}
	if len(password) > maxPasswordBytes {
		return fmt.Errorf("密码过长（bcrypt 上限 %d 字节，当前 %d 字节）", maxPasswordBytes, len(password))
	}
	return nil
}

// 排除容易看错的字符：0/O、1/l/I。
// 这些密码要被人从终端抄到浏览器里，抄错一位的代价比少几比特熵大得多。
const passwordAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789!@#$%^&*-_=+"

// GeneratePassword 生成指定长度的随机密码，供 CLI 的 `user passwd --random` 使用。
func GeneratePassword(length int) (string, error) {
	if length < 8 {
		length = 8
	}
	var b strings.Builder
	b.Grow(length)
	max := big.NewInt(int64(len(passwordAlphabet)))
	for range length {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("生成随机密码失败: %w", err)
		}
		b.WriteByte(passwordAlphabet[n.Int64()])
	}
	return b.String(), nil
}
