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

func TestWriteInitialConfig_RewritesPlaceholder(t *testing.T) {
	exeDir := t.TempDir()
	origExe := executableDirFn
	executableDirFn = func() (string, error) { return exeDir, nil }
	t.Cleanup(func() { executableDirFn = origExe })

	// 模拟 G1 自动生成的默认模板：basedir 字段为空。
	writeConfig(t, exeDir, renderDefaultConfigTemplate())

	dataDir := filepath.Join(t.TempDir(), "data")
	if err := WriteInitialConfig(dataDir); err != nil {
		t.Fatalf("WriteInitialConfig: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(exeDir, ConfigFileName))
	if err != nil {
		t.Fatalf("读取改写后的文件失败: %v", err)
	}
	content := string(raw)
	if strings.Contains(content, basedirPlaceholder) {
		t.Error("basedir 占位符应已被替换")
	}
	// 文件里的路径经过 YAML 转义（反斜杠会被转成 \\），不能直接按原始路径字符串匹配，
	// 改用真正解析这份文件、读回 basedir 字段来验证写入内容正确。
	if got := fileOnlyBaseDir(exeDir); got != dataDir {
		t.Errorf("改写后解析出的 basedir 应为 %q，实际 %q\n文件内容:\n%s", dataDir, got, content)
	}
	// 模板里的其余注释应该原样保留（抽查一行）。
	if !strings.Contains(content, "优先级：命令行 flag") {
		t.Error("改写不应丢失模板里的其余注释")
	}
}

func TestWriteInitialConfig_RefusesToClobberExistingBasedir(t *testing.T) {
	exeDir := t.TempDir()
	origExe := executableDirFn
	executableDirFn = func() (string, error) { return exeDir, nil }
	t.Cleanup(func() { executableDirFn = origExe })

	writeConfig(t, exeDir, "basedir: \"/already/set\"\nserver:\n  port: 19193\n")

	err := WriteInitialConfig("/new/path")
	if err == nil {
		t.Fatal("basedir 字段已设置时应拒绝改写")
	}

	raw, _ := os.ReadFile(filepath.Join(exeDir, ConfigFileName))
	if !strings.Contains(string(raw), "/already/set") {
		t.Error("拒绝改写时不应该动到原文件内容")
	}
}
