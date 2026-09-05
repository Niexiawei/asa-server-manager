//go:build linux

package wineprefix

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"asa-server/pkg/umu"
)

func newManager(cfg Config) *Manager {
	return New(cfg, umu.New(umu.Config{BaseDir: cfg.BaseDir, ProtonVersion: cfg.ProtonVersion}))
}

// KeyFor 是「模式」到「用哪个前缀」的唯一转换点：调用方的三处引用
// （EnsurePrefix / VC++ 回调 / 启动时的 PrefixKey）都取它的返回值，
// 它错了三处会一起错，而且是静默错位——检查的是共享前缀、跑的是独立前缀。
func TestPrefixKeyFor_FollowsMode(t *testing.T) {
	base := t.TempDir()

	m := newManager(Config{PrefixMode: "shared", BaseDir: base})
	if got := m.KeyFor("srv1"); got != "" {
		t.Errorf("shared 模式必须回落到共享前缀，got %q", got)
	}

	m.Reconfigure(Config{PrefixMode: "per-instance", BaseDir: base})
	if got := m.KeyFor("srv1"); got != "srv1" {
		t.Errorf("per-instance 模式应返回实例名，got %q", got)
	}

	m.Reconfigure(Config{PrefixMode: "overlay", BaseDir: base})
	if got := m.KeyFor("srv1"); got != "srv1" {
		t.Errorf("overlay 模式同样按实例分前缀，got %q", got)
	}
}

// SharesPrefix 的两个后果都是「不做就会出事」型的：启动闸门与 ArkApi 互斥。
// 它错在 true 的方向只是白排队，错在 false 的方向是三分钟静默挂死加一棵孤儿进程树，
// 所以未知模式必须回落到 true。三个合法值 + 零值 + 未知值一起钉死。
func TestSharesPrefix_AllModes(t *testing.T) {
	base := t.TempDir()

	cases := []struct {
		mode string
		want bool
	}{
		{"shared", true},
		{"per-instance", false},
		{"overlay", false},
		{"", true},         // 零值配置：没配过 = 与 shared 同等对待
		{"nonsense", true}, // 未知值：Dir 也会回落到共享前缀，两者必须一致
	}
	m := newManager(Config{BaseDir: base})
	for _, c := range cases {
		m.Reconfigure(Config{PrefixMode: c.mode, BaseDir: base})
		if got := m.SharesPrefix(); got != c.want {
			t.Errorf("prefix_mode=%q: SharesPrefix() = %v, want %v", c.mode, got, c.want)
		}
	}
}

func TestPrefixDir_KeyOnlyAppliesUnderPerInstance(t *testing.T) {
	base := t.TempDir()
	shared := filepath.Join(base, "umu-prefix")

	m := newManager(Config{PrefixMode: "shared", BaseDir: base})
	if got := m.Dir("srv1"); got != shared {
		t.Errorf("shared 模式下 key 必须被忽略，got %q want %q", got, shared)
	}

	m.Reconfigure(Config{PrefixMode: "per-instance", BaseDir: base})
	if got, want := m.Dir("srv1"), shared+"-srv1"; got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}

	// overlay 交出去的是**挂载点**，不是底层：Wine 就是按这个目录的 dev/ino
	// 挑 wineserver 的，指错了就等于回到 shared，而且完全没有报错。
	m.Reconfigure(Config{PrefixMode: "overlay", BaseDir: base})
	want := filepath.Join(base, "umu-prefix-overlay", "srv1", "merged")
	if got := m.Dir("srv1"); got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}

	// PrefixDir 只搬底层，可写层永远在 {BaseDir}/umu-prefix-overlay/ 下。
	m.Reconfigure(Config{PrefixMode: "overlay", BaseDir: base, PrefixDir: filepath.Join(base, "elsewhere", "wine")})
	if got := m.Dir(""); got != filepath.Join(base, "elsewhere", "wine") {
		t.Errorf("底层应跟随 PrefixDir，got %q", got)
	}
	if got := m.Dir("srv1"); got != want {
		t.Errorf("可写层不该跟随 PrefixDir，got %q want %q", got, want)
	}

	m.Reconfigure(Config{PrefixMode: "overlay", BaseDir: base})
	// 空 key 在任何模式下都是共享前缀——EnsurePrefix 靠这条区分
	// 「校验共享前缀」与「创建实例前缀」。
	if got := m.Dir(""); got != shared {
		t.Errorf("空 key 必须是共享前缀，got %q", got)
	}
}

// instanceDir 与 Dir 的区别就是它无视当前模式：切回 shared 之后仍然要能找到
// （并删掉）per-instance 时期留下的前缀。
func TestInstanceDir_IgnoresMode(t *testing.T) {
	base := t.TempDir()
	m := newManager(Config{PrefixMode: "shared", BaseDir: base})
	cfg := Config{PrefixMode: "shared", BaseDir: base}

	want := filepath.Join(base, "umu-prefix") + "-srv1"
	if got := m.instanceDir(cfg, "srv1"); got != want {
		t.Errorf("instanceDir = %q, want %q", got, want)
	}
	if got, want := m.instanceDir(cfg, ""), filepath.Join(base, "umu-prefix"); got != want {
		t.Errorf("空 key 应指向共享前缀，got %q want %q", got, want)
	}
}

// 共享前缀绝不能被 Remove 删掉，哪怕调用方传了个能算出它的 key。
func TestRemove_NeverTouchesShared(t *testing.T) {
	base := t.TempDir()
	m := newManager(Config{PrefixMode: "per-instance", BaseDir: base})

	if err := m.Remove(""); err != nil {
		t.Fatalf("空 key 应当是 no-op，got %v", err)
	}
}

// overlay 的三个目录一个都不能进属主对账的清单，除非 merged 是降级复制形态。
//
// 这条是真机回归：第一次上真机时 work 在清单里，属主漂移抽样走进内核自己建的
// work/work（root:root，mode 000），把启动挡在「重启会自动 chown 修复」上——
// 而那句话永远不可能修好它。upper 与挂载中的 merged 各有各的理由。
func TestUnmountedOverlayDirs_NeverListsUpperOrWork(t *testing.T) {
	base := t.TempDir()
	m := newManager(Config{PrefixMode: "overlay", BaseDir: base})
	cfg := Config{PrefixMode: "overlay", BaseDir: base}

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

	got := m.UnmountedOverlayDirs()
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
func TestStatus_CurrentFollowsMode(t *testing.T) {
	base := t.TempDir()

	shared := filepath.Join(base, "umu-prefix")
	perInst := shared + "-srv1"
	overlayMerged := filepath.Join(base, "umu-prefix-overlay", "srv1", "merged")
	backup := shared + ".bak-GE-Proton10-30"

	for _, d := range []string{shared, perInst, overlayMerged, backup} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	m := newManager(Config{BaseDir: base})
	current := func(t *testing.T, mode string) map[string]bool {
		t.Helper()
		m.Reconfigure(Config{PrefixMode: mode, BaseDir: base})
		out := map[string]bool{}
		for _, p := range m.Status() {
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
	// 调用方靠这个前缀决定用 RemoveAll 而不是 Remove，认错了就会去删一个
	// 不存在的路径然后报「完成」。
	m.Reconfigure(Config{PrefixMode: "shared", BaseDir: base})
	var bak *Info
	for _, p := range m.Status() {
		p := p
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
