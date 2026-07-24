# 倒计时停止/重启 与 RCON 的收敛重构方案

> 状态：**已实施**（§5 的 8 个步骤全部完成，`go build ./...` / `go vet ./...` / `go test ./countdown/...` 通过）。
> 实施中相对本文的一处补充见 §9。
>
> 目标：把当前散落在 `instance` / `webapi/serverapi` / `batchmanage` / `schedule` / `realtime`
> 五处的「延迟后停止/重启」逻辑收敛到一个 `countdown` 包；把 RCON 连接与命令执行抽成独立的
> `rconx` 包。重构完成后，所有倒计时场景的最终动作都统一收口到
> `instance.StopServer` / `instance.RestartServer` 的一次调用上。
>
> 本文档是**执行文档**：可以照着 §5 的步骤顺序改，每一步都能单独 `go build ./...` 通过。

---

## 1. 现状：功能被切成了几块

### 1.1 倒计时相关代码分布

| 位置 | 承担的职责 | 行数量级 |
|---|---|---|
| `instance/countdown.go` | 配置类型、校验、点位归一化、文案渲染、**全局登记表**、主循环、WS 推送、RCON 播报 | 423 |
| `instance/countdown_test.go` | 上述逻辑的单测 | 355 |
| `instance/server.go` | `StartServerOptions.Countdown` 字段 + `WithCountdown()`；`StopServer`/`RestartServer` 内联「跑倒计时 → 失败则回滚 started」 | ~20 |
| `webapi/serverapi/countdown.go` | query string 解析（`countdown` / `notify_points` / `notify_message` / `notify_command`）、`GET /countdown`、`POST /countdown/cancel` | 122 |
| `batchmanage/manager.go` | `BatchOperationRequest.CountdownConfig()`（秒 → Duration 转换）、`StartOperation` 里的校验、`runCountdownPhase()`（多实例并发对齐播报） | ~80 |
| `schedule/task.go` | `Task.CountdownConfig()`（**同样的**秒 → Duration 转换）、`Validate()` 里的校验 | ~25 |
| `webapi/scheduleapi/scheduleapi.go` | DTO 里再抄一遍 4 个倒计时字段 | ~8 |
| `realtime/hub.go` | `CountdownPhase*` 常量 + `BroadcastCountdownEvent()` | ~30 |

### 1.2 具体的重复与耦合

**(a) 「秒 + 点位数组 + 文案 + 指令 → Config」这段转换存在 3 份**

- `batchmanage.BatchOperationRequest.CountdownConfig()`（`manager.go:50`）
- `schedule.Task.CountdownConfig()`（`task.go:63`）
- `serverapi.parseCountdownQuery()`（`countdown.go:21`，只是入参从 JSON 换成 query）

三份逻辑一模一样，任何一次字段调整（比如加一个「静默模式」）都要改三处。

**(b) `Validate()` 在 4 个地方被各自调用**

`serverapi.parseCountdownQuery` / `batchmanage.StartOperation` / `schedule.Task.Validate` /
`instance.RunCountdown` 内部。谁忘了调就会出现「点位永远触发不到」这类只有跑起来才发现的问题。

**(c) 「等待 → 执行」的编排方式在每个调用方都不同**

| 调用方 | 编排方式 |
|---|---|
| `serverapi` | 传 `WithCountdown(cfg)`，倒计时藏在 `StopServer` 内部 |
| `batchmanage` | 自己起 goroutine 调 `RunCountdown`，再手动 `defer FinishCountdown`，然后进入阶段二串行执行（**不**传 `WithCountdown`） |
| `schedule` | 完全委托给 `batchmanage` |

`RunCountdown` / `FinishCountdown` 的配对纪律是**口头约定**，靠调用方自己 `defer`。
`batchmanage/manager.go:628` 那句 `defer instancepkg.FinishCountdown(name)` 一旦漏写，
实例就会永久留在登记表里，`GET /countdown` 一直返回 active。

**(d) 登记表是全局 map，没有防重入**

`instance/countdown.go:313` 直接 `activeCountdowns[instanceName] = ...` 覆盖写。
若单实例倒计时与批量倒计时撞上同一个实例，后者会静默顶掉前者的 `cancel`，
前一个 goroutine 就再也取消不掉了。

**(e) 取消语义分裂**

- 单实例：`serverapi.cancelCountdown` → `instance.CancelCountdown(name)` → 取消该实例的 ctx
- 批量：`batchmanage.CancelCurrent()` → 取消 `op.ctx` → 所有实例的倒计时一起断

而 `instance.CancelCountdown(name)` 在批量场景下会让 `runCountdownPhase` 把 `cancelled` 置真，
**取消一个实例 = 取消整批**——这个行为目前既没写进文档也没有日志，且与 `batchmanage` 自己
已有的 `SkipInstance`（跳过单个实例）能力自相矛盾：同一件事有两种粒度完全不同的表达。

**(f) 状态回滚只在单实例路径上做**

`StopServer` 在倒计时被取消时会 `WriteInstanceState(name, StatusStarted)` 回滚（`server.go:511`），
因为它的 CAS 发生在倒计时**之前**。批量路径的 CAS 在阶段二、倒计时**之后**，所以不需要回滚。
这个不对称目前只能靠读两处代码推出来。

**(g) `instance` → `realtime` 的反向依赖**

`instance` 包对 `realtime` 的**唯一**引用就是 `countdown.go:5`。
把倒计时挪走之后，领域包 `instance` 将彻底不依赖传输层。

### 1.3 RCON 相关代码分布

| 位置 | 内容 |
|---|---|
| `instance/server.go:726` `SendRCONCommand` | 校验存活 → 读配置 → 校验密码 → `rcon.Dial` **3 次重试、间隔 2s** → `Execute` → 日志 |
| `realtime/ws.go:200` `rconExecuteCommand` | 校验存活 → 读配置 → 校验密码 → `rcon.Dial`（**不重试**）→ `Execute` → 组装 `RCONResponse` |
| `instance/common.go:225` `SaveWorldSafely` | 调 `SendRCONCommand(name, "saveworld")` |
| `instance/server.go:572` `stopServerInternal` | 调 `SendRCONCommand(name, "DoExit")` |
| `instance/countdown.go:371` `announce` | 调 `SendRCONCommand(name, "ServerChat ...")` |

`realtime/ws.go` 完整抄了一遍连接流程（存活检查、配置加载、空密码校验、Dial、Close、错误包装），
只是把重试去掉了。两处的超时/重试/日志格式已经开始漂移，`github.com/gorcon/rcon` 这个第三方
依赖也因此同时出现在 `instance` 和 `realtime` 两个包里。

---

## 2. 目标架构

新增两个包，删掉一个文件，收缩三处重复转换。

```
pkg/*                          # 叶子工具，不变
config                         # 不变
process                        # 不变
rconx                     ★新  # RCON 连接 + 命令执行（依赖 config / process / logger）
state / installer / mirror     # 不变
instance                       # 去掉 countdown.go，改用 rconx；不再依赖 realtime
countdown                 ★新  # 倒计时编排（依赖 config / state / rconx / realtime / instance）
webapi/* / batchmanage / schedule / gui   # 依赖 countdown
```

依赖方向仍然是单向无环。关键点：

- **`rconx` 在 `instance` 之下** —— 这样 `instance` 和 `realtime` 都能用它，
  `github.com/gorcon/rcon` 只在 `rconx` 里出现一次。
- **`countdown` 在 `instance` 之上** —— 因为它的本质工作就是
  「等一段时间，然后调用 `StopServer` / `RestartServer`」。把它放在 `instance` 之上，
  执行动作可以直接调用，不必再通过回调注入；`instance` 也就不需要 `WithCountdown` 选项了。

### 2.1 为什么 `rconx` 不放进 `pkg/`

`pkg/` 按 CLAUDE.md 的约定是「纯工具叶子包（无领域依赖）」。
`rconx` 需要 `cfgpkg.LoadInstanceConfig`（拿 RCONPort / ServerAdminPassword）
和 `procpkg.IsServerRunning`（存活预检），属于领域包，因此放在顶层，与 `process` 同层之上。

包名用 `rconx` 而非 `rcon`，避免和 `github.com/gorcon/rcon` 的导入名冲突。

---

## 3. 新包 API 设计

### 3.1 `rconx` 包

```
rconx/
├── rconx.go        # Execute + 选项
└── rconx_test.go
```

```go
package rconx

// 默认策略：与现有 instance.SendRCONCommand 一致，保持行为不变。
const (
    DefaultAttempts      = 3
    DefaultRetryInterval = 2 * time.Second
)

type Option func(*options)

// WithAttempts 设置连接尝试次数（含首次）。1 = 不重试。
func WithAttempts(n int) Option

// WithRetryInterval 设置重试间隔。
func WithRetryInterval(d time.Duration) Option

// Execute 向实例发送一条 RCON 命令并返回响应。
//
// 内部完成：存活预检 → 加载实例配置 → 空密码校验 → 带重试的 Dial → Execute → Close。
// ctx 取消时中断重试等待（现有实现用的是裸 time.Sleep，不可中断）。
func Execute(ctx context.Context, instanceName, command string, opts ...Option) (string, error)
```

错误使用哨兵，便于调用方区分：

```go
var (
    ErrNotRunning     = errors.New("server is not running")
    ErrPasswordEmpty  = errors.New("RCON password is empty")
    ErrConnectFailed  = errors.New("failed to connect to RCON server")
)
```

调用方改写：

| 原调用 | 新调用 |
|---|---|
| `instance.SendRCONCommand(name, cmd)` | `rconx.Execute(ctx, name, cmd)` |
| `realtime.rconExecuteCommand` 里的 Dial 段 | `rconx.Execute(ctx, name, cmd, rconx.WithAttempts(1))` |

`instance.SendRCONCommand` 保留为一行转发（`realtime/ws.go` 以外还有 GUI 等潜在调用方），
标注 `// Deprecated: use rconx.Execute.`，后续版本删除。

### 3.2 `countdown` 包

```
countdown/
├── config.go       # Config / 常量 / Validate / normalize / FromSeconds / FromQuery
├── message.go      # formatRemaining / actionLabel / render
├── registry.go     # 登记表：Get / Cancel / register / release
├── run.go          # Wait / Stop / Restart 编排
├── config_test.go  # 由 instance/countdown_test.go 迁入
└── run_test.go
```

#### 配置与构造（消灭 3 份重复转换）

```go
package countdown

type Action string

const (
    ActionStop    Action = "stop"
    ActionRestart Action = "restart"
)

const (
    MinTotal        = 30 * time.Second
    MaxTotal        = 24 * time.Hour
    MaxPoints       = 20
    DefaultTemplate = "服务器将在 {time} 后{action}，请及时下线"
    DefaultCommand  = "ServerChat"
)

type Config struct {
    Total    time.Duration
    Points   []time.Duration
    Template string
    Command  string
}

func (c *Config) Enabled() bool
func (c *Config) Validate() error

// FromSeconds 是唯一的构造入口：batchmanage / schedule / serverapi 都走它。
// totalSeconds <= 0 返回 nil（表示不倒计时）。
func FromSeconds(totalSeconds int, points []int, template, command string) *Config

// FromQuery 解析 query string 形式的参数（serverapi 的 GET 路由用）。
// get 通常直接传 gin.Context.Query，countdown 包因此不依赖 gin。
// 返回的 Config 已通过 Validate。
func FromQuery(get func(string) string) (*Config, error)
```

`FromSeconds` 内部同时完成 `Validate()`？**不**。保持「构造不校验、调用方显式 `Validate()`」，
但 `Wait` / `Stop` / `Restart` 入口一律先 `Validate()`，避免任何调用方漏掉——
这与现在 `RunCountdown` 的行为一致，只是不再需要外层重复调。

#### 登记表（补上防重入）

```go
type Status struct {
    InstanceName string `json:"instance_name"`
    Action       Action `json:"action"`
    Phase        string `json:"phase"`
    Remaining    int    `json:"remaining"`
}

// Get 查询实例当前的倒计时状态。
func Get(instanceName string) (*Status, bool)

// Cancel 取消**该实例**正在进行的倒计时。
// 已进入 executing 阶段则返回 false（停止流程已开始，取消无意义）。
//
// 批量倒计时中只影响这一个实例，其余实例继续（见 §4.2）。
func Cancel(instanceName string) bool

var (
    ErrInProgress = errors.New("countdown already in progress for this instance")
    ErrCancelled  = errors.New("countdown cancelled")
)
```

`register` 内部改为「已存在则返回 `ErrInProgress`」，取代现在的静默覆盖。
批量与单实例撞车时，后来者拿到明确错误，而不是把前者的 `cancel` 弄丢。

#### 编排（核心）

```go
// Result 一轮倒计时的结果。
type Result struct {
    // Cancelled 是被**单独**取消倒计时的实例（用户对这些实例点了取消）。
    // 调用方应把它们排除在后续动作之外，其余实例照常执行。
    Cancelled []string
}

// AllCancelled 报告是否所有实例都被取消了。
func (r Result) AllCancelled(total int) bool

// IsCancelled 报告指定实例是否被取消。
func (r Result) IsCancelled(instanceName string) bool

// Wait 对一组实例执行一轮**对齐的**倒计时：同一时刻同一文案，并发播报。
//
// 只倒计时，不执行任何动作。batchmanage 用它做阶段一——
// 它的阶段二有 CAS、逐实例结果、实例间延迟，不适合塞进本包。
//
// 每个实例持有独立的子 ctx：Cancel(name) 只掐断那一个实例，其余继续走完。
// 返回的 error 仅在**父 ctx** 被取消时非 nil（整批中止，例如进程退出或
// batchmanage 的 CancelCurrent）；单实例取消不算错误，落在 Result.Cancelled 里。
//
// 内部保证登记表的注册与释放配对，调用方不需要也不应该再手动 Finish。
func Wait(ctx context.Context, instances []string, action Action, cfg *Config) (Result, error)

// Stop 倒计时结束后停止实例。cfg 为 nil / 未启用时等价于直接 StopServer。
//
// 倒计时被取消时：不执行停止，把实例状态回滚到 started，返回 ErrCancelled。
// opts 透传给 instance.StopServer（调用方通常传 WithStatePreset）。
func Stop(ctx context.Context, instanceName string, cfg *Config, opts ...instancepkg.StartServerOptionsFunc) error

// Restart 同上，最终调用 instance.RestartServer。
func Restart(ctx context.Context, instanceName string, cfg *Config, opts ...instancepkg.StartServerOptionsFunc) error
```

`Stop` / `Restart` 的实现骨架（把原先散在 `server.go:508` 和 `server.go:685` 的两段合成一处）：

```go
func Stop(ctx context.Context, name string, cfg *Config, opts ...instancepkg.StartServerOptionsFunc) error {
    res, err := Wait(ctx, []string{name}, ActionStop, cfg)
    if err != nil || res.IsCancelled(name) {
        // 倒计时未走完：服务器一直在正常运行，把 CAS 抢占的 stopping 状态还回去
        _ = statepkg.WriteInstanceState(name, statepkg.StatusStarted, "")
        if err != nil {
            return err
        }
        return ErrCancelled
    }
    defer release(name)          // executing 阶段结束后清登记
    return instancepkg.StopServer(name, opts...)
}
```

单实例场景下 `Result.Cancelled` 与 `error` 的区分意义不大，`Stop`/`Restart` 把两者
统一归到「不执行 + 回滚 + 返回错误」；区分只对 `Wait` 的多实例调用方（`batchmanage`）有用。

状态回滚从 `instance` 挪到 `countdown`：`instance.StopServer` 回归「立刻停止」的单一语义，
不再关心倒计时被取消这种编排层的事。

---

## 4. 已确定的两个语义

### 4.1 `instance` 移除 `WithCountdown` —— 已确定

`StartServerOptions.Countdown` 字段与 `WithCountdown()` 一并移除，
`StopServer` / `RestartServer` 里的两段倒定时代码删掉。调用方改用 `countdown.Stop/Restart`。

理由：留着会出现两条等价路径（`countdown.Stop` 和 `StopServer(WithCountdown(...))`），
而后者绕过登记表防重入与状态回滚的统一实现，正是本次重构要消灭的东西。
`WithCountdown` 的注释本身已经在警告「批量场景不要用这个」——这类「有个选项但某些调用方不许用」
的 API 就是应该上移的信号。

影响面：`WithCountdown` 目前只有 `serverapi.runStopServerTask` / `runRestartServerTask` 两处调用。

### 4.2 批量倒计时中取消单实例 = 只停这一个 —— 已确定（行为变更）

**这是一处有意的行为变更**，不是纯重构：现状「取消任一实例 = 整批取消」被改成
「只把该实例从本批剔除，其余实例继续」。

`batchmanage` 本来就有 `SkipInstance` 跳过单个实例的能力，倒计时取消在语义上就是同一件事
（「这台不要动」），却走了粒度完全不同的另一条路。统一之后两者共用一套表达。

三个层面的落地：

1. **`countdown.Wait`**：为每个实例派生独立子 ctx，`Cancel(name)` 只取消对应实例，
   被取消的实例进 `Result.Cancelled`。父 ctx 取消（进程退出 / `CancelCurrent`）仍然掐断全部。
2. **`batchmanage`**：阶段一拿到 `Result.Cancelled` 后，把这些实例接进**已有的** skip 通道
   （见 Step 6），阶段二的循环无需改动就会跳过它们。
3. **整批取消的入口不变**：`POST /api/batch/cancel` → `CancelCurrent()` → `op.cancel()`
   → 父 ctx → `Wait` 返回 error → 整批中止。

保留的边界情况：若所有实例都被逐个取消，`Result.AllCancelled()` 为真，
`batchmanage` 按「整批被取消」收尾并广播 completed(0/0/N)，与现在的表现一致。

**并发安全提醒**：`SkipInstance` 和倒计时取消都会 close 同一个 skip channel，
存在双重 close panic 的窗口（用户在倒计时期间先点了 skip，随后倒计时又被取消）。
两条路径必须统一走一个带 `sync.Once` 的 `op.signalSkip(name)`，见 Step 6。

---

## 5. 执行步骤

每一步结束都应能 `go build ./...` + `go vet ./...` 通过。

### Step 1 — 新建 `rconx` 包

1. 新建 `rconx/rconx.go`，把 `instance/server.go:726-779` 的 `SendRCONCommand` 主体搬进
   `rconx.Execute`，加上 `Option` / 哨兵错误 / ctx 可中断的重试等待。
2. `instance/server.go` 的 `SendRCONCommand` 改为一行转发 + `Deprecated` 注释。
3. `go build ./...` —— 此时行为完全不变。

### Step 2 — `realtime` 改用 `rconx`

1. `realtime/ws.go` 的 `rconExecuteCommand` 删掉存活检查、配置加载、密码校验、Dial 全段，
   改为 `rconx.Execute(ctx, name, cmd, rconx.WithAttempts(1))`，只保留 `RCONResponse` 组装。
2. 删除 `realtime/ws.go` 里的 `github.com/gorcon/rcon` 与 `cfgpkg` / `procpkg` 导入（若不再使用）。
3. 验证：交互式 RCON 面板发一条 `listplayers`。

### Step 3 — 新建 `countdown` 包（先搬运，不改调用方）

1. `git mv instance/countdown.go countdown/` 后拆成 `config.go` / `message.go` / `registry.go` / `run.go`；
   `instance/countdown_test.go` 同步迁入并改包名。
2. 类型改名：`CountdownConfig` → `Config`，`CountdownStatus` → `Status`，
   `CountdownActionStop/Restart` → `ActionStop/ActionRestart`（改为 `Action` 类型），
   `MinCountdownTotal` → `MinTotal` 等。`RunCountdown` → 内部 `run`，对外只暴露 `Wait/Stop/Restart`。
3. RCON 播报改调 `rconx.Execute`。
4. 新增 `FromSeconds` / `FromQuery` / `ErrInProgress` / `ErrCancelled` / `register` 防重入。
5. 新增 `Wait` / `Result` / `Stop` / `Restart`。要点：
   - `Wait` 为每个实例 `context.WithCancel(ctx)` 派生子 ctx 并存进登记表，
     `Cancel(name)` 只掐这一个（§4.2）；父 ctx 取消才整体返回 error。
   - 播报失败只记 WARN、不中断倒计时（保持现有行为）。
   - `Stop` / `Restart` 内含被取消后的状态回滚。
6. 此时 `instance` 编译会报错（`WithCountdown` 引用了已删类型）—— Step 4 一并修。

### Step 4 — 清理 `instance`

1. 删除 `StartServerOptions.Countdown` 字段与 `WithCountdown()`。
2. 删除 `StopServer`（`server.go:507-514`）与 `RestartServer`（`server.go:683-691`）里的倒定时代码块。
3. 确认 `instance` 已无 `asa-server/realtime` 导入。

### Step 5 — 改调用方 `webapi/serverapi`

1. `countdown.go`：`parseCountdownQuery` / `parsePointsCSV` 整体删除，
   改为 `cfg, err := countdown.FromQuery(c.Query)`。
2. `getCountdown` / `cancelCountdown` 改调 `countdown.Get` / `countdown.Cancel`。
3. `runStopServerTask` / `runRestartServerTask`：

```go
func (h *Handler) runStopServerTask(name string, cfg *countdown.Config) {
    if err := countdown.Stop(h.serverCtx, name, cfg, instancepkg.WithStatePreset()); err != nil {
        logger.GetLogger().Errorf("failed to stop server '%s': %v", name, err)
    }
}
```

   顺带修掉一处现有问题：原来传的是 `context.Background()`，进程退出时倒计时不会随之收敛；
   改用 `h.serverCtx` 后服务停止能带下倒计时。

### Step 6 — 改调用方 `batchmanage`

1. `BatchOperationRequest.CountdownConfig()` 改为一行：
   `return countdown.FromSeconds(r.Countdown, r.NotifyPoints, r.NotifyMessage, r.NotifyCommand)`。
2. `BatchOperation.countdown` 字段类型改 `*countdown.Config`；
   `StartOperation` 签名同步；`StartOperation` 里的 `Validate()` 可保留
   （批量要在 HTTP 层就返回 400，不能等后台 goroutine 跑起来才发现）。
3. **先把 skip 通道的 close 收敛到一个带 `sync.Once` 的方法**（§4.2 的并发安全前提）。
   `BatchOperation` 增加 `skipOnce map[string]*sync.Once`，与 `skipChannels` 在
   `StartOperation` 里一起初始化：

```go
// signalSkip 关闭实例的跳过通道。用户跳过与倒计时取消都走这里，
// 两条路径可能先后触发同一个实例，Once 保证不会重复 close。
func (op *BatchOperation) signalSkip(instanceName string) {
    op.mu.RLock()
    once, ok := op.skipOnce[instanceName]
    ch := op.skipChannels[instanceName]
    op.mu.RUnlock()
    if ok {
        once.Do(func() { close(ch) })
    }
}
```

   `SkipInstance` 里原来的 `close(ch)` 改调 `op.signalSkip(instanceName)`。
   注意 `SkipInstance` 持有 `op.mu` 写锁，需把 `signalSkip` 调用挪到解锁之后，
   或让 `signalSkip` 提供一个已持锁的内部变体——两种都行，别在持写锁时再取读锁。

4. `runCountdownPhase()` 从 45 行的手写并发缩成：

```go
// 返回 true 表示整批中止（父 ctx 取消或所有实例都被逐个取消）。
func (op *BatchOperation) runCountdownPhase() bool {
    if !op.countdown.Enabled() {
        return false
    }
    action := countdown.ActionStop
    if op.Type == BatchRestart {
        action = countdown.ActionRestart
    }
    op.sendLog("info", fmt.Sprintf("倒计时开始：%d 秒后%s %d 个实例",
        int(op.countdown.Total.Seconds()), action.Label(), len(op.Instances)), "")

    res, err := countdown.Wait(op.ctx, op.Instances, action, op.countdown)
    if err != nil {
        op.sendLog("warning", "倒计时被取消，批量操作未执行", "")
        return true
    }

    // 被单独取消的实例接进已有的 skip 机制，阶段二的循环无需改动就会跳过它们
    for _, name := range res.Cancelled {
        op.setResult(name, InstanceCancelled, "倒计时被取消")
        op.signalSkip(name)
        op.sendLog("warning", fmt.Sprintf("实例 '%s' 的倒计时被取消，将跳过", name), name)
    }

    if res.AllCancelled(len(op.Instances)) {
        op.sendLog("warning", "所有实例的倒计时都被取消，批量操作未执行", "")
        return true
    }

    op.sendLog("info", fmt.Sprintf("倒计时结束，开始执行（跳过 %d 个已取消的实例）",
        len(res.Cancelled)), "")
    return false
}
```

   手写的 `sync.WaitGroup` / `sync.Mutex` / `defer FinishCountdown` 全部由 `Wait` 内部承担。

5. 阶段二循环里的 skip 分支改成不覆盖阶段一已写好的结果：

```go
select {
case <-skipCh:
    // 倒计时被取消的实例已在阶段一标记为 cancelled，不要盖成 "skipped by user"
    if op.resultStatus(instanceName) != InstanceCancelled {
        op.setResult(instanceName, InstanceSkipped, "skipped by user")
        op.sendLog("warning", fmt.Sprintf("Instance '%s' skipped by user", instanceName), instanceName)
    }
    continue
default:
}
```

   需要新增一个 `op.resultStatus(name) InstanceOpStatus` 只读辅助方法
   （阶段二末尾那段查结果的内联循环也可以一并换成它）。

### Step 7 — 改调用方 `schedule`

1. `Task.CountdownConfig()` 改为一行 `countdown.FromSeconds(...)`。
2. `Task.Validate()` 里的倒计时校验保持不变（仍然复用 `cfg.Validate()`）。
3. `scheduler.go` 无需改动——它只把 `CountdownConfig()` 透传给 `batchmanage`。

### Step 8 — 收尾

1. `webapi/scheduleapi` 的 DTO 字段不动（HTTP 契约不变）。
2. 更新 `CLAUDE.md` 的目录树与分层依赖图，加入 `rconx` / `countdown`。
3. 更新 `docs/stop-restart-countdown.md`，把实现位置指向新包，并写明 §4.2 的批量取消语义。
4. 运行 `go test ./countdown/... ./rconx/... ./instance/...`。

---

## 6. 前端与 HTTP 契约

**格式不变，一处行为变更。** 本次重构不动任何路由、query 参数、JSON 字段或 WS 事件格式：

- `GET /api/server/:name/stop?countdown=600&notify_points=...` 参数名与语义原样保留
- `GET /api/server/:name/countdown` 与 `POST /api/server/:name/countdown/cancel` 响应体不变
- WS `countdown` 事件的 `action` / `phase` / `remaining` 三个字段不变
- 批量与定时任务的 JSON 请求体不变

因此 `app/src/` 下的前端代码**不需要改动就能跑**。但 §4.2 带来一处用户可感知的行为变化，
建议同步调整文案与提示：

| 位置 | 变化 |
|---|---|
| 实例卡片上的「取消倒计时」按钮 | 批量场景下从「终止整批」变成「只放过这一台」。若原有确认文案暗示了整批终止，需要改写 |
| `BatchOperationDialog.vue` 的实例列表 | 被取消倒计时的实例会以 `cancelled` 状态 + 「倒计时被取消」出现，与用户主动 skip 的 `skipped` 并列。若前端只渲染了 skipped，需补上 cancelled 的展示 |
| 整批终止 | 仍然只有批量弹窗上的「取消批量操作」按钮能做到 |

`InstanceCancelled` 这个状态值本来就已存在于 `batchmanage`（用于 ctx 取消时标记剩余实例），
前端若已处理过它，则这一项无需改动——落地前确认一下 `BatchOperationDialog.vue` 的状态映射即可。

---

## 7. 重构收益核对表

| 项 | 重构前 | 重构后 |
|---|---|---|
| 秒 → Config 的转换实现 | 3 份 | 1 份（`FromSeconds`） |
| RCON 连接实现 | 2 份（`instance` / `realtime`） | 1 份（`rconx`） |
| `github.com/gorcon/rcon` 导入点 | 2 个包 | 1 个包 |
| 登记表注册/释放配对 | 调用方 `defer`，可漏 | `Wait` 内部保证 |
| 同实例倒计时重入 | 静默覆盖，前一个取消不掉 | 返回 `ErrInProgress` |
| 倒计时被取消后的状态回滚 | 只在 `instance` 单实例路径 | 统一在 `countdown.Stop/Restart` |
| `instance` → `realtime` 依赖 | 有 | 无 |
| 倒计时的 ctx | `context.Background()`，进程退出不收敛 | 透传 `serverCtx` / `op.ctx` |
| 批量中取消单个实例 | 隐式终止整批 | 只跳过该实例，复用已有 skip 机制 |
| skip 通道的 close | 只有 `SkipInstance` 一条路径 | 两条路径统一走 `signalSkip` + `sync.Once` |
| `batchmanage.runCountdownPhase` | 45 行含手写并发原语 | ~25 行，无并发原语 |

---

## 8. 风险与回滚

- **风险 1：类型改名波及面广。** `CountdownConfig` 在 5 个包出现。
  建议 Step 3 一次性改完并立刻编译，不要分批留半天。
- **风险 2（最高）：§4.2 的单实例取消是行为变更，必须有测试兜底。**
  `countdown/run_test.go` 至少覆盖四条：
  1. 3 个实例，取消其中 1 个 → `Result.Cancelled` 恰好 1 个，另 2 个正常走完，`error` 为 nil
  2. 父 ctx 取消 → 返回 error，且**全部**实例的登记都被释放（不留残余）
  3. 全部实例逐个取消 → `AllCancelled()` 为真、`error` 仍为 nil
  4. 播报（RCON）失败只记 WARN、不中断倒计时（保持现有行为）

  `batchmanage` 侧另需一条：倒计时期间对同一实例先 `SkipInstance` 再 `Cancel`，
  确认不 panic（双重 close 防护，§4.2）——建议用 `-race` 跑。
- **风险 3：`countdown.Stop` 里 `release(name)` 的时机。** 原代码是
  `defer FinishCountdown(name)` 在 `StopServer` 全程有效，登记表在停止完成后才清；
  新实现必须保持一致，否则 executing 阶段前端会提前丢掉「服务器关闭中…」。
- **风险 4：`Wait` 的子 ctx 泄漏。** 每个实例一个 `context.WithCancel`，
  正常走完的实例也必须 `cancel()`（`go vet` 的 lostcancel 能抓到一部分，但登记表里
  存了 cancel 函数，vet 看不出来）——释放路径统一放在 `Wait` 的 defer 里逐个 release。
- **回滚**：每个 Step 都是独立 commit，出问题按 Step 逆序 revert 即可。
  HTTP/WS 契约不变，回滚不需要同步回滚前端；但 §4.2 是行为变更，
  回滚后「取消 = 整批终止」会随之复原，需同步告知用户。

---

## 9. 实施记录：本文之外的一处修正

§8 的风险 4 原本把释放路径写成「统一放在 `Wait` 的 defer 里逐个 release」。
照此实现会引入一个新的串扰缺陷，实施时已改掉：

`runOne` 在实例已被**另一轮**倒计时占用时返回 `ErrInProgress`，此时登记表里那条记录
属于那一轮。若 `Wait` 的每个 goroutine 无条件 `defer release(name)`，就会把别人的
`cancel` 函数一并删掉——那一轮倒计时从此再也取消不了，正是本次重构要消灭的问题
（§1.2 (d)）换了个位置重新出现。

实际实现改为按结果分流：

- `runOne` 返回错误 → **不碰登记表**（被取消时 `runOne` 自己已清理；`ErrInProgress` 时登记不属于本轮）
- `runOne` 返回 nil → 由 `Wait` 释放（阶段二不该被这一轮继续占着）

`Stop` / `Restart` 同理：只在 `cfg.Enabled()`（即本轮真的登记过）时才 `defer release`。

回归测试：`countdown/run_test.go` 的 `TestWaitDoesNotReleaseForeignRegistration` ——
预置一条外部登记，跑一轮 `Wait`，断言该实例以 `ErrInProgress` 落在 `Cancelled` 中，
且外部登记仍在、仍可取消。

### 测试现状

- `go test ./countdown/...` 全绿；耗时最长的 `TestWaitCancelOneInstanceContinuesOthers`
  要跑满 `MinTotal`(30s)，已加 `testing.Short()` 跳过。
- `-race` 在本机跑不起来（cgo 缺 gcc），§8 风险 2 里那条 `-race` 验证**尚未执行**，
  留待有 gcc 的环境补跑。
- 既有的环境依赖测试仍然失败，与本次改动无关：
  `instance.Test_SaveWorldSafely`（需要活着的 `ces99` 实例）、
  `instance.Test_GetAllInstanceNames`（需要指定路径的 BadgerDB）、
  `pkg/serverinfo.TestGetProcessInfo`（硬编码 PID）、
  `pkg/winproc.Test_GetPIDByPort`、`pkg/tail`（等文件超时）。
