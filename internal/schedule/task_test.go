package schedule

import (
	"testing"
	"time"
)

func at(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		t.Fatalf("测试时间解析失败 %q: %v", s, err)
	}
	return parsed
}

func TestNextRun(t *testing.T) {
	tests := []struct {
		name string
		task Task
		from string
		want string
	}{
		{
			name: "每 6 小时",
			task: Task{Name: "t", Type: TaskRestart, Rule: RuleIntervalHours, Every: 6},
			from: "2026-07-20 10:00",
			want: "2026-07-20 16:00",
		},
		{
			name: "每 2 天 04:30 —— 应落在两天后的 04:30，而不是 48 小时后",
			task: Task{Name: "t", Type: TaskRestart, Rule: RuleIntervalDays, Every: 2, At: "04:30"},
			from: "2026-07-20 10:00",
			want: "2026-07-22 04:30",
		},
		{
			name: "每天，当前早于今天的执行时刻 → 取今天",
			task: Task{Name: "t", Type: TaskRestart, Rule: RuleDaily, At: "04:30"},
			from: "2026-07-20 01:00",
			want: "2026-07-20 04:30",
		},
		{
			name: "每天，当前晚于今天的执行时刻 → 取明天",
			task: Task{Name: "t", Type: TaskRestart, Rule: RuleDaily, At: "04:30"},
			from: "2026-07-20 10:00",
			want: "2026-07-21 04:30",
		},
		{
			name: "每天，恰好等于执行时刻 → 取明天（返回值必须严格晚于 from）",
			task: Task{Name: "t", Type: TaskRestart, Rule: RuleDaily, At: "04:30"},
			from: "2026-07-20 04:30",
			want: "2026-07-21 04:30",
		},
		{
			name: "跨月边界",
			task: Task{Name: "t", Type: TaskRestart, Rule: RuleDaily, At: "03:00"},
			from: "2026-07-31 12:00",
			want: "2026-08-01 03:00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.task.NextRun(at(t, tt.from))
			if err != nil {
				t.Fatalf("NextRun() error = %v", err)
			}
			if want := at(t, tt.want); !got.Equal(want) {
				t.Errorf("NextRun()\ngot  = %s\nwant = %s", got, want)
			}
		})
	}
}

// 「错过的执行不补跑」完全依赖 NextRun 永远返回严格晚于基准的时刻。
// 若这条失效，调度器在 realignAll 里会算出一个过去的时间，
// 进程一重启就把所有实例重启一遍。
func TestNextRunIsAlwaysStrictlyAfterBase(t *testing.T) {
	tasks := []Task{
		{Name: "t", Type: TaskRestart, Rule: RuleIntervalHours, Every: 3},
		{Name: "t", Type: TaskRestart, Rule: RuleIntervalDays, Every: 7, At: "05:00"},
		{Name: "t", Type: TaskRestart, Rule: RuleDaily, At: "05:00"},
	}

	// 长期停机（很旧的基准）与正常运行（当前时间）两种基准都要成立
	bases := []time.Time{at(t, "2020-01-01 00:00"), time.Now()}

	for _, task := range tasks {
		for _, base := range bases {
			got, err := task.NextRun(base)
			if err != nil {
				t.Fatalf("NextRun() error = %v", err)
			}
			if !got.After(base) {
				t.Errorf("%s: 基于 %s 算出的 %s 不晚于基准", task.RuleSummary(), base, got)
			}
		}
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		task    Task
		wantErr bool
	}{
		{
			name: "合法的每日重启",
			task: Task{Name: "重启", Type: TaskRestart, Rule: RuleDaily, At: "04:00"},
		},
		{
			name: "合法的间隔小时",
			task: Task{Name: "重启", Type: TaskRestart, Rule: RuleIntervalHours, Every: 6},
		},
		{
			name:    "名称为空",
			task:    Task{Type: TaskRestart, Rule: RuleDaily, At: "04:00"},
			wantErr: true,
		},
		{
			name:    "Every 为 0",
			task:    Task{Name: "x", Type: TaskRestart, Rule: RuleIntervalHours, Every: 0},
			wantErr: true,
		},
		{
			name:    "非法时刻",
			task:    Task{Name: "x", Type: TaskRestart, Rule: RuleDaily, At: "25:99"},
			wantErr: true,
		},
		{
			name:    "缺少时刻",
			task:    Task{Name: "x", Type: TaskRestart, Rule: RuleIntervalDays, Every: 2},
			wantErr: true,
		},
		{
			name:    "未知规则类型",
			task:    Task{Name: "x", Type: TaskRestart, Rule: "weekly", Every: 1},
			wantErr: true,
		},
		{
			name:    "未知任务类型",
			task:    Task{Name: "x", Type: "reboot", Rule: RuleDaily, At: "04:00"},
			wantErr: true,
		},
		{
			name:    "更新任务指定了实例",
			task:    Task{Name: "x", Type: TaskUpdate, Rule: RuleDaily, At: "04:00", Instances: []string{"a"}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.task.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}
