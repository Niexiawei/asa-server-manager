//go:build linux

package runner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// The umu-launcher zipapp is executed by a Python interpreter (its shebang is
// "#!/usr/bin/env python3"). umu needs Python >= 3.10, but a distro's default
// python3 is often older (RHEL 8, Ubuntu 20.04, Debian 11) and must not be
// replaced. So instead of checking only "python3", we scan the versioned
// names a parallel install provides (python3.10 … python3.N) and pin the
// newest one for every subsequent umu-run invocation — see
// docs/UMU_PYTHON_DISCOVERY_PLAN.md.

const (
	// pythonMinMinor is the lowest Python 3.x umu-launcher's zipapp accepts.
	pythonMinMinor = 10
	// pythonMaxMinorProbe bounds the auto-detect scan (python3.10 … this).
	// Bump when CPython ships a newer minor; users already on it can also
	// point linux.umu_python_bin at it explicitly.
	pythonMaxMinorProbe = 20
	// pythonProbeScript prints "<major> <minor>" for the interpreter that runs it.
	pythonProbeScript = `import sys; print("%d %d" % sys.version_info[:2])`
)

const pythonFixHint = "install any Python >= 3.10 alongside the system python3 (no need to replace it): " +
	"Debian/Ubuntu: sudo add-apt-repository ppa:deadsnakes/ppa && sudo apt install python3.12  |  " +
	"RHEL/Alma/Rocky: sudo dnf install python3.12  |  Arch: sudo pacman -S python  |  " +
	"or set linux.umu_python_bin in config.yaml to an explicit interpreter path (venv / pyenv supported)"

// pythonInfo is a resolved, version-checked (>= 3.<pythonMinMinor>) interpreter.
type pythonInfo struct {
	Path   string // absolute; used as argv[0], so the child's own PATH is irrelevant
	Major  int
	Minor  int
	Source string // "config" | "auto"
}

func (p pythonInfo) version() string { return strconv.Itoa(p.Major) + "." + strconv.Itoa(p.Minor) }

// pythonError carries the Problem.Name the preflight check should report.
type pythonError struct {
	name string
	msg  string
}

func (e *pythonError) Error() string { return e.msg }

var pyCache struct {
	mu  sync.Mutex
	key string // the cfg.PythonBin this result was resolved for
	got *pythonInfo
}

// resolvePython finds the interpreter umu-run should run under: an explicit
// cfg.PythonBin override wins outright (no auto-detect fallback), otherwise
// the newest system python3 >= 3.<pythonMinMinor>. Successful results are
// memoised per override value; failures are not cached, because the preflight
// API is a fix-and-retry loop.
func resolvePython() (pythonInfo, error) {
	override := strings.TrimSpace(getConfig().PythonBin)

	pyCache.mu.Lock()
	defer pyCache.mu.Unlock()

	if pyCache.got != nil && pyCache.key == override {
		return *pyCache.got, nil
	}

	var (
		info pythonInfo
		err  error
	)

	switch override {
	case "":
		info, err = resolvePythonAuto()
	default:
		info, err = resolvePythonExplicit(override)
	}

	if err != nil {
		return pythonInfo{}, err
	}

	pyCache.key = override
	pyCache.got = &info

	return info, nil
}

// resolvePythonExplicit validates a user-supplied interpreter. It deliberately
// does NOT resolve symlinks: a venv's bin/python is a symlink to its base
// interpreter, and running the link (not its target) is what keeps the venv
// context.
func resolvePythonExplicit(spec string) (pythonInfo, error) {
	expanded, err := expandUser(spec)
	if err != nil {
		return pythonInfo{}, &pythonError{name: "python3-config", msg: err.Error()}
	}

	// LookPath handles both a bare name (searched on PATH) and a path with a
	// separator (checked as-is), and verifies the file is executable.
	path, err := exec.LookPath(expanded)
	if err != nil {
		return pythonInfo{}, &pythonError{
			name: "python3-config",
			msg:  fmt.Sprintf("linux.umu_python_bin %q: not found or not executable: %v", spec, err),
		}
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}

	major, minor, err := pythonVersionOf(abs)
	if err != nil {
		return pythonInfo{}, &pythonError{
			name: "python3-config",
			msg:  fmt.Sprintf("linux.umu_python_bin %q: could not determine Python version: %v", spec, err),
		}
	}

	if !pythonVersionOK(major, minor) {
		return pythonInfo{}, &pythonError{
			name: "python3-config",
			msg: fmt.Sprintf(
				"linux.umu_python_bin %q is Python %d.%d; umu-launcher needs >= 3.%d",
				spec, major, minor, pythonMinMinor,
			),
		}
	}

	return pythonInfo{Path: abs, Major: major, Minor: minor, Source: "config"}, nil
}

// resolvePythonAuto scans the system for python3 / python3.10 … python3.N /
// python and returns the highest version that is >= 3.<pythonMinMinor>.
func resolvePythonAuto() (pythonInfo, error) {
	found := []pythonInfo{}
	probed := []string{} // "name (X.Y)" for the failure message
	seen := map[string]bool{}

	for _, name := range pythonCandidateNames() {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}

		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}

		if real, err := filepath.EvalSymlinks(abs); err == nil {
			abs = real // dedupe python3 -> python3.11 against an explicit python3.11
		}

		if seen[abs] {
			continue
		}

		seen[abs] = true

		major, minor, err := pythonVersionOf(abs)
		if err != nil {
			continue
		}

		probed = append(probed, fmt.Sprintf("%s (%d.%d)", name, major, minor))

		if !pythonVersionOK(major, minor) {
			continue
		}

		found = append(found, pythonInfo{Path: abs, Major: major, Minor: minor, Source: "auto"})
	}

	if len(found) == 0 {
		if len(probed) == 0 {
			return pythonInfo{}, &pythonError{
				name: "python3",
				msg: fmt.Sprintf(
					"no Python interpreter found (looked for python3, python3.%d … python3.%d, python); umu-launcher needs one",
					pythonMinMinor, pythonMaxMinorProbe,
				),
			}
		}

		return pythonInfo{}, &pythonError{
			name: "python3-version",
			msg: fmt.Sprintf(
				"no Python >= 3.%d found; detected: %s",
				pythonMinMinor, strings.Join(probed, ", "),
			),
		}
	}

	slices.SortStableFunc(found, func(a, b pythonInfo) int {
		if a.Major != b.Major {
			return b.Major - a.Major
		}

		return b.Minor - a.Minor
	})

	return found[0], nil
}

// pythonCandidateNames lists the interpreter names to probe, versioned names
// first and highest-first so the probe log reads naturally (the actual winner
// is still chosen by detected version).
func pythonCandidateNames() []string {
	names := make([]string, 0, pythonMaxMinorProbe-pythonMinMinor+3)
	for minor := pythonMaxMinorProbe; minor >= pythonMinMinor; minor-- {
		names = append(names, fmt.Sprintf("python3.%d", minor))
	}

	return append(names, "python3", "python")
}

// pythonVersionOf runs the interpreter and parses "<major> <minor>".
func pythonVersionOf(path string) (major, minor int, err error) {
	out, err := exec.Command(path, "-c", pythonProbeScript).Output()
	if err != nil {
		return 0, 0, err
	}

	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected version probe output %q", strings.TrimSpace(string(out)))
	}

	if major, err = strconv.Atoi(fields[0]); err != nil {
		return 0, 0, fmt.Errorf("bad major %q: %w", fields[0], err)
	}

	if minor, err = strconv.Atoi(fields[1]); err != nil {
		return 0, 0, fmt.Errorf("bad minor %q: %w", fields[1], err)
	}

	return major, minor, nil
}

func pythonVersionOK(major, minor int) bool {
	if major > 3 {
		return true
	}

	return major == 3 && minor >= pythonMinMinor
}

// expandUser expands a leading "~" / "~/" against the current user's home.
func expandUser(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot expand %q: %w", p, err)
	}

	if p == "~" {
		return home, nil
	}

	return filepath.Join(home, p[2:]), nil
}

// umuInterpreter is the single choke point for "which Python runs umu-run".
// A failure is fatal to a launch: callers surface it rather than letting the
// zipapp's shebang fall back to a possibly-too-old system python3.
func umuInterpreter() (pythonInfo, error) { return resolvePython() }

// pythonProblem turns a resolve failure into a preflight Problem (nil on success).
func pythonProblem() *Problem {
	_, err := resolvePython()
	if err == nil {
		return nil
	}

	name := "python3"

	var pe *pythonError
	if errors.As(err, &pe) {
		name = pe.name
	}

	return &Problem{Name: name, Detail: err.Error(), Fix: pythonFixHint}
}

func runtimePython() RuntimePythonInfo {
	info, err := resolvePython()
	if err != nil {
		return RuntimePythonInfo{Resolved: false}
	}

	return RuntimePythonInfo{
		Resolved: true,
		Path:     info.Path,
		Version:  info.version(),
		Source:   info.Source,
	}
}
