//go:build linux

package instance

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer 是可并发读写的 bytes.Buffer：转抄在另一条协程里写，测试在这里读。
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 回归 docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md §6.2 P1-14：「等日志出现」的超时
// 曾经同时掐断了之后的转抄，ArkApi 实例开服 5 分钟后插件日志就不再更新。
// 这里把出现超时缩到 200ms，在它过去之后继续往源文件追加，转抄必须跟上。
func TestRelayArkApiLog_KeepsFollowingPastAppearTimeout(t *testing.T) {
	dir := t.TempDir()
	launchedAt := time.Now().Add(-time.Second)
	src := filepath.Join(dir, "ArkApi_42_2026-09-29_10-00.log")
	if err := os.WriteFile(src, []byte("line-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var dst syncBuffer
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		relayArkApiLog(&dst, dir, launchedAt, done, 200*time.Millisecond, func(string) {})
		close(finished)
	}()

	waitFor(t, "first line", func() bool { return strings.Contains(dst.String(), "line-1") })

	time.Sleep(500 * time.Millisecond) // 远超出现超时
	f, err := os.OpenFile(src, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("line-after-timeout\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	waitFor(t, "line written after the appear timeout", func() bool {
		return strings.Contains(dst.String(), "line-after-timeout")
	})
	select {
	case <-finished:
		t.Fatal("relay ended before the launch chain did")
	default:
	}

	close(done)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not end after done was closed")
	}
	if !strings.Contains(dst.String(), "启动链已结束，停止转抄") {
		t.Errorf("missing the closing note, got:\n%s", dst.String())
	}
}

// 日志一直不出现时，超时后写明原因并退出。
func TestRelayArkApiLog_AppearTimeout(t *testing.T) {
	var dst syncBuffer
	relayArkApiLog(&dst, t.TempDir(), time.Now(), make(chan struct{}), 100*time.Millisecond, func(string) {})
	if !strings.Contains(dst.String(), "等待超过") {
		t.Errorf("missing the timeout note, got:\n%s", dst.String())
	}
}

// 启动链退出之后才读到的那几行（秒退加载器的最后输出）不能丢：PTY 由读取方在
// 读尽之后关闭，而不是 launcher 一退出就被关掉。
func TestDrainLauncherOutputKeepsTailAfterLauncherExit(t *testing.T) {
	r, w := io.Pipe()
	done := make(chan struct{})
	var out syncBuffer
	finished := make(chan struct{})
	go func() {
		drainLauncherOutput(r, &out, done, 5*time.Second)
		close(finished)
	}()

	_, _ = w.Write([]byte("starting\n"))
	close(done) // launcher 退出了，缓冲里还有输出
	_, _ = w.Write([]byte("exit code 3: missing dll\n"))
	_ = w.Close()

	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("读到结尾后没有返回")
	}
	if !strings.Contains(out.String(), "missing dll") {
		t.Fatalf("launcher 退出后的输出丢了: %q", out.String())
	}
}

// 继承了从端、比 launcher 活得久的进程会让 PTY 迟迟不到结尾：退出后至多再等 grace。
func TestDrainLauncherOutputBoundedAfterLauncherExit(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		drainLauncherOutput(r, nil, done, 100*time.Millisecond)
		close(finished)
	}()

	_, _ = w.Write([]byte("noise\n")) // nil dst 也要有人读，否则写方阻塞
	close(done)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("launcher 退出后 PTY 没有在 grace 内关闭")
	}
}
