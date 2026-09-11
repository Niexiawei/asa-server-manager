//go:build windows

package mirror

import "testing"

// isLinkOnDisk 是「这个路径在盘上是不是链接」的地面真相，刻意不经过 isJunctionOrSymlink /
// fsutil.IsLink —— 那正是被测对象，用它判断会变成循环论证。
//
// Windows：直接读 FILE_ATTRIBUTE_REPARSE_POINT（真 NTFS junction 在 Lstat/Mode 上会漂移，
// 见 sync_safety_test.go 的 hasReparsePointAttr）。
func isLinkOnDisk(t *testing.T, path string) bool {
	t.Helper()
	return hasReparsePointAttr(t, path)
}
