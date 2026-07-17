package webapi

import (
	"asa-server/httpserver"
	"asa-server/installer"
	instancepkg "asa-server/instance"
	"asa-server/logger"
	"context"
	"fmt"
)

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

	// Clean up update cancel func on exit.
	// This Store(nil) runs before the deferred Stop() (LIFO), so it happens-before
	// running flips to false and thus before any subsequent Start() can store a new one.
	defer func() {
		s.updateCancel.Store(nil)
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
	if err := installer.DownloadAndExtractSteamCmd(ctx, writer); err != nil {
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
	if err := installer.DownloadAndUpdateArkServer(ctx, writer); err != nil {
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
	if err := installer.VerifyServerInstallation(ctx, false); err != nil {
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
	if err := instancepkg.StartServer(name,
		instancepkg.WithWaitServerCompleted(),
		instancepkg.WithStatePreset(),
	); err != nil {
		logger.GetLogger().Errorf("failed to start server '%s': %v", name, err)
	}
}

// runStopServerTask stops a server instance asynchronously
func (s *APIServer) runStopServerTask(name string) {
	if err := instancepkg.StopServer(name, instancepkg.WithStatePreset()); err != nil {
		logger.GetLogger().Errorf("failed to stop server '%s': %v", name, err)
	}
}

// runRestartServerTask restarts a server instance asynchronously
func (s *APIServer) runRestartServerTask(name string) {
	if err := instancepkg.RestartServer(name,
		instancepkg.WithStatePreset(),
		instancepkg.WithRestartStartupCompletion(func(string) {}), // 写 StatusRestarted 状态供 dispatcher 推送
	); err != nil {
		logger.GetLogger().Errorf("failed to restart server '%s': %v", name, err)
	}
}
