package umuruntime

import (
	"errors"
	"reflect"
	"testing"
)

type fakePlugin struct {
	name     string
	provides []Capability
	needs    []Need
}

func (f fakePlugin) Name() string           { return f.name }
func (f fakePlugin) Provides() []Capability { return f.provides }
func (f fakePlugin) Needs() []Need          { return f.needs }

func reg(name string, provides []Capability, needs ...Need) Registered {
	return With(fakePlugin{name: name, provides: provides, needs: needs}, Optional)
}

func names(rs []Registered) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Plugin.Name())
	}
	return out
}

// TestResolveOrdersProvidersFirst: 注册顺序是「消费者在前」时，拓扑序仍要把提供者排前面——
// VC++ 的安装器要显示，显示插件必须先就位。
func TestResolveOrdersProvidersFirst(t *testing.T) {
	g, err := Resolve([]Registered{
		reg("vcrt", []Capability{CapMSVCRT}, Need{Cap: CapGUI, Phase: PhaseProvision, Soft: true}),
		reg("xdisplay", []Capability{CapGUI}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(g.Order()), []string{"xdisplay", "vcrt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Order = %v, want %v", got, want)
	}
}

// TestResolveKeepsRegistrationOrderForIndependents: 互不依赖的插件保持注册顺序，
// 结果要确定——Provision 顺序、日志顺序都依赖它。
func TestResolveKeepsRegistrationOrderForIndependents(t *testing.T) {
	g, err := Resolve([]Registered{
		reg("c", nil), reg("a", nil), reg("b", nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(g.Order()), []string{"c", "a", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Order = %v, want %v", got, want)
	}
}

func TestResolveMissingHardProvider(t *testing.T) {
	_, err := Resolve([]Registered{
		reg("vcrt", []Capability{CapMSVCRT}, Need{Cap: CapGUI}),
	})
	var mp *MissingProviderError
	if !errors.As(err, &mp) || mp.Plugin != "vcrt" || mp.Cap != CapGUI {
		t.Fatalf("err = %v, want MissingProviderError{vcrt, %s}", err, CapGUI)
	}
}

// TestResolveSoftNeedMayBeUnprovided: 软依赖可以没有提供者——没装显示插件的程序照样能用
// VC++ 插件的 override 那一半。
func TestResolveSoftNeedMayBeUnprovided(t *testing.T) {
	g, err := Resolve([]Registered{
		reg("vcrt", []Capability{CapMSVCRT}, Need{Cap: CapGUI, Soft: true}),
	})
	if err != nil {
		t.Fatalf("soft need without provider rejected: %v", err)
	}
	if len(g.Providers(CapGUI)) != 0 {
		t.Error("Providers(CapGUI) should be empty")
	}
}

func TestResolveCycle(t *testing.T) {
	_, err := Resolve([]Registered{
		reg("a", []Capability{"cap.a"}, Need{Cap: "cap.b"}),
		reg("b", []Capability{"cap.b"}, Need{Cap: "cap.c"}),
		reg("c", []Capability{"cap.c"}, Need{Cap: "cap.a"}),
	})
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want CycleError", err)
	}
	if want := []string{"a", "b", "c", "a"}; !reflect.DeepEqual(ce.Path, want) {
		t.Errorf("cycle path = %v, want %v", ce.Path, want)
	}
}

// TestResolveSoftCycleIsStillACycle: 软依赖同样参与排序，所以同样不许成环——
// 否则「谁先 Provision」没有答案。
func TestResolveSoftCycleIsStillACycle(t *testing.T) {
	_, err := Resolve([]Registered{
		reg("a", []Capability{"cap.a"}, Need{Cap: "cap.b", Soft: true}),
		reg("b", []Capability{"cap.b"}, Need{Cap: "cap.a", Soft: true}),
	})
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want CycleError", err)
	}
}

func TestResolveSelfProvidedNeedIsNotACycle(t *testing.T) {
	if _, err := Resolve([]Registered{
		reg("a", []Capability{"cap.a"}, Need{Cap: "cap.a"}),
	}); err != nil {
		t.Fatalf("self-provided need rejected: %v", err)
	}
}

func TestResolveDuplicateName(t *testing.T) {
	_, err := Resolve([]Registered{reg("a", nil), reg("a", nil)})
	var de *DuplicatePluginError
	if !errors.As(err, &de) || de.Name != "a" {
		t.Fatalf("err = %v, want DuplicatePluginError{a}", err)
	}
}

func TestResolveNilPlugin(t *testing.T) {
	if _, err := Resolve([]Registered{{}}); err == nil {
		t.Fatal("nil plugin accepted")
	}
}

// TestResolveProvidersInRegistrationOrder: 同一能力多个提供者时按注册顺序尝试
// （宿主取第一个能用的），而不是按拓扑序。
func TestResolveProvidersInRegistrationOrder(t *testing.T) {
	g, err := Resolve([]Registered{
		reg("primary", []Capability{CapGUI}, Need{Cap: "cap.x"}),
		reg("fallback", []Capability{CapGUI}),
		reg("x", []Capability{"cap.x"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(g.Providers(CapGUI)), []string{"primary", "fallback"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Providers = %v, want %v", got, want)
	}
	if got, want := names(g.Order()), []string{"x", "primary", "fallback"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Order = %v, want %v", got, want)
	}
}

func TestLookup(t *testing.T) {
	g, err := Resolve([]Registered{reg("a", nil)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g.Lookup("a"); !ok {
		t.Error("Lookup(a) not found")
	}
	if _, ok := g.Lookup("zzz"); ok {
		t.Error("Lookup(zzz) found")
	}
}

func TestCapabilityUnavailableErrorIs(t *testing.T) {
	var err error = &CapabilityUnavailableError{Cap: CapGUI, Why: "no display"}
	if !errors.Is(err, ErrCapabilityUnavailable) {
		t.Error("CapabilityUnavailableError is not ErrCapabilityUnavailable")
	}
	if errors.Is(&AcquireError{Cap: CapGUI, Err: errors.New("x")}, ErrCapabilityUnavailable) {
		t.Error("AcquireError must NOT be ErrCapabilityUnavailable — it means a provider exists but failed")
	}
}
