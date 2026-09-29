package appconfig

import (
	"path/filepath"
	"strings"
	"testing"
)

// C0（docs/SETUP_FLOW_OPTIMIZATION_PLAN.md Part 2 §P2-3.1(c)）：Windows 上生成的
// config.yaml 带 UTF-8 BOM + CRLF，这两样都必须对 Load 与 fileOnlyBaseDir 透明——
// 否则 BOM 会粘在第一个 key 上（变成 "<BOM>basedir"），basedir 字段静默丢失，数据目录
// 回落到配置文件所在目录，而且不报任何错。
func TestLoadAcceptsBOMAndCRLF(t *testing.T) {
	clearASABaseDir(t)
	dir := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "data")

	// basedir 故意放在第一行：BOM 紧贴着它，是最容易被吃掉的位置。
	content := "basedir: " + quoteYAML(dataDir) + "\n" +
		"# 注释：中文\n" +
		"server:\n" +
		"  port: 8443\n" +
		"download:\n" +
		"  github_proxy: \"https://gh.example.com/\"\n"
	content = utf8BOM + strings.ReplaceAll(content, "\n", "\r\n")
	writeConfig(t, dir, content)

	got, err := loadFrom(t, dir)
	if err != nil {
		t.Fatalf("带 BOM + CRLF 的配置应能加载: %v", err)
	}
	if got != dataDir {
		t.Errorf("BaseDir 应取自第一行的 basedir 字段 %q，实际 %q（BOM 可能粘在了 key 上）", dataDir, got)
	}
	if fb := fileOnlyBaseDir(dir); fb != dataDir {
		t.Errorf("fileOnlyBaseDir 应读到 %q，实际 %q", dataDir, fb)
	}
	cfg := Get()
	if cfg.Server.Port != 8443 {
		t.Errorf("server.port 应为 8443，实际 %d", cfg.Server.Port)
	}
	if cfg.Download.GithubProxy != "https://gh.example.com/" {
		t.Errorf("CRLF 不应混进字符串值，github_proxy 实际 %q", cfg.Download.GithubProxy)
	}
}
