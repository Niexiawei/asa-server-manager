# 迁移指南

从 Bash 脚本 (`ark_instance_manager.sh`) 迁移到 ASA Server Manager Go 版本。

---

## 背景

ASA Server Manager 最初是一个 Bash 脚本 (`ark_instance_manager.sh`)，用于在 Linux 环境下管理 ARK: Survival Ascended 服务器实例。Go 版本完全重写了所有功能，提供了更好的性能、可靠性和用户体验。

> **注意**: Go 版本仅支持 Windows 10/11 (64-bit)，不再支持 Linux/macOS。

---

## 功能对照表

| 功能 | Bash 脚本 | Go 版本 | 状态 |
|------|-----------|---------|------|
| 实例创建/删除 | ✅ Shell 脚本 | ✅ CLI / API / GUI | ✅ 已实现 |
| 服务器启动/停止 | ✅ Shell 脚本 | ✅ CLI / API / GUI + CAS 状态管理 | ✅ 已实现 |
| RCON 命令 | ✅ 基础实现 | ✅ gorcon/rcon（3 次重试） | ✅ 已实现 |
| 配置管理 | ✅ 手动编辑 INI | ✅ API 读写 + 实例间同步 | ✅ 已实现 |
| 备份/恢复 | ✅ tar.gz | ✅ tar+zstd + 选择性恢复 | ✅ 已改进 |
| 日志查看 | ✅ tail 命令 | ✅ SSE 实时推送 + GUI 查看器 | ✅ 已改进 |
| SteamCMD 集成 | ❌ 手动 | ✅ 自动下载/更新 | ✅ 新增 |
| HTTP API | ❌ | ✅ 56 个端点 | ✅ 新增 |
| Web UI | ❌ | ✅ Vue.js SPA（内嵌） | ✅ 新增 |
| 桌面 GUI | ❌ | ✅ Fyne 系统托盘 | ✅ 新增 |
| Windows 服务 | ❌ | ✅ kardianos/service | ✅ 新增 |
| 状态持久化 | ❌ 内存 | ✅ BadgerDB | ✅ 新增 |
| FRP 反向代理 | ❌ | ✅ 内嵌 frpc.exe | ✅ 新增 |
| 集群同步 | ❌ | ✅ 内嵌 syncthing.exe | ✅ 新增 |
| 系统监控 | ❌ | ✅ gopsutil + SSE 推送 | ✅ 新增 |
| WebSocket 事件 | ❌ | ✅ 实时服务器事件广播 | ✅ 新增 |
| 存档解析 | ❌ | ✅ ARK 存档数据分析 | ✅ 新增 |

---

## 命令对照

| Bash 脚本命令 | Go 版本 CLI | Go 版本 API |
|--------------|-------------|-------------|
| `./ark_manager.sh create <name>` | 自动创建 | `POST /api/instances` |
| `./ark_manager.sh start <name>` | 通过 API | `GET /api/server/:name/start` |
| `./ark_manager.sh stop <name>` | 通过 API | `GET /api/server/:name/stop` |
| `./ark_manager.sh restart <name>` | 通过 API | `GET /api/server/:name/restart` |
| `./ark_manager.sh status` | 通过 API | `GET /api/instances` |
| `./ark_manager.sh rcon <cmd>` | 通过 API | `POST /api/rcon/:name/command` |
| `./ark_manager.sh backup <name>` | 通过 API | `POST /api/backup/:name` |
| `./ark_manager.sh update` | `.\asa-server.exe update` | `GET /api/server/update` |

---

## 配置迁移

### 实例配置

Bash 脚本和 Go 版本使用相同的 INI 格式实例配置文件 (`instance_config.ini`)。如果已有实例目录，可以直接复制到 Go 版本的 `{BaseDir}/instances/` 目录下。

### 目录结构变化

```
# Bash 脚本（Linux）
~/ark-servers/
├── instances/
│   └── {name}/
├── server-files/
└── backups/

# Go 版本（Windows）
{BaseDir}/
├── instances/          # 结构兼容
├── server-files/       # 结构兼容
├── backups/            # 格式变更：.tar.gz → .tar.zstd
├── steamcmd/           # 新增
├── frp/                # 新增
├── syncthing/          # 新增
├── database_file/      # 新增（BadgerDB）
└── logs/               # 新增
```

---

## 已知差异

| 方面 | Bash 脚本 | Go 版本 |
|------|-----------|---------|
| 平台 | Linux | Windows 仅 |
| 备份格式 | `.tar.gz` | `.tar.zstd` |
| 进程管理 | `systemd` / `screen` | 直接进程管理 + Windows Job Object |
| 配置格式 | INI（兼容） | INI（兼容，新增字段） |
| 状态管理 | 无持久化 | BadgerDB 持久化 + CAS 原子操作 |
| 日志管理 | `tail` / `journalctl` | fsnotify + Zap + lumberjack |

---

## 升级步骤

1. **安装 Go 版本**
   ```powershell
   # 编译或下载 asa-server.exe
   go build -o asa-server.exe
   ```

2. **复制实例数据**（可选）
   - 将现有实例目录复制到 `{BaseDir}/instances/`
   - 已有的 `instance_config.ini` 文件兼容

3. **安装 ARK 服务器**
   ```powershell
   .\asa-server.exe update
   ```

4. **启动使用**
   ```powershell
   # GUI 模式
   .\asa-server.exe

   # 或 API 模式
   .\asa-server.exe api
   ```
