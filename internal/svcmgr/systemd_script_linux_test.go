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
