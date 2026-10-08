package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/urfave/cli/v3"

	"asa-server/internal/meshcoord"
	"asa-server/pkg/meshid"
)

// 中转流量统计的 CLI（docs/REMOTE_MANAGER_MESH_PLAN.md §12 P7-2）。与运行中的协调节点是两个进程，只读库：
// 进行中的会话字节数最多滞后 stats.flush_interval。

func relayCommand() *cli.Command {
	jsonFlag := &cli.BoolFlag{Name: "json", Usage: "输出 JSON（字节数是原始整数，方便接监控）"}
	return &cli.Command{
		Name:  "relay",
		Usage: "中转流量统计",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "中转会话明细（新的在前）",
				Flags: []cli.Flag{
					configFlag(),
					&cli.BoolFlag{Name: "active", Usage: "只看进行中的会话"},
					&cli.StringFlag{Name: "node", Usage: "只看这个节点参与的会话（节点 ID）"},
					&cli.StringFlag{Name: "since", Value: "24h", Usage: "只看这段时间内还在进行或结束的会话（如 30m、24h、7d）"},
					&cli.IntFlag{Name: "limit", Value: 50, Usage: "最多列出多少条（0 = 不限）"},
					jsonFlag,
				},
				Action: actionRelayList,
			},
			{
				Name:  "stats",
				Usage: "中转用量汇总（按天记账，UTC）",
				Flags: []cli.Flag{
					configFlag(),
					&cli.StringFlag{Name: "by", Value: "node", Usage: "汇总方式：node（按节点）、pair（按节点对）、day（按天）"},
					&cli.StringFlag{Name: "since", Value: "7d", Usage: "统计最近多久（按天取整，含今天；如 1d、7d、30d）"},
					jsonFlag,
				},
				Action: actionRelayStats,
			},
		},
	}
}

// withStore 加载配置、打开库并交给 fn。
func withStore(cmd *cli.Command, fn func(*meshcoord.Config, *meshcoord.Store) error) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	store, err := meshcoord.OpenStore(cfg.DataDir)
	if err != nil {
		return err
	}
	defer store.Close()
	return fn(cfg, store)
}

// parseSince 解析时长，除 Go 的写法外支持「Nd」（天）。
func parseSince(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("--since %q 不是有效的时长（例：30m、24h、7d）", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("--since %q 不是有效的时长（例：30m、24h、7d）", s)
	}
	return d, nil
}

// fmtBytes 是人类可读的字节数（1024 进制）。
func fmtBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v, i := float64(n), -1
	for v >= unit && i < 4 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f %ciB", v, "KMGTP"[i])
}

// nodeLabels 返回节点 ID → 备注名（协调节点记录的登记标签）。
func nodeLabels(store *meshcoord.Store) map[meshid.ID]string {
	out := map[meshid.ID]string{}
	if nodes, err := store.ListNodes(""); err == nil {
		for _, n := range nodes {
			out[n.NodeID] = n.Label
		}
	}
	return out
}

func nodeName(labels map[meshid.ID]string, id meshid.ID) string {
	if l := labels[id]; l != "" {
		return id.Short() + "（" + l + "）"
	}
	return id.Short()
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

type relaySessionJSON struct {
	ID        string     `json:"id"`
	NetworkID string     `json:"network_id"`
	From      string     `json:"from"`
	FromLabel string     `json:"from_label"`
	To        string     `json:"to"`
	ToLabel   string     `json:"to_label"`
	OpenedAt  time.Time  `json:"opened_at"`
	PairedAt  time.Time  `json:"paired_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	EndedAt   *time.Time `json:"ended_at"`
	BytesAB   int64      `json:"bytes_ab"`
	BytesBA   int64      `json:"bytes_ba"`
	Active    bool       `json:"active"`
	EndReason string     `json:"end_reason"`
}

func actionRelayList(_ context.Context, cmd *cli.Command) error {
	f := meshcoord.RelayFilter{ActiveOnly: cmd.Bool("active"), Limit: int(cmd.Int("limit"))}
	if s := cmd.String("node"); s != "" {
		id, err := meshid.ParseID(s)
		if err != nil {
			return err
		}
		f.Node = id
	}
	if !f.ActiveOnly {
		d, err := parseSince(cmd.String("since"))
		if err != nil {
			return cli.Exit(err.Error(), 2)
		}
		f.Since = time.Now().Add(-d)
	}
	return withStore(cmd, func(cfg *meshcoord.Config, store *meshcoord.Store) error {
		list, err := store.ListRelaySessions(f)
		if err != nil {
			return err
		}
		labels := nodeLabels(store)
		if cmd.Bool("json") {
			out := make([]relaySessionJSON, 0, len(list))
			for _, r := range list {
				j := relaySessionJSON{ID: r.ID, NetworkID: r.NetworkID, From: r.From.String(), FromLabel: labels[r.From],
					To: r.To.String(), ToLabel: labels[r.To], OpenedAt: r.OpenedAt, PairedAt: r.PairedAt, UpdatedAt: r.UpdatedAt,
					BytesAB: r.BytesAB, BytesBA: r.BytesBA, Active: r.Active(), EndReason: r.EndReason}
				if !r.Active() {
					ended := r.EndedAt
					j.EndedAt = &ended
				}
				out = append(out, j)
			}
			return printJSON(out)
		}
		if len(list) == 0 {
			fmt.Println("没有符合条件的中转会话。")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "开始\t发起方\t被叫方\t时长\t发起→被叫\t被叫→发起\t状态")
		for _, r := range list {
			end := r.EndedAt
			if r.Active() {
				end = time.Now()
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", fmtTime(r.PairedAt), nodeName(labels, r.From), nodeName(labels, r.To),
				end.Sub(r.PairedAt).Round(time.Second), fmtBytes(r.BytesAB), fmtBytes(r.BytesBA), meshcoord.RelayEndReasonText(r.EndReason))
		}
		if err := w.Flush(); err != nil {
			return err
		}
		fmt.Printf("\n进行中的会话字节数最多滞后 %s（stats.flush_interval）。\n", cfg.Stats.FlushInterval)
		return nil
	})
}

func actionRelayStats(_ context.Context, cmd *cli.Command) error {
	d, err := parseSince(cmd.String("since"))
	if err != nil {
		return cli.Exit(err.Error(), 2)
	}
	by := cmd.String("by")
	if by != "node" && by != "pair" && by != "day" {
		return cli.Exit("--by 只能是 node、pair 或 day", 2)
	}
	sinceDay := meshcoord.SinceDay(time.Now(), d)
	return withStore(cmd, func(cfg *meshcoord.Config, store *meshcoord.Store) error {
		rows, err := store.RelayDailyRows("", sinceDay)
		if err != nil {
			return err
		}
		labels := nodeLabels(store)
		asJSON := cmd.Bool("json")
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		switch by {
		case "node":
			stats := meshcoord.AggregateByNode(rows)
			if asJSON {
				out := make([]map[string]any, 0, len(stats))
				for _, s := range stats {
					out = append(out, map[string]any{"network_id": s.NetworkID, "node": s.Node.String(), "label": labels[s.Node],
						"as_from": s.AsFrom, "as_to": s.AsTo, "total": s.Total()})
				}
				return printJSON(out)
			}
			fmt.Fprintln(w, "节点\t作为发起方\t作为被叫方\t合计")
			for _, s := range stats {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", nodeName(labels, s.Node), fmtBytes(s.AsFrom), fmtBytes(s.AsTo), fmtBytes(s.Total()))
			}
		case "pair":
			stats := meshcoord.AggregateByPair(rows)
			if asJSON {
				out := make([]map[string]any, 0, len(stats))
				for _, s := range stats {
					out = append(out, map[string]any{"network_id": s.NetworkID, "from": s.From.String(), "from_label": labels[s.From],
						"to": s.To.String(), "to_label": labels[s.To], "bytes_ab": s.BytesAB, "bytes_ba": s.BytesBA, "total": s.Total()})
				}
				return printJSON(out)
			}
			fmt.Fprintln(w, "发起方\t被叫方\t发起→被叫\t被叫→发起\t合计")
			for _, s := range stats {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", nodeName(labels, s.From), nodeName(labels, s.To),
					fmtBytes(s.BytesAB), fmtBytes(s.BytesBA), fmtBytes(s.Total()))
			}
		case "day":
			stats := meshcoord.AggregateByDay(rows)
			if asJSON {
				out := make([]map[string]any, 0, len(stats))
				for _, s := range stats {
					out = append(out, map[string]any{"day": s.Day, "bytes": s.Bytes, "pairs": s.Pairs})
				}
				return printJSON(out)
			}
			fmt.Fprintln(w, "日期（UTC）\t合计\t节点对数")
			for _, s := range stats {
				fmt.Fprintf(w, "%s\t%s\t%d\n", s.Day, fmtBytes(s.Bytes), s.Pairs)
			}
		}
		if len(rows) == 0 {
			fmt.Printf("%s 以来（UTC）没有中转流量。\n", sinceDay)
			return nil
		}
		if err := w.Flush(); err != nil {
			return err
		}
		var total int64
		for _, r := range rows {
			total += r.BytesAB + r.BytesBA
		}
		fmt.Printf("\n统计区间：%s 起（UTC，按天），合计 %s。进行中的会话最多少算 %s（stats.flush_interval）。\n",
			sinceDay, fmtBytes(total), cfg.Stats.FlushInterval)
		if limit := cfg.Limits.RelayDailyMiB; limit > 0 {
			fmt.Printf("每日额度：每节点 %d MiB（limits.relay_daily_mib）。\n", limit)
		}
		return nil
	})
}

// relay7d 返回各节点最近 7 天（含今天，UTC）的中转用量，给 node list 的那一列用。
func relay7d(store *meshcoord.Store) map[meshid.ID]int64 {
	out := map[meshid.ID]int64{}
	rows, err := store.RelayDailyRows("", meshcoord.SinceDay(time.Now(), 7*24*time.Hour))
	if err != nil {
		return out
	}
	for _, s := range meshcoord.AggregateByNode(rows) {
		out[s.Node] += s.Total()
	}
	return out
}
