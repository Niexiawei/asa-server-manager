package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{
		Period: totpPeriod, Digits: totpDigits, Algorithm: totpAlgorithm,
	})
	if err != nil {
		t.Fatalf("生成验证码失败: %v", err)
	}
	return code
}

func TestGenerateTOTPKey(t *testing.T) {
	key, err := GenerateTOTPKey("ASA Server Manager", "admin")
	if err != nil {
		t.Fatalf("GenerateTOTPKey: %v", err)
	}
	if key.Secret() == "" {
		t.Error("密钥不应为空")
	}
	u := key.URL()
	if !strings.HasPrefix(u, "otpauth://totp/") {
		t.Errorf("URL 应为 otpauth 格式，实际 %q", u)
	}
	if !strings.Contains(u, "ASA") {
		t.Errorf("URL 应含 issuer，实际 %q", u)
	}

	qr, err := TOTPQRCodeBase64(key, 256)
	if err != nil {
		t.Fatalf("TOTPQRCodeBase64: %v", err)
	}
	if len(qr) < 100 {
		t.Errorf("二维码 base64 长度异常: %d", len(qr))
	}
}

func TestValidateTOTP(t *testing.T) {
	key, err := GenerateTOTPKey("test", "admin")
	if err != nil {
		t.Fatalf("GenerateTOTPKey: %v", err)
	}
	secret := key.Secret()
	now := time.Now()

	step, err := ValidateTOTP(secret, codeAt(t, secret, now), 1, 0, now)
	if err != nil {
		t.Fatalf("正确的验证码应通过: %v", err)
	}
	if step == 0 {
		t.Error("应返回本次命中的时间步")
	}

	if _, err := ValidateTOTP(secret, "000000", 1, 0, now); !errors.Is(err, ErrTOTPInvalid) {
		t.Errorf("错误验证码应返回 ErrTOTPInvalid，实际 %v", err)
	}
	if _, err := ValidateTOTP("", "123456", 1, 0, now); !errors.Is(err, ErrTOTPNotEnabled) {
		t.Errorf("未绑定时应返回 ErrTOTPNotEnabled，实际 %v", err)
	}
}

// 不记录用过的时间步就等于允许重放：攻击者在肩窥或中间人场景下
// 抓到一次验证码，30 秒内还能再用一次。
func TestValidateTOTPRejectsReplay(t *testing.T) {
	key, _ := GenerateTOTPKey("test", "admin")
	secret := key.Secret()
	now := time.Now()
	code := codeAt(t, secret, now)

	step, err := ValidateTOTP(secret, code, 1, 0, now)
	if err != nil {
		t.Fatalf("首次校验应通过: %v", err)
	}

	// 把上次命中的步记下来之后，同一个码不能再用
	if _, err := ValidateTOTP(secret, code, 1, step, now); !errors.Is(err, ErrTOTPReused) {
		t.Errorf("重放同一验证码应返回 ErrTOTPReused，实际 %v", err)
	}
}

// 服务器和手机之间总有时钟偏差，skew=1 允许前后各一个 30 秒窗口。
func TestValidateTOTPSkew(t *testing.T) {
	key, _ := GenerateTOTPKey("test", "admin")
	secret := key.Secret()
	now := time.Now()

	prev := codeAt(t, secret, now.Add(-totpPeriod*time.Second))
	next := codeAt(t, secret, now.Add(totpPeriod*time.Second))

	if _, err := ValidateTOTP(secret, prev, 1, 0, now); err != nil {
		t.Errorf("skew=1 应接受上一个窗口的验证码: %v", err)
	}
	if _, err := ValidateTOTP(secret, next, 1, 0, now); err != nil {
		t.Errorf("skew=1 应接受下一个窗口的验证码: %v", err)
	}

	// 边界之外必须拒绝，否则时间窗等于形同虚设
	far := codeAt(t, secret, now.Add(3*totpPeriod*time.Second))
	if _, err := ValidateTOTP(secret, far, 1, 0, now); err == nil {
		t.Error("skew=1 不应接受 3 个窗口之外的验证码")
	}

	// skew=0 时只认当前窗口
	if _, err := ValidateTOTP(secret, prev, 0, 0, now); err == nil {
		t.Error("skew=0 不应接受上一个窗口的验证码")
	}
}

func TestTOTPLifecycle(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	mustCreate(t, db, "bob", RoleOperator)

	key, _ := GenerateTOTPKey("test", "bob")
	if err := EnableTOTP(ctx, db, "bob", key.Secret()); err != nil {
		t.Fatalf("EnableTOTP: %v", err)
	}
	u, _ := GetUser(ctx, db, "bob")
	if !u.TOTPEnabled || u.TOTPSecret != key.Secret() {
		t.Fatal("两步验证未正确启用")
	}

	if err := RecordTOTPStep(ctx, db, "bob", 12345); err != nil {
		t.Fatalf("RecordTOTPStep: %v", err)
	}
	u, _ = GetUser(ctx, db, "bob")
	if u.TOTPLastStep != 12345 {
		t.Errorf("时间步应被记录，实际 %d", u.TOTPLastStep)
	}
	// 时间步只能前进，不能倒退（否则可以重放旧码）
	if err := RecordTOTPStep(ctx, db, "bob", 100); err != nil {
		t.Fatalf("RecordTOTPStep: %v", err)
	}
	u, _ = GetUser(ctx, db, "bob")
	if u.TOTPLastStep != 12345 {
		t.Errorf("时间步不应倒退，实际 %d", u.TOTPLastStep)
	}

	if err := DisableTOTP(ctx, db, "bob"); err != nil {
		t.Fatalf("DisableTOTP: %v", err)
	}
	u, _ = GetUser(ctx, db, "bob")
	if u.TOTPEnabled || u.TOTPSecret != "" || u.TOTPLastStep != 0 {
		t.Error("解绑后应清空全部两步验证状态")
	}
}

func TestRecoveryCodes(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	mustCreate(t, db, "bob", RoleOperator)

	codes, err := GenerateRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Fatalf("应生成 %d 个恢复码，实际 %d", RecoveryCodeCount, len(codes))
	}
	for _, c := range codes {
		if len(c) != 14 { // XXXX-XXXX-XXXX
			t.Errorf("恢复码格式异常: %q", c)
		}
	}

	if err := ReplaceRecoveryCodes(ctx, db, "bob", codes, 4); err != nil {
		t.Fatalf("ReplaceRecoveryCodes: %v", err)
	}
	n, _ := CountUnusedRecoveryCodes(ctx, db, "bob")
	if n != RecoveryCodeCount {
		t.Errorf("应有 %d 个可用恢复码，实际 %d", RecoveryCodeCount, n)
	}

	// 用户抄写时的大小写和连字符差异不该导致失败
	if err := ConsumeRecoveryCode(ctx, db, "bob", strings.ToLower(codes[0])); err != nil {
		t.Fatalf("小写形式的恢复码应能使用: %v", err)
	}
	if n, _ = CountUnusedRecoveryCodes(ctx, db, "bob"); n != RecoveryCodeCount-1 {
		t.Errorf("用掉一个后应剩 %d，实际 %d", RecoveryCodeCount-1, n)
	}

	// 同一个码不能用第二次
	if err := ConsumeRecoveryCode(ctx, db, "bob", codes[0]); !errors.Is(err, ErrRecoveryUsed) {
		t.Errorf("已用过的恢复码应被拒绝，实际 %v", err)
	}
	if err := ConsumeRecoveryCode(ctx, db, "bob", "AAAA-BBBB-CCCC"); !errors.Is(err, ErrRecoveryUsed) {
		t.Errorf("不存在的恢复码应被拒绝，实际 %v", err)
	}
}

func TestRecoveryCodeHelpers(t *testing.T) {
	if got := NormalizeRecoveryCode(" abcd-efgh-ijkl "); got != "ABCDEFGHIJKL" {
		t.Errorf("NormalizeRecoveryCode = %q", got)
	}
	if !LooksLikeRecoveryCode("ABCD-EFGH-IJKL") {
		t.Error("应识别为恢复码")
	}
	if LooksLikeRecoveryCode("123456") {
		t.Error("6 位数字是 TOTP，不是恢复码")
	}
}

func TestTOTPAlgorithmIsCompatible(t *testing.T) {
	// SHA1 + 6 位 + 30 秒是所有主流验证器 App 的共同基线。
	// 改成 SHA256 或 8 位会让相当一部分 App 直接用不了。
	if totpAlgorithm != otp.AlgorithmSHA1 {
		t.Error("算法必须保持 SHA1 以兼容主流验证器 App")
	}
	if totpDigits != otp.DigitsSix {
		t.Error("位数必须保持 6 位")
	}
	if totpPeriod != 30 {
		t.Error("周期必须保持 30 秒")
	}
}
