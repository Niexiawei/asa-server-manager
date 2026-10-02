package installer

import (
	"strings"
	"testing"
	"time"

	statepkg "asa-server/internal/state"
)

func withStateManager(t *testing.T) {
	t.Helper()
	_ = statepkg.CloseStateManager()
	if err := statepkg.InitStateManager(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = statepkg.CloseStateManager() })
}

// 启动流程的前几分钟（缓存预取、镜像同步、等闸门）还没有游戏进程，进程存活看不见它；
// 此时放行更新，预取会对着正被换掉的 exe 下载（审计 §5.2 P0-3）。
func TestUpdateRefusedWhileInstanceIsLaunching(t *testing.T) {
	withTempBaseDir(t)
	withStateManager(t)

	if err := statepkg.WriteInstanceState("srv", statepkg.StatusStartStartInitialization, ""); err != nil {
		t.Fatal(err)
	}
	err := beginServerFilesUpdate()
	if err == nil {
		endServerFilesUpdate()
		t.Fatal("实例正在启动时更新被放行了")
	}
	if !strings.Contains(err.Error(), "srv") {
		t.Errorf("错误里没有点名实例: %v", err)
	}
	if IsUpdatingServerFiles() {
		t.Error("被拒绝的更新没有放掉「更新中」标记")
	}

	if err := statepkg.WriteInstanceState("srv", statepkg.StatusStartFailed, "x"); err != nil {
		t.Fatal(err)
	}
	if err := beginServerFilesUpdate(); err != nil {
		t.Fatalf("启动结束后更新仍被拒绝: %v", err)
	}
	endServerFilesUpdate()
}

// asa-server 崩溃时停在启动中的旧记录不代表有启动在进行，不能永久挡住更新。
func TestUpdateIgnoresLaunchStateFromBeforeThisProcess(t *testing.T) {
	withTempBaseDir(t)
	withStateManager(t)

	if err := statepkg.WriteInstanceState("srv", statepkg.StatusStarting, ""); err != nil {
		t.Fatal(err)
	}
	orig := processStarted
	processStarted = time.Now().Add(time.Second)
	t.Cleanup(func() { processStarted = orig })

	if err := beginServerFilesUpdate(); err != nil {
		t.Fatalf("崩溃遗留的启动中状态挡住了更新: %v", err)
	}
	endServerFilesUpdate()
}
