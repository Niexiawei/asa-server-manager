package resourcegate

import (
	"context"
	"testing"
	"time"
)

func TestGate_SerializesAcquires(t *testing.T) {
	g := New(1)

	releaseA, err := g.Acquire(context.Background(), "A")
	if err != nil {
		t.Fatalf("A should acquire immediately: %v", err)
	}
	if got := g.Holder(); got != "A" {
		t.Fatalf("Holder() = %q, want A", got)
	}

	acquiredB := make(chan struct{})
	go func() {
		releaseB, err := g.Acquire(context.Background(), "B")
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
// 不幂等的话第二次 receive 会把下一个持有者的许可吃掉，闸门直接失效。
func TestGate_ReleaseIsIdempotent(t *testing.T) {
	g := New(1)

	release, err := g.Acquire(context.Background(), "A")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	release()
	release()

	releaseB, err := g.Acquire(context.Background(), "B")
	if err != nil {
		t.Fatalf("B should acquire after A released: %v", err)
	}
	defer releaseB()

	done := make(chan struct{})
	go func() {
		r, err := g.Acquire(context.Background(), "C")
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

func TestGate_WaitIsCancellable(t *testing.T) {
	g := New(1)

	release, err := g.Acquire(context.Background(), "A")
	if err != nil {
		t.Fatalf("A: %v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	errC := make(chan error, 1)
	go func() {
		_, err := g.Acquire(ctx, "B")
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

func TestGate_HolderEmptyWhenFree(t *testing.T) {
	g := New(1)
	if got := g.Holder(); got != "" {
		t.Fatalf("Holder() on a fresh gate = %q, want empty", got)
	}
}

func TestGate_CapacityGreaterThanOne(t *testing.T) {
	g := New(2)

	releaseA, err := g.Acquire(context.Background(), "A")
	if err != nil {
		t.Fatalf("A: %v", err)
	}
	defer releaseA()

	releaseB, err := g.Acquire(context.Background(), "B")
	if err != nil {
		t.Fatalf("B should acquire concurrently with A under capacity 2: %v", err)
	}
	defer releaseB()
}
