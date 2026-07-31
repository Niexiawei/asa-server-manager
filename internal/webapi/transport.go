package webapi

import (
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// protocolsH1H2 同时启用 HTTP/1.1 与 HTTP/2。
//
// ⚠️ SetHTTP1(true) 不是可选项：WebSocket 升级依赖 http.Hijacker 劫持底层 TCP 连接，
// 而 HTTP/2 的 ResponseWriter 不实现 Hijacker —— h2 的流没有独占 TCP 连接可劫持。
// 一旦「优化」成仅 HTTP/2，所有 WebSocket 会直接失败：
//
//	websocket: response does not implement http.Hijacker
//
// 分工是：h2 服务 REST 与 SSE（多路复用，摆脱每源 6 条连接的限制），
// HTTP/1.1 留给 WebSocket 升级，以及反向代理上游（Caddy/nginx 的 ALPN 只报 http/1.1）。
//
// 注意这里没有开 SetUnencryptedHTTP2：明文 h2c 浏览器根本不支持，开了只是徒增暴露面。
func protocolsH1H2() *http.Protocols {
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetHTTP2(true)
	return p
}

func http2Config() *http.HTTP2Config {
	return &http.HTTP2Config{
		// 9 条 SSE + REST 远用不到 250，留足余量即可
		MaxConcurrentStreams: 250,
		// SSE 可能长时间零流量，靠连接级 PING 探活，别让中间设备把连接判死。
		// 这是传输层兜底，各 SSE 端点自己的 keepalive 注释帧仍然要保留。
		SendPingTimeout: 15 * time.Second,
		PingTimeout:     30 * time.Second,
	}
}

// parseTrustedProxies 解析可信代理列表。返回 nil 表示谁都不信任，
// 此时 gin 的 ClientIP() 一律取真实 RemoteAddr，X-Forwarded-For 被忽略。
func parseTrustedProxies(raw string) []string {
	list := splitList(raw)
	if len(list) == 0 {
		return nil
	}
	return list
}

func splitList(raw string) []string {
	var out []string
	for item := range strings.SplitSeq(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// currentScheme 记录服务器实际使用的协议。它未必等于 EnableTLS 推出来的结果——
// 证书准备失败时 Start() 会退回明文 HTTP，此时 GUI 再拿 https 拼链接就打不开了。
var currentScheme atomic.Value // string

// Scheme 返回 WebUI 的实际访问协议，供 GUI 拼链接用。
// 服务器尚未启动时按配置给出预期值。
func Scheme() string {
	if s, ok := currentScheme.Load().(string); ok && s != "" {
		return s
	}
	if EnableTLS {
		return "https"
	}
	return "http"
}
