package authapi

import (
	"strings"

	"asa-server/internal/appconfig"
	"asa-server/internal/auth"

	"github.com/gin-gonic/gin"
)

// WebAuthn 可用性判定。
//
// 判定统一在后端做，前端只消费结果：规则只有一处实现，前端也不用自己
// 推导域名规则。任何一项不满足都返回 Available=false，前端据此**隐藏**
// Passkey 入口，用户继续用密码登录 —— 不报错、不阻塞、不显示无效按钮。
type availability struct {
	Available bool
	Reason    string
	RPID      string
}

// reason 取值
const (
	reasonDisabled     = "disabled"           // 配置未开启
	reasonNoDomains    = "no_domains"         // 开了但 domains 为空
	reasonInsecure     = "insecure_context"   // 非 HTTPS 且非 localhost
	reasonDomainDenied = "domain_not_allowed" // 当前域名/IP 不在 domains 内
)

func webAuthnAvailability(c *gin.Context) availability {
	cfg := appconfig.Get()
	w := cfg.Auth.WebAuthn

	switch {
	case !w.Enabled:
		return availability{Reason: reasonDisabled}
	case len(w.Domains) == 0:
		return availability{Reason: reasonNoDomains}
	case !isSecureContext(c, cfg.Server.TLS.Enabled):
		return availability{Reason: reasonInsecure}
	}

	// 用 IP 访问和用未配置的域名访问都归到这里：对前端来说处理方式一样，
	// 都是隐藏入口、退回密码登录
	rpID, ok := auth.MatchDomain(c.Request.Host, w.Domains)
	if !ok {
		return availability{Reason: reasonDomainDenied}
	}
	return availability{Available: true, RPID: rpID}
}

// isSecureContext 判断浏览器是否会把当前页面视为安全上下文。
// WebAuthn 要求安全上下文：HTTPS，或者 localhost（规范特例）。
func isSecureContext(c *gin.Context, tlsEnabled bool) bool {
	if tlsEnabled || c.Request.TLS != nil {
		return true
	}
	// 反代终结 TLS 的情况：上游会带 X-Forwarded-Proto。
	// gin 只信任 trusted_proxies 里的来源设置转发头，所以这里可以采信。
	if strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
		return true
	}
	host := c.Request.Host
	if h, _, err := splitHostPort(host); err == nil {
		host = h
	}
	return strings.EqualFold(host, "localhost")
}

func splitHostPort(hostport string) (string, string, error) {
	i := strings.LastIndex(hostport, ":")
	if i < 0 || strings.Contains(hostport[i+1:], "]") {
		return hostport, "", nil
	}
	return hostport[:i], hostport[i+1:], nil
}
