import apiClient from '@/utils/http.js'

// 鉴权相关接口。
// 凭证走 HttpOnly Cookie，前端拿不到也不需要拿 —— 所有请求由浏览器自动带上，
// SSE 和 WebSocket 也一样（它们无法设置自定义请求头，这正是选 Cookie 的原因）。

/** 应用启动时问的第一个问题：要不要登录、我是谁、有哪些登录方式可用 */
export const getAuthState = () => apiClient.get('/api/auth/state')

/** 密码登录。返回 { totp_required: true } 表示还需要第二步 */
export const login = (username, password) =>
    apiClient.post('/api/auth/login', {username, password})

/** 两步验证第二步，code 可以是 6 位验证码或恢复码 */
export const loginTOTP = (code) => apiClient.post('/api/auth/login/totp', {code})

export const logout = () => apiClient.post('/api/auth/logout')
export const logoutAll = () => apiClient.post('/api/auth/logout-all')

/** 首次引导：创建第一个管理员（仅本机可调用） */
export const setupAdmin = (username, password) =>
    apiClient.post('/api/auth/setup', {username, password})

export const changePassword = (oldPassword, newPassword) =>
    apiClient.post('/api/auth/password', {old_password: oldPassword, new_password: newPassword})

// ---- 两步验证 ----

/** 生成密钥与二维码。此时还没落库，要等 confirm 通过才真正启用 */
export const totpSetup = () => apiClient.post('/api/auth/totp/setup')

/** 提交验证码确认绑定，成功后返回一次性恢复码（明文只此一次） */
export const totpConfirm = (code) => apiClient.post('/api/auth/totp/confirm', {code})

/** 解绑需要同时提供密码和验证码 */
export const totpDisable = (password, code) =>
    apiClient.post('/api/auth/totp/disable', {password, code})

export const regenerateRecoveryCodes = () =>
    apiClient.post('/api/auth/totp/recovery/regenerate')

// ---- 用户管理（仅管理员）----

export const listUsers = () => apiClient.get('/api/users')
export const createUser = (username, password, role) =>
    apiClient.post('/api/users', {username, password, role})
export const updateUser = (username, payload) =>
    apiClient.put(`/api/users/${encodeURIComponent(username)}`, payload)
export const deleteUser = (username) =>
    apiClient.delete(`/api/users/${encodeURIComponent(username)}`)
export const resetUserPassword = (username, password) =>
    apiClient.post(`/api/users/${encodeURIComponent(username)}/password`, {password})
export const resetUserTOTP = (username) =>
    apiClient.post(`/api/users/${encodeURIComponent(username)}/totp/reset`)
export const unlockUser = (username) =>
    apiClient.post(`/api/users/${encodeURIComponent(username)}/unlock`)

/** 审计日志。对一个暴露在公网的面板来说，这是排查"是不是被爆破了"的唯一手段 */
export const getAuditLog = (params = {}) => apiClient.get('/api/auth/audit', {params})
