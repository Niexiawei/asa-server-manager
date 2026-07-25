# 定时任务执行日志 与 ScheduleManager 双列布局

> 状态：**已实施**。决策见 §11，实施中相对本文的偏差见 §12。
> `go build ./...`、`go vet ./...`、`go test -race ./schedule/...`、`npm run build` 均通过。
>
> 设计文档。三件事：
> ① 后端补上定时任务的执行日志；② 日志的存储位置与 500 条上限；
> ③ 前端 `ScheduleManager.vue` 改为左右两列，右列展示执行日志。
>
> §3 直接回答「能不能存进 `schedules.json`」这个问题，并给出推荐方案与理由。

---

## 1. 现状

### 1.1 执行结果只留了最后一次，且只有一句话

`schedule.Scheduler.execute()`（`schedule/scheduler.go:185`）跑完一条任务后，
只往任务对象上回写三个字段：

```go
if mErr := s.store.mutate(t.ID, func(stored *Task) {
    stored.LastRunAt = &startedAt
    stored.LastResult = result      // "成功" 或 "失败: xxx"
}); mErr != nil { ... }
```

由此带来的问题：

| 问题 | 后果 |
|---|---|
| 只保留**最后一次**结果 | 昨天半夜那次更新失败了，今天成功了 → 失败记录被覆盖，无从追查 |
| 结果压缩成一个字符串 | 耗时、触发方式（定时/手动）全丢了 |
| 定时触发与手动触发无法区分 | `RunNow` 和 `tick` 走的是同一个 `execute`，事后看不出这次是谁触发的 |
| 中间过程只进 zap 日志 | 「更新前强停了哪几个实例」只在 `asaServer.log` 里，要 SSH 上机器翻文件 |
| 前端只能显示一行 | `ScheduleManager.vue:44-52` 的「上次执行」列就是这个字段的全部 |

### 1.2 已有的信息其实很有价值，只是被丢掉了

`runRestart` 已经会生成 `"2/5 个实例重启失败：jibian、meijue"` 这样的摘要
（`scheduler.go:239`），`runUpdate` 也会区分「没有实例可停」「批量停服未能启动」
「以下实例无法停止，更新已取消」等几种失败。这些字符串目前全都塞进
`LastResult` 然后被下一次执行覆盖。

**本设计不新造信息，主要是把已经算出来的东西留存下来。**

### 1.3 前端布局

`ScheduleManager.vue` 现在是单列：一张占满宽度的 `t-table`，8 列共约 1230px。
没有任何地方展示历史。

---

## 2. 需求拆解

1. **后端**：`schedule` 包记录每次执行的结果，可查询。
2. **存储**：持久化，最多 500 条，超出裁剪。
3. **前端**：左右两列，左列任务表格，右列执行日志。

---

## 3. 关键决策：日志存哪里

### 3.1 结论：**能存进 `schedules.json`，但建议单独一个文件**

先回答可行性——技术上完全可行，写入压力可以忽略：

- 当前 `schedules.json` 的写入时机是 Add / Update / Delete / Toggle（用户操作，低频）
  和 `mutate`（每次任务执行后回写 `NextRunAt`、`LastRunAt`，约 2 次/执行）。
- 按 §4 的模型，**一次执行只产生一条记录**，所以日志带来的额外写入是 1 次/执行。
- 500 条记录约 100～150KB，`MarshalIndent` + 临时文件 + `os.Rename` 的开销在
  「每小时最多跑几次」的频率下完全不是问题。

**但仍然建议拆成 `schedule_logs.json`，理由三条：**

**(a) 会破坏 `schedules.json` 现有的文件格式，需要迁移。**
现在这个文件的顶层是**数组**：

```go
var tasks []*Task
if err := json.Unmarshal(data, &tasks); err != nil { ... }   // store.go:50
```

要塞进日志就得改成对象 `{"tasks": [...], "logs": [...]}`。已有用户的
`schedules.json` 是数组，升级后第一次 `load()` 会直接 `Unmarshal` 失败。
虽然可以靠「首字符是 `[` 就走旧格式」来兼容，但这是本可以不欠的债。

**(b) 会毁掉 `store.go` 明确写下的一个设计意图。**

> ```go
> // 任务量是个位数，整份读写就够了，不需要 KV 库；
> // 放成明文 JSON 也方便出问题时直接打开看、手改、备份。
> ```

500 条日志会把 5 条任务定义彻底淹没。「出问题时打开看、手改」这条属性
在一个 100KB、95% 是日志的文件里就不成立了。

**(c) 两者的生命周期不同。** 日志会被裁剪、可能需要「清空」按钮、损坏了丢掉就行；
任务定义则必须保住。放一个文件里意味着日志写坏能带走任务配置
（虽然有原子 rename 兜底，但没必要共担风险）。

### 3.2 两个方案的对照

| | A：独立文件 `schedule_logs.json`（推荐） | B：并入 `schedules.json` |
|---|---|---|
| 格式迁移 | 不需要，`schedules.json` 一字不动 | 需要，顶层数组 → 对象，且要兼容旧格式 |
| 任务配置可手改 | 保持 | 被日志淹没 |
| 文件数 | 2 个 | 1 个 |
| 日志损坏的影响 | 只丢日志 | 可能带走任务定义 |
| 写入放大 | 各写各的 | 每条日志重写整个文件（含任务） |
| 代码量 | 多一个 `logStore`，约 90 行 | 改 `store` 的 load/save，约 60 行 + 迁移分支 |

**本文后续按方案 A 展开。** 若你坚持单文件，§9 给出方案 B 的差异步骤。

### 3.3 顺带要做的小重构

`store.saveLocked()` 里那段「临时文件 + Rename」的原子写（`store.go:63-92`，约 30 行）
会被 `logStore` 原样再用一次。实施时抽成 `schedule/atomicjson.go`：

```go
// writeJSONAtomic 先写临时文件再 Rename 覆盖，避免进程被杀时留下半个文件。
func writeJSONAtomic(path string, v any) error
```

`store` 与 `logStore` 共用。这不是顺手扩大范围——是避免第二份复制粘贴的原子写。

---

## 4. 数据模型

### 4.1 一次执行 = 一条记录

**不做逐步骤的流式日志。** 理由：批量重启的过程日志 `batchmanage` 已经有完整的
SSE 流和 500 条历史回放（`batchmanage/manager.go:71`），更新过程也有
`updatemanage` 的 SSE。在 `schedule` 里再存一份既重复又会让 500 条上限在
两三次执行内就被耗光。

`schedule` 该记的是**执行档案**（这次跑了没有、跑了多久、成没成、为什么没成），
过程细节留在各自的流里。

```go
// RunRecord 一次任务执行的档案。
type RunRecord struct {
    ID string `json:"id"`

    // 任务身份冗余存一份：任务被删掉之后，历史记录仍然要能读懂
    TaskID   string   `json:"task_id"`
    TaskName string   `json:"task_name"`
    TaskType TaskType `json:"task_type"`

    // Trigger 区分定时触发与手动「立即执行」。
    // 现在两者都走 execute()，事后完全分不出来。
    Trigger TriggerSource `json:"trigger"`

    StartedAt  time.Time `json:"started_at"`
    DurationMs int64     `json:"duration_ms"`

    Success bool `json:"success"`

    // Message 成功时是摘要（如「已重启 5 个实例」），
    // 失败时是原因（如「2/5 个实例重启失败：jibian、meijue」）。
    // 内容就是现在 LastResult 里那个字符串，去掉 "失败: " 前缀。
    Message string `json:"message"`
}

type TriggerSource string

const (
    TriggerSchedule TriggerSource = "schedule" // 到点自动触发
    TriggerManual   TriggerSource = "manual"   // 用户点了「立即执行」
)
```

`ID` 沿用 `scheduler.go:408` 现成的 `strconv.FormatInt(time.Now().UnixNano(), 36)`。

### 4.2 `Task.LastRunAt` / `LastResult` 保留

不删。它们是列表页的「上次执行」列，从日志现算需要每次扫一遍记录；
留着这两个字段等于一个廉价的物化视图。写日志和回写这两个字段在同一处发生，
不会漂移。

### 4.3 文件格式

`{BaseDir}/schedule_logs.json`：

```json
[
  {
    "id": "1a2b3c",
    "task_id": "xyz",
    "task_name": "每日凌晨重启",
    "task_type": "restart",
    "trigger": "schedule",
    "started_at": "2026-07-24T04:00:00+08:00",
    "duration_ms": 187000,
    "success": false,
    "message": "2/5 个实例重启失败：jibian、meijue"
  }
]
```

顶层数组，**新的在后**（append 语义，读的时候前端倒序展示）。

---

## 5. 上限语义：500 条怎么算

你的原话是「日志最多存储500条超出清空保持日志条数不超过500条」。
这里有两处需要定死：

### 5.1 500 是全局还是每任务？

**推荐全局 500。** 右列是一条统一的时间线（所有任务混排），全局上限与它一一对应；
每任务 500 则会让 10 个任务的文件涨到 5000 条。

参照：`batchmanage` 的 `maxLogHistory = 500` 是每次操作 500，`state` 是每实例 500——
但那两个都是「某个对象的过程日志」，这里是「所有任务的执行档案」，语义不同。

按每天 3 次执行估算，全局 500 条 ≈ 半年历史，够用。

### 5.2 「超出清空」= 清空整个列表，还是丢弃最旧的？

**推荐丢弃最旧的（滚动窗口），不是清空。**

字面意义的「清空」会让用户在第 501 次执行后突然丢掉全部历史，
包括昨天那次失败——恰好是最需要它的时候。

沿用 `batchmanage.sendLog` 的写法：

```go
if len(logs) >= maxRunRecords {
    logs = logs[len(logs)-maxRunRecords+1:]
}
logs = append(logs, record)
```

用切片重切而非逐条 pop，是为了应对「上限被调小」后一次裁掉多条的情况。

> 如果你要的确实是字面的「清空」，改成 `if len(logs) >= 500 { logs = nil }` 一行的事，
> 但我不建议——请在评审时明确。

### 5.3 删除任务时要不要连带删掉它的日志？

**不删。** 记录里冗余存了 `task_name` / `task_type`，任务删除后历史仍然可读。
「这个每日重启任务为什么被删了？因为它连着失败了一周」——这个追溯能力值得留着。

前端按 `task_id` 过滤时，已删任务的记录归入「全部」，不再出现在任务筛选项里。

---

## 6. 后端改动

### 6.1 新增 `schedule/runlog.go`

```go
const maxRunRecords = 500
const logStoreFileName = "schedule_logs.json"

type logStore struct {
    mu      sync.RWMutex
    path    string
    records []*RunRecord
}

func newLogStore() *logStore
func (s *logStore) load() error                       // 文件不存在 = 空列表，不算错误
func (s *logStore) append(r *RunRecord) error         // 裁剪到 maxRunRecords 后落盘
func (s *logStore) list(taskID string, limit int) []*RunRecord  // 倒序返回，taskID 空则全部
func (s *logStore) clear() error
```

`load` 的容错与 `store.load` 保持一致：文件不存在、空文件都按空列表处理。
**额外一条**：日志解析失败时**不返回错误**，只记 WARN 并按空列表继续——
日志坏了不该让调度器起不来（任务定义坏了才值得报错）。

### 6.2 `Scheduler.execute` 接上

`execute` 目前的签名是 `execute(ctx, t)`，需要知道触发来源：

```go
func (s *Scheduler) execute(ctx context.Context, t *Task, trigger TriggerSource) {
    startedAt := time.Now()
    ...
    // 原有的 LastRunAt / LastResult 回写保持不动

    s.logs.append(&RunRecord{
        ID:         newID(),
        TaskID:     t.ID,
        TaskName:   t.Name,
        TaskType:   t.Type,
        Trigger:    trigger,
        StartedAt:  startedAt,
        DurationMs: time.Since(startedAt).Milliseconds(),
        Success:    err == nil,
        Message:    message,   // 成功时的摘要 / 失败时的原因
    })

    realtime.BroadcastScheduleRun(record)   // 见 §6.3
}
```

两处调用点：
- `tick()`（`scheduler.go:158`）→ `TriggerSchedule`
- `RunNow()`（`scheduler.go:180`）→ `TriggerManual`

**成功时的 Message 需要补。** 现在成功只写死 `"成功"`，没有信息量。建议：

| 任务 | 成功摘要 |
|---|---|
| `restart` | `已重启 5 个实例` （`len(op.InstanceResults)` 减去 skipped） |
| `update`  | `已停止 3 个实例并完成更新` 或 `无实例需停止，更新完成` |

`runRestart` / `runUpdate` 改为返回 `(summary string, err error)`。

### 6.3 WS 实时推送（推荐但可后置）

右列如果只靠轮询，用户盯着看时会有最长 N 秒的延迟。前端已有事件 WS 通道，
加一个事件类型即可：

```go
// realtime/hub.go
func BroadcastScheduleRun(taskName string, success bool, data map[string]any) {
    BroadcastServerEventWithData("schedule_run", "", ...)
}
```

前端 `serverStore.js` 加一个 `case 'schedule_run'` + `scheduleCallbacks`，
与现有的 `batchCallbacks` / `updateCallbacks` 同一套写法
（`serverStore.js:170-195` 是现成的样板）。

若想先做减法，第一版可以只做 §6.4 的 REST 拉取，页面进入时和执行后手动刷新。

### 6.4 API

| Method | Path | 说明 |
|---|---|---|
| GET | `/api/schedule/logs` | 查执行日志。query: `task_id`（可选，按任务过滤）、`limit`（默认 100，上限 500） |
| DELETE | `/api/schedule/logs` | 清空全部日志 |

响应沿用 `apiresp.StatusResponse`：

```json
{ "success": true, "data": { "logs": [ ... ], "total": 137 } }
```

`total` 是裁剪前的总数，供前端显示「共 137 条」。

---

## 7. 前端：两列布局

### 7.1 布局与宽度

```
┌───────────────────────────────────────────┬──────────────────────┐
│ 定时任务管理              [刷新] [新建]    │ 执行日志   [全部 ▾][清空] │
├───────────────────────────────────────────┼──────────────────────┤
│ ┌───────────────────────────────────────┐ │ ● 每日凌晨重启   成功  │
│ │ 名称 │ 类型 │ 规则 │ 下次执行 │ ...  │ │   04:00  耗时 3m07s   │
│ │ ...                                   │ │   已重启 5 个实例      │
│ └───────────────────────────────────────┘ │ ──────────────────── │
│                                           │ ● 每周更新       失败  │
│                                           │   03:00  耗时 12s     │
│                                           │   以下实例无法停止：…  │
└───────────────────────────────────────────┴──────────────────────┘
        flex: 1（最小 720px）                    固定 380px
```

右列固定宽度、左列 `flex: 1`，理由：日志条目是定长文本块，给它弹性宽度只会让
行长忽宽忽窄；表格才是需要吃掉剩余空间的那个。

### 7.2 左列表格必须瘦身 —— 这是本次改动的主要风险

当前 8 列写死宽度合计 **约 1230px**。右列吃掉 380px + 间距后，
在 1440px 屏幕上左列只剩约 1020px，表格会出现横向滚动条，观感明显变差。

必须同时做减法。建议：

| 列 | 处置 |
|---|---|
| 名称 | 保留，160px |
| 类型 | 保留，100px |
| 规则 | 保留，130px |
| 作用实例 | **并入名称列**，作为名称下方的灰色副文本 |
| 下次执行 | 保留，160px |
| **上次执行** | **删除** —— 右列日志就是它的超集，留着是重复信息 |
| 启用 | 保留，70px |
| 操作 | 保留，160px |

处置后约 780px，1440px 屏幕上舒适，1280px 屏幕上仍可接受。

### 7.3 窄屏

`< 1280px` 时右列不再并排，改为：

- 折叠到表格下方（`flex-direction: column`，右列高度 320px）

比抽屉/隐藏更好——用户不需要额外操作就能看到日志，只是位置变了。
用纯 CSS `@media` 实现，不引入 JS 断点判断。

### 7.4 右列组件

新增 `app/src/components/ScheduleRunLog.vue`：

- **筛选**：顶部 `t-select`，「全部任务」+ 各任务名；选中后按 `task_id` 过滤
- **条目**：`t-timeline` 或自绘列表。每条显示
  任务名 + 成功/失败标签 + 触发来源图标（⏰定时 / 👆手动）+ 开始时间 + 耗时 + Message
- **失败态**：整条左侧 3px 红色竖条 + Message 用 `--td-error-color`；
  成功用绿色，与现有 `.last-result` 的 `#22c55e` / `#ef4444` 保持一致
- **空态**：`t-empty`「暂无执行记录」
- **实时**：订阅 `schedule_run` 事件，新记录 `unshift` 到列表头
- **清空**：`t-popconfirm` 二次确认后调 `DELETE /api/schedule/logs`

耗时格式化：`< 1s` 显示 `0.4s`，`< 60s` 显示 `12s`，否则 `3m07s`。

### 7.5 API 层

`app/src/apis/api.js` 补两个：

```js
export function listScheduleLogs(params) { ... }   // GET  /schedule/logs
export function clearScheduleLogs()      { ... }   // DELETE /schedule/logs
```

---

## 8. 实施顺序

每步结束都应能 `go build ./...` 通过、页面可用。

1. **`schedule/atomicjson.go`**：抽出 `writeJSONAtomic`，`store.saveLocked` 改用它
   （纯重构，行为不变，先单独验证）
2. **`schedule/runlog.go`**：`RunRecord` + `logStore` + 裁剪 + 落盘（**纯逻辑，先写单测**）
3. **`schedule/scheduler.go`**：`execute` 加 `trigger` 参数并写日志；
   `runRestart` / `runUpdate` 改为返回成功摘要
4. **`webapi/scheduleapi`**：`GET/DELETE /api/schedule/logs`
5. **`realtime/hub.go` + `serverStore.js`**：`schedule_run` 事件（可后置）
6. **`ScheduleRunLog.vue`**：右列组件，先接 REST
7. **`ScheduleManager.vue`**：两列布局 + §7.2 的表格瘦身
8. `npm run build` + 文档（`CLAUDE.md` 的运行时目录布局加 `schedule_logs.json`）

## 9. 若改用方案 B（并入 `schedules.json`）

只有第 1～2 步不同，其余照旧：

1. `store` 的顶层结构改为
   ```go
   type persisted struct {
       Tasks []*Task      `json:"tasks"`
       Logs  []*RunRecord `json:"logs"`
   }
   ```
2. `load()` 加旧格式兼容：
   ```go
   // 旧版本的 schedules.json 顶层是数组，直接 Unmarshal 到 persisted 会失败
   trimmed := bytes.TrimLeft(data, " \t\r\n")
   if len(trimmed) > 0 && trimmed[0] == '[' {
       // 按 []*Task 解析，Logs 置空，下次保存时自动升级为新格式
   }
   ```
3. 补一个单测：喂入旧格式数组，断言任务被正确加载且不报错。

代价见 §3.1 的 (b)：任务定义会被日志淹没，`store.go` 里「方便直接打开看、手改」
那条注释届时应当删掉——不要留一句已经不成立的注释。

---

## 10. 验证要点

**单测**（`schedule/runlog_test.go`）：

- 追加到第 501 条时，最旧的被丢弃，长度恒为 500，且**最新的那条在**
- 上限被调小时，一次裁掉多条仍然正确
- `list(taskID, limit)`：按任务过滤、倒序、limit 截断
- 空文件 / 文件不存在 / **内容损坏** → 均按空列表继续，不返回错误
- `clear()` 后文件为空数组而非被删除

**手工验证**：

1. 建一个 `interval_hours=1` 的重启任务，点「立即执行」，右列立刻出现
   一条 `trigger=manual` 的记录
2. 故意让任务失败（指定一个不存在的实例→改配置制造失败），确认记录为红色
   且 Message 是具体原因而不是「失败」两个字
3. 删掉该任务，确认右列历史仍在且任务名可读
4. 重启进程，确认日志从 `schedule_logs.json` 正确恢复
5. 窗口宽度拉到 1280px 以下，确认右列折叠到下方且表格无横向滚动条

---

## 11. 决策记录

1. **§3 存储位置**：独立文件 `schedule_logs.json`（方案 A）。
2. **§5.2 上限语义**：丢弃最旧的，滚动窗口，不清空。
3. **§7.2 表格瘦身**：**不做**。八列全部保留，「上次执行」列留着。
   前端布局由使用者后续自行调整，本次只把两列骨架搭起来。

   因此 §7.2 的减列方案不实施，代价是 1440px 及以下屏幕左列表格会出现
   横向滚动条——这是已知且已接受的取舍。实现上给左列加
   `min-width: 0` 与 `overflow-x: auto`，保证滚动条出现在表格容器内部，
   不会把整个页面撑宽。

---

## 12. 实施记录

### 12.1 落地的文件

| 文件 | 内容 |
|---|---|
| `schedule/atomicjson.go`（新） | `writeJSONAtomic`，从 `store.saveLocked` 抽出，与 `logStore` 共用 |
| `schedule/runlog.go`（新） | `RunRecord` / `TriggerSource` / `logStore` / `trimOldest` |
| `schedule/runlog_test.go`（新） | 12 个用例，见 §12.3 |
| `schedule/store.go` | `saveLocked` 缩成一行，改调 `writeJSONAtomic` |
| `schedule/scheduler.go` | `execute` 加 `trigger` 参数并写记录；`runRestart`/`runUpdate` 改返回 `(summary, error)` |
| `realtime/hub.go` | `BroadcastScheduleRun` |
| `webapi/scheduleapi/scheduleapi.go` | `GET /api/schedule/logs`、`DELETE /api/schedule/logs` |
| `app/src/apis/api.js` | `listScheduleLogs` / `clearScheduleLogs` |
| `app/src/store/serverStore.js` | `scheduleCallbacks` + `case 'schedule_run'` |
| `app/src/components/ScheduleRunLog.vue`（新） | 右列日志面板 |
| `app/src/views/ScheduleManager.vue` | 两列骨架 + 刷新按钮同时刷新两列 |

### 12.2 与本文的三处偏差

**(a) `BroadcastScheduleRun` 的签名。** §6.3 写的是逐字段传参，实际会变成 9 个位置参数，
可读性太差。改为 `BroadcastScheduleRun(taskName string, success bool, data map[string]any)`，
payload 由 `schedule` 侧组装。`realtime` 不能反向依赖 `schedule`（会成环），所以只能收 map。

**(b) 成功摘要比设计里更细。** `runRestart` 的摘要区分了 skipped：
`已重启 5 个实例，跳过 2 个`。跳过的来源包括状态不允许、用户手动跳过、
倒计时被单独取消——这些不体现的话，日志会显得比实际做的事更多。

**(c) `logStore.load()` 不返回 error。** 本文 §6.1 已经说了「解析失败只记 WARN」，
实施时索性把返回值也去掉：一个永远为 nil 的 error 只会诱使调用方写无用的判断。

### 12.3 测试

`go test -race ./schedule/...` 全绿。`schedule/runlog_test.go` 覆盖：

- 追加到 550 条后长度恒为 500，且**最新那条在**、排在 `list` 首位
- `trimOldest` 一次裁掉多条（上限被调小的场景）、未超限时不动、`nil` 原样返回
- `list`：按任务过滤 / limit 截断而 total 是截断前的数 / 倒序 / 不存在的任务返回空
- `list` 返回副本，调用方改它不影响存储
- 载入容错四种：文件不存在 / 空文件 / 内容损坏 / 顶层不是数组 —— 均按空列表继续，
  且载入失败后仍可正常追加
- 落盘后重新载入，字段正确恢复
- 载入超长文件时也裁剪
- `clear()` 后文件是**合法的空数组**而非被删除（否则下次 load 走「文件不存在」分支，
  区分不了「清空过」和「从没跑过」）
- 1000 次 `newRunRecordID()` 无重复

新增 `TestMain` 初始化 logger：`logStore.load()` 的容错分支会调 `logger.GetLogger()`，
未初始化时它返回 nil 并 panic——这个坑在写测试时就踩到了。
