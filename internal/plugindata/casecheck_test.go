package plugindata

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectPluginsCaseMismatch_ExactCaseIsNotAMismatch(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "ArkApi"))

	if _, ok := detectPluginsCaseMismatch(dir); ok {
		t.Fatal("exact-case ArkApi directory must not be reported as a mismatch")
	}
}

func TestDetectPluginsCaseMismatch_FindsDifferentCase(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "arkapi"))

	name, ok := detectPluginsCaseMismatch(dir)
	if !ok {
		t.Fatal("expected a case mismatch to be detected")
	}
	if name != "arkapi" {
		t.Fatalf("expected actualName %q, got %q", "arkapi", name)
	}
}

func TestDetectPluginsCaseMismatch_NoArkApiDirAtAll(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "SomethingElse"))

	if _, ok := detectPluginsCaseMismatch(dir); ok {
		t.Fatal("must not report a mismatch when no ArkApi-like directory exists")
	}
}

func TestDetectPluginsCaseMismatch_Win64DirMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")

	if _, ok := detectPluginsCaseMismatch(dir); ok {
		t.Fatal("a missing Win64 directory must not be reported as a mismatch")
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
