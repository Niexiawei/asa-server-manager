// Shared Resource Monitor Worker (SharedWorker)
// Maintains single SSE connection and distributes data to subscribers

// Configuration
const CHANGE_THRESHOLD = 0.5;
const RECONNECT_INTERVAL = 10000; // Reconnect interval 10 seconds

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
  isReconnecting = true;
  console.log('[SharedWorker] Starting auto-reconnect (interval: ' + RECONNECT_INTERVAL + 'ms)');

  broadcastToAllPorts({ type: 'SSE_RECONNECTING' });

  // Attempt immediately
  attemptReconnect();

  // Then retry on interval
  reconnectTimer = setInterval(() => {
    if (!eventSource && isReconnecting) {
      attemptReconnect();
    }
  }, RECONNECT_INTERVAL);
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
    clearInterval(reconnectTimer);
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
