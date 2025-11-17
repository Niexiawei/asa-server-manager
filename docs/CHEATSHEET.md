# ASA Server Manager - 速查表 (Cheat Sheet)

## 命令速查

### 🎮 实例管理

| 命令 | 说明 | 示例 |
|-----|------|------|
| `create` | 创建新实例 | `asa-manager create` |
| `list` | 列出所有实例 | `asa-manager list` |
| `delete` | 删除实例 | `asa-manager delete my-server` |
| `rename` | 重命名实例 | `asa-manager rename old-name` |
| `manage` | 交互式管理 | `asa-manager manage my-server` |

### 🚀 服务器控制

| 命令 | 说明 | 示例 |
|-----|------|------|
| `start` | 启动服务器 | `asa-manager start my-server` |
| `stop` | 停止服务器 | `asa-manager stop my-server` |
| `restart` | 重启服务器 | `asa-manager restart my-server` |
| `status` | 检查状态 | `asa-manager status [instance]` |
| `start-all` | 启动所有实例 | `asa-manager start-all` |
| `stop-all` | 停止所有实例 | `asa-manager stop-all` |

### 💾 备份和恢复

| 命令 | 说明 | 示例 |
|-----|------|------|
| `backup` | 创建备份 | `asa-manager backup my-server TheIsland_WP` |
| `restore` | 恢复备份 | `asa-manager restore my-server` |

### 🔌 高级操作

| 命令 | 说明 | 示例 |
|-----|------|------|
| `rcon` | 发送 RCON 命令 | `asa-manager rcon my-server "SaveWorld"` |
| `update` | 更新服务器 | `asa-manager update` |
| `config-restart` | 配置重启管理 | `asa-manager config-restart` |

### 📖 帮助和信息

| 命令 | 说明 | 示例 |
|-----|------|------|
| `--help` | 查看帮助 | `asa-manager --help` |
| `--version` | 查看版本 | `asa-manager --version` |
| `<cmd> --help` | 命令帮助 | `asa-manager start --help` |

---

## 常见命令组合

### 创建和启动新服务器

```bash
# 步骤 1：创建实例
asa-manager create
# 输入：my-server

# 步骤 2：启动服务器
asa-manager start my-server

# 步骤 3：检查状态
asa-manager status my-server
```

### 备份和恢复

```bash
# 步骤 1：创建备份
asa-manager backup my-server TheIsland_WP

# 步骤 2：列出备份
ls backups/

# 步骤 3：恢复备份
asa-manager restore my-server
```

### 批量启动所有服务器

```bash
# 启动所有实例
asa-manager start-all

# 等待并检查
asa-manager status
```

### 优雅重启

```bash
asa-manager restart my-server
```

### 强制停止和重启

```bash
# 停止
asa-manager stop my-server

# 等待日志确认已停止
# 然后启动
asa-manager start my-server
```

---

## 配置文件速查

### 配置文件位置

```
instances/<instance_name>/instance_config.ini
```

### 重要配置项

```ini
# 服务器基本信息
ServerName=My ARK Server        # 服务器名称
ServerPassword=                 # 服务器密码
ServerAdminPassword=secret123   # 管理员密码

# 服务器设置
MaxPlayers=70                   # 最大玩家数
MapName=TheIsland_WP            # 地图名称

# 端口配置
Port=7777                       # 游戏端口
RCONPort=27020                  # RCON 端口
QueryPort=27015                 # 查询端口

# 其他设置
ModIDs=                         # MOD IDs
SaveDir=<instance_name>         # 保存目录
ClusterID=                      # 集群 ID
```

### 常见修改

```ini
# 改变最大玩家数
MaxPlayers=50

# 设置服务器密码
ServerPassword=MyPassword123

# 改变端口（确保不冲突）
Port=7778
RCONPort=27021
QueryPort=27016

# 添加 MOD
ModIDs=123456789,987654321
```

---

## 目录结构速查

```
./
├── instances/                  # 实例配置
│   └── my-server/
│       ├── instance_config.ini # 配置文件
│       ├── Config/
│       │   ├── Game.ini
│       │   └── GameUserSettings.ini
│       └── server.log          # 服务器日志
├── server-files/               # 游戏文件
├── steamcmd/                   # SteamCMD
├── GE-Proton10-4/             # Proton 兼容层
└── backups/                    # 备份存储
    └── my-server_world_2024-01-15_10-30-00.tar.gz
```

---

## 故障排查快速参考

### 问题：实例无法启动

**检查清单**：
```bash
1. 检查日志：cat instances/my-server/server.log
2. 检查端口：netstat -an | findstr LISTEN
3. 检查文件：dir server-files\ShooterGame
4. 重启尝试：asa-manager start my-server
```

### 问题：端口冲突

**解决方案**：
```bash
# 编辑配置
notepad instances/my-server/instance_config.ini

# 修改端口（确保唯一）
Port=7778        # 改为 7778
RCONPort=27021   # 改为 27021
QueryPort=27016  # 改为 27016

# 重启服务器
asa-manager restart my-server
```

### 问题：备份失败

**原因和解决**：
```bash
1. 服务器还在运行 → 运行 asa-manager stop my-server
2. 磁盘满 → 检查磁盘空间：dir | measure
3. 路径错误 → 验证世界文件夹名称
```

### 问题：服务器崩溃

**恢复步骤**：
```bash
1. 检查状态：asa-manager status my-server
2. 查看日志：cat instances/my-server/server.log
3. 强制停止：asa-manager stop my-server
4. 恢复备份：asa-manager restore my-server
5. 重启服务器：asa-manager start my-server
```

---

## 性能优化速查

### 低配置服务器

```ini
MaxPlayers=10
CustomStartParameters=-NoBattlEye -crossplay -NoHangDetection
```

### 中等配置服务器

```ini
MaxPlayers=50
```

### 高性能服务器

```ini
MaxPlayers=70
# 可考虑增加 MOD，但保持稳定性
```

---

## RCON 常用命令

```bash
# 保存世界
asa-manager rcon my-server "SaveWorld"

# 广播消息
asa-manager rcon my-server "broadcast This is a message"

# 检查服务器
asa-manager rcon my-server "status"

# 停止服务器
asa-manager rcon my-server "DoExit"

# 更改难度
asa-manager rcon my-server "cheat SetDifficulty 5"

# 踢出玩家
asa-manager rcon my-server "kick PlayerName"
```

---

## 文件查找速查

| 内容 | 位置 |
|-----|------|
| 实例配置 | `instances/<instance>/instance_config.ini` |
| 游戏配置 | `instances/<instance>/Config/Game.ini` |
| 玩家设置 | `instances/<instance>/Config/GameUserSettings.ini` |
| 服务器日志 | `instances/<instance>/server.log` |
| 备份文件 | `backups/*.tar.gz` |
| 游戏文件 | `server-files/ShooterGame/` |
| 保存文件 | `server-files/ShooterGame/Saved/` |

---

## 快速启动脚本

### Windows Batch (.bat)

```batch
@echo off
REM 启动服务器脚本
cd d:\golang\asa-server
.\asa-server.exe start my-server
pause
```

### Linux/Mac Bash (.sh)

```bash
#!/bin/bash
# 启动服务器脚本
cd /path/to/asa-server
./asa-manager start my-server
```

### 定时备份脚本 (Windows)

```batch
@echo off
REM 每天执行一次备份
cd d:\golang\asa-server
FOR /f "tokens=2-4 delims=/ " %%a in ('date /t') do (set mydate=%%c-%%a-%%b)
.\asa-server.exe backup my-server TheIsland_WP
echo Backup completed at %mydate%
```

---

## 常见错误信息

| 错误 | 原因 | 解决方案 |
|-----|------|---------|
| "No instances found" | 还没有创建实例 | 运行 `asa-manager create` |
| "Port conflict" | 端口已被使用 | 编辑 `instance_config.ini` 改变端口 |
| "Server is running" | 试图启动正在运行的服务器 | 运行 `asa-manager stop <instance>` |
| "Server is not running" | 试图停止未运行的服务器 | 忽略或首先启动服务器 |
| "Failed to start server" | 启动失败 | 检查日志和磁盘空间 |

---

## 键盘快捷键

### 交互式菜单

| 快捷键 | 功能 |
|--------|------|
| `1-9` | 选择菜单项 |
| `0` | 返回上级菜单 |
| `↑↓` | 导航菜单（如果支持） |
| `Enter` | 确认选择 |
| `Ctrl+C` | 退出程序 |

---

## 环境变量（高级用户）

```bash
# Windows
set GOOS=windows
set GOARCH=amd64

# Linux
export GOOS=linux
export GOARCH=amd64

# 然后构建
go build
```

---

## 一行命令速查

```bash
# 列出所有实例并显示状态
asa-manager list && asa-manager status

# 创建、启动、检查状态
asa-manager create && asa-manager start new-server && asa-manager status

# 创建备份并列出
asa-manager backup my-server TheIsland_WP && dir backups\

# 停止并删除实例
asa-manager stop my-server && asa-manager delete my-server

# 更新所有内容
asa-manager stop-all && asa-manager update && asa-manager start-all
```

---

## 进阶技巧

### 批量管理多个实例

```bash
# 为每个实例创建日期标记备份
for instance in server1 server2 server3; do
  asa-manager backup $instance TheIsland_WP
done
```

### 监控服务器状态

```bash
# Windows - 每 30 秒刷新一次状态
powershell -Command "while($true) { Clear-Host; asa-manager status; Start-Sleep -Seconds 30 }"
```

### 获取所有实例列表

```bash
# 仅显示实例名称
asa-manager list | grep -E "^  [✅❌]" | awk '{print $2}'
```

---

## 参考文档

| 文档 | 用途 |
|-----|------|
| [README.md](README.md) | 完整参考 |
| [QUICKSTART.md](QUICKSTART.md) | 快速开始 |
| [ARCHITECTURE.md](ARCHITECTURE.md) | 系统设计 |
| [MIGRATION.md](MIGRATION.md) | 版本迁移 |
| [PROJECT.md](PROJECT.md) | 项目信息 |

---

## 更新日期：2025-11-16
## 版本：1.0.0

**Tip**：保存此文件方便快速查阅！
