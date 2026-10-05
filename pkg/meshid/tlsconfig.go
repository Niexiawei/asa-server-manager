package meshid

import (
	"crypto/tls"
	"errors"
	"fmt"
)

// ErrNoPeerCert 表示对端没有出示证书。
var ErrNoPeerCert = errors.New("对端没有出示证书")

// PinMismatchError 表示对端出示的公钥不是期望的那一把。
type PinMismatchError struct {
	Want, Got ID
}

func (e *PinMismatchError) Error() string {
	return fmt.Sprintf("对端身份不符：期望 %s，实际 %s", e.Want.Short(), e.Got.Short())
}

// 下面三个 tls.Config 都**刻意**设置 InsecureSkipVerify / 不配 ClientCAs：
// 本系统不走 CA 链、不看主机名、不看有效期——身份就是公钥，校验就是比对 SPKI 指纹
// （VerifyConnection）。这是设计，不是偷懒，见包注释与 docs/REMOTE_MANAGER_MESH_PLAN.md §5.1。
//
// 用 VerifyConnection 而不是 VerifyPeerCertificate：前者在会话恢复时同样会被调用，
// 后者不会。另外服务端关掉会话票据、客户端不配会话缓存，让每条连接都完整握手、都出示证书。

// ClientConfig 返回拨向 expect 的客户端配置：出示 own，只接受公钥是 expect 的服务端。
func ClientConfig(own tls.Certificate, expect ID) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{own},
		InsecureSkipVerify: true, //nolint:gosec // 按 SPKI 钉公钥，见上
		VerifyConnection:   pinTo(expect),
		// ServerName 只进 SNI；协调节点挂在 Nginx stream 的 ssl_preread 后面时靠它分流。
	}
}

// ServerConfig 返回只接受客户端 expect 的服务端配置。
func ServerConfig(own tls.Certificate, expect ID) *tls.Config {
	return &tls.Config{
		MinVersion:             tls.VersionTLS13,
		Certificates:           []tls.Certificate{own},
		ClientAuth:             tls.RequireAnyClientCert,
		SessionTicketsDisabled: true,
		VerifyConnection:       pinTo(expect),
	}
}

// AnyClientServerConfig 返回「要求客户端证书、但接受任何公钥」的服务端配置：
// 由上层按 PeerID 取出的节点 ID 决定权限（协调节点的登记、管理器 Peer 服务的拦截器）。
func AnyClientServerConfig(own tls.Certificate) *tls.Config {
	return &tls.Config{
		MinVersion:             tls.VersionTLS13,
		Certificates:           []tls.Certificate{own},
		ClientAuth:             tls.RequireAnyClientCert,
		SessionTicketsDisabled: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			_, err := PeerID(cs)
			return err
		},
	}
}

// PeerID 从已完成的握手里取出对端的节点 ID。
func PeerID(cs tls.ConnectionState) (ID, error) {
	if len(cs.PeerCertificates) == 0 {
		return ID{}, ErrNoPeerCert
	}
	return FromCert(cs.PeerCertificates[0]), nil
}

func pinTo(expect ID) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		got, err := PeerID(cs)
		if err != nil {
			return err
		}
		if got != expect {
			return &PinMismatchError{Want: expect, Got: got}
		}
		return nil
	}
}
