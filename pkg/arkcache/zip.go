package arkcache

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// maxEntrySize / maxTotalSize 抄自 DownloadCacheFiles（ArkBaseApi.cpp:709-716）。
	maxEntrySize     = 512 << 20 // 512 MiB
	defaultMaxSize   = 768 << 20 // 768 MiB，同时是下载体的上限（:586）
	minZipEntryCount = 2
	maxZipEntryCount = 4
	maxZipEntryName  = 1024
)

// allowedZipEntries 是 ZIP 里**唯一**允许出现的四个名字（ArkBaseApi.cpp:701-705）。
// 按整串精确匹配 —— 不是后缀、不是 base name。出现任何其他名字，**整包拒绝**，
// 不做「清洗后接受」：含目录分隔符、".." 或盘符的名字自然落进"其他"，一并拒掉。
var allowedZipEntries = map[string]bool{
	offsetsFileName:    true,
	bitfieldsFileName:  true,
	metadataFileName:   true,
	offsetsTxtFileName: true,
}

// extractedZipEntries 是我们真正落盘的两个。
//
// ZIP 自带的 cached_key.cache 一律忽略：ArkApi 自己也只是"允许它存在"，从不落盘
// （:690-695）—— 它是**指针**文件，必须由我们在 generation 完整之后原子写出，
// 照抄 ZIP 里那份会指向一个根本不存在的 generation。
// cached_offsets.txt 是调试转储，ArkApi 会落盘但从不读，不提取。
var extractedZipEntries = map[string]bool{
	offsetsFileName:   true,
	bitfieldsFileName: true,
}

var errZipRejected = errors.New("ZIP 结构不合规")

// extractCacheZip 把 zipPath 里的两个 .cache 解到 destDir。
//
// 逐条对齐 DownloadCacheFiles（:641-771），并比它稍严一点：写入字节数用
// io.LimitReader 硬限并回比实际字节数，不单信 ZIP 头里的 uncompressed_size
// （zip bomb 的入口就在那里）。
func extractCacheZip(zipPath, destDir string, maxTotal int64) error {
	if maxTotal <= 0 {
		maxTotal = defaultMaxSize
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("%w: 打不开 ZIP: %v", errZipRejected, err)
	}
	defer zr.Close()

	if n := len(zr.File); n < minZipEntryCount || n > maxZipEntryCount {
		return fmt.Errorf("%w: 条目数 %d 不在 [%d,%d]", errZipRejected, n, minZipEntryCount, maxZipEntryCount)
	}

	var (
		seen  = make(map[string]bool, len(zr.File))
		total int64
	)
	for _, f := range zr.File {
		name := f.Name
		if len(name) == 0 || len(name) > maxZipEntryName {
			return fmt.Errorf("%w: 条目名长度 %d 越界", errZipRejected, len(name))
		}
		if strings.ContainsRune(name, 0) {
			return fmt.Errorf("%w: 条目名含 NUL", errZipRejected)
		}
		if !allowedZipEntries[name] {
			return fmt.Errorf("%w: 出现不在白名单里的条目 %q", errZipRejected, name)
		}
		// entrySeen（:692,709）：同名重复即整包拒绝，cached_key.cache 也不例外。
		if seen[name] {
			return fmt.Errorf("%w: 条目 %q 重复", errZipRejected, name)
		}
		seen[name] = true

		if !extractedZipEntries[name] {
			continue
		}
		size := int64(f.UncompressedSize64)
		if f.UncompressedSize64 > uint64(maxEntrySize) || size <= 0 {
			return fmt.Errorf("%w: 条目 %q 的 uncompressed_size %d 越界", errZipRejected, name, f.UncompressedSize64)
		}
		total += size
		if total > maxTotal {
			return fmt.Errorf("%w: 提取总量 %d 超过上限 %d", errZipRejected, total, maxTotal)
		}
	}
	for name := range extractedZipEntries {
		if !seen[name] {
			return fmt.Errorf("%w: 缺少必需条目 %q", errZipRejected, name)
		}
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	for _, f := range zr.File {
		if !extractedZipEntries[f.Name] {
			continue
		}
		if err := extractOne(f, filepath.Join(destDir, f.Name)); err != nil {
			return err
		}
	}
	return nil
}

func extractOne(f *zip.File, dest string) error {
	want := int64(f.UncompressedSize64)

	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("%w: 打开条目 %q 失败: %v", errZipRejected, f.Name, err)
	}
	defer rc.Close()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	// 多读 1 字节：声明 want 却吐出更多，说明 ZIP 头在撒谎。
	written, copyErr := io.Copy(out, io.LimitReader(rc, want+1))
	syncErr := out.Sync()
	closeErr := out.Close()
	switch {
	case copyErr != nil:
		os.Remove(dest)
		return fmt.Errorf("%w: 解压条目 %q 失败: %v", errZipRejected, f.Name, copyErr)
	case syncErr != nil:
		os.Remove(dest)
		return syncErr
	case closeErr != nil:
		os.Remove(dest)
		return closeErr
	case written != want:
		os.Remove(dest)
		return fmt.Errorf("%w: 条目 %q 实际 %d 字节，声明 %d 字节", errZipRejected, f.Name, written, want)
	}
	return nil
}
