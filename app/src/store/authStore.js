import {computed, reactive} from 'vue'
import * as authApi from '@/apis/authApi.js'

/**
 * 全局鉴权状态。
 *
 * 与项目其它状态一致，用 reactive() 而不是 Vuex/Pinia。
 */
export const authState = reactive({
    ready: false,            // 是否已经问过服务端一次
    authEnabled: false,      // 服务端是否开启了鉴权
    authenticated: false,
    bypassed: false,         // 走的是内网免鉴权，没有具体账户身份
    setupRequired: false,    // 零用户状态，需要先创建管理员
    setupAllowed: false,     // 当前请求是否来自本机（只有本机能创建第一个管理员）
    user: null,              // { username, role, totp_enabled, ... }
    totpEnabledGlobal: false,
    totpRequired: false,
})

export const isAdmin = computed(() =>
    // 内网免鉴权的请求没有账户身份，服务端视同管理员，前端保持一致
    authState.bypassed || authState.user?.role === 'admin'
)

/** 是否需要展示登录界面 */
export const needsLogin = computed(() =>
    authState.authEnabled && !authState.authenticated && !authState.bypassed
)

function applyState(data) {
    authState.authEnabled = !!data.auth_enabled
    authState.authenticated = !!data.authenticated
    authState.bypassed = !!data.bypassed
    authState.setupRequired = !!data.setup_required
    authState.setupAllowed = !!data.setup_allowed
    authState.user = data.user || null
    authState.totpEnabledGlobal = !!data.totp_enabled_global
    authState.totpRequired = !!data.totp_required
    authState.ready = true
}

// 单飞（single-flight）：路由守卫、组件、401 拦截器可能同时要求刷新状态，
// 只发一次请求，其余等这一次的结果。
let inflight = null

export async function recheck(force = false) {
    if (!force && inflight) return inflight
    inflight = authApi.getAuthState()
        .then(data => {
            applyState(data)
            return data
        })
        .catch(err => {
            // 拿不到状态时按"未登录"处理，但不假设鉴权是关的 ——
            // 否则服务端临时抖动就会让前端以为不需要登录
            authState.ready = true
            authState.authenticated = false
            throw err
        })
        .finally(() => {
            inflight = null
        })
    return inflight
}

export async function doLogin(username, password) {
    const res = await authApi.login(username, password)
    if (res?.totp_required) {
        return {totpRequired: true}
    }
    await recheck(true)
    onLoggedIn()
    return {totpRequired: false}
}

export async function doLoginTOTP(code) {
    await authApi.loginTOTP(code)
    await recheck(true)
    onLoggedIn()
}

export async function doLogout() {
    try {
        await authApi.logout()
    } finally {
        authState.authenticated = false
        authState.user = null
    }
}

/** 401 拦截器调用：把状态标成未登录，并停掉实时连接 */
export function setUnauthenticated() {
    authState.authenticated = false
    authState.user = null
}

// 登录成功后需要重新拉起实时连接。用回调注册而不是直接 import wsManager，
// 避免 authStore ←→ wsManager 循环依赖。
const loginHooks = []

export function onLoginSuccess(fn) {
    loginHooks.push(fn)
}

function onLoggedIn() {
    loginHooks.forEach(fn => {
        try {
            fn()
        } catch (e) {
            console.error('[auth] 登录后回调执行失败:', e)
        }
    })
}
