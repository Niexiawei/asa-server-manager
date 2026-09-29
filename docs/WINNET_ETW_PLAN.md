# Windows 实例级网络监控（ETW）—— `pkg/winnetetw`（合并文档）

> 本文由 `WINNET_ETW_PLAN.md`（方案）与 `WINNET_ETW_TODO.md`（迭代清单，活动文档）于 2026-09-29 物理合并而成（方案甲，逐字保留）。
> Part 1 是集成方案（含实现记录与 procnet 门面委托详设）；Part 2 是迭代清单（待修缺陷、回填项、真机验收与冒烟程序）。
> ⚠️ 本文写于 `internal/` 迁移之前的部分路径若已变化，见文末「附录 Y」。

## 本文内待办索引

> 编号 `§2.x` 行取自 Part 2 §2「待修缺陷（按严重程度排）」；编号 `#N` 行取自 Part 2 §5「迭代检查表」中未被 §2 覆盖的条目。状态取自 §5 检查表与各节自身结论。**本表只作索引与交叉引用，条目的原文见 Part 2。**

| 编号 | 标题 | 状态 |
|---|---|---|
| §2.1 | 【阻断】回调线程上的无锁 map 读 → 整进程 fatal | ✅ 已修（检查表 #1、#2） |
| §2.2 | 【高】会话中途死掉时无法表达「采不到」，曲线会变成恒 0 直线 | ✅ 已修（检查表 #3） |
| §2.3 | 【中】`CloseTrace` 的正常返回码被当成错误，每次停止都会假报警 | ✅ 已修（检查表 #4） |
| §2.4 | 【中】残留清理路径复用了被 `ControlTraceW` 改写过的 properties 缓冲 | ✅ 已修（检查表 #5） |
| §2.6 | 【高】`Close()` 把 `c.sess` 置 nil，与采样器的 `Bytes` 抢同一个字段 | ✅ 已修（检查表 #5b） |
| §2.7 | 【中】「查不到该 session」的错误码写错了：是 4201 不是 4200 | ✅ 已修（检查表 #5c） |
| §2.8 | 【中】普通用户的真实降级点不是 `StartTraceW`，是 `EnableTraceEx2` | ✅ 已修（检查表 #5d） |
| §2.9 | 【高】`Load` 失败会泄漏一个系统级 ETW session | ✅ 已修（检查表 #5e） |
| §2.10 | 【阻断】`ETW_BUFFER_CONTEXT` 是 4 字节不是 2，payload 长度一直读的是别的字段 | ✅ 已修（真机首轮定位） |
| §2.11 | 【阻断】`ProcessTrace` 的返回码被丢掉，消费侧启动即退出时毫无线索 | 诊断已加，根因见 §2.14 |
| §2.12 | 【阻断】TDH 的三个调用约定全写错了，其中一个把进程直接打崩 | ✅ 已修（有测试兜住） |
| §2.13 | 【订正】`EVENT_TRACE_LOGFILEW` 的生命周期：是个真 bug，但**不是**会话早退的原因 | 订正（成因见 §2.14） |
| §2.14 | 【根因】`EVENT_TRACE_CONTROL_QUERY` 与 `STOP` 写反了 —— 前面好几条结论都是它的假象 | ✅ 已修（`TestControlCodeSemantics`） |
| §2.5 | 【观察项，不阻断】`stop()` 会写正被 `ProcessTrace` 持有的句柄字段 | 观察项，不阻断（未列检查表） |
| #6 | 丢事件计数改用 QUERY 的偏差回填 PLAN §13（§3） | ✅（文档项） |
| #7 | PLAN §9 本期项 #1/2/10 真机跑通（**需管理员终端**） | ☐ 本机未提权，冒烟程序已备好 |
| #7b | #7/#8 session 生命周期与残留清理 | ✅ 非提权下已等价验证 |
| #8 | **#3：ARK 实例 UDP 双向核对**（§4，决定性） | ☐ 执行方式见 `docs/NETMON_CLI_AND_ETW_WIRING_PLAN.md` N3 |
| #9 | T1：`procnet_windows.go` stub 改委托（PLAN §14） | ✅ 2026-09-07 已接 |

---

# Part 1 — Windows 实例级网络监控（ETW）集成方案 —— `pkg/winnetetw`（原 `WINNET_ETW_PLAN.md`）

# Windows 实例级网络监控（ETW）集成方案 —— `pkg/winnetetw`

> 状态：**本期已实现**（2026-09-06，W1–W4：包内代码 + 单测 + 双平台构建验证完成；  
> 真机冒烟验收见 §13 实现记录，#1/2/7/8/10 待真机跑）。  
> **本期范围：仅 `pkg/winnetetw` 包内实现（W1–W4），外部调用与接线不做**；  
> 后续由 `pkg/procnet` 作为统一门面透出（§5 概要 / **§14 详设**），  
> `internal/webapi` 接线一行不改；不经门面的独立使用见 **§15**。  
> **活动清单在 `docs/WINNET_ETW_PLAN.md` Part 2`**（评审发现的待修缺陷与验收阻塞项集中在那里；  
> 本文档是只增不改的档案——新缺陷加进 TODO，结论回填 PLAN）。  
> 上游设计文档：`C:\Users\niexiawei\Downloads\windows-go-process-network-etw-design.md`  
> （下称「ETW 设计文档」；本方案裁剪其范围后映射到本项目的既有架构）。  
> 关联方案：`docs/RESOURCE_RATE_CHART_PLAN.md`（P1–P7 已落地；其 §2.2 / §3.3 /  
> §11.4 中「Windows 实例级网络恒为 null」的限制由本方案补齐）。  
> 关联代码：
>
> - 接口：`pkg/serverinfo/netsource.go`（`NetSource`）、`pkg/serverinfo/sampler.go`（`sampleProcLocked` 消费侧）
> - 门面与同构参照：`pkg/procnet/`（统一对外 API，本包照它的形状做；其 `procnet_windows.go` 现为 stub，后续期改为委托 winnetetw）
> - 接线（不改）：`internal/webapi/procnet.go`（组合根模式）、`internal/webapi/actions.go`

---

## 1. 背景与目标

资源趋势图（RESOURCE_RATE_CHART_PLAN）P7 已在 Linux 上用 eBPF（`pkg/procnet`）实现按进程网络计量；  
但 **Windows 是本项目的主平台**，`instances[].net_io` 在 Windows 上恒为 `null`，  
实例详情页的「网络进/出速度」图只显示「当前平台不支持按进程网络计量」占位。

本方案新增 `pkg/winnetetw`，用 **ETW（`Microsoft-Windows-Kernel-Network` provider）**  
在 Windows 上提供与 eBPF 完全等价的数据：**按 PID 的累计网络收发字节**。

目标一句话：**让 Windows 上 `instances[].net_io` 有值，且采样器、SSE 载荷、前端一行不改**。

落地分两期：

- **本期（W1–W4）**：`pkg/winnetetw` 包内实现（会话、解析、聚合、`Load/Bytes/Close/Describe`），  
  以临时冒烟程序在真机上自验；**不改 `pkg/procnet`、不改 `internal/webapi`、不改前端**。
- **后续（T1–T3）**：`pkg/procnet` 的 `procnet_windows.go` 从 stub 改为委托 `winnetetw`（§5），  
  组合根与 `actions.go` 零改动地获得 ETW 能力，再做端到端验收与文档同步。

非目标（明确不做）：

- 连接列表（`GetExtendedTcpTable` / `GetExtendedUdpTable`）——ETW 设计文档 §8–§9 的功能二。  
  本项目当前没有按进程连接列表的 UI/API 消费方，留作将来独立事项。
- 进程名缓存（ETW 设计文档 §7）——实例名与进程名由采样器经 gopsutil 给出，不需要 ETW 侧再查一遍。
- 网卡级流量（`GetIfTable2`）——宿主机网络已有 `net.IOCounters`，语义还更对齐。
- 本期不做对外接线 / 透出（见上）。

---

## 2. 集成定位：适配 `NetSource`，不新增任何上层概念


### 2.1 集成路径：经 `pkg/procnet` 统一门面

P7 的架构决策已经把按进程网络计量抽象成了接口注入；本方案在其下再加一层  
「`pkg/procnet` 是唯一对外门面」的约定——`internal/webapi` **只 import `pkg/procnet`**，  
永远不感知 `winnetetw` 的存在：

```text
internal/webapi/procnet.go（组合根，只 import pkg/procnet，一行不改）
   ↓ startProcNet() → procnet.Load(...)
pkg/procnet                     ← 统一门面（全平台编译）
   ├─ procnet_linux.go    （linux/amd64：eBPF 实现）
   ├─ procnet_windows.go  （windows：后续期改为委托 ↓）
   │        └──→ pkg/winnetetw（ETW，整包 windows-only，本期新增）
   └─ procnet_other.go    （stub，ErrUnsupported）
   ↓ procnet.Collector 满足 serverinfo.NetSource
serverinfo.SetNetSource() 注入 → 采样器 2s 周期调 Bytes(pid)
   ↓
(cur - prev) / Δt → ProcRates.NetRx/NetTxBytesPS
   ↓
all-info SSE 载荷 instances[].net_io（契约不变）
   ↓
前端趋势图 / sparkline（渲染逻辑不变，null→有值 自动生效）
```

`NetSource` 的定义（`pkg/serverinfo/netsource.go:13`）：

```go
type NetSource interface {
    Bytes(pid int32) (rx, tx uint64, ok bool)
}
```

因此 `pkg/winnetetw` 的全部职责就是：**提供一个满足该接口的 Windows 实现**。  
但它**不直接暴露给上层**——透出统一走 `pkg/procnet`（§5）；本期连委托层也不动，只交付包本身。  
SSE 载荷、`metrics:` 历史持久化、`/api/server/metrics/history`、前端 `useResourceTrend.js`、  
`ResourceTrendPanel.vue`、首页 sparkline——全部零改动。后续期委托接通后，Windows 上数据一通，  
实例网络图自动从「占位」变成「曲线」。

### 2.2 语义对齐：与 `pkg/procnet` 的行为逐条对表

采样器对 `Bytes()` 的三个隐含契约（`sampler.go:401-412`）必须逐条满足：

| 契约                           | procnet（eBPF）的满足方式        | winnetetw 必须等价地满足                                     |
| ---------------------------- | ------------------------- | ----------------------------------------------------- |
| 返回**累计值**，速率由采样器差分           | BPF map 里是累计字节            | ETW 聚合 map 里存累计字节，**绝不在 ETW 层算 bytes/s**（ETW 设计文档 §5） |
| 首次问到某 PID：返回 0 基线，`ok=true`  | 先登记进 targets map，计数从 0 开始 | 首次问到时把 PID 加入跟踪集合，计数从登记时刻开始累计；下一轮差分恰好是这两轮之间的流量        |
| `ok=false` 表示「采不到」→ 该字段 null | 登记失败 / 读 map 失败           | 会话未启动 / PID 已被淘汰待重建等场景                                |

PID 复用的行为差异要写清楚（见 §4.6）——两边都**恰好正确**，原因不同。

### 2.3 范围裁剪声明（相对 ETW 设计文档）

上游文档是一个通用 Windows 网络监控 Agent 的完整设计（流量 + 连接列表 + 进程缓存）。  
本项目只取「功能一：进程网络流量」中 TCP/UDP RX/TX 四路计数；  
其目录结构建议（`internal/network/`）与最终 `Monitor` API（`TrafficAll` / `Connections` 等）  
**不采用**——本包照 `pkg/procnet` 的形状做，保持两个平台实现可对照。

---

## 3. 包设计

### 3.1 目录结构

```text
pkg/winnetetw/              # 包内所有文件一律 //go:build windows——Go 没有包级
│                           #   build tag，必须逐文件标注。非 Windows 平台上这个
│                           #   包整体不存在，Linux 构建的依赖图也不会拉它
├── winnetetw.go            # 包文档、Options、8 个 Event ID → (协议, 方向) 的映射表
├── collector.go            # Collector：聚合、tracked-set、Bytes/Close/Describe
├── etw_session.go          # ETW 会话生命周期（StartTrace/Enable/Open/Process/Close/Control）
├── etw_parse.go            # TDH 解析 + per-EventID schema 缓存
├── etw_syscall.go          # lazy DLL 声明与 EVENT_TRACE_PROPERTIES / EVENT_RECORD 等结构体
└── winnetetw_test.go       # 单测（全部 //go:build windows，Windows 开发机上跑）
```

文件名不带 `_windows` 后缀：整个包只有 Windows 一个构建目标，后缀没有信息量  
（`pkg/procnet` 里带后缀是因为同一目录下并存四个平台的文件）。  
比 `procnet` 拆得细（那边只有 `procnet_linux.go` 一个实现文件），因为 ETW 的  
syscall 声明 + 会话生命周期 + TDH 解析三者各自独立、合计预计 600–800 行，单文件放不下可读性。

### 3.2 API（与 `pkg/procnet` 逐一对齐）

```go
// winnetetw.go
type Options struct{} // 当前无可选项；为将来留位（如 ETW buffer 大小、flush 间隔）
```

```go
// collector.go
type Collector struct{ /* session、聚合 map、tracked-set、stats */ }

func Load(opts Options) (*Collector, error)
// Bytes 返回该 PID 的累计收发字节（自该 PID 被登记进跟踪集合起算），语义见 §2.2
func (c *Collector) Bytes(pid int32) (rx, tx uint64, ok bool)
func (c *Collector) Describe() string   // 一行日志：session 名、挂上的 Event ID 数、丢事件计数
func (c *Collector) Close() error
```

不导出 `Stats()` 单独方法——丢事件计数并进 `Describe()`，够用（procnet 同款风格）。

**不定义 `ErrUnsupported`**：那是门面概念（「这个平台没有实现」），属于 `pkg/procnet`  
（`procnet.go` 已有，Linux/其它平台由其 stub 返回）。`winnetetw` 在自己唯一的构建目标上  
要么成功、要么返回具体失败原因（权限 / session 冲突 / TDH 错误），由后续期的  
`procnet_windows.go` 委托时原样透传，上层统一按「失败即降级」处理。

### 3.3 依赖

- `golang.org/x/sys/windows`（go.mod 已有，v0.47.0）——**仅**用于类型与 `NewLazySystemDLL`；  
  `StartTraceW` / `EnableTraceEx2` / `OpenTraceW` / `ProcessTrace` / `CloseTrace` / `ControlTraceW` /  
  `TdhGetEventInformation` / `TdhGetProperty` 在 x/sys/windows 里**没有封装**，  
  需在 `etw_syscall.go` 里用 `windows.NewLazySystemDLL("advapi32.dll")` /  
  `("tdh.dll")` + `NewProc` 自行声明（ETW 设计文档 §2 的「只用 x/sys」精神即指此）。
- 不引入 `0xrawsec/golang-etw`、`bi-zone/etw`、CGO（ETW 设计文档 §2 已论证）。
- `CGO_ENABLED=0` 下可编译。

---

## 4. 技术设计（Windows 实现内部）


### 4.1 ETW 会话生命周期

照 ETW 设计文档 §12 的标准 Controller/Consumer 流程：

```text
Load
 │ StartTraceW（固定 session 名 "AsaServerProcNet"）
 │   └─ ERROR_ALREADY_EXISTS → 清理旧 session（§4.2）后重试一次
 │ EnableTraceEx2（provider GUID {7DD42A49-5329-4832-8DFD-43D979153A88}，
 │   附 Event ID 过滤器，见 §4.3）
 │ OpenTraceW（EVENT_RECORD_LOGFILE + EventRecordCallback + REAL_TIME 模式）
 │ ProcessTrace（独立 goroutine 阻塞消费；CloseTrace 后返回）
 ▼
Close
 │ CloseTrace（令 ProcessTrace 返回）
 │ 等消费 goroutine 退出
 │ ControlTraceW(EVENT_TRACE_CONTROL_STOP)（真正销毁 session）
 ▼
```

要点：

- **session 名固定**为 `AsaServerProcNet`（带项目前缀，避免与通用示例名撞车）。  
  绝不生成 `AsaServerProcNet-1/2/3` 这类带编号的实例（ETW 设计文档 §13）。
- `EVENT_TRACE_PROPERTIES` 的缓冲区布局：结构体后跟 logger name 与 session name  
  字符串，`LoggerNameOffset` / `LogFileNameOffset` 的偏移计算是这块最容易写错的地方，  
  用 `unsafe.Offsetof` + 手工拼 buffer，并留单测钉死偏移。
- 实时模式不带日志文件，`LogFileNameOffset` 指向**空字符串**（只填 `WCHAR(0)` 终止符），  
  这是官方文档允许且必须的写法。
- **ProcessTrace 的 callback 在 ETW 的原生线程上被调用**（经 `syscall.NewCallback` 进入 Go）：  
  callback 内**禁止**阻塞、禁止 panic、禁止任何可能死锁的锁操作；  
  只做「读 EVENT_RECORD → 查映射表 → 更新聚合 map」三件事（ETW 设计文档 §6）。

### 4.2 旧 session 清理（进程崩溃残留）

进程被强杀（`taskkill /F`、崩溃、断电）时 `ControlTraceW(STOP)` 不会执行，旧 session 留在系统里，  
下次 `StartTraceW` 返回 `ERROR_ALREADY_EXISTS`。处理流程：

```text
StartTraceW 返回 ERROR_ALREADY_EXISTS
 → ControlTraceW(AsaServerProcNet, QUERY)  确认是自己的残留（能查到就处理）
 → ControlTraceW(AsaServerProcNet, STOP)   销毁
 → 重试 StartTraceW 一次
仍失败 → 返回 error，上层降级（net_io 恒 null），不影响其它指标
```

ETW session 是有限系统资源，Start→Stop 生命周期必须完整（ETW 设计文档 §13）。  
验证用 `logman query -ets` 肉眼核对（§9 验收表里有这条）。

### 4.3 Provider 启用与 Event ID 过滤（数据量的第一道闸）

不收集整个 `Microsoft-Windows-Kernel-Network`，只启用 8 个 Event ID  
（ETW 设计文档 §3.2 / §14）：

| Event ID | 协议  | 地址族  | 方向 |
| -------: | --- | ---- | -- |
|       10 | TCP | IPv4 | TX |
|       11 | TCP | IPv4 | RX |
|       26 | TCP | IPv6 | TX |
|       27 | TCP | IPv6 | RX |
|       42 | UDP | IPv4 | TX |
|       43 | UDP | IPv4 | RX |
|       58 | UDP | IPv6 | TX |
|       59 | UDP | IPv6 | RX |

**过滤必须做在 `EnableTraceEx2` 里**（`ENABLE_TRACE_PARAMETERS` + `EVENT_FILTER_DESCRIPTOR`  
的 `EVENT_FILTER_TYPE_EVENT_ID` 类型，传 8 个 ID 的数组），让内核侧就不投递其余事件——  
这是唯一能在事件产生点之前削减数据量的手段。callback 里再按 Header.EventDescriptor.Id  
查一次映射表属于防御性二次过滤（查不到的 ID 直接返回）。

这张映射表是纯数据，放在 `winnetetw.go` 里（整包 windows-only 后单测只能在 Windows 上跑——  
开发机就是 Windows，无损失）。

### 4.4 TDH 解析与 schema 缓存

不硬编码 payload offset（ETW 设计文档 §15）。流程：

```text
EVENT_RECORD
 → 按 EventDescriptor.Id 查 schema 缓存（sync.Map / 预分配 8 格数组，一次会话至多 8 个 ID）
   命中 → 直接按缓存的属性名取值
   未命中 → TdhGetEventInformation 解析 TRACE_EVENT_INFO
          → 找到 PID 与 size 的属性（属性名随系统版本可能是 PID/Pid、size/Size，
             按 COUNT 为 1 的 UInt32 属性匹配，不按名字硬猜）
          → TdhGetProperty 取值并写缓存
 → 组出 (pid uint32, size uint32) → 查 §4.3 映射表 → 累加
```

TdhGetEventInformation 的缓冲区也要走「先探大小再分配」的两段式（返回  
`ERROR_INSUFFICIENT_BUFFER` 时按 needed size 重来）。


### 4.5 聚合与 tracked-set（镜像 procnet 的语义）

**与上游文档的偏差（有明确理由）**：ETW 设计文档 §17 推荐生产实现走  
「callback → non-blocking channel → aggregator goroutine」。本包**不用 channel**，  
直接在 callback 里持 `sync.Mutex` 更新 map。理由：

1. 单个 ETW session 的 `ProcessTrace` 回调是**串行**的（一个消费者线程），  
   不存在 callback 之间的竞争；唯一的并发读者是采样器每 2s 一次的 `Bytes()`——  
   mutex 竞争窗口可忽略。
2. channel 方案必须处理「channel 满载丢弃」，引入 dropped counter 与精度损失；  
   直接锁更新**零丢弃**、代码更短。上游推荐 channel 的前提是「callback 不能碰复杂锁」，  
   而这里的锁保护的是两个 map 的 get/add，纳秒级，不违反 §6 的性能原则。
3. 若真机压测（§9 后续期 #9）发现锁竞争，再升级为 channel + aggregator，接口不变。

数据结构：

```go
// 全部累计值（自登记起算），绝不存速率
type Collector struct {
    mu       sync.Mutex
    counters map[uint32]*netCounters // pid → rx/tx，只有被跟踪的 PID 会有条目
    seen     map[uint32]time.Time    // pid → 最近一次被 Bytes() 问到（TTL 用）
    // session handle、trace handle、schema 缓存、lostEvents 计数、close 相关字段
}
```

**tracked-set 语义（与 procnet 完全一致）**：

- `Bytes(pid)` 首次被问：登记 `seen` + 在 `counters` 建零值条目，返回 `(0, 0, true)`。  
  此前该 PID 的网络事件**不入账**（callback 查 `counters` 没有条目就丢弃）——  
  与 eBPF 的 targets map 先登记后计数语义对齐，`counters` 条目数被限死在  
  「被跟踪的实例数」，不会被系统全量进程撑爆（procnet 决策 27 的同一理由）。
- callback 对未登记 PID 的丢弃成本是一次 map miss（RLock），可忽略。
- **TTL 淘汰**：30s 没被 `Bytes()` 问到的 PID，`counters` + `seen` 一起删  
  （与 procnet 的 `targetTTL` 同值；采样器 2s 一问，30s = 15 轮容忍）。  
  淘汰扫描复用 procnet 的 `pruneInterval`（≥10s 一次）节奏，在 `Bytes()` 里顺带做。
- PID 0（System）事件直接忽略（ETW 设计文档 §30）。

### 4.6 PID 复用：为什么两边的表现恰好都正确

- eBPF 侧：内核 map 按 tgid 计数，PID 复用后新进程继承旧条目继续累加。
- ETW 侧（本包）：`counters[pid]` 同样是「该 PID 号码上的累计值」，复用后继续累加。

采样器（`sampler.go:340-366`）在 `CreateTime()` 变化时会**重建** `procState`  
（`hasPrevNet` 归零），下一帧重新建立 prev 基线。所以差分结果不受复用影响——  
计数器只增不减，`cur - prev` 恒为新进程在此期间的流量。**无需在 winnetetw 里做任何  
PID 复用检测**（ETW 设计文档 §7 / §31 的 ProcessInfo 缓存与 StartTime 记录因此不做）。

### 4.7 丢事件可观测性

ETW buffer overflow 时事件**静默丢失**，曲线会悄悄偏低。两个计数点：

- `EVENT_TRACE_LOGFILE.LogfileHeader.EventsLost`（BufferCallback 里读）；
- callback 侧统计 `eventsReceived`。

两者都只增不减，拼进 `Describe()` 输出（如 `已启用 8 个事件，收到 123456 事件，丢失 12`），  
procnet 组合根的启动日志（后续期接线后自动生效）与 `logger.Debugf` 周期性输出。  
不做告警联动（当前无消费方）。

### 4.8 权限与运行模式

实时消费 `Microsoft-Windows-Kernel-Network` 需要管理员或 Performance Log Users 组  
（ETW 设计文档 §25）。逐个运行模式核对：

| 模式                            | 进程身份        | 结果                                         |
| ----------------------------- | ----------- | ------------------------------------------ |
| Windows 服务（`service install`） | LocalSystem | ✅ 可用                                       |
| `api` 命令（管理员终端）               | 提权用户        | ✅ 可用                                       |
| `api` / GUI（普通终端）             | 普通用户        | ❌ `StartTraceW` `ERROR_ACCESS_DENIED` → 降级 |

降级路径与 procnet 完全一致：`Load` 返回 error → 上层记一行日志 →  
`net_io` 恒 null → **其它指标与主流程完全不受影响**。不需要 appconfig 配置项  
（对比 `linux.ebpf_btf_path`：那边是「可以配了就救回来」，这边是「权限就是不行」，  
没有可配置的余地）。

### 4.9 计数口径（写进包文档，防误读）

- `size` 是 **Windows 网络栈层面的进程数据量**，不等于网卡 wire bytes  
  （ETW 设计文档 §19）。与 Linux eBPF 的 socket 层计数口径大体对等，  
  两个平台的数字放一起看量级一致即可，不承诺逐字节相等。
- **回环流量计入**：本机浏览器连游戏服务器（127.0.0.1 / 同机直连）的流量会被算进实例。  
  Linux eBPF 同样如此（socket 层不分 loopback），两平台行为一致，不特殊处理。  
  > **2026-09-07 真机订正**：这条只对 **TCP** 成立。同机回环的 **UDP** 流量
  > Kernel-Network **不上报**（自测打 2 MiB 回环 UDP，四路分项里一个字节都没有；
  > 同一次运行里回环 TCP 16 MB、经真实网卡的 UDP 双向都正常）。
  > 对本项目无影响——要看的 ARK 流量走真实网卡。
  > 见 `docs/NETMON_CLI_AND_ETW_WIRING_PLAN.md` §11.6。
- 代理 / VPN / TUN 环境下进程流量与网卡流量会有系统性差值（ETW 设计文档 §20），  
  属预期，不修。

---

## 5. 透出设计（后续期，不在本期）

> 本章是概要；**完整实施细节（委托层全文、零改动清单、生命周期、验收）见 §14**；  
> 不经 procnet 的独立使用方式见 §15。

### 5.1 门面：`pkg/procnet` 统一透出

后续期的**全部**改动是 `pkg/procnet/procnet_windows.go` 一个文件——从 stub  
（现返回 `ErrUnsupported`）改为委托 `winnetetw`：

```go
//go:build windows

package procnet

import "asa-server/pkg/winnetetw"

// Load 在 Windows 上委托 winnetetw（ETW），API 形状与其镜像一致（§3.2）。
func Load(opts Options) (*Collector, error) {
    c, err := winnetetw.Load(winnetetw.Options{})
    if err != nil {
        return nil, err // 组合根按既有契约降级：net_io 恒 null，其它指标照常
    }
    return &Collector{inner: c}, nil
}

// Collector 包一层：Bytes/Describe/Close 转发 inner。
// 保持 procnet.Collector 满足 serverinfo.NetSource（组合根 SetNetSource(c)
// 编译通过即断言），不让 winnetetw 的类型穿透到 internal/。
```

委托层包一层 `Collector` 而非直接 `type Collector = winnetetw.Collector` 别名：  
门面的类型边界要握在 `procnet` 手里，将来 ETW 实现换掉（或加参数）不动上层。

### 5.2 组合根与 `actions.go` 零改动

`internal/webapi/procnet.go`（组合根）与 `internal/webapi/actions.go` 的  
`startProcNet()` / `stopProcNet()` **一行不改**：Windows 上 `procnet.Load` 从  
「stub 返回 `ErrUnsupported`」变成「委托 ETW」，启动日志自动从  
「实例级网络监控未启用」变为「已启用：…」，`SetNetSource` 注入、SSE 载荷、  
前端渲染链路原样生效。**这也是选择 procnet 做门面而不是让 webapi 直连 winnetetw  
的理由**：组合根不感知「有几家实现」，跨平台决策内聚在 procnet 的平台文件里。

### 5.3 依赖方向

```text
internal/webapi → pkg/procnet → pkg/winnetetw（仅 windows 构建图内存在）
```

- `pkg/winnetetw` **不** import `pkg/procnet`（单向依赖，无环）。
- **不** import `internal/**`（pkg 纯度，对照 `pkg/procx` 准入标准）。
- **不** import `pkg/serverinfo`（`NetSource` 满足性由 procnet 委托层隐含保证，  
  winnetetw 无需感知该接口的存在）。

---

## 6. 平台与构建

| 项           | 说明                                                                                                                   |
| ----------- | -------------------------------------------------------------------------------------------------------------------- |
| build tag   | **包内所有文件一律 `//go:build windows`**（Go 无包级 tag，逐文件标注）。非 Windows 平台该包整体不存在。跨平台概念（`ErrUnsupported`、门面）全部留在 `pkg/procnet` |
| Linux 构建可见性 | `GOOS=linux` 时 `pkg/winnetetw` 不参与编译，`go build ./...` 天然通过，无需任何排除动作                                                  |
| 架构          | 仅 amd64（沿用决策 22：项目整体只支持 amd64；ETW 代码本身与架构无关，arm64 万一将来支持无需改动）                                                        |
| CGO         | 不需要，`CGO_ENABLED=0` 可编译                                                                                              |
| 验证命令        | Windows：`go build ./...`、`go vet ./...`、`go test ./pkg/winnetetw/...`；Linux：`GOOS=linux go build ./...`（确认包被正确隔离）    |

---

## 7. 分阶段实施

### 本期（仅 `pkg/winnetetw` 包内实现，外部调用不做）

| 阶段     | 内容                                                                                                                                                | 交付物                                                           | 可独立验收                                                                         |
| ------ | ------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- | ----------------------------------------------------------------------------- |
| **W1** | 包骨架：`winnetetw.go`（文档 / `Options` / 8 事件映射表，`//go:build windows`）+ 映射表单测                                                                          | `pkg/winnetetw/` 首批文件                                         | Windows `go build` / `go test` 通过；`GOOS=linux go build ./...` 确认包被隔离          |
| **W2** | ETW 会话层：`etw_syscall.go`（DLL 声明 + 结构体 + properties 偏移单测）、`etw_session.go`（Start/Enable(含 Event ID 过滤器)/Open/Process/Close/Control + 旧 session 清理） | 会话能起能停，`logman query -ets` 可见 `AsaServerProcNet`；崩溃残留可被下次启动清掉 | 管理员终端跑临时冒烟程序：起 session → 打印事件条数 → 干净退出                                        |
| **W3** | 解析与聚合：`etw_parse.go`（TDH + schema 缓存）、`collector.go`（Collector / tracked-set / TTL / `Bytes` / `Describe` / 丢事件计数）                                | `Load/Bytes/Close/Describe` 全 API                             | ETW 设计文档 §35 的三步验证（curl → TCP TX/RX → nslookup → UDP TX/RX），`Bytes` 累计值肉眼核对量级 |
| **W4** | 包内真机自验：临时冒烟程序覆盖 §9 本期项（#1/2/7/8/10），W3 遗留问题回修                                                                                                     | 验证记录（记回本文档 §9）                                                | 本期验收项全绿；临时程序**不入仓库**（或仅存 `scripts/`，实施期定）                                     |

### 后续期（透出与端到端，另起计划）

| 阶段     | 内容                                                                                                                                                                                                | 交付物            | 可独立验收                                                                             |
| ------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------- | --------------------------------------------------------------------------------- |
| **T1** | 透出：`pkg/procnet/procnet_windows.go` 从 stub 改为委托 `winnetetw`（§5.1，唯一改动文件）                                                                                                                          | 组合根零改动获得 ETW   | Windows 真机起 `asa-server api`，SSE `all-info` 里 `instances[].net_io` 有值，实例网络趋势图自动渲染 |
| **T2** | 端到端真机验证与压力：权限矩阵（服务/管理员/普通用户）、ARK 实例端到端、高流量稳定性、长时间运行                                                                                                                                               | 验证记录补全 §9 后续期项 | 24h 运行 lost events 占比 < 1%，无 session 泄漏                                           |
| **T3** | 文档同步（归属透出计划，本文档仅记录范围）：`RESOURCE_RATE_CHART_PLAN.md` / `docs/API_REFERENCE.md` / `openapi.json` 中「Windows 恒 null」表述改为指向本包；**`AGENTS.md` / `CLAUDE.md` / `app/CLAUDE.md` 一律不同步**——本文档只关注 winnetetw 的实现，包清单等文档更新是透出（T1）落地时的工作，属另一个计划 | 文档一致           | grep「恒为 null / 恒 null」无残留过时表述                                                     |

依赖关系：W1 → W2 → W3 严格串行（后者依赖前者的类型与 session 句柄）；W4 收尾本期。  
T1 只依赖 W3 的 API 冻结，可在 W4 之后任意时点插入。每阶段一个 commit  
（conventional commits，`feat:` 主体），Windows `go build` + 单测随 commit 验证。

---

## 8. 测试策略

**可单测的（W1/W3，Windows 开发机直接跑，全部带 `//go:build windows`）**：

- Event ID → (协议, 方向) 映射表：8 个 ID 全覆盖 + 未知 ID 拒绝。
- `EVENT_TRACE_PROPERTIES` 缓冲区偏移拼装：钉死结构体大小与两个 Offset 的关系。
- tracked-set / TTL：把时钟抽象成注入的 `now func() time.Time`（仅测试需要），  
  覆盖「首问建零值条目」「TTL 淘汰后重建从零开始」「淘汰扫描节流」。
- TCP/UDP 端口与地址转换不涉及（无连接列表功能），不做。

**不做 mock 的（ETW 层本身）**：`StartTraceW` 之后的整条链路没法在单测里伪造出有意义的  
系统行为，与 procnet 的 BPF 层同等待遇——用真机验证覆盖（§9）。

---

## 9. 真机验收清单

「本期」= W2–W4 临时冒烟程序可完成；「后续」= 依赖 T1 透出接线。

| #  | 期  | 场景           | 操作                                               | 期望                                                             |
| -- | -- | ------------ | ------------------------------------------------ | -------------------------------------------------------------- |
| 1  | 本期 | TCP          | 管理员终端跑冒烟程序，`curl.exe https://example.com` 反复拉大文件 | 跟踪的测试进程 PID 的 TX/RX 累计值持续增长，量级与文件大小一致                          |
| 2  | 本期 | UDP          | `nslookup example.com`                           | RX/TX 有小量增长                                                    |
| 3  | 后续 | ARK 实例端到端    | 启动一个实例，开实例详情页                                    | 「网络进/出速度」图渲染曲线（不再是占位）；ASA 流量以 UDP 为主，UDP 两路必须有值                |
| 4  | 后续 | 量级核对         | 资源监控页的宿主机网络速率 vs 实例网络速率                          | 同量级（差值 = 回环 + 其它进程，方向一致即可）                                     |
| 5  | 后续 | 权限：服务模式      | `service install` 后访问页面                          | net_io 有值                                                      |
| 6  | 后续 | 权限：普通用户      | 普通终端起 `api`                                      | 启动日志一行「实例级网络监控未启用」，net_io 恒 null，**其它指标与 API 完全正常**            |
| 7  | 本期 | session 生命周期 | 冒烟程序正常退出 → `logman query -ets`                   | `AsaServerProcNet` 不在列                                         |
| 8  | 本期 | 崩溃残留清理       | 强杀冒烟程序 → 再跑一次                                    | 第二次启动成功，`logman` 里旧 session 被清掉，总数不增长                          |
| 9  | 后续 | 高流量稳定性       | 24h 运行 + 实例在跑                                    | `Describe()` 的 lost events 占比 < 1%；内存稳定（tracked-set 有界）        |
| 10 | 本期 | 优雅退出         | `Close()` 路径                                     | 无 `CloseTrace` / `ControlTraceW` 错误；再次启动无 ERROR_ALREADY_EXISTS |

---

## 10. 风险与对策

| 风险                                                                      | 等级 | 对策                                                                                                                                                                                    |
| ----------------------------------------------------------------------- | -- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **UDP RX 事件不完整**（Kernel-Network provider 的已知怪癖：部分接收路径可能不触发 Event 43/59） | 高  | 后续期 #3 专项核对 ARK 实例的 RX 是否明显偏低；若确认缺失，备选方案是改挂 `Microsoft-Windows-Kernel-Network` 的 `UdpRcv` 之外的补充 provider（如 `Microsoft-Windows-WinINet` 不合适，可能需要 `Microsoft-Windows-TCPIP`），届时在本文档追加决策 |
| `EVENT_TRACE_PROPERTIES` 偏移拼装错误 → 启动崩溃或 session 名损坏                     | 中  | W2 单测钉死偏移；`logman query -ets` 核对 session 名                                                                                                                                            |
| callback 内 panic 会带崩整个进程（`syscall.NewCallback` 不 recover）               | 中  | callback 只做 map get/add，解析路径全部防越界；W3 代码评审专项检查                                                                                                                                         |
| TDH 属性名跨 Windows 版本漂移（PID/Pid、size/Size）                                | 中  | schema 缓存按「UInt32 且数量匹配」识别而非按名字硬编码（§4.4）；Win10/Win11 双机验证                                                                                                                             |
| 高流量下锁竞争（若真发生）                                                           | 低  | 预留升级路径：换 channel + aggregator goroutine，接口不变（§4.5）                                                                                                                                    |
| 中文 Windows 上 TDH 返回本地化属性名                                               | 低  | 同上，不按名字匹配属性                                                                                                                                                                           |

---


## 11. 已确认决策

1. **包名与归属**：独立新包 `pkg/winnetetw`，不并入 `pkg/procnet`（两者平台互斥、  
   实现机理完全不同，合并只会让 build tag 交叉爆炸）；对上层经 `pkg/procnet` 门面透出（§5）。
2. **只做流量统计**：ETW 设计文档的连接列表、进程名缓存、`Monitor` 大 API 均不做（§1 / §2.3）。
3. **API 形状镜像 `pkg/procnet`**：`Load(Options) (*Collector, error)` / `Bytes` / `Describe` / `Close`。  
   **不定义 `ErrUnsupported`**（门面概念留在 procnet）；`NetSource` 满足性由后续期的  
   `procnet_windows.go` 委托层隐含保证，winnetetw 不 import `pkg/serverinfo`（§5.3）。
4. **`Bytes` 返回累计值 + tracked-set 先登记后计数**，与 eBPF 的 targets map 语义逐条对齐（§2.2 / §4.5）。
5. **聚合不用 channel**，callback 直接持 mutex 更新 map：ProcessTrace 回调线程串行 + 采样器  
   2s 一读，不存在竞争压力；换 channel 反而引入丢弃路径。这是对上游文档 §17 的**有理由偏差**（§4.5）。
6. **Event ID 过滤做进 `EnableTraceEx2`**（内核侧），callback 二次过滤仅防御（§4.3）。
7. **TDH 动态解析 + schema 缓存**，不硬编码 payload offset（§4.4，上游文档 §15）。
8. **固定 session 名 `AsaServerProcNet`**，启动时清理残留（§4.2，上游文档 §13）。
9. **不做 PID 复用检测**：计数器只增不减 + 采样器按 CreateTime 重建 prev，差分天然正确（§4.6）。
10. **不加 appconfig 配置项**：权限不足没有「配了就能救」的余地，失败即降级（§4.8）。
11. **本期只做包内实现，外部调用不做**：不改 `pkg/procnet`、`internal/webapi`、前端；  
    真机自验用临时冒烟程序（§1 / §7）。
12. **透出走 `pkg/procnet` 统一门面**：后续期 `procnet_windows.go` stub 改委托（唯一改动文件），  
    委托层包 `Collector` 保持类型边界；组合根与 `actions.go` 零改动，  
    `internal/webapi` 永远只 import procnet（§5）。~~原「组合根双调 startProcNet + startWinNetETW」  
    方案作废~~——门面方式改动更小、平台决策更内聚。
13. **整包 `//go:build windows`**：Go 无包级 tag，逐文件标注；非 Windows 平台该包不存在，  
    不需要 `winnetetw_other.go` stub（§3.1 / §6）。文件名不带 `_windows` 后缀。
14. **丢事件计数并进 `Describe()`**，不做独立 Stats API / 告警（§4.7）。

## 12. 仍待明确（实施期裁决）

- UDP RX 完整性问题（§10 第一条）——后续期验收 #3 的结果决定是否需要补充 provider，  
  目前按「Kernel-Network 足够」推进；本期 W3/W4 的 curl + nslookup 已能初步暴露。
- `Options` 是否需要暴露 ETW buffer 数量/大小/flush 间隔的调优项——先写死  
  （默认 buffer 64 个 × 64KB，flush 1s），压测发现问题再加，不加无消费者的配置面。
- 临时冒烟程序的落点（W4）：`go run` 一次性脚本、`scripts/`、还是带 tag 的集成测试  
  （如 `//go:build windows && etwsmoke`）——实施期按顺手程度定，倾向不入仓库。
- 后续期 T3 是否需要同步更新 `app/CLAUDE.md`（若其描述了实例网络图的平台行为）——  
  **已裁决：不同步**。本文档只关注 winnetetw 的实现，其余功能（透出、文档同步）都归  
  透出计划（T1–T3）落地时处理。

---

## 13. 实现记录（2026-09-06）

本期 W1–W4 已完成，交付物（全部 `//go:build windows`，依赖仅 `golang.org/x/sys/windows` + 标准库）：

| 文件                          | 内容                                                                                     |
| --------------------------- | -------------------------------------------------------------------------------------- |
| `pkg/winnetetw/winnetetw.go`   | 包文档、`Options`、`netKind`、8 Event ID → (协议,方向) 映射表、`classifyEvent`                     |
| `pkg/winnetetw/etw_syscall.go` | lazy DLL（advapi32/tdh）、全部 ETW/TDH 结构体（布局逐字段核对 MS Learn）、原生 API 薄封装              |
| `pkg/winnetetw/etw_session.go` | 会话生命周期：StartTraceW（固定名 + 残留清理）→ EnableTraceEx2（Event ID 过滤）→ OpenTraceW → ProcessTrace（goroutine）→ Close（CloseTrace → 等退出 → STOP） |
| `pkg/winnetetw/etw_parse.go`   | TDH 两层解析：快路径（schema 推算 offset + 一次 TdhGetProperty 验证）/ 慢路径（按名取值）/ 失败标记，边界检查全覆盖 |
| `pkg/winnetetw/collector.go`   | `Load`/`Bytes`/`Describe`/`Close`、aggregator（tracked-set + TTL，注入时钟）、全局唯一 callback、panic 兜底 |
| `pkg/winnetetw/winnetetw_test.go` | 12 个测试：结构体大小/偏移钉死（14 个 struct + 24 个字段偏移）、事件表、properties/过滤器拼装、payload 读取、schema 快慢路径、aggregator TTL 语义 |

验证结果：

- Windows 原生：`go build ./...`、`go vet ./...`、`go test ./pkg/winnetetw/` 全绿（12 tests）。
- Linux 隔离：`GOOS=linux GOARCH=amd64 go build ./...` 通过——包被整体排除，无 stub 文件（决策 13 成立）。
- 相邻包回归：`pkg/procnet` / `pkg/serverinfo` 测试不受影响。

实施期对方案的落地偏差（均已在代码注释标注理由）：

- `eventRecord` 的 `ExtendedData`/`UserData`/`UserContext`、`eventTraceLogfileW.Context` 声明为  
  `unsafe.Pointer` 而非 `uintptr`：结构体由 OS 分配在 ETW 缓冲区（非 Go 堆，GC 不扫），  
  直接存指针安全，且免去 `go vet` 的 uintptr→Pointer 转换告警。
- `enableTraceParameters` / `eventTraceHeader` 等尾部对齐由 Go 自动填充规则天然满足 C 布局，  
  未加显式填充字段（加了反而错位——单测钉死全部偏移）。
- **丢事件计数改用 `ControlTraceW(QUERY)`**，不走 §4.7 写的 BufferCallback +  
  `EVENT_TRACE_LOGFILE.LogfileHeader.EventsLost`：QUERY 拿到的 `props.EventsLost` /  
  `RealTimeBuffersLost` 偏移确定，而 `EVENT_TRACE_LOGFILEW.EventsLost` 官方文档标注  
  「Not used」。本包因此**不设置 BufferCallback**。（2026-09-07 评审时发现此偏差未记录，补回。）

真机验收（§9 本期项 #1/2/7/8/10，需管理员终端）：尚未执行，待真机冒烟后把结果记回 §9。

### 13.1 评审返工（2026-09-07）

接线（T1）之前做了一轮静态评审，四项缺陷已修，逐项理由与修法见  
`docs/WINNET_ETW_PLAN.md` Part 2` §2（活动清单），这里只记结论：

| 项 | 结论 |
| --- | --- |
| `aggregator.add` 锁外读 map | **已修**：读挪进锁内。原写法是 `fatal error: concurrent map read and map write`，fatal 不是 panic，callback 里的 recover 拦不住，整进程会被带走；触发条件是「某 PID 首次被 `Bytes()` 问到的同时有其网络事件」——实例一启动就满足。补了 `-race` 并发测试，**已用旧写法确认该测试能复现**（`get` 的 mapassign vs `add` 的 mapaccess2）。 |
| 会话中途死掉时 `Bytes` 仍返回 `ok=true` | **已修**：新增 `etwSession.alive()`（`consumerDone` 是否已关闭），死会话返回 `ok=false`。原行为会让采样器差分出恒 0，前端画成贴底实线而不是断点，违反 RESOURCE_RATE_CHART_PLAN §4.4「采不到必须是 null」。`Describe()` 同步加了「会话已终止」提示。 |
| `CloseTrace` 返回 `ERROR_CTX_CLOSE_PENDING`(7007) 被当成失败 | **已修**：7007 是实时消费下的**成功**语义，与 0 同等对待（`closeTraceSucceeded`）。原行为会让每次停止都打「卸载实例级网络监控出错」，验收项 #10 假红。 |
| 残留清理复用被 `ControlTraceW` 改写过的 properties 缓冲 | **已修**：STOP 用独立缓冲，重试 `StartTraceW` 前重建 `propsBuf`。对应验收项 #8。 |

另外修掉一处评审时未列出、实现中发现的并发问题：`Close()` 原先把 `c.sess` 置 `nil`，  
而采样器可能正在另一个 goroutine 里读它（组合根先撤 `NetSource` 再 `Close`，但撤下那一刻  
可能已有一次 `Bytes` 取到了接口值）。改为 `sess` 在 `Load` 之后不再改写，`Close` 只翻  
`closed atomic.Bool`。

#### 三条真机实测订正（2026-09-07，Win11 **非提权**）

跑冒烟程序时打脸了三处纸面推断，全部已修，详见 `docs/WINNET_ETW_PLAN.md` Part 2` §2.7–§2.9：

1. **§4.8 的降级点写错了。** 那张表推断普通用户会卡在 `StartTraceW`
   `ERROR_ACCESS_DENIED`——**实测 `StartTraceW` 照常成功并真的建出了 session**，
   直到 `EnableTraceEx2` 挂 provider 才 `ERROR_ACCESS_DENIED`。于是
   `translateStartError` 那段友好文案在最常见的降级场景下根本不会出现。
   已补 `translateEnableError`。**§4.8 的表按此理解，别再照抄。**
2. **「查不到 session」是 4201 不是 4200**（4200 是 `ERROR_WMI_GUID_NOT_FOUND`）。
   而且 `ControlTraceW(0, name, STOP)` **成功停掉会话之后返回的也是 4201**
   （调用前 QUERY 得 0，调用后 QUERY 得 4201）。已改成 `controlStopSucceeded`。
3. **`Load` 失败会泄漏一个系统级 session**，这是 1 的直接后果：session 已经建出来了，
   而清理路径那一发 STOP **不可靠**——`logman query -ets` 里 `AsaServerProcNet` 仍是
   `Running` 且**不会自行消失**（实测存活数分钟，直到下次 `Load` 走
   `ERROR_ALREADY_EXISTS` 收掉）。不是异步收尾：进程在 STOP 后多活 2 秒、6 秒都没用。
   已改成 `destroySession`——STOP 后 QUERY 复核，还在就再来一发（最多 5 轮 × 200ms）。
   连续 4 次非提权启动 + `logman` 核对全部干净，其中第一次顺带收掉了修复前的残留，
   等价验证了 §9 的 #7 与 #8。

单测从 12 个增至 19 个（新增：并发对撞、死会话、非法 PID、`closeTraceSucceeded`、  
`controlStopSucceeded`、`translateEnableError`、properties 缓冲独立性）。  
`go build ./...` / `go vet ./...` / `go test -race`（Windows）/  
`GOOS=linux go build ./...` 全绿；`pkg/procnet`、`pkg/serverinfo` 回归通过。

⚠️ `-race` 在本机**必须用 PowerShell 跑**，Git Bash 下 ThreadSanitizer 启动即分配失败
（`error code: 87`），那是 shell 环境问题不是代码问题。

---

## 14. 后续集成：`pkg/procnet` 门面委托详设（T1）

> 本章是 §5 的展开，供后续期（透出计划）直接照抄实施。改动面：**仅 `pkg/procnet/procnet_windows.go` 一个文件**。

### 14.1 现状与目标

`pkg/procnet` 当前的平台矩阵：

| 文件                    | build tag                | 内容                                       |
| --------------------- | ---------------------- | ---------------------------------------- |
| `procnet.go`          | （无）                    | 包文档、`ErrUnsupported`、`Options`、go:generate 指令 |
| `procnet_linux.go`    | `linux && amd64`       | eBPF 实现（cilium/ebpf + 6 探针）               |
| `procnet_windows.go`  | `windows`              | **stub**：`Load` 恒返回 `ErrUnsupported`        |
| `procnet_other.go`    | `!windows && !(linux && amd64)` | stub（linux/arm64、darwin 等）          |

T1 的目标：把 `procnet_windows.go` 从 stub 改为**委托 `winnetetw`**。此后调用链变成：

```
internal/webapi/procnet.go (组合根，零改动)
  └─ procnet.Load(Options)            ← Windows 上不再返回 ErrUnsupported
       └─ winnetetw.Load(Options{})   ← 委托，类型边界包一层
            └─ ETW 会话 + 内核事件
```

### 14.2 委托层代码（全文照抄级）

```go
//go:build windows

package procnet

import "asa-server/pkg/winnetetw"

// Windows 上按进程的网络计量走 ETW（pkg/winnetetw，
// Microsoft-Windows-Kernel-Network provider），实现与 Linux 侧 eBPF 语义对齐：
// Bytes 返回累计值、首问登记 0 基线、30s TTL 淘汰。详见 docs/WINNET_ETW_PLAN.md。
//
// 本文件只做转发，不复制任何逻辑——两平台的行为差异应该只在 winnetetw 内部。

// Collector 是 winnetetw.Collector 的门面壳。类型边界留在 procnet 手里：
// internal/webapi 与 pkg/serverinfo 永远只见 procnet.Collector，
// 不直接 import winnetetw（依赖方向 webapi → procnet → winnetetw）。
type Collector struct {
	etw *winnetetw.Collector
}

// Load 建立 ETW 会话。失败返回 error（权限不足等，winnetetw 已翻成可读中文），
// 调用方按「失败即降级」处理。
//
// opts.BTFPath 是 Linux 概念，Windows 侧没有对应物，忽略之——
// 组合根无条件传参，不需要在调用侧做平台判断。
func Load(opts Options) (*Collector, error) {
	c, err := winnetetw.Load(winnetetw.Options{})
	if err != nil {
		return nil, err
	}
	return &Collector{etw: c}, nil
}

// Bytes 返回该 PID 的累计收发字节（自其被登记进跟踪集合起算），
// 速率由调用方按 Δt 差分。
func (c *Collector) Bytes(pid int32) (rx, tx uint64, ok bool) {
	if c == nil || c.etw == nil {
		return 0, 0, false
	}
	return c.etw.Bytes(pid)
}

// Describe 返回一行可读的运行状态（会话名、事件数、丢事件计数等）。
func (c *Collector) Describe() string {
	if c == nil || c.etw == nil {
		return ""
	}
	return c.etw.Describe()
}

// Close 停止 ETW 会话（CloseTrace → 等 ProcessTrace → ControlTraceW STOP）。
// 必须在进程退出前调用，避免残留 session。
func (c *Collector) Close() error {
	if c == nil || c.etw == nil {
		return nil
	}
	return c.etw.Close()
}
```

### 14.3 为什么是「壳结构体转发」而不是类型别名

- **类型别名（`type Collector = winnetetw.Collector`）会泄漏**：调用方 `import procnet` 拿到的
  实际是 winnetetw 的类型，`fmt` 打印、错误文案、未来给 procnet 加平台公共方法都会失控；
- **壳结构体让平台决策内聚**：`Load` 在三个平台文件里各有实现，签名一致、返回的都是
  `*procnet.Collector`——这是 procnet 作为「统一门面」的全部含义（§5.1）；
- 转发层 4 个方法共十几行，没有可测试的逻辑，**不单独写测试**
  （结构与偏移已在 winnetetw 内钉死，这里加测试只是噪声）。

### 14.4 零改动清单（集成时逐一核对）

| 文件                            | 为什么不用改                                                                                   |
| ----------------------------- | --------------------------------------------------------------------------------------- |
| `internal/webapi/procnet.go`   | 组合根调 `procnet.Load(procnet.Options{BTFPath: ...})`——Windows 上 BTFPath 被委托层忽略，行为不变：成功 → `SetNetSource(c)`，失败 → 一行日志降级 |
| `internal/webapi/actions.go`   | `startProcNet()` / `stopProcNet()` 的调用点与顺序不动                                                          |
| `pkg/serverinfo/*`             | `NetSource` 是结构化接口，`procnet.Collector` 照旧满足 `Bytes(pid) (rx, tx, ok)`                              |
| `pkg/procnet/procnet.go`       | `ErrUnsupported` 留在无 tag 文件里供 linux/arm64 等 stub 继续使用；包文档补一句「Windows 走 ETW」                          |
| 前端 / SSE 载荷 / openapi        | `instances[].net_io` 字段格式与平台约定不变，只是 Windows 上从恒 null 变为有值（null 语义保留给「采集失败/降级」）      |

### 14.5 生命周期与并发语义（组合根既有约定直接继承）

- **单例**：组合根 `procNetMu` + `procNet != nil` 短路保证进程内只 Load 一次；
  ETW 侧另有系统级兜底——session 固定名 `AsaServerProcNet`，第二个进程 Load 时会先 STOP
  旧 session 再重建（§4.2），不会泄漏成 `AsaServerProcNet-1/2/3`。
- **停止顺序**：`stopProcNet` 先 `SetNetSource(nil)` 再 `Close()`——先撤接口再关资源，
  采样器不会拿着已关闭的 collector 读。winnetetw 的 Close 内部再保证
  「CloseTrace → 等 ProcessTrace 退出 → STOP」的顺序（§4.1），组合根无需关心。
- **权限降级**：Windows 服务默认以 LocalSystem 运行（有权限）；普通用户起 `api` 时
  `winnetetw.Load` 返回权限错误，组合根记一行「实例级网络监控未启用（该字段将为 null）」
  ——与 Linux 缺 BTF / 容器策略挡下的降级路径完全同构，无需新代码。

### 14.6 集成验收（对应 §7 后续期 T1）

1. Windows 真机 `go build ./...` + 全量单测；
2. 管理员终端起 `asa-server api`，日志出现「实例级网络监控已启用：ETW session=...」；
3. 启动一个 ARK 实例，实例详情页「网络进/出速度」图渲染曲线（不再是占位）；
4. `logman query -ets` 看到 `AsaServerProcNet`，`stopProcNet`（API 停止 / 退出）后消失；
5. 普通用户终端重复 2，确认降级路径（net_io 恒 null，其余指标正常）。

---

## 15. `pkg/winnetetw` 单独使用指南（不经 procnet）

> 本章面向三类读者：跑 §9 真机验收的维护者、写诊断工具的人、以及任何想在
> Windows 上拿「按 PID 的网络收发字节」的独立程序。procnet 门面（§14）只是
> 本包的一个消费者；本包可以完全独立使用。

### 15.1 API 全貌（4 个导出符号，没有别的）

```go
func Load(opts Options) (*Collector, error)
func (c *Collector) Bytes(pid int32) (rx, tx uint64, ok bool)  // 累计值
func (c *Collector) Describe() string                          // 一行状态
func (c *Collector) Close() error                              // 必须调用
```

`Options` 当前为空结构体（预留位），传 `winnetetw.Options{}` 即可。

### 15.2 调用方必须遵守的四条契约

1. **权限前置**：`Load` 需要管理员或 Performance Log Users 组。普通用户下 `Load` 返回
   error（文案已指明缺什么权限），**这不是 bug**——检查 `windows.IsAdmin` 或直接试 Load。
2. **系统级单会话**：session 名固定 `AsaServerProcNet`。**同一台机器上同时只能有一个
   消费进程**：第二个进程 `Load` 时会把第一个的 session 停掉再重建，第一个的
   `ProcessTrace` 随之退出（此后 `Bytes` 仍可调但不再增长）。单独诊断工具**不要与
   运行中的 asa-server 同时用**——真机验收冒烟程序也因此必须独立时段跑。
3. **速率要自己差分**：`Bytes` 返回自登记起的累计字节。速率 = (本次 − 上次) / Δt；
   首帧没有 prev，按约定输出 null（与资源趋势图「首帧速率为 null」一致）。
   PID 复用检测也归调用方：拿 `gopsutil` 的 `CreateTime` 做键的一部分，
   CreateTime 变了就丢弃 prev 重新基线。
4. **退出必须 Close**：`Close` 释放 ETW session。忘了调（且进程没崩）会留下
   `AsaServerProcNet` 残留——不过下次任何进程 `Load` 时会自动清掉（§4.2），
   这是兜底不是借口；SIGKILL 场景无法 Close，残留同样由该机制回收。

### 15.3 语义速查（与 pkg/procnet/eBPF 逐条对齐）

| 行为                        | 语义                                                       |
| ------------------------- | -------------------------------------------------------- |
| 首次 `Bytes(pid)`          | 把 PID 登记进跟踪集合，返回 `(0, 0, true)`——此前该 PID 的流量不计入 |
| 未登记 PID 的流量               | 内核事件照发，聚合层直接丢弃（成本一次 map miss）                        |
| 30 秒没人 `Bytes(pid)`       | 条目淘汰；下次再问重新登记、从 0 开始                                  |
| `pid <= 0` / nil Collector | 返回 `(0, 0, false)`，不 panic                             |
| 计数口径                      | Windows 网络栈的进程数据量：含回环；≠ 网卡 wire bytes；代理/VPN/TUN 下有系统性差值 |
| PID=0（内核线程）的事件            | 丢弃，不映射成普通进程                                            |
| 丢事件可观测性                   | `Describe()` 输出事件数/解析丢弃/失败 schema/丢事件计数              |

### 15.4 完整示例：独立监控一个进程的速率

```go
//go:build windows

package main

import (
	"fmt"
	"os"
	"time"

	"asa-server/pkg/winnetetw"
	"github.com/shirou/gopsutil/v4/process"
)

func main() {
	pid := int32(os.Getpid()) // 或改为任意目标进程
	tick := 2 * time.Second   // 与 asa-server 采样器同频

	c, err := winnetetw.Load(winnetetw.Options{})
	if err != nil {
		fmt.Println("ETW 未启动（需管理员或 Performance Log Users）:", err)
		os.Exit(1)
	}
	defer c.Close()
	fmt.Println(c.Describe())

	var prev, now struct {
		rx, tx uint64
		ct     int64 // 进程 CreateTime（毫秒）：变了 = PID 复用，重置基线
	}
	first := true
	for range time.Tick(tick) {
		p, err := process.NewProcess(pid)
		if err != nil {
			fmt.Println("进程不存在:", err)
			os.Exit(0)
		}
		ct, _ := p.CreateTime()
		now.rx, now.tx, _ = c.Bytes(pid)
		now.ct = ct

		switch {
		case first || now.ct != prev.ct: // 首帧 / PID 复用：只有基线，没有速率
			fmt.Printf("pid=%d 基线 rx=%d tx=%d\n", pid, now.rx, now.tx)
		default:
			fmt.Printf("pid=%d ↓%s/s ↑%s/s\n", pid,
				human(now.rx-prev.rx, tick), human(now.tx-prev.tx, tick))
		}
		prev = now
		first = false
	}
}

func human(delta uint64, d time.Duration) string {
	return fmt.Sprintf("%.1fKB", float64(delta)/1024/d.Seconds())
}
```

要点：差分窗口内的 `now.ct != prev.ct` 检查就是全部的 PID 复用处理——
winnetetw 的计数器只增不减，重建 prev 后差分天然正确，无需通知采集层。

### 15.5 真机验收冒烟程序的最小形态

§9 本期项 #1/2/7/8/10 可用比上面更短的程序验证（不需要速率，只看累计值）：

```go
c, err := winnetetw.Load(winnetetw.Options{}) // #10：错误路径 = 权限降级
if err != nil { fmt.Println(err); os.Exit(1) }
defer c.Close()
time.Sleep(500 * time.Millisecond)            // 等会话与 provider 就绪
go func() {                                   // #1：TCP——反复拉大文件
	for { _, _ = http.Get("https://example.com/big.bin") }
}()
// #2：UDP——另开终端跑 `nslookup example.com`，或代码里 exec
pid := int32(os.Getpid())
for i := 0; i < 30; i++ {
	rx, tx, _ := c.Bytes(pid)               // 注意：首次调用是 0 基线，从第二次起看增长
	fmt.Printf("rx=%d tx=%d  %s\n", rx, tx, c.Describe())
	time.Sleep(time.Second)
}
```

验收后 `logman query -ets` 确认 `AsaServerProcNet` 已消失（#7）；强杀再跑验证
残留清理（#8）。临时程序不入仓库（§12 已裁决倾向）。

### 15.6 已知限制（单独使用时更要心里有数）

- **UDP RX 完整性**（§10 第一条）：Kernel-Network 的 UDP 接收事件（43/59）在部分
  接收路径可能不触发。单看 TCP 增长正常、UDP 不动时先怀疑这个，别先怀疑自己的代码。
- **事件粒度**：按 socket 调用聚合，`size` 是应用层数据量；小包高频场景
  （DNS 之类）每事件开销固定，但不影响正确性。
- **Windows 专属**：整包 `//go:build windows`，其它平台该包不存在
  （不是返回错误，是 import 都 import 不到）——跨平台工具必须自己做 build tag 分流，
  这正是 procnet 门面存在的理由。

---

# Part 2 — `pkg/winnetetw` 迭代清单（原 `WINNET_ETW_TODO.md`）

> 本部分为活动文档：新缺陷加到此处，结论回填 Part 1。

# `pkg/winnetetw` 迭代清单（活动文档）

> 状态：**§2 的缺陷已全部修完并验证（2026-09-07，共五轮真机）**；
> `netmon etw --selftest` 已判定「捕获正常」。仍缺的是对着**在跑的 ARK 实例**
> 核对 UDP 四路分项。
>
> 📌 **验证状态汇总与下一步操作手册见 `docs/NETMON_VERIFICATION_LOG.md`**；
> 本清单保留每个缺陷的现场与修法。
> 与 `docs/WINNET_ETW_PLAN.md` 的分工同 overlay 那对文档：
> **PLAN 是只增不改的档案，新缺陷进本文件，结论回填 PLAN**。
> 关联：`docs/RESOURCE_RATE_CHART_PLAN.md`（P7，Windows 侧的上位方案）。
>
> 首次评审：2026-09-07（静态评审 + `go vet` + `go test`（12 项）+ `GOOS=linux go build ./...`，
> 均通过；`-race` 见 §6 的环境限制）。

---

## 1. 评审结论：方案符合 P7，方向没有分歧

P7 §2.2 原文就写明「eBPF-for-Windows 尚不覆盖此类网络计量，按进程网络需 ETW
（`Microsoft-Windows-Kernel-Network` provider）」，并把它留成独立事项。所以
`pkg/winnetetw` **不是** eBPF-for-Windows 的替代选型，它就是 P7 指定的那条路的实现。

与采样器契约（PLAN §2.2）逐条对表，五项全中：

| 契约 | 落点 | 结论 |
| --- | --- | --- |
| `Bytes` 返回累计值，速率归采样器差分 | `collector.go` 的 `netCounters` 只做累加 | ✅ |
| 首问返回 0 基线 + `ok=true`，先登记后计数 | `aggregator.get` | ✅ |
| tracked-set 限死条目数，未登记事件丢弃 | `aggregator.add` | ✅ 语义对，实现有缺陷（§2.1） |
| TTL 与 eBPF 侧同值（30s / 扫描间隔 10s） | `trackedTTL` / `pruneInterval` | ✅ |
| 失败即降级（`net_io` 为 null，其余指标不受影响） | `Load` 返回 error，组合根不注入 `NetSource` | ✅ |

包纯度与依赖方向也成立：只依赖 `golang.org/x/sys/windows` + 标准库，不 import
`internal/**`，不 import `pkg/serverinfo`；T1 的改动面确实只有
`pkg/procnet/procnet_windows.go` 一个文件。

**但下面 §2 的四项必须先修**——其中第一项会让整个进程 fatal，不是「数据不准」级别的问题。

---

## 2. 待修缺陷（按严重程度排）

### 2.1 【阻断】回调线程上的无锁 map 读 → 整进程 fatal

- **位置**：`pkg/winnetetw/collector.go:58-70`（`aggregator.add`）
- **现象**：`a.counters[pid]` 在 `a.mu.Lock()` **之前**被读；而 `get` 在锁内
  `a.counters[pid] = &netCounters{}`、`pruneLocked` 在锁内 `delete`。
  Go 运行时判定为 `fatal error: concurrent map read and map write`——
  **这是 fatal 不是 panic**，`collector.go:134` 那个 `defer recover()` 拦不住，
  ETW 回调线程会把整个 asa-server 带走。
- **触发条件是常规路径**，不是极端场景：某实例的 PID **首次**被 `Bytes()` 问到
  （= 插入新条目）的同一时刻，该机器上有被跟踪 PID 的网络事件在流。实例一启动就满足。
- **这不是设计取舍，是实现走偏**：PLAN §4.5 / 决策 5 写的就是「callback 直接持
  `sync.Mutex` 更新 map」，锁本来就该罩住整个 get/add。当前写法既没省掉锁，
  又丢了正确性。
- **修法**（把读挪进锁内，代价仍是纳秒级）：

  ```go
  func (a *aggregator) add(pid uint32, k netKind, size uint32) {
      a.mu.Lock()
      if c, tracked := a.counters[pid]; tracked { // 未登记：丢弃（成本 = 一次锁 + map miss）
          if k == kindTCP_RX || k == kindUDP_RX {
              c.rx += uint64(size)
          } else {
              c.tx += uint64(size)
          }
      }
      a.mu.Unlock()
  }
  ```

  行内注释与 `collector.go:30` 的「成本 = 一次 map miss」一并改成「一次锁 + 一次 map miss」。
- **验收**：新增并发单测（`add` 与 `get` 各起 goroutine 对撞），在 PowerShell 下
  `go test -race ./pkg/winnetetw/`（§6）必须干净通过。当前 12 个测试**没有一个覆盖并发**，
  所以这个缺陷靠现有测试永远暴露不出来。

### 2.2 【高】会话中途死掉时无法表达「采不到」，曲线会变成恒 0 直线

- **位置**：`pkg/winnetetw/collector.go:197-202`（`Bytes`）
- **现象**：`ok=false` 只在 `pid <= 0` 与已 `Close()` 时出现。按 PLAN §15.2，
  同机第二个消费进程 `Load` 会把本进程的 session 停掉，此时消费 goroutine 退出、
  计数永久停增，而 `Bytes` 仍返回 `ok=true`。采样器于是持续拿到「累计值不变」，
  差分出 0——**前端画的是一条贴着底边的实线，不是断点**。
- **为什么必须修**：PLAN §2.2 契约表第三行「`ok=false` 表示采不到 → 该字段 null」
  在当前实现里**没有任何活的触发路径**；而 §4.4「采不到的值必须是 null 而不是 0」
  是 RESOURCE_RATE_CHART_PLAN 全局约定。恒 0 直线会被读成「实例真的没流量」。
- **修法**：给 `etwSession` 加一个存活判据，`Bytes` 在会话已死时直接 `ok=false`
  且**不登记**新 PID：

  ```go
  func (s *etwSession) alive() bool {
      select {
      case <-s.consumerDone: // ProcessTrace 已返回：会话被抢走或已停
          return false
      default:
          return true
      }
  }
  ```

  注意 `consumerDone` 在正常 `Close()` 后也是关闭态，语义一致（关了就该 null）。
- **顺带**：`Describe()` 可以把「会话是否存活」也拼进去，排障时一眼能分清
  「没流量」和「会话没了」。

### 2.3 【中】`CloseTrace` 的正常返回码被当成错误，每次停止都会假报警

- **位置**：`pkg/winnetetw/etw_session.go:183-185`
- **现象**：任何非零返回码都被记进 `closeErr`。但实时消费下 `CloseTrace` 返回
  `ERROR_CTX_CLOSE_PENDING`(7007) 是**文档规定的成功语义**（调用已受理，
  `ProcessTrace` 稍后返回）。于是 `Close()` 返回一个假错误，
  `internal/webapi/procnet.go` 的 `stopProcNet` 每次退出都打
  「卸载实例级网络监控出错」。
- **后果**：PLAN §9 验收项 #10（「无 `CloseTrace` / `ControlTraceW` 错误」）会假红，
  而真出问题时反而分不出来。
- **修法**：`etw_syscall.go` 加 `errCtxClosePending = 7007`，
  `stop()` 里与 0 同等对待。

### 2.4 【中】残留清理路径复用了被 `ControlTraceW` 改写过的 properties 缓冲

- **位置**：`pkg/winnetetw/etw_session.go:107-115`
- **现象**：`ERROR_ALREADY_EXISTS` 分支先用 `props` 调 `ControlTraceW(STOP)`，
  再把**同一块缓冲**交给重试的 `StartTraceW`。而 `ControlTraceW` 返回时会把
  被停会话的属性写回这块缓冲（含 `LoggerNameOffset` / `LogFileNameOffset`
  与一堆 out 字段），重试用的已不是原始参数。
- **后果**：正对着 PLAN §9 验收项 #8（崩溃残留清理）。行为不确定，
  真机上可能表现为第二次启动莫名失败或 session 名异常。
- **修法**：STOP 用一块独立缓冲，重试前重建 `s.propsBuf`：

  ```go
  if errCode == errAlreadyExists {
      stopBuf := buildPropertiesBuffer(s.nameUTF16)
      _ = controlTraceW(0, namePtr, (*eventTraceProperties)(unsafe.Pointer(&stopBuf[0])),
          eventTraceControlStop)
      s.propsBuf = buildPropertiesBuffer(s.nameUTF16) // 重建：上一块已被 STOP 改写
      props = (*eventTraceProperties)(unsafe.Pointer(&s.propsBuf[0]))
      errCode = startTraceW(&s.sessionHandle, namePtr, props)
  }
  ```

- **可选加固**：`ControlTraceW` 要求缓冲能同时容下 session 名与日志文件名。
  本包的实时会话没有日志文件，当前「结构体 + 名字 + 2」刚好够；
  若要照 MS 示例留余量（+1KB），`TestBuildPropertiesBuffer` 里钉死的尺寸断言要同步改。

### 2.6 【高】`Close()` 把 `c.sess` 置 nil，与采样器的 `Bytes` 抢同一个字段

> 执行 §2.2 时发现的追加缺陷，首轮评审没列出来。**已修。**

- **位置**：原 `collector.go` 的 `Close()`（`c.sess = nil`）与 `Bytes()` / `Describe()`。
- **现象**：`Close` 写 `c.sess`，采样器在另一个 goroutine 里读它。组合根虽然
  「先撤 `NetSource` 再 `Close`」，但撤下的那一刻可能已经有一次 `Bytes` 取到了
  接口值正在执行——普通的 Go 数据竞争，`-race` 下会报。
- **修法**：`sess` 在 `Load` 之后不再改写；`Close` 只翻一个 `closed atomic.Bool`，
  会话的实际停止本来就有 `sync.Once` 保证幂等。`Bytes` / `Describe` 读该标记。

### 2.7 【中】「查不到该 session」的错误码写错了：是 4201 不是 4200

> 执行期实测发现（2026-09-07，Win11 非提权）。**已修。**

- **位置**：`etw_syscall.go` 的 `errWmiInstanceIdNotFound = 4200`。
- **事实**：`ERROR_WMI_INSTANCE_NOT_FOUND` 是 **4201**；4200 是
  `ERROR_WMI_GUID_NOT_FOUND`。
- ~~**更要紧的一条实测**：`ControlTraceW(0, name, STOP)` 把会话成功停掉之后返回的也是
  4201。~~ **已订正（§2.14）**：那是控制码写反时，一次 QUERY 打在刚被停掉的会话上的结果。
  4201 仍然要当成功处理，但理由是「停一个已经不在的会话不算失败」（重复 Close、
  上次已清干净），不是「STOP 成功也返回它」。
- **修法**：常量拆成 `errWmiGuidNotFound`(4200) / `errWmiInstanceNotFound`(4201)，
  新增 `controlStopSucceeded()` 把两者与 0 一并视为成功。与 §2.3 是同一类问题
  （把成功语义当失败，制造假报警）。

### 2.8 【中】普通用户的真实降级点不是 `StartTraceW`，是 `EnableTraceEx2`

> 执行期实测发现。**已修。**

- **PLAN §4.8 的推断是错的**：那张表写「普通用户 → `StartTraceW` `ERROR_ACCESS_DENIED`」。
  实测非提权下 **`StartTraceW` 照常成功并真的建出了 session**，直到
  `EnableTraceEx2` 挂 Kernel-Network provider 才 `ERROR_ACCESS_DENIED`。
- **后果**：`translateStartError` 那段友好文案在最常见的降级场景下根本不会出现，
  用户在「实例级网络监控未启用」日志里只能看到 `win32 error 5`。
- **修法**：新增 `translateEnableError`，把 5 翻成「EnableTraceEx2 权限不足
  （消费 Kernel-Network 事件需要管理员或 Performance Log Users 组）」。
- **实测输出**（修后，非提权）：
  ```
  [降级] ETW 未启用: EnableTraceEx2 权限不足（消费 Kernel-Network 事件需要管理员或 Performance Log Users 组）
  ```

### 2.9 【高】`Load` 失败会泄漏一个系统级 ETW session

> 执行期实测发现，是 §2.8 的直接后果。**已修，并已用 `logman query -ets` 复验。**
>
> ⚠️ **根因订正（见 §2.14）**：泄漏不是「单发 STOP 不可靠」，而是
> `stop()` 里那一发根本不是 STOP——控制码写反了，它发的是 QUERY。
> 下面「机制未查明」的记述已作废；`destroySession` 的复核重试保留，
> 但它不再是修复本身，只是一道确认。

- **现象**：非提权下 `StartTraceW` 已经把 session 建出来了（§2.8），随后
  `EnableTraceEx2` 失败走清理路径。清理里那一发 `ControlTraceW(STOP)` **不可靠**：
  `logman query -ets` 里 `AsaServerProcNet` 仍是 `Running`，**且不会自行消失**
  （实测存活数分钟，直到下一次 `Load` 走 `ERROR_ALREADY_EXISTS` 分支把它收掉）。
- **为什么必须修**：ETW session 是有限系统资源（PLAN §4.2 的原话），
  「下次启动兜底」是兜底不是常规路径。而普通用户每起一次 asa-server 就会走一次这条路。
- **排查过程留档**（免得后人重走）：不是异步收尾——让进程在 STOP 后多活 2 秒、
  6 秒都没用；同一段代码在 `go test` 进程里 3 秒内就干净了，在 `go run` 出来的进程里
  就是不消失。**单发 STOP 的行为不可预测，机制未查明**，所以改成结果导向的写法。
- **修法**：新增 `destroySession(name)`——STOP 之后 **QUERY 复核**，还查得到就再来一发，
  最多 5 轮、每轮间隔 200ms（最坏 1 秒，且只发生在停止路径）。`stop()` 与残留清理
  分支都改走它。
- **验收（已跑）**：连续 4 次非提权启动 + `logman query -ets`，全部 `CLEAN`；
  其中第一次还顺带收掉了修复前遗留的那个残留（= PLAN §9 验收项 #8 的等价验证）。

### 2.10 【阻断】`ETW_BUFFER_CONTEXT` 是 4 字节不是 2，payload 长度一直读的是别的字段

> 2026-09-07 真机首次跑 `netmon etw` 后排查发现。**已修。**

- **位置**：`etw_syscall.go` 的 `etwBufferContext`（原 `ProcessorNumber uint8` +
  `LoggerId uint8`，2 字节）。
- **正确布局**：C 侧是 `union { struct { UCHAR ProcessorNumber; UCHAR Alignment; };
  USHORT ProcessorIndex; }` + `USHORT LoggerId`，共 **4 字节**。
- **后果不在指针上，在两个 USHORT 上**：`EVENT_RECORD` 里三个指针
  （88/96/104）被 8 字节对齐兜住了，所以 `UserData` / `UserContext` 一直是对的；
  但 `ExtendedDataCount` 与 `UserDataLength` 整体前移了 2 字节——
  **`UserDataLength` 实际读到的是 C 的 `ExtendedDataCount`**（这类事件恒为 0）。
  于是 `readPayloadValues` 拿到一个**长度为 0** 的 payload，每个事件都因越界判解析失败，
  计数永远不动，而日志上只看得到「事件=N 解析丢弃=N」，没有任何指向布局的线索。
- **单测为什么没拦住**：`TestFieldOffsets` 把 `rec.UserDataLength` 钉在 84，
  而 84 正是**错误布局**下的值（正确值是 86）。钉子和被钉的东西来自同一个错误认知，
  这种测试只能防回归、防不住第一次写错。现在两个偏移都按 C 重新推导过并注明了推导过程。
- **修法**：结构体补 `Alignment uint8` 并把 `LoggerId` 改成 `uint16`；
  测试改钉 `ExtendedDataCount=84` / `UserDataLength=86`。
- **新增回归**：`TestEventCallbackThunk` 直接调用 `syscall.NewCallback` 造出来的函数指针，
  用一条合成 EVENT_RECORD 走完「回调 → 分类 → 按 offset 读 payload → 落到计数」，
  断言 1500 字节确实进了 TCP 发送方向。偏移写错时这条断言必挂。
  （⚠️ 该测试必须**预置 schema**：让它走 `TdhGetEventInformation` 会因为记录是合成的
  而访问违例 `0xc0000005` 把进程带崩——真实记录来自内核，不存在这个问题。）

### 2.11 【阻断】`ProcessTrace` 的返回码被丢掉，消费侧启动即退出时毫无线索

> 同上，2026-09-07 真机暴露。**诊断已加，根因见 §2.14（控制码写反）。**
> 下面「已排除…只在 provider 启用之后出现」的推断是错的：与 provider 无关，
> 是 `Describe()` 自己把会话停掉了。本机之所以复现不出来，正是因为那次实验
> 没有调 `Describe()`。

- **现象**：真机上 `Load` 返回成功，但第一行 `Describe()` 就报「会话已终止」——
  说明 `ProcessTrace` 起来就退了，事件数恒 0。而原代码是 `_ = processTrace(...)`，
  **返回码被丢弃**，外部只能看到「没有事件」。
- **已排除**（本机非提权复现过 consumer 路径）：`EVENT_TRACE_LOGFILEW` 布局、
  六个 API 的 proc 解析、实时模式、调用约定——不挂 provider 时
  `ProcessTrace` 能正常阻塞，`CloseTrace` 后返回 0。所以问题只在
  **provider 启用、事件真的开始流动之后**才出现。
- **顺带纠正一个直觉**：`OpenTraceW` 对**不存在的 session 名**并不返回
  `INVALID_PROCESSTRACE_HANDLE`，实测返回一个正常句柄（`0x101`），
  失败要等到 `ProcessTrace` 才暴露。所以「OpenTraceW 成功」什么都不证明。
- **修法（诊断）**：`etwSession.consumerRC` 记下返回码，
  `consumerExitReason()` 把常见码翻成人话，并入 `Describe()`。
- **修法（行为）**：`startSession` 在起 goroutine 之后等 200ms，
  发现消费侧已经退出就**当场失败**并带上原因，而不是交出一个
  「会话还在、但永远收不到事件」的 Collector——后者在上层表现为
  「一切正常但计数恒为 0」，是最难查的一种。

### 2.12 【阻断】TDH 的三个调用约定全写错了，其中一个把进程直接打崩

> 2026-09-07 第二轮真机（管理员终端）暴露。**已修，且有测试兜住。**

第一轮修完之后 ETW 会话终于活下来了（`事件=1`），紧接着在回调线程上崩了：

```
Exception 0xc0000005 0x0 0x1000
asa-server/pkg/winnetetw.tdhGetEventInformation(...)
```

`0x1000` = **4096** = 我们传进去的缓冲区长度。三处都是同一类错误：

| 函数 | 错在哪 | 后果 |
| --- | --- | --- |
| `TdhGetEventInformation` | 最后一个参数是 `ULONG *BufferSize`（in/out），**按值传了 `len(buffer)`** | TDH 把 4096 当指针解引用 → 访问违例 → **整个进程崩**。回调在 ETW 原生线程上，`recover` 拦不住 |
| `TdhGetProperty` | C 侧 **7 个参数**（`BufferSize` 按值、在 `pBuffer` 之前），代码传了 8 个，多出来一个「实际写入字节数」出参——**那个参数根本不存在** | 那个变量永远是 0，调用方按 `size == 0` 判失败 ⇒ **慢路径从来没成功过**，一直被快路径掩盖 |
| `TdhGetPropertySize` | 压根没声明 | 取值前问不到属性宽度，只能靠上面那个不存在的出参 |

**顺带纠正一个此前写进注释的错误结论**：原来说「TDH 不返回所需大小，只能从
4096 开始倍增试探」——那是把 in/out 指针当值参数之后的错觉。现在按
`ERROR_INSUFFICIENT_BUFFER` 写回的 needed 一次分对。

**新增两条测试**（都在没有管理员权限、没有真实会话的情况下跑得动）：

- `TestTdhGetEventInformationDoesNotCrash`：拿合成记录调真正的 TDH。
  调用约定写错时它会以同样的方式崩掉，比任何断言都有效。
- `TestEventCallbackThunk`：直接调 `syscall.NewCallback` 造出来的函数指针，
  走完「回调 → 分类 → 按 offset 读 payload → 落到计数」，断言 1500 字节确实
  进了 TCP 发送方向（§2.10 的偏移写错时这条必挂）。

### 2.13 【订正】`EVENT_TRACE_LOGFILEW` 的生命周期：是个真 bug，但**不是**会话早退的原因

第二轮时把 `logfile` 从局部变量改成挂在 `etwSession` 上，当时归因为
「它被 GC 回收导致 `ProcessTrace` 立刻返回」。**那个归因是错的**：
第三轮定位到真正的原因是控制码写反（§2.14）——`Describe()` 里的
「QUERY」实际是 STOP，打印一行状态就把会话停了。三轮里
「会话已终止」出现的时机不同（有时在加载那行、有时在最后），
只是 `ProcessTrace` 返回与 `alive()` 检查之间的竞争，不是别的机制。

`logfile` 的改法**保留**，它本身是对的：ETW 在整个会话期间都要用这个结构体
（回调指针、LoggerName，以及往 LogfileHeader 回填），传给 Windows 的东西只要
生命周期跨越调用返回就必须有 Go 侧引用——`propsBuf` / `filterBuf` / `nameUTF16`
当初都做了，唯独它漏了。只是它是一个**潜在**缺陷，不是已观测现象的成因。

**教训**：手上同时有两三个可疑点时，「改了 A，现象变了」不等于「A 是根因」——
这轮的现象变化其实来自竞争窗口的偏移。没有独立验证就别把因果写进文档。

### 2.14 【根因】`EVENT_TRACE_CONTROL_QUERY` 与 `STOP` 写反了 —— 前面好几条结论都是它的假象

> 2026-09-07 第三轮真机后定位，**已修，并有一条用真实会话验证语义的测试兜住**。
> 正确值：`EVENT_TRACE_CONTROL_QUERY = 0`，`EVENT_TRACE_CONTROL_STOP = 1`（evntrace.h）。
> 代码里写成了 QUERY=1 / STOP=0，**两个语义完全相反的操作互换了**。

一个常量错位，制造了此前分三轮追查的一连串「怪现象」：

| 观察到的现象 | 真实原因 |
| --- | --- |
| `Describe()` 打印完状态，会话就没了（`事件=1`、`已正常停止`、随后全是「采不到」） | `stats()` 以为在 QUERY，**实际发的是 STOP**——打印一行状态把自己的会话停了 |
| `Load` 失败后 session 泄漏，`logman` 里一直 Running | `stop()` 以为在 STOP，**实际只是 QUERY**，从来没停过任何东西 |
| 「单发一次 STOP 不可靠，加了 QUERY 复核重试才好」 | 复核那一发（`QUERY`=1）才是真正的 STOP。重试循环之所以「有效」，是因为它多发了一次 |
| 「STOP 成功停掉会话之后返回的也是 4201」 | 那是一次 QUERY 打在刚被停掉的会话上 |
| `SessionActive()` 从来没拦住过任何东西 | 它以为在查，**实际会把别人正在用的会话停掉**——本该防抢占的护栏自己在抢占 |

**验证方式**：`TestControlCodeSemantics` 建一个真会话，然后
①连发两次 QUERY，会话必须还在；②发一次 STOP，之后 QUERY 必须报 4201。
把常量换回错的，这条测试当场失败并直接指出「两个控制码写反了」。
非提权也能跑（`StartTraceW` 不需要管理员）。

**教训**：一组语义相反的魔数，单测**不能钉数值**——钉子和被钉的东西出自同一个
错误认知（§2.10 的偏移也栽在这上面）。只有让真对象跑一遍才防得住。

⚠️ 本清单里 §2.9 / §2.11 / §2.13 的「根因」表述受此影响，见各节开头的订正。

### 2.5 【观察项，不阻断】`stop()` 会写正被 `ProcessTrace` 持有的句柄字段

`etw_session.go:139` 把 `&s.traceHandle` 传给阻塞中的 `ProcessTrace`，
`stop()`（`:186`）随后把该字段置为 invalid。ETW 只在调用入口读一次句柄数组，
实际不会出问题，Go 竞态检测器也看不到（写的另一侧在原生代码里）。
记在这里只为免得后人重新推一遍；真要洁癖，可以给 `ProcessTrace` 传一份局部副本。

---

## 3. 回填 PLAN 的实现偏差（文档项）

`PLAN §4.7` 写的是从 `EVENT_TRACE_LOGFILE.LogfileHeader.EventsLost`（BufferCallback 里）读丢事件；
实现改用了 `ControlTraceW(QUERY)` 的 `props.EventsLost` / `RealTimeBuffersLost`
（`etw_session.go:169-177`），且**没有设置 BufferCallback**。

这个改动**更好**（QUERY 的 offset 确定，不依赖那个「文档标注 Not used」的字段），
但 PLAN §13 的偏差清单里没有记。修完 §2 后一并回填。

---

## 4. 真机验收：真正决定成败的那一项

> **2026-09-07 第四轮进展（初步是好消息）**：`netmon etw --selftest` 判定
> `捕获正常`，其中**经真实网卡的 UDP 收发两个方向都被报出来了**
> （DNS 查询：RX 1.2 KB / TX 1.2 KB）。ARK 的游戏流量正是这条路。
> 但**回环 UDP 不上报**（回环 TCP 上报），已回填 PLAN §4.9。
> 下面这条仍然成立：**判据是对着在跑的实例看 UDP 四路分项**，自测只说明机制可用。

PLAN §10 第一条（UDP RX 完整性）是唯一可能推翻整套方案的风险，而且**比 PLAN 写得更棘手**：

- PLAN 的表述是「部分接收路径可能不触发 Event 43/59」。
- 更常见的失败形态是**事件照发、但 payload 里的 PID 归错进程**：
  Kernel-Network 的 UDP 接收常在延迟/DPC 上下文里执行，PID 可能落到
  System(4) 或当时恰好占着 CPU 的其它进程。
- **ARK 的入站游戏流量是 UDP**。一旦命中，实例的**下行曲线接近 0 而上行正常**——
  这个形态要能一眼认出来，别去怀疑解析代码。

**执行顺序建议**：先修 §2 的四项 → 跑 PLAN §9 本期项 #1/2/7/8/10 → **专门验 #3（ARK 实例的
UDP 双向）** → 结果为真再做 T1 委托接线。先接线再回头改的代价明显更大：
T1 一旦落地，`internal/webapi` 的启动日志与前端占位行为都会跟着变。

若 #3 确认 RX 归错进程，备选路线（届时在 PLAN 里追加决策，不在本文件展开）：
改挂 `Microsoft-Windows-TCPIP` provider，或退回「UDP 只算 TX、RX 明确标注不可用」。

---

## 5. 迭代检查表

| # | 项 | 类型 | 状态 |
| --- | --- | --- | --- |
| 1 | `aggregator.add` 的 map 读挪进锁内（§2.1） | 代码 | ✅ |
| 2 | 补并发单测（`add`/`get` 对撞）+ PowerShell `-race` 通过 | 测试 | ✅ 已用旧写法验证该测试能复现 |
| 3 | 会话存活判据，死会话上 `Bytes` 返回 `ok=false`（§2.2） | 代码 | ✅ |
| 4 | `ERROR_CTX_CLOSE_PENDING`(7007) 视为成功（§2.3） | 代码 | ✅ |
| 5 | 残留清理不复用 properties 缓冲（§2.4） | 代码 | ✅ |
| 5b | `Close()` 不再置 `c.sess = nil`，改用 `closed` 标记（§2.6） | 代码 | ✅ |
| 5c | WMI 错误码 4200 → 4201 + `controlStopSucceeded`（§2.7） | 代码 | ✅ |
| 5d | `EnableTraceEx2` 的权限文案（§2.8） | 代码 | ✅ |
| 5e | `destroySession` 停完复核重试，堵住失败路径的 session 泄漏（§2.9） | 代码 | ✅ 已用 `logman` 复验 |
| 6 | 丢事件计数改用 QUERY 的偏差回填 PLAN §13（§3） | 文档 | ✅ |
| 7 | PLAN §9 本期项 #1/2/10 真机跑通（**需管理员终端**） | 验收 | ☐ 本机未提权，冒烟程序已备好（§7） |
| 7b | #7/#8 session 生命周期与残留清理 | 验收 | ✅ 非提权下已等价验证（§2.9） |
| 8 | **#3：ARK 实例 UDP 双向核对**（§4，决定性） | 验收 | ☐ 执行方式见 `docs/NETMON_CLI_AND_ETW_WIRING_PLAN.md` N3 |
| 9 | T1：`procnet_windows.go` stub 改委托（PLAN §14） | 接线 | ✅ 2026-09-07 已接（提前于 #8，理由见新方案 §11.2） |

> §5 的 7/8/9 三项已并入 **`docs/NETMON_CLI_AND_ETW_WIRING_PLAN.md`**：
> 那个方案先做两条诊断命令（`netmon ebpf` / `netmon etw`），再用它们的结论
> 决定要不要接线。本清单的其余部分（§2 的九项缺陷）已全部完成。

单测从 12 个增至 **19 个**。`go build ./...` / `go vet ./...` /
`go test -race`（PowerShell）/ `GOOS=linux go build ./...` 全绿，
`pkg/procnet`、`pkg/serverinfo` 回归通过。

---

## 7. 冒烟程序（真机验收 #1/#2/#10 用）

按 PLAN §12 的裁决**不入仓库**，已放在本次会话的 scratchpad：

```
C:\Users\NIEXIA~1\AppData\Local\Temp\claude\D--golang-asa-server\
  27b5eaaa-607e-4647-8451-ef865e10cff8\scratchpad\etwsmoke\
```

它是个独立 module（`replace asa-server => D:\golang\asa-server`），
自带 TCP（反复拉 https://example.com）与 UDP（反复 DNS 查询）流量，每秒打印
`Bytes()` 的累计值与增量。**要用管理员终端跑**：

```powershell
go run . -sec 30
```

非提权下它只会走降级路径并打印权限提示（这条已跑通）。scratchpad 是会话级目录，
要长期保留就把那个目录拷到别处，或按 PLAN §12 的另一个候选做成
`//go:build windows && etwsmoke` 的集成测试。

---

## 6. 验证命令（含本机环境限制）

```powershell
go build ./...
go vet ./pkg/winnetetw/...
go test ./pkg/winnetetw/...
go test -race ./pkg/winnetetw/...     # ⚠️ 必须在 PowerShell 下跑，见下
```

```bash
GOOS=linux GOARCH=amd64 go build ./...   # 确认整包被隔离在 Windows 构建图内
```

⚠️ **`-race` 在本机只有 PowerShell 能跑**。Git Bash 下 ThreadSanitizer 启动即失败：

```
ThreadSanitizer failed to allocate 0x0000044a0000 (71958528) bytes ... (error code: 87)
FAIL    asa-server/pkg/winnetetw    0.020s
```

这是 shell 环境问题（TSan 要的大块保留地址空间在该环境下拿不到），**不是代码缺陷**，
换 PowerShell 同一条命令 1 秒内通过。§2.1 的验收依赖 `-race`，别在 Bash 里跑完就下结论。
不带 `-race` 的 `go build` / `go vet` / `go test` 两个 shell 都正常。

---

# 附录 Y：文件路径对照（2026-09-29）

| 文档中的路径 | 实际路径 / 现状（核对于 2026-09-29） |
|---|---|
| `pkg/winnetetw/` | 与文档一致（存在） |
| `pkg/procnet/` | 与文档一致（存在：`btfhub.go`、`procnet_other.go`） |
| `pkg/procx/` | 与文档一致（存在） |
| 顶层包 `webapi/`、`state/`、`instance/` 等 | 已全部迁入 `internal/`（同名子目录） |
| 旧顶层包 `asaserver/` | 已整体迁入 `internal/` |

# 附录 Z：合并与同步记录（2026-09-29）

本文件由 `docs/WINNET_ETW_PLAN.md` 与 `docs/WINNET_ETW_TODO.md` 于 2026-09-29 逐字物理合并而成（方案甲）；两个源文件正文未作删减或改写；指向旧文档名的引用已改指本文。
