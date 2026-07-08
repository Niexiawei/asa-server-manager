# 速查表

ASA Server Manager 命令、配置、常用操作速查。

---

## CLI 命令

### 基本用法

```powershell
# GUI 模式（默认）
.\asa-server.exe

# API 服务器
.\asa-server.exe api
.\asa-server.exe api --port 19193

# 安装/更新 ARK 服务器
.\asa-server.exe update
.\asa-server.exe update --force-server   # 强制重新验证
```

### Windows 服务

```powershell
# 安装为 Windows 服务
.\asa-server.exe service install

# 启动/停止/卸载服务
.\asa-server.exe service start
.\asa-server.exe service stop
.\asa-server.exe service remove
```

### 全局参数

| 参数 | 别名 | 默认值 | 说明 |
|------|------|--------|------|
| `--api-port` | `--port` | 19193 | HTTP API 端口 |

---

## 实例配置 (instance_config.ini)

| 字段 | 类型 | 说明 |
|------|------|------|
| `ServerName` | string | 服务器名称 |
| `ServerPassword` | string | 服务器密码 |
| `ServerAdminPassword` | string | 管理员密码 |
| `MaxPlayers` | int | 最大玩家数 |
| `MapName` | string | 地图名称 |
| `RCONPort` | int | RCON 端口 |
| `QueryPort` | int | 查询端口 |
| `Port` | int | 游戏端口 |
| `ModIDs` | string | Mod ID 列表（逗号分隔） |
| `SaveDir` | string | 存档目录 |
| `ClusterID` | string | 集群 ID |
| `CustomStartParameters` | string | 自定义启动参数 |
| `EnableAsaPlugin` | bool | 是否启用 ASA 插件 |
| `BindDomain` | string | 绑定域名 |
| `MessageOfTheDay` | string | 每日消息（MOTD） |
| `MessageOfTheDayDuration` | int | MOTD 显示时长 |

---

## 目录结构

```
{BaseDir}/
├── instances/                         # 实例目录
│   └── {instance_name}/
│       ├── instance_config.ini        # 实例配置
│       ├── Config/                    # NTFS junction
│       │   ├── Game.ini
│       │   └── GameUserSettings.ini
│       └── server.log
├── server-files/                      # ARK 服务器安装
├── steamcmd/                          # SteamCMD
├── backups/                           # 备份（.zstd）
├── frp/                               # frpc.exe + 配置
├── syncthing/                         # syncthing.exe + 配置
├── database_file/                     # BadgerDB 状态数据
│   └── state_db/
├── logs/                              # 应用日志
│   ├── asaServer.log
│   └── arkApiLog.log
└── log_mapping.json                   # 实例→日志映射
```

---

## 实例状态

| 状态 | 说明 | 可执行操作 |
|------|------|-----------|
| `stopped` | 已停止 | 启动 |
| `started` | 运行中 | 停止、重启、强制停止 |
| `start_initialization` | 启动初始化中 | 强制停止 |
| `start_initialization_successful` | 初始化成功 | 强制停止 |
| `starting` | 启动中 | 强制停止 |
| `stopping` | 停止中 | 强制停止 |
| `restarting` | 重启中 | 强制停止 |
| `start_failed` | 启动失败 | 启动、强制停止 |
| `stop_failed` | 停止失败 | 启动、强制停止 |
| `restart_failed` | 重启失败 | 启动、强制停止 |

---

## RCON 常用命令

```powershell
# 通过 API 发送 RCON 命令
Invoke-RestMethod -Method Post http://localhost:19193/api/rcon/server1/command `
  -Body '{"command":"broadcast Hello"}' -ContentType "application/json"
```

| RCON 命令 | 说明 |
|-----------|------|
| `broadcast <msg>` | 广播消息 |
| `saveworld` | 保存世界 |
| `DoExit` | 关闭服务器 |
| `ListPlayers` | 列出在线玩家 |
| `KickPlayer <id>` | 踢出玩家 |
| `BanPlayer <id>` | 封禁玩家 |
| `serverchat <msg>` | 服务器聊天 |
| `SetMessageOfTheDay <msg>` | 设置 MOTD |

---

## 常见问题排查

### 端口冲突

```powershell
# 检查端口占用
netstat -ano | findstr :27015

# 检查所有实例配置的端口
Get-ChildItem instances\*\instance_config.ini | Select-String "Port|RCONPort|QueryPort"
```

### 服务器无法启动

1. 检查 `server-files/` 目录是否完整
2. 检查端口是否被占用
3. 检查实例状态是否卡在中间状态（等待自动恢复或重启程序）
4. 查看日志：`logs/asaServer.log`

### 日志位置

| 日志 | 路径 |
|------|------|
| 应用日志 | `{BaseDir}/logs/asaServer.log` |
| ARK API 日志 | `{BaseDir}/logs/arkApiLog.log` |
| 实例日志 | `{BaseDir}/instances/{name}/server.log` |
| 日志映射 | `{BaseDir}/log_mapping.json` |

### 备份格式

备份文件使用 `.zstd` 格式（tar + zstd 压缩，仅世界存档），存放在 `{BaseDir}/backups/` 目录。

---

## API 速查

完整的 API 端点文档请参阅 [API_REFERENCE.md](API_REFERENCE.md)。

### 常用 curl 示例

```powershell
# 健康检查
curl http://localhost:19193/health

# 列出实例
curl http://localhost:19193/api/instances

# 创建实例
curl -X POST http://localhost:19193/api/instances -d '{"name":"server1"}'

# 启动实例
curl http://localhost:19193/api/server/server1/start

# 停止实例
curl http://localhost:19193/api/server/server1/stop

# 获取实例配置
curl http://localhost:19193/api/instances/server1/config

# 创建备份
curl -X POST http://localhost:19193/api/backup/server1

# 更新服务器
curl http://localhost:19193/api/server/update
```
