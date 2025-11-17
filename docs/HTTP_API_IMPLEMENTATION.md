# HTTP API 实现总结

## 概述

已成功为 ASA Server Manager CLI 应用生成了对应的 HTTP REST API 接口，使用 **Gin Web Framework**。

---

## 文件清单

### 核心实现
- **`api.go`** - HTTP API 服务器实现，包含所有路由和处理程序
  - API 服务器初始化
  - 路由配置
  - 请求处理程序（16个）
  - 响应类型定义

### 配置文件
- **`go.mod`** - 更新了 Gin 框架依赖
- **`main.go`** - 添加了 `api` 命令用于启动 HTTP 服务器
- **`actions.go`** - 添加了 `actionAPI` 函数

### 文档
- **`API_GUIDE.md`** - 详细的 API 使用指南
  - API 端点列表
  - 请求/响应格式
  - 使用示例（curl、Python）
  - CLI 到 API 的映射表

- **`openapi.json`** - OpenAPI 3.0 规范
  - 完整的 API 定义
  - 可用于生成 SDK、文档和测试工具

### 测试脚本
- **`api_test_examples.sh`** - Bash 脚本测试所有 API 端点
- **`api_test_examples.ps1`** - PowerShell 脚本测试所有 API 端点

---

## API 端点总览

### 实例管理（5个端点）
| 方法 | 路径 | 功能 |
|------|------|------|
| GET | `/api/instances` | 列出所有实例 |
| POST | `/api/instances` | 创建新实例 |
| GET | `/api/instances/:name` | 获取实例状态 |
| DELETE | `/api/instances/:name` | 删除实例 |
| PUT | `/api/instances/:name` | 重命名实例 |

### 服务器控制（5个端点）
| 方法 | 路径 | 功能 |
|------|------|------|
| POST | `/api/server/:name/start` | 启动服务器 |
| POST | `/api/server/:name/stop` | 停止服务器 |
| POST | `/api/server/:name/restart` | 重启服务器 |
| POST | `/api/server/start-all` | 启动所有服务器 |
| POST | `/api/server/stop-all` | 停止所有服务器 |

### RCON 命令（1个端点）
| 方法 | 路径 | 功能 |
|------|------|------|
| POST | `/api/rcon/:name/command` | 发送 RCON 命令 |

### 备份管理（3个端点）
| 方法 | 路径 | 功能 |
|------|------|------|
| POST | `/api/backup/:name` | 创建备份 |
| GET | `/api/backup` | 列出备份 |
| POST | `/api/backup/:name/restore` | 恢复备份 |

### 服务器更新（1个端点）
| 方法 | 路径 | 功能 |
|------|------|------|
| POST | `/api/server/update` | 更新服务器 |

### 健康检查（1个端点）
| 方法 | 路径 | 功能 |
|------|------|------|
| GET | `/health` | 健康检查 |

**总计：16 个 API 端点**

---

## 启动 API 服务器

### 基本用法
```bash
./asa-server.exe api --port 8080
```

### 指定自定义端口
```bash
./asa-server.exe api --port 9090
```

### 输出示例
```
🚀 Starting API server on http://localhost:8080
```

---

## API 响应格式

所有 API 响应遵循标准 JSON 格式：

### 成功响应
```json
{
  "success": true,
  "message": "操作成功描述",
  "data": {
    // 具体响应数据
  }
}
```

### 错误响应
```json
{
  "success": false,
  "error": "错误信息"
}
```

---

## 使用示例

### 使用 curl

#### 列出所有实例
```bash
curl http://localhost:8080/api/instances
```

#### 创建实例
```bash
curl -X POST http://localhost:8080/api/instances \
  -H "Content-Type: application/json" \
  -d '{"name":"my-server"}'
```

#### 启动服务器
```bash
curl -X POST http://localhost:8080/api/server/my-server/start
```

### 使用 PowerShell
```powershell
# 列出实例
$response = Invoke-RestMethod -Uri "http://localhost:8080/api/instances" -Method Get
$response | ConvertTo-Json

# 创建实例
$body = @{name = "my-server"} | ConvertTo-Json
Invoke-RestMethod -Uri "http://localhost:8080/api/instances" -Method Post `
  -Headers @{"Content-Type"="application/json"} -Body $body
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
data = {"name": "my-server"}
response = requests.post(f"{base_url}/api/instances", json=data)
print(response.json())

# 启动服务器
response = requests.post(f"{base_url}/api/server/my-server/start")
print(response.json())
```

---

## CLI 到 API 的完整映射

| CLI 命令 | HTTP 方法 | API 端点 |
|---------|---------|---------|
| `list` | GET | `/api/instances` |
| `create` | POST | `/api/instances` |
| `manage <name>` | GET | `/api/instances/:name` |
| `start <name>` | POST | `/api/server/:name/start` |
| `stop <name>` | POST | `/api/server/:name/stop` |
| `restart <name>` | POST | `/api/server/:name/restart` |
| `status [name]` | GET | `/api/instances[/:name]` |
| `rcon <name> <cmd>` | POST | `/api/rcon/:name/command` |
| `delete <name>` | DELETE | `/api/instances/:name` |
| `rename <name>` | PUT | `/api/instances/:name` |
| `backup <name> <folder>` | POST | `/api/backup/:name` |
| `restore <name>` | POST | `/api/backup/:name/restore` |
| `start-all` | POST | `/api/server/start-all` |
| `stop-all` | POST | `/api/server/stop-all` |
| `config-restart` | - | 未实现 API |
| `update` | POST | `/api/server/update` |

---

## 集成指南

### 使用 OpenAPI 规范

项目包含 `openapi.json` 文件，可以用于：

1. **Swagger UI** - 交互式 API 文档
   ```bash
   # 在线查看：https://editor.swagger.io/
   # 导入 openapi.json 文件
   ```

2. **代码生成** - 自动生成各种语言的客户端
   ```bash
   # 使用 OpenAPI Generator
   openapi-generator-cli generate -i openapi.json -g python -o python-client/
   ```

3. **API 文档生成** - 生成专业的 API 文档
   ```bash
   # 使用 ReDoc
   redoc-cli bundle openapi.json -o api-docs.html
   ```

---

## 测试 API

### 使用提供的测试脚本

#### Bash 脚本（Linux/WSL）
```bash
bash api_test_examples.sh
```

#### PowerShell 脚本（Windows）
```powershell
.\api_test_examples.ps1
```

### 使用 Postman

1. 导入 `openapi.json` 到 Postman
2. 自动生成所有请求集合
3. 配置环境变量（如 `base_url`）
4. 运行请求测试

### 使用 cURL 测试

```bash
# 健康检查
curl -v http://localhost:8080/health

# 获取实例列表
curl -v http://localhost:8080/api/instances

# 创建实例（调试模式）
curl -v -X POST http://localhost:8080/api/instances \
  -H "Content-Type: application/json" \
  -d '{"name":"test-instance"}'
```

---

## 架构设计

### 代码结构

```
api.go
├── APIServer 结构体
│   ├── engine (*gin.Engine)
│   └── port (int)
│
├── 初始化函数
│   ├── NewAPIServer()
│   └── setupRoutes()
│
├── 响应类型
│   ├── StatusResponse
│   ├── InstanceInfo
│   ├── ListResponse
│   └── 请求类型
│
└── 处理程序（16 个）
    ├── health()
    ├── listInstances()
    ├── createInstance()
    ├── getInstanceStatus()
    ├── deleteInstance()
    ├── renameInstance()
    ├── startServer()
    ├── stopServer()
    ├── restartServer()
    ├── startAllServers()
    ├── stopAllServers()
    ├── sendRCONCommand()
    ├── backupInstance()
    ├── listBackups()
    ├── restoreBackup()
    └── updateServer()
```

### 路由组织

- `/health` - 健康检查
- `/api/instances` - 实例管理
- `/api/server` - 服务器控制
- `/api/rcon` - RCON 命令
- `/api/backup` - 备份管理

---

## 安全考虑

1. **输入验证** - 所有请求都进行了 JSON 绑定验证
2. **错误处理** - 完整的错误处理和错误消息
3. **操作原子性** - 关键操作被正确处理（如删除前停止服务器）
4. **日志记录** - 所有操作都有相应的日志输出（继承自 CLI）

### 建议的安全增强

1. 添加认证（如 API 密钥、JWT）
2. 添加速率限制
3. 添加请求日志记录
4. 使用 HTTPS（在生产环境）
5. 添加 CORS 配置

---

## 性能特性

- 使用 Gin 框架，高性能路由引擎
- 异步处理，不阻塞长时间操作
- 支持并发请求
- 低内存占用

---

## 扩展建议

### 可添加的功能

1. **WebSocket 支持** - 实时服务器日志流
2. **认证系统** - API 密钥或 OAuth2
3. **请求日志中间件** - 详细的请求跟踪
4. **速率限制** - 防止 API 滥用
5. **数据库支持** - 持久化操作历史
6. **Web UI** - 前端管理界面
7. **容器支持** - Docker 镜像和编排
8. **监控指标** - Prometheus 指标导出

---

## 编译和部署

### 编译
```bash
go mod tidy
go build -o asa-server-api.exe
```

### 部署
```bash
./asa-server-api.exe api --port 8080
```

### Docker（建议）
```dockerfile
FROM golang:1.25

WORKDIR /app
COPY . .

RUN go mod download
RUN go build -o asa-server-api.exe

EXPOSE 8080

CMD ["./asa-server-api.exe", "api", "--port", "8080"]
```

---

## 故障排除

### API 无法连接
- 检查 API 服务器是否正在运行
- 验证端口是否正确（默认 8080）
- 检查防火墙设置

### 实例操作失败
- 验证实例名称是否正确
- 检查实例配置文件是否存在
- 查看服务器日志了解详细错误信息

### RCON 命令失败
- 确保服务器正在运行
- 验证 RCON 密码配置是否正确
- 检查端口配置

---

## 支持的操作系统

- **Windows** - 完全支持（主要开发平台）
- **Linux** - 部分支持（需要 WSL 或虚拟机运行 Windows ARK 服务器）
- **macOS** - 部分支持（同上）

---

## 许可证和贡献

本项目遵循原始许可证。

---

## 总结

✅ **已完成**：
- 16 个 REST API 端点
- 完整的 OpenAPI 3.0 规范
- 详细的 API 文档
- 测试脚本（Bash 和 PowerShell）
- 错误处理和验证
- CLI 命令的完整映射

🚀 **现在可以**：
- 通过 HTTP API 管理 ARK 服务器
- 集成第三方应用程序
- 构建 Web UI 或移动应用
- 自动化服务器管理任务

