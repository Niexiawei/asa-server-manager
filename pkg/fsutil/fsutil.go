// Package fsutil provides generic filesystem helpers with no domain dependencies.
package fsutil

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// FileExists reports whether the given path exists.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !os.IsNotExist(err)
}

// CopyDir copies a directory recursively.
func CopyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := CopyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := CopyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// CopyFile copies a single file, creating parent directories and preserving mode.
func CopyFile(src, dst string) error {
	// 确保目标的父目录存在
	parentDir := filepath.Dir(dst)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file %s: %w", src, err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %w", dst, err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy file %s -> %s: %w", src, dst, err)
	}

	// 保留文件权限
	srcInfo, err := os.Stat(src)
	if err == nil {
		_ = os.Chmod(dst, srcInfo.Mode())
	}

	return nil
}

// FileMD5 returns the MD5 checksum of the file at path.
func FileMD5(path string) ([md5.Size]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [md5.Size]byte{}, err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return [md5.Size]byte{}, err
	}

	var result [md5.Size]byte
	copy(result[:], h.Sum(nil))
	return result, nil
}
