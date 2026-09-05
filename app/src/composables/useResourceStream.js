import {buildEventSourceUrl} from '@/utils/utils.js'
import {handleSSECheckAuth, registerSSEWorker} from '@/utils/sseAuthGate.js'

/**
 * 资源数据流的唯一入口。
 *
 * 每个标签页一条 `/api/server/all-info` SSE：这里把「建 Worker + 发 INIT + 登记鉴权闸门」
 * 收成模块级单例，所有资源相关组件（实例面板、趋势图、顶栏弹窗、资源监控页）都从这里订阅，
 * 不要再新开 EventSource。
 *
 * 为什么用专用 Worker 而不是 SharedWorker：SharedWorker 的 console 不进页面 devtools、
 * 实例跨刷新存活且首个 SSE_URL 会被钉死、少数浏览器不支持且无降级路径 —— 排障代价过高。
 * 默认部署是 HTTPS + HTTP/2，6 连接上限不适用；明文 HTTP 多标签页每页一条 all-info 的
 * 轻微回归可以接受。结构与 `utils/wsManager.js` + `workers/wsWorker.js` 对齐。
 * 详见 docs/RESOURCE_RATE_CHART_PLAN.md §10。
 */

let worker = null
let initialized = false

// instanceId -> Set<callback>
const instanceHandlers = new Map()
const hostHandlers = new Set()
const statusHandlers = new Set()

function ensureWorker() {
    if (worker) return worker
    try {
        worker = new Worker(new URL('@/workers/resourceWorker.js', import.meta.url))
        registerSSEWorker(worker)

        worker.onmessage = ({data}) => {
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
                    console.log('[ResourceStream] SSE 已连接')
                    statusHandlers.forEach(fn => fn({connected: true}))
                    break
                case 'ERROR':
                    console.warn('[ResourceStream] SSE 异常:', error)
                    statusHandlers.forEach(fn => fn({connected: false, error: error || '资源数据流异常'}))
                    break
                case 'SSE_CHECK_AUTH':
                    // Worker 看不到 HTTP 状态码，由主线程确认是不是会话失效
                    handleSSECheckAuth(worker)
                    break
            }
        }

        worker.onerror = (err) => {
            console.error('[ResourceStream] Worker 运行错误:', err && (err.message || err))
        }
        worker.onmessageerror = (err) => {
            console.error('[ResourceStream] Worker 通信错误:', err)
        }

        if (!initialized) {
            const sseUrl = buildEventSourceUrl('/api/server/all-info')
            console.log('[ResourceStream] 启动 Worker，SSE URL =', sseUrl)
            worker.postMessage({type: 'INIT', payload: {sseUrl}})
            initialized = true
        }
    } catch (err) {
        console.error('[ResourceStream] Worker 创建失败:', err)
        worker = null
    }
    return worker
}

/**
 * 订阅某个实例的资源数据。
 * @returns {Function} 取消订阅
 */
export function subscribeInstance(instanceId, handler) {
    if (!instanceId || typeof handler !== 'function') return () => {
    }
    const w = ensureWorker()
    if (!w) return () => {
    }

    let set = instanceHandlers.get(instanceId)
    if (!set) {
        set = new Set()
        instanceHandlers.set(instanceId, set)
        w.postMessage({type: 'SUBSCRIBE', instanceId})
    }
    set.add(handler)

    return () => {
        const cur = instanceHandlers.get(instanceId)
        if (!cur) return
        cur.delete(handler)
        // 最后一个订阅者走了才真正退订，否则会把同页面的其它面板一起断掉
        if (cur.size === 0) {
            instanceHandlers.delete(instanceId)
            w.postMessage({type: 'UNSUBSCRIBE', instanceId})
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
    const w = ensureWorker()
    if (!w) return () => {
    }

    const first = hostHandlers.size === 0
    hostHandlers.add(handler)
    if (first) w.postMessage({type: 'SUBSCRIBE_HOST'})

    return () => {
        hostHandlers.delete(handler)
        if (hostHandlers.size === 0) {
            w.postMessage({type: 'UNSUBSCRIBE_HOST'})
        }
    }
}

/** 订阅连接状态变化（连上 / 出错） */
export function subscribeStatus(handler) {
    if (typeof handler !== 'function') return () => {
    }
    ensureWorker()
    statusHandlers.add(handler)
    return () => statusHandlers.delete(handler)
}
