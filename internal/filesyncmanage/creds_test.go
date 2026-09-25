package filesyncmanage

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

// testCreds 是一套测试用的集群凭据：CA 与它签发的一张客户端证书（形状同引导证书）。
type testCreds struct {
	CAPEM, CertPEM, KeyPEM string
}

// newTestCreds 用标准库签一套凭据。同步库的 internal/pki 在本仓库 import 不到，
// 而 joinblob 只校验结构、签发关系、用途与有效期，这样就够了。
func newTestCreds(t *testing.T, validity time.Duration) testCreds {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "filesync-bootstrap", OrganizationalUnit: []string{"bootstrap"}},
		NotBefore:    now.Add(-time.Hour), NotAfter: now.Add(validity),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return testCreds{
		CAPEM:   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})),
		CertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})),
		KeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})),
	}
}

// validConfig 返回一份能通过 Validate 的配置。
func validConfig(t *testing.T, address string) *Config {
	t.Helper()
	creds := newTestCreds(t, time.Hour)
	return &Config{
		Enabled: true, Address: address,
		CAPEM: creds.CAPEM, BootstrapCertPEM: creds.CertPEM, BootstrapKeyPEM: creds.KeyPEM,
		Clusters: []ClusterRoot{{ClusterID: "alpha"}},
	}
}
