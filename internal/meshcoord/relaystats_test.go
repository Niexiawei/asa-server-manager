package meshcoord

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/meshid"
)

// v1 的库（P1～P6 部署出去的）升到 v2：原有网络、节点、拉黑表不变，新表可用。
func TestMigrateV1ToV2(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.ToSlash(filepath.Join(dir, "coordinator.db")))
	if err != nil {
		t.Fatal(err)
	}
	id := testID(t)
	for _, q := range []string{
		migrations[0],
		`PRAGMA user_version = 1`,
		`INSERT INTO networks (id, name, secret, created_at) VALUES ('default', '默认网络', 'sekrit', 1)`,
		`INSERT INTO nodes (network_id, node_id, label, first_seen, last_seen, banned) VALUES ('default', '` + id.Compact() + `', 'old', 5, 6, 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 2 {
		t.Fatalf("应升到版本 2，得到 %d（%v）", v, err)
	}
	n, err := s.Network(DefaultNetworkID)
	if err != nil || n.Secret != "sekrit" {
		t.Fatalf("网络应原样保留：%+v %v", n, err)
	}
	rec, err := s.Node(DefaultNetworkID, id)
	if err != nil || rec.Label != "old" || !rec.Banned || rec.FirstSeen.Unix() != 5 {
		t.Fatalf("节点与拉黑状态应原样保留：%+v %v", rec, err)
	}
	if err := s.InsertRelaySession(RelaySession{ID: "x", NetworkID: "default", From: id, To: id,
		OpenedAt: time.Now(), PairedAt: time.Now()}); err != nil {
		t.Fatalf("新表应可写：%v", err)
	}
	s.Close()
	// 再打开一次：已是最新版本，不重复迁移。
	s2, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
}

func testID(t *testing.T) meshid.ID {
	t.Helper()
	_, id := newIdentity(t)
	return id
}

// relayPair 建一条配齐的中转会话，返回双方的流。
func relayPair(t *testing.T, c *coord) (a, b *node, ra, rb meshpb.Coordinator_RelayClient) {
	t.Helper()
	a, b = c.newNode(t), c.newNode(t)
	a.mustRegister(t, c)
	sb := b.mustRegister(t, c)
	sid, tokenA, tokenB := openRelay(t, a, sb, b)
	return a, b, a.joinRelay(t, sid, tokenA), b.joinRelay(t, sid, tokenB)
}

func sendN(t *testing.T, rs meshpb.Coordinator_RelayClient, n int) {
	t.Helper()
	chunk := make([]byte, 16<<10)
	for n > 0 {
		k := min(n, len(chunk))
		if err := rs.Send(&meshpb.RelayFrame{Msg: &meshpb.RelayFrame_Data{Data: chunk[:k]}}); err != nil {
			t.Fatal(err)
		}
		n -= k
	}
}

func recvN(t *testing.T, rs meshpb.Coordinator_RelayClient, n int) {
	t.Helper()
	for got := 0; got < n; {
		f, err := rs.Recv()
		if err != nil {
			t.Fatalf("收到 %d / %d 字节时出错：%v", got, n, err)
		}
		got += len(f.GetData())
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("超时：%s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func onlySession(t *testing.T, s *Store, f RelayFilter) (RelaySession, bool) {
	t.Helper()
	list, err := s.ListRelaySessions(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) > 1 {
		t.Fatalf("应只有一条会话，得到 %d 条", len(list))
	}
	if len(list) == 0 {
		return RelaySession{}, false
	}
	return list[0], true
}

// 按方向计数、周期刷盘、结束写最终值；按天记录与明细一致，不重复计数。
func TestRelayStatsFlushAndEnd(t *testing.T) {
	c := startCoord(t, ServerOptions{Stats: StatsConfig{FlushInterval: 50 * time.Millisecond}})
	a, b, ra, rb := relayPair(t, c)
	const ab, ba = 100 << 10, 30 << 10
	sendN(t, ra, ab)
	sendN(t, rb, ba)
	recvN(t, rb, ab)
	recvN(t, ra, ba)

	var live RelaySession
	waitFor(t, "在途会话的字节数刷进库", func() bool {
		r, ok := onlySession(t, c.store, RelayFilter{ActiveOnly: true})
		live = r
		return ok && r.BytesAB == ab && r.BytesBA == ba
	})
	if live.From != a.id || live.To != b.id || live.NetworkID != DefaultNetworkID || !live.Active() {
		t.Fatalf("会话明细不对：%+v", live)
	}
	if used, _ := c.store.RelayDailyUsage(DefaultNetworkID, a.id, utcDay(time.Now())); used != ab+ba {
		t.Fatalf("A 今日用量应为 %d，得到 %d", ab+ba, used)
	}

	sendN(t, ra, 5000)
	recvN(t, rb, 5000)
	ra.CloseSend()
	waitFor(t, "会话结束并写入最终值", func() bool {
		r, ok := onlySession(t, c.store, RelayFilter{})
		live = r
		return ok && !r.Active()
	})
	if live.BytesAB != ab+5000 || live.BytesBA != ba || live.EndReason != RelayEndPeerClosed {
		t.Fatalf("结束后的明细不对：%+v", live)
	}
	rows, err := c.store.RelayDailyRows("", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].BytesAB != ab+5000 || rows[0].BytesBA != ba || rows[0].Day != utcDay(time.Now()) {
		t.Fatalf("按天记录应与明细一致（不重复计数）：%+v", rows)
	}
	if _, ok := onlySession(t, c.store, RelayFilter{ActiveOnly: true}); ok {
		t.Fatal("结束后不应再出现在 --active 里")
	}
	if _, ok := onlySession(t, c.store, RelayFilter{Node: testID(t)}); ok {
		t.Fatal("按无关节点筛选不应返回这条会话")
	}
	if _, ok := onlySession(t, c.store, RelayFilter{Node: b.id}); !ok {
		t.Fatal("按被叫方筛选应返回这条会话")
	}
}

// 没配齐就作废的会话不入库。
func TestUnpairedRelayNotRecorded(t *testing.T) {
	c := startCoord(t, ServerOptions{RelayJoinTimeout: 100 * time.Millisecond})
	a, b := c.newNode(t), c.newNode(t)
	a.mustRegister(t, c)
	sb := b.mustRegister(t, c)
	sid, tokenA, _ := openRelay(t, a, sb, b)
	recvErr(a.joinRelay(t, sid, tokenA).Recv)
	if list, _ := c.store.ListRelaySessions(RelayFilter{}); len(list) != 0 {
		t.Fatalf("没配齐的会话不应入库：%+v", list)
	}
}

// 协调节点异常退出后，下次启动把遗留的在途记录收尾为 coordinator_restart，时间取最后一次刷盘。
func TestDanglingRelaysClosedOnStart(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a, b := testID(t), testID(t)
	paired := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := store.InsertRelaySession(RelaySession{ID: "dangling", NetworkID: "default", From: a, To: b,
		OpenedAt: paired, PairedAt: paired}); err != nil {
		t.Fatal(err)
	}
	flushed := paired.Add(10 * time.Minute)
	if err := store.flushRelays(flushed, []relayFlush{{id: "dangling", network: "default", from: a, to: b,
		bytesAB: 7, bytesBA: 9, deltaAB: 7, deltaBA: 9}}); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(ServerOptions{Store: store})
	srv.Close()
	r, ok := onlySession(t, store, RelayFilter{})
	if !ok || r.Active() || r.EndReason != RelayEndRestart || !r.EndedAt.Equal(flushed) || r.BytesAB != 7 || r.BytesBA != 9 {
		t.Fatalf("遗留记录应收尾为 coordinator_restart、结束时间取最后一次刷盘：%+v", r)
	}
}

// 正常停止：Shutdown 结束所有中转并记为 shutdown，客户端的流随之结束。
func TestShutdownRecordsRelays(t *testing.T) {
	c := startCoord(t, ServerOptions{Stats: StatsConfig{FlushInterval: time.Hour}})
	_, _, ra, rb := relayPair(t, c)
	sendN(t, ra, 1000)
	recvN(t, rb, 1000)
	c.srv.Shutdown()
	if err := recvErr(ra.Recv); err == nil {
		t.Fatal("Shutdown 后 A 的流应结束")
	}
	r, ok := onlySession(t, c.store, RelayFilter{})
	if !ok || r.Active() || r.EndReason != RelayEndShutdown || r.BytesAB != 1000 {
		t.Fatalf("应记为 shutdown 且写入最终字节数：%+v", r)
	}
}

// 保留期：过期的明细与按天记录被删掉，在途与未过期的保留。
func TestPruneRelayStats(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a, b := testID(t), testID(t)
	now := time.Now()
	old, recent := now.Add(-40*24*time.Hour), now.Add(-time.Hour)
	for _, r := range []struct {
		id  string
		at  time.Time
		end bool
	}{{"old", old, true}, {"recent", recent, true}, {"old-active", old, false}} {
		if err := store.InsertRelaySession(RelaySession{ID: r.id, NetworkID: "n", From: a, To: b, OpenedAt: r.at, PairedAt: r.at}); err != nil {
			t.Fatal(err)
		}
		if err := store.flushRelays(r.at, []relayFlush{{id: r.id, network: "n", from: a, to: b, bytesAB: 1, deltaAB: 1,
			end: r.end, endReason: RelayEndPeerClosed}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.PruneRelayStats(now.Add(-30 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	list, _ := store.ListRelaySessions(RelayFilter{})
	ids := map[string]bool{}
	for _, r := range list {
		ids[r.ID] = true
	}
	if ids["old"] || !ids["recent"] || !ids["old-active"] {
		t.Fatalf("只该删掉过期且已结束的会话：%v", ids)
	}
	rows, _ := store.RelayDailyRows("", "")
	for _, r := range rows {
		if r.Day < utcDay(now.Add(-30*24*time.Hour)) {
			t.Fatalf("过期的按天记录应被删掉：%+v", r)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("应只剩最近一天的记录：%+v", rows)
	}
}

// 每日额度：超额后新的中转被拒（任一方超额都算），在途的不切断，无关的节点不受影响。
// 刷盘间隔设成 1 小时：额度必须把还没刷盘的在途字节也算上。
func TestRelayDailyQuota(t *testing.T) {
	c := startCoord(t, ServerOptions{
		Limits: LimitsConfig{RelayDailyMiB: 1},
		Stats:  StatsConfig{FlushInterval: time.Hour},
	})
	a, b, ra, rb := relayPair(t, c)
	// 边发边收：1 MiB 超过 gRPC 的流控窗口，先发完再收会互相等住。
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		chunk := make([]byte, 16<<10)
		for n := 1<<20 + 1; n > 0; n -= len(chunk) {
			if err := ra.Send(&meshpb.RelayFrame{Msg: &meshpb.RelayFrame_Data{Data: chunk[:min(n, len(chunk))]}}); err != nil {
				return
			}
		}
	}()
	recvN(t, rb, 1<<20+1)
	<-sent

	_, err := a.client.OpenRelay(ctxTimeout(t, 5*time.Second), &meshpb.OpenRelayRequest{TargetNodeId: b.id.Compact()})
	if codeOf(err) != codes.ResourceExhausted {
		t.Fatalf("超额后新的中转应被拒，得到 %v", err)
	}
	// 第三个节点连 B：B 作为被叫方也已超额。
	x := c.newNode(t)
	x.mustRegister(t, c)
	if _, err := x.client.OpenRelay(ctxTimeout(t, 5*time.Second), &meshpb.OpenRelayRequest{TargetNodeId: b.id.Compact()}); codeOf(err) != codes.ResourceExhausted {
		t.Fatalf("对方超额时也应被拒，得到 %v", err)
	}
	// 在途会话不切断。
	sendN(t, ra, 1000)
	recvN(t, rb, 1000)
	// 无关的节点对不受影响。
	y := c.newNode(t)
	sy := y.mustRegister(t, c)
	openRelay(t, x, sy, y)
}

func TestAggregateRelayStats(t *testing.T) {
	a, b, x := testID(t), testID(t), testID(t)
	rows := []RelayDaily{
		{Day: "2026-10-01", NetworkID: "default", From: a, To: b, BytesAB: 100, BytesBA: 50},
		{Day: "2026-10-02", NetworkID: "default", From: a, To: b, BytesAB: 10, BytesBA: 0},
		{Day: "2026-10-02", NetworkID: "default", From: b, To: x, BytesAB: 1, BytesBA: 2},
	}
	nodes := AggregateByNode(rows)
	want := map[meshid.ID][2]int64{a: {160, 0}, b: {3, 160}, x: {0, 3}}
	if len(nodes) != 3 || nodes[0].Node != b {
		t.Fatalf("按节点汇总应有 3 个、B 用量最大排第一：%+v", nodes)
	}
	for _, n := range nodes {
		if w := want[n.Node]; n.AsFrom != w[0] || n.AsTo != w[1] {
			t.Fatalf("%s：发起 %d / 被叫 %d，期望 %v", n.Node.Short(), n.AsFrom, n.AsTo, w)
		}
	}
	pairs := AggregateByPair(rows)
	if len(pairs) != 2 || pairs[0].From != a || pairs[0].BytesAB != 110 || pairs[0].BytesBA != 50 || pairs[1].Total() != 3 {
		t.Fatalf("按节点对汇总不对：%+v", pairs)
	}
	days := AggregateByDay(rows)
	if len(days) != 2 || days[0] != (DayRelayStat{"2026-10-01", 150, 1}) || days[1] != (DayRelayStat{"2026-10-02", 13, 2}) {
		t.Fatalf("按天汇总不对：%+v", days)
	}
	now := time.Date(2026, 10, 7, 1, 0, 0, 0, time.FixedZone("UTC+8", 8*3600)) // UTC 是 10-06 17:00
	for d, want := range map[time.Duration]string{
		7 * 24 * time.Hour: "2026-09-30", 24 * time.Hour: "2026-10-06", time.Hour: "2026-10-06", 0: "2026-10-06", 25 * time.Hour: "2026-10-05",
	} {
		if got := SinceDay(now, d); got != want {
			t.Errorf("SinceDay(%s) = %s，期望 %s（UTC、含今天、向上取整成天）", d, got, want)
		}
	}
}
