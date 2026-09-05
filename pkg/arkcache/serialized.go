package arkcache

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// ArkApi 的 Cache.h 用 validateSerializedMap<T> 判断一个 .cache 文件能不能读进去。
// 那是一个**无头、无对齐**的记录流，一直读到文件尾：
//
//	repeat until EOF:
//	    keySize : size_t   (Win64 → 8 字节小端)
//	    key     : keySize 字节（不含结尾 NUL）
//	    value   : sizeof(T) 字节（原样 memcpy）
//
// 这里逐条复刻它的判定（Cache.h:19-71）。在写 cached_key.cache **之前**跑一遍，
// 就能在提交前断定「ArkApi 一定读得进去」——这是本方案相对「下完就用」的增强。
const (
	// maxKeySize / maxEntryCount 抄自 Cache.h 的同名常量。
	maxKeySize    = 1024 * 1024
	maxEntryCount = 5_000_000

	// offsetValueSize 是 cached_offsets.cache 的 T = intptr_t，Win64 下 8 字节。
	offsetValueSize = 8
	// bitfieldValueSize 是 cached_bitfields.cache 的 T = BitField：
	//
	//	struct BitField {
	//	    DWORD64   offset;        // 8 @ 0
	//	    DWORD     bit_position;  // 4 @ 8
	//	    /* 4 字节 padding        @ 12 */
	//	    ULONGLONG num_bits;      // 8 @ 16
	//	    ULONGLONG length;        // 8 @ 24
	//	};                           // alignof = 8 → sizeof = 32
	//
	// ⚠️ 那 4 字节 padding **是真的写进文件的**：serializeMap（Cache.h:88）按
	// sizeof(T) 把结构体整块 write 出去，padding 里是什么就写什么。校验器不得
	// 假设它为 0 —— 下面只按宽度跳过、不解释内容，所以天然满足。
	//
	// 两个宽度都是硬编码常量，**不做运行期推断**：推断出一个「碰巧也能读完」的
	// 错误宽度，比当场报错难查得多。真机上若解析不过（ArkApi 换了结构体定义），
	// 错误信息里带着条目序号与偏移，据此改这里即可。
	bitfieldValueSize = 32
)

// validateSerializedMap 按上面的规则校验 path。valueSize 是 sizeof(T)。
// 错误信息带上出错的条目序号与文件偏移 —— 排障时这比一个 bool 有用得多。
func validateSerializedMap(path string, valueSize int) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: 不是常规文件", path)
	}
	remaining := fi.Size()
	if remaining == 0 {
		return fmt.Errorf("%s: 文件为空", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var (
		r         = bufio.NewReaderSize(f, 64*1024)
		sizeBuf   [8]byte
		seen      = make(map[string]struct{})
		entry     int
		offset    int64
		keyBuf    []byte
		valueSkip = int64(valueSize)
	)

	for remaining > 0 {
		if entry >= maxEntryCount {
			return fmt.Errorf("%s: 条目数超过上限 %d", path, maxEntryCount)
		}
		if remaining < 8 {
			return fmt.Errorf("%s: 条目 #%d @%d 尾部残留 %d 字节，不足一个 keySize", path, entry, offset, remaining)
		}
		if _, err := io.ReadFull(r, sizeBuf[:]); err != nil {
			return fmt.Errorf("%s: 条目 #%d @%d 读 keySize 失败: %w", path, entry, offset, err)
		}
		keySize := binary.LittleEndian.Uint64(sizeBuf[:])
		remaining -= 8
		offset += 8

		switch {
		case keySize == 0:
			return fmt.Errorf("%s: 条目 #%d @%d keySize 为 0", path, entry, offset-8)
		case keySize > maxKeySize:
			return fmt.Errorf("%s: 条目 #%d @%d keySize %d 超过上限 %d", path, entry, offset-8, keySize, maxKeySize)
		case int64(keySize) > remaining:
			return fmt.Errorf("%s: 条目 #%d @%d keySize %d 超出剩余字节 %d", path, entry, offset-8, keySize, remaining)
		}

		if cap(keyBuf) < int(keySize) {
			keyBuf = make([]byte, keySize)
		}
		key := keyBuf[:keySize]
		if _, err := io.ReadFull(r, key); err != nil {
			return fmt.Errorf("%s: 条目 #%d @%d 读 key 失败: %w", path, entry, offset, err)
		}
		remaining -= int64(keySize)
		offset += int64(keySize)

		// unordered_set::emplace(...).second —— 重复 key 即非法。
		if _, dup := seen[string(key)]; dup {
			return fmt.Errorf("%s: 条目 #%d @%d key %q 重复", path, entry, offset-int64(keySize), key)
		}
		seen[string(key)] = struct{}{}

		if remaining < valueSkip {
			return fmt.Errorf("%s: 条目 #%d @%d 剩余 %d 字节不足一个 %d 字节的 value", path, entry, offset, remaining, valueSize)
		}
		if _, err := io.CopyN(io.Discard, r, valueSkip); err != nil {
			return fmt.Errorf("%s: 条目 #%d @%d 读 value 失败: %w", path, entry, offset, err)
		}
		remaining -= valueSkip
		offset += valueSkip
		entry++
	}

	if entry == 0 {
		return fmt.Errorf("%s: 没有任何条目", path)
	}
	return nil
}
