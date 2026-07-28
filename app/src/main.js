import {createApp} from 'vue'
import '@/style.css'
import App from '@/App.vue'
import router from '@/router'
import {initializeWebSocket} from '@/store/serverStore.js'
import {needsLogin, onLoginSuccess, recheck} from '@/store/authStore.js'
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

function startWebSocket() {
    initializeWebSocket().then(success => {
        if (success) {
            console.log('WebSocket initialized successfully')
        } else {
            console.warn('Failed to initialize WebSocket connection')
        }
    }).catch(err => {
        console.error('Error initializing WebSocket:', err)
    })
}

// 未登录时**不发起** WebSocket 连接。
//
// 这是避免重连风暴的第一道也是最有效的一道措施：没登录就连，服务端只会
// 一路拒绝，而客户端把它当网络故障不停重试，每次都是一次完整的 TLS 握手。
// 与其靠重连策略去兜底，不如一开始就不连。
recheck()
    .catch(() => { /* 拿不到状态时按未登录处理，交给路由守卫 */ })
    .finally(() => {
        if (!needsLogin.value) {
            startWebSocket()
        }
    })

// 登录成功后再拉起实时连接
onLoginSuccess(startWebSocket)

app.mount('#app');
