import {createRouter, createWebHashHistory} from 'vue-router'
import ServerManager from '@/views/ServerManager.vue'
import InstanceDetail from '@/views/InstanceDetail/index.vue'
import SystemLogs from '@/views/SystemLogs.vue'
import ServerResourceMonitor from '@/views/ServerResourceMonitor/index.vue'
import FRPManager from '@/views/FRPManager.vue'
import {authState, isAdmin, recheck} from '@/store/authStore.js'

const routes = [
    {
        path: '/',
        name: 'ServerManager',
        component: ServerManager
    },
    {
        path: '/instance/:name',
        name: 'InstanceDetail',
        component: InstanceDetail
    },
    {
        path: '/server-resource',
        name: 'ServerResourceMonitor',
        component: ServerResourceMonitor
    },
    {
        path: '/system-logs',
        name: 'SystemLogs',
        component: SystemLogs
    },
    {
        path: '/frp-manager',
        name: 'FRPManager',
        component: FRPManager
    },
    {
        path: '/syncthing-manager',
        name: 'SyncthingManager',
        component: () => import('@/views/SyncthingManager.vue'),
    },
    {
        path: '/schedule-manager',
        name: 'ScheduleManager',
        component: () => import('@/views/ScheduleManager.vue'),
    },
    // 鉴权相关页面。standalone 表示它们自带整页布局，不套在主框架里。
    {
        path: '/login',
        name: 'Login',
        component: () => import('@/views/Login.vue'),
        meta: {standalone: true, public: true},
    },
    {
        path: '/setup',
        name: 'Setup',
        component: () => import('@/views/Setup.vue'),
        meta: {standalone: true, public: true},
    },
    {
        path: '/profile',
        name: 'Profile',
        component: () => import('@/views/Profile.vue'),
    },
    {
        path: '/user-manager',
        name: 'UserManager',
        component: () => import('@/views/UserManager.vue'),
        meta: {requiresAdmin: true},
    },
]

const router = createRouter({
    history: createWebHashHistory(),
    routes
})

router.beforeEach(async (to) => {
    // 首次进入先问一次服务端：要不要登录、我是谁。
    // recheck 内部做了单飞，多个并发导航只会发一次请求。
    if (!authState.ready) {
        await recheck().catch(() => {
        })
    }

    // 没开鉴权，或走内网免鉴权 —— 一切照旧
    if (!authState.authEnabled || authState.bypassed) {
        // 这两种情况下登录/引导页没有意义，直接回首页
        if (to.meta?.public) return {path: '/'}
        return true
    }

    // 零用户状态：先创建管理员
    if (authState.setupRequired) {
        return to.name === 'Setup' ? true : {path: '/setup'}
    }
    if (to.name === 'Setup') return {path: authState.authenticated ? '/' : '/login'}

    if (!authState.authenticated) {
        if (to.name === 'Login') return true
        return {path: '/login', query: to.fullPath !== '/' ? {redirect: to.fullPath} : undefined}
    }

    // 已登录就不该再看到登录页
    if (to.name === 'Login') return {path: '/'}

    if (to.meta?.requiresAdmin && !isAdmin.value) {
        return {path: '/'}
    }
    return true
})

export default router
