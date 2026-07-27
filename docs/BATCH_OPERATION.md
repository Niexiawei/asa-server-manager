# 批量操作（batchmanage）技术文档

`batchmanage` 是「一次对多个实例做启动/停止/重启」的编排层。它位于依赖链的顶端，
向下调用 `countdown`（倒计时播报）、`instance`（实际启停）、`state`（状态机 CAS）、
`realtime`（WebSocket 广播），向上给 HTTP API、定时任务（`schedule`）和前端批量操作弹窗提供能力。

**包路径**：`asa-server/batchmanage`
**文件**：`manager.go`（编排与状态）、`api.go`（HTTP 路由与 SSE）、`manager_test.go`

---

## 1. 设计目标

一次批量操作要同时满足四件事，任何一件做不到都会在生产中出问题：

| 目标 | 实现手段 |
|------|----------|
| 全服玩家收到**对齐**的倒计时 | 阶段一统一前置倒计时，而不是逐实例各跑一轮 |
| 不把机器压垮 | 阶段二**串行**执行，实例之间可配延迟 |
| 状态不被并发操作写乱 | 每实例一次原子 CAS，失败即跳过 |
| 单台取消不牵连整批 | 每实例独立子 context + 独立跳过通道 |

---

## 2. 数据模型

### 2.1 操作类型与哨兵错误

```go
type BatchOperationType string
const (
    BatchStart   BatchOperationType = "start"
    BatchStop    BatchOperationType = "stop"
    BatchRestart BatchOperationType = "restart"
)

var (
    ErrOperationInProgress = errors.New("a batch operation is already running")
    ErrNoInstances         = errors.New("no instances to operate on")
)
```

两个错误做成哨兵是因为**调用方必须区分它们**：对定时更新任务而言，
「没有实例可操作」无害（本来就没什么可停的，直接进更新即可），
而「已有批量在跑」意味着一堆实例还活着，硬着头皮更新必被 installer 拒绝。
见 `schedule/scheduler.go` 的 `runUpdate`。

### 2.2 单实例结果状态机

```go
type InstanceOpStatus string
const (
    InstancePending       = "pending"          // 初始
    InstanceSkipRequested = "skip_requested"   // 用户已请求跳过，等主循环轮到
    InstanceRunning       = "running"
    InstanceSuccess       = "success"
    InstanceFailed        = "failed"
    InstanceSkipped       = "skipped"          // 预检剔除 / CAS 不允许 / 用户跳过
    InstanceCancelled     = "cancelled"        // 倒计时被用户取消
)
```

`skip_requested` 是个中间态：用户点「跳过」时主循环可能还没轮到该实例，
先把意图写进结果让 `/status` 立刻反映出来，等主循环走到时才转成 `skipped`。
计算进度时要把它排除在 `done` 之外（`api.go` 的 `getBatchStatus`），否则进度会虚高。

**`skipped` 与 `cancelled` 必须分清**：前者是「这台本来就无事可做」，
后者是「用户主动反悔」。混用会把排查引向错误的方向。

### 2.3 请求体

```go
type BatchOperationRequest struct {
    Instances    []string `json:"instances"`      // 空 = 全部实例
    DelaySeconds int      `json:"delay_seconds"`  // 实例**之间**的间隔，0-300

    Countdown     int      `json:"countdown"`      // 操作**开始前**的预告秒数，0 = 不倒计时
    NotifyPoints  []int    `json:"notify_points"`
    NotifyMessage string   `json:"notify_message"`
    NotifyCommand string   `json:"notify_command"`
}
```

`DelaySeconds` 与 `Countdown` 含义不重叠，可同时使用：
前者是阶段二实例之间的喘息间隔，后者是阶段一给玩家的预告。

---

## 3. 单例模型

```go
type BatchManager struct {
    current *BatchOperation   // 进行中的操作，nil 表示空闲
    last    *BatchOperation   // 最近一次已结束的操作，仅供日志回放
    mu      sync.Mutex

    logBroadcaster *LogBroadcaster  // 全程存活，不随操作创建/销毁
}
```

全局单例由 `Initialize()` 创建（`webapi/actions.go` 在装配路由时调用），
`GetGlobalManager()` 取用。**没有 nil 保护**——未初始化时返回 nil，
调用方需自行判空（`api.go` 各 handler 都判了；`schedule` 依赖启动顺序）。

「是否有操作在跑」是**推导**出来的，不是存下来的：`bm.current != nil && bm.current.Status == "running"`。

`last` 的存在意义：批量结束后前端仍要能回放这一轮的日志。
常驻开销是一个 op 对象 + 至多 `maxLogHistory`（500）条日志。

---

## 4. 执行流程

```
StartOperation（同步，调用方线程）
  ├─ 快路径判忙 → ErrOperationInProgress
  ├─ 校验倒计时配置 → 展开实例列表 → ErrNoInstances
  ├─ 逐实例预检 → 结果表 + countdownTargets     ← 不持锁（要跑 netstat）
  └─ 持锁：复查判忙 → 组装 op → 发布单例 → go runBatchOperation

runBatchOperation（后台 goroutine）
  ├─ 阶段一 runCountdownPhase：对 countdownTargets 播报**对齐的**倒计时
  └─ 阶段二：串行遍历 Instances
       ├─ 检查整批取消 / 单实例跳过
       ├─ 预检已判 skipped → 跳过
       ├─ batchDoCAS 原子改状态，失败 → skipped
       ├─ executeInstance 调 instance.StartServer/StopServer/RestartServer
       └─ 实例间延迟
```

### 4.1 预检：必须在倒计时**之前**

```go
func operable(instanceName string, opType BatchOperationType) (bool, string) {
    if opType == BatchStart {
        return true, ""     // 启动没有倒计时，交给阶段二 CAS
    }
    return instancepkg.IsStoppable(instanceName)
}
```

`instance.IsStoppable` 的判据是**状态 + 进程存活**双条件：
状态必须是 `started`，且 `process.IsInstanceProcessAlive` 确认进程真的在。
状态记录缺失时回退到进程存活判据，并补写一条与实况一致的记录。

> **为什么不能只靠阶段二的 CAS。**
> CAS 在倒计时**之后**才跑，而且它对「无状态记录」并不报错——只是静静
> `return false, nil` 把实例标成 skipped。放行的话，一台早就停了的实例会被拉进倒计时：
> 公告一条都发不出去（RCON 连不上），整轮倒计时白烧，期间单例锁一直被占着，
> 下一次批量（比如定时更新任务）只会拿到 `ErrOperationInProgress`。
> 预检把判断提到倒计时之前，CAS 保留作为权威门禁——倒计时期间状态可能变化。

预检**不持 `bm.mu`**：它要跑 `netstat`，占着锁会把 `/api/server/batch/status` 的轮询一起拖住。
代价是并发请求时会多做一次无用的预检，可以接受。

### 4.2 阶段一：对齐的倒计时

```go
res, err := countdown.Wait(op.ctx, op.countdownTargets, action, op.countdown)
```

- **为什么统一前置**：塞进 `executeInstance` 的话，5 个实例 × 10 分钟 = 50 分钟，
  且最后一个实例的玩家要等到第 40 分钟才收到「还有 10 分钟」。
  前置之后总耗时 = 倒计时 + 原有串行耗时，全服倒计时是对齐的。
- **`countdownTargets` 而非 `Instances`**：只对通过预检的实例播报。
  目标为空（例如所有实例本来就是停止的）时**直接早退**进阶段二，整批毫秒级结束、立刻释放单例。
- **单台取消不牵连整批**：`countdown` 给每个实例独立子 context，`Cancel(name)` 只掐那一台。
  被取消的实例接进已有的跳过机制，阶段二无需额外分支。
- **整批中止的判据**：只有**用户把每一台都取消了**才算中止。
  `countdown` 兜底判出的 `ErrNotRunning`（预检漏网之鱼）不算——那些实例本来就无事可做，
  交给阶段二 CAS 正常收场即可。

### 4.3 阶段二：串行 + CAS

```go
func batchDoCAS(instanceName string, opType BatchOperationType) (bool, error) {
    switch opType {
    case BatchStart:
        // 允许 stopped / start_failed / stop_failed / restart_failed / 空
        return statepkg.CompareAndSwapInstanceState(..., StatusStartStartInitialization)
    case BatchStop:
        // 只允许 started
        return statepkg.CompareAndSwapInstanceState(...,
            []InstanceStatus{StatusStarted}, StatusStopping)
    case BatchRestart:
        // 只允许 started
        return statepkg.CompareAndSwapInstanceState(...,
            []InstanceStatus{StatusStarted}, StatusRestarting)
    }
}
```

CAS 成功即代表状态已被抢占，因此 `executeInstance` 一律传
`instancepkg.WithStatePreset()`，告诉下游「状态我已经写好了，别重复写」。

预检已标成 `skipped` 的实例在循环开头就 `continue`，
避免 CAS 用通用文案「operation not allowed in current state」盖掉预检给出的精确原因。

---

## 5. 取消与跳过

| 操作 | 入口 | 粒度 | 机制 |
|------|------|------|------|
| 取消整批 | `POST /api/server/batch/cancel` → `CancelCurrent()` | 全部 | 取消 op 的根 context |
| 取消单实例倒计时 | `POST /api/server/:name/countdown/cancel` | 单台 | `countdown.Cancel(name)`，只掐子 ctx |
| 跳过单实例 | `POST /api/server/batch/skip` → `SkipInstance()` | 单台 | 关闭该实例的 skip 通道 |
| 调用方主动取消 | `(*BatchOperation).Cancel()` | 全部 | 供 `schedule` 在任务 ctx 结束时收口 |

**跳过通道的重复关闭防护**：用户主动跳过与倒计时被取消是两条路径，
可能先后落在同一个实例上（倒计时期间先点了跳过，随后又取消了这台的倒计时），
裸 `close` 会 panic。每个实例配一个 `sync.Once`：

```go
skipOnce map[string]*sync.Once
func (op *BatchOperation) signalSkipLocked(name string) {
    once, ok := op.skipOnce[name]
    if !ok { return }
    once.Do(func() { close(op.skipChannels[name]) })
}
```

`manager_test.go` 里有 5 个用例专门覆盖这套语义（幂等、两种顺序、并发安全、只影响目标）。

> **`(*BatchOperation).Cancel()` 优于 `CancelCurrent()`**：后者取消的是「当前那个」，
> 可能误伤已经换代的新操作。`schedule` 的 `awaitBatch` 在任务 ctx 结束时
> 必须调 `op.Cancel()` 并等 `Done()`——批量操作的 ctx 派生自 `Background`，
> 不掐断的话它会继续跑完整轮倒计时，把单例一直占着，顶掉下一次调度。

---

## 6. 日志广播与 SSE 契约

### 6.1 广播器归 manager 所有

`LogBroadcaster` 在 `Initialize()` 里创建并 `Start()`，**全程存活**，
`StartOperation` 只是把它挂到 op 上（`op.logBroadcaster = bm.logBroadcaster`），
操作结束**不 Stop**，只有 `bm.Shutdown()` 才收口。

> **为什么不能每次操作一个。** SSE 客户端（批量操作弹窗）连上后要跨多次批量持续收日志。
> 广播器随操作拆掉的话订阅者会被踢下线，前端只能靠重连补救——
> 而 `EventSource` 分不清「服务端正常收尾」和「连接抖断」，那就成了每 3 秒一次的重连风暴。

`Send` 对慢订阅者是**非阻塞**的（`select`/`default`），宁可丢日志也不拖住批量主循环。
`Subscribe` 返回第三个值 `ok`，为 false 表示广播器已停止（进程正在退出）——
`running` 的读写都在同一把锁下，「Stop 跑完之后才订阅 → handler 永久挂住」的竞态就此关闭。

### 6.2 `/api/server/batch/logs` 是长连接

```
写 SSE 头 → 先订阅（避免与回放之间漏帧）→ 回放 GetCurrentOrLast 的历史
         → for/select 一直推，直到客户端断开或进程退出
```

三条约定，改动时务必保持：

1. **不因为「当前没有批量在跑」提前 return**。以前那样做的后果是
   200 已提交、流却在亚毫秒内结束，浏览器按默认 3 秒无限重连，日志里全是 0ms 的 200。
2. **服务端主动收尾时发 `event: end`**，客户端据此 `close()` 不再重连。
   客户端主动断开（`ctx.Done()`）不用发——对面已经不听了。
3. **30 秒 keepalive 注释帧**：没有批量时这条流零流量，经反向代理容易被判空闲掐掉。

连接的建立与断开**由前端弹窗的开启状态决定**（`BatchOperationDialog.vue` 的
`ensureLogSubscription` / `stopLogSubscription`），后端只管一直推。

---

## 7. HTTP API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/server/batch/start` | 批量启动（忽略倒计时参数） |
| POST | `/api/server/batch/stop` | 批量停止 |
| POST | `/api/server/batch/restart` | 批量重启 |
| GET | `/api/server/batch/status` | 当前操作状态与逐实例进度 |
| GET | `/api/server/batch/logs` | **SSE 长连接**：历史回放 + 实时日志 |
| POST | `/api/server/batch/cancel` | 取消整批 |
| POST | `/api/server/batch/skip` | 跳过指定实例 |

启动类接口的响应带预检结果：

```json
{
  "success": true,
  "data": {
    "total": 3,
    "eligible": 1,
    "skipped": [{"name": "ces99", "reason": "实例当前不处于已启动状态"}]
  }
}
```

因为预检是在 `StartOperation` 里**同步**完成的，这份 `eligible`/`skipped` 是准确的
（早期版本在协程刚启动时读结果表，永远报「全部 eligible」）。

**状态码约定**：`ErrOperationInProgress` → 409；`ErrNoInstances` 与倒计时参数非法 → 400。

---

## 8. 并发与生命周期

`runBatchOperation` 的 defer 注册顺序即 LIFO 执行顺序：

```go
defer close(op.done)        // 最先注册 = 最后执行
defer func() { /* Status=completed；bm.current=nil 归档到 last */ }()
defer func() { /* recover panic */ }()
```

所以退出时的顺序是：**recover → 写状态 + 释放单例 → 关闭 done**。
等待方（`schedule` 的 `awaitBatch`）在 `<-op.Done()` 返回后，
**保证**能观察到 `bm.current == nil`，紧接着发起新批量不会撞上 `ErrOperationInProgress`。

其它注意点：

- `op.Status = "completed"` 是无条件写的，会覆盖中途设的 `"cancelled"`。
  **判断成败要看 `op.InstanceResults`，不要看 `op.Status`**。
- `bm.current = nil` 前有 `if bm.current == op` 守卫，避免过期 goroutine 清掉新操作。
- 所有错误出口都在 `bm.current = op` **之前**，因此没有任何路径会让单例卡死。

---

## 9. 依赖关系

```
webapi / schedule                 （调用方）
        │
        ▼
   batchmanage
        ├──► countdown  ──► rconx / realtime / instance / state
        ├──► instance   ──► config / process / mirror / installer
        ├──► state      （CAS 状态机）
        ├──► config     （GetAvailableInstances）
        └──► realtime   （WS 广播批量进度）
```

`batchmanage` 位于依赖链顶端，**不被任何领域包反向依赖**，
因此可以自由使用 `instance` / `countdown` 而不必担心成环。

延迟停止/重启的编排**只走 `countdown` 一个入口**——不要在 `batchmanage` 里另起炉灶。

---

## 10. 相关文档

- [stop-restart-countdown.md](stop-restart-countdown.md) — 倒计时机制设计
- [COUNTDOWN_RCON_REFACTOR_PLAN.md](COUNTDOWN_RCON_REFACTOR_PLAN.md) — countdown/rconx 拆分方案
- [STATE_CONTROL.md](STATE_CONTROL.md) — 实例状态机与 CAS
- [SCHEDULE_RUN_LOG_DESIGN.md](SCHEDULE_RUN_LOG_DESIGN.md) — 定时任务（批量操作的调用方之一）
- [API_REFERENCE.md](API_REFERENCE.md) — HTTP API 完整参考
