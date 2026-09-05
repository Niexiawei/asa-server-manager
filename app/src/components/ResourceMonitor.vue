<template>
  <div class="resource-monitor">
    <div class="section-title" v-if="showTitleDiv">资源占用</div>
    <template v-if="isMonitoring && resourceData && !resourceData.error">
      <div class="info-item" v-if="resourceData.process">
        <span class="label">进程名称:</span>
        <span class="value">{{ resourceData.process.name }}</span>
      </div>
      <div class="info-item" v-if="resourceData.pid">
        <span class="label">进程 ID:</span>
        <span class="value">{{ resourceData.pid }}</span>
      </div>

      <!-- CPU 使用率 - 环形进度条 -->
      <div class="progress-item progress-item-cpu" v-if="resourceData.process?.cpu_percent !== undefined">
        <div class="progress-label">CPU 使用率</div>
        <div class="progress-container">
          <t-progress
              theme="circle"
              :percentage="resourceData.render.cpu_percent_normalized * 100"
              :size="80"
              :stroke-width="5"
              :color="resourceData.render.cpu_percent_color"
          >
            <template #label>
              <div class="progress-text">
                <div class="percent-value">{{ resourceData.render.cpu_percent_value }}%</div>
              </div>
            </template>
          </t-progress>
        </div>
        <div class="progress-container" v-if="resourceData.process?.cpu_total_percent">
          <t-progress
              theme="circle"
              :percentage="resourceData.render.cpu_total_percent_normalized * 100"
              :size="80"
              :stroke-width="5"
              :color="resourceData.render.cpu_total_percent_color"
          >
            <template #label>
              <div class="progress-text">
                <div class="percent-value">{{ resourceData.render.cpu_total_percent_value }}%</div>
                <div class="core-info" v-if="resourceData.cpu_cores">{{ resourceData.cpu_cores }}核</div>
              </div>
            </template>
          </t-progress>
        </div>
      </div>

      <!-- 内存使用 -->
      <div class="info-item" v-if="resourceData.process?.memory_used">
        <span class="label">内存使用:</span>
        <span class="value resource-value">{{ resourceData.render.memory_used_formatted }}</span>
        <span class="label">内存总量:</span>
        <span class="value resource-value">{{ resourceData.render.memory_total_formatted }}</span>
      </div>

      <!-- 内存使用率 - 直线进度条 -->
      <div class="progress-item" v-if="resourceData.process?.memory_percent">
        <div class="progress-label">内存使用率</div>
        <div class="linear-progress-container">
          <t-progress
              :percentage="resourceData.render.memory_percent_normalized * 100"
              :stroke-width="5"
              :label="false"
              :color="resourceData.render.memory_percent_color"
          />
          <div class="linear-progress-text">
            <span class="percent-value">{{ resourceData.render.memory_percent_value }}%</span>
          </div>
        </div>
      </div>
    </template>
    <template v-else-if="isMonitoring && resourceData?.error">
      <div class="info-item error-state">
        <error-circle-filled-icon :style="{fontSize: '20px', color: '#f53f3f'}"/>
        <span class="error-text">{{ resourceData.error }}</span>
      </div>
    </template>
    <template v-else-if="isMonitoring">
      <div class="info-item loading-state">
        <t-loading size="small"/>
        <span class="loading-text">正在获取资源信息...</span>
      </div>
    </template>
    <template v-else>
      <div class="info-item">
        <span class="no-data">服务器未运行</span>
      </div>
    </template>
  </div>
</template>

<script setup>
import {ref, watch, onUnmounted} from 'vue'
import {ErrorCircleFilledIcon} from 'tdesign-icons-vue-next'
import {getInstanceStatus} from '@/store/serverStore.js'
import {subscribeInstance, subscribeStatus} from '@/composables/useResourceStream.js'

const props = defineProps({
  instanceName: {
    type: String,
    required: true
  },
  showTitleDiv: {
    type: Boolean,
    default: true
  }
})

const isMonitoring = ref(false)
const resourceData = ref(null)

// 资源数据统一走 useResourceStream —— 全浏览器只有一条 /api/server/all-info SSE
// （SharedWorker 单例）。这里不再自建 worker/port，否则会多一条 SSE 争抢
// 明文 HTTP 下每个 origin 仅有的 6 条并发连接。
let unsubscribe = null
let unsubscribeStatus = null

const startMonitoring = () => {
  if (!props.instanceName || isMonitoring.value) return
  isMonitoring.value = true
  resourceData.value = null
  unsubscribe = subscribeInstance(props.instanceName, (data) => {
    resourceData.value = data
  })
  unsubscribeStatus = subscribeStatus(({connected, error}) => {
    if (!connected) resourceData.value = {error: error || '资源数据流已断开'}
  })
}

const stopMonitoring = () => {
  if (unsubscribe) {
    unsubscribe()
    unsubscribe = null
  }
  if (unsubscribeStatus) {
    unsubscribeStatus()
    unsubscribeStatus = null
  }
  isMonitoring.value = false
  resourceData.value = null
}

// 监听实例状态变化，从 serverStore 获取实例信息
watch(
    () => {
      const instance = getInstanceStatus(props.instanceName)
      return {
        isStartingOrRunning: instance?.isStartingOrRunning,
        status: instance?.status
      }
    },
    (newVal, oldValue) => {
      // 判断是否应该监听资源占用
      const shouldMonitor = newVal.isStartingOrRunning === true ||
          ['starting', 'started', 'stopping', 'restarting', 'restarted'].includes(newVal.status)

      if (shouldMonitor && !isMonitoring.value) {
        startMonitoring()
      } else if (!shouldMonitor && isMonitoring.value) {
        stopMonitoring()
      }
    },
    {immediate: true, deep: true}
)

// 组件卸载时清理
onUnmounted(() => {
  stopMonitoring()
  // 注意：不要终止 Worker，它是全局共享的
})

// 暴露方法供外部调用
defineExpose({
  startMonitoring,
  stopMonitoring,
  isMonitoring,
  resourceData
})
</script>

<style scoped>
.resource-monitor {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.section-title {
  font-weight: 700;
  color: #1d39c4;
  font-size: 16px;
  margin-bottom: 8px;
  padding-bottom: 8px;
  border-bottom: 2px solid #1d39c4;
}

.info-item {
  display: flex;
  align-items: center;
  height: 32px;
  padding: 0 8px;
  background-color: #f5f5f5;
  border-radius: 4px;
}

.info-item .label {
  font-weight: 600;
  color: #333;
  min-width: 100px;
  display: inline-block;
  font-size: 14px;
}

.info-item .value {
  color: #666;
  font-size: 14px;
  word-break: break-all;
  flex: 1;
}

.info-item .resource-value {
  color: #1d39c4;
  font-weight: 600;
}

.loading-state {
  display: flex;
  align-items: center;
  gap: 8px;
  justify-content: center;
  background-color: #f0f5ff;
}

.loading-text {
  color: #1d39c4;
  font-size: 13px;
}

.error-state {
  display: flex;
  align-items: center;
  gap: 8px;
  justify-content: center;
  background-color: #ffece8;
}

.error-text {
  color: #f53f3f;
  font-size: 13px;
  font-weight: 500;
}

.no-data {
  color: #999;
  font-size: 13px;
  text-align: center;
  width: 100%;
}

/* 进度条相关样式 */
.progress-item {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 8px;
  background-color: #f5f5f5;
  border-radius: 4px;
}

.progress-item-cpu {
  display: flex;
  flex-direction: row !important;
  justify-content: space-between;
}

.progress-label {
  font-weight: 600;
  color: #333;
  font-size: 14px;
}

/* 环形进度条容器 */
.progress-container {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.progress-text {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
}

.progress-text .percent-value {
  font-size: 16px;
  font-weight: 700;
  color: #1d39c4;
}

.progress-text .core-info {
  font-size: 11px;
  color: #666;
}

.progress-detail {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px 12px;
  background-color: #fff;
  border-radius: 4px;
  border: 1px solid #e8e8e8;
}

.detail-label {
  font-size: 12px;
  color: #666;
}

.detail-value {
  font-size: 14px;
  font-weight: 600;
  color: #1d39c4;
}

/* 直线进度条容器 */
.linear-progress-container {
  display: flex;
  align-items: center;
  gap: 12px;
}

.linear-progress-container :deep(.t-progress) {
  flex: 1;
}

.linear-progress-text {
  min-width: 60px;
  text-align: right;
}

.linear-progress-text .percent-value {
  font-size: 14px;
  font-weight: 600;
  color: #1d39c4;
}
</style>
