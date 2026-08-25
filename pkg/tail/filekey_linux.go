//go:build linux

package tail

import (
	"fmt"
	"os"
	"syscall"
)

// fileKey returns a stable file identity that changes only on rotation (new
// file created), not on writes. Inode+device is a better fit than Windows'
// CreationTime even: rotation always allocates a fresh inode.
func fileKey(fi os.FileInfo) string {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("ino:%d_dev:%d", st.Ino, st.Dev)
	}
	return fmt.Sprintf("size:%d_mod:%d", fi.Size(), fi.ModTime().UnixNano())
}
