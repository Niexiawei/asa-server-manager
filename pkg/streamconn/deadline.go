package streamconn

import (
	"sync"
	"time"
)

// deadline 是一个可重设的截止时刻：到期时 wait() 返回的通道关闭。
// 思路同标准库 net.Pipe 内部的 pipeDeadline。
type deadline struct {
	mu     sync.Mutex
	timer  *time.Timer
	cancel chan struct{} // 到期时关闭；未设截止时刻时为 nil
}

func newDeadline() *deadline { return &deadline{} }

// set 设置截止时刻；零值表示取消。
func (d *deadline) set(t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil && !d.timer.Stop() {
		// 计时器已经触发：通道已关闭，下面会换一条新的。
		d.cancel = nil
	}
	d.timer = nil

	if t.IsZero() {
		d.cancel = nil
		return
	}
	if d.cancel == nil {
		d.cancel = make(chan struct{})
	} else {
		select {
		case <-d.cancel:
			d.cancel = make(chan struct{})
		default:
		}
	}
	if dur := time.Until(t); dur > 0 {
		ch := d.cancel
		d.timer = time.AfterFunc(dur, func() { close(ch) })
		return
	}
	close(d.cancel)
}

// wait 返回到期时关闭的通道；没设截止时刻时返回 nil（select 里永远不就绪）。
func (d *deadline) wait() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cancel == nil {
		return nil
	}
	return d.cancel
}
