// Package updatemanage owns the server-files update orchestration.
//
// 这段编排原先长在 webapi/serverapi 的 Handler 上，只有 HTTP 层能触发。
// 定时调度属于领域层，不能反向 import webapi，因此下沉成全局单例，
// 写法对照 batchmanage：Initialize() / GetGlobalManager()。
package updatemanage

import (
	"asa-server/internal/installer"
	"asa-server/internal/logger"
	procpkg "asa-server/internal/process"
	"asa-server/internal/realtime"
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
	//
	// result 是**最近一次**更新的结果，与 done 同受 mu 保护：等到 done 关闭后读它，
	// 拿到的必然是那一次的结论。没有它的话，失败只存在于 SSE 消息流里——
	// 调度器等到 done 关闭就以为更新成功了。
	mu     sync.Mutex
	done   chan struct{}
	result error
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
	m.result = nil
	done := m.done

	go m.run(ctx, done)

	return done, true
}

// Result 返回最近一次更新的结果，nil 表示成功（或从未跑过）。
//
// 只有在对应的 done 关闭之后读才有意义；更新进行中读到的是上一次的结果。
func (m *UpdateManager) Result() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.result
}

// setResult 记录本次更新的结果。
func (m *UpdateManager) setResult(err error) {
	m.mu.Lock()
	m.result = err
	m.mu.Unlock()
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
			err := fmt.Errorf("server update panic: %v", r)
			logger.GetLogger().Errorf("Server update panic: %v", r)
			m.broadcaster.SendMessage("Error: " + err.Error())
			m.setResult(err)
		}
	}()

	// fail 统一处理失败：写日志（原先只发 SSE，失败在日志里完全没有痕迹）、
	// 保持 "Error: " 前缀的 SSE 报文格式不变、记录结果供 Result() 取回。
	fail := func(format string, args ...any) {
		err := fmt.Errorf(format, args...)
		logger.GetLogger().Errorf("Server update failed: %v", err)
		m.broadcaster.SendMessage("Error: " + err.Error())
		m.setResult(err)
	}

	markCancelled := func() {
		cancelled = true
		m.setResult(context.Canceled)
	}

	// Create progress writer
	writer := &realtime.UpdateProgressWriter{Broadcaster: m.broadcaster}

	// Check context before each step
	checkCancelled := func() bool {
		select {
		case <-ctx.Done():
			m.broadcaster.SendMessage("[CANCELLED] 更新已取消")
			markCancelled()
			return true
		default:
			return false
		}
	}

	// Step 0: 实例存活检查
	// installer 内部也会拦（CLI 走的就是那条路），这里前置一步只为给出干净的中文提示，
	// 并省掉白下载一遍 SteamCMD
	if alive := procpkg.ListAliveInstances(); len(alive) > 0 {
		fail("检测到实例正在运行：%s，请先停止后再更新", strings.Join(alive, "、"))
		return
	}

	// Step 1: SteamCMD download and extract
	if checkCancelled() {
		return
	}
	m.broadcaster.SendMessage("Downloading and extracting SteamCMD...")
	if err := installer.DownloadAndExtractSteamCmd(ctx, writer); err != nil {
		if ctx.Err() != nil {
			markCancelled()
			return // cancelled
		}
		fail("Failed to download SteamCMD: %w", err)
		return
	}

	// Step 2: ARK server update
	if checkCancelled() {
		return
	}
	m.broadcaster.SendMessage("Downloading and updating ARK server files...")
	if err := installer.DownloadAndUpdateArkServer(ctx, writer); err != nil {
		if ctx.Err() != nil {
			markCancelled()
			return // cancelled
		}
		fail("Failed to update ARK server: %w", err)
		return
	}

	// Step 3: Server verification
	if checkCancelled() {
		return
	}
	m.broadcaster.SendMessage("Verifying server installation...")
	if err := installer.VerifyServerInstallation(ctx, false); err != nil {
		if ctx.Err() != nil {
			markCancelled()
			return // cancelled
		}
		fail("Server verification failed: %w", err)
		return
	}

	// Update completed
	m.broadcaster.SendMessage("[COMPLETED] Server update completed successfully!")
}
