<template>
  <!-- 状态指示器 -->
  <div class="ws-status-indicator">
    <t-tooltip>
      <template #content>
        {{wsConnected ? 'WebSocket连接正常':'WebSocket连接断开'}}
      </template>
      <t-button variant="text" :loading="reconnecting"
                @click="reconnect"
      >
        <template #icon>
          <LinkIcon v-if="wsConnected"
                    stroke-color="#56c08d"
                    size="20px"
          />
          <LinkUnlinkIcon v-else
                          stroke-color="#f6685d"
                          size="20px"
          />
        </template>
      </t-button>
    </t-tooltip>
  </div>
</template>

<script setup>
import {ref, onMounted, onUnmounted} from 'vue'
import {wsConnected, reconnectWS} from '@/utils/wsManager.js'
import {LinkIcon, LinkUnlinkIcon} from "tdesign-icons-vue-next";

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

}
</style>
