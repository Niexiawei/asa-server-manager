package config

import "testing"

func TestValidateInstanceName(t *testing.T) {
	for _, n := range []string{"srv", "a:b", "CON", "foo."} { // 已存在的实例照常可管理
		if err := ValidateInstanceName(n); err != nil {
			t.Errorf("ValidateInstanceName(%q) = %v, want nil", n, err)
		}
	}
	for _, n := range []string{"", ".", "..", "a/b", `a\b`, "a..b", "a\x00b"} {
		if err := ValidateInstanceName(n); err == nil {
			t.Errorf("ValidateInstanceName(%q) = nil, want error", n)
		}
	}
}

// 新起的名字还必须在两个平台上都建得出来。
func TestValidateNewInstanceName(t *testing.T) {
	if err := ValidateNewInstanceName("Island-PVE"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{".", "a:b", "CON", "nul.txt", "foo.", " foo", "a|b"} {
		if err := ValidateNewInstanceName(n); err == nil {
			t.Errorf("ValidateNewInstanceName(%q) = nil, want error", n)
		}
	}
}
