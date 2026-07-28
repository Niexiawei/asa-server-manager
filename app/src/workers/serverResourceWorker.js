// Server Resource Monitor Web Worker
// Handles SSE connections for server-level resource data

// Configuration
const CHANGE_THRESHOLD = 0.5; // Only update when change exceeds this percentage
// SSE 重连采用指数退避 + 全抖动，而不是固定间隔。
//
// 固定间隔的问题不是"太快"，而是**相位锁定**：会话过期时所有客户端同时掉线、
// 同时按同一节奏重连，此后永远保持同步，每隔 N 秒给服务端来一次并发脉冲。
// 全抖动（在 [0, cap) 上均匀取值）能把这个尖峰打散。
const RECONNECT_BASE_DELAY = 1000;
const RECONNECT_MAX_DELAY = 30000;
let reconnectAttempt = 0;
// 鉴权失效后停止重连，等主线程登录成功再说。EventSource 拿不到 HTTP 状态码，
// 所以由主线程去问 /api/auth/state 来判定，判定结果通过 STOP/RESUME 消息回来。
let sseAuthBlocked = false;

function nextReconnectDelay() {
  const cap = Math.min(RECONNECT_MAX_DELAY, RECONNECT_BASE_DELAY * Math.pow(2, reconnectAttempt));
  reconnectAttempt++;
  return Math.random() * cap;
}

// Store for active SSE connections
const eventSources = new Map();
// Store for previous values to detect changes
const previousValues = new Map();

// Reconnect state
let isReconnecting = false;
let reconnectTimer = null;

// Extract API base URL from main thread message
let API_BASE_URL = '';

// Initialize Web Worker
self.onmessage = function(e) {
  const { type, payload } = e.data;

  switch (type) {
    case 'INIT':
      API_BASE_URL = payload.apiBaseUrl;
      break;

    case 'START_MONITORING':
      startServerMonitoring();
      break;

    case 'STOP_MONITORING':
      stopServerMonitoring();
      break;

    // 主线程确认过鉴权状态后告诉 Worker 该停还是该继续。
    // Worker 自己看不到 HTTP 状态码，这个判断只能由主线程做。
    case 'AUTH_BLOCKED':
      sseAuthBlocked = true;
      stopReconnect();
      break;
    case 'AUTH_RESUMED':
      sseAuthBlocked = false;
      reconnectAttempt = 0;
      break;

    case 'CLOSE_ALL':
      closeAllConnections();
      break;
  }
};

// Start monitoring server-level resources
function startServerMonitoring() {
  const SERVER_MONITOR_KEY = 'server_monitor';
  if (eventSources.has(SERVER_MONITOR_KEY)) {
    return; // Already monitoring
  }

  try {
    const eventSource = new EventSource(`${API_BASE_URL}/api/server/info`);
    eventSources.set(SERVER_MONITOR_KEY, eventSource);

    eventSource.onopen = () => {
      // 连接成功必须重置退避指数，否则下次断线会直接从上次的最大延迟起步
      reconnectAttempt = 0;
      stopReconnect();
      console.log('[ServerResourceWorker] SSE connection established');
      stopReconnect();
      self.postMessage({
        type: 'SSE_CONNECTED'
      });
    };

    eventSource.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);

        // Check for significant changes before sending to main thread
        const shouldUpdate = checkForSignificantServerChanges(data);

        if (shouldUpdate) {
          // Update previous values
          updatePreviousServerValues(data);

          // Send updated data to main thread
          self.postMessage({
            type: 'RESOURCE_UPDATE',
            payload: { data }
          });
        }
      } catch (error) {
        console.error('Failed to parse server resource info:', error);

        self.postMessage({
          type: 'ERROR',
          payload: { error: 'Failed to parse server resource info' }
        });
      }
    };

    eventSource.onerror = (error) => {
      if (eventSource.readyState === EventSource.CLOSED) {
        console.error('[ServerResourceWorker] SSE connection closed, will reconnect');
        stopServerMonitoring();
        self.postMessage({
          type: 'ERROR',
          payload: { error: 'SSE connection error' }
        });
        // CLOSED 说明浏览器已放弃（服务端返回了非 200，比如 401）。
        // 请主线程确认是不是鉴权失效——EventSource 拿不到状态码。
        self.postMessage({ type: 'SSE_CHECK_AUTH' });
        startReconnect();
      }
      // readyState === CONNECTING means browser is auto-retrying, no action needed
    };

  } catch (error) {
    console.error('Failed to start server resource monitoring:', error);

    self.postMessage({
      type: 'ERROR',
      payload: { error: 'Failed to start server monitoring' }
    });
    startReconnect();
  }
}

// Stop monitoring server-level resources
function stopServerMonitoring() {
  const SERVER_MONITOR_KEY = 'server_monitor';
  const eventSource = eventSources.get(SERVER_MONITOR_KEY);
  if (eventSource) {
    eventSource.close();
    eventSources.delete(SERVER_MONITOR_KEY);
    previousValues.delete(SERVER_MONITOR_KEY);
  }
}

// Close all connections
function closeAllConnections() {
  stopReconnect();
  eventSources.forEach((eventSource) => {
    eventSource.close();
  });
  eventSources.clear();
  previousValues.clear();
}

// Start auto-reconnect
function startReconnect() {
  if (isReconnecting) {
    return;
  }
  // 鉴权失效时不要重连：服务端只会一路 401，而每次尝试都是一条新连接。
  if (sseAuthBlocked) {
    console.log('[ServerResourceWorker] 重连已暂停：需要重新登录');
    return;
  }
  isReconnecting = true;
  scheduleReconnect();
}

// 用递归 setTimeout 而不是 setInterval：setInterval 在单次尝试耗时超过间隔时
// 会**堆积**回调，网络差的时候反而制造出更多并发连接。
function scheduleReconnect() {
  clearTimeout(reconnectTimer);
  const delay = nextReconnectDelay();
  console.log('[ServerResourceWorker] 第 ' + reconnectAttempt + ' 次重连将在 ' + Math.round(delay) + 'ms 后进行');
  reconnectTimer = setTimeout(() => {
    if (!isReconnecting || sseAuthBlocked) return;
    if (!eventSources.has('server_monitor')) {
      attemptReconnect();
      if (isReconnecting) {
        scheduleReconnect();
      }
    }
  }, delay);
}

// Attempt a single reconnect
function attemptReconnect() {
  if (eventSources.has('server_monitor')) {
    return; // Already connected
  }
  console.log('[ServerResourceWorker] Attempting to reconnect...');
  self.postMessage({ type: 'RECONNECT_ATTEMPT' });
  startServerMonitoring();
}

// Stop auto-reconnect
function stopReconnect() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  isReconnecting = false;
}

// Check if server data has significant changes compared to previous values
function checkForSignificantServerChanges(currentData) {
  const SERVER_MONITOR_KEY = 'server_monitor';
  if (!previousValues.has(SERVER_MONITOR_KEY)) {
    return true; // First update, always send
  }

  const previousData = previousValues.get(SERVER_MONITOR_KEY);
  let hasSignificantChange = false;

  // Check CPU percent changes
  if (currentData.cpu?.used_percent !== undefined && previousData.cpu?.used_percent !== undefined) {
    const change = Math.abs(currentData.cpu.used_percent - previousData.cpu.used_percent);
    if (change > CHANGE_THRESHOLD) {
      hasSignificantChange = true;
    }
  }

  // Check memory percent changes
  if (currentData.memory?.used_percent !== undefined && previousData.memory?.used_percent !== undefined) {
    const change = Math.abs(currentData.memory.used_percent - previousData.memory.used_percent);
    if (change > CHANGE_THRESHOLD) {
      hasSignificantChange = true;
    }
  }

  // If it's the first time or has significant change, return true
  return hasSignificantChange;
}

// Update previous server values store
function updatePreviousServerValues(data) {
  const SERVER_MONITOR_KEY = 'server_monitor';
  // Create a deep copy to avoid reference issues
  const dataCopy = JSON.parse(JSON.stringify(data));
  previousValues.set(SERVER_MONITOR_KEY, dataCopy);
}

// Clean up on worker termination
self.onclose = function() {
  closeAllConnections();
};
