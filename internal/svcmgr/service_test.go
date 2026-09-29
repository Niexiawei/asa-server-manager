package svcmgr

import (
	"path/filepath"
	"testing"

	"github.com/kardianos/service"
)

func TestInjectConfigLocation(t *testing.T) {
	cfg := &service.Config{}
	injectConfigLocation(cfg, "")
	if cfg.EnvVars != nil {
		t.Errorf("未设 ASA_CFG 时不应注入任何环境变量，实际 %v", cfg.EnvVars)
	}

	dir := t.TempDir()
	injectConfigLocation(cfg, dir)
	if got := cfg.EnvVars["ASA_CFG"]; got != dir {
		t.Errorf("ASA_CFG 应注入为 %q，实际 %q", dir, got)
	}

	// 平台层已经放进去的变量（Linux 的 HOME）必须保留。
	cfg = &service.Config{EnvVars: map[string]string{"HOME": "/root"}}
	injectConfigLocation(cfg, "relative-cfg")
	if cfg.EnvVars["HOME"] != "/root" {
		t.Error("不应覆盖平台层已有的环境变量")
	}
	if got := cfg.EnvVars["ASA_CFG"]; !filepath.IsAbs(got) {
		t.Errorf("相对路径应转成绝对路径（服务的工作目录与安装时不同），实际 %q", got)
	}
}

func TestNewServiceConfigInjectsASACFG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ASA_CFG", dir)
	cfg, err := newServiceConfig()
	if err != nil {
		t.Fatalf("newServiceConfig: %v", err)
	}
	if got := cfg.EnvVars["ASA_CFG"]; got != dir {
		t.Errorf("newServiceConfig 应注入当前 ASA_CFG=%q，实际 %q", dir, got)
	}
}
