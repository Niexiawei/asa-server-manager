// Instance Resource Monitor Web Worker
// Handles SSE connections for instance-level resource data

// Configuration
const CHANGE_THRESHOLD = 0.5; // Only update when change exceeds this percentage

// Store for active SSE connections
const eventSources = new Map();
// Store for previous values to detect changes
const previousValues = new Map();
// Store for instance subscriptions (which instances are listening)
const instanceSubscriptions = new Map();
// Shared all-instances SSE connection
let allInstancesEventSource = null;
// Store all instances data for last update
let allInstancesData = null;

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

    case 'STOP_MONITORING':
      stopMonitoring(payload.instanceName);
      break;

    case 'CLOSE_ALL':
      closeAllConnections();
      break;
  }
};

// Start monitoring for a specific instance using shared all-instances connection
function startMonitoring(instanceName) {
  if (instanceSubscriptions.has(instanceName)) {
    return; // Already monitoring
  }

  // Add to subscriptions
  instanceSubscriptions.set(instanceName, true);
  previousValues.set(instanceName, null);

  // Start shared connection if not already started
  if (!allInstancesEventSource) {
    startSharedAllInstancesMonitoring();
  }
}

// Start shared monitoring for all instances
function startSharedAllInstancesMonitoring() {
  if (allInstancesEventSource) {
    return; // Already running
  }

  try {
    allInstancesEventSource = new EventSource(`${API_BASE_URL}/api/server/all-info`);

    allInstancesEventSource.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        allInstancesData = data;

        // Process each instance that has subscribers
        if (data.instances && Array.isArray(data.instances)) {
          data.instances.forEach(instanceData => {
            if (instanceSubscriptions.has(instanceData.instance)) {
              processInstanceData(instanceData.instance, instanceData, data);
            }
          });
        }
      } catch (error) {
        console.error('Failed to parse all instances info:', error);
        
        // Notify all subscribers about error
        instanceSubscriptions.forEach((_, instanceName) => {
          self.postMessage({
            type: 'ERROR',
            payload: { instanceName, error: 'Failed to parse resource data' }
          });
        });
      }
    };

    allInstancesEventSource.onerror = (error) => {
      console.error('Shared SSE connection error:', error);
      
      // Notify all subscribers about error
      instanceSubscriptions.forEach((_, instanceName) => {
        self.postMessage({
          type: 'ERROR',
          payload: { instanceName, error: 'SSE connection error' }
        });
      });
      
      closeSharedConnection();
    };

  } catch (error) {
    console.error('Failed to start shared monitoring:', error);
    
    instanceSubscriptions.forEach((_, instanceName) => {
      self.postMessage({
        type: 'ERROR',
        payload: { instanceName, error: 'Failed to start monitoring' }
      });
    });
  }
}

// Process and format instance data for sending to main thread
function processInstanceData(instanceName, instanceData, fullData) {
  // Extract and format process data
  const formattedData = {
    timestamp: fullData.timestamp,
    cpu_cores: fullData.cpu_cores,
    memory: {
      total: fullData.memory.total,
      total_gb: fullData.memory.total_gb
    },
    pid: instanceData.pid,
    process: {
      name: instanceData.instance,
      cpu_percent: instanceData.cpu_percent,
      cpu_total_percent: instanceData.cpu_total_percent,
      memory_used: instanceData.memory_used,
      memory_used_mb: instanceData.memory_used_mb,
      memory_used_gb: instanceData.memory_used_gb,
      memory_percent: instanceData.memory_percent
    },
    // 预计算的渲染数据
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

  // Check for significant changes before sending
  const shouldUpdate = checkForSignificantChanges(instanceName, formattedData);
  
  if (shouldUpdate) {
    updatePreviousValues(instanceName, formattedData);
    
    self.postMessage({
      type: 'RESOURCE_UPDATE',
      payload: { instanceName, data: formattedData }
    });
  }
}

// Format memory bytes to readable format
function formatMemory(bytes) {
  if (!bytes) return '-';
  const mb = bytes / (1024 * 1024);
  if (mb < 1024) {
    return `${mb.toFixed(2)} MB`;
  }
  return `${(mb / 1024).toFixed(2)} GB`;
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

// Stop monitoring for a specific instance
function stopMonitoring(instanceName) {
  instanceSubscriptions.delete(instanceName);
  previousValues.delete(instanceName);

  // Close shared connection if no more subscribers
  if (instanceSubscriptions.size === 0) {
    closeSharedConnection();
  }
}

// Close shared all-instances connection
function closeSharedConnection() {
  if (allInstancesEventSource) {
    allInstancesEventSource.close();
    allInstancesEventSource = null;
    allInstancesData = null;
  }
}

// Close all connections
function closeAllConnections() {
  closeSharedConnection();
  eventSources.forEach((eventSource) => {
    eventSource.close();
  });
  eventSources.clear();
  instanceSubscriptions.clear();
  previousValues.clear();
}

// Check if instance data has significant changes compared to previous values
function checkForSignificantChanges(instanceName, currentData) {
  if (!previousValues.has(instanceName)) {
    return true; // First update, always send
  }

  const previousData = previousValues.get(instanceName);
  
  // If previousData is null, always send (first real update)
  if (!previousData) {
    return true;
  }
  
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

// Update previous values store
function updatePreviousValues(instanceName, data) {
  // Create a deep copy to avoid reference issues
  const dataCopy = JSON.parse(JSON.stringify(data));
  previousValues.set(instanceName, dataCopy);
}

// Clean up on worker termination
self.onclose = function() {
  closeAllConnections();
};
