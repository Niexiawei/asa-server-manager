import { reactive, ref } from 'vue'
import { connectWebSocket, disconnectWebSocket, onAnyServerEvent } from '../apis/api.js'

// 全局服务器状态存储
export const serverStore = reactive({
  instances: new Map(),
  connected: false,
  connectionError: null
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
      }
      break
      
    case 'server_started':
      if (instance_name && serverStore.instances.has(instance_name)) {
        const instance = serverStore.instances.get(instance_name)
        instance.running = true
        instance.status = 'started'
      }
      break
      
    case 'server_stopping':
      if (instance_name && serverStore.instances.has(instance_name)) {
        const instance = serverStore.instances.get(instance_name)
        instance.status = 'stopping'
      }
      break
      
    case 'server_stopped':
      if (instance_name && serverStore.instances.has(instance_name)) {
        const instance = serverStore.instances.get(instance_name)
        instance.running = false
        instance.status = 'stopped'
      }
      break
      
    case 'server_start_failed':
      if (instance_name && serverStore.instances.has(instance_name)) {
        const instance = serverStore.instances.get(instance_name)
        instance.running = false
        instance.status = 'failed'
        instance.error = message || '启动失败'
      }
      break
      
    case 'server_stop_failed':
      if (instance_name && serverStore.instances.has(instance_name)) {
        const instance = serverStore.instances.get(instance_name)
        instance.error = message || '停止失败'
      }
      break
      
    case 'server_restart_failed':
      if (instance_name && serverStore.instances.has(instance_name)) {
        const instance = serverStore.instances.get(instance_name)
        instance.error = message || '重启失败'
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
        
        // 监听所有服务器事件
        onAnyServerEvent(handleServerEvent)
        
        resolve(true)
      },
      (error) => {
        console.error('WebSocket connection error:', error)
        serverStore.connectionError = error.message || 'WebSocket 连接失败'
        resolve(false)
      },
      () => {
        console.log('WebSocket disconnected')
        serverStore.connected = false
      }
    )
  })
}

// 关闭 WebSocket 连接
export function closeWebSocket() {
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
      status: instance.running ? 'running' : 'stopped'
    })
  })
  serverStore.instances = newMap
}

// 获取实例状态
export function getInstanceStatus(instanceName) {
  return serverStore.instances.get(instanceName)
}

// 获取所有实例
export function getAllInstances() {
  return Array.from(serverStore.instances.values())
}
