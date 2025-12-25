/**
 * WebSocket 连接管理器
 * 统一管理所有 WebSocket 连接、心跳、重连等逻辑
 * 现在使用 Web Worker 运行以避免页面休眠时连接断开
 */
import {buildWebSocketUrl} from "@/utils/utils.js";
import { ref, computed } from 'vue';

// ============ Web Worker 管理 ============
let wsWorker = null;
const eventListeners = new Map(); // 存储事件监听器
let isReconnecting = false;
let clientId = null;

// 响应式的连接状态
const wsConnectedRef = ref(false);

// ============ 连接配置 ============
const WS_CONFIG = {
    events: buildWebSocketUrl("/api/ws/events"),
    heartbeatInterval: 5000,      // 心跳间隔 5 秒
    reconnectInterval: 10000,     // 重连间隔 10 秒
    maxReconnectAttempts: null    // 无限重连
}

// 初始化 Web Worker
function initWorker() {
    if (!wsWorker) {
        wsWorker = new Worker(new URL('@/workers/wsWorker.js', import.meta.url));
        
        // 监听 Worker 发送的消息
        wsWorker.onmessage = (event) => {
            const { type, message, clientId: workerClientId, error, connected, reconnecting } = event.data;
            
            switch (type) {
                case 'WS_OPEN':
                    clientId = workerClientId;
                    wsConnectedRef.value = true;
                    break;
                case 'WS_MESSAGE':
                    // 触发所有注册的监听器
                    eventListeners.forEach((callbacks, eventType) => {
                        if (!eventType || message.event_type === eventType) {
                            callbacks.forEach(callback => {
                                try {
                                    callback(message);
                                } catch (err) {
                                    console.error('[WebSocket] Error in event listener:', err);
                                }
                            });
                        }
                    });
                    break;
                case 'WS_ERROR':
                    console.error('[WebSocket Worker] Error:', error);
                    wsConnectedRef.value = false;
                    break;
                case 'WS_CLOSE':
                    wsConnectedRef.value = false;
                    break;
                case 'WS_DISCONNECTED':
                    wsConnectedRef.value = false;
                    clientId = null;
                    break;
                case 'WS_RECONNECT_ATTEMPT':
                    // 重连尝试中
                    break;
                case 'WS_CONNECTION_STATUS':
                    wsConnectedRef.value = connected;
                    clientId = workerClientId;
                    isReconnecting = reconnecting;
                    break;
            }
        };
    }
    
    return wsWorker;
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
    
    // 初始化并获取 Worker
    const worker = initWorker();
    
    // 监听连接状态变化
    const connectionHandler = (event) => {
        const { type, clientId: workerClientId, error } = event.data;
        
        if (type === 'WS_OPEN' && onOpen) {
            onOpen(workerClientId);
        } else if (type === 'WS_ERROR' && onError) {
            onError(error);
        } else if (type === 'WS_CLOSE' && onClose) {
            onClose();
        }
    };
    
    // 添加临时监听器
    worker.addEventListener('message', connectionHandler);
    
    // 发送初始化消息到 Worker
    worker.postMessage({
        type: 'INIT_WS',
        data: {
            wsUrl: wsUrl
        }
    });
    
    // 一定时间后移除临时监听器
    setTimeout(() => {
        worker.removeEventListener('message', connectionHandler);
    }, 5000); // 5秒后移除，避免内存泄漏
}

/**
 * 断开事件 WebSocket 连接
 */
export function disconnectWebSocket() {
    if (wsWorker) {
        wsWorker.postMessage({
            type: 'DISCONNECT'
        });
    }
    
    eventListeners.clear();
    clientId = null;
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
    return wsConnectedRef.value;
}



// 导出响应式连接状态
export const wsConnected = computed(() => wsConnectedRef.value);

/**
 * 发送事件 WebSocket 消息
 */
export function sendWebSocketMessage(message) {
    if (wsWorker && wsConnectedRef.value) {
        wsWorker.postMessage({
            type: 'SEND_MESSAGE',
            data: {
                message: message
            }
        });
        return true;
    }
    return false;
}

// ============ 重连管理 ============

/**
 * 启动自动重连机制
 * 每 10 秒尝试一次重连
 */
export function startReconnect(onReconnectAttempt = null) {
    if (wsWorker) {
        wsWorker.postMessage({
            type: 'START_RECONNECT'
        });
    }
    
    if (onReconnectAttempt) {
        onReconnectAttempt();
    }
}

/**
 * 停止自动重连
 */
export function stopReconnect() {
    if (wsWorker) {
        wsWorker.postMessage({
            type: 'STOP_RECONNECT'
        });
    }
}

// ============ RCON WebSocket 管理 ============
// RCON 相关逻辑已从离取至 store/rconStore.js

// ============ 导出状态检查函数 ============

/**
 * 获取 WebSocket 连接状态对象
 */
export function getWebSocketStatus() {
    if (wsWorker) {
        // 请求 Worker 发送当前连接状态
        wsWorker.postMessage({
            type: 'IS_CONNECTED'
        });
    }
    
    return {
        events: {
            connected: isWebSocketConnected(),
            clientId: clientId,
            reconnecting: isReconnecting
        }
    };
}

/**
 * 重新连接 WebSocket
 */
export async function reconnectWS() {
    if (wsWorker) {
        // 先断开当前连接
        disconnectWebSocket();
        
        return new Promise((resolve, reject) => {
            // 监听连接状态变化
            const messageHandler = (event) => {
                const { type, error } = event.data;
                
                if (type === 'WS_OPEN') {
                    console.log('[WebSocket] Reconnect successful');
                    resolve();
                } else if (type === 'WS_ERROR') {
                    console.error('[WebSocket] Reconnect failed:', error);
                    reject(error);
                } else if (type === 'WS_CLOSE') {
                    reject(new Error('Connection closed'));
                }
            };
            
            wsWorker.addEventListener('message', messageHandler);
            
            // 重新连接
            connectWebSocket(
                () => {
                    // 连接成功
                    wsWorker.removeEventListener('message', messageHandler);
                },
                (error) => {
                    // 连接失败
                    wsWorker.removeEventListener('message', messageHandler);
                    reject(error);
                },
                () => {
                    // 连接关闭
                    wsWorker.removeEventListener('message', messageHandler);
                    reject(new Error('Connection closed'));
                }
            );
        });
    } else {
        // 如果没有 Worker，创建一个
        return new Promise((resolve, reject) => {
            connectWebSocket(
                () => resolve(),
                (error) => reject(error),
                () => reject(new Error('Connection closed'))
            );
        });
    }
}
