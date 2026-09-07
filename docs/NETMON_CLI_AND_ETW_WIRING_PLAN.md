# 按进程网络计量：诊断命令 + Windows 侧接线方案

> 状态：**方案待实施**（2026-09-07 起草，先文档后代码）。
> 两件事放一个方案里：
>
> 1. 新增两条 CLI 命令 `asa-server netmon ebpf` / `asa-server netmon etw`，
>    对着一个 PID 把「到底能不能采到网络流量」当场跑出来；
> 2. 执行 `docs/WINNET_ETW_PLAN.md` §14 的 **T1**：`pkg/procnet/procnet_windows.go`
>    从 stub 改为委托 `pkg/winnetetw`，Windows 侧实例级网络监控真正接通。
>
> 为什么放一起：T1 一旦接通，Windows 上 `instances[].net_io` 就从 null 变成有值，
> 而**这个值对不对目前没有任何手段可验**——面板上看到一条曲线，不能区分
> 「采对了」「采到了别人的流量」「UDP 收方向整个缺失」。先有诊断命令，
> T1 才有验收判据。反过来，命令本身也是 `WINNET_ETW_PLAN.md` §9 验收项
> #1/#2/#3/#10 的执行工具（当前只有一个不入仓库的临时冒烟程序）。
>
> 关联文档：
>
> - `docs/WINNET_ETW_PLAN.md`（ETW 实现档案，§14 是 T1 的原始详设）
> - `docs/WINNET_ETW_TODO.md`（活动清单；§4 的 UDP RX 风险是本方案要回答的问题）
> - `docs/RESOURCE_RATE_CHART_PLAN.md`（P7，上位方案；§2.2/§3.3 的「Windows 不提供」由本方案作废）

---

## 1. 现状与要解决的问题

| 平台 | 机制 | 现状 |
| --- | --- | --- |
| Linux/amd64 | `pkg/procnet`（eBPF，6 个 kprobe） | 代码完成，**真机验收从未做过**（`RESOURCE_RATE_CHART_PLAN` §11.4） |
| Windows | `pkg/winnetetw`（ETW Kernel-Network） | 包内实现 + 19 个单测完成，缺陷已清；**未接线**，`procnet_windows.go` 仍是 stub |

两边的共同问题是**没有验证手段**。要确认一条链路能采到流量，现在只能：

- Linux：起 asa-server → 起实例 → 开面板 → 看曲线。链路上任何一环出问题都长得一样。
- Windows：接线之前根本没有入口；接线之后同上。

而恰恰有一个**已知的、可能推翻整套 Windows 方案的风险**（`WINNET_ETW_TODO.md` §4）：
Kernel-Network 的 UDP 接收事件可能不触发，或者 payload 里的 PID 归错进程。
**ARK 的入站游戏流量正是 UDP**。这个问题只能靠「对着一个真在跑的 PID 看四个方向的计数」
来回答，面板上的一条聚合曲线回答不了。

---

## 2. 命令设计

### 2.1 命名与注册

```
asa-server netmon ebpf --pid <PID>      # 仅 Linux 存在
asa-server netmon etw  --pid <PID>      # 仅 Windows 存在
```

一个 `netmon` 父命令 + 两个平台各有其一的子命令。取名 `netmon` 是跟着日志里
既有的说法走（`实例级网络监控已启用：…`）。

**⚠️ 每条子命令只在自己的平台上存在，不做「在另一个平台上打印不支持」。**
这是照 `internal/actions/prefix.go` 顶部那段注释的既有裁决办的，而且这里还有一条
更硬的理由：T1 之后 Windows 上的 `procnet.Load` **就是 ETW**，如果 `netmon ebpf`
在 Windows 上还能跑，它跑出来的其实是 ETW 的结果——命令名会当场骗人。

注册方式（与 `PermsCommand` / `PrefixCommand` 同款，但父命令是跨平台的）：

| 文件 | 内容 |
| --- | --- |
| `internal/actions/netmon.go` | 无 build tag：`NetmonCommand()` 父命令、共享 flag、采样循环、输出、判定、PID 解析 |
| `internal/actions/netmon_linux.go` | `linux`：`ebpf` 子命令，`load` 闭包调 `procnet.Load` |
| `internal/actions/netmon_windows.go` | `windows`：`etw` 子命令，`load` 闭包调 `winnetetw.Load` |
| `internal/actions/netmon_other.go` | `!linux && !windows`：子命令列表为空 |

`NetmonCommand()` 进 `main.go` 的 `commonCommands`（父命令在哪都在，只是子命令表
按平台组装）；子命令表由平台文件里的 `netmonPlatformCommands()` 提供。
**父命令在没有任何子命令的平台上直接不注册**，免得出现一条只会报错的空命令。

### 2.2 两条命令共享同一套骨架

两个 Collector 的形状本来就是镜像的（`WINNET_ETW_PLAN.md` 决策 3），
于是骨架只认一个结构化接口，平台文件只负责「Load 出一个满足它的东西」：

```go
// netmon.go（无 build tag）
type netCollector interface {
	Bytes(pid int32) (rx, tx uint64, ok bool)
	Describe() string
	Close() error
}

func runNetmon(ctx context.Context, cmd *cli.Command, load func() (netCollector, error)) error
```

`*procnet.Collector` 与 `*winnetetw.Collector` **已经**满足它，不需要为此改任何一个包。
差分、输出、判定、信号处理全部只写一遍。

### 2.3 Flags

共享（定义在 `netmon.go`，两条子命令都挂）：

| Flag | 默认 | 说明 |
| --- | --- | --- |
| `--pid` | 无 | 目标 PID。与 `--instance`、`--selftest` 三选一 |
| `--instance` | 无 | 实例名，走 `procpkg.GetInstancePID(name)` 解析 PID。**必须与 `/api/server/all-info` 用的是同一个调用**（`internal/webapi/serverapi/metrics.go:34`），否则验的不是同一个进程 |
| `--selftest` | false | 不看别人，看本进程自己，并主动打出可控流量（§2.7） |
| `--seconds` | 30 | 采样总时长；`0` = 一直跑到 Ctrl+C |
| `--interval` | `2s` | 采样间隔。默认值刻意与 `serverinfo` 采样器同频，看到的数就是面板会看到的数 |

平台专属：

| Flag | 命令 | 说明 |
| --- | --- | --- |
| `--btf` | `ebpf` | 外部 BTF 路径，对应 `linux.ebpf_btf_path`；不传则取 `appconfig` 里的值 |
| `--force` | `etw` | 无视「已有 ETW 会话在跑」的护栏（§2.6） |

### 2.4 输出

```
[加载] ETW session=AsaServerProcNet, 事件=0, 解析丢弃=0, 失败schema=0, 丢事件=0, 丢实时buffer=0
[目标] PID 12345 (ArkAscendedServer.exe)，间隔 2s，共 15 轮

   轮次     累计 RX      累计 TX      RX 速率      TX 速率
   ----------------------------------------------------------
    1        0 B          0 B           -            -        ← 首轮只有基线，没有速率
    2        1.4 MB       320 KB     712 KB/s     160 KB/s
    3        2.9 MB       661 KB     735 KB/s     170 KB/s
   ...

[分项] TCP  RX 1.2 MB   TX 340 KB      （仅 etw，见 §2.8）
       UDP  RX 26.4 MB  TX 5.1 MB
[小结] 采样 15 轮 / 30s：RX +27.6 MB，TX +5.4 MB
[判定] 捕获正常
[结束] ETW session=AsaServerProcNet, 事件=184392, 解析丢弃=0, 失败schema=0, 丢事件=0, 丢实时buffer=0
```

要点：

- **首轮不打速率**，打 `-`。这不是偷懒：`Bytes` 的首次调用是登记 + 0 基线
  （`WINNET_ETW_PLAN.md` §2.2），此时没有 prev，与面板「首帧速率为 null」是同一条规则。
  打成 `0 B/s` 会让人以为「采不到」。
- **加载与结束各打一次 `Describe()`**。Linux 侧那行会告诉你挂上了几个探针、BTF 从哪来；
  Windows 侧告诉你收了多少事件、丢了多少。**曲线为零时先看这一行**。
- 采不到（`ok=false`）那一轮打 `采不到` 而不是 0，与 `null` 语义一致。

### 2.5 判定与退出码

命令的存在价值是给出**一句结论**，所以最后必须落一个判定：

| 情况 | 判定 | 退出码 |
| --- | --- | --- |
| RX 与 TX 都有增长 | `捕获正常` | 0 |
| 只有 TX 增长，RX 恒零 | `⚠️ 只采到发送方向` + 指向 `WINNET_ETW_TODO.md` §4 的 UDP RX 风险 | 3 |
| 只有 RX 增长 | `⚠️ 只采到接收方向` | 3 |
| 两个方向都没动 | `未捕获到流量` + 三条自查（进程真的空闲？`Describe()` 里事件数为零？目标 PID 对不对？） | 2 |
| `Load` 失败 | 打印错误原样（两个包的错误文案都已经是可行动的中文） | 1 |

退出码分开是为了能写进脚本；`3` 单独留给「采到了一半」，因为那正是最需要人来看的情况。

### 2.6 ⚠️ ETW 的独占性护栏（本方案最重要的一条安全设计）

`WINNET_ETW_PLAN.md` §15.2 已经写明：session 名固定 `AsaServerProcNet`，
**同一台机器同时只能有一个消费进程**。第二个进程 `Load` 会把第一个的会话停掉。

T1 接通之后这条约束的后果变严重了：**管理员在服务正跑着的时候敲一次
`netmon etw`，就会把服务的实例级网络监控打掉**，而且（按 `WINNET_ETW_TODO.md` §2.2
的修复）服务侧从此 `Bytes` 返回 `ok=false`、`net_io` 变回 null，**直到 asa-server 重启
都不会自愈**。

对策，两层：

1. **新增只读探针** `winnetetw.SessionActive() (bool, error)`：一次
   `ControlTraceW(QUERY)`，查得到就是有人在用。这是直接测量冲突本身，
   比「扫一遍进程列表找 asa-server.exe」准确得多（服务、`api` 命令、GUI
   三种形态的进程名/命令行都不一样）。
2. **`netmon etw` 在 Load 之前先问它**，查到就**拒绝执行**并打印：

   ```
   已有 ETW 会话 AsaServerProcNet 在运行，多半是 asa-server 服务或 api 进程。
   继续会把它的实例级网络监控打掉，且对方要重启才能恢复。
   确认要抢占请加 --force。
   ```

   `--force` 是给「服务没跑但残留了一个会话」这种情况留的出口。

`netmon ebpf` **不需要**这个护栏：eBPF 侧两个进程各自加载各自的程序与 map，
互不影响（代价只是重复挂一遍探针）。这个不对称是机制决定的，不是遗漏。

### 2.7 `--selftest`：不依赖任何外部进程的「能不能采到」

给一个 PID 看曲线，前提是那个进程真的在收发。`--selftest` 把这个前提也去掉：
监控本进程，自己造流量，分三段跑，**每段单独报增量**：

| 段 | 流量 | 目的 |
| --- | --- | --- |
| 1 | 回环 TCP：进程内起一个 `127.0.0.1` echo 监听，来回打 8 MB | 最容易成功的一段；不通说明机制整个没工作 |
| 2 | 回环 UDP：同上，UDP echo，来回打 2 MB | 与 1 对比即可暴露「UDP 完全没接上」 |
| 3 | 外发 UDP：对系统 DNS 做若干次查询 | 回环若不计入时的兜底；也是最贴近 ARK 的一段（真网卡 + UDP 双向） |

两个必须写进代码注释的坑：

- **⚠️ 第 3 段必须用 `net.Resolver{PreferGo: true}`**。Windows 上默认解析器会把查询
  交给系统 DNS Client 服务，UDP 包是 `svchost.exe` 发的，**本进程的计数一个字节都不会动**——
  拿这个去判定「UDP 采不到」是纯粹的误判。
- **回环是否计入尚未真机确认**。`WINNET_ETW_PLAN.md` §4.9 断言「回环流量计入」，
  但那是推断不是实测。所以第 1、2 段为零时，输出必须明确说
  「可能是回环不被计入，看第 3 段」，而不是直接判定失败。

`--selftest` 与 `--pid` / `--instance` 互斥。

### 2.8 协议分项：只在 Windows 侧做

`netmon etw` 的输出多一行 TCP/UDP 分项，`netmon ebpf` 没有。理由是对称的：

- **ETW 侧需要**：`WINNET_ETW_TODO.md` §4 的风险就是「UDP RX 可能整个缺失」，
  而 `Bytes` 返回的是聚合值。ARK 的流量以 UDP 为主，聚合 RX 偏低到底是
  「UDP RX 缺失」还是「实例本来就没人连」，聚合值分不出来。
- **eBPF 侧不需要**：那边六个探针是**分协议挂**的，`Describe()` 已经告诉你
  哪几个挂上了；挂上了就必然计数，没有「挂上了但事件不来」这种中间态。
  要拿到分项就得改 BPF 源、改 map value 布局、重新生成 `.o`——代价和收益不成比例。

实现（`pkg/winnetetw`，改动很小）：

```go
// netCounters 由 2 个计数扩到 4 个（每个被跟踪 PID 多占 16 字节，条目数本就
// 被限死在被跟踪实例数，可忽略）
type netCounters struct {
	tcpRx, tcpTx, udpRx, udpTx uint64
}

// Bytes 照旧返回聚合值（rx = tcpRx+udpRx，tx = tcpTx+udpTx），
// NetSource 契约与 procnet 的对齐关系一个字不变。
func (c *Collector) Bytes(pid int32) (rx, tx uint64, ok bool)

// BytesByProtocol 是**诊断专用**的额外出口，只有 CLI 会调。
// 不进 serverinfo.NetSource：采样器不需要分项，加进接口就等于逼 Linux 侧
// 也实现一遍（那边拿不到，只能返回零值，是更坏的谎）。
func (c *Collector) BytesByProtocol(pid int32) (v ProtoBytes, ok bool)
```

`aggregator.add` 本来就拿到了 `netKind`，改的只是往哪个字段加。

---

## 3. T1：`procnet_windows.go` 委托 `winnetetw`

### 3.1 委托层

照 `WINNET_ETW_PLAN.md` §14.2 的全文实施，**壳结构体转发而不是类型别名**
（理由见该文 §14.3）。相对原设计的两处补充：

- `Options.BTFPath` 是 Linux 概念，Windows 侧忽略——组合根无条件传参，
  不需要在调用侧做平台判断。
- 包文档要改：`pkg/procnet/procnet.go` 现在写着「Windows 的按进程网络计量要走 ETW，
  **尚未实现**」，接线后这句话就是错的。

### 3.2 零改动清单（逐一核对，不是猜的）

| 文件 | 为什么不用改 |
| --- | --- |
| `internal/webapi/procnet.go` | 组合根照旧 `procnet.Load(Options{BTFPath: …})` → 成功就 `SetNetSource`，失败就一行日志降级 |
| `internal/webapi/actions.go` | `startProcNet()` / `stopProcNet()` 的调用点与顺序不动 |
| `pkg/serverinfo/*` | `NetSource` 是结构化接口，`procnet.Collector` 照旧满足 |
| SSE 载荷 / `openapi.json` 的字段形状 | `instances[].net_io` 的结构不变，只是 Windows 上从恒 null 变为有值 |
| 前端 | `ResourceTrendPanel.vue` 的占位分支在 `net_io` 有值时自动让位（**除了那句文案，见 §4**） |

### 3.3 接线后的平台行为矩阵

| 运行形态 | 进程身份 | 结果 |
| --- | --- | --- |
| Windows 服务（`service install`） | LocalSystem | ✅ 有值 |
| `asa-server api`（管理员终端） | 提权用户 | ✅ 有值 |
| `asa-server api` / GUI（普通终端） | 普通用户 | ❌ 降级：`EnableTraceEx2 权限不足…`，`net_io` 为 null，其余指标正常 |
| Linux + eBPF 前置满足 | root | ✅ 有值 |
| Linux 缺前置 / 容器策略挡下 | — | ❌ 降级，同上 |

⚠️ 第三行是**桌面用户的默认形态**（双击 GUI、普通终端跑 `api`），不是边角情况。
`WINNET_ETW_TODO.md` §2.8 已经把这条路径的文案改成可行动的了。

---

## 4. 文档与文案同步（T3）

T1 一落地，下面这些地方的「Windows 恒 null」就全成了假话。逐个改：

| 位置 | 现在写的 | 改成 |
| --- | --- | --- |
| `pkg/procnet/procnet.go` 包文档 | 「Windows … 尚未实现」 | Windows 走 ETW（`pkg/winnetetw`），指向本方案 |
| `pkg/procnet/procnet_windows.go` | 整个文件是 stub + 「这是设计不是故障」 | 委托层（§3.1） |
| `pkg/serverinfo/netsource.go` | 「唯一的实现是 Linux 的 eBPF」 | 两个实现，平台各一 |
| `internal/webapi/serverapi/metrics.go:142` | 「Windows 恒为 null」 | 采不到才为 null，与平台无关 |
| `docs/API_REFERENCE.md:201` | 「Windows 恒 null」 | 两平台都可能有值，前置不满足才 null |
| `openapi.json` `net_io.description` | `Always null on Windows` | 同上 |
| `docs/RESOURCE_RATE_CHART_PLAN.md` §2.2 / §3.3 / §11 | 「Windows 暂不提供」 | 加一条回填，指向 `WINNET_ETW_PLAN.md` 与本方案 |
| `app/src/components/ResourceTrendPanel.vue:58` | 「当前平台不支持按进程网络计量（需 Linux + eBPF）」 | **不能再提平台**：改成「未启用按进程网络计量（需管理员权限或内核支持），整机网络请看服务器资源监控页」 |
| `CLAUDE.md` 的 `pkg/procnet` 条目 | 「Windows（要走 ETW，未实现）… 恒 null 是设计」 | 两个实现 + 各自的降级条件 |

⚠️ **`CLAUDE.md` 这条是对既有裁决的修改**：`WINNET_ETW_PLAN.md` §12 曾裁决
「`AGENTS.md` / `CLAUDE.md` / `app/CLAUDE.md` 一律不同步」。那条裁决的前提是
「本期只做包内实现，透出是另一个计划」——现在做的就是那个计划，前提消失。
一个每次会话都被读进上下文的文件里留着一句错的平台结论，代价比同步大。

前端那句文案是**唯一一处会被用户直接看到的**，所以单列：现在的写法把
「不支持」焊死在平台上，接线之后 Windows 普通用户看到它会以为是自己搞错了。

---

## 5. 文件清单

| 文件 | 状态 | 内容 |
| --- | --- | --- |
| `internal/actions/netmon.go` | 新增 | 父命令、共享 flag、`netCollector`、采样循环、判定、`--selftest` 三段流量 |
| `internal/actions/netmon_linux.go` | 新增 | `ebpf` 子命令 + `--btf` |
| `internal/actions/netmon_windows.go` | 新增 | `etw` 子命令 + `--force` + 独占护栏 |
| `internal/actions/netmon_other.go` | 新增 | 空子命令表 |
| `internal/actions/netmon_test.go` | 新增 | 判定逻辑、humanize、PID 解析优先级（纯逻辑，无 build tag） |
| `pkg/winnetetw/collector.go` | 改 | `netCounters` 拆四路 + `BytesByProtocol` + `SessionActive()` |
| `pkg/winnetetw/winnetetw_test.go` | 改 | 分项累加的单测（现有 tracked-set 测试同步改造） |
| `pkg/procnet/procnet_windows.go` | 改 | stub → 委托 |
| `pkg/procnet/procnet.go` | 改 | 包文档一句话 |
| `main.go` | 改 | `commonCommands` 加 `actions.NetmonCommand()` |
| §4 表里的其余文件 | 改 | 文案同步 |

依赖方向（无环，`internal/actions` 本来就是组合层）：

```
internal/actions/netmon_linux.go   → pkg/procnet（eBPF 实现）
internal/actions/netmon_windows.go → pkg/winnetetw（ETW 实现，**不经 procnet**）
internal/actions/netmon.go         → internal/process（--instance 解析 PID）
internal/webapi                    → pkg/procnet → pkg/winnetetw（T1 之后）
```

⚠️ `netmon etw` **故意直连 `winnetetw` 而不走 `procnet` 门面**：命令的用途是
判断「机制本身行不行」，直连之后一旦失败就能确定问题在 ETW 层，
而不是在委托层。委托层统共 4 个转发方法、没有逻辑，绕过它不损失覆盖面。

---

## 6. 分阶段实施

| 阶段 | 内容 | 依赖 | 可独立验收 |
| --- | --- | --- | --- |
| **N1** | `pkg/winnetetw`：四路计数 + `BytesByProtocol` + `SessionActive`，单测跟上 | — | `go test -race`（PowerShell）通过；`Bytes` 的聚合值与改造前一致 |
| **N2** | `netmon` 命令骨架 + 两个平台子命令 + `--selftest` | N1 | Windows 管理员终端 `netmon etw --selftest` 三段全绿；普通终端走降级并给出权限提示 |
| **N3** | 真机诊断：对着**在跑的 ARK 实例** `netmon etw --instance <名字>` | N2 | **决定性一步**：拿到 TCP/UDP 四路分项，回答 `WINNET_ETW_TODO.md` §4 |
| **N4** | T1 委托 + §4 的全部文案同步 | N3 结论为「可用」 | 服务模式起 asa-server，实例详情页网络图渲染曲线；`logman query -ets` 在停止后无残留 |

**N3 的结论决定 N4 做不做**：若 UDP RX 确认缺失或归错进程，N4 暂缓，
改为在本方案追加一节讨论备选 provider（`Microsoft-Windows-TCPIP`），
或退化为「只报 TX，RX 明确置 null」。**不能先接线再说**——
接了线之后一条错误的曲线比没有曲线更糟，它会被当成真实数据去做容量判断。

Linux 侧（`netmon ebpf`）没有对应的阻塞项：那条链路早就接好了，
命令只是补上从没有过的验证手段，随时可跑。

---

## 7. 验收清单

| # | 平台 | 场景 | 期望 |
| --- | --- | --- | --- |
| 1 | Win | 管理员终端 `netmon etw --selftest` | 三段流量至少 TCP 回环与外发 DNS 两段有增长；判定 `捕获正常` |
| 2 | Win | 普通终端同上 | 一行权限提示，退出码 1，**不留 ETW 残留**（`logman query -ets` 干净） |
| 3 | Win | 服务在跑时执行 `netmon etw` | **拒绝执行**并提示 `--force`；服务侧的 `net_io` 不受影响 |
| 4 | Win | `netmon etw --instance <在跑的实例>` | 四路分项打印；UDP 两路都有值（**这是 N3 的判据**） |
| 5 | Win | T1 之后服务模式起 asa-server | 启动日志 `实例级网络监控已启用：ETW session=…`；实例详情页网络图有曲线 |
| 6 | Win | T1 之后停止服务 | `logman query -ets` 里无 `AsaServerProcNet` |
| 7 | Linux | root 跑 `netmon ebpf --selftest` | 判定 `捕获正常`；`Describe()` 报 6/6 探针 |
| 8 | Linux | `netmon ebpf --instance <在跑的实例>` | RX/TX 都增长，量级与 `nethogs` 对得上 |
| 9 | 双 | `--seconds 0` + Ctrl+C | 立刻退出，收尾干净（ETW 侧无残留，eBPF 侧探针卸载） |

---

## 8. 风险

| 风险 | 等级 | 对策 |
| --- | --- | --- |
| **UDP RX 缺失或 PID 归错**（`WINNET_ETW_TODO.md` §4） | 高 | 就是 N3 要回答的问题；结论为坏则 N4 不做，另议备选 provider |
| 管理员误在服务运行时跑 `netmon etw`，打掉线上监控 | 中 | §2.6 的护栏；`--force` 才能越过 |
| 回环流量不被 ETW 计入，`--selftest` 前两段假阴性 | 中 | 第 3 段外发 DNS 兜底；输出明确区分三段，不合并判定 |
| Windows 默认解析器让 DNS 走 svchost，UDP 段测了个寂寞 | 中 | 强制 `PreferGo: true`，代码注释写死原因 |
| 服务侧会话被抢后不自愈 | 低 | 当前只做护栏；自愈（`Bytes` 连续 `ok=false` 后重新 `Load`）列为后续项，见 §10 |
| `netmon` 与既有命令重名/语义撞车 | 低 | 现有命令里没有 `netmon`；父命令在无子命令的平台不注册 |

---

## 9. 已确认决策

1. **两条命令，各自只在自己的平台存在**，不做跨平台的「不支持」分支（§2.1）。
2. **`netmon etw` 直连 `pkg/winnetetw`，不走 `procnet` 门面**（§5）：命令是诊断机制的，
   要能把失败定位到 ETW 层。
3. **`netmon ebpf` 走 `procnet.Load`**：Linux 上它**就是** eBPF 实现，没有第二条路。
4. **共享骨架用结构化接口**，两个 Collector 已经满足，不改任何包（§2.2）。
5. **协议分项只在 Windows 做**（§2.8），理由是 eBPF 侧分协议挂探针、`Describe()`
   已经给出等价信息，而 ETW 侧的 UDP RX 是悬而未决的风险。
6. **`BytesByProtocol` 不进 `serverinfo.NetSource`**：采样器不需要，加进接口等于
   逼 Linux 侧返回零值——那是更坏的谎。
7. **ETW 独占护栏默认拦截**（§2.6），`--force` 是唯一出口；新增只读探针
   `SessionActive()` 而不是扫进程列表。
8. **`--interval` 默认 2s**，与 `serverinfo` 采样器同频：命令看到的数就是面板会看到的数。
9. **首轮不打速率打 `-`**（§2.4），与「首帧速率为 null」同一条规则。
10. **退出码 0/1/2/3 分开**（§2.5），`3` 专留给「只采到一个方向」。
11. **N3 的结论是 N4 的前置**（§6）：UDP 双向没验通就不接线。
12. **前端那句平台文案必须改**（§4）：它是唯一会被用户直接看到的错误结论。
13. **`CLAUDE.md` 这次要同步**，推翻 `WINNET_ETW_PLAN.md` §12 的「一律不同步」——
    那条裁决的前提（透出属于另一个计划）已经不成立（§4）。

## 10. 待裁决（实施期定）

- **会话被抢后的自愈**：组合根可以在 `Bytes` 连续 `ok=false` 若干轮后重新 `Load`。
  倾向**不做**——它会把「另一个诊断进程正在用」变成两个进程互相抢会话的循环。
  等真出现运维投诉再说。
- **`--selftest` 的回环数据量**（当前草案 TCP 8 MB / UDP 2 MB）：够不够大到
  在 2 秒采样窗口里明显高于噪声，实施时按真机调。
- **`netmon` 是否值得再加一个 `status` 子命令**（只打印 `Describe()` 与
  `SessionActive()` 就退出，不采样）。倾向做，成本几乎为零，但先看 N2 之后顺不顺手。
