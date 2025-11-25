export function buildWebSocketUrl(url, token = "") {

    // Use shortToken if available (without base64 encoding), otherwise use regular token (with base64 encoding)
    let authParam = [
        `token=${token}`,
        `clientId=${generateClientId()}`
    ]

    // In development mode, connect directly to backend server
    if (import.meta.env.DEV) {
        const proxyTarget = import.meta.env.VITE_PROXY_TARGET || 'http://localhost:19193'
        const wsTarget = proxyTarget.replace(/^https?:/, location.protocol === 'https:' ? 'wss:' : 'ws:')
        return urlJoin(wsTarget, url, `?${authParam.join('&')}`)
    }

    // In production mode, use current host
    const protocol = location.protocol === 'https:' ? 'wss://' : 'ws://'
    return urlJoin(protocol + window.location.host, window.location.pathname, url, `?${authParam.join('&')}`)
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