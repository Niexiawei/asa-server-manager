package webapi

import (
	"asa-server/asaserver"
	"asa-server/logger"
	"context"
	"errors"
	"fmt"
	"time"
)

// runUpdateTask executes the server update task
func (s *APIServer) runUpdateTask() {
	defer s.updateBroadcaster.Stop()

	defer func() {
		if r := recover(); r != nil {
			logger.GetLogger().Errorf("Server update panic: %v", r)
			s.updateBroadcaster.SendMessage(fmt.Sprintf("Error: Server update panic: %v", r))
		}
	}()

	// Create progress writer
	writer := &UpdateProgressWriter{broadcaster: s.updateBroadcaster}

	// Send SteamCMD download and extract message
	s.updateBroadcaster.SendMessage("Downloading and extracting SteamCMD...")
	if err := asaserver.DownloadAndExtractSteamCmd(writer); err != nil {
		s.updateBroadcaster.SendMessage(fmt.Sprintf("Error: Failed to download SteamCMD: %v", err))
		return
	}

	// Send ARK server update message
	s.updateBroadcaster.SendMessage("Downloading and updating ARK server files...")
	if err := asaserver.DownloadAndUpdateArkServer(writer); err != nil {
		s.updateBroadcaster.SendMessage(fmt.Sprintf("Error: Failed to update ARK server: %v", err))
		return
	}

	// Server verification
	s.updateBroadcaster.SendMessage("Verifying server installation...")
	if err := asaserver.VerifyServerInstallation(false); err != nil {
		s.updateBroadcaster.SendMessage(fmt.Sprintf("Error: Server verification failed: %v", err))
		return
	}

	// Update completed
	s.updateBroadcaster.SendMessage("[COMPLETED] Server update completed successfully!")
}

// runStartAllServersTask executes the start all servers task
func (s *APIServer) runStartAllServersTask() {
	defer s.startBroadcaster.Stop()

	defer func() {
		if r := recover(); r != nil {
			logger.GetLogger().Errorf("Start all servers panic: %v", r)
			s.startBroadcaster.SendMessage(fmt.Sprintf("Error: Start all servers panic: %v", r))
		}
	}()

	if !serverActionsLock.TryLock() {
		s.startBroadcaster.SendMessage(fmt.Sprintf("Error: There are other services being started or stopped"))
		return
	}
	defer serverActionsLock.Unlock()

	// Get all available instances
	instances, err := asaserver.GetAvailableInstances()
	if err != nil {
		s.startBroadcaster.SendMessage(fmt.Sprintf("Error: Failed to get instances: %v", err))
		return
	}

	if len(instances) == 0 {
		s.startBroadcaster.SendMessage("No instances to start")
		return
	}

	s.startBroadcaster.SendMessage(fmt.Sprintf("Starting %d server instances...", len(instances)))

	// Start each instance individually
	var failedInstances []string
	for _, instanceName := range instances {
		// Smart skip: check state manager for instances already starting/started
		state, _ := asaserver.GetLatestInstanceState(instanceName)
		switch state.Status {
		case asaserver.StatusStarting, asaserver.StatusStarted,
			asaserver.StatusStartStartInitialization,
			asaserver.StatusStartStartInitializationSuccessful:
			s.startBroadcaster.SendMessage(fmt.Sprintf("Instance '%s' skipped (status: %s)", instanceName, state.Status))
			continue
		}

		// Check if already running
		running, err := asaserver.IsServerRunning(instanceName)
		if err == nil && running {
			s.startBroadcaster.SendMessage(fmt.Sprintf("Instance '%s' is already running", instanceName))
			continue
		}

		// Broadcast starting event
		s.BroadcastServerStarting(instanceName)
		s.startBroadcaster.SendMessage(fmt.Sprintf("Starting instance '%s'...", instanceName))

		// Start the instance
		if err := asaserver.StartServer(instanceName); err != nil {
			failedInstances = append(failedInstances, instanceName)
			s.startBroadcaster.SendMessage(fmt.Sprintf("Error: starting '%s': %v", instanceName, err))
			// Broadcast error event
			s.BroadcastServerStartFailed(instanceName, err)
			continue
		}

		s.startBroadcaster.SendMessage(fmt.Sprintf("Instance '%s' started successfully", instanceName))
		// Broadcast started event
		s.BroadcastServerStarted(instanceName)
	}

	// Check if all starts were successful
	if len(failedInstances) > 0 {
		s.startBroadcaster.SendMessage(fmt.Sprintf("Error: %d of %d instances failed to start", len(failedInstances), len(instances)))
	} else {
		s.startBroadcaster.SendMessage(fmt.Sprintf("[COMPLETED] All %d servers started successfully", len(instances)))
	}
}

// runStopAllServersTask executes the stop all servers task
func (s *APIServer) runStopAllServersTask() {
	defer s.stopBroadcaster.Stop()

	defer func() {
		if r := recover(); r != nil {
			logger.GetLogger().Errorf("Stop all servers panic: %v", r)
			s.stopBroadcaster.SendMessage(fmt.Sprintf("Error: Stop all servers panic: %v", r))
		}
	}()

	if !serverActionsLock.TryLock() {
		s.stopBroadcaster.SendMessage(fmt.Sprintf("Error: There are other services being started or stopped"))
		return
	}

	defer serverActionsLock.Unlock()

	// Get all available instances
	instances, err := asaserver.GetAvailableInstances()
	if err != nil {
		s.stopBroadcaster.SendMessage(fmt.Sprintf("Error: Failed to get instances: %v", err))
		return
	}

	if len(instances) == 0 {
		s.stopBroadcaster.SendMessage("No instances to stop")
		return
	}

	s.stopBroadcaster.SendMessage(fmt.Sprintf("Stopping %d server instances...", len(instances)))

	// Stop each instance individually
	var failedInstances []string
	for _, instanceName := range instances {
		// Smart skip: check state manager for instances already stopping/stopped
		state, _ := asaserver.GetLatestInstanceState(instanceName)
		switch state.Status {
		case asaserver.StatusStopping, asaserver.StatusStopped:
			s.stopBroadcaster.SendMessage(fmt.Sprintf("Instance '%s' skipped (status: %s)", instanceName, state.Status))
			continue
		}

		// Check if already running
		running, err := asaserver.IsServerRunning(instanceName)
		if err != nil || !running {
			s.stopBroadcaster.SendMessage(fmt.Sprintf("Instance '%s' is not running", instanceName))
			continue
		}

		// Broadcast stopping event
		s.BroadcastServerStopping(instanceName)
		s.stopBroadcaster.SendMessage(fmt.Sprintf("Stopping instance '%s'...", instanceName))

		// Stop the instance
		if err := asaserver.StopServer(instanceName); err != nil {
			failedInstances = append(failedInstances, instanceName)
			s.stopBroadcaster.SendMessage(fmt.Sprintf("Error: stopping '%s': %v", instanceName, err))
			// Broadcast error event
			s.BroadcastServerStopFailed(instanceName, err)
			continue
		}

		s.stopBroadcaster.SendMessage(fmt.Sprintf("Instance '%s' stopped successfully", instanceName))
		// Broadcast stopped event
		s.BroadcastServerStopped(instanceName)
	}

	// Check if all stops were successful
	if len(failedInstances) > 0 {
		s.stopBroadcaster.SendMessage(fmt.Sprintf("Error: %d of %d instances failed to stop", len(failedInstances), len(instances)))
	} else {
		s.stopBroadcaster.SendMessage(fmt.Sprintf("[COMPLETED] All %d servers stopped successfully", len(instances)))
	}
}

// runRestartAllServersTask executes the restart all servers task
func (s *APIServer) runRestartAllServersTask() {
	defer s.restartBroadcaster.Stop()

	defer func() {
		if r := recover(); r != nil {
			logger.GetLogger().Errorf("Restart all servers panic: %v", r)
			s.restartBroadcaster.SendMessage(fmt.Sprintf("Error: Restart all servers panic: %v", r))
		}
	}()

	if !serverActionsLock.TryLock() {
		s.restartBroadcaster.SendMessage(fmt.Sprintf("Error: There are other services being started or stopped"))
		return
	}
	defer serverActionsLock.Unlock()

	// Get all available instances
	instances, err := asaserver.GetAvailableInstances()
	if err != nil {
		s.restartBroadcaster.SendMessage(fmt.Sprintf("Error: Failed to get instances: %v", err))
		return
	}

	if len(instances) == 0 {
		s.restartBroadcaster.SendMessage("No instances to restart")
		return
	}

	s.restartBroadcaster.SendMessage(fmt.Sprintf("Restarting %d server instances...", len(instances)))

	// Restart each instance individually
	var failedInstances []string
	for _, instanceName := range instances {
		// Smart skip: check state manager for instances already restarting/stopping
		state, _ := asaserver.GetLatestInstanceState(instanceName)
		switch state.Status {
		case asaserver.StatusRestart, asaserver.StatusStopping:
			s.restartBroadcaster.SendMessage(fmt.Sprintf("Instance '%s' skipped (status: %s)", instanceName, state.Status))
			continue
		}

		// Broadcast stopping event
		s.BroadcastServerStopping(instanceName)
		s.restartBroadcaster.SendMessage(fmt.Sprintf("Restarting instance '%s'...", instanceName))

		// Restart the instance
		if err := asaserver.RestartServer(instanceName); err != nil {
			failedInstances = append(failedInstances, instanceName)
			s.restartBroadcaster.SendMessage(fmt.Sprintf("Error: Failed to restart instance '%s': %v", instanceName, err))
			// Broadcast error event
			s.BroadcastServerRestartFailed(instanceName, err)
			continue
		}

		s.restartBroadcaster.SendMessage(fmt.Sprintf("Instance '%s' restarted successfully", instanceName))
		// Broadcast started event
		s.BroadcastServerStarted(instanceName)
	}

	// Check if all restarts were successful
	if len(failedInstances) > 0 {
		s.restartBroadcaster.SendMessage(fmt.Sprintf("Error: %d of %d instances failed to restart", len(failedInstances), len(instances)))
	} else {
		s.restartBroadcaster.SendMessage(fmt.Sprintf("[COMPLETED] All %d servers restarted successfully", len(instances)))
	}
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
				s.BroadcastServerStarting(name)
			}))

		if err != nil {
			if errors.Is(err, asaserver.ErrServerActionsLocked) {
				logger.GetLogger().Infof("server '%s' start skipped: another operation in progress", name)
				s.BroadcastServerStartFailed(name, fmt.Errorf("there are other services being started or stopped"))
			} else {
				logger.GetLogger().Errorf("failed to start server '%s': %v", name, err)
				s.BroadcastServerStartFailed(name, err)
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
		s.BroadcastServerStartFailed(name, fmt.Errorf("start Server %s exited", name))
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
		s.BroadcastServerStartFailed(name, fmt.Errorf("start Server %s timeout", name))
		return
	}
	s.BroadcastServerStarted(name)
}

// runStopServerTask stops a server instance asynchronously
func (s *APIServer) runStopServerTask(name string) {
	if err := asaserver.StopServer(name); err != nil {
		if errors.Is(err, asaserver.ErrServerActionsLocked) {
			logger.GetLogger().Infof("server '%s' stop skipped: another operation in progress", name)
			s.BroadcastServerStopFailed(name, fmt.Errorf("there are other services being started or stopped"))
		} else {
			logger.GetLogger().Errorf("failed to stop server '%s': %v", name, err)
			s.BroadcastServerStopFailed(name, err)
		}
		return
	}
	s.BroadcastServerStopped(name)
}

// runRestartServerTask restarts a server instance asynchronously
func (s *APIServer) runRestartServerTask(name string) {
	if err := asaserver.RestartServer(name); err != nil {
		if errors.Is(err, asaserver.ErrServerActionsLocked) {
			logger.GetLogger().Infof("server '%s' restart skipped: another operation in progress", name)
			s.BroadcastServerRestartFailed(name, fmt.Errorf("there are other services being started or stopped"))
		} else {
			logger.GetLogger().Errorf("failed to restart server '%s': %v", name, err)
			s.BroadcastServerRestartFailed(name, err)
		}
		return
	}
	_ = asaserver.WriteInstanceState(name, asaserver.StatusRestarted, "")
	s.BroadcastServerRestarted(name)
}
