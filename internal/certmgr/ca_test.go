package certmgr

import (
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/logger"
	"crypto/x509"
	"net"
	"os"
	"slices"
	"testing"
	"time"
)

// TestMain 把私钥加固换成空实现：真实实现会为每个临时文件拉起 icacls 子进程，
// 测试里既慢又没有验证价值
func TestMain(m *testing.M) {
	logger.InitLoggerWithBaseDir(os.TempDir())
	hardenKey = func(string) {}
	os.Exit(m.Run())
}

// withTempBaseDir 把证书目录指向临时目录，避免测试污染真实 {BaseDir}/certs
func withTempBaseDir(t *testing.T) {
	t.Helper()
	original := cfgpkg.BaseDir
	cfgpkg.BaseDir = t.TempDir()
	t.Cleanup(func() { cfgpkg.BaseDir = original })

	if err := os.MkdirAll(CertsDir(), 0700); err != nil {
		t.Fatalf("创建证书目录失败: %v", err)
	}
}

func TestEnsureCAIsIdempotent(t *testing.T) {
	withTempBaseDir(t)

	first, err := ensureCA()
	if err != nil {
		t.Fatalf("首次生成 CA 失败: %v", err)
	}
	second, err := ensureCA()
	if err != nil {
		t.Fatalf("再次加载 CA 失败: %v", err)
	}

	if Fingerprint(first.der) != Fingerprint(second.der) {
		t.Fatal("重复调用 ensureCA 不应重新签发 CA —— 那会让已写入系统存储的 CA 变成孤儿")
	}
	if !first.cert.IsCA || first.cert.Subject.CommonName != CACommonName {
		t.Fatalf("CA 证书属性不正确: IsCA=%v CN=%q", first.cert.IsCA, first.cert.Subject.CommonName)
	}
}

func TestEnsureLeafIsSignedByCAAndCoversLoopback(t *testing.T) {
	withTempBaseDir(t)

	ca, err := ensureCA()
	if err != nil {
		t.Fatalf("生成 CA 失败: %v", err)
	}
	if _, err := ensureLeaf(ca, []string{"asa.example.com"}); err != nil {
		t.Fatalf("签发叶子证书失败: %v", err)
	}

	leaf, _, err := readCertPEM(leafCertPath())
	if err != nil {
		t.Fatalf("读取叶子证书失败: %v", err)
	}
	if err := leaf.CheckSignatureFrom(ca.cert); err != nil {
		t.Fatalf("叶子证书不是由本地 CA 签发的: %v", err)
	}

	// 浏览器完全忽略 CN，只看 SAN：localhost 与回环地址是本机访问的最低要求
	if !slices.Contains(leaf.DNSNames, "localhost") {
		t.Errorf("SAN 缺少 localhost: %v", leaf.DNSNames)
	}
	if !slices.Contains(leaf.DNSNames, "asa.example.com") {
		t.Errorf("SAN 缺少显式配置的域名: %v", leaf.DNSNames)
	}
	if err := leaf.VerifyHostname("127.0.0.1"); err != nil {
		t.Errorf("证书不覆盖 127.0.0.1: %v", err)
	}
	if !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
		t.Error("叶子证书缺少 ServerAuth 用途，TLS 握手会被拒绝")
	}
}

func TestLeafIsReusedUntilSANsChange(t *testing.T) {
	withTempBaseDir(t)

	ca, err := ensureCA()
	if err != nil {
		t.Fatalf("生成 CA 失败: %v", err)
	}
	if _, err := ensureLeaf(ca, nil); err != nil {
		t.Fatalf("首次签发失败: %v", err)
	}
	first, _, err := readCertPEM(leafCertPath())
	if err != nil {
		t.Fatalf("读取叶子证书失败: %v", err)
	}

	// SAN 未变：必须复用，否则每次启动都换证书，浏览器连接会被反复重置
	dnsNames, ips := desiredSANs(nil)
	if reason := leafReusable(ca, dnsNames, ips); reason != "" {
		t.Fatalf("SAN 未变化时不应重签，却给出理由: %s", reason)
	}

	// 加一个域名就应当重签，否则新域名访问会报证书不匹配
	dnsNames, ips = desiredSANs([]string{"new.example.com"})
	if reason := leafReusable(ca, dnsNames, ips); reason == "" {
		t.Fatal("SAN 变化后应当重签")
	}
	if _, err := ensureLeaf(ca, []string{"new.example.com"}); err != nil {
		t.Fatalf("重签失败: %v", err)
	}
	second, _, err := readCertPEM(leafCertPath())
	if err != nil {
		t.Fatalf("读取重签后的证书失败: %v", err)
	}
	if first.SerialNumber.Cmp(second.SerialNumber) == 0 {
		t.Fatal("重签后序列号应当变化")
	}
}

func TestCARenewalDropsExpiringCertificate(t *testing.T) {
	withTempBaseDir(t)

	ca, err := ensureCA()
	if err != nil {
		t.Fatalf("生成 CA 失败: %v", err)
	}
	if remaining := time.Until(ca.cert.NotAfter); remaining < 9*365*24*time.Hour {
		t.Fatalf("CA 有效期应接近 10 年，实际剩余 %v", remaining)
	}
}

func TestDesiredSANsAlwaysIncludeLoopback(t *testing.T) {
	dnsNames, ips := desiredSANs(nil)

	if !slices.Contains(dnsNames, "localhost") {
		t.Errorf("DNS SAN 缺少 localhost: %v", dnsNames)
	}
	for _, want := range []string{"127.0.0.1", "::1"} {
		if !slices.ContainsFunc(ips, func(ip net.IP) bool { return ip.String() == want }) {
			t.Errorf("IP SAN 缺少 %s: %v", want, ips)
		}
	}

	// sanKey 是重签判据，必须与输入顺序无关，否则会无谓地反复重签
	shuffled, shuffledIPs := desiredSANs(nil)
	slices.Reverse(shuffled)
	slices.Reverse(shuffledIPs)
	if sanKey(dnsNames, ips) != sanKey(shuffled, shuffledIPs) {
		t.Fatal("sanKey 必须对顺序不敏感")
	}
}
