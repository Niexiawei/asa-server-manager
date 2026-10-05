package meshjoin

import (
	"fmt"
	"log/slog"
	"net"

	"asa-server/pkg/meshid"
)

const (
	joinKind    = "asa-mesh-join"
	joinVersion = 1
)

// JoinBlob 是接入协调节点所需的全部信息。它含网络密钥，**不能**进日志：
// 本类型的 fmt / slog 输出一律把密钥打码，防止哪天被顺手 %v 出去。
type JoinBlob struct {
	// Addr 是协调节点地址（host:port）。
	Addr string
	// Coordinator 是协调节点的身份（其证书公钥的 SPKI 指纹），管理器连接时钉住它。
	Coordinator meshid.ID
	// NetworkID 是要加入的网络。
	NetworkID string
	// NetworkSecret 是首次接入该网络所需的密钥。
	NetworkSecret string
}

// wireJoin 是 JSON 里的样子。字段名取短，是为了让整串短一点、少被聊天软件折行。
type wireJoin struct {
	Addr   string `json:"a"`
	Coord  string `json:"c"`
	Net    string `json:"n"`
	Secret string `json:"s"`
}

// Validate 检查各字段是否齐全、地址是否是 host:port。
func (j JoinBlob) Validate() error {
	if j.Addr == "" || j.Coordinator.IsZero() || j.NetworkID == "" || j.NetworkSecret == "" {
		return fmt.Errorf("%w：地址、协调节点身份、网络 ID、网络密钥都不能为空", ErrMalformed)
	}
	if _, _, err := net.SplitHostPort(j.Addr); err != nil {
		return fmt.Errorf("%w：协调节点地址 %q 不是 host:port", ErrMalformed, j.Addr)
	}
	return nil
}

// Encode 编码成 `asa-mesh-join:v1:...` 字符串。
func (j JoinBlob) Encode() (string, error) {
	if err := j.Validate(); err != nil {
		return "", err
	}
	return encode(joinKind, joinVersion, wireJoin{
		Addr: j.Addr, Coord: j.Coordinator.Compact(), Net: j.NetworkID, Secret: j.NetworkSecret,
	})
}

// ParseJoinBlob 解析 join blob。错误可用 errors.Is 区分 ErrPrefix / ErrVersion / ErrChecksum / ErrMalformed。
func ParseJoinBlob(s string) (JoinBlob, error) {
	var w wireJoin
	if err := decode(s, joinKind, joinVersion, &w); err != nil {
		return JoinBlob{}, err
	}
	coord, err := meshid.ParseID(w.Coord)
	if err != nil {
		return JoinBlob{}, fmt.Errorf("%w：协调节点身份无效", ErrMalformed)
	}
	j := JoinBlob{Addr: w.Addr, Coordinator: coord, NetworkID: w.Net, NetworkSecret: w.Secret}
	if err := j.Validate(); err != nil {
		return JoinBlob{}, err
	}
	return j, nil
}

const redacted = "<已隐藏>"

// String 不含密钥。
func (j JoinBlob) String() string {
	return fmt.Sprintf("JoinBlob{Addr: %s, Coordinator: %s, NetworkID: %s, NetworkSecret: %s}",
		j.Addr, shortOrEmpty(j.Coordinator), j.NetworkID, redacted)
}

// Format 让 %v / %+v / %#v / %s 都走 String，不会把结构体字段（含密钥）原样展开。
func (j JoinBlob) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(j.String()))
}

// LogValue 让 slog 输出同样打码。
func (j JoinBlob) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("addr", j.Addr),
		slog.String("coordinator", shortOrEmpty(j.Coordinator)),
		slog.String("network_id", j.NetworkID),
		slog.String("network_secret", redacted),
	)
}

func shortOrEmpty(id meshid.ID) string {
	if id.IsZero() {
		return ""
	}
	return id.Short()
}
