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
- [ArkApi 插件](#arkapi-插件)
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

实时流式推送宿主机整机指标与所有运行中实例的资源信息（每 2 秒）。

数据全部来自 `pkg/serverinfo` 的进程内单例采样器，不随连接数重复采样。

**SSE 事件格式**（`disk_io` / `net_io` 采不到时为 `null`，与「速率为 0」区分）:
```jsonc
{
  "timestamp": 1730000000,
  "cpu_cores": 16,
  "running_count": 1,
  "host": {
    "cpu":     { "used_percent": 8.56, "core_count": 16 },
    "memory":  { "used": 12884901888, "total": 34359738368, "used_percent": 37.5 },
    "disk_io": { "read_bytes_per_sec": 0, "write_bytes_per_sec": 5242880,
                 "read_iops": 0, "write_iops": 487 },
    "net_io":  { "recv_bytes_per_sec": 1048576, "sent_bytes_per_sec": 262144 }
  },
  "memory": { "total": 34359738368, "total_gb": 32 },   // 兼容字段，旧前端仍在读
  "instances": [
    {
      "instance": "server1", "running": true, "pid": 12345,
      "cpu_percent": 42.1,            // 单核 100% 口径，多核可 >100
      "cpu_total_percent": 2.6,       // 整机占比 = cpu_percent / 核数
      "memory_used": 6442450944, "memory_percent": 18.7,
      "process_name": "ArkAscendedServer.exe",
      "memory_used_mb": 6144, "memory_used_gb": 6,
      "disk_io": { "read_bytes_per_sec": 131072, "write_bytes_per_sec": 65536,
                   "read_iops": 12, "write_iops": 6 },
      "net_io": null                  // 采不到时为 null：Windows 需 ETW 权限，Linux 需 eBPF 可用
    }
  ]
}
```

### `GET /api/server/metrics/history`

回填接口：返回最近一段时间的采样历史，供趋势图在挂载时一次性灌满缓冲。
后端保留 **30 分钟**（内存环形缓冲，每 5 分钟落一次 Badger，重启后自动恢复）。

**查询参数**:

| 参数 | 说明 |
|---|---|
| `window` | 秒，缺省 900（15 分钟），上限 1800 |
| `instance` | 可选；不传只回 host，传了额外返回该实例的列 |

**响应**（列存，`null` 表示该点采不到）:
```jsonc
{
  "timestamps": [1730000000, 1730000002],
  "host": {
    "cpu_used_percent": [8.5, 9.1], "mem_used_percent": [37.5, 37.6],
    "disk_read_bytes_per_sec": [0, 0], "disk_write_bytes_per_sec": [5242880, 4194304],
    "disk_read_iops": [0, 0], "disk_write_iops": [487, 402],
    "net_recv_bytes_per_sec": [1048576, 999424], "net_sent_bytes_per_sec": [262144, 251904]
  },
  "instance": {                                  // 仅当传了 instance
    "cpu_percent": [42.1, 40.0], "cpu_total_percent": [2.6, 2.5],
    "memory_percent": [18.7, 18.7], "memory_used": [6442450944, 6442450944],
    "disk_read_bytes_per_sec": [131072, null], "disk_write_bytes_per_sec": [65536, null],
    "disk_read_iops": [12, null], "disk_write_iops": [6, null],
    "net_recv_bytes_per_sec": [null, null], "net_sent_bytes_per_sec": [null, null]
  }
}
```

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

### `POST /api/backup/world/:name`

为指定实例创建世界存档备份（仅 `Save/` 目录，不含实例/游戏配置——配置备份由配置同步功能负责）。

**格式**: `.zstd`（tar + zstd 压缩）
**命名**: `{instanceName}_{timestamp}.zstd`

### `GET /api/backup/world/:name`

列出指定实例的所有可用世界存档备份。

**响应**:
```json
{
  "count": 5,
  "backups": ["server1_20260618_120000.zstd", ...]
}
```

### `POST /api/backup/world/:name/restore`

恢复世界存档备份到实例（仅恢复世界存档）。如果实例不存在会自动创建。

**请求体**:
```json
{
  "backup_file": "server1_20260618_120000.zstd"
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

## ArkApi 插件

ArkApi **主程序**全局一份，装在 `server-files` 的 `Win64` 里，由各实例的 `EnableAsaPlugin` 决定用不用；
**插件**按实例独立存放在 `{BaseDir}/instances/{name}/ArkApi/Plugins/`，镜像里的 `ArkApi/Plugins` 是指向它的 junction。
见 `docs/ARKAPI_PLUGIN_INSTALL_PLAN.md`。

- 返回一律套 `{success, data, message, error}` 信封。
- 往服务器上放 dll 等同于在服务器上执行代码：上传、确认安装、卸载要求**管理员**（未开鉴权时不拦）；
  启用/禁用、改插件配置与编辑实例配置同级，登录即可。
- 安装、更新、卸载插件要求目标实例**已停止**（运行中的 ArkApi 占着 dll）；启用/禁用运行中也能改，下次启动生效。
- 接口里**没有「默认全部」**：插件操作只作用于请求里显式列出的实例。

### `GET /api/plugins/:name`

实例的插件列表。

```json
{
  "success": true,
  "data": {
    "layout": "instance",
    "plugins_dir": "E:\\asa_server_data\\instances\\meijue-pve\\ArkApi\\Plugins",
    "arkapi_installed": true,
    "count": 1,
    "plugins": [{
      "name": "Permissions",
      "full_name": "Ark:SA Permissions",
      "version": "1.1",
      "description": "Manage permissions groups",
      "min_api_version": "1.19",
      "enabled": true,
      "pending": false,
      "dll_missing": false,
      "has_config": true,
      "data_files": [{"name": "ArkDB.db", "size": 204800, "modified": "…", "is_sqlite": true}],
      "snapshots": [],
      "external_db_path": ""
    }]
  }
}
```

- `layout: "legacy"`：该实例升级时正在运行、尚未迁移到独立插件目录，列出的是 server-files 里的全局插件（只读）。
  停止后下次启动时自动迁移。
- `enabled` 取自实例配置的禁用列表；`pending: true` 表示开关改过了、插件目录还没挪到位（实例运行中改的）。
- `version` 等字段保留 `PluginInfo.json` 的原文（`1.10` 不会变成 `1.1`）。
- `dll_missing: true`：插件目录里没有 `<插件名>.dll`，ArkApi 不会加载它（典型是卸载后残留的配置与数据）。

### `GET /api/plugins/:name/:plugin/config`

读该实例插件目录里的 `config.json`：`{"content": "…", "seeded": true}`。`seeded: false` 只在旧布局下出现，表示展示的是默认值。

### `PUT /api/plugins/:name/:plugin/config`

请求体 `{"content": "…"}`，必须是合法 JSON。写进该实例的插件目录，下次启动该实例时生效。

### `PUT /api/plugins/:name/:plugin/enabled`

请求体 `{"enabled": false}`，只作用于这一个实例。返回 `data.applied`：`true` 表示已经落位（实例已停止）；
`false` 表示实例运行中或正在启动，只写了配置，下次启动时落位。

### `GET /api/arkapi`

主程序状态。

```json
{"success": true, "data": {
  "installed": true, "managed": true,
  "version": "2.03", "source": "AsaApi_2.03.zip", "installed_at": "2026-09-11T10:00:00+08:00",
  "modified_files": [], "missing_files": [],
  "overwritten": ["msvcp140.dll"],
  "busy": false
}}
```

- `installed` 以 server-files 里有没有 `AsaApiLoader.exe` 为准；`managed` 表示由本程序安装
  （有清单 `{BaseDir}/arkapi/manifest.json`），手工装的版本未知。
- `modified_files` / `missing_files`：安装之后被外部改动或删掉的文件（按 sha256 比对；`config.json` 是给用户改的，不算）。
- `overwritten`：被主程序覆盖、卸载时会还原的游戏自带文件。
- `busy`：server-files 正在被改写（Steam 更新或另一个主程序操作）。

### `DELETE /api/arkapi`  *(管理员)*

卸载主程序，影响所有实例。各实例的插件目录**完全不动**：镜像里的插件链接在下次同步时移除，主程序装回后自动恢复。

- 有清单：清单里的文件移入 `{BaseDir}/arkapi/backups/core-<时间戳>/`，被覆盖的游戏文件从原件还原；
  安装之后又被外部换过的（多半是 Steam 校验）保留现状，原件进备份。
- 无清单（手工装的）：按固定清单移除 `AsaApiLoader.exe/.pdb`、`libcrypto-3-x64.dll`、`libssl-3-x64.dll`、`msdia140.dll`、
  `Lib/AsaApi.lib`、看起来是 ArkApi 的 `config.json`；`msvcp140.dll` **保留**（无法判断来源）。
- `ArkApi/` 下剩余的内容（含 `Cache/`）移入备份；`ArkApi/Plugins/` 不空（有实例尚未迁移）时保留不动。

返回 `{"managed": true, "removed": […], "restored": ["msvcp140.dll"], "warnings": […], "backup": "…"}`。
server-files 正忙时返回 400。

### `POST /api/arkapi/packages?kind=core|plugin[&expect=<插件名>]`  *(管理员)*

上传 zip（`multipart/form-data`，字段名 `file`，上限 256 MiB），解压到暂存区并校验，返回报告与 `token`（30 分钟有效）。
`expect`：从某一行点「更新」时带上，包里的插件名与之不同就拒绝。

- 校验失败返回 **422**，`data` 与成功时同形、`token` 为空，`errors` 逐条列出原因；超过上限返回 **413**。

插件包的报告：

```json
{"success": true, "data": {
  "token": "…", "expires_at": "…", "kind": "plugin",
  "name": "TidyDamsASA", "full_name": "TidyDamsASA", "version": "1.3",
  "description": "No more only wood in beaver dams!", "min_api_version": "2", "dependencies": [],
  "files": [{"path": "TidyDamsASA.dll", "size": 179712}],
  "errors": [], "warnings": [],
  "targets": [
    {"instance": "meijue-pve", "running": true, "installed": true, "installed_version": "1.2", "enabled": true,
     "action": "update", "blocked": "实例处于 started 状态，请先停止"},
    {"instance": "meijue-2", "running": false, "installed": false, "installed_version": "", "enabled": true,
     "action": "install", "backup_available": true}
  ]
}}
```

主程序包的报告：`kind: "core"`；`version`（从文件名提取）、`action`（`install` / `update`）、`current`（同 `GET /api/arkapi`）、
`files`、`ignored`、`overwrites`（会被覆盖的游戏自带文件，如 `msvcp140.dll`）、`bundled`（包里 `ArkApi/Plugins/*` 的附带插件，
每个都是一份插件报告）、`bundled_targets`（按插件名的目标实例表，形状同上面的 `targets`）。

### `POST /api/arkapi/packages/:token/apply`  *(管理员)*

确认安装，按暂存包的类型取用请求体里的字段：

| 包 | 请求体 | 说明 |
|---|---|---|
| 插件 | `{"targets": ["a", "b"], "restore_from_backup": true}` | `targets` 必须显式给出，空列表 400。`restore_from_backup` 对「新装」且有上次卸载留下的备份的实例生效 |
| 主程序 | `{"version": "2.03", "bundled": {"Permissions": ["a"]}, "restore_from_backup": false}` | `version` 不传则沿用从文件名提取的；`bundled` 不传则附带插件都不装 |

- 插件包返回 `{"results": [{"instance": "a", "ok": true, "action": "install"}, {"instance": "b", "ok": false, "error": "…"}]}`。
  每个实例独立执行，一个失败不影响其他；有失败时 `success: false`，HTTP 仍是 200。
- 主程序包返回 `{"action": "update", "version": "2.03", "warnings": […], "backup": "…", "results": [{"instance": "a", "plugin": "Permissions", "ok": true, "action": "install"}]}`。
  换位中途失败时整体回滚，返回 400；server-files 正忙或附带插件的选择不合法时返回 400 且**暂存包保留**，可以稍后再确认。
- 暂存包用过即失效：插件包不论成败，主程序包从开始换位起。

### `DELETE /api/arkapi/packages/:token`  *(管理员)*

放弃暂存的包（关掉确认对话框时调用）。不存在时同样返回成功。

### `GET /api/arkapi/plugins/:plugin/instances`

装有该插件的全部实例（含处于禁用状态的），形状同上面的 `targets`（没有 `action`），供卸载对话框使用。

### `POST /api/arkapi/plugins/:plugin/uninstall`  *(管理员)*

请求体 `{"targets": ["a", "b"]}`，必须显式给出。每个实例的插件目录整个移入该实例的 `ArkApi/Backups/`
（配置与数据随之保留，重新安装时可以恢复），并从该实例的禁用列表里移除。返回同插件包 apply 的 `results`。

---

## FRP 管理

FRP（Fast Reverse Proxy）反向代理管理。frpc **库内调用**（`github.com/fatedier/frp/client`），
没有 `frpc.exe`，也没有 `frpc.toml` —— 配置是结构化参数，落 `{BaseDir}/frp/frpc.json`。
见 `docs/FRP_FORM_CONFIG_PLAN.md`。

### `GET /api/frp/config`

获取当前 FRP 配置。未配置过时返回一份空配置（`rules: []`），不是 404。

```json
{
  "success": true,
  "data": {
    "server_addr": "47.97.22.91",
    "server_port": 7000,
    "token": "…",
    "rules": [
      { "start": 9310, "end": 9319, "protocol": "udp", "remark": "游戏端口" }
    ]
  }
}
```

### `PUT /api/frp/config`

更新 FRP 配置。请求体就是上面的 `data` 对象（**不是**配置文件文本）。

- `server_addr`：`host` 或 `host:port`；带端口时与 `server_port` 冲突会报错
- `server_port`：省略/0 → 7000
- `protocol`：`tcp` | `udp` | `tcp+udp`；远端端口恒等于本地端口
- 上限：单条规则 ≤ 64 个端口，展开后总代理数 ≤ 128
- 同协议的端口区间不得重叠（会展开出同名代理，frps 侧登记冲突）

校验失败返回 **400** 且错误文案指出是第几条规则；落盘/热更新失败返回 500。
成功时 `data.warnings` 可能带非阻塞提示（如未设置 token）。

**生效方式分两条**：只改端口规则时走热更新，已建立的隧道不断开；
改地址/端口/token 时会重新连接 frps。

### `GET /api/frp/status`

获取 FRP 运行状态。与下面的 SSE 流返回**同一个** payload：

```json
{
  "success": true,
  "data": {
    "running": true,
    "configured": true,
    "message": "",
    "proxy_count": 10,
    "proxies": [
      { "name": "udp-asaserver-9310", "type": "udp", "local_port": 9310,
        "phase": "running", "remote_addr": "47.97.22.91:9310" }
    ]
  }
}
```

`message` 与 `proxies[].err` 分工不同：前者是**连不上 frps**（整体失败），
后者是**连上了但这条代理没起来**（局部失败，例如范围里某个端口在 frps 侧已被占用）。
`proxies` 只在运行中才有。

### `GET /api/frp/status/stream`  *(SSE)*

推送与 `GET /api/frp/status` 同形的对象。**仅在内容变化时推送**（外加首帧与 25s 心跳注释帧）。

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
| REST (JSON) | 56 | 实例 CRUD、配置、备份、RCON、Mod 信息、健康检查、存档解析、ArkApi 插件、FRP、Syncthing |
| SSE | 9 | 服务器信息流、日志推送、更新/批量任务进度、存档数据流、FRP/Syncthing 状态流 |
| WebSocket | 2 | 全局服务器事件广播、交互式 RCON |
| **合计** | **67** | |
