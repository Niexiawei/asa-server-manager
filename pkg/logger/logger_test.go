package logger

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureConsole redirects os.Stdout to a pipe for the duration of fn and
// returns everything written to it. fn is expected to call InitLoggerWithBaseDir
// (or otherwise cause the console core to be (re)built) after the swap so the
// core captures the redirected os.Stdout, not the original one.
func captureConsole(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func readLogFile(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(GetLogFilePath())
	if err != nil {
		t.Fatalf("read log file %s: %v", GetLogFilePath(), err)
	}
	return string(data)
}

func resetLevel(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { SetLevel("info") })
}

// initForTest wires InitLoggerWithBaseDir to a fresh temp dir and makes sure
// the file handle is released before that dir gets removed: t.TempDir()'s own
// cleanup is registered first and runs LIFO, so registering Close() after it
// here guarantees close-then-remove ordering (Windows refuses to delete a
// file that's still open, and lumberjack never closes it on its own).
func initForTest(t *testing.T, opts ...Option) string {
	t.Helper()
	dir := t.TempDir()
	InitLoggerWithBaseDir(dir, opts...)
	t.Cleanup(func() { _ = Close() })
	return dir
}

func TestWithConsole_VisibleOnConsoleEvenBelowFileThreshold(t *testing.T) {
	resetLevel(t)

	console := captureConsole(t, func() {
		initForTest(t)
		// 先写一条够级别的消息，确保文件真的被 lumberjack 建出来——lumberjack 是
		// 惰性建文件的，第一次写入才创建，纯低于阈值的调用不会触发创建。
		Info("bootstrap-line-forces-file-creation")
		// Debug 低于默认 Info 阈值：验证 WithConsole() 上屏不受文件级别阈值影响。
		WithConsole().Debug("console-should-see-this-debug-line")
	})

	if !strings.Contains(console, "console-should-see-this-debug-line") {
		t.Fatalf("expected WithConsole() output on stdout regardless of file level threshold, got: %q", console)
	}

	file := readLogFile(t)
	if strings.Contains(file, "console-should-see-this-debug-line") {
		t.Fatal("expected the file sink to NOT contain a below-threshold Debug message")
	}
}

func TestPlainCall_FileOnlyNotOnConsole(t *testing.T) {
	resetLevel(t)

	console := captureConsole(t, func() {
		initForTest(t)
		Info("plain-info-file-only-line")
	})

	if strings.Contains(console, "plain-info-file-only-line") {
		t.Fatalf("expected a plain (non-WithConsole) call to NOT appear on stdout, got: %q", console)
	}

	file := readLogFile(t)
	if !strings.Contains(file, "plain-info-file-only-line") {
		t.Fatal("expected a plain Info call to be written to the file sink")
	}
}

func TestSetLevel_UnlocksDebugInFile(t *testing.T) {
	resetLevel(t)
	initForTest(t)

	// 先写一条够级别的消息，确保文件真的被建出来（同 TestWithConsole_...），
	// 否则下面第一次检查会因为文件压根不存在而失败，而不是因为内容不含它。
	Info("bootstrap-line-forces-file-creation")

	Debug("debug-before-setlevel-should-be-dropped")
	file := readLogFile(t)
	if strings.Contains(file, "debug-before-setlevel-should-be-dropped") {
		t.Fatal("expected Debug to be dropped before SetLevel(\"debug\")")
	}

	SetLevel("debug")
	Debug("debug-after-setlevel-should-be-written")
	file = readLogFile(t)
	if !strings.Contains(file, "debug-after-setlevel-should-be-written") {
		t.Fatal("expected Debug to reach the file after SetLevel(\"debug\")")
	}
}

func TestWithConsole_ComposesWithNamedInEitherOrder(t *testing.T) {
	resetLevel(t)

	console := captureConsole(t, func() {
		initForTest(t)
		Named("componentA").WithConsole().Info("named-then-console-line")
		WithConsole().Named("componentB").Info("console-then-named-line")
	})

	for _, want := range []string{"named-then-console-line", "console-then-named-line", "componentA", "componentB"} {
		if !strings.Contains(console, want) {
			t.Errorf("expected console output to contain %q, got: %q", want, console)
		}
	}

	file := readLogFile(t)
	for _, want := range []string{"named-then-console-line", "console-then-named-line"} {
		if !strings.Contains(file, want) {
			t.Errorf("expected file output to contain %q, got: %q", want, file)
		}
	}
}

// TestFileEncoding_KeepsDefaultZapKeyNames pins down the compatibility
// constraint in docs/LOGGER_REDESIGN_PLAN.md §3: app/src/views/SystemLogs.vue
// parses each log line as JSON.parse(line) and reads log.ts/log.level/log.msg
// directly (zap's *default* JSON key names). Changing those key names (like
// the reference package's TimeKey = "time") would silently break that page.
func TestFileEncoding_KeepsDefaultZapKeyNames(t *testing.T) {
	resetLevel(t)
	initForTest(t)

	Info("key-name-regression-check")

	file := readLogFile(t)
	line := ""
	for _, l := range strings.Split(strings.TrimSpace(file), "\n") {
		if strings.Contains(l, "key-name-regression-check") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("could not find the test log line in %s", file)
	}

	for _, key := range []string{`"ts"`, `"level"`, `"msg"`} {
		if !strings.Contains(line, key) {
			t.Errorf("expected log line to contain default zap key %s, got: %s", key, line)
		}
	}
}

func TestGetLogFilePath_MatchesBaseDir(t *testing.T) {
	resetLevel(t)
	dir := initForTest(t)

	want := filepath.Join(dir, "logs", defaultLogFileName)
	if got := GetLogFilePath(); got != want {
		t.Fatalf("GetLogFilePath() = %q, want %q", got, want)
	}
}

func TestWithLogFileName_OverridesDefault(t *testing.T) {
	resetLevel(t)
	dir := initForTest(t, WithLogFileName("custom.log"))

	want := filepath.Join(dir, "logs", "custom.log")
	if got := GetLogFilePath(); got != want {
		t.Fatalf("GetLogFilePath() = %q, want %q", got, want)
	}
	Info("custom-file-name-line")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected %s to exist: %v", want, err)
	}
}
