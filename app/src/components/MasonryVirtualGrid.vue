<template>
  <div
      ref="viewportRef"
      class="masonry-viewport"
      @scroll="onScroll"
  >
    <div
        ref="containerRef"
        class="masonry-container"
        :style="{ height: totalHeight + 'px' }"
    >
      <div
          v-for="entry in visibleEntries"
          :key="entry.key"
          class="masonry-item"
          :style="entry.style"
          :data-index="entry.index"
      >
        <slot :item="entry.item" :index="entry.index"/>
      </div>
    </div>
  </div>
</template>

<script setup>
import {computed, nextTick, onActivated, onBeforeUnmount, onDeactivated, onMounted, ref, watch} from 'vue'
import {useElementSize} from '@vueuse/core'
import {
  calcColumns,
  clearScroll,
  createHeightCache,
  createRafScheduler,
  layoutItems,
  readScroll,
  writeScroll,
} from '@/composables/useMasonryLayout.js'

const props = defineProps({
  items: {type: Array, default: () => []},
  keyField: {type: String, default: 'name'},
  columns: {type: Number, default: 2},
  gap: {type: Number, default: 10},
  buffer: {type: Number, default: 400},
  estimatedHeight: {type: Number, default: 380},
  virtualThreshold: {type: Number, default: 0},
  rememberScroll: {type: Boolean, default: false},
  scrollStorageKey: {type: String, default: ''},
  scrollStorage: {
    type: String,
    default: 'session',
    validator: (v) => ['session', 'local', 'memory'].includes(v),
  },
})

const viewportRef = ref(null)
const containerRef = ref(null)

const heightCache = createHeightCache()
const raf = createRafScheduler()
const layoutVersion = ref(0)

const scrollTop = ref(0)
const containerWidth = ref(0)
const clientHeight = ref(0)

const {width: elWidth, height: elHeight} = useElementSize(viewportRef)

const getKey = (item, index) => {
  const k = item?.[props.keyField]
  return k !== undefined && k !== null ? k : index
}

const resolvedKey = computed(() => props.scrollStorageKey || 'masonry-scroll')

const columnWidth = computed(() => calcColumns(containerWidth.value, props.columns, props.gap))

const layoutResult = computed(() => {
  layoutVersion.value
  return layoutItems(
      props.items,
      getKey,
      heightCache.cache,
      props.columns,
      columnWidth.value,
      props.gap,
      props.estimatedHeight,
  )
})

const positions = computed(() => layoutResult.value.positions)
const totalHeight = computed(() => layoutResult.value.totalHeight)

const useVirtual = computed(() => {
  if (props.virtualThreshold <= 0) return true
  return props.items.length > props.virtualThreshold
})

function buildEntryStyle(p) {
  return {
    position: 'absolute',
    top: p.top + 'px',
    left: p.left + 'px',
    width: p.width + 'px',
  }
}

const visibleEntries = computed(() => {
  const arr = positions.value
  if (!useVirtual.value) {
    return arr.map((p) => ({
      key: p.key,
      index: p.index,
      item: p.item,
      style: buildEntryStyle(p),
    }))
  }

  const ch = clientHeight.value || 0
  const top = scrollTop.value - props.buffer
  const bottom = scrollTop.value + ch + props.buffer
  const result = []
  for (let i = 0; i < arr.length; i++) {
    const p = arr[i]
    if (p.top + p.height >= top && p.top <= bottom) {
      result.push({
        key: p.key,
        index: p.index,
        item: p.item,
        style: buildEntryStyle(p),
      })
    }
  }
  return result
})

function scheduleLayout() {
  raf.schedule(() => {
    layoutVersion.value++
  })
}

// ===== 尺寸同步 =====
function syncViewportSize() {
  containerWidth.value = viewportRef.value?.clientWidth ?? 0
  clientHeight.value = viewportRef.value?.clientHeight ?? 0
}

function syncViewportSizeWithRetry(retries = 2) {
  syncViewportSize()
  if (clientHeight.value > 0 || retries <= 0) return
  requestAnimationFrame(() => syncViewportSizeWithRetry(retries - 1))
}

watch([elWidth, elHeight], syncViewportSize)

watch(totalHeight, () => {
  nextTick(syncViewportSize)
})

// ===== 高度测量（ResizeObserver）=====
let ro = null
let observerRaf = null

function measureElement(el) {
  const idx = Number(el.dataset.index)
  const pos = positions.value[idx]
  if (!pos) return false
  const h = el.offsetHeight
  if (h > 0 && heightCache.set(pos.key, h)) return true
  return false
}

function measureAll() {
  if (!containerRef.value) return
  let changed = false
  const els = containerRef.value.querySelectorAll('.masonry-item')
  els.forEach((el) => {
    if (measureElement(el)) changed = true
  })
  if (changed) scheduleLayout()
}

function syncObserver() {
  if (!ro || !containerRef.value) return
  ro.disconnect()
  const els = containerRef.value.querySelectorAll('.masonry-item')
  els.forEach((el) => ro.observe(el))
}

function scheduleSyncObserver() {
  if (observerRaf !== null) return
  observerRaf = requestAnimationFrame(() => {
    observerRaf = null
    syncObserver()
  })
}

watch(visibleEntries, () => {
  scheduleSyncObserver()
}, {flush: 'post'})

watch(() => props.items, (items) => {
  const valid = new Set(items.map((it, i) => getKey(it, i)))
  heightCache.prune(valid)
  scheduleLayout()
  nextTick(() => {
    scheduleSyncObserver()
    measureAll()
  })
})

watch(() => [props.columns, props.gap], () => {
  scheduleLayout()
})

// ===== 滚动处理 =====
let scrollRaf = null

function onScroll() {
  if (scrollRaf !== null) return
  scrollRaf = requestAnimationFrame(() => {
    scrollRaf = null
    scrollTop.value = viewportRef.value?.scrollTop ?? 0
    clientHeight.value = viewportRef.value?.clientHeight ?? clientHeight.value
    if (props.rememberScroll) scheduleSaveScroll()
  })
}

// ===== 滚动位置记忆 =====
let saveTimer = null

function scheduleSaveScroll() {
  if (saveTimer) clearTimeout(saveTimer)
  saveTimer = setTimeout(() => {
    saveTimer = null
    writeScroll(resolvedKey.value, props.scrollStorage, scrollTop.value)
  }, 100)
}

function saveScrollPosition() {
  writeScroll(resolvedKey.value, props.scrollStorage, scrollTop.value)
}

function restoreScrollPosition() {
  if (!props.rememberScroll) return
  const v = readScroll(resolvedKey.value, props.scrollStorage)
  if (v === null) return
  nextTick(() => {
    if (viewportRef.value) {
      viewportRef.value.scrollTop = v
      scrollTop.value = viewportRef.value.scrollTop
    }
  })
}

function clearScrollPosition() {
  clearScroll(resolvedKey.value, props.scrollStorage)
  scrollTop.value = 0
  if (viewportRef.value) viewportRef.value.scrollTop = 0
}

function relayout() {
  syncViewportSizeWithRetry()
  scheduleLayout()
  nextTick(() => {
    scheduleSyncObserver()
    measureAll()
  })
}

function initLayout() {
  syncViewportSizeWithRetry()
  nextTick(() => {
    scheduleSyncObserver()
    measureAll()
    restoreScrollPosition()
  })
}

onMounted(() => {
  ro = new ResizeObserver((entries) => {
    let changed = false
    for (const entry of entries) {
      if (measureElement(entry.target)) changed = true
    }
    if (changed) scheduleLayout()
  })
  initLayout()
})

onActivated(() => {
  initLayout()
})

onDeactivated(() => {
  if (props.rememberScroll) saveScrollPosition()
})

onBeforeUnmount(() => {
  if (props.rememberScroll) saveScrollPosition()
  ro?.disconnect()
  ro = null
  raf.cancel()
  if (saveTimer) clearTimeout(saveTimer)
  if (scrollRaf !== null) cancelAnimationFrame(scrollRaf)
  if (observerRaf !== null) cancelAnimationFrame(observerRaf)
})

defineExpose({
  clearScrollPosition,
  saveScrollPosition,
  restoreScrollPosition,
  relayout,
})
</script>

<style scoped lang="less">
.masonry-viewport {
  flex: 1;
  min-height: 0;
  width: 100%;
  overflow-y: auto;
  overflow-x: hidden;
  box-sizing: border-box;
}

.masonry-container {
  position: relative;
  width: 100%;
}

.masonry-item {
  box-sizing: border-box;
}
</style>
