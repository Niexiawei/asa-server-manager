// Package streamconn 把一条「消息流」（典型是 gRPC 双向流）适配成 net.Conn：
// 读侧缓冲一条消息里没读完的字节，写侧按 MaxFrame 分帧，支持 deadline 与幂等的 Close。
//
// 上层（TLS、gRPC）因此可以跑在经协调节点中转的流上，不知道它不是一条 TCP 连接
// （docs/REMOTE_MANAGER_MESH_PLAN.md §5.5）。
//
// 本包只依赖标准库：流的收发以函数注入（Options），不认识 gRPC 的具体类型，
// 也不认识任何领域概念。
package streamconn

import (
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// MaxFrame 是写侧单条消息的最大字节数。gRPC 默认的单消息上限是 4 MiB，
// 32 KiB 远低于它，又足够摊薄每条消息的帧开销。
const MaxFrame = 32 << 10

// gracePeriod 是 Close 的优雅阶段上限：等在途的写完成、发出半关闭、等对端也关闭。
// 超时就强制中止（Options.Abort）。
const gracePeriod = 10 * time.Second

// Options 描述底层的流。
type Options struct {
	// Recv 阻塞读取下一条消息。流正常结束时返回 io.EOF。
	// 返回的切片归调用方所有（本包会持有它直到读完）。
	Recv func() ([]byte, error)
	// Send 发送一条消息。返回后本包可能复用传入的切片——gRPC 的 SendMsg 在返回前已经
	// 完成序列化，满足这个要求。Send 只会被串行调用。
	Send func([]byte) error
	// CloseSend 发出半关闭（gRPC 客户端流的 CloseSend）。服务端流没有它，留 nil。
	CloseSend func() error
	// Abort 立刻中止整条流，让阻塞中的 Recv / Send 返回（客户端流 = 取消 ctx）。
	// 服务端流留 nil：服务端 handler 应在 Done() 关闭后返回，返回本身就结束了流。
	Abort func()

	LocalAddr, RemoteAddr net.Addr
}

// Conn 是流上的 net.Conn。
type Conn struct {
	opts Options

	// 读侧：pump goroutine 把 Recv 到的消息送进 msgs；Read 消费它，没读完的留在 pending。
	readMu  sync.Mutex
	pending []byte
	msgs    chan []byte
	pumpErr error         // pump 结束的原因；pumpDone 关闭后才可读
	pumpEnd chan struct{} // pump 结束时关闭

	writeMu sync.Mutex

	readDL  *deadline
	writeDL *deadline

	closeOnce sync.Once
	closing   chan struct{} // Close 被调用时关闭
	done      chan struct{} // Close 的收尾（含优雅阶段）完成时关闭
	aborted   atomic.Bool
}

// New 在流上建一个 Conn，并开始在后台读取。
func New(opts Options) *Conn {
	if opts.LocalAddr == nil {
		opts.LocalAddr = Addr("local")
	}
	if opts.RemoteAddr == nil {
		opts.RemoteAddr = Addr("remote")
	}
	c := &Conn{
		opts:    opts,
		msgs:    make(chan []byte, 1),
		pumpEnd: make(chan struct{}),
		readDL:  newDeadline(),
		writeDL: newDeadline(),
		closing: make(chan struct{}),
		done:    make(chan struct{}),
	}
	go c.pump()
	return c
}

// pump 是唯一调用 Recv 的 goroutine。msgs 只有 1 格缓冲：Read 跟不上时它就停在这里不再
// Recv，背压经底层流（gRPC 的流控）传回对端，不会在本进程里无限堆积。
func (c *Conn) pump() {
	defer close(c.pumpEnd)
	for {
		b, err := c.opts.Recv()
		if err != nil {
			c.pumpErr = err
			return
		}
		if len(b) == 0 {
			continue
		}
		select {
		case c.msgs <- b:
		case <-c.closing:
			c.pumpErr = net.ErrClosed
			return
		}
	}
}

// Read 实现 net.Conn。
func (c *Conn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	if c.isClosing() {
		return 0, net.ErrClosed
	}
	if len(p) == 0 {
		return 0, nil
	}
	if len(c.pending) == 0 {
		select {
		case b := <-c.msgs:
			c.pending = b
		default:
			select {
			case b := <-c.msgs:
				c.pending = b
			case <-c.pumpEnd:
				// pump 结束前可能刚好塞进了最后一条消息。
				select {
				case b := <-c.msgs:
					c.pending = b
				default:
					return 0, c.readErr()
				}
			case <-c.readDL.wait():
				return 0, os.ErrDeadlineExceeded
			case <-c.closing:
				return 0, net.ErrClosed
			}
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *Conn) readErr() error {
	if c.isClosing() {
		return net.ErrClosed
	}
	if c.pumpErr == nil || errors.Is(c.pumpErr, io.EOF) {
		return io.EOF
	}
	return c.pumpErr
}

// Write 实现 net.Conn。按 MaxFrame 分帧发送。
//
// 写超时会**中止整条流**：阻塞中的 Send 无法单独打断，而写到一半的帧已经让字节流处于
// 未定义状态——net.Conn 对写超时的约定本来就是如此，crypto/tls 在写超时后也会把连接
// 判为永久损坏。deadline 在 Write 开始时读取；Write 阻塞期间再调 SetWriteDeadline 不生效。
func (c *Conn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if c.isClosing() || c.aborted.Load() {
		return 0, net.ErrClosed
	}
	var timedOut atomic.Bool
	if ch := c.writeDL.wait(); ch != nil {
		select {
		case <-ch:
			return 0, os.ErrDeadlineExceeded
		default:
		}
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			select {
			case <-ch:
				timedOut.Store(true)
				c.abort()
			case <-stop:
			}
		}()
	}

	written := 0
	for written < len(p) {
		if c.isClosing() {
			return written, net.ErrClosed
		}
		end := min(written+MaxFrame, len(p))
		if err := c.opts.Send(p[written:end]); err != nil {
			if timedOut.Load() {
				return written, os.ErrDeadlineExceeded
			}
			return written, err
		}
		written = end
	}
	return written, nil
}

// Close 实现 net.Conn：幂等，立刻返回。之后的 Read / Write 返回 net.ErrClosed。
//
// 收尾在后台进行：等在途的写完成 → 半关闭 → 等对端也关闭，整个优雅阶段最多
// gracePeriod，超时则 Abort。直接 Abort 会让 gRPC 丢掉已经 Send 但还在缓冲里的数据
// （例如 TLS 的 close_notify、一个 HTTP 响应的最后几个字节）。
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closing)
		go c.shutdown()
	})
	return nil
}

func (c *Conn) shutdown() {
	defer close(c.done)
	force := time.AfterFunc(gracePeriod, c.abort)
	defer force.Stop()

	c.writeMu.Lock()
	if c.opts.CloseSend != nil && !c.aborted.Load() {
		_ = c.opts.CloseSend()
	}
	c.writeMu.Unlock()

	if c.opts.CloseSend != nil {
		// 客户端流：等对端也关闭（pump 读到 EOF），再中止以释放资源。
		<-c.pumpEnd
	}
	c.abort()
}

func (c *Conn) abort() {
	if c.aborted.Swap(true) {
		return
	}
	if c.opts.Abort != nil {
		c.opts.Abort()
	}
}

// Done 在 Close 的收尾完成后关闭。服务端 handler 应等它关闭后再返回
// （或在流的 ctx 结束时返回）。
func (c *Conn) Done() <-chan struct{} { return c.done }

// Closing 在 Close 被调用时立即关闭。
func (c *Conn) Closing() <-chan struct{} { return c.closing }

func (c *Conn) isClosing() bool {
	select {
	case <-c.closing:
		return true
	default:
		return false
	}
}

func (c *Conn) LocalAddr() net.Addr  { return c.opts.LocalAddr }
func (c *Conn) RemoteAddr() net.Addr { return c.opts.RemoteAddr }

func (c *Conn) SetDeadline(t time.Time) error {
	c.readDL.set(t)
	c.writeDL.set(t)
	return nil
}

func (c *Conn) SetReadDeadline(t time.Time) error  { c.readDL.set(t); return nil }
func (c *Conn) SetWriteDeadline(t time.Time) error { c.writeDL.set(t); return nil }

// Addr 是没有真实网络地址时用的占位地址。
type Addr string

func (a Addr) Network() string { return "stream" }
func (a Addr) String() string  { return string(a) }

var _ net.Conn = (*Conn)(nil)
