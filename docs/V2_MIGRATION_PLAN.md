# asaserverv2 镜像启动方式迁移至 asaserver 方案

## 一、迁移目标

将 `asaserverv2` 包中的镜像启动方式（mirror-based startup）迁移至 `asaserver` 包，替代 v1 的 `setupInstanceConfig` + `confReset` 方式，然后删除 `asaserverv2` 包。

**迁移后效果**：
- 所有服务器实例可并行启动（消除全局锁）
- 消除共享目录修改（不再在 `server-files/` 上创建 junction）
- 简化日志管理（去掉全局映射文件）
- 统一代码库，消除 v1/v2 两套并存的维护负担

## 二、迁移范围

### 2.1 需要从 asaserverv2 迁入 asaserver 的内容

| 模块 | 源文件 | 目标位置 | 说明 |
|------|--------|----------|------|
| 镜像管理 | `asaserverv2/mirror.go` | `asaserver/mirror.go` | 全部函数：`SyncInstanceMirror`, `createInstanceMirror`, `syncMirrorEntries`, `CleanupInstanceMirror`, `InstanceMirrorDir` 等 |
| 启动核心 | `asaserverv2/server.go` 的 `startServerInternal` | `asaserver/server.go` 替换 v1 版本 | 镜像同步 + exe 路径 + 日志路径 |
| 日志路径 | `asaserverv2/common.go` 的 `GetGameLogFilePath` | `asaserver/common.go` 替换 v1 版本 | 简化为直接路径 |
| 存档安全 | `asaserverv2/common.go` 的 `SaveWorldSafely` | `asaserver/common.go` 更新 | 存档路径从 server-files 改为 instances/<name>/Save/ |
| 强制停止 | `asaserverv2/force_stop.go` | `asaserver/server.go` 中的 `ForceStopServer` | 添加镜像清理，去掉 WaitForNoInitializing |
| 辅助函数 | `asaserverv2/common.go` 的 `quotifyIfNeeded`, `arkApiCleanConsoleOutput` | `asaserver/common.go` | 如果 v1 不存在则迁入 |

### 2.2 需要从 asaserver 删除的内容

| 模块 | 函数/变量 | 原因 |
|------|-----------|------|
| v1 config junction | `setupInstanceConfig()` | 被 `SyncInstanceMirror` 替代 |
| v1 config reset | `confReset` 变量及其所有引用 | v2 不需要 junction 释放 |
| v1 目录复制 | `CopyDir()` (用于 config 复制的版本) | 不再需要 |
| v1 日志映射 | `InitializeLogMapping()` | v2 直接使用实例目录 |
| v1 日志映射 | `GetGameLogFileName()` | v2 直接返回固定文件名 |
| v1 日志映射 | `PersistLogMapping()` | v2 不需要持久化映射 |
| v1 日志映射 | `RemoveInstanceLogMapping()` | v2 无需清理映射 |
| v1 日志映射 | `removeNotRunningServerLogMapper()` | v2 无需清理 |
| v1 日志映射 | `instanceLogMapping` map + `logMappingMutex` | v2 不使用内存映射 |
| v1 日志映射 | `LogMappingFile` 变量 | v2 不使用映射文件 |
| v1 日志映射 | `LogMapping` struct + `LoadLogMappingFromFile` / `SaveLogMappingToFile` | v2 不使用 |
| v1 全局锁 | `isAnyInstanceInitializingLocked()` | v2 不需要全局初始化互斥 |
| v1 全局锁 | `WaitForNoInitializing()` | v2 不需要等待 junction 释放 |

### 2.3 需要保留不变的内容

| 模块 | 说明 |
|------|------|
| `state_manager.go` | 状态机逻辑不变（CAS、状态常量、状态历史、卡死恢复） |
| `config.go` 中的 `InstanceConfig` | 结构体和读写逻辑不变 |
| `config.go` 中的 `LoadInstanceConfig` / `SaveInstanceConfig` / `UpdateInstanceConfig` | 不变 |
| `config.go` 中的 `CheckForDuplicatePorts` | 不变 |
| `config.go` 中的 Config 文件操作 | `GetGameIniContent`, `SaveGameIniContent` 等不变 |
| `common.go` 中的 `TailLogFileWithLines` / `TailLogFileWithLinesContext` | 不变 |
| `common.go` 中的 `WaitGamePidExit` | 不变 |
| `common.go` 中的 `WaitArkApiRunServer` | 不变 |
| `common.go` 中的 `GetPIDByPort` | 不变 |
| `common.go` 中的 `killGameServer` | 不变 |
| `common.go` 中的 `MonitorAndExtractModInfo` | 不变 |
| `server.go` 中的 `StartServer` 公共入口 | CAS 逻辑不变 |
| `server.go` 中的 `StopServer` 核心逻辑 | 基本不变 |
| `server.go` 中的 `RestartServer` | 基本不变 |

### 2.4 需要更新调用方

| 调用方 | 文件 | 变化 |
|--------|------|------|
| webapi | `webapi/api.go`, `webapi/actions.go`, `webapi/task.go` | `asaserverv2.XXX` → `asaserver.XXX` |
| gui | `gui/gui.go` | `asaserverv2.XXX` → `asaserver.XXX` |
| winservice | `winservice/service.go` | `asaserverv2.XXX` → `asaserver.XXX` |
| main | `main.go` | `asaserverv2.XXX` → `asaserver.XXX` |

## 三、分步迁移计划

### Step 1: 将 mirror.go 迁入 asaserver 包

**操作**：
1. 复制 `asaserverv2/mirror.go` 到 `asaserver/mirror.go`
2. 修改 package 声明为 `package asaserver`
3. 更新内部引用：
   - `asaserver.BaseDir` → `BaseDir`（同包引用）
   - `asaserver.InstancesDir` → `InstancesDir`
   - `asaserver.ServerFilesDir` → `ServerFilesDir`
   - `asaserver.LoadInstanceConfig` → `LoadInstanceConfig`
   - `asaserver.InstanceConfig` → `InstanceConfig`
4. 添加缺失的 import

**影响文件**：新增 `asaserver/mirror.go`

### Step 2: 重写 startServerInternal

**操作**：在 `asaserver/server.go` 中重写 `startServerInternal`：

```go
func startServerInternal(instanceName string, options ...StartServerOptionsFunc) error {
    // ===== 保留部分 =====
    // 1. Options 初始化 + context 创建
    // 2. Deferred 错误处理（写 StatusStartFailed + 清理镜像）
    // 3. CheckForDuplicatePorts()
    // 4. LoadInstanceConfig(instanceName)
    // 5. 构建命令行参数
    // 6. 启动进程（PTY / exec.Command）
    // 7. SaveInstancePID()
    // 8. waitServerStartup() goroutine

    // ===== 替换部分 =====
    // [删除] setupInstanceConfig(instanceName, &confReset)
    // [新增] mirrorDir, err := SyncInstanceMirror(instanceName, config)
    //       ↓
    //       exe 路径改为: mirrorDir/ShooterGame/Binaries/Win64/ArkAscendedServer.exe
    //       工作目录改为: mirrorDir/ShooterGame/Binaries/Win64

    // [删除] confReset() 调用（在 successfullyCallback 中）
    // [删除] removeNotRunningServerLogMapper() 调用
    // [删除] PersistLogMapping() 调用

    // ===== 简化部分 =====
    // 日志路径: 直接使用 instances/<name>/Logs/ShooterGame.log
    // （不再经过 GetGameLogFileName → instanceLogMapping → PersistLogMapping 链路）
}
```

**关键变化**：

| 项目 | v1 代码 | v2 代码 |
|------|---------|---------|
| Config 准备 | `setupInstanceConfig(name, &confReset)` | `SyncInstanceMirror(name, config)` |
| exe 路径 | `filepath.Join(ServerFilesDir, "ShooterGame/Binaries/Win64", exeName)` | `filepath.Join(mirrorDir, "ShooterGame/Binaries/Win64", exeName)` |
| 工作目录 | `filepath.Join(ServerFilesDir, "ShooterGame/Binaries/Win64")` | `filepath.Join(mirrorDir, "ShooterGame/Binaries/Win64")` |
| 日志初始化 | `GetGameLogFilePath()` + mapping 系统 | 直接返回 `InstancesDir/<name>/Logs/ShooterGame.log` |
| confReset | `confReset()` 调用释放 junction | 不需要 |
| 启动后持久化 | `PersistLogMapping()` | 不需要 |

**影响文件**：`asaserver/server.go`

### Step 3: 更新 waitServerStartup 回调

**操作**：修改 `startServerInternal` 中 `waitServerStartup` 的两个回调：

```go
// callback (启动完成/失败)
callback := func(startup bool, err string) {
    if startup {
        WriteInstanceState(instanceName, StatusStarted, "")
        if opts.WaitServerCompleted {
            startupSuccess <- true  // 通知 WaitServerCompleted 路径
        }
        if opts.OnRestartStartupComplete != nil {
            opts.OnRestartStartupComplete(instanceName)
        }
    } else {
        WriteInstanceState(instanceName, StatusStartFailed, err)
        initFailed <- fmt.Errorf("%s", err)
    }
}

// successfullyCallback (游戏初始化完成)
successfullyCallback := func() {
    WriteInstanceState(instanceName, StatusStartStartInitializationSuccessful, "")
    // [删除] confReset()  ← v2 不需要
    WriteInstanceState(instanceName, StatusStarting, "")
    initSuccessful <- true
    if opts.GameInitializationSuccessful != nil {
        opts.GameInitializationSuccessful()
    }
}
```

**影响文件**：`asaserver/server.go` 中 `startServerInternal` 的回调定义

### Step 4: 简化日志系统

**操作**：替换 v1 的 log mapping 系统为 v2 的直接路径：

**删除**（`asaserver/server.go` 和 `asaserver/config.go`）：
- `InitializeLogMapping()` 函数及其 fsnotify watcher
- `GetGameLogFileName()` 函数
- `PersistLogMapping()` 函数
- `RemoveInstanceLogMapping()` 函数
- `removeNotRunningServerLogMapper()` 函数
- `instanceLogMapping` map 变量
- `logMappingMutex` 变量
- `LogMappingFile` 变量
- `LogMapping` struct 及其 `LoadLogMappingFromFile()` / `SaveLogMappingToFile()`

**替换为**（`asaserver/common.go`）：
```go
func GetGameLogFilePath(instanceName string) (string, error) {
    logDir := filepath.Join(InstancesDir, instanceName, "Logs")
    if err := os.MkdirAll(logDir, 0755); err != nil {
        return "", err
    }
    logPath := filepath.Join(logDir, "ShooterGame.log")
    if _, err := os.Stat(logPath); os.IsNotExist(err) {
        os.WriteFile(logPath, nil, 0644)
    }
    return logPath, nil
}
```

**影响文件**：`asaserver/server.go`, `asaserver/config.go`, `asaserver/common.go`

### Step 5: 更新 stopServerInternal

**操作**：在 `stopServerInternal` 中：
1. 删除 `removeInstanceLogMapping()` 调用
2. 更新 `SaveWorldSafely` 中的存档路径（Step 6）

**影响文件**：`asaserver/server.go`

### Step 6: 更新 SaveWorldSafely

**操作**：修改存档路径构建逻辑：

```go
// v1:
savePath := filepath.Join(ServerFilesDir, "ShooterGame/Saved", config.SaveDir, dirMapName, dirMapName+".ark")

// v2:
savePath := filepath.Join(InstancesDir, instanceName, "Save", dirMapName, dirMapName+".ark")
```

**影响文件**：`asaserver/common.go`

### Step 7: 更新 ForceStopServer

**操作**：
```go
// v1:
func ForceStopServer(instanceName string) error {
    WaitForNoInitializing(2 * time.Minute)  // [删除] 不需要等 junction
    // ... kill process ...
    // ... write StatusStopped ...
}

// v2:
func ForceStopServer(instanceName string) error {
    // [新增] 清理镜像目录
    CleanupInstanceMirror(instanceName)
    // ... kill process ...
    // ... write StatusStopped ...
    // 无需 WaitForNoInitializing，因为每个实例的 junction 是独立的
}
```

**影响文件**：`asaserver/server.go`

### Step 8: 更新 RestartServer

**操作**：
```go
// v1:
func RestartServer(instanceName string) error {
    stopServerInternal(instanceName)
    time.Sleep(10 * time.Second)  // [删除] 不需要等 junction 释放
    StartServer(instanceName, WithWaitServerCompleted(), ...)
}

// v2:
func RestartServer(instanceName string) error {
    stopServerInternal(instanceName)
    // [删除] time.Sleep(10 * time.Second)
    StartServer(instanceName, WithWaitServerCompleted(), ...)
}
```

**影响文件**：`asaserver/server.go`

### Step 9: 更新状态机（移除全局锁）

**操作**：修改 `isOperationAllowed` 和相关逻辑：

1. 从 `isOperationAllowed` 中删除全局 `start_initialization` 互斥规则
2. 仅保留 per-instance 状态检查（start 允许从 stopped/failed 状态启动）
3. 可选：保留 `isAnyInstanceInitializingLocked` / `WaitForNoInitializing` 但标记为 deprecated

**影响文件**：`asaserver/state_manager.go`

### Step 10: 更新调用方

**操作**：将所有 `asaserverv2.XXX` 调用改为 `asaserver.XXX`：

| 文件 | 变化示例 |
|------|----------|
| `webapi/api.go` | `asaserverv2.StartServer(...)` → `asaserver.StartServer(...)` |
| `webapi/actions.go` | `asaserverv2.ForceStopServer(...)` → `asaserver.ForceStopServer(...)` |
| `webapi/task.go` | `asaserverv2.RestartServer(...)` → `asaserver.RestartServer(...)` |
| `gui/gui.go` | `asaserverv2.StartServer(...)` → `asaserver.StartServer(...)` |
| `winservice/service.go` | 同上 |
| `main.go` | 同上 |

**注意**：需确认 v2 中 `RestartServer` 的签名与 v1 一致（都使用 `...StartServerOptionsFunc`）。

### Step 11: 清理 asaserverv2 包

**操作**：删除整个 `asaserverv2/` 目录：
```
asaserverv2/server.go       ← 已迁移
asaserverv2/common.go       ← 已迁移
asaserverv2/mirror.go       ← 已迁移
asaserverv2/force_stop.go   ← 已迁移
asaserverv2/server_test.go  ← 需迁移到 asaserver/
```

### Step 12: 清理 asaserver 中的冗余代码

**操作**：删除以下不再使用的代码：
- `setupInstanceConfig()` 函数
- `CopyDir()` 函数（用于 config 复制的那个版本）
- `confReset` 变量及所有引用
- v1 日志映射相关全部代码（见 Step 4）

## 四、迁移时一并修复的 Bug

| Bug | 描述 | 修复方案 |
|-----|------|----------|
| `startErr` 数据竞争 | `asaserverv2/server.go:60,272` — goroutine 写 startErr，deferred 读取 | 去掉 startErr 变量，deferred 从 `initFailed` channel 读取错误值 |
| `waitServerStartup` double-close | `asaserverv2/common.go:47,56` — 两个 goroutine 可能同时 close(startup) | 从 v1 移植 `sync.Once` + `safeCloseStartup` 模式 |
| PTY 泄漏 | `asaserverv2/server.go:224` — AsaApiLoader 路径缺少 `defer pp.Close()` | 添加 `defer pp.Close()` |

## 五、风险评估

| 风险 | 等级 | 缓解措施 |
|------|------|----------|
| 首次创建镜像耗时 | 中 | 增量同步复用已有镜像；大目录跳过递归 |
| 磁盘空间（N 个 exe 副本） | 中 | 每实例 ~200-500MB，其他均为 junction/symlink（0 空间） |
| exe 复制后版本不一致 | 低 | 增量同步的 MD5 校验会检测并更新 |
| 多实例同时启动 I/O 压力 | 中 | 非 exe 文件是 symlink，I/O 压力有限 |
| 存档路径变更影响备份功能 | 高 | 需同步更新 `backup/backup.go` 中的路径 |
| 日志路径变更影响前端日志流 | 中 | 需确认 SSE 日志流使用 `GetGameLogFilePath` 而非硬编码路径 |
| Config API 不受影响 | 低 | Config 文件位置未变（`instances/<name>/Config/`） |

## 六、验证计划

### 6.1 单元测试

为 `mirror.go` 核心函数编写测试：
- `TestCreateInstanceMirror` — 验证镜像创建正确
- `TestCleanupInstanceMirror` — 验证清理不删除目标文件
- `TestSyncMirrorEntries` — 验证增量同步
- `TestBuildExceptionTargets` — 验证 exception 映射构建

### 6.2 集成测试

- 创建两个实例，验证**可同时启动**（无全局锁）
- 验证镜像创建后 junction 指向正确目标
- 验证增量同步：修改 server-files 后启动，检查镜像更新
- 验证 启动 → 停止 → 启动 循环
- 验证 ForceStop 正确清理镜像
- 验证启动失败时镜像被清理
- 验证 Restart 不再需要 sleep 10 秒

### 6.3 回归测试

- Config API：读写 Game.ini / GameUserSettings.ini
- 备份/恢复功能
- Save 监控（parseserver）
- WebSocket / SSE 事件推送
- 日志流（`GET /api/logs/:name`）
- Windows 服务模式

## 七、迁移后目录结构变化

```
迁移前:
asaserver/
├── server.go          # v1 启动（含 setupInstanceConfig, CopyDir, log mapping）
├── common.go          # 工具函数
├── config.go          # 配置（含 LogMapping）
├── state_manager.go   # 状态机
├── installer.go       # 安装器
asaserverv2/
├── server.go          # v2 启动（含 mirror）
├── common.go          # v2 工具函数
├── mirror.go          # 镜像管理
├── force_stop.go      # 强制停止

迁移后:
asaserver/
├── server.go          # v2 启动方式（含 mirror 调用）
├── common.go          # 工具函数（简化后的日志路径）
├── config.go          # 配置（去掉 LogMapping）
├── state_manager.go   # 状态机（去掉全局锁）
├── mirror.go          # 镜像管理（从 v2 迁入）
├── installer.go       # 安装器
（asaserverv2/ 已删除）
```

## 八、备份/恢复功能配套更新

`backup/backup.go` 中的 `BackupInstanceWorld` 和 `RestoreBackupToInstance` 需要更新存档路径：

```go
// v1:
savePath := filepath.Join(ServerFilesDir, "ShooterGame/Saved", config.SaveDir)

// v2:
savePath := filepath.Join(InstancesDir, instanceName, "Save")
```

同样，`RestoreBackupToInstance` 中恢复文件的目标路径也需要同步更新。
