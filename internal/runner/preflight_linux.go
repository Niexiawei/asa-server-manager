//go:build linux

package runner

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// preflight runs the host dependency checks scripts/ark_instance_manager.sh
// does in check_dependencies()/check_userns_restriction(), reimplemented as
// functional checks (does the actual artifact exist / does the actual
// kernel knob say what we need) rather than per-distro package-manager
// queries — a working loader/library/interpreter matters here, not which
// package happened to provide it.
func preflight() []Problem {
	var problems []Problem

	if p := checkGlibc32(); p != nil {
		problems = append(problems, *p)
	}
	if p := checkPython3(); p != nil {
		problems = append(problems, *p)
	}
	if p := checkLibzstd(); p != nil {
		problems = append(problems, *p)
	}
	if p := checkTar(); p != nil {
		problems = append(problems, *p)
	}
	if p := checkAppArmorUserns(); p != nil {
		problems = append(problems, *p)
	}
	if p := checkDisplay(); p != nil {
		problems = append(problems, *p)
	}
	if p := checkACLSupport(); p != nil {
		problems = append(problems, *p)
	}
	return problems
}

// runtimeUserProblems is verifyRuntimeAccess exposed for the preflight API
// only. It is deliberately NOT folded into Preflight(): Preflight runs during
// `asa-server setup` *before* EnsureRuntime creates the user, where a
// "user missing" result would be a false alarm. The real enforcement is
// package main's startup gate (EnsureRuntimeUser then VerifyRuntimeAccess).
func runtimeUserProblems() []Problem { return verifyRuntimeAccess(false) }

// glibc32LoaderPaths are the well-known locations of the 32-bit dynamic
// linker across distros. SteamCMD is a 32-bit ELF binary; without i386
// multilib the kernel returns ENOENT for it even though the file itself
// exists on disk (see scripts/ark_instance_manager.sh check_dependencies()).
var glibc32LoaderPaths = []string{
	"/lib/ld-linux.so.2",
	"/lib32/ld-linux.so.2",
	"/usr/lib32/ld-linux.so.2",
	"/lib/i386-linux-gnu/ld-linux.so.2",
}

func checkGlibc32() *Problem {
	for _, p := range glibc32LoaderPaths {
		if fileExists(p) {
			return nil
		}
	}
	return &Problem{
		Name:   "glibc32",
		Detail: "32-bit glibc (the i386 dynamic linker) is not installed; SteamCMD is a 32-bit binary and will fail with a confusing \"file not found\" even though it exists on disk",
		Fix:    "Debian/Ubuntu: sudo dpkg --add-architecture i386 && sudo apt update && sudo apt install libc6:i386  |  Fedora: sudo dnf install glibc.i686  |  Arch: enable [multilib] in /etc/pacman.conf, then sudo pacman -S lib32-glibc",
	}
}

// checkPython3 verifies umu-run has an interpreter to run under: any of
// python3 / python3.10 … python3.N / python that is >= 3.10, or the explicit
// linux.umu_python_bin override. The scan + selection lives in
// python_linux.go so a launch pins the exact same interpreter this check
// blessed.
func checkPython3() *Problem { return pythonProblem() }

// libzstdPaths are common locations for libzstd.so.1, which umu's pyzstd
// dependency links against.
var libzstdPaths = []string{
	"/usr/lib/x86_64-linux-gnu/libzstd.so.1",
	"/usr/lib64/libzstd.so.1",
	"/lib/x86_64-linux-gnu/libzstd.so.1",
	"/usr/lib/libzstd.so.1",
}

func checkLibzstd() *Problem {
	for _, p := range libzstdPaths {
		if fileExists(p) {
			return nil
		}
	}
	// Fall back to asking the dynamic linker's cache directly — covers
	// distros/architectures not in the hardcoded path list above.
	if out, err := exec.Command("ldconfig", "-p").Output(); err == nil {
		if strings.Contains(string(out), "libzstd.so.1") {
			return nil
		}
	}
	return &Problem{
		Name:   "libzstd",
		Detail: "libzstd.so.1 was not found; umu-launcher's pyzstd dependency links against it and will fail to import",
		Fix:    "Debian/Ubuntu: sudo apt install libzstd1  |  Fedora: sudo dnf install libzstd  |  Arch: sudo pacman -S zstd",
	}
}

func checkTar() *Problem {
	if _, err := exec.LookPath("tar"); err == nil {
		return nil
	}
	return &Problem{
		Name:   "tar",
		Detail: "tar is required to unpack GE-Proton and the umu-launcher zipapp",
		Fix:    "Install tar via your distro's package manager (virtually always already present)",
	}
}

// xvfbInstallHint is the per-distro install line, kept in one place because
// three different messages point at it (preflight, the vcredist skip note and
// the launch-time error).
//
// The package names here are the ones that ship **Xvfb**, the X.Org virtual
// framebuffer server — not Debian's xvfb-run wrapper script, which this program
// no longer uses precisely because Fedora/RHEL/Arch don't ship it. See
// docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md.
const xvfbInstallHint = "安装 Xvfb（Debian/Ubuntu: sudo apt install xvfb  |  " +
	"Fedora/RHEL: sudo dnf install xorg-x11-server-Xvfb  |  " +
	"Arch: sudo pacman -S xorg-server-xvfb  |  " +
	"openSUSE: sudo zypper install xorg-x11-server-extra）"

// xvfbFontHint covers a failure only a self-managed Xvfb can even see: a
// minimal install with no fonts makes the X server exit outright
// ("could not open default font 'fixed'"). Under xvfb-run this happened too,
// it just went to /dev/null along with everything else the server said.
const xvfbFontHint = "Xvfb 缺少基础字体，装上即可（Debian/Ubuntu: sudo apt install xfonts-base  |  " +
	"Fedora/RHEL: sudo dnf install xorg-x11-fonts-misc  |  Arch: sudo pacman -S xorg-fonts-misc）"

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
	cfg := getConfig()
	_, blocked := planDisplay(cfg)
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
		Fix: xvfbInstallHint + "。若 " + x11SocketDir + " 是只读挂载（WSLg 就是这么挂的），" +
			"asa-server 以 root 运行时会尝试把它重新挂载为可写（linux.allow_x11_remount，" +
			"默认开）；这一步也失败时，需要系统里有一个不需要 xauth cookie 就能连的 X 服务，" +
			"并可用 config.yaml 的 linux.display 指定它",
	}
}

// checkAppArmorUserns reimplements
// scripts/ark_instance_manager.sh's check_userns_restriction(): Ubuntu
// 23.10+ restricts unprivileged user namespaces by default, which breaks
// pressure-vessel's bwrap sandboxing with "bwrap: setting up uid map:
// Permission denied" and no other diagnostic. Reads the /proc knob directly
// instead of shelling out to sysctl — one less external command dependency,
// same information.
func checkAppArmorUserns() *Problem {
	data, err := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns")
	if err != nil {
		return nil // sysctl doesn't exist on this kernel (most distros) — nothing to flag
	}
	val, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || val == 0 {
		return nil
	}
	return &Problem{
		Name:   "apparmor-userns",
		Detail: "this system restricts unprivileged user namespaces (Ubuntu AppArmor hardening); the Steam Linux Runtime container cannot start under this restriction and every launch will fail with \"bwrap: setting up uid map: Permission denied\"",
		Fix:    "sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0 && echo 'kernel.apparmor_restrict_unprivileged_userns = 0' | sudo tee /etc/sysctl.d/99-umu-userns.conf",
	}
}
