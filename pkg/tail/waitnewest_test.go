package tail

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func isArkApiLogName(name string) bool {
	return strings.HasPrefix(name, "ArkApi_") && strings.HasSuffix(name, ".log")
}

func writeMatchFile(t *testing.T, dir, name string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes %s: %v", name, err)
	}
	return path
}

func TestWaitNewest_PicksTheNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)

	writeMatchFile(t, dir, "ArkApi_100_2026-08-30_10-00.log", base)
	want := writeMatchFile(t, dir, "ArkApi_368_2026-08-30_12-28.log", base.Add(30*time.Minute))

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	got, err := WaitNewest(ctx, dir, base.Add(-time.Minute), isArkApiLogName, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitNewest: %v", err)
	}
	if got != want {
		t.Errorf("WaitNewest = %q, want %q", got, want)
	}
}

// TestWaitNewest_IgnoresPreviousLaunches 是 notBefore 存在的理由：目录里可能还有
// 上一轮留下的文件，没有下界会把陈旧内容误认成这一轮的。这里额外验证了真正的轮询
// 等待行为：匹配文件在调用之后才出现，WaitNewest 必须等到它，而不是立刻返回失败。
func TestWaitNewest_IgnoresPreviousLaunches(t *testing.T) {
	dir := t.TempDir()
	launchedAt := time.Now()

	writeMatchFile(t, dir, "ArkApi_100_2026-08-29_09-00.log", launchedAt.Add(-24*time.Hour))

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	resultC := make(chan struct {
		path string
		err  error
	}, 1)
	go func() {
		path, err := WaitNewest(ctx, dir, launchedAt, isArkApiLogName, 10*time.Millisecond)
		resultC <- struct {
			path string
			err  error
		}{path, err}
	}()

	time.Sleep(50 * time.Millisecond)
	want := writeMatchFile(t, dir, "ArkApi_368_2026-08-30_12-28.log", launchedAt.Add(time.Second))

	select {
	case r := <-resultC:
		if r.err != nil || r.path != want {
			t.Errorf("WaitNewest = (%q, %v), want (%q, nil) once this launch's log lands", r.path, r.err, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitNewest never picked up the newly-written matching file")
	}
}

func TestWaitNewest_FiltersNames(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)

	// 同目录下的其它文件不能被当成匹配文件。
	writeMatchFile(t, dir, "ShooterGame.log", base.Add(time.Minute))
	writeMatchFile(t, dir, "ArkApi_368_2026-08-30_12-28.log.bak", base.Add(time.Minute))
	writeMatchFile(t, dir, "notArkApi_1.log", base.Add(time.Minute))
	want := writeMatchFile(t, dir, "ArkApi_1_x.log", base)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	got, err := WaitNewest(ctx, dir, base.Add(-time.Minute), isArkApiLogName, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitNewest: %v", err)
	}
	if got != want {
		t.Errorf("WaitNewest = %q, want %q — only matching names count", got, want)
	}
}

func TestWaitNewest_MissingDirTimesOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()

	_, err := WaitNewest(ctx, filepath.Join(t.TempDir(), "does-not-exist"), time.Time{}, isArkApiLogName, 5*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("WaitNewest err = %v, want context.DeadlineExceeded — 目录不存在是正常状态，不是故障", err)
	}
}

// TestWaitNewest_IgnoresDirs: 目录名恰好长得像匹配文件时不能被选中。
func TestWaitNewest_IgnoresDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "ArkApi_9_dir.log"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()

	if got, err := WaitNewest(ctx, dir, time.Time{}, isArkApiLogName, 5*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("WaitNewest = (%q, %v), want context.DeadlineExceeded", got, err)
	}
}

// TestWaitNewest_LastLookOnCancel: 取消发生的同一瞬间文件落盘，不能被漏掉。
func TestWaitNewest_LastLookOnCancel(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Minute)

	ctx, cancel := context.WithCancel(t.Context())

	resultC := make(chan struct {
		path string
		err  error
	}, 1)
	go func() {
		path, err := WaitNewest(ctx, dir, base, isArkApiLogName, 200*time.Millisecond)
		resultC <- struct {
			path string
			err  error
		}{path, err}
	}()

	// 轮询间隔拉得比取消晚发生的时间长，逼 WaitNewest 走「取消后再看一眼」这条分支。
	time.Sleep(20 * time.Millisecond)
	want := writeMatchFile(t, dir, "ArkApi_1_x.log", base.Add(time.Second))
	cancel()

	select {
	case r := <-resultC:
		if r.err != nil || r.path != want {
			t.Errorf("WaitNewest = (%q, %v), want the file that landed right at cancellation", r.path, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitNewest did not return after cancellation")
	}
}
