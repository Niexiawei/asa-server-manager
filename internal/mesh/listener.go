package mesh

import (
	"net"
	"sync"

	"asa-server/pkg/streamconn"
)

// relayListener 是由中转会话喂连接的 net.Listener：Peer gRPC 服务在它上面 Serve。
type relayListener struct {
	ch        chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
}

func newRelayListener() *relayListener {
	return &relayListener{ch: make(chan net.Conn), closed: make(chan struct{})}
}

// deliver 把一条连接交给 Accept；监听器已关闭时关掉连接并返回 false。
func (l *relayListener) deliver(c net.Conn) bool {
	select {
	case l.ch <- c:
		return true
	case <-l.closed:
		c.Close()
		return false
	}
}

func (l *relayListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *relayListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *relayListener) Addr() net.Addr { return streamconn.Addr("relay") }
