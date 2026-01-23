import axios from 'axios'

export const API_BASE_URL = import.meta.env.VITE_API_ROOT

// 创建 axios 实例
const apiClient = axios.create({
    baseURL: API_BASE_URL,
    headers: {
        'Content-Type': 'application/json',
    }
})

// 请求拦截器
apiClient.interceptors.request.use(
    config => {
        // 可以在这里添加 token 等
        return config
    },
    error => {
        return Promise.reject(error)
    }
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
            const errorData = error.response.data
            // 尝试获取错误信息
            const errorMessage = errorData.message || errorData.error || `HTTP error! status: ${error.response.status}`
            return Promise.reject(new Error(errorMessage))
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
