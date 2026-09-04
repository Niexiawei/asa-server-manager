<template>
  <t-popup placement="bottom-right" trigger="click" showArrow :content-style="{ padding: '16px', minWidth: '360px' }">
    <template #content>
      <div class="server-resource-content">
        <div class="section-title">服务器资源占用</div>

        <template v-if="host">
          <div class="mini-metric">
            <div class="mini-head">
              <span class="mini-label">CPU 使用率</span>
              <span class="mini-value" :style="{ color: progressColor(host.cpu?.used_percent) }">
                {{ fmtPercent(host.cpu?.used_percent) }}
              </span>
            </div>
            <UPlotChart
                :series="cpuSeries"
                :data="cpuData"
                :height="40"
                :max-y="100"
                :show-axes="false"
                :fmt-y="fmtPercent"
            />
          </div>

          <div class="mini-metric">
            <div class="mini-head">
              <span class="mini-label">内存使用率</span>
              <span class="mini-value" :style="{ color: progressColor(host.memory?.used_percent) }">
                {{ fmtPercent(host.memory?.used_percent) }}
              </span>
            </div>
            <UPlotChart
                :series="memSeries"
                :data="memData"
                :height="40"
                :max-y="100"
                :show-axes="false"
                :fmt-y="fmtPercent"
            />
            <div class="mini-sub">
              {{ fmtBytes(host.memory?.used) }} / {{ fmtBytes(host.memory?.total) }}
            </div>
          </div>

          <t-button theme="primary" variant="text" block @click="goDetail">
            查看资源监控详情 →
          </t-button>
        </template>

        <template v-else>
          <div class="info-item loading-state">
            <t-loading size="small"/>
            <span class="loading-text">正在获取资源信息...</span>
          </div>
        </template>
      </div>
    </template>

    <t-badge :count="host ? 0 : ''" dot :dot-style="{ width: '8px', height: '8px' }">
      <t-button variant="text">
        <template #icon>
          <dashboard-icon :style="{ fontSize: '20px', color: iconColor }"/>
        </template>
      </t-button>
    </t-badge>
  </t-popup>
</template>

<script setup>
import {ref, computed, onMounted, onBeforeUnmount} from 'vue'
import {useRouter} from 'vue-router'
import {DashboardIcon} from 'tdesign-icons-vue-next'
import UPlotChart from '@/components/UPlotChart.vue'
import {subscribeHost} from '@/composables/useResourceStream.js'
import {useResourceTrend} from '@/composables/useResourceTrend.js'

// 顶栏只留两条迷你 sparkline + 一个入口，完整视图在 /server-resource。
//
// 数据来自 SharedWorker 的 all-info（与趋势图、实例面板共用同一条 SSE），
// 不再自己开 /api/server/info —— 内网明文 HTTP 下浏览器每个 origin 只有 6 条连接，
// 常驻 SSE 一条占一个名额。
const router = useRouter()
const host = ref(null)
let unsubscribe = null

// 弹窗是常驻挂载的，短窗口足够画 sparkline
const SPARK_WINDOW = 180
const trend = useResourceTrend({scope: 'host'})

const cpuSeries = [{label: 'CPU', stroke: '#1d39c4', fill: 'rgba(29,57,196,0.12)'}]
const memSeries = [{label: '内存', stroke: '#00b42a', fill: 'rgba(0,180,42,0.12)'}]

const cpuData = computed(() => trend.slice(['cpu_used_percent'], SPARK_WINDOW))
const memData = computed(() => trend.slice(['mem_used_percent'], SPARK_WINDOW))

onMounted(() => {
  unsubscribe = subscribeHost((payload) => {
    host.value = payload.host
  })
})

onBeforeUnmount(() => {
  if (unsubscribe) unsubscribe()
})

const goDetail = () => router.push('/server-resource')

const fmtPercent = (v) => (typeof v === 'number' ? `${v.toFixed(1)}%` : '-')

const fmtBytes = (bytes) => {
  if (!bytes) return '-'
  const gb = bytes / (1024 * 1024 * 1024)
  if (gb < 1) return `${(bytes / (1024 * 1024)).toFixed(2)} MB`
  return `${gb.toFixed(2)} GB`
}

const progressColor = (percent) => {
  const p = percent || 0
  if (p < 50) return '#00b42a'
  if (p < 70) return '#165dff'
  if (p < 90) return '#ff7d00'
  return '#f53f3f'
}

const iconColor = computed(() => {
  if (!host.value) return '#999'
  const cpu = host.value.cpu?.used_percent || 0
  const mem = host.value.memory?.used_percent || 0
  return progressColor(Math.max(cpu, mem))
})
</script>

<style scoped>
.server-resource-content {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 12px;
  min-width: 320px;
}

.section-title {
  font-weight: 700;
  color: #1d39c4;
  font-size: 16px;
  padding-bottom: 8px;
  border-bottom: 2px solid #1d39c4;
}

.mini-metric {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px;
  background-color: #f5f5f5;
  border-radius: 6px;
}

.mini-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
}

.mini-label {
  font-size: 13px;
  font-weight: 600;
  color: #333;
}

.mini-value {
  font-size: 14px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}

.mini-sub {
  font-size: 12px;
  color: #666;
  text-align: right;
}

.info-item {
  display: flex;
  align-items: center;
  min-height: 32px;
  padding: 8px;
  background-color: #f0f5ff;
  border-radius: 4px;
}

.loading-state {
  gap: 8px;
  justify-content: center;
}

.loading-text {
  color: #1d39c4;
  font-size: 13px;
}
</style>
