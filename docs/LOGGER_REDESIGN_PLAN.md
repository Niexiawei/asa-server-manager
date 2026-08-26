# logger 包重构方案

> 状态：**✅ 已实施**。`pkg/logger/logger.go` + `pkg/logger/logger_test.go`（7 个真实单测，
> 覆盖 §6 全部场景，含 §3 的 JSON 键名回归测试），全项目 50 个业务文件的 `GetLogger()`/
> `GetStdout()`/`SetLogMode` 已按 §5 表格机械替换完毕。额外顺手清理：`internal/mirror`/
> `internal/plugindata`/`internal/batchmanage`/`internal/schedule`/`internal/countdown`/
> `internal/frpmanage` 六个测试文件里"为了避开 GetLogger() 未初始化时 nil 导致 panic"而写的
> `TestMain` 日志初始化 workaround——新设计的包级 init() 自带一个不落盘的纯控制台兜底 logger，
> 这类 workaround 存在的前提已经不成立，能删的都删了（`internal/countdown` 的 TestMain 里还有
> 一条无关的 `isAlive` 测试桩，保留了那一半）。`go build`/`go vet` 两平台通过，
> `go test`（排除已知会挂起的 `pkg/tail` 与已有先例排除的 `internal/frpmanage`）仅剩 3 个
> 与本次改动无关、此前就存在的环境耦合失败（硬编码作者本机路径/PID）。真实运行验证：
> `asa-server cert status` 前台跑通、`asa-server api` 起服务后 `{BaseDir}/logs/asaServer.log`
> 落盘且 JSON 键名（`ts`/`level`/`msg`）与 `SystemLogs.vue` 期望的一致。
>
> 修订记录：
> - v2 —— 用户明确要求直接移除 `GetLogger()`/`GetStdout()` 这层无意义的取实例动作，
>   改为包级函数直接调用（`logger.Infof(...)`），并新增可链式调用的 `WithConsole()`；`With`/`Named`
>   也要作为包级函数暴露。这比 v1 稿的「只重写包内部、不碰调用点」范围更大——现在**全部约 250
>   处调用点都要机械改写**，但改写本身是纯文本替换（去掉 `.GetLogger()` 这一段），参数不变，
>   风险仍然可控。v1 稿的问题清单与前端兼容性硬约束依然成立，本次原样保留，设计与调用点章节整体重写。
> - v3 —— 用户明确要求把包从 `internal/logger` 挪到 `pkg/logger`：它不认识任何领域概念
>   （不 import 任何 `internal/<领域包>`，现有依赖只有 `os`/`path/filepath`/zap 生态），
>   也没有外部代码直接读它的包级变量（`logger.BaseDir` 全项目 0 处引用），符合
>   `docs/INTERNAL_LAYOUT_MIGRATION.md` §9 定的 `pkg/` 三条准入标准。它确实有包级全局状态
>   （当前日志级别、底层 writer），但这与 `pkg/download` 的 `Configure()` 是同一性质——
>   一次性写全局配置，不是业务生命周期状态，`pkg/download` 已经是这个先例，不算破例。
>   导入路径从 `asa-server/internal/logger` 全部改成 `asa-server/pkg/logger`，见 §5.1。

## 1. 背景

当前 `pkg/logger` 的输出「有点混乱」：两个互相独立的 logger 实例、一段从不生效的死状态、
Debug 级别永远打不出来、级别不能动态调。用户看过 v1 稿后明确了最终形态：

- **不要 `GetLogger()`/`GetStdout()` 这层「先取实例再调方法」的中间动作**。日志直接以包级函数调用：
  `logger.Infof("...", x)` / `logger.Info("...")`（对齐 `*zap.SugaredLogger` 现有的两套方法——
  `Xxx(args...)` 是 fmt.Sprint 风格，`Xxxf(template, args...)` 是 fmt.Sprintf 风格，本项目现在
  两种都在用，都要保留）。
- **需要在控制台可见时，链式调用 `logger.WithConsole().Infof(...)`**。不用 `WithConsole()` 的
  普通调用只进文件，不上屏。
- **文件写入始终按配置的级别把关**：调用的级别达到配置阈值就写文件，跟有没有链 `WithConsole()`
  无关——`WithConsole()` 只决定「这条要不要额外上屏」，不影响「够不够级别写文件」这件事。
- **`With`/`Named` 也要作为包级函数暴露**（`logger.With(...)`/`logger.Named(...)`），用于派生
  带固定字段/命名空间的子 logger，和 `WithConsole()` 一样可以链式组合。

设计仍然参考 `D:\webProject\new-grassroots-governance\user-backend-api\pkg\logger` 的思路（多路
sink、可动态调级别、初始化前的 fallback logger、`defaultLogger`/`pkgLogger` 两份实例做 caller skip
换算），但**不**照搬其 `Logger` 接口的结构化签名（`Info(msg string, fields ...zap.Field)`）——
本项目要保留 printf 风格，接口签名照抄 `*zap.SugaredLogger` 的方法形状，而不是参考包的。

## 2. 现状问题清单（都是看代码看出来的事实，不是猜测）

| # | 问题 | 证据 |
|---|---|---|
| 1 | **`GetLogger()`/`GetStdout()` 取实例这层封装没有意义** | 两个函数只是返回包级变量，每个调用点都要多写一次 `logger.GetLogger().`；本次直接删掉，改成包级函数 |
| 2 | **两个互相独立的 logger 实例，调用方要记住用哪个** | `logger`（`GetLogger()`）只有 Warn+ 才上控制台，Info 只进文件；`loggerStdout`（`GetStdout()`）是完全独立的第二个 zap 实例，Info+ 全上屏但**不写文件**。`internal/webapi/actions.go` 有 3 处专门改用 `GetStdout()`，就是因为 `GetLogger()` 的 Info 在交互式运行时屏幕上看不见——这正是 `WithConsole()` 要顶替的场景 |
| 3 | **`LogMode`/`SetLogMode`/`GetLogMode` 是死状态** | `loggerMode` 全项目只在 2 处被 `SetLogMode` 写入（`main.go` 服务模式、`webapi/actions.go` HTTP API 模式），`GetLogMode()` 全项目**没有任何地方读取**——设置了但从不影响任何行为 |
| 4 | **`fileWriterByPath` 是未使用的死代码** | 定义在 `logger.go:113`，全项目 0 处调用 |
| 5 | **Debug 级别的日志在生产环境下 100% 被丢弃，且无法调整** | 现有三个 core（`logger` 的控制台核/文件核、`loggerStdout` 的核）没有一个允许 `DebugLevel` 通过；全项目所有 `.Debugf(...)` 调用目前都是无操作，且没有开关能打开 |
| 6 | **级别阈值硬编码，不能运行时调整，也没有配置入口** | 想临时开 Debug 排查问题，只能改代码重新编译 |
| 7 | **没有人调用 `Sync()`** | 当前用的是无缓冲 `os.Stdout` + lumberjack 逐行同步写，实践中不丢数据，但不是能长期依赖的正确姿势 |

**不在本次改造范围内**（明确排除，避免范围蔓延）：

- `internal/webapi/logapi` 转发的 `arkApiLog.log`（ARK/AsaApiLoader 的原始控制台输出，走
  `console.CleanScreenOutput` 直接写文件，完全不经过 `pkg/logger`）——游戏进程输出转录，
  不是本项目自己的结构化日志，不动。
- 各实例自己的 `server.log`（`pkg/tail` 读的是 ARK 自己写的日志文件）——同上，不属于 `pkg/logger`。
- `schedule_logs.json`（定时任务执行记录，走 `internal/schedule/runlog.go` 自己的 JSON 状态文件，
  不是 zap 日志）——不动。

## 3. 硬约束：前端对文件日志格式有隐性依赖

`internal/webapi/logapi.go` 的 `GET /api/logs`（系统日志 SSE）把 `asaServer.log` 的每一行原样透传给
前端，`app/src/views/SystemLogs.vue:82-87` 对每一行做 `JSON.parse(logStr)`，直接读取
**`log.ts`（时间）、`log.level`（级别）、`log.msg`（消息）** 三个字段名——这是 zap
`NewProductionEncoderConfig()` 的**默认**键名。

> ⚠️ 新设计的文件编码必须继续使用这三个默认键名，不能像参考包那样把 `TimeKey` 改成 `"time"`。
> 这个 bug 只有打开系统日志页面才会发现，代码走查看不出来，必须列入测试计划。

## 4. 新设计

### 4.1 `Logger` 接口（对齐 `*zap.SugaredLogger` 的方法形状，不是参考包的结构化签名）

```go
type Logger interface {
    Debug(args ...any)
    Debugf(template string, args ...any)
    Info(args ...any)
    Infof(template string, args ...any)
    Warn(args ...any)
    Warnf(template string, args ...any)
    Error(args ...any)
    Errorf(template string, args ...any)
    Panic(args ...any)
    Panicf(template string, args ...any)
    Fatal(args ...any)
    Fatalf(template string, args ...any)

    // With 派生一个带固定字段的子 logger，签名对齐 SugaredLogger.With
    // （接受交替 key-value，或直接传 zap.Field）。
    With(args ...any) Logger

    // Named 派生一个带命名空间前缀的子 logger（体现在 JSON 的 logger 字段里）。
    Named(name string) Logger

    // WithConsole 派生一个「同时上屏」的子 logger：返回值调用 Info/Warn/... 时，
    // 除了照常按级别写文件之外，无条件在控制台再打一份（不受文件级别阈值影响——
    // 显式调用 WithConsole 就是在说「这条我要让人当场看见」）。
    // 不调用 WithConsole 的普通 Logger 只写文件，不上屏。
    WithConsole() Logger

    Sync() error
}
```

`With`/`Named`/`WithConsole` 都返回新的 `Logger`值，不修改调用者自身——可以任意顺序链式组合，
例如 `logger.Named("mirror").WithConsole().Warnf(...)` 或
`logger.WithConsole().With("instance", name).Infof(...)`，语义都一样：文件按级别写，控制台上屏。

### 4.2 包级函数（唯一的调用入口，不再有 `GetLogger()`/`GetStdout()`）

```go
func Debug(args ...any)                        { pkgLogger.Debug(args...) }
func Debugf(template string, args ...any)      { pkgLogger.Debugf(template, args...) }
func Info(args ...any)                         { pkgLogger.Info(args...) }
func Infof(template string, args ...any)       { pkgLogger.Infof(template, args...) }
func Warn(args ...any)                         { pkgLogger.Warn(args...) }
func Warnf(template string, args ...any)       { pkgLogger.Warnf(template, args...) }
func Error(args ...any)                        { pkgLogger.Error(args...) }
func Errorf(template string, args ...any)      { pkgLogger.Errorf(template, args...) }
func Panic(args ...any)                        { pkgLogger.Panic(args...) }
func Panicf(template string, args ...any)      { pkgLogger.Panicf(template, args...) }
func Fatal(args ...any)                        { pkgLogger.Fatal(args...) }
func Fatalf(template string, args ...any)      { pkgLogger.Fatalf(template, args...) }

func With(args ...any) Logger  { return defaultLogger.With(args...) }
func Named(name string) Logger { return defaultLogger.Named(name) }
func WithConsole() Logger      { return defaultLogger.WithConsole() }

func Sync() error         { return defaultLogger.Sync() }
func SetLevel(level string) // 运行时调整文件（与「上屏」共用的同一个）级别阈值，见 §4.4

func InitLoggerWithBaseDir(baseDir string, opts ...Option)
func GetLogFilePath() string // 保留，logapi.go 用；这个函数本身有意义，不在移除范围内
```

`defaultLogger` 与 `pkgLogger` 两份实例的区分和参考包的 `L()`/包级函数完全一致：
`logger.Named("x")`/`logger.With(...)`/`logger.WithConsole()` 这三个包级函数返回值之后由**用户
直接调用返回值的方法**（如 `logger.Named("x").Infof(...)`），中间没有再经过一层包级函数包装，
caller skip 用 `defaultLogger` 的基准值即可；而 `logger.Infof(...)` 这种直接调用本身就是一层包级
函数包装，比前者多一级调用栈，所以 `pkgLogger` 要在 `defaultLogger` 基础上再 `AddCallerSkip(1)`——
这样不管走哪条路径，日志里的 caller 文件:行号都精确指向真正的调用处，不会指向 `logger` 包自己。

### 4.3 底层实现：两棵 zap.Logger 树，靠 `WithConsole()` 切换

`Logger` 的具体实现类型（`*sugaredLogger`）内部持有两个 `*zap.SugaredLogger`：

```go
type sugaredLogger struct {
    active  *zap.SugaredLogger // 当前生效的一个：默认是「只文件」
    console *zap.SugaredLogger // 「文件 + 控制台」的等价版本，WithConsole() 切过去
}
```

- `active` 只挂文件 core，受 `levelAtomic`（见 §4.4）约束。
- `console` 是 `zapcore.NewTee(fileCore, consoleCore)`：`fileCore` 与 `active` 共享同一个
  `levelAtomic` 和同一个 lumberjack WriteSyncer（不会写两份文件）；`consoleCore` **没有级别下限**
  （`zapcore.DebugLevel`，即来什么打什么）——用户显式调用 `WithConsole()` 就是要求这条一定可见，
  不应该被文件那份配置的级别阈值连带过滤掉。
- `Debug/Info/.../Fatal(f)` 方法体统一转发到 `z.active.Xxx(...)`。
- `With(args...)`：`return &sugaredLogger{active: z.active.With(args...), console: z.console.With(args...)}`。
- `Named(name)`：同上模式，两边都要 `Named`，保证不管之后有没有再链 `WithConsole()`，命名空间都在。
- `WithConsole()`：`return &sugaredLogger{active: z.console, console: z.console}`——把 `active`
  换成 console 版本；`console` 字段保持自引用，重复调用 `WithConsole().WithConsole()` 是幂等的。
- `Sync()`：`active`与`console`各 `Sync()` 一次，`os.Stdout` 在部分 Windows 控制台环境下的
  已知误报（`sync /dev/stdout: The handle is invalid`）需要识别并吞掉，不能让它冒泡成错误。

### 4.4 级别：单一可动态调整的阈值，文件与「上屏」共用同一份判断

只有一个 `levelAtomic zap.AtomicLevel`（默认 `InfoLevel`，与现状行为一致，不隐式增加输出量）：

- 决定**文件**是否写入：调用级别 ≥ `levelAtomic` 才落盘，不管有没有 `WithConsole()`。
- **不**决定控制台是否显示：控制台可见性完全由「有没有链 `WithConsole()`」决定，一旦链了就无条件
  上屏（见 §4.3 的 `consoleCore` 无下限设计）。这是对用户原话「需要打印到控制台就
  `WithConsole().Infof()`，当设置的日志级别大于配置那就同时记录到文件日志中」最直接的实现：
  「打印到控制台」和「达到阈值写文件」是两件独立发生、可以同时成立的事，不是互斥的两条路径。

`SetLevel(level string)` 直接 `levelAtomic.SetLevel(...)`，非法字符串忽略、保留当前值。

### 4.5 构造选项（`Option`，仅用于 `InitLoggerWithBaseDir`，与 `Logger` 的链式方法是两回事）

```go
type Option func(*options)

// WithLogFileName 覆盖日志文件名，默认 "asaServer.log"（当前硬编码值，保留作默认值）
func WithLogFileName(name string) Option
```

初始级别不再单独开选项——直接默认 `InfoLevel`，需要别的值时启动后调 `SetLevel` 即可，没必要在
构造期和运行期各留一套入口。Rotation 参数（`MaxSize`/`MaxBackups`/`MaxAge`/`Compress`）继续硬编码
为当前值，不开放成选项，理由与 v1 稿相同：现状没人需要按环境调这几个数字。

## 5. 调用点迁移（全量机械替换，不是精简范围）

### 5.1 包搬家：`internal/logger` → `pkg/logger`

`git mv internal/logger pkg/logger`，包名不变（仍是 `package logger`），只是目录和导入路径变。
全项目所有 `import "asa-server/internal/logger"` 改成 `import "asa-server/pkg/logger"`——
这一步和下面的调用点替换是同一批次的机械修改，没有独立风险：包名没变，`logger.Xxx(...)` 的调用
写法不受导入路径影响，`goimports`/IDE 的批量改导入路径操作就能完成，不涉及逻辑。

`pkg/` 准入的三条标准（`docs/INTERNAL_LAYOUT_MIGRATION.md` §9）逐条核对：

| 标准 | 核对结果 |
|---|---|
| 不认识领域概念 | ✅ 日志只关心「往哪写、什么级别」，不知道「实例」「PID 文件」这些领域词 |
| 零领域依赖（不 import 任何 `internal/<领域包>`） | ✅ 现有依赖只有 `os`/`path/filepath`/`go.uber.org/zap`/`go.uber.org/zap/zapcore`/`gopkg.in/natefinch/lumberjack.v2`，0 处 import 任何 `internal/` 包 |
| 无全局状态与生命周期 | ⚠️ 有包级全局变量（当前级别、底层 writer），但那是「日志系统自己的运行参数」，不是「业务领域的生命周期状态」——与已经在 `pkg/download` 里的 `Configure()`（一次性写全局 `http.Client`）同一性质，不算破例，`pkg/download` 已是先例 |

### 5.2 调用点替换

**替换规则**（纯文本替换，参数不变，`go build` 直接验证有没有漏改）：

| 旧写法 | 新写法 |
|---|---|
| `logger.GetLogger().Infof(...)` | `logger.Infof(...)` |
| `logger.GetLogger().Info(...)` | `logger.Info(...)` |
| `logger.GetLogger().Warnf(...)` | `logger.Warnf(...)` |
| `logger.GetLogger().Warn(...)` | `logger.Warn(...)` |
| `logger.GetLogger().Errorf(...)` | `logger.Errorf(...)` |
| `logger.GetLogger().Error(...)` | `logger.Error(...)` |
| `logger.GetLogger().Debugf(...)` | `logger.Debugf(...)` |
| `logger.GetStdout().Infof(...)`（`webapi/actions.go` 3 处） | `logger.WithConsole().Infof(...)` |
| `logger.GetStdout().Errorf(...)` | `logger.WithConsole().Errorf(...)` |
| `logger.GetStdout().Warn(...)` | `logger.WithConsole().Warn(...)` |
| `logger.SetLogMode(...)`（`main.go`、`webapi/actions.go` 各 1 处） | 整行删除 |

涉及文件：`pkg/logger/logger.go`（核心重写）+ 约 50 个业务文件（纯替换 `.GetLogger()`/
`.GetStdout()` 片段，逻辑与参数零改动）。全部替换完成后 `go build ./...` 能保证没有遗漏——旧的
`GetLogger`/`GetStdout`/`SetLogMode`/`GetLogMode` 符号在新版 `logger.go` 里不再存在，任何漏改的
调用点都会在编译期报 `undefined` 而不是运行时才发现。

## 6. 测试计划

- `pkg/logger` 新增单测：
  - `WithConsole()` 派生的 logger 写一条 Debug 级消息（低于默认 Info 阈值），断言控制台侧收到、
    文件侧没收到——验证 §4.4「上屏不受文件级别阈值影响」这条核心语义
  - 不带 `WithConsole()` 的普通调用，Info 级消息只进文件、不上屏
  - `SetLevel("debug")` 后，之前会被拦截的 Debug 级消息能正常写入文件
  - `With(...)`/`Named(...)` 派生的 logger 在**先 `WithConsole()` 再 `With()`** 和
    **先 `With()` 再 `WithConsole()`** 两种链式顺序下，字段与上屏行为都符合预期（覆盖 §4.3
    "两边都要同步派生" 这条容易漏的实现细节）
  - **回归测试（最重要）**：写一条日志到临时文件，读回内容，断言 JSON 里存在 `ts`/`level`/`msg`
    三个默认键名，对应 §3 的硬约束
- 全量替换后：`go build ./...`（两平台）零 `undefined` 错误即视为迁移完整；
  `internal/webapi/actions.go` 用到 `WithConsole()` 的 3 处手动跑一次 `asa-server api` 确认
  仍然出现在控制台

## 7. 实施步骤

1. `git mv internal/logger pkg/logger`（§5.1）
2. 重写 `pkg/logger/logger.go`（`Logger` 接口、`sugaredLogger` 双树实现、包级函数、
   `SetLevel`/`Sync`/`Option`），含 §6 单测
3. 全项目机械替换：导入路径 `internal/logger`→`pkg/logger` + 调用点替换（§5.2 表格），
   逐包替换逐包编译，避免一次性改 50 个文件却在中途某处编译失败时难以定位是哪一处漏改
4. 删除 `LogMode`/`GetStdout`/`fileWriterByPath` 相关代码确认无残留引用
5. `go build`/`go vet` 两平台通过，`go test ./...`（含新增的 `pkg/logger` 测试）通过
6. 手动验证：`asa-server api` 前台运行，确认 `WithConsole()` 相关的 3 条消息出现在控制台；
   打开前端「系统日志」页面确认时间/级别/消息列正常渲染（验证 §3 硬约束没有被破坏）

## 8. 风险与回滚

- 风险主要在"面广"而不是"复杂"：约 50 个文件、250+ 处需要改动，但每一处都是同构的纯文本替换，
  参数原样不动，编译器能穷尽发现遗漏（旧符号被整体删除，漏改必定是编译错误，不会是静默的运行时
  行为差异）。按包拆分成多次小提交（§7 步骤 2）可以进一步降低单次改动的排查成本。
- 真正的语义风险只有两处，都已经在测试计划里：①`WithConsole()` 上屏是否真的不受文件级别阈值
  影响（§4.3/§4.4 的核心设计意图）；②§3 的 JSON 键名兼容性。
- 回滚方式：`pkg/logger/logger.go` 是独立文件，业务文件的改动是可逆的纯文本替换
  （`logger.Infof(` 加回 `logger.GetLogger().Infof(` 即可逐个还原），必要时按 §7 步骤 2 的
  分包提交逐个 `git revert`。
