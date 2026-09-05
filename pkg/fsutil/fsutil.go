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

// EnsureWorldReadable walks root and grants every entry o+r (files) or o+rx
// (directories, and files already owner-executable) — the minimum needed for
// an arbitrary other Unix user to traverse and read a tree it does not own,
// without touching group bits. Meant for read-only trees (a vendored runtime,
// an extracted archive) that a dropped-privilege child process merely needs
// to read, as opposed to a tree it needs to write — see pkg/shareacl for that
// case, which is a different problem (two writers, not one owner plus one
// reader).
func EnsureWorldReadable(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		want := mode | 0o044
		if d.IsDir() || mode&0o100 != 0 {
			want |= 0o011
		}
		if want != mode {
			return os.Chmod(path, want)
		}
		return nil
	})
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
