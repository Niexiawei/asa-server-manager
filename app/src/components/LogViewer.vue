<template>
  <div class="log-viewer">
    <t-tabs v-model="activeTab" class="log-tabs">
      <t-tab-panel value="game" label="实例日志"/>
      <t-tab-panel value="asaapi" label="API 日志"/>
    </t-tabs>

    <div class="header-actions">
      <t-space style="margin-bottom: 15px" align="center">
        <t-button
            @click="current.start()"
            theme="primary"
            :disabled="isStreaming"
        >
          {{ isStreaming ? '监听中...' : '开始监听' }}
        </t-button>
        <t-button
            @click="current.stop()"
            theme="warning"
            :disabled="!isStreaming"
        >
          停止监听
        </t-button>
        <t-button
            @click="current.clear()"
            :disabled="logCount === 0"
        >
          清空日志
        </t-button>
        <t-divider layout="vertical" style="height: 30px"/>
        <t-tag :color="isStreaming ? 'green' : 'gray'">
          {{ isStreaming ? '监听中' : '已停止' }}
        </t-tag>
        <span style="font-size: 16px">日志行数: {{ logCount }}</span>
      </t-space>
      <t-switch v-model="autoScroll"
                size="large"
                :label="['自动滚动', '关闭滚动']"
      ></t-switch>
    </div>

    <div class="log-container">
      <VirtualLogList
          v-show="activeTab === 'game'"
          ref="gameListRef"
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

      <VirtualLogList
          v-show="activeTab === 'asaapi'"
          ref="apiListRef"
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
import {ref, computed, nextTick, watch, onUnmounted} from 'vue'
import {streamInstanceLogs, streamAsaApiLogs} from '@/apis/api.js'
import {getInstanceStatus} from '@/store/serverStore.js'
import VirtualLogList from '@/components/VirtualLogList.vue'

const props = defineProps({
  instanceName: {
    type: String,
    required: true
  }
})

const autoScroll = ref(true)
const activeTab = ref('game')
const gameListRef = ref(null)
const apiListRef = ref(null)

// 一路日志 = 一个 SSE 流 + 一个虚拟列表，两路互不干扰
function createLogChannel(streamFn, listRef) {
  const isStreaming = ref(false)
  let stopFn = null

  const start = () => {
    isStreaming.value = true
    listRef.value?.clear()

    stopFn = streamFn(
        props.instanceName,
        (line) => listRef.value?.push(line),
        (error) => {
          console.error('日志流错误:', error)
        },
        () => {
          isStreaming.value = false
        }
    )
  }

  const stop = () => {
    if (stopFn) {
      stopFn()
      stopFn = null
    }
    isStreaming.value = false
  }

  const clear = () => listRef.value?.clear()

  return {isStreaming, listRef, start, stop, clear}
}

const channels = {
  game: createLogChannel(streamInstanceLogs, gameListRef),
  asaapi: createLogChannel(streamAsaApiLogs, apiListRef),
}
const allChannels = Object.values(channels)

// 工具栏只作用于当前 tab
const current = computed(() => channels[activeTab.value])
const isStreaming = computed(() => current.value.isStreaming.value)
const logCount = computed(() => current.value.listRef.value?.itemCount ?? 0)

// 自动滚动开关打开时立即回到底部
watch(autoScroll, (val) => {
  if (val) allChannels.forEach((ch) => ch.listRef.value?.scrollToBottom())
})

// 切回来的列表在隐藏期间高度为 0，需等布局完成再滚到底
watch(activeTab, async () => {
  if (!autoScroll.value) return
  await nextTick()
  current.value.listRef.value?.scrollToBottom()
})

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

      allChannels.forEach((ch) => {
        if (shouldMonitor && !ch.isStreaming.value) {
          ch.start()
        } else if (!shouldMonitor && ch.isStreaming.value) {
          ch.stop()
        }
      })
    },
    {immediate: true, deep: true}
)

onUnmounted(() => {
  allChannels.forEach((ch) => ch.stop())
})

// 暴露给父组件，作用于当前 tab
defineExpose({
  startLogStream: () => current.value.start(),
  stopLogStream: () => current.value.stop(),
  clearLogs: () => current.value.clear(),
  get isStreaming() {
    return isStreaming.value
  },
  get logs() {
    return current.value.listRef.value?.getItems() ?? []
  }
})
</script>

<style scoped lang="less">
.log-tabs {
  flex-shrink: 0;
  margin-bottom: 10px;
}

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
