# ARK 存档文件解析方案 - go-arkparser 原生 Go 解析

## 一、背景

需要解析 ARK: Survival Ascended 的 `.ark` 世界存档文件，提取玩家和部落信息。现采用 `go-arkparser` 纯 Go 原生库直接解析，无需外部进程或 Python 依赖。

> **历史方案**: 此前使用 Python `arkparse` 库 + PyInstaller 打包 `parse_save.exe` + `os/exec` 调用的方式，已于 2026-06 迁移至纯 Go 方案。旧方案需要维护 Python 脚本、打包流程和嵌入式 exe，新方案消除了这些复杂性。

## 二、技术选型

| 组件 | 选择 | 说明 |
|------|------|------|
| 解析库 | [go-arkparser](https://github.com/Niexiawei/go-arkparser) | 纯 Go 实现，编译时集成，无外部依赖 |
| 存档加载 | `go-arkparser/files` | 支持标准 IO 和 mmap 内存映射 |
| 持续监控 | `go-arkparser/arkmonitor` | 内置文件监控 + Observer 模式 + 快照 |
| 缓存存储 | BadgerDB | 独立实例，持久化缓存解析结果 |
| 实时推送 | SSE (Server-Sent Events) | 实时推送解析进度和结果 |

### go-arkparser 库结构

| 子包 | 功能 |
|------|------|
| `github.com/Niexiawei/go-arkparser` | 核心导出：`ExportPlayers()`, `ExportTribes()` |
| `github.com/Niexiawei/go-arkparser/files` | 存档加载：`LoadWorldSave()`, `WithMmap()` |
| `github.com/Niexiawei/go-arkparser/common` | 地图配置：`GetMapConfig()` |
| `github.com/Niexiawei/go-arkparser/arkmonitor` | 持续监控：`MonitorImpl`, `WorldSnapshot`, `Observer` 接口 |

## 三、架构设计

```
asa-server (Go)
│
├── parseserver/                    # 解析服务包
│   ├── parser.go                   # 按需解析：调用 go-arkparser 导出玩家/部落
│   ├── types.go                    # 数据类型定义（SaveParseResult / CachedSaveData）
│   ├── save_monitor.go             # 后台监控 + BadgerDB 缓存 + SSE 广播
│   └── parser_test.go              # 集成测试
│
├── webapi/                         # HTTP API 服务
│   ├── actions.go                  # 路由注册，SaveDataManager 生命周期
│   └── api.go                      # API 端点实现
│
└── database_file/                  # BadgerDB 存储目录
    └── arkworldsave/               # 存档解析缓存数据库
```

### 两条解析路径

本方案包含两条独立的解析路径：

**路径 A — 按需解析** (`parser.go`)：HTTP 请求触发，直接调用 `go-arkparser` 导出函数。

**路径 B — 持续监控** (`save_monitor.go`)：`arkmonitor.MonitorImpl` 自动监控 `.ark` 文件变动，通过 Observer 模式触发快照缓存和 SSE 广播。

## 四、按需解析 (`parser.go`)

### 核心函数

```go
func ParseSave(ctx context.Context, savePath string, parseType ParseType) (*SaveParseResult, error)
```

### 解析流程

```
HTTP 请求 (GET /api/save/:instance/all)
    │
    ▼
webapi handler
    │
    ├─→ 优先读取 BadgerDB 缓存 ─→ 有缓存直接返回 (<100ms)
    │
    └─→ 无缓存时同步解析
            │
            ▼
        parseserver.ParseSave(savePath, parseType)
            │
            ├── files.LoadWorldSave(savePath)        // 加载 .ark 文件
            ├── common.GetMapConfig(filename)         // 获取地图配置
            │
            ├── type="players" → goarkparser.ExportPlayers()
            ├── type="tribes"  → goarkparser.ExportTribes()
            └── type="all"     → 两者 + 构建双向映射
                    │
                    ▼
                SaveParseResult → 存入 BadgerDB → 返回 JSON
```

### 实现代码

```go
package parseserver

import (
    "context"
    "fmt"
    "path/filepath"

    goarkparser "github.com/Niexiawei/go-arkparser"
    "github.com/Niexiawei/go-arkparser/common"
    "github.com/Niexiawei/go-arkparser/files"
)

func ParseSave(ctx context.Context, savePath string, parseType ParseType) (*SaveParseResult, error) {
    ws, err := files.LoadWorldSave(savePath)
    if err != nil {
        return nil, fmt.Errorf("failed to load world save: %w", err)
    }

    mapConfig := common.GetMapConfig(filepath.Base(savePath))

    switch parseType {
    case ParseTypePlayers:
        players := goarkparser.ExportPlayers(nil, ws, mapConfig, nil)
        return &SaveParseResult{Success: true, Data: &SaveData{Players: players}}, nil

    case ParseTypeTribes:
        tribes := goarkparser.ExportTribes(ws, nil, nil)
        return &SaveParseResult{Success: true, Data: &SaveData{Tribes: tribes}}, nil

    case ParseTypeAll:
        players := goarkparser.ExportPlayers(nil, ws, mapConfig, nil)
        tribes := goarkparser.ExportTribes(ws, nil, nil)

        playerTribeMap := make(map[int64]int64)
        tribePlayerMap := make(map[int64][]int64)

        for _, p := range players {
            playerID := toInt64(p["playerid"])
            tribeID := toInt64(p["tribeid"])
            if playerID > 0 && tribeID > 0 {
                playerTribeMap[playerID] = tribeID
                tribePlayerMap[tribeID] = append(tribePlayerMap[tribeID], playerID)
            }
        }

        return &SaveParseResult{Success: true, Data: &SaveData{
            Players:        players,
            Tribes:         tribes,
            PlayerTribeMap: playerTribeMap,
            TribePlayerMap: tribePlayerMap,
        }}, nil

    default:
        return nil, fmt.Errorf("unknown parse type: %s", parseType)
    }
}
```

### 数据类型 (`types.go`)

```go
// SaveParseResult — 按需解析的 API 返回结构
type SaveParseResult struct {
    Success bool      `json:"success"`
    Data    *SaveData `json:"data,omitempty"`
    Error   string    `json:"error,omitempty"`
}

// SaveData — 按需解析的玩家/部落数据（untyped map，由 go-arkparser 导出）
type SaveData struct {
    Players        []map[string]any  `json:"players,omitempty"`
    Tribes         []map[string]any  `json:"tribes,omitempty"`
    PlayerTribeMap map[int64]int64   `json:"player_tribe_map,omitempty"`
    TribePlayerMap map[int64][]int64 `json:"tribe_player_map,omitempty"`
}

// CachedSaveData — 持续监控的快照缓存（strongly-typed arkmonitor 类型）
type CachedSaveData struct {
    Players   map[int]*arkmonitor.PlayerSnapshot `json:"players,omitempty"`
    Tribes    map[int]*arkmonitor.TribeSnapshot   `json:"tribes,omitempty"`
    Timestamp time.Time                           `json:"timestamp"`
}
```

**注意**: 两条路径使用不同的数据类型：
- **按需解析** → `SaveData`（`[]map[string]any`，untyped，直接序列化 go-arkparser 导出结果）
- **持续监控** → `CachedSaveData`（`map[int]*arkmonitor.PlayerSnapshot`，strongly-typed，来自 arkmonitor 快照）

## 五、持续监控 (`save_monitor.go`)

### 核心组件

```
SaveDataManager
    ├── BadgerDB              // 持久化缓存
    ├── monitors map[string]*arkmonitor.MonitorImpl   // 每实例一个监控器
    ├── broadcasters map[string]*SaveBroadcaster       // 每实例一个 SSE 广播器
    └── unsubs []func()        // Observer 取消订阅函数
```

### 监控流程

```
arkmonitor.MonitorImpl 检测到 .ark 文件变动
    │
    ▼
saveObserver.OnEvent(event)
    │
    ▼
monitor.Snapshot() → *arkmonitor.WorldSnapshot
    │
    ▼
cacheAndBroadcast(instanceName, snap)
    │
    ├── 构建 CachedSaveData{Players, Tribes, Timestamp}
    ├── BadgerDB 持久化缓存
    │
    └── SaveBroadcaster 广播 SSE 事件
            ├── {"type":"start","map":"...","timestamp":...}
            ├── {"type":"complete","map":"...","players":15,"tribes":3}
            └── {"type":"data","map":"...","data":{...}}
```

### Monitor 配置

```go
cfg := arkmonitor.MonitorConfig{
    SavePath:    arkPath,                               // .ark 文件路径
    WatchDir:    true,                                   // 监控目录级变动
    EventBuffer: 100,                                    // 事件通道缓冲
    LazyMode:    true,                                   // 延迟加载模式
    LoadOptions: []files.LoadOption{files.WithMmap()},   // 使用 mmap 内存映射
}
```

### Observer 实现

```go
type saveObserver struct {
    manager      *SaveDataManager
    instanceName string
    monitor      *arkmonitor.MonitorImpl
}

func (o *saveObserver) OnEvent(event arkmonitor.Event) {
    snap := o.monitor.Snapshot()
    if snap != nil {
        o.manager.cacheAndBroadcast(o.instanceName, snap)
    }
}
```

### 生命周期管理

```go
// webapi/actions.go — NewAPIServer() 中初始化
saveDataManager, err := parseserver.NewSaveDataManager()

// Start() 中启动监控
go s.saveDataManager.Start(s.serverCtx)

// Stop() 中关闭
s.saveDataManager.Stop()
```

## 六、API 端点

所有 API 使用**实例名称**作为路径参数：

| 端点 | 说明 |
|------|------|
| `GET /api/save/:instance/players` | 获取实例玩家列表（按需解析） |
| `GET /api/save/:instance/tribes` | 获取实例部落列表（按需解析） |
| `GET /api/save/:instance/all` | 获取全部数据（优先读缓存） |
| `GET /api/save/:instance/stream` | SSE 实时推送存档数据（订阅监控） |

### SSE 事件格式

```
// 缓存数据（连接时立即发送）
data: {"type":"cached","map":"instance_name","data":{...}}

// 解析开始
data: {"type":"start","map":"instance_name","timestamp":1234567890}

// 解析完成
data: {"type":"complete","map":"instance_name","players":15,"tribes":3}

// 完整数据
data: {"type":"data","map":"instance_name","data":{...}}
```

## 七、目录结构

```
asa-server/
├── parseserver/
│   ├── parser.go               # 按需解析：go-arkparser 导出
│   ├── types.go                # 数据类型定义
│   ├── save_monitor.go         # 后台监控 + BadgerDB + SSE 广播
│   └── parser_test.go          # 集成测试
├── webapi/
│   ├── actions.go              # SaveDataManager 生命周期管理
│   └── api.go                  # save 解析 API 端点
├── database_file/
│   └── arkworldsave/           # BadgerDB 存档缓存目录（自动创建）
└── ...
```

## 八、性能考虑

| 因素 | 说明 |
|------|------|
| 解析耗时 | 按需解析取决于存档大小；监控路径使用 mmap 加速 |
| 内存占用 | 纯 Go 进程，无外部进程开销 |
| 并发控制 | 按需解析路径无锁（每次独立加载）；监控路径由 arkmonitor 内部管理 |
| 缓存 | BadgerDB 持久化缓存，重启后仍有效 |
| 响应时间 | 有缓存：<100ms；无缓存：取决于存档大小 |
| 文件监控 | arkmonitor 内置事件缓冲（100），LazyMode + mmap 优化加载 |

### 已实现的优化

1. **纯 Go 原生解析**: 编译时集成，无外部进程启动开销
2. **mmap 内存映射**: 监控路径使用 `files.WithMmap()` 高效加载存档
3. **arkmonitor Observer 模式**: 文件变动自动触发快照，无需手动 fsnotify + debounce
4. **BadgerDB 缓存**: 解析结果持久化存储，API 优先读取缓存
5. **SSE 实时推送**: 解析进度和结果实时推送到前端
6. **LazyMode**: 监控器延迟加载，减少启动时资源消耗

## 九、错误处理

| 错误场景 | 处理方式 |
|----------|----------|
| `LoadWorldSave` 失败 | 返回 `failed to load world save` 错误 |
| 存档文件不存在 | 返回 404 Not Found |
| Monitor 创建失败 | 记录日志，跳过该实例，不影响其他实例 |
| BadgerDB 写入失败 | 记录日志，不影响 API 响应 |
| 未知 ParseType | 返回 `unknown parse type` 错误 |
| BobsMissions_WP 地图 | 监控路径自动跳过 |

## 十、与旧方案对比

| 维度 | 旧方案（Python + exe） | 新方案（go-arkparser） |
|------|----------------------|----------------------|
| 依赖 | Python 3.x + arkparse + PyInstaller | 无外部依赖 |
| 部署 | 需内嵌 `parse_save.exe`（~30MB） | 单二进制，体积更小 |
| 启动开销 | `os/exec` 创建进程 + Python 启动 | 直接函数调用 |
| 文件监控 | 手动 fsnotify + 5s debounce | arkmonitor 内置监控 + Observer |
| 内存映射 | 不支持 | `files.WithMmap()` 支持 |
| 维护成本 | Python 脚本 + 打包流程 + 嵌入机制 | 纯 Go，`go get` 更新 |
| `IsParserAvailable()` | 检测 exe 是否存在 | 始终返回 `true` |
