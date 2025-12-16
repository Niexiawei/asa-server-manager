package webapi

import (
	"asa-server/asaserver"
	"asa-server/logger"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorcon/rcon"
	"github.com/gorilla/websocket"
)

// ServerEvent represents a server event message sent via WebSocket
type ServerEvent struct {
	EventType    string `json:"event_type"`
	InstanceName string `json:"instance_name"`
	Timestamp    int64  `json:"timestamp"`
	Message      string `json:"message"`
	Status       string `json:"status,omitempty"`
}

// ClientMessage represents a message from WebSocket client
type ClientMessage struct {
	ClientID string `json:"client_id,omitempty"`
	Type     string `json:"type"`
	Data     string `json:"data,omitempty"`
}

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

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// WebSocket ping/pong 配置
const (
	// pong 响应的等待超时
	pongWait = 10 * time.Second
	// 客户端心跳超时时间（如果客户端在此时间内没有发送任何消息则断开连接）
	heartbeatTimeout = 90 * time.Second
)

// handleServerEvents handles WebSocket connections for server events (global broadcast)
func (s *APIServer) handleServerEvents(c *gin.Context) {
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.GetLogger().Warnf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	// Register client (no instance-based organization)
	s.mu.Lock()
	s.clients[conn] = true
	s.mu.Unlock()

	// 创建心跳超时 ticker
	heartbeatTicker := time.NewTicker(heartbeatTimeout)
	defer heartbeatTicker.Stop()

	// 用于控制心跳检测
	done := make(chan struct{})
	defer close(done)

	// 启动心跳超时检测 goroutine
	go func() {
		<-heartbeatTicker.C
		logger.GetLogger().Warnf("Server events WebSocket: Client heartbeat timeout, closing connection")
		conn.Close()
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
	}()

	// Send initial connection message
	s.sendEventToAll(ServerEvent{
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
			err2 := conn.WriteJSON(gin.H{
				"error": err.Error(),
			})

			if err2 != nil {
				logger.GetLogger().Debugf("Failed to send pong: %v", err)
				s.mu.Lock()
				delete(s.clients, conn)
				s.mu.Unlock()
				break
			}
			continue
		}

		// 重置心跳超时 ticker（收到任何消息）
		heartbeatTicker.Reset(heartbeatTimeout)
		// 处理客户端 ping 消息
		if msg.Type == "ping" {
			// 发送 pong 响应
			err = conn.WriteJSON(gin.H{
				"type": "pong",
			})

			if err != nil {
				logger.GetLogger().Debugf("Failed to send pong: %v", err)
				s.mu.Lock()
				delete(s.clients, conn)
				s.mu.Unlock()
				break
			}
		}
	}
}

// sendEventToAll broadcasts an event to all WebSocket clients (global broadcast)
func (s *APIServer) sendEventToAll(event ServerEvent) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.clients) == 0 {
		return
	}

	// Broadcast to all clients
	for conn := range s.clients {
		conn.WriteJSON(event)
	}
}

// BroadcastServerStarting notifies all WebSocket clients that server is starting
func (s *APIServer) BroadcastServerStarting(instanceName string) {
	s.sendEventToAll(ServerEvent{
		EventType:    "server_starting",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      fmt.Sprintf("Server '%s' is starting", instanceName),
		Status:       "starting",
	})
}

// BroadcastServerStarted notifies all WebSocket clients that server started successfully
func (s *APIServer) BroadcastServerStarted(instanceName string) {
	s.sendEventToAll(ServerEvent{
		EventType:    "server_started",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      fmt.Sprintf("Server '%s' started successfully", instanceName),
		Status:       "started",
	})
}

// BroadcastServerStopping notifies all WebSocket clients that server is stopping
func (s *APIServer) BroadcastServerStopping(instanceName string) {
	s.sendEventToAll(ServerEvent{
		EventType:    "server_stopping",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      fmt.Sprintf("Server '%s' is stopping", instanceName),
		Status:       "stopping",
	})
}

// BroadcastServerStopped notifies all WebSocket clients that server stopped
func (s *APIServer) BroadcastServerStopped(instanceName string) {
	s.sendEventToAll(ServerEvent{
		EventType:    "server_stopped",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      fmt.Sprintf("Server '%s' stopped", instanceName),
		Status:       "stopped",
	})
}

// BroadcastServerStartFailed notifies all WebSocket clients that server start failed
func (s *APIServer) BroadcastServerStartFailed(instanceName string, err error) {
	s.sendEventToAll(ServerEvent{
		EventType:    "server_start_failed",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      fmt.Sprintf("Failed to start server: %v", err),
		Status:       "failed",
	})
}

// BroadcastServerStopFailed notifies all WebSocket clients that server stop failed
func (s *APIServer) BroadcastServerStopFailed(instanceName string, err error) {
	s.sendEventToAll(ServerEvent{
		EventType:    "server_stop_failed",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      fmt.Sprintf("Failed to stop server: %v", err),
		Status:       "failed",
	})
}

// BroadcastServerRestartFailed notifies all WebSocket clients that server restart failed
func (s *APIServer) BroadcastServerRestartFailed(instanceName string, err error) {
	s.sendEventToAll(ServerEvent{
		EventType:    "server_restart_failed",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      fmt.Sprintf("Failed to restart server: %v", err),
		Status:       "failed",
	})
}

func (s *APIServer) BroadcastServerGameLogPath(instanceName string, path string) {
	s.sendEventToAll(ServerEvent{
		EventType:    "server_game_log_path",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      path,
		Status:       "success",
	})
}

// handleRCONEvents handles WebSocket connections for RCON commands
func (s *APIServer) handleRCONEvents(c *gin.Context) {
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.GetLogger().Warnf("WebSocket upgrade error for RCON: %v", err)
		return
	}
	defer conn.Close()

	// 创建心跳超时 ticker
	heartbeatTicker := time.NewTicker(heartbeatTimeout)
	defer heartbeatTicker.Stop()

	// 用于控制心跳检测
	done := make(chan struct{})
	defer close(done)

	// 启动心跳超时检测 goroutine
	go func() {
		<-heartbeatTicker.C
		logger.GetLogger().Warnf("RCON WebSocket: Client heartbeat timeout, closing connection")
		conn.Close()
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
		heartbeatTicker.Reset(heartbeatTimeout)

		// 处理客户端 ping 消息
		if msg.Action == "ping" {
			err = conn.WriteJSON(gin.H{
				"action": "pong",
			})
			if err != nil {
				logger.GetLogger().Debugf("Failed to send pong to RCON: %v", err)
				break
			}
			continue
		}

		// Handle RCON command
		response := s.handleRCONMessage(&msg)
		err = conn.WriteJSON(response)
		if err != nil {
			logger.GetLogger().Warnf("Failed to write RCON response: %v", err)
			break
		}
	}
}

// handleRCONMessage processes a single RCON message
func (s *APIServer) handleRCONMessage(msg *RCONMessage) RCONResponse {
	switch msg.Action {
	case "command":
		return s.rconExecuteCommand(msg.InstanceName, msg.Command)
	default:
		return RCONResponse{
			Success: false,
			Error:   fmt.Sprintf("Unknown action: %s", msg.Action),
		}
	}
}

// rconExecuteCommand executes an RCON command with a temporary connection
func (s *APIServer) rconExecuteCommand(instanceName string, command string) RCONResponse {
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

// ========== Public broadcast functions for external use ==========

// BroadcastServerEvent broadcasts a server event to all connected clients
func BroadcastServerEvent(eventType, instanceName, message, status string) {
	if globalAPIServer == nil {
		return
	}

	globalAPIServer.sendEventToAll(ServerEvent{
		EventType:    eventType,
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      message,
		Status:       status,
	})
}

// BroadcastServerStartingEvent broadcasts server starting event
func BroadcastServerStartingEvent(instanceName string) {
	BroadcastServerEvent("server_starting", instanceName, fmt.Sprintf("Server '%s' is starting", instanceName), "starting")
}

// BroadcastServerStartedEvent broadcasts server started event
func BroadcastServerStartedEvent(instanceName string) {
	BroadcastServerEvent("server_started", instanceName, fmt.Sprintf("Server '%s' started successfully", instanceName), "started")
}

// BroadcastServerStoppingEvent broadcasts server stopping event
func BroadcastServerStoppingEvent(instanceName string) {
	BroadcastServerEvent("server_stopping", instanceName, fmt.Sprintf("Server '%s' is stopping", instanceName), "stopping")
}

// BroadcastServerStoppedEvent broadcasts server stopped event
func BroadcastServerStoppedEvent(instanceName string) {
	BroadcastServerEvent("server_stopped", instanceName, fmt.Sprintf("Server '%s' stopped", instanceName), "stopped")
}
