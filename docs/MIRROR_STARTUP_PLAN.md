# NTFS 镜像启动：v2 迁移、架构与去管理员化（合并文档）

> 本文由 `V2_MIGRATION_PLAN.md`、`V2_MIRROR_STARTUP_ARCHITECTURE.md`、`V2_MIGRATION_CHANGELOG.md`，以及 `MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md` 的第一部分与第三部分，于 2026-09-29 逐字物理合并而成（方案甲）。
> 后者的第二部分（移除 WebAuthn）与本文无依赖关系，已拆分为独立文档 [`WEBAUTHN_REMOVAL_PLAN.md`](./WEBAUTHN_REMOVAL_PLAN.md)。
> ⚠️ 本文各「改动清单 / 实施步骤」写于包拆分重构之前（`asaserverv2` / `asaserver` 包名已不存在），实际路径见文末「附录 Y」。

---

# Part 1 — asaserverv2 镜像启动方式迁移至 asaserver 方案（原 `V2_MIGRATION_PLAN.md`）

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

| 模块 | 源文件:行号 | 目标位置 | 说明 |
|------|------------|----------|------|
| 镜像管理 | `mirror.go` (900行) | `asaserver/mirror.go` | 全部函数：`SyncInstanceMirror`(:74), `createInstanceMirror`(:145), `syncMirrorEntries`(:531), `CleanupInstanceMirror`(:411), `CleanupMirrorCache`(:523), `InstanceMirrorDir`(:68), `buildExceptionTargets`(:209), `IsElevated`(:46), `EntryType*` 常量(:28-31) 等 |
| 启动核心 | `server.go:startServerInternal`(:37) | `asaserver/server.go` 替换 v1 版本 | 镜像同步 + exe 路径 + 日志路径 |
| 日志路径 | `server.go:GetGameLogFilePath`(:497) | `asaserver/server.go` 替换 v1 版本 | 简化为直接路径 `instances/<name>/Logs/ShooterGame.log` |
| 存档安全 | `common.go:SaveWorldSafely`(:148) | `asaserver/common.go` 更新 | 存档路径从 server-files 改为 instances/<name>/Save/ |
| 地图名标准化 | `common.go:savePathReplacement`(:142) | `asaserver/common.go` 迁入 | SaveWorldSafely 中的地图名映射（如 BobsMissions_WP → BobsMissions） |
| 强制停止 | `force_stop.go:ForceStopServer`(:11) | `asaserver/server.go` 替换 | 添加镜像清理，去掉 WaitForNoInitializing |
| 配置同步 | `server.go:SyncGameConfigToInstance`(:524) | `asaserver/server.go` 迁入 | v1 已有类似实现，需对比合并 |
| 配置同步 | `server.go:syncConfigFile`(:549) | `asaserver/server.go` 迁入 | 单文件配置同步 |
| 辅助函数 | `common.go:arkApiCleanConsoleOutput`(:68) | `asaserver/common.go` | v1 已有，保留不变 |
| 辅助函数 | `server.go:quotifyIfNeeded`(:514) | `asaserver/server.go` | v1 已有(:1153)，保留不变 |

### 2.2 需要从 asaserver 删除的内容

| 模块 | 函数/变量 | 文件:行号 | 原因 |
|------|-----------|----------|------|
| v1 config junction | `setupInstanceConfig()` | `server.go:386` | 被 `SyncInstanceMirror` 替代 |
| v1 config reset | `confReset` 变量及其所有引用 | `server.go:551` | v2 不需要 junction 释放 |
| v1 目录复制 | `CopyDir()` | `server.go:357` | 不再需要 |
| v1 日志映射 | `InitializeLogMapping()` | `server.go:76` | v2 直接使用实例目录 |
| v1 日志映射 | `GetGameLogFileName()` | `server.go:177` | v2 直接返回固定文件名 |
| v1 日志映射 | `PersistLogMapping()` | `server.go:154` | v2 不需要持久化映射 |
| v1 日志映射 | `RemoveInstanceLogMapping()` | `server.go:166` | v2 无需清理映射 |
| v1 日志映射 | `removeNotRunningServerLogMapper()` | `server.go:456` | v2 无需清理 |
| v1 日志映射 | `instanceLogMapping` map + `logMappingMutex` | `server.go:32,26` | v2 不使用内存映射 |
| v1 日志映射 | `SetInstanceLogFile()` | `server.go:254` | 依赖内存映射 |
| v1 日志映射 | `GetInstanceLogFile()` | `server.go:261` | 依赖内存映射，**webapi/api.go:1006 使用** |
| v1 日志映射 | `LogMappingFile` 变量 | `config.go` | v2 不使用映射文件 |
| v1 日志映射 | `LogMapping` struct + `LoadLogMappingFromFile` / `SaveLogMappingToFile` | `config.go:305,330` | v2 不使用 |
| v1 全局锁 | `isAnyInstanceInitializingLocked()` | `state_manager.go:426` | v2 不需要全局初始化互斥 |
| v1 全局锁 | `getInitializingInstanceLocked()` | `state_manager.go:444` | v2 不需要 |
| v1 全局锁 | `IsAnyInstanceInitializing()` | `state_manager.go:640` | v2 不需要 |
| v1 全局锁 | `WaitForNoInitializing()` | `state_manager.go:650` | v2 不需要等待 junction 释放 |
| v1 isOperationAllowed | 全局互斥规则（规则1） | `state_manager.go:587-589` | 删除全局锁检查，仅保留 per-instance 状态检查 |

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

### 2.4 需要更新的调用方（非 asaserverv2 引用）

> **注意**：`asaserverv2` 包目前**未被任何外部包导入**。以下更新是因为 v1 的函数签名/行为变化而需要同步修改的调用方。

| 调用方 | 文件:行号 | 当前代码 | 迁移后 |
|--------|----------|----------|--------|
| webapi 日志流 | `webapi/api.go:1006` | `asaserver.GetInstanceLogFile(instanceName)` | `asaserver.GetGameLogFilePath(instanceName)` |
| webapi 停止 | `webapi/api.go:902` | `GetInstanceLogFile(instanceName)` | `GetGameLogFilePath(instanceName)` |
| backup 备份 | `backup/backup.go:36` | `filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved", config.SaveDir)` | `filepath.Join(asaserver.InstancesDir, instanceName, "Save")` |
| backup 恢复 | `backup/backup.go:253` | `filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved", saveDir)` | `filepath.Join(asaserver.InstancesDir, instanceName, "Save")` |

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

**操作**：修改 `state_manager.go`：

1. **删除 `isOperationAllowed` 中的全局互斥规则**（行 587-589）：
```go
// [删除] 规则 1：全局互斥 - 检查是否有实例在 start_initialization
if initInstance := sm.getInitializingInstanceLocked(); initInstance != "" {
    return false, fmt.Sprintf("instance '%s' is in start_initialization state, all operations are blocked", initInstance)
}
```

2. **删除以下函数**：
   - `isAnyInstanceInitializingLocked()` (行 426)
   - `getInitializingInstanceLocked()` (行 444)
   - `IsAnyInstanceInitializing()` (行 640)
   - `WaitForNoInitializing()` (行 650)

3. **删除调用点**：
   - `ForceStopServer` 中的 `WaitForNoInitializing(2 * time.Minute)` (server.go:963)

**影响文件**：`asaserver/state_manager.go`, `asaserver/server.go`

### Step 10: 更新调用方

**操作**：更新因 v1 函数变更而受影响的调用方：

**webapi/api.go** — 日志流（行 1006）：
```go
// 当前：
logPath, exists = asaserver.GetInstanceLogFile(instanceName)
// 迁移后（GetInstanceLogFile 被删除）：
logPath, err := asaserver.GetGameLogFilePath(instanceName)
// 注意：不再需要轮询等待，v2 的 GetGameLogFilePath 直接返回固定路径
```

**webapi/api.go** — stopServerInternal 日志路径（行 902）：
```go
// 当前：
gameLogPath, _ = GetInstanceLogFile(instanceName)
// 迁移后：
gameLogPath, _ = GetGameLogFilePath(instanceName)
```

**backup/backup.go** — 备份存档路径（行 36）：
```go
// 当前：
savePath := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved", config.SaveDir)
// 迁移后：
savePath := filepath.Join(asaserver.InstancesDir, instanceName, "Save")
```

**backup/backup.go** — 恢复存档路径（行 253）：
```go
// 当前：
target = filepath.Join(filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved", saveDir), relPath)
// 迁移后：
target = filepath.Join(filepath.Join(asaserver.InstancesDir, instanceName, "Save"), relPath)
```

### Step 11: 清理 asaserverv2 包

**操作**：删除整个 `asaserverv2/` 目录：
```
asaserverv2/server.go       ← 已迁移
asaserverv2/common.go       ← 已迁移
asaserverv2/mirror.go       ← 已迁移
asaserverv2/force_stop.go   ← 已迁移
asaserverv2/server_test.go  ← 迁移到 asaserver/
```

**注意**：`StartAllInstances` 和 `StopAllInstances` 已被手动移除，不需要迁移。

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
| 存档路径变更影响备份功能 | **高** | 需同步更新 `backup/backup.go` 中的路径（行 36, 253） |
| webapi 日志流中断 | **高** | `GetInstanceLogFile` 被删除后必须同步更新 `webapi/api.go:1006` |
| 日志路径变更影响前端日志流 | 中 | SSE 日志流当前使用 `GetInstanceLogFile`，需改为 `GetGameLogFilePath` |
| Config API 不受影响 | 低 | Config 文件位置未变（`instances/<name>/Config/`） |
| IsServerRunning 依赖端口扫描 | 中 | v2 没有此函数，需确认 stopServerInternal 如何判断运行状态 |

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

`backup/backup.go` 中的 `BackupInstanceWorld` 和 `RestoreInstanceWorld` 需要更新存档路径：

```go
// v1:
savePath := filepath.Join(ServerFilesDir, "ShooterGame/Saved", config.SaveDir)

// v2:
savePath := filepath.Join(InstancesDir, instanceName, "Save")
```

同样，`RestoreInstanceWorld` 中恢复文件的目标路径也需要同步更新。

---

# Part 2 — ARK 服务器实例 v2 镜像启动方式技术文档（原 `V2_MIRROR_STARTUP_ARCHITECTURE.md`）

# ARK 服务器实例 v2 镜像启动方式技术文档

## 一、概述

v2 启动方式的核心改进是将 v1 的**共享目录 + 全局 NTFS Junction** 替换为**独立镜像目录**方案。每个服务器实例在 `server-files-tmp-<instanceName>/` 下拥有独立的镜像目录，通过 NTFS Junction 和 symlink 链接到原始文件，消除了 v1 中同一时间只能启动一个实例的全局锁限制。

## 二、核心思想

### 2.1 目录结构

```
server-files/                              ← 原始服务器文件（只读）
├── ShooterGame/
│   ├── Binaries/Win64/                    ← 整个目录需完整复制到镜像（隔离启动期缓存）
│   │   ├── ArkAscendedServer.exe
│   │   └── AsaApiLoader.exe
│   ├── Content/                           ← 资源文件（可 symlink / junction）
│   └── Saved/
│       ├── Config/WindowsServer/          ← exception target → junction 到实例 Config
│       ├── Logs/                          ← exception target → junction 到实例 Logs
│       └── <SaveDir>/                     ← exception target → junction 到实例 Save

server-files-tmp-<instanceName>/           ← 每实例独立镜像
├── ShooterGame/
│   ├── Binaries/Win64/                    ← 真实目录，内部所有文件均为真实副本
│   │   ├── ArkAscendedServer.exe
│   │   └── AsaApiLoader.exe
│   ├── Content/ → (junction → server-files/ShooterGame/Content/)
│   └── Saved/
│       ├── Config/WindowsServer/ → (junction → instances/<name>/Config/)
│       ├── Logs/ → (junction → instances/<name>/Logs/)
│       └── <SaveDir>/ → (junction → instances/<name>/Save/)

instances/<name>/                          ← 每实例本地存储
├── instance_config.ini
├── Config/
│   ├── Game.ini
│   └── GameUserSettings.ini
├── Logs/
│   └── ShooterGame.log
└── Save/
    └── <MapName>/
        └── <MapName>.ark
```

### 2.2 文件处理策略

> **变更说明**：早期版本对「exe 文件」和「包含 exe 的目录」有单独的复制特判（`exeFiles` / `containsExeFiles`）。
> 由于两个 exe（`ArkAscendedServer.exe` / `AsaApiLoader.exe`）都位于 `Binaries/Win64` 内，而该目录现已**整体完整复制**，
> exe 特判被 `isUnderWin64` 判断完全覆盖，属冗余，已移除。现在的策略只按「是否位于 `Binaries/Win64` 内」区分。

**目录**：

| 目录类型 | 处理方式 | 原因 |
|----------|----------|------|
| **Exception target 目录**（Config/Logs/SaveDir） | NTFS Junction → 实例本地目录 | 游戏引擎需要 per-instance 读写 |
| **包含 exception 子目录的父目录** | 真实目录（不 junction） | 需要容纳下级 junction |
| **`Binaries/Win64` 目录及其所有子目录** | 真实目录（不 junction） | 需要容纳整体复制的文件与启动期缓存 |
| **Win64 的祖先目录**（`ShooterGame`、`ShooterGame/Binaries`） | 真实目录（不 junction） | 必须真实才能容纳隔离的真实 Win64 子目录；否则整体 junction 到源会让各实例共享同一份 Win64 |
| **其他普通目录** | NTFS Junction → 原始目录 | 节省磁盘空间，免复制 |

**文件**：

| 文件类型 | 处理方式 | 失败回退 | 原因 |
|----------|----------|----------|------|
| **`.log` 日志文件** | 跳过，不镜像 | — | 运行期日志会造成增量 diff 抖动 |
| **`Binaries/Win64` 内的所有文件**（exe / .dll / .pak / .ucas 等） | 真实文件复制（`fsutil.CopyFile`），**不 symlink** | 复制失败则报错 | 启动过程中会在 Win64 内生成缓存文件，symlink 会使缓存落回原目录导致镜像读不到缓存而无法启动，故整体复制隔离 |
| **其他普通文件** | 文件 symlink → 原始文件 | **自动回退到 `fsutil.CopyFile` 完整复制** | 无 symlink 权限时仍需保证文件可用 |

### 2.3 Exception Targets（例外目标）

启动时通过 `buildExceptionTargets` 构建例外映射表：

```go
func buildExceptionTargets(instanceName string, cfg *InstanceConfig) map[string]string {
    return map[string]string{
        "ShooterGame/Saved/Config/WindowsServer":  filepath.Join(InstancesDir, instanceName, "Config"),
        "ShooterGame/Saved/Logs":                  filepath.Join(InstancesDir, instanceName, "Logs"),
        "ShooterGame/Saved/" + cfg.SaveDir:        filepath.Join(InstancesDir, instanceName, "Save"),
    }
}
```

这三个路径是游戏引擎会读写的位置，需要指向各实例独立的存储空间。

## 三、Junction 创建机制

### 3.1 使用 Go 原生 os.Symlink

```go
// mirror.go
func createJunction(linkPath, targetPath string) error {
    absTarget, _ := filepath.Abs(targetPath)
    return os.Symlink(absTarget, linkPath)
}
```

**Go 1.21+ 在 Windows 上对目录目标的 `os.Symlink` 调用自动创建 NTFS Junction**（reparse point），无需管理员权限。

与 v1 使用 `cmd /c mklink /J` 的对比：

| 维度 | v1 (`cmd /c mklink /J`) | v2 (`os.Symlink`) |
|------|--------------------------|---------------------|
| 进程开销 | 启动 cmd.exe 子进程 | 无外部进程 |
| 权限 | 无需管理员，但有进程创建开销 | 无需管理员 |
| 安全性 | 命令注入风险 | 无注入风险 |
| 可靠性 | 依赖 cmd.exe 行为 | Go 标准库保证 |
| 错误处理 | 需解析 stdout/stderr | 直接返回 error |

### 3.2 Junction 与 Symlink 的区别

在 Windows 上，`os.Symlink` 根据目标类型表现不同：

- **目录目标** → 创建 NTFS Junction（reparse point），不需要管理员权限
- **文件目标** → 创建文件 symlink，**需要管理员权限或开启开发者模式**

### 3.3 文件 Symlink 的 Copy 回退机制

Windows 对文件 symlink 的权限要求比目录 junction 严格得多。`os.Symlink` 在 Windows 上的行为取决于目标类型和当前进程权限：

| 目标类型 | 权限要求 | `os.Symlink` 结果 |
|----------|----------|-------------------|
| **目录** | 无需管理员 | 成功创建 NTFS Junction（reparse point） |
| **文件** | 需要管理员 **或** 开启开发者模式 | 有权限时成功，无权限时失败 |

文件 symlink 在 Windows 上失败的常见场景：
1. 进程以普通用户身份运行，且系统未开启"开发者模式"
2. 企业域控策略禁止创建 symlink
3. 杀毒软件拦截 symlink 创建

**v2 的回退策略**：当 `os.Symlink` 创建文件 symlink 失败时，自动回退到 `copyFile` 将文件完整复制到镜像中。这确保了即使在无 symlink 权限的环境下，镜像目录也能正常工作。

```go
// mirror.go — createFileSymlink 带 copy 回退
func createFileSymlink(linkPath, targetPath string) error {
    absTarget, _ := filepath.Abs(targetPath)

    // 确保父目录存在
    parentDir := filepath.Dir(linkPath)
    os.MkdirAll(parentDir, 0755)

    // 尝试创建文件 symlink
    if err := os.Symlink(absTarget, linkPath); err != nil {
        // symlink 失败 → 回退到完整复制
        if IsElevated() {
            // 以管理员运行仍然失败 → 异常情况，记录警告
            logger.Warnf("Symlink failed even with admin, fallback copy: %s: %v", linkPath, err)
        } else {
            // 普通用户 → 预期行为，记录调试信息
            logger.Debugf("No admin, fallback copy: %s", linkPath)
        }
        return fsutil.CopyFile(targetPath, linkPath)  // 完整复制文件
    }

    // symlink 成功
    logger.Debugf("Created file symlink: %s -> %s", linkPath, absTarget)
    return nil
}
```

#### 回退决策流程图

```
createFileSymlink(linkPath, targetPath)
    │
    ├── os.Symlink(absTarget, linkPath)
    │     │
    │     ├── 成功 → 返回 nil（文件 symlink 已创建）
    │     │
    │     └── 失败 →
    │           ├── IsElevated() == true ?
    │           │     ├── 是 → 记录 WARN（管理员仍失败，异常情况）
    │           │     └── 否 → 记录 DEBUG（普通用户，预期行为）
    │           │
    │           └── fsutil.CopyFile(targetPath, linkPath)  ← 回退到完整复制
    │                 │
    │                 ├── 创建父目录
    │                 ├── os.Open(src) + os.Create(dst)
    │                 ├── io.Copy(dst, src)
    │                 └── os.Chmod(dst, srcInfo.Mode())  ← 保留文件权限
```

#### 哪些文件会被回退复制？

在镜像创建流程中，只有**位于 `Binaries/Win64` 之外的普通文件**会走 `createFileSymlink` 路径：

| 文件类型 | 处理方式 | 走 symlink+copy 回退？ |
|----------|----------|----------------------|
| `Binaries/Win64` 内的文件（含 exe / dll 等） | 直接 `fsutil.CopyFile`（整体复制） | ❌ 不走，直接复制 |
| Win64 外的普通文件（.pak, .ucas 等） | `createFileSymlink` → 失败时 copy | ✅ 是 |
| 父目录是 junction 的文件 | 跳过（通过 junction 已可访问） | ❌ 不走 |
| `.log` 日志文件 | 跳过，不镜像 | ❌ 不走 |

#### 回退复制的代价

当 symlink 回退到 copy 时，会产生以下影响：

| 维度 | Symlink（正常） | Copy 回退 |
|------|-----------------|-----------|
| 镜像占用空间 | 0（仅链接） | 与源文件相同大小 |
| 创建速度 | 即时（创建链接） | 取决于文件大小和磁盘速度 |
| 更新同步 | 无需更新（链接自动指向最新） | 增量同步时需 MD5 比对 + 重新复制 |
| 游戏运行时行为 | 透明读取原文件 | 读取本地副本，与源文件独立 |

**实际影响评估**：ASA 服务器的 `Content/` 目录包含大量 `.pak`、`.ucas`、`.utoc` 资源文件，总计可达 **20-40GB**。如果所有文件都回退到 copy，每个实例的磁盘占用将从近 0 增长到 20-40GB。

#### 开发者模式开启方法

为避免 copy 回退带来的磁盘空间问题，建议在运行 ASA Server Manager 的 Windows 系统上开启开发者模式：

```
Windows 设置 → 更新和安全 → 开发者选项 → 开发人员模式 → 开启
```

开启后，普通用户进程也可以创建文件 symlink，无需管理员权限。

#### IsElevated 检测

代码通过 `IsElevated()` 检测当前进程是否以管理员身份运行，仅用于决定日志级别：

```go
func IsElevated() bool {
    // 通过 Windows API 检查进程 token 是否包含管理员 SID
    var token windows.Token
    windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
    // 检查 BUILTIN\Administrators 组成员身份
    // 结果缓存在 sync.Once 中，只检测一次
}
```

注意：`IsElevated` **不决定是否回退**——无论是否管理员，symlink 失败都会回退到 copy。管理员检测仅影响日志级别（管理员失败记录 WARN，普通用户失败记录 DEBUG）。

## 四、镜像生命周期管理

### 4.1 全量创建 (`createInstanceMirror`)

首次启动实例时，遍历整个 `server-files/` 目录树，根据 §2.2 的处理策略创建 junction/symlink/real file：

```
1. filepath.Walk(server-files/)
   ├── 对每个目录调用 processDirectory()
   │     ├── 是 exception target → 创建 junction 到实例本地目录，跳过递归
   │     ├── 有 exception 子目录 → 创建真实目录，继续递归
   │     ├── 位于 Binaries/Win64 内（含子目录） → 创建真实目录，继续递归
   │     ├── 是 Win64 的祖先目录（ShooterGame/Binaries 等） → 创建真实目录，继续递归
   │     └── 其他 → 创建 junction 到原始目录，跳过递归
   │
   └── 对每个文件调用 processFile()
         ├── 是 .log 日志文件 → 跳过（不镜像）
         ├── 父目录是 junction → 跳过（通过 junction 已可访问）
         ├── 位于 Binaries/Win64 内 → 复制文件（fsutil.CopyFile，整体复制隔离缓存）
         └── 其他 → 创建文件 symlink（失败回退复制）

2. 补充缺失的 exception targets
   ├── 如果 instances/<name>/Config 不存在 → 创建目录 + junction
   ├── 如果 instances/<name>/Logs 不存在 → 创建目录 + junction
   └── 如果 instances/<name>/Save 不存在 → 创建目录 + junction
```

### 4.2 增量同步 (`syncMirrorEntries`)

当镜像已存在时，基于 diff 库计算源目录与镜像的差异，仅处理变更部分：

```
1. collectSourceEntries()  → 收集源目录所有条目（按 RelPath 排序）
2. collectMirrorEntries()  → 收集镜像所有条目（按 RelPath 排序）
3. diff.EditsFunc()        → 计算差异
     ├── Insert (源有、镜像无) → 创建新条目
     ├── Delete (镜像有、源无) → 安全删除
     └── Match (两者都有)     → reconcileEntry 校验
           ├── 真实文件（含 Binaries/Win64 内所有文件） → 逐文件 MD5 校验，不匹配则从原文件覆盖复制到镜像
           ├── Symlink → 检查目标是否正确 / 是否断裂，不正确则重建
           └── Junction → 通过 os.Lstat 检查，不正确则重建
4. 补充缺失的 exception targets
```

#### 删除同步（源目录删除文件 → 镜像同步删除）

当源 `server-files` 删除了某些文件/目录后，下次增量同步会命中 `diff.Delete`（源无、镜像有），调用 `removeMirrorEntry`（`mirror.go`）按条目类型**安全删除**：

| 镜像条目类型 | 删除方式 | 说明 |
|--------------|----------|------|
| junction / symlink | `os.Remove` | 只删链接本身，**不删链接目标** |
| 真实文件（`EntryTypeFile`，含 `Binaries/Win64` 复制文件） | `os.Remove` | 删除镜像内的真实副本 |
| 真实目录 | `os.RemoveAll` | 游戏运行时可能在其中写入，强制清除 |

各目录类型的删除表现：

- **`Binaries/Win64` 内**：源删除某文件 → `collectSourceEntries` 不再产出该条目 → 镜像内的真实副本在下次同步时被删除。
- **junction 目录（如 `Content/`）**：镜像只是一个 junction，不逐文件追踪，源删除通过 junction 天然透传，无需同步动作。
- **exception target（Config/Logs/Save）**：junction 指向实例本地目录，与源 `server-files` 无关，不受源删除影响。

**边界**：游戏运行期在镜像 Win64 内新生成、而源目录本就没有的缓存文件，属于「源无、镜像有」，会在下次同步时被 `diff.Delete` 清除——这是预期行为（缓存每次启动重新生成），不影响启动。删除同步仅发生在**镜像已存在的增量同步**路径；首次全量创建（`createInstanceMirror`）从空目录按源构建，不涉及「多余文件」删除。

### 4.3 清理 (`CleanupInstanceMirror`)

安全清理流程，仅移除链接，不删除链接目标：

```
1. Walk 镜像，收集所有条目 + 深度信息
2. 按深度降序排序（从最深层开始）
3. 第一轮：移除所有 junction 和 symlink（os.Remove 不跟随链接）
4. 第二轮：移除剩余真实文件，尝试删除空目录
5. 最后：移除镜像根目录（如果仍非空则用 RemoveAll）
```

## 五、与 v1 架构对比

### 5.1 全局锁消除

**v1 问题**：`setupInstanceConfig` 在共享的 `server-files/ShooterGame/Saved/Config/WindowsServer` 上创建 junction，同一时间只能有一个实例处于 `start_initialization`。状态机通过 `isAnyInstanceInitializingLocked()` 强制全局互斥。

**v2 解决**：每个实例拥有独立的镜像目录，不同实例的 junction 不相互影响。所有实例可以同时启动，`ForceStopServer` 也不需要等待其他实例释放 junction。

### 5.2 完整对比表

| 维度 | v1 (setupInstanceConfig) | v2 (mirror) |
|------|--------------------------|-------------|
| 全局锁 | 有 — 同一时间只能启动一个实例 | 无 — 所有实例可并行启动 |
| 修改共享目录 | 是 — 直接在 server-files 上创建 junction | 否 — 镜像在独立目录 |
| Backup/Restore | 需要 `WindowsServer.bak` | 不需要 |
| confReset 机制 | 需要 — 启动完成后释放 junction | 不需要 |
| 日志管理 | 全局映射文件 `.instance_log_mapping.json` | 每实例独立目录 `instances/<name>/Logs/` |
| exe 路径 | `server-files/.../ArkAscendedServer.exe` | `server-files-tmp-<name>/.../ArkAscendedServer.exe` |
| 工作目录 | `server-files/ShooterGame/Binaries/Win64` | `server-files-tmp-<name>/ShooterGame/Binaries/Win64` |
| Force stop | 需等 junction 释放 (WaitForNoInitializing) | 直接清理独立镜像 |
| 启动失败清理 | 移除 junction + 恢复备份 | CleanupInstanceMirror |
| 增量同步 | 无（每次全量处理） | 有（diff 库 + MD5 校验） |
| 存档路径 | `server-files/ShooterGame/Saved/<SaveDir>/` | `instances/<name>/Save/` |

### 5.3 启动并发度对比

```
v1 时间线：
  实例A启动 ████████████████████
  实例B启动                    ████████████████████   （需等 A 释放 junction）
  实例C启动                                          ████████████████████

v2 时间线：
  实例A启动 ████████████████████
  实例B启动 ████████████████████   （独立镜像，无需等待）
  实例C启动 ████████████████████   （独立镜像，无需等待）
```

## 六、磁盘空间分析

### 6.1 镜像空间占用

由于大部分目录和文件使用 NTFS Junction / symlink（仅创建链接，不复制数据），镜像实际占用的磁盘空间很小：

| 内容 | 处理方式 | 空间占用 |
|------|----------|----------|
| `Binaries/Win64` 目录（含 exe / dll 等全部文件） | 整体复制 | ~Win64 目录实际大小 × 实例数 |
| Exception target 目录 | Junction | 0（链接） |
| Content/ 等资源目录 | Junction | 0（链接） |
| 普通文件 | Symlink | 0（链接） |
| Config/Logs/Save | 真实文件（实例独立） | 取决于实际使用 |

**结论**：每个实例的额外磁盘开销主要是 `Binaries/Win64` 目录的整体副本（用于隔离启动期缓存），其他体量的 `Content/` 等资源目录仍为 junction/symlink，不占额外空间。

### 6.2 增量同步开销

首次创建镜像后，后续启动只需进行增量同步：
- 源/镜像文件列表对比：毫秒级
- MD5 校验：对 `Binaries/Win64` 目录内所有真实文件逐一校验，不一致则从原文件覆盖复制
- 仅在 server-files 更新（如游戏版本更新）后才有实际文件操作

## 七、容错机制

### 7.1 镜像创建失败

`createInstanceMirror` 在 `filepath.Walk` 失败或 exception junction 创建失败时，会自动调用 `CleanupInstanceMirror` 清理不完整的镜像。

### 7.2 增量同步失败

`syncMirrorEntries` 失败时，`SyncInstanceMirror` 会清理现有镜像并从头重建。

### 7.3 启动失败

`startServerInternal` 的 deferred 函数检查 `mirrorDir` 变量，如果镜像已创建但启动失败，自动调用 `CleanupInstanceMirror` 清理。

### 7.4 Force Stop

`ForceStopServer` 不需要等待 junction 释放（因为每个实例的 junction 是独立的），直接 kill 进程 + 清理镜像 + 写入 stopped 状态。

---

# Part 3 — 镜像去管理员化（真 NTFS junction）（原 `MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md` 第一部分）

# 第一部分：镜像去管理员化（真 NTFS junction）

## 1.1 现状与问题

`internal/mirror/mirror.go:462` 的 `createJunction` **名叫 junction，实现却是 `os.Symlink`**：

```go
func createJunction(linkPath, targetPath string) error {
	// ...
	if err := os.Symlink(absTarget, linkPath); err != nil {
		return fmt.Errorf("failed to create junction %s -> %s: %w", linkPath, absTarget, err)
	}
	// ...
}
```

在 Windows 上，`os.Symlink` 对目录目标创建的是**目录符号链接**，需要
`SeCreateSymbolicLinkPrivilege`（管理员，或开启开发者模式）。而真正的 **NTFS junction**
（`mklink /J` / `FSCTL_SET_REPARSE_POINT`）**普通用户就能创建**。

于是出现一个不对称：

| 条目类型 | 函数 | 失败时行为 |
|---|---|---|
| 文件 | `createFileSymlink`（`mirror.go:484`） | ✅ 回退 `fsutil.CopyFile`，功能正常 |
| **目录** | `createJunction`（`mirror.go:462`） | ❌ 直接返回 error |

目录侧的错误会一路上抛：
`processDirectory`（`mirror.go:374`）→ `filepath.Walk` 的回调 `return err`（`mirror.go:233-236`）
→ `createInstanceMirror` 失败 → `_ = CleanupInstanceMirror(instanceName)` 回滚整个镜像。

**结论**：非管理员且未开开发者模式时，实例**根本起不来**，
而不是 `main.go` 警告里说的「用文件复制模式起来」——那句话只对文件成立。

## 1.2 已实证的前提

在**非管理员** shell（Windows 11 / Go 1.27.0）下实测：

| 操作 | 结果 |
|---|---|
| `mklink /J`（真 NTFS junction） | ✅ 创建成功 |
| `mklink /D`（目录符号链接，等价于 `os.Symlink`） | ❌ 「您没有足够的权限执行此操作」 |

**换成真 junction 后，建目录链接不再需要任何特权。前提成立。**

配合业务侧的演进——可执行文件与 DLL 因启动期报错已改为**复制**而非链接
（`processFile` 里 `isUnderWin64(relPath)` → `fsutil.CopyFile`，`mirror.go:421-423`），
**现在需要建链接的实质上只剩目录**——管理员权限对镜像功能已不再必需。

## 1.3 ⚠️ 最大的连带风险：junction 的**识别**必须同步改

这是本次改造真正的难点，不是 `createJunction` 本身。

同一次实测（Go 1.27.0）：

| 类型 | `ModeSymlink` | `ModeIrregular` | `IsDir()` | `FILE_ATTRIBUTE_REPARSE_POINT` | `os.Readlink` |
|---|---|---|---|---|---|
| 真 junction（`mklink /J`） | **false** | **true** | false | true | ✅ 返回目标 |
| 目录 symlink（`os.Symlink`） | true | false | false | true | ✅ 返回目标 |
| 普通目录 | false | false | true | false | ✗ not a reparse point |

而 `isJunctionOrSymlink`（`mirror.go:430-437`）**只查 `ModeSymlink`**：

```go
// os.ModeSymlink 检测符号链接和 junction      ← 这句注释在 Go 1.23+ 上已经是错的
if fi.Mode()&os.ModeSymlink != 0 {
```

Go 1.23 起，Windows 上的 mount point（junction）不再被报告为 symlink，改报 `ModeIrregular`。
**所以只改 `createJunction` 而不改 `isJunctionOrSymlink`，同步逻辑会持续出错。**

> ⚠️ **本节初稿把后果写成了「会穿过 junction 把源文件删掉」，那是错的，已按实测更正。**
> 实测方式：把 `isJunctionOrSymlink` 改回只查 `ModeSymlink`，跑 `TestSyncDoesNotDeleteThroughJunctions`。

真实后果如下：

| 调用点 | 识别失效后的后果 |
|---|---|
| `collectMirrorEntries` | junction 被归成 `EntryTypeFile`（源侧意图是 `EntryTypeSymlink`），**每轮同步都判类型不匹配、把所有 junction 删掉重建** |
| `reconcileEntry` | 顺着上一条，对着一个目录调 `fsutil.CopyFile`，报 `Incorrect function`；同步返回错误后触发整个镜像重建 |
| `migrateExceptionJunctions`（`mirror.go:281`） | 该处另有 `!fi.IsDir()` 兜底（`os.Lstat` 对 junction 返回 `IsDir()==false`），**不会误迁移** |
| `CleanupInstanceMirror` / `removeMirrorEntry` | junction 落到 `os.Remove` 分支，**只删链接本身**，源目标安全 |

**为什么不会删到源**：`os.Lstat` 对 junction 返回 `IsDir()==false`，`filepath.Walk` 因此**不会递归进 junction**，
`migrateExceptionJunctions` 的 `!fi.IsDir()` 也拦得住。这层结构性保护与 `isJunctionOrSymlink` 无关，
所以识别失效表现为**性能与稳定性问题**，而不是数据丢失。

即便如此，**这两处仍应在同一个提交里一起改** —— 分开上线会留下一个每次同步都全量重建 junction、
且日志里刷满 `Incorrect function` 的中间版本。

**✅ 定案：用 `os.Readlink` 判定。**

| 方案 | 实现 | 结论 |
|---|---|---|
| **A** | `os.Readlink(path)` 成功即视为链接 | ✅ **采用**。跨平台、不依赖 `Mode` 语义在 Go 版本间的漂移、Linux 侧同样正确；开销与 `Lstat` 同量级 |
| B | `fi.Mode()&(os.ModeSymlink\|os.ModeIrregular) != 0` | ❌ `ModeIrregular` 语义偏宽，其他 reparse 点（如 OneDrive 占位文件、去重块）也会命中 |
| C | `windows.GetFileAttributes` 查 `FILE_ATTRIBUTE_REPARSE_POINT` | ❌ 判据最权威，但要为此拆 `_windows.go`/`_linux.go`，收益不抵成本 |

实测三种路径下 `os.Readlink` 的表现完全符合需要：真 junction ✅ 返回目标、目录 symlink ✅ 返回目标、
普通目录 ✗ 报 `not a reparse point`。实现：

```go
// isJunctionOrSymlink 判断路径是否是链接（NTFS junction / 符号链接）。
//
// 不用 os.ModeSymlink 判定：Go 1.23 起 Windows 的 mount point（junction）
// 报 ModeIrregular 而非 ModeSymlink，只查 ModeSymlink 会把真 junction 漏判成普通目录，
// 进而让增量同步穿过它删到源目录。Readlink 对两种链接都成功、对普通目录/文件都失败。
func isJunctionOrSymlink(path string) bool {
	_, err := os.Readlink(path)
	return err == nil
}
```

## 1.4 改造清单

| # | 位置 | 改动 | 备注 |
|---|---|---|---|
| 1 | `mirror.createJunction`（`mirror.go:462`） | 换成真 NTFS junction | 见下方实现选型 |
| 2 | `mirror.isJunctionOrSymlink`（`mirror.go:430`） | 改为能识别真 junction | **与 #1 同一提交**，见 §1.3 |
| 3 | `mirror.createFileSymlink`（`mirror.go:484`） | 简化为直接 `CopyFile`，并删掉 `reconcileEntry` 的 copy-fallback 特例（`mirror.go:1028-1045`） | 实测只影响 11 个文件 / 110 MB，见下 |
| 4 | `mirror.IsElevated()`（`mirror.go:95`）及 `elevated`/`elevatedErr`/`once` 全局变量 | 删除 | 唯一用途就是这套提权 |
| 5 | `main.go:273 ensureAdminElevation()` / `buildElevatedArgs()` / `quoteArg()` | 删除 | 连同 `main.go:194` 的调用 |
| 6 | `main.go` 的 `--no-admin` 标志与 `hasArgFlag` | 删除 | 提权没了，开关失去意义 |
| 7 | ~~`pkg/winproc.RunAsAdmin`~~ | ❌ **保留** | 实施时发现 `internal/certmgr/cli.go:67` 也在用它（`cert install --machine` 写 `LocalMachine\Root` 确实需要管理员）。本项计划有误，已放弃 |
| 8 | `certmgr.IsElevated()`（`store.go:210`） | **保留** | 写 `LocalMachine\Root` 证书存储仍需管理员，与镜像无关 |
| 9 | `CLAUDE.md` / `LINUX_COMPATIBILITY_PLAN.md` §5.6、§10.10 | 同步措辞 | 「uses NTFS junctions」到这时才名副其实 |

### #1 的实现：**✅ 定案用 `DeviceIoControl`**

| 方案 | 做法 | 结论 |
|---|---|---|
| **A** | `DeviceIoControl` + `FSCTL_SET_REPARSE_POINT` + `IO_REPARSE_TAG_MOUNT_POINT` | ✅ **采用**。纯 `golang.org/x/sys/windows`，无子进程、无 locale 依赖 |
| B | `cmd /c mklink /J` | ❌ 每条链接一个子进程；输出受系统语言影响（本次实测中文系统下报错信息即为 GBK 乱码）；参数需转义 |

步骤与注意点：

1. `os.MkdirAll(linkPath)` 建一个**空目录**（junction 必须建在空目录上）
2. `windows.CreateFile(linkPath, GENERIC_WRITE, ..., FILE_FLAG_BACKUP_SEMANTICS|FILE_FLAG_OPEN_REPARSE_POINT, ...)`
3. 构造 `REPARSE_DATA_BUFFER`：`ReparseTag = IO_REPARSE_TAG_MOUNT_POINT`，
   `SubstituteName` 用 NT 路径形式 **`\??\D:\path\to\target`**，`PrintName` 用显示路径 `D:\path\to\target`，
   两者均为 UTF-16 且各带 NUL 结尾
4. `windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, ...)`
5. **失败要 `os.Remove(linkPath)` 回滚**那个空目录，否则留下半成品会让后续同步误判

已核实 `golang.org/x/sys/windows@v0.47.0` 提供了所需常量：
`FSCTL_SET_REPARSE_POINT`(`types_windows.go:1983`)、`IO_REPARSE_TAG_MOUNT_POINT`、
`FILE_FLAG_OPEN_REPARSE_POINT`(`types_windows.go:136`)、`FILE_FLAG_BACKUP_SEMANTICS`。
但 `reparseDataBuffer` 结构体是**未导出**的（`types_windows.go:1933`），**需要自己定义一份**。

平台拆分：Linux 上 `os.Symlink` 本就免特权，所以 `createJunction` 要拆
`mirror_windows.go` / `mirror_linux.go` —— 与 `LINUX_COMPATIBILITY_PLAN.md` 的 P0 阶段合流，建议一起做。

### #3 文件符号链接：**不是「所有文件改成复制」**

先澄清一个容易误解的点：**镜像里绝大多数文件根本不参与「链接还是复制」的选择**。
`processFile`（`mirror.go:408`）把文件分成三类，只有第三类才走 `createFileSymlink`：

| 类 | 判据（`processFile` 分支） | 处理 | 在真实安装中的量 |
|---|---|---|---|
| ① **不进镜像** | 父目录已是 junction → `return nil`（`mirror.go:416`） | 不复制、不链接，**通过父目录的 junction 直接访问** | `Engine/`、`steamapps/`、`ShooterGame/Content/` 等，**约 11 GB，占绝大部分** |
| ② **完整复制** | `isUnderWin64(relPath)` → `fsutil.CopyFile`（`mirror.go:421`） | 真实副本，隔离启动期缓存 | `Win64/` 全部，**894 MB** —— exe 与 DLL 都在这里，**早已是复制，本次不变** |
| ③ **文件符号链接** | 其余 → `createFileSymlink`（`mirror.go:426`） | `os.Symlink`，失败回退 `CopyFile` | **只有 11 个文件，110 MB**（见下） |

在 `E:\asa_server_data\server-files`（12 GB，实测）上枚举第③类的全部成员 ——
它们全部落在 `server-files/` 根目录，`ShooterGame/`、`ShooterGame/Binaries/`、
`ShooterGame/Saved/`、`ShooterGame/Saved/Config/` 这几个「真实目录」里**一个散落文件都没有**：

```
  48.0 MB  Manifest_UFSFiles_Win64.txt
  23.2 MB  steamclient64.dll
  19.7 MB  steamclient.dll
  12.4 MB  steamwebrtc64.dll
   4.5 MB  steamwebrtc.dll
   0.6 MB  vstdlib_s.dll      0.5 MB  vstdlib_s64.dll
   0.4 MB  tier0_s64.dll      0.3 MB  tier0_s.dll
   0.0 MB  Manifest_DebugFiles_Win64.txt / Manifest_NonUFSFiles_Win64.txt
  ────────────────────────────────────────────
  合计 11 个文件 / 110 MB
```

**所以「文件都不链接了吗、全部使用复制吗」的答案是：**
① 类（约 11 GB）本来就既不链接也不复制，走 junction 访问，**不受影响**；
② 类（894 MB）本来就是复制，**不受影响**；
真正被这项改动波及的，**只有第 ③ 类这 11 个文件、110 MB**。

#### 为什么这 11 个也要改成复制

**因为 Windows 上文件符号链接同样需要 `SeCreateSymbolicLinkPrivilege`。**
去掉提权逻辑（#4–#7）之后，`os.Symlink` 对这 11 个文件**必然失败**，
每次建镜像都会白跑 11 次注定失败的系统调用、写 11 条 debug 日志，
最后仍旧回退到 `fsutil.CopyFile`。留着它就是一段**永远走不通的死代码**。

#### 代价与收益

| | 数值 |
|---|---|
| 每实例镜像增加 | **+110 MB**，在现有 894 MB（Win64 复制）基础上约 **+12%** |
| 5 个实例合计增加 | 约 550 MB |
| 增量同步增加 | 这 110 MB 的 MD5 计算（`reconcileEntry`，`mirror.go:1053`） |

> 上述代价**非管理员用户今天就已经在付**——他们本来就走 CopyFile 回退。
> 这项改动只是让管理员用户与之对齐，换来两种权限下行为完全一致。

附带收益：`reconcileEntry`（`mirror.go:1028-1045`）里那段
「source=Symlink 但 mirror=File，这是无权限时 fallback 到 copyFile 的合法结果」的特例分支
**可以整段删掉** —— 源侧意图类型不再有 `EntryTypeSymlink` 的文件，两边永远同为 `EntryTypeFile`，
走统一的 MD5 比对路径。这段特例正是当年为了兼容「有时链接、有时复制」而写的，
一旦行为统一它就没有存在理由了。

#### 被否的替代方案：NTFS 硬链接

`CreateHardLinkW` 同样免特权、且**不额外占空间**，镜像目录
（`{BaseDir}/server-files-tmp-<name>`，`mirror.go:117`）与 `server-files` 同在 `BaseDir` 下、
必然同卷，硬链接的技术前提是成立的。但仍然否掉：

1. **共享同一份内容**：任何进程写镜像侧的文件都会直接污染 `server-files` 源。
   这 11 个文件虽然运行期只读，但这是一条没有防护的路径。
2. **省下的空间会随更新流失**：SteamCMD 更新若以「删除+重建」方式替换源文件，
   硬链接会保留旧内容；`reconcileEntry` 的 MD5 比对发现不一致后会 `CopyFile` 覆盖，
   于是硬链接退化成普通副本 —— 空间收益不可持续。
3. 为 110 MB 引入一套额外机制与上述风险，不划算。

## 1.5 验收

1. **在普通（非管理员）账户下**：创建实例 → 启动 → 玩家可连入 → 停止，全流程通过。
2. 用 `fsutil reparsepoint query <镜像目录>` 确认建出来的是 **mount point**（`0xA0000003`）而非 symlink。
3. **回归重点（§1.3 的兜底）**：多实例并发同步 + 服务端更新后的增量同步，
   跑完确认 `server-files/` 下**没有任何文件被误删**。建议改造前先对源目录做一次全量快照用于比对。
4. **兼容旧镜像**：升级前用旧版本创建的镜像里是 `os.Symlink`，新代码必须能正确识别与清理，
   两种形态在过渡期并存。
5. 启动过程无 UAC 弹窗、无提权重启。

## 1.6 收益

- 去掉 Windows 提权重启：启动更快、无 UAC 弹窗、可在受限账户与 CI 环境下运行。
- 安装器与首次引导对管理员的依赖减半（只剩「注册服务」和「装本地 CA」两项）。
- 与 `LINUX_COMPATIBILITY_PLAN.md` §5.6 合流：**两个平台都免特权建链接**，行为终于一致。

---

# Part 4 — asaserverv2 → asaserver 迁移变更日志（原 `V2_MIGRATION_CHANGELOG.md`）

# asaserverv2 → asaserver 迁移变更日志

## 一、背景

`asaserverv2` 是一个实验性包，实现了基于 **NTFS Junction 镜像** 的服务器启动方式（mirror-based startup），解决了 v1 中共享 junction 目录导致的全局互斥问题。本次迁移将 v2 的镜像启动逻辑正式合入 `asaserver` 包，替代 v1 的 `setupInstanceConfig` + `confReset` 方式，并删除整个 `asaserverv2` 包。

**迁移效果**：
- 多实例可并行启动，消除全局锁
- 每个实例拥有独立的镜像目录 `server-files-tmp-<name>/`
- 简化日志管理，去掉全局映射文件
- 统一代码库，消除 v1/v2 两套并存的维护负担

---

## 二、变更统计

```
22 files changed, 208 insertions(+), 2773 deletions(-)
```

| 类别 | 文件数 | 新增行 | 删除行 |
|------|--------|--------|--------|
| 后端核心（asaserver） | 5 | ~30 | ~570 |
| 后端调用方 | 6 | ~30 | ~80 |
| asaserverv2 删除 | 5 | 0 | ~2090 |
| 前端 | 4 | ~10 | ~55 |
| 文档 | 2 | ~130 | ~30 |

---

## 三、后端变更

### 3.1 asaserver/server.go — 核心启动逻辑替换

**删除的 v1 遗留代码**（~400 行）：

| 删除项 | 说明 |
|--------|------|
| `logMappingMutex` / `instanceLogMapping` | 日志映射全局变量与读写锁 |
| `sync` / `fsnotify` import | 日志映射相关的依赖 |
| `InitializeLogMapping()` | 启动时从 JSON 文件加载日志映射 |
| `PersistLogMapping()` | 每次启动后持久化日志映射到 JSON |
| `RemoveInstanceLogMapping()` | 停止时从映射中移除实例 |
| `GetGameLogFileName()` | 通过 fsnotify 监控日志目录，动态发现日志文件名 |
| `setupInstanceConfig()` | v1 方式：在 server-files 上创建 NTFS Junction 指向实例 Config |
| `removeNotRunningServerLogMapper()` | 清理非运行实例的日志映射 |

**核心逻辑变更**：

| 变更点 | v1 (旧) | v2 (新) |
|--------|---------|---------|
| 启动前目录准备 | `setupInstanceConfig()` 在 server-files 上创建 junction | `SyncInstanceMirror()` 创建独立镜像目录 |
| exe 路径 | `filepath.Join(ServerFilesDir, ...)` | `filepath.Join(mirrorDir, ...)` |
| 进程工作目录 | 未设置（默认当前目录） | `c.Dir = exeWorkDir`（镜像内 exe 目录）|
| 启动后回调 | `confReset()` 释放 junction | 不调用 `confReset`（镜像独立，无需恢复）|
| 日志路径获取 | `GetInstanceLogFile()`（查映射）| `GetGameLogFilePath()`（直接路径）|
| 日志映射持久化 | 启动后 `PersistLogMapping()`，停止后 `RemoveInstanceLogMapping()` | 已删除（不再需要）|
| 启动失败处理 | 仅写 `StatusStartFailed` | 同时通过 `initFailed` channel 通知调用方 |
| `WaitServerCompleted` | 仅在非重启时等待 | 重启回调中也发送完成信号 |

**ForceStopServer 变更**：

| 变更点 | v1 (旧) | v2 (新) |
|--------|---------|---------|
| 全局互斥 | `WaitForNoInitializing(2min)` 等待全局就绪 | 无等待（镜像独立）|
| 清理操作 | `RemoveInstanceLogMapping()` | `CleanupInstanceMirror()` 清理镜像目录 |

**RestartServer 变更**：

| 变更点 | v1 (旧) | v2 (新) |
|--------|---------|---------|
| Stop 后等待 | `time.Sleep(10s)` 等待 junction 释放 | 删除（镜像独立，无需等待）|

### 3.2 asaserver/state_manager.go — 全局互斥规则移除

**删除的函数/变量**（~63 行）：

| 删除项 | 说明 |
|--------|------|
| `isAnyInstanceInitializingLocked()` | 在持锁状态下检查是否有实例处于初始化 |
| `getInitializingInstanceLocked()` | 获取正在初始化的实例名 |
| `IsAnyInstanceInitializing()` | 公开的初始化状态检查 |
| `WaitForNoInitializing()` | 等待所有实例完成初始化（带超时）|
| `isOperationAllowed` 规则 1 | 全局互斥检查：有实例在初始化时阻止所有操作 |

**`isOperationAllowed` 变更**：

```go
// 改前：两条规则
// 规则 1: 如果有实例在 start_initialization，不允许操作（全局互斥）
// 规则 2: 检查目标实例自身状态

// 改后：仅保留规则 2
// 仅检查目标实例自身状态，不再检查其他实例
```

### 3.3 asaserver/config.go — 日志映射持久化删除

**删除项**（~52 行）：

| 删除项 | 说明 |
|--------|------|
| `encoding/json` import | JSON 序列化依赖 |
| `LogMappingFile` 变量 | 日志映射文件路径 `log_mapping.json` |
| `LogMapping` struct | 日志映射数据结构 |
| `LoadLogMappingFromFile()` | 从 JSON 文件加载映射 |
| `SaveLogMappingToFile()` | 将映射持久化到 JSON 文件 |

### 3.4 asaserver/common.go — 存档路径与辅助函数

| 变更 | 说明 |
|------|------|
| `SaveWorldSafely()` | 存档路径从 `server-files` 改为 `instances/<name>/Save/` |
| `savePathReplacement()` | 从 asaserverv2 迁入，地图名标准化（如 `BobsMissions_WP` → `BobsMissions`）|

### 3.5 asaserver/config_test.go — 测试文件更新

移除 `init()` 函数中的 `InitializeLogMapping()` 调用（该函数已删除）。

### 3.6 asaserverv2/ — 整包删除

删除整个 `asaserverv2` 包（5 个文件，~2090 行）：

| 文件 | 行数 | 说明 |
|------|------|------|
| `common.go` | 217 | 存档安全保存、辅助函数 |
| `force_stop.go` | 30 | 强制停止（无全局等待）|
| `mirror.go` | 900 | 镜像管理核心逻辑 |
| `server.go` | 620 | 启动/停止/重启/配置同步 |
| `server_test.go` | 351 | 单元测试 |

### 3.7 调用方文件更新

| 文件 | 变更说明 |
|------|---------|
| `main.go` | 移除 `asaserverv2` import；移除 `asaserver.InitializeLogMapping()` 调用 |
| `webapi/api.go` | `asaserverv2.SyncInstanceMirror` → `asaserver.SyncInstanceMirror`；`asaserverv2.CleanupInstanceMirror` → `asaserver.CleanupInstanceMirror` |
| `webapi/task.go` | 删除死代码 `isTransitionalState` 函数（无调用方）|
| `backup/backup.go` | 移除 `asaserverv2` import；使用 `asaserver` 包的镜像路径 |
| `batchmanage/manager.go` | 移除 `asaserverv2` import 及相关引用 |
| `parseserver/save_monitor.go` | `asaserverv2` → `asaserver` 引用更新 |

---

## 四、前端变更

### 4.1 核心问题

后端已移除全局互斥（`isOperationAllowed` 规则 1），但前端仍保留了 `isAnyInstanceInitializing()` 逻辑——当任一实例处于 `start_initialization` 时，禁用**所有**实例的启动/停止/重启按钮。同时，组件各自独立计算 `globalInitBlocked`，违反封装原则。

### 4.2 app/src/composables/useInstanceState.js — 唯一入口

所有按钮 disabled/loading 判断逻辑集中在此文件，组件只做纯调用。

**变更**：

| 变更点 | 改前 | 改后 |
|--------|------|------|
| `canStart(status, globalBlocked)` | 接受外部传入的全局阻塞标志 | `canStart(status)` 仅由实例自身 status 决定 |
| `canStop(status, globalBlocked)` | 同上 | `canStop(status)` |
| `canRestart(status, globalBlocked)` | 同上 | `canRestart(status)` |
| `useInstanceState` composable | 内部计算 `globalBlocked` computed | 移除 `globalBlocked`，不再暴露 |
| import | `import {serverStore, isAnyInstanceInitializing}` | `import {serverStore}` |
| `computed` import | 需要 `computed` 创建 `globalBlocked` | 不再需要 |

### 4.3 app/src/store/serverStore.js — 删除死代码

删除 `isAnyInstanceInitializing()` 函数（8 行），该函数在后端移除全局互斥后已无对应逻辑。

### 4.4 app/src/views/ServerManager.vue — 纯调用

| 变更点 | 改前 | 改后 |
|--------|------|------|
| import | `import {initServer, serverStore, addRestartPending, isAnyInstanceInitializing}` | `import {initServer, serverStore, addRestartPending}` |
| `computed` import | 需要 `computed` | 不再需要 |
| `globalInitBlocked` | `const globalInitBlocked = computed(() => isAnyInstanceInitializing())` | 已删除 |
| 启动按钮 | `:disabled="!canStart(instance.status, globalInitBlocked)"` | `:disabled="!canStart(instance.status)"` |
| 停止按钮 | `:disabled="!canStop(instance.status, globalInitBlocked)"` | `:disabled="!canStop(instance.status)"` |
| 重启按钮 | `:disabled="!canRestart(instance.status, globalInitBlocked)"` | `:disabled="!canRestart(instance.status)"` |

### 4.5 app/src/views/InstanceDetail.vue — 纯调用

| 变更点 | 改前 | 改后 |
|--------|------|------|
| import | `import {getInstanceStatus, initServer, addRestartPending, isAnyInstanceInitializing}` | `import {getInstanceStatus, initServer, addRestartPending}` |
| `globalInitBlocked` | `const globalInitBlocked = computed(() => isAnyInstanceInitializing())` | 已删除 |
| 启动按钮 | `:disabled="!canStart(instanceStatus, globalInitBlocked)"` | `:disabled="!canStart(instanceStatus)"` |
| 停止按钮 | `:disabled="!canStop(instanceStatus, globalInitBlocked)"` | `:disabled="!canStop(instanceStatus)"` |
| 重启按钮 | `:disabled="!canRestart(instanceStatus, globalInitBlocked)"` | `:disabled="!canRestart(instanceStatus)"` |
| RCON 终端按钮 | `:disabled="!canStop(instanceStatus, globalInitBlocked)"` | `:disabled="!canStop(instanceStatus)"` |

---

## 五、文档变更

### 5.1 docs/STATE_CONTROL.md

更新 7 处描述以匹配镜像迁移后的行为：

| 变更位置 | 改前 | 改后 |
|---------|------|------|
| 状态枚举 `start_initialization` | "正在创建 junction / 同步镜像目录" | "正在同步镜像目录" |
| 状态枚举 `start_initialization_successful` | "junction / 镜像已释放，等待进程就绪" | "镜像已建立 + 进程已启动，等待进程就绪" |
| 流转图 | "junction/镜像完成" | "镜像同步完成" |
| 批量操作表格 | "任意实例在 start_initialization → 全局阻塞" | "目标实例在 start_initialization → 跳过该实例" |
| 全局互斥规则章节 | 描述全局互斥规则 | 替换为「并行启动能力」说明 |
| ForceStop 对比表 | "旧代码等待，v2 不等待" | "镜像方式无需等待" |
| 状态分类速查 | 中间态说明 | 补充"镜像方式下多实例可并行启动" |

---

## 六、运行时行为变更

### 6.1 目录结构

```
{BaseDir}/
├── server-files/                            ← 原始服务器文件（只读）
├── server-files-tmp-<instanceName>/         ← 每实例独立镜像（新增）
│   └── ShooterGame/
│       ├── Binaries/Win64/
│       │   ├── ArkAscendedServer.exe        ← 真实文件副本
│       │   └── AsaApiLoader.exe
│       ├── Content/ → (junction → server-files)
│       └── Saved/
│           ├── Config/ → (junction → instances/<name>/Config/)
│           ├── Logs/ → (junction → instances/<name>/Logs/)
│           └── <SaveDir>/ → (junction → instances/<name>/Save/)
├── instances/<instanceName>/
│   ├── instance_config.ini
│   ├── Config/
│   └── server.log
```

### 6.2 启动流程对比

```
v1: setupInstanceConfig → 创建 junction → 启动 exe → confReset 释放 junction → 启动完成
    ↓ 问题：同一时间只能有一个实例在 start_initialization（全局互斥）

v2: SyncInstanceMirror → 同步镜像目录 → 从镜像启动 exe → 镜像独立，无需释放 → 启动完成
    ↓ 优势：多实例可并行启动，互不阻塞
```

### 6.3 isOperationAllowed 规则变更

```
v1:
  规则 1: 任意实例在 start_initialization → 拒绝所有操作（全局互斥）
  规则 2: 目标实例自身状态检查

v2:
  仅规则 2: 目标实例自身状态检查（全局互斥已移除）
```

---

## 七、已删除的 API / 函数清单

### 后端（Go）

| 函数 | 所在文件 | 删除原因 |
|------|---------|---------|
| `InitializeLogMapping()` | asaserver/server.go | 日志映射系统废弃 |
| `PersistLogMapping()` | asaserver/server.go | 同上 |
| `RemoveInstanceLogMapping()` | asaserver/server.go | 同上 |
| `GetGameLogFileName()` | asaserver/server.go | 同上 |
| `setupInstanceConfig()` | asaserver/server.go | 被 `SyncInstanceMirror` 替代 |
| `removeNotRunningServerLogMapper()` | asaserver/server.go | 日志映射系统废弃 |
| `isAnyInstanceInitializingLocked()` | asaserver/state_manager.go | 全局互斥移除 |
| `getInitializingInstanceLocked()` | asaserver/state_manager.go | 同上 |
| `IsAnyInstanceInitializing()` | asaserver/state_manager.go | 同上 |
| `WaitForNoInitializing()` | asaserver/state_manager.go | 同上 |
| `LoadLogMappingFromFile()` | asaserver/config.go | 日志映射系统废弃 |
| `SaveLogMappingToFile()` | asaserver/config.go | 同上 |
| `isTransitionalState()` | webapi/task.go | 死代码，无调用方 |

### 前端（JavaScript / Vue）

| 函数/变量 | 所在文件 | 删除原因 |
|-----------|---------|---------|
| `isAnyInstanceInitializing()` | app/src/store/serverStore.js | 后端已无全局互斥 |
| `globalInitBlocked` (computed) | ServerManager.vue / InstanceDetail.vue | 组件不应做独立判断 |
| `globalBlocked` (参数) | useInstanceState.js | 逻辑集中化，不再接受外部传入 |

### 运行时文件

| 文件 | 说明 |
|------|------|
| `log_mapping.json` | 日志映射持久化文件（不再创建）|

---

## 八、验证清单

- [x] `go build ./...` — 后端编译通过
- [x] `go vet ./...` — 后端静态检查通过
- [x] `npm run build` — 前端编译通过
- [x] 全局搜索 `isAnyInstanceInitializing` — 0 残留
- [x] 全局搜索 `globalInitBlocked` / `globalBlocked` — 0 残留
- [x] `asaserverv2/` 目录已完全删除
- [x] STATE_CONTROL.md 文档与代码行为一致

---

# Part 5 — 实施顺序与工作量（原 `MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md` 第三部分）

# 第三部分：实施顺序与工作量

| 工作项 | 估算 | 风险 | 说明 |
|---|---|---|---|
| 第二部分（移除 WebAuthn） | 1.5–2 天 | **低** | 基本是纯删除；最碎的是 `Profile.vue` 的 32 处 |
| 第一部分（镜像去管理员化） | 2–3 天 | **中高** | §1.3 的识别改造与回归验证占大头，涉及**源目录数据安全** |

**建议先做第二部分**：纯删除、风险低、能快速把 CI 和验收流程跑通，
再带着这套验收习惯去做第一部分。

**例外**：如果 `LINUX_COMPATIBILITY_PLAN.md` 的安装器 / 首次引导（§10）是当前主线，
那第一部分应当优先——它直接决定引导程序对管理员权限的依赖程度（§1.6），
早做能少一次返工。

## 与其他计划的交叉

- 第一部分的 `createJunction` 平台拆分，与 `LINUX_COMPATIBILITY_PLAN.md` **P0 阶段**是同一块工作面，建议合并。
- 第一部分完成后，`LINUX_COMPATIBILITY_PLAN.md` §5.6 里
  「Linux 上 symlink 免特权、比 Windows 更省事」的论述可以简化为「两平台都免特权」。
- 第一部分完成后，§10.7.5.1「引导拒绝提权即退出」的理由随之收窄——
  引导仍需管理员（注册服务、装 CA），但**镜像不再需要**，提示文案要相应修改。

### ⚠️ 第一部分给 Linux 兼容留下的一个新阻断点

`createJunction` 移进 `internal/mirror/junction_windows.go`（`//go:build windows`）之后，
**无构建约束的 `mirror.go` 里有 6 处调用它**（`mirror.go:233,300,351,376,691,887,900`），
于是 `internal/mirror` 现在在 `GOOS=linux` 下**根本编译不过**。

这不是缺陷，是把工作从「运行时不对」前移成了「编译期报错」—— 后者好得多。
补救就是 8 行：`junction_linux.go` 里用 `os.Symlink` 实现同名函数，
语义与 Windows 侧对齐（**绝对路径 target**、**已存在时报错不覆盖**）。
`LINUX_COMPATIBILITY_PLAN.md` §5.6 已按此重写，列进 P0。

同一次改造里有两项对 Linux **纯粹是白赚的**，值得记下来免得将来重复讨论：

- `isJunctionOrSymlink` 改用 `os.Readlink`（§1.3 方案 A）——
  它在 Linux 上对 symlink 同样正确，**这一处不需要拆平台文件**。
  当初选它是为了避开 `Mode` 语义在 Go 版本间的漂移，跨平台正确性是顺带拿到的。
- `createFileSymlink` 删除、11 个根目录文件统一走 `CopyFile` ——
  Linux 上其实可以恢复 symlink（免特权、永不失败、省 110 MB/实例），
  但**不要这么做**：`reconcileEntry` 里那段「source=Symlink 但 mirror=File」的特例分支
  已经整段删了，为一个平台把它加回来，等于让两平台的同步语义分叉。理由见 `LINUX_COMPATIBILITY_PLAN.md` §5.6。

**第二部分（移除 WebAuthn）对 Linux 兼容零影响** —— 删掉的 `go-webauthn` / `go-tpm` /
`fxamacker/cbor` / `x448/float16` 全是纯 Go，`internal/auth` 本来就在跨平台清单里。

---

# 附录 Y：文件路径对照（2026-09-29）

| 文档中的路径 | 实际路径 / 现状（核对于 2026-09-29） |
|---|---|
| `asaserverv2/` 包 | **已删除**（其镜像启动逻辑先合入 `asaserver`，随后随包拆分迁入 `internal/`；全仓已无 `asaserverv2` 痕迹） |
| `asaserver/` 顶层包 | **已不存在**。先后经 `docs/PACKAGE_RESTRUCTURE_PLAN.md`（神包拆分）与 `docs/INTERNAL_LAYOUT_MIGRATION.md`（全部 Go 包收敛进 `internal/`）两次重构：镜像管理 → `internal/mirror/`；启动与生命周期 → `internal/instance/`；配置与目录变量 → `internal/config/`；状态机 → `internal/state/`；安装器 → `internal/installer/`。**仓库中并没有 `internal/asaserver` 这一目录** |
| `internal/mirror/junction_windows.go`、`internal/mirror/junction_linux.go` | 存在（与「第一部分」改造后的描述一致；`createJunction` 已按 `GOOS` 拆分在这两个文件里，分别为 126 行 / 34 行） |
| `internal/mirror/mirror.go` | 存在（1209 行；`SyncInstanceMirror`、`CleanupInstanceMirror`、`isJunctionOrSymlink` 均在此文件） |
| `asaserver/mirror.go`（本文多处引用） | 现为 `internal/mirror/mirror.go` |
| `asaserver/server.go`（`startServerInternal` / `StopServer` / `RestartServer` / `ForceStopServer`） | 现为 `internal/instance/server.go` |
| `asaserver/common.go`、`asaserver/state_manager.go`、`asaserver/config.go`、`asaserver/force_stop.go` | 均不存在；已分别演化为 `internal/instance/`、`internal/state/`、`internal/config/` 下的文件（`force_stop.go` 的内容已并入 `internal/instance/`） |
| `webapi/api.go`（本文引用 `api.go:902`、`api.go:1006`） | **文件已不存在**；`webapi` 已拆为 `internal/webapi/*` 子包。日志流调用 `GetGameLogFilePath` 现位于 `internal/webapi/logapi/logapi.go`；**原文中的行号已失效** |
| `backup/backup.go` | 现为 `internal/backup/backup.go` |
| `parseserver/save_monitor.go` | 现为 `internal/parseserver/save_monitor.go` |
| `batchmanage/manager.go` | 现为 `internal/batchmanage/manager.go` |
| `pkg/winproc`（「第一部分」#7 提到 `RunAsAdmin`） | 现为 `pkg/procx/`（`RunAsAdmin` 在 `pkg/procx/procx_windows.go` 与 `pkg/procx/procx_linux.go`） |
| `mirror.IsElevated()`（「第一部分」#4 要求删除） | 已删除（全仓 `IsElevated` 仅剩 `internal/certmgr/store_windows.go` / `store_linux.go`，服务于本地 CA，与镜像无关） |
| `main.go` | 仍在仓库根目录（未随包迁移进 `internal/`） |
| `app/src/...` 前端路径（如 `app/src/views/Profile.vue`、`app/src/store/serverStore.js`、`app/src/composables/useInstanceState.js`） | 仍在 `app/` 下，路径有效 |

---

# 附录 Z：合并与同步记录（2026-09-29）

本文件由 `docs/V2_MIGRATION_PLAN.md`、`docs/V2_MIRROR_STARTUP_ARCHITECTURE.md`、`docs/V2_MIGRATION_CHANGELOG.md` 三份全文，与 `docs/MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md` 的第一部分、第三部分，于 2026-09-29 逐字物理合并而成（方案甲）；四个源文件正文未作删减或改写；指向旧文档名的引用已改指本文。
