package webapi

import (
	"asa-server/asaserver"
	"asa-server/httpserver"
	"asa-server/logger"
	"context"
	"errors"
	"fmt"
	"time"
)

// isTransitionalState 检查是否为中间态（批量操作应跳过）
func isTransitionalState(status asaserver.InstanceStatus) bool {
	switch status {
	case asaserver.StatusStarting, asaserver.StatusRestarting, asaserver.StatusStopping,
		asaserver.StatusStartStartInitialization:
		return true
	}
	return false
}

// waitForGlobalReady 等待全局初始化完成（基于 sync.Cond 广播，不轮询）
// 任何实例在 start_initialization 时，所有操作都被阻塞
func waitForGlobalReady(broadcaster *httpserver.TaskBroadcaster, timeout time.Duration) error {
	if asaserver.IsAnyInstanceInitializing() {
		broadcaster.SendMessage("Waiting for instance initialization to complete...")
		if err := asaserver.WaitForNoInitializing(timeout); err != nil {
			return fmt.Errorf("timeout waiting for initialization: %w", err)
		}
	}
	return nil
}

// runUpdateTask executes the server update task with context cancellation support
func (s *APIServer) runUpdateTask(ctx context.Context) {
	defer s.updateBroadcaster.Stop()

	httpserver.BroadcastUpdateStarted()

	cancelled := false
	defer func() {
		if cancelled {
			httpserver.BroadcastUpdateCancelled()
		} else {
			httpserver.BroadcastUpdateCompleted()
		}
	}()

	// Clean up update context on exit
	defer func() {
		s.updateMu.Lock()
		s.updateCtx = nil
		s.updateCancel = nil
		s.updateMu.Unlock()
	}()

	defer func() {
		if r := recover(); r != nil {
			logger.GetLogger().Errorf("Server update panic: %v", r)
			s.updateBroadcaster.SendMessage(fmt.Sprintf("Error: Server update panic: %v", r))
		}
	}()

	// Create progress writer
	writer := &httpserver.UpdateProgressWriter{Broadcaster: s.updateBroadcaster}

	// Check context before each step
	checkCancelled := func() bool {
		select {
		case <-ctx.Done():
			s.updateBroadcaster.SendMessage("[CANCELLED] 更新已取消")
			cancelled = true
			return true
		default:
			return false
		}
	}

	// Step 1: SteamCMD download and extract
	if checkCancelled() {
		return
	}
	s.updateBroadcaster.SendMessage("Downloading and extracting SteamCMD...")
	if err := asaserver.DownloadAndExtractSteamCmd(ctx, writer); err != nil {
		if ctx.Err() != nil {
			cancelled = true
			return // cancelled
		}
		s.updateBroadcaster.SendMessage(fmt.Sprintf("Error: Failed to download SteamCMD: %v", err))
		return
	}

	// Step 2: ARK server update
	if checkCancelled() {
		return
	}
	s.updateBroadcaster.SendMessage("Downloading and updating ARK server files...")
	if err := asaserver.DownloadAndUpdateArkServer(ctx, writer); err != nil {
		if ctx.Err() != nil {
			cancelled = true
			return // cancelled
		}
		s.updateBroadcaster.SendMessage(fmt.Sprintf("Error: Failed to update ARK server: %v", err))
		return
	}

	// Step 3: Server verification
	if checkCancelled() {
		return
	}
	s.updateBroadcaster.SendMessage("Verifying server installation...")
	if err := asaserver.VerifyServerInstallation(ctx, false); err != nil {
		if ctx.Err() != nil {
			cancelled = true
			return // cancelled
		}
		s.updateBroadcaster.SendMessage(fmt.Sprintf("Error: Server verification failed: %v", err))
		return
	}

	// Update completed
	s.updateBroadcaster.SendMessage("[COMPLETED] Server update completed successfully!")
}

// runStartServerTask monitors a single server startup process
func (s *APIServer) runStartServerTask(name string) {
	startErr := make(chan error, 2)
	startupSuccess := make(chan bool, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		err := asaserver.StartServer(name, asaserver.WithCtx(ctx), asaserver.WithWaitServerCompleted(),
			asaserver.WithGameInitializationSuccessfulCallback(func() {
				httpserver.BroadcastServerStartingEvent(name)
			}))

		if err != nil {
			if errors.Is(err, asaserver.ErrOperationNotAllowed) {
				logger.GetLogger().Infof("server '%s' start skipped: operation not allowed in current state", name)
				httpserver.BroadcastServerEvent("server_start_failed", name, fmt.Sprintf("Failed to start server: %v", err), "failed")
			} else {
				logger.GetLogger().Errorf("failed to start server '%s': %v", name, err)
				httpserver.BroadcastServerEvent("server_start_failed", name, fmt.Sprintf("Failed to start server: %v", err), "failed")
			}
			startErr <- err
			return
		}
		startupSuccess <- true
	}()

	select {
	case err := <-startErr:
		logger.GetLogger().Errorf("start Server %s fail err: %v", name, err)
		return
	case <-ctx.Done():
		logger.GetLogger().Errorf("start Server %s exited", name)
		httpserver.BroadcastServerEvent("server_start_failed", name, fmt.Sprintf("Failed to start server: %v", fmt.Errorf("start Server %s exited", name)), "failed")
		return
	case <-startupSuccess:
		// completed
	case <-time.After(5 * time.Minute):
		// 超时：先取消 ctx 通知 StartServer 内部停止，再强制杀进程
		cancel()
		if err := asaserver.KillServer(name); err != nil {
			logger.GetLogger().Errorf("kill server fail:%v", err)
		}
		logger.GetLogger().Errorf("Server startup timeout name:%s", name)
		httpserver.BroadcastServerEvent("server_start_failed", name, fmt.Sprintf("Failed to start server: %v", fmt.Errorf("start Server %s timeout", name)), "failed")
		return
	}
	httpserver.BroadcastServerStartedEvent(name)
}

// runStopServerTask stops a server instance asynchronously
func (s *APIServer) runStopServerTask(name string) {
	if err := asaserver.StopServer(name); err != nil {
		if errors.Is(err, asaserver.ErrOperationNotAllowed) {
			logger.GetLogger().Infof("server '%s' stop skipped: operation not allowed in current state", name)
			httpserver.BroadcastServerEvent("server_stop_failed", name, fmt.Sprintf("Failed to stop server: %v", fmt.Errorf("operation not allowed in current state")), "failed")
		} else {
			logger.GetLogger().Errorf("failed to stop server '%s': %v", name, err)
			httpserver.BroadcastServerEvent("server_stop_failed", name, fmt.Sprintf("Failed to stop server: %v", err), "failed")
		}
		return
	}
	httpserver.BroadcastServerStoppedEvent(name)
}

// runRestartServerTask restarts a server instance asynchronously
func (s *APIServer) runRestartServerTask(name string) {
	if err := asaserver.RestartServer(name); err != nil {
		if errors.Is(err, asaserver.ErrOperationNotAllowed) {
			logger.GetLogger().Infof("server '%s' restart skipped: operation not allowed in current state", name)
			httpserver.BroadcastServerEvent("server_restart_failed", name, fmt.Sprintf("Failed to restart server: %v", fmt.Errorf("operation not allowed in current state")), "failed")
		} else {
			logger.GetLogger().Errorf("failed to restart server '%s': %v", name, err)
			httpserver.BroadcastServerEvent("server_restart_failed", name, fmt.Sprintf("Failed to restart server: %v", err), "failed")
		}
		return
	}
	_ = asaserver.WriteInstanceState(name, asaserver.StatusRestarted, "")
	httpserver.BroadcastServerRestartedEvent(name)
}
