import {buildEventSourceUrl} from '@/utils/utils.js'
import {handleSSECheckAuth, registerSSEWorker} from '@/utils/sseAuthGate.js'

/**
 * 资源数据流的唯一入口。
 *
 * 全应用（乃至跨标签页）只有一条 `/api/server/all-info` SSE：SharedWorker 按脚本 URL
 * 去重，这里再把「建 worker + 发 INIT + 登记鉴权闸门」收成模块级单例，端口也只有一个。
 *
 * 为什么要这么抠连接数：本服务默认 HTTPS + HTTP/2 时多路复用没这个问题，但内网常以
 * 明文 HTTP 访问，那就是 HTTP/1.1 —— 浏览器对同一 origin 只给 6 条并发连接，而 SSE 是
 * 长连接、一条占一个名额不放。日志流、系统日志、FRP/Syncthing 状态流已经占了几条，
 * 资源监控再按面板数开就会把请求饿死（表现是后续请求挂起而不是报错）。
 */

let port = null
let initialized = false

// instanceId -> Set<callback>
const instanceHandlers = new Map()
const hostHandlers = new Set()
const statusHandlers = new Set()

function ensurePort() {
    if (port) return port
    try {
        const worker = new SharedWorker(new URL('@/workers/sharedResourceWorker.js', import.meta.url))
        port = worker.port
        registerSSEWorker(port)

        port.onmessage = ({data}) => {
            const {type, instanceId, data: payload, error} = data || {}
            switch (type) {
                case 'RESOURCE_UPDATE': {
                    const set = instanceHandlers.get(instanceId)
                    if (set) set.forEach(fn => fn(payload))
                    break
                }
                case 'HOST_UPDATE':
                    hostHandlers.forEach(fn => fn(payload))
                    break
                case 'SSE_CONNECTED':
                    statusHandlers.forEach(fn => fn({connected: true}))
                    break
                case 'ERROR':
                    statusHandlers.forEach(fn => fn({connected: false, error: error || '资源数据流异常'}))
                    break
                case 'SSE_CHECK_AUTH':
                    // Worker 看不到 HTTP 状态码，由主线程确认是不是会话失效
                    handleSSECheckAuth(port)
                    break
            }
        }

        port.onmessageerror = (err) => {
            console.error('[ResourceStream] 端口通信错误:', err)
        }

        if (!initialized) {
            port.postMessage({type: 'INIT', payload: {sseUrl: buildEventSourceUrl('/api/server/all-info')}})
            initialized = true
        }
    } catch (err) {
        console.error('[ResourceStream] SharedWorker 创建失败:', err)
        port = null
    }
    return port
}

/**
 * 订阅某个实例的资源数据。
 * @returns {Function} 取消订阅
 */
export function subscribeInstance(instanceId, handler) {
    if (!instanceId || typeof handler !== 'function') return () => {
    }
    const p = ensurePort()
    if (!p) return () => {
    }

    let set = instanceHandlers.get(instanceId)
    if (!set) {
        set = new Set()
        instanceHandlers.set(instanceId, set)
        p.postMessage({type: 'SUBSCRIBE', instanceId})
    }
    set.add(handler)

    return () => {
        const cur = instanceHandlers.get(instanceId)
        if (!cur) return
        cur.delete(handler)
        // 最后一个订阅者走了才真正退订，否则会把同页面的其它面板一起断掉
        if (cur.size === 0) {
            instanceHandlers.delete(instanceId)
            p.postMessage({type: 'UNSUBSCRIBE', instanceId})
        }
    }
}

/**
 * 订阅宿主机整机指标。
 * @returns {Function} 取消订阅
 */
export function subscribeHost(handler) {
    if (typeof handler !== 'function') return () => {
    }
    const p = ensurePort()
    if (!p) return () => {
    }

    const first = hostHandlers.size === 0
    hostHandlers.add(handler)
    if (first) p.postMessage({type: 'SUBSCRIBE_HOST'})

    return () => {
        hostHandlers.delete(handler)
        if (hostHandlers.size === 0) {
            p.postMessage({type: 'UNSUBSCRIBE_HOST'})
        }
    }
}

/** 订阅连接状态变化（连上 / 出错） */
export function subscribeStatus(handler) {
    if (typeof handler !== 'function') return () => {
    }
    ensurePort()
    statusHandlers.add(handler)
    return () => statusHandlers.delete(handler)
}
