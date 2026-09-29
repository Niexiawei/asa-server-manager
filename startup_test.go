package main

import "testing"

func TestStartupModeFor(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want startupMode
	}{
		{"无参", []string{"asa"}, defaultStartup},
		{"api", []string{"asa", "api"}, defaultStartup},
		{"setup 暂保持旧行为（C6 再切）", []string{"asa", "setup", "--basedir", "x"}, defaultStartup},
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
			if got := startupModeFor(c.args); got != c.want {
				t.Errorf("startupModeFor(%q) = %+v，期望 %+v", c.args, got, c.want)
			}
		})
	}
}
