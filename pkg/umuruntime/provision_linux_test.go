//go:build linux

package umuruntime

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"asa-server/pkg/umu"
)

// fakeProv is a PrefixProvisioner that records calls into a shared log.
type fakeProv struct {
	fakePlugin
	log       *[]string
	pending   bool
	satisfied Readiness
	outcome   Outcome
	err       error
	gotCtx    *ProvisionContext
}

func (f *fakeProv) Pending(string) bool        { return f.pending }
func (f *fakeProv) Satisfied(string) Readiness { return f.satisfied }
func (f *fakeProv) InspectPrefix(ic InspectContext) Status {
	ok, detail := ic.Probe(CapGUI)
	return Status{Ready: ok, Detail: detail, Data: ic}
}
func (f *fakeProv) Provision(_ context.Context, pc *ProvisionContext) (Outcome, error) {
	*f.log = append(*f.log, f.name)
	f.gotCtx = pc
	return f.outcome, f.err
}

func newProv(log *[]string, name string, cap Capability, needs ...Need) *fakeProv {
	return &fakeProv{fakePlugin: fakePlugin{name: name, provides: []Capability{cap}, needs: needs}, log: log}
}

// probeNo is a display provider whose Probe always says no.
type probeNo struct{ *fakeEnv }

func (p *probeNo) Probe() (bool, string) { return false, "无显示" }

func hostWith(t *testing.T, cfg Config, regs ...Registered) *Host {
	t.Helper()
	h, err := New(cfg, regs...)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func collect(outs *[]Outcome) func(Outcome, func(string, ...any)) {
	return func(o Outcome, _ func(string, ...any)) { *outs = append(*outs, o) }
}

func noLog(string, ...any) {}

// TestProvisionProvidersFirstAndReportsOutcomes: 提供者先装；每个结果都交给 OnOutcome，
// 且带上插件名与 prefix 键。
func TestProvisionProvidersFirstAndReportsOutcomes(t *testing.T) {
	var log []string
	var outs []Outcome
	consumer := newProv(&log, "consumer", "cap.b", Need{Cap: "cap.a", Phase: PhaseProvision, Soft: true})
	provider := newProv(&log, "provider", "cap.a")
	provider.outcome = Outcome{Kind: AlreadySatisfied}
	cfg := Config{Umu: umu.Config{BaseDir: t.TempDir()}, OnOutcome: collect(&outs)}
	h := hostWith(t, cfg, With(consumer, Optional), With(provider, Optional))

	if err := h.provision(context.Background(), "inst", "/p", noLog, false, nil); err != nil {
		t.Fatal(err)
	}
	if want := []string{"provider", "consumer"}; !reflect.DeepEqual(log, want) {
		t.Errorf("provision order = %v, want %v", log, want)
	}
	if len(outs) != 2 || outs[0].Plugin != "provider" || outs[0].Key != "inst" || outs[0].Kind != AlreadySatisfied {
		t.Errorf("outcomes = %+v", outs)
	}
	pc := consumer.gotCtx
	if pc == nil || pc.Prefix != "/p" || pc.Key != "inst" || pc.Umu != h.Umu() || pc.Acquire == nil || pc.Logf == nil {
		t.Errorf("ProvisionContext = %+v", pc)
	}
}

// TestImplicitOptionalFailureGoesToOnOutcome: 可选插件在 Ensure/EnsurePrefix 里失败，
// 不许让操作失败，但必须经 OnOutcome 响亮地报出来。
func TestImplicitOptionalFailureGoesToOnOutcome(t *testing.T) {
	var log []string
	var outs []Outcome
	boom := errors.New("boom")
	p := newProv(&log, "vc", CapMSVCRT)
	p.err = boom
	h := hostWith(t, Config{OnOutcome: collect(&outs)}, With(p, Optional))

	if err := h.provision(context.Background(), "", "/p", noLog, false, nil); err != nil {
		t.Fatalf("optional failure failed the operation: %v", err)
	}
	if len(outs) != 1 || outs[0].Kind != Failed || !errors.Is(outs[0].Cause, boom) {
		t.Errorf("outcomes = %+v, want one Failed carrying the cause", outs)
	}
}

func TestImplicitRequiredFailureFails(t *testing.T) {
	var log []string
	boom := errors.New("boom")
	p := newProv(&log, "vc", CapMSVCRT)
	p.err = boom
	h := hostWith(t, Config{}, With(p, Required))

	if err := h.provision(context.Background(), "", "/p", noLog, false, nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the required plugin's failure", err)
	}
}

// TestExplicitProvisionReturnsEveryFailure: 显式的「现在就装」请求（verify-arkapi
// --install-vcredist）不管插件是否可选都要把失败交回去——用户问的就是这件事。
// 同一个失败不许再经 OnOutcome 报第二遍。
func TestExplicitProvisionReturnsEveryFailure(t *testing.T) {
	var log []string
	var outs []Outcome
	boom := errors.New("boom")
	p := newProv(&log, "vc", CapMSVCRT)
	p.err = boom
	h := hostWith(t, Config{Umu: umu.Config{BaseDir: t.TempDir()}, OnOutcome: collect(&outs)}, With(p, Optional))

	if err := h.Provision(context.Background(), "", nil, CapMSVCRT); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if len(outs) != 0 {
		t.Errorf("failure also reported through OnOutcome: %+v", outs)
	}
}

func TestProvisionFiltersByCapability(t *testing.T) {
	var log []string
	a := newProv(&log, "a", "cap.a")
	b := newProv(&log, "b", "cap.b")
	h := hostWith(t, Config{Umu: umu.Config{BaseDir: t.TempDir()}}, With(a, Optional), With(b, Optional))

	if err := h.Provision(context.Background(), "", nil, "cap.b"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"b"}; !reflect.DeepEqual(log, want) {
		t.Errorf("provisioned %v, want %v", log, want)
	}
}

// TestCustomRuntimeNeverProvisions: custom 运行时的 prefix 是运维自己搭的，
// 任何插件都不许往里写。
func TestCustomRuntimeNeverProvisions(t *testing.T) {
	var log []string
	p := newProv(&log, "vc", CapMSVCRT)
	p.pending = true
	h := hostWith(t, Config{Runtime: "custom"}, With(p, Optional))

	if err := h.Provision(context.Background(), "", nil); err != nil {
		t.Fatal(err)
	}
	if len(log) != 0 {
		t.Errorf("provisioned under a custom runtime: %v", log)
	}
	if cfg := h.prefixConfig(h.config()); cfg.Provision != nil || cfg.Pending != nil {
		t.Error("wineprefix hooks installed under a custom runtime")
	}
}

// TestHardProvisionNeedUnavailableSkips: 硬依赖拿不到时插件被跳过（不是失败），
// 原因是类型化的「能力不可用」，带着提供者自己的说法。
func TestHardProvisionNeedUnavailableSkips(t *testing.T) {
	var log []string
	var outs []Outcome
	gui := &probeNo{fakeEnv: newFakeEnv("xd", CapGUI, "", nil)}
	p := newProv(&log, "needs-gui", "cap.x", Need{Cap: CapGUI, Phase: PhaseProvision})
	h := hostWith(t, Config{OnOutcome: collect(&outs)}, With(gui, Optional), With(p, Optional))

	if err := h.provision(context.Background(), "", "/p", noLog, false, nil); err != nil {
		t.Fatal(err)
	}
	if len(log) != 0 {
		t.Error("plugin ran although its hard dependency is unavailable")
	}
	var ue *CapabilityUnavailableError
	if len(outs) != 1 || outs[0].Kind != Skipped || !errors.As(outs[0].Cause, &ue) || ue.Why != "无显示" {
		t.Errorf("outcomes = %+v, want one Skipped with the provider's reason", outs)
	}
}

func TestPendingHook(t *testing.T) {
	var log []string
	a := newProv(&log, "a", "cap.a")
	b := newProv(&log, "b", "cap.b")
	h := hostWith(t, Config{}, With(a, Optional), With(b, Optional))
	if h.pending("/p") {
		t.Error("pending with nothing pending")
	}
	b.pending = true
	if !h.pending("/p") {
		t.Error("not pending although one provisioner is")
	}
	if cfg := h.prefixConfig(h.config()); cfg.Pending == nil || cfg.Provision == nil {
		t.Error("wineprefix hooks missing under a managed runtime")
	}
}

// TestCheckNeeds: 阻断还是告警由 Readiness.Definitive 决定——显示拿不到是事实，
// VC++ 是启发式；没有提供者也是事实。
func TestCheckNeeds(t *testing.T) {
	var log []string
	gui := &probeNo{fakeEnv: newFakeEnv("xd", CapGUI, "", nil)}
	vc := newProv(&log, "vc", CapMSVCRT)
	vc.satisfied = Readiness{OK: false, Definitive: false, Detail: "不是原生"}
	h := hostWith(t, Config{Umu: umu.Config{BaseDir: t.TempDir()}}, With(gui, Optional), With(vc, Optional))

	got := h.CheckNeeds("", []Capability{CapGUI, CapMSVCRT, "cap.nobody", CapGUI})
	if len(got) != 3 {
		t.Fatalf("CheckNeeds = %+v, want 3 unmet", got)
	}
	if u := got[0]; u.Cap != CapGUI || u.Plugin != "xd" || !u.Readiness.Definitive || u.Readiness.Detail != "无显示" {
		t.Errorf("gui unmet = %+v", u)
	}
	if u := got[1]; u.Cap != CapMSVCRT || u.Readiness.Definitive || u.Readiness.Detail != "不是原生" {
		t.Errorf("msvcrt unmet = %+v", u)
	}
	if u := got[2]; u.Cap != "cap.nobody" || !u.Readiness.Definitive || u.Plugin != "" {
		t.Errorf("unprovided unmet = %+v", u)
	}
	if gui.acquired != 0 {
		t.Error("CheckNeeds acquired a display")
	}

	vc.satisfied.OK = true
	if got := h.CheckNeeds("", []Capability{CapMSVCRT}); len(got) != 0 {
		t.Errorf("satisfied capability reported unmet: %+v", got)
	}
}

// TestCapabilityStatusInspectsPrefix: 诊断按 prefix 与 exe 目录问 PrefixInspector，
// 并给它一个只读的 Probe。
func TestCapabilityStatusInspectsPrefix(t *testing.T) {
	var log []string
	gui := newFakeEnv("xd", CapGUI, "", nil)
	vc := newProv(&log, "vc", CapMSVCRT)
	h := hostWith(t, Config{Umu: umu.Config{BaseDir: t.TempDir()}}, With(gui, Optional), With(vc, Optional))

	st := h.CapabilityStatus(CapMSVCRT, "inst", "/game")
	if len(st) != 1 || st[0].Name != "vc" {
		t.Fatalf("CapabilityStatus = %+v", st)
	}
	ic, ok := st[0].Data.(InspectContext)
	if !ok || ic.Prefix != h.Prefixes().Dir("inst") || ic.ExeDir != "/game" {
		t.Errorf("inspect context = %+v", st[0].Data)
	}
	if !st[0].Ready || st[0].Detail != "probe" {
		t.Errorf("Probe through the context = (%v, %q), want the display plugin's answer", st[0].Ready, st[0].Detail)
	}
	if gui.acquired != 0 {
		t.Error("CapabilityStatus acquired a display")
	}
	if len(h.CapabilityStatus("cap.nobody", "", "")) != 0 {
		t.Error("status for an unprovided capability")
	}
}
