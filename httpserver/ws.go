package httpserver

import (
	"asa-server/asaserver"
	"asa-server/logger"
	"fmt"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorcon/rcon"
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

	// Validate instance is running
	running, err := asaserver.IsServerRunning(instanceName)
	if err != nil || !running {
		return RCONResponse{
			Success: false,
			Error:   fmt.Sprintf("Server instance '%s' is not running", instanceName),
		}
	}

	// Load instance config
	config, err := asaserver.LoadInstanceConfig(instanceName)
	if err != nil {
		return RCONResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to load config for instance '%s': %v", instanceName, err),
		}
	}

	// Validate RCON password
	if config.ServerAdminPassword == "" {
		return RCONResponse{
			Success: false,
			Error:   fmt.Sprintf("RCON password is empty for instance '%s'. Please set ServerAdminPassword in config", instanceName),
		}
	}

	// Create temporary RCON connection for this command
	rconAddr := fmt.Sprintf("localhost:%d", config.RCONPort)
	logger.GetLogger().Infof("WebSocket RCON: Creating temporary connection to %s for command execution on instance '%s': %s", rconAddr, instanceName, command)

	client, connectErr := rcon.Dial(rconAddr, config.ServerAdminPassword)
	if connectErr != nil {
		logger.GetLogger().Errorf("WebSocket RCON: Failed to create temporary connection to %s: %v", rconAddr, connectErr)
		return RCONResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to connect to RCON server at %s: %v", rconAddr, connectErr),
		}
	}

	logger.GetLogger().Infof("WebSocket RCON: Temporary connection established to instance '%s'", instanceName)

	// Execute command with temporary connection
	response, err := client.Execute(command)
	// Immediately close the temporary connection
	client.Close()
	logger.GetLogger().Infof("WebSocket RCON: Closed temporary connection for instance '%s'", instanceName)

	if err != nil {
		logger.GetLogger().Errorf("WebSocket RCON: Command execution failed: %v", err)
		return RCONResponse{
			Success: false,
			Error:   fmt.Sprintf("RCON command execution failed: %v", err),
		}
	}

	logger.GetLogger().Infof("WebSocket RCON: Response: %s", response)
	return RCONResponse{
		Success:      true,
		Response:     response,
		InstanceName: instanceName,
	}
}
