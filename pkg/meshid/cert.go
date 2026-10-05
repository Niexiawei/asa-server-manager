package meshid

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"asa-server/pkg/atomicfile"
)

const (
	// KeyFileName / CertFileName 是 LoadOrCreate 在目录里使用的文件名。
	KeyFileName  = "node.key"
	CertFileName = "node.crt"

	// DefaultValidity 是自签证书的有效期。到期重签不改 ID，所以取长一点，
	// 省掉一类「证书过期导致连不上」的故障。
	DefaultValidity = 20 * 365 * 24 * time.Hour

	// renewBefore：证书剩余有效期不足它时，LoadOrCreate 顺手重签。
	renewBefore = 30 * 24 * time.Hour
)

// Generate 生成一把新的 Ed25519 私钥。
func Generate() (ed25519.PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	return priv, err
}

// SelfSignedCert 用 key 给自己签一张证书。CommonName 写节点 ID，只为人看（例如用
// openssl 查看时），校验从不读它。
func SelfSignedCert(key ed25519.PrivateKey, validity time.Duration) (tls.Certificate, error) {
	id, err := FromPublicKey(key.Public())
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: id.String()},
		// 往前留一小时，对方时钟稍慢时也不会判成「尚未生效」——虽然我们自己不验有效期，
		// 抓包排障时用的通用工具会验。
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, nil
}

// LoadOrCreate 从 dir 读取本机身份；没有就生成。私钥 node.key（PKCS#8 PEM，0600）
// 与证书 node.crt（PEM）都原子写入。
//
// 证书缺失、损坏、与私钥不匹配或临近到期时只重签证书，**不动私钥**——ID 因此不变。
// 私钥文件存在但读不出来时报错，绝不覆盖：那多半是权限或磁盘问题，悄悄换一把新钥
// 等于让所有已配对的对端都认不出本机。
func LoadOrCreate(dir string) (tls.Certificate, ID, error) {
	return LoadOrCreateFiles(filepath.Join(dir, KeyFileName), filepath.Join(dir, CertFileName))
}

// LoadOrCreateFiles 同 LoadOrCreate，但由调用方指定两个文件的路径（协调节点用
// coordinator.key / coordinator.crt）。
func LoadOrCreateFiles(keyPath, certPath string) (tls.Certificate, ID, error) {
	for _, d := range []string{filepath.Dir(keyPath), filepath.Dir(certPath)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return tls.Certificate{}, ID{}, err
		}
	}

	key, err := readKey(keyPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if key, err = Generate(); err != nil {
			return tls.Certificate{}, ID{}, err
		}
		if err := writeKey(keyPath, key); err != nil {
			return tls.Certificate{}, ID{}, fmt.Errorf("写入 %s: %w", keyPath, err)
		}
	case err != nil:
		return tls.Certificate{}, ID{}, fmt.Errorf("读取 %s: %w", keyPath, err)
	}

	id, err := FromPublicKey(key.Public())
	if err != nil {
		return tls.Certificate{}, ID{}, err
	}

	if cert, ok := readMatchingCert(certPath, key, id); ok {
		return cert, id, nil
	}
	cert, err := SelfSignedCert(key, DefaultValidity)
	if err != nil {
		return tls.Certificate{}, ID{}, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	if err := atomicfile.Write(certPath, pemBytes, 0o644); err != nil {
		return tls.Certificate{}, ID{}, fmt.Errorf("写入 %s: %w", certPath, err)
	}
	return cert, id, nil
}

// Reset 删除 dir 里的身份文件。下一次 LoadOrCreate 会生成新的 ID，已配对的对端需要重新配对。
func Reset(dir string) error {
	for _, name := range []string{KeyFileName, CertFileName} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func readKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("不是 PKCS#8 PEM 私钥")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("私钥类型是 %T，需要 Ed25519", parsed)
	}
	return key, nil
}

func writeKey(path string, key ed25519.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	return atomicfile.Write(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
}

// readMatchingCert 读 path 里的证书；它属于 key 且离到期还远时返回 ok。
func readMatchingCert(path string, key ed25519.PrivateKey, id ID) (tls.Certificate, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return tls.Certificate{}, false
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return tls.Certificate{}, false
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil || FromCert(leaf) != id {
		return tls.Certificate{}, false
	}
	if time.Until(leaf.NotAfter) < renewBefore {
		return tls.Certificate{}, false
	}
	return tls.Certificate{Certificate: [][]byte{block.Bytes}, PrivateKey: key, Leaf: leaf}, true
}
