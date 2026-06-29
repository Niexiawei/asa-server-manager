<template>
  <div class="vll-viewport" ref="viewportRef" @scroll.passive="onScroll">
    <template v-if="items.length === 0">
      <slot name="empty"/>
    </template>
    <template v-else>
      <div class="vll-spacer" :style="{ height: topSpacerHeight + 'px' }"></div>
      <div
          v-for="entry in renderedItems"
          :key="entry.index"
          class="vll-item"
          :data-index="entry.index"
      >
        <slot name="item" :item="entry.item" :index="entry.index"/>
      </div>
      <div class="vll-spacer" :style="{ height: bottomSpacerHeight + 'px' }"></div>
    </template>
  </div>
</template>

<script setup>
import {computed, nextTick, onBeforeUnmount, onMounted, ref, watch} from 'vue'
import {useElementSize} from '@vueuse/core'

const props = defineProps({
  estimatedItemHeight: {type: Number, default: 28},
  buffer: {type: Number, default: 300},
  autoScroll: {type: Boolean, default: true},
})

const BOTTOM_THRESHOLD = 50

const viewportRef = ref(null)
const {width: vpWidth, height: vpHeightRaw} = useElementSize(viewportRef)
const viewportHeight = computed(() => vpHeightRaw.value || 600)

// 'anchored' = 固定在底部自动追踪；'free' = 用户自由滚动
const mode = ref('anchored')
const scrollTop = ref(0)

// 内部数据
const items = ref([])

// 实测高度缓存
const heightMap = new Map()
const heightVersion = ref(0)

function getHeight(i) {
  return heightMap.get(i) ?? props.estimatedItemHeight
}

// 前缀和：sums[i] = items[0..i-1] 的高度总和
const prefixSums = computed(() => {
  heightVersion.value // 追踪依赖
  const len = items.value.length
  const s = new Array(len + 1)
  s[0] = 0
  for (let i = 0; i < len; i++) s[i + 1] = s[i] + getHeight(i)
  return s
})

// 底部锚定模式下最多渲染的条目数（视口高度 + 上下 buffer）
const capacity = computed(() =>
    Math.ceil((viewportHeight.value + props.buffer * 2) / props.estimatedItemHeight) + 10
)

// 首个满足 s[i] >= target 的下标（二分查找）
function lowerBound(s, target) {
  let lo = 0, hi = s.length - 1
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (s[mid] < target) lo = mid + 1
    else hi = mid
  }
  return lo
}

const visibleRange = computed(() => {
  const len = items.value.length
  if (!len) return {start: 0, end: 0}

  if (mode.value === 'anchored') {
    // 只渲染末尾 capacity 条，高度全部实测，bottomSpacer 始终为 0
    return {start: Math.max(0, len - capacity.value), end: len}
  }

  // 自由滚动：二分定位可见范围
  const s = prefixSums.value
  const st = Math.max(0, scrollTop.value)
  const bot = st + viewportHeight.value
  const start = Math.max(0, lowerBound(s, Math.max(0, st - props.buffer)) - 1)
  const end = Math.min(len, lowerBound(s, bot + props.buffer) + 1)
  return {start, end}
})

const renderedItems = computed(() => {
  const {start, end} = visibleRange.value
  const out = []
  for (let i = start; i < end; i++) out.push({index: i, item: items.value[i]})
  return out
})

const topSpacerHeight = computed(() => {
  const {start} = visibleRange.value
  return start > 0 ? prefixSums.value[start] : 0
})

const bottomSpacerHeight = computed(() => {
  const {end} = visibleRange.value
  const len = items.value.length
  if (end >= len) return 0
  const s = prefixSums.value
  return s[len] - s[end]
})

// ===== 滚动处理 =====
// _isPinning：即时滚动的一次性标志，消费后清除
// _smoothScrolling：平滑滚动进行中，期间忽略模式切换
// _smoothScrollAbortFn：取消当前平滑滚动的清理函数
let _isPinning = false
let _smoothScrolling = false
let _smoothScrollAbortFn = null

function onScroll() {
  if (_isPinning) {
    _isPinning = false
    return
  }

  const el = viewportRef.value
  if (!el) return
  const dist = el.scrollHeight - el.scrollTop - el.clientHeight

  if (_smoothScrolling) {
    // 程序触发的平滑滚动期间：追踪位置供虚拟渲染使用，不切换 mode
    scrollTop.value = el.scrollTop
    if (dist <= BOTTOM_THRESHOLD) mode.value = 'anchored'
    return
  }

  if (dist > BOTTOM_THRESHOLD) {
    scrollTop.value = el.scrollTop
    if (mode.value !== 'free') mode.value = 'free'
  } else if (mode.value !== 'anchored') {
    mode.value = 'anchored'
  }
}

function pinToBottom() {
  if (mode.value !== 'anchored') return
  if (_smoothScrolling) return // 平滑滚动进行中，不中断
  const el = viewportRef.value
  if (!el) return
  const target = el.scrollHeight - el.clientHeight
  if (Math.abs(el.scrollTop - target) < 1) return
  _isPinning = true
  el.scrollTop = target
}

function _startSmoothScroll(el, target) {
  if (_smoothScrollAbortFn) { _smoothScrollAbortFn(); _smoothScrollAbortFn = null }
  _smoothScrolling = true

  const cleanup = (shouldAnchor) => {
    _smoothScrolling = false
    _smoothScrollAbortFn = null
    clearTimeout(fallbackTimer)
    el.removeEventListener('scrollend', onEnd)
    if (shouldAnchor) {
      mode.value = 'anchored'
      nextTick(pinToBottom) // 精确对齐到底部
    }
  }

  const onEnd = () => {
    const dist = el.scrollHeight - el.scrollTop - el.clientHeight
    cleanup(dist <= BOTTOM_THRESHOLD) // 若用户中途打断滚动则不强制切回 anchored
  }

  // scrollend 兜底：600ms 后若事件未触发则强制收尾
  const fallbackTimer = setTimeout(() => cleanup(true), 600)

  el.addEventListener('scrollend', onEnd, {once: true})
  _smoothScrollAbortFn = () => cleanup(false)

  el.scrollTo({top: target, behavior: 'smooth'})
}

// 底部锚定模式下追加新条目时自动固定到底部
watch(() => items.value.length, (n, o) => {
  if (n > o && mode.value === 'anchored') nextTick(pinToBottom)
}, {flush: 'post'})

// 视口宽度变化时清除高度缓存（换行数变化）
watch(vpWidth, () => {
  heightMap.clear()
  heightVersion.value++
  if (mode.value === 'anchored') nextTick(pinToBottom)
})

// ===== RAF 批量缓冲 =====
let _pending = []
let _batchRaf = null

const _flush = () => {
  if (!_pending.length) return
  items.value.push(..._pending)
  _pending = []
  _batchRaf = null
  if (props.autoScroll) scrollToBottom(true)
}

// ===== ResizeObserver 高度测量 =====
let ro = null
let roRaf = null

function syncObserver() {
  if (!ro || !viewportRef.value) return
  ro.disconnect()
  viewportRef.value.querySelectorAll('.vll-item').forEach(el => ro.observe(el))
}

// 渲染后重建观察集合
watch(renderedItems, () => {
  if (roRaf !== null) return
  roRaf = requestAnimationFrame(() => {
    roRaf = null
    syncObserver()
  })
}, {flush: 'post'})

onMounted(() => {
  ro = new ResizeObserver((entries) => {
    let changed = false
    for (const e of entries) {
      const idx = Number(e.target.dataset.index)
      const h = e.target.offsetHeight
      if (h > 0 && heightMap.get(idx) !== h) {
        heightMap.set(idx, h)
        changed = true
      }
    }
    if (changed) {
      heightVersion.value++
      if (mode.value === 'anchored') nextTick(pinToBottom)
    }
  })
  nextTick(pinToBottom)
})

onBeforeUnmount(() => {
  ro?.disconnect()
  ro = null
  if (roRaf !== null) cancelAnimationFrame(roRaf)
  if (_batchRaf !== null) cancelAnimationFrame(_batchRaf)
  if (_smoothScrollAbortFn) _smoothScrollAbortFn()
})

// ===== 对外 API =====
function push(item) {
  _pending.push(item)
  if (_batchRaf === null) _batchRaf = requestAnimationFrame(_flush)
}

function clear() {
  items.value = []
  _pending = []
  if (_batchRaf !== null) { cancelAnimationFrame(_batchRaf); _batchRaf = null }
  mode.value = 'anchored'
}

function scrollToBottom(smooth = true) {
  nextTick(() => {
    const el = viewportRef.value
    if (!el) return
    const target = el.scrollHeight - el.clientHeight
    if (Math.abs(el.scrollTop - target) < 1) { mode.value = 'anchored'; return }
    if (smooth) {
      _startSmoothScroll(el, target)
    } else {
      mode.value = 'anchored'
      _isPinning = true
      el.scrollTop = target
    }
  })
}

function scrollToTop() {
  mode.value = 'free'
  scrollTop.value = 0
  nextTick(() => {
    const el = viewportRef.value
    if (!el) return
    if (el.scrollTop === 0) return
    _isPinning = true
    el.scrollTop = 0
  })
}

function scrollToIndex(index) {
  const s = prefixSums.value
  const target = s[Math.min(Math.max(0, index), items.value.length)] ?? 0
  mode.value = 'free'
  scrollTop.value = target
  nextTick(() => {
    const el = viewportRef.value
    if (!el) return
    if (Math.abs(el.scrollTop - target) < 1) return
    _isPinning = true
    el.scrollTop = target
  })
}

const itemCount = computed(() => items.value.length)
const getItems = () => items.value

defineExpose({push, clear, scrollToBottom, scrollToTop, scrollToIndex, itemCount, getItems})
</script>

<style scoped>
.vll-viewport {
  height: 100%;
  width: 100%;
  overflow-y: auto;
  overflow-x: hidden;
  /* 禁用浏览器原生滚动锚定，由组件自身管理 */
  overflow-anchor: none;
  box-sizing: border-box;
}

.vll-spacer {
  flex-shrink: 0;
  pointer-events: none;
}

.vll-item {
  box-sizing: border-box;
}
</style>
