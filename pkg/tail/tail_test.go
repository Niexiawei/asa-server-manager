package tail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// lineWait 是等一行日志出现的上限。Tailer 靠 fsnotify 即时读、800ms 的 ticker 兜底，
// 正常几十毫秒内就到；给宽一些，避免慢机器上误报。
const lineWait = 5 * time.Second

// expectLines 按顺序从 ch 读出 want 里的每一行，超时或内容不符即失败。
func expectLines(t *testing.T, ch <-chan string, want ...string) {
	t.Helper()
	for _, w := range want {
		select {
		case got, ok := <-ch:
			if !ok {
				t.Fatalf("channel 提前关闭，还在等 %q", w)
			}
			if got != w {
				t.Fatalf("收到 %q，期望 %q", got, w)
			}
		case <-time.After(lineWait):
			t.Fatalf("%s 内没有收到 %q", lineWait, w)
		}
	}
}

// expectClosed 断言 Stop 之后 channel 会被关闭（后台协程确实退出了），
// 并且关闭前没有多出来的行。
func expectClosed(t *testing.T, ch <-chan string) {
	t.Helper()
	deadline := time.After(lineWait)
	for {
		select {
		case line, ok := <-ch:
			if !ok {
				return
			}
			t.Errorf("多交出了一行 %q", line)
		case <-deadline:
			t.Fatal("Stop 之后 channel 没有关闭，后台协程没有退出")
		}
	}
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

func startTailer(t *testing.T, path string, lastN int) (*Tailer, <-chan string) {
	t.Helper()
	tailer, ch, err := NewTailer(path, lastN)
	if err != nil {
		t.Fatalf("NewTailer: %v", err)
	}
	tailer.Start()
	t.Cleanup(tailer.Stop)
	return tailer, ch
}

// 先回放最后 N 行历史，再跟随新追加的行；Stop 后 channel 关闭。
func TestTailer_ReplaysLastNThenFollows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	appendLines(t, path, "a", "b", "c", "d")

	tailer, ch := startTailer(t, path, 2)
	expectLines(t, ch, "c", "d")

	appendLines(t, path, "e", "f")
	expectLines(t, ch, "e", "f")

	tailer.Stop()
	expectClosed(t, ch)
}

// Start 返回之后立刻写入的行必须交出来，而且只交一次：它不属于历史（历史只读到
// Start 那一刻为止），由跟随阶段交出。起点曾在后台协程里才确定，这一行会被当成
// 已读直接跳过，或者被历史回放读到之后跟随阶段再交一次。
func TestTailer_LinesWrittenRightAfterStartAreDeliveredOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	appendLines(t, path, "h1", "h2")

	tailer, ch := startTailer(t, path, 10)
	appendLines(t, path, "right-after-start")
	expectLines(t, ch, "h1", "h2", "right-after-start")

	tailer.Stop()
	expectClosed(t, ch)
}

// lastNLines 为 0 时不回放历史，只给之后新写的行（等待启动日志的调用方就是这样用的）。
func TestTailer_NoHistoryWhenLastNIsZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	appendLines(t, path, "old1", "old2")

	_, ch := startTailer(t, path, 0)
	appendLines(t, path, "new")
	expectLines(t, ch, "new")
}

// 日志文件在 Tailer 启动之后才出现（实例第一次启动前就打开了日志面板）。
func TestTailer_FileCreatedAfterStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")

	_, ch := startTailer(t, path, 10)
	appendLines(t, path, "first", "second")
	expectLines(t, ch, "first", "second")
}

// 日志被轮转（旧文件改名、同名新建）后，从新文件的开头继续读。
//
// 新文件故意比旧文件短：Linux 上靠 inode 变化认出轮转；Windows 上 fileKey 是创建时间，
// 而 NTFS 的「文件名隧道」会让 15 秒内同名新建的文件继承旧文件的创建时间，认不出轮转，
// 此时靠「文件比已读偏移量短 → 从头读」兜底。两条路径都必须把新内容交出来。
func TestTailer_FollowsRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.log")
	appendLines(t, path, "before-rotation-line-1", "before-rotation-line-2")

	_, ch := startTailer(t, path, 0)
	appendLines(t, path, "still-old-file")
	expectLines(t, ch, "still-old-file")

	if err := os.Rename(path, filepath.Join(dir, "server.log.1")); err != nil {
		t.Fatal(err)
	}
	appendLines(t, path, "new1")
	expectLines(t, ch, "new1")

	appendLines(t, path, "new2")
	expectLines(t, ch, "new2")
}

// 目录不存在时 NewTailer 直接报错，而不是返回一个永远收不到东西的 Tailer。
func TestNewTailer_MissingDirFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-dir", "server.log")
	tailer, ch, err := NewTailer(path, 10)
	if err == nil {
		tailer.Stop()
		t.Fatal("目录不存在时 NewTailer 应当返回错误")
	}
	if tailer != nil || ch != nil {
		t.Error("出错时不应返回 Tailer 或 channel")
	}
}

func TestReadLastNLines(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name    string
		content string
		n       int
		end     int64 // 0 = 读到文件末尾
		want    []string
	}{
		{"空文件", "", 3, 0, []string{}},
		{"行数少于 N", "a\nb\n", 5, 0, []string{"a", "b"}},
		{"取最后 N 行", "a\nb\nc\nd\n", 2, 0, []string{"c", "d"}},
		{"没有结尾换行", "a\nb\nc", 2, 0, []string{"b", "c"}},
		{"CRLF 换行", "a\r\nb\r\nc\r\n", 2, 0, []string{"b", "c"}},
		// 超过一个 4KiB 读块，验证跨块向前扫描。
		{"跨读块", strings.Repeat("x", 5000) + "\nlast\n", 1, 0, []string{"last"}},
		// 只看前 end 个字节：Start 之后才写进来的行不属于历史。
		{"只读到 end 为止", "a\nb\nc\nd\n", 2, 4, []string{"a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "_")+".log")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			end := tc.end
			if end == 0 {
				end = int64(len(tc.content))
			}
			got, err := readLastNLines(path, tc.n, end)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) || strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("readLastNLines = %q，期望 %q", got, tc.want)
			}
		})
	}
}
