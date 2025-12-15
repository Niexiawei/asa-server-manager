package webapi

import (
	"asa-server/asaserver"
	"asa-server/logger"
	"fmt"
	"os"
	"strings"
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
func (s *APIServer) runStartServerTask(name string, broadcaster *TaskBroadcaster) {
	defer s.instanceStartBroadcasters.Cleanup(name)

	if err := asaserver.StartServer(name); err != nil {
		logger.GetLogger().Errorf("failed to start server '%s': %v", name, err)
		broadcaster.SendMessage(fmt.Sprintf("Error: Failed to start server: %v", err))
		// Broadcast error event
		s.BroadcastServerStartFailed(name, err)
		return
	}

	// Monitor startup progress using log file
	logPath, exists := asaserver.GetInstanceLogFile(name)
	if !exists {
		broadcaster.SendMessage("Error: Failed to get log file path")
		s.BroadcastServerStartFailed(name, fmt.Errorf("failed to get log file path"))
		return
	}

	// Check if log file exists with timeout
	var logFileExists bool
	for i := 0; i < 30; i++ {
		if _, err := os.Stat(logPath); err == nil {
			logFileExists = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !logFileExists {
		broadcaster.SendMessage("Error: Log file not found")
		s.BroadcastServerStartFailed(name, fmt.Errorf("log file not found"))
		return
	}

	// Tail log file and monitor for startup completion message
	done := make(chan struct{})
	startupSuccess := make(chan bool, 1)

	stopMonitoring := asaserver.TailLogFileWithLines(logPath, 100, func(line string) {
		broadcaster.SendMessage(fmt.Sprintf("[startup] %s", line))

		fmt.Println(line)

		// Check for successful startup message
		if strings.Contains(line, "Server has completed startup and is now advertising for join") {
			select {
			case startupSuccess <- true:
			case <-done:
			}
		}
	})

	defer stopMonitoring()

	// Wait for startup to complete or timeout (120 seconds)
	select {
	case <-startupSuccess:
		broadcaster.SendMessage("[COMPLETED] Server startup completed successfully")
	case <-time.After(10 * time.Minute):
		broadcaster.SendMessage("Error: Server startup timeout")
	case <-done:
		return
	}

	close(done)
	// Broadcast server started event
	s.BroadcastServerStarted(name)
}
