# ASA Server Manager

ARK: Survival Ascended (ASA) 专用服务器管理工具。基于 Go + Vue.js 构建，提供 GUI 桌面界面、HTTP API、CLI 和 Windows 服务四种使用方式。

> **平台限制：仅支持 Windows 10/11 (64-bit)**

## 功能特性

- **实例管理** — 创建、删除、重命名多个服务器实例，每个实例独立配置
- **服务器控制** — 启动、停止、重启、强制停止，支持批量操作
- **RCON 通信** — 发送 RCON 命令、实时交互，WebSocket 双向通道
- **配置管理** — Game.ini / GameUserSettings.ini 读写，实例间配置同步
- **备份/恢复** — tar+zstd 压缩格式，支持选择性恢复（存档/实例配置/游戏配置）
- **日志流** — SSE 实时推送实例日志和系统日志
- **状态管理** — BadgerDB 持久化，CAS 原子状态转换，卡死自动恢复
- **SteamCMD 集成** — 一键安装/更新 ARK 服务器
- **FRP 管理** — 内嵌 frpc，反向代理配置与状态监控
- **Syncthing 管理** — 内嵌 syncthing，集群文件同步
- **系统监控** — CPU/内存/进程指标实时流式推送
- **存档解析** — 解析 .ark 存档文件，获取玩家和部落数据
- **Web UI** — 内嵌 Vue.js SPA（TDesign 组件库）

## 快速开始

### 环境要求

- Windows 10/11 (64-bit)
- Go 1.26+（编译）
- Node.js 16+（编译前端）

### 构建

```powershell
# 编译后端
go build -o asa-server.exe

# 编译前端
cd app
npm install
npm run build
cd ..
```

### 运行

```powershell
# GUI 模式（默认，双击 exe 或无参数运行）
.\asa-server.exe

# API 服务器模式
.\asa-server.exe api

# 指定端口
.\asa-server.exe api --port 19193

# 安装为 Windows 服务
.\asa-server.exe service install
.\asa-server.exe service start

# 安装/更新 ARK 服务器
.\asa-server.exe update
```

### 默认端口

HTTP API 默认端口：**19193**

```
http://localhost:19193        # Web UI
http://localhost:19193/health # 健康检查
```

## 项目结构

原 `asaserver` 神包已按单一职责拆分为下列领域包，纯工具集中到 `pkg/`。
拆分理由见 [PACKAGE_RESTRUCTURE_PLAN.md](PACKAGE_RESTRUCTURE_PLAN.md)。

```
asa-server/
├── main.go                  # 入口：CLI 命令、GUI、Windows 服务检测
│
│  ── 领域包（自底向上，无环）──
├── pkg/                     # 叶子工具：fsutil、winproc、netutil、tail、console、iox、
│                            #   processjob（Windows Job Object）、serverinfo（gopsutil 指标）
├── config/                  # 目录布局、InstanceConfig、INI 读写、配置同步
├── process/                 # PID 文件存储 + IsServerRunning（解 state ↔ instance 环的关键层）
├── rconx/                   # RCON 连接与命令执行（重试、哨兵错误）
├── realtime/                # WebSocket 中枢：服务器事件 + 交互式 RCON
├── state/                   # BadgerDB 实例状态持久化（CAS 状态机）
├── installer/               # SteamCMD 下载、ARK 服务器更新
├── mirror/                  # 实例镜像 / NTFS junction 管理
├── instance/                # 生命周期 Start/Stop/Restart、存档、Mod 提取、ASA 版本
├── countdown/               # 延迟停止/重启编排：倒计时 + 游戏内公告 + 登记表
├── batchmanage/             # 多实例批量启停（详见 BATCH_OPERATION.md）
├── schedule/                # 定时任务（重启 / 更新）
├── updatemanage/            # 服务器更新任务单例
│
│  ── 交互层 ──
├── webapi/                  # HTTP API，按领域拆子包
│   ├── actions.go           # APIServer 装配 + setupRoutes
│   ├── state_dispatcher.go  # 状态变更 WS 推送
│   └── instanceapi/ serverapi/ backupapi/ configapi/ saveapi/ logapi/ iconapi/ apiresp/
├── app/                     # 内嵌 Vue.js 前端（//go:embed dist）
│   ├── appembed.go          # 内嵌 dist/ 供 Gin 静态服务
│   └── src/                 # Vue.js 源码（TDesign 组件）
├── gui/                     # Fyne 桌面 GUI（系统托盘、服务管理、日志查看）
├── winservice/              # Windows 服务集成（kardianos/service）
├── actions/                 # CLI 命令处理器（update）
│
│  ── 支撑 ──
├── backup/                  # tar+zstd 备份/恢复（函数选项模式）
├── frpmanage/               # FRP 反向代理管理（内嵌 frpc.exe）
├── syncthingmanage/         # Syncthing 文件同步管理（内嵌 syncthing.exe）
├── parseserver/             # ARK 存档解析（go-arkparser + save_monitor）
├── githubreleases/          # GitHub Releases API 客户端（带下载进度）
├── logger/                  # Zap + lumberjack 结构化日志（带轮转）
└── docs/                    # 文档
```

## 运行时目录

```
{BaseDir}/
├── instances/
│   └── {instance_name}/
│       ├── instance_config.ini
│       ├── Config/
│       │   ├── Game.ini
│       │   └── GameUserSettings.ini
│       └── server.log
├── server-files/            # ARK 服务器安装目录
├── steamcmd/                # SteamCMD
├── backups/                 # 备份文件（.zstd）
├── frp/                     # 提取的 frpc.exe
├── syncthing/               # 提取的 syncthing.exe
├── database_file/           # BadgerDB 状态数据
├── logs/                    # asaServer.log、arkApiLog.log
├── schedules.json           # 定时任务定义（顶层数组，可手改）
├── schedule_logs.json       # 定时任务执行日志（全局滚动窗口，最多 500 条）
└── log_mapping.json         # 实例到日志文件的映射
```

## 主要依赖

| 依赖 | 用途 |
|------|------|
| `github.com/gin-gonic/gin` | HTTP 框架 |
| `fyne.io/fyne/v2` | 桌面 GUI |
| `github.com/shirou/gopsutil/v4` | 系统指标 |
| `github.com/dgraph-io/badger/v4` | 持久化状态存储 |
| `github.com/gorcon/rcon` | 游戏 RCON 协议 |
| `github.com/fsnotify/fsnotify` | 文件系统通知（日志 tail） |
| `github.com/kardianos/service` | Windows 服务 |
| `github.com/urfave/cli/v3` | CLI 框架 |
| `github.com/gorilla/websocket` | WebSocket |
| `go.uber.org/zap` | 结构化日志 |
| `gopkg.in/natefinch/lumberjack.v2` | 日志轮转 |
| `github.com/jinzhu/copier` | 结构体拷贝 |
| `github.com/klauspost/compress` | zstd 压缩（备份） |
| `github.com/microsoft/wmi` | WMI 查询 |

## 文档索引

### 架构与设计

| 文档 | 说明 |
|------|------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | 系统架构与设计模式 |
| [PACKAGE_RESTRUCTURE_PLAN.md](PACKAGE_RESTRUCTURE_PLAN.md) | `asaserver` 神包按领域拆分方案 |
| [STATE_CONTROL.md](STATE_CONTROL.md) | 实例状态机、CAS 转换与互斥机制 |
| [V2_MIRROR_STARTUP_ARCHITECTURE.md](V2_MIRROR_STARTUP_ARCHITECTURE.md) | NTFS 镜像目录方案，支持多实例并行启动 |
| [HTTP2_CONNECTION_OPTIMIZATION.md](HTTP2_CONNECTION_OPTIMIZATION.md) | HTTP/2 连接数优化方案（SSE 挤占浏览器 6 条额度） |
| [instance-manager-daemon.md](instance-manager-daemon.md) | 实例管理守护进程设计 |

### 功能设计

| 文档 | 说明 |
|------|------|
| [BATCH_OPERATION.md](BATCH_OPERATION.md) | **批量启停** —— 编排流程、预检、CAS、SSE 日志长连接 |
| [stop-restart-countdown.md](stop-restart-countdown.md) | 延迟停止/重启倒计时与游戏内公告 |
| [COUNTDOWN_RCON_REFACTOR_PLAN.md](COUNTDOWN_RCON_REFACTOR_PLAN.md) | `countdown` / `rconx` 包拆分方案 |
| [SCHEDULE_RUN_LOG_DESIGN.md](SCHEDULE_RUN_LOG_DESIGN.md) | 定时任务与执行日志 |
| [ARK_SAVE_PARSE_SOLUTION.md](ARK_SAVE_PARSE_SOLUTION.md) | ARK 存档解析设计方案 |
| [PARSESERVER_REDESIGN.md](PARSESERVER_REDESIGN.md) | `parseserver` 重构设计 |
| [state-change-ws-push.md](state-change-ws-push.md) | 状态变更的 WebSocket 推送 |
| [ws-state-push-refactor.md](ws-state-push-refactor.md) | WebSocket 状态推送重构 |
| [VirtualLogList.md](VirtualLogList.md) | 前端虚拟滚动日志列表 |

### 参考手册

| 文档 | 说明 |
|------|------|
| [API_REFERENCE.md](API_REFERENCE.md) | HTTP API 完整参考 |
| [CHEATSHEET.md](CHEATSHEET.md) | 命令、配置、RCON 速查 |
| [asa-server-configuration.md](asa-server-configuration.md) | ARK 服务器配置参考 |
| [asa-game-configuration-reference.md](asa-game-configuration-reference.md) | Game.ini / GameUserSettings.ini 参考 |
| [game-ini-visual-config-guide.md](game-ini-visual-config-guide.md) | Game.ini 可视化配置指南 |
| [asa-creatureids.md](asa-creatureids.md) · [asa-itemsids.md](asa-itemsids.md) · [asa-engrams.md](asa-engrams.md) | 生物 / 物品 / 引擎蓝图 ID 对照表 |

### 迁移与历史

| 文档 | 说明 |
|------|------|
| [MIGRATION.md](MIGRATION.md) | 从 bash 脚本迁移指南 |
| [V2_MIGRATION_PLAN.md](V2_MIGRATION_PLAN.md) · [V2_MIGRATION_CHANGELOG.md](V2_MIGRATION_CHANGELOG.md) | v2 迁移方案与变更日志 |
| [STARTUP_FIXES.md](STARTUP_FIXES.md) | 启动/停止流程修复记录 |

### 工具

| 文档 | 说明 |
|------|------|
| [ark-translation-tool.md](ark-translation-tool.md) | ARK 翻译工具 |
| [download-creature-icons.md](download-creature-icons.md) · [download-item-icons.md](download-item-icons.md) | 图标下载脚本 |

## 开发说明

- 项目仅支持 Windows，`main.go` 检查 `runtime.GOOS` 并在非 Windows 系统退出
- 前端使用 TDesign Vue 组件库
- FRP 和 Syncthing 通过 `//go:embed` 嵌入，更新需重新编译
- 实例状态持久化在 BadgerDB 中，重启后保持
- 服务器启动使用 NTFS 镜像目录方案，每个实例拥有独立的 `server-files-tmp-<name>/` 镜像，通过 junction/symlink 链接到原始文件，支持多实例并行启动
- 长时间操作通过 SSE 流式推送进度，非普通 HTTP 响应
- API 服务器使用互斥锁（`serverActionsLock`）防止并发启动/停止操作
