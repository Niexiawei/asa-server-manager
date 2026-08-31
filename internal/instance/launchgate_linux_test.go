//go:build linux

package instance

import (
	"context"
	"testing"
	"time"

	"asa-server/internal/runner"
)

func configurePrefixMode(t *testing.T, mode string) {
	t.Helper()
	runner.Configure(runner.Config{
		Runtime:       "umu",
		ProtonVersion: "GE-Proton10-34",
		PrefixMode:    mode,
		GameID:        "umu-default",
		BaseDir:       t.TempDir(),
	})
	t.Cleanup(func() { runner.Configure(runner.Config{PrefixMode: "shared"}) })
}

// 共享 prefix 下第二台必须等第一台放行——这正是本闸门存在的理由。
func TestLaunchGate_SharedSerializesLaunches(t *testing.T) {
	configurePrefixMode(t, "shared")

	releaseA, err := acquireLaunchGate(context.Background(), "A")
	if err != nil {
		t.Fatalf("A should acquire immediately: %v", err)
	}

	acquiredB := make(chan struct{})
	go func() {
		releaseB, err := acquireLaunchGate(context.Background(), "B")
		if err == nil {
			defer releaseB()
			close(acquiredB)
		}
	}()

	select {
	case <-acquiredB:
		t.Fatal("B acquired the gate while A still held it")
	case <-time.After(100 * time.Millisecond):
	}

	releaseA()

	select {
	case <-acquiredB:
	case <-time.After(2 * time.Second):
		t.Fatal("B never acquired the gate after A released it")
	}
}

// 释放函数必须幂等：调用方在初始化成功后显式放行一次，defer 还会再放行一次。
// 不幂等的话第二次 `<-sem` 会把下一台的持有权吃掉，闸门直接失效。
func TestLaunchGate_ReleaseIsIdempotent(t *testing.T) {
	configurePrefixMode(t, "shared")

	release, err := acquireLaunchGate(context.Background(), "A")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	release()
	release()

	releaseB, err := acquireLaunchGate(context.Background(), "B")
	if err != nil {
		t.Fatalf("B should acquire after A released: %v", err)
	}
	defer releaseB()

	done := make(chan struct{})
	go func() {
		r, err := acquireLaunchGate(context.Background(), "C")
		if err == nil {
			r()
			close(done)
		}
	}()
	select {
	case <-done:
		t.Fatal("C acquired while B held the gate — the double release leaked a permit")
	case <-time.After(100 * time.Millisecond):
	}
}

// per-instance 下每台自己一个 prefix，不该有任何排队。
func TestLaunchGate_PerInstanceDoesNotSerialize(t *testing.T) {
	configurePrefixMode(t, "per-instance")

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
		t.Fatal("per-instance mode must not make B wait")
	}
}

// per-instance 下每实例自带 wineserver，ArkApi 多开是**合法**的。
// 漏掉模式判断会把本来正常的启动拦下来，还会建议用户去改一个已经改好的配置项。
func TestConflictingArkApiInstance_SilentUnderPerInstance(t *testing.T) {
	configurePrefixMode(t, "per-instance")

	if got := conflictingArkApiInstance("whatever"); got != "" {
		t.Fatalf("per-instance 下不得报 ArkApi 冲突，got %q", got)
	}
}

// 等锁必须可取消，否则用户取消启动后这条协程会挂到天荒地老。
func TestLaunchGate_WaitIsCancellable(t *testing.T) {
	configurePrefixMode(t, "shared")

	releaseA, err := acquireLaunchGate(context.Background(), "A")
	if err != nil {
		t.Fatalf("A: %v", err)
	}
	defer releaseA()

	ctx, cancel := context.WithCancel(context.Background())
	errC := make(chan error, 1)
	go func() {
		_, err := acquireLaunchGate(ctx, "B")
		errC <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errC:
		if err == nil {
			t.Fatal("cancelled wait must return an error, not a permit")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling the context did not unblock the waiter")
	}
}
