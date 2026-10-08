package meshcoord

import (
	"cmp"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	"asa-server/pkg/meshid"
)

// 中转统计的存储与汇总（docs/REMOTE_MANAGER_MESH_PLAN.md §12 P7）。写入只有运行中的协调节点一个进程；
// asa-coordinator relay list / stats 是另一个进程，只读。

// 中转会话的结束原因（存库的是英文代码，CLI 翻译成中文）。
const (
	RelayEndPeerClosed  = "peer_closed"         // 任一侧的流结束（管理器断开、网络中断）
	RelayEndIdleTimeout = "idle_timeout"        // limits.relay_idle_timeout 内双方都没有数据
	RelayEndShutdown    = "shutdown"            // 协调节点正常停止
	RelayEndRestart     = "coordinator_restart" // 协调节点异常退出，下次启动时收尾（字节数取最后一次刷盘值）
)

// RelayEndReasonText 把结束原因翻成中文；在途会话（空）返回「进行中」。
func RelayEndReasonText(code string) string {
	switch code {
	case "":
		return "进行中"
	case RelayEndPeerClosed:
		return "一方断开"
	case RelayEndIdleTimeout:
		return "空闲超时"
	case RelayEndShutdown:
		return "协调节点停止"
	case RelayEndRestart:
		return "协调节点异常重启"
	}
	return code
}

// RelaySession 是 relay_sessions 的一行。只有配齐（双方都 Join）的会话才入库。
type RelaySession struct {
	ID        string
	NetworkID string
	From      meshid.ID // 发起方 A
	To        meshid.ID // 被叫方 B
	OpenedAt  time.Time
	PairedAt  time.Time
	UpdatedAt time.Time // 最后一次刷盘
	EndedAt   time.Time // 零值 = 在途
	BytesAB   int64     // A → B
	BytesBA   int64     // B → A
	EndReason string
}

// Active 报告会话是否仍在途。
func (r RelaySession) Active() bool { return r.EndedAt.IsZero() }

// relayFlush 是一次刷盘里一个会话要写的东西：当前累计（写明细）与距上次刷盘的增量（记到当天）。
type relayFlush struct {
	id               string
	network          string
	from, to         meshid.ID
	bytesAB, bytesBA int64
	deltaAB, deltaBA int64
	end              bool
	endReason        string
}

// utcDay 是按天统计用的日期（UTC）。
func utcDay(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// InsertRelaySession 在会话配齐时插入一行。
func (s *Store) InsertRelaySession(r RelaySession) error {
	_, err := s.db.Exec(`INSERT INTO relay_sessions (id, network_id, from_node, to_node, opened_at, paired_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.NetworkID, r.From.Compact(), r.To.Compact(), r.OpenedAt.Unix(), r.PairedAt.Unix(), r.PairedAt.Unix())
	return err
}

// flushRelays 在一个事务里写明细的累计值、把增量加到 now 当天（UTC），结束的会话同时收尾。
func (s *Store) flushRelays(now time.Time, items []relayFlush) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	day := utcDay(now)
	for _, it := range items {
		if it.end {
			_, err = tx.Exec(`UPDATE relay_sessions SET bytes_ab = ?, bytes_ba = ?, updated_at = ?, ended_at = ?, end_reason = ?
				WHERE id = ?`, it.bytesAB, it.bytesBA, now.Unix(), now.Unix(), it.endReason, it.id)
		} else {
			_, err = tx.Exec(`UPDATE relay_sessions SET bytes_ab = ?, bytes_ba = ?, updated_at = ? WHERE id = ?`,
				it.bytesAB, it.bytesBA, now.Unix(), it.id)
		}
		if err != nil {
			return err
		}
		if it.deltaAB == 0 && it.deltaBA == 0 {
			continue
		}
		_, err = tx.Exec(`INSERT INTO relay_daily (day, network_id, from_node, to_node, bytes_ab, bytes_ba) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (day, network_id, from_node, to_node) DO UPDATE SET
				bytes_ab = relay_daily.bytes_ab + excluded.bytes_ab, bytes_ba = relay_daily.bytes_ba + excluded.bytes_ba`,
			day, it.network, it.from.Compact(), it.to.Compact(), it.deltaAB, it.deltaBA)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CloseDanglingRelays 把上次异常退出时留下的在途会话收尾：结束时间与字节数取最后一次刷盘的值。
// 只能在本进程还没有任何中转会话时调用（NewServer 里）。
func (s *Store) CloseDanglingRelays() (int64, error) {
	res, err := s.db.Exec(`UPDATE relay_sessions SET ended_at = updated_at, end_reason = ? WHERE ended_at IS NULL`, RelayEndRestart)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneRelayStats 删掉 cutoff 之前结束的会话明细与 cutoff 那天（UTC）之前的按天记录。
func (s *Store) PruneRelayStats(cutoff time.Time) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`DELETE FROM relay_sessions WHERE ended_at IS NOT NULL AND ended_at < ?`, cutoff.Unix())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	res, err = tx.Exec(`DELETE FROM relay_daily WHERE day < ?`, utcDay(cutoff))
	if err != nil {
		return 0, err
	}
	m, _ := res.RowsAffected()
	return n + m, tx.Commit()
}

// RelayDailyUsage 返回节点在 day（UTC）作为任一方已刷盘的中转字节数（两个方向合计）。
func (s *Store) RelayDailyUsage(networkID string, node meshid.ID, day string) (int64, error) {
	var n sql.NullInt64
	err := s.db.QueryRow(`SELECT SUM(bytes_ab + bytes_ba) FROM relay_daily
		WHERE day = ? AND network_id = ? AND (from_node = ? OR to_node = ?)`,
		day, networkID, node.Compact(), node.Compact()).Scan(&n)
	return n.Int64, err
}

// RelayFilter 是 relay list 的筛选条件。
type RelayFilter struct {
	NetworkID  string    // 空 = 全部网络
	Node       meshid.ID // 零值 = 全部节点；否则作为任一方
	Since      time.Time // 只看这之后还在途或结束的会话；零值 = 不限
	ActiveOnly bool
	Limit      int // <= 0 = 不限
}

// ListRelaySessions 按开始时间倒序列出会话明细。
func (s *Store) ListRelaySessions(f RelayFilter) ([]RelaySession, error) {
	var where []string
	var args []any
	if f.NetworkID != "" {
		where = append(where, "network_id = ?")
		args = append(args, f.NetworkID)
	}
	if !f.Node.IsZero() {
		where = append(where, "(from_node = ? OR to_node = ?)")
		args = append(args, f.Node.Compact(), f.Node.Compact())
	}
	if f.ActiveOnly {
		where = append(where, "ended_at IS NULL")
	} else if !f.Since.IsZero() {
		where = append(where, "(ended_at IS NULL OR ended_at >= ?)")
		args = append(args, f.Since.Unix())
	}
	q := `SELECT id, network_id, from_node, to_node, opened_at, paired_at, updated_at, ended_at, bytes_ab, bytes_ba, end_reason
		FROM relay_sessions`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY paired_at DESC, id"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RelaySession
	for rows.Next() {
		var r RelaySession
		var from, to string
		var opened, paired, updated int64
		var ended sql.NullInt64
		if err := rows.Scan(&r.ID, &r.NetworkID, &from, &to, &opened, &paired, &updated, &ended,
			&r.BytesAB, &r.BytesBA, &r.EndReason); err != nil {
			return nil, err
		}
		if r.From, err = meshid.ParseID(from); err != nil {
			return nil, fmt.Errorf("数据库里的节点 ID %q 无效: %w", from, err)
		}
		if r.To, err = meshid.ParseID(to); err != nil {
			return nil, fmt.Errorf("数据库里的节点 ID %q 无效: %w", to, err)
		}
		r.OpenedAt, r.PairedAt, r.UpdatedAt = time.Unix(opened, 0), time.Unix(paired, 0), time.Unix(updated, 0)
		if ended.Valid {
			r.EndedAt = time.Unix(ended.Int64, 0)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RelayDaily 是 relay_daily 的一行：某天（UTC）某个节点对的中转字节数。
type RelayDaily struct {
	Day       string
	NetworkID string
	From      meshid.ID
	To        meshid.ID
	BytesAB   int64
	BytesBA   int64
}

// RelayDailyRows 返回 sinceDay（含，UTC 的 YYYY-MM-DD；空 = 全部）以来的按天记录。
func (s *Store) RelayDailyRows(networkID, sinceDay string) ([]RelayDaily, error) {
	q := `SELECT day, network_id, from_node, to_node, bytes_ab, bytes_ba FROM relay_daily WHERE day >= ?`
	args := []any{sinceDay}
	if networkID != "" {
		q += " AND network_id = ?"
		args = append(args, networkID)
	}
	q += " ORDER BY day, network_id, from_node, to_node"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RelayDaily
	for rows.Next() {
		var r RelayDaily
		var from, to string
		if err := rows.Scan(&r.Day, &r.NetworkID, &from, &to, &r.BytesAB, &r.BytesBA); err != nil {
			return nil, err
		}
		if r.From, err = meshid.ParseID(from); err != nil {
			return nil, fmt.Errorf("数据库里的节点 ID %q 无效: %w", from, err)
		}
		if r.To, err = meshid.ParseID(to); err != nil {
			return nil, fmt.Errorf("数据库里的节点 ID %q 无效: %w", to, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SinceDay 把「最近 d」换成按天统计的起始日期（UTC）：按天记账，粒度就是一天。d 向上取整成 n 天、
// 含今天——7d 是今天和之前 6 天，24h 与 1d 都只有今天。
func SinceDay(now time.Time, d time.Duration) string {
	n := max(int((d+24*time.Hour-1)/(24*time.Hour)), 1)
	return utcDay(now.UTC().AddDate(0, 0, -(n - 1)))
}

// NodeRelayStat 是一个节点的中转用量：作为发起方的会话里两个方向合计、作为被叫方的合计。
type NodeRelayStat struct {
	NetworkID string
	Node      meshid.ID
	AsFrom    int64
	AsTo      int64
}

// Total 是两种身份合计。
func (n NodeRelayStat) Total() int64 { return n.AsFrom + n.AsTo }

// PairRelayStat 是一个节点对（有方向：From 发起）的中转用量。
type PairRelayStat struct {
	NetworkID string
	From      meshid.ID
	To        meshid.ID
	BytesAB   int64
	BytesBA   int64
}

// Total 是两个方向合计。
func (p PairRelayStat) Total() int64 { return p.BytesAB + p.BytesBA }

// DayRelayStat 是某天（UTC）的中转用量。
type DayRelayStat struct {
	Day   string
	Bytes int64
	Pairs int // 当天有流量的节点对数
}

// AggregateByNode 按节点汇总，用量大的在前。
func AggregateByNode(rows []RelayDaily) []NodeRelayStat {
	type key struct {
		network string
		node    meshid.ID
	}
	m := map[key]*NodeRelayStat{}
	get := func(k key) *NodeRelayStat {
		if m[k] == nil {
			m[k] = &NodeRelayStat{NetworkID: k.network, Node: k.node}
		}
		return m[k]
	}
	for _, r := range rows {
		total := r.BytesAB + r.BytesBA
		get(key{r.NetworkID, r.From}).AsFrom += total
		get(key{r.NetworkID, r.To}).AsTo += total
	}
	out := make([]NodeRelayStat, 0, len(m))
	for _, v := range m {
		out = append(out, *v)
	}
	slices.SortFunc(out, func(a, b NodeRelayStat) int {
		return cmp.Or(cmp.Compare(b.Total(), a.Total()), cmp.Compare(a.NetworkID, b.NetworkID),
			cmp.Compare(a.Node.String(), b.Node.String()))
	})
	return out
}

// AggregateByPair 按节点对汇总，用量大的在前。
func AggregateByPair(rows []RelayDaily) []PairRelayStat {
	type key struct {
		network  string
		from, to meshid.ID
	}
	m := map[key]*PairRelayStat{}
	for _, r := range rows {
		k := key{r.NetworkID, r.From, r.To}
		if m[k] == nil {
			m[k] = &PairRelayStat{NetworkID: r.NetworkID, From: r.From, To: r.To}
		}
		m[k].BytesAB += r.BytesAB
		m[k].BytesBA += r.BytesBA
	}
	out := make([]PairRelayStat, 0, len(m))
	for _, v := range m {
		out = append(out, *v)
	}
	slices.SortFunc(out, func(a, b PairRelayStat) int {
		return cmp.Or(cmp.Compare(b.Total(), a.Total()), cmp.Compare(a.NetworkID, b.NetworkID),
			cmp.Compare(a.From.String(), b.From.String()), cmp.Compare(a.To.String(), b.To.String()))
	})
	return out
}

// AggregateByDay 按天汇总，日期升序。
func AggregateByDay(rows []RelayDaily) []DayRelayStat {
	m := map[string]*DayRelayStat{}
	for _, r := range rows {
		if m[r.Day] == nil {
			m[r.Day] = &DayRelayStat{Day: r.Day}
		}
		m[r.Day].Bytes += r.BytesAB + r.BytesBA
		m[r.Day].Pairs++
	}
	out := make([]DayRelayStat, 0, len(m))
	for _, v := range m {
		out = append(out, *v)
	}
	slices.SortFunc(out, func(a, b DayRelayStat) int { return cmp.Compare(a.Day, b.Day) })
	return out
}
