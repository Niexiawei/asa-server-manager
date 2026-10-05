// Package atomicfile 提供「要么是旧内容、要么是新内容」的整文件写入。
//
// 只依赖标准库：pkg/meshid 与独立部署的协调节点（cmd/asa-coordinator）都用它，
// 后者的依赖面要尽量小，不能经 pkg/fsutil 拖进 gopsutil。
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write 先写同目录临时文件、fsync，再 rename 覆盖目标。同目录保证 rename 不跨
// 文件系统；Windows 上 os.Rename 走 MoveFileEx(MOVEFILE_REPLACE_EXISTING)，
// 目标已存在也能替换。
//
// 权限在 rename 之前设好：目标文件从出现的那一刻起就是 perm，不存在
// 「先以默认权限出现、再收紧」的窗口——私钥文件尤其要这样。
func Write(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后这里是无害的 ENOENT

	if err := tmp.Chmod(perm); err != nil && !isChmodUnsupported(err) {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
