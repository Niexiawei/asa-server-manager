package frpmanage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"asa-server/internal/webapi/apiresp"

	"github.com/gin-gonic/gin"
)

// GetFRPConfig 返回结构化的 FRP 配置。
//
// 未配置时返回一份空配置而不是 404：前端表单要有个东西可以绑定，
// 「没配过」与「配置文件读坏了」的区别由 /status 的 configured 字段表达。
func GetFRPConfig(c *gin.Context) {
	manager := GetGlobalManager()
	if manager == nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Message: "Failed to retrieve FRP config",
			Error:   "FRP manager not initialized",
		})
		return
	}

	cfg := manager.Config()
	if cfg == nil {
		cfg = &Config{Rules: []PortRule{}}
	}
	if cfg.Rules == nil {
		cfg.Rules = []PortRule{}
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "FRP config retrieved successfully",
		Data:    cfg,
	})
}

// UpdateFRPConfig 用结构化参数覆盖 FRP 配置。
//
// 与旧接口的区别：收的是参数不是配置文件文本，因此能在保存时就校验并给出
// 「第几条规则错在哪」这种可读错误，而不是等启动失败。
func UpdateFRPConfig(c *gin.Context) {
	manager := GetGlobalManager()
	if manager == nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Message: "Failed to update FRP config",
			Error:   "FRP manager not initialized",
		})
		return
	}

	var cfg Config
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Message: "Failed to update FRP config",
			Error:   fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	warnings, err := manager.SetConfig(&cfg)
	if err != nil {
		// 校验失败是用户输入问题（400）；落盘/热更新失败是服务端问题（500）。
		// 两者对用户的意义完全不同：前者改表单就好，后者要去看日志。
		status := http.StatusInternalServerError
		if _, verr := cfg.Validate(); verr != nil {
			status = http.StatusBadRequest
		}
		c.JSON(status, apiresp.StatusResponse{
			Success: false,
			Message: "Failed to update FRP config",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "FRP config updated successfully",
		Data:    gin.H{"warnings": warnings},
	})
}

// GetFRPStatus 返回一次性的运行状态。
func GetFRPStatus(c *gin.Context) {
	manager := GetGlobalManager()
	if manager == nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Message: "Failed to retrieve FRP status",
			Error:   "FRP manager not initialized",
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "FRP status retrieved successfully",
		Data:    manager.Status(),
	})
}

// StreamFRPStatus 用 SSE 推送状态变化。
//
// 与 /status 返回**同一个** FRPStatus —— 两个端点由同一处构造，不再各自拼字符串。
// 只在内容变化时推送（外加首帧与心跳）：状态本身只在用户操作和异步登录失败时
// 才变，每秒无脑推一遍纯属噪声。
func StreamFRPStatus(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	manager := GetGlobalManager()
	if manager == nil {
		fmt.Fprint(c.Writer, "data: {\"error\":\"FRP manager not initialized\"}\n\n")
		return
	}

	const (
		pollInterval      = time.Second
		heartbeatInterval = 25 * time.Second
	)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var lastPayload []byte
	lastSent := time.Now()

	// 首帧立即发，别让前端等满一个 tick 才看到状态。
	if payload, err := json.Marshal(manager.Status()); err == nil {
		lastPayload = payload
		fmt.Fprintf(c.Writer, "data: %s\n\n", payload)
		c.Writer.Flush()
	}

	c.Stream(func(w io.Writer) bool {
		select {
		case <-ticker.C:
			payload, err := json.Marshal(manager.Status())
			if err != nil {
				return true
			}
			if !bytes.Equal(payload, lastPayload) {
				lastPayload = payload
				lastSent = time.Now()
				fmt.Fprintf(w, "data: %s\n\n", payload)
				return true
			}
			// 心跳注释帧：保持连接不被中间的反代按空闲超时掐断。
			if time.Since(lastSent) >= heartbeatInterval {
				lastSent = time.Now()
				fmt.Fprint(w, ": ping\n\n")
			}
			return true
		case <-c.Request.Context().Done():
			return false
		}
	})
}

// StartFRP starts the FRP client
func StartFRP(c *gin.Context) {
	manager := GetGlobalManager()
	if manager == nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Message: "Failed to start FRP",
			Error:   "FRP manager not initialized",
		})
		return
	}

	if manager.IsRunning() {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Message: "Failed to start FRP",
			Error:   "FRP is already running",
		})
		return
	}

	if err := manager.Start(); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrNotConfigured) {
			status = http.StatusBadRequest
		}
		c.JSON(status, apiresp.StatusResponse{
			Success: false,
			Message: "Failed to start FRP",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "FRP started successfully",
	})
}

// StopFRP stops the FRP client
func StopFRP(c *gin.Context) {
	manager := GetGlobalManager()
	if manager == nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Message: "Failed to stop FRP",
			Error:   "FRP manager not initialized",
		})
		return
	}

	if !manager.IsRunning() {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Message: "Failed to stop FRP",
			Error:   "FRP is not running",
		})
		return
	}

	if err := manager.Stop(); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Message: "Failed to stop FRP",
			Error:   fmt.Sprintf("Failed to stop FRP: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "FRP stopped successfully",
	})
}

// RestartFRP restarts the FRP client
func RestartFRP(c *gin.Context) {
	manager := GetGlobalManager()
	if manager == nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Message: "Failed to restart FRP",
			Error:   "FRP manager not initialized",
		})
		return
	}

	if err := manager.Restart(); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrNotConfigured) {
			status = http.StatusBadRequest
		}
		c.JSON(status, apiresp.StatusResponse{
			Success: false,
			Message: "Failed to restart FRP",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
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
