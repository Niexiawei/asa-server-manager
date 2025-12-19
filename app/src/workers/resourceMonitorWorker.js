// Resource Monitor Web Worker
// Handles SSE connections and resource data processing

// Configuration
const CHANGE_THRESHOLD = 0.5; // Only update when change exceeds this percentage

// Store for active SSE connections
const eventSources = new Map();
// Store for previous values to detect changes
const previousValues = new Map();

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
      startMonitoring(payload.instanceName);
      break;

    case 'START_SERVER_MONITORING':
      startServerMonitoring();
      break;

    case 'STOP_MONITORING':
      stopMonitoring(payload.instanceName);
      break;

    case 'STOP_SERVER_MONITORING':
      stopServerMonitoring();
      break;

    case 'CLOSE_ALL':
      closeAllConnections();
      break;
  }
};

// Start monitoring for a specific instance
function startMonitoring(instanceName) {
  if (eventSources.has(instanceName)) {
    return; // Already monitoring
  }

  try {
    const eventSource = new EventSource(`${API_BASE_URL}/api/server/${instanceName}/info`);
    eventSources.set(instanceName, eventSource);

    eventSource.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        
        // Check for significant changes before sending to main thread
        const shouldUpdate = checkForSignificantChanges(instanceName, data);
        
        if (shouldUpdate) {
          // Update previous values
          updatePreviousValues(instanceName, data);
          
          // Send updated data to main thread
          self.postMessage({
            type: 'RESOURCE_UPDATE',
            payload: { instanceName, data }
          });
        }
      } catch (error) {
        console.error('Failed to parse resource info:', error);
        
        self.postMessage({
          type: 'ERROR',
          payload: { instanceName, error: 'Failed to parse resource info' }
        });
      }
    };

    eventSource.onerror = (error) => {
      console.error('SSE connection error:', error);
      
      self.postMessage({
        type: 'ERROR',
        payload: { instanceName, error: 'SSE connection error' }
      });
      
      stopMonitoring(instanceName);
    };

  } catch (error) {
    console.error('Failed to start monitoring:', error);
    
    self.postMessage({
      type: 'ERROR',
      payload: { instanceName, error: 'Failed to start monitoring' }
    });
  }
}

// Stop monitoring for a specific instance
function stopMonitoring(instanceName) {
  const eventSource = eventSources.get(instanceName);
  if (eventSource) {
    eventSource.close();
    eventSources.delete(instanceName);
    previousValues.delete(instanceName);
  }
}

// Start monitoring server-level resources
function startServerMonitoring() {
  const SERVER_MONITOR_KEY = 'server_monitor';
  if (eventSources.has(SERVER_MONITOR_KEY)) {
    return; // Already monitoring
  }

  try {
    const eventSource = new EventSource(`${API_BASE_URL}/api/server/info`);
    eventSources.set(SERVER_MONITOR_KEY, eventSource);

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
            type: 'SERVER_RESOURCE_UPDATE',
            payload: { data }
          });
        }
      } catch (error) {
        console.error('Failed to parse server resource info:', error);
        
        self.postMessage({
          type: 'SERVER_ERROR',
          payload: { error: 'Failed to parse server resource info' }
        });
      }
    };

    eventSource.onerror = (error) => {
      console.error('Server SSE connection error:', error);
      
      self.postMessage({
        type: 'SERVER_ERROR',
        payload: { error: 'SSE connection error' }
      });
      
      stopServerMonitoring();
    };

  } catch (error) {
    console.error('Failed to start server resource monitoring:', error);
    
    self.postMessage({
      type: 'SERVER_ERROR',
      payload: { error: 'Failed to start server monitoring' }
    });
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

// Close all SSE connections
function closeAllConnections() {
  eventSources.forEach((eventSource, instanceName) => {
    eventSource.close();
  });
  eventSources.clear();
  previousValues.clear();
}

// Check if instance data has significant changes compared to previous values
function checkForSignificantChanges(instanceName, currentData) {
  if (!previousValues.has(instanceName)) {
    return true; // First update, always send
  }

  const previousData = previousValues.get(instanceName);
  let hasSignificantChange = false;

  // Check CPU percent changes
  if (currentData.process?.cpu_percent !== undefined && previousData.process?.cpu_percent !== undefined) {
    const change = Math.abs(currentData.process.cpu_percent - previousData.process.cpu_percent);
    if (change > CHANGE_THRESHOLD) {
      hasSignificantChange = true;
    }
  }

  // Check CPU total percent changes
  if (currentData.process?.cpu_total_percent !== undefined && previousData.process?.cpu_total_percent !== undefined) {
    const change = Math.abs(currentData.process.cpu_total_percent - previousData.process.cpu_total_percent);
    if (change > CHANGE_THRESHOLD) {
      hasSignificantChange = true;
    }
  }

  // Check memory percent changes
  if (currentData.process?.memory_percent !== undefined && previousData.process?.memory_percent !== undefined) {
    const change = Math.abs(currentData.process.memory_percent - previousData.process.memory_percent);
    if (change > CHANGE_THRESHOLD) {
      hasSignificantChange = true;
    }
  }

  // If it's the first time or has significant change, return true
  return hasSignificantChange;
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

// Update previous values store
function updatePreviousValues(instanceName, data) {
  // Create a deep copy to avoid reference issues
  const dataCopy = JSON.parse(JSON.stringify(data));
  previousValues.set(instanceName, dataCopy);
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
