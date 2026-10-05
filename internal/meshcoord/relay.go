package meshcoord

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
)

// relay 是一个中转会话：A 发起，B 被通知；双方各开一条 Relay 流，协调节点把两条流的
// data 帧对拷。协调节点只搬不透明字节——之上是 A、B 之间的端到端 mTLS（§5.4）。
type relay struct {
	id      string
	network string
	nodes   [2]meshid.ID // [0] = 发起方 A，[1] = 被叫方 B
	tokens  [2][]byte
	joined  [2]bool
	ends    [2]*relayEnd
	dead    bool // 已作废（过期或结束），不再接受 Join。受 Server.mu 保护

	paired  chan struct{} // 双方都 Join 后关闭
	expired chan struct{} // 配对期限到了仍未配齐时关闭
	done    chan struct{} // 会话结束（任一侧结束、空闲超时、服务关闭）时关闭

	expiry       *time.Timer
	finishOnce   sync.Once
	lastActivity atomic.Int64 // UnixNano
	pairedAt     atomic.Int64 // UnixNano，双方都 Join 的时刻；0 = 没配齐
	bytes        atomic.Int64 // 双向转发的字节数（结束时记一行，便于运维与排障）
}

// relayEnd 是一侧的流。只有**对侧**的接收循环会往它发数据；sendMu + closed 保证
// handler 返回之后不会再有人对这条流调 Send（gRPC 不允许）。
type relayEnd struct {
	stream meshpb.Coordinator_RelayServer
	sendMu sync.Mutex
	closed bool
}

func (e *relayEnd) send(data []byte) error {
	e.sendMu.Lock()
	defer e.sendMu.Unlock()
	if e.closed {
		return context.Canceled
	}
	return e.stream.Send(&meshpb.RelayFrame{Msg: &meshpb.RelayFrame_Data{Data: data}})
}

func (e *relayEnd) close() {
	e.sendMu.Lock()
	e.closed = true
	e.sendMu.Unlock()
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// OpenRelay 实现中转申请：通过对端的 Session 下发 IncomingRelay，返回本端凭据。
// 凭据一次性，RelayJoinTimeout 内双方都未 Join 则作废。
func (s *Server) OpenRelay(ctx context.Context, req *meshpb.OpenRelayRequest) (*meshpb.OpenRelayResponse, error) {
	caller, err := s.callerSession(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	target, err := s.lookupLocked(caller, req.GetTargetNodeId())
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if target.id == caller.id {
		s.mu.Unlock()
		return nil, status.Error(codes.InvalidArgument, "不能中转到自己")
	}
	limit := s.opts.Limits.MaxRelaysPerNode
	if s.relayCount[caller.id] >= limit || s.relayCount[target.id] >= limit {
		s.mu.Unlock()
		return nil, status.Errorf(codes.ResourceExhausted, "中转会话数已达上限（每节点 %d）", limit)
	}
	r := &relay{
		id:      hex.EncodeToString(randomBytes(16)),
		network: caller.network,
		nodes:   [2]meshid.ID{caller.id, target.id},
		tokens:  [2][]byte{randomBytes(32), randomBytes(32)},
		paired:  make(chan struct{}),
		expired: make(chan struct{}),
		done:    make(chan struct{}),
	}
	s.relays[r.id] = r
	s.relayCount[caller.id]++
	s.relayCount[target.id]++
	r.expiry = time.AfterFunc(s.opts.RelayJoinTimeout, func() { s.expireRelay(r) })
	s.mu.Unlock()

	ok := target.push(&meshpb.CoordMessage{Msg: &meshpb.CoordMessage_IncomingRelay{IncomingRelay: &meshpb.IncomingRelay{
		SessionId:  r.id,
		FromNodeId: caller.id.Compact(),
		Token:      r.tokens[1],
	}}})
	if !ok {
		s.finishRelay(r)
		return nil, status.Error(codes.Unavailable, "对端暂时无法接收通知，请稍后重试")
	}
	return &meshpb.OpenRelayResponse{SessionId: r.id, Token: r.tokens[0]}, nil
}

// expireRelay：配对期限到了仍有一方没来，作废。
func (s *Server) expireRelay(r *relay) {
	s.mu.Lock()
	complete := r.joined[0] && r.joined[1]
	if !complete {
		r.dead = true
	}
	s.mu.Unlock()
	if complete {
		return
	}
	close(r.expired)
	s.finishRelay(r)
}

// finishRelay 结束会话并释放计数，幂等。
func (s *Server) finishRelay(r *relay) {
	r.finishOnce.Do(func() {
		r.expiry.Stop()
		close(r.done)
		s.mu.Lock()
		r.dead = true
		if s.relays[r.id] == r {
			delete(s.relays, r.id)
		}
		for _, n := range r.nodes {
			if s.relayCount[n]--; s.relayCount[n] <= 0 {
				delete(s.relayCount, n)
			}
		}
		s.mu.Unlock()
		if at := r.pairedAt.Load(); at != 0 {
			logger.Infof("[coord] 中转 %s 结束：%s → %s，持续 %s，转发 %d 字节", r.id[:8], r.nodes[0].Short(), r.nodes[1].Short(),
				time.Since(time.Unix(0, at)).Round(time.Second), r.bytes.Load())
		}
	})
}

// Relay 实现中转字节流：首帧 join，之后对拷 data 帧。
func (s *Server) Relay(stream meshpb.Coordinator_RelayServer) error {
	ctx := stream.Context()
	id, _, err := callerID(ctx)
	if err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	join := first.GetJoin()
	if join == nil {
		return status.Error(codes.InvalidArgument, "Relay 的首帧必须是 join")
	}
	invalid := status.Error(codes.PermissionDenied, "中转凭据无效或已过期")

	s.mu.Lock()
	r := s.relays[join.GetSessionId()]
	side := -1
	if r != nil && !r.dead {
		for i := range r.nodes {
			// 凭据绑定节点：别人拿到 token 也用不了；一次性：同一侧不能 Join 两次。
			if r.nodes[i] == id && !r.joined[i] && subtle.ConstantTimeCompare(join.GetToken(), r.tokens[i]) == 1 {
				side = i
			}
		}
	}
	if side < 0 {
		s.mu.Unlock()
		return invalid
	}
	end := &relayEnd{stream: stream}
	r.joined[side] = true
	r.ends[side] = end
	bothJoined := r.joined[0] && r.joined[1]
	s.mu.Unlock()

	if bothJoined {
		r.expiry.Stop()
		r.lastActivity.Store(time.Now().UnixNano())
		r.pairedAt.Store(time.Now().UnixNano())
		close(r.paired)
		go s.idleWatch(r)
		logger.Infof("[coord] 中转 %s 已建立：%s → %s", r.id[:8], r.nodes[0].Short(), r.nodes[1].Short())
	}

	select {
	case <-r.paired:
	case <-r.expired:
		return invalid
	case <-r.done:
		return status.Error(codes.Aborted, "中转会话已结束")
	case <-ctx.Done():
		s.finishRelay(r)
		return ctx.Err()
	}

	s.mu.Lock()
	other := r.ends[1-side]
	s.mu.Unlock()
	limiter := s.limiterFor(id)

	go func() {
		defer s.finishRelay(r)
		for {
			f, err := stream.Recv()
			if err != nil {
				return
			}
			data := f.GetData()
			if len(data) == 0 {
				continue
			}
			if limiter != nil && waitN(ctx, limiter, len(data)) != nil {
				return
			}
			r.lastActivity.Store(time.Now().UnixNano())
			r.bytes.Add(int64(len(data)))
			if err := other.send(data); err != nil {
				return
			}
		}
	}()

	select {
	case <-r.done:
	case <-ctx.Done():
		s.finishRelay(r)
	}
	end.close()
	return nil
}

// idleWatch：双方都没有数据超过 RelayIdleTimeout 就结束会话。SSE 有心跳，不会被误杀。
func (s *Server) idleWatch(r *relay) {
	timeout := s.opts.Limits.RelayIdleTimeout
	t := time.NewTicker(max(timeout/4, 10*time.Millisecond))
	defer t.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-t.C:
			if time.Since(time.Unix(0, r.lastActivity.Load())) >= timeout {
				logger.Infof("[coord] 中转 %s 空闲超过 %s，关闭", r.id[:8], timeout)
				s.finishRelay(r)
				return
			}
		}
	}
}

// limiterFor 返回节点的发送速率限制器；未配置限速时为 nil。
func (s *Server) limiterFor(id meshid.ID) *rate.Limiter {
	kbps := s.opts.Limits.RelayRateKBps
	if kbps <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.limiters[id]
	if l == nil {
		bps := kbps * 1024
		l = rate.NewLimiter(rate.Limit(bps), max(bps, 64<<10))
		s.limiters[id] = l
	}
	return l
}

// waitN 等 n 个令牌；n 超过突发量时分段等（gRPC 单帧可能比突发量大）。
func waitN(ctx context.Context, l *rate.Limiter, n int) error {
	for n > 0 {
		chunk := min(n, l.Burst())
		if err := l.WaitN(ctx, chunk); err != nil {
			return err
		}
		n -= chunk
	}
	return nil
}
