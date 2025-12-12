<template>
  <a-card :title="headerDisable ? null :'RCON终端'" class="rcon-terminal-card"
          :bordered="false"
  >
    <a-space style="margin-bottom: 15px">
      <a-tag :color="rconConnected ? 'green' : 'gray'">
        {{ rconConnected ? '已连接' : '未连接' }}
      </a-tag>
    </a-space>

    <div v-if="rconConnected" class="rcon-terminal-wrapper">
      <vue-web-terminal
          name="ASAServerRcon"
          ref="rconTerminalRef"
          :context="`AsaServer-${instanceName}`"
          @exec-cmd="handleRCONCommand"
          :show-header="false"
      />
    </div>
    <div v-else class="rcon-disconnected-tip">
      <a-empty description="请刷新页面或在 Game.ini 中配置 ServerAdminPassword"/>
    </div>
  </a-card>
</template>

<script setup>
import {ref, onUnmounted, computed, onMounted} from 'vue'
import VueWebTerminal from 'vue-web-terminal'
import {TerminalApi} from 'vue-web-terminal';
import {Message} from '@arco-design/web-vue'
import {
  connectRCONWebSocket,
  disconnectRCONWebSocket,
  sendRCONCommandViaWebSocket,
  onRCONMessage
} from '@/store/rconStore.js'

const props = defineProps({
  instanceName: {
    type: String,
    required: true
  },
  instanceRunning: {
    type: Boolean,
    default: false
  },
  headerDisable: {
    type: Boolean,
    default: false
  }
})

const terminalId = "ASAServerRcon"

// RCON 相关
const rconTerminalRef = ref(null)
const rconConnected = ref(false)
let unlistenRCONMessage = null
let pendingCommandCallback = null  // 存储待执行的命令回调
let rconOnOpen = null  // 缓存 onOpen 回调
let rconOnError = null  // 缓存 onError 回调
let rconOnClose = null  // 缓存 onClose 回调

// 连接 RCON
const initRCON = () => {
  // 定义回调函数
  rconOnOpen = () => {
    rconConnected.value = true
    Message.success('RCON 已连接')
    // 监听 RCON 消息
    unlistenRCONMessage = onRCONMessage((message) => {
      console.log('RCON message:', message)
      handleRCONMessage(message)
    })
  }

  rconOnError = (error) => {
    Message.error('RCON 连接失败')
    console.error('RCON connection error:', error)
  }

  rconOnClose = () => {
    rconConnected.value = false
    console.log('RCON disconnected')
  }

  connectRCONWebSocket(rconOnOpen, rconOnError, rconOnClose)
}

// 处理 RCON 命令执行
const handleRCONCommand = (key, command, success, failed) => {
  console.log(key, command)
  console.log('Executing RCON command:', command)
  // 保存成功回调，等待响应后执行
  pendingCommandCallback = success
  sendRCONCommandViaWebSocket('command', props.instanceName, command)
}

// 处理 RCON 消息响应
const handleRCONMessage = (message) => {
  if (!rconTerminalRef.value) {
    return
  }

  // 根据消息类型处理
  if (message.success === false) {
    // 错误消息
    if (message.error) {
      rconTerminalRef.value.print(message.error)
    } else if (message.message) {
      rconTerminalRef.value.print(message.message)
    }
  } else {
    // 成功消息
    if (message.response) {
      TerminalApi.pushMessage(terminalId, {
        type: "normal",
        class: 'success',
        tag: '',
        content: message.response
      })
    } else if (message.message) {
      TerminalApi.pushMessage(terminalId, {
        type: "normal",
        class: 'success',
        tag: '',
        content: message.message
      })
    }
  }

  // 收到回复后执行待执行的命令回调
  if (pendingCommandCallback) {
    pendingCommandCallback()
    pendingCommandCallback = null
  }
}

onMounted(() => {
  // 自动初始化 RCON 连接
  console.log("init Rcon")
  initRCON()
})

onUnmounted(() => {
  // 断开 RCON 连接
  console.log("disconnect Rcon")
  // 停止重连和断开连接
  disconnectRCONWebSocket()
})
</script>

<style scoped lang="less">
.rcon-terminal-card {
  height: 100%;

  :deep(.arco-card-body) {
    height: 100%;
    box-sizing: border-box;
    display: flex;
    flex-direction: column;
  }
}

.rcon-terminal-wrapper {
  flex: 1;
  display: flex;
  flex-direction: column;
  width: 100%;
  box-sizing: border-box;
  height: calc(100% - 40px);
  border: 1px solid #e5e5e5;
  border-radius: 4px;
  background-color: #1e1e1e;
}

.rcon-disconnected-tip {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 400px;
  background-color: #f5f5f5;
  border-radius: 4px;
}
</style>
