//go:build linux

package mirror

import (
	"os"
	"testing"
)

// isLinkOnDisk 是「这个路径在盘上是不是链接」的地面真相，刻意不经过 isJunctionOrSymlink /
// fsutil.IsLink —— 那正是被测对象，用它判断会变成循环论证。
//
// Linux：createJunction 就是 os.Symlink，Lstat 的 ModeSymlink 在这里是可靠的判据
// （Mode 语义漂移只发生在 Windows 的 junction 上）。
func isLinkOnDisk(t *testing.T, path string) bool {
	t.Helper()
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}
