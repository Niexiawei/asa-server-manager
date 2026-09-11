package archive

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// 解压上限的默认值。上传的 zip 来自用户，条目数与解压后的字节数都要设防（zip bomb）。
const (
	DefaultMaxEntries    = 4096
	DefaultMaxTotalBytes = 1 << 30
)

// Limits 限定一次 zip 解压能消耗的资源。零值字段取默认值。
type Limits struct {
	MaxEntries    int
	MaxTotalBytes int64
}

// Entry 是一个解压出来的条目。
type Entry struct {
	Path  string // 相对解压目录的路径，forward slash，已规范化
	Size  int64  // 实际写出的字节数；目录为 0
	IsDir bool
}

// ExtractZip 把 zipPath 解压进 dest，返回实际解压出的条目（按包内顺序）。
//
// 先把全部条目校验一遍、再开始写盘，所以校验失败时 dest 里什么都没有；
// 写盘中途失败（超出字节上限、磁盘满）则可能留下部分文件，由调用方清理 dest。
//
// 拒绝：绝对路径、盘符、含 .. 的路径、含 : 的路径（Windows 的备用数据流）、符号链接、
// 仅大小写不同的重复路径（Windows 上会互相覆盖）、同一路径既是文件又是目录、
// 未置 UTF-8 标志且含非 ASCII 字符的条目名（编码无从判断，按错的编码解出来就是乱码文件名）。
// 忽略：__MACOSX/、.DS_Store、Thumbs.db。
// 字节上限按**实际写出**计数，不信 zip 头里声明的大小。
func ExtractZip(zipPath, dest string, lim Limits) ([]Entry, error) {
	if lim.MaxEntries <= 0 {
		lim.MaxEntries = DefaultMaxEntries
	}
	if lim.MaxTotalBytes <= 0 {
		lim.MaxTotalBytes = DefaultMaxTotalBytes
	}

	// 标准库对不安全的条目名只报 ErrInsecurePath 并照常返回 reader（取决于 GODEBUG），
	// 这里统一由下面的逐条校验来拒绝，给出能看懂的原因。
	r, err := zip.OpenReader(zipPath)
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, fmt.Errorf("不是有效的 zip 文件: %w", err)
	}
	defer r.Close()

	if len(r.File) > lim.MaxEntries {
		return nil, fmt.Errorf("zip 包含 %d 个条目，超过上限 %d", len(r.File), lim.MaxEntries)
	}

	type planned struct {
		f     *zip.File
		entry Entry
	}
	var plan []planned
	seen := newCaseIndex()
	for _, f := range r.File {
		name, skip, err := cleanEntryName(f)
		if err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		isDir := f.FileInfo().IsDir()
		if err := seen.add(name, isDir); err != nil {
			return nil, err
		}
		plan = append(plan, planned{f: f, entry: Entry{Path: name, IsDir: isDir}})
	}

	if err := os.MkdirAll(dest, 0755); err != nil {
		return nil, err
	}
	cleanDest := filepath.Clean(dest)
	remaining := lim.MaxTotalBytes
	entries := make([]Entry, 0, len(plan))
	for _, p := range plan {
		target := filepath.Join(cleanDest, filepath.FromSlash(p.entry.Path))
		// cleanEntryName 已经挡过，这里是纵深防御
		if !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			return nil, fmt.Errorf("zip 条目 %q 越出了解压目录", p.f.Name)
		}
		if p.entry.IsDir {
			if err := os.MkdirAll(target, 0755); err != nil {
				return nil, err
			}
			entries = append(entries, p.entry)
			continue
		}
		n, err := extractZipFile(p.f, target, remaining)
		if err != nil {
			return nil, err
		}
		remaining -= n
		p.entry.Size = n
		entries = append(entries, p.entry)
	}
	return entries, nil
}

// extractZipFile 写出一个文件，最多写 budget 字节，超出即报错。
func extractZipFile(f *zip.File, target string, budget int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return 0, err
	}
	rc, err := f.Open()
	if err != nil {
		return 0, fmt.Errorf("读取 zip 条目 %q 失败: %w", f.Name, err)
	}
	defer rc.Close()

	// O_EXCL：重复路径已在校验阶段拒绝，这里再撞上说明有漏洞，宁可失败也不覆盖
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(out, io.LimitReader(rc, budget+1))
	closeErr := out.Close()
	if copyErr != nil {
		return n, fmt.Errorf("解压 zip 条目 %q 失败: %w", f.Name, copyErr)
	}
	if closeErr != nil {
		return n, closeErr
	}
	if n > budget {
		return n, fmt.Errorf("解压后的总大小超过上限（解到 %q 时）", f.Name)
	}
	return n, nil
}

// cleanEntryName 把条目名规范化成 forward slash 的相对路径。skip=true 表示该条目应被忽略。
func cleanEntryName(f *zip.File) (name string, skip bool, err error) {
	raw := f.Name
	if f.NonUTF8 && !isASCII(raw) {
		return "", false, fmt.Errorf("zip 条目名 %q 不是 UTF-8 编码，请用 UTF-8 文件名重新打包", raw)
	}
	if !utf8.ValidString(raw) {
		return "", false, fmt.Errorf("zip 条目名 %q 不是合法的 UTF-8", raw)
	}
	if f.Mode()&fs.ModeSymlink != 0 {
		return "", false, fmt.Errorf("zip 条目 %q 是符号链接，不允许", raw)
	}

	// 有些 Windows 打包工具用反斜杠作分隔符
	n := strings.ReplaceAll(raw, `\`, "/")
	switch {
	case strings.HasPrefix(n, "/"):
		return "", false, fmt.Errorf("zip 条目 %q 是绝对路径，不允许", raw)
	case strings.Contains(n, ":"):
		return "", false, fmt.Errorf("zip 条目 %q 含盘符或冒号，不允许", raw)
	}
	if slices.Contains(strings.Split(n, "/"), "..") {
		return "", false, fmt.Errorf("zip 条目 %q 含 ..，不允许", raw)
	}
	n = path.Clean(strings.TrimSuffix(n, "/"))
	if n == "." || n == "" {
		return "", true, nil
	}
	if !filepath.IsLocal(filepath.FromSlash(n)) {
		return "", false, fmt.Errorf("zip 条目 %q 不是合法的相对路径", raw)
	}

	first, _, _ := strings.Cut(n, "/")
	base := path.Base(n)
	if strings.EqualFold(first, "__MACOSX") || strings.EqualFold(base, ".DS_Store") || strings.EqualFold(base, "Thumbs.db") {
		return "", true, nil
	}
	return n, false, nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// caseIndex 登记每条路径及其所有父目录（按小写），发现仅大小写不同、重复文件、
// 文件与目录同名这三种冲突。
type caseIndex struct {
	actual map[string]string // 小写 → 首次出现的实际写法
	isDir  map[string]bool   // 实际写法 → 是否目录
}

func newCaseIndex() *caseIndex {
	return &caseIndex{actual: map[string]string{}, isDir: map[string]bool{}}
}

func (c *caseIndex) add(p string, isDir bool) error {
	if dir := path.Dir(p); dir != "." {
		if err := c.add(dir, true); err != nil {
			return err
		}
	}
	low := strings.ToLower(p)
	prev, ok := c.actual[low]
	if !ok {
		c.actual[low] = p
		c.isDir[p] = isDir
		return nil
	}
	if prev != p {
		return fmt.Errorf("zip 里的 %q 与 %q 仅大小写不同，解压到 Windows 上会互相覆盖", p, prev)
	}
	if isDir && c.isDir[p] {
		return nil // 同一个目录出现多次（显式目录条目 + 文件的父目录）是正常的
	}
	if isDir != c.isDir[p] {
		return fmt.Errorf("zip 里的 %q 既是文件又是目录", p)
	}
	return fmt.Errorf("zip 里有重复的条目 %q", p)
}
