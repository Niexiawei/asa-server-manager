package installer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitForConfigDir_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	if err := waitForConfigDir(context.Background(), dir, 5*time.Second); err != nil {
		t.Fatalf("waitForConfigDir: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("expected an immediate return for an already-existing dir, took %s", elapsed)
	}
}

func TestWaitForConfigDir_AppearsWhilePolling(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "WindowsServer")

	go func() {
		time.Sleep(2500 * time.Millisecond)
		_ = os.MkdirAll(target, 0755)
	}()

	start := time.Now()
	if err := waitForConfigDir(context.Background(), target, 10*time.Second); err != nil {
		t.Fatalf("waitForConfigDir: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 2*time.Second {
		t.Errorf("expected to wait for the directory to appear, only waited %s", elapsed)
	}
}

func TestWaitForConfigDir_TimesOut(t *testing.T) {
	target := filepath.Join(t.TempDir(), "never-appears")
	err := waitForConfigDir(context.Background(), target, 500*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
}

func TestWaitForConfigDir_RespectsCancellation(t *testing.T) {
	target := filepath.Join(t.TempDir(), "never-appears")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := waitForConfigDir(ctx, target, 30*time.Second)
	if err == nil {
		t.Fatal("expected cancellation to produce an error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("expected cancellation to return promptly, took %s", elapsed)
	}
}
