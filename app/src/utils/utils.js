// WebSocket / SSE 一律走**同源**地址，由 vite 代理（dev）或本服务自身（生产）转发。
//
// 以前 dev 模式是直连后端 https://localhost:19193，而页面在 http://localhost:3000。
// 上了 Cookie 鉴权之后这条路走不通了：
//   - scheme 不同（http vs https）就算跨站，SameSite=Lax 的会话 Cookie 不会被带上
//   - 改成 SameSite=None 又要求 Secure，而 dev 页面是明文 http，浏览器会拒绝写入
// 走同源则两个问题都不存在。顺带还省掉了「dev 必须先把本地 CA 装进系统信任存储
// 才能用 EventSource」这个老麻烦 —— vite 代理侧已经配了 secure: false。
//
// basePrefix：
//   dev  —— VITE_API_ROOT 是 /api，vite 代理会剥掉这一层再转发给后端，所以要补上
//   生产 —— 用 window.location.pathname，保留部署在子路径下的支持
//           （hash 路由下 pathname 是稳定的，路由信息都在 # 后面）
function basePrefix() {
    return import.meta.env.DEV
        ? (import.meta.env.VITE_API_ROOT || '/')
        : window.location.pathname
}

export function buildWebSocketUrl(url) {
    // 凭证走 HttpOnly Cookie，浏览器会自动带上，不需要在 query 里传令牌。
    // （令牌进 query 会落到 access log、反代日志和浏览器历史里。）
    const protocol = location.protocol === 'https:' ? 'wss://' : 'ws://'
    return urlJoin(protocol + window.location.host, basePrefix(), url, `?clientId=${generateClientId()}`)
}

export function buildEventSourceUrl(url) {
    return urlJoin(window.location.origin, basePrefix(), url)
}

export function getSSEBaseUrl() {
    return urlJoin(window.location.origin, basePrefix())
}

function urlJoin(...args) {
    return args
        .filter(arg => arg)
        .join('/')
        .replace(/\/+/g, '/')
        .replace(/^(.+):\//, '$1://')
        .replace(/^file:/, 'file:/')
        .replace(/\/(\?|&|#[^!])/g, '$1')
        .replace(/\?/g, '&')
        .replace('&', '?')
}

function generateClientId() {
    return 'client_' + Math.random().toString(36).substring(2, 15) + '_' + Date.now()
}