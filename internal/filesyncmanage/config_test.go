package filesyncmanage

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Niexiawei/simple-file-sync/pkg/joinblob"
)

func TestValidateAcceptsAWellFormedConfig(t *testing.T) {
	if err := validConfig(t, "sync.example.com:7443").Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestValidateAcceptsAnEnrolledMachineWithoutBootstrap：接入成功后引导凭据就没用了，
// 删掉它的机器必须仍能保存配置——但 CA 不能少，节点证书的握手也要用它验证协调端。
func TestValidateAcceptsAnEnrolledMachineWithoutBootstrap(t *testing.T) {
	cfg := validConfig(t, "sync.example.com:7443")
	cfg.BootstrapCertPEM, cfg.BootstrapKeyPEM = "", ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a config without bootstrap credentials should be valid: %v", err)
	}
	cfg.Address = "sync.example.com"
	if err := cfg.Validate(); err == nil {
		t.Fatal("the address must be checked even without bootstrap credentials")
	}
	cfg.Address = "sync.example.com:7443"
	cfg.CAPEM = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("a config without a CA must be rejected")
	}
}

func TestValidateRejects(t *testing.T) {
	other := newTestCreds(t, time.Hour)
	cases := map[string]func(*Config){
		"empty address":           func(c *Config) { c.Address = "" },
		"address without port":    func(c *Config) { c.Address = "sync.example.com" },
		"negative limit":          func(c *Config) { c.UploadLimitKBps = -1 },
		"cluster id with slash":   func(c *Config) { c.Clusters = []ClusterRoot{{ClusterID: "a/b"}} },
		"cluster id dot dot":      func(c *Config) { c.Clusters = []ClusterRoot{{ClusterID: ".."}} },
		"cluster id with colon":   func(c *Config) { c.Clusters = []ClusterRoot{{ClusterID: "c:x"}} },
		"certificate without key": func(c *Config) { c.BootstrapKeyPEM = "" },
		"key of another cert":     func(c *Config) { c.BootstrapKeyPEM = other.KeyPEM },
		"cert from another CA":    func(c *Config) { c.BootstrapCertPEM, c.BootstrapKeyPEM = other.CertPEM, other.KeyPEM },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig(t, "sync.example.com:7443")
			change(cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected Validate to fail")
			}
		})
	}
}

func TestValidateRejectsAnExpiredBootstrapCertificateWithAClearMessage(t *testing.T) {
	cfg := validConfig(t, "sync.example.com:7443")
	expired := newTestCreds(t, -time.Minute)
	cfg.CAPEM, cfg.BootstrapCertPEM, cfg.BootstrapKeyPEM = expired.CAPEM, expired.CertPEM, expired.KeyPEM
	err := cfg.Validate()
	if !errors.Is(err, joinblob.ErrInvalidCredential) || !strings.Contains(err.Error(), "重新生成") {
		t.Fatalf("expected an invalid-credential error telling the user to regenerate, got %v", err)
	}
}

func TestNormalizeTrimsAndDeduplicatesClusters(t *testing.T) {
	cfg := &Config{Address: "  sync.example.com:7443 ", Clusters: []ClusterRoot{
		{ClusterID: " alpha "}, {ClusterID: "beta"}, {ClusterID: "alpha"}, {ClusterID: "  "},
	}}
	cfg.Normalize()
	if cfg.Address != "sync.example.com:7443" {
		t.Fatalf("address not trimmed: %q", cfg.Address)
	}
	if got := strings.Join(cfg.clusterIDs(), ","); got != "alpha,beta" {
		t.Fatalf("clusters = %s, want alpha,beta in first-seen order", got)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadConfig(dir); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("an empty directory should report ErrNotConfigured, got %v", err)
	}
	cfg := validConfig(t, "sync.example.com:7443")
	if err := SaveConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Address != cfg.Address || loaded.BootstrapKeyPEM != cfg.BootstrapKeyPEM || len(loaded.Clusters) != 1 {
		t.Fatalf("round trip lost data: %+v", loaded)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(configPath(dir))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("config.json holds a private key and must be 0600, is %o", info.Mode().Perm())
		}
	}
}

// TestConnectionChangedSeparatesClusterEdits 钉住"改集群列表热更新、改连接重建节点"的分界：
// 把 Clusters 误算进连接字段，加一个集群就会断开所有在途传输。
func TestConnectionChangedSeparatesClusterEdits(t *testing.T) {
	prev := validConfig(t, "sync.example.com:7443")
	clustersOnly := prev.clone()
	clustersOnly.Clusters = append(clustersOnly.Clusters, ClusterRoot{ClusterID: "beta"})
	if clustersOnly.connectionChanged(prev) {
		t.Fatal("changing only the cluster list must not count as a connection change")
	}
	for name, change := range map[string]func(*Config){
		"address":   func(c *Config) { c.Address = "other.example.com:7443" },
		"enabled":   func(c *Config) { c.Enabled = false },
		"label":     func(c *Config) { c.Label = "x" },
		"limit":     func(c *Config) { c.DownloadLimitKBps = 10 },
		"ca":        func(c *Config) { c.CAPEM = "other" },
		"bootstrap": func(c *Config) { c.BootstrapKeyPEM = "other" },
	} {
		next := prev.clone()
		change(next)
		if !next.connectionChanged(prev) {
			t.Errorf("%s: expected a connection change", name)
		}
	}
	if !prev.connectionChanged(nil) {
		t.Fatal("a first configuration is a connection change")
	}
}

func TestApplyJoinBlobOverwritesAddressAndCredentials(t *testing.T) {
	creds := newTestCreds(t, time.Hour)
	blob, err := joinblob.Encode(joinblob.Info{
		Address: "blob.example.com:17443", CAPEM: []byte(creds.CAPEM), CertPEM: []byte(creds.CertPEM), KeyPEM: []byte(creds.KeyPEM),
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Address: "typed.example.com:7443"}
	if err := cfg.ApplyJoinBlob(blob); err != nil {
		t.Fatal(err)
	}
	if cfg.Address != "blob.example.com:17443" || cfg.BootstrapKeyPEM != creds.KeyPEM || cfg.CAPEM != creds.CAPEM {
		t.Fatalf("join blob not applied: %+v", cfg)
	}

	preview, err := InspectJoinBlob(blob)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Address != "blob.example.com:17443" || preview.CAFingerprint == "" || preview.CertNotAfter.IsZero() {
		t.Fatalf("incomplete preview: %+v", preview)
	}
}

func TestInspectJoinBlobExplainsAPasteAccident(t *testing.T) {
	_, err := InspectJoinBlob("filesync-join:v1:truncated")
	if err == nil || !strings.Contains(err.Error(), "重新复制") {
		t.Fatalf("expected a hint to copy it again, got %v", err)
	}
}
