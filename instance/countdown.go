package instance

import (
	"asa-server/logger"
	"asa-server/realtime"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// 倒计时的目标操作。
const (
	CountdownActionStop    = "stop"
	CountdownActionRestart = "restart"
)

// 倒计时总时长的上下界。
//
// 下界 30s：再短的倒计时对玩家没有意义，反而让公告刷在一起。
// 上界 24h：防止把秒当毫秒填这类手滑，配错了不至于让实例挂着一整周不停。
const (
	MinCountdownTotal = 30 * time.Second
	MaxCountdownTotal = 24 * time.Hour
	MaxCountdownPoint = 20
)

// defaultNotifyPoints 是用户只填了总时长、没给播报点位时的预设。
// 取其中所有不超过总时长的点位，天然形成「前疏后密」的节奏。
var defaultNotifyPoints = []time.Duration{
	time.Hour,
	30 * time.Minute,
	15 * time.Minute,
	10 * time.Minute,
	5 * time.Minute,
	3 * time.Minute,
	time.Minute,
	30 * time.Second,
	10 * time.Second,
}

// DefaultCountdownTemplate 是未指定文案时的默认公告。
const DefaultCountdownTemplate = "服务器将在 {time} 后{action}，请及时下线"

// DefaultCountdownCommand 是未指定时使用的 RCON 指令。
// ServerChat 走聊天频道；Broadcast 是屏幕中央大字，更醒目也更打扰，留给调用方选。
const DefaultCountdownCommand = "ServerChat"

// CountdownConfig 停止/重启前的倒计时与游戏内公告配置。
type CountdownConfig struct {
	// Total 倒计时总时长。为 0 表示不倒计时，立即执行（保持现有行为）。
	Total time.Duration

	// Points 播报时间点：在「剩余时间」等于这些值时各播报一次。
	// 允许乱序传入，内部会降序排序并去重；为空则按 Total 推导默认点位。
	Points []time.Duration

	// Template 公告文案，支持 {time} / {action} / {instance} 占位符。
	Template string

	// Command 发公告用的 RCON 指令，默认 ServerChat。
	Command string
}

// Enabled 报告该配置是否要求倒计时。
func (c *CountdownConfig) Enabled() bool {
	return c != nil && c.Total > 0
}

// Validate 校验倒计时配置。
//
// 这些检查必须在**保存/接收参数时**就跑，而不是等倒计时开始才发现有个点位
// 永远不会触发——那时玩家已经在等一条永远不来的公告了。
func (c *CountdownConfig) Validate() error {
	if !c.Enabled() {
		return nil
	}

	if c.Total < MinCountdownTotal {
		return fmt.Errorf("倒计时最少 %d 秒", int(MinCountdownTotal.Seconds()))
	}
	if c.Total > MaxCountdownTotal {
		return fmt.Errorf("倒计时最多 %d 小时", int(MaxCountdownTotal.Hours()))
	}
	if len(c.Points) > MaxCountdownPoint {
		return fmt.Errorf("播报时间点最多 %d 个", MaxCountdownPoint)
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
func (c *CountdownConfig) normalized() CountdownConfig {
	out := *c

	if out.Template == "" {
		out.Template = DefaultCountdownTemplate
	}
	if out.Command == "" {
		out.Command = DefaultCountdownCommand
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
		points = defaultNotifyPoints
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

// formatRemaining 把剩余时间格式化成中文，按量级选粒度。
func formatRemaining(d time.Duration) string {
	if d < 0 {
		d = 0
	}

	totalSeconds := int(d.Round(time.Second).Seconds())

	switch {
	case totalSeconds >= 3600:
		hours := totalSeconds / 3600
		minutes := (totalSeconds % 3600) / 60
		if minutes == 0 {
			return fmt.Sprintf("%d 小时", hours)
		}
		return fmt.Sprintf("%d 小时 %d 分钟", hours, minutes)
	case totalSeconds >= 60:
		minutes := totalSeconds / 60
		seconds := totalSeconds % 60
		if seconds == 0 {
			return fmt.Sprintf("%d 分钟", minutes)
		}
		return fmt.Sprintf("%d 分 %d 秒", minutes, seconds)
	default:
		return fmt.Sprintf("%d 秒", totalSeconds)
	}
}

// actionLabel 返回操作的中文名，用于 {action} 占位符。
func actionLabel(action string) string {
	if action == CountdownActionRestart {
		return "重启"
	}
	return "停止"
}

// renderCountdownMessage 渲染公告文案。
//
// 未识别的占位符原样保留：用户写错了应该能在游戏里一眼看出来，
// 静默吞掉只会让人以为是功能坏了。
func renderCountdownMessage(template, instanceName, action string, remaining time.Duration) string {
	return strings.NewReplacer(
		"{time}", formatRemaining(remaining),
		"{action}", actionLabel(action),
		"{instance}", instanceName,
	).Replace(template)
}

// ---- 运行中的倒计时登记表 ----

// CountdownStatus 是某个实例当前的倒计时状态，供 HTTP 查询。
type CountdownStatus struct {
	InstanceName string `json:"instance_name"`
	Action       string `json:"action"`
	Phase        string `json:"phase"`
	Remaining    int    `json:"remaining"`
}

type countdownEntry struct {
	action   string
	phase    string
	deadline time.Time
	cancel   context.CancelFunc
}

var (
	countdownMu      sync.RWMutex
	activeCountdowns = make(map[string]*countdownEntry)
)

// GetCountdown 查询实例当前的倒计时状态。
func GetCountdown(instanceName string) (*CountdownStatus, bool) {
	countdownMu.RLock()
	defer countdownMu.RUnlock()

	entry, ok := activeCountdowns[instanceName]
	if !ok {
		return nil, false
	}

	remaining := 0
	if entry.phase == realtime.CountdownPhaseCounting {
		if d := time.Until(entry.deadline); d > 0 {
			remaining = int(d.Round(time.Second).Seconds())
		}
	}

	return &CountdownStatus{
		InstanceName: instanceName,
		Action:       entry.action,
		Phase:        entry.phase,
		Remaining:    remaining,
	}, true
}

// CancelCountdown 取消实例正在进行的倒计时。
// 返回 false 表示该实例当前没有在倒计时（或已进入执行阶段，取消不了了）。
func CancelCountdown(instanceName string) bool {
	countdownMu.Lock()

	entry, ok := activeCountdowns[instanceName]
	// 已经进入执行阶段就晚了：停止流程已经开始，取消没有意义
	if !ok || entry.phase != realtime.CountdownPhaseCounting {
		countdownMu.Unlock()
		return false
	}
	cancel := entry.cancel
	countdownMu.Unlock()

	cancel()
	return true
}

// markCountdownPhase 更新登记表里的阶段并推送 WS。
func markCountdownPhase(instanceName, phase, message string) {
	countdownMu.Lock()
	entry, ok := activeCountdowns[instanceName]
	if ok {
		entry.phase = phase
	}
	action := CountdownActionStop
	if ok {
		action = entry.action
	}
	countdownMu.Unlock()

	realtime.BroadcastCountdownEvent(instanceName, action, phase, message, 0)
}

// ---- 倒计时主流程 ----

// RunCountdown 执行一轮倒计时：按配置的点位发游戏内公告，并每秒推送 WS 进度。
//
// 返回 nil 表示倒计时正常走完，调用方可以继续执行停止/重启；
// 返回 error（ctx 取消或被 CancelCountdown 打断）表示**不应**继续执行。
//
// 公告发送失败不会中断倒计时——RCON 发不出去（玩家全下线、服务器卡住）
// 只记 WARN。公告是尽力而为的通知，不是停止操作的前置条件。
func RunCountdown(ctx context.Context, instanceName, action string, cfg *CountdownConfig) error {
	if !cfg.Enabled() {
		return nil
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	norm := cfg.normalized()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	deadline := time.Now().Add(norm.Total)

	countdownMu.Lock()
	activeCountdowns[instanceName] = &countdownEntry{
		action:   action,
		phase:    realtime.CountdownPhaseCounting,
		deadline: deadline,
		cancel:   cancel,
	}
	countdownMu.Unlock()

	logger.GetLogger().Infof(
		"Countdown started for instance %s: %s until %s, %d notify point(s)",
		instanceName, norm.Total, deadline.Format(time.TimeOnly), len(norm.Points),
	)

	err := runCountdownLoop(ctx, instanceName, action, &norm, deadline)

	if err != nil {
		markCountdownPhase(instanceName, realtime.CountdownPhaseCancelled,
			fmt.Sprintf("实例 %s 的%s已取消", instanceName, actionLabel(action)))
		clearCountdown(instanceName)
		return err
	}

	// 倒计时归零 ≠ 操作完成：接下来的停止本身还要花时间。
	// 切到 executing，让前端显示「服务器关闭中…」而不是继续卡在 0 秒。
	markCountdownPhase(instanceName, realtime.CountdownPhaseExecuting,
		fmt.Sprintf("服务器%s中…", actionLabel(action)))

	return nil
}

// FinishCountdown 清理实例的倒计时登记。停止/重启流程结束后调用。
func FinishCountdown(instanceName string) {
	clearCountdown(instanceName)
}

func clearCountdown(instanceName string) {
	countdownMu.Lock()
	delete(activeCountdowns, instanceName)
	countdownMu.Unlock()
}

// runCountdownLoop 每秒推一次 WS 进度，并在到达点位时发游戏内公告。
//
// 每秒 tick 是为了让前端能平滑逐秒递减，而不是只在点位跳变。
func runCountdownLoop(
	ctx context.Context,
	instanceName, action string,
	cfg *CountdownConfig,
	deadline time.Time,
) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// 点位按剩余时间降序排列，用一个游标从前往后消费
	pending := slices.Clone(cfg.Points)

	announce := func(remaining time.Duration) {
		msg := renderCountdownMessage(cfg.Template, instanceName, action, remaining)
		if _, err := SendRCONCommand(instanceName, fmt.Sprintf("%s %s", cfg.Command, msg)); err != nil {
			logger.GetLogger().Warnf(
				"Failed to announce countdown to instance %s (countdown continues): %v",
				instanceName, err,
			)
		}
	}

	// 剩余时间恰好等于总时长的点位要在开场立即播，等第一个 tick 就晚了 1 秒
	remaining := time.Until(deadline)
	for len(pending) > 0 && pending[0] >= remaining {
		announce(pending[0])
		pending = pending[1:]
	}
	pushCountdownProgress(instanceName, action, cfg.Template, remaining)

	for {
		select {
		case <-ctx.Done():
			logger.GetLogger().Infof("Countdown for instance %s cancelled", instanceName)
			return ctx.Err()

		case <-ticker.C:
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return nil
			}

			// 一个 tick 内可能跨过多个点位（例如系统休眠后唤醒），全部补播
			for len(pending) > 0 && pending[0] >= remaining {
				announce(pending[0])
				pending = pending[1:]
			}

			pushCountdownProgress(instanceName, action, cfg.Template, remaining)
		}
	}
}

// pushCountdownProgress 推送一条 WS 倒计时进度。
func pushCountdownProgress(instanceName, action, template string, remaining time.Duration) {
	if remaining < 0 {
		remaining = 0
	}

	realtime.BroadcastCountdownEvent(
		instanceName,
		action,
		realtime.CountdownPhaseCounting,
		renderCountdownMessage(template, instanceName, action, remaining),
		int(remaining.Round(time.Second).Seconds()),
	)
}
