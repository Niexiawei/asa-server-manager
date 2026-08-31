//go:build windows

package instance

import (
	"context"
	"testing"
	"time"
)

// Windows 上没有 Wine prefix，闸门必须整段短路——这是「Linux 改动不得影响
// Windows 行为」的可执行版本。两台同时进入启动路径，谁都不许等谁。
func TestLaunchGate_NoOpOnWindows(t *testing.T) {
	releaseA, err := acquireLaunchGate(context.Background(), "A")
	if err != nil {
		t.Fatalf("A: %v", err)
	}
	defer releaseA()

	done := make(chan struct{})
	go func() {
		r, err := acquireLaunchGate(context.Background(), "B")
		if err == nil {
			r()
			close(done)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the launch gate must never serialize anything on Windows")
	}
}
