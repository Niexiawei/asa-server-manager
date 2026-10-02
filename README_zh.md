# ASA Server Manager

> 目录已于本次迁移收进 `internal/`，包名与分层不变（详见 `docs/INTERNAL_LAYOUT_MIGRATION.md`）。

一个基于 Go 和 Vue.js 构建的全面的 ARK Server Ascended (ASA) 服务器管理工具，具有命令行界面、HTTP API 和 Web 仪表盘。

## 功能特性

### 核心功能
- ✅ **实例管理**：创建、列出、重命名和删除服务器实例
- ✅ **服务器控制**：启动、停止和重启单个或所有实例
- ✅ **RCON 支持**：向服务器实例发送 RCON 命令
- ✅ **备份与恢复**：创建和恢复世界备份，具有灵活的选项
- ✅ **配置管理**：查看和编辑游戏配置文件（Game.ini、GameUserSettings.ini）
- ✅ **日志记录**：实时查看服务器和实例日志

### 高级功能
- ✅ **Windows 服务集成**：作为 Windows 服务运行，实现后台操作
- ✅ **HTTP API 服务器**：支持 WebSocket 和 SSE 的 RESTful API，提供实时更新
- ✅ **Web 仪表盘**：嵌入式 Vue.js Web 界面，实现直观管理
- ✅ **FRP 集成**：内置快速反向代理管理，用于服务器暴露
- ✅ **Syncthing 集成**：用于服务器配置的文件同步功能
- ✅ **端口管理**：自动端口冲突检测和解决

## 系统要求

- **操作系统**：Windows 10/11（64位）原生；或 Linux（x86_64，经 umu + GE-Proton + Wine）
- **Go 版本**：1.26 或更高（用于开发）
- **Node.js**：16.x 或更高（用于前端开发）
- **磁盘空间**：服务器文件和实例至少需要 20GB
- **内存**：最小 8GB，推荐 16GB

## 安装

### 预构建二进制文件

1. 从 [Releases](https://github.com/yourusername/asa-server/releases) 页面下载最新版本
2. 将归档文件解压缩到您的首选位置
3. 运行 `asa-manager.exe` 启动应用程序

### 从源代码构建

#### 后端（Go）
```bash
go mod tidy
go build -o asa-manager.exe
```

#### 前端（Vue.js）
```bash
cd app
npm install
npm run build
```

## 使用方法

### 命令行界面

#### 基本命令

```bash
# 列出所有实例
asa-manager list

# 创建新实例
asa-manager create

# 启动实例
asa-manager start <instance_name>

# 停止实例
asa-manager stop <instance_name>

# 重启实例
asa-manager restart <instance_name>

# 检查服务器状态
asa-manager status [instance_name]
```

#### 备份与恢复

```bash
# 创建备份
asa-manager backup <instance_name> <world_folder>

# 恢复备份
asa-manager restore <instance_name> <backup_file> [flags]
```

可用的恢复标志：
- `--worldfile`：恢复世界文件（SaveDir）
- `--instance-config`：恢复 instance_config.ini
- `--game-config`：恢复游戏配置文件

#### 批量操作

```bash
# 启动所有实例
asa-manager start-all

# 停止所有实例
asa-manager stop-all
```

#### 配置管理

```bash
# 查看实例的 Game.ini
asa-manager view-game [instance_name]

# 查看实例的 GameUserSettings.ini
asa-manager view-game-user-settings [instance_name]

# 同步游戏配置文件
asa-manager sync-config <instance_name> [instance_name2] [...]
```

#### Windows 服务管理

```bash
# 安装为 Windows 服务
asa-manager service install

# 启动 Windows 服务
asa-manager service start

# 停止 Windows 服务
asa-manager service stop

# 移除 Windows 服务
asa-manager service remove
```

#### HTTP API 服务器

`api`（以及服务模式）需要一份已存在且有效的 `config.yaml`：先运行 `asa-manager config init`
（或 `asa-manager setup`）。缺少配置或配置无效时以退出码 78 退出，不会再用默认配置启动。

```bash
# 启动 HTTP API 服务器（默认 HTTPS + HTTP/2）
asa-manager api

# 退回明文 HTTP/1.1
asa-manager api --tls=false

# 使用自备证书，不生成本地 CA
asa-manager api --cert-file C:\certs\asa.crt --key-file C:\certs\asa.key

# 往证书 SAN 里追加域名（例如反向代理对外用的域名）
asa-manager api --tls-domains asa.example.com
```

API 服务器默认在端口 19193 上运行。

#### HTTPS 与本地 CA

服务默认以 **HTTPS + HTTP/2** 提供。这不只是加密问题：浏览器只在 TLS 上通过 ALPN
协商 HTTP/2，而 HTTP/2 才能摆脱「每源 6 条连接」的限制——常驻的 SSE 流会把这 6 条额度
吃光，导致普通请求静静排队。详见
[docs/HTTP2_CONNECTION_OPTIMIZATION.md](docs/HTTP2_CONNECTION_OPTIMIZATION.md)。

首次启动时程序会在 `{BaseDir}/certs/` 下生成本地 CA，并写入 Windows 受信任根存储，
浏览器打开 `https://localhost:19193` 不会有任何证书警告。CA 私钥**在你本机生成**，
绝不随二进制分发。

```bash
# 查看 CA 指纹、有效期与是否已受信任
asa-manager cert status

# 手动安装（--machine 装给所有用户，需要管理员权限）
asa-manager cert install [--machine]

# 从受信任根存储移除 CA
asa-manager cert uninstall
```

`asa-manager service remove` 会自动移除 CA。若不希望程序碰系统受信任存储，
用 `--tls-trust=false`（浏览器会提示一次证书警告，手动信任即可）；
若连 TLS 都不想要，用 `--tls=false`。

### Web 仪表盘

HTTP API 服务器运行后，可通过以下地址访问 Web 仪表盘：
```
https://localhost:19193
```

仪表盘提供用户友好的界面，用于：
- 管理服务器实例
- 控制服务器操作
- 查看实时日志
- 编辑配置
- 管理备份
- 监控服务器资源

## 项目结构

### 目录结构

原 `asaserver` 神包已按单一职责拆分为下列领域包，纯工具集中到 `pkg/`。
拆分理由见 [docs/PACKAGE_RESTRUCTURE_PLAN.md](docs/PACKAGE_RESTRUCTURE_PLAN.md)。

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
│   ├── appconfig/           # config.yaml 应用配置与校验
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
├── app/                     # 内嵌 Vue.js 前端（//go:embed dist）
└── docs/                    # 文档（索引见下）
```

### 实例目录结构

```
instances/
└── <instance_name>/
    ├── instance_config.ini    # 实例特定配置
    ├── Config/                # 游戏配置文件
    │   ├── Game.ini
    │   └── GameUserSettings.ini
    └── server.log             # 服务器日志文件
```

## HTTP API

HTTP API 为所有服务器管理功能提供 RESTful 端点。有关详细文档，请参阅 [API 参考](docs/API_REFERENCE.md)。

### 主要 API 端点

- `GET /health` - 健康检查
- `GET /api/instances` - 列出所有实例
- `POST /api/instances` - 创建新实例
- `GET /api/instances/:name` - 获取实例状态
- `POST /api/server/:name/start` - 启动实例
- `POST /api/server/:name/stop` - 停止实例
- `POST /api/rcon/:name/command` - 发送 RCON 命令
- `GET /api/ws/events` - 实时事件的 WebSocket
- `GET /api/logs/:name` - 实例日志的 SSE 流

## 配置

### 实例配置

每个实例都有一个 `instance_config.ini` 文件，结构如下：

```ini
[ServerSettings]
ServerName=ARK Server <instance_name>
ServerPassword=
ServerAdminPassword=adminpassword
MaxPlayers=70
MapName=TheIsland_WP
RCONPort=27020
Port=7777
ModIDs=
CustomStartParameters=-NoBattlEye -crossplay -NoHangDetection
SaveDir=<instance_name>
ClusterID=
```

## 集成功能

### FRP 集成

该工具包含内置的 FRP（快速反向代理）管理，用于将服务器暴露到互联网。

### Syncthing 集成

Syncthing 集成允许跨多个服务器轻松同步配置文件。

## 开发

### 先决条件

- Go 1.26 或更高版本
- Node.js 16.x 或更高版本
- npm 8.x 或更高版本

### 构建应用程序

1. **构建后端**：
   ```bash
   go build -o asa-manager.exe
   ```

2. **构建前端**：
   ```bash
   cd app
   npm install
   npm run build
   ```

### 运行开发服务器

1. **启动 API 服务器**：
   ```bash
   go run main.go api
   ```

2. **启动前端开发服务器**：
   ```bash
   cd app
   npm run dev
   ```

### 添加新功能

1. **CLI 命令**：在 `actions/actions.go` 中添加新命令，并在 `main.go` 中注册
2. **API 端点**：在 `webapi/actions.go` 中添加新端点
3. **前端组件**：在 `app/src/components/` 中添加新组件
4. **前端视图**：在 `app/src/views/` 中添加新视图

## 故障排除

### 常见问题

#### 命令未找到
确保 `asa-manager.exe` 在您的 PATH 中，或从正确的目录运行它。

#### 实例启动失败
- 检查实例配置是否正确
- 验证所需端口是否未被占用
- 查看服务器日志以获取更多详细信息

#### 备份失败
- 确保在创建备份之前实例已停止
- 验证世界文件夹名称是否正确
- 确保有足够的磁盘空间

#### API 服务器无法访问
- 检查服务是否正在运行
- 验证防火墙设置是否允许端口 19193 上的流量

## 文档索引

全部文档位于 [`docs/`](docs/)，总览见 [docs/README.md](docs/README.md)。

### 架构与设计

| 文档 | 说明 |
|------|------|
| [ARCHITECTURE.md](docs/ARCHITECTURE.md) | 系统架构与设计模式 |
| [PACKAGE_RESTRUCTURE_PLAN.md](docs/PACKAGE_RESTRUCTURE_PLAN.md) | `asaserver` 神包按领域拆分方案 |
| [STATE_CONTROL.md](docs/STATE_CONTROL.md) | 实例状态机、CAS 转换与互斥机制 |
| [MIRROR_STARTUP_PLAN.md](docs/MIRROR_STARTUP_PLAN.md) | NTFS 镜像启动：v2 迁移方案、技术架构、迁移变更日志与镜像去管理员化（真 junction） |
| [HTTP2_CONNECTION_OPTIMIZATION.md](docs/HTTP2_CONNECTION_OPTIMIZATION.md) | HTTP/2 连接数优化方案（SSE 挤占浏览器 6 条额度） |
| [instance-manager-daemon.md](docs/instance-manager-daemon.md) | 实例管理守护进程设计 |
| [LOGGER_REDESIGN_PLAN.md](docs/LOGGER_REDESIGN_PLAN.md) | `logger` 包重构方案（已实施，现为 `pkg/logger`）：console/file 多路 sink、`WithConsole` 链式调用、调用点全量迁移 |
| [INTERNAL_LAYOUT_MIGRATION.md](docs/INTERNAL_LAYOUT_MIGRATION.md) | 包目录整体收进 `internal/` 的迁移记录（包名与分层不变） |
| [RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md](docs/RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md) | `internal/runner` + `internal/instance` 拆包：机制下沉 `pkg/`、分层无环，含后续 Gap 清单与拆分审阅 |
| [WINNET_ETW_PLAN.md](docs/WINNET_ETW_PLAN.md) | 实例级网络监控（ETW）：`pkg/winnetetw` 设计、`pkg/procnet` 门面委托、迭代与真机验收清单 |

### Linux 兼容

| 文档 | 说明 |
|------|------|
| [LINUX_COMPATIBILITY_PLAN.md](docs/LINUX_COMPATIBILITY_PLAN.md) | Linux 兼容改造方案：耦合点清单、抽象层设计、分阶段实施记录（P0–P5 已实施） |
| [LINUX_DEPLOYMENT.md](docs/LINUX_DEPLOYMENT.md) | Linux 部署指南：依赖清单、安装步骤、systemd 服务化、故障排查 |
| [UMU_PREFIX_PLAN.md](docs/UMU_PREFIX_PLAN.md) | Wine prefix 三模式（shared / per-instance / overlay）：两道闸、启动闸门、`PROTON_VERB`，与 prefix 初始化失败排查（D0–D6） |
| [LINUX_RUNTIME_PRIVILEGE_PLAN.md](docs/LINUX_RUNTIME_PRIVILEGE_PLAN.md) | 降权运行时用户 `asa-umu-runtime`、共享写权限（组 + setgid + 默认 ACL + chown 兜底）、Python 解释器探测 |
| [XVFB_DISPLAY_PLAN.md](docs/XVFB_DISPLAY_PLAN.md) | 跨发行版虚拟显示：自管 Xvfb、显示候选链、WSL `/tmp/.X11-unix` remount、关掉 Xalia |
| [ARKAPI_LINUX_VCREDIST_PLAN.md](docs/ARKAPI_LINUX_VCREDIST_PLAN.md) | 把 VC++ 运行时装进 Wine prefix（DLL override + 微软安装器） |
| [ARKAPI_CACHE_PREFETCH_PLAN.md](docs/ARKAPI_CACHE_PREFETCH_PLAN.md) | ArkApi offsets cache 预取：多 CDN、断点续传、`validateSerializedMap` 格式复刻 |
| [ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md](docs/ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md) | ArkApi 日志转抄与游戏 PID 识别（`GameThread` comm 判据） |
| [ARKAPI_PLUGIN_PLAN.md](docs/ARKAPI_PLUGIN_PLAN.md) | ArkApi 插件按实例独立安装 / 更新 / 卸载，与插件数据、配置隔离 |
| [SETUP_FLOW_OPTIMIZATION_PLAN.md](docs/SETUP_FLOW_OPTIMIZATION_PLAN.md) | 环境未初始化时的引导：`setup` 跨平台化、`api`/`service install` 就绪门禁、Windows GUI 带实时进度的初始化面板（已实施） |
| [PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md](docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md) | 上述 Linux / umu / ArkApi 计划的**只读代码审计**：P0×3 / P1×23，附修复排期、文档合并说明与路径对照 |

### 功能设计

| 文档 | 说明 |
|------|------|
| [BATCH_OPERATION.md](docs/BATCH_OPERATION.md) | **批量启停** —— 编排流程、预检、CAS、SSE 日志长连接 |
| [stop-restart-countdown.md](docs/stop-restart-countdown.md) | 延迟停止/重启倒计时与游戏内公告 |
| [COUNTDOWN_RCON_REFACTOR_PLAN.md](docs/COUNTDOWN_RCON_REFACTOR_PLAN.md) | `countdown` / `rconx` 包拆分方案 |
| [SCHEDULE_RUN_LOG_DESIGN.md](docs/SCHEDULE_RUN_LOG_DESIGN.md) | 定时任务与执行日志 |
| [ARK_SAVE_PARSE_SOLUTION.md](docs/ARK_SAVE_PARSE_SOLUTION.md) | ARK 存档解析设计方案 |
| [PARSESERVER_REDESIGN.md](docs/PARSESERVER_REDESIGN.md) | `parseserver` 重构设计 |
| [state-change-ws-push.md](docs/state-change-ws-push.md) | 状态变更的 WebSocket 推送 |
| [ws-state-push-refactor.md](docs/ws-state-push-refactor.md) | WebSocket 状态推送重构 |
| [VirtualLogList.md](docs/VirtualLogList.md) | 前端虚拟滚动日志列表 |

### 参考手册

| 文档 | 说明 |
|------|------|
| [API_REFERENCE.md](docs/API_REFERENCE.md) | HTTP API 完整参考 |
| [CHEATSHEET.md](docs/CHEATSHEET.md) | 命令、配置、RCON 速查 |
| [asa-server-configuration.md](docs/asa-server-configuration.md) | ARK 服务器配置参考 |
| [asa-game-configuration-reference.md](docs/asa-game-configuration-reference.md) | Game.ini / GameUserSettings.ini 参考 |
| [game-ini-visual-config-guide.md](docs/game-ini-visual-config-guide.md) | Game.ini 可视化配置指南 |
| [asa-creatureids.md](docs/asa-creatureids.md) · [asa-itemsids.md](docs/asa-itemsids.md) · [asa-engrams.md](docs/asa-engrams.md) | 生物 / 物品 / 引擎蓝图 ID 对照表 |

### 迁移与历史

| 文档 | 说明 |
|------|------|
| [MIGRATION.md](docs/MIGRATION.md) | 从 bash 脚本迁移指南 |
| [WEBAUTHN_REMOVAL_PLAN.md](docs/WEBAUTHN_REMOVAL_PLAN.md) | 移除 WebAuthn，只保留密码 + TOTP 两步验证 + 恢复码 |
| [STARTUP_FIXES.md](docs/STARTUP_FIXES.md) | 启动/停止流程修复记录 |

### 工具

| 文档 | 说明 |
|------|------|
| [ark-translation-tool.md](docs/ark-translation-tool.md) | ARK 翻译工具 |
| [download-creature-icons.md](docs/download-creature-icons.md) · [download-item-icons.md](docs/download-item-icons.md) | 图标下载脚本 |

## 贡献

欢迎贡献！请随时提交 Pull Request。

## 许可证

MIT 许可证

## 支持

对于问题和功能请求，请创建新的 [GitHub Issue](https://github.com/yourusername/asa-server/issues)。
