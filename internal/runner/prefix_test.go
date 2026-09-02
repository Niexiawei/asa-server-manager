package runner

import "testing"

// 这三种前缀是同一个名字词干下的兄弟目录，所以「谁持有哪个前缀」只能按路径边界
// 比，不能按字符串前缀比。表里前两行是真实场景（umu 导出的是 "<prefix>/pfx/"），
// 后面几行是 2026-09-01 之前会误判成 true 的那些。
// 见 docs/UMU_PREFIX_OVERLAY_PLAN.md §12.2。
func TestWineprefixValueUnder(t *testing.T) {
	const (
		shared   = "/opt/asa/umu-prefix"
		perInst  = "/opt/asa/umu-prefix-jibian"
		overlayA = "/opt/asa/umu-prefix-overlay/A/merged"
	)

	cases := []struct {
		name   string
		value  string // 某个活着的 wineserver 的 WINEPREFIX
		prefix string // 在问的那个前缀目录
		want   bool
	}{
		{"共享前缀自己在跑", shared + "/pfx/", shared, true},
		{"每实例前缀自己在跑", perInst + "/pfx/", perInst, true},
		{"值就是前缀目录本身", shared, shared, true},
		{"前缀带尾斜杠", shared + "/pfx/", shared + "/", true},

		{"每实例前缀不算共享前缀在跑", perInst + "/pfx/", shared, false},
		{"overlay 挂载点不算底层在跑", overlayA + "/pfx/", shared, false},
		{"名字是另一个前缀的字符串前缀", "/opt/asa/umu-prefix-AB/pfx/", "/opt/asa/umu-prefix-A", false},
		{"反过来也不成立", shared + "/pfx/", perInst, false},

		{"空值", "", shared, false},
		{"空前缀由调用方处理，这里一律不算", shared + "/pfx/", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wineprefixValueUnder(c.value, c.prefix); got != c.want {
				t.Errorf("wineprefixValueUnder(%q, %q) = %v, want %v", c.value, c.prefix, got, c.want)
			}
		})
	}
}
