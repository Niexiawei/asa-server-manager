package meshcoord

import (
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
)

// 运行中的协调节点这一侧的中转记账（§12 P7-1、P7-3）：配齐时插一行，定期把在途会话的字节数刷进库，
// 结束时写最终值。所有写库都在 statsMu 下串行——刷盘与收尾要对同一个会话的「已刷盘值」达成一致，
// 否则增量会被记两次或漏记。

const pruneInterval = time.Hour

// recordRelayStart 在会话配齐时插入明细行。失败只记日志：统计不能挡住中转本身。
func (s *Server) recordRelayStart(r *relay, now time.Time) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	err := s.opts.Store.InsertRelaySession(RelaySession{
		ID: r.id, NetworkID: r.network, From: r.nodes[0], To: r.nodes[1], OpenedAt: r.openedAt, PairedAt: now,
	})
	if err != nil {
		logger.Warnf("[coord] 记录中转 %s 失败：%v", r.id[:8], err)
	}
}

// pendingLocked 返回会话距上次刷盘的增量与当前累计。调用方持有 statsMu。
func (r *relay) pendingLocked() (ab, ba, dab, dba int64) {
	ab, ba = r.bytesAB.Load(), r.bytesBA.Load()
	return ab, ba, ab - r.flushedAB, ba - r.flushedBA
}

// recordRelayEnd 写最终字节数与结束原因。
func (s *Server) recordRelayEnd(r *relay, reason string) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if r.recorded {
		return
	}
	ab, ba, dab, dba := r.pendingLocked()
	err := s.opts.Store.flushRelays(time.Now(), []relayFlush{{
		id: r.id, network: r.network, from: r.nodes[0], to: r.nodes[1],
		bytesAB: ab, bytesBA: ba, deltaAB: dab, deltaBA: dba, end: true, endReason: reason,
	}})
	if err != nil {
		logger.Warnf("[coord] 记录中转 %s 的结束失败：%v", r.id[:8], err)
		return
	}
	r.flushedAB, r.flushedBA, r.recorded = ab, ba, true
}

// activePaired 返回当前配齐、未结束的会话。
func (s *Server) activePaired() []*relay {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*relay, 0, len(s.relays))
	for _, r := range s.relays {
		if r.pairedAt.Load() != 0 && !r.dead {
			out = append(out, r)
		}
	}
	return out
}

// flushStats 把所有在途会话的字节数在一个事务里写进库。
func (s *Server) flushStats() {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	var items []relayFlush
	var rs []*relay
	for _, r := range s.activePaired() {
		if r.recorded {
			continue
		}
		ab, ba, dab, dba := r.pendingLocked()
		if dab == 0 && dba == 0 {
			continue
		}
		items = append(items, relayFlush{id: r.id, network: r.network, from: r.nodes[0], to: r.nodes[1],
			bytesAB: ab, bytesBA: ba, deltaAB: dab, deltaBA: dba})
		rs = append(rs, r)
	}
	if err := s.opts.Store.flushRelays(time.Now(), items); err != nil {
		logger.Warnf("[coord] 中转统计刷盘失败：%v", err)
		return
	}
	for i, r := range rs {
		r.flushedAB, r.flushedBA = items[i].bytesAB, items[i].bytesBA
	}
}

// pruneStats 删掉过了保留期的统计。保留期 0 = 永久。
func (s *Server) pruneStats() {
	keep := s.opts.Stats.Retention()
	if keep <= 0 {
		return
	}
	n, err := s.opts.Store.PruneRelayStats(time.Now().Add(-keep))
	if err != nil {
		logger.Warnf("[coord] 清理过期的中转统计失败：%v", err)
	} else if n > 0 {
		logger.Infof("[coord] 已清理 %d 条超过 %d 天的中转统计", n, int(keep/(24*time.Hour)))
	}
}

// statsLoop 定期刷盘与清理。
func (s *Server) statsLoop() {
	flush := time.NewTicker(s.opts.Stats.FlushInterval)
	defer flush.Stop()
	prune := time.NewTicker(pruneInterval)
	defer prune.Stop()
	s.pruneStats()
	for {
		select {
		case <-s.stop:
			return
		case <-flush.C:
			s.flushStats()
		case <-prune.C:
			s.pruneStats()
		}
	}
}

// dailyUsage 返回节点今天（UTC）作为任一方的中转字节数：库里已刷盘的 + 在途会话还没刷盘的。
func (s *Server) dailyUsage(network string, node meshid.ID) (int64, error) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	used, err := s.opts.Store.RelayDailyUsage(network, node, utcDay(time.Now()))
	if err != nil {
		return 0, err
	}
	for _, r := range s.activePaired() {
		if r.network == network && (r.nodes[0] == node || r.nodes[1] == node) && !r.recorded {
			_, _, dab, dba := r.pendingLocked()
			used += dab + dba
		}
	}
	return used, nil
}

// checkDailyQuota：任一方今天的中转用量达到 limits.relay_daily_mib 就拒绝新的中转（在途的不切断）。
// 查库失败时放行——额度是成本闸门，不能因为统计出错把中转整个关掉。
func (s *Server) checkDailyQuota(network string, caller, target meshid.ID) error {
	limit := int64(s.opts.Limits.RelayDailyMiB) << 20
	if limit <= 0 {
		return nil
	}
	for _, n := range []meshid.ID{caller, target} {
		used, err := s.dailyUsage(network, n)
		if err != nil {
			logger.Warnf("[coord] 查询 %s 的今日中转用量失败（放行）：%v", n.Short(), err)
			return nil
		}
		if used < limit {
			continue
		}
		who := "本机"
		if n == target {
			who = "对方"
		}
		logger.Infof("[coord] 拒绝 %s → %s 的中转：%s 今日已用 %d 字节，额度 %d MiB", caller.Short(), target.Short(),
			n.Short(), used, s.opts.Limits.RelayDailyMiB)
		return status.Errorf(codes.ResourceExhausted, "%s今日中转额度已用完（每节点每天 %d MiB，UTC 0 点重置）；直连与打洞不受影响",
			who, s.opts.Limits.RelayDailyMiB)
	}
	return nil
}

// Shutdown 在停止 gRPC 服务之前调用：结束所有中转会话并记为 shutdown（不等 GracefulStop 超时），
// 之后到达的结束一律也记为 shutdown。
func (s *Server) Shutdown() {
	s.stopping.Store(true)
	s.mu.Lock()
	rs := make([]*relay, 0, len(s.relays))
	for _, r := range s.relays {
		rs = append(rs, r)
	}
	s.mu.Unlock()
	for _, r := range rs {
		s.finishRelay(r, RelayEndShutdown)
	}
}
