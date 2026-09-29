# ASA Server Manager

ARK: Survival Ascended (ASA) 专用服务器管理工具。基于 Go + Vue.js 构建，提供 GUI 桌面界面、HTTP API、CLI 和 OS 服务四种使用方式。

> **平台支持**：Windows 10/11 (64-bit) 原生运行；Linux 经 **umu + GE-Proton + Wine** 运行同一套后端，详见 [LINUX_DEPLOYMENT.md](LINUX_DEPLOYMENT.md) 与 [LINUX_COMPATIBILITY_PLAN.md](LINUX_COMPATIBILITY_PLAN.md)。

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

- Windows 10/11 (64-bit) 或 Linux（x86_64）
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

### 首次部署：先配置、后初始化

双击 exe 时 GUI 会弹出首次设置向导：选配置文件位置（程序目录 / 系统目录 / 自定义目录）→ 选数据目录 →
检查配置（可用记事本打开修改）→ 初始化环境。命令行的等价流程：

```powershell
.\asa-server.exe config init --basedir E:\ASA-Data   # 只生成 config.yaml，不建目录、不下载
notepad .\config.yaml                                # 改下载代理、端口等
.\asa-server.exe config validate                     # 校验
.\asa-server.exe setup                               # SteamCMD + ARK 本体（约 25GB）
```

`asa-server config path` 显示当前使用的是哪一份 `config.yaml`、数据目录来自哪里。
配置文件放在程序目录与 `%ProgramData%\ASAServerManager` 之外时，用 `config init --dir <目录> --set-env`
设置环境变量 `ASA_CFG`。Linux 见 [LINUX_DEPLOYMENT.md](LINUX_DEPLOYMENT.md) §2.1。

### 默认端口

HTTP API 默认端口：**19193**，默认以 HTTPS + HTTP/2 提供服务。

```
https://localhost:19193        # Web UI
https://localhost:19193/health # 健康检查
```

首次启动会生成本地 CA 并写入 Windows 受信任根存储，浏览器无证书警告；
`asa-server cert status` 可查看，`cert uninstall` 可移除。
用 `--tls=false` 可退回明文 HTTP/1.1。详见
[HTTP2_CONNECTION_OPTIMIZATION.md](HTTP2_CONNECTION_OPTIMIZATION.md)。

## 项目结构

原 `asaserver` 神包已按单一职责拆分为下列领域包，纯工具集中到 `pkg/`。
拆分理由见 [PACKAGE_RESTRUCTURE_PLAN.md](PACKAGE_RESTRUCTURE_PLAN.md)。

```
```
asa-server/
├── main.go                  # 入口：CLI 命令、GUI、OS 服务检测
├── main_windows.go          # Windows 平台专属入口
├── main_linux.go            # Linux 平台专属入口
│
│  ── 领域包（自底向上，无环；已整体收进 internal/）──
├── internal/
│   ├── config/              # 目录布局、InstanceConfig、INI 读写、配置同步
│   ├── appconfig/           # config.yaml 应用配置与校验；模板渲染（中 / 英）与 config init
│   ├── bootstrap/           # 应用配置 → 下载器与 runner 的唯一应用点（Apply / Reload）
│   ├── process/             # PID 文件存储 + IsServerRunning（解 state ↔ instance 环的关键层）
│   ├── certmgr/             # 本地 CA + 叶子证书、受信任根存储（HTTPS/h2）
│   ├── rconx/               # RCON 连接与命令执行（重试、哨兵错误）
│   ├── realtime/            # WebSocket 中枢：服务器事件 + 交互式 RCON
│   ├── state/               # BadgerDB 实例状态持久化（CAS 状态机）
│   ├── installer/           # SteamCMD 下载 / ARK 服务器更新
│   ├── mirror/              # 实例镜像 / junction（Linux 为 symlink）管理
│   ├── instance/            # 生命周期 Start/Stop/Restart、存档、Mod 提取、ASA 版本
│   ├── runner/              # 跨平台实例启动器：Windows 原生 / Linux（umu + Proton + Wine）组合根
│   ├── arkapimanage/        # ArkApi 主程序与插件（每实例）安装 / 更新 / 卸载
│   ├── plugindata/          # 插件数据与配置隔离（布局、迁移、分类）
│   ├── countdown/           # 延迟停止/重启编排：倒计时 + 游戏内公告 + 登记表
│   ├── batchmanage/         # 多实例批量启停（详见 BATCH_OPERATION.md）
│   ├── schedule/            # 定时任务（重启 / 更新）
│   ├── updatemanage/        # 服务器更新任务单例
│   ├── filesyncmanage/      # 文件同步管理
│   ├── backup/              # tar+zstd 备份/恢复（函数选项模式）
│   ├── frpmanage/           # FRP 反向代理管理
│   ├── syncthingmanage/     # Syncthing 文件同步管理
│   ├── parseserver/         # ARK 存档解析
│   ├── auth/                # 鉴权：登录、限流、TOTP、审计日志
│   │
│   │  ── 交互层 ──
│   ├── webapi/              # HTTP API，按领域拆子包：instanceapi、serverapi、backupapi、
│   │                        #   configapi、saveapi、logapi、iconapi、authapi、scheduleapi、apiresp
│   ├── gui/                 # Fyne 桌面 GUI（系统托盘、服务管理、日志查看）
│   ├── svcmgr/              # OS 服务集成（kardianos/service：Windows SCM / Linux systemd）
│   └── actions/             # CLI 命令处理器（update / setup / perms / prefix / netmon 等）
│
├── pkg/                     # 叶子工具，零领域依赖：
│                            #   archive asaversion console download fsutil iox linuxdeps logger
│                            #   netutil problem procmatch procnet proctree procx pyfinder
│                            #   resourcegate serverinfo shareacl steamrt sysuser tail umu
│                            #   vcredist wineprefix winnetetw xvfb arkcache display
│                            #   userenv（持久化环境变量）folderpicker（原生选择文件夹对话框）
├── app/                     # 内嵌 Vue.js 前端（//go:embed dist）
└── docs/                    # 文档（索引见下）
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
├── certs/                   # 本地 CA（ca.crt/ca.key）与服务器证书（server.crt/server.key）
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
| `github.com/kardianos/service` | OS 服务（Windows SCM / Linux systemd） |
| `github.com/urfave/cli/v3` | CLI 框架 |
| `github.com/gorilla/websocket` | WebSocket |
| `go.uber.org/zap` | 结构化日志 |
| `gopkg.in/natefinch/lumberjack.v2` | 日志轮转 |
| `github.com/jinzhu/copier` | 结构体拷贝 |
| `github.com/klauspost/compress` | zstd 压缩（备份） |
| `github.com/yusufpapurcu/wmi` | WMI 查询（与 gopsutil 共用同一份依赖） |

## 文档索引

### 架构与设计

| 文档 | 说明 |
|------|------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | 系统架构与设计模式 |
| [PACKAGE_RESTRUCTURE_PLAN.md](PACKAGE_RESTRUCTURE_PLAN.md) | `asaserver` 神包按领域拆分方案 |
| [STATE_CONTROL.md](STATE_CONTROL.md) | 实例状态机、CAS 转换与互斥机制 |
| [MIRROR_STARTUP_PLAN.md](MIRROR_STARTUP_PLAN.md) | NTFS 镜像启动：v2 迁移方案、技术架构、迁移变更日志与镜像去管理员化（真 junction） |
| [HTTP2_CONNECTION_OPTIMIZATION.md](HTTP2_CONNECTION_OPTIMIZATION.md) | **HTTPS + HTTP/2**（已实施）——本地 CA、受信任存储、反向代理兼容 |
| [instance-manager-daemon.md](instance-manager-daemon.md) | 实例管理守护进程设计 |
| [LOGGER_REDESIGN_PLAN.md](LOGGER_REDESIGN_PLAN.md) | `logger` 包重构方案（已实施，现为 `pkg/logger`）：console/file 多路 sink、`WithConsole` 链式调用、调用点全量迁移 |
| [INTERNAL_LAYOUT_MIGRATION.md](INTERNAL_LAYOUT_MIGRATION.md) | 包目录整体收进 `internal/` 的迁移记录（包名与分层不变） |
| [RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md](RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md) | `internal/runner` + `internal/instance` 拆包：机制下沉 `pkg/`、分层无环，含后续 Gap 清单与拆分审阅 |
| [WINNET_ETW_PLAN.md](WINNET_ETW_PLAN.md) | 实例级网络监控（ETW）：`pkg/winnetetw` 设计、`pkg/procnet` 门面委托、迭代与真机验收清单 |

### Linux 兼容

| 文档 | 说明 |
|------|------|
| [LINUX_COMPATIBILITY_PLAN.md](LINUX_COMPATIBILITY_PLAN.md) | Linux 兼容改造方案：耦合点清单、抽象层设计、分阶段实施记录（P0–P5 已实施） |
| [LINUX_DEPLOYMENT.md](LINUX_DEPLOYMENT.md) | Linux 部署指南：依赖清单、安装步骤、systemd 服务化、故障排查 |
| [UMU_PREFIX_PLAN.md](UMU_PREFIX_PLAN.md) | Wine prefix 三模式（shared / per-instance / overlay）：两道闸、启动闸门、`PROTON_VERB`，与 prefix 初始化失败排查（D0–D6） |
| [LINUX_RUNTIME_PRIVILEGE_PLAN.md](LINUX_RUNTIME_PRIVILEGE_PLAN.md) | 降权运行时用户 `asa-umu-runtime`、共享写权限（组 + setgid + 默认 ACL + chown 兜底）、Python 解释器探测 |
| [XVFB_DISPLAY_PLAN.md](XVFB_DISPLAY_PLAN.md) | 跨发行版虚拟显示：自管 Xvfb、显示候选链、WSL `/tmp/.X11-unix` remount、关掉 Xalia |
| [ARKAPI_LINUX_VCREDIST_PLAN.md](ARKAPI_LINUX_VCREDIST_PLAN.md) | 把 VC++ 运行时装进 Wine prefix（DLL override + 微软安装器） |
| [ARKAPI_CACHE_PREFETCH_PLAN.md](ARKAPI_CACHE_PREFETCH_PLAN.md) | ArkApi offsets cache 预取：多 CDN、断点续传、`validateSerializedMap` 格式复刻 |
| [ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md](ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md) | ArkApi 日志转抄与游戏 PID 识别（`GameThread` comm 判据） |
| [ARKAPI_PLUGIN_PLAN.md](ARKAPI_PLUGIN_PLAN.md) | ArkApi 插件按实例独立安装 / 更新 / 卸载，与插件数据、配置隔离 |
| [SETUP_FLOW_OPTIMIZATION_PLAN.md](SETUP_FLOW_OPTIMIZATION_PLAN.md) | 环境未初始化时的引导：`setup` 跨平台化、`api`/`service install` 就绪门禁、Windows GUI 带实时进度的初始化面板；Part 2：`config init` 先配置后初始化、配置文件乱码、GUI 首次设置向导、`ASA_CFG` 持久化（均已实施） |
| [PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md](PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md) | 上述 Linux / umu / ArkApi 计划的**只读代码审计**：P0×3 / P1×23，附修复排期、文档合并说明与路径对照 |

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
| [WEBAUTHN_REMOVAL_PLAN.md](WEBAUTHN_REMOVAL_PLAN.md) | 移除 WebAuthn，只保留密码 + TOTP 两步验证 + 恢复码 |
| [STARTUP_FIXES.md](STARTUP_FIXES.md) | 启动/停止流程修复记录 |

### 工具

| 文档 | 说明 |
|------|------|
| [ark-translation-tool.md](ark-translation-tool.md) | ARK 翻译工具 |
| [download-creature-icons.md](download-creature-icons.md) · [download-item-icons.md](download-item-icons.md) | 图标下载脚本 |

## 开发说明

- 双平台：Windows 原生；Linux 经 umu + GE-Proton + Wine 运行。入口按平台拆分（`main_windows.go` / `main_linux.go`），OS 服务由 `internal/svcmgr` 统一抽象（Windows SCM / Linux systemd）
- 前端使用 TDesign Vue 组件库
- FRP 和 Syncthing 通过 `//go:embed` 嵌入，更新需重新编译
- 实例状态持久化在 BadgerDB 中，重启后保持
- 服务器启动使用 NTFS 镜像目录方案，每个实例拥有独立的 `server-files-tmp-<name>/` 镜像，通过 junction/symlink 链接到原始文件，支持多实例并行启动
- 长时间操作通过 SSE 流式推送进度，非普通 HTTP 响应
- **没有**全局启停互斥锁（`serverActionsLock` 早已删除，此处旧说法有误）。不同实例的并发启停是允许的；
  单实例的串行由状态机 CAS 保证，批量操作本身就是串行的。唯一的例外是 Linux 且
  `linux.prefix_mode: shared` 时的启动闸门（`internal/instance/launchgate.go`），Windows 上为 no-op
