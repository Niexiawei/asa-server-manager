// Server Resource Monitor Web Worker
// Handles SSE connections for server-level resource data

// Configuration
const CHANGE_THRESHOLD = 0.5; // Only update when change exceeds this percentage
const RECONNECT_INTERVAL = 10000; // Reconnect interval 10 seconds

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
  isReconnecting = true;
  console.log('[ServerResourceWorker] Starting auto-reconnect (interval: ' + RECONNECT_INTERVAL + 'ms)');

  // Attempt immediately
  attemptReconnect();

  // Then retry on interval
  reconnectTimer = setInterval(() => {
    if (!eventSources.has('server_monitor') && isReconnecting) {
      attemptReconnect();
    }
  }, RECONNECT_INTERVAL);
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
    clearInterval(reconnectTimer);
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
