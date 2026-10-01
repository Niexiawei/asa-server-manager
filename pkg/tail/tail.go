package tail

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"context"

	"github.com/fsnotify/fsnotify"
)

type Tailer struct {
	logPath    string
	lastNLines int
	ch         chan string

	ctx    context.Context
	cancel context.CancelFunc

	watcher *fsnotify.Watcher
	ticker  *time.Ticker

	mu      sync.Mutex
	offset  int64
	fileKey string

	// historyEnd 是 Start 那一刻文件的大小：历史回放只读到这里为止，
	// 之后写入的内容一律交给跟随阶段。
	historyEnd int64
}

// NewTailer creates a Tailer that watches logPath and returns a read-only channel
// that receives log lines. The last lastNLines historical lines are emitted first
// (inside the goroutine started by Start), followed by new lines as they appear.
func NewTailer(logPath string, lastNLines int) (*Tailer, <-chan string, error) {
	ctx, cancel := context.WithCancel(context.Background())

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		cancel()
		return nil, nil, err
	}

	dir := filepath.Dir(logPath)
	if err := watcher.Add(dir); err != nil {
		watcher.Close()
		cancel()
		return nil, nil, err
	}

	ch := make(chan string, 64)
	t := &Tailer{
		logPath:    logPath,
		lastNLines: lastNLines,
		ch:         ch,
		ctx:        ctx,
		cancel:     cancel,
		watcher:    watcher,
		ticker:     time.NewTicker(800 * time.Millisecond),
	}
	return t, ch, nil
}

// Start launches the background goroutine. Call this after the channel consumer
// is ready to receive, to avoid any buffering race.
//
// The tail position is fixed here, synchronously: every line written after
// Start returns is delivered. It used to be fixed inside the goroutine, so
// lines written between Start returning and the goroutine getting scheduled
// were skipped — and waitServerStartup, which waits for one specific line
// with lastNLines=0, could miss exactly that line.
func (t *Tailer) Start() {
	t.markStart()
	go t.loop()
}

// WithCallback tails logPath for the lifetime of ctx, invoking fn for each line.
// The last lastNLines historical lines are replayed first (0 means tail new lines
// only), followed by new lines as they appear. When ctx is cancelled the tailer is
// stopped and its resources released automatically.
func WithCallback(ctx context.Context, logPath string, lastNLines int, fn func(string)) error {
	t, ch, err := NewTailer(logPath, lastNLines)
	if err != nil {
		return err
	}
	t.Start()
	go func() {
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case line, ok := <-ch:
				if !ok {
					return
				}
				fn(line)
			}
		}
	}()
	return nil
}

// Stop cancels the background goroutine. The owned channel is closed when the
// goroutine exits, so the consumer will see ok=false and can return cleanly.
func (t *Tailer) Stop() {
	t.cancel()
}

func (t *Tailer) loop() {
	defer close(t.ch)
	t.replayHistory()

	for {
		select {
		case <-t.ctx.Done():
			t.cleanup()
			return

		case <-t.ticker.C:
			t.readNewLines()

		case event, ok := <-t.watcher.Events:
			if !ok {
				return
			}
			if filepath.Clean(event.Name) == filepath.Clean(t.logPath) {
				if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename|fsnotify.Remove) != 0 {
					// 写事件即时读取并回调：readNewLines 从当前 offset 读增量，
					// 内部通过 fileKey 比对自带轮转检测（轮转时会重置 offset 从头读），
					// 因此对所有事件类型都正确，无需在此单独 resetState。
					t.readNewLines()
				}
			}

		case err, ok := <-t.watcher.Errors:
			if !ok {
				return
			}
			t.send("watcher error: " + err.Error())
		}
	}
}

// send delivers a line to the channel, respecting cancellation so it never
// blocks after Stop() is called.
func (t *Tailer) send(line string) {
	select {
	case t.ch <- line:
	case <-t.ctx.Done():
	}
}

func (t *Tailer) readNewLines() {
	content, newOffset, rotated := t.readFromOffset()

	if rotated {
		t.resetState()
		return
	}

	if content != "" {
		for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
			if line != "" {
				t.send(line)
			}
		}
	}

	t.mu.Lock()
	t.offset = newOffset
	t.mu.Unlock()
}

func (t *Tailer) readFromOffset() (string, int64, bool) {
	t.mu.Lock()
	oldKey := t.fileKey
	offset := t.offset
	t.mu.Unlock()

	f, err := os.Open(t.logPath)
	if err != nil {
		return "", offset, false
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", offset, false
	}

	currentKey := fileKey(fi)

	if oldKey != "" && currentKey != oldKey {
		return "", 0, true
	}

	if fi.Size() < offset {
		offset = 0
	}

	_, err = f.Seek(offset, io.SeekStart)
	if err != nil {
		return "", offset, false
	}

	buf := make([]byte, 64*1024)
	n, _ := f.Read(buf)
	if n == 0 {
		return "", offset, false
	}

	return string(buf[:n]), offset + int64(n), false
}

// markStart records where tailing begins: the current end of file, which is
// also where the history replay stops. A missing file starts at offset 0 with
// no identity, so it is read from the beginning once it appears.
func (t *Tailer) markStart() {
	f, err := os.Open(t.logPath)
	if err != nil {
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return
	}

	t.mu.Lock()
	t.offset = fi.Size()
	t.fileKey = fileKey(fi)
	t.historyEnd = fi.Size()
	t.mu.Unlock()
}

// replayHistory emits the last N lines that existed when Start was called.
// It reads only up to that point: reading to the current end of file would
// emit lines written since as history *and* again when the follow phase
// reaches them.
func (t *Tailer) replayHistory() {
	if t.lastNLines <= 0 {
		return
	}
	t.mu.Lock()
	end := t.historyEnd
	t.mu.Unlock()
	if end <= 0 {
		return
	}
	lines, _ := readLastNLines(t.logPath, t.lastNLines, end)
	for _, line := range lines {
		if line != "" {
			t.send(line)
		}
	}
}

// resetState is called on rotation. Resets offset to 0 so the new file is
// tailed from the beginning.
func (t *Tailer) resetState() {
	f, err := os.Open(t.logPath)
	if err != nil {
		t.mu.Lock()
		t.fileKey = ""
		t.offset = 0
		t.mu.Unlock()
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return
	}

	t.mu.Lock()
	t.offset = 0
	t.fileKey = fileKey(fi)
	t.mu.Unlock()
}

func (t *Tailer) cleanup() {
	if t.watcher != nil {
		t.watcher.Close()
	}
	if t.ticker != nil {
		t.ticker.Stop()
	}
}

// readLastNLines returns the last n lines among filePath's first end bytes
// (the whole file when end exceeds it).
// Pass 1: scan backward counting \n to find the start offset.
// Pass 2: read forward from that offset up to end and split.
func readLastNLines(filePath string, n int, end int64) ([]string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := min(fi.Size(), end)
	if size <= 0 {
		return []string{}, nil
	}

	const bufSize = 4096
	buf := make([]byte, bufSize)
	pos := size
	newlines := 0
	startPos := int64(0)

outer:
	for pos > 0 {
		toRead := int64(bufSize)
		if pos < toRead {
			toRead = pos
		}
		pos -= toRead

		if _, err := f.Seek(pos, io.SeekStart); err != nil {
			return nil, err
		}
		m, err := f.Read(buf[:toRead])
		if err != nil && err != io.EOF {
			return nil, err
		}

		for i := m - 1; i >= 0; i-- {
			if buf[i] != '\n' {
				continue
			}
			// ignore a trailing newline at the very end of the file
			if pos+int64(i) == size-1 {
				continue
			}
			newlines++
			if newlines == n {
				startPos = pos + int64(i) + 1
				break outer
			}
		}
	}

	if _, err := f.Seek(startPos, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, size-startPos))
	if err != nil {
		return nil, err
	}

	content := strings.TrimRight(string(data), "\r\n")
	if content == "" {
		return []string{}, nil
	}
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	return lines, nil
}
