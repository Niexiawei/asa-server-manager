# ARK 存档文件解析方案 - Python + Go CLI 集成

## 一、背景

需要解析 ARK: Survival Ascended 的 `.ark` 世界存档文件，提取玩家和部落信息。ASA 基于 UE5 开发，存档使用 UE5 序列化格式。Go 生态无现成解析库，采用 Python `arkparse` 库 + Go CLI 调用的方案。

## 二、技术选型

| 组件 | 选择 | 说明 |
|------|------|------|
| 解析库 | [arkparse](https://github.com/VincentHenauGithub/ark-save-parser) | Python，最新维护中（2026-04），支持 ASA |
| 集成方式 | Go `os/exec` 调用 PyInstaller 打包的 exe | exe 输出 JSON，Go 解析 JSON |
| 缓存存储 | BadgerDB | 独立实例，持久化缓存解析结果 |
| 文件监控 | fsnotify | 监控 .ark 文件变动，自动触发解析 |
| 实时推送 | SSE (Server-Sent Events) | 实时推送解析进度和结果 |

### arkparse 库能力

- 解析 `.ark` 世界存档（玩家、部落、恐龙、建筑等）
- 解析 `.arkprofile` 玩家存档
- 解析 `.arktribe` 部落存档
- 支持 `-usestore` 模式（玩家/部落数据嵌入世界文件）
- 导出为 JSON

### 核心数据结构

```python
# 玩家数据 (ArkPlayer)
{
    "id_": 123456789,           # PlayerDataID
    "name": "Steam用户名",       # PlayerName
    "char_name": "角色名",       # PlayerCharacterName
    "unique_id": "xxx",          # UniqueID
    "tribe": 100,                # TribeID
    "ip_address": "1.2.3.4",    # SavedNetworkAddress
    "first_spawned": true,
    "nr_of_deaths": 3,
    "login_time": 1234567890.0,
    "last_time_died": 1234567890.0
}

# 部落数据 (ArkTribe)
{
    "tribe_id": 100,
    "tribe_name": "部落名称",
    "member_ids": [123456789, ...],
    "members": ["玩家名1", "玩家名2", ...]
}
```

## 三、架构设计

```
asa-server (Go)
│
├── parseserver/                    # 解析服务包
│   ├── parser.go                   # 调用 parse_save.exe，解析 JSON 输出
│   ├── embed.go                    # //go:embed 内嵌 parse_save.exe
│   ├── types.go                    # 数据类型定义
│   ├── save_monitor.go             # 后台监控 + BadgerDB 缓存 + SSE 广播
│   ├── parser_test.go              # Go 集成测试
│   └── scripts/
│       ├── parse_save.py           # Python 解析脚本（源码）
│       ├── requirements.txt        # Python 依赖
│       ├── build_exe.bat           # PyInstaller 构建脚本
│       ├── test_parse_save.py      # Python 单元测试
│       └── dist/
│           └── parse_save.exe      # PyInstaller 打包的 exe（内嵌到二进制）
│
├── webapi/                         # HTTP API 服务
│   ├── actions.go                  # 路由注册，SaveDataManager 生命周期
│   └── api.go                      # API 端点实现
│
└── database_file/                  # BadgerDB 存储目录
    └── arkworldsave/               # 存档解析缓存数据库
```

### 调用流程

```
HTTP 请求 (GET /api/save/:instance/players)
    │
    ▼
webapi handler
    │
    ├─→ 优先读取 BadgerDB 缓存 ─→ 有缓存直接返回 (<100ms)
    │
    └─→ 无缓存时同步解析
            │
            ▼
        parseserver.ParseSave(savePath)
            │
            ▼
        从 embed.FS 解压 parse_save.exe 到临时目录
            │
            ▼
        os/exec: {tempDir}/parse_save.exe --save /path/to/file.ark --type players
            │
            ▼
        Python: arkparse 解析 .ark 文件 → JSON → stdout
            │
            ▼
        Go: json.Unmarshal → 存入 BadgerDB 缓存 → 返回结果
```

### 后台监控流程

```
fsnotify 事件 (.ark 文件变动)
    │
    ▼
debounce (等待 5s 稳定)
    │
    ▼
ParseSave(ctx, savePath, "all")
    │
    ▼
SaveData → JSON → BadgerDB (key: save:{instanceName}:all)
    │
    ▼
SaveBroadcaster 推送 SSE 事件
    │
    ├─→ {"type":"start","map":"instance_name","timestamp":...}
    ├─→ {"type":"complete","map":"instance_name","players":15,"tribes":3}
    └─→ {"type":"data","map":"instance_name","data":{...}}
```

## 四、Python 脚本设计

### 脚本路径
```
parseserver/scripts/parse_save.py
```

### 脚本功能

接收命令行参数，输出 JSON 到 stdout：

```bash
# 获取玩家列表
parse_save.exe --save /path/to/Aberration_WP.ark --type players

# 获取部落列表
parse_save.exe --save /path/to/Aberration_WP.ark --type tribes

# 获取全部数据（玩家+部落+关系映射）
parse_save.exe --save /path/to/Aberration_WP.ark --type all

# 指定输出格式
parse_save.exe --save /path/to/Aberration_WP.ark --type players --format json
```

### 输出格式

**玩家列表** (`--type players`):
```json
{
  "success": true,
  "data": {
    "players": [
      {
        "id": 123456789,
        "name": "SteamUser",
        "character_name": "InGameName",
        "unique_id": "xxxx-xxxx",
        "tribe_id": 100,
        "ip_address": "1.2.3.4",
        "first_spawned": true,
        "deaths": 3,
        "login_time": 1234567890.0,
        "last_died": 1234567890.0
      }
    ],
    "player_total": 1
  }
}
```

**部落列表** (`--type tribes`):
```json
{
  "success": true,
  "data": {
    "tribes": [
      {
        "tribe_id": 100,
        "tribe_name": "TestTribe",
        "member_count": 3,
        "members": [
          {"id": 123, "name": "Player1", "character_name": "Char1", "active": true},
          {"id": 456, "name": "Player2", "character_name": "Char2", "active": true},
          {"id": 789, "name": "Player3", "character_name": "Char3", "active": false}
        ]
      }
    ],
    "tribe_total": 1
  }
}
```

**全部数据** (`--type all`):
```json
{
  "success": true,
  "data": {
    "players": [...],
    "tribes": [...],
    "player_tribe_map": {"123": 100, "456": 100},
    "tribe_player_map": {"100": [123, 456, 789]}
  }
}
```

**错误输出**:
```json
{
  "success": false,
  "error": "Save file not found: /path/to/file.ark"
}
```

## 五、Go 集成设计

### 数据类型 (`parseserver/types.go`)

```go
package parseserver

type SaveParseResult struct {
    Success bool         `json:"success"`
    Data    *SaveData    `json:"data,omitempty"`
    Error   string       `json:"error,omitempty"`
}

type SaveData struct {
    Players         []PlayerInfo      `json:"players,omitempty"`
    Tribes          []TribeInfo       `json:"tribes,omitempty"`
    PlayerTribeMap  map[int64]int64   `json:"player_tribe_map,omitempty"`
    TribePlayerMap  map[int64][]int64 `json:"tribe_player_map,omitempty"`
}

type PlayerInfo struct {
    ID            int64   `json:"id"`
    Name          string  `json:"name"`
    CharacterName string  `json:"character_name"`
    UniqueID      string  `json:"unique_id"`
    TribeID       int64   `json:"tribe_id"`
    IPAddress     string  `json:"ip_address"`
    FirstSpawned  bool    `json:"first_spawned"`
    Deaths        int     `json:"deaths"`
    LoginTime     float64 `json:"login_time"`
    LastDied      float64 `json:"last_died"`
}

type TribeInfo struct {
    TribeID     int64        `json:"tribe_id"`
    TribeName   string       `json:"tribe_name"`
    MemberCount int          `json:"member_count"`
    Members     []MemberInfo `json:"members"`
}

type MemberInfo struct {
    ID            int64  `json:"id"`
    Name          string `json:"name"`
    CharacterName string `json:"character_name"`
    Active        bool   `json:"active"`
}
```

### 解析器 (`parseserver/parser.go`)

```go
package parseserver

import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "sync"
    "time"
)

type ParseType string

const (
    ParseTypePlayers ParseType = "players"
    ParseTypeTribes  ParseType = "tribes"
    ParseTypeAll     ParseType = "all"
)

var parseMu sync.Mutex

// ParseSave parses an ARK save file by calling the parse_save.exe
func ParseSave(ctx context.Context, savePath string, parseType ParseType) (*SaveParseResult, error) {
    // Serialize parse operations to avoid concurrent processes
    parseMu.Lock()
    defer parseMu.Unlock()

    exePath, err := getExePath()
    if err != nil {
        return nil, err
    }

    ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
    defer cancel()

    cmd := exec.CommandContext(ctx, exePath,
        "--save", savePath,
        "--type", string(parseType),
    )

    output, err := cmd.CombinedOutput()
    if err != nil {
        return nil, fmt.Errorf("parse_save.exe failed: %w, output: %s", err, string(output))
    }

    var result SaveParseResult
    if err := json.Unmarshal(output, &result); err != nil {
        return nil, fmt.Errorf("failed to parse JSON output: %w, raw: %s", err, string(output))
    }

    if !result.Success {
        return nil, fmt.Errorf("parse failed: %s", result.Error)
    }

    return &result, nil
}

func getExePath() (string, error) {
    exe, err := os.Executable()
    if err != nil {
        return "", fmt.Errorf("failed to get executable path: %w", err)
    }
    exeDir := filepath.Dir(exe)

    // Multi-path lookup
    paths := []string{
        filepath.Join(exeDir, "parse_save.exe"),
        filepath.Join(exeDir, "scripts", "parse_save.exe"),
        filepath.Join(exeDir, "scripts", "dist", "parse_save.exe"),
        filepath.Join("parseserver", "scripts", "parse_save.exe"),
    }

    for _, p := range paths {
        if _, err := os.Stat(p); err == nil {
            return p, nil
        }
    }

    return "", fmt.Errorf("parse_save.exe not found, please build it first: parseserver/scripts/build_exe.bat")
}

// IsParseExeAvailable checks if parse_save.exe exists
func IsParseExeAvailable() bool {
    _, err := getExePath()
    return err == nil
}
```

### 后台监控 + BadgerDB 缓存 (`parseserver/save_monitor.go`)

```go
package parseserver

import (
    "asa-server/asaserver"
    "asa-server/logger"
    "context"
    "encoding/json"
    "fmt"
    "path/filepath"
    "sync"
    "time"

    "github.com/dgraph-io/badger/v4"
    "github.com/fsnotify/fsnotify"
)

const (
    saveKeyPrefix = "save:"
    saveKeyAll    = "save:%s:all"
)

// SaveDataManager manages BadgerDB cache and file monitoring for ARK save files
type SaveDataManager struct {
    db            *badger.DB
    mu            sync.RWMutex
    watchers      map[string]context.CancelFunc
    broadcasters  map[string]*SaveBroadcaster
    parseMu       sync.Mutex
}

// SaveBroadcaster fans out save events to SSE subscribers
type SaveBroadcaster struct {
    msgChan      chan []byte
    subscribers  map[chan []byte]struct{}
    subMu        sync.Mutex
}

// NewSaveDataManager creates a new SaveDataManager with its own BadgerDB instance
func NewSaveDataManager() (*SaveDataManager, error) {
    dbPath := filepath.Join(asaserver.BaseDir, "database_file", "arkworldsave")
    opts := badger.DefaultOptions(dbPath).
        WithLogger(nil).
        WithNumVersionsToKeep(1)

    db, err := badger.Open(opts)
    if err != nil {
        return nil, fmt.Errorf("failed to open arkworldsave badger db: %w", err)
    }

    return &SaveDataManager{
        db:           db,
        watchers:     make(map[string]context.CancelFunc),
        broadcasters: make(map[string]*SaveBroadcaster),
    }, nil
}

// SetCached stores parsed save data in BadgerDB
func (m *SaveDataManager) SetCached(instanceName string, data *SaveData) error {
    return m.db.Update(func(txn *badger.Txn) error {
        key := []byte(fmt.Sprintf(saveKeyAll, instanceName))
        jsonBytes, err := json.Marshal(data)
        if err != nil {
            return err
        }
        return txn.Set(key, jsonBytes)
    })
}

// GetCached retrieves cached save data from BadgerDB
func (m *SaveDataManager) GetCached(instanceName string) (*SaveData, error) {
    var data SaveData
    err := m.db.View(func(txn *badger.Txn) error {
        key := []byte(fmt.Sprintf(saveKeyAll, instanceName))
        item, err := txn.Get(key)
        if err != nil {
            return err
        }
        return item.Value(func(val []byte) error {
            return json.Unmarshal(val, &data)
        })
    })
    if err != nil {
        return nil, err
    }
    return &data, nil
}

// Start begins monitoring all instance save directories for changes
func (m *SaveDataManager) Start(ctx context.Context) {
    instances, err := asaserver.GetAvailableInstances()
    if err != nil {
        logger.GetLogger().Errorf("Failed to get instances for save monitoring: %v", err)
        return
    }

    for _, instanceName := range instances {
        config, err := asaserver.LoadInstanceConfig(instanceName)
        if err != nil {
            continue
        }
        arkPath := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved/SavedArks",
            config.SaveDir, config.MapName, config.MapName+".ark")

        go m.watchFile(ctx, instanceName, arkPath)
    }
}

// watchFile monitors a single .ark file for changes
func (m *SaveDataManager) watchFile(ctx context.Context, instanceName, arkPath string) {
    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        logger.GetLogger().Errorf("Failed to create watcher for %s: %v", instanceName, err)
        return
    }
    defer watcher.Close()

    if err := watcher.Add(arkPath); err != nil {
        logger.GetLogger().Warnf("Failed to watch %s: %v (file may not exist yet)", arkPath, err)
        return
    }

    var debounceTimer *time.Timer
    debounceDuration := 5 * time.Second

    for {
        select {
        case <-ctx.Done():
            if debounceTimer != nil {
                debounceTimer.Stop()
            }
            return
        case event, ok := <-watcher.Events:
            if !ok {
                return
            }
            if event.Name == arkPath && (event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create) {
                if debounceTimer != nil {
                    debounceTimer.Stop()
                }
                debounceTimer = time.AfterFunc(debounceDuration, func() {
                    m.parseAndCache(ctx, instanceName, arkPath)
                })
            }
        case err, ok := <-watcher.Errors:
            if !ok {
                return
            }
            logger.GetLogger().Warnf("Save watcher error for %s: %v", instanceName, err)
        }
    }
}

// parseAndCache triggers parsing and stores result in BadgerDB
func (m *SaveDataManager) parseAndCache(ctx context.Context, instanceName, arkPath string) {
    m.parseMu.Lock()
    defer m.parseMu.Unlock()

    broadcaster := m.GetBroadcaster(instanceName)

    // Broadcast start event
    startMsg, _ := json.Marshal(map[string]interface{}{
        "type":      "start",
        "map":       instanceName,
        "timestamp": time.Now().Unix(),
    })
    broadcaster.Broadcast(startMsg)

    // Parse
    result, err := ParseSave(ctx, arkPath, ParseTypeAll)
    if err != nil {
        errMsg, _ := json.Marshal(map[string]interface{}{
            "type":  "error",
            "map":   instanceName,
            "error": err.Error(),
        })
        broadcaster.Broadcast(errMsg)
        return
    }

    // Cache to BadgerDB
    if err := m.SetCached(instanceName, result.Data); err != nil {
        logger.GetLogger().Errorf("Failed to cache save data for %s: %v", instanceName, err)
    }

    // Broadcast complete event
    completeMsg, _ := json.Marshal(map[string]interface{}{
        "type":    "complete",
        "map":     instanceName,
        "players": len(result.Data.Players),
        "tribes":  len(result.Data.Tribes),
    })
    broadcaster.Broadcast(completeMsg)

    // Broadcast full data
    dataMsg, _ := json.Marshal(map[string]interface{}{
        "type": "data",
        "map":  instanceName,
        "data": result.Data,
    })
    broadcaster.Broadcast(dataMsg)
}

// Stop closes the BadgerDB and all watchers
func (m *SaveDataManager) Stop() {
    m.mu.Lock()
    for _, cancel := range m.watchers {
        cancel()
    }
    for _, b := range m.broadcasters {
        b.Stop()
    }
    m.mu.Unlock()

    if m.db != nil {
        m.db.Close()
    }
}
```

### API 端点 (`webapi/api.go`)

所有 API 使用**实例名称**作为路径参数（不同实例可能使用相同地图名）：

| 端点 | 说明 |
|------|------|
| `GET /api/save/:instance/players` | 获取实例玩家列表 |
| `GET /api/save/:instance/tribes` | 获取实例部落列表 |
| `GET /api/save/:instance/all` | 获取全部数据（优先读缓存） |
| `GET /api/save/:instance/stream` | SSE 实时推送存档数据 |

### 缓存读取逻辑

```go
func (s *APIServer) getSaveAll(c *gin.Context) {
    instanceName := c.Param("instance")

    // 1. 先查 BadgerDB 缓存
    cached, err := s.saveDataManager.GetCached(instanceName)
    if err == nil && cached != nil {
        c.JSON(200, gin.H{"success": true, "data": cached})
        return
    }

    // 2. 无缓存，同步解析
    savePath, err := findSaveFileByInstance(instanceName)
    if err != nil {
        c.JSON(404, gin.H{"error": err.Error()})
        return
    }

    result, err := parseserver.ParseSave(c.Request.Context(), savePath, parseserver.ParseTypeAll)
    if err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }

    // 3. 存入缓存
    s.saveDataManager.SetCached(instanceName, result.Data)

    c.JSON(200, gin.H{"success": true, "data": result.Data})
}
```

### SSE 流式接口

```go
func (s *APIServer) streamSaveData(c *gin.Context) {
    instanceName := c.Param("instance")

    // SSE headers
    c.Header("Content-Type", "text/event-stream")
    c.Header("Cache-Control", "no-cache")
    c.Header("Connection", "keep-alive")

    // 立即发送缓存数据
    cached, err := s.saveDataManager.GetCached(instanceName)
    if err == nil && cached != nil {
        dataMsg, _ := json.Marshal(map[string]interface{}{
            "type": "cached",
            "map":  instanceName,
            "data": cached,
        })
        fmt.Fprintf(c.Writer, "data: %s\n\n", dataMsg)
        c.Writer.Flush()
    }

    // 订阅实时更新
    broadcaster := s.saveDataManager.GetBroadcaster(instanceName)
    ch, unsubscribe := broadcaster.Subscribe()
    defer unsubscribe()

    ctx := c.Request.Context()
    for {
        select {
        case <-ctx.Done():
            return
        case msg, ok := <-ch:
            if !ok {
                return
            }
            fmt.Fprintf(c.Writer, "data: %s\n\n", msg)
            c.Writer.Flush()
        }
    }
}
```

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

// 解析错误
data: {"type":"error","map":"instance_name","error":"error message"}
```

### 路由注册 (`webapi/actions.go`)

```go
// Save parse endpoints
save := s.engine.Group("/api/save")
{
    save.GET("/:instance/players", s.getSavePlayers)
    save.GET("/:instance/tribes", s.getSaveTribes)
    save.GET("/:instance/all", s.getSaveAll)
    save.GET("/:instance/stream", s.streamSaveData)
}
```

### 生命周期管理

```go
// NewAPIServer() 中初始化
saveDataManager, err := parseserver.NewSaveDataManager()

// Start() 中启动监控
go s.saveDataManager.Start(s.serverCtx)

// Stop() 中关闭
s.saveDataManager.Stop()
```

## 六、目录结构变更

```
asa-server/
├── parseserver/                    # 解析服务包
│   ├── parser.go                   # 调用 parse_save.exe
│   ├── embed.go                    # //go:embed 内嵌 parse_save.exe
│   ├── types.go                    # 数据类型定义
│   ├── save_monitor.go             # 后台监控 + BadgerDB + SSE 广播
│   ├── parser_test.go              # Go 集成测试
│   └── scripts/
│       ├── parse_save.py           # Python 解析脚本（源码）
│       ├── requirements.txt        # Python 依赖
│       ├── build_exe.bat           # PyInstaller 构建脚本
│       ├── test_parse_save.py      # Python 单元测试
│       └── dist/
│           └── parse_save.exe      # PyInstaller 打包的 exe（内嵌到二进制）
├── webapi/
│   ├── actions.go                  # SaveDataManager 生命周期管理
│   └── api.go                      # save 解析 API 端点
├── database_file/
│   └── arkworldsave/               # BadgerDB 存档缓存目录
└── ...
```

## 七、部署要求

### 打包方案（PyInstaller → 内嵌 exe）

使用 PyInstaller 将 Python 脚本打包为 exe，然后通过 `//go:embed` 内嵌到 Go 二进制中。运行时自动解压到临时目录执行。

#### 打包步骤

```bash
# 1. 安装依赖
pip install -r parseserver/scripts/requirements.txt pyinstaller

# 2. 构建 exe
cd parseserver/scripts
pyinstaller --onefile --name parse_save --clean parse_save.py

# 3. 复制到 dist 目录（供 go:embed 使用）
cp dist/parse_save.exe scripts/dist/
```

或直接运行构建脚本：`parseserver/scripts/build_exe.bat`

#### Go 内嵌机制

```go
// parseserver/embed.go
//go:embed scripts/dist/parse_save.exe
var parseSaveExeFS embed.FS
```

- 运行时自动解压到 `{tempDir}/parse_save.exe`
- 程序退出时自动清理临时目录
- 单个二进制文件部署，无需额外文件

#### 文件部署位置

```
{BaseDir}/
├── asa-server.exe          # 主程序（内嵌 parse_save.exe）
├── database_file/
│   └── arkworldsave/       # BadgerDB 缓存目录（自动创建）
└── ...
```

## 八、性能考虑

| 因素 | 说明 |
|------|------|
| 解析耗时 | 大型存档（>500MB）可能需要 30-60 秒 |
| 内存占用 | Python 进程约 200-500MB |
| 并发控制 | 使用 mutex 限制同时只有一个解析任务 |
| 缓存 | BadgerDB 持久化缓存，重启后仍有效 |
| 超时 | 设置 5 分钟超时，防止长时间阻塞 |
| 响应时间 | 有缓存：<100ms；无缓存：30-60s |

### 已实现的优化

1. **BadgerDB 缓存**: 解析结果持久化存储，API 优先读取缓存
2. **后台监控**: fsnotify 监控 .ark 文件变动，自动触发解析
3. **SSE 实时推送**: 解析进度和结果实时推送到前端
4. **Debounce**: 5s 防抖，避免频繁触发解析
5. **实例级别**: 使用实例名称而非地图名，支持多实例共享地图

## 九、错误处理

| 错误场景 | 处理方式 |
|----------|----------|
| parse_save.exe 不存在 | 启动时检测，返回 503 Service Unavailable |
| 存档文件不存在 | 返回 404 Not Found |
| 解析超时 | 返回 504 Gateway Timeout |
| 解析失败 | 返回 500 Internal Server Error + 错误详情 |
| 存档格式不支持 | 返回 400 Bad Request |
| BadgerDB 写入失败 | 记录日志，不影响 API 响应 |

## 十、测试方案

### Go 集成测试 (`parseserver/parser_test.go`)

```go
func TestParseSave_Players(t *testing.T) {
    result, err := ParseSave(context.Background(), testSavePath, ParseTypePlayers)
    // 验证解析结果
}

func TestParseSave_Tribes(t *testing.T) {
    result, err := ParseSave(context.Background(), testSavePath, ParseTypeTribes)
    // 验证部落数据
}

func TestParseSave_All(t *testing.T) {
    result, err := ParseSave(context.Background(), testSavePath, ParseTypeAll)
    // 验证完整数据
}
```

### Python 单元测试 (`parseserver/scripts/test_parse_save.py`)

```python
class TestParseSavePlayers(unittest.TestCase):
    def test_parse_players(self):
        result = parse_save_file(TEST_SAVE_FILE, "players")
        self.assertTrue(result["success"])
        self.assertIn("players", result["data"])

class TestParseSaveAll(unittest.TestCase):
    def test_parse_all(self):
        result = parse_save_file(TEST_SAVE_FILE, "all")
        self.assertTrue(result["success"])
        self.assertIn("player_tribe_map", result["data"])
```

## 十一、实施步骤

1. 创建 `parseserver/` 目录结构
2. 编写 `parse_save.py` Python 脚本和 `requirements.txt`
3. 编写 `parseserver/parser.go` 和 `types.go`
4. 实现 `parseserver/save_monitor.go`（后台监控 + BadgerDB + SSE）
5. 创建 `parseserver/embed.go` 内嵌 `parse_save.exe`
6. 在 `webapi/actions.go` 中注册路由和生命周期管理
7. 在 `webapi/api.go` 中实现 API 端点
8. 使用 PyInstaller 打包为 `parse_save.exe`
9. 将 `parse_save.exe` 放到 `parseserver/scripts/dist/` 目录
10. 集成到现有项目构建流程
