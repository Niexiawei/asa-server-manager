// 「当前机器」上下文（管理器互控，docs/REMOTE_MANAGER_MESH_PLAN.md §12 P4-1）。
//
// 远程上下文里，业务请求的路径从 /api/... 改写为 /api/peers/<节点ID>/fwd/api/...，由本机转发给对方；
// 会话（/api/auth/）、远程管理本身（/api/mesh/）与转发入口（/api/peers/，防多跳）永远留在本机。
//
// 选择存在 sessionStorage：每个标签页各管一台，互不干扰，刷新后保持。
// 切换机器 = 写 sessionStorage + 整页重载：wsManager / rconStore 在模块加载时就拼好了 URL，
// 资源 Worker、SSE、各 store 都有自己的长连接与缓存，逐个通知它们换前缀既容易漏，
// 又会把两台机器的数据短暂混在一个页面里。重载是唯一不会串数据的做法。
import {reactive} from 'vue'
import {MessagePlugin} from 'tdesign-vue-next'

const STORAGE_KEY = 'asa.mesh.peer'
const LOCAL_PREFIXES = ['/api/auth/', '/api/mesh/', '/api/peers/']

function load() {
    try {
        const raw = sessionStorage.getItem(STORAGE_KEY)
        const v = raw ? JSON.parse(raw) : null
        if (v && typeof v.id === 'string' && v.id) return v
    } catch (e) {
        // sessionStorage 不可用（隐私模式等）或内容损坏：按本机处理
    }
    return null
}

const initial = load()

/** 当前机器：id 为空 = 本机。label / version 只用于显示。 */
export const peerState = reactive({
    id: initial?.id || '',
    label: initial?.label || '',
})

export const isRemote = () => !!peerState.id

/** 把一个 /api/... 路径按当前机器改写（本机时原样返回）。 */
export function peerPath(url) {
    if (!peerState.id || typeof url !== 'string') return url
    const isApi = url.startsWith('/api/') || url === '/health' || url.startsWith('/health?')
    if (!isApi || LOCAL_PREFIXES.some(p => url.startsWith(p))) return url
    return `/api/peers/${encodeURIComponent(peerState.id)}/fwd${url}`
}

function reloadHome() {
    // 回到首页再重载：当前路由（例如某个实例的详情页）在另一台机器上多半不存在。
    window.location.hash = '#/'
    window.location.reload()
}

/** 切到对端 peer（{node_id, display_name}）并重载。 */
export function switchToPeer(peer) {
    try {
        sessionStorage.setItem(STORAGE_KEY, JSON.stringify({id: peer.node_id, label: peer.display_name || ''}))
    } catch (e) {
        MessagePlugin.error('浏览器不允许保存当前机器的选择（sessionStorage 不可用）')
        return
    }
    reloadHome()
}

/** 回到本机并重载。 */
export function switchToLocal() {
    try {
        sessionStorage.removeItem(STORAGE_KEY)
    } catch (e) {
        // ignore
    }
    reloadHome()
}

// 远程上下文里转发入口返回的错误码（internal/webapi/meshapi/forward.go）。
const PEER_ERRORS = {
    peer_unreachable: '无法连接远程机器',
    peer_not_paired: '远程机器已不再授权本机',
    peer_unauthorized: '远程机器拒绝了本次请求',
    peer_forbidden: '远程访问不允许使用此功能',
    mesh_not_running: '本机的管理器互控没有在运行',
}

let lastNotice = 0

/**
 * http.js 的响应拦截器调用：远程上下文的错误给一个提示（带「回到本机」），不跳登录页。
 * 同一批并发请求只提示一次。返回 true 表示已处理。
 */
export function notifyPeerError(code) {
    const msg = PEER_ERRORS[code]
    if (!msg || !peerState.id) return false
    const now = Date.now()
    if (now - lastNotice < 3000) return true
    lastNotice = now
    MessagePlugin.warning({
        content: `${msg}（${peerState.label || peerState.id.slice(0, 8)}）。可在顶部点「回到本机」。`,
        duration: 5000,
        closeBtn: true,
    })
    return true
}
