<template>
  <a-popover position="br" trigger="click" :content-style="{ padding: '16px', minWidth: '400px' }">
    <template #content>
      <div class="server-resource-content">
        <div class="section-title">服务器资源占用</div>

        <template v-if="isMonitoring && resourceData && !resourceData.error">
          <!-- CPU 使用率 - 环形进度条 -->
          <div class="progress-item progress-item-cpu" v-if="resourceData.cpu">
            <div class="progress-label">CPU 使用率</div>
            <div class="progress-container">
              <a-progress
                  type="circle"
                  :percent="(resourceData.cpu.used_percent / 100)"
                  :width="80"
                  :stroke-width="5"
                  :color="getProgressColor(resourceData.cpu.used_percent)"
              >
                <template #text="{ percent }">
                  <div class="progress-text">
                    <div class="percent-value">{{ (percent * 100).toFixed(1) }}%</div>
                    <div class="core-info" v-if="resourceData.cpu.core_count">{{ resourceData.cpu.core_count }}核</div>
                  </div>
                </template>
              </a-progress>
            </div>
          </div>

          <!-- 内存使用 -->
          <div class="info-item" v-if="resourceData.memory">
            <span class="label">内存使用:</span>
            <span class="value resource-value">{{ formatMemory(resourceData.memory.used) }}</span>
            <span class="label">内存总量:</span>
            <span class="value resource-value">{{ formatMemory(resourceData.memory.total) }}</span>
          </div>

          <!-- 内存使用率 - 直线进度条 -->
          <div class="progress-item" v-if="resourceData.memory">
            <div class="progress-label">内存使用率</div>
            <div class="linear-progress-container">
              <a-progress
                  :percent="(resourceData.memory.used_percent / 100)"
                  :stroke-width="5"
                  :show-text="false"
                  :color="getProgressColor(resourceData.memory.used_percent)"
              />
              <div class="linear-progress-text">
                <span class="percent-value">{{ resourceData.memory.used_percent.toFixed(2) }}%</span>
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
            <span class="no-data">暂无资源信息</span>
          </div>
        </template>
      </div>
    </template>

    <a-badge :count="isMonitoring ? 0 : ''" dot :dot-style="{ width: '8px', height: '8px' }">
      <a-button type="text" @click="handlePopoverClick">
        <template #icon>
          <icon-dashboard :style="{ fontSize: '20px', color: getIconColor() }"/>
        </template>
      </a-button>
    </a-badge>
  </a-popover>
</template>

<script setup>
import {ref, onMounted, onUnmounted} from 'vue'
import {IconDashboard, IconExclamationCircleFill} from '@arco-design/web-vue/es/icon'
import {streamServerResourceInfo} from '@/apis/api.js'

const isMonitoring = ref(false)
const resourceData = ref(null)
let closeStream = null

// 开始监控
const startMonitoring = () => {
  if (isMonitoring.value) return

  console.log('Starting server resource monitoring')
  isMonitoring.value = true
  resourceData.value = null

  closeStream = streamServerResourceInfo(
      (data) => {
        if (data.error) {
          resourceData.value = {error: data.error}
        } else {
          resourceData.value = data
        }
      },
      (error) => {
        console.error('Server resource monitoring error:', error)
        resourceData.value = {error: '连接失败'}
      }
  )
}

// 停止监控
const stopMonitoring = () => {
  console.log('Stopping server resource monitoring')
  if (closeStream) {
    closeStream()
    closeStream = null
  }
  isMonitoring.value = false
  resourceData.value = null
}

// 处理气泡点击
const handlePopoverClick = () => {
  if (!isMonitoring.value) {
    startMonitoring()
  }
}

// 组件挂载时自动开始监控
onMounted(() => {
  startMonitoring()
})

// 组件卸载时停止监控
onUnmounted(() => {
  stopMonitoring()
})

// 格式化内存大小
const formatMemory = (bytes) => {
  if (!bytes) return '-'
  const gb = bytes / (1024 * 1024 * 1024)
  if (gb < 1) {
    const mb = bytes / (1024 * 1024)
    return `${mb.toFixed(2)} MB`
  }
  return `${gb.toFixed(2)} GB`
}

// 根据占用率获取进度条颜色
const getProgressColor = (percent) => {
  if (percent < 50) {
    return '#00b42a' // 绿色
  } else if (percent < 70) {
    return '#165dff' // 蓝色
  } else if (percent < 90) {
    return '#ff7d00' // 黄色
  } else {
    return '#f53f3f' // 红色
  }
}

// 获取图标颜色
const getIconColor = () => {
  if (!isMonitoring.value || !resourceData.value) {
    return '#999'
  }
  if (resourceData.value.error) {
    return '#f53f3f'
  }

  // 根据 CPU 或内存使用率返回颜色
  const cpuPercent = resourceData.value.cpu?.used_percent || 0
  const memPercent = resourceData.value.memory?.used_percent || 0
  const maxPercent = Math.max(cpuPercent, memPercent)

  return getProgressColor(maxPercent)
}
</script>

<style scoped>
.server-resource-content {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 350px;
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
  min-height: 32px;
  padding: 8px;
  background-color: #f5f5f5;
  border-radius: 4px;
}

.info-item .label {
  font-weight: 600;
  color: #333;
  min-width: 80px;
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
  border-radius: 6px;
}

.progress-item-cpu {
  display: flex;
  flex-direction: row !important;
  justify-content: space-between;
  align-items: center;
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
  justify-content: center;
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
