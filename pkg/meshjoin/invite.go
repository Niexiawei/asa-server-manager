package meshjoin

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"time"

	"asa-server/pkg/meshid"
)

const (
	inviteKind    = "asa-mesh-invite"
	inviteVersion = 1
	// InviteSecretLen 是邀请密钥的字节数。
	InviteSecretLen = 32
)

// Invite 是配对邀请码（docs/REMOTE_MANAGER_MESH_PLAN.md §6.2、§12 P3-2）：被控方 B 生成，
// 复制给控制方 A。A 据 Node 钉住 B 的公钥，在端到端 mTLS 里出示 ID + Secret。
// 含一次性密钥，fmt / slog 输出一律打码。
type Invite struct {
	// Node 是 B 的节点 ID。
	Node meshid.ID
	// ID 是邀请编号，B 据此找到记录（不用拿密钥逐条比对）。
	ID string
	// Secret 是一次性密钥（InviteSecretLen 字节）。
	Secret []byte
	// Role 是授予的角色。只是提示，以 B 侧记录为准。
	Role string
	// Addrs 是可选的 B 的直连地址（host:port）；带上它时无协调节点也能配对。
	Addrs []string
	// Expires 是过期时间。
	Expires time.Time
}

type wireInvite struct {
	Node    string   `json:"n"`
	ID      string   `json:"i"`
	Secret  string   `json:"s"`
	Role    string   `json:"r,omitempty"`
	Addrs   []string `json:"a,omitempty"`
	Expires int64    `json:"e"`
}

// Validate 检查各字段是否齐全。不检查是否过期——那是 B 的判断（A 的时钟可能不准）。
func (v Invite) Validate() error {
	if v.Node.IsZero() || v.ID == "" || len(v.Secret) != InviteSecretLen || v.Expires.IsZero() {
		return fmt.Errorf("%w：节点 ID、邀请编号、密钥、有效期都不能为空", ErrMalformed)
	}
	for _, a := range v.Addrs {
		if _, _, err := net.SplitHostPort(a); err != nil {
			return fmt.Errorf("%w：直连地址 %q 不是 host:port", ErrMalformed, a)
		}
	}
	return nil
}

// Encode 编码成 `asa-mesh-invite:v1:...` 字符串。
func (v Invite) Encode() (string, error) {
	if err := v.Validate(); err != nil {
		return "", err
	}
	return encode(inviteKind, inviteVersion, wireInvite{
		Node: v.Node.Compact(), ID: v.ID, Secret: base64.RawURLEncoding.EncodeToString(v.Secret),
		Role: v.Role, Addrs: v.Addrs, Expires: v.Expires.Unix(),
	})
}

// ParseInvite 解析邀请码。错误可用 errors.Is 区分 ErrPrefix / ErrVersion / ErrChecksum / ErrMalformed。
func ParseInvite(s string) (Invite, error) {
	var w wireInvite
	if err := decode(s, inviteKind, inviteVersion, &w); err != nil {
		return Invite{}, err
	}
	node, err := meshid.ParseID(w.Node)
	if err != nil {
		return Invite{}, fmt.Errorf("%w：节点 ID 无效", ErrMalformed)
	}
	secret, err := base64.RawURLEncoding.DecodeString(w.Secret)
	if err != nil {
		return Invite{}, fmt.Errorf("%w：密钥无效", ErrMalformed)
	}
	v := Invite{Node: node, ID: w.ID, Secret: secret, Role: w.Role, Addrs: w.Addrs}
	if w.Expires > 0 {
		v.Expires = time.Unix(w.Expires, 0)
	}
	if err := v.Validate(); err != nil {
		return Invite{}, err
	}
	return v, nil
}

// String 不含密钥。
func (v Invite) String() string {
	return fmt.Sprintf("Invite{Node: %s, ID: %s, Secret: %s, Role: %s, Addrs: %v, Expires: %s}",
		shortOrEmpty(v.Node), v.ID, redacted, v.Role, v.Addrs, v.Expires.Format(time.RFC3339))
}

// Format 让各种动词都走 String，不展开含密钥的字段。
func (v Invite) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(v.String()))
}

// LogValue 让 slog 输出同样打码。
func (v Invite) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("node", shortOrEmpty(v.Node)),
		slog.String("id", v.ID),
		slog.String("secret", redacted),
		slog.String("role", v.Role),
	)
}
