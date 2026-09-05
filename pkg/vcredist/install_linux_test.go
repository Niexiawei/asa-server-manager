//go:build linux

package vcredist

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"asa-server/pkg/umu"
)

// 真正跑一次安装需要 umu-run + GE-Proton + 一个已初始化的 Wine 前缀，不适合单测。
// 这里钉住的是**类型化契约**那一层：哪些情形返回 error、哪些返回 Result.Skip、
// 以及两次 umu-run 调用的选项 —— 那些正是把编排搬进本包时唯一改了形状的东西。

func discard(string, ...any) {}

// fakeRuntime: 构造一个 *umu.Runtime 不启动任何东西（第一次 RunInPrefix 才动手），
// 所以用它满足 Config.Umu 必填、又不会真的去跑 umu-run。
func fakeRuntime() *umu.Runtime { return umu.New(umu.Config{}) }

// TestEnsureRequiresUmu: Config.Umu 是必填的。忘了接会在第一次 RunInPrefix 上空指针
// 崩掉，而那时已经写了 .reg 文件 —— 提前拒绝，别留半个副作用。
func TestEnsureRequiresUmu(t *testing.T) {
	_, err := New(Config{Dir: t.TempDir()}).Ensure(context.Background(), t.TempDir(), discard)
	if err == nil || !strings.Contains(err.Error(), "Umu") {
		t.Errorf("Ensure without Config.Umu = %v, want an error naming Umu", err)
	}
}

// TestEnsureRejectsUninitializedPrefix: 前缀还没 wineboot 过就装 VC++ 是错的，
// 而且**必须是 error 不是 Skip** —— 它是调用方编排顺序搞反了，不是环境不具备。
func TestEnsureRejectsUninitializedPrefix(t *testing.T) {
	prefix := t.TempDir() // 没有 system.reg
	res, err := New(Config{Dir: t.TempDir(), Umu: fakeRuntime()}).
		Ensure(context.Background(), prefix, discard)
	if err == nil {
		t.Fatalf("Ensure on an uninitialized prefix = %+v, want an error", res)
	}
	if res.OverridesApplied {
		t.Error("前缀没初始化就报 override 已写入")
	}
}

// TestClassifySkipSeparatesTheTwoCauses: 「本机没有显示能力」与「有能力但这次没
// 拿到」必须分得开 —— 前者该去装一个 X 服务，后者该去看失败原文（常见是 Xvfb
// 起不来）。分不开就只能给一句和稀泥的提示，这正是把它做成类型的理由。
func TestClassifySkipSeparatesTheTwoCauses(t *testing.T) {
	blocked := fmt.Errorf("%w: 本机没有 Xvfb —— 请安装 Xvfb", ErrNoDisplay)
	if got := classifySkip(blocked); got != SkipNoDisplay {
		t.Errorf("classifySkip(ErrNoDisplay 包装) = %q, want %q", got, SkipNoDisplay)
	}
	if got := classifySkip(errors.New("Xvfb 启动失败：缺字体")); got != SkipDisplayUnavailable {
		t.Errorf("classifySkip(其它错误) = %q, want %q", got, SkipDisplayUnavailable)
	}
}

// TestAcquireDisplayDefaultsToNoDisplay: 没接 AcquireDisplay 回调 = 本机没有显示，
// 而不是 panic，也不是「有显示」。Windows 侧的调用方压根不构造 Installer，但
// 一个只想写 override 的调用方可以合法地不接这个回调。
func TestAcquireDisplayDefaultsToNoDisplay(t *testing.T) {
	_, _, err := (&Installer{}).acquireDisplay()
	if !errors.Is(err, ErrNoDisplay) {
		t.Errorf("nil AcquireDisplay → %v, want ErrNoDisplay", err)
	}
	if classifySkip(err) != SkipNoDisplay {
		t.Error("nil AcquireDisplay 应当归到 SkipNoDisplay")
	}
}

// TestAutoDownloadDisabledCarriesBothPaths: 调用方要用 Dest 与 URL 拼一句带自己
// 配置项名字的指引，所以这两样都必须带出来 —— 只给一句话就等于把文案锁死在本包里。
func TestAutoDownloadDisabledCarriesBothPaths(t *testing.T) {
	dir := t.TempDir()
	i := New(Config{Dir: dir, AutoDownload: false})

	_, _, err := i.ensureInstaller(context.Background(), discard)
	var e *AutoDownloadDisabledError
	if !errors.As(err, &e) {
		t.Fatalf("ensureInstaller = %v, want *AutoDownloadDisabledError", err)
	}
	if want := filepath.Join(dir, InstallerName); e.Dest != want {
		t.Errorf("Dest = %q, want %q", e.Dest, want)
	}
	if e.URL != DefaultURL {
		t.Errorf("URL = %q, want the default %q", e.URL, DefaultURL)
	}
}

// TestRunOptionsDifferFromWarmPrefix: 本包两次 umu-run 与 umu.WarmPrefix 的
// wineboot 只有三处刻意的不同，其中两处是**正确性**而非偏好：
//   - Verb "run"：umu 默认的 waitforexitandrun 会先跑 `wineserver -w`，共享 prefix 上
//     只要有实例在跑就永不返回，整条调用挂到硬超时且错得毫无线索；
//   - NoRuntimeUpdate：运行时早装好了，没理由再去 repo.steampowered.com 查更新。
func TestRunOptionsDifferFromWarmPrefix(t *testing.T) {
	opt := runOptions(installTimeout, []string{"DISPLAY=:0"})
	if opt.Verb != "run" {
		t.Errorf("Verb = %q, want \"run\"", opt.Verb)
	}
	if !opt.NoRuntimeUpdate {
		t.Error("NoRuntimeUpdate = false")
	}
	if opt.Timeout != installTimeout {
		t.Errorf("Timeout = %v, want %v", opt.Timeout, installTimeout)
	}
	if len(opt.ExtraEnv) != 1 || opt.ExtraEnv[0] != "DISPLAY=:0" {
		t.Errorf("ExtraEnv = %v, want the display to be passed through", opt.ExtraEnv)
	}
	// override 那一步故意不带显示 —— 它无头可用，这正是它承重的原因。
	if env := runOptions(overrideImportTimeout, nil).ExtraEnv; env != nil {
		t.Errorf("override 那一步带上了显示：%v", env)
	}
}
