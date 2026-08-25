//go:build windows

package tail

import (
	"fmt"
	"os"
	"syscall"
)

// fileKey returns a stable file identity that changes only on rotation (new
// file created), not on writes. On Windows, CreationTime serves this role
// perfectly.
func fileKey(fi os.FileInfo) string {
	if v, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		return fmt.Sprintf("ctime:%d%d", v.CreationTime.HighDateTime, v.CreationTime.LowDateTime)
	}
	return fmt.Sprintf("size:%d_mod:%d", fi.Size(), fi.ModTime().UnixNano())
}
