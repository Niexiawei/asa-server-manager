# 快速开始指南

## 5 分钟快速启动

### 步骤 1：构建应用

```bash
cd d:\golang\asa-server
go build -o asa-manager.exe
```

### 步骤 2：创建第一个实例

```bash
.\asa-manager.exe create
```

按照提示输入实例名称（例如：`my-server`）。

### 步骤 3：列出实例

```bash
.\asa-manager.exe list
```

你应该看到刚创建的实例：
```
📋 Available instances:
  ❌ my-server
```

### 步骤 4：启动实例

```bash
.\asa-manager.exe start my-server
```

服务器将在约 60 秒内完全启动。

### 步骤 5：检查状态

```bash
.\asa-manager.exe status my-server
```

## 常用命令速查

### 实例管理

```bash
# 创建实例
asa-manager create

# 列出所有实例
asa-manager list

# 删除实例
asa-manager delete <instance_name>

# 重命名实例
asa-manager rename <instance_name>
```

### 服务器控制

```bash
# 启动实例
asa-manager start <instance_name>

# 停止实例
asa-manager stop <instance_name>

# 重启实例
asa-manager restart <instance_name>

# 检查状态（所有实例）
asa-manager status

# 检查状态（特定实例）
asa-manager status <instance_name>

# 启动所有实例
asa-manager start-all

# 停止所有实例
asa-manager stop-all
```

### 备份和恢复

```bash
# 创建备份
asa-manager backup <instance_name> <world_folder>

# 恢复备份
asa-manager restore <instance_name>
```

### 高级操作

```bash
# 发送 RCON 命令
asa-manager rcon <instance_name> "<command>"

# 交互式管理
asa-manager manage <instance_name>
```

## 配置编辑

配置文件位置：
```
instances/<instance_name>/instance_config.ini
```

编辑配置后，重启服务器使更改生效。

### 常见配置项

```ini
ServerName=My ARK Server         # 服务器名称
ServerPassword=                  # 服务器密码（空=无密码）
ServerAdminPassword=secret123    # 管理员密码
MaxPlayers=70                    # 最大玩家数
MapName=TheIsland_WP             # 地图名称
Port=7777                        # 游戏端口
RCONPort=27020                   # RCON 端口
QueryPort=27015                  # 查询端口
```

## 故障排查

### 问题 1：实例无法启动

**症状**：启动命令执行但服务器没有运行

**解决方案**：
1. 检查日志：`cat instances/<instance>/server.log`
2. 确保服务器文件已下载
3. 检查端口是否被占用：`netstat -an | grep LISTEN`

### 问题 2：端口冲突错误

**症状**：看到 "Port conflict" 错误

**解决方案**：
1. 编辑 `instance_config.ini` 中的端口号
2. 确保每个实例使用不同的端口
3. 重新启动实例

### 问题 3：备份失败

**症状**：备份操作报错

**解决方案**：
1. 确保服务器已停止
2. 检查世界文件夹名称是否正确
3. 确保磁盘空间充足

## 下一步

- 📖 阅读完整的 [README.md](README.md)
- 🏗️ 了解 [系统架构](ARCHITECTURE.md)
- 🔄 查看 [迁移指南](MIGRATION.md)
- 💡 查看 [使用示例](examples.sh)

## 获取帮助

### 查看命令帮助

```bash
# 所有命令
asa-manager --help

# 特定命令
asa-manager start --help
```

### 查看版本

```bash
asa-manager --version
```

## Tips 和技巧

### 1. 批量启动所有实例

```bash
asa-manager start-all
```

这会按顺序启动所有实例，每个实例之间间隔 30 秒。

### 2. 创建定期备份

```bash
# 每天午夜运行（Linux/Mac）
0 0 * * * asa-manager backup my-server TheIsland_WP
```

### 3. 监控服务器状态

```bash
# 监视状态（每 30 秒更新一次）
watch -n 30 'asa-manager status'
```

### 4. 快速重启单个实例

```bash
asa-manager restart my-server
```

### 5. 批量查看所有日志

```bash
# 最后 50 行
tail -50 instances/*/server.log
```

## 常见配置场景

### 场景 1：多个独立服务器

```bash
# 创建 3 个独立服务器
asa-manager create  # server1, 端口 7777
asa-manager create  # server2, 端口 7778
asa-manager create  # server3, 端口 7779

# 启动所有
asa-manager start-all

# 检查状态
asa-manager status
```

### 场景 2：开发/测试环境

```bash
# 创建测试实例
asa-manager create

# 使用较小的玩家限制
# 编辑 instances/test/instance_config.ini
# MaxPlayers=10

# 启动
asa-manager start test
```

### 场景 3：生产环境备份策略

```bash
# 每周创建完整备份
# crontab -e
# 每周日凌晨 2 点备份
0 2 * * 0 asa-manager backup production TheIsland_WP
```

## 安全建议

1. **设置强密码**
   ```ini
   ServerAdminPassword=YourStrongPassword123!
   ```

2. **限制服务器密码**
   ```ini
   ServerPassword=YourServerPassword
   ```

3. **定期备份**
   ```bash
   # 每天备份一次
   asa-manager backup my-server TheIsland_WP
   ```

4. **监控磁盘空间**
   ```bash
   # 检查备份大小
   du -sh backups/
   ```

5. **保护配置文件**
   ```bash
   # 配置文件默认权限：600（仅所有者可读写）
   chmod 600 instances/*/instance_config.ini
   ```

## 性能优化

### 建议配置

```ini
# 小型服务器 (<=10 玩家)
MaxPlayers=10
CustomStartParameters=-NoBattlEye -crossplay -NoHangDetection

# 中型服务器 (10-50 玩家)
MaxPlayers=50

# 大型服务器 (50+ 玩家)
MaxPlayers=70
```

### 监控资源使用

```bash
# Windows 任务管理器
tasklist | findstr ArkAscendedServer

# Linux
ps aux | grep ArkAscendedServer
top -p $(pgrep -f ArkAscendedServer)
```

## 进一步学习

- 🎮 [ARK Survival Ascended 官网](https://www.playark.com)
- 📚 [官方服务器管理指南](https://survivalascended.zendesk.com)
- 💻 [Go 编程语言](https://golang.org)
- 📦 [cli/v3 文档](https://github.com/urfave/cli/tree/main)

---

**需要帮助？** 检查完整的 README.md 或查看相应的 .log 文件获取更多信息。
