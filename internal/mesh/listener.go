package mesh

import (
	"net"
	"sync"

	"asa-server/pkg/streamconn"
)

// injectListener 是由别处喂连接的 net.Listener：中转会话与打洞（QUIC）的入站连接都经它交给
// Peer gRPC 服务——它们不是从某个端口 Accept 来的。
type injectListener struct {
	ch        chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
}

func newInjectListener() *injectListener {
	return &injectListener{ch: make(chan net.Conn), closed: make(chan struct{})}
}

// deliver 把一条连接交给 Accept；监听器已关闭时关掉连接并返回 false。
func (l *injectListener) deliver(c net.Conn) bool {
	select {
	case l.ch <- c:
		return true
	case <-l.closed:
		c.Close()
		return false
	}
}

func (l *injectListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *injectListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *injectListener) Addr() net.Addr { return streamconn.Addr("inject") }
