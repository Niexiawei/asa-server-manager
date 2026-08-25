//go:build linux

package mirror

import (
	"fmt"
	"os"
	"path/filepath"
)

// createJunction 在 Linux 上就是普通符号链接。
//
// 语义必须与 junction_windows.go 对齐：linkPath 已存在时报错而不是覆盖。
// os.Symlink 天然如此（返回 EEXIST），不需要额外判断——调用方
// （migrateExceptionJunctions / reconcileEntry）全部依赖「先删再建」的显式顺序，
// 静默覆盖会掩盖同步逻辑里的真实错误。
//
// target 必须是绝对路径：Windows 侧的 junction 存的是 NT 绝对路径，
// 相对路径会让语义随 CWD 漂移，两平台就对不齐了。
func createJunction(linkPath, targetPath string) error {
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for %s: %w", targetPath, err)
	}

	if err := os.MkdirAll(filepath.Dir(linkPath), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory for %s: %w", linkPath, err)
	}

	if err := os.Symlink(absTarget, linkPath); err != nil {
		return fmt.Errorf("failed to create symlink %s -> %s: %w", linkPath, absTarget, err)
	}
	return nil
}
