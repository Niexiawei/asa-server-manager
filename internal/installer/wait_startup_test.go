package installer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stubProbes swaps the port/liveness probes for the duration of a test, and
// speeds the poll loop up so timeouts are measured in milliseconds.
func stubProbes(t *testing.T, inUse func(int) (bool, error), exited func(int) bool) {
	t.Helper()
	oldPort, oldExited, oldInterval := portInUse, processExited, verifyPollInterval
	portInUse, processExited, verifyPollInterval = inUse, exited, 5*time.Millisecond
	t.Cleanup(func() {
		portInUse, processExited, verifyPollInterval = oldPort, oldExited, oldInterval
	})
}

func neverExits(int) bool { return false }

func discard(string) {}

// TestWaitForVerificationServer_ConfigDirIsNotSuccess is the regression this
// function was rewritten for: an already-present config directory (every
// force=true re-run) must NOT be mistaken for a started server.
func TestWaitForVerificationServer_ConfigDirIsNotSuccess(t *testing.T) {
	dir := t.TempDir() // exists from the very first poll

	stubProbes(t, func(int) (bool, error) { return false, nil }, neverExits)

	err := waitForVerificationServer(context.Background(), dir, 39862, 4242, 100*time.Millisecond, discard)
	if err == nil {
		t.Fatal("an existing config dir with nothing listening must not pass verification")
	}
}

func TestWaitForVerificationServer_SucceedsWhenPortOpens(t *testing.T) {
	dir := t.TempDir()

	var polls atomic.Int32
	stubProbes(t, func(int) (bool, error) {
		return polls.Add(1) >= 2, nil // not bound on the first poll, bound on the second
	}, neverExits)

	if err := waitForVerificationServer(context.Background(), dir, 39862, 4242, 5*time.Second, discard); err != nil {
		t.Fatalf("waitForVerificationServer: %v", err)
	}
}

func TestWaitForVerificationServer_FailsFastWhenProcessDies(t *testing.T) {
	dir := t.TempDir()

	stubProbes(t, func(int) (bool, error) { return false, nil }, func(int) bool { return true })

	start := time.Now()
	err := waitForVerificationServer(context.Background(), dir, 39862, 4242, 5*time.Minute, discard)
	if err == nil {
		t.Fatal("expected an error when the launcher process is gone")
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Errorf("a dead process must be reported immediately, took %s", elapsed)
	}
}

// A port probe that errors (permission, an unavailable /proc) must not be read
// as "the server is up", nor abort the wait — it just keeps polling.
func TestWaitForVerificationServer_ProbeErrorsDoNotPass(t *testing.T) {
	dir := t.TempDir()

	stubProbes(t, func(int) (bool, error) { return false, os.ErrPermission }, neverExits)

	if err := waitForVerificationServer(context.Background(), dir, 39862, 4242, 100*time.Millisecond, discard); err == nil {
		t.Fatal("a failing port probe must not be treated as success")
	}
}

// The message has to say which of the two stages was reached, because the two
// point at completely different causes: no config dir means the server never
// got far enough to write its own files (a permission or launch problem),
// while config-but-no-port means it booted and then hung.
func TestWaitForVerificationServer_DistinguishesStages(t *testing.T) {
	stubProbes(t, func(int) (bool, error) { return false, nil }, neverExits)

	missing := filepath.Join(t.TempDir(), "never-appears")
	err := waitForVerificationServer(context.Background(), missing, 39862, 4242, 100*time.Millisecond, discard)
	if err == nil || !strings.Contains(err.Error(), "never created") {
		t.Fatalf("want a 'config dir never created' error, got %v", err)
	}

	present := t.TempDir()
	err = waitForVerificationServer(context.Background(), present, 39862, 4242, 100*time.Millisecond, discard)
	if err == nil || !strings.Contains(err.Error(), "never started listening") {
		t.Fatalf("want a 'never started listening' error, got %v", err)
	}
}

func TestWaitForVerificationServer_ReportsConfigDirMilestone(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "WindowsServer")

	var polls atomic.Int32
	stubProbes(t, func(int) (bool, error) {
		n := polls.Add(1)
		if n == 1 {
			_ = os.MkdirAll(target, 0o755) // appears between the two polls
		}
		return n >= 2, nil
	}, neverExits)

	var messages []string
	emit := func(s string) { messages = append(messages, s) }

	if err := waitForVerificationServer(context.Background(), target, 39862, 4242, 5*time.Second, emit); err != nil {
		t.Fatalf("waitForVerificationServer: %v", err)
	}
	if len(messages) < 2 {
		t.Fatalf("expected both the config-dir milestone and the listening message, got %v", messages)
	}
}

func TestWaitForVerificationServer_RespectsCancellation(t *testing.T) {
	dir := t.TempDir()
	stubProbes(t, func(int) (bool, error) { return false, nil }, neverExits)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if err := waitForVerificationServer(ctx, dir, 39862, 4242, 5*time.Second, discard); err == nil {
		t.Fatal("expected cancellation to produce an error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("expected cancellation to return promptly, took %s", elapsed)
	}
}

func TestTailLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := tailLines(path, 2), "c\nd"; got != want {
		t.Errorf("tailLines = %q, want %q", got, want)
	}
	if got, want := tailLines(path, 99), "a\nb\nc\nd"; got != want {
		t.Errorf("tailLines(more than present) = %q, want %q", got, want)
	}
	if got := tailLines(filepath.Join(t.TempDir(), "missing"), 5); got != "" {
		t.Errorf("tailLines(missing file) = %q, want empty", got)
	}
}
