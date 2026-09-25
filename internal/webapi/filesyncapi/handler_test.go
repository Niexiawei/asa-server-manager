package filesyncapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Niexiawei/simple-file-sync/pkg/joinblob"
	"github.com/gin-gonic/gin"

	"asa-server/internal/filesyncmanage"
)

// testBlob 签一套 CA + 客户端证书并打成接入字符串。
func testBlob(t *testing.T, address string) (blob string, info joinblob.Info) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := time.Now()
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leafDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "bootstrap"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	info = joinblob.Info{
		Address: address,
		CAPEM:   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}
	blob, err = joinblob.Encode(info)
	if err != nil {
		t.Fatal(err)
	}
	return blob, info
}

func TestBuildConfigRejectsBlobAndFilesTogether(t *testing.T) {
	blob, info := testBlob(t, "sync.example.com:7443")
	_, err := buildConfig(nil, updateRequest{JoinBlob: blob, CAPEM: string(info.CAPEM)})
	if err == nil {
		t.Fatal("a request carrying both a join blob and PEM files must be rejected")
	}
}

// TestBuildConfigJoinBlobOverridesTypedAddress：接入字符串里的地址是协调端自己给出的，
// 优先于表单里手填的。
func TestBuildConfigJoinBlobOverridesTypedAddress(t *testing.T) {
	blob, info := testBlob(t, "blob.example.com:17443")
	next, err := buildConfig(nil, updateRequest{Enabled: true, Address: "typed.example.com:7443", JoinBlob: blob})
	if err != nil {
		t.Fatal(err)
	}
	if next.Address != "blob.example.com:17443" || next.BootstrapKeyPEM != string(info.KeyPEM) {
		t.Fatalf("join blob not applied: address %q", next.Address)
	}
}

// TestBuildConfigEmptyPEMKeepsStoredCredentials：已保存过凭据后只改地址或集群，
// 前端不会（也拿不到）重传 PEM，留空必须表示"不修改"。
func TestBuildConfigEmptyPEMKeepsStoredCredentials(t *testing.T) {
	_, info := testBlob(t, "sync.example.com:7443")
	current := &filesyncmanage.Config{
		Address: "sync.example.com:7443",
		CAPEM:   string(info.CAPEM), BootstrapCertPEM: string(info.CertPEM), BootstrapKeyPEM: string(info.KeyPEM),
	}
	next, err := buildConfig(current, updateRequest{
		Enabled: true, Address: "sync.example.com:7443", Clusters: []filesyncmanage.ClusterRoot{{ClusterID: "alpha"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.BootstrapKeyPEM != string(info.KeyPEM) || next.CAPEM != string(info.CAPEM) || len(next.Clusters) != 1 {
		t.Fatalf("stored credentials were dropped: %+v", next)
	}
	if current.Clusters != nil {
		t.Fatal("buildConfig must not modify the current configuration in place")
	}
}

// TestGetConfigNeverReturnsPEM：配置里有引导私钥，GET 的返回里不能出现任何 PEM 内容。
func TestGetConfigNeverReturnsPEM(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m, err := filesyncmanage.Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, info := testBlob(t, "sync.example.com:7443")
	cfg := &filesyncmanage.Config{
		Enabled: false, Address: info.Address,
		CAPEM: string(info.CAPEM), BootstrapCertPEM: string(info.CertPEM), BootstrapKeyPEM: string(info.KeyPEM),
	}
	if err := m.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}

	r := gin.New()
	NewHandler().RegisterRouter(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/filesync/config", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "BEGIN") || strings.Contains(body, "PRIVATE") {
		t.Fatalf("GET /config leaked PEM content: %s", body)
	}
	for _, want := range []string{`"has_bootstrap":true`, `"ca_fingerprint":`, `"address":"sync.example.com:7443"`} {
		if !strings.Contains(body, want) {
			t.Errorf("response is missing %s: %s", want, body)
		}
	}
}
