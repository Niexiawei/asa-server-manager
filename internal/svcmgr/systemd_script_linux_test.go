//go:build linux

package svcmgr

import (
	"strings"
	"testing"
)

// kardianosSystemdScriptV130 is a verbatim copy of
// github.com/kardianos/service@v1.3.0/service_systemd_linux.go's
// `const systemdScript`. umuRuntimeSystemdScript must be exactly this plus the
// two consecutive `# asa-server:` / `RestartPreventExitStatus=78` lines. When
// a kardianos bump makes this test fail, re-copy the upstream constant here
// and re-apply the two-line change (docs/UMU_RUNTIME_USER_PLAN.md §9.3c).
const kardianosSystemdScriptV130 = `[Unit]
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
EnvironmentFile=-/etc/sysconfig/{{Name}}

{{range EnvVars}}{{.}}
{{end}}[Install]
WantedBy=multi-user.target
`

// The two lines this fork adds, together, and nothing else.
var forkAddedLines = []string{
	"# asa-server: exit 78 (EX_CONFIG) = drop-privileges runtime user unavailable; retrying cannot fix it",
	"RestartPreventExitStatus=78",
}

func TestUmuRuntimeSystemdScript_IsKardianosPlusExactlyTheForkLines(t *testing.T) {
	var kept []string
	for _, line := range strings.Split(umuRuntimeSystemdScript, "\n") {
		if line == forkAddedLines[0] || line == forkAddedLines[1] {
			continue
		}
		kept = append(kept, line)
	}
	got := strings.Join(kept, "\n")
	if got != kardianosSystemdScriptV130 {
		t.Fatalf("umuRuntimeSystemdScript with the fork lines removed no longer matches "+
			"kardianos v1.3.0's systemdScript — re-diff on kardianos bump.\n--- want ---\n%s\n--- got ---\n%s",
			kardianosSystemdScriptV130, got)
	}
}

func TestUmuRuntimeSystemdScript_PreventLinePinnedTo78(t *testing.T) {
	if n := strings.Count(umuRuntimeSystemdScript, "RestartPreventExitStatus="); n != 1 {
		t.Fatalf("want exactly one RestartPreventExitStatus= line, got %d", n)
	}
	if !strings.Contains(umuRuntimeSystemdScript, "\nRestartPreventExitStatus=78\n") {
		t.Fatal("RestartPreventExitStatus must be pinned to 78 (EX_CONFIG), matching package main's exit code")
	}
	// It must land unconditionally, right after RestartSec=120 (which is
	// itself preceded by a {{end}} that closes the SuccessExitStatus block).
	if !strings.Contains(umuRuntimeSystemdScript, "RestartSec=120\n# asa-server:") {
		t.Fatal("the prevent line must sit right after the unconditional RestartSec=120")
	}
}

func TestSystemdScriptWithEnv_QuotesValues(t *testing.T) {
	script, err := systemdScriptWithEnv(umuRuntimeSystemdScript, map[string]string{
		"HOME":    "/root",
		"ASA_CFG": "/opt/asa data/cfg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, envVarsRangeBlock) {
		t.Error("EnvVars 段应已被替换")
	}
	// 按 key 排序，带引号：路径里的空格不能把赋值切开。
	want := "Environment=\"ASA_CFG=/opt/asa data/cfg\"\nEnvironment=\"HOME=/root\"\n[Install]"
	if !strings.Contains(script, want) {
		t.Errorf("渲染结果不对，期望包含:\n%s\n实际:\n%s", want, script)
	}
}

func TestSystemdEnvLine_Escapes(t *testing.T) {
	cases := map[string]string{
		`/opt/a"b`:  `Environment="K=/opt/a\"b"`,
		`/opt/a\b`:  `Environment="K=/opt/a\b"`,
		`/opt/100%`: `Environment="K=/opt/100%%"`,
	}
	for in, want := range cases {
		got, err := systemdEnvLine("K", in)
		if err != nil || got != want {
			t.Errorf("systemdEnvLine(%q) = %q, %v；期望 %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"/a\nb", "/a{{b", "/a}}b"} {
		if _, err := systemdEnvLine("K", bad); err == nil {
			t.Errorf("%q 应被拒绝", bad)
		}
	}
}

// 走一遍安装时真实的组装路径：HOME 与 ASA_CFG 都应带引号出现在 unit 模板里。
func TestNewServiceConfig_SystemdScriptCarriesASACFG(t *testing.T) {
	dir := t.TempDir() + "/with space"
	t.Setenv("ASA_CFG", dir)
	cfg, err := newServiceConfig()
	if err != nil {
		t.Fatal(err)
	}
	script, _ := cfg.Option["SystemdScript"].(string)
	if !strings.Contains(script, `Environment="ASA_CFG=`+dir+`"`) {
		t.Errorf("unit 模板里应有带引号的 ASA_CFG，实际:\n%s", script)
	}
	if !strings.Contains(script, `Environment="HOME=`) {
		t.Error("HOME 也应保留")
	}
}
