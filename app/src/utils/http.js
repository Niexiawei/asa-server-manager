import axios from 'axios'

export const API_BASE_URL = import.meta.env.VITE_API_ROOT

// 创建 axios 实例
const apiClient = axios.create({
    baseURL: API_BASE_URL,
    // 会话凭证是 HttpOnly Cookie，跨域时必须显式允许携带
    withCredentials: true,
    headers: {
        'Content-Type': 'application/json',
        // 跨站的 HTML 表单设不了这个头，带上它会触发 CORS 预检，
        // 于是构成一道 CSRF 防线（另一道是 Cookie 的 SameSite=Lax）
        'X-Requested-With': 'XMLHttpRequest',
    }
})

// 401 时跳转登录页。用一个标志位去重：
// 页面刚打开时往往有十几个请求并发，会话失效会让它们同时返回 401，
// 不去重的话会连续触发十几次跳转。
let redirecting = false

function handleUnauthorized(code) {
    if (redirecting) return
    redirecting = true

    // 动态 import 打断 http.js ←→ authStore ←→ api 的循环依赖
    Promise.all([
        import('@/store/authStore.js'),
        import('@/router/index.js'),
    ]).then(([{setUnauthenticated, recheck}, {default: router}]) => {
        setUnauthenticated()
        // 重新问一次服务端：可能是零用户状态（要去 /setup）而不是单纯没登录
        return recheck(true).catch(() => {
        }).then(() => {
            const target = code === 'setup_required' ? '/setup' : '/login'
            if (router.currentRoute.value.path !== target) {
                const from = router.currentRoute.value.fullPath
                router.replace({
                    path: target,
                    query: target === '/login' && from !== '/login' ? {redirect: from} : undefined,
                })
            }
        })
    }).finally(() => {
        // 留一点窗口，避免同一批并发请求反复触发
        setTimeout(() => {
            redirecting = false
        }, 500)
    })
}

// 请求拦截器
apiClient.interceptors.request.use(
    config => config,
    error => Promise.reject(error)
)

// 响应拦截器
apiClient.interceptors.response.use(
    response => {
        // 直接返回数据部分
        return response.data
    },
    async error => {
        if (error.response) {
            // 服务器返回了状态码，但不在 2xx 范围内
            const errorData = error.response.data || {}

            if (error.response.status === 401) {
                handleUnauthorized(errorData.code)
            }

            // 尝试获取错误信息
            const errorMessage = errorData.message || errorData.error || `HTTP error! status: ${error.response.status}`
            const err = new Error(errorMessage)
            err.status = error.response.status
            err.code = errorData.code
            return Promise.reject(err)
        } else if (error.request) {
            // 请求已发出，但没有收到响应
            return Promise.reject(new Error('Network Error: No response received'))
        } else {
            // 设置请求时发生错误
            return Promise.reject(error)
        }
    }
)

export default apiClient
