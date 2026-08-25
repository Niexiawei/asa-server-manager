//go:build linux

package mirror

import (
	"os"
	"path/filepath"
	"testing"
)

// createJunction 在 Linux 上建的是符号链接，且必须能被 isJunctionOrSymlink
// 识别，否则增量同步会穿过它递归删到源目录。
func TestCreateJunctionMakesSymlink(t *testing.T) {
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
		t.Fatalf("createJunction 失败: %v", err)
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("建出来的不是符号链接")
	}

	if !isJunctionOrSymlink(link) {
		t.Fatal("isJunctionOrSymlink 未能识别符号链接")
	}

	if got, err := os.Readlink(link); err != nil {
		t.Errorf("Readlink 失败: %v", err)
	} else if got != target {
		t.Errorf("Readlink = %q，期望绝对路径 %q", got, target)
	}

	through, err := os.ReadFile(filepath.Join(link, "payload.txt"))
	if err != nil {
		t.Fatalf("穿过符号链接读文件失败: %v", err)
	}
	if string(through) != "hello" {
		t.Errorf("穿过符号链接读到 %q，期望 %q", through, "hello")
	}
}

// 删除符号链接只能删掉链接本身，不能带走目标内容——与 Windows 侧
// junction 的删除语义一致，镜像清理逻辑不用区分平台。
func TestRemoveJunctionKeepsTargetLinux(t *testing.T) {
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
		t.Fatalf("删除符号链接失败: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("符号链接应已消失，Lstat err = %v", err)
	}
	if b, err := os.ReadFile(payload); err != nil {
		t.Fatalf("目标文件被连带删除了: %v", err)
	} else if string(b) != "keep me" {
		t.Errorf("目标文件内容被改动: %q", b)
	}
}

// 语义要与 Windows 侧对齐：链接路径已存在时报错，而不是静默覆盖——
// 调用方依赖「先删再建」的显式顺序。
func TestCreateJunctionRefusesExistingPathLinux(t *testing.T) {
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
	if _, err := os.Stat(occupied); err != nil {
		t.Errorf("失败路径不应动已有内容: %v", err)
	}
}
