import {createRouter, createWebHashHistory, createWebHistory} from 'vue-router'
import ServerManager from '@/views/ServerManager.vue'
import InstanceDetail from '@/views/InstanceDetail.vue'
import ServerControl from '@/views/ServerController/ServerControl.vue'
import SystemLogs from '@/views/SystemLogs.vue'

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
        path: '/control',
        name: 'ServerControl',
        component: ServerControl
    },
    {
        path: '/system-logs',
        name: 'SystemLogs',
        component: SystemLogs
    }
]

const router = createRouter({
    history: createWebHashHistory(),
    routes
})

export default router