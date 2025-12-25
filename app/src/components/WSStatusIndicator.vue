<template>
  <a-popover
      trigger="click"
      :content-style="{ padding: '0' }"
  >
    <template #title>
      <div class="ws-status-title">
        <span>WebSocket状态</span>
      </div>
    </template>
    <template #content>
      <div class="ws-status-content">
        <div class="ws-status-item">
          <a-tag size="large" :color="wsConnected ? 'green' : 'red'">
            {{ wsConnected ? '已连接' : '已断开' }}
          </a-tag>
        </div>
        <div class="ws-status-actions">
          <a-button
              size="mini"
              :disabled="wsConnected"
              @click="reconnect"
              :loading="reconnecting"
          >
            重连
          </a-button>
        </div>
      </div>
    </template>

    <!-- 状态指示器 -->
    <div class="ws-status-indicator" :class="{ connected: wsConnected, disconnected: !wsConnected }">
      <div class="status-dot" :style="{ backgroundColor: wsConnected ? '#52c41a' : '#f53f3f' }"></div>
      <span class="status-text">{{ wsConnected ? '已连接' : '已断开' }}</span>
    </div>
  </a-popover>
</template>

<script setup>
import {ref, onMounted, onUnmounted} from 'vue'
import {wsConnected, reconnectWS} from '@/utils/wsManager.js'

const reconnecting = ref(false)

const reconnect = async () => {
  if (reconnecting.value) return

  reconnecting.value = true
  try {
    await reconnectWS()
  } catch (error) {
    console.error('WebSocket 重连失败:', error)
  } finally {
    reconnecting.value = false
  }
}

onMounted(() => {
  // 组件挂载时不需要特殊处理，wsConnected 会自动响应式更新
})

onUnmounted(() => {
  // 组件卸载时不需要特殊处理
})
</script>

<style scoped lang="less">
.ws-status-indicator {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 8px;
  border-radius: 4px;
  cursor: pointer;
  user-select: none;
}

.ws-status-indicator.connected {
  background-color: #f6ffed;
  border: 1px solid #b7eb8f;
}

.ws-status-indicator.disconnected {
  background-color: #fff2f0;
  border: 1px solid #ffccc7;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.status-text {
  font-size: 12px;
  color: #666;
}

.ws-status-title {
  border-bottom: 1px solid #e8e8e8;
  text-align: center;
  padding: 6px 0;
}

.ws-status-content {
  padding: 12px;
  width: 120px;

  > div {
    width: 100%;
  }
}

.ws-status-item {
  margin-bottom: 12px;

  :deep(.arco-tag) {
    width: 100%;
    text-align: center;
    display: inline-block;
  }
}

.ws-status-actions {
  display: flex;
  justify-content: flex-end;
}
</style>