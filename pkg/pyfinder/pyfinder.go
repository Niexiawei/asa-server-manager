// Package pyfinder finds a Python interpreter suitable for running the
// umu-launcher zipapp (its shebang is "#!/usr/bin/env python3", and it needs
// Python >= MinMinor). It does not know about umu, ASA, or config files — the
// caller supplies an optional explicit override string and gets back a
// resolved, version-checked interpreter or a typed error.
//
// A distro's default python3 is often older than umu-launcher needs (RHEL 8,
// Ubuntu 20.04, Debian 11) and must not be replaced, so instead of checking
// only "python3", Resolve scans the versioned names a parallel install
// provides (python3.10 … python3.N) and picks the newest one that qualifies.
// See docs/UMU_PYTHON_DISCOVERY_PLAN.md.
package pyfinder

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

const (
	// MinMinor is the lowest Python 3.x umu-launcher's zipapp accepts.
	MinMinor = 10
	// MaxMinorProbe bounds the auto-detect scan (python3.10 … this). Bump
	// when CPython ships a newer minor; users already on it can also point
	// the override at it explicitly.
	MaxMinorProbe = 20
	// probeScript prints "<major> <minor>" for the interpreter that runs it.
	probeScript = `import sys; print("%d %d" % sys.version_info[:2])`
)

// FixHint is a suggested remediation for a failed Resolve, suitable for a
// Problem.Fix field.
const FixHint = "install any Python >= 3.10 alongside the system python3 (no need to replace it): " +
	"Debian/Ubuntu: sudo add-apt-repository ppa:deadsnakes/ppa && sudo apt install python3.12  |  " +
	"RHEL/Alma/Rocky: sudo dnf install python3.12  |  Arch: sudo pacman -S python  |  " +
	"or point the override at an explicit interpreter path (venv / pyenv supported)"

// Info is a resolved, version-checked (>= 3.MinMinor) interpreter.
type Info struct {
	Path   string // absolute; used as argv[0], so the child's own PATH is irrelevant
	Major  int
	Minor  int
	Source string // "config" | "auto"
}

// Version renders e.g. "3.14".
func (i Info) Version() string { return strconv.Itoa(i.Major) + "." + strconv.Itoa(i.Minor) }

// Error carries a short id (for a Problem.Name-shaped field) alongside a
// human-readable message.
type Error struct {
	Name string
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Resolver caches the most recent successful Resolve, keyed by the override
// string it was resolved for, so repeated calls with an unchanged override
// don't re-run exec.LookPath / spawn a version probe on every launch.
// Failures are not cached — resolution failure is meant to be a fix-and-retry
// loop (e.g. the preflight API).
type Resolver struct {
	mu  sync.Mutex
	key string // the override this result was resolved for
	got *Info
}

// New returns a Resolver with an empty cache.
func New() *Resolver {
	return &Resolver{}
}

// Resolve finds the interpreter umu-run should run under: a non-empty
// override wins outright (no auto-detect fallback), otherwise the newest
// system python3 >= 3.MinMinor.
func (r *Resolver) Resolve(override string) (Info, error) {
	override = strings.TrimSpace(override)

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.got != nil && r.key == override {
		return *r.got, nil
	}

	var (
		info Info
		err  error
	)

	if override == "" {
		info, err = resolveAuto()
	} else {
		info, err = resolveExplicit(override)
	}

	if err != nil {
		return Info{}, err
	}

	r.key = override
	r.got = &info

	return info, nil
}

// resolveExplicit validates a user-supplied interpreter. It deliberately does
// NOT resolve symlinks: a venv's bin/python is a symlink to its base
// interpreter, and running the link (not its target) is what keeps the venv
// context.
func resolveExplicit(spec string) (Info, error) {
	expanded, err := expandUser(spec)
	if err != nil {
		return Info{}, &Error{Name: "python3-config", Msg: err.Error()}
	}

	// LookPath handles both a bare name (searched on PATH) and a path with a
	// separator (checked as-is), and verifies the file is executable.
	path, err := exec.LookPath(expanded)
	if err != nil {
		return Info{}, &Error{
			Name: "python3-config",
			Msg:  fmt.Sprintf("interpreter override %q: not found or not executable: %v", spec, err),
		}
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}

	major, minor, err := versionOf(abs)
	if err != nil {
		return Info{}, &Error{
			Name: "python3-config",
			Msg:  fmt.Sprintf("interpreter override %q: could not determine Python version: %v", spec, err),
		}
	}

	if !versionOK(major, minor) {
		return Info{}, &Error{
			Name: "python3-config",
			Msg: fmt.Sprintf(
				"interpreter override %q is Python %d.%d; umu-launcher needs >= 3.%d",
				spec, major, minor, MinMinor,
			),
		}
	}

	return Info{Path: abs, Major: major, Minor: minor, Source: "config"}, nil
}

// resolveAuto scans the system for python3 / python3.10 … python3.N / python
// and returns the highest version that is >= 3.MinMinor.
func resolveAuto() (Info, error) {
	found := []Info{}
	probed := []string{} // "name (X.Y)" for the failure message
	seen := map[string]bool{}

	for _, name := range CandidateNames() {
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

		major, minor, err := versionOf(abs)
		if err != nil {
			continue
		}

		probed = append(probed, fmt.Sprintf("%s (%d.%d)", name, major, minor))

		if !versionOK(major, minor) {
			continue
		}

		found = append(found, Info{Path: abs, Major: major, Minor: minor, Source: "auto"})
	}

	if len(found) == 0 {
		if len(probed) == 0 {
			return Info{}, &Error{
				Name: "python3",
				Msg: fmt.Sprintf(
					"no Python interpreter found (looked for python3, python3.%d … python3.%d, python); umu-launcher needs one",
					MinMinor, MaxMinorProbe,
				),
			}
		}

		return Info{}, &Error{
			Name: "python3-version",
			Msg: fmt.Sprintf(
				"no Python >= 3.%d found; detected: %s",
				MinMinor, strings.Join(probed, ", "),
			),
		}
	}

	slices.SortStableFunc(found, func(a, b Info) int {
		if a.Major != b.Major {
			return b.Major - a.Major
		}

		return b.Minor - a.Minor
	})

	return found[0], nil
}

// CandidateNames lists the interpreter names Resolve probes in auto mode,
// versioned names first and highest-first so a probe log reads naturally
// (the actual winner is still chosen by detected version).
func CandidateNames() []string {
	names := make([]string, 0, MaxMinorProbe-MinMinor+3)
	for minor := MaxMinorProbe; minor >= MinMinor; minor-- {
		names = append(names, fmt.Sprintf("python3.%d", minor))
	}

	return append(names, "python3", "python")
}

// versionOf runs the interpreter and parses "<major> <minor>".
func versionOf(path string) (major, minor int, err error) {
	out, err := exec.Command(path, "-c", probeScript).Output()
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

func versionOK(major, minor int) bool {
	if major > 3 {
		return true
	}

	return major == 3 && minor >= MinMinor
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

// AsError extracts an *Error from err, if any — a convenience for callers
// that want the short Name without importing errors.As at each call site.
func AsError(err error) (*Error, bool) {
	var pe *Error
	ok := errors.As(err, &pe)
	return pe, ok
}
