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
              :percent="resourceData.render.cpu_percent_normalized"
              :width="80"
              :stroke-width="5"
              :color="resourceData.render.cpu_percent_color"
          >
            <template #text="{ percent }">
              <div class="progress-text">
                <div class="percent-value">{{ resourceData.render.cpu_percent_value }}%</div>
              </div>
            </template>
          </a-progress>
        </div>
        <div class="progress-container" v-if="resourceData.process?.cpu_total_percent">
          <a-progress
              type="circle"
              :percent="resourceData.render.cpu_total_percent_normalized"
              :width="80"
              :stroke-width="5"
              :color="resourceData.render.cpu_total_percent_color"
          >
            <template #text="{ percent }">
              <div class="progress-text">
                <div class="percent-value">{{ resourceData.render.cpu_total_percent_value }}%</div>
                <div class="core-info" v-if="resourceData.cpu_cores">{{ resourceData.cpu_cores }}核</div>
              </div>
            </template>
          </a-progress>
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
          <a-progress
              :percent="resourceData.render.memory_percent_normalized"
              :stroke-width="5"
              :show-text="false"
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

// 全局共享 Worker 实例（实例资源监控）
let globalWorker = null
// Worker 初始化标志
let workerInitialized = false
// 等待 Worker 初始化的 Promise 列表
const workerInitPromises = []
// 资源更新和错误的事件监听器
let unsubscribeResourceUpdates = null

// 获取或创建全局 Worker
const getSharedWorker = () => {
  if (!globalWorker) {
    try {
      globalWorker = new Worker(new URL('@/workers/instanceResourceWorker.js', import.meta.url))
      
      // Worker 初始化
      globalWorker.postMessage({
        type: 'INIT',
        payload: { apiBaseUrl: API_BASE_URL }
      })
      
      setupWorkerMessageHandler()
      workerInitialized = true
      // 解决所有等待的 Promise
      workerInitPromises.forEach(resolve => resolve())
      workerInitPromises.length = 0
    } catch (error) {
      console.error('Failed to create shared worker:', error)
      globalWorker = null
      workerInitialized = false
    }
  }
  return globalWorker
}

// 等待 Worker 初始化
const ensureWorkerReady = async () => {
  const worker = getSharedWorker()
  if (!worker) {
    throw new Error('Failed to initialize worker')
  }
  if (!workerInitialized) {
    await new Promise(resolve => workerInitPromises.push(resolve))
  }
}

// 处理 Worker 消息
const setupWorkerMessageHandler = () => {
  const worker = globalWorker
  if (!worker || worker.messageHandlerSetup) return
  
  worker.messageHandlerSetup = true
  
  worker.onmessage = (event) => {
    const { type, payload } = event.data
    
    switch (type) {
      case 'RESOURCE_UPDATE':
        // 广播给所有监听该实例的组件
        window.dispatchEvent(new CustomEvent('resource-update', {
          detail: { instanceName: payload.instanceName, data: payload.data }
        }))
        break
      case 'ERROR':
        window.dispatchEvent(new CustomEvent('resource-error', {
          detail: { instanceName: payload.instanceName, error: payload.error }
        }))
        break
    }
  }
  
  worker.onerror = (error) => {
    console.error('Resource monitor worker error:', error)
    window.dispatchEvent(new CustomEvent('resource-error', {
      detail: { instanceName: 'all', error: '资源监控异常' }
    }))
  }
}

// 订阅资源更新
const subscribeToResourceUpdates = () => {
  const handleResourceUpdate = (event) => {
    if (event.detail.instanceName === props.instanceName) {
      resourceData.value = event.detail.data
    }
  }
  
  const handleResourceError = (event) => {
    if (event.detail.instanceName === props.instanceName) {
      console.error(`Resource monitoring error for ${props.instanceName}:`, event.detail.error)
      resourceData.value = { error: '获取资源信息失败' }
    }
  }
  
  window.addEventListener('resource-update', handleResourceUpdate)
  window.addEventListener('resource-error', handleResourceError)
  
  // 返回取消监听的函数
  return () => {
    window.removeEventListener('resource-update', handleResourceUpdate)
    window.removeEventListener('resource-error', handleResourceError)
  }
}

// 开始资源监控
const startMonitoring = async () => {
  if (!props.instanceName || isMonitoring.value) return

  try {
    console.log(`Starting resource monitoring for ${props.instanceName}`)
    isMonitoring.value = true
    resourceData.value = null

    // 确保 Worker 就绪
    await ensureWorkerReady()
    
    // 订阅更新
    unsubscribeResourceUpdates = subscribeToResourceUpdates()
    
    // 告诉 Worker 开始监控此实例
    if (globalWorker) {
      globalWorker.postMessage({
        type: 'START_MONITORING',
        payload: { instanceName: props.instanceName }
      })
    }
  } catch (error) {
    console.error('Failed to start monitoring:', error)
    isMonitoring.value = false
    resourceData.value = { error: '启动资源监控失败' }
  }
}

// 停止资源监控
const stopMonitoring = () => {
  console.log(`Stopping resource monitoring for ${props.instanceName}`)
  
  if (globalWorker) {
    globalWorker.postMessage({
      type: 'STOP_MONITORING',
      payload: { instanceName: props.instanceName }
    })
  }
  
  // 取消事件监听
  if (unsubscribeResourceUpdates) {
    unsubscribeResourceUpdates()
    unsubscribeResourceUpdates = null
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
  // 注意：不要终止 Worker，哠它是全局共享的
})

// 格式化内存大小（从 Worker 获取，保持为本地函数以支持直接调用）
const formatMemory = (bytes) => {
  if (!bytes) return '-'
  const mb = bytes / (1024 * 1024)
  if (mb < 1024) {
    return `${mb.toFixed(2)} MB`
  }
  return `${(mb / 1024).toFixed(2)} GB`
}

// 格式化 CPU 百分比（从 Worker 获取，保持为本地函数以支持直接调用）
const formatCPU = (percent) => {
  if (percent === undefined || percent === null) return '-'
  return `${percent.toFixed(2)}%`
}

// 根据占用率获取进度条颜色（从 Worker 获取，保持为本地函数以支持直接调用）
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
