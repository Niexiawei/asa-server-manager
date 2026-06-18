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

```
asa-server/
├── main.go                  # 入口：CLI 命令、GUI、Windows 服务检测
├── asaserver/               # 核心：实例生命周期、配置、RCON、安装器、状态管理
│   ├── config.go            # 目录布局、InstanceConfig、INI 读写
│   ├── server.go            # 启动/停止/重启、RCON 命令
│   ├── common.go            # 日志 tail（fsnotify）、文件工具、mod 提取
│   ├── installer.go         # SteamCMD 下载、ARK 服务器更新
│   └── state_manager.go     # BadgerDB 实例状态持久化
├── asaserverv2/             # 核心 v2：重构中的实例管理（mirror、force_stop 等）
├── webapi/                  # HTTP API + WebSocket + SSE
│   ├── actions.go           # APIServer 结构、路由注册
│   ├── api.go               # 所有 HTTP 处理器
│   ├── broadcast.go         # TaskBroadcaster 发布/订阅
│   ├── task.go              # 后台任务（更新、批量操作）
│   └── ws.go                # WebSocket 事件广播 + RCON
├── gui/                     # Fyne 桌面 GUI（系统托盘、服务管理、日志查看）
├── winservice/              # Windows 服务集成（kardianos/service）
├── actions/                 # CLI 命令处理器（update）
├── backup/                  # tar+zstd 备份/恢复（函数选项模式）
├── frpmanage/               # FRP 反向代理管理（内嵌 frpc.exe）
├── syncthingmanage/         # Syncthing 文件同步管理（内嵌 syncthing.exe）
├── processjob/              # Windows Job Object 进程树管理
├── serverinfo/              # CPU/内存/进程指标（gopsutil）
├── parseserver/             # ARK 存档解析（go-arkparser + save_monitor）
├── win32api/                # Windows API 互操作（user32/kernel32）
├── common/                  # 共享工具（DNS 解析、WMI 查询）
├── githubreleases/          # GitHub Releases API 客户端（带下载进度）
├── logger/                  # Zap + lumberjack 结构化日志（带轮转）
├── app/                     # 内嵌 Vue.js 前端（//go:embed dist）
│   ├── appembed.go          # 内嵌 dist/ 供 Gin 静态服务
│   └── src/                 # Vue.js 源码（TDesign 组件）
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
├── backups/                 # 备份文件（.tar.zstd）
├── frp/                     # 提取的 frpc.exe
├── syncthing/               # 提取的 syncthing.exe
├── database_file/           # BadgerDB 状态数据
├── logs/                    # asaServer.log、arkApiLog.log
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

| 文档 | 说明 |
|------|------|
| [API_REFERENCE.md](API_REFERENCE.md) | HTTP API 完整参考（56 个端点） |
| [ARCHITECTURE.md](ARCHITECTURE.md) | 系统架构与设计模式 |
| [CHEATSHEET.md](CHEATSHEET.md) | 命令、配置、RCON 速查 |
| [MIGRATION.md](MIGRATION.md) | 从 bash 脚本迁移指南 |
| [STATE_CONTROL.md](STATE_CONTROL.md) | 实例状态控制与互斥机制 |
| [STARTUP_FIXES.md](STARTUP_FIXES.md) | 启动/停止流程修复记录 |
| [ARK_SAVE_PARSE_SOLUTION.md](ARK_SAVE_PARSE_SOLUTION.md) | ARK 存档解析设计方案 |

## 开发说明

- 项目仅支持 Windows，`main.go` 检查 `runtime.GOOS` 并在非 Windows 系统退出
- 前端使用 TDesign Vue 组件库
- FRP 和 Syncthing 通过 `//go:embed` 嵌入，更新需重新编译
- 实例状态持久化在 BadgerDB 中，重启后保持
- 服务器启动使用 NTFS junction 共享基础服务器配置，同时允许每实例自定义
- 长时间操作通过 SSE 流式推送进度，非普通 HTTP 响应
- API 服务器使用互斥锁（`serverActionsLock`）防止并发启动/停止操作
