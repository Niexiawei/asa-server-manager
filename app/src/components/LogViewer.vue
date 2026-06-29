<template>
  <div class="log-viewer">
    <div class="header-actions">
      <t-space style="margin-bottom: 15px" align="center">
        <t-button
            @click="startLogStream"
            theme="primary"
            :disabled="isStreaming"
            ref="startButtonRef"
        >
          {{ isStreaming ? '监听中...' : '开始监听' }}
        </t-button>
        <t-button
            @click="stopLogStream"
            theme="warning"
            :disabled="!isStreaming"
            ref="stopButtonRef"
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
      <t-switch v-model="autoScroll"
                size="large"
                :label="['自动滚动', '关闭滚动']"
      ></t-switch>
    </div>

    <div class="log-container">
      <VirtualLogList
          ref="vllRef"
          :auto-scroll="autoScroll"
          class="log-content"
          :estimated-item-height="28"
          :buffer="400"
      >
        <template #item="{ item, index }">
          <div class="log-line">
            <span class="log-number">{{ index + 1 }}</span>
            <span class="log-text">{{ item }}</span>
          </div>
        </template>
        <template #empty>
          <div class="empty-logs">
            <t-empty description="暂无日志"/>
          </div>
        </template>
      </VirtualLogList>
    </div>
  </div>
</template>

<script setup>
import {ref, nextTick, watch} from 'vue'
import {streamInstanceLogs} from '@/apis/api.js'
import {getInstanceStatus} from '@/store/serverStore.js'
import VirtualLogList from '@/components/VirtualLogList.vue'

const props = defineProps({
  instanceName: {
    type: String,
    required: true
  }
})

const autoScroll = ref(true)
const isStreaming = ref(false)
const vllRef = ref(null)
let stopLogStream_func = null

// 自动滚动开关打开时立即回到底部
watch(autoScroll, (val) => {
  if (val) vllRef.value?.scrollToBottom()
})

// 开始监听日志
const startLogStream = () => {
  isStreaming.value = true
  vllRef.value?.clear()

  stopLogStream_func = streamInstanceLogs(
      props.instanceName,
      (line) => vllRef.value?.push(line),
      (error) => {
        console.error('日志流错误:', error)
      },
      () => {
        isStreaming.value = false
      }
  )
}

// 停止日志流监听
const stopLogStream = () => {
  if (stopLogStream_func) {
    stopLogStream_func()
    stopLogStream_func = null
  }
  isStreaming.value = false
}

// 监听实例状态变化，自动开始/停止日志监听
watch(
    () => {
      const instance = getInstanceStatus(props.instanceName)
      return {
        isStartingOrRunning: instance?.isStartingOrRunning,
        status: instance?.status
      }
    },
    (newVal) => {
      const shouldMonitor = newVal.isStartingOrRunning === true ||
          ['starting', 'started', 'stopping', 'restarting', 'restarted'].includes(newVal.status)

      if (shouldMonitor && !isStreaming.value) {
        startLogStream()
      } else if (!shouldMonitor && isStreaming.value) {
        stopLogStream()
      }
    },
    {immediate: true, deep: true}
)

// 清空日志
const clearLogs = () => {
  vllRef.value?.clear()
}

// 暴露给父组件
defineExpose({
  startLogStream,
  stopLogStream,
  clearLogs,
  get isStreaming() {
    return isStreaming.value
  },
  get logs() {
    return vllRef.value?.getItems() ?? []
  }
})
</script>

<style scoped lang="less">
.header-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.log-viewer {
  display: flex;
  flex-direction: column;
  height: 100%;

  :deep(.t-badge__text) {
    font-size: 16px;
    color: var(--color-text-2);
  }
}

.log-container {
  border-radius: 4px;
  flex: 1;
  display: flex;
  min-height: 0;
  overflow: hidden;
  background: #1a1a1a;

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

.log-content {
  flex: 1;
  font-size: 14px;
  color: #e0e0e0;
}

.log-line {
  display: flex;
  padding: 4px 15px;
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

.log-text {
  flex: 1;
  color: #e0e0e0;
  word-break: break-word;
}

.empty-logs {
  display: flex;
  justify-content: center;
  align-items: center;
  width: 100%;
  color: #999;
}
</style>
