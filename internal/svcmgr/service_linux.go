//go:build linux

package svcmgr

import (
	cfgpkg "asa-server/internal/config"
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
	if os.Geteuid() == 0 {
		fmt.Println("警告: 服务将以 root 身份运行。ARK/Proton 生态普遍假设非 root，")
		fmt.Println("      建议改用专用用户：先用 `useradd -r -m asa` 创建，装完服务后手动")
		fmt.Printf("      执行 `systemctl edit %s.service` 加一行 User=asa，\n", ServiceName)
		fmt.Println("      并确保该用户对 BaseDir 有读写权限，再执行 `systemctl daemon-reload`。")
	}
}
