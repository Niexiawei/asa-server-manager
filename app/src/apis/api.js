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

// 启动所有服务器实例
export async function startAllServers() {
  const response = await fetch(`${API_BASE_URL}/api/server/start-all`, {
    method: 'POST',
  })
  return handleResponse(response)
}

// 停止所有服务器实例
export async function stopAllServers() {
  const response = await fetch(`${API_BASE_URL}/api/server/stop-all`, {
    method: 'POST',
  })
  return handleResponse(response)
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

// 更新服务器
export async function updateServer(forceServer = false) {
  const response = await fetch(`${API_BASE_URL}/api/server/update?force-server=${forceServer}`, {
    method: 'POST',
  })
  return handleResponse(response)
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