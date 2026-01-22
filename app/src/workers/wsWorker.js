// WebSocket Worker - 在 Worker 中运行 WebSocket 连接以避免页面休眠时被断开
// 与主线程通过 postMessage 通信

let wsConnection = null;
let heartbeatInterval = null;
let reconnectInterval = null;
let isReconnecting = false;
let clientId = null;
let eventListeners = new Map(); // 存储事件监听器
let isIntentionalDisconnect = false; // 标记是否是用户主动断开

// WebSocket 配置
const WS_CONFIG = {
    events: '', // 将从主线程接收此 URL
    heartbeatInterval: 5000,      // 心跳间隔 5 秒
    reconnectInterval: 10000,     // 重连间隔 10 秒
    maxReconnectAttempts: null    // 无限重连
};

// 生成唯一的客户端 ID
function generateClientId() {
    return 'client_' + Math.random().toString(36).substring(2, 15) + '_' + Date.now();
}

// 创建事件消息对象
function createEventMessage(type = 'ping', extraData = {}) {
    return {
        client_id: clientId,
        type,
        ...extraData
    };
}

// 建立 WebSocket 连接
function connectWebSocket(wsUrl) {
    isIntentionalDisconnect = false;
    try {
        wsConnection = new WebSocket(wsUrl);

        wsConnection.onopen = () => {
            console.log('[WebSocket Worker] Connected with client ID:', clientId);
            // 发送初始化消息，包含客户端 ID
            const initMessage = createEventMessage('heartbeat');
            wsConnection.send(JSON.stringify(initMessage));

            // 启动心跳
            startHeartbeat();

            // 通知主线程连接已建立
            postMessage({
                type: 'WS_OPEN',
                clientId: clientId
            });
        };

        wsConnection.onmessage = (event) => {
            try {
                const message = JSON.parse(event.data);
                console.log('[WebSocket Worker] Message received:', message);

                // 通知主线程有新消息
                postMessage({
                    type: 'WS_MESSAGE',
                    message: message
                });
            } catch (err) {
                console.error('[WebSocket Worker] Failed to parse message:', err);
                postMessage({
                    type: 'WS_ERROR',
                    error: err.message
                });
            }
        };

        wsConnection.onerror = (error) => {
            console.error('[WebSocket Worker] Error:', error);
            stopHeartbeat();

            postMessage({
                type: 'WS_ERROR',
                error: error
            });
        };

        wsConnection.onclose = () => {
            console.log('[WebSocket Worker] Closed');
            wsConnection = null;
            stopHeartbeat();

            postMessage({
                type: 'WS_CLOSE'
            });

            // 如果不是主动断开，则尝试重连
            if (!isIntentionalDisconnect) {
                startReconnect();
            }
        };
    } catch (err) {
        console.error('[WebSocket Worker] Failed to connect:', err);
        postMessage({
            type: 'WS_ERROR',
            error: err.message
        });
    }
}

// 断开 WebSocket 连接
function disconnectWebSocket() {
    isIntentionalDisconnect = true;
    stopHeartbeat();
    stopReconnect();
    if (wsConnection) {
        wsConnection.close();
        wsConnection = null;
    }
    eventListeners.clear();
    clientId = null;
    
    postMessage({
        type: 'WS_DISCONNECTED'
    });
}

// 启动心跳机制
function startHeartbeat() {
    // 立即发送第一个 ping
    sendHeartbeat();

    // 清除之前的心跳定时器
    if (heartbeatInterval) {
        clearInterval(heartbeatInterval);
    }

    // 然后每 5 秒发送一次
    heartbeatInterval = setInterval(() => {
        if (isWebSocketConnected()) {
            sendHeartbeat();
        }
    }, WS_CONFIG.heartbeatInterval);

    console.log('[WebSocket Worker Heartbeat] Started (interval: ' + WS_CONFIG.heartbeatInterval + 'ms)');
}

// 发送心跳
function sendHeartbeat() {
    const message = createEventMessage('ping');
    if (sendWebSocketMessage(message)) {
        console.log('[WebSocket Worker Heartbeat] Ping sent');
    }
}

// 停止心跳机制
function stopHeartbeat() {
    if (heartbeatInterval) {
        clearInterval(heartbeatInterval);
        heartbeatInterval = null;
    }
    console.log('[WebSocket Worker Heartbeat] Stopped');
}

// 发送 WebSocket 消息
function sendWebSocketMessage(message) {
    if (wsConnection && wsConnection.readyState === WebSocket.OPEN) {
        wsConnection.send(JSON.stringify(message));
        return true;
    }
    return false;
}

// 检查 WebSocket 连接状态
function isWebSocketConnected() {
    return wsConnection !== null && wsConnection.readyState === WebSocket.OPEN;
}

// 启动自动重连机制
function startReconnect() {
    if (isReconnecting) {
        return;
    }

    isReconnecting = true;
    console.log('[WebSocket Worker Reconnect] Starting auto-reconnect mechanism (interval: ' + WS_CONFIG.reconnectInterval + 'ms)');

    // 清除之前的重连定时器
    if (reconnectInterval) {
        clearInterval(reconnectInterval);
    }

    // 立即尝试一次
    attemptReconnect();

    // 然后每 10 秒尝试一次
    reconnectInterval = setInterval(() => {
        if (!isWebSocketConnected() && isReconnecting) {
            attemptReconnect();
        }
    }, WS_CONFIG.reconnectInterval);
}

// 尝试重新连接
function attemptReconnect() {
    if (isWebSocketConnected()) {
        return;
    }

    console.log('[WebSocket Worker Reconnect] Attempting to reconnect...');
    
    // 通知主线程正在尝试重连
    postMessage({
        type: 'WS_RECONNECT_ATTEMPT'
    });
    
    if (WS_CONFIG.events) {
        connectWebSocket(WS_CONFIG.events);
    }
}

// 停止自动重连
function stopReconnect() {
    if (reconnectInterval) {
        clearInterval(reconnectInterval);
        reconnectInterval = null;
    }
    isReconnecting = false;
    console.log('[WebSocket Worker Reconnect] Stopped');
}

// 处理来自主线程的消息
self.onmessage = function(event) {
    const { type, data } = event.data;

    switch (type) {
        case 'INIT_WS':
            // 初始化 WebSocket 连接
            WS_CONFIG.events = data.wsUrl;
            clientId = generateClientId();
            connectWebSocket(WS_CONFIG.events);
            break;

        case 'SEND_MESSAGE':
            // 发送消息到 WebSocket
            if (isWebSocketConnected()) {
                sendWebSocketMessage(data.message);
            } else {
                postMessage({
                    type: 'WS_ERROR',
                    error: 'WebSocket is not connected'
                });
            }
            break;

        case 'DISCONNECT':
            // 断开连接
            disconnectWebSocket();
            break;

        case 'START_RECONNECT':
            // 启动重连
            startReconnect();
            break;

        case 'STOP_RECONNECT':
            // 停止重连
            stopReconnect();
            break;

        case 'IS_CONNECTED':
            // 检查连接状态
            postMessage({
                type: 'WS_CONNECTION_STATUS',
                connected: isWebSocketConnected(),
                clientId: clientId,
                reconnecting: isReconnecting
            });
            break;
            
        case 'ADD_LISTENER':
            // 添加事件监听器
            const { eventType, listenerId } = data;
            if (!eventListeners.has(eventType)) {
                eventListeners.set(eventType, new Map());
            }
            eventListeners.get(eventType).set(listenerId, data.callback);
            break;
            
        case 'REMOVE_LISTENER':
            // 移除事件监听器
            const { eventType: removeEventType, listenerId: removeListenerId } = data;
            if (eventListeners.has(removeEventType)) {
                eventListeners.get(removeEventType).delete(removeListenerId);
            }
            break;
    }
};