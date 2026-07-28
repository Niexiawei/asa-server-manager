package appconfig

import (
	"fmt"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
)

// domainPattern 是一个宽松但足够的域名字面量校验：小写字母/数字/连字符组成的
// label，用点分隔。"localhost" 也能通过。
var domainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

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
	if err := a.WebAuthn.validate(); err != nil {
		return err
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

func (w *WebAuthnConfig) validate() error {
	w.UserVerification = strings.ToLower(strings.TrimSpace(w.UserVerification))
	if !slices.Contains([]string{"discouraged", "preferred", "required"}, w.UserVerification) {
		return fmt.Errorf("auth.webauthn.user_verification: 只能是 discouraged / preferred / required，当前为 %q", w.UserVerification)
	}
	w.CloneDetection = strings.ToLower(strings.TrimSpace(w.CloneDetection))
	if !slices.Contains([]string{"off", "warn", "disable_credential"}, w.CloneDetection) {
		return fmt.Errorf("auth.webauthn.clone_detection: 只能是 off / warn / disable_credential，当前为 %q", w.CloneDetection)
	}

	seen := make(map[string]struct{}, len(w.Domains))
	out := w.Domains[:0]
	for i, d := range w.Domains {
		norm, err := NormalizeWebAuthnDomain(d)
		if err != nil {
			return fmt.Errorf("auth.webauthn.domains[%d]: %w", i, err)
		}
		if _, dup := seen[norm]; dup {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, norm)
	}
	w.Domains = out

	for i, o := range w.ExtraOrigins {
		o = strings.TrimSpace(strings.TrimSuffix(o, "/"))
		if !strings.HasPrefix(o, "http://") && !strings.HasPrefix(o, "https://") {
			return fmt.Errorf("auth.webauthn.extra_origins[%d]: %q 必须是完整 Origin，形如 https://ark.example.com", i, o)
		}
		w.ExtraOrigins[i] = o
	}
	return nil
}

// NormalizeWebAuthnDomain 把一项 webauthn.domains 配置归一化成合法的 RP ID。
//
// 错误信息刻意写得具体（指出是协议、端口还是路径写错了），因为这类配置
// 一旦写错，表现只是"Passkey 按钮不出现"，用户很难自己想到是配置问题。
func NormalizeWebAuthnDomain(d string) (string, error) {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimSuffix(d, ".") // FQDN 尾点
	if d == "" {
		return "", fmt.Errorf("不得为空")
	}
	if strings.Contains(d, "://") {
		return "", fmt.Errorf("不要带协议前缀，应为 %s", d[strings.Index(d, "://")+3:])
	}
	// IPv6 字面量含冒号，必须在端口判断之前先认出来
	if ip := net.ParseIP(strings.Trim(d, "[]")); ip != nil {
		return "", fmt.Errorf("IP 地址不能作为 RP ID，请使用域名或 localhost")
	}
	if strings.Contains(d, "/") {
		return "", fmt.Errorf("不要带路径，应为 %s", d[:strings.Index(d, "/")])
	}
	if strings.Contains(d, ":") {
		return "", fmt.Errorf("不要带端口，端口由 server.port 自动推导，应为 %s", d[:strings.Index(d, ":")])
	}
	if !domainPattern.MatchString(d) {
		return "", fmt.Errorf("%q 不是合法的域名", d)
	}
	return d, nil
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
