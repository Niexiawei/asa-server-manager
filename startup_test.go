package main

import "testing"

func TestStartupModeFor(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want startupMode
	}{
		{"无参（Linux：api）", []string{"asa"}, defaultStartup},
		{"api", []string{"asa", "api"}, defaultStartup},
		{"setup", []string{"asa", "setup", "--basedir", "x"}, firstRunStartup},
		{"gui", []string{"asa", "gui"}, firstRunStartup},
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
	if got := startupModeFor("windows", []string{"asa.exe"}); got != firstRunStartup {
		t.Errorf("Windows 无参应为 firstRunStartup，实际 %+v", got)
	}
	if got := startupModeFor("windows", []string{"asa.exe", "api"}); got != defaultStartup {
		t.Errorf("Windows api 应为 defaultStartup，实际 %+v", got)
	}
}
