// Package updatemanage owns the server-files update orchestration.
//
// 这段编排原先长在 webapi/serverapi 的 Handler 上，只有 HTTP 层能触发。
// 定时调度属于领域层，不能反向 import webapi，因此下沉成全局单例，
// 写法对照 batchmanage：Initialize() / GetGlobalManager()。
package updatemanage

import (
	"asa-server/installer"
	"asa-server/logger"
	procpkg "asa-server/process"
	"asa-server/realtime"
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// UpdateManager 全局单例，同一时刻只允许一次更新在跑。
type UpdateManager struct {
	broadcaster *realtime.TaskBroadcaster
	cancel      atomic.Pointer[context.CancelFunc]

	// done 在每次更新结束时关闭，供调度器等待更新完成。
	// 初值是一个已关闭的 channel，这样「从未跑过」时等待方不会永久阻塞。
	mu   sync.Mutex
	done chan struct{}
}

var globalManager *UpdateManager

// Initialize 初始化全局 UpdateManager。
func Initialize() {
	closed := make(chan struct{})
	close(closed)

	globalManager = &UpdateManager{
		broadcaster: realtime.NewTaskBroadcaster(),
		done:        closed,
	}
}

// GetGlobalManager 获取全局 UpdateManager。
func GetGlobalManager() *UpdateManager { return globalManager }

// Start 启动一次更新。
//
// 返回 (done, started)：started 表示本次调用是否真的发起了更新；
// 已有更新在跑时 started=false，此时 done 是**那一次**更新的完成信号，
// 调用方照样可以等它结束。
func (m *UpdateManager) Start() (<-chan struct{}, bool) {
	// 全程持锁：否则在 broadcaster.Start() 成功之后、新 done 装好之前，
	// 并发进来的调用者会看到「已在运行」却拿到上一轮那个**已关闭**的 done，
	// 于是误以为更新瞬间就结束了
	m.mu.Lock()
	defer m.mu.Unlock()

	// Start() 内部会原子地选出唯一赢家，只有赢家那条路径才创建 ctx 与 done
	if m.broadcaster.IsRunning() || !m.broadcaster.Start() {
		return m.done, false
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel.Store(&cancel)

	m.done = make(chan struct{})
	done := m.done

	go m.run(ctx, done)

	return done, true
}

// IsRunning 是否有更新正在进行。
func (m *UpdateManager) IsRunning() bool { return m.broadcaster.IsRunning() }

// Cancel 取消正在进行的更新，没有在跑时返回 false。
func (m *UpdateManager) Cancel() bool {
	cancelPtr := m.cancel.Load()
	if cancelPtr == nil || !m.broadcaster.IsRunning() {
		return false
	}
	(*cancelPtr)()
	return true
}

// Subscribe 订阅更新进度消息，返回 channel 与退订函数。
// 可以在 Start 之前调用（HTTP 层正是先订阅再 Start，以避免 TOCTOU 丢消息）。
func (m *UpdateManager) Subscribe() (chan string, func()) { return m.broadcaster.Subscribe() }

// GetHistory 返回已产生的消息，供中途接入的订阅者补齐。
func (m *UpdateManager) GetHistory() []string { return m.broadcaster.GetHistory() }

// run 执行更新的三个步骤。逻辑与原 serverapi.Handler.runUpdateTask 一致。
func (m *UpdateManager) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	defer m.broadcaster.Stop()

	realtime.BroadcastUpdateStarted()

	cancelled := false
	defer func() {
		if cancelled {
			realtime.BroadcastUpdateCancelled()
		} else {
			realtime.BroadcastUpdateCompleted()
		}
	}()

	// Clean up update cancel func on exit.
	// This Store(nil) runs before the deferred Stop() (LIFO), so it happens-before
	// running flips to false and thus before any subsequent Start() can store a new one.
	defer func() {
		m.cancel.Store(nil)
	}()

	defer func() {
		if r := recover(); r != nil {
			logger.GetLogger().Errorf("Server update panic: %v", r)
			m.broadcaster.SendMessage(fmt.Sprintf("Error: Server update panic: %v", r))
		}
	}()

	// Create progress writer
	writer := &realtime.UpdateProgressWriter{Broadcaster: m.broadcaster}

	// Check context before each step
	checkCancelled := func() bool {
		select {
		case <-ctx.Done():
			m.broadcaster.SendMessage("[CANCELLED] 更新已取消")
			cancelled = true
			return true
		default:
			return false
		}
	}

	// Step 0: 实例存活检查
	// installer 内部也会拦（CLI 走的就是那条路），这里前置一步只为给出干净的中文提示，
	// 并省掉白下载一遍 SteamCMD
	if alive := procpkg.ListAliveInstances(); len(alive) > 0 {
		m.broadcaster.SendMessage(
			fmt.Sprintf("Error: 检测到实例正在运行：%s，请先停止后再更新", strings.Join(alive, "、")),
		)
		return
	}

	// Step 1: SteamCMD download and extract
	if checkCancelled() {
		return
	}
	m.broadcaster.SendMessage("Downloading and extracting SteamCMD...")
	if err := installer.DownloadAndExtractSteamCmd(ctx, writer); err != nil {
		if ctx.Err() != nil {
			cancelled = true
			return // cancelled
		}
		m.broadcaster.SendMessage(fmt.Sprintf("Error: Failed to download SteamCMD: %v", err))
		return
	}

	// Step 2: ARK server update
	if checkCancelled() {
		return
	}
	m.broadcaster.SendMessage("Downloading and updating ARK server files...")
	if err := installer.DownloadAndUpdateArkServer(ctx, writer); err != nil {
		if ctx.Err() != nil {
			cancelled = true
			return // cancelled
		}
		m.broadcaster.SendMessage(fmt.Sprintf("Error: Failed to update ARK server: %v", err))
		return
	}

	// Step 3: Server verification
	if checkCancelled() {
		return
	}
	m.broadcaster.SendMessage("Verifying server installation...")
	if err := installer.VerifyServerInstallation(ctx, false); err != nil {
		if ctx.Err() != nil {
			cancelled = true
			return // cancelled
		}
		m.broadcaster.SendMessage(fmt.Sprintf("Error: Server verification failed: %v", err))
		return
	}

	// Update completed
	m.broadcaster.SendMessage("[COMPLETED] Server update completed successfully!")
}
