<template>
  <div class="system-logs">
    <!-- WebSocket 连接状态指示器 -->
    <WSStatusIndicator/>

    <a-card class="logs-card" :bordered="false">
      <template #title>
        <div class="logs-header">
          <span class="page-title">系统日志</span>
        </div>
      </template>

      <a-space style="margin-bottom: 15px">
        <a-button
            @click="startLogStream"
            type="primary"
            :disabled="isStreaming"
        >
          {{ isStreaming ? '监听中...' : '开始监听' }}
        </a-button>
        <a-button
            @click="stopLogStream"
            status="warning"
            :disabled="!isStreaming"
        >
          停止监听
        </a-button>
        <a-button
            @click="clearLogs"
            :disabled="logs.length === 0"
        >
          清空日志
        </a-button>
        <a-divider direction="vertical"/>
        <a-badge :color="isStreaming ? 'green' : 'gray'"
                 :text="isStreaming ? '监听中' : '已停止'"
        />
        <span style="font-size: 16px">日志行数: {{ logs.length }}</span>
      </a-space>

      <div class="log-viewer">
        <div class="log-container" ref="logContainer">
          <div
              v-for="(log, index) in logs"
              :key="index"
              class="log-line"
          >
            <span class="log-number">{{ index + 1 }}</span>
            <span class="log-text">{{ log }}</span>
          </div>
          <div v-if="logs.length === 0" class="log-empty">
            暂无日志。点击"开始监听"按钮开始实时查看系统日志。
          </div>
        </div>
      </div>
    </a-card>
  </div>
</template>

<script setup>
import {ref, onMounted, onBeforeUnmount, nextTick} from 'vue'
import {streamSystemLogs} from '@/apis/api'
import WSStatusIndicator from '@/components/WSStatusIndicator.vue'
import {IconLeft} from '@arco-design/web-vue/es/icon'

const logs = ref([])
const isStreaming = ref(false)
const logContainer = ref(null)
let stopStreamFn = null

// 开始监听日志
const startLogStream = () => {
  if (isStreaming.value) return
  isStreaming.value = true
  stopStreamFn = streamSystemLogs(
      (log) => {
        // 每次添加新日志后自动滚动到底部
        logs.value.push(log)
        nextTick(() => {
          if (logContainer.value) {
            logContainer.value.scrollTop = logContainer.value.scrollHeight
          }
        })
      },
      (error) => {
        console.error('日志流错误:', error)
        isStreaming.value = false
      },
      () => {
        console.log('日志流已关闭')
        isStreaming.value = false
      }
  )
}

// 停止监听日志
const stopLogStream = () => {
  if (stopStreamFn) {
    stopStreamFn()
    stopStreamFn = null
  }
  isStreaming.value = false
}

// 清空日志
const clearLogs = () => {
  logs.value = []
}

// 组件卸载时停止监听
onBeforeUnmount(() => {
  if (stopStreamFn) {
    stopStreamFn()
  }
})
</script>

<style scoped lang="less">
.system-logs {
  height: 100%;
  width: 100%;
}

:deep(.arco-badge-status-text) {
  line-height: 16px !important;
  font-size: 16px;
  color: var(--color-text-2);
}

.logs-card {
  border-radius: var(--border-radius-large);
  height: calc(100% - 64px);

  :deep(.arco-card-body) {
    height: calc(100% - 42px);
    box-sizing: border-box;
  }
}

.logs-header {
  display: flex;
  align-items: center;
  gap: 12px;
}

.page-title {
  font-size: 18px;
  font-weight: 500;
  color: var(--color-text-1);
}

.log-viewer {
  position: relative;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  background-color: #1a1a1a;
  height: calc(100% - 54px);
}

.log-container {
  height: 100%;
  overflow-y: auto;
  padding: 15px;
  box-sizing: border-box;
  font-family: 'Courier New', monospace;
  font-size: 14px;
  color: var(--color-white);
  white-space: pre-wrap;
  word-wrap: break-word;
  line-height: 1.5;
}

.log-line {
  display: flex;
  margin-bottom: 2px;
  white-space: pre-wrap;
  word-break: break-word;
  line-height: 1.5;
}

.log-number {
  display: inline-block;
  min-width: 50px;
  margin-right: 10px;
  color: #888;
  user-select: none;
  flex-shrink: 0;
}

.log-text {
  flex: 1;
  color: #e0e0e0;
}

.log-empty {
  text-align: center;
  color: #666;
  padding: 40px;
  font-size: 14px;
}

.log-loading {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
}

/* 滚动条样式 */
.log-container::-webkit-scrollbar {
  width: 8px;
}

.log-container::-webkit-scrollbar-track {
  background: #2a2a2a;
}

.log-container::-webkit-scrollbar-thumb {
  background: #555;
  border-radius: 4px;
}

.log-container::-webkit-scrollbar-thumb:hover {
  background: #777;
}
</style>
