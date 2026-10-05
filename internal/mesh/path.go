package mesh

import (
	"context"
	"errors"
	"fmt"
	"net"

	"asa-server/pkg/meshid"
)

// PathKind 是到对端的路径类型，决定优先级与页面显示（§5.3）。
type PathKind string

const (
	PathLAN     PathKind = "lan"     // P2
	PathPublic  PathKind = "public"  // P2
	PathPunched PathKind = "punched" // P6
	PathRelay   PathKind = "relay"
)

// pathProvider 是一种到达对端的方式。选路器内部按优先级尝试，上层（端到端 TLS、gRPC）
// 只拿到一个 net.Conn，不知道它从哪来——以后加直连、打洞、反向直连都是新增一个实现，不改上层。
type pathProvider interface {
	Kind() PathKind
	Dial(ctx context.Context, peer meshid.ID) (net.Conn, error)
}

// pathConn 记下连接走的是哪条路径。
type pathConn struct {
	net.Conn
	kind PathKind
}

// dialPeer 按优先级依次尝试各路径。P1 只有中转。
func dialPeer(ctx context.Context, providers []pathProvider, peer meshid.ID) (*pathConn, error) {
	var errs []error
	for _, p := range providers {
		c, err := p.Dial(ctx, peer)
		if err == nil {
			return &pathConn{Conn: c, kind: p.Kind()}, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", p.Kind(), err))
		if ctx.Err() != nil {
			break
		}
	}
	if len(errs) == 0 {
		return nil, errors.New("没有可用的路径")
	}
	return nil, errors.Join(errs...)
}
