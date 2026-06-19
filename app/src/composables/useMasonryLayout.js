/**
 * 瀑布流布局核心 composable
 *
 * 提供：
 * - 最短列分配算法（layoutItems）
 * - 高度缓存（getHeight / setHeight）
 * - rAF 批量重算调度（scheduleLayout）
 * - 滚动位置记忆工具（readScroll / writeScroll / clearScroll）
 *
 * 设计说明：参考文档采用 `grid-auto-rows + span`，本实现改为像素级绝对定位，
 * 以便配合虚拟滚动安全移除不可见 DOM，并支持 ~2000 条规模。
 */

/**
 * 按指定列数计算单列宽度
 * @param {number} containerWidth 容器可用宽度（px）
 * @param {number} columns 列数
 * @param {number} gap 列间距（px）
 * @returns {number} 单列宽度（px）
 */
export function calcColumns(containerWidth, columns, gap) {
  const cols = Math.max(1, Math.floor(columns) || 1)
  const totalGap = gap * (cols - 1)
  const width = (containerWidth - totalGap) / cols
  return width > 0 ? width : 0
}

/**
 * 参考文档保留的 span 算法（供调试/备用，主路径不使用）
 * @param {number} height 元素高度
 * @param {number} rowHeight grid-auto-rows 高度
 * @param {number} gap 间距
 * @returns {number} 跨行数
 */
export function calcSpan(height, rowHeight, gap) {
  return Math.ceil((height + gap) / (rowHeight + gap))
}

/**
 * 最短列分配，计算每项的绝对定位
 * @param {Array} items 数据项
 * @param {(item: any, index: number) => string|number} getKey 取唯一键
 * @param {Map} heights 高度缓存
 * @param {number} columns 列数
 * @param {number} columnWidth 单列宽度
 * @param {number} gap 间距
 * @param {number} estimatedHeight 未测量时的预估高度
 * @returns {{positions: Array, totalHeight: number}}
 */
export function layoutItems(items, getKey, heights, columns, columnWidth, gap, estimatedHeight) {
  const cols = Math.max(1, Math.floor(columns) || 1)
  const columnHeights = new Array(cols).fill(0)
  const positions = new Array(items.length)

  for (let i = 0; i < items.length; i++) {
    const item = items[i]
    const key = getKey(item, i)
    const height = heights.has(key) ? heights.get(key) : estimatedHeight

    // 找到当前最短列
    let col = 0
    let min = columnHeights[0]
    for (let c = 1; c < cols; c++) {
      if (columnHeights[c] < min) {
        min = columnHeights[c]
        col = c
      }
    }

    const top = columnHeights[col]
    const left = col * (columnWidth + gap)

    positions[i] = {
      key,
      index: i,
      item,
      top,
      left,
      width: columnWidth,
      height,
      measured: heights.has(key),
    }

    columnHeights[col] = top + height + gap
  }

  // 总高度为最高列（去掉末尾多余的 gap）
  let totalHeight = 0
  for (let c = 0; c < cols; c++) {
    if (columnHeights[c] > totalHeight) totalHeight = columnHeights[c]
  }
  if (totalHeight > 0) totalHeight -= gap

  return { positions, totalHeight }
}

/**
 * 创建高度缓存
 */
export function createHeightCache() {
  const cache = new Map()
  return {
    cache,
    get: (key) => cache.get(key),
    has: (key) => cache.has(key),
    /**
     * 写入高度，返回是否发生有意义的变化（> threshold）
     */
    set: (key, value, threshold = 2) => {
      const prev = cache.get(key)
      if (prev !== undefined && Math.abs(prev - value) <= threshold) {
        return false
      }
      cache.set(key, value)
      return true
    },
    delete: (key) => cache.delete(key),
    clear: () => cache.clear(),
    /**
     * 仅保留 validKeys 中的键，清理已删除项的缓存
     */
    prune: (validKeys) => {
      for (const key of cache.keys()) {
        if (!validKeys.has(key)) cache.delete(key)
      }
    },
  }
}

/**
 * 创建一个 rAF 批量调度器，合并同一帧内的多次重算请求
 */
export function createRafScheduler() {
  let rafId = null
  return {
    schedule: (fn) => {
      if (rafId !== null) return
      rafId = requestAnimationFrame(() => {
        rafId = null
        fn()
      })
    },
    cancel: () => {
      if (rafId !== null) {
        cancelAnimationFrame(rafId)
        rafId = null
      }
    },
  }
}

// 模块级内存存储，供 scrollStorage = 'memory' 使用（KeepAlive 场景）
const memoryScrollStore = new Map()

function resolveStorage(storage) {
  try {
    if (storage === 'local') return window.localStorage
    if (storage === 'session') return window.sessionStorage
  } catch {
    // 某些环境下访问 storage 会抛异常（隐私模式等），回退到 memory
    return null
  }
  return null
}

/**
 * 读取记忆的滚动位置
 * @param {string} key 存储键
 * @param {'session'|'local'|'memory'} storage 载体
 * @returns {number|null}
 */
export function readScroll(key, storage) {
  if (!key) return null
  if (storage === 'memory') {
    return memoryScrollStore.has(key) ? memoryScrollStore.get(key) : null
  }
  const s = resolveStorage(storage)
  if (!s) return null
  const raw = s.getItem(key)
  if (raw === null) return null
  const value = Number(raw)
  return Number.isFinite(value) ? value : null
}

/**
 * 写入记忆的滚动位置
 */
export function writeScroll(key, storage, value) {
  if (!key) return
  if (storage === 'memory') {
    memoryScrollStore.set(key, value)
    return
  }
  const s = resolveStorage(storage)
  if (!s) {
    // storage 不可用时回退到内存，保证功能可用
    memoryScrollStore.set(key, value)
    return
  }
  try {
    s.setItem(key, String(value))
  } catch {
    memoryScrollStore.set(key, value)
  }
}

/**
 * 清除记忆的滚动位置
 */
export function clearScroll(key, storage) {
  if (!key) return
  memoryScrollStore.delete(key)
  if (storage === 'memory') return
  const s = resolveStorage(storage)
  if (!s) return
  try {
    s.removeItem(key)
  } catch {
    // ignore
  }
}
