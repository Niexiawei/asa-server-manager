//go:build windows

package runner

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestRun_NonPTY(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable unavailable: %v", err)
	}
	// Re-launch this test binary itself with a flag that makes the Go test
	// runner exit immediately (-test.list matches nothing and returns fast)
	// — avoids depending on any external binary being on PATH.
	h, err := Run(context.Background(), exe, []string{"-test.run=^$"}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.LauncherPID == 0 {
		t.Error("expected a non-zero LauncherPID")
	}
	if h.Process == nil {
		t.Error("expected Process to be set in non-PTY mode")
	}

	done := make(chan error, 1)
	go func() { done <- h.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("process did not exit within 10s")
	}
}

func TestGamePath_IsIdentityOnWindows(t *testing.T) {
	for _, p := range []string{`C:\a\b`, `relative\path`, ""} {
		if got := GamePath(p); got != p {
			t.Errorf("GamePath(%q) = %q, want identity", p, got)
		}
	}
}
