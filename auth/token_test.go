package auth

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func testClaims() Claims {
	now := time.Now()
	return Claims{
		Username:  "admin",
		Version:   1,
		JTI:       "test-jti",
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(time.Hour).Unix(),
		Stage:     StageFull,
		AMR:       []string{AMRPassword},
	}
}

func TestTokenRoundTrip(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	want := testClaims()

	tok, err := SignToken(secret, want)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	got, err := ParseToken(secret, tok)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if got.Username != want.Username || got.Version != want.Version ||
		got.JTI != want.JTI || got.Stage != want.Stage {
		t.Errorf("载荷往返不一致: %+v vs %+v", got, want)
	}
}

func TestTokenRejectsTampering(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	tok, err := SignToken(secret, testClaims())
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	body, sig, _ := strings.Cut(tok, ".")

	t.Run("改载荷", func(t *testing.T) {
		mangled := flipLastChar(body) + "." + sig
		if _, err := ParseToken(secret, mangled); err == nil {
			t.Error("载荷被改动后应校验失败")
		}
	})
	t.Run("改签名", func(t *testing.T) {
		mangled := body + "." + flipLastChar(sig)
		if _, err := ParseToken(secret, mangled); !errors.Is(err, ErrTokenSignature) {
			t.Errorf("签名被改动后应返回 ErrTokenSignature，实际 %v", err)
		}
	})
	t.Run("换密钥", func(t *testing.T) {
		other := []byte("ffffffffffffffffffffffffffffffff")
		if _, err := ParseToken(other, tok); !errors.Is(err, ErrTokenSignature) {
			t.Errorf("用别的密钥应校验失败，实际 %v", err)
		}
	})
	t.Run("格式不对", func(t *testing.T) {
		for _, bad := range []string{"", "没有点", ".", "abc.", ".abc"} {
			if _, err := ParseToken(secret, bad); err == nil {
				t.Errorf("%q 应被拒绝", bad)
			}
		}
	})
}

func TestTokenExpiry(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	c := testClaims()
	c.ExpiresAt = time.Now().Add(-time.Second).Unix()

	tok, err := SignToken(secret, c)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	if _, err := ParseToken(secret, tok); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("过期令牌应返回 ErrTokenExpired，实际 %v", err)
	}
}

// pre-auth 令牌绝不能当成完整会话凭证使用，否则两步验证形同虚设。
func TestPreAuthTokenIsolation(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	c := testClaims()
	c.Stage = StagePre

	tok, err := SignToken(secret, c)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	if _, err := ParseTokenWithStage(secret, tok, StageFull); !errors.Is(err, ErrTokenStage) {
		t.Errorf("pre-auth 令牌不得通过 StageFull 校验，实际 %v", err)
	}
	if _, err := ParseTokenWithStage(secret, tok, StagePre); err != nil {
		t.Errorf("pre-auth 令牌应通过 StagePre 校验，实际 %v", err)
	}

	// 反过来，完整令牌也不该被第二步接口接受
	full, _ := SignToken(secret, testClaims())
	if _, err := ParseTokenWithStage(secret, full, StagePre); !errors.Is(err, ErrTokenStage) {
		t.Errorf("完整令牌不得通过 StagePre 校验，实际 %v", err)
	}
}

func TestLoadOrCreateSecret(t *testing.T) {
	dir := t.TempDir()

	s1, err := LoadOrCreateSecret(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateSecret: %v", err)
	}
	if len(s1) != 32 {
		t.Errorf("密钥应为 32 字节，实际 %d", len(s1))
	}

	s2, err := LoadOrCreateSecret(dir)
	if err != nil {
		t.Fatalf("第二次 LoadOrCreateSecret: %v", err)
	}
	if string(s1) != string(s2) {
		t.Error("重复调用应返回同一把密钥，否则每次重启都会把所有人登出")
	}

	if _, err := os.Stat(SecretPath(dir)); err != nil {
		t.Errorf("密钥文件未创建: %v", err)
	}
}

func TestShouldRenew(t *testing.T) {
	now := time.Now()
	ttl := time.Hour

	fresh := &Claims{ExpiresAt: now.Add(50 * time.Minute).Unix()}
	if ShouldRenew(fresh, ttl, now) {
		t.Error("剩余寿命超过一半时不该续期")
	}
	stale := &Claims{ExpiresAt: now.Add(10 * time.Minute).Unix()}
	if !ShouldRenew(stale, ttl, now) {
		t.Error("剩余寿命不足一半时应该续期")
	}
}

func flipLastChar(s string) string {
	if s == "" {
		return "x"
	}
	last := s[len(s)-1]
	repl := byte('A')
	if last == 'A' {
		repl = 'B'
	}
	return s[:len(s)-1] + string(repl)
}
