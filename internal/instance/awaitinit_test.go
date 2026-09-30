package instance

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 回归 docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md §6.2 P1-19：初始化迟迟不来时，
// 闸门必须在超时后被放行，而等待本身继续——超时不是失败。
func TestAwaitInitialization_ReleasesGateOnTimeoutAndKeepsWaiting(t *testing.T) {
	initFailed := make(chan error, 1)
	initSuccessful := make(chan bool, 1)
	released := make(chan struct{}, 4)

	done := make(chan error, 1)
	go func() {
		done <- awaitInitialization(context.Background(), initFailed, initSuccessful,
			20*time.Millisecond, func() { released <- struct{}{} })
	}()

	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("gate was not released after the timeout")
	}
	select {
	case err := <-done:
		t.Fatalf("timeout must not end the wait, returned %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	initSuccessful <- true
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("late success must return nil, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("late success never ended the wait")
	}
	if n := len(released); n != 0 {
		t.Fatalf("gate released %d extra times", n)
	}
}

// 进程退出（initFailed）在超时前后都照常结束等待，返回那个错误。
func TestAwaitInitialization_FailureEndsWait(t *testing.T) {
	initFailed := make(chan error, 1)
	want := errors.New("process exited")
	initFailed <- want
	err := awaitInitialization(context.Background(), initFailed, make(chan bool), time.Hour, func() {
		t.Error("gate timeout must not fire")
	})
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

// ctx 被取消时返回 ctx.Err()。
func TestAwaitInitialization_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := awaitInitialization(ctx, make(chan error), make(chan bool), time.Hour, func() {})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}
