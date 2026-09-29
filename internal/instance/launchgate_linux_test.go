//go:build linux

package instance

import (
	"context"
	"sync"
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

// stubArkApiWorld 替换冲突判定对外部世界的三个依赖：实例列表、存活判定、是否启用 ArkApi。
func stubArkApiWorld(t *testing.T, names []string, alive map[string]bool) {
	t.Helper()
	origList, origAlive, origEnabled := listInstanceNames, arkApiInstanceAlive, arkApiEnabledFor
	listInstanceNames = func() ([]string, error) { return names, nil }
	arkApiInstanceAlive = func(name string) bool { return alive[name] }
	arkApiEnabledFor = func(string) bool { return true }
	t.Cleanup(func() {
		listInstanceNames, arkApiInstanceAlive, arkApiEnabledFor = origList, origAlive, origEnabled
	})
}

// 回归 docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md §3.2 P1-6：A 还在启动、端口没绑、
// 进程判不出存活时，B 也必须被拦下——以前的判据只看端口，这一段完全看不见 A。
func TestClaimArkApiSlot_SeesInstanceStillStarting(t *testing.T) {
	configurePrefixMode(t, "shared")
	stubArkApiWorld(t, []string{"A", "B"}, nil)

	other, releaseA := claimArkApiSlot("A")
	if other != "" {
		t.Fatalf("A should claim with nobody else around, got conflict with %q", other)
	}
	if other, _ := claimArkApiSlot("B"); other != "A" {
		t.Fatalf("B must see A while A is still starting, got %q", other)
	}
	if got := conflictingArkApiInstance("B"); got != "A" {
		t.Fatalf("the in-gate re-check must see A too, got %q", got)
	}

	releaseA()
	releaseA() // 幂等
	other, releaseB := claimArkApiSlot("B")
	if other != "" {
		t.Fatalf("B should claim after A's launch ended and A isn't alive, got %q", other)
	}
	releaseB()
}

// 已经在跑（存活判定为真、没有登记）的 ArkApi 实例同样构成冲突。
func TestClaimArkApiSlot_SeesRunningInstance(t *testing.T) {
	configurePrefixMode(t, "shared")
	stubArkApiWorld(t, []string{"A", "B"}, map[string]bool{"A": true})

	if other, _ := claimArkApiSlot("B"); other != "A" {
		t.Fatalf("B must see running A, got %q", other)
	}
}

// 并发：N 个同时抢，只能有一个成功。检查与登记分两步做就会有不止一个。
func TestClaimArkApiSlot_OnlyOneWinsConcurrently(t *testing.T) {
	configurePrefixMode(t, "shared")
	names := []string{"A", "B", "C", "D", "E", "F", "G", "H"}
	stubArkApiWorld(t, names, nil)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var releases []func()
	winners := 0
	for _, n := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			if other, release := claimArkApiSlot(n); other == "" {
				mu.Lock()
				winners++
				releases = append(releases, release)
				mu.Unlock()
			}
		}(n)
	}
	wg.Wait()
	for _, r := range releases {
		r()
	}
	if winners != 1 {
		t.Fatalf("%d concurrent claims succeeded, want exactly 1", winners)
	}
}

// per-instance 下不共享 Wine 会话：什么都不登记，两台都能过。
func TestClaimArkApiSlot_NoopUnderPerInstance(t *testing.T) {
	configurePrefixMode(t, "per-instance")
	stubArkApiWorld(t, []string{"A", "B"}, nil)

	_, releaseA := claimArkApiSlot("A")
	defer releaseA()
	if other, _ := claimArkApiSlot("B"); other != "" {
		t.Fatalf("per-instance must not report a conflict, got %q", other)
	}
}
