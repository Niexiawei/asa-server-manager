package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"asa-server/internal/appconfig"
)

func TestStartupModeFor(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want startupMode
	}{
		{"无参（Linux：api）", []string{"asa"}, defaultStartup},
		{"api", []string{"asa", "api"}, defaultStartup},
		{"setup", []string{"asa", "setup", "--basedir", "x"}, firstRunStartup},
		{"gui", []string{"asa", "gui"}, guiStartup},
		{"setup --help 仍是只读", []string{"asa", "setup", "--help"}, readOnlyStartup},
		{"config init", []string{"asa", "config", "init"}, readOnlyStartup},
		{"config 带参数", []string{"asa", "config", "init", "--dir", "/tmp/x"}, readOnlyStartup},
		{"全局取值 flag 在前", []string{"asa", "--port", "1", "config", "init"}, readOnlyStartup},
		{"全局取值 flag 的值像命令名", []string{"asa", "--port", "config", "api"}, defaultStartup},
		{"--flag=value 不吞下一个参数", []string{"asa", "--port=1", "config"}, readOnlyStartup},
		{"全局布尔 flag 在前", []string{"asa", "--tls", "config", "init"}, readOnlyStartup},
		{"--help", []string{"asa", "--help"}, readOnlyStartup},
		{"-h", []string{"asa", "-h"}, readOnlyStartup},
		{"--version", []string{"asa", "--version"}, readOnlyStartup},
		{"-v", []string{"asa", "-v"}, readOnlyStartup},
		{"help 子命令", []string{"asa", "help", "api"}, readOnlyStartup},
		{"子命令 --help", []string{"asa", "api", "--help"}, readOnlyStartup},
		{"嵌套子命令 -h", []string{"asa", "service", "install", "-h"}, readOnlyStartup},
		{"子命令 -v 不是版本", []string{"asa", "api", "-v"}, defaultStartup},
		{"-- 之后不再解析", []string{"asa", "api", "--", "--help"}, defaultStartup},
		{"只有 flag 没有命令", []string{"asa", "--port", "1"}, defaultStartup},
		{"service install 要求配置", []string{"asa", "service", "install"}, defaultStartup},
		{"service install --force", []string{"asa", "service", "install", "--force"}, defaultStartup},
		{"service remove 是维护命令", []string{"asa", "service", "remove"}, readOnlyStartup},
		{"service stop 是维护命令", []string{"asa", "service", "stop"}, readOnlyStartup},
		{"service start 是维护命令", []string{"asa", "service", "start"}, readOnlyStartup},
		{"全局 flag 后的维护命令", []string{"asa", "--port", "1", "service", "stop"}, readOnlyStartup},
		{"cert uninstall 是维护命令", []string{"asa", "cert", "uninstall"}, readOnlyStartup},
		{"cert install 要求配置", []string{"asa", "cert", "install"}, defaultStartup},
		{"service 不带子命令", []string{"asa", "service"}, defaultStartup},
		{"子命令名出现在别的命令下不算维护", []string{"asa", "user", "remove"}, defaultStartup},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := startupModeFor("linux", c.args); got != c.want {
				t.Errorf("startupModeFor(%q) = %+v，期望 %+v", c.args, got, c.want)
			}
		})
	}
}

// Windows 无参进 GUI：GUI 的首次启动向导自己处理配置缺失。
func TestStartupModeForWindowsNoArgs(t *testing.T) {
	if got := startupModeFor("windows", []string{"asa.exe"}); got != guiStartup {
		t.Errorf("Windows 无参应为 guiStartup，实际 %+v", got)
	}
	if got := startupModeFor("windows", []string{"asa.exe", "api"}); got != defaultStartup {
		t.Errorf("Windows api 应为 defaultStartup，实际 %+v", got)
	}
}

// startupConfigBlocks 按 docs/APPCONFIG_BASEDIR_PLAN.md Part 2 P2-3 第 6.1 条的表逐行验证。
func TestStartupConfigBlocks(t *testing.T) {
	invalid := fmt.Errorf("%w: 端口超出范围", appconfig.ErrConfigInvalid)
	locateFailed := errors.New("定位 config.yaml 失败")
	cases := []struct {
		name    string
		mode    startupMode
		missing bool
		err     error
		want    bool
	}{
		{"api 缺配置", defaultStartup, true, nil, true},
		{"api 配置无效", defaultStartup, false, invalid, true},
		{"api 配置正常", defaultStartup, false, nil, false},
		{"服务缺配置", serviceStartup, true, nil, true},
		{"服务配置无效", serviceStartup, false, invalid, true},
		{"setup 缺配置：自己生成", firstRunStartup, true, nil, false},
		{"setup 配置无效", firstRunStartup, false, invalid, true},
		{"GUI 缺配置：走向导", guiStartup, true, nil, false},
		{"GUI 配置无效", guiStartup, false, invalid, true},
		{"GUI 定位失败：走向导", guiStartup, true, locateFailed, false},
		{"api 定位失败按缺配置拦", defaultStartup, true, locateFailed, true},
		{"只读命令缺配置", readOnlyStartup, true, nil, false},
		{"只读命令配置无效", readOnlyStartup, false, invalid, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := startupConfigBlocks(c.mode, c.missing, c.err); got != c.want {
				t.Errorf("startupConfigBlocks(%+v, missing=%v, %v) = %v，期望 %v", c.mode, c.missing, c.err, got, c.want)
			}
		})
	}
}

func TestStartupConfigMessage(t *testing.T) {
	dirs := appconfig.SearchDirs{ExeDir: "/opt/asa", SystemDir: "/etc/asa-server"}
	missing := startupConfigMessage(true, "/opt/asa/config.yaml", nil, dirs)
	for _, want := range []string{"未找到配置文件", "（未设置）", "/opt/asa", "/etc/asa-server", "config init", "setup"} {
		if !strings.Contains(missing, want) {
			t.Errorf("缺配置的提示应包含 %q:\n%s", want, missing)
		}
	}

	invalid := startupConfigMessage(false, "/opt/asa/config.yaml",
		fmt.Errorf("%w: server.port 超出范围", appconfig.ErrConfigInvalid), dirs)
	for _, want := range []string{"/opt/asa/config.yaml", "server.port 超出范围", "config validate"} {
		if !strings.Contains(invalid, want) {
			t.Errorf("配置无效的提示应包含 %q:\n%s", want, invalid)
		}
	}
}
