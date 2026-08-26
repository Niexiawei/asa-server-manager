package logapi

import (
	instancepkg "asa-server/internal/instance"
	"asa-server/internal/webapi/apiresp"
	"asa-server/pkg/logger"
	"asa-server/pkg/tail"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	serverCtx context.Context
}

func NewHandler(serverCtx context.Context) *Handler {
	return &Handler{serverCtx: serverCtx}
}

func (h *Handler) RegisterRouter(r *gin.Engine) {
	logs := r.Group("/api/logs")
	{
		logs.GET("/:name", h.streamInstanceLogs)
		logs.GET("/:name/asaapi", h.streamAsaApiLogs)
		logs.GET("", h.streamSystemLogs)
	}
}

func (h *Handler) streamInstanceLogs(c *gin.Context) {
	instanceName := c.Param("name")

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")
	// Get the log file path for the instance (v2: direct path, no polling needed)
	logPath, err := instancepkg.GetGameLogFilePath(instanceName)
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

	// Start tailing the log file, reading the last 200 lines first
	tailer, logChan, err := tail.NewTailer(logPath, 200)
	if err != nil {
		fmt.Fprintf(c.Writer, "data: %s\n\n", "failed to tail log file")
		c.Writer.Flush()
		return
	}

	tailer.Start()
	defer tailer.Stop()

	// Stream new log lines as they arrive
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
		case <-h.serverCtx.Done():
			return false
		}
	})
}

// streamAsaApiLogs streams the AsaApiLoader console output captured from its PTY.
// 该文件在每次以插件模式启动实例时被清空重写，见 instance.startServerInternal。
func (h *Handler) streamAsaApiLogs(c *gin.Context) {
	instanceName := c.Param("name")

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Content-Type")

	// 路径 helper 会创建空文件，实例从未以插件模式启动过时也能挂上 tail
	logPath, err := instancepkg.GetAsaApiLogFilePath(instanceName)
	if err != nil {
		fmt.Fprintf(c.Writer, "data: %s\n\n", "failed to get asaApi log file path")
		c.Writer.Flush()
		return
	}

	// Start tailing the log file, reading the last 200 lines first
	tailer, logChan, err := tail.NewTailer(logPath, 200)
	if err != nil {
		fmt.Fprintf(c.Writer, "data: %s\n\n", "failed to tail asaApi log file")
		c.Writer.Flush()
		return
	}

	tailer.Start()
	defer tailer.Stop()

	// Stream new log lines as they arrive
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
		case <-h.serverCtx.Done():
			return false
		}
	})
}

func (h *Handler) streamSystemLogs(c *gin.Context) {
	// Get the system log file path from logger package
	logPath := logger.GetLogFilePath()

	// Check if log file exists
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("system log file not found: %s", logPath),
		})
		return
	}

	tailer, logChan, err := tail.NewTailer(logPath, 500)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{Success: false, Error: err.Error()})
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
		case <-h.serverCtx.Done():
			return false
		}
	})
}
