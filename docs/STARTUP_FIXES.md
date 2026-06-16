# ASA 服务器启动流程缺陷修复记录

**日期**: 2026-06-13  
**范围**: ASA Server 启动/停止/重启全流程  
**涉及文件**:
- `asaserver/common.go`
- `asaserver/server.go`
- `webapi/task.go`

---

## 修复内容

### 修复 #1 [P0]: `waitServerStartup` 永久阻塞 / 协程泄漏

**文件**: `asaserver/common.go` — `waitServerStartup()`

**问题**: 当游戏进程在日志输出 `"Server has completed startup"` 之前崩溃退出时，`WaitGamePidExit` 调用 `cancel()` 停止了日志追踪，但 `startup` channel 永远不收到信号，`<-startup` 永久阻塞，导致 goroutine 泄漏。

**修复方案**:
- 将 `startup` 从缓冲 channel（`make(chan struct{}, 1)`）改为无缓冲 channel（`make(chan struct{})`）
- 在 `WaitGamePidExit` 分支中使用 `close(startup)` 替代原来的 `cancel()`，确保进程退出时解除 `<-startup` 阻塞
- 在日志检测分支中同样使用 `close(startup)` 替代 `startup <- struct{}{}`

```go
// 修复前
startup = make(chan struct{}, 1)
go func() {
    if exited := WaitGamePidExit(ctx, pid); exited {
        callback(false, latestLogLine)
        cancel() // startup 永远不收到信号
    }
}()
// 日志检测分支
startup <- struct{}{}

// 修复后
startup = make(chan struct{}) // 无缓冲
go func() {
    if exited := WaitGamePidExit(ctx, pid); exited {
        callback(false, latestLogLine)
        close(startup) // 确保解除阻塞
    }
}()
// 日志检测分支
close(startup)
```

---

### 修复 #2 [P0]: `startErr` 遗漏赋值导致状态永久错误

**文件**: `asaserver/server.go` — `StartServer()`

**问题**: `GetGameLogFilePath` 失败时直接 return，但未设置 `startErr`，导致 defer 中的 `if startErr != nil` 判断为 false，不会写入 `start_failed` 状态，实例状态永久停留在 `start_initialization`。

**修复方案**: 在 return 前设置 `startErr`：

```go
// 修复前
gameLogPath, err := GetGameLogFilePath(instanceName)
if err != nil {
    return fmt.Errorf("failed to get game log file path: %w", err)
}

// 修复后
gameLogPath, err := GetGameLogFilePath(instanceName)
if err != nil {
    startErr = fmt.Errorf("failed to get game log file path: %w", err)
    return startErr
}
```

---

### 修复 #3 [P2]: `StopServer` 等待进程退出添加 5 分钟超时

**文件**: `asaserver/server.go` — `StopServer()`

**问题**: 等待进程退出的 `for` 循环无超时机制，进程挂起时永久阻塞。

**修复方案**: 添加 5 分钟超时，超时后 `taskkill /F` 强制终止（大型存档保存时间较长，需要充足的等待时间）：

```go
// 修复前
for {
    exited, err := win32api.IsProcessExited(uint32(pid))
    if err != nil || exited {
        break
    }
    time.Sleep(2 * time.Second)
}

// 修复后
deadline := time.Now().Add(5 * time.Minute)
for {
    exited, err := win32api.IsProcessExited(uint32(pid))
    if err != nil || exited {
        break
    }
    if time.Now().After(deadline) {
        logger.GetLogger().Warnf("Process %d did not exit within 5min, force killing", pid)
        _ = exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid)).Run()
        break
    }
    time.Sleep(2 * time.Second)
}
```

---

### 修复 #4 [P2]: `runStartServerTask` 将 ctx 传递给 `StartServer`

**文件**: `webapi/task.go` — `runStartServerTask()`

**问题**: 
1. `ctx` 创建后未传递给 `StartServer`，导致 `StartServer` 内部的 goroutine 无法被超时取消
2. 超时分支未调用 `cancel()`，`select` 中的 `case <-ctx.Done()` 是死代码

**修复方案**:
1. 通过 `WithCtx(ctx)` 将 ctx 传递给 `StartServer`
2. 超时分支先调用 `cancel()` 再调用 `KillServer`

```go
// 修复前
err := asaserver.StartServer(name, asaserver.WithWaitServerCompleted(), ...)
// ...
case <-time.After(5 * time.Minute):
    //TODO
    //超时强制杀掉进程
    if err := asaserver.KillServer(name); err != nil { ... }

// 修复后
err := asaserver.StartServer(name, asaserver.WithCtx(ctx), asaserver.WithWaitServerCompleted(), ...)
// ...
case <-time.After(5 * time.Minute):
    // 超时：先取消 ctx 通知 StartServer 内部停止，再强制杀进程
    cancel()
    if err := asaserver.KillServer(name); err != nil { ... }
```

---

### 修复 #5 [P3]: `WaitArkApiRunServer` 超时从 10 秒增加到 30 秒

**文件**: `asaserver/common.go` — `WaitArkApiRunServer()`

**问题**: 等待 `ArkAscendedServer.exe` 进程出现的超时仅 10 秒，对模组较多的服务器不足。

**修复方案**: 超时增加到 30 秒，并改善错误信息：

```go
// 修复前
case <-time.After(10 * time.Second):
    return 0, fmt.Errorf("ARK API loading server error")

// 修复后
case <-time.After(30 * time.Second):
    return 0, fmt.Errorf("ARK API loading server error: ArkAscendedServer.exe did not appear within 30 seconds")
```

---

## 审查确认（未修改）

以下问题经审查确认当前设计正确，无需修改：

| 项目 | 说明 |
|------|------|
| `setupInstanceConfig` junction 删除 | `RemoveAll` 只删除 Junction 本身，不影响目标目录；首次启动通过 `Rename` 保护原始数据 |
| API 层双重端口检查 | API 层与核心层的 `CheckForDuplicatePorts()` 双重检查是正确设计 |
| `StopServer` 双重存档保存 | `SaveWorldSafely`（`saveworld`）+ `DoExit`（自动保存）是双重保障设计 |
| `waitServerStartup` 状态写入 | `start_initialization_successful` 和 `started` 两个状态间隔约 3 分钟，均为必要状态 |

## 延后处理

| 项目 | 说明 |
|------|------|
| `serverActionsLock` 锁范围优化 | HTTP 处理器中锁在返回时释放，但异步任务仍在运行。需要整体重新设计锁机制 |
