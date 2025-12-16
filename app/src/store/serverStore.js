import { reactive, ref } from 'vue'
import { connectWebSocket, disconnectWebSocket, onAnyServerEvent, sendWebSocketMessage, startReconnect, stopReconnect, isWebSocketConnected } from '@/utils/wsManager.js'

// 全局服务器状态存储
export const serverStore = reactive({
  instances: new Map(),
  connected: false,
  connectionError: null,
  isReconnecting: false,
  resourceInfo: new Map(), // 存储每个实例的资源占用信息
  gameLogPathEvent: new Map() // 存储游戏日志路径事件，用于自动开启日志监听
})

// WebSocket 事件处理函数
function handleServerEvent(event) {
  console.log('Handling server event:', event)
  
  const { event_type, instance_name, status, message } = event
  
  switch (event_type) {
    case 'server_starting':
      if (instance_name && serverStore.instances.has(instance_name)) {
        const instance = serverStore.instances.get(instance_name)
        instance.status = 'starting'
        instance.message = `${instance_name} 正在启动...`
        instance.isStartingOrRunning = true // 标记为启动中或运行中
      }
      break
      
    case 'server_started':
      if (instance_name && serverStore.instances.has(instance_name)) {
        const instance = serverStore.instances.get(instance_name)
        instance.running = true
        instance.status = 'started'
        instance.message = `${instance_name} 已启动`
        instance.isStartingOrRunning = true // 标记为启动中或运行中
      }
      break
      
    case 'server_stopping':
      if (instance_name && serverStore.instances.has(instance_name)) {
        const instance = serverStore.instances.get(instance_name)
        instance.status = 'stopping'
        instance.message = `${instance_name} 正在停止...`
        instance.isStartingOrRunning = false // 标记为已停止
      }
      break
      
    case 'server_stopped':
      if (instance_name && serverStore.instances.has(instance_name)) {
        const instance = serverStore.instances.get(instance_name)
        instance.running = false
        instance.status = 'stopped'
        instance.message = `${instance_name} 已停止`
        instance.isStartingOrRunning = false // 标记为已停止
      }
      break
      
    case 'server_start_failed':
      if (instance_name && serverStore.instances.has(instance_name)) {
        const instance = serverStore.instances.get(instance_name)
        instance.running = false
        instance.status = 'failed'
        instance.error = message || '启动失败'
        instance.message = `${instance_name} 启动失败: ${instance.error}`
        instance.isStartingOrRunning = false // 标记为已停止
      }
      break
      
    case 'server_stop_failed':
      if (instance_name && serverStore.instances.has(instance_name)) {
        const instance = serverStore.instances.get(instance_name)
        instance.error = message || '停止失败'
        instance.message = `${instance_name} 停止失败: ${instance.error}`
        instance.isStartingOrRunning = false
      }
      break
      
    case 'server_restart_failed':
      if (instance_name && serverStore.instances.has(instance_name)) {
        const instance = serverStore.instances.get(instance_name)
        instance.error = message || '重启失败'
        instance.message = `${instance_name} 重启失败: ${instance.error}`
      }
      break
      
    case 'server_game_log_path':
      if (instance_name) {
        serverStore.gameLogPathEvent.set(instance_name, {
          path: message,
          timestamp: Date.now()
        })
        console.log(`Game log path updated for ${instance_name}: ${message}`)
      }
      break
      
    case 'connected':
      console.log('WebSocket connected')
      serverStore.connected = true
      serverStore.connectionError = null
      break
      
    default:
      console.log('Unknown event type:', event_type)
  }
}

// 初始化 WebSocket 连接
export function initializeWebSocket() {
  return new Promise((resolve) => {
    connectWebSocket(
      () => {
        console.log('WebSocket initialized')
        serverStore.connected = true
        serverStore.connectionError = null
        serverStore.isReconnecting = false
        
        // 停止重连尝试
        stopReconnect()
        
        // 监听所有服务器事件
        onAnyServerEvent(handleServerEvent)
        
        resolve(true)
      },
      (error) => {
        console.error('WebSocket connection error:', error)
        serverStore.connectionError = error.message || 'WebSocket 连接失败'
        // 启动自动重连
        startReconnect(() => {
          initializeWebSocket()
        })
        resolve(false)
      },
      () => {
        console.log('WebSocket disconnected')
        serverStore.connected = false
        // 启动自动重连
        startReconnect(() => {
          initializeWebSocket()
        })
      }
    )
  })
}

// 关闭WebSocket 连接
export function closeWebSocket() {
  stopReconnect()
  disconnectWebSocket()
  serverStore.connected = false
  serverStore.instances.clear()
}

// 更新实例列表
export function updateInstancesInStore(instances) {
  const newMap = new Map()
  instances.forEach(instance => {
    newMap.set(instance.name, {
      ...instance,
      status: instance.running ? 'running' : 'stopped',
      isStartingOrRunning: instance.running // 初始化状态标记
    })
  })
  serverStore.instances = newMap
}

// 更新实例资源信息
export function updateInstanceResourceInfo(instanceName, resourceData) {
  serverStore.resourceInfo.set(instanceName, resourceData)
}

// 获取实例资源信息
export function getInstanceResourceInfo(instanceName) {
  return serverStore.resourceInfo.get(instanceName)
}

// 清除实例资源信息
export function clearInstanceResourceInfo(instanceName) {
  serverStore.resourceInfo.delete(instanceName)
}

// 获取实例状态
export function getInstanceStatus(instanceName) {
  return serverStore.instances.get(instanceName)
}

// 获取所有实例
export function getAllInstances() {
  return Array.from(serverStore.instances.values())
}

// 手动触发重新连接
export function manualReconnect() {
  console.log('Manual reconnect triggered')
  if (serverStore.connected) {
    console.log('Already connected, no need to reconnect')
    return
  }
  startReconnect(() => {
    initializeWebSocket()
  })
}
