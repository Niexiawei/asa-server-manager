package syncthingmanage

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseAsset(t *testing.T) {
	cases := []struct {
		goos, goarch string
		wantName     string
		wantExt      string
		wantErr      bool
	}{
		{"windows", "amd64", "syncthing-windows-amd64-v" + syncthingVersion + ".zip", "zip", false},
		{"linux", "amd64", "syncthing-linux-amd64-v" + syncthingVersion + ".tar.gz", "tar.gz", false},
		{"darwin", "amd64", "", "", true},
	}
	for _, c := range cases {
		name, ext, err := releaseAsset(c.goos, c.goarch)
		if c.wantErr {
			if err == nil {
				t.Errorf("releaseAsset(%q, %q): want error, got nil", c.goos, c.goarch)
			}
			continue
		}
		if err != nil {
			t.Fatalf("releaseAsset(%q, %q): %v", c.goos, c.goarch, err)
		}
		if name != c.wantName || ext != c.wantExt {
			t.Errorf("releaseAsset(%q, %q) = (%q, %q), want (%q, %q)", c.goos, c.goarch, name, ext, c.wantName, c.wantExt)
		}
	}
}

func TestExtractBinaryFromZip(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "release.zip")
	writeTestZip(t, archivePath, map[string]string{
		"syncthing-windows-amd64-v2.0.11/README.md":     "not the binary",
		"syncthing-windows-amd64-v2.0.11/syncthing.exe": "pretend binary contents",
	})

	destPath := filepath.Join(dir, "syncthing.exe")
	if err := extractBinaryFromZip(archivePath, "syncthing.exe", destPath); err != nil {
		t.Fatalf("extractBinaryFromZip: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("reading extracted binary: %v", err)
	}
	if string(got) != "pretend binary contents" {
		t.Errorf("extracted content = %q, want %q", got, "pretend binary contents")
	}
}

func TestExtractBinaryFromZipMissingEntry(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "release.zip")
	writeTestZip(t, archivePath, map[string]string{"README.md": "no binary in here"})

	err := extractBinaryFromZip(archivePath, "syncthing.exe", filepath.Join(dir, "out"))
	if err == nil {
		t.Fatal("extractBinaryFromZip: want error for missing entry, got nil")
	}
}

func TestExtractBinaryFromTarGz(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "release.tar.gz")
	writeTestTarGz(t, archivePath, map[string]string{
		"syncthing-linux-amd64-v2.0.11/README.md": "not the binary",
		"syncthing-linux-amd64-v2.0.11/syncthing": "pretend binary contents",
	})

	destPath := filepath.Join(dir, "syncthing")
	if err := extractBinaryFromTarGz(archivePath, "syncthing", destPath); err != nil {
		t.Fatalf("extractBinaryFromTarGz: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("reading extracted binary: %v", err)
	}
	if string(got) != "pretend binary contents" {
		t.Errorf("extracted content = %q, want %q", got, "pretend binary contents")
	}
}

func TestExtractBinaryFromTarGzMissingEntry(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "release.tar.gz")
	writeTestTarGz(t, archivePath, map[string]string{"README.md": "no binary in here"})

	err := extractBinaryFromTarGz(archivePath, "syncthing", filepath.Join(dir, "out"))
	if err == nil {
		t.Fatal("extractBinaryFromTarGz: want error for missing entry, got nil")
	}
}

func writeTestZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip Create(%s): %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip Write(%s): %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("writing test zip: %v", err)
	}
}

func writeTestTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0755, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar WriteHeader(%s): %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar Write(%s): %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar Close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip Close: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("writing test tar.gz: %v", err)
	}
}
