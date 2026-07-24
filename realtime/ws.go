package realtime

import (
	"asa-server/logger"
	"asa-server/rconx"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// RCONMessage represents RCON-related messages
type RCONMessage struct {
	Action       string `json:"action"` // "connect", "disconnect", "command"
	InstanceName string `json:"instance_name,omitempty"`
	Command      string `json:"command,omitempty"`
}

// RCONResponse represents response from RCON operations
type RCONResponse struct {
	Success      bool   `json:"success"`
	Message      string `json:"message,omitempty"`
	Response     string `json:"response,omitempty"`
	Error        string `json:"error,omitempty"`
	InstanceName string `json:"instance_name,omitempty"`
}

// HandleServerEvents handles WebSocket connections for server events (global broadcast)
func HandleServerEvents(c *gin.Context) {
	conn, err := WSUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.GetLogger().Warnf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	// Register client
	connMu := &sync.Mutex{}
	globalHub.RegisterClient(conn, connMu)

	// 创建心跳超时 ticker
	heartbeatTicker := time.NewTicker(HeartbeatTimeout)
	defer heartbeatTicker.Stop()

	// 用于控制心跳检测
	done := make(chan struct{})
	defer close(done)

	// 启动心跳超时检测 goroutine
	go func() {
		select {
		case <-heartbeatTicker.C:
			logger.GetLogger().Warnf("Server events WebSocket: Client heartbeat timeout, closing connection")
			conn.Close()
			globalHub.RemoveClient(conn)
		case <-done:
			// Connection closed normally, exit goroutine
		}
	}()

	// Send initial connection message
	globalHub.sendEventToAll(ServerEvent{
		EventType:    "connected",
		InstanceName: "",
		Timestamp:    time.Now().Unix(),
		Message:      "WebSocket connected to server events",
	})

	// Keep connection open and listen for client messages
	for {
		var msg ClientMessage
		err := conn.ReadJSON(&msg)
		if err != nil {
			connMu.Lock()
			err2 := conn.WriteJSON(gin.H{
				"error": err.Error(),
			})
			connMu.Unlock()

			if err2 != nil {
				logger.GetLogger().Debugf("Failed to send error response: %v", err)
				globalHub.RemoveClient(conn)
				break
			}
			continue
		}

		// 重置心跳超时 ticker（收到任何消息）
		heartbeatTicker.Reset(HeartbeatTimeout)
		// 处理客户端 ping 消息
		if msg.Type == "ping" {
			// 发送 pong 响应
			connMu.Lock()
			err = conn.WriteJSON(gin.H{
				"type": "pong",
			})
			connMu.Unlock()

			if err != nil {
				logger.GetLogger().Debugf("Failed to send pong: %v", err)
				globalHub.RemoveClient(conn)
				break
			}
		}
	}
}

// HandleRCONEvents handles WebSocket connections for RCON commands
func HandleRCONEvents(c *gin.Context) {
	conn, err := WSUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.GetLogger().Warnf("WebSocket upgrade error for RCON: %v", err)
		return
	}
	defer conn.Close()

	// L4 fix: Register RCON connection to hub for CloseAllClients
	connMu := &sync.Mutex{}
	globalHub.RegisterClient(conn, connMu)
	defer globalHub.RemoveClient(conn)

	// 创建心跳超时 ticker
	heartbeatTicker := time.NewTicker(HeartbeatTimeout)
	defer heartbeatTicker.Stop()

	// 用于控制心跳检测
	done := make(chan struct{})
	defer close(done)

	// 启动心跳超时检测 goroutine
	// C10 fix: select on done channel to exit when connection closes
	go func() {
		select {
		case <-heartbeatTicker.C:
			logger.GetLogger().Warnf("RCON WebSocket: Client heartbeat timeout, closing connection")
			conn.Close()
		case <-done:
			// Connection closed normally, exit goroutine
		}
	}()

	// Keep connection open and listen for RCON commands
	for {
		var msg RCONMessage
		err := conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.GetLogger().Warnf("WebSocket error: %v", err)
			}
			break
		}

		// 重置心跳超时 ticker（收到任何消息）
		heartbeatTicker.Reset(HeartbeatTimeout)

		// 处理客户端 ping 消息
		if msg.Action == "ping" {
			connMu.Lock()
			err = conn.WriteJSON(gin.H{
				"action": "pong",
			})
			connMu.Unlock()
			if err != nil {
				logger.GetLogger().Debugf("Failed to send pong to RCON: %v", err)
				break
			}
			continue
		}

		// Handle RCON command
		response := handleRCONMessage(&msg)
		connMu.Lock()
		err = conn.WriteJSON(response)
		connMu.Unlock()
		if err != nil {
			logger.GetLogger().Warnf("Failed to write RCON response: %v", err)
			break
		}
	}
}

// handleRCONMessage processes a single RCON message
func handleRCONMessage(msg *RCONMessage) RCONResponse {
	switch msg.Action {
	case "command":
		return rconExecuteCommand(msg.InstanceName, msg.Command)
	default:
		return RCONResponse{
			Success: false,
			Error:   fmt.Sprintf("Unknown action: %s", msg.Action),
		}
	}
}

// rconExecuteCommand executes an RCON command with a temporary connection
func rconExecuteCommand(instanceName string, command string) RCONResponse {
	if command == "" {
		return RCONResponse{
			Success: false,
			Error:   "Command cannot be empty",
		}
	}

	if instanceName == "" {
		return RCONResponse{
			Success: false,
			Error:   "No RCON instance selected. Please connect first.",
		}
	}

	logger.GetLogger().Infof("WebSocket RCON: Executing command on instance '%s': %s", instanceName, command)

	// 交互式面板不重试：用户就坐在屏幕前，连不上要立刻回话，
	// 而不是让输入框卡住 4 秒（rconx 默认的 3 次尝试 × 2s 间隔）再报错。
	response, err := rconx.Execute(context.Background(), instanceName, command, rconx.WithAttempts(1))
	if err != nil {
		logger.GetLogger().Errorf("WebSocket RCON: Command failed on instance '%s': %v", instanceName, err)
		return RCONResponse{
			Success: false,
			Error:   rconErrorMessage(instanceName, err),
		}
	}

	logger.GetLogger().Infof("WebSocket RCON: Response: %s", response)
	return RCONResponse{
		Success:      true,
		Response:     response,
		InstanceName: instanceName,
	}
}

// rconErrorMessage 把 rconx 的错误翻成面板上直接可读的一句话。
// 保留拆分前的措辞，前端与用户的既有认知不受影响。
func rconErrorMessage(instanceName string, err error) string {
	switch {
	case errors.Is(err, rconx.ErrNotRunning):
		return fmt.Sprintf("Server instance '%s' is not running", instanceName)
	case errors.Is(err, rconx.ErrPasswordEmpty):
		return fmt.Sprintf("RCON password is empty for instance '%s'. Please set ServerAdminPassword in config", instanceName)
	default:
		return err.Error()
	}
}
