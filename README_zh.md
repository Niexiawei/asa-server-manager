# ASA Server Manager

一个基于 Go 和 Vue.js 构建的全面的 ARK Server Ascended (ASA) 服务器管理工具，具有命令行界面、HTTP API 和 Web 仪表盘。

## 功能特性

### 核心功能
- ✅ **实例管理**：创建、列出、重命名和删除服务器实例
- ✅ **服务器控制**：启动、停止和重启单个或所有实例
- ✅ **RCON 支持**：向服务器实例发送 RCON 命令
- ✅ **备份与恢复**：创建和恢复世界备份，具有灵活的选项
- ✅ **配置管理**：查看和编辑游戏配置文件（Game.ini、GameUserSettings.ini）
- ✅ **日志记录**：实时查看服务器和实例日志

### 高级功能
- ✅ **Windows 服务集成**：作为 Windows 服务运行，实现后台操作
- ✅ **HTTP API 服务器**：支持 WebSocket 和 SSE 的 RESTful API，提供实时更新
- ✅ **Web 仪表盘**：嵌入式 Vue.js Web 界面，实现直观管理
- ✅ **FRP 集成**：内置快速反向代理管理，用于服务器暴露
- ✅ **Syncthing 集成**：用于服务器配置的文件同步功能
- ✅ **端口管理**：自动端口冲突检测和解决

## 系统要求

- **操作系统**：Windows 10/11（64位）
- **Go 版本**：1.25.4 或更高（用于开发）
- **Node.js**：16.x 或更高（用于前端开发）
- **磁盘空间**：服务器文件和实例至少需要 20GB
- **内存**：最小 8GB，推荐 16GB

## 安装

### 预构建二进制文件

1. 从 [Releases](https://github.com/yourusername/asa-server/releases) 页面下载最新版本
2. 将归档文件解压缩到您的首选位置
3. 运行 `asa-manager.exe` 启动应用程序

### 从源代码构建

#### 后端（Go）
```bash
go mod tidy
go build -o asa-manager.exe
```

#### 前端（Vue.js）
```bash
cd app
npm install
npm run build
```

## 使用方法

### 命令行界面

#### 基本命令

```bash
# 列出所有实例
asa-manager list

# 创建新实例
asa-manager create

# 启动实例
asa-manager start <instance_name>

# 停止实例
asa-manager stop <instance_name>

# 重启实例
asa-manager restart <instance_name>

# 检查服务器状态
asa-manager status [instance_name]
```

#### 备份与恢复

```bash
# 创建备份
asa-manager backup <instance_name> <world_folder>

# 恢复备份
asa-manager restore <instance_name> <backup_file> [flags]
```

可用的恢复标志：
- `--worldfile`：恢复世界文件（SaveDir）
- `--instance-config`：恢复 instance_config.ini
- `--game-config`：恢复游戏配置文件

#### 批量操作

```bash
# 启动所有实例
asa-manager start-all

# 停止所有实例
asa-manager stop-all
```

#### 配置管理

```bash
# 查看实例的 Game.ini
asa-manager view-game [instance_name]

# 查看实例的 GameUserSettings.ini
asa-manager view-game-user-settings [instance_name]

# 同步游戏配置文件
asa-manager sync-config <instance_name> [instance_name2] [...]
```

#### Windows 服务管理

```bash
# 安装为 Windows 服务
asa-manager service install

# 启动 Windows 服务
asa-manager service start

# 停止 Windows 服务
asa-manager service stop

# 移除 Windows 服务
asa-manager service remove
```

#### HTTP API 服务器

```bash
# 启动 HTTP API 服务器
asa-manager api
```

API 服务器默认在端口 19193 上运行。

### Web 仪表盘

HTTP API 服务器运行后，可通过以下地址访问 Web 仪表盘：
```
http://localhost:19193
```

仪表盘提供用户友好的界面，用于：
- 管理服务器实例
- 控制服务器操作
- 查看实时日志
- 编辑配置
- 管理备份
- 监控服务器资源

## 项目结构

### 目录结构

```
d:\golang\asa-server\
├── actions/             # 命令行操作处理器
├── app/                 # Vue.js 前端应用程序
├── asaserver/           # 核心服务器管理逻辑
├── backup/              # 备份和恢复功能
├── docs/                # 文档文件
├── frpmanage/           # FRP 集成
├── logger/              # 日志系统
├── processjob/          # 进程管理
├── serverinfo/          # 服务器信息收集
├── syncthingmanage/     # Syncthing 集成
├── tui/                 # 基于文本的用户界面组件
├── webapi/              # HTTP API 实现
├── win32api/            # Windows API 绑定
├── winservice/          # Windows 服务功能
├── main.go              # 应用程序入口点
└── README.md            # 本文件
```

### 实例目录结构

```
instances/
└── <instance_name>/
    ├── instance_config.ini    # 实例特定配置
    ├── Config/                # 游戏配置文件
    │   ├── Game.ini
    │   └── GameUserSettings.ini
    └── server.log             # 服务器日志文件
```

## HTTP API

HTTP API 为所有服务器管理功能提供 RESTful 端点。有关详细文档，请参阅 [API 文档](docs/API_GUIDE.md)。

### 主要 API 端点

- `GET /health` - 健康检查
- `GET /api/instances` - 列出所有实例
- `POST /api/instances` - 创建新实例
- `GET /api/instances/:name` - 获取实例状态
- `POST /api/server/:name/start` - 启动实例
- `POST /api/server/:name/stop` - 停止实例
- `POST /api/rcon/:name/command` - 发送 RCON 命令
- `GET /api/ws/events` - 实时事件的 WebSocket
- `GET /api/logs/:name` - 实例日志的 SSE 流

## 配置

### 实例配置

每个实例都有一个 `instance_config.ini` 文件，结构如下：

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

## 集成功能

### FRP 集成

该工具包含内置的 FRP（快速反向代理）管理，用于将服务器暴露到互联网。

### Syncthing 集成

Syncthing 集成允许跨多个服务器轻松同步配置文件。

## 开发

### 先决条件

- Go 1.25.4 或更高版本
- Node.js 16.x 或更高版本
- npm 8.x 或更高版本

### 构建应用程序

1. **构建后端**：
   ```bash
   go build -o asa-manager.exe
   ```

2. **构建前端**：
   ```bash
   cd app
   npm install
   npm run build
   ```

### 运行开发服务器

1. **启动 API 服务器**：
   ```bash
   go run main.go api
   ```

2. **启动前端开发服务器**：
   ```bash
   cd app
   npm run dev
   ```

### 添加新功能

1. **CLI 命令**：在 `actions/actions.go` 中添加新命令，并在 `main.go` 中注册
2. **API 端点**：在 `webapi/actions.go` 中添加新端点
3. **前端组件**：在 `app/src/components/` 中添加新组件
4. **前端视图**：在 `app/src/views/` 中添加新视图

## 故障排除

### 常见问题

#### 命令未找到
确保 `asa-manager.exe` 在您的 PATH 中，或从正确的目录运行它。

#### 实例启动失败
- 检查实例配置是否正确
- 验证所需端口是否未被占用
- 查看服务器日志以获取更多详细信息

#### 备份失败
- 确保在创建备份之前实例已停止
- 验证世界文件夹名称是否正确
- 确保有足够的磁盘空间

#### API 服务器无法访问
- 检查服务是否正在运行
- 验证防火墙设置是否允许端口 19193 上的流量

## 贡献

欢迎贡献！请随时提交 Pull Request。

## 许可证

MIT 许可证

## 支持

对于问题和功能请求，请创建新的 [GitHub Issue](https://github.com/yourusername/asa-server/issues)。
