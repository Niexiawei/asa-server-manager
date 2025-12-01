// API 服务封装
const API_BASE_URL = import.meta.env.VITE_API_ROOT

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

// 重启服务器实例（SSE 流式响应）
export async function restartServerSSE(name, onMessage, onError, onComplete) {
  try {
    const response = await fetch(`${API_BASE_URL}/api/server/${name}/restart`, {
      method: 'POST',
    })

    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`)
    }

    const reader = response.body.getReader()
    const processor = createSSEStreamProcessor(onMessage)

    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      processor.process(value)
    }
    
    processor.flush()

    if (onComplete) {
      onComplete()
    }
  } catch (error) {
    console.error('Restart server error:', error)
    if (onError) {
      onError(error)
    }
  }
}

// SSE 流处理辅助函数 - 正确处理 UTF-8 多字节字符
function createSSEStreamProcessor(onMessage) {
  const decoder = new TextDecoder('utf-8')
  let textBuffer = ''
  let byteBuffer = new Uint8Array(0)

  return {
    process: (chunk) => {
      // 合并之前的不完整字节和新的字节
      const combined = new Uint8Array(byteBuffer.length + chunk.length)
      combined.set(byteBuffer)
      combined.set(chunk, byteBuffer.length)
      
      // 尝试解码，处理可能的不完整 UTF-8 序列
      let lastCompleteIndex = combined.length
      
      // 检查末尾是否有不完整的 UTF-8 序列
      for (let i = Math.max(0, combined.length - 4); i < combined.length; i++) {
        const byte = combined[i]
        // UTF-8 多字节字符的开始标记
        if ((byte & 0x80) === 0) {
          lastCompleteIndex = i + 1
        } else if ((byte & 0xE0) === 0xC0) {
          // 2字节字符
          if (i + 1 < combined.length) {
            lastCompleteIndex = i + 2
          } else {
            lastCompleteIndex = i
            break
          }
        } else if ((byte & 0xF0) === 0xE0) {
          // 3字节字符（中文常见）
          if (i + 2 < combined.length) {
            lastCompleteIndex = i + 3
          } else {
            lastCompleteIndex = i
            break
          }
        } else if ((byte & 0xF8) === 0xF0) {
          // 4字节字符
          if (i + 3 < combined.length) {
            lastCompleteIndex = i + 4
          } else {
            lastCompleteIndex = i
            break
          }
        } else if ((byte & 0xC0) === 0x80) {
          // 连续字节，更新完整位置
          lastCompleteIndex = i + 1
        }
      }
      
      // 解码完整的部分
      if (lastCompleteIndex > 0) {
        textBuffer += decoder.decode(combined.slice(0, lastCompleteIndex), { stream: true })
      }
      
      // 保留不完整的字节
      byteBuffer = combined.slice(lastCompleteIndex)
      
      // 处理完整的消息
      const messages = textBuffer.split(/\n\n+/)
      
      for (let i = 0; i < messages.length - 1; i++) {
        const message = messages[i].trim()
        if (message.startsWith('data: ')) {
          const content = message.substring(6).trim()
          if (content && onMessage) {
            onMessage(content)
          }
        }
      }
      
      // 保留不完整的消息
      textBuffer = messages[messages.length - 1]
    },
    
    flush: () => {
      // 处理剩余字节
      if (byteBuffer.length > 0) {
        textBuffer += decoder.decode(byteBuffer)
      }
      
      // 处理剩余的消息
      if (textBuffer.trim()) {
        const message = textBuffer.trim()
        if (message.startsWith('data: ')) {
          const content = message.substring(6).trim()
          if (content && onMessage) {
            onMessage(content)
          }
        }
      }
    }
  }
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

    const reader = response.body.getReader()
    const processor = createSSEStreamProcessor(onMessage)

    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      processor.process(value)
    }
    
    processor.flush()

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

    const reader = response.body.getReader()
    const processor = createSSEStreamProcessor(onMessage)

    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      processor.process(value)
    }
    
    processor.flush()

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
export async function createBackup(name) {
  const response = await fetch(`${API_BASE_URL}/api/backup/${name}`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({}),
  })
  return handleResponse(response)
}

// 列出所有备份
export async function listBackups() {
  const response = await fetch(`${API_BASE_URL}/api/backup`)
  return handleResponse(response)
}

// 恢复备份（可选择恢复的内容）
export async function restoreBackup(name, backupFile, options = {}) {
  const requestBody = {
    backup_file: backupFile
  }
  
  // 如果提供了选项参数，添加到请求体
  if (options.restoreWorldfile !== undefined) {
    requestBody.restore_worldfile = options.restoreWorldfile
  }
  if (options.restoreInstanceConfig !== undefined) {
    requestBody.restore_instance_config = options.restoreInstanceConfig
  }
  if (options.restoreGameConfig !== undefined) {
    requestBody.restore_game_config = options.restoreGameConfig
  }
  
  const response = await fetch(`${API_BASE_URL}/api/backup/${name}/restore`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(requestBody),
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

    const reader = response.body.getReader()
    const processor = createSSEStreamProcessor(onMessage)

    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      processor.process(value)
    }
    
    processor.flush()

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

// 获取最近 1000 条日志（使用 Server-Sent Events）
export function getRecentInstanceLogs(instanceName, onLog, onError, onClose) {
  const eventSource = new EventSource(`${API_BASE_URL}/api/logs/${instanceName}/recent`)

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

  // 完成事件
  eventSource.addEventListener('end', () => {
    eventSource.close()
    if (onClose) {
      onClose()
    }
  })

  // Return a function to stop listening
  return () => {
    eventSource.close()
    if (onClose) {
      onClose()
    }
  }
}

// 实时查看系统日志（使用 Server-Sent Events）
export function streamSystemLogs(onLog, onError, onClose) {
  const eventSource = new EventSource(`${API_BASE_URL}/api/logs`)

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

// 同步实例配置
export async function syncInstanceConfig(sourceInstance, targetInstances, syncCustomStartParameters, syncEnableAsaPlugin) {
  const response = await fetch(`${API_BASE_URL}/api/config/sync-instance`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      source_instance: sourceInstance,
      target_instances: targetInstances,
      sync_custom_start_parameters: syncCustomStartParameters,
      sync_enable_asa_plugin: syncEnableAsaPlugin,
    }),
  })
  return handleResponse(response)
}

// 同步游戏配置（Game.ini 和 GameUserSettings.ini）
export async function syncGameConfig(instances) {
  const response = await fetch(`${API_BASE_URL}/api/config/sync`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      instances: instances,
    }),
  })
  return handleResponse(response)
}

// 导入事件 WebSocket 管理器
import {
  connectWebSocket,
  disconnectWebSocket,
  onServerEvent,
  onAnyServerEvent,
  isWebSocketConnected,
  sendWebSocketMessage,
  startReconnect,
  stopReconnect,
  getWebSocketStatus
} from '@/utils/wsManager.js'

// 导入 RCON WebSocket 管理器
import {
  connectRCONWebSocket,
  disconnectRCONWebSocket,
  sendRCONCommandViaWebSocket,
  onRCONMessage,
  isRCONWebSocketConnected,
  startRCONReconnect,
  stopRCONReconnect
} from '@/store/rconStore.js'

// 重新导出这些函数供其他模块使用
export {
  connectWebSocket,
  disconnectWebSocket,
  onServerEvent,
  onAnyServerEvent,
  isWebSocketConnected,
  sendWebSocketMessage,
  startReconnect,
  stopReconnect,
  getWebSocketStatus,
  connectRCONWebSocket,
  disconnectRCONWebSocket,
  sendRCONCommandViaWebSocket,
  onRCONMessage,
  isRCONWebSocketConnected,
  startRCONReconnect,
  stopRCONReconnect
}
