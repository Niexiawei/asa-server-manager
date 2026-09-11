package plugindata

import (
	"path/filepath"
	"slices"
	"testing"
)

// 官方插件的 Version 是 JSON 数字：按 float 解析会把 1.10 读成 1.1。
// 文件名大小写与 UTF-8 BOM 都是 Windows 上打包工具的常见产物。
func TestReadPluginMetaKeepsNumberTextAndStripsBOM(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plugininfo.json"),
		"\xef\xbb\xbf"+`{"FullName":"X","Description":"d","Version":1.10,"MinApiVersion":2,"Dependencies":["Permissions"]}`)

	m, err := ReadPluginMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.FullName != "X" || m.Description != "d" {
		t.Errorf("FullName/Description = %q/%q", m.FullName, m.Description)
	}
	if m.Version != "1.10" {
		t.Errorf("Version = %q，期望保留原文 \"1.10\"", m.Version)
	}
	if m.MinApiVersion != "2" {
		t.Errorf("MinApiVersion = %q", m.MinApiVersion)
	}
	if !slices.Equal(m.Dependencies, []string{"Permissions"}) {
		t.Errorf("Dependencies = %v", m.Dependencies)
	}
}

func TestReadPluginMetaAcceptsStringVersion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, pluginInfoFileName), `{"FullName":"X","Version":"2.0-beta"}`)

	m, err := ReadPluginMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "2.0-beta" {
		t.Errorf("Version = %q", m.Version)
	}
}

func TestReadPluginMetaMissingFile(t *testing.T) {
	if _, err := ReadPluginMeta(t.TempDir()); err == nil {
		t.Error("没有 PluginInfo.json 时应返回错误")
	}
}
