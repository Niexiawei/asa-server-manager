# ARK 存档文件解析方案 - Python + Go CLI 集成

## 一、背景

需要解析 ARK: Survival Ascended 的 `.ark` 世界存档文件，提取玩家和部落信息。ASA 基于 UE5 开发，存档使用 UE5 序列化格式。Go 生态无现成解析库，采用 Python `arkparse` 库 + Go CLI 调用的方案。

## 二、技术选型

| 组件 | 选择 | 说明 |
|------|------|------|
| 解析库 | [arksaveparser](https://github.com/VincentHenauGithub/ark-save-parser) | Python，最新维护中（2026-04），支持 ASA |
| 集成方式 | Go `os/exec` 调用 Python 脚本 | 脚本输出 JSON，Go 解析 JSON |
| 输出格式 | JSON (stdout) | 跨语言通用，易于解析 |

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
├── parseserver/                    # 新增 Go 包
│   ├── parser.go                   # 调用 Python 脚本，解析 JSON 输出
│   ├── types.go                    # 数据类型定义
│   └── scripts/
│       └── parse_save.py           # Python 解析脚本
│
└── webapi/                         # 现有 API 层
    └── api.go                      # 新增 API 端点
```

### 调用流程

```
HTTP 请求 (GET /api/save/:map/players)
    │
    ▼
webapi handler
    │
    ▼
parseserver.ParsePlayers(savePath)
    │
    ▼
os/exec: python parse_save.py --map Aberration_WP --type players
    │
    ▼
Python: arkparse 解析 .ark 文件 → JSON → stdout
    │
    ▼
Go: json.Unmarshal → 返回结构化数据
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
python parse_save.py --save /path/to/Aberration_WP.ark --type players

# 获取部落列表
python parse_save.py --save /path/to/Aberration_WP.ark --type tribes

# 获取全部数据（玩家+部落+关系映射）
python parse_save.py --save /path/to/Aberration_WP.ark --type all

# 指定输出格式
python parse_save.py --save /path/to/Aberration_WP.ark --type players --format json
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
    "total": 1
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
    "total": 1
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
    "os/exec"
    "path/filepath"
    "time"
)

type ParseType string

const (
    ParseTypePlayers ParseType = "players"
    ParseTypeTribes  ParseType = "tribes"
    ParseTypeAll     ParseType = "all"
)

// ParseSave parses an ARK save file by calling the Python script
func ParseSave(ctx context.Context, savePath string, parseType ParseType) (*SaveParseResult, error) {
    scriptPath := getScriptPath()

    ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
    defer cancel()

    cmd := exec.CommandContext(ctx, "python", scriptPath,
        "--save", savePath,
        "--type", string(parseType),
    )

    output, err := cmd.CombinedOutput()
    if err != nil {
        return nil, fmt.Errorf("python script failed: %w, output: %s", err, string(output))
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

func getScriptPath() string {
    // Script is in the same directory as the executable
    exe, _ := os.Executable()
    return filepath.Join(filepath.Dir(exe), "scripts", "parse_save.py")
}
```

### API 端点 (`webapi/api.go` 新增)

```
GET /api/save/:map/players      - 获取地图玩家列表
GET /api/save/:map/tribes       - 获取地图部落列表
GET /api/save/:map/all          - 获取全部数据
GET /api/save/:map/player/:id   - 获取单个玩家详情
```

### 响应示例

```json
// GET /api/save/Aberration_WP/tribes
{
  "code": 200,
  "data": {
    "tribes": [
      {
        "tribe_id": 100,
        "tribe_name": "DragonSlayers",
        "member_count": 5,
        "members": [
          {"id": 1, "name": "Player1", "character_name": "DragonKnight", "active": true},
          {"id": 2, "name": "Player2", "character_name": "Mage", "active": true}
        ]
      }
    ],
    "total": 1
  }
}
```

## 六、目录结构变更

```
asa-server/
├── parseserver/                    # 新增
│   ├── parser.go                   # 调用 Python 脚本
│   ├── types.go                    # 数据类型定义
│   └── scripts/
│       ├── parse_save.py           # Python 解析脚本
│       └── requirements.txt        # Python 依赖
├── webapi/
│   └── api.go                      # 新增 save 解析 API 端点
└── ...
```

## 七、部署要求

### Python 环境

- Python 3.9+
- 安装依赖: `pip install arkparse`

### Go 集成

- 使用 `os/exec` 调用 Python 脚本
- 脚本通过 stdout 输出 JSON
- Go 端解析 JSON 并返回 API 响应

### 打包方案

1. **开发环境**: 直接调用系统 Python
2. **生产环境**: 
   - 方案 A: 随部署包附带 Python 虚拟环境
   - 方案 B: 使用 PyInstaller 将 Python 脚本打包为独立 exe
   - 方案 C: 使用 Docker 容器运行

## 八、性能考虑

| 因素 | 说明 |
|------|------|
| 解析耗时 | 大型存档（>500MB）可能需要 30-60 秒 |
| 内存占用 | Python 进程约 200-500MB |
| 并发控制 | 使用 mutex 限制同时只有一个解析任务 |
| 缓存 | 可选：缓存解析结果，避免重复解析 |
| 超时 | 设置 5 分钟超时，防止长时间阻塞 |

### 优化建议

1. **增量解析**: 如果存档未变化，返回缓存结果
2. **后台任务**: 大型存档使用异步任务 + SSE 推送结果
3. **预解析**: 在存档保存后自动触发解析，结果存入 BadgerDB

## 九、错误处理

| 错误场景 | 处理方式 |
|----------|----------|
| Python 未安装 | 启动时检测，返回 503 Service Unavailable |
| 存档文件不存在 | 返回 404 Not Found |
| 解析超时 | 返回 504 Gateway Timeout |
| 解析失败 | 返回 500 Internal Server Error + 错误详情 |
| 存档格式不支持 | 返回 400 Bad Request |

## 十、测试方案

1. **单元测试**: Mock Python 脚本输出，测试 JSON 解析
2. **集成测试**: 使用小型测试存档验证端到端流程
3. **性能测试**: 大型存档解析耗时和内存占用

## 十一、实施步骤

1. 创建 `parseserver/` 目录结构
2. 编写 `parse_save.py` Python 脚本
3. 编写 `parseserver/parser.go` 和 `types.go`
4. 在 `webapi/api.go` 中添加 API 端点
5. 添加错误处理和超时控制
6. 编写单元测试
7. 集成到现有项目构建流程
