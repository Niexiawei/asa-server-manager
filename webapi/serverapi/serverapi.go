package serverapi

import (
	cfgpkg "asa-server/config"
	instancepkg "asa-server/instance"
	"asa-server/logger"
	"asa-server/pkg/serverinfo"
	"asa-server/pkg/winproc"
	procpkg "asa-server/process"
	statepkg "asa-server/state"
	"asa-server/updatemanage"
	"asa-server/webapi/apiresp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	serverCtx context.Context
}

func NewHandler(serverCtx context.Context) *Handler {
	return &Handler{serverCtx: serverCtx}
}

func (h *Handler) RegisterRouter(r *gin.Engine) {
	server := r.Group("/api/server")
	{
		server.GET("/:name/start", h.startServer)
		server.GET("/:name/stop", h.stopServer)
		server.GET("/:name/restart", h.restartServer)
		server.GET("/:name/force-stop", h.forceStopServer)
		server.GET("/:name/countdown", h.getCountdown)
		server.POST("/:name/countdown/cancel", h.cancelCountdown)
		server.GET("/update", h.handleServerUpdate)
		server.GET("/update/status", h.getUpdateStatus)
		server.POST("/update/cancel", h.cancelUpdate)
		server.GET("/info", h.streamServerInfo)
		server.GET("/all-info", h.streamAllInstancesInfo)
	}
}

func (h *Handler) startServer(c *gin.Context) {
	instanceName := c.Param("name")

	// Check for duplicate ports before acquiring state lock
	if err := cfgpkg.CheckForDuplicatePorts(); err != nil {
		c.JSON(http.StatusConflict, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Port conflicts detected: %v", err),
		})
		return
	}

	// 同步 CAS：原子检查并设置状态，立即返回 409 如果不允许
	ok, err := statepkg.CompareAndSwapInstanceState(instanceName,
		[]statepkg.InstanceStatus{
			statepkg.StatusStopped, statepkg.StatusStartFailed,
			statepkg.StatusStopFailed, statepkg.StatusRestartFailed, "",
		},
		statepkg.StatusStartStartInitialization)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to check instance state: %v", err),
		})
		return
	}
	if !ok {
		c.JSON(http.StatusConflict, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Server '%s' operation not allowed in current state", instanceName),
		})
		return
	}

	// CAS 已成功（state = start_initialization），异步完成启动
	go h.runStartServerTask(instanceName)

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Server '%s' is starting", instanceName),
	})
}

func (h *Handler) stopServer(c *gin.Context) {
	name := c.Param("name")

	// 先解析并校验倒计时参数：配错了要在 CAS 改状态之前就返回 400，
	// 否则实例会被留在 stopping 状态上却没人去停它
	countdown, err := parseCountdownQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}

	// 同步 CAS：原子检查并设置状态，立即返回 409 如果不允许
	ok, err := statepkg.CompareAndSwapInstanceState(name,
		[]statepkg.InstanceStatus{statepkg.StatusStarted},
		statepkg.StatusStopping)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to check instance state: %v", err),
		})
		return
	}
	if !ok {
		c.JSON(http.StatusConflict, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Server '%s' operation not allowed in current state", name),
		})
		return
	}

	go h.runStopServerTask(name, countdown)

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Server '%s' is stopping", name),
	})
}

func (h *Handler) restartServer(c *gin.Context) {
	name := c.Param("name")

	countdown, err := parseCountdownQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}

	// 同步 CAS：原子检查并设置状态，立即返回 409 如果不允许
	ok, err := statepkg.CompareAndSwapInstanceState(name,
		[]statepkg.InstanceStatus{statepkg.StatusStarted},
		statepkg.StatusRestarting)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to check instance state: %v", err),
		})
		return
	}
	if !ok {
		c.JSON(http.StatusConflict, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Server '%s' operation not allowed in current state", name),
		})
		return
	}

	go h.runRestartServerTask(name, countdown)

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Server '%s' is restarting", name),
	})
}

func (h *Handler) forceStopServer(c *gin.Context) {
	name := c.Param("name")
	if err := instancepkg.ForceStopServer(name); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Server '%s' has been force stopped", name),
	})
}

func (h *Handler) handleServerUpdate(c *gin.Context) {
	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")
	c.Header("X-Accel-Buffering", "no")

	mgr := updatemanage.GetGlobalManager()

	// H8 fix: Subscribe FIRST to avoid TOCTOU race (missing events between check and subscribe)
	subscriber, unsubscribe := mgr.Subscribe()
	defer unsubscribe()

	// Only start a new update task when none is running; reconnecting clients subscribe only.
	// Start() atomically elects a single winner.
	mgr.Start()

	// Replay history for late subscribers (e.g. page refresh)
	for _, msg := range mgr.GetHistory() {
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
		case <-h.serverCtx.Done():
			return false
		}
	})
}

func (h *Handler) getUpdateStatus(c *gin.Context) {
	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Data: gin.H{
			"running": updatemanage.GetGlobalManager().IsRunning(),
		},
	})
}

func (h *Handler) cancelUpdate(c *gin.Context) {
	if !updatemanage.GetGlobalManager().Cancel() {
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{
			Success: false,
			Error:   "没有正在进行的更新",
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "已发送取消指令",
	})
}

func (h *Handler) streamServerInfo(c *gin.Context) {
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
		case <-h.serverCtx.Done():
			return false
		}
	})
}

func (h *Handler) streamAllInstancesInfo(c *gin.Context) {
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
			instances, err := cfgpkg.GetAvailableInstances()
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
				pid, err := procpkg.GetInstancePID(instanceName)
				if err != nil {
					continue
				}

				exited, err := winproc.IsProcessExited(uint32(pid))
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
		case <-h.serverCtx.Done():
			return false
		}
	})
}

func (h *Handler) runStartServerTask(name string) {
	if err := instancepkg.StartServer(name,
		instancepkg.WithWaitServerCompleted(),
		instancepkg.WithStatePreset(),
	); err != nil {
		logger.GetLogger().Errorf("failed to start server '%s': %v", name, err)
	}
}

func (h *Handler) runStopServerTask(name string, countdown *instancepkg.CountdownConfig) {
	if err := instancepkg.StopServer(name,
		instancepkg.WithStatePreset(),
		instancepkg.WithCountdown(countdown),
	); err != nil {
		logger.GetLogger().Errorf("failed to stop server '%s': %v", name, err)
	}
}

func (h *Handler) runRestartServerTask(name string, countdown *instancepkg.CountdownConfig) {
	if err := instancepkg.RestartServer(name,
		instancepkg.WithStatePreset(),
		instancepkg.WithCountdown(countdown),
		instancepkg.WithRestartStartupCompletion(func(string) {}), // 写 StatusRestarted 状态供 dispatcher 推送
	); err != nil {
		logger.GetLogger().Errorf("failed to restart server '%s': %v", name, err)
	}
}
