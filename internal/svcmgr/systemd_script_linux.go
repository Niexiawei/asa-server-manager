//go:build linux

package svcmgr

import (
	"fmt"
	"sort"
	"strings"
)

// umuRuntimeSystemdScript is a fork of kardianos/service v1.3.0's built-in
// `systemdScript` (github.com/kardianos/service@v1.3.0/service_systemd_linux.go,
// const systemdScript). It is passed through Config.Option["SystemdScript"] so
// the generated unit can carry one extra line kardianos's Option table has no
// key for:
//
//	# asa-server: RestartPreventExitStatus=78
//
// Exit code 78 (EX_CONFIG) is what package main returns when the drop-privileges
// runtime user can't be established (docs/UMU_RUNTIME_USER_PLAN.md §9.3b). With
// this line systemd sends the service straight to `failed` on that exit instead
// of restart-looping it, while Restart=on-failure still recovers real crashes
// (any other exit code).
//
// DRIFT: everything except the `# asa-server:`-marked line is a verbatim copy of
// upstream. On a kardianos version bump, re-diff against the new built-in
// systemdScript and update this copy. See docs/UMU_RUNTIME_USER_PLAN.md §9.3c.
const umuRuntimeSystemdScript = `[Unit]
Description={{Description}}
ConditionFileIsExecutable={{Path | cmdEscape}}
{{range Dependencies}}{{.}}
{{end}}
[Service]
StartLimitInterval=5
StartLimitBurst=10
ExecStart={{Path | cmdEscape}}{{range Arguments}} {{. | cmd}}{{end}}
{{if ChRoot}}RootDirectory={{ChRoot | cmd}}
{{end}}{{if WorkingDirectory}}WorkingDirectory={{WorkingDirectory | cmdEscape}}
{{end}}{{if UserName}}User={{UserName}}
{{end}}{{if ReloadSignal}}ExecReload=/bin/kill -{{ReloadSignal}} "$MAINPID"
{{end}}{{if PIDFile}}PIDFile={{PIDFile | cmd}}
{{end}}{{if OutputFileSupport}}StandardOutput=file:{{LogDirectory}}/{{Name}}.out
StandardError=file:{{LogDirectory}}/{{Name}}.err
{{end}}{{if LimitNOFILE}}LimitNOFILE={{LimitNOFILE}}
{{end}}{{if Restart}}Restart={{Restart}}
{{end}}{{if SuccessExitStatus}}SuccessExitStatus={{SuccessExitStatus}}
{{end}}RestartSec=120
# asa-server: exit 78 (EX_CONFIG) = drop-privileges runtime user unavailable; retrying cannot fix it
RestartPreventExitStatus=78
EnvironmentFile=-/etc/sysconfig/{{Name}}

{{range EnvVars}}{{.}}
{{end}}[Install]
WantedBy=multi-user.target
`

// envVarsRangeBlock 是上游模板里渲染 EnvVars 的那一段。
const envVarsRangeBlock = "{{range EnvVars}}{{.}}\n{{end}}"

// systemdScriptWithEnv 把 script 里的 envVarsRangeBlock 换成已经渲染好的
// `Environment="K=V"` 行（按 key 排序）。
//
// 为什么不直接交给 kardianos 渲染 EnvVars：它渲染成不带引号的
// `Environment=K=V`，值里有空格时 systemd 按空白把它切成几段赋值，
// `ASA_CFG=/opt/asa data` 会变成 ASA_CFG=/opt/asa 外加一段非法赋值被忽略——
// 服务读的是另一个目录，而且 unit 语法检查不会报错。它那个迷你模板引擎只有
// string→string 的 cmd / cmdEscape 两个函数，cmd 会把整行连 "Environment=" 一起
// 包进引号，模板里拼不出正确的形式，所以在安装时由这里预先渲染好写进模板文本。
//
// 渲染结果还要再经过一遍 kardianos 的模板解析，值里出现 "{{" 会被当成模板语法，
// 连同换行（systemd 的一行一条赋值）一起拒绝。
func systemdScriptWithEnv(script string, vars map[string]string) (string, error) {
	if !strings.Contains(script, envVarsRangeBlock) {
		return "", fmt.Errorf("systemd 模板里找不到 EnvVars 段，无法写入环境变量")
	}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		line, err := systemdEnvLine(k, vars[k])
		if err != nil {
			return "", err
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.Replace(script, envVarsRangeBlock, b.String(), 1), nil
}

// systemdEnvLine 渲染一条 `Environment="K=V"`。systemd 在双引号内支持 C 风格转义，
// 另外 % 是 unit 文件的说明符前缀（%h、%n……），字面量要写成 %%。
func systemdEnvLine(key, value string) (string, error) {
	kv := key + "=" + value
	if strings.ContainsAny(kv, "\n\r\x00") || strings.Contains(kv, "{{") || strings.Contains(kv, "}}") {
		return "", fmt.Errorf("环境变量 %s 的值含有换行或模板标记，不能写进 systemd unit：%q", key, value)
	}
	esc := strings.NewReplacer(`\`, `\`, `"`, `\"`, `%`, `%%`).Replace(kv)
	return `Environment="` + esc + `"`, nil
}
