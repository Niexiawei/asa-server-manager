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
           // 追底由 items.length watcher 的 pinToBottom 负责（见 §十四），_flush 不再触发滚动
```

同一帧内的所有 `push` 调用合并为一次响应式更新，无论 SSE 同帧推送多少条日志。

> 追底路径：`anchored` 模式下 `items.length` 增加时 `watch` 触发 `nextTick(pinToBottom)`
> （即时跳转，`props.autoScroll` 为真才执行）。平滑滚动只留给显式 `scrollToBottom()`。
> 历史原因与洪流下的抖动修复见 §十四。

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

---

## 十四、底部抖动修复方案（anchored 模式 + 高频日志）

### 14.1 现象

在 `FRPManager.vue` 里，向上滚到「倒数第二行」时视口会被自动拽回最后一行，需要连续滚动
好几次才能真正停在末尾；持续滚动时画面在最后一行与倒数第二行之间来回跳。
`SystemLogs.vue` 用的是同一个组件、同一套 props，却没有这个问题 —— 差异**不在组件**，
在**日志到达频率**（FRP 的 `[frpc]` 行是洪流，系统日志是零星）与调用方 CSS。

### 14.2 根因

| # | 根因 | 位置 | 说明 |
|---|------|------|------|
| 1（主因） | `anchored` 模式下每批 `push` 都强制回底，而「离开 anchored」只有 `dist > BOTTOM_THRESHOLD(50)` 一个出口 | `_flush()`（`scrollToBottom(true)`）、`items.length` watcher（`pinToBottom`）、ResizeObserver（`pinToBottom`）；出口在 `onScroll` | 往上滚一行 ≈ 28~30px，`dist` 落在 `0<dist≤50`，仍是 `anchored`，下一条日志 `pinToBottom` 立刻拍回底部。必须一次滚过 50px 才能切 `free` |
| 2（放大） | FRP 的 `.log-line { margin-bottom: 2px }`，而 `.vll-item` 无 padding/border | `FRPManager.vue` `.log-line`；`VirtualLogList.vue` `.vll-item` | 子元素外边距**合并/外溢**到 `.vll-item` 之外，不计入 ResizeObserver 读的 `offsetHeight`。每行实测高度少 ~2px，`prefixSums` 虚拟高度与真实 `scrollHeight` 逐行累积偏差，`dist` 在 50px 阈值附近抖动 |
| 3（放大） | 洪流下每批 `_flush` 都启动一次平滑滚动，`_startSmoothScroll` 开头 `_smoothScrollAbortFn()` 取消上一次 | `_flush` → `scrollToBottom(true)` → `_startSmoothScroll` | 日志比一次 smooth 动画（数百 ms）来得快 → 动画反复中止重启，`scrollend` 从不干净触发，`_smoothScrolling` 长期 `true`，`pinToBottom` 里 `if (_smoothScrolling) return` 被跳过 → 视口一直追一个不断后移的底部 |

`SystemLogs.vue` 免疫的原因：日志稀疏，两次 `push` 之间平滑滚动早已结束，根因 1/3 的「洪流」前提不成立；且它的 `.log-line` 用 `min-height: 28px; box-sizing: border-box` 而非 `margin`，无根因 2。

### 14.3 修复方案

四项，A/B/C 改组件，D 是调用方对齐（C 落地后非必需）。改动都限定在既有的双模式/RAF/测量框架内，不引入新状态机。

#### 方案 A —— 用「用户滚动意图」离开 anchored，不再依赖 50px 阈值

`onScroll` 里新增「非程序触发的向上移动」判据：`anchored` 模式下，只要 `scrollTop` 比上一次
变小且不是 `_isPinning`/`_smoothScrolling` 造成的，立即切 `free`；不再等 `dist` 越过阈值。
`BOTTOM_THRESHOLD` 保留，仅用于**反方向**——`free` 模式滚回底部附近时重新吸附（这个方向留
大阈值手感才好，是刻意的非对称）。

```js
// 新增模块级变量：上一次 onScroll 观察到的 scrollTop
let _lastScrollTop = 0

function onScroll() {
  const el = viewportRef.value
  if (!el) return
  const st = el.scrollTop

  if (_isPinning) {
    _isPinning = false
    _lastScrollTop = st          // 程序跳转也要刷新基线，否则下一次被误判为 movedUp
    return
  }

  const movedUp = st < _lastScrollTop - 0.5
  _lastScrollTop = st
  const dist = el.scrollHeight - st - el.clientHeight

  if (_smoothScrolling) {
    scrollTop.value = st
    if (dist <= BOTTOM_THRESHOLD) mode.value = 'anchored'
    return
  }

  if (mode.value === 'anchored') {
    // movedUp 且确实离开了底部带（dist > 4）→ 切 free。一次一行(~28px)的上滚也能停住。
    // dist > 4 护栏：内容收缩时浏览器把 scrollTop 向下夹取，movedUp 为真但 dist≈0，非用户离开。
    if (movedUp && dist > 4) {
      mode.value = 'free'
      scrollTop.value = st
    }
    return
  }

  // free
  scrollTop.value = st
  if (dist <= BOTTOM_THRESHOLD) mode.value = 'anchored'
}
```

配套：`clear()` 里补 `_lastScrollTop = 0`（内容清空后 scrollTop 归零，重置基线避免下一帧
被误判为 movedUp）。

切 `free` 后，`pinToBottom`（`if (mode.value !== 'anchored') return`）与方案 B 后的 `_flush`
都会早退，单次上滚即可稳定脱离洪流。

> 可选增强：再挂一个 `@wheel` 监听，`e.deltaY < 0` 直接切 `free`。`wheel` 只由用户产生、
> 程序 `scrollTo` 不触发，是比「比较 scrollTop」更早、更干净的意图信号；键盘 `PageUp`/
> `ArrowUp`/`Home` 同理。非必需，方案 A 主体已覆盖鼠标滚轮场景。

#### 方案 B —— anchored 追加一律即时追底，平滑滚动只留给显式 API

去掉 `_flush` 里的 `scrollToBottom(true)`，追加追底完全交给 `items.length` watcher 的
`pinToBottom`（即时、廉价、不会被 abort）。平滑动画只保留给**显式** `scrollToBottom()`
调用（清空后回底、`onActivated`、按钮）。顺带给 watcher 补上 `props.autoScroll` 判据，
让 `:auto-scroll="false"` 时真的不自动追底（当前 watcher 无视这个 prop，是既有的不一致）。

```js
const _flush = () => {
  if (!_pending.length) return
  items.value.push(..._pending)
  _pending = []
  _batchRaf = null
  // 删除原来的 if (props.autoScroll) scrollToBottom(true)
  // 追底由下面的 length watcher 统一负责
}

watch(() => items.value.length, (n, o) => {
  if (n > o && mode.value === 'anchored' && props.autoScroll) nextTick(pinToBottom)
}, {flush: 'post'})
```

这样根因 3 的「平滑动画互相 abort」在洪流下彻底不出现；洪流停止后用户仍能用按钮平滑回底。

#### 方案 C —— `.vll-item` 建立 BFC，行高测量计入 slot 外边距

给 `.vll-item` 加 `display: flow-root`（或 `overflow: hidden`）。建立块级格式化上下文后，
slot 内容的 `margin` 不再合并/外溢到 `.vll-item` 之外，`offsetHeight` 如实包含它，
ResizeObserver 实测高度与真实 `scrollHeight` 重新一致。视觉行距不变（2px 仍在，只是从
「外溢的合并 margin」变成「`.vll-item` 内部空间」），但对任何用 `margin` 做行距的调用方
都免疫，不必逐个改调用方 CSS。

```css
.vll-item {
  box-sizing: border-box;
  display: flow-root;   /* 含入子元素外边距，使实测高度 == 真实占位 */
}
```

对 `SystemLogs.vue`（`.log-line` 无 margin）是无副作用的 no-op。

#### 方案 D（调用方，可选）—— FRPManager 行样式与 SystemLogs 对齐

`FRPManager.vue` 的 `.log-line` 删掉 `margin-bottom: 2px`，改与 `SystemLogs.vue` 一致
（`min-height: 28px; box-sizing: border-box; align-items: flex-start;`）；若要保留行距用
`padding-bottom` 代替。方案 C 落地后此项非硬性要求，但对齐两处样式能减少后续困惑，
也是 `LogViewer.vue` / `SyncthingManager.vue` 的推荐写法：**行距用 padding，不用 margin**。

### 14.4 回归检查清单

- `SystemLogs.vue`：停底自动追新；上滚停住；滚回底部重新吸附（稀疏日志，不应有行为变化）
- `FRPManager.vue`：洪流日志下，上滚一格即停住、不回弹；滚回底部恢复自动追底
- `LogViewer.vue` / `SyncthingManager.vue`：同上两条
- 显式 `scrollToBottom()`（清空后、`onActivated`、按钮）仍平滑，远距离不滑过空白
- `scrollToTop()` / `scrollToIndex()` 跳转后不被下一批日志拽回（`_isPinning` + `mode='free'`）
- 视口宽度变化触发换行、heightMap 清空后，anchored 仍精确贴底
- `estimatedItemHeight` 与实测差异大的多行日志，贴底不累积偏移
- `:auto-scroll="false"` 时新日志到达不跳动（方案 B 的 watcher 判据）

### 14.5 不采纳的方案

- **只调 `BOTTOM_THRESHOLD`**：调小会让「滚回底部自动吸附」手感变差；根因是「离开」不该用
  距离阈值判定，而非阈值大小
- **给 `pinToBottom` 加时间节流**：治标，洪流未停前仍周期性回弹
- **改用浏览器原生 `overflow-anchor`**：组件明确禁用了它（`.vll-viewport { overflow-anchor: none }`），
  且浏览器锚定不认识虚拟 spacer，会与 `prefixSums` 打架

### 14.6 落地状态

方案 A / B / C 已实施（`VirtualLogList.vue`）：

- `onScroll` 重写为「意图判据」+ 新增 `_lastScrollTop`；`clear()` 补 `_lastScrollTop = 0`
- `_flush` 去掉 `scrollToBottom(true)`；`items.length` watcher 加 `props.autoScroll` 判据
- `.vll-item` 加 `display: flow-root`

方案 D（`FRPManager.vue` 行样式对齐）与「可选 `@wheel` 增强」未做——C 落地后非必需，留作后续清理。
`npm run build` 通过。
