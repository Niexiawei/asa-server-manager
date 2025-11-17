# 快速启动指南 - HTTP API

## 1️⃣ 启动 API 服务器

### 默认端口 (8080)
```bash
./asa-server.exe api
```

### 自定义端口 (例如：9090)
```bash
./asa-server.exe api --port 9090
```

## 2️⃣ 验证 API 是否正常

### 健康检查
```bash
curl http://localhost:8080/health
```

### 预期响应
```json
{
  "status": "healthy",
  "service": "asa-server-manager-api"
}
```

## 3️⃣ 常用 API 调用

### 列出所有实例
```bash
curl http://localhost:8080/api/instances
```

### 创建新实例
```bash
curl -X POST http://localhost:8080/api/instances \
  -H "Content-Type: application/json" \
  -d '{"name":"my-server"}'
```

### 启动服务器
```bash
curl -X POST http://localhost:8080/api/server/my-server/start
```

### 停止服务器
```bash
curl -X POST http://localhost:8080/api/server/my-server/stop
```

### 检查服务器状态
```bash
curl http://localhost:8080/api/instances/my-server
```

### 发送 RCON 命令
```bash
curl -X POST http://localhost:8080/api/rcon/my-server/command \
  -H "Content-Type: application/json" \
  -d '{"command":"ListPlayers"}'
```

## 4️⃣ 使用 PowerShell (Windows 推荐)

### 运行测试脚本
```powershell
.\api_test_examples.ps1
```

### 简单示例
```powershell
# 列表实例
$response = Invoke-RestMethod -Uri "http://localhost:8080/api/instances" -Method Get
$response | ConvertTo-Json

# 创建实例
$body = @{name = "new-server"} | ConvertTo-Json
Invoke-RestMethod -Uri "http://localhost:8080/api/instances" -Method Post `
  -Headers @{"Content-Type"="application/json"} -Body $body
```

## 5️⃣ 文档参考

- **详细 API 文档**: 查看 `API_GUIDE.md`
- **实现总结**: 查看 `HTTP_API_IMPLEMENTATION.md`
- **OpenAPI 规范**: 查看 `openapi.json`

## 6️⃣ 可视化 API 文档

### 使用 Swagger UI（在线）
1. 访问 https://editor.swagger.io/
2. 点击 "File" → "Import URL"
3. 提供你的 `openapi.json` 文件路径

### 或本地使用
```bash
# 需要 Docker
docker run -p 80:8080 -e SWAGGER_JSON=/openapi.json -v $(pwd):/app swaggerapi/swagger-ui
```

## 7️⃣ 常见问题

### Q: API 无法连接怎么办？
A: 
1. 确保 API 服务器正在运行：`./asa-server.exe api`
2. 检查端口是否正确
3. 检查防火墙设置

### Q: 如何修改 API 端口？
A: 使用 `--port` 参数：
```bash
./asa-server.exe api --port 9090
```

### Q: 支持哪些 HTTP 方法？
A: 主要使用 GET、POST、PUT、DELETE

### Q: 响应格式是什么？
A: 所有响应都是 JSON 格式，包含 `success`、`message`、`data` 和 `error` 字段

## 8️⃣ 与 CLI 命令对比

| CLI 命令 | 等价的 API 调用 |
|---------|---------------|
| `list` | `GET /api/instances` |
| `create` | `POST /api/instances` |
| `start <name>` | `POST /api/server/:name/start` |
| `stop <name>` | `POST /api/server/:name/stop` |
| `restart <name>` | `POST /api/server/:name/restart` |
| `status` | `GET /api/instances` |
| `rcon <name> <cmd>` | `POST /api/rcon/:name/command` |
| `delete <name>` | `DELETE /api/instances/:name` |
| `rename <name>` | `PUT /api/instances/:name` |
| `backup <name> <folder>` | `POST /api/backup/:name` |
| `restore <name>` | `POST /api/backup/:name/restore` |
| `start-all` | `POST /api/server/start-all` |
| `stop-all` | `POST /api/server/stop-all` |
| `update` | `POST /api/server/update` |

## 9️⃣ 下一步

### 构建 Web UI
创建一个前端应用程序（React、Vue 等）来调用这些 API

### 集成到现有系统
使用 API 集成到自动化工具、监控系统等

### 容器化部署
使用 Docker 容器化应用以便于部署

### 添加认证
考虑添加 API 密钥或 JWT 认证以增强安全性

---

## 📚 完整文件清单

- ✅ `api.go` - API 实现
- ✅ `API_GUIDE.md` - API 使用指南
- ✅ `HTTP_API_IMPLEMENTATION.md` - 实现总结
- ✅ `openapi.json` - OpenAPI 规范
- ✅ `api_test_examples.sh` - Bash 测试脚本
- ✅ `api_test_examples.ps1` - PowerShell 测试脚本
- ✅ `QUICK_START.md` - 本文件

---

祝你使用愉快！ 🚀
