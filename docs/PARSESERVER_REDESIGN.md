# parseserver 重构 + arkmonitor 改造设计文档（待审阅 v2）

> 目标：
> 1. 改造 SDK `go-arkparser/arkmonitor`，使其加载 `.ark + .arkprofile + .arktribe` 三类文件，产出与 `cmd/compare-snapshot`（即 `ExportAll`）**完全一致**的富数据，便于后续项目复用。
> 2. 重写 asa-server 的 `parseserver` 解析层与 `webapi/saveapi` 接口层：收敛为两个接口、移除 SSE、部落列表富化、文件发现下沉。
> 3. 通过现有 WebSocket 通道推送 §3.1 / §3.2 约定的数据格式。
>
> 本文档仅供审阅，**审阅通过后再执行编码**。

## 0. 本轮已确认的决策

| 编号 | 决策 | 结论 |
|------|------|------|
| D1 | 部落「玩家数组」字段命名 | 使用 **`player_list`**；SDK 原有 `players`(人数 int) 与 `members`(成员基础信息) 保持不变 |
| D4 | 监控/缓存层处置 | **保留监控**，改造 `arkmonitor` 使其返回富数据；`parseserver` 基于该富数据投影 |
| R2 | 推送 | 实现 WS 推送，payload 为 §3.1 玩家列表 + §3.2 富化部落列表 |
| SDK | 改 arkmonitor 返回 | 使其 `Export()` 返回与 `compare-snapshot`/`ExportAll` 一致的 `map[string][]map[string]any` |

---

## 1. 需求回顾

| # | 需求 |
|---|------|
| R1 | HTTP 接口收敛为 **两个**：玩家列表、部落列表 |
| R2 | 移除 SSE；改由 WebSocket 推送 §3.1 / §3.2 数据格式 |
| R3 | 部落列表新增 `tamed_list`（已驯服生物）、`tribe_logs`（部落日志）、`player_list`（该部落玩家列表） |
| R4 | `ParseSave` 只传实例名，内部自动发现 `.ark + .arkprofile + .arktribe`（三类缺一不可） |
| R5 | 文件发现逻辑从 API 层下沉到 `parseserver` 解析层 |
| SDK | 改造 `arkmonitor` 返回富数据（对齐 `compare-snapshot`），便于后续项目解析 |

参考实现：`D:\golang\arkparser\go-arkparser\cmd\compare-snapshot\main.go`（加载 ark+profiles+tribes 后 `ExportAll`）。

---

## 2. 依赖与影响面

- asa-server `go.mod` 通过 `replace github.com/Niexiawei/go-arkparser => D:\golang\arkparser\go-arkparser` 指向本地源码，**改 arkmonitor 立即生效**。
- `arkmonitor` 当前消费者仅：① 其自身测试（`monitor_test.go`/`integration_test.go`）；② asa-server 的 `parseserver/save_monitor.go` 与 `types.go`（本次重写）。**外部无其它调用方，可安全改公开 API**。
- `ExportAll(save, profiles, tribes, mapConfig)` 返回 7 个键：
  `ASV_Tamed`、`ASV_Wild`、`ASV_Players`、`ASV_Tribes`、`ASV_Structures`、`ASV_TribeLogs`、`ASV_MapStructures`。
- 现状缺陷：`arkmonitor` 只监听 `.ark`，`classifyFromContainer` 甚至从不填充 `Tribes`；且不加载 profiles/tribes → 无法产出 `tribe_logs`、完整玩家档案、部落成员。这正是本次要修复的。
- 前端 `app/src/` 当前**未引用** `/api/save/*`（已确认），HTTP 改动无前端回归；WS 消费为后续前端工作。

---

## 3. 目标数据格式（asa-server 侧）

### 3.1 玩家列表
`data` = `ExportAll` 的 `ASV_Players` 原样数组（profiles 已加载，信息完整）：
```jsonc
{ "success": true, "data": [ { "playerid": ..., "name": "...", "tribeid": ..., "steamid": "...", ... } ] }
```

### 3.2 部落列表（富化）
每条 = `ASV_Tribes` 原记录 + 三个新增字段：
```jsonc
{
  "success": true,
  "data": [{
    "tribeid": 1717259748,
    "tribe": "部落名",
    "players": 3,                 // SDK 原有：人数(int)，保持不变
    "members": [ ... ],           // SDK 原有：成员基础信息，保持不变
    "active": "...",
    // —— 新增（均以 tribeid 关联）——
    "tamed_list": [ { "dinoid": ..., "creature": "...", "lvl": ..., ... } ],  // ASV_Tamed 按 tribeid(=TargetingTeam) 分组
    "tribe_logs": [ "2026.01.01 ...", ... ],                                  // ASV_TribeLogs 中该 tribeid 的 logs
    "player_list": [ { "playerid": ..., "name": "...", ... } ]               // ASV_Players 按 tribeid 分组
  }]
}
```
- 无对应数据时给空数组 `[]`（字段恒存在，便于前端）。
- **富化在 `parseserver` 完成，不改 arkmonitor 的 `ExportAll` 输出**（保持与 compare-snapshot 一致）。

---

## 4. arkmonitor 改造（SDK 跨仓）

**原则**：`arkmonitor` 对外输出的富数据 == `ExportAll`，一字不改；同时保留「变更检测 + 事件通知」能力作为推送触发器。

### 4.1 加载三类文件（Reload 改造）
- `MonitorConfig.SavePath` 仍是 `.ark` 路径。**新增自动发现**：每次 `Reload()` 用 `filepath.Glob(filepath.Dir(SavePath)+"/*.arkprofile")` 与 `.../*.arktribe` 收集 profiles/tribes（三类文件同目录共存，已在真实存档中确认）。
- `Reload()` 新流程：
  1. 加载 `.ark`（沿用 `LazyMode`/mmap；导出前完成物化）。
  2. 加载全部 `.arkprofile`（`files.LoadProfile`/`LoadProfileFromBytes`）与全部 `.arktribe`（`files.LoadTribe(path)`，按路径以填充 `SourcePath`）。
  3. `mc := common.GetMapConfig(filepath.Base(SavePath))`。
  4. `export := goarkparser.ExportAll(ws, profiles, tribes, mc)`（**必须在 `save.Close()` 之前**）。
  5. 生成新 `WorldSnapshot{ Export: export, Timestamp: now }`，与旧快照 diff → 发事件；再 `Close()`。

> 说明：`ExportAll` 需要属性已物化，`LazyMode` 下先「物化全部 → ExportAll → 驱逐 → 关闭」（沿用 `buildSnapshotLazy` 的物化/驱逐骨架，只是把「分类建轻量快照」替换为「ExportAll」）。

### 4.2 返回值改造
```go
// WorldSnapshot 改为承载 ExportAll 富数据
type WorldSnapshot struct {
    Export    map[string][]map[string]any // == ExportAll：ASV_Tamed/Wild/Players/Tribes/Structures/TribeLogs/MapStructures
    Timestamp time.Time
}

// Monitor 新增便捷访问器（等价 Snapshot().Export）
func (m *MonitorImpl) Export() map[string][]map[string]any
```
- `Snapshot() *WorldSnapshot` 签名保留，但内部字段由「4 张 typed map」改为 `Export`。
- **移除** typed `PlayerSnapshot/TribeSnapshot/CreatureSnapshot/StructureSnapshot` 及基于它们的 `snapshot.go` 分类逻辑（改由 ExportAll 产出）。

> **决策点 E1（事件粒度）**
> 变更检测/事件 `Event{Type, ObjectType, ObjectID}` 如何保留？
> - **推荐**：基于 `Export` 的四类记录（`ASV_Players/ASV_Tamed/ASV_Tribes/ASV_Structures`）按 `id`/`playerid`/`tribeid`/`dinoid` + 记录内容哈希做通用 diff，`ObjectType` 取 ASV 类别。保留现有 `Observer/Event/Subscribe` API 不变。
> - 备选（更省事）：只发一个粗粒度「world_changed」事件（推送场景只需「变了就重推」）。
> 请二选一（推荐前者，兼容现有测试语义）。

### 4.3 Watcher 改造
- `watcher.go` 目前只在「文件名 == `.ark` 基名」且 Write/Create 时触发。**放宽**为：目标 `.ark` 或任意 `*.arkprofile` / `*.arktribe` 的 Write/Create 均触发 `file_changed`（部落/玩家档案独立于 `.ark` 变化）。
- 仍监听 `.ark` 所在目录（已是目录级 fsnotify）。

### 4.4 arkmonitor 测试
- 更新 `monitor_test.go`/`integration_test.go`：断言从 typed map 改为 `Export()` 的 ASV_* 键；补充「加载 profiles/tribes 后 `ASV_Players` 有 steamid、`ASV_TribeLogs` 非空」。测试数据可复用 `go-arkparser/test_file/{Extinction_WP,Valguero_WP}`。

---

## 5. parseserver 重写（asa-server 侧）

### 5.1 公开 API
```go
// SaveData 仅两种顶层列表
type SaveData struct {
    Players []map[string]any `json:"players"`
    Tribes  []map[string]any `json:"tribes"` // 每条已注入 tamed_list / tribe_logs / player_list
}

// ParseInstanceSave 一次性解析（供 HTTP 按需查询用）。
// 内部：locateInstanceSaveFiles → arkmonitor 一次性加载(或直接管线) → ExportAll → buildSaveData
func ParseInstanceSave(ctx context.Context, instanceName string) (*SaveData, error)

// buildSaveData 从 ExportAll 富数据投影出 §3.1/§3.2 结构（供 HTTP 与 WS 复用）
func buildSaveData(export map[string][]map[string]any) *SaveData
```

### 5.2 文件发现下沉（R4/R5）
```go
// locateInstanceSaveFiles 返回 {InstancesDir}/{instance}/Save/{MapName}/ 下的
// .ark、全部 .arkprofile、全部 .arktribe 路径。
func locateInstanceSaveFiles(instanceName string) (arkPath string, profilePaths, tribePaths []string, err error)
```
- `.ark` 必须存在；profiles/tribes 为空按 **决策点 E2** 处理。
- **删除** `saveapi.go` 中的 `findSaveFileByInstance`。

### 5.3 富化投影 `buildSaveData`
- `Players = export["ASV_Players"]`（原样）。
- `Tribes`：遍历 `export["ASV_Tribes"]`，为每条注入：
  - `player_list` ← `ASV_Players` 按 `tribeid` 分组
  - `tamed_list` ← `ASV_Tamed` 按 `tribeid` 分组
  - `tribe_logs` ← `ASV_TribeLogs` 中该 `tribeid` 的 `logs`
- 分组统一用 `toInt64` 读 `tribeid`（player 的 `tribeid` 类型可能非 int，保留该辅助）。

### 5.4 SaveDataManager（监控 + 推送）
- 保留 `SaveDataManager`，但改为消费 arkmonitor 的 `Export()`：
  - `Start()`：对每个实例建 arkmonitor（SavePath=`.ark`，自动发现 profiles/tribes），`Subscribe` observer。
  - observer `OnEvent` → `mon.Export()` → `buildSaveData` → ①更新内存当前值 ②WS 推送（§6）。
  - HTTP 按需查询优先读「内存当前值」，未命中回退 `ParseInstanceSave`。
- **决策点 E3（是否保留 BadgerDB 缓存）**：arkmonitor 已在内存持有当前 Export，`arkworldsave` 这个 BadgerDB 缓存可**移除**（推荐，去掉一套持久化/序列化）；如需重启后即时可用再议。移除后 `CachedSaveData`、`SetCached/GetCached`、`saveKeyAll`、badger 依赖一并删除。
- 移除 `SaveBroadcaster`（SSE 专用）。

---

## 6. WebSocket 推送（R2）

**复用现有 `realtime.Hub` 与 `/api/ws/events`**（避免新增 WS 连接——这正是 R2 收敛 SSE 的初衷：单后端地址连接数吃紧）。

- 新增广播方法（`realtime/hub.go`）：
  ```go
  func BroadcastSaveData(instanceName string, data *SaveDataPayload)
  ```
- WS 消息体：
  ```jsonc
  {
    "type": "save_data",
    "instance": "xxx",
    "timestamp": 1719120000,
    "data": { "players": [ /*§3.1*/ ], "tribes": [ /*§3.2 富化*/ ] }
  }
  ```
- 触发点：`SaveDataManager` observer 收到变更事件时推送。

> **决策点 E4（推送通道）**：复用 `/api/ws/events` 全局 hub（**推荐**，零新增连接，客户端按 `type=="save_data"` 过滤）；或新开 `/api/ws/save` 专用端点（更清晰但多占一个连接）。
> **决策点 E5（推送粒度/频率）**：`.ark` 每次存档即变更，富数据体积可能较大。推荐先「变更即全量重推」，后续如有压力再做「按 instance 订阅」或「增量」。

---

## 7. 改动清单

**go-arkparser/arkmonitor/**（SDK）
- `types.go`：`WorldSnapshot` 改为 `{Export, Timestamp}`；删除 typed *Snapshot（视 E1）。
- `monitor.go`：`Reload` 改为加载三类文件 + `ExportAll`；新增 `Export()`。
- `snapshot.go`：删除/重写（分类逻辑由 ExportAll 取代；保留 diff 所需的记录级哈希，视 E1）。
- `diff.go`：改为对 Export 记录做通用 diff（视 E1）。
- `watcher.go`：放宽触发到 `.ark`/`.arkprofile`/`.arktribe`。
- `config.go`：如需，加 `MapName` 覆盖项（默认取 SavePath 基名）。
- `*_test.go`：按新返回更新断言。

**asa-server/parseserver/**
- `parser.go`：重写 —— `ParseInstanceSave`、`locateInstanceSaveFiles`、`buildSaveData`、富化分组；删 `ParseType`/旧 `ParseSave`；保留 `toInt64`。
- `types.go`：`SaveData` 收敛两字段；删 `SaveParseResult`、`CachedSaveData`（视 E3）。
- `save_monitor.go`：重写 `SaveDataManager` 消费 arkmonitor `Export()` + WS 推送；删 `SaveBroadcaster`、（视 E3）BadgerDB 缓存。

**asa-server/webapi/saveapi/saveapi.go**
- 路由收敛为 `/players`、`/tribes`；删 `getSaveAll`/`streamSaveData`/`handleSaveParse(parseType)`/`findSaveFileByInstance`。
- 两接口调用 `ParseInstanceSave`（或经 `SaveDataManager` 读内存当前值）。

**asa-server/webapi/actions.go**
- `saveapi.NewHandler(...)` 入参按新签名调整；`SaveDataManager` 装配保留（消费 arkmonitor）。

**asa-server/realtime/hub.go**
- 新增 `BroadcastSaveData` + payload 结构（视 E4）。

**前端**：本次 HTTP 无回归；WS 消费为后续工作。

---

## 8. 待确认决策点

| 编号 | 决策 | 推荐 |
|------|------|------|
| E1 | arkmonitor 事件粒度 | 基于 Export 记录做通用 diff，保留 Observer/Event API |
| E2 | profiles/tribes 缺失行为 | `.ark` 缺失报错；profiles/tribes 缺失仅告警降级，不报错 |
| E3 | 是否移除 BadgerDB `arkworldsave` 缓存 | 移除，改用 arkmonitor 内存当前值 |
| E4 | WS 推送通道 | 复用 `/api/ws/events` 全局 hub，客户端按 type 过滤 |
| E5 | 推送粒度/频率 | 变更即全量重推，后续再优化 |

请对 E1–E5 逐条确认（或「全部采用推荐项」），确认后开始编码。

---

## 9. 实施状态（已完成）

E1–E5 均按推荐项落地，D1=`player_list`。已构建 + vet + 测试通过（两仓）。

### 9.1 arkmonitor（SDK，`go-arkparser`）
- `types.go`：`WorldSnapshot` 改为 `{Export, Timestamp}` + 内部 diff 索引；删除 typed *Snapshot 与未使用的 *Event 包装类型。
- `snapshot.go`：`NewWorldSnapshot(export)` 建索引；记录级 `identityKey` + `contentHash`(fnv/json) + `objectID`。
- `diff.go`：按 `diffCategories`（ASV_Players/Tamed/Tribes/Structures/TribeLogs）做通用 diff，保留 `Observer/Event` API。
- `monitor.go`：新增**导出的一次性管线** `ExportSave(arkPath, lazy, opts...)`（发现同目录 profiles/tribes + 物化 + `ExportAll` + 驱逐/关闭）；`Reload()` 复用它并**改为锁外加载**（不再长时间阻塞读）；新增 `Export()` 访问器。
- `watcher.go`：触发放宽到 `.ark` / `.arkprofile` / `.arktribe`。
- 测试：`integration_test.go` 用真实存档断言 `Export()` 与 `ASV_*.json` 记录数一致（实测 野生 15867 / 驯服 7 / 建筑 1173 全一致，玩家 3 / 部落 5 / 部落日志 5 均非空 → 证明 profiles/tribes 已生效）。

### 9.2 parseserver（asa-server）
- `types.go`：`SaveData{Players, Tribes}` 两字段。
- `parser.go`：`ParseInstanceSave(ctx, instance)`（经 `locateInstanceArkPath` 定位 .ark → `arkmonitor.ExportSave` → `buildSaveData`）；`buildSaveData` 投影 §3.1/§3.2；`enrichTribes` 按 tribeid 注入 `player_list`/`tamed_list`/`tribe_logs`；保留 `toInt64`。
- `save_monitor.go`：`SaveDataManager` 内存缓存 `current`（**无 BadgerDB**）；每实例一 arkmonitor + **去抖 worker**（`coalesceDelay=300ms`，解决「每条记录一个事件 → 全量重推放大」问题）；变更时 `buildSaveData` → 更新缓存 + WS 双 type 推送。
- `parser_test.go`：改测 `buildSaveData`/`enrichTribes`/`toInt64`（合成数据，纯函数）。

### 9.3 WS 推送（realtime）
- `hub.go` 新增包级 `BroadcastSavePlayers`（`event_type="save_players"`, `data.players`）与 `BroadcastSaveTribes`（`event_type="save_tribes"`, `data.tribes"`），复用现有 `/api/ws/events` 全局 hub。

### 9.4 HTTP 接口（saveapi）
- 仅两个：`GET /api/save/:instance/players`、`GET /api/save/:instance/tribes`。
- 优先读监控器内存缓存，未命中回退 `ParseInstanceSave`。删除 `/all`、`/stream`(SSE)、`findSaveFileByInstance`。

### 9.5 相对计划的两处细化
1. **新增 `arkmonitor.ExportSave` 导出函数**：把一次性解析管线抽出，供 `Reload()` 与 `parseserver` 按需解析共用，避免重复实现（原计划未显式提及）。
2. **去抖 worker**：per-record 事件会导致同一次存档触发大量重复推送，加入 300ms 合并窗口（原计划 §6 E5 仅说「全量重推」，此为必要的落地补充）。

### 9.6 前端 TODO（未做）
- WS 客户端按 `event_type in {save_players, save_tribes}` 消费 `data.players` / `data.tribes`（当前前端无 `/api/save` 引用，属后续工作）。
