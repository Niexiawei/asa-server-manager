# WS 状态推送重构说明

## 背景

原有实现中，WebSocket 状态推送散落在 `webapi/api.go`、`webapi/task.go`、`batchmanage/manager.go` 中，
通过手动调用 `httpserver.Broadcast*` 完成。这导致：

1. `asaserver/server.go` 内部 goroutine 写入的中间态（`start_initialization`、`starting`、
   失败后的 `stopped` 等）无法到达前端。
2. 前端收到的状态滞后甚至丢失（如启动重试全部失败后的 `stopped` 状态）。
3. 预检使用非原子 `IsOperationAllowed`，CAS 在 goroutine 内延迟执行，客户端无法立即得到拒绝响应。
4. `ErrOperationNotAllowed` 时错误推送了操作失败事件（操作根本未执行）。
5. `batchmanage` 与 state dispatcher 存在重复广播，前端可能收到同一事件两次。

---

## 一、状态变更统一订阅推送（state dispatcher）

### 涉及文件

- `asaserver/state_manager.go`
- `webapi/state_dispatcher.go`（新建）
- `webapi/actions.go`
- `docs/state-change-ws-push.md`（架构说明）

### 核心思路

在 `StateManager` 上增加轻量 pub/sub 机制。每次 `WriteInstanceState` 调用后，
`writeStateLocked` 除通知内部等待者（`stateChange.Broadcast`）外，
还向所有订阅 channel 非阻塞投递 `InstanceState`。
`webapi` 启动时订阅，将**所有**状态变更统一转发为 WebSocket 事件。

### `asaserver/state_manager.go` 改动

`StateManager` 新增字段：

```go
subMu      sync.Mutex
subs       map[int]chan InstanceState
nextSubID  int
```

新增方法：

| 方法 | 说明 |
|---|---|
| `Subscribe(bufSize int) (int, <-chan InstanceState)` | 注册订阅，返回 ID 和只读 channel |
| `Unsubscribe(id int)` | 注销订阅，关闭 channel |
| `notifySubscribers(state InstanceState)` | 在 `sm.mu` 持有期间非阻塞投递（锁序 `sm.mu → subMu`） |

`writeStateLocked` 在 `stateChange.Broadcast()` 后调用 `notifySubscribers`。

`Close()` 关闭所有订阅 channel（使订阅 goroutine 正常退出）。

包级便捷函数：`SubscribeStateChanges(bufSize)` / `UnsubscribeStateChanges(id)`。

### `webapi/state_dispatcher.go`（新建）

状态 → WS 事件映射：

| InstanceStatus | event_type | status |
|---|---|---|
| `start_initialization` / `start_initialization_successful` / `starting` | `server_starting` | `starting` |
| `started` | `server_started` | `started` |
| `stopping` | `server_stopping` | `stopping` |
| `stopped` | `server_stopped` | `stopped` |
| `start_failed` | `server_start_failed` | `failed` |
| `stop_failed` | `server_stop_failed` | `failed` |
| `restart_failed` | `server_restart_failed` | `failed` |
| `restarting` | `server_restarting` | `restarting` |
| `restarted` | `server_restarted` | `restarted` |

所有事件附带 `data: {"raw_status": "...", "error": "...（可选）"}`。

### `webapi/actions.go` 改动

`Start()` 中 `InitStateManager` 成功后立即启动 dispatcher：

```go
s.startStateChangeDispatcher(s.serverCtx)
```

dispatcher goroutine 退出路径：
- `serverCtx.Done()`（正常关闭）
- `CloseStateManager()` 关闭所有订阅 channel

---

## 二、HTTP Handler 同步 CAS + task 清理

### 涉及文件

- `asaserver/server.go`
- `webapi/api.go`
- `webapi/task.go`

### 核心思路

将 CAS 提前到 HTTP handler 同步执行，立即返回 409（不允许）或 200（已预设状态）。
goroutine 通过 `WithStatePreset()` 跳过重复 CAS。

### `asaserver/server.go` 改动

`StartServerOptions` 新增字段：

```go
StatePreset bool // CAS 已由调用方完成，跳过内部 CAS
```

新增 option 函数：

```go
func WithStatePreset() StartServerOptionsFunc
```

`StartServer`、`StopServer`（新增 `...StartServerOptionsFunc` 参数）、`RestartServer`
均在 `!opts.StatePreset` 条件下才执行内部 CAS。

### `webapi/api.go` 改动

| Handler | 旧方式 | 新方式 |
|---|---|---|
| `startServer` | `IsServerRunning` + `checkInstanceState` | `CompareAndSwapInstanceState(stopped/failed → start_initialization)` |
| `stopServer` | `checkInstanceState` | `CompareAndSwapInstanceState(started → stopping)` |
| `restartServer` | `checkInstanceState` | `CompareAndSwapInstanceState(started → restarting)` |

CAS 失败立即返回 HTTP 409；成功则 spawn goroutine。
删除 `checkInstanceState` 辅助函数及 `httpserver` import（已无用）。

### `webapi/task.go` 改动

三个 task 函数均传 `WithStatePreset()`，移除所有 `ErrOperationNotAllowed` 分支及对应错误广播：

| 函数 | 关键变化 |
|---|---|
| `runStartServerTask` | 传 `WithStatePreset()`，移除 `ErrOperationNotAllowed` 分支 |
| `runStopServerTask` | 传 `WithStatePreset()`，移除 `ErrOperationNotAllowed` 分支 |
| `runRestartServerTask` | 传 `WithStatePreset()` + `WithRestartStartupCompletion(func(string){})` 确保写入 `StatusRestarted` 状态 |

`WithRestartStartupCompletion` 空回调的作用：仅触发 `StatusRestarted` 状态写入，
使 dispatcher 能推送 `server_restarted` 事件，前端可区分重启完成与普通启动完成。

保留的广播（不经过 StateManager，dispatcher 感知不到）：
- `ErrOperationNotAllowed` 路径（CAS 未发生，无状态变更）
- `BroadcastUpdateStarted/Completed/Cancelled`（更新任务事件）
- `BroadcastBatch*`（批量操作进度事件）

---

## 三、batchmanage 批量操作改造

### 涉及文件

- `batchmanage/manager.go`

### 核心思路

与单实例操作对齐：替换非原子 `IsOperationAllowed` 为原子 `batchDoCAS`，
传 `WithStatePreset()` 给操作函数，移除所有与 state dispatcher 重复的 WS 广播。

### `batchmanage/manager.go` 改动

**新增 `batchDoCAS` 辅助函数**：

```go
func batchDoCAS(instanceName string, opType BatchOperationType) (bool, error)
```

按操作类型调用对应的 `CompareAndSwapInstanceState`：
- `BatchStart`：`stopped/failed/... → start_initialization`
- `BatchStop`：`started → stopping`
- `BatchRestart`：`started → restarting`

**`runBatchOperation` 改动**：

将 `IsOperationAllowed`（非原子只读）替换为 `batchDoCAS`（原子 CAS）：
- CAS 失败（状态不允许）→ `InstanceSkipped`
- CAS 出错 → `InstanceFailed`，`failed++`
- CAS 成功 → 调用 `executeInstance`（状态已预设）

删除不再使用的 `toASAOperationType` 辅助函数。

**`executeInstance` 改动**：

| 项目 | 旧 | 新 |
|---|---|---|
| `StartServer` | `StartServer(instanceName)` | `StartServer(instanceName, WithStatePreset())` |
| `StopServer` | `StopServer(instanceName)` | `StopServer(instanceName, WithStatePreset())` |
| `RestartServer` | `RestartServer(instanceName, WithRestartStartupCompletion(广播回调))` | `RestartServer(instanceName, WithStatePreset(), WithRestartStartupCompletion(func(string){}))` |
| 启动/停止/重启前广播 | `BroadcastServerStartingEvent` 等 | **删除**（dispatcher 推） |
| 成功广播 | `BroadcastServerStartedEvent` 等 | **删除**（dispatcher 推） |
| 失败广播 | `BroadcastServerEvent("server_start_failed", ...)` 等 | **删除**（dispatcher 从 `StatusStartFailed` 等推） |

保留：批量操作自身的进度广播（`BroadcastBatchOperationStarted/Progress/Completed`），
这些事件与实例状态无关，不经过 StateManager。

---

## 总结：各类事件推送责任归属

| 事件类型 | 推送方 |
|---|---|
| 实例状态变更（starting/started/stopping/stopped/failed/restarting/restarted） | **state dispatcher**（统一） |
| 操作被拒绝（`ErrOperationNotAllowed`，仅单实例） | task.go 手动广播 |
| 批量操作进度（batch_started/batch_progress/batch_completed） | batchmanage 手动广播 |
| 服务器更新进度（update_started/update_completed/update_cancelled） | task.go 手动广播 |
