package webapi

import (
	"asa-server/asaserver"
	"asa-server/backup"
	"asa-server/common/tail"
	"asa-server/logger"
	"asa-server/parseserver"
	"asa-server/serverinfo"
	"asa-server/win32api"
	"context"
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

// H16 fix: validateInstanceName checks for path traversal attacks
func validateInstanceName(name string) error {
	if name == "" {
		return fmt.Errorf("instance name is required")
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) || strings.ContainsAny(name, "\x00") {
		return fmt.Errorf("invalid instance name")
	}
	return nil
}

type InstanceInfo struct {
	Name          string                    `json:"name"`
	Running       bool                      `json:"running"`
	Config        interface{}               `json:"config,omitempty"`
	Status        string                    `json:"status"`
	StatusHistory []asaserver.InstanceState `json:"status_history"`
	Error         string                    `json:"error,omitempty"`
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
	BackupFile string `json:"backup_file" binding:"required"`
}

type ConfigFileRequest struct {
	Content string `json:"content" binding:"required"`
}

type SyncInstanceConfigRequest struct {
	SourceInstance              string   `json:"source_instance" binding:"required"`
	TargetInstances             []string `json:"target_instances" binding:"required,min=1"`
	SyncCustomStartParameters   *bool    `json:"sync_custom_start_parameters,omitempty"`
	SyncEnableAsaPlugin         *bool    `json:"sync_enable_asa_plugin,omitempty"`
	OnlySyncServerGameINIConfig *bool    `json:"only_sync_server_game_ini_config,omitempty"`
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
		_status, _ := asaserver.GetLatestInstanceState(instanceName)
		history, _ := asaserver.GetInstanceStateHistory(instanceName, 200)

		info := InstanceInfo{
			Name:          instanceName,
			Running:       running,
			Status:        string(_status.Status),
			StatusHistory: history,
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

	if err := validateInstanceName(req.Name); err != nil {
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
	if name == "" {
		c.JSON(http.StatusNotFound, StatusResponse{
			Success: false,
			Error:   "Instance not found",
		})
		return
	}
	running, err := asaserver.IsServerRunning(name)
	config, cfgErr := asaserver.LoadInstanceConfig(name)
	_status, _ := asaserver.GetLatestInstanceState(name)
	history, _ := asaserver.GetInstanceStateHistory(name, 200)
	info := InstanceInfo{
		Name:          name,
		Running:       running,
		Status:        string(_status.Status),
		StatusHistory: history,
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
	if err := validateInstanceName(name); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{Success: false, Error: err.Error()})
		return
	}

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

	// Delete save directories (v2: save is under instances/<name>/Save)
	savePath := filepath.Join(asaserver.InstancesDir, name, "Save")
	os.RemoveAll(savePath)

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance '%s' deleted successfully", name),
	})
}

// renameInstance renames an instance
func (s *APIServer) renameInstance(c *gin.Context) {
	oldName := c.Param("name")
	if err := validateInstanceName(oldName); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{Success: false, Error: err.Error()})
		return
	}

	var req RenameInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	if err := validateInstanceName(req.NewName); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   err.Error(),
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

	// Rename save directories (v2: save is under instances/<name>/Save)
	oldSavePath := filepath.Join(asaserver.InstancesDir, oldName, "Save")
	newSavePath := filepath.Join(asaserver.InstancesDir, req.NewName, "Save")
	if err := os.Rename(oldSavePath, newSavePath); err != nil {
		logger.GetLogger().Warnf("Failed to rename save directory for instance %s: %v", oldName, err)
	}

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

	// Check for duplicate ports before acquiring state lock
	if err := asaserver.CheckForDuplicatePorts(); err != nil {
		c.JSON(http.StatusConflict, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Port conflicts detected: %v", err),
		})
		return
	}

	// 同步 CAS：原子检查并设置状态，立即返回 409 如果不允许
	ok, err := asaserver.CompareAndSwapInstanceState(instanceName,
		[]asaserver.InstanceStatus{
			asaserver.StatusStopped, asaserver.StatusStartFailed,
			asaserver.StatusStopFailed, asaserver.StatusRestartFailed, "",
		},
		asaserver.StatusStartStartInitialization)
	if err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to check instance state: %v", err),
		})
		return
	}
	if !ok {
		c.JSON(http.StatusConflict, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Server '%s' operation not allowed in current state", instanceName),
		})
		return
	}

	// CAS 已成功（state = start_initialization），异步完成启动
	go s.runStartServerTask(instanceName)

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Server '%s' is starting", instanceName),
	})
}

// stopServer stops a server instance
func (s *APIServer) stopServer(c *gin.Context) {
	name := c.Param("name")

	// 同步 CAS：原子检查并设置状态，立即返回 409 如果不允许
	ok, err := asaserver.CompareAndSwapInstanceState(name,
		[]asaserver.InstanceStatus{asaserver.StatusStarted},
		asaserver.StatusStopping)
	if err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to check instance state: %v", err),
		})
		return
	}
	if !ok {
		c.JSON(http.StatusConflict, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Server '%s' operation not allowed in current state", name),
		})
		return
	}

	go s.runStopServerTask(name)

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Server '%s' is stopping", name),
	})
}

// restartServer restarts a server instance
func (s *APIServer) restartServer(c *gin.Context) {
	name := c.Param("name")

	// 同步 CAS：原子检查并设置状态，立即返回 409 如果不允许
	ok, err := asaserver.CompareAndSwapInstanceState(name,
		[]asaserver.InstanceStatus{asaserver.StatusStarted},
		asaserver.StatusRestarting)
	if err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to check instance state: %v", err),
		})
		return
	}
	if !ok {
		c.JSON(http.StatusConflict, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Server '%s' operation not allowed in current state", name),
		})
		return
	}

	go s.runRestartServerTask(name)

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Server '%s' is restarting", name),
	})
}

// forceStopServer force stops a server instance (bypasses state check)
func (s *APIServer) forceStopServer(c *gin.Context) {
	name := c.Param("name")
	if err := asaserver.ForceStopServer(name); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{Success: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Server '%s' has been force stopped", name),
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

// listBackups returns available backup filenames for the given instance
func (s *APIServer) listBackups(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   "instance name is required",
		})
		return
	}

	backups, err := backup.GetAvailableBackups(name)
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

// restoreBackup restores a backup to an instance.
// The backup file is looked up by filename inside the instance's backup directory.
// Before restoring, a latest snapshot of the current state is created automatically.
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

	if name == "" {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   "instance name is required (provide in URL path)",
		})
		return
	}

	// Resolve filename to full path within the instance's backup directory
	backupPath := filepath.Join(asaserver.InstancesDir, name, "backup", req.BackupFile)
	if _, err := os.Stat(backupPath); err != nil {
		c.JSON(http.StatusNotFound, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("backup file not found: %s", req.BackupFile),
		})
		return
	}

	// Save current state as latest snapshot before restoring (best-effort)
	if err := backup.BackupLatestSnapshot(name); err != nil {
		logger.GetLogger().Warnf("Failed to create latest snapshot before restore for '%s': %v", name, err)
	}

	if err := backup.RestoreBackupToInstance(name, backupPath); err != nil {
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

// deleteBackup deletes a specific backup file for an instance.
func (s *APIServer) deleteBackup(c *gin.Context) {
	name := c.Param("name")
	file := c.Param("file")

	if name == "" || file == "" {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   "instance name and file name are required",
		})
		return
	}

	if err := backup.DeleteBackup(name, file); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Backup '%s' deleted successfully", file),
	})
}

// handleServerUpdate handles both starting update and streaming progress via SSE
func (s *APIServer) handleServerUpdate(c *gin.Context) {
	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")
	c.Header("X-Accel-Buffering", "no")

	// H8 fix: Subscribe FIRST to avoid TOCTOU race (missing events between check and subscribe)
	subscriber, unsubscribe := s.updateBroadcaster.Subscribe()
	defer unsubscribe()

	// Only start a new update task when none is running; reconnecting clients subscribe only
	s.updateMu.Lock()
	if !s.updateBroadcaster.IsRunning() {
		if s.updateBroadcaster.Start() {
			ctx, cancel := context.WithCancel(context.Background())
			s.updateCtx = ctx
			s.updateCancel = cancel
			s.updateMu.Unlock()
			go s.runUpdateTask(ctx)
		} else {
			s.updateMu.Unlock()
		}
	} else {
		s.updateMu.Unlock()
	}

	// Replay history for late subscribers (e.g. page refresh)
	for _, msg := range s.updateBroadcaster.GetHistory() {
		fmt.Fprintf(c.Writer, "data: %s\n\n", msg)
	}
	c.Writer.Flush()

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
		case <-s.serverCtx.Done():
			return false
		}
	})
}

// getUpdateStatus returns whether an update is currently running
func (s *APIServer) getUpdateStatus(c *gin.Context) {
	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Data: gin.H{
			"running": s.updateBroadcaster.IsRunning(),
		},
	})
}

// cancelUpdate cancels the currently running update task
func (s *APIServer) cancelUpdate(c *gin.Context) {
	s.updateMu.Lock()
	cancel := s.updateCancel
	s.updateMu.Unlock()

	if cancel == nil || !s.updateBroadcaster.IsRunning() {
		c.JSON(http.StatusNotFound, StatusResponse{
			Success: false,
			Error:   "没有正在进行的更新",
		})
		return
	}

	cancel()
	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "已发送取消指令",
	})
}

// streamServerInfo streams server resource information (CPU, Memory) via SSE every 2000ms
func (s *APIServer) streamServerInfo(c *gin.Context) {
	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")

	// Create ticker for 2000ms interval
	ticker := time.NewTicker(2000 * time.Millisecond)
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
		case <-s.serverCtx.Done():
			return false
		}
	})
}

// streamAllInstancesInfo streams resource information for all running servers via SSE every 2000ms
func (s *APIServer) streamAllInstancesInfo(c *gin.Context) {
	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")

	// Create ticker for 2000ms interval
	ticker := time.NewTicker(2000 * time.Millisecond)
	defer ticker.Stop()
	immediate := make(chan struct{}, 1)
	immediate <- struct{}{}
	defer close(immediate)

	// Stream all instances info
	c.Stream(func(w io.Writer) bool {
		sendMsg := func(w io.Writer) bool {
			// Get all available instances
			instances, err := asaserver.GetAvailableInstances()
			if err != nil {
				fmt.Fprintf(w, "data: {\"error\":\"Failed to get instances: %v\"}\n\n", err)
				return true
			}

			// Get CPU and memory info once for all instances
			cpuInfo, err := serverinfo.GetCPUInfo()
			if err != nil {
				fmt.Fprintf(w, "data: {\"error\":\"Failed to get CPU info: %v\"}\n\n", err)
				return true
			}

			memInfo, err := serverinfo.GetMemoryInfo()
			if err != nil {
				fmt.Fprintf(w, "data: {\"error\":\"Failed to get memory info: %v\"}\n\n", err)
				return true
			}

			// Collect data for all running instances
			instancesData := make([]interface{}, 0)

			for _, instanceName := range instances {

				// Get PID for the instance
				pid, err := asaserver.GetInstancePID(instanceName)
				if err != nil {
					continue
				}

				exited, err := win32api.IsProcessExited(uint32(pid))
				if err != nil {
					continue
				}

				// If process has exited, it's not running
				if exited {
					continue
				}

				// Get process info
				processInfo, err := serverinfo.GetProcessInfo(int32(pid))
				if err != nil {
					continue
				}

				// Calculate total CPU usage: instance CPU% / 100% * core count
				totalCPUUsage := (processInfo.CPUPercent / float64(cpuInfo.CoreCount*100)) * 100

				// Build instance data
				instanceData := map[string]interface{}{
					"instance":          instanceName,
					"running":           true,
					"pid":               pid,
					"cpu_percent":       processInfo.CPUPercent,
					"cpu_total_percent": totalCPUUsage,
					"memory_used":       processInfo.MemoryUsed,
					"memory_percent":    processInfo.MemoryPercent,
					"process_name":      processInfo.Name,
					"memory_used_mb":    float64(processInfo.MemoryUsed) / (1024 * 1024),
					"memory_used_gb":    float64(processInfo.MemoryUsed) / (1024 * 1024 * 1024),
				}

				instancesData = append(instancesData, instanceData)
			}

			// Build response data
			data := map[string]interface{}{
				"timestamp":     time.Now().Unix(),
				"cpu_cores":     cpuInfo.CoreCount,
				"running_count": len(instancesData),
				"memory": map[string]interface{}{
					"total":    memInfo.Total,
					"total_gb": float64(memInfo.Total) / (1024 * 1024 * 1024),
				},
				"instances": instancesData,
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
		}

		select {
		case <-immediate:
			return sendMsg(w)
		case <-ticker.C:
			return sendMsg(w)
		case <-c.Request.Context().Done():
			// Client disconnected
			return false
		case <-s.serverCtx.Done():
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
	// Get the log file path for the instance (v2: direct path, no polling needed)
	logPath, err := asaserver.GetGameLogFilePath(instanceName)
	if err != nil {
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
	logChan := make(chan string, 100)
	done := make(chan struct{})

	// Start tailing the log file, reading the last 500 lines first
	stopMonitoring := asaserver.TailLogFileWithLines(logPath, 200, func(line string) {
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
		case <-s.serverCtx.Done():
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

	tailer, logChan, err := tail.NewTailer(logPath, 500)
	if err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{Success: false, Error: err.Error()})
		return
	}

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")

	tailer.Start()
	defer tailer.Stop()

	c.Stream(func(w io.Writer) bool {
		select {
		case line, ok := <-logChan:
			if !ok {
				return false
			}
			fmt.Fprintf(w, "data: %s\n\n", line)
			return true
		case <-c.Request.Context().Done():
			return false
		case <-s.serverCtx.Done():
			return false
		}
	})
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

// updateInstanceConfig updates the configuration for an instance
func (s *APIServer) updateInstanceConfig(c *gin.Context) {
	instanceName := c.Param("name")

	var req asaserver.UpdateInstanceConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	if err := asaserver.UpdateInstanceConfig(instanceName, req); err != nil {
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

	var (
		opts []asaserver.SetSyncInstanceConfig
	)

	if req.OnlySyncServerGameINIConfig != nil {
		opts = append(opts, asaserver.WithOnlySyncServerGameINIConfig(*req.OnlySyncServerGameINIConfig))
	}

	if req.SyncCustomStartParameters != nil {
		opts = append(opts, asaserver.WithSyncCustomStartParameters(*req.SyncCustomStartParameters))
	}

	if req.SyncEnableAsaPlugin != nil {
		opts = append(opts, asaserver.WithSyncEnableAsaPlugin(*req.SyncEnableAsaPlugin))
	}

	// Sync config from source to each target instance
	results := asaserver.SyncInstanceConfigToMultiple(req.SourceInstance, req.TargetInstances,
		opts...,
	)

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

// getModInfo returns the content of mod_info.json
func (s *APIServer) getModInfo(c *gin.Context) {
	// Construct the path to mod_info.json
	modInfoPath := filepath.Join(asaserver.BaseDir, "mod_info.json")

	// Check if the file exists
	if _, err := os.Stat(modInfoPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, StatusResponse{
			Success: false,
			Error:   "mod_info.json file not found",
		})
		return
	}

	// Read the file
	file, err := os.Open(modInfoPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to open mod_info.json: %v", err),
		})
		return
	}
	defer file.Close()

	// Decode JSON content
	var modInfo []asaserver.ModInfo
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&modInfo); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to decode mod_info.json: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "Mod info retrieved successfully",
		Data:    modInfo,
	})
}

// ========== Save Parse handlers ==========

func (s *APIServer) getSavePlayers(c *gin.Context) {
	s.handleSaveParse(c, parseserver.ParseTypePlayers)
}

func (s *APIServer) getSaveTribes(c *gin.Context) {
	s.handleSaveParse(c, parseserver.ParseTypeTribes)
}

func (s *APIServer) getSaveAll(c *gin.Context) {
	instanceName := c.Param("instance")
	if instanceName == "" {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Message: "instance parameter is required",
			Error:   "missing instance parameter",
		})
		return
	}

	// Try cache first
	if s.saveDataManager != nil {
		cached, err := s.saveDataManager.GetCached(instanceName)
		if err == nil && cached != nil {
			c.JSON(http.StatusOK, StatusResponse{
				Success: true,
				Message: "Save data retrieved from cache",
				Data:    cached,
			})
			return
		}
	}

	// Fallback to synchronous parse
	s.handleSaveParse(c, parseserver.ParseTypeAll)
}

func (s *APIServer) handleSaveParse(c *gin.Context, parseType parseserver.ParseType) {
	if !parseserver.IsParserAvailable() {
		c.JSON(http.StatusServiceUnavailable, StatusResponse{
			Success: false,
			Message: "Save parser not available",
			Error:   "save parser not available",
		})
		return
	}

	instanceName := c.Param("instance")
	if instanceName == "" {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Message: "instance parameter is required",
			Error:   "missing instance parameter",
		})
		return
	}

	savePath, err := findSaveFileByInstance(instanceName)
	if err != nil {
		c.JSON(http.StatusNotFound, StatusResponse{
			Success: false,
			Message: "Save file not found",
			Error:   err.Error(),
		})
		return
	}

	result, err := parseserver.ParseSave(c.Request.Context(), savePath, parseType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: "Failed to parse save file",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "Save file parsed successfully",
		Data:    result.Data,
	})
}

// streamSaveData streams save data changes via SSE
func (s *APIServer) streamSaveData(c *gin.Context) {
	instanceName := c.Param("instance")
	if instanceName == "" {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Message: "instance parameter is required",
		})
		return
	}

	if s.saveDataManager == nil {
		c.JSON(http.StatusServiceUnavailable, StatusResponse{
			Success: false,
			Message: "Save data manager not initialized",
		})
		return
	}

	// Set SSE headers once
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	// Send cached data immediately if available
	cached, err := s.saveDataManager.GetCached(instanceName)
	if err == nil && cached != nil {
		data, _ := json.Marshal(map[string]any{
			"type":     "cached",
			"instance": instanceName,
			"data":     cached,
		})
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		c.Writer.Flush()
	}

	broadcaster := s.saveDataManager.GetBroadcaster(instanceName)
	ch, unsubscribe := broadcaster.Subscribe()
	defer unsubscribe()

	c.Writer.Flush()

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.serverCtx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(c.Writer, "data: %s\n\n", msg)
			c.Writer.Flush()
		}
	}
}

// findSaveFileByInstance finds the .ark save file for a given instance name
func findSaveFileByInstance(instanceName string) (string, error) {
	config, err := asaserver.LoadInstanceConfig(instanceName)
	if err != nil {
		return "", fmt.Errorf("failed to load instance config for %s: %w", instanceName, err)
	}

	saveDir := filepath.Join(asaserver.InstancesDir, instanceName, "Save", config.MapName)
	savePath := filepath.Join(saveDir, config.MapName+".ark")
	if _, err := os.Stat(savePath); err != nil {
		return "", fmt.Errorf("save file not found for instance %s: %s", instanceName, savePath)
	}

	return savePath, nil
}
