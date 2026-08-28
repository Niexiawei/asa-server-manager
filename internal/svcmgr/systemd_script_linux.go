//go:build linux

package svcmgr

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
