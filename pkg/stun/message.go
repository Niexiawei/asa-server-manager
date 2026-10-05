// Package stun 实现 STUN Binding（RFC 5389）的一个最小子集：报文编解码、无状态服务端、
// 最小客户端与 NAT 映射行为判断。
//
// 只做「你的包是从哪个 IP:端口来的」这一件事——协调节点的 STUN 端点（P1-8）与 P6 管理器侧的
// 地址发现都只需要它。不做认证（MESSAGE-INTEGRITY）、不做 RFC 5780 的变 IP / 端口测试、
// 不做 TURN。见 docs/REMOTE_MANAGER_MESH_PLAN.md §5.6.3、§12「P1-8」。
//
// 只依赖标准库。编解码按 RFC 5769 的官方测试向量验证。
package stun

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"net/netip"
)

const (
	headerLen = 20

	// MagicCookie 是 RFC 5389 报文头里的固定值。
	MagicCookie uint32 = 0x2112A442

	fingerprintXOR uint32 = 0x5354554E
)

// 报文类型（方法 + 类别编码后的 16 位值）。
const (
	TypeBindingRequest uint16 = 0x0001
	TypeBindingSuccess uint16 = 0x0101
	TypeBindingError   uint16 = 0x0111
)

// 属性类型。0x0000–0x7FFF 是「必须理解」区间：不认识就得拒绝；0x8000–0xFFFF 可以忽略。
const (
	AttrMappedAddress     uint16 = 0x0001
	AttrChangeRequest     uint16 = 0x0003 // RFC 5780，本实现不支持
	AttrUsername          uint16 = 0x0006
	AttrMessageIntegrity  uint16 = 0x0008
	AttrErrorCode         uint16 = 0x0009
	AttrUnknownAttributes uint16 = 0x000A
	AttrXORMappedAddress  uint16 = 0x0020
	AttrPriority          uint16 = 0x0024 // ICE
	AttrSoftware          uint16 = 0x8022
	AttrFingerprint       uint16 = 0x8028
)

// Class 是报文类别。
type Class uint8

const (
	ClassRequest    Class = 0
	ClassIndication Class = 1
	ClassSuccess    Class = 2
	ClassError      Class = 3
)

// MethodBinding 是 Binding 方法的编号。
const MethodBinding uint16 = 0x001

// ErrMalformed 是 Parse 对一切格式错误的回答（具体原因包在里面）。
var ErrMalformed = errors.New("stun: 报文格式错误")

// TxID 是 96 位事务 ID。
type TxID [12]byte

// NewTxID 生成一个随机事务 ID。
func NewTxID() TxID {
	var id TxID
	_, _ = rand.Read(id[:])
	return id
}

// Attr 是一个属性。Value 不含填充。
type Attr struct {
	Type  uint16
	Value []byte
}

// Message 是一条 STUN 报文。
type Message struct {
	Type  uint16
	TxID  TxID
	Attrs []Attr
	// HasFingerprint 报告解析时是否带了（且通过校验的）FINGERPRINT。它不在 Attrs 里。
	HasFingerprint bool
}

// Method 返回报文的方法编号。
func (m *Message) Method() uint16 {
	t := m.Type
	return (t & 0x000F) | ((t & 0x00E0) >> 1) | ((t & 0x3E00) >> 2)
}

// Class 返回报文的类别。
func (m *Message) Class() Class {
	t := m.Type
	return Class(((t >> 4) & 1) | ((t >> 7) & 2))
}

// Get 返回第一个类型为 t 的属性值。
func (m *Message) Get(t uint16) ([]byte, bool) {
	for _, a := range m.Attrs {
		if a.Type == t {
			return a.Value, true
		}
	}
	return nil, false
}

// IsMessage 快速判断 b 看起来是不是 STUN 报文（首 2 位为 0 且 cookie 正确）。
// 与 QUIC 等协议复用同一个 UDP socket 时用来分流，不做完整校验。
func IsMessage(b []byte) bool {
	return len(b) >= headerLen && b[0]&0xC0 == 0 && binary.BigEndian.Uint32(b[4:8]) == MagicCookie
}

// Parse 严格解析一条报文：首 2 位为 0、长度是 4 的倍数且与实际一致、cookie 正确、
// 属性不越界；带 FINGERPRINT 时它必须是最后一个属性且 CRC 正确。
func Parse(b []byte) (*Message, error) {
	if len(b) < headerLen {
		return nil, fmt.Errorf("%w：长度 %d 不足报文头", ErrMalformed, len(b))
	}
	if b[0]&0xC0 != 0 {
		return nil, fmt.Errorf("%w：首 2 位不为 0", ErrMalformed)
	}
	length := int(binary.BigEndian.Uint16(b[2:4]))
	if length%4 != 0 || headerLen+length != len(b) {
		return nil, fmt.Errorf("%w：长度字段 %d 与实际 %d 不符", ErrMalformed, length, len(b)-headerLen)
	}
	if binary.BigEndian.Uint32(b[4:8]) != MagicCookie {
		return nil, fmt.Errorf("%w：magic cookie 不对", ErrMalformed)
	}
	m := &Message{Type: binary.BigEndian.Uint16(b[0:2])}
	copy(m.TxID[:], b[8:20])

	off := headerLen
	for off < len(b) {
		if len(b)-off < 4 {
			return nil, fmt.Errorf("%w：属性头越界", ErrMalformed)
		}
		typ := binary.BigEndian.Uint16(b[off : off+2])
		vlen := int(binary.BigEndian.Uint16(b[off+2 : off+4]))
		padded := (vlen + 3) &^ 3
		if len(b)-off-4 < padded {
			return nil, fmt.Errorf("%w：属性 0x%04X 越界", ErrMalformed, typ)
		}
		value := b[off+4 : off+4+vlen]
		if typ == AttrFingerprint {
			if vlen != 4 || off+8 != len(b) {
				return nil, fmt.Errorf("%w：FINGERPRINT 必须是最后一个属性", ErrMalformed)
			}
			want := crc32.ChecksumIEEE(b[:off]) ^ fingerprintXOR
			if binary.BigEndian.Uint32(value) != want {
				return nil, fmt.Errorf("%w：FINGERPRINT 校验失败", ErrMalformed)
			}
			m.HasFingerprint = true
			break
		}
		m.Attrs = append(m.Attrs, Attr{Type: typ, Value: value})
		off += 4 + padded
	}
	return m, nil
}

// Encode 编码报文。fingerprint 为 true 时在末尾追加 FINGERPRINT。
func (m *Message) Encode(fingerprint bool) []byte {
	size := headerLen
	for _, a := range m.Attrs {
		size += 4 + (len(a.Value)+3)&^3
	}
	if fingerprint {
		size += 8
	}
	b := make([]byte, size)
	binary.BigEndian.PutUint16(b[0:2], m.Type)
	binary.BigEndian.PutUint16(b[2:4], uint16(size-headerLen))
	binary.BigEndian.PutUint32(b[4:8], MagicCookie)
	copy(b[8:20], m.TxID[:])

	off := headerLen
	for _, a := range m.Attrs {
		binary.BigEndian.PutUint16(b[off:off+2], a.Type)
		binary.BigEndian.PutUint16(b[off+2:off+4], uint16(len(a.Value)))
		copy(b[off+4:], a.Value) // 填充字节保持为 0
		off += 4 + (len(a.Value)+3)&^3
	}
	if fingerprint {
		crc := crc32.ChecksumIEEE(b[:off]) ^ fingerprintXOR
		binary.BigEndian.PutUint16(b[off:off+2], AttrFingerprint)
		binary.BigEndian.PutUint16(b[off+2:off+4], 4)
		binary.BigEndian.PutUint32(b[off+4:off+8], crc)
	}
	return b
}

const (
	familyIPv4 = 0x01
	familyIPv6 = 0x02
)

// EncodeXORMappedAddress 编码 XOR-MAPPED-ADDRESS 的值。
//
// IPv4-mapped IPv6（::ffff:a.b.c.d）先还原成 IPv4 族：双栈 socket 收到的 IPv4 来源就是这种形式，
// 不处理的话 IPv4 客户端会拿到一个 IPv6 族的地址。
func EncodeXORMappedAddress(ap netip.AddrPort, tx TxID) []byte {
	addr := ap.Addr().Unmap()
	port := ap.Port() ^ uint16(MagicCookie>>16)
	key := xorKey(tx)
	if addr.Is4() {
		v := make([]byte, 8)
		v[1] = familyIPv4
		binary.BigEndian.PutUint16(v[2:4], port)
		ip := addr.As4()
		for i := range ip {
			v[4+i] = ip[i] ^ key[i]
		}
		return v
	}
	v := make([]byte, 20)
	v[1] = familyIPv6
	binary.BigEndian.PutUint16(v[2:4], port)
	ip := addr.As16()
	for i := range ip {
		v[4+i] = ip[i] ^ key[i]
	}
	return v
}

// DecodeXORMappedAddress 解码 XOR-MAPPED-ADDRESS 的值。
func DecodeXORMappedAddress(v []byte, tx TxID) (netip.AddrPort, error) {
	return decodeAddress(v, xorKey(tx), true)
}

// DecodeMappedAddress 解码老式 MAPPED-ADDRESS 的值（RFC 3489 服务器只回这个）。
func DecodeMappedAddress(v []byte) (netip.AddrPort, error) {
	return decodeAddress(v, [16]byte{}, false)
}

func decodeAddress(v []byte, key [16]byte, xor bool) (netip.AddrPort, error) {
	if len(v) < 4 {
		return netip.AddrPort{}, fmt.Errorf("%w：地址属性太短", ErrMalformed)
	}
	port := binary.BigEndian.Uint16(v[2:4])
	if xor {
		port ^= uint16(MagicCookie >> 16)
	}
	switch v[1] {
	case familyIPv4:
		if len(v) != 8 {
			return netip.AddrPort{}, fmt.Errorf("%w：IPv4 地址属性长度不对", ErrMalformed)
		}
		var ip [4]byte
		for i := range ip {
			ip[i] = v[4+i] ^ key[i]
		}
		return netip.AddrPortFrom(netip.AddrFrom4(ip), port), nil
	case familyIPv6:
		if len(v) != 20 {
			return netip.AddrPort{}, fmt.Errorf("%w：IPv6 地址属性长度不对", ErrMalformed)
		}
		var ip [16]byte
		for i := range ip {
			ip[i] = v[4+i] ^ key[i]
		}
		return netip.AddrPortFrom(netip.AddrFrom16(ip), port), nil
	default:
		return netip.AddrPort{}, fmt.Errorf("%w：未知地址族 %d", ErrMalformed, v[1])
	}
}

// xorKey = magic cookie ‖ 事务 ID。IPv4 只用前 4 字节。
func xorKey(tx TxID) [16]byte {
	var k [16]byte
	binary.BigEndian.PutUint32(k[0:4], MagicCookie)
	copy(k[4:], tx[:])
	return k
}

// EncodeErrorCode 编码 ERROR-CODE 的值。
func EncodeErrorCode(code int, reason string) []byte {
	v := make([]byte, 4, 4+len(reason))
	v[2] = byte(code/100) & 0x07
	v[3] = byte(code % 100)
	return append(v, reason...)
}

// DecodeErrorCode 解码 ERROR-CODE 的值。
func DecodeErrorCode(v []byte) (code int, reason string, err error) {
	if len(v) < 4 {
		return 0, "", fmt.Errorf("%w：ERROR-CODE 太短", ErrMalformed)
	}
	return int(v[2]&0x07)*100 + int(v[3]), string(v[4:]), nil
}

// EncodeUnknownAttributes 编码 UNKNOWN-ATTRIBUTES 的值。
func EncodeUnknownAttributes(types []uint16) []byte {
	v := make([]byte, 2*len(types))
	for i, t := range types {
		binary.BigEndian.PutUint16(v[2*i:], t)
	}
	return v
}

// DecodeUnknownAttributes 解码 UNKNOWN-ATTRIBUTES 的值。
func DecodeUnknownAttributes(v []byte) []uint16 {
	out := make([]uint16, 0, len(v)/2)
	for i := 0; i+1 < len(v); i += 2 {
		out = append(out, binary.BigEndian.Uint16(v[i:]))
	}
	return out
}

// comprehensionRequired 报告属性类型是否在「必须理解」区间。
func comprehensionRequired(t uint16) bool { return t < 0x8000 }
