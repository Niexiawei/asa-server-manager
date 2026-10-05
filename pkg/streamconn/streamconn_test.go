package streamconn

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errAborted = errors.New("stream aborted")

// memStream 是内存里的一条双向流：两个方向各一个带缓冲的通道。
// abort 模拟 gRPC 的取消：双方阻塞中的收发立即出错。
type memStream struct {
	a2b, b2a  chan []byte
	broken    chan struct{}
	breakOnce sync.Once
	closeA    sync.Once
	closeB    sync.Once
	frames    atomic.Int64 // A→B 方向发出的帧数
}

func newMemStream(buf int) *memStream {
	return &memStream{a2b: make(chan []byte, buf), b2a: make(chan []byte, buf), broken: make(chan struct{})}
}

func (m *memStream) abort() { m.breakOnce.Do(func() { close(m.broken) }) }

func (m *memStream) side(send, recv chan []byte, once *sync.Once, countFrames bool) Options {
	return Options{
		Send: func(b []byte) error {
			if len(b) > MaxFrame {
				panic("帧超过 MaxFrame")
			}
			cp := append([]byte(nil), b...) // Send 返回后调用方会复用 b
			select {
			case send <- cp:
				if countFrames {
					m.frames.Add(1)
				}
				return nil
			case <-m.broken:
				return errAborted
			}
		},
		Recv: func() ([]byte, error) {
			select {
			case b, ok := <-recv:
				if !ok {
					return nil, io.EOF
				}
				return b, nil
			case <-m.broken:
				return nil, errAborted
			}
		},
		CloseSend: func() error { once.Do(func() { close(send) }); return nil },
		Abort:     m.abort,
	}
}

func pair(t *testing.T, buf int) (a, b *Conn, m *memStream) {
	t.Helper()
	m = newMemStream(buf)
	a = New(m.side(m.a2b, m.b2a, &m.closeA, true))
	b = New(m.side(m.b2a, m.a2b, &m.closeB, false))
	t.Cleanup(func() { a.Close(); b.Close(); m.abort() })
	return a, b, m
}

func TestLargeWriteIsFramedAndReassembled(t *testing.T) {
	a, b, m := pair(t, 64)
	payload := make([]byte, 1<<20)
	rand.Read(payload)

	errc := make(chan error, 1)
	go func() {
		n, err := a.Write(payload)
		if err == nil && n != len(payload) {
			err = io.ErrShortWrite
		}
		errc <- err
	}()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(b, got); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("重组后的数据不一致")
	}
	if want := int64(len(payload) / MaxFrame); m.frames.Load() != want {
		t.Fatalf("帧数 = %d，应为 %d", m.frames.Load(), want)
	}
}

func TestConcurrentBidirectional(t *testing.T) {
	a, b, _ := pair(t, 4)
	const total = 2 << 20
	var wg sync.WaitGroup
	check := func(w, r net.Conn) {
		defer wg.Done()
		src := make([]byte, total)
		rand.Read(src)
		var inner sync.WaitGroup
		inner.Add(1)
		go func() {
			defer inner.Done()
			for off := 0; off < total; off += 7777 {
				if _, err := w.Write(src[off:min(off+7777, total)]); err != nil {
					t.Error(err)
					return
				}
			}
		}()
		dst := make([]byte, total)
		if _, err := io.ReadFull(r, dst); err != nil {
			t.Error(err)
		}
		inner.Wait()
		if !bytes.Equal(src, dst) {
			t.Error("数据不一致")
		}
	}
	wg.Add(2)
	go check(a, b)
	go check(b, a)
	wg.Wait()
}

func TestPeerCloseGivesEOF(t *testing.T) {
	a, b, _ := pair(t, 4)
	if _, err := a.Write([]byte("bye")); err != nil {
		t.Fatal(err)
	}
	a.Close()
	got, err := io.ReadAll(b)
	if err != nil {
		t.Fatalf("对端关闭后应读到 io.EOF（ReadAll 返回 nil），得到 %v", err)
	}
	if string(got) != "bye" {
		t.Fatalf("关闭前写入的数据丢了：%q", got)
	}
	// B 也关闭后，A 的优雅阶段结束。
	b.Close()
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("双方都关闭后 A 的收尾应很快完成")
	}
}

func TestReadDeadline(t *testing.T) {
	a, b, _ := pair(t, 4)
	b.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	start := time.Now()
	_, err := b.Read(make([]byte, 1))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("应为 os.ErrDeadlineExceeded，得到 %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("deadline 没有及时生效")
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatal("超时错误应满足 net.Error.Timeout()")
	}

	// 读超时不破坏连接：清掉 deadline 后照常收数据。
	b.SetReadDeadline(time.Time{})
	go a.Write([]byte("x"))
	buf := make([]byte, 1)
	if _, err := io.ReadFull(b, buf); err != nil || buf[0] != 'x' {
		t.Fatalf("清除 deadline 后读取失败：%v", err)
	}

	// 已经过去的时刻立即超时。
	b.SetReadDeadline(time.Now().Add(-time.Second))
	if _, err := b.Read(buf); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("过去的 deadline 应立即超时，得到 %v", err)
	}
}

func TestWriteDeadlineAbortsBlockedSend(t *testing.T) {
	// 无缓冲且对端应用层不读：对端 pump 收下两帧（一帧进 msgs、一帧拿在手里）之后，
	// 第三帧的 Send 会一直阻塞。
	a, _, m := pair(t, 0)
	a.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
	_, err := a.Write(make([]byte, 4*MaxFrame))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("应为 os.ErrDeadlineExceeded，得到 %v", err)
	}
	select {
	case <-m.broken:
	default:
		t.Fatal("写超时应中止整条流")
	}
	if _, err := a.Write([]byte("again")); err == nil {
		t.Fatal("流被中止后写应失败")
	}
}

func TestCloseIsIdempotentAndUnblocks(t *testing.T) {
	a, _, _ := pair(t, 4)
	readErr := make(chan error, 1)
	go func() {
		_, err := a.Read(make([]byte, 1))
		readErr <- err
	}()
	time.Sleep(20 * time.Millisecond)
	a.Close()
	a.Close()
	select {
	case err := <-readErr:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Close 后阻塞中的 Read 应返回 net.ErrClosed，得到 %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close 没有唤醒阻塞中的 Read")
	}
	if _, err := a.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Close 后 Write 应返回 net.ErrClosed，得到 %v", err)
	}
}
