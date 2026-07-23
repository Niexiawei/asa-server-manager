package instance

import (
	"asa-server/logger"
	"context"
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

func TestRenderCountdownMessage(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		remaining time.Duration
		action    string
		want      string
	}{
		{
			name:      "三个占位符都被替换",
			template:  "{instance} 将在 {time} 后{action}",
			remaining: 10 * time.Minute,
			action:    CountdownActionRestart,
			want:      "meijue 将在 10 分钟 后重启",
		},
		{
			name:      "停止的 action 文案",
			template:  "{time} 后{action}",
			remaining: 30 * time.Second,
			action:    CountdownActionStop,
			want:      "30 秒 后停止",
		},
		{
			name:      "没有占位符时原样输出",
			template:  "服务器即将维护",
			remaining: time.Minute,
			action:    CountdownActionStop,
			want:      "服务器即将维护",
		},
		{
			name:      "未识别的占位符保持原样，不被清空",
			template:  "{time} 后{action}，联系 {foo}",
			remaining: time.Minute,
			action:    CountdownActionStop,
			want:      "1 分钟 后停止，联系 {foo}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderCountdownMessage(tt.template, "meijue", tt.action, tt.remaining)
			if got != tt.want {
				t.Errorf("renderCountdownMessage()\ngot  = %q\nwant = %q", got, tt.want)
			}
		})
	}
}

func TestCountdownConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     CountdownConfig
		wantErr bool
	}{
		{
			name: "未启用倒计时（Total=0）一律通过",
			cfg:  CountdownConfig{Total: 0, Points: []time.Duration{time.Hour}},
		},
		{
			name:    "Total 29 秒 → 拒绝",
			cfg:     CountdownConfig{Total: 29 * time.Second},
			wantErr: true,
		},
		{
			name: "Total 30 秒 → 通过",
			cfg:  CountdownConfig{Total: 30 * time.Second},
		},
		{
			name:    "Total 超过 24 小时 → 拒绝",
			cfg:     CountdownConfig{Total: 25 * time.Hour},
			wantErr: true,
		},
		{
			// 本次评审点名要防的情况
			name: "Total=600 但点位 700 → 拒绝",
			cfg: CountdownConfig{
				Total:  600 * time.Second,
				Points: []time.Duration{700 * time.Second},
			},
			wantErr: true,
		},
		{
			name: "Total=600 但点位 605 → 拒绝",
			cfg: CountdownConfig{
				Total:  600 * time.Second,
				Points: []time.Duration{605 * time.Second},
			},
			wantErr: true,
		},
		{
			name: "点位等于 Total → 通过（开场即播是合法的）",
			cfg: CountdownConfig{
				Total:  600 * time.Second,
				Points: []time.Duration{600 * time.Second},
			},
		},
		{
			name: "点位为 0 → 拒绝",
			cfg: CountdownConfig{
				Total:  600 * time.Second,
				Points: []time.Duration{0},
			},
			wantErr: true,
		},
		{
			name: "点位为负 → 拒绝",
			cfg: CountdownConfig{
				Total:  600 * time.Second,
				Points: []time.Duration{-time.Second},
			},
			wantErr: true,
		},
		{
			name: "超过 20 个点位 → 拒绝",
			cfg: CountdownConfig{
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
	cfg := CountdownConfig{
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
	secs := func(ds []time.Duration) []int {
		out := make([]int, len(ds))
		for i, d := range ds {
			out[i] = int(d.Seconds())
		}
		return out
	}
	equal := func(a []int, b []int) bool {
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
			got := secs(normalizePoints(tt.total, tt.in))
			if !equal(got, tt.want) {
				t.Errorf("normalizePoints()\ngot  = %v\nwant = %v", got, tt.want)
			}
		})
	}
}

func TestCountdownDisabledIsNoop(t *testing.T) {
	cfg := &CountdownConfig{}
	if cfg.Enabled() {
		t.Error("Total=0 时 Enabled() 应为 false")
	}

	// 未启用时应立即返回，不阻塞、不报错
	done := make(chan error, 1)
	go func() { done <- RunCountdown(context.Background(), "meijue", CountdownActionStop, cfg) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("未启用倒计时时 RunCountdown 不应报错，实际: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("未启用倒计时时 RunCountdown 应立即返回")
	}
}

// ctx 取消后 RunCountdown 必须返回错误，调用方据此**不**执行停止。
// 若这条失效，「取消关服」会变成「延迟关服」。
func TestRunCountdownCancelled(t *testing.T) {
	cfg := &CountdownConfig{
		Total: MinCountdownTotal,
		// 点位留空会走默认推导，这里显式给一个很小的集合避免测试期间真的发 RCON
		Points: []time.Duration{time.Second},
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- RunCountdown(ctx, "___countdown_test_instance", CountdownActionStop, cfg) }()

	// 等倒计时登记完成再取消
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("倒计时被取消后 RunCountdown 应返回错误，否则调用方会继续执行停止")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunCountdown 未在取消后及时返回")
	}

	// 取消后登记表应被清理
	if _, ok := GetCountdown("___countdown_test_instance"); ok {
		t.Error("倒计时取消后登记表未清理")
	}
}

// CancelCountdown 走的是登记表，和 ctx 取消是两条路，各测一次
func TestCancelCountdownViaRegistry(t *testing.T) {
	const name = "___countdown_cancel_test"

	cfg := &CountdownConfig{
		Total:  MinCountdownTotal,
		Points: []time.Duration{time.Second},
	}

	done := make(chan error, 1)
	go func() { done <- RunCountdown(context.Background(), name, CountdownActionRestart, cfg) }()

	time.Sleep(200 * time.Millisecond)

	status, ok := GetCountdown(name)
	if !ok {
		t.Fatal("倒计时进行中，GetCountdown 应能查到")
	}
	if status.Action != CountdownActionRestart {
		t.Errorf("action = %q, want %q", status.Action, CountdownActionRestart)
	}
	if status.Remaining <= 0 {
		t.Errorf("倒计时进行中，剩余秒数应大于 0，实际 %d", status.Remaining)
	}

	if !CancelCountdown(name) {
		t.Fatal("CancelCountdown 应返回 true")
	}

	select {
	case err := <-done:
		if err == nil {
			t.Error("被取消的倒计时应返回错误")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunCountdown 未在取消后及时返回")
	}

	if CancelCountdown(name) {
		t.Error("已结束的倒计时不应还能被取消")
	}
}
