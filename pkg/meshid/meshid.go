// Package meshid 是管理器互控的身份层：Ed25519 密钥、节点 ID、钉公钥的 TLS 配置。
//
// 节点 ID = SHA-256(公钥 SPKI DER)，绑定的是**公钥**而不是证书：自签证书到期重签不改变 ID。
// 证书只是 TLS 握手的载体，校验一律按 SPKI 指纹钉住，不走任何 CA 链——
// 协调节点不当 CA，被攻破也冒充不了任何一台管理器（docs/REMOTE_MANAGER_MESH_PLAN.md §5.1、§6.1）。
//
// 只依赖标准库与 pkg/atomicfile：独立部署的协调节点也用它。
package meshid

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
)

// ID 是节点 ID：SPKI DER 的 SHA-256。
type ID [sha256.Size]byte

// encoding 是无填充的标准 base32（A–Z2–7）。32 字节编码成 52 个字符。
var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// groupLen 是显示时的分组长度：52 个字符分成 13 组，比一整串好核对。
const groupLen = 4

// ErrInvalidID 是 ParseID 对一切格式错误的统一回答。
var ErrInvalidID = errors.New("节点 ID 格式不正确")

// FromSPKI 由 SubjectPublicKeyInfo 的 DER 计算节点 ID。
func FromSPKI(spkiDER []byte) ID {
	return sha256.Sum256(spkiDER)
}

// FromCert 由证书计算节点 ID（只看其中的公钥）。
func FromCert(cert *x509.Certificate) ID {
	return FromSPKI(cert.RawSubjectPublicKeyInfo)
}

// FromPublicKey 由公钥计算节点 ID。
func FromPublicKey(pub any) (ID, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return ID{}, err
	}
	return FromSPKI(der), nil
}

// String 返回分组显示形式，形如 MFZW-I3DB-ONSG-...（13 组）。
func (id ID) String() string {
	raw := encoding.EncodeToString(id[:])
	var b strings.Builder
	b.Grow(len(raw) + len(raw)/groupLen)
	for i := 0; i < len(raw); i += groupLen {
		if i > 0 {
			b.WriteByte('-')
		}
		b.WriteString(raw[i:min(i+groupLen, len(raw))])
	}
	return b.String()
}

// Compact 返回不带分隔符的 52 字符形式，用于协议字段与文件名。
func (id ID) Compact() string {
	return encoding.EncodeToString(id[:])
}

// Short 返回前 8 个字符，供页面与日志显示。它**不是**唯一标识，不能用来查找节点。
func (id ID) Short() string {
	return encoding.EncodeToString(id[:])[:8]
}

// IsZero 报告 id 是否是零值（未设置）。
func (id ID) IsZero() bool {
	return id == ID{}
}

// Less 是确定的全序（按字节比较），打洞时决定谁发起握手之类的场合用（§5.6.3）。
func (id ID) Less(other ID) bool {
	return bytes.Compare(id[:], other[:]) < 0
}

// ParseID 解析节点 ID：接受大小写、有无 '-' / 空白分隔。
func ParseID(s string) (ID, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '-', ' ', '\t', '\r', '\n':
			return -1
		}
		return r
	}, strings.ToUpper(s))
	if len(cleaned) != encoding.EncodedLen(len(ID{})) {
		return ID{}, fmt.Errorf("%w：应为 %d 个字符，实际 %d 个", ErrInvalidID, encoding.EncodedLen(len(ID{})), len(cleaned))
	}
	raw, err := encoding.DecodeString(cleaned)
	if err != nil || len(raw) != len(ID{}) {
		return ID{}, ErrInvalidID
	}
	var id ID
	copy(id[:], raw)
	return id, nil
}

// MustParseID 是测试与常量用的 ParseID，出错 panic。
func MustParseID(s string) ID {
	id, err := ParseID(s)
	if err != nil {
		panic(err)
	}
	return id
}

// MarshalText 让 ID 在 JSON 里以分组形式出现。
func (id ID) MarshalText() ([]byte, error) {
	return []byte(id.String()), nil
}

// UnmarshalText 是 MarshalText 的逆，接受 ParseID 接受的一切形式。
func (id *ID) UnmarshalText(b []byte) error {
	parsed, err := ParseID(string(b))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}
