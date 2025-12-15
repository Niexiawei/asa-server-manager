package syncthingmanage

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
)

type StatusResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// GetSyncthingConfig retrieves the Syncthing configuration file
func GetSyncthingConfig(c *gin.Context) {
	configPath := filepath.Join(syncthingConfigDir, syncthingConfigFileName)

	// Check if config file exists
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty XML if config file doesn't exist
			emptyXML := `<?xml version="1.0" encoding="UTF-8"?>
<configuration></configuration>`
			c.JSON(http.StatusOK, StatusResponse{
				Success: true,
				Message: "Syncthing config not found, returning empty template",
				Data:    emptyXML,
			})
		} else {
			c.JSON(http.StatusInternalServerError, StatusResponse{
				Success: false,
				Message: "Failed to retrieve Syncthing config",
				Error:   fmt.Sprintf("Failed to read config: %v", err),
			})
		}
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "Syncthing config retrieved successfully",
		Data:    string(data),
	})
}

// UpdateSyncthingConfig updates the Syncthing configuration file
func UpdateSyncthingConfig(c *gin.Context) {
	var req struct {
		Config string `json:"config" binding:"required"`
	}

	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Message: "Failed to update Syncthing config",
			Error:   fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	configPath := filepath.Join(syncthingConfigDir, syncthingConfigFileName)

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Message: "Cannot update Syncthing config",
			Error:   "Syncthing config file does not exist. Please start Syncthing first to generate the config file.",
		})
		return
	}

	// Write config to file
	if err := os.WriteFile(configPath, []byte(req.Config), 0644); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: "Failed to update Syncthing config",
			Error:   fmt.Sprintf("Failed to write config: %v", err),
		})
		return
	}

	// Restart syncthing if running
	manager := GetGlobalManager()
	if manager != nil && manager.IsRunning() {
		if err := manager.Restart(); err != nil {
			c.JSON(http.StatusInternalServerError, StatusResponse{
				Success: false,
				Message: "Syncthing config updated but failed to restart",
				Error:   fmt.Sprintf("Failed to restart syncthing: %v", err),
			})
			return
		}
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "Syncthing config updated successfully",
	})
}

// GetSyncthingStatus retrieves the current Syncthing status
func GetSyncthingStatus(c *gin.Context) {
	manager := GetGlobalManager()

	status := "stopped"
	if manager != nil && manager.CheckStatus() {
		status = "running"
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "Syncthing status retrieved successfully",
		Data: gin.H{
			"status": status,
		},
	})
}

// StreamSyncthingStatus streams Syncthing status changes via SSE
func StreamSyncthingStatus(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	manager := GetGlobalManager()
	if manager == nil {
		fmt.Fprintf(c.Writer, "data: {\"error\":\"Syncthing manager not initialized\"}\n\n")
		return
	}

	// Channel for status updates
	statusChannel := make(chan string, 1)
	done := make(chan struct{})

	// Start background goroutine to monitor status changes
	go func() {
		defer close(statusChannel)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		lastStatus := ""

		for {
			select {
			case <-ticker.C:
				currentStatus := "stopped"
				// Check if there was a start error
				if manager.GetStartErr() != nil {
					currentStatus = "stopped"
				} else if manager.CheckStatus() {
					currentStatus = "running"
				}

				// Only send if status changed
				if currentStatus != lastStatus {
					lastStatus = currentStatus
					select {
					case statusChannel <- currentStatus:
					case <-done:
						return
					}
				}
			case <-done:
				return
			}
		}
	}()

	// Send initial status
	initialStatus := "stopped"
	if manager.CheckStatus() {
		initialStatus = "running"
	}
	statusChannel <- initialStatus

	// Stream status using c.Stream
	c.Stream(func(w io.Writer) bool {
		select {
		case status, ok := <-statusChannel:
			if !ok {
				return false
			}
			fmt.Fprintf(w, "data: {\"status\":\"%s\"}\n\n", status)
			return true
		case <-c.Request.Context().Done():
			close(done)
			return false
		}
	})
}

// StartSyncthing starts the Syncthing client
func StartSyncthing(c *gin.Context) {
	manager := GetGlobalManager()
	if manager == nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: "Failed to start Syncthing",
			Error:   "Syncthing manager not initialized",
		})
		return
	}

	if manager.IsRunning() {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Message: "Failed to start Syncthing",
			Error:   "Syncthing is already running",
		})
		return
	}

	if err := manager.Start(); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: "Failed to start Syncthing",
			Error:   fmt.Sprintf("Failed to start Syncthing: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "Syncthing started successfully",
	})
}

// StopSyncthing stops the Syncthing client
func StopSyncthing(c *gin.Context) {
	manager := GetGlobalManager()
	if manager == nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: "Failed to stop Syncthing",
			Error:   "Syncthing manager not initialized",
		})
		return
	}

	if !manager.IsRunning() {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Message: "Failed to stop Syncthing",
			Error:   "Syncthing is not running",
		})
		return
	}

	if err := manager.Stop(); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: "Failed to stop Syncthing",
			Error:   fmt.Sprintf("Failed to stop Syncthing: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "Syncthing stopped successfully",
	})
}

// RestartSyncthing restarts the Syncthing client
func RestartSyncthing(c *gin.Context) {
	manager := GetGlobalManager()
	if manager == nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: "Failed to restart Syncthing",
			Error:   "Syncthing manager not initialized",
		})
		return
	}

	if err := manager.Restart(); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: "Failed to restart Syncthing",
			Error:   fmt.Sprintf("Failed to restart Syncthing: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "Syncthing restarted successfully",
	})
}

// RegisterSyncthingRoutes registers Syncthing-related routes to the API server
func RegisterSyncthingRoutes(router *gin.Engine) {
	syncthing := router.Group("/api/syncthing")
	{
		syncthing.GET("/config", GetSyncthingConfig)
		syncthing.PUT("/config", UpdateSyncthingConfig)
		syncthing.GET("/status", GetSyncthingStatus)
		syncthing.GET("/status/stream", StreamSyncthingStatus)
		syncthing.POST("/start", StartSyncthing)
		syncthing.POST("/stop", StopSyncthing)
		syncthing.POST("/restart", RestartSyncthing)
	}
}
