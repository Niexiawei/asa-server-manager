package frpmanage

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

// GetFRPConfig retrieves the FRP client configuration file
func GetFRPConfig(c *gin.Context) {
	configPath := filepath.Join(frpConfigDir, frpcConfigFileName)

	// Check if config file exists
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, StatusResponse{
				Success: false,
				Message: "Failed to retrieve FRP config",
				Error:   "FRP config file not found",
			})
		} else {
			c.JSON(http.StatusInternalServerError, StatusResponse{
				Success: false,
				Message: "Failed to retrieve FRP config",
				Error:   fmt.Sprintf("Failed to read config: %v", err),
			})
		}
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "FRP config retrieved successfully",
		Data:    string(data),
	})
}

// UpdateFRPConfig updates the FRP client configuration file
func UpdateFRPConfig(c *gin.Context) {
	var req struct {
		Config string `json:"config" binding:"required"`
	}

	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Message: "Failed to update FRP config",
			Error:   fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	configPath := filepath.Join(frpConfigDir, frpcConfigFileName)

	// Write config to file
	if err := os.WriteFile(configPath, []byte(req.Config), 0644); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: "Failed to update FRP config",
			Error:   fmt.Sprintf("Failed to write config: %v", err),
		})
		return
	}

	// Restart frpc if running
	manager := GetGlobalManager()
	if manager != nil && manager.IsRunning() {
		if err := manager.Restart(); err != nil {
			c.JSON(http.StatusInternalServerError, StatusResponse{
				Success: false,
				Message: "FRP config updated but failed to restart",
				Error:   fmt.Sprintf("Failed to restart frpc: %v", err),
			})
			return
		}
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "FRP config updated successfully",
	})
}

// GetFRPStatus retrieves the current FRP client status
func GetFRPStatus(c *gin.Context) {
	manager := GetGlobalManager()

	status := "stopped"
	if manager != nil && manager.CheckStatus() {
		status = "running"
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "FRP status retrieved successfully",
		Data: gin.H{
			"status": status,
		},
	})
}

// StreamFRPStatus streams FRP status changes via SSE
func StreamFRPStatus(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	manager := GetGlobalManager()
	if manager == nil {
		c.String(http.StatusInternalServerError, "FRP manager not initialized")
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
				if manager.CheckStatus() {
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

// StartFRP starts the FRP client
func StartFRP(c *gin.Context) {
	manager := GetGlobalManager()
	if manager == nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: "Failed to start FRP",
			Error:   "FRP manager not initialized",
		})
		return
	}

	if manager.IsRunning() {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Message: "Failed to start FRP",
			Error:   "FRP is already running",
		})
		return
	}

	if err := manager.Start(); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: "Failed to start FRP",
			Error:   fmt.Sprintf("Failed to start FRP: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "FRP started successfully",
	})
}

// StopFRP stops the FRP client
func StopFRP(c *gin.Context) {
	manager := GetGlobalManager()
	if manager == nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: "Failed to stop FRP",
			Error:   "FRP manager not initialized",
		})
		return
	}

	if !manager.IsRunning() {
		c.JSON(http.StatusBadRequest, StatusResponse{
			Success: false,
			Message: "Failed to stop FRP",
			Error:   "FRP is not running",
		})
		return
	}

	if err := manager.Stop(); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: "Failed to stop FRP",
			Error:   fmt.Sprintf("Failed to stop FRP: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "FRP stopped successfully",
	})
}

// RestartFRP restarts the FRP client
func RestartFRP(c *gin.Context) {
	manager := GetGlobalManager()
	if manager == nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: "Failed to restart FRP",
			Error:   "FRP manager not initialized",
		})
		return
	}

	if err := manager.Restart(); err != nil {
		c.JSON(http.StatusInternalServerError, StatusResponse{
			Success: false,
			Message: "Failed to restart FRP",
			Error:   fmt.Sprintf("Failed to restart FRP: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, StatusResponse{
		Success: true,
		Message: "FRP restarted successfully",
	})
}

// RegisterFRPRoutes registers FRP-related routes to the API server
func RegisterFRPRoutes(router *gin.Engine) {
	frp := router.Group("/api/frp")
	{
		frp.GET("/config", GetFRPConfig)
		frp.PUT("/config", UpdateFRPConfig)
		frp.GET("/status", GetFRPStatus)
		frp.GET("/status/stream", StreamFRPStatus)
		frp.POST("/start", StartFRP)
		frp.POST("/stop", StopFRP)
		frp.POST("/restart", RestartFRP)
	}
}
