package instance

import (
	statepkg "asa-server/internal/state"
	"asa-server/pkg/logger"
	"os"
	"testing"
)

// withTempStateManager 把全局状态管理器指到一个临时目录，避免污染真实状态库。
func withTempStateManager(t *testing.T) {
	t.Helper()

	logger.InitLoggerWithBaseDir(os.TempDir())

	// 前面的用例可能已经初始化过全局单例，先关掉才能重新指向临时目录
	_ = statepkg.CloseStateManager()
	if err := statepkg.InitStateManager(t.TempDir()); err != nil {
		t.Fatalf("初始化临时状态管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = statepkg.CloseStateManager() })
}

// 实例没有任何状态记录时，绝不能放行。
//
// 这是本轮 bug 的直接回归防线：以前这里 return true 放行，于是一台早就停了的实例
// 被拉进倒计时，公告发不出去、到点也停不掉，而阶段二的 CAS 对「无记录」并不报错，
// 只是悄悄 skip——白烧一整轮。
func TestIsStoppableRejectsInstanceWithoutState(t *testing.T) {
	withTempStateManager(t)

	// 这个实例名没有配置文件，进程判活必然为假
	const name = "___instance_without_state"

	if _, err := statepkg.GetLatestInstanceState(name); err == nil {
		t.Fatal("测试前提：该实例本应读不到状态记录")
	}

	ok, reason := IsStoppable(name)
	if ok {
		t.Error("无状态记录且进程已死的实例不应被判为可停止")
	}
	if reason != ReasonProcessGone {
		t.Errorf("原因应为 %q，实际 %q", ReasonProcessGone, reason)
	}

	// 补写记录才是关键：让 CAS、前端列表、下一次预检读到同一个事实
	if _, err := statepkg.GetLatestInstanceState(name); err != nil {
		t.Errorf("预检应补写一条状态记录，实际仍读不到: %v", err)
	}
	if got := statepkg.GetInstanceStateOrDefault(name).Status; got != statepkg.StatusStopped {
		t.Errorf("补写的状态应为 stopped，实际 %q", got)
	}
}

// 状态明确是 stopped 时同样不放行，且不该去查进程（这条路径连 netstat 都不该跑）
func TestIsStoppableRejectsStoppedInstance(t *testing.T) {
	withTempStateManager(t)

	const name = "___instance_stopped"
	if err := statepkg.WriteInstanceState(name, statepkg.StatusStopped, ""); err != nil {
		t.Fatalf("写入前置状态失败: %v", err)
	}

	ok, reason := IsStoppable(name)
	if ok {
		t.Error("已停止的实例不应被判为可停止")
	}
	if reason != ReasonNotStarted {
		t.Errorf("原因应为 %q，实际 %q", ReasonNotStarted, reason)
	}
}

// 状态是 started 但进程已经死了（服务器崩溃、状态没来得及更新）：
// 同样要拦下，否则又是空跑一轮倒计时再失败
func TestIsStoppableRejectsStartedButDeadInstance(t *testing.T) {
	withTempStateManager(t)

	const name = "___instance_started_but_dead"
	if err := statepkg.WriteInstanceState(name, statepkg.StatusStarted, ""); err != nil {
		t.Fatalf("写入前置状态失败: %v", err)
	}

	ok, reason := IsStoppable(name)
	if ok {
		t.Error("进程已不存在的实例不应被判为可停止")
	}
	if reason != ReasonProcessGone {
		t.Errorf("原因应为 %q，实际 %q", ReasonProcessGone, reason)
	}
}
