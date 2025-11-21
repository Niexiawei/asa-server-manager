<template>
  <div class="log-viewer">
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
      <span style="font-size: 14px">
        <a-badge :color="isStreaming ? 'green' : 'gray'"/>
        {{ isStreaming ? '监听中' : '已停止' }}
      </span>
      <span style="font-size: 14px">日志行数: {{ logs.length }}</span>
    </a-space>

    <div class="log-container">
      <div class="log-content" ref="logContentRef">
        <div
            v-for="(log, index) in logs"
            :key="index"
            class="log-line"
        >
          <span class="log-number">{{ index + 1 }}</span>
          <span class="log-text">{{ log }}</span>
        </div>
        <div v-if="logs.length === 0" class="empty-logs">
          <a-empty description="暂无日志"/>
        </div>
      </div>
      <div ref="logEndRef"></div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { streamInstanceLogs } from '@/apis/api.js'

const props = defineProps({
  instanceName: {
    type: String,
    required: true
  }
})

const logs = ref([])
const isStreaming = ref(false)
const logContentRef = ref(null)
const logEndRef = ref(null)
let stopLogStream_func = null

// 开始监听日志
const startLogStream = () => {
  isStreaming.value = true
  logs.value = []

  stopLogStream_func = streamInstanceLogs(
      props.instanceName,
      // onLog 回调
      (line) => {
        logs.value.push(line)
        // 自动滚动到底部
        nextTick(() => {
          if (logContentRef.value) {
            logContentRef.value.scrollTop = logContentRef.value.scrollHeight
          }
        })
      },
      // onError 回调
      (error) => {
        console.error('日志流错误:', error)
      },
      // onClose 回调
      () => {
        isStreaming.value = false
      }
  )
}

// 停止监听日志
const stopLogStream = () => {
  if (stopLogStream_func) {
    stopLogStream_func()
    stopLogStream_func = null
  }
  isStreaming.value = false
}

// 清空日志
const clearLogs = () => {
  logs.value = []
}

// 组件卸载时停止监听
onUnmounted(() => {
  if (isStreaming.value) {
    stopLogStream()
  }
})
</script>

<style scoped>
.log-viewer {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.log-container {
  border: 1px solid #d9d9d9;
  border-radius: 4px;
  background-color: #fafafa;
  overflow: hidden;
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.log-content {
  flex: 1;
  overflow-y: auto;
  padding: 10px;
  font-family: 'Courier New', monospace;
  font-size: 12px;
  background-color: #1f1f1f;
  color: #e0e0e0;
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

.empty-logs {
  display: flex;
  justify-content: center;
  align-items: center;
  height: 100%;
  color: #999;
}

/* 滚动条样式 */
.log-content::-webkit-scrollbar {
  width: 8px;
}

.log-content::-webkit-scrollbar-track {
  background: #2a2a2a;
}

.log-content::-webkit-scrollbar-thumb {
  background: #555;
  border-radius: 4px;
}

.log-content::-webkit-scrollbar-thumb:hover {
  background: #777;
}
</style>
