//go:build linux

package procx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// IsProcessExited reports whether the process with the given PID has exited.
//
// os.FindProcess never fails on Unix (it doesn't check existence), so
// liveness is checked with signal 0: the kernel still validates the PID
// without actually delivering anything. A zombie still answers this as
// "not exited" (matches the Windows semantics: exit status not yet reaped).
func IsProcessExited(pid uint32) (bool, error) {
	proc, err := os.FindProcess(int(pid))
	if err != nil {
		return true, nil
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return true, nil
	}
	return false, nil
}

// ProcessImageName returns the full path of the executable behind pid, read
// from the /proc/<pid>/exe symlink. Unlike Windows, this fails for processes
// owned by other users without matching privileges, and for zombies.
func ProcessImageName(pid uint32) (string, error) {
	link, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return "", fmt.Errorf("process %d not found or inaccessible: %w", pid, err)
	}
	return link, nil
}

// RunAsAdmin has no Linux equivalent. It existed on Windows solely to
// relaunch with elevation for actions like writing the machine-wide
// certificate trust store; the Linux implementations of those actions
// (internal/certmgr's Linux TrustCA, systemd service install) work at the
// OS/root layer directly rather than through a GUI-style elevation prompt.
func RunAsAdmin(args string) error {
	return errors.New("procx: RunAsAdmin is not applicable on linux")
}

// QueryProcess scans /proc for processes whose image/comm name contains name
// (substring, case-insensitive — mirrors the Windows WQL "LIKE '%name%'"
// semantics used by the Windows implementation) and whose cmdline contains
// cmdlineSubstr (exact substring; empty means "don't filter").
func QueryProcess(name, cmdlineSubstr string) ([]Win32Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("failed to read /proc: %w", err)
	}

	nameLower := strings.ToLower(name)
	var results []Win32Process
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue // not a PID directory
		}

		cmdline, err := readCmdline(pid)
		if err != nil {
			continue // process gone or inaccessible between ReadDir and here
		}

		procName := processImageBaseName(pid)
		if nameLower != "" && !strings.Contains(strings.ToLower(procName), nameLower) {
			continue
		}
		if cmdlineSubstr != "" && !strings.Contains(cmdline, cmdlineSubstr) {
			continue
		}

		results = append(results, Win32Process{
			Name:        procName,
			ProcessId:   uint32(pid),
			CommandLine: cmdline,
		})
	}
	return results, nil
}

// readCmdline returns /proc/<pid>/cmdline with its NUL argument separators
// turned into spaces, matching the flat string shape WMI's CommandLine gives.
func readCmdline(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.ReplaceAll(string(data), "\x00", " ")), nil
}

// processImageBaseName resolves a process's image name the way WMI's
// Win32_Process.Name does (the executable's base file name): the
// /proc/<pid>/exe symlink target's basename, falling back to
// /proc/<pid>/comm (kernel-truncated to 15 bytes but always present) when
// exe is unreadable (permission, or the process already exited).
func processImageBaseName(pid int) string {
	if link, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil {
		return filepath.Base(link)
	}
	if comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
		return strings.TrimSpace(string(comm))
	}
	return ""
}

// Terminate sends SIGTERM to a single process.
func Terminate(pid int) error {
	return signalPID(pid, syscall.SIGTERM)
}

// Kill sends SIGKILL to a single process.
func Kill(pid int) error {
	return signalPID(pid, syscall.SIGKILL)
}

// TerminateTree sends SIGTERM to pid's entire process group.
//
// This is only meaningful for a process started as its own group leader
// (setsid — see pkg/proctree's Linux implementation, and
// docs/LINUX_COMPATIBILITY_PLAN.md §5.4/§5.6 risk 9). The pgid>1 &&
// pgid!=os.Getpid() assertion exists precisely so a wrong lookup (pgid 1 is
// init's, or ours) can never take down the wrong thing.
func TerminateTree(pid int) error {
	return signalGroup(pid, syscall.SIGTERM)
}

// KillTree sends SIGKILL to pid's entire process group. See TerminateTree.
func KillTree(pid int) error {
	return signalGroup(pid, syscall.SIGKILL)
}

func signalPID(pid int, sig syscall.Signal) error {
	if pid <= 1 {
		return fmt.Errorf("procx: refusing to signal pid %d", pid)
	}
	return syscall.Kill(pid, sig)
}

func signalGroup(pid int, sig syscall.Signal) error {
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return fmt.Errorf("procx: failed to resolve process group for pid %d: %w", pid, err)
	}
	if pgid <= 1 || pgid == os.Getpid() {
		return fmt.Errorf("procx: refusing to signal process group %d", pgid)
	}
	return syscall.Kill(-pgid, sig)
}
