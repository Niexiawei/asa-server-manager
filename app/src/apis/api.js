// API 服务封装
const API_BASE_URL = 'http://127.0.0.1:8080'

// 处理 API 响应
async function handleResponse(response) {
  if (!response.ok) {
    throw new Error(`HTTP error! status: ${response.status}`)
  }
  return await response.json()
}

// 健康检查
export async function healthCheck() {
  const response = await fetch(`${API_BASE_URL}/health`)
  return handleResponse(response)
}

// 获取实例列表
export async function listInstances() {
  const response = await fetch(`${API_BASE_URL}/api/instances`)
  return handleResponse(response)
}

// 创建实例
export async function createInstance(name) {
  const response = await fetch(`${API_BASE_URL}/api/instances`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ name }),
  })
  return handleResponse(response)
}

// 获取实例状态
export async function getInstanceStatus(name) {
  const response = await fetch(`${API_BASE_URL}/api/instances/${name}`)
  return handleResponse(response)
}

// 获取实例完整配置
export async function getInstanceConfig(name) {
  const response = await fetch(`${API_BASE_URL}/api/instances/${name}`)
  return handleResponse(response)
}

// 更新实例配置
export async function updateInstanceConfig(name, config) {
  const response = await fetch(`${API_BASE_URL}/api/instances/${name}/config`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(config),
  })
  return handleResponse(response)
}

// 删除实例
export async function deleteInstance(name) {
  const response = await fetch(`${API_BASE_URL}/api/instances/${name}`, {
    method: 'DELETE',
  })
  return handleResponse(response)
}

// 重命名实例
export async function renameInstance(name, newName) {
  const response = await fetch(`${API_BASE_URL}/api/instances/${name}`, {
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ new_name: newName }),
  })
  return handleResponse(response)
}

// 启动服务器实例
export async function startServer(name) {
  const response = await fetch(`${API_BASE_URL}/api/server/${name}/start`, {
    method: 'POST',
  })
  return handleResponse(response)
}

// 停止服务器实例
export async function stopServer(name) {
  const response = await fetch(`${API_BASE_URL}/api/server/${name}/stop`, {
    method: 'POST',
  })
  return handleResponse(response)
}

// 重启服务器实例
export async function restartServer(name) {
  const response = await fetch(`${API_BASE_URL}/api/server/${name}/restart`, {
    method: 'POST',
  })
  return handleResponse(response)
}

// 启动所有服务器实例（SSE 流式响应）
export async function startAllServers(onMessage, onError, onComplete) {
  try {
    const response = await fetch(`${API_BASE_URL}/api/server/start-all`, {
      method: 'POST',
    })

    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`)
    }

    // 创建读取器来处理流式响应
    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''

    while (true) {
      const { done, value } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      
      // 处理完整的行
      for (let i = 0; i < lines.length - 1; i++) {
        const line = lines[i]
        if (line.startsWith('data: ')) {
          const message = line.substring(6)
          if (onMessage) {
            onMessage(message)
          }
        }
      }
      
      // 保留未完成的行
      buffer = lines[lines.length - 1]
    }

    // 处理剩余的 buffer
    if (buffer.startsWith('data: ')) {
      const message = buffer.substring(6)
      if (onMessage) {
        onMessage(message)
      }
    }

    if (onComplete) {
      onComplete()
    }
  } catch (error) {
    console.error('Start all servers error:', error)
    if (onError) {
      onError(error)
    }
  }
}

// 停止所有服务器实例（SSE 流式响应）
export async function stopAllServers(onMessage, onError, onComplete) {
  try {
    const response = await fetch(`${API_BASE_URL}/api/server/stop-all`, {
      method: 'POST',
    })

    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`)
    }

    // 创建读取器来处理流式响应
    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''

    while (true) {
      const { done, value } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      
      // 处理完整的行
      for (let i = 0; i < lines.length - 1; i++) {
        const line = lines[i]
        if (line.startsWith('data: ')) {
          const message = line.substring(6)
          if (onMessage) {
            onMessage(message)
          }
        }
      }
      
      // 保留未完成的行
      buffer = lines[lines.length - 1]
    }

    // 处理剩余的 buffer
    if (buffer.startsWith('data: ')) {
      const message = buffer.substring(6)
      if (onMessage) {
        onMessage(message)
      }
    }

    if (onComplete) {
      onComplete()
    }
  } catch (error) {
    console.error('Stop all servers error:', error)
    if (onError) {
      onError(error)
    }
  }
}

// 发送 RCON 命令
export async function sendRCONCommand(name, command) {
  const response = await fetch(`${API_BASE_URL}/api/rcon/${name}/command`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ command }),
  })
  return handleResponse(response)
}

// 创建备份
export async function createBackup(name, worldFolder) {
  const response = await fetch(`${API_BASE_URL}/api/backup/${name}`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ world_folder: worldFolder }),
  })
  return handleResponse(response)
}

// 列出所有备份
export async function listBackups() {
  const response = await fetch(`${API_BASE_URL}/api/backup`)
  return handleResponse(response)
}

// 恢复备份
export async function restoreBackup(name, backupFile) {
  const response = await fetch(`${API_BASE_URL}/api/backup/${name}/restore`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ backup_file: backupFile }),
  })
  return handleResponse(response)
}

// 更新服务器（SSE 流式响应）
export async function updateServer(onMessage, onError, onComplete) {
  try {
    const response = await fetch(`${API_BASE_URL}/api/server/update`, {
      method: 'POST',
    })

    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`)
    }

    // 创建读取器来处理流式响应
    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''

    while (true) {
      const { done, value } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      
      // 处理完整的行
      for (let i = 0; i < lines.length - 1; i++) {
        const line = lines[i]
        if (line.startsWith('data: ')) {
          const message = line.substring(6)
          if (onMessage) {
            onMessage(message)
          }
        }
      }
      
      // 保留未完成的行
      buffer = lines[lines.length - 1]
    }

    // 处理剩余的 buffer
    if (buffer.startsWith('data: ')) {
      const message = buffer.substring(6)
      if (onMessage) {
        onMessage(message)
      }
    }

    if (onComplete) {
      onComplete()
    }
  } catch (error) {
    console.error('Update server error:', error)
    if (onError) {
      onError(error)
    }
  }
}

// 实时查看服务器日志（使用 Server-Sent Events）
export function streamInstanceLogs(instanceName, onLog, onError, onClose) {
  const eventSource = new EventSource(`${API_BASE_URL}/api/logs/${instanceName}`)

  eventSource.onmessage = (event) => {
    if (onLog) {
      onLog(event.data)
    }
  }

  eventSource.onerror = (error) => {
    console.error('SSE connection error:', error)
    if (onError) {
      onError(error)
    }
    eventSource.close()
  }

  // Return a function to stop listening
  return () => {
    eventSource.close()
    if (onClose) {
      onClose()
    }
  }
}

// 获取 Game.ini 配置文件内容
export async function getGameIni(instanceName) {
  const response = await fetch(`${API_BASE_URL}/api/config/${instanceName}/game-ini`)
  return handleResponse(response)
}

// 获取 GameUserSettings.ini 配置文件内容
export async function getGameUserSettings(instanceName) {
  const response = await fetch(`${API_BASE_URL}/api/config/${instanceName}/game-user-settings`)
  return handleResponse(response)
}


// 更新 Game.ini 配置文件内容（直接通过文本）
export async function updateGameIni(instanceName, content) {
  const response = await fetch(`${API_BASE_URL}/api/config/${instanceName}/game-ini`, {
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ content }),
  })
  return handleResponse(response)
}

// 更新 GameUserSettings.ini 配置文件内容（直接通过文本）
export async function updateGameUserSettings(instanceName, content) {
  const response = await fetch(`${API_BASE_URL}/api/config/${instanceName}/game-user-settings`, {
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ content }),
  })
  return handleResponse(response)
}

// 上传 Game.ini 文件（FormData 方式）
export async function uploadGameIniFile(instanceName, file) {
  const formData = new FormData()
  formData.append('file', file)
  const response = await fetch(`${API_BASE_URL}/api/config/${instanceName}/game-ini`, {
    method: 'POST',
    body: formData,
  })
  return handleResponse(response)
}

// 上传 GameUserSettings.ini 文件（FormData 方式）
export async function uploadGameUserSettingsFile(instanceName, file) {
  const formData = new FormData()
  formData.append('file', file)
  const response = await fetch(`${API_BASE_URL}/api/config/${instanceName}/game-user-settings`, {
    method: 'POST',
    body: formData,
  })
  return handleResponse(response)
}

// WebSocket 事件监听器管理
let wsConnection = null
const eventListeners = new Map()

// 生成唯一的客户端 ID
function generateClientId() {
  return 'client_' + Math.random().toString(36).substring(2, 15) + '_' + Date.now()
}

// 建立 WebSocket 连接
export function connectWebSocket(onOpen, onError, onClose) {
  const wsUrl = `ws://127.0.0.1:8080/api/ws/events`
  const clientId = generateClientId()
  
  try {
    wsConnection = new WebSocket(wsUrl)
    
    wsConnection.onopen = () => {
      console.log('WebSocket connected with client ID:', clientId)
      // 发送客户端 ID 给服务器
      wsConnection.send(JSON.stringify({
        client_id: clientId,
        type: 'heartbeat'
      }))
      if (onOpen) onOpen(clientId)
    }
    
    wsConnection.onmessage = (event) => {
      try {
        const message = JSON.parse(event.data)
        console.log('WebSocket message received:', message)
        
        // 触发所有注册的监听器
        eventListeners.forEach((callbacks, eventType) => {
          if (!eventType || message.event_type === eventType) {
            callbacks.forEach(callback => {
              try {
                callback(message)
              } catch (err) {
                console.error('Error in event listener:', err)
              }
            })
          }
        })
      } catch (err) {
        console.error('Failed to parse WebSocket message:', err)
      }
    }
    
    wsConnection.onerror = (error) => {
      console.error('WebSocket error:', error)
      if (onError) onError(error)
    }
    
    wsConnection.onclose = () => {
      console.log('WebSocket closed')
      wsConnection = null
      if (onClose) onClose()
    }
  } catch (err) {
    console.error('Failed to connect WebSocket:', err)
    if (onError) onError(err)
  }
}

// 断开 WebSocket 连接
export function disconnectWebSocket() {
  if (wsConnection) {
    wsConnection.close()
    wsConnection = null
    eventListeners.clear()
  }
}

// 监听特定事件类型
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

// 监听所有服务器事件
export function onAnyServerEvent(callback) {
  return onServerEvent(null, callback)
}

// 获取 WebSocket 连接状态
export function isWebSocketConnected() {
  return wsConnection !== null && wsConnection.readyState === WebSocket.OPEN
}