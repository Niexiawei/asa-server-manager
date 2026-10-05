package atomicfile

import (
	"errors"
	"syscall"
)

// isChmodUnsupported：Windows 上 (*os.File).Chmod 不受支持（只有只读位有意义，
// 走的是路径版的 os.Chmod），返回 EWINDOWS。权限在 Windows 上靠目录 ACL，不靠 mode。
func isChmodUnsupported(err error) bool {
	return errors.Is(err, syscall.EWINDOWS)
}
