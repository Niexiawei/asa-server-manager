package mesh

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
	"asa-server/pkg/streamconn"
)

// TunnelRemoteAddr 是隧道请求的 RemoteAddr：RFC 6666 的丢弃前缀。无论直连还是中转都固定是它——
// 永远不会是回环或内网地址，任何按来源 IP 判断的旧逻辑（IsLoopbackRequest、lan_bypass）对它都不成立。
// 真实来源在 PeerIdentity.Addr 里。
const TunnelRemoteAddr = "[100::1]:0"

// tunnelHost 是隧道请求的 Host。
const tunnelHost = "mesh.peer"

// strippedRequestHeaders 是 B 侧在交给 Gin 之前删掉的请求头。A 侧也删，但 A 可能是恶意的，
// B 自己再删一遍（§12 P3-4）。
var strippedRequestHeaders = []string{
	"Cookie", "Authorization", "Proxy-Authorization",
	"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip", "Forwarded", "Origin",
}

// tunnelServer 是 Peer.HTTP 的服务端（B 侧）：一条流 = 一个请求，直接调本机 Gin engine 的 ServeHTTP，
// 不经过本机的 TCP 端口、TLS 与 CORS。
type tunnelServer struct {
	store   *PeerStore
	handler atomic.Pointer[http.Handler]

	// active 按节点 ID 登记在途隧道流的 cancel：授权撤销时逐个取消。
	mu     sync.Mutex
	active map[meshid.ID]map[*context.CancelFunc]struct{}
}

func (t *tunnelServer) track(id meshid.ID, cancel *context.CancelFunc) func() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active == nil {
		t.active = map[meshid.ID]map[*context.CancelFunc]struct{}{}
	}
	set := t.active[id]
	if set == nil {
		set = map[*context.CancelFunc]struct{}{}
		t.active[id] = set
	}
	set[cancel] = struct{}{}
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		delete(t.active[id], cancel)
		if len(t.active[id]) == 0 {
			delete(t.active, id)
		}
	}
}

// cancelID 取消 id 的全部在途隧道流，返回取消的条数。
func (t *tunnelServer) cancelID(id meshid.ID) int {
	t.mu.Lock()
	list := make([]context.CancelFunc, 0, len(t.active[id]))
	for c := range t.active[id] {
		list = append(list, *c)
	}
	t.mu.Unlock()
	for _, c := range list {
		c()
	}
	return len(list)
}

func (t *tunnelServer) setHandler(h http.Handler) {
	if h == nil {
		t.handler.Store(nil)
		return
	}
	t.handler.Store(&h)
}

func headersFromProto(hs []*meshpb.HTTPHeader) http.Header {
	h := make(http.Header, len(hs))
	for _, kv := range hs {
		name := http.CanonicalHeaderKey(kv.GetName())
		if name == "" {
			continue
		}
		h[name] = append(h[name], kv.GetValues()...)
	}
	return h
}

func headersToProto(h http.Header) []*meshpb.HTTPHeader {
	out := make([]*meshpb.HTTPHeader, 0, len(h))
	for k, v := range h {
		out = append(out, &meshpb.HTTPHeader{Name: k, Values: v})
	}
	return out
}

// buildTunnelRequest 由请求头帧构造 http.Request（不含 body 与 context）。
func buildTunnelRequest(head *meshpb.HTTPRequestHead) (*http.Request, error) {
	path := head.GetPath()
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, errors.New("路径必须以 / 开头")
	}
	uri := path
	if q := head.GetRawQuery(); q != "" {
		uri += "?" + q
	}
	u, err := url.ParseRequestURI(uri)
	if err != nil {
		return nil, err
	}
	method := head.GetMethod()
	if method == "" {
		method = http.MethodGet
	}
	h := headersFromProto(head.GetHeaders())
	for _, name := range strippedRequestHeaders {
		h.Del(name)
	}
	req := &http.Request{
		Method:     method,
		URL:        u,
		RequestURI: uri,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     h,
		Host:       tunnelHost,
		RemoteAddr: TunnelRemoteAddr,
	}
	u.Host = tunnelHost
	switch {
	case h.Get("Content-Length") != "":
		n, err := strconv.ParseInt(h.Get("Content-Length"), 10, 64)
		if err != nil || n < 0 {
			return nil, errors.New("Content-Length 无效")
		}
		req.ContentLength = n
	case strings.EqualFold(h.Get("Transfer-Encoding"), "chunked"):
		req.ContentLength = -1
		req.TransferEncoding = []string{"chunked"}
		h.Del("Transfer-Encoding")
	}
	return req, nil
}

// tunnelInput 是唯一调用 stream.Recv 的 goroutine：end 之前的 body 帧写进请求体的管道；
// end 之后的 body 帧（协议升级后的数据）交给被劫持的连接。
type tunnelInput struct {
	pr       *io.PipeReader
	pw       *io.PipeWriter
	upgraded chan []byte
	recvEnd  chan struct{} // Recv 结束（对端半关闭或流结束）时关闭
	recvErr  error
}

func newTunnelInput(ctx context.Context, stream meshpb.Peer_HTTPServer) *tunnelInput {
	pr, pw := io.Pipe()
	in := &tunnelInput{pr: pr, pw: pw, upgraded: make(chan []byte), recvEnd: make(chan struct{})}
	go in.run(ctx, stream)
	return in
}

func (in *tunnelInput) run(ctx context.Context, stream meshpb.Peer_HTTPServer) {
	defer close(in.recvEnd)
	bodyDone := false
	for {
		f, err := stream.Recv()
		if err != nil {
			in.recvErr = err
			if !bodyDone {
				if errors.Is(err, io.EOF) {
					err = io.ErrUnexpectedEOF
				}
				in.pw.CloseWithError(err)
			}
			return
		}
		switch m := f.GetMsg().(type) {
		case *meshpb.HTTPFrame_Body:
			if !bodyDone {
				// handler 不读请求体就返回时管道读端已关，写入失败——丢弃剩下的 body。
				_, _ = in.pw.Write(m.Body)
				continue
			}
			select {
			case in.upgraded <- m.Body:
			case <-ctx.Done():
				return
			}
		case *meshpb.HTTPFrame_End:
			if !bodyDone {
				bodyDone = true
				in.pw.Close()
			}
		}
	}
}

// tunnelWriter 是交给 Gin 的 http.ResponseWriter。响应头在第一次真正发数据时才发出
// （WriteHeader 之后 handler 还可能改 Header，例如 Gin 的 c.Header 写在 WriteHeader 之前）；
// body 攒到 MaxFrame 或 Flush 时发一帧。
type tunnelWriter struct {
	ctx    context.Context
	stream meshpb.Peer_HTTPServer
	in     *tunnelInput

	mu          sync.Mutex
	header      http.Header
	status      int
	wroteHeader bool
	headSent    bool
	buf         []byte
	err         error
	written     int64

	hijacked bool
	hc       *streamconn.Conn
}

func (w *tunnelWriter) Header() http.Header { return w.header }

func (w *tunnelWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writeHeaderLocked(code)
}

func (w *tunnelWriter) writeHeaderLocked(code int) {
	if w.wroteHeader || w.hijacked {
		return
	}
	// 1xx 信息性响应（101 之外）不转发：ReverseProxy 那一侧也不需要。
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		return
	}
	w.wroteHeader, w.status = true, code
}

func (w *tunnelWriter) sendLocked(f *meshpb.HTTPFrame) error {
	if w.err != nil {
		return w.err
	}
	if err := w.stream.Send(f); err != nil {
		w.err = err
	}
	return w.err
}

func (w *tunnelWriter) sendHeadLocked() error {
	if w.headSent {
		return w.err
	}
	w.headSent = true
	return w.sendLocked(&meshpb.HTTPFrame{Msg: &meshpb.HTTPFrame_Response{Response: &meshpb.HTTPResponseHead{
		Status: int32(w.status), Headers: headersToProto(w.header),
	}}})
}

func (w *tunnelWriter) flushLocked() error {
	if err := w.sendHeadLocked(); err != nil {
		return err
	}
	for len(w.buf) > 0 {
		n := min(len(w.buf), streamconn.MaxFrame)
		if err := w.sendLocked(&meshpb.HTTPFrame{Msg: &meshpb.HTTPFrame_Body{Body: w.buf[:n]}}); err != nil {
			return err
		}
		w.buf = w.buf[n:]
	}
	w.buf = w.buf[:0]
	return nil
}

func (w *tunnelWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.hijacked {
		return 0, http.ErrHijacked
	}
	w.writeHeaderLocked(http.StatusOK)
	if w.err != nil {
		return 0, w.err
	}
	w.buf = append(w.buf, p...)
	w.written += int64(len(p))
	if len(w.buf) >= streamconn.MaxFrame {
		if err := w.flushLocked(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Flush 实现 http.Flusher（SSE 要）。
func (w *tunnelWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.hijacked {
		return
	}
	w.writeHeaderLocked(http.StatusOK)
	_ = w.flushLocked()
}

// CloseNotify 实现 http.CloseNotifier：Gin 的 c.Stream 要它（不实现会 panic）。
func (w *tunnelWriter) CloseNotify() <-chan bool {
	ch := make(chan bool, 1)
	go func() {
		<-w.ctx.Done()
		ch <- true
	}()
	return ch
}

// Hijack 实现 http.Hijacker（gorilla/websocket 升级要）：返回由这条流包装的连接。
// gorilla 自己往连接里写 101 响应，B 不解析，原样作为 raw 帧转给 A，由 A 解析。
func (w *tunnelWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.hijacked {
		return nil, nil, http.ErrHijacked
	}
	if w.headSent || len(w.buf) > 0 {
		return nil, nil, errors.New("响应已开始发送，无法劫持连接")
	}
	w.hijacked = true
	w.hc = streamconn.New(streamconn.Options{
		Recv: func() ([]byte, error) {
			select {
			case b := <-w.in.upgraded:
				return b, nil
			case <-w.in.recvEnd:
				// recvEnd 关闭前可能刚好塞进了最后一条。
				select {
				case b := <-w.in.upgraded:
					return b, nil
				default:
				}
				if w.in.recvErr == nil {
					return nil, io.EOF
				}
				return nil, w.in.recvErr
			case <-w.ctx.Done():
				return nil, w.ctx.Err()
			}
		},
		Send: func(b []byte) error {
			w.mu.Lock()
			defer w.mu.Unlock()
			return w.sendLocked(&meshpb.HTTPFrame{Msg: &meshpb.HTTPFrame_Raw{Raw: b}})
		},
		RemoteAddr: streamconn.Addr(TunnelRemoteAddr),
		LocalAddr:  streamconn.Addr(tunnelHost),
	})
	return w.hc, bufio.NewReadWriter(bufio.NewReader(w.hc), bufio.NewWriter(w.hc)), nil
}

// finish 在 handler 返回后发出剩余的响应与 end。
func (w *tunnelWriter) finish() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writeHeaderLocked(http.StatusOK)
	if err := w.flushLocked(); err != nil {
		return err
	}
	return w.sendLocked(&meshpb.HTTPFrame{Msg: &meshpb.HTTPFrame_End{End: &meshpb.HTTPBodyEnd{}}})
}

func (w *tunnelWriter) statusCode() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.hijacked {
		return http.StatusSwitchingProtocols
	}
	return w.status
}

// serve 处理一条隧道流。
func (t *tunnelServer) serve(stream meshpb.Peer_HTTPServer) error {
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	id, ok := peerIDFromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "无法识别调用者")
	}
	untrack := t.track(id, &cancel)
	defer untrack()
	role := t.store.Grant(id)
	if role == "" {
		return status.Error(codes.PermissionDenied, "本机没有授权你的节点")
	}
	hp := t.handler.Load()
	if hp == nil {
		return status.Error(codes.Unavailable, "本机的 HTTP 服务尚未就绪")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	head := first.GetRequest()
	if head == nil {
		return status.Error(codes.InvalidArgument, "隧道流的首帧必须是请求头")
	}
	req, err := buildTunnelRequest(head)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	label := id.Short()
	if rec, ok := t.store.Peer(id); ok {
		label = rec.DisplayLabel()
	}
	pi := PeerIdentity{NodeID: id.String(), Label: label, Role: role, RemoteUser: head.GetRemoteUser(), Addr: peerAddrFromContext(ctx)}

	in := newTunnelInput(ctx, stream)
	defer in.pr.Close()
	req.Body = in.pr
	req = req.WithContext(WithPeerIdentity(ctx, pi))
	w := &tunnelWriter{ctx: ctx, stream: stream, in: in, header: http.Header{}}

	start := time.Now()
	(*hp).ServeHTTP(w, req)

	var serveErr error
	if w.hijacked {
		// gorilla 的 handler 在 WebSocket 结束后才返回，此时连接已关；等收尾（发完缓冲的 raw 帧）。
		w.hc.Close()
		select {
		case <-w.hc.Done():
		case <-ctx.Done():
		}
	} else {
		serveErr = w.finish()
	}
	logTunnelRequest(pi, req.Method, req.URL.Path, w.statusCode(), time.Since(start))
	if serveErr != nil && ctx.Err() == nil {
		return serveErr
	}
	return nil
}

// logTunnelRequest 记一行 B 侧的隧道请求日志：GET/HEAD/OPTIONS 记 DEBUG，其余 INFO（§12 P3-6）。
func logTunnelRequest(pi PeerIdentity, method, path string, code int, d time.Duration) {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		logger.Debugf("[mesh] %s %s %s → %d（%s）", pi.ActorName(), method, path, code, d.Round(time.Millisecond))
	default:
		logger.Infof("[mesh] %s %s %s → %d（%s）", pi.ActorName(), method, path, code, d.Round(time.Millisecond))
	}
}
