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
	return problems
}

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

func checkPython3() *Problem {
	path, err := exec.LookPath("python3")
	if err != nil {
		return &Problem{
			Name:   "python3",
			Detail: "python3 is required to run the umu-launcher zipapp",
			Fix:    "Install python3 >= 3.10 via your distro's package manager",
		}
	}

	out, err := exec.Command(path, "-c", "import sys; print(sys.version_info >= (3, 10))").Output()
	if err != nil || strings.TrimSpace(string(out)) != "True" {
		ver, _ := exec.Command(path, "--version").CombinedOutput()
		return &Problem{
			Name:   "python3-version",
			Detail: "umu-launcher requires Python >= 3.10, found: " + strings.TrimSpace(string(ver)),
			Fix:    "Update Python, or use a backports repo on older distros",
		}
	}
	return nil
}

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
