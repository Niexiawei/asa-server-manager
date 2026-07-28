// Shared Resource Monitor Worker (SharedWorker)
// Maintains single SSE connection and distributes data to subscribers

// Configuration
const CHANGE_THRESHOLD = 0.5;
// SSE 重连采用指数退避 + 全抖动，而不是固定间隔。
//
// 固定间隔的问题不是"太快"，而是**相位锁定**：会话过期时所有客户端同时掉线、
// 同时按同一节奏重连，此后永远保持同步，每隔 N 秒给服务端来一次并发脉冲。
// 全抖动（在 [0, cap) 上均匀取值）能把这个尖峰打散。
const RECONNECT_BASE_DELAY = 1000;
const RECONNECT_MAX_DELAY = 30000;
let reconnectAttempt = 0;
// 鉴权失效后停止重连，等主线程登录成功再说。EventSource 拿不到 HTTP 状态码，
// 所以由主线程去问 /api/auth/state 来判定。
let sseAuthBlocked = false;

function nextReconnectDelay() {
  const cap = Math.min(RECONNECT_MAX_DELAY, RECONNECT_BASE_DELAY * Math.pow(2, reconnectAttempt));
  reconnectAttempt++;
  return Math.random() * cap;
}

// Store all connected ports
const ports = new Set();
// Store subscribers by instance ID: instanceId -> Set<port>
const subscribers = new Map();
// Single SSE connection
let eventSource = null;
// API base URL
let API_BASE_URL = '';

// Reconnect state
let isReconnecting = false;
let reconnectTimer = null;

// Handle new port connections
self.onconnect = (event) => {
  const port = event.ports[0];
  ports.add(port);
  console.log(`[SharedWorker] New port connected. Total ports: ${ports.size}`);
  port.onmessage = ({ data }) => {
    const { type, instanceId, payload } = data;

    switch (type) {
      case 'INIT':
        // Initialize API base URL from first port
        if (!API_BASE_URL) {
          API_BASE_URL = payload.apiBaseUrl;
          console.log('[SharedWorker] Initialized with API base URL');
          startSSEConnection();
        }
        break;

      case 'SUBSCRIBE':
        handleSubscribe(port, instanceId);
        break;

      case 'UNSUBSCRIBE':
        handleUnsubscribe(port, instanceId);
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

  port.onmessageerror = (error) => {
    console.error('[SharedWorker] Port message error:', error);
  };

  // Start port communication
  port.start();
};

// Handle subscribe request
function handleSubscribe(port, instanceId) {
  if (!instanceId) return;

  const set = subscribers.get(instanceId) || new Set();
  set.add(port);
  subscribers.set(instanceId, set);

  console.log(`[SharedWorker] Instance ${instanceId} subscribed. Total subscribers: ${subscribers.size}`);

  // Send initial subscription confirmation
  port.postMessage({
    type: 'SUBSCRIBED',
    instanceId
  });
}

// Handle unsubscribe request
function handleUnsubscribe(port, instanceId) {
  if (!instanceId) return;

  const set = subscribers.get(instanceId);
  if (set) {
    set.delete(port);
    if (set.size === 0) {
      subscribers.delete(instanceId);
      console.log(`[SharedWorker] Instance ${instanceId} unsubscribed`);
    }
  }

  // Remove port from all connections if no subscribers
  if (Array.from(subscribers.values()).every(set => !set.has(port))) {
    ports.delete(port);
    console.log(`[SharedWorker] Port disconnected. Remaining ports: ${ports.size}`);
  }
}

// Start single SSE connection
function startSSEConnection() {
  if (eventSource) {
    console.warn('[SharedWorker] SSE connection already exists');
    return;
  }

  try {
    console.log('[SharedWorker] Creating SSE connection to /api/server/all-info');
    eventSource = new EventSource(`${API_BASE_URL}/api/server/all-info`);

    eventSource.onopen = () => {
      // 连接成功必须重置退避指数，否则下次断线会直接从上次的最大延迟起步
      reconnectAttempt = 0;
      stopReconnect();
      console.log('[SharedWorker] SSE connection established');
      stopReconnect();
      broadcastToAllPorts({
        type: 'SSE_CONNECTED'
      });
    };

    eventSource.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);

        // Distribute data to subscribers
        if (data.instances && Array.isArray(data.instances)) {
          data.instances.forEach(instanceData => {
            const instanceId = instanceData.instance;
            const subscriberSet = subscribers.get(instanceId);

            if (subscriberSet) {
              const formattedData = formatInstanceData(instanceData, data);

              subscriberSet.forEach(port => {
                port.postMessage({
                  type: 'RESOURCE_UPDATE',
                  instanceId,
                  data: formattedData
                });
              });
            }
          });
        }
      } catch (error) {
        console.error('[SharedWorker] Failed to parse SSE data:', error);
        broadcastErrorToAllPorts('Failed to parse resource data');
      }
    };

    eventSource.onerror = (error) => {
      if (eventSource.readyState === EventSource.CLOSED) {
        console.error('[SharedWorker] SSE connection closed, will reconnect');
        closeSSEConnection();
        broadcastErrorToAllPorts('SSE connection error');
        // CLOSED 说明浏览器已放弃（服务端返回了非 200，比如 401）。
        // 请主线程确认是不是鉴权失效——EventSource 拿不到状态码。
        broadcastToAllPorts({ type: 'SSE_CHECK_AUTH' });
        startReconnect();
      }
      // readyState === CONNECTING means browser is auto-retrying, no action needed
    };
  } catch (error) {
    console.error('[SharedWorker] Failed to start SSE connection:', error);
    broadcastErrorToAllPorts('Failed to start monitoring');
    startReconnect();
  }
}

// Start auto-reconnect
function startReconnect() {
  if (isReconnecting) {
    return;
  }
  // 鉴权失效时不要重连：服务端只会一路 401，而每次尝试都是一条新连接。
  if (sseAuthBlocked) {
    console.log('[SharedWorker] 重连已暂停：需要重新登录');
    return;
  }
  isReconnecting = true;
  broadcastToAllPorts({ type: 'SSE_RECONNECTING' });
  scheduleReconnect();
}

// 用递归 setTimeout 而不是 setInterval：setInterval 在单次尝试耗时超过间隔时
// 会**堆积**回调，网络差的时候反而制造出更多并发连接。
function scheduleReconnect() {
  clearTimeout(reconnectTimer);
  const delay = nextReconnectDelay();
  console.log('[SharedWorker] 第 ' + reconnectAttempt + ' 次重连将在 ' + Math.round(delay) + 'ms 后进行');
  reconnectTimer = setTimeout(() => {
    if (!isReconnecting || sseAuthBlocked) return;
    if (!eventSource) {
      attemptReconnect();
      if (isReconnecting) {
        scheduleReconnect();
      }
    }
  }, delay);
}

// Attempt a single reconnect
function attemptReconnect() {
  if (eventSource) {
    return; // Already connected
  }
  console.log('[SharedWorker] Attempting to reconnect...');
  broadcastToAllPorts({ type: 'SSE_RECONNECT_ATTEMPT' });
  startSSEConnection();
}

// Stop auto-reconnect
function stopReconnect() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  isReconnecting = false;
}

// Format instance data for sending to component
function formatInstanceData(instanceData, fullData) {
  return {
    timestamp: fullData.timestamp,
    cpu_cores: fullData.cpu_cores,
    memory: {
      total: fullData.memory.total,
      total_gb: fullData.memory.total_gb
    },
    pid: instanceData.pid,
    process: {
      name: instanceData.process_name,
      cpu_percent: instanceData.cpu_percent,
      cpu_total_percent: instanceData.cpu_total_percent,
      memory_used: instanceData.memory_used,
      memory_used_mb: instanceData.memory_used_mb,
      memory_used_gb: instanceData.memory_used_gb,
      memory_percent: instanceData.memory_percent
    },
    render: {
      cpu_percent_value: instanceData.cpu_percent.toFixed(1),
      cpu_percent_normalized: instanceData.cpu_percent / 100,
      cpu_percent_color: getProgressColor(instanceData.cpu_percent),

      cpu_total_percent_value: instanceData.cpu_total_percent.toFixed(1),
      cpu_total_percent_normalized: instanceData.cpu_total_percent / 100,
      cpu_total_percent_color: getProgressColor(instanceData.cpu_total_percent),

      memory_used_formatted: formatMemory(instanceData.memory_used),
      memory_total_formatted: formatMemory(fullData.memory.total),

      memory_percent_value: instanceData.memory_percent.toFixed(2),
      memory_percent_normalized: instanceData.memory_percent / 100,
      memory_percent_color: getProgressColor(instanceData.memory_percent)
    }
  };
}

// Format memory bytes to readable format
function formatMemory(bytes) {
  if (!bytes) return '-';

  // Helper function to format number to max 4 digits
  function formatToMax4Digits(value) {
    // First try the value as is (if integer)
    const intValue = Math.floor(value);
    if (intValue === value && intValue.toString().length <= 4) {
      return value.toString();
    }

    // Try with 2 decimal places
    let formatted = value.toFixed(2);
    // Check if the numeric part (without decimal point) has at most 4 digits
    if (formatted.replace('.', '').length <= 4) {
      return formatted;
    }

    // Try with 1 decimal place
    formatted = value.toFixed(1);
    if (formatted.replace('.', '').length <= 4) {
      return formatted;
    }

    // Use integer value if it has 4 or fewer digits
    if (intValue.toString().length <= 4) {
      return intValue.toString();
    }

    // If integer part has more than 4 digits, truncate to 4 digits
    if (intValue.toString().length > 4) {
      return intValue.toString().substring(0, 4);
    }

    // Fallback
    return value.toFixed(0);
  }

  const mb = bytes / (1024 * 1024);
  if (mb < 1024) {
    return `${formatToMax4Digits(mb)} MB`;
  }

  const gb = mb / 1024;
  return `${formatToMax4Digits(gb)} GB`;
}

// Get progress color based on percentage
function getProgressColor(percent) {
  if (percent < 50) {
    return '#00b42a'; // 绿色
  } else if (percent < 70) {
    return '#165dff'; // 蓝色
  } else if (percent < 90) {
    return '#ff7d00'; // 黄色
  } else {
    return '#f53f3f'; // 红色
  }
}

// Broadcast message to all ports
function broadcastToAllPorts(message) {
  ports.forEach(port => {
    try {
      port.postMessage(message);
    } catch (error) {
      console.error('[SharedWorker] Failed to send message to port:', error);
    }
  });
}

// Broadcast error to all ports
function broadcastErrorToAllPorts(error) {
  broadcastToAllPorts({
    type: 'ERROR',
    error
  });
}

// Close SSE connection
function closeSSEConnection() {
  if (eventSource) {
    eventSource.close();
    eventSource = null;
    console.log('[SharedWorker] SSE connection closed');
  }
}

// Close all connections
function closeAllConnections() {
  console.log('[SharedWorker] Closing all connections');
  stopReconnect();
  closeSSEConnection();
  ports.clear();
  subscribers.clear();
}

// Cleanup on worker termination
self.onclose = function () {
  closeAllConnections();
};
