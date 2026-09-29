package userenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteProfileScript(t *testing.T) {
	dir := t.TempDir()
	path, err := writeProfileScriptIn(dir, "asa-server.sh", "ASA_CFG", "/opt/asa data/it's")
	if err != nil {
		t.Fatalf("写入: %v", err)
	}
	if path != filepath.Join(dir, "asa-server.sh") {
		t.Errorf("路径 %q", path)
	}
	raw, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(raw), profileMarker) {
		t.Error("首行应是标记")
	}
	if !strings.Contains(string(raw), `export ASA_CFG='/opt/asa data/it'\''s'`) {
		t.Errorf("单引号转义不对:\n%s", raw)
	}

	// 自己写的可以覆盖。
	if _, err := writeProfileScriptIn(dir, "asa-server.sh", "ASA_CFG", "/new"); err != nil {
		t.Fatalf("覆盖自己写的文件: %v", err)
	}
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), "export ASA_CFG='/new'") {
		t.Errorf("应已更新:\n%s", raw)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("不应留下临时文件，目录里有 %d 项", len(entries))
	}
}

func TestWriteProfileScriptRefusesForeignFile(t *testing.T) {
	dir := t.TempDir()
	foreign := filepath.Join(dir, "asa-server.sh")
	if err := os.WriteFile(foreign, []byte("export FOO=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeProfileScriptIn(dir, "asa-server.sh", "ASA_CFG", "/x"); err == nil {
		t.Fatal("不是本程序写的同名文件应拒绝覆盖")
	}
	if raw, _ := os.ReadFile(foreign); string(raw) != "export FOO=1\n" {
		t.Error("拒绝时不应改动原文件")
	}
}

func TestWriteProfileScriptRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct{ file, name, value string }{
		{"asa-server.sh", "1BAD", "/x"},
		{"asa-server.sh", "ASA_CFG", "/a\nb"},
		{"../evil.sh", "ASA_CFG", "/x"},
		{"asa-server", "ASA_CFG", "/x"},
	} {
		if _, err := writeProfileScriptIn(dir, c.file, c.name, c.value); err == nil {
			t.Errorf("%+v 应被拒绝", c)
		}
	}
}

// 真让 sh 去 source 生成的片段：转义对不对只有 shell 说了算。
func TestProfileScriptIsSourceable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("需要 POSIX sh")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("找不到 sh")
	}
	dir := t.TempDir()
	const value = `/opt/asa data/it's $HOME "q" \n`
	path, err := writeProfileScriptIn(dir, "asa-server.sh", "ASA_CFG", value)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(sh, "-c", `. "$1" && printf %s "$ASA_CFG"`, "sh", path).Output()
	if err != nil {
		t.Fatalf("source 失败: %v", err)
	}
	if string(out) != value {
		t.Errorf("source 后 ASA_CFG = %q，期望 %q", out, value)
	}
}
