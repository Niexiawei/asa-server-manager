package appconfig

import (
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
)

// Validate 校验配置并就地做归一化（小写、补默认值）。
//
// 校验失败一律返回错误而不是"过滤 + 告警"：用户配错了却看到功能"已启用"、
// 然后发现按钮不出现，排查成本远高于启动时直接告诉他哪一项写错了。
func (c *Config) Validate() error {
	if err := c.Server.validate(); err != nil {
		return err
	}
	return c.Auth.validate()
}

func (s *ServerConfig) validate() error {
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("server.port: 端口必须在 1-65535 之间，当前为 %d", s.Port)
	}
	if (s.TLS.CertFile == "") != (s.TLS.KeyFile == "") {
		return fmt.Errorf("server.tls: cert_file 与 key_file 必须同时提供或同时留空")
	}
	for i, p := range s.TrustedProxies {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if net.ParseIP(p) == nil {
			if _, _, err := net.ParseCIDR(p); err != nil {
				return fmt.Errorf("server.trusted_proxies[%d]: %q 既不是 IP 也不是 CIDR", i, p)
			}
		}
		s.TrustedProxies[i] = p
	}
	for i, o := range s.CORS.AllowedOrigins {
		o = strings.TrimSpace(strings.TrimSuffix(o, "/"))
		if !strings.HasPrefix(o, "http://") && !strings.HasPrefix(o, "https://") {
			return fmt.Errorf("server.cors.allowed_origins[%d]: %q 必须是完整 Origin，形如 https://ark.example.com", i, o)
		}
		s.CORS.AllowedOrigins[i] = o
	}
	return nil
}

func (a *AuthConfig) validate() error {
	if err := a.Session.validate(); err != nil {
		return err
	}
	if err := a.LANBypass.validate(); err != nil {
		return err
	}
	if a.TOTP.Skew > 10 {
		return fmt.Errorf("auth.totp.skew: %d 过大（每个窗口 30 秒，建议 1）", a.TOTP.Skew)
	}
	if a.Password.MinLength < 6 {
		return fmt.Errorf("auth.password.min_length: 不得小于 6，当前为 %d", a.Password.MinLength)
	}
	if a.Password.BcryptCost < 4 || a.Password.BcryptCost > 31 {
		return fmt.Errorf("auth.password.bcrypt_cost: 必须在 4-31 之间，当前为 %d", a.Password.BcryptCost)
	}
	if a.RateLimit.MaxFailures < 1 {
		return fmt.Errorf("auth.ratelimit.max_failures: 必须至少为 1，当前为 %d", a.RateLimit.MaxFailures)
	}
	if a.RateLimit.Window <= 0 {
		return fmt.Errorf("auth.ratelimit.window: 必须为正数时长，例如 15m")
	}
	if a.RateLimit.Lockout <= 0 {
		return fmt.Errorf("auth.ratelimit.lockout: 必须为正数时长，例如 15m")
	}
	if a.Audit.MaxRows < 100 {
		return fmt.Errorf("auth.audit.max_rows: 不得小于 100，当前为 %d", a.Audit.MaxRows)
	}
	return nil
}

func (s *SessionConfig) validate() error {
	if s.TTL <= 0 {
		return fmt.Errorf("auth.session.ttl: 必须为正数时长，例如 168h")
	}
	if s.IdleTimeout < 0 {
		return fmt.Errorf("auth.session.idle_timeout: 不得为负数（0 表示不启用空闲失效）")
	}
	if s.CookieName == "" {
		return fmt.Errorf("auth.session.cookie_name: 不得为空")
	}
	if s.CookiePath == "" {
		s.CookiePath = "/"
	}
	s.SameSite = strings.ToLower(strings.TrimSpace(s.SameSite))
	if !slices.Contains([]string{"lax", "strict", "none"}, s.SameSite) {
		return fmt.Errorf("auth.session.same_site: 只能是 lax / strict / none，当前为 %q", s.SameSite)
	}
	return nil
}

func (l *LANBypassConfig) validate() error {
	if len(l.Networks) == 0 {
		l.Networks = slices.Clone(DefaultPrivateNetworks)
	}
	if l.AutoDetectLocalSubnets {
		// 始终追加，不管上面 Networks 用的是默认值还是用户自定义：
		// 语义统一为"在你配置的网段之外，再信任我当前连接的物理局域网段"。
		l.Networks = append(l.Networks, detectLocalPrivateSubnets()...)
	}
	for i, n := range l.Networks {
		n = strings.TrimSpace(n)
		if _, _, err := net.ParseCIDR(n); err != nil {
			// 允许写裸 IP，自动补成单主机网段
			if ip := net.ParseIP(n); ip != nil {
				if ip.To4() != nil {
					n += "/32"
				} else {
					n += "/128"
				}
			} else {
				return fmt.Errorf("auth.lan_bypass.networks[%d]: %q 不是合法的 CIDR 或 IP", i, n)
			}
		}
		l.Networks[i] = n
	}
	return nil
}

// SameSiteMode 把配置里的字符串转成 http.SameSite
func (s *SessionConfig) SameSiteMode() http.SameSite {
	switch s.SameSite {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}
