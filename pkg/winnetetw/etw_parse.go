//go:build windows

package winnetetw

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unsafe"
)

// TDH 解析：把 EVENT_RECORD 的 payload 解出 (pid, size) 两个值（方案 §4.4）。
//
// 不硬编码 payload offset（上游设计文档 §15 的告诫）。策略分两层：
//
//   - 快路径：首次遇到某 Event ID 时用 TdhGetEventInformation 拿 schema，
//     按「属性声明顺序 + 标量自然对齐」推算 pid/size 的 offset，再用
//     TdhGetProperty 的权威值**验证一次**；通过后缓存 offset，后续事件直接读
//     payload（纳秒级）。
//   - 慢路径：schema 里有结构体/参数化/变长属性、或验证不通过时，退化为
//     每事件两次 TdhGetProperty（按属性名取值，TDH 内部有 schema 缓存）。
//
// schema 缓存只在 ProcessTrace 的回调线程里读写——单个 session 的回调是
// 串行的（方案 §4.5 的前提），不需要加锁。
//
// Kernel-Network 事件的属性名随系统版本可能是 PID/Pid、size/Size（中文系统
// 不本地化 manifest 字段名，但大小写有过漂移），一律按大小写不敏感匹配；
// 找不到这两个属性名的事件无法解析，整个 ID 标记失败并计数（Describe 可见）。

const (
	schemaPending uint8 = iota // 不存在（未初始化，map 里不会有这个状态）
	schemaFast                 // 快路径：offset 已验证
	schemaSlow                 // 慢路径：每事件走 TdhGetProperty
	schemaFailed               // 解析失败：该 ID 的事件全部丢弃
)

type eventSchema struct {
	state uint8

	// 快路径
	pidOffset  uint32
	sizeOffset uint32
	pidSize    uint8 // 4 或 8（PID 属性偶尔是 UInt64/Pointer）
	sizeSize   uint8

	// 慢路径（属性名的 UTF-16LE 字节串，含终止符）
	pidNameUTF16  []byte
	sizeNameUTF16 []byte
}

// schemaCache 是 Collector 的一部分，见 collector.go。key = Event ID。
type schemaCache map[uint16]*eventSchema

// buildEventSchema 在首次遇到某 Event ID 时调用一次（不在热路径上，
// 一次会话至多 8 个 ID）。rec 是触发解析的第一个事件实例。
func buildEventSchema(rec *eventRecord) *eventSchema {
	info, err := getEventInformation(rec)
	if err != nil {
		return &eventSchema{state: schemaFailed}
	}
	s := analyzeSchema(info)
	if s.state == schemaFailed {
		return s
	}

	// 静态 offset 用 TdhGetProperty 的权威值验证一次（防「顺序对齐」假设失效）
	if s.state == schemaFast {
		wantPID, ok1 := tdhPropertyValue(rec, s.pidNameUTF16)
		wantSize, ok2 := tdhPropertyValue(rec, s.sizeNameUTF16)
		gotPID, gotSize, ok3 := readPayloadValues(rec, s)
		if !ok1 || !ok2 || !ok3 || wantPID != gotPID || wantSize != gotSize {
			s.state = schemaSlow // 宁可慢，不可错
		}
	}
	return s
}

// analyzeSchema 解析 TRACE_EVENT_INFO：找 pid/size 两个属性名，
// 并在布局完全静态时算出它们的 payload offset。
func analyzeSchema(info []byte) *eventSchema {
	hdr := (*traceEventInfo)(unsafe.Pointer(&info[0]))
	count := int(hdr.TopLevelPropertyCount)

	s := &eventSchema{}
	var (
		pidName, sizeName string
		pidOff, sizeOff   = -1, -1
		staticValid       = true
		offset            = 0
	)

	for i := 0; i < count; i++ {
		p, ok := propertyAt(info, i)
		if !ok {
			return &eventSchema{state: schemaFailed}
		}
		name := nameAt(info, p.NameOffset)
		isPID := strings.EqualFold(name, "pid")
		isSize := strings.EqualFold(name, "size")
		if isPID {
			pidName = name
		}
		if isSize {
			sizeName = name
		}

		if staticValid {
			switch {
			case p.Flags&(propertyStruct|propertyParamLen|propertyParamCount) != 0:
				staticValid = false // 结构体/参数化属性：其后的 offset 静态算不了
			case inTypeSize(p.InType) == 0:
				staticValid = false // 变长属性（字符串/SID/Binary 等）
			default:
				sz := inTypeSize(p.InType)
				n := int(p.count) // 数组元素数（非数组恒 1；定长数组不破坏静态布局）
				if n < 1 {
					staticValid = false
					break
				}
				offset = (offset + sz - 1) / sz * sz // 自然对齐
				if isPID {
					pidOff, s.pidSize = offset, uint8(sz)
				}
				if isSize {
					sizeOff, s.sizeSize = offset, uint8(sz)
				}
				offset += sz * n
			}
		}
	}

	if pidName == "" || sizeName == "" {
		// 属性名都对不上：这版系统的事件模板不认识，别猜
		return &eventSchema{state: schemaFailed}
	}
	s.pidNameUTF16 = utf16NameBytes(pidName)
	s.sizeNameUTF16 = utf16NameBytes(sizeName)

	if staticValid && pidOff >= 0 && sizeOff >= 0 {
		s.state = schemaFast
		s.pidOffset = uint32(pidOff)
		s.sizeOffset = uint32(sizeOff)
	} else {
		s.state = schemaSlow
	}
	return s
}

// parsePayload 是 callback 热路径上的解析入口（schema 已缓存）。
// 返回 false 表示该事件解析失败（schema 失败或 payload 越界），调用方计数丢弃。
func parsePayload(rec *eventRecord, s *eventSchema) (pid, size uint32, ok bool) {
	switch s.state {
	case schemaFailed:
		return 0, 0, false
	case schemaFast:
		return readPayloadValues(rec, s)
	case schemaSlow:
		pid, ok1 := tdhPropertyValue(rec, s.pidNameUTF16)
		size, ok2 := tdhPropertyValue(rec, s.sizeNameUTF16)
		return pid, size, ok1 && ok2
	default:
		return 0, 0, false
	}
}

// readPayloadValues 按 schema 缓存的 offset 直接读。所有读取都做边界检查——
// callback 里越界 panic 会带崩整个进程（syscall.NewCallback 不 recover panic）。
func readPayloadValues(rec *eventRecord, s *eventSchema) (pid, size uint32, ok bool) {
	payload := unsafe.Slice((*byte)(rec.UserData), int(rec.UserDataLength))
	pid, ok = readUintLE(payload, s.pidOffset, s.pidSize)
	if !ok {
		return 0, 0, false
	}
	size, ok = readUintLE(payload, s.sizeOffset, s.sizeSize)
	if !ok {
		return 0, 0, false
	}
	return pid, size, true
}

// readUintLE 读 1/2/4/8 字节小端无符号整数并截断到 uint32。
func readUintLE(b []byte, off uint32, size uint8) (uint32, bool) {
	if int(off)+int(size) > len(b) {
		return 0, false
	}
	var v uint64
	switch size {
	case 1:
		v = uint64(b[off])
	case 2:
		v = uint64(binary.LittleEndian.Uint16(b[off:]))
	case 4:
		v = uint64(binary.LittleEndian.Uint32(b[off:]))
	case 8:
		v = binary.LittleEndian.Uint64(b[off:])
	default:
		return 0, false
	}
	return uint32(v), true
}

// propertyAt 返回第 i 个 EVENT_PROPERTY_INFO。数组紧跟 TRACE_EVENT_INFO
// 头部（offset 112），元素定长 24 字节。全部边界检查。
func propertyAt(info []byte, i int) (*eventPropertyInfo, bool) {
	const headerSize = 112
	const propSize = 24
	off := headerSize + i*propSize
	if off+propSize > len(info) {
		return nil, false
	}
	return (*eventPropertyInfo)(unsafe.Pointer(&info[off])), true
}

// nameAt 读 TRACE_EVENT_INFO 尾部的 UTF-16 属性名。offset 相对 info 起始，
// 越界/为零返回空串。
func nameAt(info []byte, off uint32) string {
	if off == 0 || int(off)+2 > len(info) {
		return ""
	}
	var runes []rune
	for p := int(off); p+2 <= len(info); p += 2 {
		u := binary.LittleEndian.Uint16(info[p : p+2])
		if u == 0 {
			break
		}
		runes = append(runes, rune(u))
	}
	return string(runes)
}

// utf16NameBytes 编码一个 UTF-16LE 含终止符的属性名（TdhGetProperty 用）。
func utf16NameBytes(name string) []byte {
	u := utf16.Encode([]rune(name + "\x00"))
	out := make([]byte, len(u)*2)
	for i, r := range u {
		binary.LittleEndian.PutUint16(out[i*2:], r)
	}
	return out
}

// getEventInformation 两段式获取 TRACE_EVENT_INFORMATION。TDH 不返回所需
// 大小（BufferSize 是值参数），只能从小到大倍增重试，上限 1MB。
func getEventInformation(rec *eventRecord) ([]byte, error) {
	size := 4096
	for size <= 1<<20 {
		buf := make([]byte, size)
		if rc := tdhGetEventInformation(rec, buf); rc == 0 {
			return buf, nil
		} else if rc != errInsufficientBuffer {
			return nil, fmt.Errorf("TdhGetEventInformation 失败: win32 error %d", rc)
		}
		size *= 2
	}
	return nil, errors.New("TdhGetEventInformation 缓冲区需求超过 1MB")
}

// tdhPropertyValue 按属性名取一个标量值（TdhGetProperty 慢路径 + schema 验证）。
func tdhPropertyValue(rec *eventRecord, nameUTF16 []byte) (uint32, bool) {
	if len(nameUTF16) == 0 {
		return 0, false
	}
	desc := propertyDataDescriptor{PropertyName: uintptr(unsafe.Pointer(&nameUTF16[0]))}
	var (
		buf  [8]byte
		size uint32
	)
	if rc := tdhGetProperty(rec, &desc, buf[:], &size); rc != 0 || size == 0 || size > 8 {
		return 0, false
	}
	v := binary.LittleEndian.Uint64(buf[:size])
	return uint32(v), true
}
