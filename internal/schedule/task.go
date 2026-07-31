// Package schedule provides timed execution of server maintenance tasks
// (scheduled updates and scheduled restarts).
package schedule

import (
	"asa-server/countdown"
	"fmt"
	"time"
)

// RuleType 触发规则类型。
type RuleType string

const (
	RuleIntervalHours RuleType = "interval_hours" // 每 N 小时
	RuleIntervalDays  RuleType = "interval_days"  // 每 N 天，在 At 指定的时刻
	RuleDaily         RuleType = "daily"          // 每天，在 At 指定的时刻
)

// TaskType 任务类型。
type TaskType string

const (
	// TaskUpdate 更新服务端文件。执行时会先停掉全部实例——
	// installer 在有实例存活时会拒绝更新，所以停服不是可选项而是前置条件。
	TaskUpdate TaskType = "update"
	// TaskRestart 批量重启实例。
	TaskRestart TaskType = "restart"
)

// timeLayout 是 At 字段的格式（24 小时制）。
const timeLayout = "15:04"

// Task 一条定时任务。
type Task struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Type    TaskType `json:"type"`
	Enabled bool     `json:"enabled"`

	Rule  RuleType `json:"rule"`
	Every int      `json:"every"` // interval_* 用；daily 忽略
	At    string   `json:"at"`    // "HH:mm"；interval_hours 忽略

	// Instances 仅 restart 使用，空表示全部实例。
	Instances []string `json:"instances"`

	// Countdown 执行前的倒计时秒数，0 = 不倒计时（默认，保持原有行为）。
	// 对 update 任务作用于「更新前的批量停服」那一步——
	// 半夜自动更新同样需要提前通知玩家。
	Countdown     int    `json:"countdown"`
	NotifyPoints  []int  `json:"notify_points"`
	NotifyMessage string `json:"notify_message"`
	NotifyCommand string `json:"notify_command"`

	LastRunAt  *time.Time `json:"last_run_at,omitempty"`
	NextRunAt  *time.Time `json:"next_run_at,omitempty"`
	LastResult string     `json:"last_result,omitempty"`
}

// CountdownConfig 把任务里的倒计时字段转成 countdown 包的配置。
// Countdown 为 0 时返回 nil，表示不倒计时。
func (t *Task) CountdownConfig() *countdown.Config {
	return countdown.FromSeconds(t.Countdown, t.NotifyPoints, t.NotifyMessage, t.NotifyCommand)
}

// Validate 校验任务字段。
func (t *Task) Validate() error {
	if t.Name == "" {
		return fmt.Errorf("任务名称不能为空")
	}

	switch t.Type {
	case TaskUpdate, TaskRestart:
	default:
		return fmt.Errorf("未知的任务类型: %s", t.Type)
	}

	switch t.Rule {
	case RuleIntervalHours:
		if t.Every < 1 {
			return fmt.Errorf("间隔小时数必须大于 0")
		}
	case RuleIntervalDays:
		if t.Every < 1 {
			return fmt.Errorf("间隔天数必须大于 0")
		}
		if err := validateAt(t.At); err != nil {
			return err
		}
	case RuleDaily:
		if err := validateAt(t.At); err != nil {
			return err
		}
	default:
		return fmt.Errorf("未知的规则类型: %s", t.Rule)
	}

	// update 作用于整个服务端文件，指定实例没有意义
	if t.Type == TaskUpdate && len(t.Instances) > 0 {
		return fmt.Errorf("更新任务不能指定实例")
	}

	// 复用 instance 包那一套倒计时校验，避免两边规则漂移
	if cfg := t.CountdownConfig(); cfg != nil {
		if err := cfg.Validate(); err != nil {
			return err
		}
	}

	return nil
}

func validateAt(at string) error {
	if at == "" {
		return fmt.Errorf("执行时刻不能为空")
	}
	if _, err := time.Parse(timeLayout, at); err != nil {
		return fmt.Errorf("执行时刻格式错误，应为 HH:mm: %s", at)
	}
	return nil
}

// intervalDays 返回该规则对应的天数间隔。
// daily 在语义上就是 interval_days 且 Every=1，两者共用下面的计算；
// 之所以仍保留成两个类型，是为了让用户选了「每天」再打开编辑弹窗时看到的还是「每天」。
func (t *Task) intervalDays() int {
	if t.Rule == RuleDaily {
		return 1
	}
	return t.Every
}

// NextRun 计算基于 from 之后的下一次执行时刻。
//
// 返回值**严格晚于** from：即便任务已经很久没跑，也只推进到下一个未来时刻，
// 不会返回过去的时间（调度器据此实现「错过的执行不补跑」）。
func (t *Task) NextRun(from time.Time) (time.Time, error) {
	if err := t.Validate(); err != nil {
		return time.Time{}, err
	}

	if t.Rule == RuleIntervalHours {
		return from.Add(time.Duration(t.Every) * time.Hour), nil
	}

	// 按天的规则：取 At 指定的时刻，从今天开始按间隔往后找第一个晚于 from 的
	at, err := time.Parse(timeLayout, t.At)
	if err != nil {
		return time.Time{}, fmt.Errorf("执行时刻格式错误，应为 HH:mm: %s", t.At)
	}

	step := t.intervalDays()
	candidate := time.Date(
		from.Year(), from.Month(), from.Day(),
		at.Hour(), at.Minute(), 0, 0,
		from.Location(),
	)

	// AddDate 而不是加 24h：这样跨夏令时/时区调整时执行时刻仍然固定在 At
	for !candidate.After(from) {
		candidate = candidate.AddDate(0, 0, step)
	}

	return candidate, nil
}

// RuleSummary 返回规则的中文摘要，供日志与 UI 使用。
func (t *Task) RuleSummary() string {
	switch t.Rule {
	case RuleIntervalHours:
		return fmt.Sprintf("每 %d 小时", t.Every)
	case RuleIntervalDays:
		return fmt.Sprintf("每 %d 天 %s", t.Every, t.At)
	case RuleDaily:
		return fmt.Sprintf("每天 %s", t.At)
	default:
		return string(t.Rule)
	}
}
