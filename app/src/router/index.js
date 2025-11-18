import { createRouter, createWebHistory } from 'vue-router'
import ServerManager from '../components/ServerManager.vue'
import APIDocs from '../components/APIDocs.vue'

const routes = [
  {
    path: '/',
    name: 'ServerManager',
    component: ServerManager
  },
  {
    path: '/api-docs',
    name: 'APIDocs',
    component: APIDocs
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

export default router