# Windows 实例级网络监控（ETW）集成方案 —— `pkg/winnetetw`

> 状态：**本期已实现**（2026-09-06，W1–W4：包内代码 + 单测 + 双平台构建验证完成；  
> 真机冒烟验收见 §13 实现记录，#1/2/7/8/10 待真机跑）。  
> **本期范围：仅 `pkg/winnetetw` 包内实现（W1–W4），外部调用与接线不做**；  
> 后续由 `pkg/procnet` 作为统一门面透出（§5 概要 / **§14 详设**），  
> `internal/webapi` 接线一行不改；不经门面的独立使用见 **§15**。  
> **活动清单在 `docs/WINNET_ETW_TODO.md`**（评审发现的待修缺陷与验收阻塞项集中在那里；  
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
`docs/WINNET_ETW_TODO.md` §2（活动清单），这里只记结论：

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

跑冒烟程序时打脸了三处纸面推断，全部已修，详见 `docs/WINNET_ETW_TODO.md` §2.7–§2.9：

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
