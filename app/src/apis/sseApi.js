import {buildEventSourceUrl} from "@/utils/utils.js";

/**
 * 封装 EventSource 处理逻辑，模拟 fetch 的 onComplete 行为
 * 当连接错误时，如果不是明确的错误状态，我们假设它是服务器主动关闭（完成）
 */
function createEventSourceAction(url, onMessage, onError, onComplete, errorCallback) {
    return new Promise((resolve, reject) => {
        const eventSource = new EventSource(url)
        let hasError = false
        let currError = null

        eventSource.onmessage = (event) => {
            let data = JSON.parse(event.data)
            let {success, error} = errorCallback(data)
            if (!success) {
                hasError = true
                currError = error
                onError(currError)
                return
            }
            onMessage(data)
        }

        eventSource.onerror = (error) => {
            // EventSource 的 onerror 无法区分网络错误和服务器关闭
            // 对于一次性任务（如启动服务器），通常意味着流结束
            eventSource.close()
            if (onComplete) {
                onComplete()
            }

            if (hasError) {
                reject(currError)
                return
            }

            resolve()
        }
    })
}

// 启动服务器实例
export function startServer(name, onMessage, onError, onComplete) {
    const url = buildEventSourceUrl(`/api/server/${name}/start`)

    return createEventSourceAction(url, onMessage, onError, onComplete, (data) => {
        if (data.status === 'error' || data.status === 'start_failed') {
            return {
                success: false,
                error: new Error(data.message)
            }
        }
        return {success: true, error: null}
    })
}

// 重启服务器实例（SSE）
export function restartServerSSE(name, onMessage, onError, onComplete) {
    const url = buildEventSourceUrl(`/api/server/${name}/restart`)

    return createEventSourceAction(url, onMessage, onError, onComplete, (content) => {
        if (content.startsWith('Error:')) {
            return {success: false, error: new Error(content)}
        } else {
            return {success: true, error: null}
        }
    })
}

// 启动所有服务器实例
export function startAllServers(onMessage, onError, onComplete) {
    const url = buildEventSourceUrl('/api/server/start-all')

    return createEventSourceAction(url, onMessage, onError, onComplete, (content) => {
        if (content.startsWith('Error:')) {
            return {success: false, error: new Error(content)}
        } else {
            return {success: true, error: null}
        }
    })
}

// 停止所有服务器实例
export function stopAllServers(onMessage, onError, onComplete) {
    const url = buildEventSourceUrl('/api/server/stop-all')

    return createEventSourceAction(url, onMessage, onError, onComplete, (content, setError, setSuccess) => {
        if (content.startsWith('Error:')) {
            return {success: false, error: new Error(content)}
        } else {
            return {success: true, error: null}
        }
    })
}

// 重启所有服务器实例
export function restartAllServers(onMessage, onError, onComplete) {
    const url = buildEventSourceUrl('/api/server/restart-all')

    return createEventSourceAction(url, onMessage, onError, onComplete, (content, setError, setSuccess) => {
        if (content.startsWith('Error:')) {
            return {success: false, error: new Error(content)}
        } else {
            return {success: true, error: null}
        }
    })
}

// 更新服务器
export function updateServer(onMessage, onError, onComplete) {

    const url = buildEventSourceUrl('/api/server/update')
    return new Promise((resolve) => {
        const eventSource = new EventSource(url)

        eventSource.onmessage = (event) => {
            if (event.data?.startsWith('Error:') && onError) {
                onError(event.data)
                return
            }
            if (onMessage) {
                onMessage(event.data)
            }
        }
        eventSource.onerror = (error) => {
            console.error('SSE connection error:', error)
            if (onComplete) {
                onComplete()
            }
            eventSource.close()
            resolve()
        }
    })

    // return createEventSourceAction(url, onMessage, onError, onComplete, (content) => {
    //     return {success: true, error: null}
    // })
}

// 实时查看服务器日志
export function streamInstanceLogs(instanceName, onLog, onError, onClose) {
    const eventSource = new EventSource(buildEventSourceUrl(`/api/logs/${instanceName}`))

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

    return () => {
        eventSource.close()
        if (onClose) {
            onClose()
        }
    }
}

// 实时查看系统日志
export function streamSystemLogs(onLog, onError, onClose) {
    const eventSource = new EventSource(buildEventSourceUrl('/api/logs'))

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

    return () => {
        eventSource.close()
        if (onClose) {
            onClose()
        }
    }
}

// 流式获取 FRP 状态变化
export function streamFRPStatus(onStatus, onError, onClose) {
    console.log(buildEventSourceUrl('/api/frp/status/stream'))
    const eventSource = new EventSource(buildEventSourceUrl('/api/frp/status/stream'))

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

    eventSource.onerror = (error) => {
        console.error('SSE connection error:', error)
        if (onError) {
            onError(error)
        }
        eventSource.close()
        if (onClose) onClose()
    }

    return () => {
        eventSource.close()
        if (onClose) onClose()
    }
}

// 流式获取 Syncthing 状态变化（SSE - 保持 EventSource）
export function streamSyncthingStatus(onStatus, onError, onClose) {
    const eventSource = new EventSource(buildEventSourceUrl('/api/syncthing/status/stream'))

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
