//go:build linux

package svcmgr

import (
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/runner"
	"context"
	"fmt"
	"os"

	"github.com/kardianos/service"
)

// configurePlatform layers the systemd hardening docs/LINUX_COMPATIBILITY_PLAN.md
// §5.8 calls out on top of kardianos's bare-bones default unit:
//
//   - Dependencies: don't race the network — umu/GE-Proton downloads and the
//     game server itself need it up.
//   - LimitNOFILE: ARK + Wine between them open a lot of file descriptors;
//     the systemd default (usually 1024) is nowhere near enough.
//   - Restart=on-failure: recover from a crashed server without operator
//     intervention, without masking a deliberate `service stop`.
//   - WorkingDirectory: BaseDir, so relative paths behave the same as the
//     interactive/Windows case.
//   - EnvVars["HOME"]: THE landmine. A systemd system service normally starts
//     with HOME unset or "/", and umu needs $HOME/.local/share/umu (Steam
//     Linux Runtime) while lsteamclient needs $HOME/.steam/sdk{32,64}. Get
//     this wrong and every start re-downloads the runtime, or steamclient
//     just crashes. Baking it into the unit at install time (rather than
//     relying on whatever the shell had) is what actually fixes it.
//
// RestartSec deliberately stays at kardianos's built-in template default
// (120s) rather than the plan's suggested 10s: customizing it means shipping
// a full custom SystemdScript template (kardianos v1.3.0 has no separate
// RestartSec option key), which is a forked copy of upstream's unit template
// that can silently drift on a kardianos version bump. 120s is more
// conservative anyway for a service that needs to save the world before it
// can restart cleanly.
func configurePlatform(cfg *service.Config) {
	cfg.Dependencies = []string{
		"After=network-online.target",
		"Wants=network-online.target",
	}
	cfg.Option = service.KeyValue{
		"LimitNOFILE": 1048576,
		"Restart":     "on-failure",
		// Custom unit template: identical to kardianos's built-in one plus a
		// RestartPreventExitStatus=78 line, so a drop-privileges runtime-user
		// failure (exit 78) goes straight to `failed` instead of
		// restart-looping. See docs/UMU_RUNTIME_USER_PLAN.md §9.3b and
		// systemd_script_linux.go.
		"SystemdScript": umuRuntimeSystemdScript,
	}
	cfg.WorkingDirectory = cfgpkg.BaseDir
	cfg.EnvVars = map[string]string{
		"HOME": serviceHomeDir(),
	}
}

// serviceHomeDir resolves the HOME to bake into the unit. cfg.UserName is
// left empty (root, matching the Windows LocalSystem default — see
// warnBeforeInstall for why that's not necessarily what you want), so this
// mirrors whatever HOME the installing process itself sees: root's real home
// when run via a normal `sudo`, which is exactly the value systemd would
// otherwise fail to set on its own.
func serviceHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		return home
	}
	return "/root"
}

// warnBeforeInstall flags the one Linux-specific risk the plan calls out that
// this package cannot safely automate: running the game server as root.
// pressure-vessel's unprivileged user-namespace path behaves differently
// under root, and the Proton ecosystem broadly assumes non-root. Detecting
// and creating a dedicated system user crosses into territory (chown-ing
// BaseDir, migrating an already-running root-owned install) this function
// deliberately doesn't attempt — it only surfaces the choice.
func warnBeforeInstall() {
	if os.Geteuid() != 0 {
		return
	}
	// asa-server itself still runs as root (see the note above / §5.8). What
	// we now DO automate is the narrower thing: create the dedicated non-root
	// user the game process tree gets dropped to, and hand it the runtime
	// subtrees it needs. See docs/UMU_RUNTIME_USER_PLAN.md.
	if err := runner.EnsureRuntimeUser(context.Background()); err != nil {
		fmt.Println("警告: 未能自动创建降权运行时用户：")
		fmt.Printf("      %v\n", err)
		fmt.Println("      服务启动时会因此拒绝启动（退出码 78），除非在 config.yaml 设 linux.umu_run_as_root: true。")
		return
	}
	fmt.Printf("游戏实例将以专用非 root 用户 %s 运行（asa-server 服务本身仍为 root）。\n", runner.RuntimeUserName())
}
