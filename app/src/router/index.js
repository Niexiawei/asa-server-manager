import { createRouter, createWebHistory } from 'vue-router'
import ServerManager from '../views/ServerManager.vue'
import InstanceDetail from '../views/InstanceDetail.vue'
import ServerControl from '../views/ServerControl.vue'

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
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

export default router