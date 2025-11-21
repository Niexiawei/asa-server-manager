<template>
  <a-card title="RCON 交互式终端" class="rcon-terminal-card">
    <a-space style="margin-bottom: 15px">
      <a-button
          @click="connectRCON"
          :loading="rconConnecting"
          :disabled="rconConnected || !instanceRunning"
          type="primary"
      >
        连接 RCON
      </a-button>
      <a-button
          @click="disconnectRCON"
          :disabled="!rconConnected"
          status="warning"
      >
        断开连接
      </a-button>
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
      <a-empty description="点击上方按钮连接 RCON 服务器"/>
    </div>
  </a-card>
</template>

<script setup>
import {ref, onUnmounted, computed} from 'vue'
import VueWebTerminal from 'vue-web-terminal'
import {TerminalApi} from 'vue-web-terminal';
import {Message} from '@arco-design/web-vue'
import {
  connectRCONWebSocket,
  disconnectRCONWebSocket,
  sendRCONCommandViaWebSocket,
  onRCONMessage
} from '@/utils/wsManager.js'

const props = defineProps({
  instanceName: {
    type: String,
    required: true
  },
  instanceRunning: {
    type: Boolean,
    default: false
  }
})

const terminalId = "ASAServerRcon"

// RCON 相关
const rconTerminalRef = ref(null)
const rconConnected = ref(false)
const rconConnecting = ref(false)
const rconWelcomeMessage = 'RCON 交互式终端 - 请输入命令'
let unlistenRCONMessage = null

// 连接 RCON
const connectRCON = () => {
  rconConnecting.value = true
  connectRCONWebSocket(
      () => {
        rconConnected.value = true
        rconConnecting.value = false
        Message.success('RCON 已连接')
        // 发送 connect 命令
        sendRCONCommandViaWebSocket('connect', props.instanceName)
        // 监听 RCON 消息
        unlistenRCONMessage = onRCONMessage((message) => {
          console.log('RCON message:', message)
          handleRCONMessage(message)
        })
      },
      (error) => {
        rconConnecting.value = false
        Message.error('RCON 连接失败')
        console.error('RCON connection error:', error)
      },
      () => {
        rconConnected.value = false
        console.log('RCON disconnected')
      }
  )
}

// 断开 RCON
const disconnectRCON = () => {
  if (unlistenRCONMessage) {
    unlistenRCONMessage()
  }
  disconnectRCONWebSocket()
  rconConnected.value = false
  Message.success('RCON 已断开')
}

// 处理 RCON 命令执行
const handleRCONCommand = (key, command, success, failed) => {
  console.log(key, command)
  console.log('Executing RCON command:', command)
  sendRCONCommandViaWebSocket('command', props.instanceName, key)
  success()
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
      TerminalApi.push
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
      // rconTerminalRef.value.print(message.response)
    } else if (message.message) {
      TerminalApi.pushMessage(terminalId, {
        type: "normal",
        class: 'success',
        tag: '',
        content: message.message
      })
      // rconTerminalRef.value.print(message.message)
    }
  }
}

onUnmounted(() => {
  // 断开 RCON 连接
  if (unlistenRCONMessage) {
    unlistenRCONMessage()
  }
  if (rconConnected.value) {
    disconnectRCONWebSocket()
  }
})
</script>

<style scoped lang="less">
.rcon-terminal-card {
  height: 500px !important;
  margin-top: 20px;

  :deep(.arco-card-body) {
    height: calc(100% - 45.5px);
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
  min-height: 400px;
  border: 1px solid #e5e5e5;
  border-radius: 4px;
  background-color: #1e1e1e;
  overflow: hidden;
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
