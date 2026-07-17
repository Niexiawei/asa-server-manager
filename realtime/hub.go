package realtime

import (
	"asa-server/logger"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ServerEvent represents a server event message sent via WebSocket
type ServerEvent struct {
	EventType    string         `json:"event_type"`
	InstanceName string         `json:"instance_name"`
	Timestamp    int64          `json:"timestamp"`
	Message      string         `json:"message"`
	Status       string         `json:"status,omitempty"`
	Data         map[string]any `json:"data,omitempty"`
}

// ClientMessage represents a message from WebSocket client
type ClientMessage struct {
	ClientID string `json:"client_id,omitempty"`
	Type     string `json:"type"`
	Data     string `json:"data,omitempty"`
}

// WSUpgrader is the WebSocket upgrader for all WS connections
var WSUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// M1 fix: Validate origin to prevent CSRF attacks
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // Allow same-origin and non-browser clients
		}
		// Allow localhost origins for development
		if strings.HasPrefix(origin, "http://localhost") ||
			strings.HasPrefix(origin, "http://127.0.0.1") ||
			strings.HasPrefix(origin, "https://localhost") ||
			strings.HasPrefix(origin, "https://127.0.0.1") {
			return true
		}
		// In production, restrict to same origin
		host := r.Host
		if host == "" {
			return false
		}
		return strings.Contains(origin, host)
	},
}

// WebSocket ping/pong 配置
const (
	// pong 响应的等待超时
	PongWait = 10 * time.Second
	// 客户端心跳超时时间（如果客户端在此时间内没有发送任何消息则断开连接）
	HeartbeatTimeout = 90 * time.Second
)

// wsClientSnapshot holds a snapshot of WebSocket connections and their mutexes for iteration
type wsClientSnapshot struct {
	Conns []*websocket.Conn
	Mus   []*sync.Mutex
}

// wsSnapshotPool pools wsClientSnapshot to avoid repeated allocations during high-frequency broadcasts
var wsSnapshotPool = sync.Pool{
	New: func() any {
		return &wsClientSnapshot{
			Conns: make([]*websocket.Conn, 0, 64),
			Mus:   make([]*sync.Mutex, 0, 64),
		}
	},
}

// Hub manages all WebSocket client connections and broadcasting
type Hub struct {
	mu      sync.RWMutex
	clients map[*websocket.Conn]*sync.Mutex
}

// NewHub creates a new Hub
func NewHub() *Hub {
	return &Hub{
		clients: make(map[*websocket.Conn]*sync.Mutex),
	}
}

// RegisterClient adds a WebSocket client to the hub
func (h *Hub) RegisterClient(conn *websocket.Conn, connMu *sync.Mutex) {
	h.mu.Lock()
	h.clients[conn] = connMu
	h.mu.Unlock()
}

// RemoveClient removes a WebSocket client from the hub
func (h *Hub) RemoveClient(conn *websocket.Conn) {
	h.mu.Lock()
	delete(h.clients, conn)
	h.mu.Unlock()
}

// CloseAllClients 主动关闭所有 WebSocket 连接，使 ReadJSON 返回错误退出
func (h *Hub) CloseAllClients() {
	h.mu.Lock()
	for conn, connMu := range h.clients {
		// H9 fix: Acquire per-connection mutex before closing to prevent
		// concurrent WriteJSON from panicking on a closed connection
		connMu.Lock()
		conn.Close()
		connMu.Unlock()
	}
	h.clients = make(map[*websocket.Conn]*sync.Mutex)
	h.mu.Unlock()
}

// sendEventToAll broadcasts an event to all WebSocket clients (global broadcast)
// It uses a sync.Pool to reuse slice allocations and per-connection mutexes to prevent concurrent writes.
func (h *Hub) sendEventToAll(event ServerEvent) {
	snap := wsSnapshotPool.Get().(*wsClientSnapshot)
	snap.Conns = snap.Conns[:0]
	snap.Mus = snap.Mus[:0]

	h.mu.RLock()
	for conn, mu := range h.clients {
		snap.Conns = append(snap.Conns, conn)
		snap.Mus = append(snap.Mus, mu)
	}
	h.mu.RUnlock()

	for i, conn := range snap.Conns {
		snap.Mus[i].Lock()
		err := conn.WriteJSON(event)
		snap.Mus[i].Unlock()
		if err != nil {
			logger.GetLogger().Debugf("WebSocket write error: %v", err)
		}
	}

	wsSnapshotPool.Put(snap)
}

// ========== Hub instance broadcast methods ==========

// BroadcastServerStartingEvent notifies all WebSocket clients that server is starting
func (h *Hub) BroadcastServerStartingEvent(instanceName string) {
	h.sendEventToAll(ServerEvent{
		EventType:    "server_starting",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      fmt.Sprintf("Server '%s' is starting", instanceName),
		Status:       "starting",
	})
}

// BroadcastServerStartedEvent notifies all WebSocket clients that server started successfully
func (h *Hub) BroadcastServerStartedEvent(instanceName string) {
	h.sendEventToAll(ServerEvent{
		EventType:    "server_started",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      fmt.Sprintf("Server '%s' started successfully", instanceName),
		Status:       "started",
	})
}

// BroadcastServerStoppingEvent notifies all WebSocket clients that server is stopping
func (h *Hub) BroadcastServerStoppingEvent(instanceName string) {
	h.sendEventToAll(ServerEvent{
		EventType:    "server_stopping",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      fmt.Sprintf("Server '%s' is stopping", instanceName),
		Status:       "stopping",
	})
}

// BroadcastServerStoppedEvent notifies all WebSocket clients that server stopped
func (h *Hub) BroadcastServerStoppedEvent(instanceName string) {
	h.sendEventToAll(ServerEvent{
		EventType:    "server_stopped",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      fmt.Sprintf("Server '%s' stopped", instanceName),
		Status:       "stopped",
	})
}

// BroadcastServerStartFailedEvent notifies all WebSocket clients that server start failed
func (h *Hub) BroadcastServerStartFailedEvent(instanceName string, err error) {
	h.sendEventToAll(ServerEvent{
		EventType:    "server_start_failed",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      fmt.Sprintf("Failed to start server: %v", err),
		Status:       "failed",
	})
}

// BroadcastServerStopFailedEvent notifies all WebSocket clients that server stop failed
func (h *Hub) BroadcastServerStopFailedEvent(instanceName string, err error) {
	h.sendEventToAll(ServerEvent{
		EventType:    "server_stop_failed",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      fmt.Sprintf("Failed to stop server: %v", err),
		Status:       "failed",
	})
}

// BroadcastServerRestartFailedEvent notifies all WebSocket clients that server restart failed
func (h *Hub) BroadcastServerRestartFailedEvent(instanceName string, err error) {
	h.sendEventToAll(ServerEvent{
		EventType:    "server_restart_failed",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      fmt.Sprintf("Failed to restart server: %v", err),
		Status:       "failed",
	})
}

// BroadcastServerRestartingEvent 通知客户端服务器正在重启
func (h *Hub) BroadcastServerRestartingEvent(instanceName string) {
	h.sendEventToAll(ServerEvent{
		EventType:    "server_restarting",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      fmt.Sprintf("Server '%s' is restarting", instanceName),
		Status:       "restarting",
	})
}

// BroadcastServerRestartedEvent 通知客户端服务器重启完成
func (h *Hub) BroadcastServerRestartedEvent(instanceName string) {
	h.sendEventToAll(ServerEvent{
		EventType:    "server_restarted",
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      fmt.Sprintf("Server '%s' restarted successfully", instanceName),
		Status:       "restarted",
	})
}

// ========== Package-level broadcast functions (using globalHub) ==========

var globalHub *Hub

// SetGlobalHub registers the global Hub instance
func SetGlobalHub(hub *Hub) {
	globalHub = hub
}

// GetGlobalHub returns the global Hub instance
func GetGlobalHub() *Hub {
	return globalHub
}

// BroadcastServerEvent broadcasts a server event to all connected clients
func BroadcastServerEvent(eventType, instanceName, message, status string) {
	BroadcastServerEventWithData(eventType, instanceName, message, status, nil)
}

// BroadcastServerEventWithData broadcasts a server event with optional structured data
func BroadcastServerEventWithData(eventType, instanceName, message, status string, data map[string]any) {
	if globalHub == nil {
		return
	}

	globalHub.sendEventToAll(ServerEvent{
		EventType:    eventType,
		InstanceName: instanceName,
		Timestamp:    time.Now().Unix(),
		Message:      message,
		Status:       status,
		Data:         data,
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

// BroadcastServerRestartingEvent broadcasts server restarting event
func BroadcastServerRestartingEvent(instanceName string) {
	BroadcastServerEvent("server_restarting", instanceName, fmt.Sprintf("Server '%s' is restarting", instanceName), "restarting")
}

// BroadcastServerRestartedEvent broadcasts server restarted event
func BroadcastServerRestartedEvent(instanceName string) {
	BroadcastServerEvent("server_restarted", instanceName, fmt.Sprintf("Server '%s' restarted successfully", instanceName), "restarted")
}

// BroadcastBatchOperationStarted broadcasts batch operation started event
func BroadcastBatchOperationStarted(opType string, totalInstances int) {
	BroadcastServerEventWithData("batch_started", "",
		fmt.Sprintf("Batch %s started with %d instances", opType, totalInstances), opType,
		map[string]any{
			"type":  opType,
			"total": totalInstances,
		})
}

// BroadcastBatchProgress broadcasts batch operation progress after each instance completes
func BroadcastBatchProgress(opType string, done, total int, instanceName string) {
	BroadcastServerEventWithData("batch_progress", instanceName,
		fmt.Sprintf("Batch %s progress: %d/%d", opType, done, total), opType,
		map[string]any{
			"type":          opType,
			"done":          done,
			"total":         total,
			"instance_name": instanceName,
		})
}

// BroadcastBatchOperationCompleted broadcasts batch operation completed event
func BroadcastBatchOperationCompleted(opType string, succeeded, failed, total int) {
	BroadcastServerEventWithData("batch_completed", "",
		fmt.Sprintf("Batch %s completed: %d succeeded, %d failed of %d", opType, succeeded, failed, total),
		opType,
		map[string]any{
			"type":      opType,
			"succeeded": succeeded,
			"failed":    failed,
			"total":     total,
		})
}

// BroadcastUpdateStarted broadcasts server update started event
func BroadcastUpdateStarted() {
	BroadcastServerEventWithData("update_started", "",
		"Server update started", "running", nil)
}

// BroadcastUpdateCompleted broadcasts server update completed event
func BroadcastUpdateCompleted() {
	BroadcastServerEventWithData("update_completed", "",
		"Server update completed", "completed", nil)
}

// BroadcastUpdateCancelled broadcasts server update cancelled event
func BroadcastUpdateCancelled() {
	BroadcastServerEventWithData("update_cancelled", "",
		"Server update cancelled", "cancelled", nil)
}
