// Package countdown 负责「延迟一段时间后停止/重启实例」的全部编排。
//
// 拆出来之前，这件事被切成了五块：配置转换在 batchmanage / schedule / webapi 各有一份，
// 主循环与登记表在 instance，编排方式则每个调用方一套（选项 / 手写并发 / 透传）。
// 现在统一为：配置只有 FromSeconds / FromQuery 两个入口，编排只有 Wait / Stop / Restart 三个出口。
package countdown

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Action 倒计时的目标操作。
type Action string

const (
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
)

// Label 返回操作的中文名，用于日志与 {action} 占位符。
func (a Action) Label() string {
	if a == ActionRestart {
		return "重启"
	}
	return "停止"
}

// 倒计时总时长的上下界。
//
// 下界 30s：再短的倒计时对玩家没有意义，反而让公告刷在一起。
// 上界 24h：防止把秒当毫秒填这类手滑，配错了不至于让实例挂着一整周不停。
const (
	MinTotal  = 30 * time.Second
	MaxTotal  = 24 * time.Hour
	MaxPoints = 20
)

// DefaultTemplate 是未指定文案时的默认公告。
const DefaultTemplate = "服务器将在 {time} 后{action}，请及时下线"

// DefaultCommand 是未指定时使用的 RCON 指令。
// ServerChat 走聊天频道；Broadcast 是屏幕中央大字，更醒目也更打扰，留给调用方选。
const DefaultCommand = "ServerChat"

// defaultPoints 是用户只填了总时长、没给播报点位时的预设。
// 取其中所有不超过总时长的点位，天然形成「前疏后密」的节奏。
var defaultPoints = []time.Duration{
	time.Hour,
	30 * time.Minute,
	15 * time.Minute,
	10 * time.Minute,
	5 * time.Minute,
	4 * time.Minute,
	3 * time.Minute,
	2 * time.Minute,
	time.Minute,
	30 * time.Second,
	10 * time.Second,
	5 * time.Second,
	4 * time.Second,
	3 * time.Second,
	2 * time.Second,
	1 * time.Second,
}

// Config 停止/重启前的倒计时与游戏内公告配置。
type Config struct {
	// Total 倒计时总时长。为 0 表示不倒计时，立即执行。
	Total time.Duration

	// Points 播报时间点：在「剩余时间」等于这些值时各播报一次。
	// 允许乱序传入，内部会降序排序并去重；为空则按 Total 推导默认点位。
	Points []time.Duration

	// Template 公告文案，支持 {time} / {action} / {instance} 占位符。
	Template string

	// Command 发公告用的 RCON 指令，默认 ServerChat。
	Command string
}

// FromSeconds 是配置的统一构造入口：batchmanage 的 JSON 请求体、schedule 的任务定义
// 都走它，避免「秒 → Duration」这段转换再次分裂成多份。
//
// totalSeconds <= 0 返回 nil，表示不倒计时。返回值未经校验，调用方需要自行 Validate
// （Wait / Stop / Restart 入口也会兜底校验一次）。
func FromSeconds(totalSeconds int, points []int, template, command string) *Config {
	if totalSeconds <= 0 {
		return nil
	}

	converted := make([]time.Duration, 0, len(points))
	for _, p := range points {
		converted = append(converted, time.Duration(p)*time.Second)
	}

	return &Config{
		Total:    time.Duration(totalSeconds) * time.Second,
		Points:   converted,
		Template: template,
		Command:  command,
	}
}

// FromQuery 解析 query string 形式的倒计时参数：
//
//	?countdown=600&notify_points=600,300,60,30,10&notify_message=...&notify_command=ServerChat
//
// get 通常直接传 gin.Context.Query，countdown 包因此不依赖 gin。
// 全部可选：不传 countdown 返回 (nil, nil)，表示立即执行。
//
// 与 FromSeconds 不同，这里**会**校验——配错的参数应该在 HTTP 层就返回 400，
// 而不是等倒计时跑起来才发现有个点位永远触发不到。
func FromQuery(get func(string) string) (*Config, error) {
	raw := strings.TrimSpace(get("countdown"))
	if raw == "" {
		return nil, nil
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return nil, fmt.Errorf("countdown 必须是整数秒: %s", raw)
	}
	if seconds == 0 {
		return nil, nil
	}
	if seconds < 0 {
		return nil, fmt.Errorf("countdown 不能为负数")
	}

	points, err := parsePointsCSV(get("notify_points"))
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Total:    time.Duration(seconds) * time.Second,
		Points:   points,
		Template: get("notify_message"),
		Command:  get("notify_command"),
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// parsePointsCSV 解析 "600,300,60" 形式的播报点位。
func parsePointsCSV(raw string) ([]time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	var points []time.Duration
	for part := range strings.SplitSeq(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("notify_points 必须是逗号分隔的整数秒: %s", part)
		}
		points = append(points, time.Duration(n)*time.Second)
	}

	return points, nil
}

// Enabled 报告该配置是否要求倒计时。
func (c *Config) Enabled() bool {
	return c != nil && c.Total > 0
}

// Validate 校验倒计时配置。
//
// 这些检查必须在**保存/接收参数时**就跑，而不是等倒计时开始才发现有个点位
// 永远不会触发——那时玩家已经在等一条永远不来的公告了。
func (c *Config) Validate() error {
	if !c.Enabled() {
		return nil
	}

	if c.Total < MinTotal {
		return fmt.Errorf("倒计时最少 %d 秒", int(MinTotal.Seconds()))
	}
	if c.Total > MaxTotal {
		return fmt.Errorf("倒计时最多 %d 小时", int(MaxTotal.Hours()))
	}
	if len(c.Points) > MaxPoints {
		return fmt.Errorf("播报时间点最多 %d 个", MaxPoints)
	}

	for _, p := range c.Points {
		if p <= 0 {
			return fmt.Errorf("播报时间点必须大于 0")
		}
		// 核心规则：点位超过总时长就永远触发不到。
		// 允许相等——那表示「倒计时一开始就播一条」，是常见用法。
		if p > c.Total {
			return fmt.Errorf(
				"播报时间点 %d 秒超过了倒计时总时长 %d 秒",
				int(p.Seconds()), int(c.Total.Seconds()),
			)
		}
	}

	return nil
}

// normalized 返回补齐默认值并整理过点位的副本。
func (c *Config) normalized() Config {
	out := *c

	if out.Template == "" {
		out.Template = DefaultTemplate
	}
	if out.Command == "" {
		out.Command = DefaultCommand
	}
	out.Points = normalizePoints(out.Total, out.Points)

	return out
}

// normalizePoints 整理播报点位：去重、剔除越界值、按剩余时间降序排列。
// 传空则按 Total 推导默认点位。
func normalizePoints(total time.Duration, points []time.Duration) []time.Duration {
	if total <= 0 {
		return nil
	}

	if len(points) == 0 {
		points = defaultPoints
	}

	seen := make(map[time.Duration]bool, len(points))
	out := make([]time.Duration, 0, len(points))
	for _, p := range points {
		if p <= 0 || p > total || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}

	// 降序：先播剩余时间长的
	slices.SortFunc(out, func(a, b time.Duration) int {
		switch {
		case a > b:
			return -1
		case a < b:
			return 1
		default:
			return 0
		}
	})

	return out
}
