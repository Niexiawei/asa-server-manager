# 实例状态变更统一推送方案

## 背景与问题

当前 WebSocket 状态推送分散在 `webapi/api.go` 和 `webapi/task.go`，手动调用
`httpserver.Broadcast*` 推送。导致 `asaserver/server.go` 内部写入的中间态
（`start_initialization`、`start_initialization_successful`、`starting`、
启动重试失败后的 `stopped` 等）无法到达前端，前端状态显示滞后。

根本原因：`StateManager.writeStateLocked` 已通过 `stateChange.Broadcast()` 通知内部等待者，
但没有对外提供订阅接口，`webapi` 层无法感知全部状态变更。

## 解决思路

在 `asaserver/StateManager` 上增加轻量 pub/sub 机制。`webapi` 启动时订阅，
将**所有** `WriteInstanceState` 调用（包括内部 goroutine 的写入）统一转发为
WebSocket 事件，彻底替代 `api.go`/`task.go` 中零散的手动广播。

---

## 架构

```
asaserver.WriteInstanceState()
    └─ StateManager.writeStateLocked()
           ├─ stateChange.Broadcast()        ← 内部等待者（已有）
           └─ notifySubscribers(state)        ← 新增：非阻塞投递到订阅 channel

webapi.APIServer.startStateChangeDispatcher()
    └─ goroutine: for state := range ch
           └─ broadcastInstanceStateChange(state)
                  └─ httpserver.BroadcastServerEventWithData(...)
                         └─ globalHub.sendEventToAll(ServerEvent{...})
                                └─ WebSocket 客户端
```

---

## 实现细节

### StateManager 订阅机制

`StateManager` 新增字段：

```go
subMu     sync.Mutex
subs      map[int]chan InstanceState
nextSubID int
```

**锁序**：`writeStateLocked` 持有 `sm.mu` 时调用 `notifySubscribers`，后者只获取 `subMu`。
`Subscribe/Unsubscribe` 只获取 `subMu`，从不获取 `sm.mu`。
顺序始终 `sm.mu → subMu`，无死锁风险。

`notifySubscribers` 使用非阻塞 select，慢订阅者直接丢弃事件，
保证状态写入路径不被外部消费者阻塞。

### state_dispatcher.go

`startStateChangeDispatcher` 在 `InitStateManager` 之后启动，goroutine 退出由两条路径覆盖：
- `serverCtx.Done()`：正常关闭时 `Stop()` 取消 context
- `CloseStateManager()` 关闭所有订阅 channel，`<-ch` 返回 `ok=false`

### 状态 → WS 事件映射

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

---

## 手动广播清理

以下调用被删除（现由 dispatcher 统一覆盖）：

**`webapi/api.go`**：
- `startServer` → `BroadcastServerStartingEvent`
- `stopServer` → `BroadcastServerStoppingEvent`
- `restartServer` → `BroadcastServerRestartingEvent`
- `forceStopServer` → `BroadcastServerStoppedEvent`

**`webapi/task.go`**：
- `runStartServerTask` 的 `WithGameInitializationSuccessfulCallback` 内的 `BroadcastServerStartingEvent`
- `runStartServerTask` 成功路径的 `BroadcastServerStartedEvent`
- `runStartServerTask` 失败路径的 `BroadcastServerEvent("server_start_failed",...)`
- `runStopServerTask` 成功路径的 `BroadcastServerStoppedEvent`
- `runStopServerTask` 失败路径的 `BroadcastServerEvent("server_stop_failed",...)`
- `runRestartServerTask` 的 `WithRestartStartupCompletion` 内的 `BroadcastServerRestartedEvent`
- `runRestartServerTask` 成功路径的 `BroadcastServerStartedEvent`
- `runRestartServerTask` 失败路径的 `BroadcastServerEvent("server_restart_failed",...)`

**保留**（不经过 StateManager）：
- `ErrOperationNotAllowed` 分支的广播（CAS 失败，不写入状态）
- `BroadcastUpdateStarted/Completed/Cancelled`（更新任务，非实例状态）
- `BroadcastBatch*`（批量操作事件，非实例状态）
