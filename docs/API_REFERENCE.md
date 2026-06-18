# HTTP API 参考

ASA Server Manager 完整 HTTP API 参考文档。

**Base URL**: `http://localhost:19193`
**默认端口**: 19193（可通过 `--port` 参数修改）

---

## 目录

- [健康检查](#健康检查)
- [实例管理](#实例管理)
- [服务器控制](#服务器控制)
- [RCON](#rcon)
- [备份/恢复](#备份恢复)
- [日志流](#日志流)
- [配置管理](#配置管理)
- [存档解析](#存档解析)
- [Mod 信息](#mod-信息)
- [FRP 管理](#frp-管理)
- [Syncthing 管理](#syncthing-管理)
- [WebSocket](#websocket)
- [通用说明](#通用说明)

---

## 健康检查

### `GET /health`

检查 API 服务是否正常运行。

**响应**:
```json
{
  "status": "healthy",
  "service": "asa-server-manager-api"
}
```

---

## 实例管理

### `GET /api/instances`

获取所有实例列表，包含运行状态、配置和状态历史。

**响应**:
```json
[
  {
    "name": "server1",
    "running": true,
    "config": { ... },
    "state_history": [ ... ]
  }
]
```

### `POST /api/instances`

创建新实例。

**请求体**:
```json
{
  "name": "my-server"
}
```

**说明**: 复制基础服务器配置文件并创建默认实例配置。

### `GET /api/instances/:name`

获取指定实例状态。

**参数**: `name` — 实例名称（路径参数）

**响应**: 包含运行状态、配置、状态历史的 JSON 对象。

### `DELETE /api/instances/:name`

删除实例。如果正在运行会先停止，然后删除实例目录和存档目录。

### `PUT /api/instances/:name`

重命名实例。

**请求体**:
```json
{
  "new_name": "new-server-name"
}
```

**说明**: 如果服务器正在运行会先停止，然后重命名目录并更新配置。

### `GET /api/instances/:name/config`

获取实例配置详情（端口、路径、RCON 密码等）。

### `PATCH /api/instances/:name/config`

更新实例配置（部分更新，仅传需要修改的字段）。

**请求体**:
```json
{
  "server_name": "New Name",
  "max_players": 70,
  "rcon_port": 27020
}
```

---

## 服务器控制

### `GET /api/server/:name/start`

启动单个实例。异步执行，检查端口冲突和操作状态，通过 WebSocket 广播事件。

### `GET /api/server/:name/stop`

停止单个实例。异步执行，发送 `saveworld` 后 `DoExit`。

### `GET /api/server/:name/restart`

重启单个实例。异步执行，先停止再启动。

### `GET /api/server/:name/force-stop`

强制停止实例，绕过状态检查，直接终止进程并重置状态。

### `GET /api/server/:name/info`  *(SSE)*

实时流式推送单个实例的 CPU/内存/进程信息（每 2 秒）。

**SSE 事件格式**:
```
data: {"cpu_usage": 15.2, "memory_usage": 2048000000, "pid": 12345}
```

### `GET /api/server/start-all`  *(SSE)*

启动所有实例，通过 SSE 流式推送每个实例的启动进度。

### `GET /api/server/stop-all`  *(SSE)*

停止所有实例，通过 SSE 流式推送进度。

### `GET /api/server/restart-all`  *(SSE)*

重启所有实例，通过 SSE 流式推送进度。

### `GET /api/server/update`  *(SSE)*

通过 SteamCMD 下载/更新 ARK 服务器，SSE 流式推送下载进度和日志。

### `GET /api/server/info`  *(SSE)*

实时流式推送系统级 CPU/内存信息（每 2 秒）。

**SSE 事件格式**:
```
data: {"cpu_usage": 35.0, "memory_total": 17179869184, "memory_used": 8589934592}
```

### `GET /api/server/all-info`  *(SSE)*

实时流式推送所有运行中实例的 CPU/内存信息（每 2 秒）。

---

## RCON

### `POST /api/rcon/:name/command`

向运行中的实例发送 RCON 命令。

**请求体**:
```json
{
  "command": "broadcast Hello World"
}
```

**响应**:
```json
{
  "success": true,
  "response": "Server: Hello World"
}
```

---

## 备份/恢复

### `POST /api/backup/:name`

为指定实例创建世界备份。

**格式**: `.tar.zstd`（tar + zstd 压缩）
**命名**: `{instanceName}_{timestamp}.tar.zstd`

### `GET /api/backup`

列出所有可用备份。

**响应**:
```json
{
  "count": 5,
  "backups": ["server1_20260618_120000.tar.zstd", ...]
}
```

### `POST /api/backup/:name/restore`

恢复备份到实例。如果实例不存在会自动创建。

**请求体**:
```json
{
  "backup_file": "server1_20260618_120000.tar.zstd",
  "restore_worldfile": true,
  "restore_instance_config": true,
  "restore_game_config": false
}
```

---

## 日志流

### `GET /api/logs/:name`  *(SSE)*

实时推送实例日志，tail 方式流式传输新行。如果日志文件不存在，最多等待 120 秒。

### `GET /api/logs`  *(SSE)*

实时推送系统日志，连接时返回最近 500 行，之后 tail 新行。

---

## 配置管理

### `GET /api/config/server/configs`

获取基础服务器目录的 Game.ini 和 GameUserSettings.ini。

### `GET /api/config/:name/configs`

获取指定实例的 Game.ini 和 GameUserSettings.ini。

### `GET /api/config/:name/game-ini`

获取实例的 Game.ini 内容。

### `GET /api/config/:name/game-user-settings`

获取实例的 GameUserSettings.ini 内容。

### `POST /api/config/:name/game-ini`

上传 Game.ini 文件（multipart form，最大 10MB），覆盖已有文件。

### `POST /api/config/:name/game-user-settings`

上传 GameUserSettings.ini 文件（multipart form，最大 10MB），覆盖已有文件。

### `PUT /api/config/:name/game-ini`

直接更新 Game.ini 内容。

**请求体**:
```json
{
  "content": "[/Script/ShooterGame.ShooterGameMode]\nMaxPlayers=70\n..."
}
```

### `PUT /api/config/:name/game-user-settings`

直接更新 GameUserSettings.ini 内容。

**请求体**:
```json
{
  "content": "[ServerSettings]\nServerPassword=mypass\n..."
}
```

### `POST /api/config/sync`

将基础服务器的 Game.ini 和 GameUserSettings.ini 同步到指定实例。

**请求体**:
```json
{
  "instances": ["server1", "server2", "server3"]
}
```

### `POST /api/config/sync-instance`

在实例之间同步配置。

**请求体**:
```json
{
  "source_instance": "server1",
  "target_instances": ["server2", "server3"],
  "sync_custom_start_parameters": true,
  "sync_enable_asa_plugin": false,
  "only_sync_server_game_ini_config": false
}
```

---

## 存档解析

### `GET /api/save/:instance/players`

解析 `.ark` 存档文件，返回玩家数据。

### `GET /api/save/:instance/tribes`

解析 `.ark` 存档文件，返回部落数据。

### `GET /api/save/:instance/all`

返回所有解析的存档数据（玩家 + 部落）。优先使用 SaveDataManager 缓存。

### `GET /api/save/:instance/stream`  *(SSE)*

实时推送存档数据变更。连接时立即发送缓存数据。

---

## Mod 信息

### `GET /api/mod-info`

读取并返回基础目录下的 `mod_info.json` 文件内容。

---

## FRP 管理

FRP（Fast Reverse Proxy）反向代理管理，内嵌 `frpc.exe`。

### `GET /api/frp/config`

获取当前 FRP 客户端配置。

### `PUT /api/frp/config`

更新 FRP 客户端配置。

### `GET /api/frp/status`

获取 FRP 客户端运行状态。

### `GET /api/frp/status/stream`  *(SSE)*

实时推送 FRP 客户端状态变更。

### `POST /api/frp/start`

启动 FRP 客户端。

### `POST /api/frp/stop`

停止 FRP 客户端。

### `POST /api/frp/restart`

重启 FRP 客户端。

---

## Syncthing 管理

Syncthing 文件同步管理，内嵌 `syncthing.exe`。

### `GET /api/syncthing/config`

获取当前 Syncthing 配置。

### `PUT /api/syncthing/config`

更新 Syncthing 配置。

### `GET /api/syncthing/status`

获取 Syncthing 运行状态。

### `GET /api/syncthing/status/stream`  *(SSE)*

实时推送 Syncthing 状态变更。

### `POST /api/syncthing/start`

启动 Syncthing 服务。

### `POST /api/syncthing/stop`

停止 Syncthing 服务。

### `POST /api/syncthing/restart`

重启 Syncthing 服务。

---

## WebSocket

### `GET /api/ws/events`

全局服务器事件广播通道。所有实例的生命周期事件通过此通道推送。

**事件类型**:
| 事件 | 说明 |
|------|------|
| `server_starting` | 实例开始启动 |
| `server_started` | 实例启动完成 |
| `server_stopping` | 实例开始停止 |
| `server_stopped` | 实例已停止 |
| `server_start_failed` | 实例启动失败 |
| `server_stop_failed` | 实例停止失败 |
| `server_restarting` | 实例开始重启 |
| `server_restarted` | 实例重启完成 |
| `server_restart_failed` | 实例重启失败 |
| `server_game_log_path` | 游戏日志路径变更 |
| `connected` | WebSocket 连接建立 |

**心跳**: Ping/Pong，超时 90 秒。

### `GET /api/ws/rcon`

双向 RCON 交互通道。

**客户端发送**:
```json
{
  "action": "command",
  "instance_name": "server1",
  "command": "broadcast Hello"
}
```

**服务端响应**:
```json
{
  "success": true,
  "response": "Server: Hello",
  "error": ""
}
```

**心跳**: Ping/Pong，超时 90 秒。

---

## 通用说明

### 响应格式

所有 REST 端点返回 JSON 格式。错误响应：

```json
{
  "error": "错误描述"
}
```

### SSE (Server-Sent Events)

SSE 端点以 `text/event-stream` 格式推送数据，每条消息格式：

```
data: {"key": "value"}

```

### 未匹配路由

- 以 `/api` 开头的未匹配路径返回 `404` JSON
- 其他路径返回内嵌 Vue.js SPA 的 `index.html`

### 端点统计

| 协议 | 数量 | 说明 |
|------|------|------|
| REST (JSON) | 45 | 实例 CRUD、配置、备份、RCON、Mod 信息、健康检查、存档解析、FRP、Syncthing |
| SSE | 9 | 服务器信息流、日志推送、更新/批量任务进度、存档数据流、FRP/Syncthing 状态流 |
| WebSocket | 2 | 全局服务器事件广播、交互式 RCON |
| **合计** | **56** | |
