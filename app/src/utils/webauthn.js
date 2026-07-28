import apiClient from '@/utils/http.js'

// WebAuthn 前端工具。
//
// 它只负责浏览器侧的编解码与 navigator.credentials 调用；
// 「当前访问地址能不能用 WebAuthn」由后端判定并通过 /api/auth/state 下发
// （后端才知道域名白名单、是否安全上下文）。

/** 浏览器是否支持 WebAuthn。后端不知道这件事，只能前端自己检测。 */
export function isWebAuthnSupported() {
    return typeof window !== 'undefined'
        && typeof window.PublicKeyCredential === 'function'
        && !!navigator.credentials
}

/** 是否有平台认证器（Windows Hello / Touch ID），用于决定按钮文案 */
export async function isPlatformAuthenticatorAvailable() {
    if (!isWebAuthnSupported()) return false
    try {
        return await window.PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable()
    } catch {
        return false
    }
}

// ---- base64url ⇄ ArrayBuffer ----
// WebAuthn 的 JSON 表示里 challenge / id / user.id 都是 base64url 字符串，
// 但 navigator.credentials 要的是 ArrayBuffer，返回值又是 ArrayBuffer，
// 两个方向都得转。

function b64urlToBuf(s) {
    const pad = '='.repeat((4 - (s.length % 4)) % 4)
    const b64 = (s + pad).replace(/-/g, '+').replace(/_/g, '/')
    const bin = atob(b64)
    const buf = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i)
    return buf.buffer
}

function bufToB64url(buf) {
    const bytes = new Uint8Array(buf)
    let bin = ''
    for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i])
    return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function decodeCreationOptions(opts) {
    const p = {...opts}
    p.challenge = b64urlToBuf(p.challenge)
    p.user = {...p.user, id: b64urlToBuf(p.user.id)}
    if (p.excludeCredentials) {
        p.excludeCredentials = p.excludeCredentials.map(c => ({...c, id: b64urlToBuf(c.id)}))
    }
    return p
}

function decodeRequestOptions(opts) {
    const p = {...opts}
    p.challenge = b64urlToBuf(p.challenge)
    if (p.allowCredentials) {
        p.allowCredentials = p.allowCredentials.map(c => ({...c, id: b64urlToBuf(c.id)}))
    }
    return p
}

function encodeAttestation(cred) {
    return {
        id: cred.id,
        rawId: bufToB64url(cred.rawId),
        type: cred.type,
        clientExtensionResults: cred.getClientExtensionResults?.() ?? {},
        response: {
            clientDataJSON: bufToB64url(cred.response.clientDataJSON),
            attestationObject: bufToB64url(cred.response.attestationObject),
            transports: cred.response.getTransports?.() ?? [],
        },
    }
}

function encodeAssertion(cred) {
    return {
        id: cred.id,
        rawId: bufToB64url(cred.rawId),
        type: cred.type,
        clientExtensionResults: cred.getClientExtensionResults?.() ?? {},
        response: {
            clientDataJSON: bufToB64url(cred.response.clientDataJSON),
            authenticatorData: bufToB64url(cred.response.authenticatorData),
            signature: bufToB64url(cred.response.signature),
            userHandle: cred.response.userHandle ? bufToB64url(cred.response.userHandle) : null,
        },
    }
}

// ---- 注册 ----

/** 注册一个新的 Passkey。name 是用户给它起的名字，如 "YubiKey 5C" */
export async function registerPasskey(name) {
    const begin = await apiClient.post('/api/auth/webauthn/register/begin')
    const cred = await navigator.credentials.create({
        publicKey: decodeCreationOptions(begin.publicKey),
    })
    if (!cred) throw new Error('未能创建凭证')
    return apiClient.post('/api/auth/webauthn/register/finish', {
        name: name || '',
        credential: encodeAttestation(cred),
    })
}

// ---- 登录 ----

/**
 * Passkey 登录。username 留空则走 discoverable 流程（不需要输用户名）。
 *
 * 调用方要区分 NotAllowedError / AbortError —— 那是用户取消了系统弹窗，
 * 不是失败，不该提示错误也不该计入限流。
 */
export async function passkeyLogin(username = '') {
    const begin = await apiClient.post('/api/auth/webauthn/login/begin',
        username ? {username} : {})
    const cred = await navigator.credentials.get({
        publicKey: decodeRequestOptions(begin.publicKey),
    })
    if (!cred) throw new Error('未能获取凭证')
    return apiClient.post('/api/auth/webauthn/login/finish', {
        credential: encodeAssertion(cred),
    })
}

// ---- 凭证管理 ----

export const listPasskeys = () => apiClient.get('/api/auth/webauthn/credentials')
export const renamePasskey = (id, name) =>
    apiClient.put(`/api/auth/webauthn/credentials/${id}`, {name})
export const deletePasskey = (id) =>
    apiClient.delete(`/api/auth/webauthn/credentials/${id}`)
