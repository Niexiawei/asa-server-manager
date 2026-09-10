package frpmanage

import (
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
	"time"

	"asa-server/pkg/logger"
)

func TestMain(m *testing.M) {
	logger.InitLoggerWithBaseDir(os.TempDir())
	os.Exit(m.Run())
}

// unreachableConfig 指向一个必定拒绝连接的本地端口。
func unreachableConfig() *Config {
	return &Config{
		ServerAddr: "127.0.0.1",
		ServerPort: 1,
		Token:      "test",
		Rules:      []PortRule{{Start: 19310, End: 19311, Protocol: ProtocolUDP}},
	}
}

// TestRestartFiftyTimesNoGoroutineLeak 是 F3 验收的管理器一侧：
// 连续 Start/Stop 不会在 FrpcManager 这层留下 goroutine
// （docs/LINUX_COMPATIBILITY_PLAN.md §5.10.6 / §9.2）。
func TestRestartFiftyTimesNoGoroutineLeak(t *testing.T) {
	// ⚠️ 配置要写进 Initialize 建出来的 {base}/frp 子目录，不是 base 本身。
	// 改造前的这条用例正是写错了这一层：它的 leakConfig 从没被读到过，跑的一直是
	// 自动生成的默认配置 —— 于是它标称要压的「登录失败后退避重试」路径其实从未
	// 被覆盖。那条路径现在由 TestRetryLoopCloseNoGoroutineLeak 真正压住。
	base := t.TempDir()
	runDir, err := Initialize(base)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := SaveConfig(runDir, unreachableConfig()); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	m := GetGlobalManager()

	// Warm up once so any one-time setup goroutines (e.g. from package inits
	// reached lazily) don't get counted as "leaked" in the real measurement.
	runOnce(t, m)
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	for i := 0; i < 50; i++ {
		runOnce(t, m)
	}

	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	final := runtime.NumGoroutine()

	// Allow slack — timers/backoff goroutines can still be unwinding — but a
	// leak would show up as growth roughly proportional to the 50 iterations,
	// not a handful.
	if final > baseline+10 {
		t.Errorf("goroutine count grew from %d to %d after 50 Start/Stop cycles — looks like a leak", baseline, final)
	}
}

func runOnce(t *testing.T, m *FrpcManager) {
	t.Helper()
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if err := m.Stop(); err != nil && m.IsRunning() {
		t.Fatalf("Stop: %v", err)
	}
}

// TestRetryLoopCloseNoGoroutineLeak 是 F3 验收的 frp 一侧，也是本包配置面改造后
// 唯一还能覆盖到「退避重试循环」的地方。
//
// loginFailExit 在生产配置里恒为 frp 默认的 true（见 docs/FRP_FORM_CONFIG_PLAN.md
// 决策 D2），此时连接被拒会走**立即失败**路径，Run 直接返回。而 §5.10.4 坑 #6
// 担心的泄漏场景恰恰是另一条：登录失败后不退出、留在 retry-with-backoff 循环里，
// 这时 GracefulClose 要能把那个循环干净地取消掉。所以这里手动把 LoginFailExit
// 翻成 false，专门压那条路径。
func TestRetryLoopCloseNoGoroutineLeak(t *testing.T) {
	common, proxyCfgs, err := unreachableConfig().build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	noExit := false
	common.LoginFailExit = &noExit

	// 关闭方式与 stopLocked 一致：取消自己喂进 Run 的 ctx，而不是 svr.GracefulClose
	// —— 后者会读 Run 尚未写完的 svr.cancel，既是数据竞争也有 nil 调用的窗口。
	run := func() {
		svr, _, err := newService(common, proxyCfgs)
		if err != nil {
			t.Fatalf("newService: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = svr.Run(ctx)
		}()
		time.Sleep(30 * time.Millisecond)
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("svr.Run did not return within 5s after ctx cancel")
		}
	}

	run()
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	for i := 0; i < 50; i++ {
		run()
	}

	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	if final := runtime.NumGoroutine(); final > baseline+10 {
		t.Errorf("goroutine count grew from %d to %d after 50 retry-loop cycles — looks like a leak", baseline, final)
	}
}

// TestStopImmediatelyAfterStart 压 stopLocked 注释里那个窗口：Start 之后**不留
// 任何间隔**就 Stop / Restart 时，frp 的 svr.cancel 可能还没被 Run 赋值。
// 走 svr.GracefulClose 的话这里是在调一个 nil 函数 —— 而 frp 是库内调用，没有
// 崩溃隔离，一次 panic 就带走整个 asa-server。
func TestStopImmediatelyAfterStart(t *testing.T) {
	base := t.TempDir()
	runDir, err := Initialize(base)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := SaveConfig(runDir, unreachableConfig()); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	m := GetGlobalManager()

	for i := 0; i < 100; i++ {
		if err := m.Start(); err != nil {
			t.Fatalf("Start #%d: %v", i, err)
		}
		// 不 sleep：就是要撞上 Run 还没跑到赋值那一行的时刻
		if err := m.Stop(); err != nil && m.IsRunning() {
			t.Fatalf("Stop #%d: %v", i, err)
		}
	}
}

// TestStartWithoutConfigIsNotConfigured 守住自动启动路径的日志降级依据：
// 没配过 frp 必须能被 errors.Is(err, ErrNotConfigured) 识别出来。
func TestStartWithoutConfigIsNotConfigured(t *testing.T) {
	dir := t.TempDir()
	if _, err := Initialize(dir); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	err := GetGlobalManager().Start()
	if err == nil {
		t.Fatal("Start on an unconfigured manager should fail")
	}
	if !errors.Is(err, ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestTrimGolibHeader(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2026-09-08 10:11:12.345 [W] login to server failed", "login to server failed"},
		{"2026-09-08 10:11:12.345 [I] start proxy success", "start proxy success"},
		{"no header at all", "no header at all"},
		{"short", "short"},
		// 形状对不上时原样返回：宁可多一段前缀，也不要截掉正文
		{"2026-09-08 10:11:12.345  W  something", "2026-09-08 10:11:12.345  W  something"},
	}
	for _, c := range cases {
		if got := trimGolibHeader(c.in); got != c.want {
			t.Errorf("trimGolibHeader(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
