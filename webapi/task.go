package webapi

import (
	"asa-server/asaserver"
	"asa-server/httpserver"
	"asa-server/logger"
	"context"
	"errors"
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
	err := asaserver.StartServer(name, asaserver.WithWaitServerCompleted())
	if err != nil {
		if errors.Is(err, asaserver.ErrOperationNotAllowed) {
			logger.GetLogger().Infof("server '%s' start skipped: operation not allowed in current state", name)
			httpserver.BroadcastServerEvent("server_start_failed", name, fmt.Sprintf("Failed to start server: %v", err), "failed")
		} else {
			logger.GetLogger().Errorf("failed to start server '%s': %v", name, err)
		}
		return
	}
}

// runStopServerTask stops a server instance asynchronously
func (s *APIServer) runStopServerTask(name string) {
	if err := asaserver.StopServer(name); err != nil {
		if errors.Is(err, asaserver.ErrOperationNotAllowed) {
			logger.GetLogger().Infof("server '%s' stop skipped: operation not allowed in current state", name)
			httpserver.BroadcastServerEvent("server_stop_failed", name, fmt.Sprintf("Failed to stop server: %v", fmt.Errorf("operation not allowed in current state")), "failed")
		} else {
			logger.GetLogger().Errorf("failed to stop server '%s': %v", name, err)
		}
		return
	}
}

// runRestartServerTask restarts a server instance asynchronously
func (s *APIServer) runRestartServerTask(name string) {
	err := asaserver.RestartServer(name)
	if err != nil {
		if errors.Is(err, asaserver.ErrOperationNotAllowed) {
			logger.GetLogger().Infof("server '%s' restart skipped: operation not allowed in current state", name)
			httpserver.BroadcastServerEvent("server_restart_failed", name, fmt.Sprintf("Failed to restart server: %v", fmt.Errorf("operation not allowed in current state")), "failed")
		} else {
			logger.GetLogger().Errorf("failed to restart server '%s': %v", name, err)
		}
		return
	}
}
