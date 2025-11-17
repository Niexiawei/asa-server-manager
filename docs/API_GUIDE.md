# ASA Server Manager API 指南

## 启动 API 服务器

```bash
asa-manager api --port 8080
```

默认端口是 `8080`，你可以通过 `--port` 参数修改。

---

## API 端点列表

### 健康检查
- **GET** `/health`
  - 检查 API 服务器是否正常运行

### 实例管理

#### 列出所有实例
- **GET** `/api/instances`
  - 返回所有可用实例及其运行状态

#### 创建新实例
- **POST** `/api/instances`
  - 请求体：
    ```json
    {
      "name": "my-instance"
    }
    ```

#### 获取实例状态
- **GET** `/api/instances/:name`
  - 返回指定实例的详细信息和运行状态

#### 删除实例
- **DELETE** `/api/instances/:name`
  - 删除指定实例及其所有数据

#### 重命名实例
- **PUT** `/api/instances/:name`
  - 请求体：
    ```json
    {
      "new_name": "new-instance-name"
    }
    ```

### 服务器控制

#### 启动服务器
- **POST** `/api/server/:name/start`
  - 启动指定实例的服务器

#### 停止服务器
- **POST** `/api/server/:name/stop`
  - 停止指定实例的服务器

#### 重启服务器
- **POST** `/api/server/:name/restart`
  - 重启指定实例的服务器

#### 启动所有服务器
- **POST** `/api/server/start-all`
  - 启动所有实例的服务器

#### 停止所有服务器
- **POST** `/api/server/stop-all`
  - 停止所有实例的服务器

### RCON 命令

#### 发送 RCON 命令
- **POST** `/api/rcon/:name/command`
  - 请求体：
    ```json
    {
      "command": "ListPlayers"
    }
    ```
  - 向指定实例发送 RCON 命令

### 备份和还原

#### 创建备份
- **POST** `/api/backup/:name`
  - 请求体：
    ```json
    {
      "world_folder": "TheIsland_WP"
    }
    ```
  - 为指定实例创建世界备份

#### 列出所有备份
- **GET** `/api/backup`
  - 返回所有可用的备份文件列表

#### 恢复备份
- **POST** `/api/backup/:name/restore`
  - 请求体：
    ```json
    {
      "backup_file": "/path/to/backup.tar.gz"
    }
    ```
  - 将备份恢复到指定实例

### 服务器更新

#### 更新服务器
- **POST** `/api/server/update`
  - 查询参数：
    - `force-server` (可选): `true` 或 `false` (默认: false)
  - 示例：`POST /api/server/update?force-server=true`
  - 下载并更新 ARK 服务器文件

---

## 响应格式

所有 API 响应都遵循以下格式：

```json
{
  "success": true,
  "message": "操作成功的描述",
  "data": {
    // 可选：具体的响应数据
  },
  "error": "错误信息（仅在失败时出现）"
}
```

### 列表实例的响应示例

```json
{
  "success": true,
  "message": "Instances retrieved successfully",
  "data": {
    "instances": [
      {
        "name": "island-1",
        "running": true,
        "config": {
          "ServerName": "My ARK Server",
          "Port": 7777,
          "RCONPort": 27020,
          "MaxPlayers": 70
        }
      },
      {
        "name": "island-2",
        "running": false
      }
    ],
    "count": 2
  }
}
```

---

## 使用示例

### 使用 curl

#### 列出所有实例
```bash
curl http://localhost:8080/api/instances
```

#### 创建新实例
```bash
curl -X POST http://localhost:8080/api/instances \
  -H "Content-Type: application/json" \
  -d '{"name":"new-server"}'
```

#### 启动服务器
```bash
curl -X POST http://localhost:8080/api/server/island-1/start
```

#### 发送 RCON 命令
```bash
curl -X POST http://localhost:8080/api/rcon/island-1/command \
  -H "Content-Type: application/json" \
  -d '{"command":"ListPlayers"}'
```

#### 创建备份
```bash
curl -X POST http://localhost:8080/api/backup/island-1 \
  -H "Content-Type: application/json" \
  -d '{"world_folder":"TheIsland_WP"}'
```

### 使用 Python

```python
import requests
import json

base_url = "http://localhost:8080"

# 列出实例
response = requests.get(f"{base_url}/api/instances")
print(json.dumps(response.json(), indent=2))

# 创建实例
data = {"name": "new-server"}
response = requests.post(f"{base_url}/api/instances", json=data)
print(response.json())

# 启动服务器
response = requests.post(f"{base_url}/api/server/new-server/start")
print(response.json())

# 发送 RCON 命令
data = {"command": "ListPlayers"}
response = requests.post(f"{base_url}/api/rcon/new-server/command", json=data)
print(response.json())
```

---

## CLI 命令映射

| CLI 命令 | HTTP 方法 | API 端点 | 说明 |
|---------|---------|---------|------|
| `list` | GET | `/api/instances` | 列出所有实例 |
| `create` | POST | `/api/instances` | 创建新实例 |
| `start <name>` | POST | `/api/server/:name/start` | 启动服务器 |
| `stop <name>` | POST | `/api/server/:name/stop` | 停止服务器 |
| `restart <name>` | POST | `/api/server/:name/restart` | 重启服务器 |
| `status [name]` | GET | `/api/instances` 或 `/api/instances/:name` | 检查状态 |
| `rcon <name> <cmd>` | POST | `/api/rcon/:name/command` | 发送 RCON 命令 |
| `delete <name>` | DELETE | `/api/instances/:name` | 删除实例 |
| `rename <name>` | PUT | `/api/instances/:name` | 重命名实例 |
| `backup <name> <folder>` | POST | `/api/backup/:name` | 创建备份 |
| `restore <name>` | POST | `/api/backup/:name/restore` | 恢复备份 |
| `start-all` | POST | `/api/server/start-all` | 启动所有服务器 |
| `stop-all` | POST | `/api/server/stop-all` | 停止所有服务器 |
| `update` | POST | `/api/server/update` | 更新服务器 |

