import { createRouter, createWebHistory } from 'vue-router'
import ServerManager from '../components/ServerManager.vue'
import ServerControl from '../components/ServerControl.vue'
import APIDocs from '../components/APIDocs.vue'

const routes = [
  {
    path: '/',
    name: 'ServerManager',
    component: ServerManager
  },
  {
    path: '/control',
    name: 'ServerControl',
    component: ServerControl
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