<template>
  <!-- 外层根节点会被 App.vue 加上 layout-card-wrapper（全局样式里是
       flex 列 + overflow:hidden，且优先级压过这里的 scoped 样式），
       所以两栏网格与滚动都放在内层，别指望在根节点上写 display:grid -->
  <div class="server-resource-page">
    <div class="page-flex">
      <t-card class="summary-card" headerBordered>
        <template #title>
          <div class="card-title">
            <span>服务器资源</span>
            <span class="card-subtitle" v-if="host?.cpu?.core_count">{{ host.cpu.core_count }} 核</span>
          </div>
        </template>

        <div class="summary-body" v-if="host">
          <div class="summary-item">
            <t-progress
                theme="circle"
                :percentage="round(host.cpu?.used_percent)"
                :size="130"
                :stroke-width="12"
                :color="progressColor(host.cpu?.used_percent)"
            >
              <template #label>
                <div class="progress-text">
                  <div class="percent-value">{{ fmtPercent(host.cpu?.used_percent) }}</div>
                  <div class="percent-label">CPU</div>
                </div>
              </template>
            </t-progress>
          </div>

          <div class="summary-detail">
            <div class="detail-row">
              <span class="detail-label">内存使用</span>
              <span class="detail-value">{{ fmtBytes(host.memory?.used) }} / {{ fmtBytes(host.memory?.total) }}</span>
            </div>
            <div class="linear-progress-container">
              <t-progress
                  :percentage="round(host.memory?.used_percent)"
                  :stroke-width="6"
                  :label="false"
                  :color="progressColor(host.memory?.used_percent)"
              />
              <span class="linear-value">{{ fmtPercent(host.memory?.used_percent) }}</span>
            </div>

            <div class="detail-grid">
              <div class="grid-item">
                <span class="grid-label">磁盘读</span>
                <span class="grid-value">{{ fmtBytesPerSec(host.disk_io?.read_bytes_per_sec) }}</span>
              </div>
              <div class="grid-item">
                <span class="grid-label">磁盘写</span>
                <span class="grid-value">{{ fmtBytesPerSec(host.disk_io?.write_bytes_per_sec) }}</span>
              </div>
              <div class="grid-item">
                <span class="grid-label">读 IOPS</span>
                <span class="grid-value">{{ round(host.disk_io?.read_iops) }}</span>
              </div>
              <div class="grid-item">
                <span class="grid-label">写 IOPS</span>
                <span class="grid-value">{{ round(host.disk_io?.write_iops) }}</span>
              </div>
              <div class="grid-item">
                <span class="grid-label">网络接收</span>
                <span class="grid-value">{{ fmtBytesPerSec(host.net_io?.recv_bytes_per_sec) }}</span>
              </div>
              <div class="grid-item">
                <span class="grid-label">网络发送</span>
                <span class="grid-value">{{ fmtBytesPerSec(host.net_io?.sent_bytes_per_sec) }}</span>
              </div>
            </div>

            <div class="detail-row running-count">
              <span class="detail-label">运行中实例</span>
              <span class="detail-value">{{ runningCount }}</span>
            </div>
          </div>
        </div>

        <div class="summary-loading" v-else>
          <t-loading size="small"/>
          <span>正在获取资源信息...</span>
        </div>
      </t-card>

      <t-card class="trend-card" headerBordered>
        <template #title>
          <div class="card-title"><span>资源趋势</span></div>
        </template>
        <resource-trend-panel scope="host" :chart-height="150"/>
      </t-card>
    </div>
  </div>
</template>

<script setup>
import {ref, onMounted, onBeforeUnmount} from 'vue'
import ResourceTrendPanel from '@/components/ResourceTrendPanel.vue'
import {subscribeHost} from '@/composables/useResourceStream.js'

// KeepAlive 的 include 匹配的是**组件名**，而 <script setup> 的组件名由文件名推断——
// 本文件叫 index.vue，推断出来会是 "index"，写进 App.vue 的 include 会静默不生效。
// 这里显式命名。
defineOptions({name: 'ServerResourceMonitor'})

const host = ref(null)
const runningCount = ref(0)
let unsubscribe = null

onMounted(() => {
  // 与趋势图共用同一条 all-info（SharedWorker 单例），不额外开连接
  unsubscribe = subscribeHost((payload) => {
    host.value = payload.host
    runningCount.value = payload.running_count ?? 0
  })
})

onBeforeUnmount(() => {
  if (unsubscribe) unsubscribe()
})

const round = (v) => (typeof v === 'number' ? Math.round(v) : 0)

const fmtPercent = (v) => (typeof v === 'number' ? `${v.toFixed(1)}%` : '-')

const fmtBytes = (bytes) => {
  if (!bytes) return '-'
  const gb = bytes / (1024 * 1024 * 1024)
  if (gb < 1) return `${(bytes / (1024 * 1024)).toFixed(2)} MB`
  return `${gb.toFixed(2)} GB`
}

const fmtBytesPerSec = (v) => {
  if (typeof v !== 'number') return '-'
  if (v < 1024) return `${Math.round(v)} B/s`
  const kb = v / 1024
  if (kb < 1024) return `${kb.toFixed(kb < 10 ? 1 : 0)} KB/s`
  const mb = kb / 1024
  if (mb < 1024) return `${mb.toFixed(mb < 10 ? 2 : 1)} MB/s`
  return `${(mb / 1024).toFixed(2)} GB/s`
}

const progressColor = (percent) => {
  const p = percent || 0
  if (p < 50) return '#00b42a'
  if (p < 70) return '#165dff'
  if (p < 90) return '#ff7d00'
  return '#f53f3f'
}
</script>

<style scoped lang="less">
.server-resource-page {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
}

.page-flex {
  display: flex;
  flex-direction: column;
  gap: 15px;
  box-sizing: border-box;
  width: 100%;
  height: 100%;

  .summary-card {
    flex: 0 0 auto;
    min-height: 0;
  }

  .trend-card {
    flex: 1 1 auto;
    min-height: 0;
    display: flex;
    flex-direction: column;

    :deep(.t-card__header) {
      flex: 0 0 auto;
    }

    :deep(.t-loading__parent) {
      flex: 1 1 auto;
      min-height: 0;

      .t-card__body {
        height: 100%;
        .custom-scrollbar-style();
        box-sizing: border-box;
      }
    }
  }
}

.summary-card,
.trend-card {
  border-radius: 6px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.08);
}

.card-title {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-weight: 600;
}

.card-subtitle {
  font-size: 14px;
  color: #999;
  font-weight: 400;
}

.summary-body {
  display: flex;
  gap: 20px;
  align-items: center;
  flex-wrap: wrap;
}

.summary-item {
  display: flex;
  align-items: center;
  justify-content: center;
}

.summary-detail {
  flex: 1;
  min-width: 220px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.detail-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 13px;
}

.detail-label {
  color: #666;
}

.detail-value {
  color: #1d39c4;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.linear-progress-container {
  display: flex;
  align-items: center;
  gap: 10px;

  :deep(.t-progress) {
    flex: 1;
  }
}

.linear-value {
  min-width: 56px;
  text-align: right;
  font-size: 13px;
  font-weight: 600;
  color: #1d39c4;
  font-variant-numeric: tabular-nums;
}

.detail-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 8px 16px;
  padding: 10px;
  background-color: #f5f5f5;
  border-radius: 6px;
}

.grid-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 12px;
}

.grid-label {
  color: #666;
}

.grid-value {
  color: #333;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.running-count {
  padding-top: 2px;
}

.progress-text {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
}

.progress-text .percent-value {
  font-size: 20px;
  font-weight: 700;
  color: #1d39c4;
}

.progress-text .percent-label {
  font-size: 12px;
  color: #666;
}

.summary-loading {
  display: flex;
  align-items: center;
  gap: 8px;
  justify-content: center;
  padding: 24px;
  color: #1d39c4;
  font-size: 13px;
}
</style>
