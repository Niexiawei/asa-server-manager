package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

// Regular files, directories and missing paths are never links. The positive
// case (a real NTFS junction) is covered by internal/mirror's Windows tests,
// which can create junctions without the symlink privilege.
func TestIsLinkFalseForNonLinks(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{dir, file, filepath.Join(dir, "missing")} {
		if IsLink(p) {
			t.Errorf("IsLink(%q) = true, want false", p)
		}
	}
}
