//go:build linux

package pyfinder

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// fakePython writes an executable stub that prints "<major> <minor>" (what
// the version probe parses) regardless of its arguments.
func fakePython(t *testing.T, dir, name string, major, minor int) string {
	t.Helper()

	p := filepath.Join(dir, name)
	body := "#!/bin/sh\necho \"" + strconv.Itoa(major) + " " + strconv.Itoa(minor) + "\"\n"

	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}

	return p
}

func fakeBrokenPython(t *testing.T, dir, name string) string {
	t.Helper()

	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}

	return p
}

// isolate points PATH at dir only.
func isolate(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir)
}

func TestResolveAuto_PicksNewest(t *testing.T) {
	dir := t.TempDir()
	isolate(t, dir)
	r := New()

	fakePython(t, dir, "python3.11", 3, 11)
	fakePython(t, dir, "python3.14", 3, 14)
	fakePython(t, dir, "python3", 3, 9)

	got, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got.Minor != 14 || got.Major != 3 {
		t.Fatalf("got %s, want 3.14", got.Version())
	}

	if got.Source != "auto" {
		t.Fatalf("Source = %q, want auto", got.Source)
	}

	if filepath.Base(got.Path) != "python3.14" {
		t.Fatalf("Path = %q, want .../python3.14", got.Path)
	}
}

func TestResolveAuto_VersionTiePrefersVersionedName(t *testing.T) {
	dir := t.TempDir()
	isolate(t, dir)
	r := New()

	// Two distinct real binaries, same version — versioned name is probed first.
	fakePython(t, dir, "python3", 3, 12)
	fakePython(t, dir, "python3.12", 3, 12)

	got, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if filepath.Base(got.Path) != "python3.12" {
		t.Fatalf("Path = %q, want .../python3.12", got.Path)
	}
}

func TestResolveAuto_DedupesSymlink(t *testing.T) {
	dir := t.TempDir()
	isolate(t, dir)
	r := New()

	real := fakePython(t, dir, "python3.11", 3, 11)
	if err := os.Symlink(real, filepath.Join(dir, "python3")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Just needs to resolve without error; both names collapse to one entry.
	if _, err := r.Resolve(""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestResolveAuto_SkipsBroken(t *testing.T) {
	dir := t.TempDir()
	isolate(t, dir)
	r := New()

	fakeBrokenPython(t, dir, "python3.13")
	fakePython(t, dir, "python3.10", 3, 10)

	got, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got.Minor != 10 {
		t.Fatalf("got %s, want 3.10", got.Version())
	}
}

func TestResolveAuto_AllTooOld(t *testing.T) {
	dir := t.TempDir()
	isolate(t, dir)
	r := New()

	fakePython(t, dir, "python3", 3, 9)
	fakePython(t, dir, "python", 2, 7)

	_, err := r.Resolve("")
	if err == nil {
		t.Fatal("want error, got nil")
	}

	pe, ok := AsError(err)
	if !ok || pe.Name != "python3-version" {
		t.Fatalf("want *Error{Name:python3-version}, got %v", err)
	}
}

func TestResolveAuto_NoneFound(t *testing.T) {
	dir := t.TempDir()
	isolate(t, dir)
	r := New()

	_, err := r.Resolve("")
	if err == nil {
		t.Fatal("want error, got nil")
	}

	pe, ok := AsError(err)
	if !ok || pe.Name != "python3" {
		t.Fatalf("want *Error{Name:python3}, got %v", err)
	}
}

func TestResolveExplicit_AbsolutePathVenvStyle(t *testing.T) {
	dir := t.TempDir()
	isolate(t, dir)
	r := New()

	// Only an old python3 on PATH — the override must win regardless.
	fakePython(t, dir, "python3", 3, 9)

	venv := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(venv, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	bin := fakePython(t, venv, "python", 3, 12) // venv interpreters are just "python"

	got, err := r.Resolve(bin)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got.Source != "config" || got.Path != bin || got.Minor != 12 {
		t.Fatalf("got %+v, want {config, %s, 3.12}", got, bin)
	}
}

func TestResolveExplicit_TildeExpanded(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	isolate(t, dir)
	t.Setenv("HOME", home)
	r := New()

	binDir := filepath.Join(home, "venv", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	fakePython(t, binDir, "python", 3, 13)

	got, err := r.Resolve("~/venv/bin/python")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got.Path != filepath.Join(binDir, "python") {
		t.Fatalf("Path = %q, want %q", got.Path, filepath.Join(binDir, "python"))
	}
}

func TestResolveExplicit_NoFallbackWhenMissing(t *testing.T) {
	dir := t.TempDir()
	isolate(t, dir)
	r := New()

	// A perfectly good auto candidate exists; explicit-but-missing must still fail.
	fakePython(t, dir, "python3.14", 3, 14)

	_, err := r.Resolve("/nonexistent/python")
	if err == nil {
		t.Fatal("want error, got nil")
	}

	pe, ok := AsError(err)
	if !ok || pe.Name != "python3-config" {
		t.Fatalf("want *Error{Name:python3-config}, got %v", err)
	}
}

func TestResolveExplicit_RejectsTooOld(t *testing.T) {
	dir := t.TempDir()
	isolate(t, dir)
	r := New()

	bin := fakePython(t, dir, "oldpy", 3, 9)

	_, err := r.Resolve(bin)

	pe, ok := AsError(err)
	if !ok || pe.Name != "python3-config" {
		t.Fatalf("want *Error{Name:python3-config}, got %v", err)
	}
}

func TestResolve_CacheInvalidatesOnOverrideChange(t *testing.T) {
	dir := t.TempDir()
	isolate(t, dir)
	r := New()

	fakePython(t, dir, "python3.14", 3, 14)

	first, err := r.Resolve("")
	if err != nil || first.Minor != 14 {
		t.Fatalf("first resolve: %+v %v", first, err)
	}

	// Override changes -> must re-resolve, not hand back the cached auto result.
	if _, err := r.Resolve("/nonexistent/python"); err == nil {
		t.Fatal("want error after override change, got cached success")
	}

	// Back to auto -> succeeds again.
	if again, err := r.Resolve(""); err != nil || again.Minor != 14 {
		t.Fatalf("re-resolve auto: %+v %v", again, err)
	}
}

func TestCandidateNames_Order(t *testing.T) {
	names := CandidateNames()

	if names[0] != "python3."+strconv.Itoa(MaxMinorProbe) {
		t.Fatalf("names[0] = %q, want highest versioned", names[0])
	}

	if names[len(names)-2] != "python3" || names[len(names)-1] != "python" {
		t.Fatalf("tail = %v, want [... python3 python]", names[len(names)-2:])
	}
}
