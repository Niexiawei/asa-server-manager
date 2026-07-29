package realtime

import (
	"asa-server/logger"
	"asa-server/rconx"
	"context"
	"errors"
	"fmt"
	"net/http"
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

// CloseAuthFailed 是鉴权失效时使用的应用级关闭码（4000-4999 为应用私有区间）。
//
// 前端必须把它和普通断线区别对待：普通断线要退避重连，4401 要**彻底停止**
// 重连并跳转登录页。否则会话过期后每个标签页都会陷入永久重连循环，
// 而每次重连都是一次完整的 TLS 握手。
const CloseAuthFailed = 4401

// AuthGate 由 webapi 在启动时注入（authapi.IsAuthenticated）。
//
// 用函数变量而不是直接 import，是为了不让实时层反向依赖 HTTP 接入层。
// nil 表示未配置鉴权，一律放行。
var AuthGate func(c *gin.Context) bool

// sessionRecheckInterval 是连接建立后复查会话有效性的间隔。
// WebSocket 可以挂好几天，期间会话可能过期或被管理员吊销。
const sessionRecheckInterval = 60 * time.Second

func authorized(c *gin.Context) bool {
	return AuthGate == nil || AuthGate(c)
}

// closeUnauthorized 发送 4401 关闭帧后断开。
// 与"直接 Close"的区别在于前端能据此判断这是鉴权问题、不该重连。
func closeUnauthorized(conn *websocket.Conn, mu *sync.Mutex) {
	mu.Lock()
	_ = conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(CloseAuthFailed, "session expired"),
		time.Now().Add(time.Second))
	mu.Unlock()
	conn.Close()
}

// HandleServerEvents handles WebSocket connections for server events (global broadcast)
func HandleServerEvents(c *gin.Context) {
	// 在 Upgrade **之前**拒绝，绝不"先升级再关闭"。
	// 中间件其实已经拦过一道，这里是纵深防御：防止将来有人把这条路由
	// 挪出中间件的覆盖范围。
	if !authorized(c) {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

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

	// 启动心跳超时 + 会话复查 goroutine
	authTicker := time.NewTicker(sessionRecheckInterval)
	defer authTicker.Stop()
	go func() {
		for {
			select {
			case <-heartbeatTicker.C:
				logger.GetLogger().Warnf("Server events WebSocket: Client heartbeat timeout, closing connection")
				conn.Close()
				globalHub.RemoveClient(conn)
				return
			case <-authTicker.C:
				// 连接期间会话可能过期或被吊销（改密码、管理员踢人）。
				// 用 4401 关闭，前端才知道该跳登录页而不是重连。
				if !authorized(c) {
					logger.GetLogger().Infof("Server events WebSocket: 会话已失效，主动断开")
					closeUnauthorized(conn, connMu)
					globalHub.RemoveClient(conn)
					return
				}
			case <-done:
				// Connection closed normally, exit goroutine
				return
			}
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
			// 读错误后连接的读侧已不可用：gorilla/websocket 一旦 NextReader 出错
			// 就会把连接标记为损坏，再次调用 ReadJSON 会直接 panic
			// ("repeated read on failed websocket connection")。所以这里必须
			// 无条件退出循环，不能尝试写回错误消息后 continue 再读一次
			// （写侧短暂可用不代表读侧健康）。写法与 HandleRCONEvents 保持一致。
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.GetLogger().Warnf("Server events WebSocket: read error: %v", err)
			}
			globalHub.RemoveClient(conn)
			break
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
	// RCON 能直接对游戏服务器下命令，鉴权检查必须在 Upgrade 之前
	if !authorized(c) {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

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

	// 启动心跳超时 + 会话复查 goroutine
	// C10 fix: select on done channel to exit when connection closes
	authTicker := time.NewTicker(sessionRecheckInterval)
	defer authTicker.Stop()
	go func() {
		for {
			select {
			case <-heartbeatTicker.C:
				logger.GetLogger().Warnf("RCON WebSocket: Client heartbeat timeout, closing connection")
				conn.Close()
				return
			case <-authTicker.C:
				if !authorized(c) {
					logger.GetLogger().Infof("RCON WebSocket: 会话已失效，主动断开")
					closeUnauthorized(conn, connMu)
					return
				}
			case <-done:
				// Connection closed normally, exit goroutine
				return
			}
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
