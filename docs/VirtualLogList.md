# VirtualLogList 技术文档

> 源文件：`app/src/components/VirtualLogList.vue`
> 使用方：`SystemLogs.vue`、`LogViewer.vue`、`FRPManager.vue`、`SyncthingManager.vue`

---

## 一、背景与设计目标

日志页面通过 SSE（EventSource）实时接收后端推送的日志行。在高频场景下（如服务器启动时 500 条日志在毫秒内连续到达），若每条日志都触发一次 Vue re-render + DOM 滚动，浏览器会冻结。

VirtualLogList 解决三个核心问题：

| 问题 | 解决方案 |
|------|---------|
| 大量日志渲染卡顿 | 虚拟滚动，任意时刻 DOM 只保留视口附近约 N 条 |
| 500 条日志 500 次 re-render | RAF 批量缓冲：同一帧内所有 `push` 合并为一次 `items.push(...batch)` |
| 滚动到底部动画空白 | 双模式（anchored/free）切换，平滑滚动前自动切换渲染模式 |

---

## 二、架构概览

```
┌──────────────────────────────────────────────────┐
│                  VirtualLogList                   │
│                                                   │
│  ┌─────────────┐    ┌────────────────────────┐   │
│  │  RAF 批量   │    │     虚拟滚动引擎        │   │
│  │  缓冲层     │───▶│  prefixSums / lowerBound│   │
│  │  push()     │    │  visibleRange computed  │   │
│  └─────────────┘    └─────────┬──────────────┘   │
│                               │                   │
│  ┌────────────────────────────▼──────────────┐   │
│  │             DOM 结构                       │   │
│  │  [topSpacer] [visible items] [bottomSpacer]│   │
│  └───────────────────────────────────────────┘   │
│                                                   │
│  ┌──────────────────┐   ┌──────────────────────┐ │
│  │  ResizeObserver  │   │  平滑滚动状态机       │ │
│  │  实测各行高度    │   │  _isPinning           │ │
│  │  heightMap       │   │  _smoothScrolling     │ │
│  └──────────────────┘   │  _startSmoothScroll() │ │
│                          └──────────────────────┘ │
└──────────────────────────────────────────────────┘
```

---

## 三、双模式（anchored / free）

这是整个组件最核心的设计。

### anchored 模式（默认）

- **触发时机**：组件初始化、用户滚动到底部附近（距底 ≤ 50px）、`scrollToBottom()` 调用完成后
- **渲染窗口**：固定渲染最后 `capacity` 条（capacity ≈ 视口高度 / 估算行高 × 2 + buffer），不依赖 scrollTop
- **DOM 结构**：`topSpacer（大）+ 末尾几十条日志 + bottomSpacer（0）`
- **优点**：新日志追加时，已测量高度的条目全部在渲染窗口内，`scrollHeight` 稳定，追底精确

### free 模式

- **触发时机**：用户向上滚动（距底 > 50px）、平滑滚动过程中（远距离滚动时临时切换）
- **渲染窗口**：根据 `scrollTop.value` 用二分查找定位，渲染 `[scrollTop - buffer, scrollTop + viewportHeight + buffer]` 范围内的条目
- **DOM 结构**：`topSpacer（动态）+ 当前视口附近日志 + bottomSpacer（动态）`
- **优点**：用户自由滚动时，任意位置都能看到正确的日志内容

### 模式切换条件

```
用户向上滚动（dist > 50px）         → anchored → free
用户滚到底部（dist ≤ 50px）         → free → anchored
scrollToBottom() 完成               → * → anchored
平滑滚动起点距底 > 1屏（临时）      → anchored → free（动画结束后切回）
```

---

## 四、Props

| Prop | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `estimatedItemHeight` | `Number` | `28` | 每条日志的估算高度（px），用于计算虚拟窗口大小和前缀和初始值 |
| `buffer` | `Number` | `300` | 视口上下额外渲染的缓冲区高度（px），避免快速滚动时出现空白 |
| `autoScroll` | `Boolean` | `true` | 批量 flush 后是否自动平滑滚动到底部 |

---

## 五、公开 API（defineExpose）

通过 `ref` 获取组件实例后可调用：

```javascript
const vllRef = ref(null)
// <VirtualLogList ref="vllRef" />
```

### `push(item)`

将一条日志推入内部 RAF 批量缓冲队列。不会立即更新 DOM，当前帧结束前统一 flush。

```javascript
vllRef.value?.push({ time: '...', level: 'INFO', msg: '...' })
```

### `clear()`

清空所有日志（含 pending 队列），取消未执行的 RAF，将模式重置为 anchored。

```javascript
vllRef.value?.clear()
```

### `scrollToBottom(smooth = true)`

滚动到列表底部。默认平滑动画；传 `false` 为即时跳转。

```javascript
vllRef.value?.scrollToBottom()        // 平滑
vllRef.value?.scrollToBottom(false)   // 即时
```

### `scrollToTop()`

即时滚动到列表顶部，切换为 free 模式。

### `scrollToIndex(index)`

即时滚动到指定下标的日志条目（基于前缀和计算偏移），切换为 free 模式。

### `itemCount`（computed ref）

当前日志条目总数，可直接在模板中绑定：

```html
<span>{{ vllRef?.itemCount ?? 0 }}</span>
<t-button :disabled="(vllRef?.itemCount ?? 0) === 0">清空</t-button>
```

### `getItems()`

返回内部 `items` 数组引用，供父组件（如 LogViewer 的 `defineExpose`）透传使用。

---

## 六、Slots

### `#item`

渲染每一条日志，作用域变量为 `{ item, index }`：

```html
<template #item="{ item, index }">
  <div class="log-line">
    <span>{{ index + 1 }}</span>
    <span>{{ item.time }}</span>
    <span>{{ item.msg }}</span>
  </div>
</template>
```

### `#empty`

当 `items.length === 0` 时显示，通常展示引导文案：

```html
<template #empty>
  <div class="log-empty">暂无日志，点击"开始监听"。</div>
</template>
```

---

## 七、使用示例

```html
<VirtualLogList
    ref="vllRef"
    class="log-vll"
    :estimated-item-height="28"
    :buffer="400"
    :auto-scroll="autoScroll"
>
  <template #item="{ item, index }">
    <div class="log-line">
      <span class="log-number">{{ index + 1 }}</span>
      <span class="log-time">{{ item.time }}</span>
      <span class="log-level" :class="`level-${item.level}`">{{ item.level }}</span>
      <span class="log-text">{{ item.msg }}</span>
    </div>
  </template>
  <template #empty>
    <div class="log-empty">暂无日志</div>
  </template>
</VirtualLogList>
```

```javascript
// SSE 回调直接 push，无需管理 RAF
stopFn = streamSystemLogs(
  (logStr) => vllRef.value?.push(parseLogLine(logStr)),
  (err) => { isStreaming.value = false }
)

// 清空
const clearLogs = () => vllRef.value?.clear()

// 页面激活时滚到底部
onActivated(() => nextTick(() => vllRef.value?.scrollToBottom()))
```

---

## 八、内部实现详解

### 8.1 前缀和（prefixSums）

```
prefixSums[i] = items[0..i-1] 的高度总和
topSpacerHeight = prefixSums[visibleRange.start]
bottomSpacerHeight = prefixSums[totalLen] - prefixSums[visibleRange.end]
totalScrollHeight ≈ prefixSums[totalLen]
```

- 未测量的条目使用 `estimatedItemHeight` 估算
- 由 `heightVersion` 响应式变量追踪高度缓存变更，任何测量更新都会触发前缀和重算

### 8.2 二分查找（lowerBound）

free 模式下，根据 `scrollTop` 定位可见范围：

```javascript
// 找第一个前缀和 ≥ target 的下标 → O(log n)
start = lowerBound(prefixSums, scrollTop - buffer) - 1
end   = lowerBound(prefixSums, scrollTop + viewportHeight + buffer) + 1
```

### 8.3 ResizeObserver

每次 `renderedItems` 变化后（flush: 'post'），通过 RAF 重建 ResizeObserver 观察集合。当观察到某条目实际高度与缓存不同时，更新 `heightMap`，递增 `heightVersion`（触发前缀和重算），并在 anchored 模式下调用 `pinToBottom()` 重新对齐。

### 8.4 RAF 批量缓冲

```
push(item) → _pending.push(item)
           → if _batchRaf === null: _batchRaf = requestAnimationFrame(_flush)

_flush()   → items.value.push(..._pending)   // 一次 Vue re-render
           → _pending = []
           → _batchRaf = null
           → if autoScroll: scrollToBottom(true)
```

同一帧内的所有 `push` 调用合并为一次响应式更新，无论 SSE 同帧推送多少条日志。

### 8.5 滚动状态标志

| 标志 | 类型 | 用途 |
|------|------|------|
| `_isPinning` | boolean | 即时滚动（`el.scrollTop = x`）的一次性标志。`onScroll` 消费后清除，防止程序触发的 scroll 事件被误判为用户滚动 |
| `_smoothScrolling` | boolean | 平滑滚动进行中。`onScroll` 期间只追踪 `scrollTop.value`，不切换 mode |
| `_smoothScrollAbortFn` | function \| null | 当前平滑滚动的清理函数，新的平滑滚动启动时自动调用取消上一次 |

### 8.6 平滑滚动（_startSmoothScroll）

```
_startSmoothScroll(el, target)
  ├─ 取消上一次平滑滚动（若有）
  ├─ _smoothScrolling = true
  ├─ el.scrollTo({ top: target, behavior: 'smooth' })
  ├─ 监听 scrollend 事件（cleanup 入口）
  └─ 600ms 超时兜底（scrollend 未触发时强制 cleanup）

cleanup(shouldAnchor)
  ├─ _smoothScrolling = false
  ├─ 清理 scrollend 监听 + 超时定时器
  └─ if shouldAnchor: mode = 'anchored' → nextTick(pinToBottom)
```

`scrollend` 触发时检查 `dist ≤ 50px`：若用户在动画途中打断（dist > 50px），则 `shouldAnchor = false`，不强制切回 anchored，交由后续 `onScroll` 自然处理。

### 8.7 平滑滚动的空白问题及修复

**问题根因**：anchored 模式下 DOM 为 `[大片空白 topSpacer][末尾日志]`。从 scrollTop=0 启动平滑动画时，前半段全在空白区内滑动，快到底部才出现日志。

**修复逻辑（在 `scrollToBottom` 内）**：

```
距底部 > 1个视口高度 且当前为 anchored 模式？
  ├─ YES → mode = 'free', scrollTop.value = el.scrollTop
  │        （Vue 重算 visibleRange：当前位置的日志渲染到 DOM）
  │        nextTick → 取新 scrollHeight → _startSmoothScroll
  └─ NO  → 直接 _startSmoothScroll（近距离或已是 free）
```

切为 free 后，`onScroll`（`_smoothScrolling` 分支）持续更新 `scrollTop.value`，`visibleRange` 跟随动画位置渲染对应日志，全程无空白。

---

## 九、`_isPinning` 与 `scrollToBottom` 的设计取舍

**问题**：`scrollToBottom()` 调用 `el.scrollTop = target` 会同步触发 `onScroll`，而 `onScroll` 会根据 `dist` 决定是否切换 mode。如果 `dist > 50` 时被判定为用户向上滚动，mode 就错误地切为 free。

**设计**：

- `_isPinning = true` 在 `el.scrollTop = target` **之前**同步设置（JS 是单线程，scroll 事件异步派发，`_isPinning` 必然先于事件执行）
- `onScroll` 的第一个 scroll 事件消费 `_isPinning`（设为 false）并 `return`，跳过 mode 判断

**为什么 `_isPinning` 不能用于平滑滚动**：`behavior: 'smooth'` 会触发**多次** scroll 事件，`_isPinning` 只能消费一次。因此平滑滚动改用 `_smoothScrolling` 持续标志，直到 `scrollend` 才清除。

---

## 十、`scrollToIndex` / `scrollToTop` 的注意事项

两个 API 同样使用 `_isPinning`，且在 `el.scrollTop = target` 前设置：

- `scrollToIndex()` 额外保护：若目标位置接近底部（dist ≤ 50px），若无 `_isPinning`，`onScroll` 会将 mode 切回 anchored，导致下次新日志到达时意外追底。`_isPinning` 确保这次滚动的 mode（free）不被打断。

---

## 十一、Watcher 与生命周期

| 监听目标 | 触发条件 | 操作 |
|---------|---------|------|
| `items.value.length` | 条目增加 且 anchored 模式 | `nextTick(pinToBottom)` |
| `vpWidth` | 视口宽度变化 | 清空 heightMap、递增 heightVersion、anchored 时 pinToBottom |
| `renderedItems` | 渲染条目变化（flush: post） | RAF 重建 ResizeObserver 观察集合 |

**onMounted**：创建 ResizeObserver，`nextTick(pinToBottom)` 初始对齐

**onBeforeUnmount**：
- `ro.disconnect()` — 停止高度观察
- `cancelAnimationFrame(roRaf)` — 取消待执行的 observer 重建
- `cancelAnimationFrame(_batchRaf)` — 取消未执行的日志 flush
- `_smoothScrollAbortFn()` — 取消进行中的平滑滚动

---

## 十二、CSS 约定

组件自身只定义三个 class：

| Class | 说明 |
|-------|------|
| `.vll-viewport` | 根元素，`height: 100%; overflow-y: auto; overflow-anchor: none` |
| `.vll-spacer` | 上下占位块，`pointer-events: none` |
| `.vll-item` | 每条日志的包装 div，`data-index` 属性供 ResizeObserver 读取 |

调用方通过 `:deep(.vll-viewport::-webkit-scrollbar)` 自定义滚动条样式，通过给 VirtualLogList 传 `class` 自定义字体/颜色。

---

## 十三、已知限制与扩展建议

### 当前限制

1. **estimatedItemHeight 偏差**：若实际行高与估算值差异大（如部分日志多行展示），平滑滚动目标 `target` 可能轻微偏差，由 `scrollend` 后的 `pinToBottom()` 兜底修正
2. **scrollend 浏览器支持**：Chrome 114+、Firefox 109+。若需兼容更旧浏览器，已有 600ms 超时兜底
3. **不支持横向虚拟化**：长行日志使用 `word-break: break-word` 折行处理

### 扩展方向

- **搜索 / 高亮**：在 `#item` slot 中渲染搜索结果高亮；跳转到指定结果用 `scrollToIndex()`
- **日志过滤**：在外部过滤后传入已过滤数据，或在 `push` 前过滤（当前 FRPManager/SyncthingManager 的做法）
- **固定行高模式**：若日志均为单行，可跳过 ResizeObserver，直接 `itemHeight * index` 计算偏移，性能更优
- **历史日志分页加载**：在 `scrollToTop` 或 `visibleRange.start === 0` 时触发向上翻页，向 `items` 头部插入旧日志（需重新计算 heightMap 下标）
