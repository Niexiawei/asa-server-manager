package webapi

import (
	"asa-server/asaserver"
	"asa-server/backup"
	"asa-server/logger"
	"asa-server/serverinfo"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ========== Response types ==========

type InstanceInfo struct {
	Name    string      `json:"name"`
	Running bool        `json:"running"`
	Config  interface{} `json:"config,omitempty"`
	Error   string      `json:"error,omitempty"`
}

type ListResponse struct {
	Instances []InstanceInfo `json:"instances"`
	Count     int            `json:"count"`
}

type StatusResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

type CreateInstanceRequest struct {
	Name string `json:"name" binding:"required"`
}

type RenameInstanceRequest struct {
	NewName string `json:"new_name" binding:"required"`
}

type RCONCommandRequest struct {
	Command string `json:"command" binding:"required"`
}

type BackupRequest struct {
}

type RestoreRequest struct {
	BackupFile            string `json:"backup_file" binding:"required"`
	RestoreWorldfile      *bool  `json:"restore_worldfile,omitempty"`
	RestoreInstanceConfig *bool  `json:"restore_instance_config,omitempty"`
	RestoreGameConfig     *bool  `json:"restore_game_config,omitempty"`
}

type ConfigFileRequest struct {
	Content string `json:"content" binding:"required"`
}

type SyncConfigRequest struct {
	Instances []string `json:"instances" binding:"required,min=1"`
}

type SyncInstanceConfigRequest struct {
	SourceInstance            string   `json:"source_instance" binding:"required"`
	TargetInstances           []string `json:"target_instances" binding:"required,min=1"`
	SyncCustomStartParameters *bool    `json:"sync_custom_start_parameters,omitempty"`
	SyncEnableAsaPlugin       *bool    `json:"sync_enable_asa_plugin,omitempty"`
}

// ========== Handlers ==========

// health checks if the API server is running
func (s *APIServer) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"service": "asa-server-manager-api",
	})
}

// listInstances returns all available instances with their basic configuration
func (s *APIServer) listInstances(c *gin.Context) {
	instances, err := asaserver.GetAvailableInstances()
	if err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: "Failed to get instances",
			Error:   err.Error(),
		})
		return
	}

	var instanceInfos []InstanceInfo
	for _, instanceName := range instances {
		running, err := asaserver.IsServerRunning(instanceName)
		config, cfgErr := asaserver.LoadInstanceConfig(instanceName)

		info := InstanceInfo{
			Name:    instanceName,
			Running: running,
		}

		if cfgErr == nil {
			info.Config = config
		}

		if err != nil {
			info.Error = err.Error()
		}

		instanceInfos = append(instanceInfos, info)
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "Instances retrieved successfully",
		Data: ListResponse{
			Instances: instanceInfos,
			Count:     len(instanceInfos),
		},
	})
}

// createInstance creates a new server instance
func (s *APIServer) createInstance(c *gin.Context) {
	var req CreateInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Create instance directory
	instanceDir := filepath.Join(asaserver.InstancesDir, req.Name, "Config")
	if err := os.MkdirAll(instanceDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Copy base server configuration files to instance Config directory
	baseConfigDir := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer")
	if _, err := os.Stat(baseConfigDir); err == nil {
		if err := asaserver.CopyDir(baseConfigDir, instanceDir); err != nil {
			// Log warning but continue as this is not critical
			logger.GetLogger().Warnf("Failed to copy base server configuration: %v", err)
		}
	} else {
		// Create empty Game.ini if it doesn't exist
		if err := asaserver.SaveGameIniContent(req.Name, ""); err != nil {
			logger.GetLogger().Warnf("Failed to create Game.ini: %v", err)
		}
		// Create empty GameUserSettings.ini if it doesn't exist
		if err := asaserver.SaveGameUserSettingsContent(req.Name, ""); err != nil {
			logger.GetLogger().Warnf("Failed to create GameUserSettings.ini: %v", err)
		}
	}

	// Check and create missing config files if directory exists but files don't
	gameIniPath := filepath.Join(instanceDir, "Game.ini")
	if _, err := os.Stat(gameIniPath); os.IsNotExist(err) {
		if err := asaserver.SaveGameIniContent(req.Name, ""); err != nil {
			logger.GetLogger().Warnf("Failed to create Game.ini: %v", err)
		}
	}

	gameUserSettingsPath := filepath.Join(instanceDir, "GameUserSettings.ini")
	if _, err := os.Stat(gameUserSettingsPath); os.IsNotExist(err) {
		if err := asaserver.SaveGameUserSettingsContent(req.Name, ""); err != nil {
			logger.GetLogger().Warnf("Failed to create GameUserSettings.ini: %v", err)
		}
	}

	// Create default configuration
	config := asaserver.CreateDefaultInstanceConfig(req.Name)

	if err := asaserver.SaveInstanceConfig(req.Name, config); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance '%s' created successfully", req.Name),
	})
}

// getInstanceStatus returns the status of a specific instance
func (s *APIServer) getInstanceStatus(c *gin.Context) {
	name := c.Param("name")

	running, err := asaserver.IsServerRunning(name)
	config, cfgErr := asaserver.LoadInstanceConfig(name)

	info := InstanceInfo{
		Name:    name,
		Running: running,
	}

	if cfgErr == nil {
		info.Config = config
	}

	if err != nil && cfgErr != nil {
		c.JSON(http.StatusNotFound, StatusResponse{
			Success: false,
			Error:   "Instance not found",
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "Instance status retrieved",
		Data:    info,
	})
}

// deleteInstance deletes an instance
func (s *APIServer) deleteInstance(c *gin.Context) {
	name := c.Param("name")

	// Stop instance if running
	if running, _ := asaserver.IsServerRunning(name); running {
		if err := asaserver.StopServer(name); err != nil {
			c.JSON(http.StatusInternalServerError, StatusResponse{
				Success: false,
				Error:   fmt.Sprintf("Failed to stop server: %v", err),
			})
			return
		}
	}

	// Delete instance directory
	instanceDir := filepath.Join(asaserver.InstancesDir, name)
	if err := os.RemoveAll(instanceDir); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Delete save directories
	savePath := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved", name)
	os.RemoveAll(savePath)

	savedArksPath := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved/SavedArks", name)
	os.RemoveAll(savedArksPath)

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance '%s' deleted successfully", name),
	})
}

// renameInstance renames an instance
func (s *APIServer) renameInstance(c *gin.Context) {
	oldName := c.Param("name")

	var req RenameInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	if req.NewName == "" {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   "New name cannot be empty",
		})
		return
	}

	// Stop instance if running
	if running, _ := asaserver.IsServerRunning(oldName); running {
		if err := asaserver.StopServer(oldName); err != nil {
			c.JSON(http.StatusInternalServerError, StatusResponse{
				Success: false,
				Error:   fmt.Sprintf("Failed to stop server: %v", err),
			})
			return
		}
	}

	// Rename instance directory
	oldPath := filepath.Join(asaserver.InstancesDir, oldName)
	newPath := filepath.Join(asaserver.InstancesDir, req.NewName)

	if err := os.Rename(oldPath, newPath); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Rename save directories
	oldSavePath := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved", oldName)
	newSavePath := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved", req.NewName)
	os.Rename(oldSavePath, newSavePath)

	oldArksPath := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved/SavedArks", oldName)
	newArksPath := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved/SavedArks", req.NewName)
	os.Rename(oldArksPath, newArksPath)

	// Update SaveDir in configuration
	config, err := asaserver.LoadInstanceConfig(req.NewName)
	if err == nil {
		config.SaveDir = req.NewName
		asaserver.SaveInstanceConfig(req.NewName, config)
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance renamed from '%s' to '%s'", oldName, req.NewName),
	})
}

// startServer starts a server instance
func (s *APIServer) startServer(c *gin.Context) {
	instanceName := c.Param("name")

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")
	flusher := c.Writer.(http.Flusher)

	if !serverActionsLock.TryLock() {
		response := gin.H{
			"status":    "error",
			"timestamp": time.Now().Format(time.RFC3339Nano),
			"message":   "there are other services being started or stopped",
		}
		jsonData, _ := json.Marshal(response)
		fmt.Fprintf(c.Writer, "data: %s\n\n", jsonData)
		flusher.Flush()
		return
	}
	defer serverActionsLock.Unlock()

	// Check if server is already running first
	running, err := asaserver.IsServerRunning(instanceName)
	if err != nil {
		response := gin.H{
			"status":    "error",
			"timestamp": time.Now().Format(time.RFC3339Nano),
			"message":   fmt.Sprintf("Failed to check server status: %v", err),
		}
		jsonData, _ := json.Marshal(response)
		fmt.Fprintf(c.Writer, "data: %s\n\n", jsonData)
		flusher.Flush()
		return
	}

	if running {
		response := gin.H{
			"status":    "error",
			"timestamp": time.Now().Format(time.RFC3339Nano),
			"message":   fmt.Sprintf("Server '%s' is already running", instanceName),
		}
		jsonData, _ := json.Marshal(response)
		fmt.Fprintf(c.Writer, "data: %s\n\n", jsonData)
		flusher.Flush()
		return
	}

	// Check for duplicate ports
	if err := asaserver.CheckForDuplicatePorts(); err != nil {
		response := gin.H{
			"status":    "error",
			"timestamp": time.Now().Format(time.RFC3339Nano),
			"message":   fmt.Sprintf("Port conflicts detected: %v", err),
		}
		jsonData, _ := json.Marshal(response)
		fmt.Fprintf(c.Writer, "data: %s\n\n", jsonData)
		flusher.Flush()
		return
	}

	// Broadcast server starting event
	s.BroadcastServerStarting(instanceName)

	// Create and start broadcaster for this instance startup
	broadcaster := s.instanceStartBroadcasters.Get(instanceName)

	if !broadcaster.IsRunning() {
		if !broadcaster.Start() {
			logger.GetLogger().Infof("Server '%s' is currently starting, please wait", instanceName)
		} else {
			// Start server asynchronously
			go s.runStartServerTask(instanceName, broadcaster)
		}
	}

	// Subscribe to startup broadcaster and stream updates
	subscriber, unsubscribe := broadcaster.Subscribe()
	defer unsubscribe()

	c.Stream(func(w io.Writer) bool {
		select {
		case msg, ok := <-subscriber:
			if !ok {
				return false
			}
			// Format response with status, timestamp, and message

			// Check if startup completed
			if strings.Contains(msg, "[COMPLETED]") {
				// Push started status
				startedResponse := gin.H{
					"status":    "started",
					"timestamp": time.Now().Format(time.RFC3339Nano),
					"message":   "Server has started successfully",
				}
				startedData, _ := json.Marshal(startedResponse)
				fmt.Fprintf(w, "data: %s\n\n", startedData)
				return false
			}

			if strings.Contains(msg, "[ERROR]") {
				startedResponse := gin.H{
					"status":    "start_failed",
					"timestamp": time.Now().Format(time.RFC3339Nano),
					"message":   msg,
				}
				startedData, _ := json.Marshal(startedResponse)
				fmt.Fprintf(w, "data: %s\n\n", startedData)
				return false
			}

			response := gin.H{
				"status":    "starting",
				"timestamp": time.Now().Format(time.RFC3339Nano),
				"message":   msg,
			}
			jsonData, _ := json.Marshal(response)
			fmt.Fprintf(w, "data: %s\n\n", jsonData)

			return true
		case <-c.Request.Context().Done():
			return false
		}
	})
}

// stopServer stops a server instance
func (s *APIServer) stopServer(c *gin.Context) {
	name := c.Param("name")
	if !serverActionsLock.TryLock() {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("there are other services being started or stopped"),
		})
		return
	}
	defer serverActionsLock.Unlock()
	// Broadcast server stopping event
	s.BroadcastServerStopping(name)

	if err := asaserver.StopServer(name); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		// Broadcast error event
		s.BroadcastServerStopFailed(name, err)
		return
	}

	// Broadcast server stopped event
	s.BroadcastServerStopped(name)

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Server '%s' stopped successfully", name),
	})
}

// restartServer restarts a server instance
func (s *APIServer) restartServer(c *gin.Context) {
	name := c.Param("name")

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")

	// Create channel to stream messages
	msgChan := make(chan string)
	done := make(chan struct{})

	// Run restart in a goroutine
	go func() {
		defer close(msgChan)
		select {
		case msgChan <- fmt.Sprintf("Restarting server '%s'...", name):
		case <-done:
			return
		}

		if !serverActionsLock.TryLock() {
			select {
			case msgChan <- fmt.Sprintf("Error: there are other services being started or stopped"):
			case <-done:
			}
			return
		}

		defer serverActionsLock.Unlock()

		// Broadcast server stopping event
		s.BroadcastServerStopping(name)

		if err := asaserver.RestartServer(name); err != nil {
			select {
			case msgChan <- fmt.Sprintf("Error: Failed to restart server: %v", err):
			case <-done:
			}
			// Broadcast error event
			s.BroadcastServerRestartFailed(name, err)
			return
		}

		select {
		case msgChan <- fmt.Sprintf("Server '%s' restarted successfully", name):
		case <-done:
		}
		// Broadcast server restarted event
		s.BroadcastServerStarted(name)
	}()

	// Stream the progress
	c.Stream(func(w io.Writer) bool {
		select {
		case msg, ok := <-msgChan:
			if !ok {
				return false
			}
			// Send SSE formatted data
			fmt.Fprintf(w, "data: %s\n\n", msg)
			return true
		case <-c.Request.Context().Done():
			// Client disconnected - signal goroutine to stop
			close(done)
			return false
		}
	})
}

// startAllServers starts all server instances with SSE progress streaming
func (s *APIServer) startAllServers(c *gin.Context) {
	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")

	// Check if task is already running and start if needed
	if !s.startBroadcaster.IsRunning() {
		if !s.startBroadcaster.Start() {
			logger.GetLogger().Infof("Start all servers task already started by another request")
		} else {
			// Started successfully, run task in background
			go s.runStartAllServersTask()
		}
	}

	// Subscribe to start progress after ensuring task is properly started
	subscriber, unsubscribe := s.startBroadcaster.Subscribe()
	defer unsubscribe()

	// Stream progress
	c.Stream(func(w io.Writer) bool {
		select {
		case msg, ok := <-subscriber:
			if !ok {
				return false
			}
			// Send SSE formatted data
			fmt.Fprintf(w, "data: %s\n\n", msg)
			return true
		case <-c.Request.Context().Done():
			// Client disconnected but task continues
			return false
		}
	})
}

// stopAllServers stops all server instances with SSE progress streaming
func (s *APIServer) stopAllServers(c *gin.Context) {
	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")

	// Check if task is already running and start if needed
	if !s.stopBroadcaster.IsRunning() {
		if !s.stopBroadcaster.Start() {
			logger.GetLogger().Infof("Stop all servers task already started by another request")
		} else {
			// Started successfully, run task in background
			go s.runStopAllServersTask()
		}
	}

	// Subscribe to stop progress after ensuring task is properly started
	subscriber, unsubscribe := s.stopBroadcaster.Subscribe()
	defer unsubscribe()

	// Stream progress
	c.Stream(func(w io.Writer) bool {
		select {
		case msg, ok := <-subscriber:
			if !ok {
				return false
			}
			// Send SSE formatted data
			fmt.Fprintf(w, "data: %s\n\n", msg)
			return true
		case <-c.Request.Context().Done():
			// Client disconnected but task continues
			return false
		}
	})
}

// restartAllServers restarts all server instances with SSE progress streaming
func (s *APIServer) restartAllServers(c *gin.Context) {
	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")

	// Check if task is already running and start if needed
	if !s.restartBroadcaster.IsRunning() {
		if !s.restartBroadcaster.Start() {
			logger.GetLogger().Infof("Restart all servers task already started by another request")
		} else {
			// Started successfully, run task in background
			go s.runRestartAllServersTask()
		}
	}

	// Subscribe to restart progress after ensuring task is properly started
	subscriber, unsubscribe := s.restartBroadcaster.Subscribe()
	defer unsubscribe()

	// Stream progress
	c.Stream(func(w io.Writer) bool {
		select {
		case msg, ok := <-subscriber:
			if !ok {
				return false
			}
			// Send SSE formatted data
			fmt.Fprintf(w, "data: %s\n\n", msg)
			return true
		case <-c.Request.Context().Done():
			// Client disconnected but task continues
			return false
		}
	})
}

// sendRCONCommand sends an RCON command to a server
func (s *APIServer) sendRCONCommand(c *gin.Context) {
	name := c.Param("name")

	var req RCONCommandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	response, err := asaserver.SendRCONCommand(name, req.Command)
	if err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "RCON command executed successfully",
		Data: gin.H{
			"response": response,
		},
	})
}

// backupInstance creates a backup of a server instance
func (s *APIServer) backupInstance(c *gin.Context) {
	name := c.Param("name")

	if err := backup.BackupInstanceWorld(name); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance '%s' backed up successfully", name),
	})
}

// listBackups returns all available backups
func (s *APIServer) listBackups(c *gin.Context) {
	backups, err := backup.GetAvailableBackups()
	if err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "Backups retrieved successfully",
		Data: gin.H{
			"backups": backups,
			"count":   len(backups),
		},
	})
}

// restoreBackup restores a backup to an instance
// If instance doesn't exist, creates it automatically
func (s *APIServer) restoreBackup(c *gin.Context) {
	name := c.Param("name")

	var req RestoreRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// If name is empty from URL, try to use name from backup metadata
	// For now, name must be provided in URL
	if name == "" {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   "instance name is required (provide in URL path)",
		})
		return
	}

	// Build restore options based on request parameters
	var optFuncs []backup.RestoreOptionFunc

	// Default behavior: restore all if no parameters specified
	// If any parameter is specified, use explicit values
	hasExplicitOptions := req.RestoreWorldfile != nil || req.RestoreInstanceConfig != nil || req.RestoreGameConfig != nil

	if !hasExplicitOptions {
		// No parameters specified - restore everything
		optFuncs = append(optFuncs, backup.WithRestoreAll())
	} else {
		// Parameters specified - use explicit values with defaults to false
		restoreWorldfile := req.RestoreWorldfile != nil && *req.RestoreWorldfile
		restoreInstanceConfig := req.RestoreInstanceConfig != nil && *req.RestoreInstanceConfig
		restoreGameConfig := req.RestoreGameConfig != nil && *req.RestoreGameConfig

		if restoreWorldfile {
			optFuncs = append(optFuncs, backup.WithRestoreWorldfile())
		}
		if restoreInstanceConfig {
			optFuncs = append(optFuncs, backup.WithRestoreInstanceConfig())
		}
		if restoreGameConfig {
			optFuncs = append(optFuncs, backup.WithRestoreGameConfig())
		}
	}

	if err := backup.RestoreBackupToInstance(name, req.BackupFile, optFuncs...); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance '%s' restored successfully", name),
	})
}

// handleServerUpdate handles both starting update and streaming progress via SSE
// handleServerUpdate handles the server update SSE endpoint
func (s *APIServer) handleServerUpdate(c *gin.Context) {
	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")

	// Subscribe to update progress
	subscriber, unsubscribe := s.updateBroadcaster.Subscribe()
	defer unsubscribe()

	// Check if update task is already running
	if !s.updateBroadcaster.IsRunning() {
		if !s.updateBroadcaster.Start() {
			logger.GetLogger().Infof("Server update already started by another request")
		} else {
			// Started successfully, run update in background
			go s.runUpdateTask()
		}
	}

	// Stream update progress
	c.Stream(func(w io.Writer) bool {
		select {
		case msg, ok := <-subscriber:
			if !ok {
				return false
			}
			// Send SSE formatted data
			fmt.Fprintf(w, "data: %s\n\n", msg)
			return true
		case <-c.Request.Context().Done():
			// Client disconnected but task continues
			return false
		}
	})
}

// streamServerInfo streams server resource information (CPU, Memory) via SSE every 200ms
func (s *APIServer) streamServerInfo(c *gin.Context) {
	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")

	// Create ticker for 200ms interval
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Stream server info
	c.Stream(func(w io.Writer) bool {
		select {
		case <-ticker.C:
			// Get system memory info
			memInfo, err := serverinfo.GetMemoryInfo()
			if err != nil {
				fmt.Fprintf(w, "data: {\"error\":\"Failed to get memory info: %v\"}\n\n", err)
				return true
			}

			// Get system CPU info
			cpuInfo, err := serverinfo.GetCPUInfo()
			if err != nil {
				fmt.Fprintf(w, "data: {\"error\":\"Failed to get CPU info: %v\"}\n\n", err)
				return true
			}

			// Build response data
			data := map[string]interface{}{
				"timestamp": time.Now().Unix(),
				"memory": map[string]interface{}{
					"total":        memInfo.Total,
					"used":         memInfo.Used,
					"available":    memInfo.Available,
					"used_percent": memInfo.UsedPercent,
					"total_gb":     float64(memInfo.Total) / (1024 * 1024 * 1024),
					"used_gb":      float64(memInfo.Used) / (1024 * 1024 * 1024),
					"available_gb": float64(memInfo.Available) / (1024 * 1024 * 1024),
				},
				"cpu": map[string]interface{}{
					"core_count":   cpuInfo.CoreCount,
					"used_percent": cpuInfo.UsedPercent,
				},
			}

			// Convert to JSON
			jsonData, err := json.Marshal(data)
			if err != nil {
				fmt.Fprintf(w, "data: {\"error\":\"Failed to marshal JSON: %v\"}\n\n", err)
				return true
			}

			// Send SSE formatted data
			fmt.Fprintf(w, "data: %s\n\n", jsonData)
			return true

		case <-c.Request.Context().Done():
			// Client disconnected
			return false
		}
	})
}

// streamInstanceInfo streams instance resource information (CPU, Memory) via SSE every 200ms
func (s *APIServer) streamInstanceInfo(c *gin.Context) {
	instanceName := c.Param("name")

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")

	// Load instance configuration to get port
	config, err := asaserver.LoadInstanceConfig(instanceName)
	if err != nil {
		c.JSON(500, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Failed to load instance config: %v", err),
		})
		return
	}

	// Create ticker for 200ms interval
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Stream instance info
	c.Stream(func(w io.Writer) bool {
		select {
		case <-ticker.C:
			// Get PID by port
			pid, err := asaserver.GetPIDByPort(config.Port)
			if err != nil {
				fmt.Fprintf(w, "data:{\"error\":\"Failed to get PID: %v\",\"instance\":\"%s\",\"running\":false}", err, instanceName)
				return true
			}

			// Get process info
			processInfo, err := serverinfo.GetProcessInfo(int32(pid))
			if err != nil {
				fmt.Fprintf(w, "data: {\"error\":\"Failed to get process info: %v\",\"instance\":\"%s\",\"pid\":%d,\"running\":false}\n\n", err, instanceName, pid)
				return true
			}

			// Get system CPU info to calculate total CPU usage
			cpuInfo, err := serverinfo.GetCPUInfo()
			if err != nil {
				fmt.Fprintf(w, "data: {\"error\":\"Failed to get CPU info: %v\"}\n\n", err)
				return true
			}

			// Get system memory info
			memInfo, err := serverinfo.GetMemoryInfo()
			if err != nil {
				fmt.Fprintf(w, "data: {\"error\":\"Failed to get memory info: %v\"}\n\n", err)
				return true
			}

			// Calculate total CPU usage: instance CPU% / 100% * core count
			totalCPUUsage := (processInfo.CPUPercent / 100.0) * float64(cpuInfo.CoreCount)

			// Build response data
			data := map[string]interface{}{
				"timestamp": time.Now().Unix(),
				"instance":  instanceName,
				"running":   true,
				"pid":       pid,
				"cpu_cores": cpuInfo.CoreCount,
				"memory": map[string]interface{}{
					"total":    memInfo.Total,
					"total_gb": float64(memInfo.Total) / (1024 * 1024 * 1024),
				},
				"process": map[string]interface{}{
					"name":              processInfo.Name,
					"cpu_percent":       processInfo.CPUPercent,
					"cpu_total_percent": totalCPUUsage,
					"memory_used":       processInfo.MemoryUsed,
					"memory_percent":    processInfo.MemoryPercent,
					"memory_used_mb":    float64(processInfo.MemoryUsed) / (1024 * 1024),
					"memory_used_gb":    float64(processInfo.MemoryUsed) / (1024 * 1024 * 1024),
				},
			}

			// Convert to JSON
			jsonData, err := json.Marshal(data)
			if err != nil {
				fmt.Fprintf(w, "data: {\"error\":\"Failed to marshal JSON: %v\"}\n\n", err)
				return true
			}

			// Send SSE formatted data
			fmt.Fprintf(w, "data: %s\n\n", jsonData)
			return true

		case <-c.Request.Context().Done():
			// Client disconnected
			return false
		}
	})
}

// streamInstanceLogs streams server logs and startup status via Server-Sent Events (SSE)
func (s *APIServer) streamInstanceLogs(c *gin.Context) {
	instanceName := c.Param("name")

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")

	// Get the log file path for the instance
	logPath, exists := asaserver.GetInstanceLogFile(instanceName)
	if !exists {
		fmt.Fprintf(c.Writer, "data: %s\n\n", "failed to get log file path")
		c.Writer.Flush()
		return
	}

	// Check if log file exists
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		fmt.Fprintf(c.Writer, "data: %s\n\n", "log file not found")
		c.Writer.Flush()
		return
	}

	// Create a channel to receive log lines with larger buffer to prevent message loss
	logChan := make(chan string)
	done := make(chan struct{})

	// Start tailing the log file, reading the last 500 lines first
	stopMonitoring := asaserver.TailLogFileWithLines(logPath, 500, func(line string) {
		select {
		case logChan <- line:
		case <-done:
			return
		}
	})

	defer func() {
		close(done)
		stopMonitoring()
	}()

	// Stream new log lines as they arrive
	for {
		select {
		case line, ok := <-logChan:
			if !ok {
				return
			}
			fmt.Fprintf(c.Writer, "data: %s\n\n", line)
			c.Writer.Flush()
		case <-c.Request.Context().Done():
			return
		}
	}
}

// streamSystemLogs streams system logs via Server-Sent Events (SSE)
func (s *APIServer) streamSystemLogs(c *gin.Context) {
	// Get the system log file path from logger package
	logPath := logger.GetLogFilePath()

	// Check if log file exists
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("system log file not found: %s", logPath),
		})
		return
	}

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")

	// Create a channel to receive log lines with larger buffer to prevent message loss
	logChan := make(chan string)
	done := make(chan struct{})

	// Start tailing the log file, reading the last 500 lines first
	stopMonitoring := asaserver.TailLogFileWithLines(logPath, 500, func(line string) {
		select {
		case logChan <- line:
		case <-done:
			return
		}
	})

	defer func() {
		close(done)
		stopMonitoring()
	}()

	// Stream new log lines as they arrive
	for {
		select {
		case line, ok := <-logChan:
			if !ok {
				return
			}
			fmt.Fprintf(c.Writer, "data: %s\n\n", line)
			c.Writer.Flush()
		case <-c.Request.Context().Done():
			return
		}
	}
}

// getServerConfigs returns both Game.ini and GameUserSettings.ini from the base server directory
func (s *APIServer) getServerConfigs(c *gin.Context) {
	gameIniContent, gameIniErr := asaserver.GetServerGameIniContent()
	gameUserSettingsContent, gameUserSettingsErr := asaserver.GetServerGameUserSettingsContent()

	if gameIniErr != nil && gameUserSettingsErr != nil {
		c.JSON(http.StatusNotFound, StatusResponse{
			Success: false,
			Error:   "Both configuration files not found in server base directory",
		})
		return
	}

	// Build response data with error handling
	gameIniData := gin.H{
		"filename": "Game.ini",
		"content":  gameIniContent,
	}
	if gameIniErr != nil {
		gameIniData["error"] = gameIniErr.Error()
	}

	gameUserSettingsData := gin.H{
		"filename": "GameUserSettings.ini",
		"content":  gameUserSettingsContent,
	}
	if gameUserSettingsErr != nil {
		gameUserSettingsData["error"] = gameUserSettingsErr.Error()
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "Configuration files retrieved from server base directory",
		Data: gin.H{
			"game_ini":           gameIniData,
			"game_user_settings": gameUserSettingsData,
		},
	})
}

// getInstanceConfigs returns both Game.ini and GameUserSettings.ini for an instance
func (s *APIServer) getInstanceConfigs(c *gin.Context) {
	instanceName := c.Param("name")

	gameIniContent, gameIniErr := asaserver.GetGameIniContent(instanceName)
	gameUserSettingsContent, gameUserSettingsErr := asaserver.GetGameUserSettingsContent(instanceName)

	if gameIniErr != nil && gameUserSettingsErr != nil {
		c.JSON(http.StatusNotFound, StatusResponse{
			Success: false,
			Error:   "Both configuration files not found",
		})
		return
	}

	// Build response data with error handling
	gameIniData := gin.H{
		"filename": "Game.ini",
		"content":  gameIniContent,
	}
	if gameIniErr != nil {
		gameIniData["error"] = gameIniErr.Error()
	}

	gameUserSettingsData := gin.H{
		"filename": "GameUserSettings.ini",
		"content":  gameUserSettingsContent,
	}
	if gameUserSettingsErr != nil {
		gameUserSettingsData["error"] = gameUserSettingsErr.Error()
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Configuration files retrieved for instance '%s'", instanceName),
		Data: gin.H{
			"game_ini":           gameIniData,
			"game_user_settings": gameUserSettingsData,
		},
	})
}

// getGameIni returns the content of Game.ini for an instance
func (s *APIServer) getGameIni(c *gin.Context) {
	instanceName := c.Param("name")

	content, err := asaserver.GetGameIniContent(instanceName)
	if err != nil {
		c.JSON(http.StatusNotFound, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Game.ini retrieved for instance '%s'", instanceName),
		Data: gin.H{
			"filename": "Game.ini",
			"content":  content,
		},
	})
}

// getGameUserSettings returns the content of GameUserSettings.ini for an instance
func (s *APIServer) getGameUserSettings(c *gin.Context) {
	instanceName := c.Param("name")

	content, err := asaserver.GetGameUserSettingsContent(instanceName)
	if err != nil {
		c.JSON(http.StatusNotFound, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("GameUserSettings.ini retrieved for instance '%s'", instanceName),
		Data: gin.H{
			"filename": "GameUserSettings.ini",
			"content":  content,
		},
	})
}

// uploadGameIni uploads and overwrites the Game.ini file for an instance
func (s *APIServer) uploadGameIni(c *gin.Context) {
	instanceName := c.Param("name")

	// Get the uploaded file
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   "No file provided",
		})
		return
	}

	// Validate file size (max 10MB)
	if file.Size > 10*1024*1024 {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   "File size exceeds 10MB limit",
		})
		return
	}

	// Read the file content
	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to open file: %v", err),
		})
		return
	}
	defer src.Close()

	// Read all content
	content, err := io.ReadAll(src)
	if err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to read file: %v", err),
		})
		return
	}

	// Save the file content to the instance config directory
	if err := asaserver.SaveGameIniContent(instanceName, string(content)); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Game.ini uploaded and saved for instance '%s'", instanceName),
		Data: gin.H{
			"filename": file.Filename,
			"size":     file.Size,
		},
	})
}

// uploadGameUserSettings uploads and overwrites the GameUserSettings.ini file for an instance
func (s *APIServer) uploadGameUserSettings(c *gin.Context) {
	instanceName := c.Param("name")

	// Get the uploaded file
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   "No file provided",
		})
		return
	}

	// Validate file size (max 10MB)
	if file.Size > 10*1024*1024 {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   "File size exceeds 10MB limit",
		})
		return
	}

	// Read the file content
	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to open file: %v", err),
		})
		return
	}
	defer src.Close()

	// Read all content
	content, err := io.ReadAll(src)
	if err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to read file: %v", err),
		})
		return
	}

	// Save the file content to the instance config directory
	if err := asaserver.SaveGameUserSettingsContent(instanceName, string(content)); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("GameUserSettings.ini uploaded and saved for instance '%s'", instanceName),
		Data: gin.H{
			"filename": file.Filename,
			"size":     file.Size,
		},
	})
}

// updateGameIni updates Game.ini content directly from request body text
func (s *APIServer) updateGameIni(c *gin.Context) {
	instanceName := c.Param("name")

	var req ConfigFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Validate content is not empty
	if strings.TrimSpace(req.Content) == "" {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   "Content cannot be empty",
		})
		return
	}

	// Save the content to Game.ini
	if err := asaserver.SaveGameIniContent(instanceName, req.Content); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Game.ini updated successfully for instance '%s'", instanceName),
		Data: gin.H{
			"filename": "Game.ini",
			"size":     len(req.Content),
		},
	})
}

// updateGameUserSettings updates GameUserSettings.ini content directly from request body text
func (s *APIServer) updateGameUserSettings(c *gin.Context) {
	instanceName := c.Param("name")

	var req ConfigFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Validate content is not empty
	if strings.TrimSpace(req.Content) == "" {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   "Content cannot be empty",
		})
		return
	}

	// Save the content to GameUserSettings.ini
	if err := asaserver.SaveGameUserSettingsContent(instanceName, req.Content); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("GameUserSettings.ini updated successfully for instance '%s'", instanceName),
		Data: gin.H{
			"filename": "GameUserSettings.ini",
			"size":     len(req.Content),
		},
	})
}

// getInstanceConfig returns the configuration of a specific instance
func (s *APIServer) getInstanceConfig(c *gin.Context) {
	instanceName := c.Param("name")

	config, err := asaserver.LoadInstanceConfig(instanceName)
	if err != nil {
		c.JSON(http.StatusNotFound, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to load instance config: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance config retrieved for '%s'", instanceName),
		Data:    config,
	})
}

// UpdateInstanceConfigRequest represents a request to update instance configuration
type UpdateInstanceConfigRequest struct {
	ServerName            string `json:"ServerName,omitempty"`
	ServerPassword        string `json:"ServerPassword,omitempty"`
	ServerAdminPassword   string `json:"ServerAdminPassword,omitempty"`
	MaxPlayers            *int   `json:"MaxPlayers,omitempty"`
	MapName               string `json:"MapName,omitempty"`
	RCONPort              *int   `json:"RCONPort,omitempty"`
	QueryPort             *int   `json:"QueryPort,omitempty"`
	Port                  *int   `json:"Port,omitempty"`
	ModIDs                string `json:"ModIDs,omitempty"`
	SaveDir               string `json:"SaveDir,omitempty"`
	ClusterID             string `json:"ClusterID,omitempty"`
	CustomStartParameters string `json:"CustomStartParameters,omitempty"`
	EnableAsaPlugin       *bool  `json:"EnableAsaPlugin,omitempty"`
}

// updateInstanceConfig updates the configuration for an instance
func (s *APIServer) updateInstanceConfig(c *gin.Context) {
	instanceName := c.Param("name")

	var req UpdateInstanceConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Build updates map
	updates := make(map[string]interface{})
	if req.ServerName != "" {
		updates["ServerName"] = req.ServerName
	}
	updates["ServerPassword"] = req.ServerPassword
	if req.ServerAdminPassword != "" {
		updates["ServerAdminPassword"] = req.ServerAdminPassword
	}
	if req.MaxPlayers != nil {
		updates["MaxPlayers"] = *req.MaxPlayers
	}
	if req.MapName != "" {
		updates["MapName"] = req.MapName
	}
	if req.RCONPort != nil {
		updates["RCONPort"] = *req.RCONPort
	}
	if req.QueryPort != nil {
		updates["QueryPort"] = *req.QueryPort
	}
	if req.Port != nil {
		updates["Port"] = *req.Port
	}
	updates["ModIDs"] = req.ModIDs
	if req.SaveDir != "" {
		updates["SaveDir"] = req.SaveDir
	}
	if req.ClusterID != "" {
		updates["ClusterID"] = req.ClusterID
	}
	if req.CustomStartParameters != "" {
		updates["CustomStartParameters"] = req.CustomStartParameters
	}
	if req.EnableAsaPlugin != nil {
		updates["EnableAsaPlugin"] = *req.EnableAsaPlugin
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   "No fields to update",
		})
		return
	}

	if err := asaserver.UpdateInstanceConfig(instanceName, updates); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance config updated successfully for '%s'", instanceName),
	})
}

// syncGameConfig syncs game configuration files (Game.ini and GameUserSettings.ini) from base server to instances
func (s *APIServer) syncGameConfig(c *gin.Context) {
	var req SyncConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	if len(req.Instances) == 0 {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   "At least one instance name is required",
		})
		return
	}

	// Sync config for each instance
	var failedInstances []string
	var successInstances []string

	for _, instanceName := range req.Instances {
		if err := asaserver.SyncGameConfigToInstance(instanceName); err != nil {
			logger.GetLogger().Errorf("Failed to sync config for instance '%s': %v", instanceName, err)
			failedInstances = append(failedInstances, instanceName)
		} else {
			logger.GetLogger().Infof("Successfully synced game configuration for instance '%s'", instanceName)
			successInstances = append(successInstances, instanceName)
		}
	}

	// Return results
	if len(failedInstances) > 0 {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: fmt.Sprintf("%d of %d instances synced successfully", len(successInstances), len(req.Instances)),
			Error:   fmt.Sprintf("failed to sync configuration for instances: %v", failedInstances),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Configuration synced successfully for %d instances", len(successInstances)),
		Data: gin.H{
			"synced_instances": successInstances,
			"count":            len(successInstances),
		},
	})
}

// syncInstanceConfig syncs instance configuration and Config folder from a source instance to multiple target instances
func (s *APIServer) syncInstanceConfig(c *gin.Context) {
	var req SyncInstanceConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	if len(req.TargetInstances) == 0 {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   "At least one target instance name is required",
		})
		return
	}

	// Sync config from source to each target instance
	results := asaserver.SyncInstanceConfigToMultiple(req.SourceInstance, req.TargetInstances, req.SyncCustomStartParameters, req.SyncEnableAsaPlugin)

	// Separate successful and failed instances
	var successInstances []string
	var failedInstances []map[string]interface{}

	for instanceName, err := range results {
		if err != nil {
			failedInstances = append(failedInstances, map[string]interface{}{
				"instance": instanceName,
				"error":    err.Error(),
			})
			logger.GetLogger().Errorf("Failed to sync config for instance '%s': %v", instanceName, err)
		} else {
			successInstances = append(successInstances, instanceName)
			logger.GetLogger().Infof("Successfully synced config from '%s' to instance '%s'", req.SourceInstance, instanceName)
		}
	}

	// Return results
	if len(failedInstances) > 0 {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: fmt.Sprintf("Synced %d of %d target instances", len(successInstances), len(req.TargetInstances)),
			Error:   fmt.Sprintf("Failed to sync %d instances", len(failedInstances)),
			Data: gin.H{
				"synced_instances": successInstances,
				"failed_instances": failedInstances,
				"success_count":    len(successInstances),
				"failure_count":    len(failedInstances),
			},
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance configuration synced successfully from '%s' to %d target instances", req.SourceInstance, len(successInstances)),
		Data: gin.H{
			"synced_instances": successInstances,
			"count":            len(successInstances),
		},
	})
}
