import {createRouter, createWebHashHistory, createWebHistory} from 'vue-router'
import ServerManager from '@/views/ServerManager.vue'
import InstanceDetail from '@/views/InstanceDetail.vue'
import ServerControl from '@/views/ServerController/ServerControl.vue'
import SystemLogs from '@/views/SystemLogs.vue'
import FRPManager from '@/views/FRPManager.vue'

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
    }
]

const router = createRouter({
    history: createWebHashHistory(),
    routes
})

export default router