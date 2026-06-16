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
            :disabled="logs.length === 0"
        >
          清空日志
        </t-button>
        <t-divider layout="vertical" style="height: 30px"/>
        <t-tag :color="isStreaming ? 'green' : 'gray'"
        >
          {{ isStreaming ? '监听中' : '已停止' }}
        </t-tag>
        <span style="font-size: 16px">日志行数: {{ logs.length }}</span>
      </t-space>
      <t-switch v-model="autoScroll"
                size="large"
                :label="['自动滚动', '关闭滚动']"
      ></t-switch>
    </div>

    <div class="log-container" ref="logContainerRef">
      <t-list
          ref="listRef"
          :split="false"
          :style="{
          height: `${height}px`
        }"
          :scroll="{
          type: 'virtual',
          rowHeight: 30,
          bufferSize: 10,
          threshold: 10
        }"
          class="log-content"
      >
        <template v-if="logs?.length > 0">
          <t-list-item v-for="(item,index) in logs" :key="index">
            <div class="log-line">
              <span class="log-number">{{ index + 1 }}</span>
              <span class="log-text">{{ item }}</span>
            </div>
          </t-list-item>
        </template>
        <template v-else>
          <div class="empty-logs"
               :style="{
            marginTop: (height / 2 ) - 100 + 'px'
            }"
          >
            <t-empty description="暂无日志"/>
          </div>
        </template>
      </t-list>
    </div>
  </div>
</template>

<script setup>
import {ref, onMounted, onUnmounted, nextTick, watch, computed, useTemplateRef} from 'vue'
import {streamInstanceLogs} from '@/apis/api.js'
import {useElementSize} from "@vueuse/core"
import {serverStore, getInstanceStatus} from '@/store/serverStore.js';

const props = defineProps({
  instanceName: {
    type: String,
    required: true
  }
})

const el = useTemplateRef('logContainerRef')
const {width, height} = useElementSize(el)

const autoScroll = ref(true)
const logs = ref([])
const isStreaming = ref(false)
const listRef = ref(null)
let stopLogStream_func = null
let resizeObserver = null
let scrollInterval = null


scrollInterval = setInterval(() => {
  scrollToBottom()
}, 500)


// 滚动到底部
const scrollToBottom = () => {
  if (!autoScroll.value) {
    return
  }
  listRef.value?.scrollTo({
    index: logs.value?.length - 1,
    behavior: 'smooth',
  })
}

// 开始监听日志
const startLogStream = () => {
  isStreaming.value = true
  logs.value = []

  stopLogStream_func = streamInstanceLogs(
      props.instanceName,
      // onLog 回调
      (line) => {
        logs.value.push(line)
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

  // 首次加载后滚动到底部
  setTimeout(() => {
    scrollToBottom()
  }, 100)
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
      // 判断是否应该监听日志
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
  logs.value = []
}

// 组件卸载时清理资源
onUnmounted(() => {
  if (isStreaming.value) {
    stopLogStream()
  }
  if (scrollInterval) {
    clearInterval(scrollInterval)
  }
})

// 暴露函数给父组件
defineExpose({
  startLogStream,
  stopLogStream,
  clearLogs,
  // 暴露状态供父组件检查
  get isStreaming() {
    return isStreaming.value
  },
  get logs() {
    return logs.value
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
}

.log-content {
  flex: 1;
  font-size: 14px;
  background: #1a1a1a !important;
  color: #e0e0e0;
  height: 100%;
  box-sizing: border-box;

  &::-webkit-scrollbar-thumb{
    background-color: rgba(255,255,255,0.7);
    &:hover{
      background-color: rgba(255,255,255,0.7);
    }
  }

  :deep(.t-list-item) {
    padding: 2px 0 !important;
  }
}

.log-line {
  display: flex;
  padding: 4px 15px;
  white-space: pre-wrap;
  word-break: break-word;
  line-height: 1.5;
  min-height: 28px;
  align-items: center;
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
  height: 100%;
  color: #999;
}

/* 虚拟列表样式 */
:deep(.t-list) {
  background-color: transparent;
  border: none;
}

:deep(.t-list__empty) {
  color: #999;
}

:deep(.t-virtual-list) {
  overflow-y: auto;
  background-color: #1a1a1a;
  padding: 0 !important;
}

:deep(.t-virtual-list__content) {
  padding: 15px !important;
  box-sizing: border-box;
}

/* 滚动条样式 */
:deep(.t-virtual-list::-webkit-scrollbar) {
  width: 8px;
}

:deep(.t-virtual-list::-webkit-scrollbar-track) {
  background: #2a2a2a;
}

:deep(.t-virtual-list::-webkit-scrollbar-thumb) {
  background: #555;
  border-radius: 4px;
}

:deep(.t-virtual-list::-webkit-scrollbar-thumb:hover) {
  background: #777;
}
</style>
