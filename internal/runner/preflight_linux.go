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
	if p := checkXvfb(); p != nil {
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
const xvfbInstallHint = "安装 xvfb（Debian/Ubuntu: sudo apt install xvfb  |  " +
	"Fedora: sudo dnf install xorg-x11-server-Xvfb  |  Arch: sudo pacman -S xorg-server-xvfb）"

// checkXvfb requires xvfb-run, and does so as a **blocker**, not an advisory.
//
// A headless box has no X server, and Wine's winex11.drv failing to connect
// makes every CreateWindow call fail — which kills, before it prints a single
// line, both AsaApiLoader.exe (ArkApi: measured exit 3 after 5s, empty log,
// see display_linux.go) and Microsoft's vc_redist installer (exit 203).
// Neither has a headless mode to fall back to, so "install a virtual X server"
// is the only fix, and xvfb-run is how the launch path uses it.
//
// Why this one is allowed to be a blocker when "the acl package isn't
// installed" was explicitly demoted to an advisory
// (docs/ACL_PERMISSION_HARDENING_PLAN.md §1): missing ACLs degrade to a
// working chown fallback, missing xvfb degrades to nothing at all. There is no
// second path.
//
// Deliberately NOT satisfied by a live DISPLAY. `asa-server setup` is usually
// typed in a desktop/WSL shell that has one, while the systemd unit that
// actually launches instances does not — accepting DISPLAY here would let the
// check pass on the exact machines where the launch later fails.
// --ignore-preflight remains the escape hatch.
func checkXvfb() *Problem {
	if _, err := exec.LookPath("xvfb-run"); err == nil {
		return nil
	}
	return &Problem{
		Name: "xvfb",
		Detail: "xvfb-run was not found; ArkApi's AsaApiLoader.exe and Microsoft's VC++ redist installer " +
			"both create Win32 windows, and under Wine that fails outright without an X display " +
			"(the loader dies with exit code 3 before writing any log). A live DISPLAY in your " +
			"current shell does not count — the systemd service runs without one",
		Fix: xvfbInstallHint,
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
