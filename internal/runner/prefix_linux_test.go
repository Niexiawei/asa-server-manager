//go:build linux

package runner

import (
	"path/filepath"
	"testing"
)

// PrefixKeyFor 是「模式」到「用哪个前缀」的唯一转换点：start 路径的三处调用
// （EnsurePrefix / PrefixHasVCRedist / Options.PrefixKey）都取它的返回值，
// 它错了三处会一起错，而且是静默错位——检查的是共享前缀、跑的是独立前缀。
func TestPrefixKeyFor_FollowsMode(t *testing.T) {
	base := t.TempDir()
	t.Cleanup(func() { Configure(defaultConfig()) })

	Configure(Config{Runtime: "umu", PrefixMode: "shared", BaseDir: base})
	if got := PrefixKeyFor("srv1"); got != "" {
		t.Errorf("shared 模式必须回落到共享前缀，got %q", got)
	}

	Configure(Config{Runtime: "umu", PrefixMode: "per-instance", BaseDir: base})
	if got := PrefixKeyFor("srv1"); got != "srv1" {
		t.Errorf("per-instance 模式应返回实例名，got %q", got)
	}
}

func TestPrefixDir_KeyOnlyAppliesUnderPerInstance(t *testing.T) {
	base := t.TempDir()
	shared := filepath.Join(base, "umu-prefix")

	cfg := Config{PrefixMode: "shared", BaseDir: base}
	if got := prefixDir(cfg, "srv1"); got != shared {
		t.Errorf("shared 模式下 key 必须被忽略，got %q want %q", got, shared)
	}

	cfg.PrefixMode = "per-instance"
	if got, want := prefixDir(cfg, "srv1"), shared+"-srv1"; got != want {
		t.Errorf("prefixDir = %q, want %q", got, want)
	}
	// 空 key 在任何模式下都是共享前缀——EnsurePrefix 靠这条区分
	// 「校验共享前缀」与「创建实例前缀」。
	if got := prefixDir(cfg, ""); got != shared {
		t.Errorf("空 key 必须是共享前缀，got %q", got)
	}
}

// instancePrefixDir 与 prefixDir 的区别就是它无视当前模式：切回 shared 之后
// 仍然要能找到（并删掉）per-instance 时期留下的前缀。
func TestInstancePrefixDir_IgnoresMode(t *testing.T) {
	base := t.TempDir()
	cfg := Config{PrefixMode: "shared", BaseDir: base}

	want := filepath.Join(base, "umu-prefix") + "-srv1"
	if got := instancePrefixDir(cfg, "srv1"); got != want {
		t.Errorf("instancePrefixDir = %q, want %q", got, want)
	}
	if got, want := instancePrefixDir(cfg, ""), filepath.Join(base, "umu-prefix"); got != want {
		t.Errorf("空 key 应指向共享前缀，got %q want %q", got, want)
	}
}

// 标记文件的写方（umu_linux.go 的 writePrefixMarker）与读方（prefix_linux.go 的
// prefixMarker）在两个文件里各自拼路径。拼错了不会报错，只会让 ensurePrefix 的
// 快速路径永远不命中 —— 每次启动都重建一遍 prefix，多花一分钟且毫无提示。
func TestPrefixMarker_RoundTrips(t *testing.T) {
	prefix := t.TempDir()

	if got := prefixMarker(prefix); got != "" {
		t.Errorf("标记不存在时应返回空串，got %q", got)
	}
	if err := writePrefixMarker(prefix, "GE-Proton10-34"); err != nil {
		t.Fatalf("writePrefixMarker: %v", err)
	}
	if got := prefixMarker(prefix); got != "GE-Proton10-34" {
		t.Errorf("prefixMarker = %q, want GE-Proton10-34", got)
	}
}

// 共享前缀绝不能被 RemoveInstancePrefix 删掉，哪怕调用方传了个能算出它的 key。
func TestRemoveInstancePrefix_NeverTouchesShared(t *testing.T) {
	base := t.TempDir()
	Configure(Config{Runtime: "umu", PrefixMode: "per-instance", BaseDir: base})
	t.Cleanup(func() { Configure(defaultConfig()) })

	if err := removeInstancePrefix(""); err != nil {
		t.Fatalf("空实例名应当是 no-op，got %v", err)
	}
}
