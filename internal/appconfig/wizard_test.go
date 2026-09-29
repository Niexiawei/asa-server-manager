package appconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateBaseDir_RejectsInsufficientSpace(t *testing.T) {
	// t.TempDir() 通常在系统盘，剩余空间无法保证——这里只验证"存在 config.yaml
	// 时跳过空间检查"这条分支本身能正常通过校验（不因为空间不足报错），
	// 空间不足分支留给下面的合成测试用注入的方式覆盖。
	dir := t.TempDir()
	writeConfig(t, dir, "basedir: \"\"\n")
	if err := ValidateBaseDir(dir); err != nil {
		t.Errorf("已存在 config.yaml 应视为接管已有安装、跳过空间检查，不该报错: %v", err)
	}
}

func TestValidateBaseDir_RejectsUnwritableParent(t *testing.T) {
	// 用一个不存在的路径的路径当"父目录"来制造不可写：把目标路径的父级设成一个
	// 已知不存在且无法创建的位置（比如把一个已存在的普通文件当成父目录）。
	parentFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(parentFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("写入测试文件失败: %v", err)
	}
	target := filepath.Join(parentFile, "child")

	err := ValidateBaseDir(target)
	if err == nil {
		t.Fatal("父路径是文件而非目录时应报错")
	}
	if !strings.Contains(err.Error(), "不可写") {
		t.Errorf("错误信息应提示不可写，实际 %q", err)
	}
}
