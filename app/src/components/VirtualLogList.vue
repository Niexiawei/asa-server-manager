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
// _lastScrollTop：上一次 onScroll 观察到的 scrollTop，用于判断滚动方向
let _isPinning = false
let _smoothScrolling = false
let _smoothScrollAbortFn = null
let _lastScrollTop = 0

function onScroll() {
  const el = viewportRef.value
  if (!el) return
  const st = el.scrollTop

  if (_isPinning) {
    _isPinning = false
    _lastScrollTop = st // 程序跳转也要刷新基线，否则下一次被误判为 movedUp
    return
  }

  const movedUp = st < _lastScrollTop - 0.5
  _lastScrollTop = st
  const dist = el.scrollHeight - st - el.clientHeight

  if (_smoothScrolling) {
    // 程序触发的平滑滚动期间：追踪位置供虚拟渲染使用，不切换 mode
    scrollTop.value = st
    if (dist <= BOTTOM_THRESHOLD) mode.value = 'anchored'
    return
  }

  if (mode.value === 'anchored') {
    // 非程序触发的向上移动且确实离开了底部带 → 立即让位，不再等 dist 越过 50px。
    // 高频日志场景里用户一次只滚一行(~28px)也能停住，不会被下一条日志拽回底部。
    // dist > 4 的护栏：内容收缩时浏览器把 scrollTop 向下夹取，movedUp 会为真但 dist≈0，
    // 那不是用户离开，忽略。
    if (movedUp && dist > 4) {
      mode.value = 'free'
      scrollTop.value = st
    }
    return
  }

  // free 模式：滚回底部附近（大阈值，手感友好）时重新吸附
  scrollTop.value = st
  if (dist <= BOTTOM_THRESHOLD) mode.value = 'anchored'
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

// 底部锚定模式下追加新条目时自动固定到底部（即时 pinToBottom，不启动平滑滚动）。
// 高频场景下平滑滚动只会互相 abort、永远收不了尾；平滑效果留给显式 scrollToBottom()。
watch(() => items.value.length, (n, o) => {
  if (n > o && mode.value === 'anchored' && props.autoScroll) nextTick(pinToBottom)
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
  // 追底交给 items.length watcher 的 pinToBottom 统一负责（即时、不会被 abort）。
  // 这里不再每批启动平滑滚动，否则洪流下动画互相取消、视口永远追不上底部。
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
  _lastScrollTop = 0 // 内容清空后 scrollTop 归零，重置基线避免下一帧被误判为 movedUp
}

function scrollToBottom(smooth = true) {
  nextTick(() => {
    const el = viewportRef.value
    if (!el) return
    const startTarget = el.scrollHeight - el.clientHeight
    if (Math.abs(el.scrollTop - startTarget) < 1) { mode.value = 'anchored'; return }

    if (smooth) {
      const distFromBottom = startTarget - el.scrollTop
      if (mode.value === 'anchored' && distFromBottom > viewportHeight.value) {
        // anchored 模式下距底部超过一屏：topSpacer 是大片空白，动画前段会全部滑过空白区
        // 先切为 free 模式，让 visibleRange 根据 scrollTop.value 渲染当前位置的日志
        mode.value = 'free'
        scrollTop.value = el.scrollTop
        nextTick(() => {
          // 等 Vue 更新 DOM（spacer 重算）后再取准确的 scrollHeight
          const target = el.scrollHeight - el.clientHeight
          _startSmoothScroll(el, target)
        })
      } else {
        // 近距离（≤ 一屏）或已在 free 模式：直接启动，onScroll 会追踪 scrollTop.value
        _startSmoothScroll(el, startTarget)
      }
    } else {
      mode.value = 'anchored'
      _isPinning = true
      el.scrollTop = startTarget
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
  /* 建立 BFC：slot 内容用 margin 做行距时也计入 offsetHeight，
     使 ResizeObserver 的实测高度与真实 scrollHeight 一致 */
  display: flow-root;
}
</style>
