package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

// APIServer represents the HTTP API server for ARK Server Ascended Instance Management
type APIServer struct {
	engine *gin.Engine
	port   int
}

// NewAPIServer creates a new API server instance
func NewAPIServer(port int) *APIServer {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.Default()

	server := &APIServer{
		engine: engine,
		port:   port,
	}

	// Setup routes
	server.setupRoutes()

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
	}

	// Server control endpoints
	server := s.engine.Group("/api/server")
	{
		server.POST("/:name/start", s.startServer)
		server.POST("/:name/stop", s.stopServer)
		server.POST("/:name/restart", s.restartServer)
		server.POST("/start-all", s.startAllServers)
		server.POST("/stop-all", s.stopAllServers)
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

	// Server update endpoints
	s.engine.POST("/api/server/update", s.updateServer)
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

// ========== Handlers ==========

// health checks if the API server is running
func (s *APIServer) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"service": "asa-server-manager-api",
	})
}

// listInstances returns all available instances
func (s *APIServer) listInstances(c *gin.Context) {
	instances, err := GetAvailableInstances()
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
		running, err := IsServerRunning(instanceName)
		info := InstanceInfo{
			Name:    instanceName,
			Running: running,
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
	config := CreateDefaultInstanceConfig(req.Name)

	if err := SaveInstanceConfig(req.Name, config); err != nil {
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

	running, err := IsServerRunning(name)
	config, cfgErr := LoadInstanceConfig(name)

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
	if running, _ := IsServerRunning(name); running {
		if err := StopServer(name); err != nil {
			c.JSON(http.StatusInternalServerError, StatusResponse{
				Success: false,
				Error:   fmt.Sprintf("Failed to stop server: %v", err),
			})
			return
		}
	}

	// Delete instance directory
	instanceDir := filepath.Join(InstancesDir, name)
	if err := os.RemoveAll(instanceDir); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Delete save directories
	savePath := filepath.Join(ServerFilesDir, "ShooterGame/Saved", name)
	os.RemoveAll(savePath)

	savedArksPath := filepath.Join(ServerFilesDir, "ShooterGame/Saved/SavedArks", name)
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
	if running, _ := IsServerRunning(oldName); running {
		if err := StopServer(oldName); err != nil {
			c.JSON(http.StatusInternalServerError, StatusResponse{
				Success: false,
				Error:   fmt.Sprintf("Failed to stop server: %v", err),
			})
			return
		}
	}

	// Rename instance directory
	oldPath := filepath.Join(InstancesDir, oldName)
	newPath := filepath.Join(InstancesDir, req.NewName)

	if err := os.Rename(oldPath, newPath); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Rename save directories
	oldSavePath := filepath.Join(ServerFilesDir, "ShooterGame/Saved", oldName)
	newSavePath := filepath.Join(ServerFilesDir, "ShooterGame/Saved", req.NewName)
	os.Rename(oldSavePath, newSavePath)

	oldArksPath := filepath.Join(ServerFilesDir, "ShooterGame/Saved/SavedArks", oldName)
	newArksPath := filepath.Join(ServerFilesDir, "ShooterGame/Saved/SavedArks", req.NewName)
	os.Rename(oldArksPath, newArksPath)

	// Update SaveDir in configuration
	config, err := LoadInstanceConfig(req.NewName)
	if err == nil {
		config.SaveDir = req.NewName
		SaveInstanceConfig(req.NewName, config)
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance renamed from '%s' to '%s'", oldName, req.NewName),
	})
}

// startServer starts a server instance
func (s *APIServer) startServer(c *gin.Context) {
	name := c.Param("name")

	if err := StartServer(name); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Server '%s' started successfully", name),
	})
}

// stopServer stops a server instance
func (s *APIServer) stopServer(c *gin.Context) {
	name := c.Param("name")

	if err := StopServer(name); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Server '%s' stopped successfully", name),
	})
}

// restartServer restarts a server instance
func (s *APIServer) restartServer(c *gin.Context) {
	name := c.Param("name")

	if err := RestartServer(name); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Server '%s' restarted successfully", name),
	})
}

// startAllServers starts all server instances
func (s *APIServer) startAllServers(c *gin.Context) {
	if err := StartAllInstances(); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "All servers started successfully",
	})
}

// stopAllServers stops all server instances
func (s *APIServer) stopAllServers(c *gin.Context) {
	if err := StopAllInstances(); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "All servers stopped successfully",
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

	response, err := SendRCONCommand(name, req.Command)
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

	if err := BackupInstanceWorld(name, req.WorldFolder); err != nil {
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
	backups, err := GetAvailableBackups()
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

	if err := RestoreBackupToInstance(name, req.BackupFile); err != nil {
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
func (s *APIServer) updateServer(c *gin.Context) {
	forceServer := c.DefaultQuery("force-server", "false") == "true"

	if err := DownloadAndExtractSteamCmd(); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	if err := DownloadAndUpdateArkServer(); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	if err := VerifyServerInstallation(forceServer); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "Server updated successfully",
	})
}

// Start starts the API server
func (s *APIServer) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	fmt.Printf("🚀 Starting API server on http://localhost%s\n", addr)
	return s.engine.Run(addr)
}
