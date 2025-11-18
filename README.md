# ASA Server Manager - Go 版本

这是一个基于 `ark_instance_manager.sh` 的 Go 语言实现，使用 `github.com/urfave/cli/v3` 库构建。

## 功能特性

- ✅ 实例管理（创建、删除、重命名）
- ✅ 服务器控制（启动、停止、重启）
- ✅ 服务器状态检查
- ✅ 世界备份和恢复
- ✅ RCON 命令发送
- ✅ 批量操作（启动/停止所有实例）
- ✅ 配置文件管理
- ✅ Windows 服务支持（安装、启动、停止、移除）
- ✅ HTTP API 服务（通过Windows服务自动启动）

## 安装

### 前置要求

- Go 1.25.4 或更高版本
- Windows 或 Linux 系统（带 Proton 支持）

### 构建

```bash
cd d:\golang\asa-server
go mod tidy
go build -o asa-manager.exe
```

## 使用方法

### 基本命令

#### 列出所有实例
```bash
asa-manager list
```

#### 创建新实例
```bash
asa-manager create
```
按照提示输入实例名称。

#### 启动实例
```bash
asa-manager start <instance_name>
```

#### 停止实例
```bash
asa-manager stop <instance_name>
```

#### 重启实例
```bash
asa-manager restart <instance_name>
```

#### 检查实例状态
```bash
# 检查所有实例
asa-manager status

# 检查特定实例
asa-manager status <instance_name>
```

#### 删除实例
```bash
asa-manager delete <instance_name>
```

#### 重命名实例
```bash
asa-manager rename <instance_name>
```

#### 创建备份
```bash
asa-manager backup <instance_name> <world_folder>
```

#### 恢复备份
```bash
asa-manager restore <instance_name>
```
将交互式选择要恢复的备份。

#### 发送 RCON 命令
```bash
asa-manager rcon <instance_name> "<command>"
```

#### 启动所有实例
```bash
asa-manager start-all
```

#### 停止所有实例
```bash
asa-manager stop-all
```

#### 交互式管理
```bash
asa-manager manage <instance_name>
```

#### Windows服务管理
```bash
# 安装为Windows服务
asa-manager service install

# 启动Windows服务
asa-manager service start

# 停止Windows服务
asa-manager service stop

# 移除Windows服务
asa-manager service remove
```

## 目录结构

应用运行后会创建以下目录结构：

```
.
├── instances/              # 实例配置和日志
│   └── <instance_name>/
│       ├── instance_config.ini
│       ├── Config/
│       │   ├── Game.ini
│       │   └── GameUserSettings.ini
│       └── server.log
├── server-files/           # ARK 服务器文件
├── steamcmd/              # SteamCMD 工具
├── GE-Proton10-4/         # Proton 兼容层
├── backups/               # 世界备份
└── clusters/              # 集群配置（如果有）
```

## 实例配置

每个实例都有一个 `instance_config.ini` 文件，包含以下设置：

```ini
[ServerSettings]
ServerName=ARK Server <instance_name>
ServerPassword=
ServerAdminPassword=adminpassword
MaxPlayers=70
MapName=TheIsland_WP
RCONPort=27020
QueryPort=27015
Port=7777
ModIDs=
CustomStartParameters=-NoBattlEye -crossplay -NoHangDetection
SaveDir=<instance_name>
ClusterID=
```

## 特性说明

### 端口管理

工具会自动检查端口冲突，确保以下端口不会被多个实例使用：
- `Port` - 游戏端口
- `RCONPort` - RCON 端口
- `QueryPort` - 查询端口

### 备份和恢复

- 备份保存为压缩的 tar.gz 格式
- 文件名格式：`<instance_name>_<world_folder>_<timestamp>.tar.gz`
- 备份保存在 `backups/` 目录
- 恢复时会覆盖现有的世界数据

### 服务器日志

每个实例的日志保存在：
```
instances/<instance_name>/server.log
```

## 与原始 Shell 脚本的区别

### 已实现的功能

✅ 所有基本的实例管理功能
✅ 服务器启动/停止/重启
✅ 备份和恢复（基础实现）
✅ RCON 命令发送（占位符）
✅ 配置管理

### 待实现的功能

⏳ SteamCMD 集成（下载和更新服务器文件）
⏳ Proton 集成（下载和设置 Proton）
⏳ 完整的 RCON 客户端实现
⏳ 重启管理器配置（Cron 任务）
⏳ 依赖项检查

## 文件结构

```
d:\golang\asa-server\
├── main.go           # 应用入口和命令定义
├── config.go         # 配置管理
├── server.go         # 服务器操作
├── backup.go         # 备份和恢复
├── actions.go        # 命令处理器
├── go.mod            # Go 模块定义
└── README.md         # 本文件
```

## 开发

### 添加新命令

1. 在 `main.go` 的 `app.Commands` 中添加新的 `cli.Command`
2. 在 `actions.go` 中实现对应的 `action*` 函数
3. 重新编译：`go build`

### 扩展功能

- 编辑 `server.go` 以添加新的服务器操作
- 编辑 `config.go` 以支持新的配置选项
- 编辑 `backup.go` 以改进备份和恢复逻辑

## 许可证

基于原始 bash 脚本转换为 Go 语言版本。

## 注意事项

1. **Windows 兼容性**：当前代码主要针对 Linux/Proton 环境优化，Windows 上的某些功能可能需要调整
2. **权限**：某些操作可能需要管理员权限（如 Proton 初始化）
3. **首次运行**：需要下载 SteamCMD 和 Proton，这可能需要一些时间

## 故障排查

### 命令找不到
确保 `asa-manager.exe` 在 PATH 中或从工作目录直接运行

### 实例无法启动
检查：
- 服务器文件是否已下载
- 端口是否被其他程序占用
- 配置文件是否正确

### 备份失败
确保：
- 实例已停止
- 世界文件夹名称正确
- 有足够的磁盘空间

## Windows 服务支持

ASA Server Manager 支持作为 Windows 服务运行，这使得服务器可以在系统启动时自动启动，
并在后台持续运行。

### 服务功能

- 自动在系统启动时启动
- 启动后自动运行 HTTP API 服务器（默认端口 8080）
- 通过 HTTP API 管理所有 ASA 服务器实例
- 自动重启功能，如果服务意外停止会自动重启

### 服务管理命令

```bash
# 安装为Windows服务（需要管理员权限）
asa-manager service install

# 启动Windows服务
asa-manager service start

# 停止Windows服务
asa-manager service stop

# 移除Windows服务
asa-manager service remove
```

### 使用前准备

1. 确保已使用 `asa-manager update` 命令安装了基础服务器
2. 以管理员身份运行命令提示符或 PowerShell
3. 确保防火墙允许端口 8080 的入站连接（如果需要远程访问 API）

### API 端点

- `GET /health` - 健康检查
- `GET /api/instances` - 列出所有实例
- `POST /api/instances` - 创建新实例
- `GET /api/instances/{name}` - 获取实例状态
- `DELETE /api/instances/{name}` - 删除实例
- `PUT /api/instances/{name}` - 重命名实例
- `POST /api/server/{name}/start` - 启动服务器实例
- `POST /api/server/{name}/stop` - 停止服务器实例
- `POST /api/server/{name}/restart` - 重启服务器实例
- `POST /api/server/start-all` - 启动所有实例
- `POST /api/server/stop-all` - 停止所有实例
