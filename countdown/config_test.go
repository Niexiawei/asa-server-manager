package countdown

import (
	"asa-server/logger"
	"os"
	"strings"
	"testing"
	"time"
)

// 本包的生产代码到处直接调 logger.GetLogger()，未初始化时它返回 nil，
// 任何走到日志的测试都会 panic。这里统一初始化到临时目录，
// 避免把日志写进仓库。
func TestMain(m *testing.M) {
	logger.InitLoggerWithBaseDir(os.TempDir())
	os.Exit(m.Run())
}

func TestFormatRemaining(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{90 * time.Minute, "1 小时 30 分钟"},
		{2 * time.Hour, "2 小时"},
		{10 * time.Minute, "10 分钟"},
		{90 * time.Second, "1 分 30 秒"},
		{30 * time.Second, "30 秒"},
		{0, "0 秒"},
		{-5 * time.Second, "0 秒"},
	}

	for _, tt := range tests {
		if got := formatRemaining(tt.in); got != tt.want {
			t.Errorf("formatRemaining(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRenderMessage(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		remaining time.Duration
		action    Action
		want      string
	}{
		{
			name:      "三个占位符都被替换",
			template:  "{instance} 将在 {time} 后{action}",
			remaining: 10 * time.Minute,
			action:    ActionRestart,
			want:      "meijue 将在 10 分钟 后重启",
		},
		{
			name:      "停止的 action 文案",
			template:  "{time} 后{action}",
			remaining: 30 * time.Second,
			action:    ActionStop,
			want:      "30 秒 后停止",
		},
		{
			name:      "没有占位符时原样输出",
			template:  "服务器即将维护",
			remaining: time.Minute,
			action:    ActionStop,
			want:      "服务器即将维护",
		},
		{
			name:      "未识别的占位符保持原样，不被清空",
			template:  "{time} 后{action}，联系 {foo}",
			remaining: time.Minute,
			action:    ActionStop,
			want:      "1 分钟 后停止，联系 {foo}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderMessage(tt.template, "meijue", tt.action, tt.remaining)
			if got != tt.want {
				t.Errorf("renderMessage()\ngot  = %q\nwant = %q", got, tt.want)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "未启用倒计时（Total=0）一律通过",
			cfg:  Config{Total: 0, Points: []time.Duration{time.Hour}},
		},
		{
			name:    "Total 29 秒 → 拒绝",
			cfg:     Config{Total: 29 * time.Second},
			wantErr: true,
		},
		{
			name: "Total 30 秒 → 通过",
			cfg:  Config{Total: 30 * time.Second},
		},
		{
			name:    "Total 超过 24 小时 → 拒绝",
			cfg:     Config{Total: 25 * time.Hour},
			wantErr: true,
		},
		{
			name: "Total=600 但点位 700 → 拒绝",
			cfg: Config{
				Total:  600 * time.Second,
				Points: []time.Duration{700 * time.Second},
			},
			wantErr: true,
		},
		{
			name: "Total=600 但点位 605 → 拒绝",
			cfg: Config{
				Total:  600 * time.Second,
				Points: []time.Duration{605 * time.Second},
			},
			wantErr: true,
		},
		{
			name: "点位等于 Total → 通过（开场即播是合法的）",
			cfg: Config{
				Total:  600 * time.Second,
				Points: []time.Duration{600 * time.Second},
			},
		},
		{
			name: "点位为 0 → 拒绝",
			cfg: Config{
				Total:  600 * time.Second,
				Points: []time.Duration{0},
			},
			wantErr: true,
		},
		{
			name: "点位为负 → 拒绝",
			cfg: Config{
				Total:  600 * time.Second,
				Points: []time.Duration{-time.Second},
			},
			wantErr: true,
		},
		{
			name: "超过 20 个点位 → 拒绝",
			cfg: Config{
				Total:  600 * time.Second,
				Points: make([]time.Duration, 21),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

// 越界点位的错误文案要能指出是哪个点位、超了什么，否则用户不知道改哪
func TestValidateErrorMentionsOffendingPoint(t *testing.T) {
	cfg := Config{
		Total:  600 * time.Second,
		Points: []time.Duration{700 * time.Second},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("期望校验失败")
	}
	if !strings.Contains(err.Error(), "700") || !strings.Contains(err.Error(), "600") {
		t.Errorf("错误文案应同时包含越界点位与总时长，实际: %v", err)
	}
}

func TestNormalizePoints(t *testing.T) {
	tests := []struct {
		name  string
		total time.Duration
		in    []time.Duration
		want  []int
	}{
		{
			name:  "Total=0 不产生任何点位（兼容开关）",
			total: 0,
			in:    []time.Duration{time.Minute},
			want:  []int{},
		},
		{
			name:  "默认点位由 Total 推导",
			total: 600 * time.Second,
			want:  []int{600, 300, 180, 60, 30, 10},
		},
		{
			name:  "Total=30 的默认点位",
			total: 30 * time.Second,
			want:  []int{30, 10},
		},
		{
			name:  "乱序输入按降序整理",
			total: 600 * time.Second,
			in:    []time.Duration{60 * time.Second, 600 * time.Second, 300 * time.Second},
			want:  []int{600, 300, 60},
		},
		{
			name:  "重复点位被去重",
			total: 600 * time.Second,
			in:    []time.Duration{60 * time.Second, 60 * time.Second},
			want:  []int{60},
		},
		{
			name:  "越界点位被剔除",
			total: 600 * time.Second,
			in:    []time.Duration{700 * time.Second, 60 * time.Second},
			want:  []int{60},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := seconds(normalizePoints(tt.total, tt.in))
			if !equalInts(got, tt.want) {
				t.Errorf("normalizePoints()\ngot  = %v\nwant = %v", got, tt.want)
			}
		})
	}
}

// FromSeconds 是三处重复转换收敛后的唯一构造入口，行为要钉死
func TestFromSeconds(t *testing.T) {
	if cfg := FromSeconds(0, []int{60}, "x", "y"); cfg != nil {
		t.Errorf("totalSeconds=0 应返回 nil（不倒计时），实际 %+v", cfg)
	}
	if cfg := FromSeconds(-1, nil, "", ""); cfg != nil {
		t.Errorf("totalSeconds<0 应返回 nil，实际 %+v", cfg)
	}

	cfg := FromSeconds(600, []int{600, 300, 60}, "自定义文案", "Broadcast")
	if cfg == nil {
		t.Fatal("totalSeconds>0 应返回配置")
	}
	if cfg.Total != 600*time.Second {
		t.Errorf("Total = %v, want 600s", cfg.Total)
	}
	if got := seconds(cfg.Points); !equalInts(got, []int{600, 300, 60}) {
		t.Errorf("Points = %v, want [600 300 60]（构造阶段不排序，交给 normalize）", got)
	}
	if cfg.Template != "自定义文案" || cfg.Command != "Broadcast" {
		t.Errorf("文案/指令未原样透传: %+v", cfg)
	}
}

func TestFromQuery(t *testing.T) {
	query := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	t.Run("不传 countdown 表示立即执行", func(t *testing.T) {
		cfg, err := FromQuery(query(map[string]string{}))
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if cfg != nil {
			t.Errorf("应返回 nil，实际 %+v", cfg)
		}
	})

	t.Run("countdown=0 等价于不倒计时", func(t *testing.T) {
		cfg, err := FromQuery(query(map[string]string{"countdown": "0"}))
		if err != nil || cfg != nil {
			t.Errorf("want (nil, nil), got (%+v, %v)", cfg, err)
		}
	})

	t.Run("非整数 countdown 报错", func(t *testing.T) {
		if _, err := FromQuery(query(map[string]string{"countdown": "abc"})); err == nil {
			t.Error("期望报错")
		}
	})

	t.Run("负数 countdown 报错", func(t *testing.T) {
		if _, err := FromQuery(query(map[string]string{"countdown": "-60"})); err == nil {
			t.Error("期望报错")
		}
	})

	t.Run("完整参数被解析", func(t *testing.T) {
		cfg, err := FromQuery(query(map[string]string{
			"countdown":      "600",
			"notify_points":  "600, 300 ,60",
			"notify_message": "{time} 后{action}",
			"notify_command": "Broadcast",
		}))
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if cfg.Total != 600*time.Second {
			t.Errorf("Total = %v", cfg.Total)
		}
		if got := seconds(cfg.Points); !equalInts(got, []int{600, 300, 60}) {
			t.Errorf("Points = %v，空白应被 trim", got)
		}
		if cfg.Command != "Broadcast" {
			t.Errorf("Command = %q", cfg.Command)
		}
	})

	t.Run("非法点位在 HTTP 层就被拒绝", func(t *testing.T) {
		// 点位 700 > 总时长 600，跑起来永远触发不到，必须在解析阶段挡下
		_, err := FromQuery(query(map[string]string{
			"countdown":     "600",
			"notify_points": "700",
		}))
		if err == nil {
			t.Error("期望校验失败")
		}
	})

	t.Run("countdown 低于下界被拒绝", func(t *testing.T) {
		if _, err := FromQuery(query(map[string]string{"countdown": "10"})); err == nil {
			t.Error("期望校验失败")
		}
	})
}

func seconds(ds []time.Duration) []int {
	out := make([]int, len(ds))
	for i, d := range ds {
		out[i] = int(d.Seconds())
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
