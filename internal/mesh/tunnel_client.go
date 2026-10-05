package mesh

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/meshid"
	"asa-server/pkg/streamconn"
)

var (
	// ErrPeerUnreachable：连不上对端（不在线、所有路径都失败）。
	ErrPeerUnreachable = errors.New("无法连接对端")
	// ErrPeerNotPaired：对端没有授权本机。
	ErrPeerNotPaired = errors.New("对方没有授权本机")
)

// tunnelError 把 gRPC 错误归到上面两类，供 A 侧的转发入口给出明确的状态码。
func tunnelError(err error) error {
	if err == nil {
		return nil
	}
	switch status.Code(err) {
	case codes.PermissionDenied:
		return fmt.Errorf("%w：%s", ErrPeerNotPaired, status.Convert(err).Message())
	case codes.Unavailable, codes.DeadlineExceeded, codes.NotFound:
		return fmt.Errorf("%w：%v", ErrPeerUnreachable, err)
	}
	if errors.Is(err, ErrNotRunning) {
		return err
	}
	return fmt.Errorf("%w：%v", ErrPeerUnreachable, err)
}

// RoundTripper 返回把请求经 Peer.HTTP 发给 peer 的 http.RoundTripper（A 侧，§12 P3-4）。
// remoteUser 是本机上发起者的用户名，随请求头帧告诉 B（只用于 B 的审计）。
func (m *Manager) RoundTripper(peer meshid.ID, remoteUser string) http.RoundTripper {
	return &tunnelTransport{m: m, peer: peer, user: remoteUser}
}

type tunnelTransport struct {
	m    *Manager
	peer meshid.ID
	user string
}

func (t *tunnelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	h, release, err := t.m.acquire(t.peer)
	if err != nil {
		closeBody(req)
		return nil, tunnelError(err)
	}
	ctx, cancel := context.WithCancel(req.Context())
	done := func() {
		cancel()
		release()
	}
	stream, err := meshpb.NewPeerClient(h.cc).HTTP(ctx)
	if err != nil {
		done()
		closeBody(req)
		return nil, tunnelError(err)
	}
	ts := &tunnelStream{stream: stream}
	head := &meshpb.HTTPRequestHead{
		Method:     req.Method,
		Path:       req.URL.EscapedPath(),
		RawQuery:   req.URL.RawQuery,
		Headers:    headersToProto(req.Header),
		RemoteUser: t.user,
	}
	if req.ContentLength > 0 && req.Header.Get("Content-Length") == "" {
		head.Headers = append(head.Headers, &meshpb.HTTPHeader{Name: "Content-Length", Values: []string{strconv.FormatInt(req.ContentLength, 10)}})
	}
	if err := ts.send(&meshpb.HTTPFrame{Msg: &meshpb.HTTPFrame_Request{Request: head}}); err != nil {
		done()
		closeBody(req)
		return nil, tunnelError(err)
	}
	// 请求体在后台抄过去（大文件上传是流式的，不整块缓冲），抄完发 end。
	go ts.copyBody(req.Body)

	first, err := stream.Recv()
	if err != nil {
		done()
		return nil, tunnelError(err)
	}
	switch m := first.GetMsg().(type) {
	case *meshpb.HTTPFrame_Response:
		resp := &http.Response{
			Status:     strconv.Itoa(int(m.Response.GetStatus())) + " " + http.StatusText(int(m.Response.GetStatus())),
			StatusCode: int(m.Response.GetStatus()),
			Proto:      "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
			Header:        headersFromProto(m.Response.GetHeaders()),
			ContentLength: -1,
			Request:       req,
			Body:          &tunnelBody{ts: ts, done: done},
		}
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			if n, err := strconv.ParseInt(cl, 10, 64); err == nil && n >= 0 {
				resp.ContentLength = n
			}
		}
		if req.Method == http.MethodHead {
			resp.Body.Close()
			resp.Body = http.NoBody
		}
		return resp, nil
	case *meshpb.HTTPFrame_Raw:
		// B 的连接被劫持（WebSocket）：raw 帧是 B 写出的原始字节，从 101 响应行开始。
		raw := &rawReader{ts: ts, pending: m.Raw}
		br := bufio.NewReader(raw)
		resp, err := http.ReadResponse(br, req)
		if err != nil {
			done()
			return nil, fmt.Errorf("解析对端的升级响应：%w", err)
		}
		if resp.StatusCode != http.StatusSwitchingProtocols {
			// 劫持后写的不是 101（少见）：照常当普通响应交出去。
			resp.Body = &readCloser{Reader: resp.Body, close: done}
			return resp, nil
		}
		// httputil.ReverseProxy 处理协议升级要求 Body 是 io.ReadWriteCloser。
		resp.Body = &upgradedBody{r: br, ts: ts, done: done}
		return resp, nil
	default:
		done()
		return nil, errors.New("对端的隧道响应格式不对")
	}
}

func closeBody(req *http.Request) {
	if req.Body != nil {
		req.Body.Close()
	}
}

// tunnelStream 串行化 Send（gRPC 不允许并发 Send：请求体抄写与升级后的写可能交替）。
type tunnelStream struct {
	stream meshpb.Peer_HTTPClient
	mu     sync.Mutex
	// bodyDone 在请求体抄完（发出 end）后关闭：升级后的写必须排在它后面。
	bodyDone chan struct{}
	once     sync.Once
}

func (ts *tunnelStream) send(f *meshpb.HTTPFrame) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.stream.Send(f)
}

func (ts *tunnelStream) bodyDoneCh() chan struct{} {
	ts.once.Do(func() { ts.bodyDone = make(chan struct{}) })
	return ts.bodyDone
}

func (ts *tunnelStream) copyBody(body io.ReadCloser) {
	defer close(ts.bodyDoneCh())
	if body != nil && body != http.NoBody {
		defer body.Close()
		buf := make([]byte, streamconn.MaxFrame)
		for {
			n, err := body.Read(buf)
			if n > 0 {
				if ts.send(&meshpb.HTTPFrame{Msg: &meshpb.HTTPFrame_Body{Body: append([]byte(nil), buf[:n]...)}}) != nil {
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					// 读请求体失败（浏览器中途断开）：不发 end，B 的 handler 会读到 ErrUnexpectedEOF。
					_ = ts.stream.CloseSend()
					return
				}
				break
			}
		}
	}
	_ = ts.send(&meshpb.HTTPFrame{Msg: &meshpb.HTTPFrame_End{End: &meshpb.HTTPBodyEnd{}}})
}

// tunnelBody 是普通响应的 Body：读 body 帧直到 end。
type tunnelBody struct {
	ts      *tunnelStream
	pending []byte
	eof     bool
	err     error
	once    sync.Once
	done    func()
}

func (b *tunnelBody) Read(p []byte) (int, error) {
	for len(b.pending) == 0 {
		if b.eof {
			return 0, io.EOF
		}
		if b.err != nil {
			return 0, b.err
		}
		f, err := b.ts.stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			b.err = err
			continue
		}
		switch m := f.GetMsg().(type) {
		case *meshpb.HTTPFrame_Body:
			b.pending = m.Body
		case *meshpb.HTTPFrame_End:
			b.eof = true
		}
	}
	n := copy(p, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}

func (b *tunnelBody) Close() error {
	b.once.Do(b.done)
	return nil
}

// rawReader 把 raw 帧串成字节流。
type rawReader struct {
	ts      *tunnelStream
	pending []byte
}

func (r *rawReader) Read(p []byte) (int, error) {
	for len(r.pending) == 0 {
		f, err := r.ts.stream.Recv()
		if err != nil {
			return 0, err
		}
		if raw := f.GetRaw(); len(raw) > 0 {
			r.pending = raw
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

// upgradedBody 是协议升级后的双向字节流：读 = B 发来的 raw，写 = 发给 B 的 body 帧。
type upgradedBody struct {
	r    *bufio.Reader
	ts   *tunnelStream
	once sync.Once
	done func()
}

func (u *upgradedBody) Read(p []byte) (int, error) { return u.r.Read(p) }

func (u *upgradedBody) Write(p []byte) (int, error) {
	<-u.ts.bodyDoneCh()
	written := 0
	for written < len(p) {
		end := min(written+streamconn.MaxFrame, len(p))
		if err := u.ts.send(&meshpb.HTTPFrame{Msg: &meshpb.HTTPFrame_Body{Body: append([]byte(nil), p[written:end]...)}}); err != nil {
			return written, err
		}
		written = end
	}
	return written, nil
}

func (u *upgradedBody) Close() error {
	u.once.Do(func() {
		u.ts.mu.Lock()
		_ = u.ts.stream.CloseSend()
		u.ts.mu.Unlock()
		u.done()
	})
	return nil
}

type readCloser struct {
	io.Reader
	once  sync.Once
	close func()
}

func (r *readCloser) Close() error {
	r.once.Do(r.close)
	return nil
}
