package instance

import (
	"strings"
	"testing"
)

// PrecheckStart 是给用户看的提示，不是权威判断：它自己读不到东西时必须放行，
// 让真正的启动路径去决定。拦在这里的代价是「实例明明能起，面板却说不能」，
// 而放行的代价只是错过一次提前提示。
func TestPrecheckStart_SilentWhenInstanceUnknown(t *testing.T) {
	if err := PrecheckStart("no-such-instance-" + t.Name()); err != nil {
		t.Fatalf("读不到实例配置时必须放行，got %v", err)
	}
}

// 冲突文案要同时点到两台实例的名字与出路：用户看到的第一句话就是他要做的事。
// 这条同时钉住 HTTP 应答与日志用的是同一段话（两处都调这个函数）。
func TestArkApiConflictError_MentionsBothInstancesAndTheWayOut(t *testing.T) {
	msg := arkApiConflictError("jibian-pve", "meijue-pve").Error()

	for _, want := range []string{"jibian-pve", "meijue-pve", "per-instance", "prefix_mode"} {
		if !strings.Contains(msg, want) {
			t.Errorf("冲突文案里缺少 %q：%s", want, msg)
		}
	}
}
