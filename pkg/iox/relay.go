package iox

import (
	"context"
	"errors"
	"io"
	"time"
)

// Relay copies everything written to src into dst as it arrives, until src
// reaches EOF after ctx is done. It polls rather than relying on a blocking
// Read that reacts to cancellation — a plain os.File has no way to interrupt
// a Read in progress, so this is the only cancellable shape that works for
// arbitrary io.Reader sources.
//
// note, if non-nil, receives a one-line description of why Relay returned
// early (a write error, or a read error other than EOF). Relay does no
// logging itself — it does not know what the caller's log format looks
// like — so a caller that wants this surfaced wires note to its own logger.
func Relay(ctx context.Context, src io.Reader, dst io.Writer, poll time.Duration, note func(string)) {
	buf := make([]byte, 32*1024)
	finishing := false
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				if note != nil {
					note("relay write failed: " + werr.Error())
				}
				return
			}
			continue // 还有内容就接着读，别急着去睡
		}
		if err != nil && !errors.Is(err, io.EOF) {
			if note != nil {
				note("relay read failed: " + err.Error())
			}
			return
		}
		if finishing {
			return // 收尾这一轮已经读到 EOF，真的没有了
		}
		select {
		case <-ctx.Done():
			// ctx 已结束：再跑一轮把尾巴读干净，然后退出。
			finishing = true
		case <-time.After(poll):
		}
	}
}
