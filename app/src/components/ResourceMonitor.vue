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

      <!-- CPU 使用率 -->
      <div class="mini-metric" v-if="resourceData.process?.cpu_percent !== undefined">
        <div class="mini-head">
          <span class="mini-label">CPU 使用率</span>
          <span class="mini-value" :style="{ color: resourceData.render.cpu_percent_color }">
            {{ resourceData.render.cpu_percent_value }}%
          </span>
        </div>
        <UPlotChart
            :series="cpuSeries"
            :data="cpuData"
            :height="CHART_HEIGHT"
            :show-axes="false"
            :fmt-y="fmtPercent"
        />
        <div class="mini-sub" v-if="resourceData.process?.cpu_total_percent !== undefined">
          <span>整机占比 {{ resourceData.render.cpu_total_percent_value }}%</span>
          <span v-if="resourceData.cpu_cores">{{ resourceData.cpu_cores }} 核</span>
        </div>
      </div>

      <!-- 内存使用 -->
      <div class="mini-metric" v-if="resourceData.process?.memory_used">
        <div class="mini-head">
          <span class="mini-label">内存使用</span>
          <span class="mini-value" :style="{ color: resourceData.render.memory_percent_color }">
            {{ resourceData.render.memory_used_formatted }}
          </span>
        </div>
        <UPlotChart
            :series="memSeries"
            :data="memData"
            :height="CHART_HEIGHT"
            :min-y="memMinY"
            :show-axes="false"
            :fmt-y="fmtBytes"
        />
        <div class="mini-sub">
          <span>占用率 {{ resourceData.render.memory_percent_value }}%</span>
          <span>总量 {{ resourceData.render.memory_total_formatted }}</span>
        </div>
      </div>

      <!-- 进程 I/O 速度 -->
      <div class="mini-metric">
        <div class="mini-head">
          <span class="mini-label">
            进程 I/O 速度
            <t-tooltip :content="IO_TIP" placement="top" :overlay-style="{ maxWidth: '300px' }">
              <help-circle-icon class="mini-help"/>
            </t-tooltip>
          </span>
          <span class="mini-values">
            <span class="mini-value io-value">
              <i class="io-dot" :style="{ backgroundColor: COLORS.read }"/>
              读 {{ fmtBytesPerSec(resourceData.disk_io?.read_bytes_per_sec) }}
            </span>
            <span class="mini-value io-value">
              <i class="io-dot" :style="{ backgroundColor: COLORS.write }"/>
              写 {{ fmtBytesPerSec(resourceData.disk_io?.write_bytes_per_sec) }}
            </span>
          </span>
        </div>
        <UPlotChart
            :series="ioSeries"
            :data="ioData"
            :height="CHART_HEIGHT"
            :show-axes="false"
            :fmt-y="fmtBytesPerSec"
        />
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
import {ref, computed, watch, onUnmounted} from 'vue'
import {ErrorCircleFilledIcon, HelpCircleIcon} from 'tdesign-icons-vue-next'
import {getInstanceStatus} from '@/store/serverStore.js'
import UPlotChart from '@/components/UPlotChart.vue'
import {subscribeInstance, subscribeStatus} from '@/composables/useResourceStream.js'
import {useResourceTrend} from '@/composables/useResourceTrend.js'

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

// 资源数据统一走 useResourceStream —— 每个标签页只有一条 /api/server/all-info SSE。
// 这里不再自建 worker/port，否则会多一条 SSE 争抢明文 HTTP 下每个 origin 仅有的
// 6 条并发连接。
let unsubscribe = null
let unsubscribeStatus = null

// 瞬时值（上面那行数字）来自 SSE 的最新一帧，曲线来自 useResourceTrend 的缓冲：
// 后者会先 GET 一次历史再接增量，所以卡片一打开就有过去几分钟的形状，不是空图。
//
// enabled 绑到 isMonitoring：首页 masonry 下每个实例卡片都挂一份，
// 不加这个门，页面一加载就会为每个**没在跑**的实例各发一次历史回填请求。
const SPARK_WINDOW = 300
const trend = useResourceTrend({
  scope: 'instance',
  instanceName: computed(() => props.instanceName),
  enabled: isMonitoring,
  // 只画 5 分钟，没必要按默认的 15 分钟拉
  backfillSeconds: SPARK_WINDOW,
})

const CHART_HEIGHT = 44

const COLORS = {
  primary: '#1d39c4',
  read: '#165dff',
  write: '#ff7d00',
}

// 迷你图上只画 cpu_percent（单核 100% 口径，多核可 >100），整机占比作为下方的
// 数字给出 —— 两条线挤在 44px 里时，整机占比那条会贴着底边看不见。
// 需要两条线对照的完整版在下方的「资源趋势」面板（ResourceTrendPanel）。
const cpuSeries = [{label: 'CPU', stroke: COLORS.primary, fill: 'rgba(29,57,196,0.12)'}]
const memSeries = [{label: '内存', stroke: COLORS.primary, fill: 'rgba(29,57,196,0.12)'}]
const ioSeries = [
  {label: '读', stroke: COLORS.read},
  {label: '写', stroke: COLORS.write},
]

// 内存画的是**占用字节**而不是占用率：两条曲线的形状完全一样（总量是常数），
// 画两张纯属重复，而绝对值更直接。占用率与总量作为数字放在图下方。
const cpuData = computed(() => trend.slice(['cpu_percent'], SPARK_WINDOW))
const memData = computed(() => trend.slice(['memory_used'], SPARK_WINDOW))
const ioData = computed(() => trend.slice(['disk_read_bytes_per_sec', 'disk_write_bytes_per_sec'], SPARK_WINDOW))

// 内存图的纵轴下限不能是 0（CPU 与 I/O 那两张是 0）：ARK 的 RSS 常年稳在 6~10GB
// 上下缓慢漂移，0 起点会把 6.1→6.3GB 这种真正要看的变化压成一条直线。
// 取窗口内的最小值再往下留一点余量，曲线才有形状；绝对值与占用率都在图外的文字里，
// 不存在「贴着底边 = 快没内存了」的误读。
const memMinY = computed(() => {
  const col = memData.value[1] || []
  let lo = Infinity
  let hi = -Infinity
  for (const v of col) {
    if (v == null) continue
    if (v < lo) lo = v
    if (v > hi) hi = v
  }
  if (!isFinite(lo)) return 0
  const span = hi - lo
  return Math.max(0, lo - (span > 0 ? span * 0.25 : lo * 0.02))
})

// 措辞按 docs/RESOURCE_RATE_CHART_PLAN.md §2.1：这是「进程 I/O」不是「磁盘吞吐」，
// Windows 上的口径尤其容易被误读
const IO_TIP = '该进程自身的 I/O 速率。Windows 取自 GetProcessIoCounters，'
    + '统计的是进程的全部 I/O（文件、管道、设备），不等于物理磁盘吞吐；'
    + 'Linux 取自 /proc/<pid>/io 的 read_bytes/write_bytes（真正过块层的量）。'
    + '采不到时曲线断开，与「速率为 0」是两回事。'

const fmtPercent = (v) => (v == null ? '-' : `${v.toFixed(v >= 100 ? 0 : 1)}%`)

const fmtBytes = (v) => {
  if (v == null) return '-'
  const mb = v / (1024 * 1024)
  if (mb < 1024) return `${mb.toFixed(mb < 10 ? 1 : 0)} MB`
  return `${(mb / 1024).toFixed(2)} GB`
}

const fmtBytesPerSec = (v) => {
  if (v == null) return '-'
  if (v < 1024) return `${Math.round(v)} B/s`
  const kb = v / 1024
  if (kb < 1024) return `${kb.toFixed(kb < 10 ? 1 : 0)} KB/s`
  const mb = kb / 1024
  if (mb < 1024) return `${mb.toFixed(mb < 10 ? 2 : 1)} MB/s`
  return `${(mb / 1024).toFixed(2)} GB/s`
}

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

/* 迷你 sparkline 卡片：一行「标题 + 当前值」，下面一条无坐标轴的曲线。
   数值随光标的浮动 tooltip 由 UPlotChart 自带。 */
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
  gap: 8px;
  flex-wrap: wrap;
}

.mini-label {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  font-weight: 600;
  color: #333;
}

.mini-help {
  font-size: 14px;
  color: #999;
  cursor: help;
  align-self: center;
}

.mini-values {
  display: inline-flex;
  align-items: baseline;
  gap: 10px;
  flex-wrap: wrap;
}

.mini-value {
  font-size: 14px;
  font-weight: 700;
  color: #1d39c4;
  font-variant-numeric: tabular-nums;
}

.io-value {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  font-weight: 600;
  color: #666;
}

.io-dot {
  width: 8px;
  height: 2px;
  border-radius: 1px;
  display: inline-block;
}

.mini-sub {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  font-size: 12px;
  color: #666;
  font-variant-numeric: tabular-nums;
}
</style>
