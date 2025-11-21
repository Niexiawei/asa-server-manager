import { createApp } from 'vue'
import '@/style.css'
import App from '@/App.vue'
import router from '@/router'
import { initializeWebSocket } from '@/store/serverStore.js'

// 引入 arco-design 组件库
import ArcoVue from '@arco-design/web-vue';
import ArcoVueIcon from '@arco-design/web-vue/es/icon';
import '@arco-design/web-vue/dist/arco.css';
import {createTerminal} from "vue-web-terminal";

const terminal = createTerminal()
//  default is 'terminal'
terminal.configStoreName('asa-server-terminal')

const app = createApp(App);
app.use(terminal)
app.use(router);
app.use(ArcoVue);
app.use(ArcoVueIcon);

// 初始化 WebSocket 连接
initializeWebSocket().then(success => {
  if (success) {
    console.log('WebSocket initialized successfully')
  } else {
    console.warn('Failed to initialize WebSocket connection')
  }
}).catch(err => {
  console.error('Error initializing WebSocket:', err)
})

app.mount('#app');