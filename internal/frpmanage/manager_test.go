package frpmanage

import (
	"asa-server/pkg/logger"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	logger.InitLoggerWithBaseDir(os.TempDir())
	os.Exit(m.Run())
}

// leakConfig points frpc at a closed local port with loginFailExit=false, so
// Start() lands the client in its real retry-with-backoff loop (not the
// instant-failure path) — Stop() shortly after must cancel that loop
// cleanly, which is the scenario the library-call switch could leak on
// (docs/LINUX_COMPATIBILITY_PLAN.md §5.10.4 #6).
const leakConfig = `
serverAddr = "127.0.0.1"
serverPort = 1
loginFailExit = false
log.to = "console"
log.level = "warn"
`

// TestRestartFiftyTimesNoGoroutineLeak is the F3 acceptance check from
// docs/LINUX_COMPATIBILITY_PLAN.md §5.10.6 / §9.2: connect-refused retries
// don't leave goroutines behind across repeated Start/Stop cycles.
func TestRestartFiftyTimesNoGoroutineLeak(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, frpcConfigFileName), []byte(leakConfig), 0644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}
	if _, err := Initialize(dir); err != nil {
		t.Fatalf("Initialize: %v", err)
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
