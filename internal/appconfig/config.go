// Package appconfig 提供应用级配置（viper + YAML）。
//
// 它与 config 包（cfgpkg）是两件不同的事：cfgpkg 管的是目录布局和每个 ARK 实例的
// INI 配置，appconfig 管的是本程序自身的运行参数（端口、TLS、鉴权）。两者不要混。
//
// 依赖上 appconfig 是叶子包：只用标准库和 viper，不引入任何领域包，
// 这样 auth / webapi / certmgr 都能安全地依赖它而不成环。
package appconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spf13/viper"
)

// ConfigFileName 是 BaseDir 下的配置文件名
const ConfigFileName = "config.yaml"

// Config 是整份应用配置
type Config struct {
	Server ServerConfig `mapstructure:"server"`
	Auth   AuthConfig   `mapstructure:"auth"`
}

// ServerConfig 对应 HTTP 服务本身
type ServerConfig struct {
	Port           int        `mapstructure:"port"`
	TLS            TLSConfig  `mapstructure:"tls"`
	TrustedProxies []string   `mapstructure:"trusted_proxies"`
	CORS           CORSConfig `mapstructure:"cors"`
}

type TLSConfig struct {
	Enabled      bool     `mapstructure:"enabled"`
	TrustLocalCA bool     `mapstructure:"trust_local_ca"`
	CertFile     string   `mapstructure:"cert_file"`
	KeyFile      string   `mapstructure:"key_file"`
	Domains      []string `mapstructure:"domains"`
}

type CORSConfig struct {
	AllowedOrigins []string `mapstructure:"allowed_origins"`
}

// AuthConfig 是鉴权总配置。Enabled 为 false 时其余字段都不生效，
// 且完全不会打开 auth.db。
type AuthConfig struct {
	Enabled   bool            `mapstructure:"enabled"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Session   SessionConfig   `mapstructure:"session"`
	LANBypass LANBypassConfig `mapstructure:"lan_bypass"`
	TOTP      TOTPConfig      `mapstructure:"totp"`
	Password  PasswordConfig  `mapstructure:"password"`
	RateLimit RateLimitConfig `mapstructure:"ratelimit"`
	Audit     AuditConfig     `mapstructure:"audit"`
}

type DatabaseConfig struct {
	// Path 为空时用 {BaseDir}/database_file/auth.db
	Path        string `mapstructure:"path"`
	AutoMigrate bool   `mapstructure:"auto_migrate"`
}

type SessionConfig struct {
	TTL         time.Duration `mapstructure:"ttl"`
	IdleTimeout time.Duration `mapstructure:"idle_timeout"`
	CookieName  string        `mapstructure:"cookie_name"`
	CookiePath  string        `mapstructure:"cookie_path"`
	SameSite    string        `mapstructure:"same_site"` // lax | strict | none
}

// LANBypassConfig 控制内网免鉴权。
//
// 默认关闭是刻意的：本项目的典型部署是反代跑在同一台机器上转发到 127.0.0.1，
// 此时公网请求和本机请求的 RemoteAddr 完全一样。DenyIfForwarded 是把两者区分开的
// 唯一信号，所以它也默认为 true。
type LANBypassConfig struct {
	Enabled         bool     `mapstructure:"enabled"`
	Networks        []string `mapstructure:"networks"`
	DenyIfForwarded bool     `mapstructure:"deny_if_forwarded"`
	// AutoDetectLocalSubnets 额外把本机「物理网卡」当前所在的子网追加进 Networks，
	// 排除 Docker/Hyper-V/WSL2/VPN 等虚拟适配器，且只信任私有/链路本地地址。
	// 默认关闭：是对 Networks 的补充，不是替换，不能在升级后悄悄扩大信任范围。
	AutoDetectLocalSubnets bool `mapstructure:"auto_detect_local_subnets"`
}

type TOTPConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Required bool   `mapstructure:"required"`
	Issuer   string `mapstructure:"issuer"`
	Skew     uint   `mapstructure:"skew"`
}

type PasswordConfig struct {
	MinLength  int `mapstructure:"min_length"`
	BcryptCost int `mapstructure:"bcrypt_cost"`
}

type RateLimitConfig struct {
	MaxFailures int           `mapstructure:"max_failures"`
	Window      time.Duration `mapstructure:"window"`
	Lockout     time.Duration `mapstructure:"lockout"`
}

type AuditConfig struct {
	MaxRows int `mapstructure:"max_rows"`
}

// current 让读侧无锁：热重载时整体换指针即可。
var current atomic.Pointer[Config]

func init() {
	// 保证 Get() 在 Load() 之前（或 Load 失败后）也永远返回一份可用的配置。
	// 默认值里 auth.enabled 是 false，所以配置出问题时最坏结果是"不鉴权"，
	// 而不是"把所有人锁在门外"。
	def := defaultConfig()
	current.Store(&def)
}

// Get 返回当前生效的配置，永不返回 nil
func Get() *Config { return current.Load() }

// DatabasePath 返回鉴权数据库的绝对路径。baseDir 用于 Path 未显式配置时的回落。
func (c *Config) DatabasePath(baseDir string) string {
	if c.Auth.Database.Path != "" {
		return c.Auth.Database.Path
	}
	return filepath.Join(baseDir, "database_file", "auth.db")
}

// Load 读取 {baseDir}/config.yaml。
//
// 文件不存在时会写出一份带注释的模板再继续（不算错误）。
// 返回错误时调用方应记录日志并继续运行 —— 此时 Get() 仍返回默认配置。
func Load(baseDir string) error {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(baseDir)
	setDefaults(v)

	v.SetEnvPrefix("ASA")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return fmt.Errorf("读取 %s 失败: %w", ConfigFileName, err)
		}
		// 首次运行：写出带注释的模板，方便用户后续手改。
		// 写失败不阻断启动 —— 内存里的默认值一样能跑。
		if writeErr := writeDefaultConfig(filepath.Join(baseDir, ConfigFileName)); writeErr != nil {
			return fmt.Errorf("生成默认 %s 失败: %w", ConfigFileName, writeErr)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return wrapIfAuthWanted(v, fmt.Errorf("解析 %s 失败: %w", ConfigFileName, err))
	}
	if err := cfg.Validate(); err != nil {
		return wrapIfAuthWanted(v, err)
	}
	current.Store(&cfg)
	return nil
}

// ErrAuthConfigInvalid 表示配置有错，而且这份配置**明确要求开启鉴权**。
//
// 调用方必须让程序停下来，不能像普通配置错误那样"回落默认值继续跑"：
// 默认值里 auth.enabled 是 false，一个 domains 的拼写错误就会让服务
// 静默地不带鉴权启动 —— 而这台机器很可能正暴露在公网上。
// 配置错误应该表现为"起不来"，不该表现为"安全防护悄悄消失了"。
var ErrAuthConfigInvalid = errors.New("鉴权配置无效")

func wrapIfAuthWanted(v *viper.Viper, err error) error {
	if v.GetBool("auth.enabled") {
		return fmt.Errorf("%w: %w", ErrAuthConfigInvalid, err)
	}
	return err
}

// writeDefaultConfig 写出带注释的模板。
// 用手写模板而不是 viper.SafeWriteConfigAs，是因为后者会丢掉所有注释，
// 而这份文件是给用户手改的，注释比什么都重要。
func writeDefaultConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // 已存在就不覆盖
	}
	return os.WriteFile(path, []byte(defaultConfigTemplate), 0o644)
}

// DefaultPrivateNetworks 是 lan_bypass 未配置 networks 时使用的内网网段集合
var DefaultPrivateNetworks = []string{
	"127.0.0.0/8",
	"::1/128",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"169.254.0.0/16",
	"fc00::/7",
	"fe80::/10",
}

func defaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Port:           19193,
			TLS:            TLSConfig{Enabled: true, TrustLocalCA: true},
			TrustedProxies: []string{"127.0.0.1", "::1"},
		},
		Auth: AuthConfig{
			Enabled:  false,
			Database: DatabaseConfig{AutoMigrate: true},
			Session: SessionConfig{
				TTL:         7 * 24 * time.Hour,
				IdleTimeout: 24 * time.Hour,
				CookieName:  "asa_session",
				CookiePath:  "/",
				SameSite:    "lax",
			},
			LANBypass: LANBypassConfig{
				Enabled:                false,
				Networks:               DefaultPrivateNetworks,
				DenyIfForwarded:        true,
				AutoDetectLocalSubnets: false,
			},
			TOTP: TOTPConfig{
				Enabled: true,
				Issuer:  "ASA Server Manager",
				Skew:    1,
			},
			Password:  PasswordConfig{MinLength: 8, BcryptCost: 12},
			RateLimit: RateLimitConfig{MaxFailures: 5, Window: 15 * time.Minute, Lockout: 15 * time.Minute},
			Audit:     AuditConfig{MaxRows: 2000},
		},
	}
}

func setDefaults(v *viper.Viper) {
	d := defaultConfig()

	v.SetDefault("server.port", d.Server.Port)
	v.SetDefault("server.tls.enabled", d.Server.TLS.Enabled)
	v.SetDefault("server.tls.trust_local_ca", d.Server.TLS.TrustLocalCA)
	v.SetDefault("server.tls.cert_file", "")
	v.SetDefault("server.tls.key_file", "")
	v.SetDefault("server.tls.domains", []string{})
	v.SetDefault("server.trusted_proxies", d.Server.TrustedProxies)
	v.SetDefault("server.cors.allowed_origins", []string{})

	v.SetDefault("auth.enabled", d.Auth.Enabled)
	v.SetDefault("auth.database.path", "")
	v.SetDefault("auth.database.auto_migrate", d.Auth.Database.AutoMigrate)

	v.SetDefault("auth.session.ttl", d.Auth.Session.TTL)
	v.SetDefault("auth.session.idle_timeout", d.Auth.Session.IdleTimeout)
	v.SetDefault("auth.session.cookie_name", d.Auth.Session.CookieName)
	v.SetDefault("auth.session.cookie_path", d.Auth.Session.CookiePath)
	v.SetDefault("auth.session.same_site", d.Auth.Session.SameSite)

	v.SetDefault("auth.lan_bypass.enabled", d.Auth.LANBypass.Enabled)
	v.SetDefault("auth.lan_bypass.networks", d.Auth.LANBypass.Networks)
	v.SetDefault("auth.lan_bypass.deny_if_forwarded", d.Auth.LANBypass.DenyIfForwarded)
	v.SetDefault("auth.lan_bypass.auto_detect_local_subnets", d.Auth.LANBypass.AutoDetectLocalSubnets)

	v.SetDefault("auth.totp.enabled", d.Auth.TOTP.Enabled)
	v.SetDefault("auth.totp.required", d.Auth.TOTP.Required)
	v.SetDefault("auth.totp.issuer", d.Auth.TOTP.Issuer)
	v.SetDefault("auth.totp.skew", d.Auth.TOTP.Skew)

	v.SetDefault("auth.password.min_length", d.Auth.Password.MinLength)
	v.SetDefault("auth.password.bcrypt_cost", d.Auth.Password.BcryptCost)

	v.SetDefault("auth.ratelimit.max_failures", d.Auth.RateLimit.MaxFailures)
	v.SetDefault("auth.ratelimit.window", d.Auth.RateLimit.Window)
	v.SetDefault("auth.ratelimit.lockout", d.Auth.RateLimit.Lockout)

	v.SetDefault("auth.audit.max_rows", d.Auth.Audit.MaxRows)
}
