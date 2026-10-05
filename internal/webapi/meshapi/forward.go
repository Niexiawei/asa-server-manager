package meshapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"asa-server/internal/mesh"
	"asa-server/internal/realtime"
	"asa-server/internal/webapi/authapi"
	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
)

// forwardStrippedHeaders 是转发前删掉的请求头（§12 P3-7）：A 的会话凭证绝不能带给 B；
// 来源类的头在 B 那边没有意义（B 侧还会再删一遍）。Origin 在校验之后删掉。
var forwardStrippedHeaders = []string{
	"Cookie", "Authorization", "Proxy-Authorization",
	"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip", "Forwarded", "Origin", "Referer",
}

const tunnelHost = "mesh.peer"

// forwardPath 从 /api/peers/<id>/fwd/<rest> 里取出 /<rest>（保持转义）。
func forwardPath(escaped string) string {
	parts := strings.SplitN(escaped, "/", 6) // "", api, peers, id, fwd, rest
	if len(parts) < 6 {
		return "/"
	}
	return "/" + parts[5]
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// writeJSONError 写一个与 authapi 拒绝响应同形的错误（{"error","code"}），前端据 code 分辨。
func writeJSONError(w http.ResponseWriter, code int, errCode, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(gin.H{"error": msg, "code": errCode})
}

// forward 是 A 侧的转发入口：ReverseProxy + 隧道 RoundTripper（§7.2、§12 P3-7）。
func (h *Handler) forward(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	id, err := meshid.ParseID(c.Param("id"))
	if err != nil {
		writeJSONError(c.Writer, http.StatusBadRequest, "bad_peer_id", err.Error())
		return
	}
	// WebSocket 先在 A 侧按本机的同源规则校验 Origin：否则这里就成了一个绕过同源检查的 WS 入口。
	// 校验之后删掉 Origin 再转发（B 的 CheckOrigin 对空 Origin 放行）。
	if isWebSocketUpgrade(c.Request) && !realtime.WSUpgrader.CheckOrigin(c.Request) {
		writeJSONError(c.Writer, http.StatusForbidden, "forbidden", "跨源的 WebSocket 请求被拒绝")
		return
	}
	target, err := url.Parse(forwardPath(c.Request.URL.EscapedPath()))
	if err != nil {
		writeJSONError(c.Writer, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	user := authapi.ActorName(c)

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL = &url.URL{Scheme: "http", Host: tunnelHost, Path: target.Path, RawPath: target.RawPath, RawQuery: pr.In.URL.RawQuery}
			pr.Out.Host = tunnelHost
			for _, name := range forwardStrippedHeaders {
				pr.Out.Header.Del(name)
			}
		},
		Transport:      m.RoundTripper(id, user),
		ModifyResponse: modifyPeerResponse,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			switch {
			case errors.Is(err, context.Canceled):
				// 浏览器先走了，不用回答。
			case errors.Is(err, mesh.ErrPeerNotPaired):
				writeJSONError(w, http.StatusForbidden, "peer_not_paired", "对方没有授权本机，或已撤销授权")
			case errors.Is(err, mesh.ErrNotRunning):
				writeJSONError(w, http.StatusServiceUnavailable, "mesh_not_running", "本机的管理器互控没有在运行")
			default:
				writeJSONError(w, http.StatusBadGateway, "peer_unreachable", "无法连接对方："+err.Error())
			}
		},
		ErrorLog: nil,
	}
	start := time.Now()
	rp.ServeHTTP(c.Writer, c.Request)
	code := c.Writer.Status()
	switch c.Request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		logger.Debugf("[mesh] → %s %s %s → %d（%s）", id.Short(), c.Request.Method, target.Path, code, time.Since(start).Round(time.Millisecond))
	default:
		logger.Infof("[mesh] %s → %s %s %s → %d（%s）", user, id.Short(), c.Request.Method, target.Path, code, time.Since(start).Round(time.Millisecond))
	}
}

// modifyPeerResponse 清洗对端的响应：
//   - 删 Set-Cookie：B 不许往 A 的源上种 Cookie；
//   - B 回 401 改成 403 peer_unauthorized：A 的前端见到 401 会把**本机**登出。
func modifyPeerResponse(resp *http.Response) error {
	resp.Header.Del("Set-Cookie")
	if resp.StatusCode != http.StatusUnauthorized {
		return nil
	}
	_ = resp.Body.Close()
	body, _ := json.Marshal(gin.H{"error": "对方拒绝了本次请求（未认证）", "code": "peer_unauthorized"})
	resp.StatusCode = http.StatusForbidden
	resp.Status = strconv.Itoa(http.StatusForbidden) + " " + http.StatusText(http.StatusForbidden)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.Header.Set("Content-Type", "application/json; charset=utf-8")
	return nil
}
