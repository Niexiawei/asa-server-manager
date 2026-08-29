# 修复计划：资源监控 SSE 的 `//` 拼接 与 `/api/auth/state` 反复调用

> **状态**：**已实施**（方案 A + 收紧点 1、2；纯前端改动，不动后端）。
> 单元测试（§1.4 附带项）暂缓——仓库前端无测试框架（无 vitest/jest），
> 引入需单独决策，当前以 `npm run build` + 人工验证为准。
> **前置**：生产已用 `--tls=false` 解决了「证书不受信任导致 WS/SSE 连不上」
> （见 [WS_SSE_PROD_CONNECTION_TROUBLESHOOTING.md](WS_SSE_PROD_CONNECTION_TROUBLESHOOTING.md)）。
> 本文处理其后暴露的两个残留问题。

---

## 0. 两个问题一句话

| # | 现象 | 真因 | 关系 |
|---|------|------|------|
| 一 | `new EventSource("http://127.0.0.1:19193//api/server/info")` 多一个 `/`，404 连不上 | `serverResourceWorker.js` / `sharedResourceWorker.js` 用 `${base}/api/...` 朴素拼接，而 `base` 在生产带尾斜杠 | —— |
| 二 | `/api/auth/state` 像被「轮询」 | **不是定时轮询**。SSE 每次 `CLOSED` → worker 发 `SSE_CHECK_AUTH` → 主线程 `recheck(true)` → `GET /api/auth/state`。问题一让 SSE 一直断，于是配合指数退避看起来像持续轮询 | **问题二 90% 是问题一的下游症状**；修好问题一后剩下的是设计层面的小优化 |

---

## 1. 问题一：SSE URL 出现 `//`

### 1.1 复现路径

`ServerResourceMonitor.vue:99` / `ResourceMonitor.vue:124`：

```js
let url = getSSEBaseUrl()
worker.postMessage({ type: 'INIT', payload: { apiBaseUrl: url } })
```

`utils/utils.js`：

```js
export function getSSEBaseUrl() {
    return urlJoin(window.location.origin, basePrefix())
}
// 生产：basePrefix() === window.location.pathname === '/'
```

`urlJoin('http://127.0.0.1:19193', '/')` 逐步化简：

| 步骤 | 结果 |
|------|------|
| `.join('/')` | `http://127.0.0.1:19193//` |
| `.replace(/\/+/g,'/')` | `http:/127.0.0.1:19193/` |
| `.replace(/^(.+):\//,'$1://')` | `http://127.0.0.1:19193/` ← **带尾斜杠** |

然后 worker 里（`serverResourceWorker.js:78` / `sharedResourceWorker.js:134`）：

```js
new EventSource(`${API_BASE_URL}/api/server/info`)
// = "http://127.0.0.1:19193/" + "/api/server/info"
// = "http://127.0.0.1:19193//api/server/info"   ← 多一个斜杠
```

Gin/httprouter 不会把 `//api/...` 规整成 `/api/...`（`RedirectFixedPath` 默认关），直接 404 →
`EventSource` `onerror` → `readyState === CLOSED` → 触发重连 + `SSE_CHECK_AUTH`（连到问题二）。

### 1.2 为什么 dev 不复现

dev 下 `basePrefix()` 返回 `VITE_API_ROOT` = `/api`：

- `getSSEBaseUrl()` = `urlJoin('http://localhost:3000', '/api')` = `http://localhost:3000/api`（**无**尾斜杠）
- `${base}/api/server/info` = `http://localhost:3000/api/api/server/info`
- vite proxy 命中 `/api` 前缀，`rewrite: ^/api → ''` 把多出来的那截 `/api` 吃掉 → `/api/server/info` 转给后端

两个 bug（尾斜杠 + 多一层 `/api`）在 dev 恰好互相抵消，所以只在生产同源部署暴露。

### 1.3 对照：`sseApi.js` 没这问题

`sseApi.js` 里所有 SSE 都走 `buildEventSourceUrl('/api/xxx')`：

```js
export function buildEventSourceUrl(url) {
    return urlJoin(window.location.origin, basePrefix(), url)
}
// urlJoin('http://127.0.0.1:19193', '/', '/api/server/info')
//   → .join('/')             "http://127.0.0.1:19193///api/server/info"
//   → .replace(/\/+/g,'/')   "http:/127.0.0.1:19193/api/server/info"
//   → 还原 scheme            "http://127.0.0.1:19193/api/server/info"   ✅
```

`urlJoin` 的 `/\/+/g → '/'` 能把**三个**斜杠压成一个，问题只出在「先算出带尾斜杠的 base，
再在 worker 里用模板字符串二次拼接」这条绕过了 `urlJoin` 的路径。

### 1.4 修复方案

#### 方案 A（推荐）：URL 在组件里一次性构造好，worker 只管连

让 worker 不再拥有「拼 API 路径」的知识，与 `sseApi.js` 的既有写法对齐。

| 文件 | 改动 |
|------|------|
| `components/ServerResourceMonitor.vue` | `getSSEBaseUrl()` → `buildEventSourceUrl('/api/server/info')`；INIT payload 改传 `{ sseUrl }` |
| `components/ResourceMonitor.vue` | `getSSEBaseUrl()` → `buildEventSourceUrl('/api/server/all-info')`；同上 |
| `workers/serverResourceWorker.js` | `API_BASE_URL` → `SSE_URL`；`new EventSource(SSE_URL)`，删掉 `${...}/api/server/info` 拼接 |
| `workers/sharedResourceWorker.js` | 同上；注意 `if (!API_BASE_URL)` 首次赋值的逻辑（`:48`）改成 `if (!SSE_URL)`，语义不变（第一个连接的 tab 定 URL） |
| `utils/utils.js` | 删除 `getSSEBaseUrl()`（改造后无引用；已确认全仓仅这两个组件用它） |

改动量小、无行为歧义，且把「一个 worker 只连一个固定端点」这件事显式化。

#### 方案 B（最小 diff，治标）

仅在两个 worker 里去掉重复斜杠：

```js
new EventSource(`${API_BASE_URL.replace(/\/+$/, '')}/api/server/info`)
```

不推荐：URL 构造知识仍散落在 worker 里，下次加端点还会踩；且没解决 `getSSEBaseUrl`
返回值「有时带尾斜杠有时不带」这个本质的不一致。

#### 附带：补 `utils.js` 单元测试

覆盖 `buildEventSourceUrl` / `buildWebSocketUrl` 在以下输入下**不产生 `//`**（scheme 后的除外）：

- `pathname === '/'`（生产根路径）
- `pathname === '/asa/'`（生产子路径）
- dev（`VITE_API_ROOT === '/api'`）
- `url` 传 `'/api/x'` 与传 `'api/x'` 两种写法

---

## 2. 问题二：`/api/auth/state` 反复调用

### 2.1 它到底在哪被调

全仓搜下来，`getAuthState()`（`GET /api/auth/state`）的调用点：

| 位置 | 时机 | 频率 |
|------|------|------|
| `main.js:40` `recheck()` | 应用启动 | 1 次 |
| `authStore.js` `doLogin/doLoginTOTP/doLogout` + `Login/Setup/Profile.vue` | 登录/登出/建号/改资料 | 用户操作触发 |
| `utils/http.js:34` 401 拦截器 | 任一 API 返回 401 | 会话失效时 |
| **`utils/sseAuthGate.js:31` `handleSSECheckAuth`** | **收到 worker 的 `SSE_CHECK_AUTH`** | **每次 SSE `CLOSED`** |

**没有任何 `setInterval` 在轮询它。** 用户观察到的「轮询」就是最后一行：
问题一让资源监控 SSE 持续 404 → `serverResourceWorker.js:129` / `sharedResourceWorker.js:183`
每次失败都 `postMessage({type:'SSE_CHECK_AUTH'})` → `sseAuthGate` `recheck(true)` → 打一次 `/api/auth/state`。
指数退避（1s→2s→4s…封顶 30s）让它表现为稳定节奏的请求流。

### 2.2 除了「修问题一」，还该收紧的设计点

即便 SSE 恢复正常，以下情况仍会打无谓的 `/api/auth/state`：服务端重启、网络抖动、
任何非鉴权原因的 SSE 中断。逐条处理：

1. **鉴权关闭时直接短路**（对当前 `--tls=false` 无鉴权部署收益最大）
   `sseAuthGate.handleSSECheckAuth` 开头加：

   ```js
   if (!authState.authEnabled) {
       post(target, { type: 'AUTH_RESUMED' })
       return
   }
   ```

   `authState` 在 `main.js` 启动时已 `recheck()` 过一次，`authEnabled` 可信；
   鉴权没开，SSE 断了绝不可能是「会话失效」，没有问服务端的必要。

2. **`recheck(true)` 改 `recheck()`**（复用 single-flight）
   `authStore.js:48` 是 `if (!force && inflight) return inflight`——`force=true` 会**绕过**合并，
   多个 worker 同时报 CLOSED 时可能并发多发。`sseAuthGate` 里这次检查不需要「强制最新」，
   去掉 `true` 即可让并发请求自然合并成一个。（`sseAuthGate` 自己的 `checking` 1s 窗口保留。）

3. **worker 侧只在「快速失败」时才查 auth**（可选，进一步降噪）
   `EventSource` 连上后**立刻** `CLOSED` ≈ 被拒（401/403/404）；连了一段时间才断 ≈ 服务端重启。
   在 worker 里记 `onopen` 时间戳，`onerror`+`CLOSED` 时若「距上次 open/建连 < ~2s」才发
   `SSE_CHECK_AUTH`，否则走纯重连不惊动鉴权。

4. **不需要后端改动**。`/api/auth/state` 是廉价只读接口；把调用次数降到「启动 1 次 + 事件驱动」
   就够了，不必加缓存或限流。

### 2.3 期望结果

| 场景（鉴权关闭） | 修复前 | 修复后 |
|------------------|--------|--------|
| 打开首页放 5 分钟 | `/api/auth/state` 每 ≤30s 一次 | 启动 1 次，此后 0 次 |
| 后端重启一次 | 重启期间每次重连各打一次 | 0 次（短路 1）+ SSE 自动重连成功 |

| 场景（鉴权开启，会话有效） | 修复后 |
|----------------------------|--------|
| 后端重启一次 | ≤1 次（single-flight + 快速/慢速失败判定） |
| 会话真失效 | 1 次，拿到结论后 worker 进入 `AUTH_BLOCKED`，停止重连（现有逻辑不变） |

---

## 3. 实施记录

| 步骤 | 状态 | 落地改动 |
|------|------|---------|
| 问题一 / 方案 A | ✅ 已实施 | `utils/utils.js` 删除 `getSSEBaseUrl()`；`ServerResourceMonitor.vue` / `ResourceMonitor.vue` 改用 `buildEventSourceUrl('/api/server/info' \| '/api/server/all-info')`，INIT payload 由 `{apiBaseUrl}` 改为 `{sseUrl}`；`serverResourceWorker.js` / `sharedResourceWorker.js` 把 `API_BASE_URL` 换成 `SSE_URL`，`new EventSource(SSE_URL)` 不再拼路径 |
| 问题二 / 收紧点 1 | ✅ 已实施 | `sseAuthGate.handleSSECheckAuth` 开头：`if (!authState.authEnabled) { post(target, {type:'AUTH_RESUMED'}); return }` |
| 问题二 / 收紧点 2 | ✅ 已实施 | 同函数内 `recheck(true)` → `recheck()`，复用 single-flight |
| 问题二 / 收紧点 3（worker 侧快速失败判定） | ⏸ 未做 | 可选降噪，留作独立后续 commit |
| §1.4 附带单元测试 | ⏸ 未做 | 仓库无前端测试框架，需单独引入 vitest |

`npm run build` 通过。人工验证见 §4。

---

## 4. 验证清单

- [ ] `npm run build` 通过；新增 `utils.js` 测试通过。
- [ ] **生产（`--tls=false`，同源）**：DevTools Network 里 `/api/server/info`、`/api/server/all-info`
      的请求 URL **无 `//`**，状态 200，数据持续推送；实例卡片 CPU/内存实时刷新。
- [ ] 生产空闲 5 分钟：`/api/auth/state` 请求数 = 启动那 1 次（鉴权关时之后恒为 0）。
- [ ] `kill` 后端再拉起：资源监控 SSE 自动重连成功；期间 `/api/auth/state` 鉴权关 0 次 / 鉴权开 ≤1 次。
- [ ] **子路径部署**（nginx `location /asa/`）：SSE URL 为 `.../asa/api/server/info`，无 `//`，可连。
- [ ] **dev（vite proxy）回归**：资源监控、事件 WS、日志 SSE 全部正常。
- [ ] 鉴权开启 + 手动使会话失效：worker 收到 `AUTH_BLOCKED`，停止重连，登录后自动恢复（`onLoginSuccess` 路径不变）。

---

## 5. 影响面与风险

- **纯前端**，不触碰 Go 代码与 API 契约。
- `getSSEBaseUrl` 删除前已确认全仓仅 `ResourceMonitor.vue` / `ServerResourceMonitor.vue` 引用。
- `sharedResourceWorker` 是 SharedWorker：INIT 仍是「第一个连接的 tab 定 URL」，
  传完整 URL 不改变这个语义（同源下各 tab 算出的 URL 本就一致）。
- 收紧点 2（去掉 `force`）：理论上若 `authState` 因更早的失败被置为 `authenticated=false`
  但服务端其实正常，`recheck()` 复用的 inflight 会拿到正确结果刷新回来，无回归。
- 收紧点 3 是纯增量降噪，判据保守（默认 2s 阈值），最坏情况退化为「跟修复前一样每次都查」。

---

## 6. 参考

- `app/src/utils/utils.js` —— `urlJoin` / `buildEventSourceUrl` / `getSSEBaseUrl`
- `app/src/workers/serverResourceWorker.js`、`sharedResourceWorker.js` —— SSE 连接与重连
- `app/src/utils/sseAuthGate.js` —— `SSE_CHECK_AUTH` → `/api/auth/state` 的闸门
- `app/src/store/authStore.js` —— `recheck` 的 single-flight（`force` 会绕过）
- `app/src/apis/sseApi.js` —— 正确使用 `buildEventSourceUrl` 的对照实现
