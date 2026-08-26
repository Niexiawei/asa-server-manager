package archive

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// writeTar builds an in-memory tar stream from the given entries for tests.
type tarEntry struct {
	name     string
	typeflag byte
	body     string
	linkname string
	mode     int64
}

func buildTar(t *testing.T, entries []tarEntry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0644
		}
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: e.typeflag,
			Mode:     mode,
			Size:     int64(len(e.body)),
			Linkname: e.linkname,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader(%s): %v", e.name, err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("Write(%s): %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar Close: %v", err)
	}
	return &buf
}

func TestExtractTar_PlainFiles(t *testing.T) {
	dest := t.TempDir()
	src := buildTar(t, []tarEntry{
		{name: "a.txt", typeflag: tar.TypeReg, body: "hello"},
		{name: "sub/", typeflag: tar.TypeDir},
		{name: "sub/b.txt", typeflag: tar.TypeReg, body: "world"},
	})

	if err := ExtractTar(src, dest, ""); err != nil {
		t.Fatalf("extractTar: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "a.txt"))
	if err != nil || string(got) != "hello" {
		t.Errorf("a.txt = %q, %v; want %q, nil", got, err, "hello")
	}
	got, err = os.ReadFile(filepath.Join(dest, "sub", "b.txt"))
	if err != nil || string(got) != "world" {
		t.Errorf("sub/b.txt = %q, %v; want %q, nil", got, err, "world")
	}
}

// TestExtractTar_StripPrefix mirrors umu-launcher's release layout: a single
// "umu/" directory wrapping the real payload, which must land directly in
// destDir (the "tar --strip-components=1" equivalent).
func TestExtractTar_StripPrefix(t *testing.T) {
	dest := t.TempDir()
	src := buildTar(t, []tarEntry{
		{name: "umu/", typeflag: tar.TypeDir},
		{name: "umu/umu-run", typeflag: tar.TypeReg, body: "#!/bin/sh\n", mode: 0755},
		{name: "other/should-be-skipped.txt", typeflag: tar.TypeReg, body: "nope"},
	})

	if err := ExtractTar(src, dest, "umu/"); err != nil {
		t.Fatalf("extractTar: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dest, "umu-run")); err != nil {
		t.Errorf("expected %s/umu-run to exist: %v", dest, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "umu")); err == nil {
		t.Errorf("expected no leftover %s/umu directory", dest)
	}
	if _, err := os.Stat(filepath.Join(dest, "other")); err == nil {
		t.Errorf("expected entries outside the stripped prefix to be skipped")
	}
}

// TestExtractTar_RejectsZipSlip is the actual security-relevant case: a
// crafted archive entry using ".." to escape destDir must be rejected, not
// silently written outside it.
func TestExtractTar_RejectsZipSlip(t *testing.T) {
	dest := t.TempDir()
	src := buildTar(t, []tarEntry{
		{name: "../evil.txt", typeflag: tar.TypeReg, body: "pwned"},
	})

	err := ExtractTar(src, dest, "")
	if err == nil {
		t.Fatal("expected extractTar to reject a path-traversal entry, got nil error")
	}

	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "evil.txt")); statErr == nil {
		t.Fatal("zip-slip entry was written outside destDir")
	}
}

// TestExtractTar_RejectsZipSlipNestedTraversal covers a traversal buried
// under an otherwise-legitimate-looking nested path, not just a bare "../".
func TestExtractTar_RejectsZipSlipNestedTraversal(t *testing.T) {
	dest := t.TempDir()
	src := buildTar(t, []tarEntry{
		{name: "GE-Proton10-34/../../evil.txt", typeflag: tar.TypeReg, body: "pwned"},
	})

	if err := ExtractTar(src, dest, ""); err == nil {
		t.Fatal("expected extractTar to reject a nested path-traversal entry, got nil error")
	}
}
