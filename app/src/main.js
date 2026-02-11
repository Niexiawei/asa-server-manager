import {createApp} from 'vue'
import '@/style.css'
import App from '@/App.vue'
import router from '@/router'
import {initializeWebSocket} from '@/store/serverStore.js'
import {createTerminal} from "vue-web-terminal";
import '@/assets/scrollbar.css'
import TDesign from 'tdesign-vue-next';
import 'tdesign-vue-next/es/style/index.css';
import "@/app.less"


const terminal = createTerminal()
//  default is 'terminal'
terminal.configStoreName('asa-server-terminal')

const app = createApp(App);
app.use(terminal)
app.use(router);
app.use(TDesign)

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
