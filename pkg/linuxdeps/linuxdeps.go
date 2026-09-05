// Package linuxdeps probes the host-level dependencies a Wine/Proton game
// server launch needs on Linux (32-bit glibc, libzstd, tar, the AppArmor
// userns restriction), reimplementing what
// scripts/ark_instance_manager.sh's check_dependencies()/
// check_userns_restriction() do as functional checks — does the actual
// artifact exist / does the actual kernel knob say what we need — rather
// than per-distro package-manager queries. A working loader/library/
// interpreter matters here, not which package happened to provide it.
//
// It does not know about umu, ASA, or config: every check here reads a fixed
// well-known path or runs a fixed command. The Python interpreter check is
// the one exception — it needs the caller's configured override and shared
// resolver cache, so Check takes it as an injected callback rather than
// importing asa-server/pkg/pyfinder itself.
package linuxdeps

import (
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"asa-server/pkg/problem"
)

// Check runs every host-dependency probe this package knows about, plus
// pythonProblem — an injected check the caller builds (it needs a resolver
// cache and a possibly-configured interpreter override that only the caller
// knows about).
func Check(pythonProblem func() *problem.Problem) []problem.Problem {
	var problems []problem.Problem
	for _, check := range []func() *problem.Problem{
		checkGlibc32,
		pythonProblem,
		checkLibzstd,
		checkTar,
		checkAppArmorUserns,
	} {
		if p := check(); p != nil {
			problems = append(problems, *p)
		}
	}
	return problems
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
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

func checkGlibc32() *problem.Problem {
	if slices.ContainsFunc(glibc32LoaderPaths, fileExists) {
		return nil
	}
	return &problem.Problem{
		Name:   "glibc32",
		Detail: "32-bit glibc (the i386 dynamic linker) is not installed; SteamCMD is a 32-bit binary and will fail with a confusing \"file not found\" even though it exists on disk",
		Fix:    "Debian/Ubuntu: sudo dpkg --add-architecture i386 && sudo apt update && sudo apt install libc6:i386  |  Fedora: sudo dnf install glibc.i686  |  Arch: enable [multilib] in /etc/pacman.conf, then sudo pacman -S lib32-glibc",
	}
}

// libzstdPaths are common locations for libzstd.so.1, which umu's pyzstd
// dependency links against.
var libzstdPaths = []string{
	"/usr/lib/x86_64-linux-gnu/libzstd.so.1",
	"/usr/lib64/libzstd.so.1",
	"/lib/x86_64-linux-gnu/libzstd.so.1",
	"/usr/lib/libzstd.so.1",
}

func checkLibzstd() *problem.Problem {
	if slices.ContainsFunc(libzstdPaths, fileExists) {
		return nil
	}
	// Fall back to asking the dynamic linker's cache directly — covers
	// distros/architectures not in the hardcoded path list above.
	if out, err := exec.Command("ldconfig", "-p").Output(); err == nil {
		if strings.Contains(string(out), "libzstd.so.1") {
			return nil
		}
	}
	return &problem.Problem{
		Name:   "libzstd",
		Detail: "libzstd.so.1 was not found; umu-launcher's pyzstd dependency links against it and will fail to import",
		Fix:    "Debian/Ubuntu: sudo apt install libzstd1  |  Fedora: sudo dnf install libzstd  |  Arch: sudo pacman -S zstd",
	}
}

func checkTar() *problem.Problem {
	if _, err := exec.LookPath("tar"); err == nil {
		return nil
	}
	return &problem.Problem{
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
func checkAppArmorUserns() *problem.Problem {
	data, err := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns")
	if err != nil {
		return nil // sysctl doesn't exist on this kernel (most distros) — nothing to flag
	}
	val, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || val == 0 {
		return nil
	}
	return &problem.Problem{
		Name:   "apparmor-userns",
		Detail: "this system restricts unprivileged user namespaces (Ubuntu AppArmor hardening); the Steam Linux Runtime container cannot start under this restriction and every launch will fail with \"bwrap: setting up uid map: Permission denied\"",
		Fix:    "sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0 && echo 'kernel.apparmor_restrict_unprivileged_userns = 0' | sudo tee /etc/sysctl.d/99-umu-userns.conf",
	}
}
