package actions

import (
	"testing"

	"asa-server/internal/runner"
)

// gc 的删除判据是这套命令里唯一有可能造成数据损失的地方（虽然前缀里没有存档，
// 但删掉一个在用实例的前缀等于让它下次启动多花一分钟重建，删掉共享前缀则等于
// 让所有实例都要重跑 setup）。所以这里逐条钉死"什么不能删"。
func TestGCCandidates(t *testing.T) {
	instances := map[string]bool{"alive": true, "bak-GE-Proton10-1": true}

	prefixes := []runner.PrefixInfo{
		{Key: "", Path: "/b/umu-prefix"}, // 共享，永不回收
		// 判据是「**当前模式**还会不会用到这个目录」（Current），不是「实例还在不在」：
		// 换过 prefix_mode 的机器上，上一个模式给仍然存在的实例留下的目录才是最大的
		// 一类垃圾，按「实例还在」判会永远漏掉它们。
		{Key: "alive", Path: "/b/umu-prefix-overlay/alive/merged", Current: true, Overlay: true},
		{Key: "alive", Path: "/b/umu-prefix-alive"},                  // 旧模式残留 → 回收
		{Key: "gone", Path: "/b/umu-prefix-gone"},                    // 孤儿 → 回收
		{Key: "running", Path: "/b/umu-prefix-running", InUse: true}, // 正在用
		{Key: "bak-GE-Proton10-1", Path: "/b/umu-prefix.bak-GE-Proton10-1"},
	}

	// 同一个 Key 会出现两行（当前模式的可写层 + 旧模式的残留前缀），所以按
	// **路径**记结果：按 Key 记会让「回收残留」和「保留在用的」互相覆盖。
	got := map[string]bool{}
	for _, p := range gcCandidates(prefixes, instances) {
		got[p.Path] = true
	}

	if !got["/b/umu-prefix-gone"] {
		t.Error("孤儿前缀应当被回收")
	}
	if !got["/b/umu-prefix-alive"] {
		t.Error("换模式后残留的前缀应当被回收，哪怕它的实例还在")
	}
	// 备份目录不属于任何实例，即使碰巧有个同名实例也一样回收——名字里的
	// "bak-" 前缀来自 reconcilePrefixVersion，不是用户能取到的实例名。
	if !got["/b/umu-prefix.bak-GE-Proton10-1"] {
		t.Error("旧版本备份目录应当被回收")
	}
	if got["/b/umu-prefix"] {
		t.Error("共享前缀绝不能被回收")
	}
	if got["/b/umu-prefix-overlay/alive/merged"] {
		t.Error("当前模式正在使用、且实例仍存在的前缀不能被回收")
	}
	if got["/b/umu-prefix-running"] {
		t.Error("wineserver 占用中的前缀不能被回收")
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1536, "1.5 KiB"},
		{5 * 1024 * 1024, "5.0 MiB"},
		{3 * 1024 * 1024 * 1024, "3.0 GiB"},
	}
	for _, c := range cases {
		if got := humanSize(c.in); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
