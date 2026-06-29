<template>
  <t-card class="system-logs logs-card layout-card" :bordered="false">
    <template #title>
      <div class="logs-header">
        <span class="page-title">系统日志</span>
      </div>
    </template>

    <t-space style="margin-bottom: 15px" align="center">
      <t-button
          @click="startLogStream"
          theme="primary"
          :disabled="isStreaming"
      >
        {{ isStreaming ? '监听中...' : '开始监听' }}
      </t-button>
      <t-button
          @click="stopLogStream"
          theme="warning"
          :disabled="!isStreaming"
      >
        停止监听
      </t-button>
      <t-button
          @click="clearLogs"
          :disabled="(vllRef?.itemCount ?? 0) === 0"
      >
        清空日志
      </t-button>
      <t-divider layout="vertical" style="height: 30px"/>
      <t-tag :color="isStreaming ? 'green' : 'gray'">
        {{ isStreaming ? '监听中' : '已停止' }}
      </t-tag>
      <span style="font-size: 16px">日志行数: {{ vllRef?.itemCount ?? 0 }}</span>
    </t-space>

    <div class="log-viewer">
      <VirtualLogList
          ref="vllRef"
          class="log-vll"
          :estimated-item-height="28"
          :buffer="400"
      >
        <template #item="{ item, index }">
          <div class="log-line" :class="`log-level-${item.level}`">
            <span class="log-number">{{ index + 1 }}</span>
            <span class="log-time">{{ item.time }}</span>
            <span class="log-level" :class="`level-${item.level}`">{{ item.level }}</span>
            <span class="log-text">{{ item.msg }}</span>
          </div>
        </template>
        <template #empty>
          <div class="log-empty">
            暂无日志。{{ isStreaming ? '' : '点击"开始监听"按钮开始实时查看系统日志。' }}
          </div>
        </template>
      </VirtualLogList>
    </div>
  </t-card>
</template>

<script setup>
defineOptions({
  name: 'SystemLogs'
})
import {ref, onBeforeUnmount, onActivated, nextTick} from 'vue'
import dayjs from 'dayjs'
import {streamSystemLogs} from '@/apis/api'
import VirtualLogList from '@/components/VirtualLogList.vue'

const isStreaming = ref(false)
const vllRef = ref(null)
let stopStreamFn = null

const formatTimestamp = (ts) => {
  if (!ts) return ''
  return dayjs(ts).format('YYYY-MM-DD HH:mm:ss')
}

const parseLogLine = (logStr) => {
  try {
    const log = JSON.parse(logStr)
    return {
      time: formatTimestamp(log.ts),
      level: (log.level || 'INFO').toUpperCase(),
      msg: log.msg || logStr
    }
  } catch (e) {
    return {
      time: new Date().toLocaleString('zh-CN', {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit'
      }).replace(/\//g, '-'),
      level: 'INFO',
      msg: logStr
    }
  }
}

const startLogStream = () => {
  if (isStreaming.value) return
  isStreaming.value = true
  stopStreamFn = streamSystemLogs(
      (log) => vllRef.value?.push(parseLogLine(log)),
      (error) => {
        console.error('日志流错误:', error)
        isStreaming.value = false
      },
      () => {
        isStreaming.value = false
      }
  )
}

const stopLogStream = () => {
  if (stopStreamFn) {
    stopStreamFn()
    stopStreamFn = null
  }
  isStreaming.value = false
}

onActivated(() => {
  nextTick(() => vllRef.value?.scrollToBottom())
})

const clearLogs = () => {
  vllRef.value?.clear()
}

onBeforeUnmount(() => {
  if (stopStreamFn) stopStreamFn()
})
</script>

<style scoped lang="less">
.system-logs {
  height: 100%;
  width: 100%;
}

:deep(.t-badge__text) {
  line-height: 16px !important;
  font-size: 16px;
  color: var(--color-text-2);
}

.logs-card {
  border-radius: var(--border-radius-large);
  height: 100%;

  :deep(.t-card__body) {
    height: calc(100% - 45.2px);
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
  height: calc(100% - 47px);
  overflow: hidden;

  :deep(.vll-viewport::-webkit-scrollbar) {
    width: 8px;
  }

  :deep(.vll-viewport::-webkit-scrollbar-track) {
    background: #2a2a2a;
  }

  :deep(.vll-viewport::-webkit-scrollbar-thumb) {
    background: #555;
    border-radius: 4px;

    &:hover {
      background: #777;
    }
  }
}

.log-vll {
  font-family: 'Courier New', monospace;
  font-size: 14px;
  color: var(--color-white);
}

.log-line {
  display: flex;
  padding: 2px 15px;
  white-space: pre-wrap;
  word-break: break-word;
  line-height: 1.5;
  min-height: 28px;
  align-items: flex-start;
  box-sizing: border-box;
}

.log-number {
  display: inline-block;
  min-width: 50px;
  margin-right: 10px;
  color: #888;
  user-select: none;
  flex-shrink: 0;
}

.log-time {
  display: inline-block;
  min-width: 170px;
  margin-right: 12px;
  color: #a0aec0;
  font-weight: 400;
  flex-shrink: 0;
  font-family: 'Courier New', monospace;
  font-size: 13px;
}

.log-level {
  display: inline-block;
  min-width: 70px;
  margin-right: 12px;
  font-weight: 600;
  text-align: center;
  border-radius: 3px;
  padding: 0 6px;
  flex-shrink: 0;
  height: 20px;
  line-height: 20px;
  white-space: nowrap;
}

.level-INFO {
  color: #4fc3f7;
  background-color: rgba(79, 195, 247, 0.15);
}

.level-WARN {
  color: #ffb74d;
  background-color: rgba(255, 183, 77, 0.15);
}

.level-ERROR {
  color: #ef5350;
  background-color: rgba(239, 83, 80, 0.15);
}

.level-DEBUG {
  color: #81c784;
  background-color: rgba(129, 199, 132, 0.15);
}

.log-text {
  flex: 1;
  color: #e0e0e0;
  word-break: break-word;
}

.log-empty {
  display: flex;
  justify-content: center;
  align-items: center;
  height: 100%;
  text-align: center;
  color: #666;
  padding: 40px;
  font-size: 14px;
  box-sizing: border-box;
}

</style>
