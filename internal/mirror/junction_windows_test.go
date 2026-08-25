//go:build windows

package mirror

import (
	"os"
	"path/filepath"
	"testing"

	"asa-server/internal/logger"
)

// createJunction 会写 Debug 日志，GetLogger() 未初始化时返回 nil 会 panic。
func TestMain(m *testing.M) {
	logger.InitLoggerWithBaseDir(os.TempDir())
	os.Exit(m.Run())
}

// createJunction 必须建出真正的 NTFS mount point，而不是目录符号链接。
// 这条区别是整个去管理员化的前提：符号链接要 SeCreateSymbolicLinkPrivilege，
// junction 不要。测试跑在普通用户下能通过，就说明前提成立。
func TestCreateJunctionMakesRealMountPoint(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	link := filepath.Join(base, "link")

	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatalf("准备目标目录: %v", err)
	}
	payload := filepath.Join(target, "payload.txt")
	if err := os.WriteFile(payload, []byte("hello"), 0644); err != nil {
		t.Fatalf("写测试文件: %v", err)
	}

	if err := createJunction(link, target); err != nil {
		t.Fatalf("createJunction 失败（普通用户下也应成功）: %v", err)
	}

	// 是 mount point 而不是 symlink：符号链接会带 ModeSymlink，junction 不带
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("建出来的是符号链接而不是 junction —— 那样又会需要管理员权限")
	}

	// 但必须能被 isJunctionOrSymlink 认出来，否则增量同步会穿过它删到源目录
	if !isJunctionOrSymlink(link) {
		t.Fatal("isJunctionOrSymlink 未能识别真 junction —— 这会让同步逻辑把它当普通目录递归")
	}

	if got, err := os.Readlink(link); err != nil {
		t.Errorf("Readlink 失败: %v", err)
	} else if got != target {
		t.Errorf("Readlink = %q，期望 %q", got, target)
	}

	// 能穿过 junction 读到目标内容
	through, err := os.ReadFile(filepath.Join(link, "payload.txt"))
	if err != nil {
		t.Fatalf("穿过 junction 读文件失败: %v", err)
	}
	if string(through) != "hello" {
		t.Errorf("穿过 junction 读到 %q，期望 %q", through, "hello")
	}
}

// 删除 junction 只能删掉链接本身。若 os.Remove 顺着链接把目标内容删了，
// 镜像清理就会把 server-files 里的真实文件一起带走。
func TestRemoveJunctionKeepsTarget(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	link := filepath.Join(base, "link")

	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatalf("准备目标目录: %v", err)
	}
	payload := filepath.Join(target, "payload.txt")
	if err := os.WriteFile(payload, []byte("keep me"), 0644); err != nil {
		t.Fatalf("写测试文件: %v", err)
	}
	if err := createJunction(link, target); err != nil {
		t.Fatalf("createJunction: %v", err)
	}

	if err := os.Remove(link); err != nil {
		t.Fatalf("删除 junction 失败: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("junction 应已消失，Lstat err = %v", err)
	}
	if b, err := os.ReadFile(payload); err != nil {
		t.Fatalf("目标文件被连带删除了: %v", err)
	} else if string(b) != "keep me" {
		t.Errorf("目标文件内容被改动: %q", b)
	}
}

// 语义要与 os.Symlink 对齐：链接路径已存在时报错，而不是往一个非空目录上
// 盖 reparse point（那会让原有内容变得不可访问）。
func TestCreateJunctionRefusesExistingPath(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	link := filepath.Join(base, "link")
	for _, d := range []string{target, link} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("准备目录 %s: %v", d, err)
		}
	}
	occupied := filepath.Join(link, "existing.txt")
	if err := os.WriteFile(occupied, []byte("x"), 0644); err != nil {
		t.Fatalf("写占位文件: %v", err)
	}

	if err := createJunction(link, target); err == nil {
		t.Fatal("链接路径已存在时 createJunction 应报错")
	}
	// 失败后不得破坏已有内容
	if _, err := os.Stat(occupied); err != nil {
		t.Errorf("失败路径不应动已有内容: %v", err)
	}
}

// createJunction 失败时要把占位空目录回滚掉，否则后续同步会把它当成真实目录。
func TestCreateJunctionRollsBackOnFailure(t *testing.T) {
	base := t.TempDir()
	link := filepath.Join(base, "link")

	// 目标路径含非法字符（NUL），UTF16FromString 会失败，从而触发回滚分支
	if err := createJunction(link, "C:\\bad\x00path"); err == nil {
		t.Fatal("非法目标路径应导致 createJunction 失败")
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("失败后占位目录应被删除，Lstat err = %v", err)
	}
}
