package actions

import (
	"asa-server/internal/installer"
	"asa-server/internal/runner"
	"context"
	"fmt"
	"io"
	"strings"
)

// VerifyEnvironmentReady aggregates the Wine/Proton runtime (Linux only),
// SteamCMD and the ARK server-files installation into one readiness check.
// Returns nil when everything a game instance needs is present; otherwise a
// multi-line error whose text is meant to be shown to the user verbatim,
// ending with "请运行：asa-server setup".
//
// See docs/SETUP_FLOW_OPTIMIZATION_PLAN.md §3.1.3.
func VerifyEnvironmentReady() error {
	var missing []string

	if err := runner.CheckRuntime(); err != nil { // always nil on Windows
		missing = append(missing, "  - "+err.Error())
	}

	st := installer.CheckInstalled()
	if !st.SteamCmdReady {
		missing = append(missing, "  - SteamCMD 未安装")
	}
	if !st.ServerBinaryReady {
		missing = append(missing, "  - ARK 服务端本体未安装")
	}
	if !st.ServerConfigReady {
		missing = append(missing, "  - ARK 首次配置文件未生成")
	}

	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("基础环境尚未初始化，检测到以下缺失：\n%s\n\n请运行：asa-server setup",
		strings.Join(missing, "\n"))
}

// InstallBaseEnvironment runs the three platform-agnostic install steps:
// SteamCMD, the ARK server files, then a first-run launch to generate the
// default config. Progress is streamed to w (os.Stdout for the CLI, a
// UI-bound writer for the GUI — see internal/gui/setup_progress.go).
//
// On Linux the caller is responsible for the host dependency preflight and
// runner.EnsureRuntime beforehand; this function is the part that is
// identical on both platforms.
func InstallBaseEnvironment(ctx context.Context, w io.Writer) error {
	if err := installer.DownloadAndExtractSteamCmd(ctx, w); err != nil {
		return fmt.Errorf("安装 SteamCMD 失败: %w", err)
	}
	if err := installer.DownloadAndUpdateArkServer(ctx, w); err != nil {
		return fmt.Errorf("安装 ARK 服务端本体失败: %w", err)
	}
	if err := installer.VerifyServerInstallation(ctx, false, w); err != nil {
		return fmt.Errorf("生成首次配置失败: %w", err)
	}
	return nil
}
