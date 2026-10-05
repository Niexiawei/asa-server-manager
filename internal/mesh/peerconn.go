package mesh

import (
	"context"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"

	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
)

// peerMaxBackoff 是对端连接重连退避的上限。
const peerMaxBackoff = 5 * time.Second

// peerHandle 是到一个对端的 gRPC 连接，带引用计数（§12 P2-5）。
//
// 路径升级（中转 → 直连）时换上一个新的 handle、旧的标记退役；旧 handle 上的在途流
// （日志 SSE、WebSocket）不被打断，**引用计数归零才真正关闭**——新请求立即走直连。
type peerHandle struct {
	peer meshid.ID
	cc   *grpc.ClientConn

	mu        sync.Mutex
	kind      PathKind
	refs      int
	retired   bool
	closed    bool
	upgrading bool
	lastUsed  time.Time
}

func (h *peerHandle) setKind(k PathKind) {
	h.mu.Lock()
	h.kind = k
	h.mu.Unlock()
}

func (h *peerHandle) currentKind() PathKind {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.kind
}

// release 归还一次引用；已退役且没人用了就关闭。
func (h *peerHandle) release() {
	h.mu.Lock()
	h.refs--
	h.lastUsed = time.Now()
	closeNow := h.retired && h.refs == 0 && !h.closed
	if closeNow {
		h.closed = true
	}
	h.mu.Unlock()
	if closeNow {
		h.cc.Close()
	}
}

// retire 标记退役；没人用时立即关闭。
func (h *peerHandle) retire() {
	h.mu.Lock()
	h.retired = true
	closeNow := h.refs == 0 && !h.closed
	if closeNow {
		h.closed = true
	}
	h.mu.Unlock()
	if closeNow {
		h.cc.Close()
	}
}

// forceClose 不管引用计数直接关（Stop 时）。
func (h *peerHandle) forceClose() {
	h.mu.Lock()
	h.retired = true
	already := h.closed
	h.closed = true
	h.mu.Unlock()
	if !already {
		h.cc.Close()
	}
}

// acquire 返回到 peer 的连接与归还函数（每个对端一个 handle，懒建）。所有 RPC 都经它取连接。
func (m *Manager) acquire(peer meshid.ID) (*peerHandle, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running {
		return nil, nil, ErrNotRunning
	}
	if peer == m.id {
		return nil, nil, errCannotDialSelf
	}
	h := m.peers[peer]
	if h == nil {
		var err error
		if h, err = m.newHandleLocked(peer, nil); err != nil {
			return nil, nil, err
		}
		m.peers[peer] = h
	}
	h.mu.Lock()
	h.refs++
	h.lastUsed = time.Now()
	h.mu.Unlock()
	var once sync.Once
	return h, func() { once.Do(h.release) }, nil
}

// newHandleLocked 建一个 handle。first 非空时，第一次拨号直接交出这条已认证的连接
// （路径升级时用：升级循环已经拨通了直连，不该再拨一次）。调用方持有 m.mu。
func (m *Manager) newHandleLocked(peer meshid.ID, first *pathConn) (*peerHandle, error) {
	h := &peerHandle{peer: peer, lastUsed: time.Now()}
	if first != nil {
		h.kind = first.kind
	}
	providers := m.providers
	ctx := m.ctx
	var firstMu sync.Mutex
	cc, err := grpc.NewClient("passthrough:///"+peer.Compact(),
		grpc.WithContextDialer(func(dctx context.Context, _ string) (net.Conn, error) {
			firstMu.Lock()
			pc := first
			first = nil
			firstMu.Unlock()
			if pc == nil {
				var err error
				if pc, err = dialPeer(dctx, providers, peer); err != nil {
					return nil, err
				}
			}
			h.setKind(pc.kind)
			m.recordPath(peer, pc.kind)
			if pc.kind == PathRelay {
				m.startUpgrade(ctx, h)
			}
			return pc, nil
		}),
		// 端到端 mTLS 已在 dialer 里完成（P2-1），这里只把 TLS 状态交给 gRPC。
		grpc.WithTransportCredentials(handshakenCreds{}),
		// 重连退避封顶 5 秒：gRPC 默认会涨到 120 秒，对端恢复后远程面板要干等两分钟。
		// 拨号本身很便宜（直连 2 秒上限 + 一次中转申请），不需要那么保守。
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           backoff.Config{BaseDelay: 500 * time.Millisecond, Multiplier: 1.6, Jitter: 0.2, MaxDelay: peerMaxBackoff},
			MinConnectTimeout: 20 * time.Second,
		}),
	)
	if err != nil {
		return nil, err
	}
	h.cc = cc
	return h, nil
}

func (m *Manager) recordPath(peer meshid.ID, k PathKind) {
	m.mu.Lock()
	if m.lastPath != nil {
		m.lastPath[peer] = k
	}
	m.mu.Unlock()
}

// startUpgrade 为走中转的 handle 启动升级循环（每个 handle 至多一个）。
func (m *Manager) startUpgrade(ctx context.Context, h *peerHandle) {
	if m.direct == nil {
		return
	}
	h.mu.Lock()
	if h.upgrading || h.retired {
		h.mu.Unlock()
		return
	}
	h.upgrading = true
	h.mu.Unlock()
	go m.upgradeLoop(ctx, h)
}

// upgradeLoop 按 UpgradeMin 起、翻倍到 UpgradeMax 的间隔重试直连（§5.3 第 4 步）。
// 成功 ⇒ 用这条已握手的连接建新 handle 换上，旧的退役。最近 UpgradeMax 内没人用过的对端不试——
// 没人在看的面板不值得维持一条直连。
func (m *Manager) upgradeLoop(ctx context.Context, h *peerHandle) {
	defer func() {
		h.mu.Lock()
		h.upgrading = false
		h.mu.Unlock()
	}()
	interval := m.opts.UpgradeMin
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		h.mu.Lock()
		retired, idle := h.retired, h.refs == 0 && time.Since(h.lastUsed) > m.opts.UpgradeMax
		h.mu.Unlock()
		if retired || idle || h.currentKind() != PathRelay {
			return
		}
		dctx, cancel := context.WithTimeout(ctx, happyBudget+time.Second)
		pc, err := m.direct.Dial(dctx, h.peer)
		cancel()
		if err == nil {
			if m.swapHandle(h, pc) {
				logger.Infof("[mesh] 到 %s 的路径已从中转升级为直连（%s）", h.peer.Short(), pc.kind)
			} else {
				pc.Close()
			}
			return
		}
		interval = min(interval*2, m.opts.UpgradeMax)
	}
}

// swapHandle 用已拨通的直连 pc 建新 handle 换掉 old。old 已不是当前 handle 时放弃。
func (m *Manager) swapHandle(old *peerHandle, pc *pathConn) bool {
	m.mu.Lock()
	if !m.running || m.peers[old.peer] != old {
		m.mu.Unlock()
		return false
	}
	nh, err := m.newHandleLocked(old.peer, pc)
	if err != nil {
		m.mu.Unlock()
		return false
	}
	m.peers[old.peer] = nh
	if m.lastPath != nil {
		m.lastPath[old.peer] = pc.kind
	}
	m.mu.Unlock()
	// 让新连接立即建立（交出 pc），而不是等第一个请求。
	nh.cc.Connect()
	old.retire()
	return true
}
