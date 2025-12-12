import {buildWebSocketUrl} from "@/utils/utils.js"

// ============ RCON WebSocket 连接状态 ============
let rconWsConnection = null
const rconMessageCallbacks = new Map()
let rconHeartbeatInterval = null
let rconReconnectInterval = null
let isRCONReconnecting = false
let isRCONReConnect = false

// ============ RCON 连接回调管理 ============
let rconConnectionCallbacks = {
    onOpen: null,
    onError: null,
    onClose: null
}

// ============ 连接配置 ============
const RCON_CONFIG = {
    url: buildWebSocketUrl("/api/ws/rcon"),
    heartbeatInterval: 5000,      // 心跳间隔 5 秒
    reconnectInterval: 10000,     // 重连间隔 10 秒
    maxReconnectAttempts: null    // 无限重连
}

// ============ 工具函数 ============

/**
 * 创建 RCON 消息对象
 */
function createRCONMessage(action, instanceName = null, command = null) {
    const message = {action}
    if (instanceName) message.instance_name = instanceName
    if (command) message.command = command
    return message
}

// ============ RCON WebSocket 管理 ============

/**
 * 建立 RCON WebSocket 连接
 * @param {Function} onOpen - 连接成功的回调
 * @param {Function} onError - 连接失败的回调
 * @param {Function} onClose - 连接关闭的回调
 */
export function connectRCONWebSocket(onOpen, onError, onClose) {
    // 保存回调函数供重连使用
    rconConnectionCallbacks = { onOpen, onError, onClose }
    
    const wsUrl = RCON_CONFIG.url
    isRCONReConnect = true
    try {
        rconWsConnection = new WebSocket(wsUrl)

        rconWsConnection.onopen = () => {
            console.log('[RCON WebSocket] Connected')
            // 启动 RCON 心跳
            startRCONHeartbeat()
            // 停止重连
            stopRCONReconnect()
            if (rconConnectionCallbacks.onOpen) rconConnectionCallbacks.onOpen()
        }

        rconWsConnection.onmessage = (event) => {
            try {
                const message = JSON.parse(event.data)
                console.log('[RCON WebSocket] Message received:', message)

                // 触发对应的回调函数
                if (rconMessageCallbacks.has('message')) {
                    rconMessageCallbacks.get('message').forEach(callback => {
                        try {
                            callback(message)
                        } catch (err) {
                            console.error('[RCON WebSocket] Error in message callback:', err)
                        }
                    })
                }
            } catch (err) {
                console.error('[RCON WebSocket] Failed to parse message:', err)
            }
        }

        rconWsConnection.onerror = (error) => {
            console.error('[RCON WebSocket] Error:', error)
            stopRCONHeartbeat()
            if (rconConnectionCallbacks.onError) rconConnectionCallbacks.onError(error)
            // 自动启动重连
            startRCONReconnect()
        }

        rconWsConnection.onclose = () => {
            console.log('[RCON WebSocket] Closed')
            rconWsConnection = null
            stopRCONHeartbeat()
            if (rconConnectionCallbacks.onClose) rconConnectionCallbacks.onClose()
            // 自动启动重连
            startRCONReconnect()
        }
    } catch (err) {
        console.error('[RCON WebSocket] Failed to connect:', err)
        if (rconConnectionCallbacks.onError) rconConnectionCallbacks.onError(err)
        // 自动启动重连
        startRCONReconnect()
    }
}

/**
 * 断开 RCON WebSocket 连接
 */
export function disconnectRCONWebSocket() {
    isRCONReConnect = false
    stopRCONHeartbeat()
    stopRCONReconnect()
    if (rconWsConnection) {
        rconWsConnection.close()
        rconWsConnection = null
    }
    rconMessageCallbacks.clear()
}

/**
 * 发送 RCON 命令
 */
export function sendRCONCommandViaWebSocket(action, instanceName = null, command = null) {
    if (rconWsConnection && rconWsConnection.readyState === WebSocket.OPEN) {
        const message = createRCONMessage(action, instanceName, command)
        rconWsConnection.send(JSON.stringify(message))
        return true
    }
    return false
}

/**
 * 监听 RCON 消息
 */
export function onRCONMessage(callback) {
    if (!rconMessageCallbacks.has('message')) {
        rconMessageCallbacks.set('message', [])
    }
    rconMessageCallbacks.get('message').push(callback)

    // 返回取消监听函数
    return () => {
        const callbacks = rconMessageCallbacks.get('message')
        if (callbacks) {
            const index = callbacks.indexOf(callback)
            if (index > -1) {
                callbacks.splice(index, 1)
            }
        }
    }
}

/**
 * 获取 RCON WebSocket 连接状态
 */
export function isRCONWebSocketConnected() {
    return rconWsConnection !== null && rconWsConnection.readyState === WebSocket.OPEN
}

// ============ RCON 心跳管理 ============

/**
 * 启动 RCON 心跳
 * 每 5 秒发送一次 ping 消息
 */
function startRCONHeartbeat() {
    // 立即发送第一个 ping
    sendRCONHeartbeat()

    // 清除之前的心跳定时器
    if (rconHeartbeatInterval) {
        clearInterval(rconHeartbeatInterval)
    }

    // 然后每 5 秒发送一次
    rconHeartbeatInterval = setInterval(() => {
        if (isRCONWebSocketConnected()) {
            sendRCONHeartbeat()
        }
    }, RCON_CONFIG.heartbeatInterval)

    console.log('[RCON Heartbeat] Started (interval: ' + RCON_CONFIG.heartbeatInterval + 'ms)')
}

/**
 * 发送 RCON 心跳
 */
function sendRCONHeartbeat() {
    if (sendRCONCommandViaWebSocket('ping')) {
        console.log('[RCON Heartbeat] Ping sent')
    }
}

/**
 * 停止 RCON 心跳
 */
function stopRCONHeartbeat() {
    if (rconHeartbeatInterval) {
        clearInterval(rconHeartbeatInterval)
        rconHeartbeatInterval = null
    }
    console.log('[RCON Heartbeat] Stopped')
}

// ============ RCON 重连管理 ============

/**
 * 启动 RCON 自动重连机制 (内部方法)
 * 每 10 秒尝试一次重连，自动调用 connectRCONWebSocket 重新建立连接
 */
function startRCONReconnect() {
    if (isRCONReconnecting || !isRCONReConnect) {
        return
    }

    isRCONReconnecting = true
    console.log('[RCON Reconnect] Starting auto-reconnect mechanism (interval: ' + RCON_CONFIG.reconnectInterval + 'ms)')

    // 清除之前的重连定时器
    if (rconReconnectInterval) {
        clearInterval(rconReconnectInterval)
    }

    // 立即尝试一次
    attemptRCONReconnect()

    // 然后每 10 秒尝试一次
    rconReconnectInterval = setInterval(() => {
        if (!isRCONWebSocketConnected() && isRCONReconnecting) {
            attemptRCONReconnect()
        }
    }, RCON_CONFIG.reconnectInterval)
}

/**
 * 尝试重新连接 RCON (内部方法)
 */
function attemptRCONReconnect() {
    if (isRCONWebSocketConnected()) {
        return
    }

    console.log('[RCON Reconnect] Attempting to reconnect...')
    
    // 使用保存的回调函数重新建立连接
    if (rconConnectionCallbacks.onOpen || rconConnectionCallbacks.onError || rconConnectionCallbacks.onClose) {
        connectRCONWebSocket(
            rconConnectionCallbacks.onOpen,
            rconConnectionCallbacks.onError,
            rconConnectionCallbacks.onClose
        )
    }
}

/**
 * 停止 RCON 自动重连 (内部方法)
 */
function stopRCONReconnect() {
    if (rconReconnectInterval) {
        clearInterval(rconReconnectInterval)
        rconReconnectInterval = null
    }
    isRCONReconnecting = false
    console.log('[RCON Reconnect] Stopped')
}
