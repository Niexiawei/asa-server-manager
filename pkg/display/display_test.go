package display

import (
	"reflect"
	"testing"
)

// TestTargetApplyAppendsLast: 显示以环境变量的形式追加，且必须排在最后 ——
// exec 取同名变量的最后一个，排最后就不会被将来新增的过滤逻辑吃掉。
func TestTargetApplyAppendsLast(t *testing.T) {
	d := Target{Env: []string{"DISPLAY=:0"}}
	env := d.Apply([]string{"PATH=/bin", "HOME=/root"})

	if want := []string{"PATH=/bin", "HOME=/root", "DISPLAY=:0"}; !reflect.DeepEqual(env, want) {
		t.Errorf("Apply = %v, want %v", env, want)
	}
}

// TestTargetApplyDoesNotAliasCaller: Apply 必须返回新切片。就地 append 会让
// 「解析一次显示、拿它包两条命令」悄悄污染第一条 —— internal/runner 的 vcredist
// 安装路径正是这么用的。
func TestTargetApplyDoesNotAliasCaller(t *testing.T) {
	env := make([]string, 1, 8) // 有富余容量，就地 append 不会重新分配
	env[0] = "PATH=/bin"

	d := Target{Env: []string{"DISPLAY=:0"}}
	got := d.Apply(env)

	if len(env) != 1 || env[0] != "PATH=/bin" {
		t.Errorf("caller env was mutated: %v", env)
	}
	if len(got) != 2 {
		t.Errorf("Apply = %v, want the caller env plus DISPLAY", got)
	}
}

// TestTargetApplyZeroValue: 空 Target 是恒等变换，调用方不必先判空。
func TestTargetApplyZeroValue(t *testing.T) {
	if got := (Target{}).Apply([]string{"K=V"}); !reflect.DeepEqual(got, []string{"K=V"}) {
		t.Errorf("zero Target changed the env: %v", got)
	}
}
