export function buildWebSocketUrl(url, token = "") {

    // Use shortToken if available (without base64 encoding), otherwise use regular token (with base64 encoding)
    let authParam = [
        `token=${token}`,
        `clientId=${generateClientId()}`
    ]

    // In development mode, connect directly to backend server.
    // 协议必须跟着后端走，而不是跟着页面走：dev 页面是 http://localhost:3000，
    // 后端默认已启用 TLS，按 location.protocol 推导会得到 ws:// 去敲 wss 端口，连不上。
    if (import.meta.env.DEV) {
        const proxyTarget = import.meta.env.VITE_PROXY_TARGET || 'https://localhost:19193'
        const wsTarget = proxyTarget.replace(/^https:/, 'wss:').replace(/^http:/, 'ws:')
        return urlJoin(wsTarget, url, `?${authParam.join('&')}`)
    }

    // In production mode, use current host
    const protocol = location.protocol === 'https:' ? 'wss://' : 'ws://'
    return urlJoin(protocol + window.location.host, window.location.pathname, url, `?${authParam.join('&')}`)
}

export function buildEventSourceUrl(url) {
    // In production mode, use current host
    const protocol = location.protocol

    // In development mode, connect directly to backend server.
    // EventSource 没有任何忽略证书错误的手段，所以 dev 直连 https 后端要求
    // 本地 CA 已装进系统受信任存储（默认行为，见 certmgr 包）。
    if (import.meta.env.DEV) {
        const proxyTarget = import.meta.env.VITE_PROXY_TARGET || 'https://localhost:19193'
        return urlJoin(proxyTarget, url)
    }

    return urlJoin(protocol + "//" + window.location.host, window.location.pathname, url)
}

export function getSSEBaseUrl() {
    // In production mode, use current host
    const protocol = location.protocol
    // In development mode, connect directly to backend server
    if (import.meta.env.DEV) {
        return import.meta.env.VITE_PROXY_TARGET || 'https://localhost:19193'
    }
    return protocol + "//" + window.location.host, window.location.pathname
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