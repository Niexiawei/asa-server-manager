//go:build linux

package umuruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"

	"asa-server/pkg/problem"
	"asa-server/pkg/umu"
	"asa-server/pkg/wineprefix"
)

// fakeLease sets one environment variable.
type fakeLease struct {
	kv       string
	released *int
}

func (l fakeLease) Apply(env []string) []string { return append(append([]string{}, env...), l.kv) }
func (l fakeLease) Describe() string            { return l.kv }
func (l fakeLease) Release() {
	if l.released != nil {
		*l.released++
	}
}

// fakeEnv is an EnvProvider that records every call.
type fakeEnv struct {
	fakePlugin
	kv       string
	err      error // returned by Acquire
	acquired int
	released int
	closed   *[]string
}

func (f *fakeEnv) Probe() (bool, string) { return f.err == nil, "probe" }
func (f *fakeEnv) Acquire(context.Context) (Lease, error) {
	f.acquired++
	if f.err != nil {
		return nil, f.err
	}
	return fakeLease{kv: f.kv, released: &f.released}, nil
}
func (f *fakeEnv) Preflight() []problem.Problem {
	return []problem.Problem{{Name: f.name}}
}
func (f *fakeEnv) Report() Status { return Status{Ready: f.err == nil, Detail: f.name} }
func (f *fakeEnv) Close() {
	if f.closed != nil {
		*f.closed = append(*f.closed, f.name)
	}
}

func newFakeEnv(name string, cap Capability, kv string, err error, needs ...Need) *fakeEnv {
	return &fakeEnv{fakePlugin: fakePlugin{name: name, provides: []Capability{cap}, needs: needs}, kv: kv, err: err}
}

func mustHost(t *testing.T, cfg Config, plugins ...Plugin) *Host {
	t.Helper()
	var regs []Registered
	for _, p := range plugins {
		regs = append(regs, With(p, Optional))
	}
	h, err := New(cfg, regs...)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// readyBase lays out the three files Check looks for and returns a Config
// pointing at them.
func readyBase(t *testing.T) Config {
	t.Helper()
	base := t.TempDir()
	for _, p := range []string{
		filepath.Join(base, "umu-launcher", "umu-run"),
		filepath.Join(base, "proton", "GE-Proton10-34", "proton"),
		filepath.Join(base, "umu-prefix", "system.reg"),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return Config{
		Runtime: "umu",
		Umu:     umu.Config{BaseDir: base, ProtonVersion: "GE-Proton10-34", GameID: "umu-default"},
		Prefix:  wineprefix.Config{BaseDir: base, ProtonVersion: "GE-Proton10-34"},
	}
}

func TestCheckReportsNotReadyTyped(t *testing.T) {
	h := mustHost(t, Config{Umu: umu.Config{BaseDir: t.TempDir(), ProtonVersion: "GE-Proton10-34"}})
	err := h.Check()
	var nr *NotReadyError
	if !errors.As(err, &nr) || nr.Component != "umu-run" {
		t.Fatalf("Check() = %v, want *NotReadyError for umu-run", err)
	}
	// 修法由调用方说，本包不许替它说。
	if strings.Contains(err.Error(), "asa-server") {
		t.Errorf("Check() error names the application: %q", err)
	}
}

func TestCheckPassesWhenReady(t *testing.T) {
	if err := mustHost(t, readyBase(t)).Check(); err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
}

// TestAcquireFallsThroughUnavailable: 第一个提供者「这台机器给不了」时换下一个。
func TestAcquireFallsThroughUnavailable(t *testing.T) {
	first := newFakeEnv("first", CapGUI, "DISPLAY=:1",
		&CapabilityUnavailableError{Cap: CapGUI, Why: "没有显示"})
	second := newFakeEnv("second", CapGUI, "DISPLAY=:2", nil)
	h := mustHost(t, Config{}, first, second)

	lease, err := h.Acquire(context.Background(), CapGUI)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Describe() != "DISPLAY=:2" {
		t.Errorf("lease = %q, want the second provider's", lease.Describe())
	}
}

// TestAcquireStopsOnRealFailure: 提供者存在但这次失败了，不许悄悄换下一个——
// 那会把「为什么没用上它」藏起来。
func TestAcquireStopsOnRealFailure(t *testing.T) {
	boom := errors.New("Xvfb 起不来")
	first := newFakeEnv("first", CapGUI, "DISPLAY=:1", boom)
	second := newFakeEnv("second", CapGUI, "DISPLAY=:2", nil)
	h := mustHost(t, Config{}, first, second)

	_, err := h.Acquire(context.Background(), CapGUI)
	var ae *AcquireError
	if !errors.As(err, &ae) || ae.Plugin != "first" || !errors.Is(err, boom) {
		t.Fatalf("err = %v, want AcquireError from first wrapping %v", err, boom)
	}
	if errors.Is(err, ErrCapabilityUnavailable) {
		t.Error("a failed acquisition must not look like an unavailable capability")
	}
	if second.acquired != 0 {
		t.Error("second provider was tried after a real failure")
	}
}

func TestAcquireAllUnavailableKeepsFirstReason(t *testing.T) {
	first := newFakeEnv("first", CapGUI, "", &CapabilityUnavailableError{Cap: CapGUI, Why: "原因一"})
	second := newFakeEnv("second", CapGUI, "", &CapabilityUnavailableError{Cap: CapGUI, Why: "原因二"})
	h := mustHost(t, Config{}, first, second)

	_, err := h.Acquire(context.Background(), CapGUI)
	var ue *CapabilityUnavailableError
	if !errors.As(err, &ue) || ue.Why != "原因一" {
		t.Fatalf("err = %v, want the first provider's reason", err)
	}
}

func TestAcquireNoProvider(t *testing.T) {
	_, err := mustHost(t, Config{}).Acquire(context.Background(), CapGUI)
	if !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("err = %v, want ErrCapabilityUnavailable", err)
	}
}

// TestStatusAndPreflightAcquireNothing: 诊断视图不许有副作用——被状态接口问一句
// 就拉起一个 X 服务端是不行的（docs/XVFB_DISPLAY_PLAN.md）。
func TestStatusAndPreflightAcquireNothing(t *testing.T) {
	p := newFakeEnv("xd", CapGUI, "DISPLAY=:1", nil)
	h := mustHost(t, Config{}, p)

	st := h.Status()
	pf := h.Preflight()
	if p.acquired != 0 {
		t.Fatal("Status/Preflight acquired a lease")
	}
	if len(st) != 1 || st[0].Name != "xd" || !st[0].Ready || !slices.Equal(st[0].Provides, []Capability{CapGUI}) {
		t.Errorf("Status = %+v", st)
	}
	if len(pf) != 1 || pf[0].Name != "xd" {
		t.Errorf("Preflight = %+v", pf)
	}
}

// TestCloseReverseOrder: 消费者先于提供者关闭。
func TestCloseReverseOrder(t *testing.T) {
	var closed []string
	provider := newFakeEnv("provider", CapGUI, "", nil)
	consumer := newFakeEnv("consumer", "cap.other", "", nil, Need{Cap: CapGUI})
	provider.closed, consumer.closed = &closed, &closed
	h := mustHost(t, Config{}, consumer, provider)

	h.Close()
	if want := []string{"consumer", "provider"}; !reflect.DeepEqual(closed, want) {
		t.Errorf("close order = %v, want %v", closed, want)
	}
}

func commandOrSkip(t *testing.T, h *Host, spec LaunchSpec) *Command {
	t.Helper()
	if _, err := h.Umu().Interpreter(); err != nil {
		t.Skipf("no usable Python interpreter here: %v", err)
	}
	c, err := h.Command(context.Background(), "/srv/Game.exe", []string{"-a"}, spec)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func indexOf(env []string, prefix string) int {
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return i
		}
	}
	return -1
}

// TestCommandEnvLayering: 叠加顺序是承重的——我们的变量压过继承来的、
// WINEDLLOVERRIDES 最后说了算、租约排在降权改写之后（见 Command 的注释）。
func TestCommandEnvLayering(t *testing.T) {
	cfg := readyBase(t)
	cfg.WineDLLOverrides = "d3d11=n"
	cfg.Identity = Identity{
		Credential: func() (*syscall.Credential, string, error) {
			return &syscall.Credential{Uid: 4242, Gid: 4242}, "/home/runtime", nil
		},
		UserName: func() string { return "runtime" },
	}
	gui := newFakeEnv("xd", CapGUI, "DISPLAY=:77", nil)
	h := mustHost(t, cfg, gui)

	c := commandOrSkip(t, h, LaunchSpec{
		Env:   []string{"PROTON_VERB=waitforexitandrun", "HOME=/root", "XDG_RUNTIME_DIR=/run/user/0"},
		Needs: []Capability{CapGUI, CapGUI},
	})

	if !reflect.DeepEqual(c.Args, []string{h.Umu().RunPath(), "/srv/Game.exe", "-a"}) {
		t.Errorf("Args = %v", c.Args)
	}
	if c.Credential == nil || c.Credential.Uid != 4242 {
		t.Errorf("Credential = %+v, want the identity's", c.Credential)
	}
	if v := c.Env[indexOf(c.Env, "PROTON_VERB=")]; v != "PROTON_VERB=run" {
		t.Errorf("effective %s, want PROTON_VERB=run", v)
	}
	if v := c.Env[indexOf(c.Env, "HOME=")]; v != "HOME=/home/runtime" {
		t.Errorf("effective %s, want the dropped user's HOME", v)
	}
	if indexOf(c.Env, "XDG_RUNTIME_DIR=") >= 0 {
		t.Error("root's XDG_RUNTIME_DIR survived the privilege drop")
	}
	if i, j := indexOf(c.Env, "WINEDLLOVERRIDES="), indexOf(c.Env, "PROTON_VERB="); i < j {
		t.Errorf("WINEDLLOVERRIDES (%d) must come after the umu variables (%d)", i, j)
	}
	if i, j := indexOf(c.Env, "DISPLAY="), indexOf(c.Env, "HOME="); i < j {
		t.Errorf("lease DISPLAY (%d) must come after the privilege-drop rewrite (%d)", i, j)
	}
	if gui.acquired != 1 || len(c.Leases) != 1 || c.Leases[0].Plugin != "xd" {
		t.Errorf("duplicate need acquired %d times, leases %+v", gui.acquired, c.Leases)
	}
}

// TestCommandUnprovidedNeedFails: 声明了需要、却没有任何插件提供——
// 启动必须失败，并且已经拿到的租约要还回去。
func TestCommandUnprovidedNeedFails(t *testing.T) {
	gui := newFakeEnv("xd", CapGUI, "DISPLAY=:77", nil)
	h := mustHost(t, readyBase(t), gui)
	if _, err := h.Umu().Interpreter(); err != nil {
		t.Skipf("no usable Python interpreter here: %v", err)
	}

	_, err := h.Command(context.Background(), "/srv/Game.exe", nil, LaunchSpec{
		Needs: []Capability{CapGUI, "cap.nobody"},
	})
	var ue *CapabilityUnavailableError
	if !errors.As(err, &ue) || ue.Cap != "cap.nobody" {
		t.Fatalf("err = %v, want CapabilityUnavailableError for cap.nobody", err)
	}
	if gui.released != 1 {
		t.Errorf("already-acquired lease released %d times, want 1", gui.released)
	}
}

func TestCommandNotReady(t *testing.T) {
	h := mustHost(t, Config{Umu: umu.Config{BaseDir: t.TempDir()}})
	_, err := h.Command(context.Background(), "/srv/Game.exe", nil, LaunchSpec{})
	var nr *NotReadyError
	if !errors.As(err, &nr) {
		t.Fatalf("err = %v, want *NotReadyError", err)
	}
}
