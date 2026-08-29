//go:build linux

package procx

import (
	"bytes"
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

// processCmdline returns the command line of the process with the given
// pid, in the same flattened shape QueryProcess's CommandLine field uses.
func processCmdline(pid uint32) (string, error) {
	return readCmdline(int(pid))
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

// TerminateTree sends SIGTERM to pid and every descendant of it.
//
// KillTree/TerminateTree used to signal pid's process group instead
// (kill(-pgid)), on the assumption that Run's Setsid made the launcher the
// group leader of everything it spawns. On the umu/Wine path that assumption
// is false, and measurably so: umu-run -> srt-bwrap -> pv-adverb -> proton ->
// the game crosses THREE setsid boundaries, and a kill(-pgid) on the launcher
// reaches a process group whose only member is the launcher itself, leaving
// the whole container tree — including ArkAscendedServer.exe — running. See
// docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md §2.
//
// So this walks the real parent/child tree instead, which matches what
// Windows's `taskkill /T` does. The game runs in the same PID namespace as
// this process (only mount namespaces differ under pressure-vessel), so the
// PIDs read here are directly signallable.
func TerminateTree(pid int) error {
	return signalTree(pid, syscall.SIGTERM)
}

// KillTree sends SIGKILL to pid and every descendant of it. See TerminateTree.
func KillTree(pid int) error {
	return signalTree(pid, syscall.SIGKILL)
}

func signalPID(pid int, sig syscall.Signal) error {
	if pid <= 1 {
		return fmt.Errorf("procx: refusing to signal pid %d", pid)
	}
	return syscall.Kill(pid, sig)
}

func signalTree(pid int, sig syscall.Signal) error {
	if pid <= 1 {
		return fmt.Errorf("procx: refusing to signal pid %d", pid)
	}
	self := os.Getpid()
	tree := processTree(pid)

	// Process groups still get swept, but only those *led by a member of the
	// tree* — that catches grandchildren already reparented away (their ppid
	// link is gone, their pgid isn't) without ever touching a group this
	// launch doesn't own.
	inTree := make(map[int]bool, len(tree))
	for _, p := range tree {
		inTree[p] = true
	}
	for _, p := range tree {
		if p == self {
			continue
		}
		if pgid, err := syscall.Getpgid(p); err == nil && pgid > 1 && pgid != self && inTree[pgid] {
			_ = syscall.Kill(-pgid, sig)
		}
	}

	// Leaves first: signalling a parent before its children would let the
	// children be reparented to init mid-loop, and the entries after it in
	// the snapshot would then be signalled by PID anyway — but doing it in
	// this order keeps that window closed instead of relying on it.
	var firstErr error
	for i := len(tree) - 1; i >= 0; i-- {
		if tree[i] == self {
			continue
		}
		if err := syscall.Kill(tree[i], sig); err != nil && !errors.Is(err, syscall.ESRCH) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// processTree snapshots /proc and returns root plus every descendant of it,
// parents before children.
//
// It MUST be called before any signal is sent: the moment a parent dies its
// children are reparented to init and the ppid chain that identifies them as
// ours is gone for good.
func processTree(root int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return []int{root}
	}

	children := make(map[int][]int)
	for _, e := range entries {
		pid, convErr := strconv.Atoi(e.Name())
		if convErr != nil {
			continue // not a PID directory
		}
		if ppid, ok := readPPID(pid); ok {
			children[ppid] = append(children[ppid], pid)
		}
	}

	out := []int{root}
	seen := map[int]bool{root: true}
	for i := 0; i < len(out); i++ {
		for _, c := range children[out[i]] {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// readPPID reads the parent pid out of /proc/<pid>/stat.
func readPPID(pid int) (int, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, false
	}
	return parsePPIDFromStat(data)
}

// parsePPIDFromStat extracts field 4 (ppid) from a /proc/<pid>/stat line.
//
// The second field is the executable name in parentheses and may itself
// contain spaces and closing parens — Wine renames its threads freely, and
// "(Web Content)" is the classic example elsewhere — so the fixed fields are
// counted from the LAST ')' rather than by splitting the whole line. After it
// come state (field 3) and ppid (field 4).
func parsePPIDFromStat(data []byte) (int, bool) {
	i := bytes.LastIndexByte(data, ')')
	if i < 0 {
		return 0, false
	}
	fields := strings.Fields(string(data[i+1:]))
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false
	}
	return ppid, true
}
