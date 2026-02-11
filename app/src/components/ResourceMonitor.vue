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
import {ref, watch, onUnmounted, onMounted} from 'vue'
import {ErrorCircleFilledIcon} from 'tdesign-icons-vue-next'
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

// SharedWorker 实例和端口
let sharedWorker = null
let workerPort = null
let workerInitialized = false

// 获取或创建 SharedWorker
const getSharedWorker = () => {
  if (!sharedWorker) {
    try {
      console.log('[ResourceMonitor] Creating SharedWorker')
      sharedWorker = new SharedWorker(new URL('@/workers/sharedResourceWorker.js', import.meta.url))
      workerPort = sharedWorker.port

      // 设置消息处理
      workerPort.onmessage = (event) => {
        const {type, instanceId, data, error} = event.data

        switch (type) {
          case 'RESOURCE_UPDATE':
            if (instanceId === props.instanceName) {
              resourceData.value = data
            }
            break
          case 'ERROR':
            console.error(`[ResourceMonitor] Error for ${props.instanceName}:`, error)
            resourceData.value = {error: '获取资源信息失败'}
            break
          case 'SSE_CONNECTED':
            console.log('[ResourceMonitor] SharedWorker SSE connected')
            break
        }
      }

      workerPort.onmessageerror = (error) => {
        console.error('[ResourceMonitor] Worker port error:', error)
        resourceData.value = {error: 'Worker 通信错误'}
      }

      // 初始化 SharedWorker
      workerPort.postMessage({
        type: 'INIT',
        payload: {apiBaseUrl: API_BASE_URL}
      })

      workerInitialized = true
    } catch (error) {
      console.error('[ResourceMonitor] Failed to create SharedWorker:', error)
      sharedWorker = null
      workerPort = null
      workerInitialized = false
    }
  }
  return sharedWorker
}

// 开始资源监控
const startMonitoring = () => {
  if (!props.instanceName || isMonitoring.value) return

  try {
    console.log(`[ResourceMonitor] Starting resource monitoring for ${props.instanceName}`)
    isMonitoring.value = true
    resourceData.value = null

    // 确保 SharedWorker 就绪
    getSharedWorker()

    if (!workerPort) {
      throw new Error('Worker port not available')
    }

    // 订阅该实例的数据
    workerPort.postMessage({
      type: 'SUBSCRIBE',
      instanceId: props.instanceName
    })
  } catch (error) {
    console.error('[ResourceMonitor] Failed to start monitoring:', error)
    isMonitoring.value = false
    resourceData.value = {error: '启动资源监控失败'}
  }
}

// 停止资源监控
const stopMonitoring = () => {
  console.log(`[ResourceMonitor] Stopping resource monitoring for ${props.instanceName}`)

  if (workerPort) {
    workerPort.postMessage({
      type: 'UNSUBSCRIBE',
      instanceId: props.instanceName
    })
  }

  isMonitoring.value = false
  resourceData.value = null
  // 停止订阅后，将状态置为"服务器未运行"
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
          ['starting', 'started', 'stopping', 'started'].includes(newVal.status)

      console.log(props.instanceName)
      console.log(newVal.status)
      console.log("isStartingOrRunning", newVal.isStartingOrRunning)
      console.log("shouldMonitor:", shouldMonitor)
      console.log("isMonitoring", isMonitoring.value)

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
