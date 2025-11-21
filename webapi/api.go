package webapi

import (
	"asa-server/asaserver"
	"asa-server/logger"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/urfave/cli/v3"
)

// APIServer represents the HTTP API server for ARK Server Ascended Instance Management
type APIServer struct {
	engine  *gin.Engine
	port    int
	mu      sync.RWMutex
	clients map[*websocket.Conn]bool
}

var globalAPIServer *APIServer

// NewAPIServer creates a new API server instance
func NewAPIServer(port int) *APIServer {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.Default()
	engine.Use(cors.Default())

	server := &APIServer{
		engine:  engine,
		port:    port,
		clients: make(map[*websocket.Conn]bool),
	}

	// Setup routes
	server.setupRoutes()

	// Set global API server instance
	globalAPIServer = server

	return server
}

// setupRoutes configures all API endpoints
func (s *APIServer) setupRoutes() {
	// Health check endpoint
	s.engine.GET("/health", s.health)

	// Instance management endpoints
	instances := s.engine.Group("/api/instances")
	{
		instances.GET("", s.listInstances)
		instances.POST("", s.createInstance)
		instances.GET("/:name", s.getInstanceStatus)
		instances.DELETE("/:name", s.deleteInstance)
		instances.PUT("/:name", s.renameInstance)
		instances.GET("/:name/config", s.getInstanceConfig)
		instances.PATCH("/:name/config", s.updateInstanceConfig)
	}

	// Server control endpoints
	server := s.engine.Group("/api/server")
	{
		server.POST("/:name/start", s.startServer)
		server.POST("/:name/stop", s.stopServer)
		server.POST("/:name/restart", s.restartServer)
		server.POST("/start-all", s.startAllServers)
		server.POST("/stop-all", s.stopAllServers)
		server.POST("/restart-all", s.restartAllServers)
	}

	// RCON endpoints
	rcon := s.engine.Group("/api/rcon")
	{
		rcon.POST("/:name/command", s.sendRCONCommand)
	}

	// Backup/Restore endpoints
	backup := s.engine.Group("/api/backup")
	{
		backup.POST("/:name", s.backupInstance)
		backup.GET("", s.listBackups)
		backup.POST("/:name/restore", s.restoreBackup)
	}

	// Logs endpoints
	logs := s.engine.Group("/api/logs")
	{
		logs.GET("/:name", s.streamInstanceLogs)
	}

	// Config file endpoints
	config := s.engine.Group("/api/config")
	{
		config.GET("/:name/game-ini", s.getGameIni)
		config.GET("/:name/game-user-settings", s.getGameUserSettings)
		config.POST("/:name/game-ini", s.uploadGameIni)
		config.POST("/:name/game-user-settings", s.uploadGameUserSettings)
		config.PUT("/:name/game-ini", s.updateGameIni)
		config.PUT("/:name/game-user-settings", s.updateGameUserSettings)
	}

	// Server update endpoints
	s.engine.POST("/api/server/update", s.updateServer)

	// WebSocket endpoints
	s.engine.GET("/api/ws/events", s.handleServerEvents)
}

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
	WorldFolder string `json:"world_folder" binding:"required"`
}

type RestoreRequest struct {
	BackupFile string `json:"backup_file" binding:"required"`
}

type ConfigFileRequest struct {
	Content string `json:"content" binding:"required"`
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
			fmt.Printf("Warning: Failed to copy base server configuration: %v\n", err)
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
	name := c.Param("name")

	// Check if server is already running first (synchronous validation)
	running, err := asaserver.IsServerRunning(name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to check server status: %v", err),
		})
		return
	}

	if running {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Server '%s' is already running", name),
		})
		return
	}

	// Check for duplicate ports (synchronous validation)
	if err := asaserver.CheckForDuplicatePorts(); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Port conflicts detected: %v", err),
		})
		return
	}

	// Broadcast server starting event
	s.BroadcastServerStarting(name)

	// Start server asynchronously to avoid blocking the API request
	go func() {
		if err := asaserver.StartServer(name); err != nil {
			logger.GetLogger().Errorf("failed to start server '%s': %v", name, err)
			// Broadcast error event
			s.BroadcastServerStartFailed(name, err)
			return
		}

		// Broadcast server started event
		s.BroadcastServerStarted(name)
	}()

	// Return success immediately without waiting for the 60-second startup
	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Server '%s' is starting in the background. It should be fully operational in approximately 60 seconds.", name),
	})
}

// stopServer stops a server instance
func (s *APIServer) stopServer(c *gin.Context) {
	name := c.Param("name")

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

	// Create channel to stream messages
	msgChan := make(chan string, 100)
	done := make(chan struct{})

	// Run startup in a goroutine
	go func() {
		defer close(msgChan)
		// Get all available instances
		instances, err := asaserver.GetAvailableInstances()
		if err != nil {
			select {
			case msgChan <- fmt.Sprintf("Error: Failed to get instances: %v", err):
			case <-done:
			}
			return
		}

		if len(instances) == 0 {
			select {
			case msgChan <- "No instances to start":
			case <-done:
			}
			return
		}

		select {
		case msgChan <- fmt.Sprintf("Starting %d server instances...", len(instances)):
		case <-done:
			return
		}

		// Start each instance individually
		var failedInstances []string
		for _, instanceName := range instances {
			// Check if already running
			running, err := asaserver.IsServerRunning(instanceName)
			if err == nil && running {
				select {
				case msgChan <- fmt.Sprintf("Instance '%s' is already running", instanceName):
				case <-done:
					return
				}
				continue
			}

			// Broadcast starting event
			s.BroadcastServerStarting(instanceName)
			select {
			case msgChan <- fmt.Sprintf("Starting instance '%s'...", instanceName):
			case <-done:
				return
			}

			// Start the instance
			if err := asaserver.StartServer(instanceName); err != nil {
				failedInstances = append(failedInstances, instanceName)
				select {
				case msgChan <- fmt.Sprintf("Error starting '%s': %v", instanceName, err):
				case <-done:
					return
				}
				// Broadcast error event
				s.BroadcastServerStartFailed(instanceName, err)
				continue
			}

			select {
			case msgChan <- fmt.Sprintf("Instance '%s' started successfully", instanceName):
			case <-done:
				return
			}
			// Broadcast started event
			s.BroadcastServerStarted(instanceName)
		}

		// Check if all starts were successful
		if len(failedInstances) > 0 {
			select {
			case msgChan <- fmt.Sprintf("Error: %d of %d instances failed to start", len(failedInstances), len(instances)):
			case <-done:
			}
		} else {
			select {
			case msgChan <- fmt.Sprintf("All %d servers started successfully", len(instances)):
			case <-done:
			}
		}
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

// stopAllServers stops all server instances with SSE progress streaming
func (s *APIServer) stopAllServers(c *gin.Context) {
	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")

	// Create channel to stream messages
	msgChan := make(chan string, 100)
	done := make(chan struct{})

	// Run shutdown in a goroutine
	go func() {
		defer close(msgChan)
		// Get all available instances
		instances, err := asaserver.GetAvailableInstances()
		if err != nil {
			select {
			case msgChan <- fmt.Sprintf("Error: Failed to get instances: %v", err):
			case <-done:
			}
			return
		}

		if len(instances) == 0 {
			select {
			case msgChan <- "No instances to stop":
			case <-done:
			}
			return
		}

		select {
		case msgChan <- fmt.Sprintf("Stopping %d server instances...", len(instances)):
		case <-done:
			return
		}

		// Stop each instance individually
		var failedInstances []string
		for _, instanceName := range instances {
			// Check if already running
			running, err := asaserver.IsServerRunning(instanceName)
			if err != nil || !running {
				select {
				case msgChan <- fmt.Sprintf("Instance '%s' is not running", instanceName):
				case <-done:
					return
				}
				continue
			}

			// Broadcast stopping event
			s.BroadcastServerStopping(instanceName)
			select {
			case msgChan <- fmt.Sprintf("Stopping instance '%s'...", instanceName):
			case <-done:
				return
			}

			// Stop the instance
			if err := asaserver.StopServer(instanceName); err != nil {
				failedInstances = append(failedInstances, instanceName)
				select {
				case msgChan <- fmt.Sprintf("Error stopping '%s': %v", instanceName, err):
				case <-done:
					return
				}
				// Broadcast error event
				s.BroadcastServerStopFailed(instanceName, err)
				continue
			}

			select {
			case msgChan <- fmt.Sprintf("Instance '%s' stopped successfully", instanceName):
			case <-done:
				return
			}
			// Broadcast stopped event
			s.BroadcastServerStopped(instanceName)
		}

		// Check if all stops were successful
		if len(failedInstances) > 0 {
			select {
			case msgChan <- fmt.Sprintf("Error: %d of %d instances failed to stop", len(failedInstances), len(instances)):
			case <-done:
			}
		} else {
			select {
			case msgChan <- fmt.Sprintf("All %d servers stopped successfully", len(instances)):
			case <-done:
			}
		}
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

// restartAllServers restarts all server instances
func (s *APIServer) restartAllServers(c *gin.Context) {
	// Get all available instances
	instances, err := asaserver.GetAvailableInstances()
	if err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	if len(instances) == 0 {
		c.JSON(http.StatusOK, StatusResponse{
			Success: true,
			Message: "No instances to restart",
		})
		return
	}

	// Restart each instance individually
	var failedInstances []string
	for _, instanceName := range instances {
		// Broadcast stopping event
		s.BroadcastServerStopping(instanceName)

		// Restart the instance
		if err := asaserver.RestartServer(instanceName); err != nil {
			failedInstances = append(failedInstances, instanceName)
			// Broadcast error event
			s.BroadcastServerRestartFailed(instanceName, err)
			continue
		}

		// Broadcast started event
		s.BroadcastServerStarted(instanceName)
	}

	// Check if all restarts were successful
	if len(failedInstances) > 0 {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to restart some instances: %v", failedInstances),
			Error:   fmt.Sprintf("%d of %d instances failed to restart", len(failedInstances), len(instances)),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("All %d servers restarted successfully", len(instances)),
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

	var req BackupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	if err := asaserver.BackupInstanceWorld(name, req.WorldFolder); err != nil {
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
	backups, err := asaserver.GetAvailableBackups()
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

	if err := asaserver.RestoreBackupToInstance(name, req.BackupFile); err != nil {
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

// updateServer updates the base server
// SSE Writer for streaming update progress
type SSEWriter struct {
	c chan string
}

func (w *SSEWriter) Write(p []byte) (n int, err error) {
	select {
	case w.c <- string(p):
		return len(p), nil
	default:
		// Channel full, skip this message
		return len(p), nil
	}
}

func (s *APIServer) updateServer(c *gin.Context) {
	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")

	// Create channel to stream update progress
	updateChan := make(chan string, 100)
	done := make(chan struct{})

	// Run update in a goroutine
	go func() {
		defer close(updateChan)
		// Send SteamCMD download and extract message
		select {
		case updateChan <- "Downloading and extracting SteamCMD...":
		case <-done:
			return
		}
		if err := asaserver.DownloadAndExtractSteamCmd(&SSEWriter{c: updateChan}); err != nil {
			select {
			case updateChan <- fmt.Sprintf("Error: Failed to download SteamCMD: %v", err):
			case <-done:
			}
			return
		}

		// Send ARK server update message
		select {
		case updateChan <- "Downloading and updating ARK server files...":
		case <-done:
			return
		}
		if err := asaserver.DownloadAndUpdateArkServer(&SSEWriter{c: updateChan}); err != nil {
			select {
			case updateChan <- fmt.Sprintf("Error: Failed to update ARK server: %v", err):
			case <-done:
			}
			return
		}

		// Server verification
		select {
		case updateChan <- "Verifying server installation...":
		case <-done:
			return
		}
		if err := asaserver.VerifyServerInstallation(false); err != nil {
			select {
			case updateChan <- fmt.Sprintf("Error: Server verification failed: %v", err):
			case <-done:
			}
			return
		}

		// Update completed
		select {
		case updateChan <- "Server update completed successfully!":
		case <-done:
		}
	}()

	// Stream the progress
	c.Stream(func(w io.Writer) bool {
		select {
		case msg, ok := <-updateChan:
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

// Start starts the API server
func (s *APIServer) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	logger.GetLogger().Infof("API server on http://localhost%s", addr)
	return s.engine.Run(addr)
}

// StartWithContext starts the API server with context support for graceful shutdown
func (s *APIServer) StartWithContext(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.port)
	logger.GetLogger().Infof("API server on http://localhost%s", addr)

	// Create HTTP server
	server := &http.Server{
		Addr:    addr,
		Handler: s.engine,
	}

	// Start server in background
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.GetLogger().Errorf("API server error: %v", err)
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()

	// Gracefully shutdown the server
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}

// ActionAPI starts the HTTP API server
func ActionAPI(ctx context.Context, cmd *cli.Command) error {
	port := cmd.Int("port")
	logger.SetLogMode(logger.HttpApiMode)
	apiServer := NewAPIServer(port)
	return apiServer.Start()
}

// streamInstanceLogs streams server logs via Server-Sent Events (SSE)
func (s *APIServer) streamInstanceLogs(c *gin.Context) {
	instanceName := c.Param("name")

	// Get the log file path for the instance
	logPath, exists := asaserver.GetInstanceLogFile(instanceName)
	if !exists {
		// Try to get the log path if not in mapping
		var err error
		logPath, err = asaserver.GetGameLogFilePath(instanceName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, StatusResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to get log file path: %v", err),
			})
			return
		}
	}

	// Check if log file exists
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("log file not found: %s", logPath),
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

	// Start tailing the log file for new lines only (avoids re-reading existing content)
	stopMonitoring := asaserver.TailLogFile(logPath, func(line string) {
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
	if req.ServerPassword != "" {
		updates["ServerPassword"] = req.ServerPassword
	}
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
	if req.ModIDs != "" {
		updates["ModIDs"] = req.ModIDs
	}
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
