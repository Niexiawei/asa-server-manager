# ASA Server Manager - Go 版本 架构文档

## 项目概述

ASA Server Manager 是一个用 Go 语言编写的命令行工具，用于管理 ARK: Survival Ascended 游戏服务器实例。本文档描述了应用的整体架构、组件设计和数据流。

## 高层架构

```
┌─────────────────────────────────────────┐
│         CLI Interface (main.go)         │
│     (github.com/urfave/cli/v3)          │
└──────────────────┬──────────────────────┘
                   │
        ┌──────────┴──────────┐
        │                     │
┌───────▼───────────┐  ┌─────▼──────────────┐
│  Actions (...)    │  │  Actions (...).    │
│  actionStart      │  │  actionStop        │
│  actionStop       │  │  actionRestart     │
│  actionBackup     │  │  etc.              │
└────────┬──────────┘  └────────┬───────────┘
         │                      │
         └──────────┬───────────┘
                    │
        ┌───────────┴────────────┐
        │                        │
┌───────▼─────────────┐  ┌──────▼──────────────┐
│  Config (config.go) │  │  Server (server.go) │
│  ────────────────── │  │  ──────────────────  │
│  LoadConfig         │  │  StartServer        │
│  SaveConfig         │  │  StopServer         │
│  GetInstances       │  │  IsServerRunning    │
│  CheckPortConflicts │  │  SendRCONCommand    │
└─────────────────────┘  └──────────────────────┘
                            │
        ┌───────────────────┴──────────────────┐
        │                                      │
┌───────▼─────────────────┐         ┌─────────▼──────────┐
│ Backup (backup.go)      │         │ OS/File Operations  │
│ ──────────────────────  │         │ ─────────────────   │
│ BackupInstanceWorld     │         │ os.Exec (Proton)    │
│ RestoreBackupToInstance │         │ os.Rename           │
│ GetAvailableBackups     │         │ tar/gzip            │
└─────────────────────────┘         └─────────────────────┘
```

## 模块说明

### 1. main.go - 应用入口

**职责**：
- 定义 CLI 应用结构
- 注册所有可用命令
- 初始化目录结构

**关键函数**：
```go
func main() {
    // 初始化目录
    // 创建 CLI 应用
    // 运行应用
}
```

**命令列表**：
- `update` - 更新基础服务器
- `list` - 列出实例
- `create` - 创建实例
- `start` - 启动实例
- `stop` - 停止实例
- `restart` - 重启实例
- `status` - 检查状态
- `delete` - 删除实例
- `backup` - 创建备份
- `restore` - 恢复备份
- 等等...

### 2. config.go - 配置管理

**职责**：
- 加载和保存实例配置
- 管理目录结构
- 检查端口冲突

**关键数据结构**：

```go
type InstanceConfig struct {
    ServerName            string
    ServerPassword        string
    ServerAdminPassword   string
    MaxPlayers            int
    MapName               string
    RCONPort              int
    QueryPort             int
    Port                  int
    ModIDs                string
    SaveDir               string
    ClusterID             string
    CustomStartParameters string
}
```

**关键函数**：

| 函数 | 功能 |
|------|------|
| `ensureDirectories()` | 创建必要的目录 |
| `LoadInstanceConfig()` | 从 INI 文件加载配置 |
| `SaveInstanceConfig()` | 保存配置到 INI 文件 |
| `GetAvailableInstances()` | 获取所有实例列表 |
| `CheckForDuplicatePorts()` | 检查端口冲突 |

### 3. server.go - 服务器操作

**职责**：
- 启动/停止/重启服务器
- 检查服务器运行状态
- 发送 RCON 命令
- 管理多个实例

**关键函数**：

| 函数 | 功能 |
|------|------|
| `IsServerRunning()` | 检查服务器是否运行 |
| `StartServer()` | 启动服务器 |
| `StopServer()` | 停止服务器（优雅关闭） |
| `RestartServer()` | 重启服务器 |
| `SendRCONCommand()` | 发送 RCON 命令 |
| `StartAllInstances()` | 启动所有实例 |
| `StopAllInstances()` | 停止所有实例 |

**启动流程**：

```
StartServer()
├── 检查端口冲突
├── 加载实例配置
├── 创建 Config 目录
├── 设置 Proton 环境变量
├── 构建启动命令
└── 执行 Proton 运行 ARK 服务器
```

### 4. backup.go - 备份和恢复

**职责**：
- 创建压缩备份
- 恢复备份数据
- 管理备份文件

**关键函数**：

| 函数 | 功能 |
|------|------|
| `BackupInstanceWorld()` | 创建世界备份 |
| `RestoreBackupToInstance()` | 恢复备份 |
| `GetAvailableBackups()` | 列出所有备份 |

**备份格式**：
- 格式：tar.gz（gzip 压缩的 tar 归档）
- 命名：`<instance>_<world>_<timestamp>.tar.gz`
- 存储：`backups/` 目录

### 5. actions.go - CLI 命令处理

**职责**：
- 实现所有 CLI 命令的处理逻辑
- 处理用户交互
- 调用核心功能模块

**命令处理函数命名约定**：`action<Command>`

示例：
```go
func actionStart(ctx context.Context, cmd *cli.Command) error
func actionStop(ctx context.Context, cmd *cli.Command) error
func actionList(ctx context.Context, cmd *cli.Command) error
```

## 数据流示例

### 启动服务器流程

```
用户输入: asa-manager start server1
        │
        ▼
main.go: 解析命令
        │
        ▼
actions.go: actionStart()
        │
        ├─► config.go: LoadInstanceConfig("server1")
        │
        ├─► server.go: CheckForDuplicatePorts()
        │
        ├─► server.go: IsServerRunning()
        │
        └─► server.go: StartServer()
            ├─► 设置环境变量
            ├─► 构建命令参数
            └─► exec.Command() 执行 Proton
                └─► 运行 ArkAscendedServer.exe
```

### 创建备份流程

```
用户输入: asa-manager backup server1 TheIsland_WP
        │
        ▼
main.go: 解析命令
        │
        ▼
actions.go: actionBackup()
        │
        ├─► server.go: IsServerRunning() [检查服务器已停止]
        │
        └─► backup.go: BackupInstanceWorld()
            ├─► 验证世界文件夹
            ├─► 创建备份目录
            ├─► tar: 创建归档
            ├─► gzip: 压缩数据
            └─► 生成备份文件: instance_world_timestamp.tar.gz
```

## 目录结构详解

```
d:\golang\asa-server\
├── main.go                    # CLI 应用入口
├── config.go                  # 配置管理
├── server.go                  # 服务器操作
├── backup.go                  # 备份和恢复
├── actions.go                 # 命令处理
├── go.mod                      # Go 模块定义
├── go.sum                      # 依赖版本锁定
├── asa-server.exe             # 编译后的可执行文件
├── README.md                   # 用户文档
├── MIGRATION.md                # 迁移指南
├── ARCHITECTURE.md             # 本文件
└── examples.sh                 # 使用示例
```

### 运行时目录结构

```
.
├── instances/                 # 实例配置和日志
│   ├── server1/
│   │   ├── instance_config.ini
│   │   ├── Config/
│   │   │   ├── Game.ini
│   │   │   └── GameUserSettings.ini
│   │   └── server.log
│   └── server2/
│       └── ...
├── server-files/              # ARK 服务器文件
│   └── ShooterGame/
│       ├── Binaries/
│       ├── Saved/
│       └── ...
├── steamcmd/                  # SteamCMD 工具
│   └── steamcmd.sh
├── GE-Proton10-4/            # Proton 兼容层
│   ├── proton
│   └── files/
├── backups/                   # 世界备份
│   ├── server1_TheIsland_WP_2024-01-15_10-30-00.tar.gz
│   └── server2_Ragnarok_WP_2024-01-15_11-30-00.tar.gz
└── clusters/                  # 集群数据（如果配置）
    └── cluster1/
```

## 配置文件格式

### instance_config.ini

```ini
[ServerSettings]
ServerName=ARK Server Instance Name
ServerPassword=
ServerAdminPassword=adminpassword
MaxPlayers=70
MapName=TheIsland_WP
RCONPort=27020
QueryPort=27015
Port=7777
ModIDs=
CustomStartParameters=-NoBattlEye -crossplay -NoHangDetection
SaveDir=instance_name
ClusterID=
```

## 错误处理策略

### 错误分类

1. **配置错误**
   - 缺少配置文件
   - 无效的配置值
   - 端口冲突

2. **操作错误**
   - 服务器已运行
   - 服务器未运行
   - 启动失败

3. **文件操作错误**
   - 目录创建失败
   - 文件读写失败
   - 备份失败

### 错误处理模式

```go
if err != nil {
    return fmt.Errorf("operation failed: %w", err)
}
```

## 并发和并行处理

当前版本：
- ✅ 顺序启动多个实例（带 30 秒延迟）
- ✅ 顺序停止所有实例
- ❌ 并发操作（保留作为未来改进）

## 依赖关系

### 外部依赖

```
github.com/urfave/cli/v3 v3.0.0-beta1
```

### 标准库依赖

```
- archive/tar
- compress/gzip
- bufio
- context
- fmt
- os
- path/filepath
- strconv
- strings
- time
```

## 安全考虑

1. **权限管理**
   - 配置文件权限：600（仅所有者可读写）
   - 日志文件可由任何用户读取

2. **输入验证**
   - 实例名称验证
   - 端口号范围检查
   - 文件路径安全性

3. **密码处理**
   - 密码存储在配置文件中
   - 建议使用强密码
   - 配置文件应受保护

## 性能特征

### 时间复杂度

| 操作 | 时间复杂度 |
|------|-----------|
| 列出实例 | O(n) - n 为实例数 |
| 启动实例 | O(1) |
| 停止实例 | O(1) |
| 检查端口冲突 | O(n) |
| 创建备份 | O(m) - m 为文件总大小 |

### 内存使用

- 基线：~2MB
- 每个实例配置：~5KB
- 运行列表操作：~1MB 峰值

## 扩展点

### 添加新命令

1. 在 `main.go` 中的 `app.Commands` 数组中添加新的 `cli.Command`
2. 在 `actions.go` 中实现 `action<CommandName>` 函数
3. 实现相关的核心功能（在 config.go/server.go/backup.go 等中）

### 示例：添加新的"List Backups"命令

```go
// main.go
{
    Name: "list-backups",
    Usage: "List all available backups",
    Action: actionListBackups,
},

// actions.go
func actionListBackups(ctx context.Context, cmd *cli.Command) error {
    backups, err := GetAvailableBackups()
    if err != nil {
        return err
    }
    for _, backup := range backups {
        fmt.Println(backup)
    }
    return nil
}
```

## 测试策略

### 单元测试（待实现）

```go
// 配置测试
TestLoadInstanceConfig()
TestSaveInstanceConfig()
TestCheckForDuplicatePorts()

// 服务器操作测试
TestIsServerRunning()
TestStartServer()
TestStopServer()

// 备份测试
TestBackupInstanceWorld()
TestRestoreBackupToInstance()
```

### 集成测试（待实现）

1. 创建实例
2. 启动/停止实例
3. 创建备份
4. 恢复备份
5. 清理

## 部署和打包

### Windows 构建

```bash
go build -o asa-manager.exe
```

### Linux/Mac 构建

```bash
GOOS=linux GOARCH=amd64 go build -o asa-manager
```

### 创建发布包

包含：
- 编译后的可执行文件
- README.md
- MIGRATION.md
- ARCHITECTURE.md
- examples.sh

## 维护和演进

### 版本管理

- 当前版本：1.0.0
- 遵循 Semantic Versioning
- 更新日志在单独的 CHANGELOG 中维护（待创建）

### 未来路线图

1. **0.1 版本（当前）**
   - ✅ 基本实例管理
   - ✅ 服务器控制
   - ✅ 备份和恢复

2. **0.2 版本**
   - ⏳ SteamCMD 集成
   - ⏳ 完整 RCON 实现
   - ⏳ 单元测试

3. **0.3 版本**
   - ⏳ Web API
   - ⏳ 数据库支持
   - ⏳ 高级监控

4. **1.0 版本**
   - ⏳ Web UI
   - ⏳ 集群管理
   - ⏳ 云部署支持

## 总结

ASA Server Manager 采用模块化设计，将不同的关注点分离到不同的包中，使代码易于维护和扩展。CLI 框架通过 urfave/cli 提供，提供了强大的命令行接口。核心业务逻辑独立于 UI 层，便于未来添加 Web 界面或 API。
