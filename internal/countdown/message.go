package countdown

import (
	"fmt"
	"strings"
	"time"
)

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

// renderMessage 渲染公告文案。
//
// 未识别的占位符原样保留：用户写错了应该能在游戏里一眼看出来，
// 静默吞掉只会让人以为是功能坏了。
func renderMessage(template, instanceName string, action Action, remaining time.Duration) string {
	return strings.NewReplacer(
		"{time}", formatRemaining(remaining),
		"{action}", action.Label(),
		"{instance}", instanceName,
	).Replace(template)
}
