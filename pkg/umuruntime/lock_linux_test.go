//go:build linux

package umuruntime

import (
	"context"
	"testing"
	"time"

	"asa-server/pkg/umu"
)

func lockTestHost(t *testing.T) *Host {
	t.Helper()
	return MustNew(Config{Umu: umu.Config{BaseDir: t.TempDir()}})
}

// 嵌套：同一条调用链带着 Lock 返回的 ctx 再要这把锁，必须立刻放行。
// 这把锁属于「打开的文件」而不是进程，没有这一条，嵌套调用会等它自己的调用方
// 释放——永远等下去。
func TestLockIsReentrantThroughContext(t *testing.T) {
	h := lockTestHost(t)
	ctx, unlock, err := h.Lock(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	done := make(chan error, 1)
	go func() {
		_, inner, err := h.Lock(ctx, nil)
		if err == nil {
			inner() // 内层的释放是空操作，不能把外层的锁放掉
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("nested Lock: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nested Lock with the holder's context waited for itself")
	}

	// 内层释放之后，外层仍然持有：一个独立的调用方还是拿不到。
	short, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	if _, other, err := h.Lock(short, nil); err == nil {
		other()
		t.Fatal("the inner no-op release let go of the outer lock")
	}
}

// 并发：各自带着自己的 ctx 的调用方（另一个进程，或本进程里的另一个任务）照常排队，
// 等久了说一声。
func TestLockQueuesIndependentCallers(t *testing.T) {
	h := lockTestHost(t)
	_, unlock, err := h.Lock(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	announced := make(chan struct{}, 1)
	got := make(chan func(), 1)
	go func() {
		_, second, err := h.Lock(context.Background(), func(string, ...any) {
			select {
			case announced <- struct{}{}:
			default:
			}
		})
		if err == nil {
			got <- second
		}
	}()

	select {
	case <-announced:
	case <-time.After(lockAnnounceAge + 2*time.Second):
		t.Fatal("a long wait for the runtime lock was not announced")
	}
	select {
	case <-got:
		t.Fatal("second caller got the lock while the first still held it")
	default:
	}

	unlock()
	select {
	case second := <-got:
		second()
	case <-time.After(5 * time.Second):
		t.Fatal("second caller never got the lock after it was released")
	}
}
