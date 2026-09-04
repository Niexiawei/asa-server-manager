<template>
  <div class="resource-trend-panel">
    <div class="trend-toolbar">
      <span class="trend-hint">{{ scope === 'host' ? '整机资源趋势' : '实例资源趋势' }}</span>
      <t-radio-group v-model="windowSeconds" variant="default-filled" size="small">
        <t-radio-button :value="180">3 分钟</t-radio-button>
        <t-radio-button :value="360">6 分钟</t-radio-button>
        <t-radio-button :value="900">15 分钟</t-radio-button>
      </t-radio-group>
    </div>

    <div class="trend-charts">
      <UPlotChart
          title="CPU 使用率"
          :series="cpuSeries"
          :data="cpuData"
          :height="chartHeight"
          :max-y="scope === 'host' ? 100 : null"
          :fmt-y="fmtPercent"
      />

      <UPlotChart
          title="内存使用率"
          :series="memSeries"
          :data="memData"
          :height="chartHeight"
          :max-y="100"
          :fmt-y="fmtPercent"
      />

      <UPlotChart
          :title="scope === 'host' ? '磁盘读写速度' : '进程 I/O 速度'"
          :series="diskSeries"
          :data="diskData"
          :height="chartHeight"
          :fmt-y="fmtBytesPerSec"
      />

      <UPlotChart
          :title="scope === 'host' ? '磁盘 IOPS' : '进程 I/O 次数'"
          :series="iopsSeries"
          :data="iopsData"
          :height="chartHeight"
          :fmt-y="fmtIOPS"
      />

      <UPlotChart
          v-if="scope === 'host' || hasInstanceNet"
          title="网络收发速度"
          :series="netSeries"
          :data="netData"
          :height="chartHeight"
          :fmt-y="fmtBytesPerSec"
      />
      <div v-else class="trend-placeholder">
        <div class="placeholder-title">网络收发速度</div>
        <div class="placeholder-text">
          当前平台不支持按进程网络计量（需 Linux + eBPF），整机网络请看服务器资源监控页
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import {computed, ref, watch} from 'vue'
import UPlotChart from '@/components/UPlotChart.vue'
import {useResourceTrend} from '@/composables/useResourceTrend.js'

const props = defineProps({
  scope: {type: String, default: 'instance'}, // 'instance' | 'host'
  instanceName: {type: String, default: ''},
  chartHeight: {type: Number, default: 110},
})

// 窗口选择按 scope 分别记忆：看整机和看单实例的习惯往往不同
const storageKey = computed(() => `resTrend.window.${props.scope}`)
const readWindow = () => {
  const raw = Number(localStorage.getItem(storageKey.value))
  return [180, 360, 900].includes(raw) ? raw : 360
}
const windowSeconds = ref(readWindow())
watch(windowSeconds, (v) => localStorage.setItem(storageKey.value, String(v)))

const trend = useResourceTrend({
  scope: props.scope,
  instanceName: computed(() => props.instanceName),
})

const COLORS = {
  primary: '#1d39c4',
  secondary: '#00b42a',
  read: '#165dff',
  write: '#ff7d00',
  recv: '#00b42a',
  sent: '#f53f3f',
}

const isHost = props.scope === 'host'

// 实例 CPU 画两条线：cpu_percent 是单核 100% 口径（多核可 >100），
// cpu_total_percent 是整机占比 —— 与资源占用卡片上的两个环形进度条一一对应
const cpuSeries = isHost
    ? [{label: 'CPU', stroke: COLORS.primary, fill: 'rgba(29,57,196,0.08)'}]
    : [
      {label: '进程占用', stroke: COLORS.primary},
      {label: '整机占比', stroke: COLORS.secondary},
    ]
const cpuFields = isHost ? ['cpu_used_percent'] : ['cpu_percent', 'cpu_total_percent']

const memSeries = [{label: '内存', stroke: COLORS.primary, fill: 'rgba(29,57,196,0.08)'}]
const memFields = isHost ? ['mem_used_percent'] : ['memory_percent']

const diskSeries = [
  {label: '读', stroke: COLORS.read},
  {label: '写', stroke: COLORS.write},
]
const diskFields = ['disk_read_bytes_per_sec', 'disk_write_bytes_per_sec']

const iopsSeries = [
  {label: '读', stroke: COLORS.read},
  {label: '写', stroke: COLORS.write},
]
const iopsFields = ['disk_read_iops', 'disk_write_iops']

const netSeries = [
  {label: '接收', stroke: COLORS.recv},
  {label: '发送', stroke: COLORS.sent},
]
const netFields = ['net_recv_bytes_per_sec', 'net_sent_bytes_per_sec']

const cpuData = computed(() => trend.slice(cpuFields, windowSeconds.value))
const memData = computed(() => trend.slice(memFields, windowSeconds.value))
const diskData = computed(() => trend.slice(diskFields, windowSeconds.value))
const iopsData = computed(() => trend.slice(iopsFields, windowSeconds.value))
const netData = computed(() => trend.slice(netFields, windowSeconds.value))

// 实例网络只有 Linux + eBPF 可用时才有值，否则整列都是 null，画出来是空图，
// 不如直接说明原因
const hasInstanceNet = computed(() => trend.hasData('net_recv_bytes_per_sec'))

const fmtPercent = (v) => (v == null ? '-' : `${v.toFixed(v >= 100 ? 0 : 1)}%`)

const fmtIOPS = (v) => (v == null ? '-' : Math.round(v).toString())

const fmtBytesPerSec = (v) => {
  if (v == null) return '-'
  if (v < 1024) return `${Math.round(v)} B/s`
  const kb = v / 1024
  if (kb < 1024) return `${kb.toFixed(kb < 10 ? 1 : 0)} KB/s`
  const mb = kb / 1024
  if (mb < 1024) return `${mb.toFixed(mb < 10 ? 2 : 1)} MB/s`
  return `${(mb / 1024).toFixed(2)} GB/s`
}
</script>

<style lang="less" scoped>
.resource-trend-panel {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.trend-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  position: sticky;
  top: 0;
  z-index: 99;
}

.trend-hint {
  font-size: 16px;
  color: #666;
}

.trend-charts {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.trend-placeholder {
  padding: 12px;
  background-color: #f5f5f5;
  border-radius: 6px;
}

.placeholder-title {
  font-size: 13px;
  font-weight: 600;
  color: #333;
  margin-bottom: 4px;
}

.placeholder-text {
  font-size: 12px;
  color: #999;
  line-height: 1.5;
}
</style>
