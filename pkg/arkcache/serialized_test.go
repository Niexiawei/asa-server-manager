package arkcache

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// buildRecords 按 Cache.h 的记录流格式拼字节：keySize(8, LE) + key + value。
func buildRecords(t *testing.T, valueSize int, keys ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	for i, k := range keys {
		var size [8]byte
		binary.LittleEndian.PutUint64(size[:], uint64(len(k)))
		b.Write(size[:])
		b.WriteString(k)
		// value 内容刻意非零、且各条目不同：校验器只按宽度跳过，不得解释内容
		// —— BitField 那 4 字节 padding 是真的写进文件的，里面是什么就写什么。
		v := bytes.Repeat([]byte{byte(0xA0 + i)}, valueSize)
		b.Write(v)
	}
	return b.Bytes()
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.bin")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestValidateSerializedMapAcceptsWellFormed(t *testing.T) {
	for _, tc := range []struct {
		name      string
		valueSize int
		keys      []string
	}{
		{"offsets 多条目", offsetValueSize, []string{"AShooterGameMode.BeginPlay", "UWorld.Tick", "x"}},
		{"bitfields 多条目", bitfieldValueSize, []string{"bIsDead", "bReplicates"}},
		{"单条目边界", offsetValueSize, []string{"k"}},
		{"1 MiB 的 key 正好在上限", offsetValueSize, []string{string(bytes.Repeat([]byte("k"), maxKeySize))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeTemp(t, buildRecords(t, tc.valueSize, tc.keys...))
			if err := validateSerializedMap(p, tc.valueSize); err != nil {
				t.Fatalf("应当通过，却报错: %v", err)
			}
		})
	}
}

func TestValidateSerializedMapRejects(t *testing.T) {
	good := buildRecords(t, offsetValueSize, "alpha", "beta")

	zeroKeySize := make([]byte, 8+offsetValueSize) // keySize == 0

	oversizeKey := make([]byte, 8)
	binary.LittleEndian.PutUint64(oversizeKey, maxKeySize+1)
	oversizeKey = append(oversizeKey, bytes.Repeat([]byte("k"), 32)...)

	beyondEnd := make([]byte, 8)
	binary.LittleEndian.PutUint64(beyondEnd, 1024) // 声明 1024 字节的 key，文件里没那么多
	beyondEnd = append(beyondEnd, []byte("short")...)

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"空文件", nil},
		{"keySize 为 0", zeroKeySize},
		{"key 超过 1 MiB", oversizeKey},
		{"keySize 越过文件尾", beyondEnd},
		{"尾部残留字节", append(append([]byte{}, good...), 0x01, 0x02, 0x03)},
		{"value 被截断", good[:len(good)-1]},
		{"重复 key", buildRecords(t, offsetValueSize, "dup", "dup")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeTemp(t, tc.data)
			if err := validateSerializedMap(p, offsetValueSize); err == nil {
				t.Fatal("应当报错，却通过了")
			}
		})
	}
}

// bitfields 用 8 字节宽度去读会"碰巧也能读完"吗？这条用例把宽度写错时的行为钉住：
// 32 字节的记录流用 offsetValueSize 解析必须失败，否则宽度常量写错了也发现不了。
func TestValidateSerializedMapWidthMatters(t *testing.T) {
	p := writeTemp(t, buildRecords(t, bitfieldValueSize, "bIsDead"))
	if err := validateSerializedMap(p, bitfieldValueSize); err != nil {
		t.Fatalf("32 字节宽度应当通过: %v", err)
	}
	if err := validateSerializedMap(p, offsetValueSize); err == nil {
		t.Fatal("8 字节宽度不应通过")
	}
}

func TestValidateSerializedMapMissingFile(t *testing.T) {
	if err := validateSerializedMap(filepath.Join(t.TempDir(), "nope"), offsetValueSize); err == nil {
		t.Fatal("文件不存在时应当报错")
	}
}
