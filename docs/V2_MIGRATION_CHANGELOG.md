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
