package realtime

import (
	"asa-server/pkg/logger"
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
			logger.Debugf("WebSocket write error: %v", err)
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

// BroadcastSavePlayers 推送某实例的玩家列表（存档解析结果，§3.1 格式）。
// 前端按 event_type == "save_players" 过滤，从 data.players 取数组。
func BroadcastSavePlayers(instanceName string, players []map[string]any) {
	BroadcastServerEventWithData("save_players", instanceName, "", "", map[string]any{
		"players": players,
	})
}

// BroadcastSaveTribes 推送某实例的富化部落列表（存档解析结果，§3.2 格式）。
// 前端按 event_type == "save_tribes" 过滤，从 data.tribes 取数组。
func BroadcastSaveTribes(instanceName string, tribes []map[string]any) {
	BroadcastServerEventWithData("save_tribes", instanceName, "", "", map[string]any{
		"tribes": tribes,
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

// batchActionLabel 把批量操作类型翻成中文动词，供 WS 消息文案使用。
// realtime 不能 import batchmanage（会成环，batchmanage 已经 import 了 realtime），
// 所以这里独立维护一份小映射，而不是复用 batchmanage.batchActionLabel。
func batchActionLabel(opType string) string {
	switch opType {
	case "start":
		return "启动"
	case "stop":
		return "停止"
	case "restart":
		return "重启"
	default:
		return opType
	}
}

// BroadcastBatchOperationStarted broadcasts batch operation started event.
//
// originKind/originLabel 说明这一轮批量是谁发起的（用户手动 / 定时任务 / 恢复启动等）。
// 不能直接传 batchmanage.BatchOrigin 结构体：会形成 import 环，只能拆成两个 string。
func BroadcastBatchOperationStarted(opType string, totalInstances int, originKind, originLabel string) {
	BroadcastServerEventWithData("batch_started", "",
		fmt.Sprintf("批量%s开始，共 %d 个实例", batchActionLabel(opType), totalInstances), opType,
		map[string]any{
			"type":         opType,
			"total":        totalInstances,
			"origin_kind":  originKind,
			"origin_label": originLabel,
		})
}

// BroadcastBatchProgress broadcasts batch operation progress after each instance completes
func BroadcastBatchProgress(opType string, done, total int, instanceName string) {
	BroadcastServerEventWithData("batch_progress", instanceName,
		fmt.Sprintf("批量%s进度：%d/%d", batchActionLabel(opType), done, total), opType,
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
		fmt.Sprintf("批量%s完成：成功 %d，失败 %d，共 %d 个", batchActionLabel(opType), succeeded, failed, total),
		opType,
		map[string]any{
			"type":      opType,
			"succeeded": succeeded,
			"failed":    failed,
			"total":     total,
		})
}

// BroadcastBatchInstanceSkipped broadcasts a user-requested instance skip.
// 该实例此时尚未真正跳过，只是意图已被记录，等待主循环轮到它。
func BroadcastBatchInstanceSkipped(opType, instanceName string) {
	BroadcastServerEventWithData("batch_instance_skipped", instanceName,
		fmt.Sprintf("实例 '%s' 已请求跳过", instanceName), opType,
		map[string]any{
			"type":          opType,
			"instance_name": instanceName,
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

// BroadcastScheduleRun 推送一条定时任务的执行结果。
//
// 前端 ScheduleManager 的执行日志面板据此实时插入新记录，不必轮询。
// data 是 RunRecord 的扁平化字段（含 outcome），由 schedule 包组装——realtime
// 不能反向依赖 schedule（那会成环），所以这里只接受 map。
//
// outcome 是 "success" / "failed" / "cancelled" 三态之一：取消是用户主动叫停，
// 不是故障，必须与失败分开文案，否则执行日志的失败率统计会失真。
func BroadcastScheduleRun(taskName string, outcome string, data map[string]any) {
	var message string
	switch outcome {
	case "cancelled":
		message = fmt.Sprintf("定时任务「%s」已取消", taskName)
	case "success":
		message = fmt.Sprintf("定时任务「%s」执行成功", taskName)
	default:
		message = fmt.Sprintf("定时任务「%s」执行失败", taskName)
	}

	BroadcastServerEventWithData("schedule_run", "", message, outcome, data)
}

// BroadcastScheduleRunStarted 推送一条定时任务**开始执行**的通知。
//
// 前端据此维护「正在运行的任务」列表，用来在任务列表页显示可点击的「取消」按钮——
// 任务可能在页面没开着的时候就开始了，光靠进页面时拉一次 GET /runs 不够及时。
func BroadcastScheduleRunStarted(runID, taskID, taskName, taskType, trigger string) {
	BroadcastServerEventWithData("schedule_run_started", "",
		fmt.Sprintf("定时任务「%s」开始执行", taskName), "running",
		map[string]any{
			"run_id":    runID,
			"task_id":   taskID,
			"task_name": taskName,
			"task_type": taskType,
			"trigger":   trigger,
		})
}

// BroadcastPendingRestore 推送「待恢复现场」的变化。
//
// exists=false 表示现场已被恢复或忽略，前端据此收起提示——多个页面同时开着时，
// 一个人点了确认/忽略，其他人的提示也要跟着消失。
func BroadcastPendingRestore(exists bool, data map[string]any) {
	msg := "有实例在定时更新后未恢复启动"
	if !exists {
		msg = "待恢复实例已处理"
	}
	BroadcastServerEventWithData("pending_restore", "", msg, "warning", data)
}

// 倒计时阶段。前端据此决定显示「x 秒后重启」还是「服务器重启中…」，
// 不要靠 remaining <= 0 自行推断：归零后的实际停止可能还要跑几分钟，
// 只看数字会让 UI 卡在 0 秒上，看起来像卡死。
const (
	CountdownPhaseCounting  = "counting"
	CountdownPhaseExecuting = "executing"
	CountdownPhaseCancelled = "cancelled"
)

// BroadcastCountdownEvent 推送停止/重启倒计时进度。
//
// action 为 "stop" / "restart"；phase 见上面的常量；
// remaining 是剩余秒数，phase != counting 时恒为 0。
func BroadcastCountdownEvent(instanceName, action, phase, message string, remaining int) {
	status := "stopping"
	if action == "restart" {
		status = "restarting"
	}

	if phase != CountdownPhaseCounting {
		remaining = 0
	}

	BroadcastServerEventWithData("countdown", instanceName, message, status, map[string]any{
		"action":    action,
		"phase":     phase,
		"remaining": remaining,
	})
}
