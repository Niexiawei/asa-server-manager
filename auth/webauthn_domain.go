package auth

import (
	"net"
	"slices"
	"strings"
)

// WebAuthn 的域名闸门。
//
// 这部分刻意和 go-webauthn 库解耦：它是纯函数，也是整个 WebAuthn 设计里
// 安全最关键的一环——RP ID 来自 Host 头，而 Host 头是客户端可控的。
// 不做白名单校验的话，任何人都能用伪造的 Host 让服务端在别的 RP ID 下
// 签发或接受凭证。

// MatchDomain 返回当前请求应使用的 RP ID。
//
// 不命中时返回 ok=false，调用方一律退回密码登录——WebAuthn 只是补充，
// 任何一步不满足都不该阻断用户。
func MatchDomain(host string, domains []string) (rpID string, ok bool) {
	host = normalizeHost(host)
	if host == "" || len(domains) == 0 {
		return "", false
	}
	// IP 地址不是合法的 RP ID（规范层面，无法绕过）。
	// 这意味着用 https://192.168.x.x:19193 访问时 WebAuthn 一定不可用。
	if net.ParseIP(host) != nil {
		return "", false
	}

	// 精确匹配优先：同时配了 example.com 和 ark.example.com 时，
	// 从 ark.example.com 访问应该用更具体的那个。
	if slices.Contains(domains, host) {
		return host, true
	}

	// 父域名匹配：配 example.com 可让所有子域名共享同一套凭证。
	// 这正是规范允许的「RP ID 必须是 Origin 有效域名的可注册后缀」。
	// 取最长的父域名，行为才可预测。
	best := ""
	for _, d := range domains {
		// 后缀判断必须带上点，否则 notexample.com 会被 example.com 误命中
		if strings.HasSuffix(host, "."+d) && len(d) > len(best) {
			best = d
		}
	}
	if best != "" {
		return best, true
	}
	return "", false
}

// OriginsFor 返回某个 RP ID 下所有合法的 Origin。
//
// Origin 校验是逐字符精确匹配的，端口也算在内。父域名场景下尤其要注意：
// RP ID 是 example.com 而实际 Origin 是 https://ark.example.com，两者不同，
// 必须把实际访问的主机名也列进去。
func OriginsFor(rpID string, domains []string, port int, tlsEnabled bool, extra []string) []string {
	hosts := []string{rpID}
	for _, d := range domains {
		if d != rpID && strings.HasSuffix(d, "."+rpID) {
			hosts = append(hosts, d)
		}
	}

	var out []string
	for _, h := range hosts {
		if tlsEnabled {
			out = append(out, "https://"+h)
			if port != 443 {
				out = append(out, "https://"+h+":"+itoa(port))
			}
			continue
		}
		// 关掉 TLS 时只有 localhost 还能用 WebAuthn：
		// 规范把它当作安全上下文特例，其余主机名在明文 HTTP 下一律不可用
		if h == "localhost" {
			out = append(out, "http://localhost")
			if port != 80 {
				out = append(out, "http://localhost:"+itoa(port))
			}
		}
	}
	return append(out, extra...)
}

// normalizeHost 把 Host 头归一化成可用于比较的域名：
// 去端口、去 IPv6 方括号、转小写、去掉 FQDN 尾点。
func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	host = strings.ToLower(host)
	return strings.TrimSuffix(host, ".")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
