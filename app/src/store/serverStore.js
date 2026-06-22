import {reactive} from 'vue'
import {
    connectWebSocket,
    disconnectWebSocket,
    onAnyServerEvent,
    stopReconnect,
    forceReconnect
} from '@/utils/wsManager.js'
import {listInstances} from "@/apis/api.js";

// 全局服务器状态存储
export const serverStore = reactive({
    instances: new Map(),
    connected: false,
    connectionError: null,
    isReconnecting: false,
    resourceInfo: new Map(), // 存储每个实例的资源占用信息
    gameLogPathEvent: new Map(), // 存储游戏日志路径事件，用于自动开启日志监听
    batchRunning: false,  // 是否有批量操作在运行
    batchOpType: '',      // 当前批量操作类型
    batchProgress: null,  // { done, total }
    batchCallbacks: [],   // 批量状态变更回调
    updateRunning: false, // 是否有服务器更新在运行
    updateCallbacks: [],   // 更新状态变更回调
    restartPending: new Set(), // 重启进行中（等待 server_restarted / server_restart_failed）
})

// WebSocket 事件处理函数
function handleServerEvent(event) {
    console.log('Handling server event:', event)

    const {event_type, instance_name, status, message, data} = event

    switch (event_type) {
        case 'server_start_initialization':
            if (instance_name && serverStore.instances.has(instance_name)) {
                const instance = serverStore.instances.get(instance_name)
                instance.status = 'start_initialization'
                instance.running = false
                instance.message = `${instance_name} 初始化中...`
                instance.isStartingOrRunning = false
            }
            break

        case 'server_start_initialization_successful':
            if (instance_name && serverStore.instances.has(instance_name)) {
                const instance = serverStore.instances.get(instance_name)
                instance.status = 'start_initialization_successful'
                instance.running = false
                instance.message = `${instance_name} 初始化完成，等待启动...`
                instance.isStartingOrRunning = false
            }
            break

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
                instance.status = 'start_failed'
                instance.error = message || '启动失败'
                instance.message = `${instance_name} 启动失败: ${instance.error}`
                instance.isStartingOrRunning = false // 标记为已停止
            }
            break

        case 'server_stop_failed':
            if (instance_name && serverStore.instances.has(instance_name)) {
                const instance = serverStore.instances.get(instance_name)
                instance.error = message || '停止失败'
                instance.status = 'stop_failed'
                instance.message = `${instance_name} 停止失败: ${instance.error}`
                instance.isStartingOrRunning = false
            }
            break

        case 'server_restart_failed':
            if (instance_name && serverStore.instances.has(instance_name)) {
                const instance = serverStore.instances.get(instance_name)
                instance.error = message || '重启失败'
                instance.status = 'restart_failed'
                instance.message = `${instance_name} 重启失败: ${instance.error}`
                serverStore.restartPending.delete(instance_name)
            }
            break

        case 'server_restarting':
            if (instance_name && serverStore.instances.has(instance_name)) {
                const instance = serverStore.instances.get(instance_name)
                instance.status = 'restarting'
                instance.isStartingOrRunning = true
                serverStore.restartPending.add(instance_name)
            }
            break

        case 'server_restarted':
            if (instance_name) {
                serverStore.restartPending.delete(instance_name)
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

        case 'batch_started':
            serverStore.batchRunning = true
            serverStore.batchOpType = data?.type || status || ''
            if (data?.total != null) {
                serverStore.batchProgress = {done: 0, total: data.total}
            }
            serverStore.batchCallbacks.forEach(cb => cb('batch_started', event))
            break

        case 'batch_progress':
            if (data?.done != null && data?.total != null) {
                serverStore.batchProgress = {done: data.done, total: data.total}
            }
            serverStore.batchCallbacks.forEach(cb => cb('batch_progress', event))
            break

        case 'batch_completed':
            serverStore.batchRunning = false
            serverStore.batchOpType = ''
            serverStore.batchProgress = null
            serverStore.batchCallbacks.forEach(cb => cb('batch_completed', event))
            break

        case 'update_started':
            serverStore.updateRunning = true
            serverStore.updateCallbacks.forEach(cb => cb('update_started', event))
            break

        case 'update_completed':
            serverStore.updateRunning = false
            serverStore.updateCallbacks.forEach(cb => cb('update_completed', event))
            break

        case 'update_cancelled':
            serverStore.updateRunning = false
            serverStore.updateCallbacks.forEach(cb => cb('update_cancelled', event))
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
                // Worker 内部的 onclose 会自动触发 startReconnect，无需此处递归调用 initializeWebSocket
                console.error('WebSocket connection error:', error)
                serverStore.connectionError = error.message || 'WebSocket 连接失败'
                serverStore.connected = false
                resolve(false)
            },
            () => {
                // Worker 内部的 onclose 会自动触发 startReconnect，无需此处递归调用 initializeWebSocket
                console.log('WebSocket disconnected')
                serverStore.connected = false
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


export async function initServer() {
    const data = await listInstances()
    if (data.success) {
        // 同时更新全局状态存储
        updateInstancesInStore(data.data.instances)
        return data.data.instances
    } else {
        throw new Error(data.error)
    }
}

// 更新实例列表
export function updateInstancesInStore(instances) {
    const newMap = new Map()
    instances.forEach(instance => {
        newMap.set(instance.name, {
            ...instance,
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
    // 通过 Worker 的 RECONNECT 消息实现：停止现有重连 + 清除旧状态 + 新连接
    forceReconnect()
}

export function addRestartPending(instanceName) {
    serverStore.restartPending.add(instanceName)
}

export function removeRestartPending(instanceName) {
    serverStore.restartPending.delete(instanceName)
}
