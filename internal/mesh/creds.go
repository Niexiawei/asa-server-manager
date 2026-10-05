package mesh

import (
	"context"
	"crypto/tls"
	"errors"
	"net"

	"google.golang.org/grpc/credentials"
)

// handshakenCreds 是对端 gRPC 客户端用的传输凭据：端到端 mTLS 已经在 dialer 里完成
// （§12 P2-1），这里不再握手，只把 TLS 状态交给 gRPC，上层看到的 AuthInfo 与
// credentials.NewTLS 给出的一样。
//
// 为什么握手要挪进 dialer：钉公钥的校验必须在**选路时**完成。若 dialer 只交出一条原始
// 连接、由 gRPC 去握手，拨到「恰好也叫 192.168.1.10 的别人家机器」时握手失败发生在 gRPC
// 内部 → gRPC 退避后再调同一个 dialer、再拨同一个错误地址，永远落不到中转。
type handshakenCreds struct{}

type tlsStater interface {
	ConnectionState() tls.ConnectionState
}

var errNotHandshaken = errors.New("mesh: dialer 必须交出已完成 TLS 握手的连接")

func (handshakenCreds) ClientHandshake(_ context.Context, _ string, conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	ts, ok := conn.(tlsStater)
	if !ok {
		conn.Close()
		return nil, nil, errNotHandshaken
	}
	return conn, credentials.TLSInfo{
		State:          ts.ConnectionState(),
		CommonAuthInfo: credentials.CommonAuthInfo{SecurityLevel: credentials.PrivacyAndIntegrity},
	}, nil
}

func (handshakenCreds) ServerHandshake(conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn.Close()
	return nil, nil, errors.New("mesh: handshakenCreds 只用于客户端")
}

func (handshakenCreds) Info() credentials.ProtocolInfo {
	return credentials.ProtocolInfo{SecurityProtocol: "tls", SecurityVersion: "1.3"}
}

func (c handshakenCreds) Clone() credentials.TransportCredentials { return c }

func (handshakenCreds) OverrideServerName(string) error { return nil }
