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
          <a-progress
              type="circle"
              :percent="(resourceData.process.cpu_percent / 100)"
              :width="80"
              :stroke-width="5"
              :color="getProgressColor(resourceData.process.cpu_percent)"
          >
            <template #text="{ percent }">
              <div class="progress-text">
                <div class="percent-value">{{ (percent * 100).toFixed(1) }}%</div>
              </div>
            </template>
          </a-progress>
        </div>
        <div class="progress-container" v-if="resourceData.process?.cpu_total_percent">
          <a-progress
              type="circle"
              :percent="(resourceData.process.cpu_total_percent / 100)"
              :width="80"
              :stroke-width="5"
              :color="getProgressColor(resourceData.process.cpu_total_percent)"
          >
            <template #text="{ percent }">
              <div class="progress-text">
                <div class="percent-value">{{ (percent * 100).toFixed(1) }}%</div>
                <div class="core-info" v-if="resourceData.cpu_cores">{{ resourceData.cpu_cores }}核</div>
              </div>
            </template>
          </a-progress>
        </div>
      </div>

      <!-- 内存使用 -->
      <div class="info-item" v-if="resourceData.process?.memory_used">
        <span class="label">内存使用:</span>
        <span class="value resource-value">{{ formatMemory(resourceData.process.memory_used) }}</span>
        <span class="label">内存总量:</span>
        <span class="value resource-value">{{ formatMemory(resourceData.memory.total) }}</span>
      </div>

      <!-- 内存使用率 - 直线进度条 -->
      <div class="progress-item" v-if="resourceData.process?.memory_percent">
        <div class="progress-label">内存使用率</div>
        <div class="linear-progress-container">
          <a-progress
              :percent="(resourceData.process.memory_percent / 100)"
              :stroke-width="5"
              :show-text="false"
              :color="getProgressColor(resourceData.process.memory_percent)"
          />
          <div class="linear-progress-text">
            <span class="percent-value">{{ formatCPU(resourceData.process.memory_percent) }}</span>
          </div>
        </div>
      </div>
    </template>
    <template v-else-if="isMonitoring && resourceData?.error">
      <div class="info-item error-state">
        <icon-exclamation-circle-fill :style="{fontSize: '20px', color: '#f53f3f'}"/>
        <span class="error-text">{{ resourceData.error }}</span>
      </div>
    </template>
    <template v-else-if="isMonitoring">
      <div class="info-item loading-state">
        <a-spin/>
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
import {ref, watch, onUnmounted, onMounted} from 'vue'
import {IconExclamationCircleFill} from '@arco-design/web-vue/es/icon'
import {serverStore, getInstanceStatus} from '@/store/serverStore.js'
import {API_BASE_URL} from '@/apis/api.js'

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
let worker = null

// 创建 Web Worker
const createWorker = () => {
  if (worker) return

  try {
    worker = new Worker(new URL('@/workers/resourceMonitorWorker.js', import.meta.url))
    
    // Initialize worker with API base URL
    worker.postMessage({
      type: 'INIT',
      payload: { apiBaseUrl: API_BASE_URL }
    })
    
    // Handle messages from worker
    worker.onmessage = (event) => {
      const { type, payload } = event.data
      
      switch (type) {
        case 'RESOURCE_UPDATE':
          if (payload.instanceName === props.instanceName) {
            resourceData.value = payload.data
          }
          break
        case 'ERROR':
          if (payload.instanceName === props.instanceName) {
            console.error(`Resource monitoring error for ${props.instanceName}:`, payload.error)
            resourceData.value = { error: '获取资源信息失败' }
          }
          break
      }
    }
    
    worker.onerror = (error) => {
      console.error('Resource monitor worker error:', error)
      resourceData.value = { error: '获取资源信息失败' }
    }
    
  } catch (error) {
    console.error('Failed to create resource monitor worker:', error)
    resourceData.value = { error: '创建资源监控失败' }
  }
}

// 开始资源监控
const startMonitoring = () => {
  if (!props.instanceName || isMonitoring.value) return

  console.log(`Starting resource monitoring for ${props.instanceName}`)
  isMonitoring.value = true
  resourceData.value = null

  // Ensure worker is created
  createWorker()
  
  // Start monitoring via worker
  if (worker) {
    worker.postMessage({
      type: 'START_MONITORING',
      payload: { instanceName: props.instanceName }
    })
  }
}

// 停止资源监控
const stopMonitoring = () => {
  console.log(`Stopping resource monitoring for ${props.instanceName}`)
  
  if (worker) {
    worker.postMessage({
      type: 'STOP_MONITORING',
      payload: { instanceName: props.instanceName }
    })
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
      console.log(newVal)
      // 判断是否应该监听资源占用
      const shouldMonitor = newVal.isStartingOrRunning === true ||
          ['starting', 'started', 'stopping'].includes(newVal.status)
      const wasMonitoring = oldValue?.isStartingOrRunning === true ||
          ['starting', 'started', 'stopping'].includes(oldValue?.status)

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
  // Clean up worker if it exists
  if (worker) {
    worker.terminate()
    worker = null
  }
})

// 格式化内存大小
const formatMemory = (bytes) => {
  if (!bytes) return '-'
  const mb = bytes / (1024 * 1024)
  if (mb < 1024) {
    return `${mb.toFixed(2)} MB`
  }
  return `${(mb / 1024).toFixed(2)} GB`
}

// 格式化 CPU 百分比
const formatCPU = (percent) => {
  if (percent === undefined || percent === null) return '-'
  return `${percent.toFixed(2)}%`
}

// 根据占用率获取进度条颜色
const getProgressColor = (percent) => {
  if (percent < 50) {
    return '#00b42a' // 绿色
  } else if (percent < 70) {
    return '#165dff' // 蓝色（默认）
  } else if (percent < 90) {
    return '#ff7d00' // 黄色
  } else {
    return '#f53f3f' // 红色
  }
}

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

.linear-progress-container :deep(.arco-progress) {
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
