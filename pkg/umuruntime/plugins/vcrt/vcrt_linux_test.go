//go:build linux

package vcrt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"asa-server/pkg/umuruntime"
	"asa-server/pkg/vcredist"
)

// TestPluginShape: 提供 win32.msvcrt，对 win32.gui 是**软**依赖——没有显示时
// override（承重的那一半）照样要写。
func TestPluginShape(t *testing.T) {
	p := New(Config{})
	if p.Name() != Name {
		t.Errorf("Name = %q", p.Name())
	}
	if got := p.Provides(); len(got) != 1 || got[0] != umuruntime.CapMSVCRT {
		t.Errorf("Provides = %v", got)
	}
	needs := p.Needs()
	if len(needs) != 1 || needs[0].Cap != umuruntime.CapGUI || needs[0].Phase != umuruntime.PhaseProvision || !needs[0].Soft {
		t.Errorf("Needs = %+v, want a soft provision-time need on %s", needs, umuruntime.CapGUI)
	}
	if _, err := umuruntime.Resolve([]umuruntime.Registered{umuruntime.With(p, umuruntime.Optional)}); err != nil {
		t.Errorf("must resolve even with no display plugin registered: %v", err)
	}
}

// TestPendingOnlyWhenActive: 不归我们管（custom 运行时）或被关掉（install_vcredist:
// false）时，永远没有待办——否则每次启动都会去碰一个不该碰的 prefix。
func TestPendingOnlyWhenActive(t *testing.T) {
	prefix := t.TempDir() // 没有 user.reg ⇒ override 未写
	for _, tc := range []struct {
		cfg  Config
		want bool
	}{
		{Config{Managed: true, Install: true}, true},
		{Config{Managed: false, Install: true}, false},
		{Config{Managed: true, Install: false}, false},
	} {
		if got := New(tc.cfg).Pending(prefix); got != tc.want {
			t.Errorf("Pending(%+v) = %v, want %v", tc.cfg, got, tc.want)
		}
	}
}

// TestPendingFollowsOverridesNotInstall: 判据是 override 而不是「装没装原生 DLL」——
// 无头机上安装器永远装不上，拿它当判据会让每次启动都重跑一遍 regedit 容器。
func TestPendingFollowsOverridesNotInstall(t *testing.T) {
	prefix := t.TempDir()
	var reg strings.Builder
	reg.WriteString("[Software\\\\Wine\\\\DllOverrides]\n")
	for _, name := range vcredist.OverrideDLLs {
		reg.WriteString("\"*" + name + "\"=\"native,builtin\"\n")
	}
	if err := os.WriteFile(filepath.Join(prefix, "user.reg"), []byte(reg.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if !vcredist.OverridesApplied(prefix) {
		t.Skip("fixture does not match vcredist's override format; covered by pkg/vcredist")
	}
	p := New(Config{Managed: true, Install: true})
	if p.Pending(prefix) {
		t.Error("Pending with all overrides in place (the native DLLs being absent must not matter)")
	}
	if p.Satisfied(prefix).OK {
		t.Error("Satisfied without the native runtime in system32")
	}
}

// TestSatisfiedIsNeverDefinitive: PE 头判断是启发式的，不许拿它拦启动
// （docs/LINUX_COMPATIBILITY_PLAN.md §1 目标 5）。
func TestSatisfiedIsNeverDefinitive(t *testing.T) {
	r := New(Config{}).Satisfied(t.TempDir())
	if r.OK || r.Definitive || r.Detail == "" {
		t.Errorf("Satisfied(empty prefix) = %+v, want a non-definitive no with a reason", r)
	}
}

func TestProvisionInactiveIsSkipped(t *testing.T) {
	o, err := New(Config{Managed: false, Install: true}).Provision(context.Background(), &umuruntime.ProvisionContext{
		Prefix: t.TempDir(), Logf: func(string, ...any) {},
	})
	if err != nil || o.Kind != umuruntime.Skipped {
		t.Errorf("Provision(unmanaged) = (%+v, %v), want Skipped", o, err)
	}
}

// TestInspectPrefixFillsCallerFields: vcredist.Inspect 留给调用方的三个字段由插件补：
// Managed 来自配置，安装器用的显示来自只读 Probe（不许真的起 X 服务）。
func TestInspectPrefixFillsCallerFields(t *testing.T) {
	prefix := t.TempDir()
	p := New(Config{Managed: true})

	st := p.InspectPrefix(umuruntime.InspectContext{
		Prefix: prefix,
		Probe:  func(umuruntime.Capability) (bool, string) { return true, "自管 Xvfb 虚拟显示" },
	})
	info, ok := st.Data.(vcredist.Info)
	if !ok || !info.Managed || info.Prefix != prefix || info.InstallerDisplay != "自管 Xvfb 虚拟显示" || info.InstallerBlocked != "" {
		t.Errorf("InspectPrefix = %+v", st.Data)
	}
	if st.Ready {
		t.Error("Ready without the native runtime")
	}

	st = p.InspectPrefix(umuruntime.InspectContext{
		Prefix: prefix,
		Probe:  func(umuruntime.Capability) (bool, string) { return false, "本机没有 Xvfb" },
	})
	if info := st.Data.(vcredist.Info); info.InstallerBlocked != "本机没有 Xvfb" || info.InstallerDisplay != "" {
		t.Errorf("blocked InspectPrefix = %+v", info)
	}
}

// TestFingerprintFollowsPrefixState: 指纹随 Provision 对 prefix 的每一种改动而变 ——
// overlay 可写层靠它发现「底层后来装了 VC++」（docs/UMU_PREFIX_PLAN.md §8.1）。
// 格式里不能有分号与换行（它们是 .lower-stamp 的分隔符）。
func TestFingerprintFollowsPrefixState(t *testing.T) {
	prefix := t.TempDir()
	p := New(Config{})
	empty := p.Fingerprint(prefix)
	if strings.ContainsAny(empty, ";\n") {
		t.Fatalf("fingerprint %q contains a stamp separator", empty)
	}

	body := "installer=/x\nchecksum=cc0ff0eb1dc3f5188ae6300faef32bf5\n"
	if err := os.WriteFile(filepath.Join(prefix, vcredist.MarkerFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	installed := p.Fingerprint(prefix)
	if installed == empty {
		t.Error("fingerprint unchanged after an installer marker appeared")
	}
	if !strings.Contains(installed, "installer=cc0ff0eb1dc3") || strings.Contains(installed, "cc0ff0eb1dc3f") {
		t.Errorf("fingerprint %q should carry a 12-char checksum prefix", installed)
	}
	if p.Fingerprint(prefix) != installed {
		t.Error("fingerprint is not stable for an unchanged prefix")
	}
}
