package mesh

import "context"

// PeerIdentity 是经隧道进来的请求的身份（§6.4）：由 B 的隧道服务端按 TLS 身份与授权表填写，
// 放在请求的 **context** 里——请求头可以被 A 伪造，context 不能。
type PeerIdentity struct {
	// NodeID 是对端节点 ID（完整格式）。
	NodeID string
	// Label 是本机给它起的备注名（没有时为对方自报的备注名或短 ID）。
	Label string
	// Role 是本机授予它的角色：RoleAdmin / RoleOperator。
	Role string
	// RemoteUser 是 A 上发起者的用户名——A 自述的，B 只能验证到「是 A 这台机器说的」，只用于审计。
	RemoteUser string
	// Addr 是对端的来源地址（直连的 TCP 地址，或 relay:<短 ID>）。
	Addr string
}

// ActorName 是审计里的操作者：peer:<备注名>/<A 上的用户名>。
func (p PeerIdentity) ActorName() string {
	user := p.RemoteUser
	if user == "" {
		user = "-"
	}
	return "peer:" + p.Label + "/" + user
}

type peerIdentityKey struct{}

// WithPeerIdentity 把身份挂到 context 上。只有隧道服务端调用它。
func WithPeerIdentity(ctx context.Context, p PeerIdentity) context.Context {
	return context.WithValue(ctx, peerIdentityKey{}, p)
}

// PeerIdentityFrom 取出隧道请求的身份；不是隧道请求时 ok 为 false。
func PeerIdentityFrom(ctx context.Context) (PeerIdentity, bool) {
	p, ok := ctx.Value(peerIdentityKey{}).(PeerIdentity)
	return p, ok
}
