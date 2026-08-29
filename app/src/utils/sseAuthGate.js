import {authState, recheck, onLoginSuccess} from '@/store/authStore.js'

/**
 * SSE Worker 的鉴权闸门。
 *
 * EventSource 拿不到 HTTP 状态码：服务端返回 401 时，Worker 只知道
 * readyState 变成了 CLOSED，无法区分"会话过期"和"服务器挂了"。
 * 所以 Worker 在 CLOSED 时发 SSE_CHECK_AUTH 上来，由主线程去问
 * /api/auth/state 得到结论，再把结论发回去。
 *
 * 不这么做的话，会话一过期，每个 SSE Worker 都会陷入无限重连，
 * 而服务端每次都只能回 401。
 */

// 多个 Worker 可能同时报 CLOSED，合并成一次状态查询
let checking = null

function post(target, msg) {
    // 普通 Worker 用 postMessage，SharedWorker 用 port.postMessage
    if (typeof target.postMessage === 'function') {
        target.postMessage(msg)
    }
}

/**
 * 处理 Worker 发来的 SSE_CHECK_AUTH。
 * @param {Worker|MessagePort} target 用于把结论发回去
 */
export function handleSSECheckAuth(target) {
    // 鉴权没开：SSE 断开绝不可能是"会话失效"，没必要问服务端。
    // authState 在 main.js 启动时已 recheck() 过一次，authEnabled 可信。
    if (!authState.authEnabled) {
        post(target, {type: 'AUTH_RESUMED'})
        return
    }
    if (!checking) {
        // 不用 recheck(true)：这里不需要"强制最新"，去掉 force 让并发调用
        // （401 拦截器、路由守卫、其它 Worker）复用同一次 single-flight 请求。
        checking = recheck()
            .catch(() => {
            })
            .finally(() => {
                setTimeout(() => {
                    checking = null
                }, 1000)
            })
    }
    checking.then(() => {
        const blocked = authState.authEnabled && !authState.authenticated && !authState.bypassed
        post(target, {type: blocked ? 'AUTH_BLOCKED' : 'AUTH_RESUMED'})
    })
}

// 登录成功后解除所有已登记 Worker 的封锁
const registered = new Set()

/** 登记一个 Worker/MessagePort，登录成功时会自动通知它恢复 */
export function registerSSEWorker(target) {
    registered.add(target)
}

export function unregisterSSEWorker(target) {
    registered.delete(target)
}

onLoginSuccess(() => {
    registered.forEach(t => post(t, {type: 'AUTH_RESUMED'}))
})
