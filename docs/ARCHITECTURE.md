# 系统架构

ASA Server Manager 系统架构文档。

---

## 整体架构

```
┌─────────────────────────────────────────────────────────┐
│                    用户交互层                              │
├──────────┬──────────┬──────────────┬─────────────────────┤
│  GUI     │  CLI     │  HTTP API    │  Windows Service    │
│  (Fyne)  │ (urfave) │  (Gin+SPA)   │  (kardianos)        │
└────┬─────┴────┬─────┴──────┬───────┴──────────┬──────────┘
     │          │            │                  │
     │          │            │                  │
     ▼          ▼            ▼                  ▼
┌─────────────────────────────────────────────────────────┐
│                    webapi 层                              │
│  APIServer · 路由 · SSE TaskBroadcaster · WebSocket      │
└────────────────────────┬────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────┐
│                   asaserver 核心层                        │
├──────────┬──────────┬──────────┬────────────────────────┤
│ server.go│ config.go│common.go │ state_manager.go       │
│ 启动/停止 │ 配置管理  │ 日志/进程 │ BadgerDB 状态持久化     │
├──────────┴──────────┴──────────┴────────────────────────┤
│ asaserverv2（重构中）: mirror · force_stop               │
└────┬─────┴────┬─────┴────┬─────┴──────────┬─────────────┘
     │          │          │                │
     ▼          ▼          ▼                ▼
┌────────┐ ┌────────┐ ┌──────────┐ ┌──────────────┐
│win32api│ │common  │ │processjob│ │database_file │
│Win API │ │WMI/DNS │ │Job Object│ │ BadgerDB     │
└────────┘ └────────┘ └──────────┘ └──────────────┘

┌─────────────────────────────────────────────────────────┐
│                   辅助服务层                              │
├──────────┬──────────────┬───────────────────────────────┤
│frpmanage │syncthingmanage│ backup · installer · logger  │
│FRP 反代   │Syncthing 同步 │ 备份 · SteamCMD · 日志       │
├──────────┴──────────────┼───────────────────────────────┤
│      parseserver        │  存档解析（go-arkparser）       │
└─────────────────────────┴───────────────────────────────┘
```

---

## 包职责

### 入口与交互

| 包 | 职责 | 关键文件 |
|---|---|---|
| `main` | 程序入口，CLI 命令定义，Windows 服务检测，GUI/CLI/API 模式选择 | `main.go` |
| `webapi` | HTTP API 服务器（Gin），路由注册，SSE 流式推送，WebSocket 事件广播 | `actions.go`, `api.go`, `ws.go`, `task.go`, `broadcast.go` |
| `gui` | Fyne 桌面 GUI，系统托盘，服务管理，日志查看器 | 包内多个文件 |
| `winservice` | Windows 服务安装/卸载/启动/停止，使用 `kardianos/service` | 包内文件 |
| `actions` | CLI 命令处理器（如 `update` 命令） | 包内文件 |

### 核心逻辑

| 包 | 职责 | 关键文件 |
|---|---|---|
| `asaserver` | **核心包** — 实例生命周期管理、配置读写、RCON 通信、SteamCMD 安装、状态管理 | `server.go`, `config.go`, `common.go`, `state_manager.go`, `installer.go` |
| `asaserverv2` | 核心包 v2（重构中） — 实例管理新实现，含 mirror、force_stop 等 | `server.go`, `common.go`, `force_stop.go`, `mirror.go` |
| `backup` | tar+zstd 备份/恢复，函数选项模式（`WithRestoreWorldfile()`, `WithRestoreInstanceConfig()` 等） | 包内文件 |

### 系统集成

| 包 | 职责 |
|---|---|
| `win32api` | Windows API 互操作（user32/kernel32），进程检查 |
| `common` | 共享工具 — DNS 解析、WMI 查询 |
| `processjob` | Windows Job Object，`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` 确保进程树清理 |
| `serverinfo` | 系统指标采集（gopsutil） — CPU、内存、进程信息 |

### 辅助服务

| 包 | 职责 |
|---|---|
| `frpmanage` | FRP 反向代理管理 — 内嵌 `frpc.exe`，MD5 校验避免重复提取，子进程生命周期管理 |
| `syncthingmanage` | Syncthing 文件同步管理 — 内嵌 `syncthing.exe`，使用 Job Object 管理进程树 |
| `githubreleases` | GitHub Releases API 客户端，带下载进度回调 |
| `parseserver` | ARK 存档解析（基于 go-arkparser），save_monitor 实时监控 |
| `logger` | Zap + lumberjack 结构化日志，带文件轮转 |

### 前端

| 包 | 职责 |
|---|---|
| `app` (嵌入) | Vue.js SPA，通过 `//go:embed dist` 嵌入，Gin 静态文件服务 |
| `app/src` | Vue.js 源码，使用 TDesign 组件库 |

---

## 关键数据流

### 启动服务器实例

```
HTTP: POST /api/server/:name/start
  │
  ▼
webapi.startServer()
  │ 检查端口冲突 (asaserver.CheckForDuplicatePorts)
  │ 检查操作是否允许 (asaserver.IsOperationAllowed)
  ▼
asaserver.StartServer(instanceName)
  │ CAS 状态转换: stopped → start_initialization
  │ 构建命令行参数
  │ 创建 NTFS junction（实例 Config 目录）
  │ 启动进程 (ArkAscendedServer.exe 或 AsaApiLoader.exe)
  │ CAS: start_initialization → starting
  │ 等待端口监听 (WaitForCondition)
  │ CAS: starting → started
  ▼
webapi 广播 WebSocket 事件: server_started
```

### 停止服务器实例

```
HTTP: POST /api/server/:name/stop
  │
  ▼
webapi.stopServer()
  ▼
asaserver.StopServer(instanceName)
  │ CAS: started → stopping
  │ RCON: saveworld
  │ RCON: DoExit
  │ 等待进程退出（5 分钟超时）
  │ CAS: stopping → stopped
  ▼
webapi 广播 WebSocket 事件: server_stopped
```

### SSE 流式推送

```
客户端连接 GET /api/server/info
  │
  ▼
webapi 创建 TaskBroadcaster 订阅
  │
  ▼
后台 goroutine:
  │ 每 2 秒采集 gopsutil 指标
  │ 写入 broadcaster
  │
  ▼
SSE 推送到客户端:
  data: {"cpu_usage":35.0, "memory_used":8589934592}
```

---

## 状态机

实例状态使用 CAS（Compare-And-Swap）原子转换，持久化在 BadgerDB 中。

```
                    ┌──────────────────────────────────┐
                    │         中间状态（自动恢复）         │
                    │                                  │
 ┌──────┐  start   │  start_initialization             │
 │stopped├────────►│       │                          │
 └──▲───┘          │       ▼                          │
    │              │  start_initialization_successful  │
    │ stop         │       │                          │
    │              │       ▼                          │
 ┌──┴───┐          │    starting ──────► started      │
 │started│◄────────┤                                  │
 └──┬───┘  完成     │  stopping ──────► stopped       │
    │              │                                  │
    │              │  restarting                      │
    │              │    ├──► restart ──► restarted     │
    │              │    └──► restart_failed            │
    │              │                                  │
    │              │  start_failed / stop_failed       │
    │              └──────────────────────────────────┘
    │
    │ force-stop
    ▼
 ┌──────┐
 │stopped│  (直接终止进程，重置状态)
 └──────┘
```

**全局互斥规则**: 当任何实例处于 `start_initialization` 状态时，所有其他操作（启动/停止/重启）均被阻塞。

**卡死自动恢复**: 后台每 30 秒检查一次，中间状态超过 10 分钟自动重置为 `stopped`。

详细状态控制文档参见 [STATE_CONTROL.md](STATE_CONTROL.md)。

---

## 设计模式

### 1. CAS 原子状态转换

```go
// compareAndSwapState — 仅在当前状态匹配允许列表时才转换
ok, err := stateManager.CompareAndSwapInstanceState(
    instanceName,
    []InstanceStatus{StatusStopped, StatusStartFailed},
    StatusStartStartInitialization,
)
```

防止并发操作导致状态混乱。

### 2. 函数选项模式

```go
// StartServer 支持多种可选配置
func StartServer(name string, options ...StartServerOptionsFunc) error

// 使用方式
StartServer("server1",
    WithGameLogPathCallback(func(path string) { ... }),
    WithGameInitializationSuccessfulCallback(func() { ... }),
    WithCtx(ctx),
    WithPidCallback(func(pid int) { ... }),
    WithWaitServerCompleted(),
)
```

### 3. 广播等待机制

```go
// 无轮询等待，基于条件变量广播
stateManager.WaitForCondition(
    func() bool { return isPortListening(port) },
    5*time.Minute,
)
```

### 4. TaskBroadcaster（SSE 发布/订阅）

```go
broadcaster := NewTaskBroadcaster()
go func() {
    broadcaster.Publish(TaskProgress{Message: "启动中...", Progress: 50})
}()
// HTTP handler 订阅并 SSE 推送
for event := range broadcaster.Subscribe() {
    c.SSEvent("progress", event)
}
```

### 5. 内嵌二进制 + MD5 校验

```go
//go:embed frpc.exe
var frpcBinary []byte

// 运行时提取，MD5 校验避免重复提取
if md5Match(targetPath, expectedMD5) {
    return // 已存在且正确
}
os.WriteFile(targetPath, frpcBinary, 0755)
```

### 6. Windows Job Object

```go
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE — 进程组关闭时自动终止所有子进程
job, _ := processjob.CreateJobObject()
processjob.AssignProcessToJob(job, process)
```

确保 FRP/Syncthing 等子进程在父进程退出时被可靠清理。

---

## 并发模型

| 组件 | 并发策略 |
|------|---------|
| 实例状态管理 | `sync.RWMutex` + CAS 原子操作 |
| API 请求串行化 | `serverActionsLock` 互斥锁防止并发 start/stop |
| SSE 推送 | 每个连接一个 goroutine，通过 TaskBroadcaster 解耦 |
| WebSocket | 每个连接一个 goroutine，Ping/Pong 90 秒超时 |
| 日志 tail | 每个实例一个 goroutine，fsnotify 监听文件变更 |
| 状态恢复检查 | 后台 goroutine 每 30 秒扫描卡死状态 |

---

## 目录布局（运行时）

```
{BaseDir}/
├── instances/              # 实例目录
│   └── {name}/
│       ├── instance_config.ini    # 实例配置（端口、密码、地图等）
│       ├── Config/                # NTFS junction → server-files/Config
│       │   ├── Game.ini
│       │   └── GameUserSettings.ini
│       └── server.log             # 实例日志
├── server-files/           # ARK 服务器安装目录（SteamCMD App ID 2430930）
├── steamcmd/               # SteamCMD 安装目录
├── backups/                # 备份文件（.tar.zstd）
├── frp/                    # 提取的 frpc.exe + 配置
├── syncthing/              # 提取的 syncthing.exe + 配置
├── database_file/          # BadgerDB 状态数据库
│   └── state_db/
├── logs/                   # 应用日志
│   ├── asaServer.log
│   └── arkApiLog.log
└── log_mapping.json        # 实例到日志文件的映射
```

---

## 技术栈

| 层 | 技术 |
|---|---|
| HTTP 框架 | Gin |
| 桌面 GUI | Fyne v2 |
| 前端 | Vue.js + TDesign（嵌入式 SPA） |
| 实时通信 | WebSocket (gorilla/websocket) + SSE |
| 状态持久化 | BadgerDB |
| 日志 | Zap + lumberjack（结构化、轮转） |
| 系统监控 | gopsutil |
| RCON | gorcon/rcon（3 次重试） |
| 文件通知 | fsnotify |
| 压缩 | klauspost/compress (zstd) |
| 进程管理 | Windows Job Object + WMI |
| Windows 服务 | kardianos/service |
| CLI | urfave/cli/v3 |
