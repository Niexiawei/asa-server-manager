package fsutil

import (
	"strings"
	"testing"
)

func TestValidPortableName(t *testing.T) {
	ok := []string{"srv", "Island-PVE_2", "TheIsland.v2", "服务器一", "a b", "COM10", "CONSOLE", "nul-ish", "LPT0"}
	for _, n := range ok {
		if err := ValidPortableName(n); err != nil {
			t.Errorf("ValidPortableName(%q) = %v, want nil", n, err)
		}
	}
	bad := []string{
		"", ".", "..", "foo.", "foo ", " foo",
		"CON", "con", "Nul.txt", "AUX .log", "com1", "LPT9.dat", "COM¹",
		"a:b", "a<b", `a"b`, "a|b", "a?b", "a*b", "a/b", `a\b`,
		"a\x00b", "a\tb", "a\x7fb",
		strings.Repeat("x", maxPortableNameLen+1),
	}
	for _, n := range bad {
		if err := ValidPortableName(n); err == nil {
			t.Errorf("ValidPortableName(%q) = nil, want error", n)
		}
	}
	if err := ValidPortableName(strings.Repeat("服", maxPortableNameLen)); err != nil {
		t.Errorf("上限按字符数而不是字节数计: %v", err)
	}
}
