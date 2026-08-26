//go:build linux

package plugindata

import "testing"

func TestPathCompareKey_PreservesCaseOnLinux(t *testing.T) {
	if pathCompareKey("/a/DB") == pathCompareKey("/a/db") {
		t.Fatal("expected case-sensitive comparison key on Linux")
	}
}

func TestPathWithin_CaseSensitiveOnLinux(t *testing.T) {
	if pathWithin("/instances/foo/DB", "/instances/foo") {
		t.Fatal("expected a differently-cased child path to NOT be recognized as within root on Linux")
	}
	if !pathWithin("/instances/foo/db", "/instances/foo") {
		t.Fatal("expected an exactly-cased child path to be recognized as within root on Linux")
	}
}
