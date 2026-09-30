package filelock

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// 锁属于「打开的文件」而不是进程：同一进程里两次打开同一路径，冲突规则与两个进程
// 完全一致。这里的每一条断言因此也代表跨进程的行为。
func TestTryLockConflicts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "x.lock") // 目录不存在也要能建

	ex, err := TryLock(path, Exclusive)
	if err != nil {
		t.Fatalf("first exclusive lock: %v", err)
	}
	if _, err := TryLock(path, Exclusive); !errors.Is(err, ErrLocked) {
		t.Fatalf("second exclusive lock: got %v, want ErrLocked", err)
	}
	if _, err := TryLock(path, Shared); !errors.Is(err, ErrLocked) {
		t.Fatalf("shared lock while exclusively held: got %v, want ErrLocked", err)
	}
	ex()
	ex() // 幂等

	sh1, err := TryLock(path, Shared)
	if err != nil {
		t.Fatalf("shared lock after release: %v", err)
	}
	sh2, err := TryLock(path, Shared)
	if err != nil {
		t.Fatalf("two shared holders must coexist: %v", err)
	}
	if _, err := TryLock(path, Exclusive); !errors.Is(err, ErrLocked) {
		t.Fatalf("exclusive lock while shared-held: got %v, want ErrLocked", err)
	}
	sh1()
	sh2()
	if release, err := TryLock(path, Exclusive); err != nil {
		t.Fatalf("exclusive lock after all shared holders released: %v", err)
	} else {
		release()
	}
}

func TestLockWaitsAndCanBeCancelled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	held, err := TryLock(path, Exclusive)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	waited := false
	if _, err := Lock(ctx, path, Exclusive, 20*time.Millisecond, func(time.Duration) { waited = true }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Lock while held: got %v, want context.DeadlineExceeded", err)
	}
	if !waited {
		t.Error("waiting callback was never called")
	}

	got := make(chan func(), 1)
	go func() {
		if release, err := Lock(context.Background(), path, Exclusive, 20*time.Millisecond, nil); err == nil {
			got <- release
		}
	}()
	time.Sleep(60 * time.Millisecond)
	held()
	select {
	case release := <-got:
		release()
	case <-time.After(5 * time.Second):
		t.Fatal("waiter never got the lock after it was released")
	}
}
