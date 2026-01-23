// API 服务封装
import apiClient, { API_BASE_URL } from '@/utils/http'

export { API_BASE_URL }

// 为了兼容之前的 SSE 逻辑，保留 createSSEStreamProcessor
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
                textBuffer += decoder.decode(combined.slice(0, lastCompleteIndex), {stream: true})
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

// 健康检查
export function healthCheck() {
    return apiClient.get('/health')
}

// 获取实例列表
export function listInstances() {
    return apiClient.get('/api/instances')
}

// 创建实例
export function createInstance(name) {
    return apiClient.post('/api/instances', {name})
}

// 获取实例状态
export function getInstanceStatus(name) {
    return apiClient.get(`/api/instances/${name}`)
}

// 获取实例完整配置
export function getInstanceConfig(name) {
    return apiClient.get(`/api/instances/${name}`)
}

// 更新实例配置
export function updateInstanceConfig(name, config) {
    return apiClient.patch(`/api/instances/${name}/config`, config)
}

// 删除实例
export function deleteInstance(name) {
    return apiClient.delete(`/api/instances/${name}`)
}

// 重命名实例
export function renameInstance(name, newName) {
    return apiClient.put(`/api/instances/${name}`, {new_name: newName})
}

// 启动服务器实例（SSE 流式响应 - 保持 fetch，因为是流式）
export async function startServer(name, onMessage, onError, onComplete) {
    try {
        const response = await fetch(`${API_BASE_URL}/api/server/${name}/start`, {
            method: 'POST',
        })

        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`)
        }

        const reader = response.body.getReader()
        let hasError = false

        const processor = createSSEStreamProcessor((content) => {
            try {
                const data = JSON.parse(content)
                if (data.status === 'error' || data.status === 'start_failed') {
                    hasError = true
                    if (onError) {
                        onError(new Error(data.message))
                    }
                } else if (onMessage) {
                    onMessage(data)
                }
            } catch (e) {
                console.error('Failed to parse start server response:', e, 'content:', content)
                if (onError) {
                    onError(new Error('Failed to parse server response'))
                }
            }
        })

        while (true) {
            const {done, value} = await reader.read()
            if (done) break
            processor.process(value)
        }

        processor.flush()

        // 如果没有错误，则调用完成回调
        if (!hasError && onComplete) {
            onComplete()
        }
    } catch (error) {
        console.error('Start server error:', error)
        if (onError) {
            onError(error)
        }
    }
}

// 停止服务器实例
export function stopServer(name) {
    return apiClient.post(`/api/server/${name}/stop`)
}

// 重启服务器实例
export function restartServer(name) {
    return apiClient.post(`/api/server/${name}/restart`)
}

// 重启服务器实例（SSE 流式响应 - 保持 fetch）
export async function restartServerSSE(name, onMessage, onError, onComplete) {
    try {
        const response = await fetch(`${API_BASE_URL}/api/server/${name}/restart`, {
            method: 'POST',
        })

        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`)
        }

        const reader = response.body.getReader()
        let hasError = false

        const processor = createSSEStreamProcessor((content) => {
            // 检测错误消息
            if (content.startsWith('Error:')) {
                hasError = true
                if (onError) {
                    onError(new Error(content))
                }
            } else if (onMessage) {
                onMessage(content)
            }
        })

        while (true) {
            const {done, value} = await reader.read()
            if (done) break
            processor.process(value)
        }

        processor.flush()

        // 如果没有错误，则调用完成回调
        if (!hasError && onComplete) {
            onComplete()
        }
    } catch (error) {
        console.error('Restart server error:', error)
        if (onError) {
            onError(error)
        }
    }
}

// 启动所有服务器实例（SSE 流式响应 - 保持 fetch）
export async function startAllServers(onMessage, onError, onComplete) {
    try {
        const response = await fetch(`${API_BASE_URL}/api/server/start-all`, {
            method: 'POST',
        })

        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`)
        }

        const reader = response.body.getReader()
        let hasError = false

        const processor = createSSEStreamProcessor((content) => {
            // 检测错误消息
            if (content.startsWith('Error:')) {
                hasError = true
                if (onError) {
                    onError(new Error(content))
                }
            } else if (onMessage) {
                onMessage(content)
            }
        })

        while (true) {
            const {done, value} = await reader.read()
            if (done) break
            processor.process(value)
        }

        processor.flush()

        // 如果没有错误，则调用完成回调
        if (!hasError && onComplete) {
            onComplete()
        }
    } catch (error) {
        console.error('Start all servers error:', error)
        if (onError) {
            onError(error)
        }
    }
}

// 停止所有服务器实例（SSE 流式响应 - 保持 fetch）
export async function stopAllServers(onMessage, onError, onComplete) {
    try {
        const response = await fetch(`${API_BASE_URL}/api/server/stop-all`, {
            method: 'POST',
        })

        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`)
        }

        const reader = response.body.getReader()
        let hasError = false

        const processor = createSSEStreamProcessor((content) => {
            // 检测错误消息
            if (content.startsWith('Error:')) {
                hasError = true
                if (onError) {
                    onError(new Error(content))
                }
            } else if (onMessage) {
                onMessage(content)
            }
        })

        while (true) {
            const {done, value} = await reader.read()
            if (done) break
            processor.process(value)
        }

        processor.flush()

        // 如果没有错误，则调用完成回调
        if (!hasError && onComplete) {
            onComplete()
        }
    } catch (error) {
        console.error('Stop all servers error:', error)
        if (onError) {
            onError(error)
        }
    }
}

// 重启所有服务器实例（SSE 流式响应 - 保持 fetch）
export async function restartAllServers(onMessage, onError, onComplete) {
    try {
        const response = await fetch(`${API_BASE_URL}/api/server/restart-all`, {
            method: 'POST',
        })

        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`)
        }

        const reader = response.body.getReader()
        let hasError = false

        const processor = createSSEStreamProcessor((content) => {
            // 检测错误消息
            if (content.startsWith('Error:')) {
                hasError = true
                if (onError) {
                    onError(new Error(content))
                }
            } else if (onMessage) {
                onMessage(content)
            }
        })

        while (true) {
            const {done, value} = await reader.read()
            if (done) break
            processor.process(value)
        }

        processor.flush()

        // 如果没有错误，则调用完成回调
        if (!hasError && onComplete) {
            onComplete()
        }
    } catch (error) {
        console.error('Restart all servers error:', error)
        if (onError) {
            onError(error)
        }
    }
}

// 发送 RCON 命令
export function sendRCONCommand(name, command) {
    return apiClient.post(`/api/rcon/${name}/command`, {command})
}

// 创建备份
export function createBackup(name) {
    return apiClient.post(`/api/backup/${name}`, {})
}

// 列出所有备份
export function listBackups() {
    return apiClient.get('/api/backup')
}

// 恢复备份（可选择恢复的内容）
export function restoreBackup(name, backupFile, options = {}) {
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

    return apiClient.post(`/api/backup/${name}/restore`, requestBody)
}

// 更新服务器（SSE 流式响应 - 保持 fetch）
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
            const {done, value} = await reader.read()
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

// 实时查看服务器日志（使用 Server-Sent Events - 保持 EventSource）
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

// 获取最近 1000 条日志（使用 Server-Sent Events - 保持 EventSource）
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

// 实时查看系统日志（使用 Server-Sent Events - 保持 EventSource）
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
export function getGameIni(instanceName) {
    return apiClient.get(`/api/config/${instanceName}/game-ini`)
}

// 获取 GameUserSettings.ini 配置文件内容
export function getGameUserSettings(instanceName) {
    return apiClient.get(`/api/config/${instanceName}/game-user-settings`)
}

// 获取服务器基础配置文件（Game.ini 和 GameUserSettings.ini）
export function getServerConfigs() {
    return apiClient.get('/api/config/server/configs')
}

// 获取实例配置文件（Game.ini 和 GameUserSettings.ini）
export function getInstanceConfigs(instanceName) {
    return apiClient.get(`/api/config/${instanceName}/configs`)
}


// 更新 Game.ini 配置文件内容（直接通过文本）
export function updateGameIni(instanceName, content) {
    return apiClient.put(`/api/config/${instanceName}/game-ini`, {content})
}

// 更新 GameUserSettings.ini 配置文件内容（直接通过文本）
export function updateGameUserSettings(instanceName, content) {
    return apiClient.put(`/api/config/${instanceName}/game-user-settings`, {content})
}

// 上传 Game.ini 文件（FormData 方式）
export function uploadGameIniFile(instanceName, file) {
    const formData = new FormData()
    formData.append('file', file)
    return apiClient.post(`/api/config/${instanceName}/game-ini`, formData)
}

// 上传 GameUserSettings.ini 文件（FormData 方式）
export function uploadGameUserSettingsFile(instanceName, file) {
    const formData = new FormData()
    formData.append('file', file)
    return apiClient.post(`/api/config/${instanceName}/game-user-settings`, formData)
}

// 同步实例配置
export function syncInstanceConfig(sourceInstance, targetInstances, syncCustomStartParameters, syncEnableAsaPlugin) {
    return apiClient.post('/api/config/sync-instance', {
        source_instance: sourceInstance,
        target_instances: targetInstances,
        sync_custom_start_parameters: syncCustomStartParameters,
        sync_enable_asa_plugin: syncEnableAsaPlugin,
    })
}

// 同步游戏配置（Game.ini 和 GameUserSettings.ini）
export function syncGameConfig(instances) {
    return apiClient.post('/api/config/sync', {instances})
}

// 获取实例资源占用信息（SSE 流式响应 - 保持 EventSource）
export function streamInstanceResourceInfo(instanceName, onData, onError) {
    const eventSource = new EventSource(`${API_BASE_URL}/api/server/${instanceName}/info`)

    eventSource.onmessage = (event) => {
        try {
            const data = JSON.parse(event.data)
            if (onData) {
                onData(data)
            }
        } catch (error) {
            console.error('Failed to parse resource info:', error)
        }
    }

    eventSource.onerror = (error) => {
        console.error('SSE connection error:', error)
        if (onError) {
            onError(error)
        }
        eventSource.close()
    }

    // 返回关闭函数
    return () => {
        eventSource.close()
    }
}

// 获取系统资源占用信息（SSE 流式响应 - 保持 EventSource）
export function streamServerResourceInfo(onData, onError) {
    const eventSource = new EventSource(`${API_BASE_URL}/api/server/info`)

    eventSource.onmessage = (event) => {
        try {
            const data = JSON.parse(event.data)
            if (onData) {
                onData(data)
            }
        } catch (error) {
            console.error('Failed to parse server resource info:', error)
        }
    }

    eventSource.onerror = (error) => {
        console.error('SSE connection error:', error)
        if (onError) {
            onError(error)
        }
        eventSource.close()
    }

    // 返回关闭函数
    return () => {
        eventSource.close()
    }
}

// FRP 管理接口
// 获取 FRP 配置
export function getFRPConfig() {
    return apiClient.get('/api/frp/config')
}

// 更新 FRP 配置
export function updateFRPConfig(config) {
    return apiClient.put('/api/frp/config', {config})
}

// 获取 FRP 状态
export function getFRPStatus() {
    return apiClient.get('/api/frp/status')
}

// 流式获取 FRP 状态变化（SSE - 保持 EventSource）
export function streamFRPStatus(onStatus, onError, onClose) {
    const eventSource = new EventSource(`${API_BASE_URL}/api/frp/status/stream`)

    eventSource.onmessage = (event) => {
        try {
            const data = JSON.parse(event.data)
            if (data.status) {
                onStatus(data.status)
            }
        } catch (error) {
            console.error('Failed to parse status event:', error)
        }
    }

    eventSource.onerror = () => {
        eventSource.close()
        if (onError) {
            onError(new Error('SSE connection closed'))
        }
        if (onClose) {
            onClose()
        }
    }

    // 返回关闭函数
    return () => {
        eventSource.close()
        if (onClose) {
            onClose()
        }
    }
}

// 启动 FRP
export function startFRP() {
    return apiClient.post('/api/frp/start')
}

// 停止 FRP
export function stopFRP() {
    return apiClient.post('/api/frp/stop')
}

// 重启 FRP
export function restartFRP() {
    return apiClient.post('/api/frp/restart')
}

// Syncthing 管理接口
// 获取 Syncthing 配置
export function getSyncthingConfig() {
    return apiClient.get('/api/syncthing/config')
}

// 更新 Syncthing 配置
export function updateSyncthingConfig(config) {
    return apiClient.put('/api/syncthing/config', {config})
}

// 获取 Syncthing 状态
export function getSyncthingStatus() {
    return apiClient.get('/api/syncthing/status')
}

// 流式获取 Syncthing 状态变化（SSE - 保持 EventSource）
export function streamSyncthingStatus(onStatus, onError, onClose) {
    const eventSource = new EventSource(`${API_BASE_URL}/api/syncthing/status/stream`)

    eventSource.onmessage = (event) => {
        try {
            const data = JSON.parse(event.data)
            if (data.status) {
                onStatus(data.status)
            }
        } catch (error) {
            console.error('Failed to parse status event:', error)
        }
    }

    eventSource.onerror = () => {
        eventSource.close()
        if (onError) {
            onError(new Error('SSE connection closed'))
        }
        if (onClose) {
            onClose()
        }
    }

    // 返回关闭函数
    return () => {
        eventSource.close()
        if (onClose) {
            onClose()
        }
    }
}

// 启动 Syncthing
export function startSyncthing() {
    return apiClient.post('/api/syncthing/start')
}

// 停止 Syncthing
export function stopSyncthing() {
    return apiClient.post('/api/syncthing/stop')
}

// 重启 Syncthing
export function restartSyncthing() {
    return apiClient.post('/api/syncthing/restart')
}

// 获取 Mod 信息
export function getModInfo() {
    return apiClient.get('/api/mod-info')
}
