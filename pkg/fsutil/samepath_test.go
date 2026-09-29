package fsutil

import (
	"path/filepath"
	"testing"
)

func TestSamePathFor(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		goos string
		a, b string
		want bool
	}{
		{"linux", filepath.Join(dir, "a"), filepath.Join(dir, "a") + string(filepath.Separator), true},
		{"linux", filepath.Join(dir, "a", "..", "b"), filepath.Join(dir, "b"), true},
		{"linux", filepath.Join(dir, "Config"), filepath.Join(dir, "config"), false},
		{"windows", filepath.Join(dir, "Config"), filepath.Join(dir, "config"), true},
		{"windows", filepath.Join(dir, "a"), filepath.Join(dir, "b"), false},
	}
	for _, c := range cases {
		if got := samePathFor(c.goos, c.a, c.b); got != c.want {
			t.Errorf("samePathFor(%s, %q, %q) = %v，期望 %v", c.goos, c.a, c.b, got, c.want)
		}
	}
}
