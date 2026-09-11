//go:build linux

package plugindata

import "testing"

func TestPathCompareKey_PreservesCaseOnLinux(t *testing.T) {
	if pathCompareKey("/a/DB") == pathCompareKey("/a/db") {
		t.Fatal("expected case-sensitive comparison key on Linux")
	}
}

func TestPathWithin_CaseSensitiveOnLinux(t *testing.T) {
	// 大小写不同的必须是根目录那一段：/instances/foo/DB 本来就在 /instances/foo 之内，
	// 与大小写无关（此前这里写的就是它，断言本身是错的，Linux 上恒失败）。
	if pathWithin("/instances/FOO/db", "/instances/foo") {
		t.Fatal("expected a path under a differently-cased root to NOT be recognized as within root on Linux")
	}
	if !pathWithin("/instances/foo/db", "/instances/foo") {
		t.Fatal("expected an exactly-cased child path to be recognized as within root on Linux")
	}
}
