<template>
  <div class="uplot-chart" ref="containerRef">
    <div class="chart-header" v-if="title">
      <span class="chart-title">{{ title }}</span>
      <span class="chart-legend">
        <span
            v-for="(s, i) in legendItems"
            :key="s.label"
            class="legend-item"
        >
          <i class="legend-dot" :style="{ backgroundColor: s.stroke }"/>
          <span class="legend-label">{{ s.label }}</span>
          <span class="legend-value">{{ latestText(i + 1) }}</span>
        </span>
      </span>
    </div>
    <div class="chart-body" ref="chartRef"></div>
  </div>
</template>

<script setup>
import {ref, shallowRef, onMounted, onBeforeUnmount, watch, computed, nextTick} from 'vue'
import uPlot from 'uplot'
import 'uplot/dist/uPlot.min.css'

// uPlot 是命令式 API，这里只做一件事：把它的生命周期收进 Vue 的生命周期。
//
// 数据更新走 setData 而不是重建实例 —— 每 2 秒重建一次图表既浪费又会丢失
// 光标位置。尺寸变化走 ResizeObserver + setSize，uPlot 不会自己跟随容器。
const props = defineProps({
  title: {type: String, default: ''},
  // series: [{ label, stroke, fill? }]，与 data 的第 2..n 项一一对应
  series: {type: Array, default: () => []},
  // data: [xs, ys1, ys2...]，xs 为秒级时间戳。null 表示断点
  data: {type: Array, default: () => []},
  height: {type: Number, default: 120},
  // 纵轴与图例的数值格式化
  fmtY: {type: Function, default: (v) => (v == null ? '-' : String(Math.round(v)))},
  // 纵轴下限固定为 0（占用率、速率都不会为负），上限自适应
  minY: {type: Number, default: 0},
  // 固定纵轴上限，例如百分比图传 100；不传则自适应
  maxY: {type: Number, default: null},
  showAxes: {type: Boolean, default: true},
  // 纵轴留白宽度，速率类（"9.67 MB/s"）比百分比需要更宽
  axisWidth: {type: Number, default: 76},
})

const containerRef = ref(null)
const chartRef = ref(null)
// 图表实例不需要响应式代理：uPlot 内部有大量自引用对象，
// 被 reactive 包一层会显著拖慢每次 setData。
const chart = shallowRef(null)
let resizeObserver = null

const legendItems = computed(() => props.series)

// uPlot 没有内置浮动 tooltip，自带的「跟随光标显示数值」是那块 legend 表格，
// 已被 legend:{show:false} 关掉。这个插件在 .u-over 里塞一个绝对定位的浮层，
// 用 setCursor 钩子拿 cursor.idx（光标下的数据下标）逐 series 填值。
const tooltipPlugin = (fmtY) => {
  const fmtX = (t) => (t == null ? '' : new Date(t * 1000).toLocaleTimeString())
  let tip = null

  return {
    hooks: {
      init(u) {
        tip = document.createElement('div')
        tip.className = 'u-tooltip'
        tip.style.display = 'none'
        u.over.appendChild(tip)
        u.over.addEventListener('mouseleave', () => {
          if (tip) tip.style.display = 'none'
        })
      },
      setCursor(u) {
        if (!tip) return
        const {idx, left, top} = u.cursor
        if (idx == null || left == null || left < 0) {
          tip.style.display = 'none'
          return
        }
        let html = `<div class="u-tt-x">${fmtX(u.data[0][idx])}</div>`
        for (let i = 1; i < u.series.length; i++) {
          const s = u.series[i]
          if (s.show === false) continue
          html += `<div class="u-tt-row">`
            + `<i style="background:${s.stroke}"></i>`
            + `<span>${s.label ?? ''}</span>`
            + `<b>${fmtY(u.data[i][idx])}</b>`
            + `</div>`
        }
        tip.innerHTML = html
        tip.style.display = 'block'

        // 跟随光标，贴近边界时翻到另一侧，避免溢出裁切
        const tw = tip.offsetWidth
        const th = tip.offsetHeight
        let x = left + 12
        if (x + tw > u.over.clientWidth) x = left - tw - 12
        let y = top + 12
        if (y + th > u.over.clientHeight) y = top - th - 12
        tip.style.transform = `translate(${Math.max(x, 0)}px, ${Math.max(y, 0)}px)`
      },
    },
  }
}

const latestText = (seriesIdx) => {
  const col = props.data[seriesIdx]
  if (!col || col.length === 0) return '-'
  for (let i = col.length - 1; i >= 0; i--) {
    if (col[i] != null) return props.fmtY(col[i])
  }
  return '-'
}

const buildOptions = (width) => ({
  width,
  height: props.height,
  // 自带的 legend 是一整块表格，占地方且与卡片风格不搭，改用上面自绘的那行
  legend: {show: false},
  // legend 关了就没有跟随光标的数值，用插件补一个浮动 tooltip
  plugins: [tooltipPlugin(props.fmtY)],
  cursor: {
    y: false,
    points: {size: 5},
  },
  scales: {
    x: {time: true},
    y: {
      range: (u, dataMin, dataMax) => {
        const min = props.minY ?? dataMin
        if (props.maxY != null) return [min, props.maxY]
        // 全 null 或全 0 时给一个像样的默认量程，免得画出一条贴边的线
        if (dataMax == null || !isFinite(dataMax) || dataMax <= 0) return [min, 1]
        return [min, dataMax * 1.15]
      },
    },
  },
  axes: [
    {
      show: props.showAxes,
      stroke: '#8a8a8a',
      grid: {stroke: 'rgba(0,0,0,0.06)', width: 1},
      ticks: {stroke: 'rgba(0,0,0,0.1)'},
    },
    {
      show: props.showAxes,
      stroke: '#8a8a8a',
      // 刻度文字可能宽到 "1.23 MB/s"，默认宽度会把它切掉
      size: props.axisWidth,
      grid: {stroke: 'rgba(0,0,0,0.06)', width: 1},
      ticks: {stroke: 'rgba(0,0,0,0.1)'},
      values: (u, ticks) => ticks.map((v) => props.fmtY(v)),
    },
  ],
  series: [
    {},
    ...props.series.map((s) => ({
      label: s.label,
      stroke: s.stroke,
      fill: s.fill,
      width: 1.6,
      // 断点必须显示为空洞：null 在这里表示「采不到」，不是 0
      spanGaps: false,
      points: {show: false},
      value: (u, v) => props.fmtY(v),
    })),
  ],
})

const currentWidth = () => {
  const el = chartRef.value
  if (!el) return 300
  return Math.max(el.clientWidth || el.offsetWidth || 300, 120)
}

const create = () => {
  if (!chartRef.value || chart.value) return
  chart.value = new uPlot(buildOptions(currentWidth()), normalized(), chartRef.value)
}

// uPlot 要求至少有 x 轴数组；空数据时给一对空数组，避免它抛异常
const normalized = () => {
  if (!props.data || props.data.length === 0) {
    return [[], ...props.series.map(() => [])]
  }
  return props.data
}

const destroy = () => {
  if (chart.value) {
    chart.value.destroy()
    chart.value = null
  }
}

watch(
    () => props.data,
    () => {
      if (chart.value) chart.value.setData(normalized())
    },
    {deep: false}
)

// series 变了（比如实例网络图从「不支持」切成有数据）只能重建
watch(
    () => props.series,
    async () => {
      destroy()
      await nextTick()
      create()
    }
)

onMounted(() => {
  create()
  if (typeof ResizeObserver !== 'undefined' && chartRef.value) {
    resizeObserver = new ResizeObserver(() => {
      if (chart.value) chart.value.setSize({width: currentWidth(), height: props.height})
    })
    resizeObserver.observe(chartRef.value)
  }
})

onBeforeUnmount(() => {
  if (resizeObserver) {
    resizeObserver.disconnect()
    resizeObserver = null
  }
  destroy()
})
</script>

<style scoped>
.uplot-chart {
  width: 100%;
}

.chart-header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 2px;
  flex-wrap: wrap;
}

.chart-title {
  font-size: 13px;
  font-weight: 600;
  color: #333;
}

.chart-legend {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.legend-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: #666;
}

.legend-dot {
  width: 8px;
  height: 2px;
  border-radius: 1px;
  display: inline-block;
}

.legend-value {
  font-weight: 600;
  color: #1d39c4;
  font-variant-numeric: tabular-nums;
}

.chart-body {
  width: 100%;
}

.chart-body :deep(.u-over) {
  cursor: crosshair;
}

/* 插件运行时插入到 .u-over 里的浮层，scoped 下需要 :deep 穿透 */
.chart-body :deep(.u-tooltip) {
  position: absolute;
  top: 0;
  left: 0;
  z-index: 10;
  pointer-events: none;
  padding: 6px 8px;
  font-size: 12px;
  line-height: 1.5;
  white-space: nowrap;
  background: rgba(255, 255, 255, 0.96);
  border: 1px solid rgba(0, 0, 0, 0.1);
  border-radius: 6px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.12);
}

.chart-body :deep(.u-tt-x) {
  margin-bottom: 4px;
  color: #999;
}

.chart-body :deep(.u-tt-row) {
  display: flex;
  align-items: center;
  gap: 6px;
  font-variant-numeric: tabular-nums;
}

.chart-body :deep(.u-tt-row i) {
  width: 8px;
  height: 2px;
  border-radius: 1px;
  flex: none;
}

.chart-body :deep(.u-tt-row b) {
  margin-left: auto;
  padding-left: 12px;
  color: #1d39c4;
}
</style>
