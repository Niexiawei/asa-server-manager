package appconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadCreatesTemplateWhenMissing(t *testing.T) {
	dir := t.TempDir()

	if err := Load(dir); err != nil {
		t.Fatalf("首次 Load 不应报错: %v", err)
	}
	path := filepath.Join(dir, ConfigFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("首次 Load 应写出 %s: %v", ConfigFileName, err)
	}

	cfg := Get()
	if cfg.Server.Port != 19193 {
		t.Errorf("默认端口应为 19193，实际 %d", cfg.Server.Port)
	}
	if cfg.Auth.Enabled {
		t.Error("auth.enabled 默认必须为 false —— 升级存量用户时不能把人锁在门外")
	}
	if !cfg.Server.TLS.Enabled {
		t.Error("tls.enabled 默认应为 true")
	}
}

// 模板文件本身必须能被解析和校验通过。否则用户第一次运行生成了配置，
// 第二次启动就会因为模板里的笔误而报错。
func TestGeneratedTemplateIsLoadable(t *testing.T) {
	dir := t.TempDir()

	if err := Load(dir); err != nil { // 生成模板
		t.Fatalf("首次 Load: %v", err)
	}
	if err := Load(dir); err != nil { // 这次真的读它
		t.Fatalf("读取自己生成的模板失败，模板有问题: %v", err)
	}

	cfg := Get()
	if cfg.Auth.Session.TTL != 168*time.Hour {
		t.Errorf("模板里的 ttl 应解析为 168h，实际 %v", cfg.Auth.Session.TTL)
	}
	if len(cfg.Auth.LANBypass.Networks) == 0 {
		t.Error("模板里的 lan_bypass.networks 未被解析")
	}
	if !cfg.Auth.LANBypass.DenyIfForwarded {
		t.Error("deny_if_forwarded 必须默认为 true")
	}
}

func TestLoadReadsUserValues(t *testing.T) {
	dir := t.TempDir()
	yaml := `
server:
  port: 8443
auth:
  enabled: true
  session:
    ttl: 2h
    same_site: strict
  webauthn:
    enabled: true
    domains:
      - LocalHost
      - ark.example.com.
`
	writeConfig(t, dir, yaml)

	if err := Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := Get()

	if cfg.Server.Port != 8443 {
		t.Errorf("端口应为 8443，实际 %d", cfg.Server.Port)
	}
	if !cfg.Auth.Enabled {
		t.Error("auth.enabled 应为 true")
	}
	if cfg.Auth.Session.TTL != 2*time.Hour {
		t.Errorf("ttl 应为 2h，实际 %v", cfg.Auth.Session.TTL)
	}
	// 未在文件里出现的字段应保留默认值
	if cfg.Auth.Password.BcryptCost != 12 {
		t.Errorf("bcrypt_cost 应回落到默认 12，实际 %d", cfg.Auth.Password.BcryptCost)
	}
	// 归一化：小写 + 去掉 FQDN 尾点
	want := []string{"localhost", "ark.example.com"}
	if len(cfg.Auth.WebAuthn.Domains) != 2 ||
		cfg.Auth.WebAuthn.Domains[0] != want[0] ||
		cfg.Auth.WebAuthn.Domains[1] != want[1] {
		t.Errorf("domains 应被归一化为 %v，实际 %v", want, cfg.Auth.WebAuthn.Domains)
	}
}

func TestLoadRejectsBadYAML(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "server:\n  port: [this is not an int\n")

	// 模拟进程刚启动的状态：current 里是 init() 存进去的默认值
	def := defaultConfig()
	current.Store(&def)

	if err := Load(dir); err == nil {
		t.Fatal("语法错误的 YAML 应返回错误")
	}
	// 关键：Load 失败不得清空 current。主程序据此"记 ERROR 并用默认配置继续启动"，
	// 而默认配置里 auth.enabled 为 false，所以配置写坏的最坏结果是不鉴权，
	// 不是把所有人锁在门外。
	cfg := Get()
	if cfg == nil {
		t.Fatal("Load 失败后 Get() 不得返回 nil")
	}
	if cfg.Auth.Enabled {
		t.Error("Load 失败后应保留默认配置（auth 关闭）")
	}
}

// 配置有错**且明确要求开启鉴权**时，错误必须能被识别出来，
// 好让主程序拒绝启动。否则一个 domains 的拼写错误就会让服务
// 静默地以"鉴权关闭"的默认值跑起来——而这台机器可能正暴露在公网上。
func TestInvalidAuthConfigIsFatal(t *testing.T) {
	t.Run("鉴权开启时可识别", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `
auth:
  enabled: true
  webauthn:
    domains:
      - 192.168.1.10
`)
		err := Load(dir)
		if err == nil {
			t.Fatal("非法 domains 应返回错误")
		}
		if !errors.Is(err, ErrAuthConfigInvalid) {
			t.Errorf("鉴权开启时的配置错误应可用 errors.Is 匹配 ErrAuthConfigInvalid，实际 %v", err)
		}
		// 错误信息仍要指出具体哪里错了
		if !strings.Contains(err.Error(), "domains[0]") {
			t.Errorf("错误信息应指出是第几项，实际 %q", err)
		}
	})

	t.Run("鉴权关闭时只是普通错误", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `
auth:
  enabled: false
  webauthn:
    domains:
      - 192.168.1.10
`)
		err := Load(dir)
		if err == nil {
			t.Fatal("非法 domains 应返回错误")
		}
		// 没开鉴权，回落默认值继续跑是安全的，不该让服务起不来
		if errors.Is(err, ErrAuthConfigInvalid) {
			t.Error("鉴权关闭时不该标记为致命错误")
		}
	})
}

// Load 失败时保留上一次成功加载的配置，而不是回退到默认值 ——
// 热重载场景下，配置文件被改坏不应该让正在运行的服务突然停止鉴权。
func TestFailedLoadKeepsLastGoodConfig(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "auth:\n  enabled: true\n")
	if err := Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !Get().Auth.Enabled {
		t.Fatal("前置条件失败：auth 应已启用")
	}

	writeConfig(t, dir, "auth:\n  enabled: [坏掉了\n")
	if err := Load(dir); err == nil {
		t.Fatal("应返回错误")
	}
	if !Get().Auth.Enabled {
		t.Error("Load 失败后应保留上一次成功的配置，鉴权不得被静默关闭")
	}
}

func TestEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "server:\n  port: 8443\n")
	t.Setenv("ASA_SERVER_PORT", "9999")

	if err := Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := Get().Server.Port; got != 9999 {
		t.Errorf("环境变量应覆盖文件，期望 9999，实际 %d", got)
	}
}

func TestNormalizeWebAuthnDomain(t *testing.T) {
	ok := []struct{ in, want string }{
		{"localhost", "localhost"},
		{"LocalHost", "localhost"},
		{"ark.example.com", "ark.example.com"},
		{"ark.example.com.", "ark.example.com"},
		{"  example.com  ", "example.com"},
		{"my-panel.example.co.uk", "my-panel.example.co.uk"},
	}
	for _, c := range ok {
		got, err := NormalizeWebAuthnDomain(c.in)
		if err != nil {
			t.Errorf("NormalizeWebAuthnDomain(%q) 不应报错: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeWebAuthnDomain(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}

	// 错误信息必须指出具体是哪种写法错了，否则用户只会看到"按钮不出现"
	bad := []struct{ in, wantMsg string }{
		{"", "不得为空"},
		{"192.168.1.10", "IP 地址"},
		{"::1", "IP 地址"},
		{"[::1]", "IP 地址"},
		{"https://ark.example.com", "协议前缀"},
		{"ark.example.com:19193", "端口"},
		{"ark.example.com/panel", "路径"},
		{"-bad.example.com", "不是合法的域名"},
	}
	for _, c := range bad {
		_, err := NormalizeWebAuthnDomain(c.in)
		if err == nil {
			t.Errorf("NormalizeWebAuthnDomain(%q) 应报错", c.in)
			continue
		}
		if !strings.Contains(err.Error(), c.wantMsg) {
			t.Errorf("NormalizeWebAuthnDomain(%q) 的错误信息 %q 应包含 %q", c.in, err, c.wantMsg)
		}
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"端口越界", func(c *Config) { c.Server.Port = 70000 }, "server.port"},
		{"证书只给一半", func(c *Config) { c.Server.TLS.CertFile = "a.pem" }, "同时提供"},
		{"可信代理非法", func(c *Config) { c.Server.TrustedProxies = []string{"not-an-ip"} }, "trusted_proxies"},
		{"CORS 缺协议", func(c *Config) { c.Server.CORS.AllowedOrigins = []string{"ark.example.com"} }, "allowed_origins"},
		{"same_site 非法", func(c *Config) { c.Auth.Session.SameSite = "sometimes" }, "same_site"},
		{"ttl 为零", func(c *Config) { c.Auth.Session.TTL = 0 }, "ttl"},
		{"网段非法", func(c *Config) { c.Auth.LANBypass.Networks = []string{"哦豁"} }, "networks"},
		{"skew 过大", func(c *Config) { c.Auth.TOTP.Skew = 99 }, "skew"},
		{"uv 非法", func(c *Config) { c.Auth.WebAuthn.UserVerification = "maybe" }, "user_verification"},
		{"克隆检测非法", func(c *Config) { c.Auth.WebAuthn.CloneDetection = "explode" }, "clone_detection"},
		{"domains 含 IP", func(c *Config) { c.Auth.WebAuthn.Domains = []string{"10.0.0.1"} }, "domains[0]"},
		{"密码过短", func(c *Config) { c.Auth.Password.MinLength = 3 }, "min_length"},
		{"bcrypt 成本越界", func(c *Config) { c.Auth.Password.BcryptCost = 99 }, "bcrypt_cost"},
		{"失败次数为零", func(c *Config) { c.Auth.RateLimit.MaxFailures = 0 }, "max_failures"},
		{"审计条数过少", func(c *Config) { c.Auth.Audit.MaxRows = 1 }, "max_rows"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := defaultConfig()
			c.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("应返回错误")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("错误信息 %q 应包含 %q", err, c.want)
			}
		})
	}
}

func TestValidateNormalizesLANBypass(t *testing.T) {
	cfg := defaultConfig()
	cfg.Auth.LANBypass.Networks = []string{"192.168.1.5", "::1", "10.0.0.0/8"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	want := []string{"192.168.1.5/32", "::1/128", "10.0.0.0/8"}
	for i, w := range want {
		if cfg.Auth.LANBypass.Networks[i] != w {
			t.Errorf("networks[%d] = %q，期望 %q", i, cfg.Auth.LANBypass.Networks[i], w)
		}
	}

	// 留空时应补上内置默认集
	cfg2 := defaultConfig()
	cfg2.Auth.LANBypass.Networks = nil
	if err := cfg2.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(cfg2.Auth.LANBypass.Networks) != len(DefaultPrivateNetworks) {
		t.Error("networks 留空时应回落到 DefaultPrivateNetworks")
	}
}

// 默认关闭时不应引入任何副作用——行为必须与 TestValidateNormalizesLANBypass
// 完全一致，升级到带这个开关的版本不能悄悄改变已有部署的信任范围。
func TestAutoDetectLocalSubnetsDefaultOffNoSideEffect(t *testing.T) {
	cfg := defaultConfig()
	cfg.Auth.LANBypass.Networks = []string{"10.0.0.0/8"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(cfg.Auth.LANBypass.Networks) != 1 || cfg.Auth.LANBypass.Networks[0] != "10.0.0.0/8" {
		t.Errorf("auto_detect_local_subnets 默认关闭时不应追加任何网段，实际 %v", cfg.Auth.LANBypass.Networks)
	}
}

// 开启后应在已有网段基础上追加探测结果（不假设具体内容，只断言不会变少、无错误）。
func TestAutoDetectLocalSubnetsAppendsWhenEnabled(t *testing.T) {
	cfg := defaultConfig()
	cfg.Auth.LANBypass.AutoDetectLocalSubnets = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(cfg.Auth.LANBypass.Networks) < len(DefaultPrivateNetworks) {
		t.Errorf("开启探测后网段数不应少于默认集，实际 %d 项", len(cfg.Auth.LANBypass.Networks))
	}
}

func TestValidateDeduplicatesDomains(t *testing.T) {
	cfg := defaultConfig()
	cfg.Auth.WebAuthn.Domains = []string{"example.com", "EXAMPLE.com.", "other.com"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(cfg.Auth.WebAuthn.Domains) != 2 {
		t.Errorf("重复域名应被去重，实际 %v", cfg.Auth.WebAuthn.Domains)
	}
}

func TestDatabasePath(t *testing.T) {
	cfg := defaultConfig()
	got := cfg.DatabasePath(`C:\asa`)
	want := filepath.Join(`C:\asa`, "database_file", "auth.db")
	if got != want {
		t.Errorf("DatabasePath = %q，期望 %q", got, want)
	}

	cfg.Auth.Database.Path = `D:\custom\auth.db`
	if got := cfg.DatabasePath(`C:\asa`); got != `D:\custom\auth.db` {
		t.Errorf("显式配置的 path 应优先，实际 %q", got)
	}
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(content), 0o644); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
}
