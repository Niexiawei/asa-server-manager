package certmgr

import (
	"asa-server/pkg/logger"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	caValidity   = 10 * 365 * 24 * time.Hour // CA 10 年
	leafValidity = 365 * 24 * time.Hour      // 叶子 1 年
	// 临期阈值：提前 30 天重签。重签零成本，宁可勤一点也别让用户撞上过期
	renewBefore = 30 * 24 * time.Hour
)

// caBundle 是解析后的本地 CA
type caBundle struct {
	cert *x509.Certificate
	key  crypto.Signer
	der  []byte
}

// ensureCA 加载本地 CA，不存在或已损坏/临期时重新生成。
//
// CA 一旦重建，之前写进系统存储的旧 CA 就成了孤儿——所以这里在重建时会顺手把旧的
// 从存储里摘掉，避免用户的受信任根里堆积一串同名证书。
func ensureCA() (*caBundle, error) {
	ca, err := loadCA()
	if err == nil && time.Until(ca.cert.NotAfter) > renewBefore {
		return ca, nil
	}
	if err != nil && !os.IsNotExist(err) {
		logger.Warnf("本地 CA 不可用，将重新生成: %v", err)
	}
	if ca != nil {
		// 旧 CA 即将失效，先把它从受信任存储里摘掉再换新的
		if rmErr := untrustFingerprint(Fingerprint(ca.der)); rmErr != nil {
			logger.Warnf("移除过期本地 CA 失败: %v", rmErr)
		}
	}

	return generateCA()
}

func loadCA() (*caBundle, error) {
	cert, der, err := readCertPEM(caCertPath())
	if err != nil {
		return nil, err
	}
	key, err := readKeyPEM(caKeyPath())
	if err != nil {
		return nil, err
	}
	if !cert.IsCA {
		return nil, fmt.Errorf("%s 不是 CA 证书", caCertPath())
	}
	return &caBundle{cert: cert, key: key, der: der}, nil
}

func generateCA() (*caBundle, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成 CA 私钥失败: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   CACommonName,
			Organization: []string{organization},
		},
		// 回拨一小时，容忍本机与签发时刻之间的时钟偏差
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("签发 CA 证书失败: %w", err)
	}
	if err := writeCertPEM(caCertPath(), der); err != nil {
		return nil, err
	}
	if err := writeKeyPEM(caKeyPath(), key); err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	logger.Infof("已生成本地 CA（指纹 %s，有效期至 %s）",
		Fingerprint(der), cert.NotAfter.Format("2006-01-02"))

	return &caBundle{cert: cert, key: key, der: der}, nil
}

// ensureLeaf 确保叶子证书与当前网络环境匹配。SAN 变了（换网络、加域名）或临期时自动重签，
// 用户不需要为「换了个网就打不开」做任何事。
func ensureLeaf(ca *caBundle, extraDomains []string) (tls.Certificate, error) {
	dnsNames, ips := desiredSANs(extraDomains)

	if reason := leafReusable(ca, dnsNames, ips); reason == "" {
		pair, err := tls.LoadX509KeyPair(leafCertPath(), leafKeyPath())
		if err == nil {
			return pair, nil
		}
		logger.Warnf("叶子证书加载失败，将重新签发: %v", err)
	} else {
		logger.Infof("重新签发服务器证书：%s", reason)
	}

	return generateLeaf(ca, dnsNames, ips)
}

// leafReusable 返回空串表示现有叶子证书可直接复用，否则返回需要重签的原因
func leafReusable(ca *caBundle, dnsNames []string, ips []net.IP) string {
	cert, _, err := readCertPEM(leafCertPath())
	if err != nil {
		return "尚无服务器证书"
	}
	if err := cert.CheckSignatureFrom(ca.cert); err != nil {
		return "服务器证书并非由当前本地 CA 签发"
	}
	if time.Until(cert.NotAfter) <= renewBefore {
		return "服务器证书即将过期"
	}
	if have, want := sanKey(cert.DNSNames, cert.IPAddresses), sanKey(dnsNames, ips); have != want {
		return fmt.Sprintf("本机地址已变化（%s → %s）", have, want)
	}
	return ""
}

func generateLeaf(ca *caBundle, dnsNames []string, ips []net.IP) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("生成服务器私钥失败: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return tls.Certificate{}, err
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "localhost",
			Organization: []string{organization},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(leafValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		// 现代浏览器完全忽略 CN，只看 SAN
		DNSNames:    dnsNames,
		IPAddresses: ips,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("签发服务器证书失败: %w", err)
	}
	if err := writeCertPEM(leafCertPath(), der); err != nil {
		return tls.Certificate{}, err
	}
	if err := writeKeyPEM(leafKeyPath(), key); err != nil {
		return tls.Certificate{}, err
	}

	logger.Infof("已签发服务器证书，SAN: %s", sanKey(dnsNames, ips))

	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        tmpl,
	}, nil
}

// desiredSANs 计算叶子证书应当覆盖的名字：localhost 与回环地址保证本机访问，
// 本机所有网卡 IP 保证 VPS / 局域网直接按 IP 访问也不报警告。
func desiredSANs(extraDomains []string) ([]string, []net.IP) {
	dnsSet := map[string]struct{}{"localhost": {}}
	for _, d := range extraDomains {
		if d = strings.TrimSpace(d); d != "" {
			dnsSet[d] = struct{}{}
		}
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		dnsSet[host] = struct{}{}
	}

	ipSet := map[string]net.IP{
		"127.0.0.1": net.ParseIP("127.0.0.1"),
		"::1":       net.ParseIP("::1"),
	}
	for _, ip := range localInterfaceIPs() {
		ipSet[ip.String()] = ip
	}

	dnsNames := make([]string, 0, len(dnsSet))
	for d := range dnsSet {
		dnsNames = append(dnsNames, d)
	}
	sort.Strings(dnsNames)

	keys := make([]string, 0, len(ipSet))
	for k := range ipSet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ips := make([]net.IP, 0, len(keys))
	for _, k := range keys {
		ips = append(ips, ipSet[k])
	}

	return dnsNames, ips
}

func localInterfaceIPs() []net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		logger.Warnf("枚举网卡失败，证书 SAN 将只覆盖回环地址: %v", err)
		return nil
	}

	var ips []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.IsLinkLocalUnicast() {
				// 链路本地地址（169.254.x / fe80::）会随网卡插拔频繁变化，
				// 收进 SAN 只会导致无谓的重签
				continue
			}
			ips = append(ips, ipNet.IP)
		}
	}
	return ips
}

// sanKey 把 SAN 归一化成一个可比较的字符串，用于判断是否需要重签
func sanKey(dnsNames []string, ips []net.IP) string {
	parts := make([]string, 0, len(dnsNames)+len(ips))
	parts = append(parts, dnsNames...)
	for _, ip := range ips {
		parts = append(parts, ip.String())
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("生成证书序列号失败: %w", err)
	}
	return serial, nil
}

func writeCertPEM(path string, der []byte) error {
	buf := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, buf, 0644); err != nil {
		return fmt.Errorf("写入证书 %s 失败: %w", path, err)
	}
	return nil
}

func writeKeyPEM(path string, key crypto.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("序列化私钥失败: %w", err)
	}
	buf := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, buf, 0600); err != nil {
		return fmt.Errorf("写入私钥 %s 失败: %w", path, err)
	}
	// Windows 上 0600 只映射到只读属性，不是真正的 ACL，必须再收一次权限
	hardenKey(path)
	return nil
}

func readCertPEM(path string) (*x509.Certificate, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, nil, fmt.Errorf("%s 不是有效的 PEM 证书", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("解析证书 %s 失败: %w", path, err)
	}
	return cert, block.Bytes, nil
}

func readKeyPEM(path string) (crypto.Signer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("%s 不是有效的 PEM 私钥", path)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析私钥 %s 失败: %w", path, err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("%s 的私钥类型不支持签名", path)
	}
	return signer, nil
}
