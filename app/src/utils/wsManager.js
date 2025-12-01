/**
 * WebSocket 连接管理器
 * 统一管理所有 WebSocket 连接、心跳、重连等逻辑
 */
import {buildWebSocketUrl} from "@/utils/utils.js";

// ============ 事件服务 WebSocket 连接 ============
let wsConnection = null
const eventListeners = new Map()
let heartbeatInterval = null
let reconnectInterval = null
let isReconnecting = false
let clientId = null

// ============ 连接配置 ============
const WS_CONFIG = {
    events: buildWebSocketUrl("/api/ws/events"),
    heartbeatInterval: 5000,      // 心跳间隔 5 秒
    reconnectInterval: 10000,     // 重连间隔 10 秒
    maxReconnectAttempts: null    // 无限重连
}

// ============ 工具函数 ============

/**
 * 生成唯一的客户端 ID
 */
function generateClientId() {
    return 'client_' + Math.random().toString(36).substring(2, 15) + '_' + Date.now()
}

/**
 * 创建事件消息对象
 */
function createEventMessage(type = 'ping', extraData = {}) {
    return {
        client_id: clientId,
        type,
        ...extraData
    }
}


// ============ 事件 WebSocket 管理 ============

/**
 * 建立事件 WebSocket 连接
 */
export function connectWebSocket(onOpen, onError, onClose) {
    const wsUrl = WS_CONFIG.events
    clientId = generateClientId()

    try {
        wsConnection = new WebSocket(wsUrl)

        wsConnection.onopen = () => {
            console.log('[WebSocket] Connected with client ID:', clientId)
            // 发送初始化消息，包含客户端 ID
            const initMessage = createEventMessage('heartbeat')
            wsConnection.send(JSON.stringify(initMessage))

            // 启动心跳
            startHeartbeat()

            if (onOpen) onOpen(clientId)
        }

        wsConnection.onmessage = (event) => {
            try {
                const message = JSON.parse(event.data)
                console.log('[WebSocket] Message received:', message)

                // 触发所有注册的监听器
                eventListeners.forEach((callbacks, eventType) => {
                    if (!eventType || message.event_type === eventType) {
                        callbacks.forEach(callback => {
                            try {
                                callback(message)
                            } catch (err) {
                                console.error('[WebSocket] Error in event listener:', err)
                            }
                        })
                    }
                })
            } catch (err) {
                console.error('[WebSocket] Failed to parse message:', err)
            }
        }

        wsConnection.onerror = (error) => {
            console.error('[WebSocket] Error:', error)
            stopHeartbeat()
            if (onError) onError(error)
        }

        wsConnection.onclose = () => {
            console.log('[WebSocket] Closed')
            wsConnection = null
            stopHeartbeat()
            if (onClose) onClose()
        }
    } catch (err) {
        console.error('[WebSocket] Failed to connect:', err)
        if (onError) onError(err)
    }
}

/**
 * 断开事件 WebSocket 连接
 */
export function disconnectWebSocket() {
    stopHeartbeat()
    stopReconnect()
    if (wsConnection) {
        wsConnection.close()
        wsConnection = null
    }
    eventListeners.clear()
    clientId = null
}

/**
 * 监听特定事件类型
 */
export function onServerEvent(eventType, callback) {
    if (!eventListeners.has(eventType)) {
        eventListeners.set(eventType, [])
    }
    eventListeners.get(eventType).push(callback)

    // 返回取消监听函数
    return () => {
        const callbacks = eventListeners.get(eventType)
        if (callbacks) {
            const index = callbacks.indexOf(callback)
            if (index > -1) {
                callbacks.splice(index, 1)
            }
        }
    }
}

/**
 * 监听所有服务器事件
 */
export function onAnyServerEvent(callback) {
    return onServerEvent(null, callback)
}

/**
 * 获取事件 WebSocket 连接状态
 */
export function isWebSocketConnected() {
    return wsConnection !== null && wsConnection.readyState === WebSocket.OPEN
}

/**
 * 发送事件 WebSocket 消息
 */
export function sendWebSocketMessage(message) {
    if (wsConnection && wsConnection.readyState === WebSocket.OPEN) {
        wsConnection.send(JSON.stringify(message))
        return true
    }
    return false
}

// ============ 心跳管理 ============

/**
 * 启动心跳机制
 * 每 5 秒发送一次 ping 消息
 */
function startHeartbeat() {
    // 立即发送第一个 ping
    sendHeartbeat()

    // 清除之前的心跳定时器
    if (heartbeatInterval) {
        clearInterval(heartbeatInterval)
    }

    // 然后每 5 秒发送一次
    heartbeatInterval = setInterval(() => {
        if (isWebSocketConnected()) {
            sendHeartbeat()
        }
    }, WS_CONFIG.heartbeatInterval)

    console.log('[Heartbeat] Started (interval: ' + WS_CONFIG.heartbeatInterval + 'ms)')
}

/**
 * 发送心跳
 */
function sendHeartbeat() {
    const message = createEventMessage('ping')
    if (sendWebSocketMessage(message)) {
        console.log('[Heartbeat] Ping sent')
    }
}

/**
 * 停止心跳机制
 */
function stopHeartbeat() {
    if (heartbeatInterval) {
        clearInterval(heartbeatInterval)
        heartbeatInterval = null
    }
    console.log('[Heartbeat] Stopped')
}

// ============ 重连管理 ============

/**
 * 启动自动重连机制
 * 每 10 秒尝试一次重连
 */
export function startReconnect(onReconnectAttempt = null) {
    if (isReconnecting) {
        return
    }

    isReconnecting = true
    console.log('[Reconnect] Starting auto-reconnect mechanism (interval: ' + WS_CONFIG.reconnectInterval + 'ms)')

    // 清除之前的重连定时器
    if (reconnectInterval) {
        clearInterval(reconnectInterval)
    }

    // 立即尝试一次
    attemptReconnect(onReconnectAttempt)

    // 然后每 10 秒尝试一次
    reconnectInterval = setInterval(() => {
        if (!isWebSocketConnected() && isReconnecting) {
            attemptReconnect(onReconnectAttempt)
        }
    }, WS_CONFIG.reconnectInterval)
}

/**
 * 尝试重新连接
 */
function attemptReconnect(onReconnectAttempt) {
    if (isWebSocketConnected()) {
        return
    }

    console.log('[Reconnect] Attempting to reconnect...')

    if (onReconnectAttempt) {
        onReconnectAttempt()
    }
}

/**
 * 停止自动重连
 */
export function stopReconnect() {
    if (reconnectInterval) {
        clearInterval(reconnectInterval)
        reconnectInterval = null
    }
    isReconnecting = false
    console.log('[Reconnect] Stopped')
}

// ============ RCON WebSocket 管理 ============
// RCON 相关逻辑已从离取至 store/rconStore.js

// ============ 导出状态检查函数 ============

/**
 * 获取 WebSocket 连接状态对象
 */
export function getWebSocketStatus() {
    return {
        events: {
            connected: isWebSocketConnected(),
            clientId: clientId,
            reconnecting: isReconnecting
        }
    }
}
