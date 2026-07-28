// WebSocket Worker - 在 Worker 中运行 WebSocket 连接以避免页面休眠时被断开
// 与主线程通过 postMessage 通信

let wsConnection = null;
let heartbeatInterval = null;
let reconnectTimer = null;
let isReconnecting = false;
let clientId = null;
let eventListeners = new Map(); // 存储事件监听器
let isIntentionalDisconnect = false; // 标记是否是用户主动断开
let authFailed = false;          // 鉴权失败后不再重连，直到主线程明确要求
let reconnectAttempt = 0;        // 退避指数

// 服务端在会话失效时发送的应用级关闭码（4000-4999 为应用私有区间）。
// 必须和普通断线区别对待：普通断线要重连，这个要彻底停下来去登录。
const CLOSE_AUTH_FAILED = 4401;

// WebSocket 配置
const WS_CONFIG = {
    events: '', // 将从主线程接收此 URL
    heartbeatInterval: 5000,      // 心跳间隔 5 秒
    // 重连采用指数退避 + 全抖动，而不是固定间隔。
    //
    // 固定间隔的问题不是"太快"，而是**相位锁定**：所有客户端的会话在同一
    // 时刻过期，于是同时掉线、同时按同一节奏重连，此后永远保持同步，
    // 每隔 N 秒就对服务端来一次并发脉冲。而每次重连都是一次完整的 TLS 握手。
    // 全抖动（在 [0, cap) 上均匀取值）能把这个尖峰彻底打散。
    reconnectBaseDelay: 1000,
    reconnectMaxDelay: 30000
};

// 计算下一次重连延迟：min(max, base * 2^n) 之内均匀随机
function nextReconnectDelay() {
    const cap = Math.min(WS_CONFIG.reconnectMaxDelay, WS_CONFIG.reconnectBaseDelay * Math.pow(2, reconnectAttempt));
    reconnectAttempt++;
    return Math.random() * cap;
}

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

// 摘除 socket 的所有回调，丢弃旧连接前调用，防止其异步事件（尤其 onclose）
// 干扰新连接或触发多余重连
function detachSocket(ws) {
    if (!ws) return;
    ws.onopen = null;
    ws.onmessage = null;
    ws.onerror = null;
    ws.onclose = null;
}

// 建立 WebSocket 连接
function connectWebSocket(wsUrl) {
    // 防重入守卫：如果已有活跃连接或正在连接中，跳过
    if (wsConnection && (wsConnection.readyState === WebSocket.OPEN || wsConnection.readyState === WebSocket.CONNECTING)) {
        console.log('[WebSocket Worker] Already connected/connecting, skip duplicate connect');
        return;
    }

    // 清理可能残留的旧连接（CLOSED/CLOSING 状态）：先摘回调再关闭，
    // 避免其异步 onclose 在新连接建立后清空引用/触发重连
    if (wsConnection) {
        detachSocket(wsConnection);
        try { wsConnection.close(); } catch (e) { /* ignore */ }
        wsConnection = null;
    }

    isIntentionalDisconnect = false;
    try {
        // 使用局部引用 ws，所有回调用 `ws !== wsConnection` 做身份守卫：
        // 非当前连接（僵尸连接）触发的事件一律忽略，从根源杜绝重复投递与旧 close 干扰
        const ws = new WebSocket(wsUrl);
        wsConnection = ws;

        ws.onopen = () => {
            if (ws !== wsConnection) return;
            console.log('[WebSocket Worker] Connected with client ID:', clientId);
            // 连接成功必须重置退避指数，否则下次断线会直接从上次的最大延迟起步
            reconnectAttempt = 0;
            stopReconnect();
            // 发送初始化消息，包含客户端 ID
            const initMessage = createEventMessage('heartbeat');
            ws.send(JSON.stringify(initMessage));

            // 启动心跳
            startHeartbeat();

            // 通知主线程连接已建立
            postMessage({
                type: 'WS_OPEN',
                clientId: clientId
            });
        };

        ws.onmessage = (event) => {
            if (ws !== wsConnection) return; // 僵尸连接的消息直接丢弃
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

        ws.onerror = (error) => {
            if (ws !== wsConnection) return;
            console.error('[WebSocket Worker] Error:', error);
            stopHeartbeat();

            postMessage({
                type: 'WS_ERROR',
                error: error
            });
        };

        ws.onclose = (event) => {
            if (ws !== wsConnection) return; // 非当前连接的 close 不清引用、不重连
            console.log('[WebSocket Worker] Closed, code =', event && event.code);
            wsConnection = null;
            stopHeartbeat();

            // 鉴权失败是**致命**的，不是网络抖动：再怎么重连也不会成功。
            // 把它当成可重试错误，就会变成每个标签页一个永久热循环，
            // 而且每次尝试都是一次完整的 TLS 握手 + 一条服务端错误日志。
            if (event && event.code === CLOSE_AUTH_FAILED) {
                authFailed = true;
                stopReconnect();
                postMessage({ type: 'WS_AUTH_FAILED' });
                return;
            }

            postMessage({
                type: 'WS_CLOSE'
            });

            // 如果不是主动断开、也不是鉴权问题，才退避重连
            if (!isIntentionalDisconnect && !authFailed) {
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
        detachSocket(wsConnection);
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

// 启动自动重连机制（指数退避 + 全抖动）
function startReconnect() {
    if (isReconnecting) {
        return;
    }
    // 鉴权失败后不重连：得先让用户去登录。登录成功后主线程会发 RECONNECT。
    if (authFailed) {
        console.log('[WebSocket Worker Reconnect] Skipped: 需要重新登录');
        return;
    }

    isReconnecting = true;
    scheduleReconnect();
}

// 用递归 setTimeout 而不是 setInterval：
// setInterval 在单次连接尝试耗时超过间隔时会**堆积**回调，
// 网络差的时候反而制造出更多并发连接。
function scheduleReconnect() {
    clearTimeout(reconnectTimer);
    const delay = nextReconnectDelay();
    console.log('[WebSocket Worker Reconnect] 第 ' + reconnectAttempt + ' 次尝试将在 ' + Math.round(delay) + 'ms 后进行');
    reconnectTimer = setTimeout(() => {
        if (!isReconnecting || authFailed) return;
        if (!isWebSocketConnected()) {
            attemptReconnect();
            // 这一次没连上的话，下一轮延迟会更长；连上了 onopen 会 stopReconnect
            if (isReconnecting) {
                scheduleReconnect();
            }
        }
    }, delay);
}

// 尝试重新连接
function attemptReconnect() {
    // 增加 CONNECTING 状态检查，避免重复连接
    if (wsConnection && (wsConnection.readyState === WebSocket.OPEN || wsConnection.readyState === WebSocket.CONNECTING)) {
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
    if (reconnectTimer) {
        clearTimeout(reconnectTimer);
        reconnectTimer = null;
    }
    isReconnecting = false;
}

// 处理来自主线程的消息
self.onmessage = function(event) {
    const { type, data } = event.data;

    switch (type) {
        case 'INIT_WS':
            // 幂等守卫：如果已有活跃连接或正在重连，忽略重复初始化
            if (wsConnection && (wsConnection.readyState === WebSocket.OPEN || wsConnection.readyState === WebSocket.CONNECTING)) {
                console.log('[WebSocket Worker] INIT_WS ignored: already connected/connecting');
                postMessage({ type: 'WS_OPEN', clientId: clientId });
                break;
            }
            if (isReconnecting) {
                console.log('[WebSocket Worker] INIT_WS ignored: reconnecting in progress');
                break;
            }
            if (authFailed) {
                // 会话已失效，连了也是白连。等主线程登录成功后发 RECONNECT。
                console.log('[WebSocket Worker] INIT_WS ignored: 需要重新登录');
                postMessage({ type: 'WS_AUTH_FAILED' });
                break;
            }
            // 首次初始化：正常流程
            WS_CONFIG.events = data.wsUrl;
            clientId = generateClientId();
            connectWebSocket(WS_CONFIG.events);
            break;

        case 'RECONNECT':
            // 手动重连：停止现有重连 + 清除旧状态 + 新连接。
            // 登录成功后主线程会发这条消息，它是解除 authFailed 的唯一途径。
            stopReconnect();
            stopHeartbeat();
            if (wsConnection) {
                detachSocket(wsConnection);
                try { wsConnection.close(); } catch (e) { /* ignore */ }
                wsConnection = null;
            }
            WS_CONFIG.events = data.wsUrl;
            clientId = generateClientId();
            isIntentionalDisconnect = false;
            authFailed = false;
            reconnectAttempt = 0;
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
                reconnecting: isReconnecting,
                authFailed: authFailed
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