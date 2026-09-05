//go:build linux

package runner

import (
	"os"
	"strings"

	"asa-server/pkg/linuxdeps"
	"asa-server/pkg/xvfb"
)

// preflight runs the host dependency checks scripts/ark_instance_manager.sh
// does in check_dependencies()/check_userns_restriction() (32-bit glibc,
// Python, libzstd, tar, the AppArmor userns restriction — the portable part
// lives in pkg/linuxdeps), plus the checks that need this package's own
// config/subsystem state (display, ACL support, overlayfs).
func preflight() []Problem {
	problems := linuxdeps.Check(pythonProblem)

	if p := checkDisplay(); p != nil {
		problems = append(problems, *p)
	}
	if p := checkACLSupport(); p != nil {
		problems = append(problems, *p)
	}
	if p := checkOverlayfs(); p != nil {
		problems = append(problems, *p)
	}
	return problems
}

// checkOverlayfs reports whether prefix_mode "overlay" can actually mount.
//
// Advisory, never a blocker, and on two counts. Nobody who left prefix_mode at
// its default needs overlayfs at all, so a hard stop would punish the majority
// for a feature they don't use — the same mistake "missing acl blocks setup"
// was (docs/ACL_PERMISSION_HARDENING_PLAN.md §1). And even with overlay
// configured, an unmountable layer degrades to a copy of the lower rather than
// failing the start, so "you will use more disk than you asked for" is exactly
// an advisory.
//
// Only the two conditions that can be decided from a file read are checked
// here. Whether the specific filesystem under BaseDir can hold an upperdir
// (xfs without ftype=1, NFS, an already-overlaid directory) can only be
// learned by trying to mount, which is not something a read-only preflight
// gets to do — that one surfaces as the fallback's warning at first launch.
func checkOverlayfs() *Problem {
	if getConfig().PrefixMode != "overlay" {
		return nil
	}
	if b, err := os.ReadFile("/proc/filesystems"); err != nil || !strings.Contains(string(b), "\toverlay\n") {
		return &Problem{
			Name:    "overlayfs",
			Detail:  "linux.prefix_mode 配置为 overlay，但当前内核没有报告 overlay 文件系统支持；每个实例会改用「从底层前缀复制一份」，功能相同但更占磁盘",
			Fix:     "确认 /proc/filesystems 里有一行 \"nodev\toverlay\"（必要时 modprobe overlay），或把 linux.prefix_mode 改回 shared / per-instance",
			Warning: true,
		}
	}
	if os.Geteuid() != 0 {
		return &Problem{
			Name:    "overlayfs",
			Detail:  "linux.prefix_mode 配置为 overlay，但当前不是以 root 运行，mount(2) 会被拒绝；每个实例会改用「从底层前缀复制一份」，功能相同但更占磁盘",
			Fix:     "以 root 运行 asa-server 服务本体（游戏进程仍会降权到专用用户），或把 linux.prefix_mode 改回 shared / per-instance",
			Warning: true,
		}
	}
	return nil
}

// runtimeUserProblems is verifyRuntimeAccess exposed for the preflight API
// only. It is deliberately NOT folded into Preflight(): Preflight runs during
// `asa-server setup` *before* EnsureRuntime creates the user, where a
// "user missing" result would be a false alarm. The real enforcement is
// package main's startup gate (EnsureRuntimeUser then VerifyRuntimeAccess).
func runtimeUserProblems() []Problem { return verifyRuntimeAccess(false) }

// checkDisplay reports whether this host can hand a Wine process an X display.
//
// Wine's winex11.drv failing to connect makes every CreateWindow call fail —
// which kills, before it prints a single line, both AsaApiLoader.exe (ArkApi:
// measured exit 3 after 5s, empty log) and Microsoft's vc_redist installer
// (exit 203). Neither has a headless mode to fall back to.
//
// # Why this is an advisory and not a blocker (it used to be one)
//
// The blocker was justified with "missing ACLs degrade to a working chown
// fallback, a missing display degrades to nothing at all"
// (docs/ACL_PERMISSION_HARDENING_PLAN.md §1). That is true *for ArkApi*, and
// only for ArkApi — which is opt-in per instance. A display is a **feature**
// dependency, and preflight was treating it as an **install** dependency:
//
//   - ArkAscendedServer.exe itself never needs a display (same host, listening
//     in 42 seconds) — see display_linux.go's header;
//   - the vc_redist step already degrades on its own (vcredist_linux.go: no
//     display means skip the install, keep the DLL overrides, don't fail);
//   - a launch that really needs one fails loudly at runner.Run with the same
//     message, so nothing gets to fail silently.
//
// So blocking here made a machine that will never run ArkApi impossible to
// even install onto — the exact shape of mistake this file's package comment
// warns about, one layer up: judging the *severity* by the check's own subject
// rather than by what actually breaks. Whoever needs ArkApi still sees the
// advisory during setup, in `asa-server verify-arkapi`, and at launch.
//
// # Why it asks planDisplay
//
// "Some xvfb package is installed" turned out not to imply "a display is
// obtainable" — twice. On WSLg /tmp/.X11-unix is a read-only mount, so Xvfb
// cannot publish a socket pressure-vessel can bind; and on Fedora/RHEL/Arch
// the xvfb-run script this check used to look for doesn't exist at all even
// though Xvfb does (docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md §1). Sharing the
// resolver means this check answers exactly the question the launch will ask.
//
// planDisplay, not acquire: a self-check must not start an X server as a side
// effect of being asked.
//
// Detail carries planDisplay's own reason string. It used to be a fixed
// sentence, which meant a host with Xvfb installed and a permission problem on
// /tmp/.X11-unix was told, and told only, to go install Xvfb
// (docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md §11).
func checkDisplay() *Problem {
	_, blocked := planDisplay()
	if blocked == "" {
		return nil
	}
	return &Problem{
		Name:    "x11-display",
		Warning: true,
		Detail: blocked + "。ArkApi 的 AsaApiLoader.exe 与微软的 VC++ 安装器都会创建 Win32 窗口，" +
			"Wine 下没有显示时它们直接失败（加载器 5 秒后以退出码 3 退出，一行日志都不写）。" +
			"**不启用 ArkApi 的实例不受影响** —— ArkAscendedServer.exe 本身不需要显示，" +
			"所以这一项不阻断安装",
		Fix: xvfb.InstallHint + "。若 " + xvfb.SocketDir + " 是只读挂载（WSLg 就是这么挂的），" +
			"asa-server 以 root 运行时会尝试把它重新挂载为可写（linux.allow_x11_remount，" +
			"默认开）；这一步也失败时，需要系统里有一个不需要 xauth cookie 就能连的 X 服务，" +
			"并可用 config.yaml 的 linux.display 指定它",
	}
}

