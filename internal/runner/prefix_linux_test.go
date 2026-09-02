//go:build linux

package runner

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

	Configure(Config{Runtime: "umu", PrefixMode: "overlay", BaseDir: base})
	if got := PrefixKeyFor("srv1"); got != "srv1" {
		t.Errorf("overlay 模式同样按实例分前缀，got %q", got)
	}
}

// SharesWinePrefix 的两个后果都是「不做就会出事」型的：启动闸门与 ArkApi 互斥。
// 它错在 true 的方向只是白排队，错在 false 的方向是三分钟静默挂死加一棵孤儿进程树，
// 所以未知模式必须回落到 true。三个合法值 + 零值 + 未知值一起钉死。
func TestSharesWinePrefix_AllModes(t *testing.T) {
	base := t.TempDir()
	t.Cleanup(func() { Configure(defaultConfig()) })

	cases := []struct {
		mode string
		want bool
	}{
		{"shared", true},
		{"per-instance", false},
		{"overlay", false},
		{"", true},         // 零值配置：没配过 = 与 shared 同等对待
		{"nonsense", true}, // 未知值：prefixDir 也会回落到共享前缀，两者必须一致
	}
	for _, c := range cases {
		Configure(Config{Runtime: "umu", PrefixMode: c.mode, BaseDir: base})
		if got := SharesWinePrefix(); got != c.want {
			t.Errorf("prefix_mode=%q: SharesWinePrefix() = %v, want %v", c.mode, got, c.want)
		}
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

	// overlay 交出去的是**挂载点**，不是底层：Wine 就是按这个目录的 dev/ino
	// 挑 wineserver 的，指错了就等于回到 shared，而且完全没有报错。
	cfg.PrefixMode = "overlay"
	want := filepath.Join(base, "umu-prefix-overlay", "srv1", "merged")
	if got := prefixDir(cfg, "srv1"); got != want {
		t.Errorf("prefixDir = %q, want %q", got, want)
	}

	// prefix_dir 只搬底层，可写层永远在 {BaseDir}/umu-prefix-overlay/ 下。
	cfg.PrefixDir = filepath.Join(base, "elsewhere", "wine")
	if got := prefixDir(cfg, ""); got != cfg.PrefixDir {
		t.Errorf("底层应跟随 prefix_dir，got %q", got)
	}
	if got := prefixDir(cfg, "srv1"); got != want {
		t.Errorf("可写层不该跟随 prefix_dir，got %q want %q", got, want)
	}
	cfg.PrefixDir = ""
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

// overlay 的三个目录一个都不能进属主对账的清单，除非 merged 是降级复制形态。
//
// 这条是真机回归：第一次上真机时 work 在清单里，属主漂移抽样走进内核自己建的
// work/work（root:root，mode 000），把启动挡在「重启 asa-server 会自动 chown 修复」
// 上 —— 而那句话永远不可能修好它。upper 与挂载中的 merged 各有各的理由，
// 都写在 overlayRWSubtrees 的注释里。
func TestOverlayRWSubtrees_NeverListsUpperOrWork(t *testing.T) {
	base := t.TempDir()
	t.Cleanup(func() { Configure(defaultConfig()) })
	Configure(Config{Runtime: "umu", PrefixMode: "overlay", BaseDir: base})
	cfg := getConfig()

	for _, key := range []string{"alpha", "beta"} {
		for _, d := range []string{overlayUpperDir(cfg, key), overlayWorkDir(cfg, key), overlayMergedDir(cfg, key)} {
			if err := os.MkdirAll(d, 0755); err != nil {
				t.Fatal(err)
			}
		}
		// 内核在挂载时建的那个：root 所有、mode 000、userspace 不该碰。
		if err := os.MkdirAll(filepath.Join(overlayWorkDir(cfg, key), "work"), 0); err != nil {
			t.Fatal(err)
		}
	}

	got := overlayRWSubtrees(cfg)
	for _, p := range got {
		if base := filepath.Base(p); base == "work" || base == "upper" {
			t.Errorf("upper/work 不得出现在属主对账清单里，got %q", p)
		}
	}

	// 没挂载的 merged 就是降级复制形态：里面是真文件，必须对账。
	for _, key := range []string{"alpha", "beta"} {
		want := overlayMergedDir(cfg, key)
		if !slices.Contains(got, want) {
			t.Errorf("未挂载的 merged 应当在清单里：%q（got %v）", want, got)
		}
	}
}

// Current 是 gc 的判据：换过模式之后，上一个模式给**仍然存在的实例**留下的目录
// 必须能被认出来。真机上这就是两个各 690 MiB、永远不会再被打开、而旧判据
// （"实例还在就跳过"）永远不肯回收的前缀。
func TestPrefixStatus_CurrentFollowsMode(t *testing.T) {
	base := t.TempDir()
	t.Cleanup(func() { Configure(defaultConfig()) })

	shared := filepath.Join(base, "umu-prefix")
	perInst := shared + "-srv1"
	overlayMerged := filepath.Join(base, "umu-prefix-overlay", "srv1", "merged")
	backup := shared + ".bak-GE-Proton10-30"

	for _, d := range []string{shared, perInst, overlayMerged, backup} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	current := func(t *testing.T, mode string) map[string]bool {
		t.Helper()
		Configure(Config{Runtime: "umu", PrefixMode: mode, BaseDir: base})
		out := map[string]bool{}
		for _, p := range prefixStatus() {
			out[p.Path] = p.Current
		}
		return out
	}

	// 共享底层在任何模式下都是"当前用得到"的：overlay 模式下它还是所有可写层的底层。
	for _, mode := range []string{"shared", "per-instance", "overlay"} {
		if got := current(t, mode); !got[shared] {
			t.Errorf("prefix_mode=%s: 共享前缀必须是 Current", mode)
		}
	}

	got := current(t, "overlay")
	if got[perInst] {
		t.Error("overlay 模式下 per-instance 目录是旧模式残留，不该是 Current")
	}
	if !got[overlayMerged] {
		t.Error("overlay 模式下可写层必须是 Current")
	}

	got = current(t, "per-instance")
	if !got[perInst] {
		t.Error("per-instance 模式下 umu-prefix-<实例名> 必须是 Current")
	}
	if got[overlayMerged] {
		t.Error("per-instance 模式下可写层是旧模式残留，不该是 Current")
	}

	// 版本备份任何模式下都不是 Current，且它的 Key 必须以 "bak-" 开头 ——
	// 调用方靠这个前缀决定用 RemoveAll 而不是 RemoveInstancePrefix，认错了
	// 就会去删一个不存在的路径然后报「完成」。
	Configure(Config{Runtime: "umu", PrefixMode: "shared", BaseDir: base})
	var bak *PrefixInfo
	for _, p := range prefixStatus() {
		if p.Path == backup {
			bak = &p
		}
	}
	if bak == nil {
		t.Fatal("版本备份目录没有出现在 prefix status 里")
	}
	if bak.Current {
		t.Error("版本备份不该是 Current")
	}
	if !strings.HasPrefix(bak.Key, "bak-") {
		t.Errorf("版本备份的 Key = %q，应以 \"bak-\" 开头", bak.Key)
	}
}
