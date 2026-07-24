# 停止 / 重启倒计时与游戏内公告

> 状态：**已实施**。本文保留原始设计（v2），下面是与当前代码的两处对齐说明。
> 变更点见文末 §11 的评审记录。

> **实现位置已迁移。** 本文成文时倒计时住在 `instance/countdown.go`，现已收敛为独立的
> `countdown` 包（RCON 另拆为 `rconx`）。重构动机、分层与逐步骤记录见
> `docs/COUNTDOWN_RCON_REFACTOR_PLAN.md`。名字对照：
>
> | 本文写的 | 当前代码 |
> |---|---|
> | `instance.CountdownConfig` | `countdown.Config`（构造只走 `FromSeconds` / `FromQuery`） |
> | `StopServer(name, WithCountdown(cfg))` | `countdown.Stop(ctx, name, cfg, opts...)`；`WithCountdown` 已删除 |
> | `instance.RunCountdown` / `FinishCountdown` | `countdown.Wait`（多实例）；登记表的释放由包内保证 |
> | `instance.GetCountdown` / `CancelCountdown` | `countdown.Get` / `countdown.Cancel` |
> | `CountdownActionStop/Restart` | `countdown.ActionStop` / `ActionRestart` |
>
> **取消语义已变更。** 本文 §6.2 的批量倒计时里，取消任一实例会终止整批；
> 现在每个实例持有独立的子 context，`Cancel(name)` **只放过那一台**，
> 它被接进 batchmanage 已有的 skip 机制、在阶段二被跳过，其余实例照常执行。
> 要终止整批请用 batchmanage 的取消接口。

## 1. 背景与目标

现在停止和重启是「立即执行」：`StopServer` 直接 RCON `DoExit`，`RestartServer` 先停再起。
玩家没有任何预警就被踢下线。

目标：在真正执行停止/重启之前，先等一段可配置的倒计时，
期间通过 RCON 周期性地向游戏内广播公告，告知玩家还有多久服务器将停止/重启；
同时通过 WebSocket 把剩余秒数实时推给前端。

## 2. 影响范围

| 包 | 改动性质 |
|---|---|
| `instance` | **新增能力**：倒计时 + 公告原语，挂到 `StopServer` / `RestartServer` 的 Options 上 |
| `batchmanage` | **透传 + 编排**：请求体新增倒计时字段，批量场景下倒计时统一前置 |
| `webapi/serverapi` | **透传**：单实例 stop/restart 接口接收倒计时参数 |
| `realtime` | **新增 WS 事件**：倒计时进度推送 |
| `schedule` | **透传**：定时任务可指定是否倒计时及其参数 |

## 3. 数据模型

新增到 `instance` 包（`instance/countdown.go`）：

```go
// CountdownConfig 停止/重启前的倒计时与游戏内公告配置。
type CountdownConfig struct {
    // Total 倒计时总时长。为 0 表示不倒计时，立即执行（保持现有行为）。
    // 非 0 时最小 30s，见 §5 的校验规则。
    Total time.Duration

    // Points 播报时间点：在「剩余时间」等于这些值时各播报一次。
    // 例如 [600s, 300s, 60s, 30s, 10s] 表示剩余 10分/5分/1分/30秒/10秒 时各播一次。
    // 允许乱序传入，内部会降序排序并去重。
    Points []time.Duration

    // Template 公告文案，支持占位符，见 §4。
    Template string

    // Command 发公告用的 RCON 指令，默认 "ServerChat"。
    Command string
}
```

**默认值**（`Total > 0` 但其余字段留空时）：

| 字段 | 默认 | 理由 |
|---|---|---|
| `Points` | 由 `Total` 推导，见 §5.3 | 用户不填也要有合理节奏 |
| `Template` | `服务器将在 {time} 后{action}，请及时下线` | |
| `Command` | `ServerChat` | ARK 的聊天频道公告。`Broadcast` 是屏幕中央大字提示，更醒目但也更打扰，留给调用方选 |

`Total == 0` 是关键的兼容开关：所有现有调用方（不传倒计时）行为完全不变。

## 4. 占位符

模板中支持的占位符，统一用 `{name}` 花括号语法：

| 占位符 | 替换为 | 示例 |
|---|---|---|
| `{time}` | 剩余时间（已格式化的中文） | `10 分钟` / `30 秒` |
| `{action}` | 本次操作 | `停止` / `重启` |
| `{instance}` | 实例名 | `meijue` |

未识别的占位符（如 `{foo}`）**原样保留**，不清空——用户写错了应该能在游戏里一眼看出来，
静默吞掉只会让人以为是功能坏了。

剩余时间的格式化规则（`formatRemaining`）：

| 剩余 | 输出 |
|---|---|
| ≥ 1 小时 | `1 小时 30 分钟` |
| ≥ 1 分钟 | `10 分钟` |
| < 1 分钟 | `30 秒` |

## 5. 播报节奏与表单校验

采用**播报时间点列表**（原方案 3）：调用方直接给出在剩余多少秒时播报。
这贴近 ARK 服主的实际用法——前疏后密，最后一分钟连着提醒。

### 5.1 校验规则（后端强制，前端同步实现）

`Points` 是最容易配错的地方（`Total=600` 却填了 `700`），必须在**保存时**就拦下来，
而不是等倒计时跑起来才发现有个点位永远不会触发：

| 规则 | 说明 | 错误文案 |
|---|---|---|
| `Total >= 30s` | 非 0 时的最小值 | `倒计时最少 30 秒` |
| `Total <= 24h` | 上限，防手滑填成秒/毫秒混淆 | `倒计时最多 24 小时` |
| 每个 `point > 0` | | `播报时间点必须大于 0` |
| 每个 `point <= Total` | **核心规则**：不能出现 700 > 600 这种 | `播报时间点 {point} 超过了倒计时总时长 {total}` |
| `len(Points) <= 20` | 防刷屏 | `播报时间点最多 20 个` |
| 去重 | 重复的点位只播一次 | 静默去重，不报错 |

`point == Total` 是**合法**的：表示「倒计时一开始就播一条」，是常见用法。

排序与去重由后端做（降序），前端不必要求用户按顺序填。

### 5.2 触发语义

`Total=600s, Points=[600,300,60,30,10]` 的时间线：

| 时刻 | 剩余 | 动作 |
|---|---|---|
| t=0s | 600s | 播报「服务器将在 10 分钟 后重启」 |
| t=300s | 300s | 播报「…5 分钟…」 |
| t=540s | 60s | 播报「…1 分钟…」 |
| t=570s | 30s | 播报「…30 秒…」 |
| t=590s | 10s | 播报「…10 秒…」 |
| t=600s | 0 | **执行停止/重启** |

用 `time.Timer` 逐个点位触发，不做 `for { sleep(1s) }` 轮询。

### 5.3 默认点位推导

用户只填 `Total` 不填 `Points` 时，取所有 `<= Total` 的预设点位：

```
预设 = [3600, 1800, 900, 600, 300, 180, 60, 30, 10]  (秒)
```

`Total=600` → `[600, 300, 180, 60, 30, 10]`；`Total=60` → `[60, 30, 10]`；
`Total=30` → `[30, 10]`。

## 6. 执行流程

### 6.1 单实例

```
StopServer(name, WithCountdown(cfg))
  │
  ├─ cfg.Total == 0 → 直接走原有逻辑，行为不变
  │
  ├─ runCountdown(ctx, name, "停止", cfg)
  │    ├─ 到点位 → RCON 公告 + WS 推送剩余秒数
  │    └─ 剩余 0 → 返回
  │
  └─ stopServerInternal(name)   ← 原有逻辑，一行不改
```

`runCountdown` 的行为约定：

- **公告失败不中断倒计时。** RCON 发不出去（玩家全下线、服务器卡住）只记 WARN，
  倒计时照常走完并执行停止。公告是尽力而为的通知，不是停止操作的前置条件。
- **ctx 取消 → 立即返回错误，不执行停止。** 用于「取消关服」。

### 6.2 批量（batchmanage）

**倒计时统一前置，停止仍然串行。**

```
批量停止（含倒计时）
  │
  ├─ 阶段一：并发向【所有】目标实例播报同一轮倒计时
  │           所有实例的玩家在同一时刻看到同样的剩余时间
  │           WS 同步推送每个实例的剩余秒数
  │
  └─ 阶段二：倒计时归零后，按原有串行逻辑逐个停止
              WS 推送切换为「执行中」态，见 §6.3
```

总耗时 = 倒计时 + 原有串行停止耗时，且全服玩家的倒计时是对齐的。

### 6.3 倒计时归零后的显示态（重要）

倒计时归零不等于操作完成——阶段二的串行停止本身要花几十秒到几分钟。
**剩余秒数减到 0 之后就不再显示倒计时**，而是切换成执行态文案：

| 阶段 | `remaining` | 前端显示 |
|---|---|---|
| 倒计时中 | `> 0` | `将在 3 分 20 秒 后重启` |
| 倒计时归零、操作执行中 | `0` | `服务器重启中…` / `服务器关闭中…` |
| 操作完成 | — | 由既有的 `server_stopped` / `server_started` 事件接管 |

因此 WS 事件里除了 `remaining` 还要带一个 `phase` 字段（见 §7.3），
前端**不要**靠 `remaining <= 0` 自行推断——阶段二可能持续几分钟，
只靠数字会让 UI 卡在「0 秒」上不动，看起来像卡死了。

### 6.4 与 CAS 状态机的关系

**保持现状**：`webapi/serverapi` 仍然先 CAS 把状态改成 `stopping`/`restarting` 再异步执行。
实例在整个倒计时期间显示为「停止中」。

- 语义上「已经进入停止流程」是准确的
- 期间任何重复的停止/重启请求都会被 CAS 挡掉（409），天然防重复触发
- 更精确的进度由 §7.3 的 WS 事件提供，不需要新增状态枚举

## 7. 接口变更

### 7.1 单实例（`webapi/serverapi`）

现有路由是 **GET**：`GET /api/server/:name/stop`、`GET /api/server/:name/restart`。
**本次不改成 POST**（后续迭代再说），倒计时参数走 query string，全部可选：

| 参数 | 类型 | 说明 |
|---|---|---|
| `countdown` | int | 倒计时秒数，缺省或 0 = 立即执行（现有行为） |
| `notify_points` | string | 逗号分隔的播报点位秒数，如 `600,300,60,30,10`；缺省按 §5.3 推导 |
| `notify_message` | string | 公告模板，缺省用内置文案 |
| `notify_command` | string | `ServerChat`（默认）或 `Broadcast` |

```
GET /api/server/meijue/restart?countdown=600&notify_points=600,300,60,30,10
```

校验失败返回 **400** 并带上 §5.1 的具体错误文案。

新增两个接口：

| Method | Path | 说明 |
|---|---|---|
| GET | `/api/server/:name/countdown` | 查询该实例的倒计时状态（供页面首次加载补状态，WS 之外的兜底） |
| POST | `/api/server/:name/countdown/cancel` | 取消倒计时，回滚状态到 `started` |

### 7.2 批量（`batchmanage`）

`BatchOperationRequest` 增加字段：

```go
type BatchOperationRequest struct {
    Instances    []string `json:"instances"`
    DelaySeconds int      `json:"delay_seconds"`

    // 新增
    Countdown     int      `json:"countdown"`      // 倒计时秒数，0 = 不倒计时
    NotifyPoints  []int    `json:"notify_points"`  // 播报点位（秒），空则按 §5.3 推导
    NotifyMessage string   `json:"notify_message"` // 公告模板
    NotifyCommand string   `json:"notify_command"` // ServerChat / Broadcast
}
```

与既有 `DelaySeconds` 含义不重叠，两者都保留：

- `DelaySeconds`：**实例之间**的间隔，防止同时启动多个实例把机器压垮
- `Countdown`：**操作开始前**给玩家的预告时间

批量操作的 SSE 日志里也要能看到倒计时进度（`op.sendLog`），
否则用户在前端会觉得点了没反应。

### 7.3 WebSocket 事件（新增）

复用现有的 `/api/ws/events` 通道与 `realtime.ServerEvent` 结构（`realtime/hub.go:15`），
新增一个事件类型：

```go
// realtime/hub.go
func (h *Hub) BroadcastCountdownEvent(instanceName, action, phase string, remaining int)
```

推送的消息体：

```json
{
  "event_type": "countdown",
  "instance_name": "meijue",
  "timestamp": 1753000000,
  "message": "服务器将在 3 分 20 秒 后重启",
  "status": "restarting",
  "data": {
    "action": "restart",
    "phase": "counting",
    "remaining": 200
  }
}
```

| 字段 | 取值 | 说明 |
|---|---|---|
| `data.action` | `stop` / `restart` | 目标操作 |
| `data.phase` | `counting` / `executing` / `cancelled` | 见 §6.3 |
| `data.remaining` | int（秒） | `phase != "counting"` 时恒为 0 |
| `instance_name` | 实例名 | |

**推送时机**：

- 每个播报点位触发时推一次（与 RCON 公告同步）
- **另外每秒推一次**，让前端能平滑倒数，而不是只在点位跳变
  ⚠️ 每秒 × N 个实例的推送量：批量停 10 个实例 = 10 msg/s，
  对现有 Hub（`sendEventToAll`）无压力，但若以后实例数上百需要改成聚合成一条
- 倒计时归零切 `phase=executing` 时推一次
- 被取消时推一条 `phase=cancelled`

前端据此可以在实例卡片上直接显示「3 分 20 秒后重启」并逐秒递减。

### 7.4 定时任务（`schedule`）

`Task` 增加倒计时配置，**由入参指定是否启用**：

```go
type Task struct {
    // ...既有字段...

    // Countdown 倒计时秒数。0 = 不倒计时（默认，保持现有行为）。
    Countdown     int    `json:"countdown"`
    NotifyPoints  []int  `json:"notify_points"`
    NotifyMessage string `json:"notify_message"`
    NotifyCommand string `json:"notify_command"`
}
```

`Task.Validate()` 复用 §5.1 的同一套校验函数。

对 `TaskRestart`：倒计时参数原样透传给 `batchmanage.StartOperation`。
对 `TaskUpdate`：倒计时作用于「更新前的批量停服」那一步——
半夜自动更新同样需要提前通知玩家。

前端 `ScheduleManager.vue` 的新建/编辑弹窗增加一组倒计时字段
（一个「启用倒计时」开关 + 展开后的总时长/点位/文案/指令）。

## 8. 不做的事

- **不阻止新玩家在倒计时期间进入**。ARK 没有直接的「拒绝新连接」RCON 指令，
  能做的只有反复公告。
- **不改 stop/restart 的 HTTP 方法**（保持 GET），后续迭代再说。
- **不新增 `stopping_countdown` 状态枚举**，见 §6.4。

## 9. 实施顺序

1. `instance/countdown.go`：`CountdownConfig` + 校验 + `formatRemaining` + 模板渲染 + `runCountdown`
   （**纯逻辑，先写单测**）
2. `realtime/hub.go`：`BroadcastCountdownEvent`
3. `instance`：`WithCountdown` Option，接进 `StopServer` / `RestartServer`
4. `webapi/serverapi`：query 参数解析 + `/countdown` 查询与取消接口
5. `batchmanage`：请求体字段 + §6.2 的两阶段编排
6. `schedule`：Task 字段 + 透传 + 校验复用
7. 前端：实例卡片倒计时显示、`ScheduleManager.vue` 表单、停止/重启对话框的倒计时选项

## 10. 验证要点

**单测**（`instance/countdown_test.go`，纯函数部分优先）：

- `formatRemaining`：1h30m / 10m / 30s / 0 各自的输出
- 模板渲染：三个占位符都被正确替换；无占位符时原样输出；
  **未知占位符 `{foo}` 保持原样**而不是被清空
- 校验（§5.1 每条规则一个用例）：
  - `Total=29s` → 拒绝；`Total=30s` → 通过
  - **`Total=600, Points=[700]` → 拒绝**（这是本次评审点名要防的情况）
  - `Total=600, Points=[605]` → 拒绝
  - `Total=600, Points=[600]` → 通过（开场即播是合法的）
  - 乱序 `[60,600,300]` → 通过且内部按降序整理
  - 重复 `[60,60]` → 通过且去重成一个
- 默认点位推导：`Total=600` → `[600,300,180,60,30,10]`；`Total=30` → `[30,10]`
- `Total=0` → 不产生任何点位（兼容开关）
- ctx 取消 → `runCountdown` 立即返回且**未**执行停止

**端到端**：

1. **兼容性回归先做**：不带任何倒计时参数调 stop/restart，行为与改动前完全一致
2. 单实例 `countdown=120&notify_points=120,60,30,10`，进游戏确认收到 4 条公告，
   第 120 秒服务器关闭
3. 公告文案里的 `{time}` 被正确替换成「2 分钟」「30 秒」
4. **WS**：前端订阅 `/api/ws/events`，确认收到 `event_type=countdown` 且 `remaining` 逐秒递减
5. **归零切换**：倒计时归零后 WS 推 `phase=executing`，前端显示从「x 秒后重启」
   变为「服务器重启中…」，且**不会**卡在「0 秒」
6. 倒计时期间调 `/countdown/cancel`：服务器**不**关闭，状态回到 `started`，
   WS 推 `phase=cancelled`
7. 倒计时期间手动 force-stop 该实例，倒计时应记 WARN 但不 panic
8. 批量停止 3 个实例带倒计时：3 个实例的玩家**同时**收到同样的剩余时间，
   且三条 WS 倒计时并行递减（验证 §6.2 的编排）
9. 定时重启任务开启倒计时，到点后确认先公告再重启
10. `Broadcast` 与 `ServerChat` 两种 command 在游戏内的显示效果都确认一遍
11. **表单校验**：前端填 `Total=600, Points=700` 应在保存时就被拦下，
    且直接调 API 传同样的值也返回 400

## 11. 评审记录

| 编号 | 问题 | 结论 |
|---|---|---|
| A | 占位符语法 | ✅ 用 `{time}` 花括号格式 |
| B | 播报节奏 | ✅ 采用**方案 3**（时间点列表）；`Total` 最小 30s；**必须校验点位不超过 Total** |
| C | 批量倒计时编排 | ✅ 统一前置并发播报、停止串行；**归零后显示「服务器关闭中/重启中」而非继续显示倒计时** |
| D | 倒计时期间实例状态 | ✅ 保持现状 |
| E | stop/restart 改 POST | ✅ 本次不改，后续迭代 |
| F | `schedule` 接入 | ✅ 接入，入参指定是否倒计时 |
| G | 阻止新玩家进入 | ✅ 不做 |
| H | **新增**：WS 推送 | ✅ 推剩余秒数、目标操作、实例名，见 §7.3 |
