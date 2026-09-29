package fsutil

import (
	"path/filepath"
	"runtime"
	"strings"
)

// SamePath 按当前平台的文件系统语义比较两个路径字符串是否指向同一位置：
// 先各自 Abs + Clean；Windows（NTFS 默认大小写不敏感）忽略大小写，其余平台区分。
// 不解析符号链接 / junction，也不要求路径存在——比较的是「写法」而非 inode。
func SamePath(a, b string) bool {
	return samePathFor(runtime.GOOS, a, b)
}

func samePathFor(goos, a, b string) bool {
	if absA, err := filepath.Abs(a); err == nil {
		a = absA
	}
	if absB, err := filepath.Abs(b); err == nil {
		b = absB
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if goos == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
