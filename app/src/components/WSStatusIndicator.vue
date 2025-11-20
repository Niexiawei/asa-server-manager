<template>
  <div class="ws-status-indicator">
    <!-- 连接成功 -->
    <div v-if="serverStore.connected" class="ws-connected">
      <a-badge color="green" text="实时同步已连接" />
    </div>
    
    <!-- 连接失败或断开 -->
    <div v-else class="ws-disconnected" style="width: 100%">
      <a-alert 
        type="error" 
        :title="serverStore.connectionError ? '实时同步连接失败' : '实时同步已断开连接'"
        closable
      >
        <template #extra>
          <a-button 
            type="primary" 
            size="small"
            @click="handleReconnect"
            :loading="isReconnecting"
          >
            {{ isReconnecting ? '重连中...' : '重新连接' }}
          </a-button>
        </template>
      </a-alert>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { serverStore, initializeWebSocket } from '../store/serverStore.js'
import { Message } from '@arco-design/web-vue'

const isReconnecting = ref(false)

const handleReconnect = async () => {
  isReconnecting.value = true
  try {
    await initializeWebSocket()
    Message.success('已重新连接')
  } catch (error) {
    Message.error('重连失败，请稍后重试')
    console.error('重连失败:', error)
  } finally {
    isReconnecting.value = false
  }
}
</script>

<style scoped>
.ws-status-indicator {
  margin-bottom: 16px;
  width: 100%;
  display: flex;
  align-items: center;
}

.ws-connected {
  width: 100%;
  padding: 8px 16px;
  background-color: #f6ffed;
  border-radius: 4px;
  border-left: 4px solid #52c41a;
  display: flex;
  align-items: center;
  height: 32px;
}

.ws-disconnected {
  width: 100%;
  /* alert 组件会处理样式 */
}
</style>
