package webapi

import (
	"asa-server/logger"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
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
	ClientID string `json:"client_id"`
	Type     string `json:"type"`
	Data     string `json:"data,omitempty"`
}

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

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
			// Unregister client on disconnect
			s.mu.Lock()
			delete(s.clients, conn)
			s.mu.Unlock()
			break
		}
		// Handle client messages if needed (heartbeat, etc.)
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
