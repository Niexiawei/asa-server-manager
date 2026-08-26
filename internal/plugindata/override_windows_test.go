//go:build windows

package plugindata

import "testing"

func TestPathCompareKey_FoldsCaseOnWindows(t *testing.T) {
	if pathCompareKey(`C:\a\DB`) != pathCompareKey(`C:\a\db`) {
		t.Fatal("expected case-insensitive comparison key on Windows")
	}
}

func TestPathWithin_CaseInsensitiveOnWindows(t *testing.T) {
	if !pathWithin(`C:\Instances\Foo\DB`, `C:\instances\foo`) {
		t.Fatal("expected a differently-cased child path to be recognized as within root on Windows")
	}
}
