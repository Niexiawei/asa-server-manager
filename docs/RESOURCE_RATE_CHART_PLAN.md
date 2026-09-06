# 资源使用趋势图（uPlot）方案

> 状态：**P1–P6 已落地并验证（2026-09-04）**，**P7（eBPF）已实现，真机验收未做（2026-09-06，见 §11）**
> ｜ 实现记录见 §9 ｜ 实现前审查订正见 §7.1 ｜ **SharedWorker → 专用 Worker 的返工见 §10（2026-09-06）**
> 关联文件：
> - 前端：`app/src/components/ResourceMonitor.vue`、`app/src/components/ServerResourceMonitor.vue`（顶栏弹窗）、
>   `app/src/workers/resourceWorker.js`（原 `sharedResourceWorker.js`，改专用 Worker，见 §10）、
>   `app/src/workers/serverResourceWorker.js`（**删除**，见决策 20）、
>   `app/src/views/InstanceDetail/components/InstanceOverviewTab.vue`、`app/src/router/index.js`、`app/src/App.vue`
> - 新增前端：`app/src/components/UPlotChart.vue`、`app/src/components/ResourceTrendPanel.vue`、
>   `app/src/views/ServerResourceMonitor/index.vue`（新页面）、`app/src/composables/useInstanceResourceStream.js`
> - 前端（需同步改动，易漏）：`app/src/App.vue`（导航项 + 路由高亮映射 + KeepAlive）、
>   `app/src/utils/sseAuthGate.js`（新 Worker 必须注册）
> - 后端：`internal/webapi/serverapi/serverapi.go`（`streamAllInstancesInfo`）、`pkg/serverinfo/serverinfo.go`、
>   `internal/webapi/actions.go`（采样器启停接线）、`internal/appconfig/`（新增 `linux.ebpf_btf_path`）、
>   `internal/state/state.go`（复用 Badger 存指标历史，`metrics:` 前缀）
> - 新增后端：`pkg/serverinfo/sampler.go`、`pkg/procnet/`（eBPF，仅 Linux）
> - 文档：`openapi.json`（`/api/server/all-info` 响应）、`docs/API_REFERENCE.md`、`app/CLAUDE.md`

---

## 1. 背景与目标

`ResourceMonitor.vue` / `ServerResourceMonitor.vue` 目前只用 TDesign 的环形/直线进度条展示**当前瞬时值**，
看不到「过去几分钟怎么变化的」。本方案新增一块**滚动时间序列图**（用 `uPlot` 渲染），
在同一批指标上展示随时间的变化曲线：

- 现有指标：**CPU 使用率**、**内存使用（率）**
- 后期新增：**磁盘 IOPS**、**磁盘读/写速度（B/s）**、**网络进/出速度（B/s）**

每个指标都分**服务器级（宿主机整机）**与**实例级（单个游戏进程）**两个维度。
数据仍从 `streamAllInstancesInfo`（`GET /api/server/all-info`）这一个 SSE 流返回，
不新增 SSE 端点。

### 1.1 术语定义（已确认）："变化率" = 每个时间点的采样值随时间铺开

「资源使用变化率」指的是**把每次采样到的瞬时值按时间轴铺成曲线**，**不是**一阶导数（Δ/Δt）。
参考截图就是这种：CPU / 网络 / 磁盘 IO 的采样值随时间的曲线。新增的三类指标（IOPS、B/s）
本身就是速率量，直接采样即可。

---

## 2. 指标清单与数据来源

| 指标 | 维度 | gopsutil v4 来源 | 计算方式 | 可行性 |
|---|---|---|---|---|
| CPU 使用率 % | 服务器 | `cpu.Percent(200ms, false)` | 直接取 | ✅ 已有 |
| CPU 使用率 %（进程） | 实例 | `process.Process.CPUPercent()` | 直接取 | ✅ 已有 |
| 内存使用 / 使用率 % | 服务器 | `mem.VirtualMemory()` | 直接取 | ✅ 已有 |
| 内存使用 / 使用率 %（进程 RSS） | 实例 | `process.Process.MemoryInfo()` / `MemoryPercent()` | 直接取 | ✅ 已有 |
| 磁盘读速度 B/s | 服务器 | `disk.IOCounters()` → 各物理盘 `ReadBytes`（累计） | `(cur-prev)/Δt`，汇总所有物理盘 | ✅ 新增 |
| 磁盘写速度 B/s | 服务器 | `disk.IOCounters()` → `WriteBytes` | 同上 | ✅ 新增 |
| 磁盘读 IOPS | 服务器 | `disk.IOCounters()` → `ReadCount` | `(cur-prev)/Δt` | ✅ 新增 |
| 磁盘写 IOPS | 服务器 | `disk.IOCounters()` → `WriteCount` | 同上 | ✅ 新增 |
| 网络进速度 B/s | 服务器 | `net.IOCounters(false)` → `BytesRecv`（累计） | `(cur-prev)/Δt` | ✅ 新增 |
| 网络出速度 B/s | 服务器 | `net.IOCounters(false)` → `BytesSent` | 同上 | ✅ 新增 |
| 磁盘读/写速度 B/s（进程） | 实例 | `process.Process.IOCounters()` → `ReadBytes`/`WriteBytes` | `(cur-prev)/Δt` per PID | ⚠️ 见 2.1 |
| 磁盘读/写 IOPS（进程） | 实例 | `process.Process.IOCounters()` → `ReadCount`/`WriteCount` | 同上 | ⚠️ 见 2.1 |
| 网络进/出速度 B/s（进程） | 实例 | eBPF（`github.com/cilium/ebpf`），hook TCP/UDP 收发按 tgid 聚合 | BPF map 累计字节，用户态 `(cur-prev)/Δt` | ⚠️ 仅 Linux，见 2.2 |

### 2.1 实例级磁盘 IO 的注意点

- **Windows**：`Process.IOCounters()` 底层是 `GetProcessIoCounters`，`ReadBytes`/`WriteBytes` 统计的是
  **该进程全部 I/O（文件 + 管道 + 设备）**，不只是物理磁盘；`ReadOperationCount`/`WriteOperationCount` 作为
  IOPS 近似。用作「这个实例 IO 活跃程度」的信号足够，但**不等于纯磁盘吞吐**，图例/文案要写清楚是
  「进程 I/O」。
- **Linux**：读 `/proc/<pid>/io`。`asa-server` 以 root 运行（有 `CAP_SYS_PTRACE`），即使游戏进程被降权到
  运行时用户也能读到；读失败仍按 null 降级。字段用 `read_bytes`/`write_bytes`（真正过块层的量），
  `rchar`/`wchar` 含 page cache 命中，偏大。
- 取不到时**不阻断**，该实例这几个字段返回 `null`，前端曲线断点。

### 2.2 实例级网络 IO：Linux 走 eBPF（`github.com/cilium/ebpf`），Windows 暂不提供

gopsutil 的 `Process.NetIOCounters()` 在 **Windows 上未实现**，在 **Linux 上读的是网络 namespace 级
（等价于整机）**，不是单进程。按进程精确计量只能用内核侧钩子。

**Linux 方案（新增 `pkg/procnet/` 包）**：

- **采集点（已确认：分协议 kprobe/kretprobe，不用统一 `sock_*` 挂点）**：用
  `github.com/cilium/ebpf` 加载 BPF 程序，按 `bpf_get_current_pid_tgid() >> 32`（tgid）聚合到一张
  `BPF_MAP_TYPE_HASH`（key=tgid，value=`{rx_bytes, tx_bytes}` 累计）：
  - TCP：`tcp_sendmsg` kprobe（发送字节 = `size` 参数）、`tcp_cleanup_rbuf` kprobe（接收字节 = `copied`）
  - **UDP（ARK 游戏流量是 UDP，必须覆盖）**：`udp_sendmsg` / `udpv6_sendmsg` kprobe（发送字节 = `len`）、
    `udp_recvmsg` / `udpv6_recvmsg` kretprobe（返回值 = 实际拷贝字节数，负值忽略）
  - 用 **kprobe 而非 fentry**：kernel 5.4 没有 BPF trampoline（x86 fentry 需 ≥ 5.5），kprobe 是
    5.4 上的可用挂法。
  - map 轮询即可，**不用 `BPF_MAP_TYPE_RINGBUF`**（5.8+ 才有）。
- **用户态**：`pkg/procnet` 每个采样周期把 map 里目标 tgid 的累计值读出来，
  与上一周期做差 → 该实例的 `rx_bytes_per_sec` / `tx_bytes_per_sec`；tgid 消失即清理条目
  （BPF 侧也可挂 `sched_process_exit` 自删）。
- **BPF 对象产出**：用 `bpf2go`（`go generate`）在开发机用 clang 编译 C 源为 `*.o` + 生成 Go 绑定，
  **把 `.o` 提交进仓库**并 `//go:embed`；这样常规 `go build` **不需要 clang/llvm**，只有改 BPF 源时才需。
  CO-RE + 内嵌 BTF（`github.com/cilium/ebpf/btf`），尽量不依赖目标机 kernel headers。
- **前置条件与降级（目标：kernel 5.4+）**：`asa-server` 本就以 **root** 运行，不依赖 `CAP_BPF`
  （那是 5.8+ 才有的细分权限）——root 在 5.4 上加载 kprobe BPF 没问题。仍需 BTF：优先用内核自带
  `/sys/kernel/btf/vmlinux`（`CONFIG_DEBUG_INFO_BTF=y`；Ubuntu 20.04 的 5.4 发行内核有，
  自编译精简内核可能没有）。
- **⚠️ `RLIMIT_MEMLOCK`（5.4 基线特有，别漏）**：BPF map 的 **memcg 计费是 5.11 才引入的**，
  5.4 上 map 内存走的是进程的 locked-memory 配额，默认 ulimit（常见 64KB）下 map 创建直接 `EPERM`。
  加载前必须调 `rlimit.RemoveMemlock()`（`github.com/cilium/ebpf/rlimit`）。失败同样只降级不阻断。
- **外部 BTF（已确认）**：内核无自带 BTF 时，从**配置文件指定的路径**加载外部 BTF——
  `appconfig`（`{BaseDir}/config.yaml`）新增 `linux.ebpf_btf_path`（默认空）。取值支持两种形态：
  - **单文件**：直接 `btf.LoadSpec(path)`。
  - **btfhub 目录**：指向 btfhub-archive 的本地副本，按 `uname -r` + 发行版信息拼出
    `<dir>/<distro>/<arch>/<release>.btf`（或 `.btf.tar.xz` 解一层）再 `LoadSpec`。
  加载到的 spec 喂给 `ebpf.CollectionOptions{ Programs: { KernelTypes: spec } }`；
  没配且内核也没有 → 降级。BTF 文件/目录由用户自备。
- **⚠️ 配置项怎么送达 `pkg/procnet`（本包准入标准的直接后果）**：`pkg/procnet` 既然论证了「零领域依赖」，
  就**不能 import `internal/appconfig`**。`linux.ebpf_btf_path` 必须一路传参下来：
  `appconfig` → `serverinfo.StartSampler(ctx, serverinfo.Options{BTFPath: ...})` →
  `procnet.Load(procnet.Options{BTFPath: ...})`。`pkg/serverinfo` 同理不读配置，只收参数。
- 容器 / AppArmor / lockdown 也可能禁掉 `bpf(2)`。**任一环节失败 → `pkg/procnet` 返回 `unsupported`，
  实例级网络字段整体置 `null`，宿主机网络（`net.IOCounters`）与其它指标不受影响。**
- **加载时机**：进程级单例，`serverinfo` 采样器首次需要时 lazy load；`asa-server` 退出时 `Close()`
  卸载 BPF、释放 map。

**Windows 方案**：eBPF-for-Windows 尚不覆盖此类网络计量，按进程网络需 ETW
（`Microsoft-Windows-Kernel-Network` provider）。**本方案 Windows 上实例级网络指标不提供**
（`pkg/procnet` 的 `procnet_windows.go` 直接返回 `unsupported`），留作后续独立事项。

**包归属**：`pkg/procnet/`——不认识实例/PID 文件等领域概念、零领域依赖、有自己的 load/close
生命周期但不持有领域状态，符合 `pkg/` 准入（对照 `pkg/procx`）。按平台拆
`procnet_linux.go`（cilium/ebpf 实现）/ `procnet_windows.go`（stub）/ `procnet_other.go`（stub）。

---

## 3. 后端设计

### 3.1 新增：`serverinfo` 速率采样器（推荐方案 A）

速率类指标需要「上一次累计计数 + 时间戳」，而 `streamAllInstancesInfo` 是**每个 SSE 连接一个 goroutine + ticker**，
把状态放在 handler 局部会导致：多客户端各自采样、各自 200ms 阻塞 CPU 采样、状态随连接断开丢失。

**做法**：在 `pkg/serverinfo` 增加一个进程内单例采样器：

```go
// pkg/serverinfo/sampler.go （新增）
type Rates struct {
    Host      HostRates
    ByPID     map[int32]ProcRates
    Timestamp time.Time
}

type HostRates struct {
    CPUUsedPercent    float64
    MemUsedPercent    float64
    MemUsed, MemTotal uint64
    DiskReadBytesPS   float64
    DiskWriteBytesPS  float64
    DiskReadIOPS      float64
    DiskWriteIOPS     float64
    NetRecvBytesPS    float64
    NetSentBytesPS    float64
}

type ProcRates struct {
    IOReadBytesPS  *float64 // nil = 采不到
    IOWriteBytesPS *float64
    IOReadIOPS     *float64
    IOWriteIOPS    *float64
    NetRxBytesPS   *float64 // 仅 Linux + eBPF 可用（pkg/procnet），否则 nil
    NetTxBytesPS   *float64
}

// Start 由 API server 启动时调用一次；内部 1s 采一次，保存上次累计计数，算出速率缓存。
// 内部 lazy-load pkg/procnet（eBPF）：失败只记日志，NetRx/NetTx 恒为 nil。
func StartSampler(ctx context.Context) { ... }

// Snapshot 无锁读最新一份（atomic.Pointer[Rates]）；SSE handler 只读这个，不再自己采。
func Snapshot() *Rates { ... }

// SetTrackedPIDs 由 SSE handler 每轮把「当前在跑的实例 PID」告知采样器，
// 采样器只对这些 PID 调 Process.IOCounters / 读 procnet map，并清理已退出 PID 的 prev 状态。
func SetTrackedPIDs(pids []int32) { ... }
```

要点：

- **计数回绕/重置保护**：`cur < prev` 时该指标本轮记 0，不产生负数尖峰。
- **首次采样**：prev 为空时速率记 0。
- **磁盘设备过滤（已确认）**：
  - **Windows（订正）**：`disk.IOCounters()` 返回的 key 是**盘符**（`"C:"`/`"D:"`），**不是 `PhysicalDriveN`**——
    gopsutil v4.26.8 `disk/disk_windows.go:297` 走 `GetLogicalDriveStrings` + 逐卷
    `IOCTL_DISK_PERFORMANCE`，并且已经只保留 `DRIVE_FIXED`（自动排除光驱/可移动/网络盘）。
    所以「直接全收」的结论仍成立，但理由是「gopsutil 已过滤到固定卷」，不是「它已经是物理盘」。
    同一物理盘的多个卷各返回一份，求和 ≈ 整盘吞吐，**可接受但不是精确的物理盘计数**，文案别写成「物理盘」。
    另注：该实现每次采样对每个盘符 `CreateFile` 一次句柄，采样周期不要下探到亚秒级。
  - **Linux** 只统计**顶层 block device**，判据是 **`/sys/block/<device>` 存在且其类型为 `disk`**，
    **不靠设备名是否含数字**（`nvme0n1` / `mmcblk0` 是合法整盘名，本身带数字）：
    - **纳入**：`sda`/`sdb`/`sdc`、`vda`/`vdb`、`xvda`、`nvme0n1`/`nvme1n1`、`mmcblk0`，以及其它顶层
      `disk` 类型设备。
    - **排除**：分区（`sda1`、`nvme0n1p1` …——它们在 `/sys/block/<parent>/<part>` 下，不在 `/sys/block/` 里）、
      device-mapper（`dm-*`）、loop（`loop*`）、RAM disk（`ram*`）、其它非 `disk` 类型虚拟 block device。
    - **实现**：对 `disk.IOCounters()` 返回的每个 key，判断 `/sys/block/<key>` 是否存在；存在再排除
      其 `realpath` 落在 `/sys/devices/virtual/block/` 下的（`dm-*`/`loop*`/`ram*` 都在这里）。
      等价于 `lsblk -d -n -o NAME,TYPE` 里 `TYPE == disk` 的那批，但不 shell out。
- **⚠️ 网卡过滤（订正，原方案缺失）**：**不能用 `net.IOCounters(false)`**。`pernic=false` 会把
  `/proc/net/dev` 里**所有**接口求和（gopsutil v4.26.8 `net/net_linux.go:135-140` 的 `getIOCountersAll`），
  包含 `lo`、`docker0`、`veth*`、`br-*`——本机 SSE / RCON / 反代回环流量会被算进「网络进出速度」，
  数字明显虚高。改为 **`net.IOCounters(true)` 自己筛**：排除回环（`lo`）与虚拟网卡
  （Linux 判据同磁盘：`/sys/class/net/<dev>` 的 `realpath` 落在 `/sys/devices/virtual/net/` 下），
  Windows 侧同样要确认回环适配器是否在列并排除。
- **⚠️ PID 复用防护（原方案缺失）**：prev 计数与 `process.Process` 对象若只按 `int32` PID 存，
  实例重启后 PID 被系统复用就会拿旧进程的累计值做差。`cur < prev` 兜底挡不住「新进程计数恰好更大」的情形。
  **key 必须带 `CreateTime()`**（或在 `SetTrackedPIDs` 察觉集合变化时丢弃对应条目并重新建对象）。
- **错误降级**：任一子项采集失败只记日志 + 该项置零/nil，采样器本身不退出。
- **CPU 采样（订正）**：**复用 `process.Process` 对象并不能让 `CPUPercent()` 变成瞬时值**——
  gopsutil v4.26.8 `process/process.go:365-383` 的实现是 `cput.Total() / time.Since(createTime)`，
  与对象是否复用无关，永远是「进程创建至今的平均占用」。要「两次采样之间」的值必须用
  **`p.Percent(0)`**（`process/process.go:258`），它靠对象上的 `lastCPUTimes`/`lastCPUTime` 记住上一次，
  所以**必须复用同一个 `Process` 对象**，且**首次调用返回 0**（与「首次采样速率记 0」一致）。
  量纲不变：`calculatePercent`（`process.go:335`）最后乘回 `numcpu`，仍是「单核 100%」口径，
  因此 `cpu_total_percent = cpu_percent / (核数*100) * 100` 的算法**不用改**。

#### 3.1.1 采样器契约（原方案留白，必须写死）

- **采样周期与推送周期必须对齐**：原方案「采样器 1s、SSE 2s、读最近一份」会让曲线上每个点
  只代表**最近 1 秒**的窗口，另外 1 秒的磁盘/网络字节被丢弃——这是抽样不是平均，突发 IO 会时有时无。
  **定为采样器周期 = SSE 推送周期 = 2s**；若将来要解耦，则 `Snapshot()` 改为返回**累计计数 + 时间戳**，
  由 handler 按「距上次发布的 Δt」自行求速率。
- **`SetTrackedPIDs` 的所有权与并发**：多条 SSE 连接会并发调用，语义定为**「本轮全量覆盖」**且
  以**并集 + TTL（如 3 个采样周期未再出现即淘汰）**收敛，避免两个连接互相抹掉对方的 PID。
  没有任何客户端时列表为空，采样器**只保留 host 指标的采集**（或整体转入空闲，见下条）。
- **首帧必然缺速率**：handler 是「先 `SetTrackedPIDs` 再 `Snapshot()`」，新 PID 至少要等一个采样周期
  才有 prev 可做差。**约定首帧该 PID 的 `disk_io`/`net_io` 为 `null`**，前端按断点处理（§4.3）。
- **`Snapshot()` 返回的对象一经发布即只读**：`ByPID` map 不得在发布后写入（并发读写 map 会直接崩）。
  实现上每轮**构造全新 map 再 `atomic.Pointer.Store` 整体替换**。
- **空闲策略**：无 SSE 客户端时采样器仍每 2s 打一次 `disk.IOCounters` + `net.IOCounters`。
  可接受（开销极小），但要写明是**有意为之**（保证下一个客户端接入时立刻有 prev 可做差），
  否则容易被后人「优化」成 lazy 而丢掉首帧。
- **生命周期接线点**：`StartSampler` 在 `internal/webapi/actions.go` 的 `APIServer.Start()`（约 :134）
  调一次，`APIServer.Stop()`（约 :224）里停采样并 `procnet.Close()`。
  注意 **Windows 服务模式与 GUI 模式走的不是同一条启动路径**；`internal/gui/gui.go:111` 直接调
  `serverinfo.GetCPUInfo()` 显示托盘信息，**保持原样不接采样器**。

#### 3.1.2 历史环形缓冲 + Badger 持久化（已确认）

**目的**：前端打开页面时不该是空图。后端保留最近 **30 分钟**采样，新面板挂载时一次性回填。

**适用范围（已确认）**：**host（宿主机整机）与实例级走的是同一套机制**，不做任何区分——
同一个环形缓冲、同一次 5 分钟刷盘、同一个回填接口。区别只在键前缀（`metrics:h:` / `metrics:i:`）
与回填时传不传 `instance` 参数。服务器资源监控页与顶栏弹窗因此打开即有 30 分钟内的曲线。

**内存部分（真相所在）**：

- 采样器内一个环形缓冲，**30 分钟 / 2s = 900 点**。列存：`timestamps []float64` +
  每条曲线一个 `[]float64`（host 约 10 条；每实例约 8 条）。
- 体量：host 72KB + 每实例 65KB，10 个实例也就 **< 1MB**，可忽略。
- **按实例名存，不按 PID**：实例重启后 PID 变了，曲线应当连续（中间是 null 空洞）。
- **未运行的实例照样写 null 点**，不要跳过——否则时间轴对不齐，前端还得再补一次。
- **淘汰**：某实例连续 30 分钟只有 null 就整条丢弃，避免已删除的实例常驻内存。
- 单写者（采样器 goroutine），读走 `RWMutex` 或发布不可变快照，与 §3.1.1 的约定一致。
- 存 30 分钟而渲染窗口最大 15 分钟（§4.3）：留一倍余量，将来加 30 分钟档不用动后端。

**持久化部分（每 5 分钟落 Badger）**：

- **复用现有的 `state_db` Badger 实例**（`{BaseDir}/database_file/state_db`），**不开第二个 LSM**。
  键前缀 `metrics:`，与状态记录的 `state:` 互不干扰——已核实 `internal/state` 的每一处迭代
  （`state.go:150`/`188`/`248`/`474`）都按 `state:` 前缀，不会扫到 `metrics:`。
  ⚠️ 但 `ClearStateDatabase()`（`state.go:833`）是**整目录删除**，会连带清掉指标历史；可接受，记一笔即可。
- **分块 + 拆键写**，每 5 分钟一次，写入这一段的 150 个点：
  - `metrics:h:<起始ts>` —— host 的各条曲线（~12KB）
  - `metrics:i:<实例名>:<起始ts>` —— 该实例的各条曲线（~10KB/实例）
  - 同一批放进**一个事务**；同时删除 `<起始ts>` 早于 30 分钟的旧块。
  - 拆键而不是整份 30 分钟一个 key，有三个好处：单值恒定在几十 KB、删除实例时能单独清、
    重启恢复只做一次前缀扫描。
- **⚠️ 单值必须 < 1MB**：Badger v4.9.6 `DefaultOptions` 的 `ValueThreshold = maxValueThreshold`（1MB），
  ≤1MB 的值**内联在 LSM 里**，靠正常 compaction 回收；超过 1MB 才落 value log，而
  **本仓库全局没有任何 `RunValueLogGC` 调用**（已核实），落了 vlog 就等于磁盘只增不减。
  上面的拆键方案单值几十 KB，天然满足；但编码后仍要做一次长度检查，超限只记日志并跳过该块。
- 编码用 `encoding/gob` + 一个 `schemaVersion int`；解码失败或版本不匹配**直接丢弃该块**，不做迁移。
  （Badger 默认 `Compression: Snappy`，列存的 float 数组压缩率还行，不必自己压。）
- **写入频率的实际代价**：每 5 分钟一个事务、十几个键、合计几百 KB → 每天约 288 次写、~50MB 原始写入量。
  相比「每 2s 写一行」的方案低两个数量级，也不会污染自己测的磁盘 IOPS 曲线。
- **刷盘时机**：5 分钟定时 + **优雅退出时补一次**（`APIServer.Stop()` 里，且必须在
  `state.CloseStateManager()` **之前**）。正常重启因此几乎无缝；崩溃最多丢 5 分钟，
  表现为曲线上一段 null 空洞。
- **启动恢复**：前缀扫 `metrics:`，丢弃 `ts` 早于 30 分钟或**位于未来**（时钟回拨/改过系统时间）的点，
  其余按 ts 排序灌回环形缓冲。恢复失败一律降级为空缓冲，不阻断启动。

**⚠️ `pkg/` 纯度约束**：`pkg/serverinfo` 不能 import `internal/state`。做法同 §2.2 的 BTF 路径——
在 `pkg/serverinfo` 定义一个只搬字节的接口，实现放 `internal/state`：

```go
// pkg/serverinfo：零领域概念，只是个 KV 字节搬运工
type HistoryStore interface {
    Put(key string, value []byte) error
    Scan(prefix string) (map[string][]byte, error)
    Delete(keys []string) error
}
```

分块、编码、过期裁剪的逻辑全在 `serverinfo` 侧；`internal/state` 只提供一个复用 `sm.db` 的实现。
接线仍在 `internal/webapi/actions.go` 的 `APIServer.Start()`：
`serverinfo.StartSampler(ctx, serverinfo.Options{History: statepkg.MetricsStore(), BTFPath: ...})`。

**方案 B（备选，改动更小）**：不引入采样器，在 `streamAllInstancesInfo` handler 内用局部
`map[string]prevCounters` + `prevTime` 算增量。缺点：每个 SSE 连接重复采样、CPU 瞬时值仍不准、
`streamServerInfo` 要各写一份。**建议用 A**。

### 3.2 `streamAllInstancesInfo` 载荷扩展（增量、向后兼容）

现有结构原样保留，**只新增字段**：

```jsonc
{
  "timestamp": 1730000000,
  "cpu_cores": 16,
  "running_count": 2,

  // 新增：宿主机整机指标（供「服务器级」曲线用；原来这里只有 memory.total）
  "host": {
    "cpu": { "used_percent": 8.56, "core_count": 16 },
    "memory": { "used": 12884901888, "total": 34359738368, "used_percent": 37.5 },
    "disk_io": {
      "read_bytes_per_sec": 0,
      "write_bytes_per_sec": 5242880,
      "read_iops": 0,
      "write_iops": 487
    },
    "net_io": {
      "recv_bytes_per_sec": 1048576,
      "sent_bytes_per_sec": 262144
    }
  },

  "memory": { "total": 34359738368, "total_gb": 32 },   // 保留，前端旧逻辑不动

  "instances": [
    {
      "instance": "server1",
      "running": true,
      "pid": 12345,
      "cpu_percent": 42.1,
      "cpu_total_percent": 2.6,
      "memory_used": 6442450944,
      "memory_percent": 18.7,
      "process_name": "ArkAscendedServer.exe",
      "memory_used_mb": 6144,
      "memory_used_gb": 6,

      // 新增：进程 I/O 速率（采不到则为 null）
      "disk_io": {
        "read_bytes_per_sec": 131072,
        "write_bytes_per_sec": 65536,
        "read_iops": 12,
        "write_iops": 6
      },

      // 新增：进程网络速率。Windows 恒为 null；Linux 仅在 eBPF 可用时为对象，否则 null（见 §2.2）
      "net_io": {
        "recv_bytes_per_sec": 524288,
        "sent_bytes_per_sec": 131072
      }
    }
  ]
}
```

- `streamServerInfo`（`/api/server/info`）**不动**：服务器级趋势图改由 `ServerResourceMonitor.vue`
  消费 `all-info` 的 `host` 字段（见 §4.5），`/api/server/info` 维持现状只喂旧的进度条弹窗内容。
  ⚠️ 但要记一笔：**弹窗改吃 `all-info` 后，`/api/server/info` 就没有任何前端消费者了**
  （GUI 走的是 `serverinfo` 包函数，不走 HTTP）。保留它只为兼容外部调用者，文档里要标注清楚，
  免得后人以为还在用。
- **⚠️ 实例的 CPU/内存也一并从采样器取（原方案留白）**：handler 每轮先收集在跑实例的 PID 列表，
  调 `serverinfo.SetTrackedPIDs(pids)`，再 `Snapshot()` **一次性拿到 host + 每 PID 的 cpu/mem/io**，
  handler **只做 JSON 组装**。`serverapi.go` 里现有的 `serverinfo.GetCPUInfo()`（:384）与
  `serverinfo.GetProcessInfo()`（:418）**必须从 all-info 路径上撤掉**——不撤的话：
  ①每个 SSE 连接每轮仍各自阻塞 200ms 采 CPU；②§3.1 承诺的「CPU 口径修正」根本落不了地
  （`GetProcessInfo` 内部每轮 `NewProcess` + `CPUPercent()`，仍是创建至今的平均值）。
  `pid`/`process_name` 也由采样器一并给出。
- SSE tick 保持 **2s**，采样器周期与之对齐（§3.1.1）。

### 3.2.1 新增回填接口 `GET /api/server/metrics/history`（已确认）

**为什么必须是独立 REST 接口、不能是「SSE 首帧带 backfill」**：`sharedResourceWorker` 全浏览器
只维持**一条**长连接（`sharedResourceWorker.js:127-135`）。面板在 SSE 已经跑了 5 分钟之后才挂载
并 `SUBSCRIBE`，它永远收不到「首帧」——首帧 5 分钟前就发给别人了。回填必须由每个面板自己拉。

```
GET /api/server/metrics/history?window=900&instance=<name>
    window   秒，缺省 900（15 分钟），上限 1800
    instance 可选；不传只回 host，传了额外回该实例的列
→ 200
{
  "timestamps": [1730000000, 1730000002, ...],   // 列存，不是对象数组
  "host":     { "cpu_used_percent": [...], "mem_used_percent": [...],
                "disk_read_bytes_per_sec": [...], ... },
  "instance": { "cpu_percent": [...], "memory_percent": [...],
                "disk_read_bytes_per_sec": [...], "net_recv_bytes_per_sec": [...] }  // 无则省略
}
```

- 列存而非 `[{ts, cpu, mem}, ...]`：900 点下 JSON 体积差 3~4 倍，且前端拿到就能直接喂 uPlot。
- 采不到的点是 **`null`**，不是 0（与 §4.4 的约定一致）。
- 走 `serverapi` 注册，鉴权中间件自动覆盖，无需额外处理。
- 前端时序：**先 GET 灌满缓冲 → 再 `SUBSCRIBE` 接增量**，重叠帧由 §4.3 那条
  「丢弃 `ts <= 末点`」的规则自动去掉——同一条规则正好把回填与实时流的接缝也处理了。

### 3.3 平台差异小结

| 项 | Windows | Linux |
|---|---|---|
| `disk.IOCounters` | key 是**盘符**（`"C:"`），gopsutil 已只留 `DRIVE_FIXED`，全收 | 需过滤分区/`dm-`/`loop` |
| 网络计数 | **必须 `pernic=true` 自筛**，排除回环适配器 | **必须 `pernic=true` 自筛**，排除 `lo`/`docker0`/`veth*` |
| 进程 `IOCounters` | 全量 I/O 计数，可用 | `/proc/<pid>/io`，需权限，否则 null |
| 进程网络 | 不提供（gopsutil 不支持；ETW 留作后续） | `pkg/procnet` eBPF（cilium/ebpf），前置不满足则 null |

Windows 是主平台，Linux 上采不到的项一律 null 降级，不影响启动与其它指标。
实例级 `net_io` 字段：Windows 恒为 `null`；Linux eBPF 可用时为速率对象，否则 `null`。

---

## 4. 前端设计

### 4.1 依赖

- 新增 `uplot`（`npm i uplot`，约 45KB min，无依赖）。
- `vite.config.js` 的 `manualChunks` 增加 `uplot` 独立分包（与 monaco/terminal 同级）。
- 全局引一次 `uplot/dist/uPlot.min.css`（在封装组件里 `import` 即可）。

### 4.2 封装组件 `UPlotChart.vue`

uPlot 是命令式 API，用一个薄封装收口生命周期：

- props：`title`、`series`（uPlot series 配置）、`data`（`[xs, ...ys]`）、`height`、`fmtY`（Y 轴/tooltip 格式化函数）、`palette`。
- `onMounted` 建实例；`watch(data)` 调 `u.setData(data)`（不重建）；`onUnmounted` 调 `u.destroy()`。
- 用 `ResizeObserver` 监听容器宽度，`u.setSize({ width, height })`。
- 深色/浅色：从当前主题取轴线/网格色（项目是浅色为主，先按浅色写死，留 `palette` prop）。
- 不做 tooltip 插件的话，uPlot 自带 legend 跟随光标显示数值，够用。

### 4.3 数据缓冲与刷新

- 每个消费组件维护**环形缓冲**：`timestamps: Float64Array` + 每条曲线一个 `Float64Array`，
  长度按**最大窗口**分配（15 分钟 → 2s × 450 = **450 点**）。
- **挂载时先回填**：`onMounted` 调 `GET /api/server/metrics/history`（§3.2.1）把缓冲灌满，
  **再** `SUBSCRIBE` 接增量。这样打开页面就有过去 15 分钟的曲线，不用等 6 分钟才填满。
- 每收到一条 SSE（2s）push 一个点、超 450 丢头。
- **时间窗口可切（已确认）**：分段控件 **3 / 6 / 15 分钟**（= 90 / 180 / 450 点，2s 采样）。
  切换只改「渲染时取缓冲尾部多少个点」，不动缓冲本身；默认 **6 分钟**，选择存 `localStorage`
  （key 如 `resTrend.window`），实例级与服务器级各自记忆。
- **⚠️ 时间轴必须严格递增（原方案缺失）**：uPlot 要求 x 有序。而 `streamAllInstancesInfo` 在
  **连接建立时会先发一帧 immediate**（`serverapi.go:369-371`），断线重连后这一帧的 `timestamp`
  可能与缓冲区末点**相同甚至更早**（服务端时钟为准、秒级精度）。
  **入缓冲前统一丢弃 `ts <= 上一个点的 ts` 的帧**。
- **⚠️ 实例停机时曲线是「冻结」不是「空洞」（原方案缺失）**：`all-info` 只输出在跑实例
  （`serverapi.go:399-415` 对未运行实例 `continue`），实例一停，`sharedResourceWorker` 就不再对该
  instanceId 发消息，且 `ResourceMonitor.vue:240-244` 的 watch 会直接 `stopMonitoring`。
  结果是曲线停在最后一个点、时间轴不再前进。**面板需自带一个 2s 本地定时器**：本轮没收到该实例的数据
  就补一个 `null` 点（uPlot 关掉 `spanGaps` 即渲染为断点），这样「实例已停」在图上是可见的空洞。
- **图表需要「每个 tick 都有点」**（否则时间轴不动）：
  - `sharedResourceWorker.js` 目前 `formatInstanceData` 是**每轮都 postMessage**（`CHANGE_THRESHOLD`
    常量在该 worker 里定义了但未使用）——实例级图**无需改 worker 转发频率**，只需在 `formatInstanceData`
    里**透传新增字段**（`disk_io`、`net_io`）。
  - host 数据由新增的 `SUBSCRIBE_HOST` 通道分发（§4.6 第 5 条），同样每 tick 一帧，
    `serverResourceWorker.js` 整个删除。

### 4.4 组件：`ResourceTrendPanel.vue`

- props：`scope`（`'instance'` | `'host'`）、`instanceName?`。
- 顶部一个分段控件（`t-radio-group` / `t-segmented`）切时间窗口 **3 / 6 / 15 分钟**（§4.3），
  作用于本面板所有小图。
- `scope='instance'`：复用 `ResourceMonitor.vue` 里那套 `SharedWorker` 订阅逻辑（抽成 composable
  `useInstanceResourceStream(instanceName)` 更干净），取 `cpu_percent` / `memory_percent` /
  `disk_io.*` / `net_io.*`。
- `scope='host'`：订阅同一个 `all-info` 流，取 `host.cpu.used_percent` / `host.memory.used_percent` /
  `host.disk_io.*` / `host.net_io.*`。
- 布局：纵向若干张小图（每张 `UPlotChart`，高度 ~120px）：
  1. CPU 使用率（%）
  2. 内存使用率（%）
  3. 磁盘读/写速度（B/s，双曲线）—— host & instance
  4. 磁盘读/写 IOPS（/s，双曲线）—— host & instance
  5. 网络进/出速度（B/s，双曲线）—— host 恒有；instance 仅 Linux+eBPF 可用时显示，
     否则该图渲染为「当前平台不支持按进程网络计量」占位
- 单位格式化：`formatBytesPerSec`（B/s → KiB/s → MiB/s，参考 `sharedResourceWorker.js` 里
  `formatMemory` 的 4 位有效数字风格）、IOPS 取整。
- **⚠️ CPU 图的 Y 轴不能固定 0–100（原方案缺失）**：实例的 `cpu_percent` 是**单核 100% 口径**
  （见 §3.1 的 `calculatePercent` 说明），16 核机器上能到 1600%。Y 轴用自适应上限。
  **已确认：实例 CPU 图画两条线**——`cpu_percent`（单核口径）与 `cpu_total_percent`（整机占比），
  与现有两个环形进度条一一对应，用户不至于对不上号。**先按两条线实现，样式/密度后期再调**。
  host 的 `cpu.used_percent` 才是 0–100。
- **⚠️ 采不到的值必须是 `null` 而不是 0**：`disk_io`/`net_io` 整块为 `null`（Windows 的实例网络、
  Linux 无 eBPF、`/proc/<pid>/io` 读失败）与「速率真的是 0」在图上要能区分，前者断点、后者贴地。

### 4.5 挂载位置

- **实例级（已确认）**：在 `InstanceOverviewTab.vue` 现有「资源占用」卡片**下方**追加一张
  「资源趋势」卡片（`ResourceTrendPanel` `scope="instance"`），与 `ResourceMonitor`（进度条）并存，
  一眼看瞬时 + 趋势。不新开 Tab。
- **服务器级（已确认）**：**新增一个页面**「服务器资源监控」，把 `ServerResourceMonitor.vue` 的现有
  逻辑（进度条摘要 + worker）和新增的趋势图**都搬进这个页面**。
  - 新增路由：`app/src/router/index.js` 加一条（如 `/server-resource`，Hash 模式，立即加载，
    参照现有路由——注意现在是 **10 条**（含 login/setup/profile/user-manager），不是旧文档说的 6 条）。
    新页面**不加 `meta.public`**，`router.beforeEach` 会自动纳入鉴权保护，无需额外处理。
  - 新页面 `app/src/views/ServerResourceMonitor.vue`（view，与顶栏那个 component 同名注意区分——
    可把 view 命名 `views/ServerResourceMonitor/index.vue`）：
    - 上半：原进度条摘要（CPU 环形 / 内存直线），逻辑从旧 component 平移。
    - 下半：`ResourceTrendPanel scope="host"`（CPU / 内存 / 磁盘速率 / 磁盘 IOPS / 网络速率）+
      3/6/15 分钟分段控件。
  - 数据源统一走 **`/api/server/all-info`** 的顶层 `host` 字段（`host.cpu` / `host.memory` /
    `host.disk_io` / `host.net_io`），不再连 `/api/server/info`；历史部分走
    `GET /api/server/metrics/history`（不传 `instance`）。
  - **不再使用 `serverResourceWorker.js`**：host 数据改从 `sharedResourceWorker` 的
    `SUBSCRIBE_HOST` 通道取，该文件连同其 `CHANGE_THRESHOLD` / `previousValues` 死代码一并删除
    （§4.6 第 5 条）。
  - 顶栏 `components/ServerResourceMonitor.vue`（弹窗）：主体逻辑搬到页面后，弹窗**保留迷你
    CPU sparkline + 内存 sparkline**（各一条 `UPlotChart`，无坐标轴、~40px 高，取 `host.cpu.used_percent`
    / `host.memory.used_percent` 的短窗口），点击弹窗内的按钮 `router.push('/server-resource')` 进详情页。

### 4.6 前端易漏点（审查补充）

1. **`App.vue` 有三处要一起改，只加菜单项会出 bug**：
   - `t-head-menu` 加 `t-menu-item value="server-resource"`（`App.vue:14-30`）；
   - **`watch(() => route.path)` 的 if 链**（`App.vue:123-137`）加一支，否则进页面后顶部菜单不高亮；
   - **`handleMenuClick` 的 switch**（`App.vue:139-168`）加一个 case，否则点了不跳转。
2. **新页面加入 `KeepAlive :include`（已确认）**：`App.vue:57-60` 的数组加上新页面，
   切走再回来不重建、不重新拉回填。
   ⚠️ **命名坑**：`KeepAlive` 的 `include` 匹配的是**组件名**。`<script setup>` 的组件名由**文件名**推断，
   所以 `views/ServerResourceMonitor/index.vue` 推断出来的名字是 **`index`**，写进 include 的
   `'ServerResourceMonitor'` 会**静默不生效**（不报错，只是没缓存）。两种解法二选一：
   页面里显式 `defineOptions({ name: 'ServerResourceMonitor' })`，或者别用 `index.vue` 这个文件名。
   本方案取**前者**（保留目录式布局，与 `views/InstanceDetail/index.vue` 一致）。
   另注：`App.vue:62` 的 `:key` 用的是 `route.name`，与 include 的组件名是两回事，别混。
3. **新建的 Worker 必须接鉴权 gate**：`utils/sseAuthGate.js` 的 `registerSSEWorker(target)`
   + 消息里处理 `SSE_CHECK_AUTH` → `handleSSECheckAuth(target)`（现有两个消费者
   `ResourceMonitor.vue:128,149` 与 `ServerResourceMonitor.vue:119,123` 都这么做）。
   漏掉的话，会话过期后 Worker 会一路 401 无限重连。组件卸载时对应调 `unregisterSSEWorker`。
4. **弹窗组件的字段要整体迁移**：它现在直读 `/api/server/info` 的顶层结构——
   `resourceData.cpu`（`ServerResourceMonitor.vue:9`）、`resourceData.memory`（:30,:38）、
   `getIconColor` 里的 `resourceData.cpu/memory`（:217-219）。改吃 `all-info` 后**全部变成
   `data.host.cpu` / `data.host.memory`**，模板与 `getIconColor` 都要跟着动。
5. **host 数据改走 `sharedResourceWorker` 的 `SUBSCRIBE_HOST`（已确认，`serverResourceWorker.js` 删除）**

   **理由是浏览器的连接数上限**：本服务默认 HTTPS + HTTP/2，h2 下所有请求多路复用在一条 TCP 上，
   没有这个问题；但**内网常以明文 HTTP 访问**（`--tls=false`），此时是 HTTP/1.1，
   浏览器对同一 origin 只给 **6 条并发连接**，而 SSE 是长连接、**一条占一个名额不放**。
   现有的长连接消费者已经不少：all-info（SharedWorker）、`/api/server/info`（弹窗专用 Worker）、
   实例日志、系统日志、FRP/Syncthing 状态流，再加上启停/更新期间的临时 SSE 与普通 XHR——
   开几个标签页就能把 6 条吃满，表现是**后续请求全部挂起**（不是报错，是干等，极难排查）。
   （WebSocket 不占这 6 个名额，Chrome 对 WS 另有 255/host 的限制，所以压力全在 SSE + XHR 上。）

   **改法**：
   - `sharedResourceWorker.js` 增加 `SUBSCRIBE_HOST` / `UNSUBSCRIBE_HOST`，内部维护
     `hostSubscribers: Set<port>`；每帧向它们 `postMessage({type:'HOST_UPDATE', data: data.host, timestamp})`。
     实例分发逻辑不动。
   - 顶栏弹窗与新页面都改用这个通道 → **整个浏览器（跨标签页）只剩一条 `all-info`**。
   - `serverResourceWorker.js` 连同 `CHANGE_THRESHOLD` / `previousValues` 死代码一起删除。
   - 顺手把「创建 SharedWorker + 发 INIT + `registerSSEWorker`」抽成模块级单例
     （如 `composables/useResourceStream.js`）：现在 `ResourceMonitor.vue:116-121` 的
     `sharedWorker` 变量在 `<script setup>` 内，**每个组件实例各建一个 port**。SharedWorker 本身
     按脚本 URL 去重、SSE 仍只有一条，所以不是 bug，但端口数随面板数增长没必要。
   - SharedWorker 不可用的浏览器（少数移动端）没有降级路径——与现有 `ResourceMonitor.vue` 的
     现状一致，本方案不扩大这个范围。

---

## 5. 分阶段实施

| 阶段 | 内容 | 依赖 | 可独立验收 |
|---|---|---|---|
| **P1** | `serverinfo` 采样器（§3.1）：host CPU/内存/磁盘/网络速率；`Snapshot()`/`SetTrackedPIDs()`；API server 启动接线 | — | 单元测试 + 打日志核对数值 |
| **P2** | `streamAllInstancesInfo` 载荷加 `host` 字段 + 实例 cpu/mem 改吃 Snapshot、撤掉 `GetCPUInfo`/`GetProcessInfo`（§3.2）；同步更新 `openapi.json:319` 与 `docs/API_REFERENCE.md:171`；`streamServerInfo` / `/api/server/info` 不动 | P1 | curl SSE 看 JSON |
| **P2b** | 历史环形缓冲（30 分钟，内存）+ 每 5 分钟落 Badger `metrics:` 前缀 + 启动恢复（§3.1.2）；新增 `GET /api/server/metrics/history`（§3.2.1）并登记进 `openapi.json` / `docs/API_REFERENCE.md` | P1、P2 | 重启后 curl history 看是否续得上；核对单值 < 1MB |
| **P3** | 前端 `uplot` 依赖 + `UPlotChart.vue` 封装 + `useInstanceResourceStream` composable | — | Storybook/临时页渲染假数据 |
| **P4** | `ResourceTrendPanel.vue`（`scope='instance'`）：CPU/内存两图 + **3/6/15 分钟窗口分段控件**（§4.3）+ **挂载时先 GET 回填再订阅**，挂到 `InstanceOverviewTab` 资源卡片下方 | P2b、P3 | 真机跑实例看曲线、切窗口、刷新页面看有没有历史 |
| **P5** | 实例级磁盘 I/O：采样器加 per-PID `IOCounters`（§2.1）+ 载荷 `instances[].disk_io` + 面板加两图 | P1–P4 | Windows 真机；Linux 权限场景验证降级 |
| **P6** | 新增「服务器资源监控」页（路由 + 导航入口）：平移旧 `ServerResourceMonitor.vue` 进度条逻辑 + 内嵌 `ResourceTrendPanel scope="host"`（CPU/内存/磁盘/网络/IOPS）；`sharedResourceWorker.js` 加 `SUBSCRIBE_HOST` 并删除 `serverResourceWorker.js`（§4.6-5）；页面加入 `KeepAlive`（注意 `defineOptions` 组件名，§4.6-2）；顶栏弹窗改为跳转入口 + 迷你 sparkline（§4.5） | P2b–P4 | 真机进页面看曲线；开多标签页确认明文 HTTP 下不再逼近 6 连接上限 |
| **P7** | `pkg/procnet` eBPF（§2.2）：`bpf2go` + `go generate` 工具链、BPF 源（分协议 kprobe/kretprobe，TCP+UDP，按 tgid）、`.o` 提交、平台 stub；`serverinfo` 采样器接线 `NetRx/NetTx`；载荷 `instances[].net_io`；面板加实例网络图 | P1、P5 | Linux 5.4 真机（有/无 BTF 两种）；Windows 确认恒 null 不报错 |
| **P8** | 打磨：单位格式化、深色适配、Linux 磁盘盘符过滤复核、`localStorage` 窗口记忆；更新 `app/CLAUDE.md` 的前端结构说明（它现在还写着已不存在的 `resourceMonitorWorker.js` 与 `/control` 路由） | 全部 | — |

P1–P2（后端）与 P3（前端封装）可并行。P4 只依赖 CPU/内存（P2 之前用现有 `cpu_percent`/`memory_percent` 就能做），
所以前端能很早看到效果。P7（eBPF）相对独立，可在 P4 之后任意时点插入，不阻塞其它阶段。

---

## 6. 兼容性与风险

- **向后兼容**：载荷纯增量，`sharedResourceWorker.formatInstanceData` 只加透传字段，
  旧的进度条组件（`ResourceMonitor.vue` / `ServerResourceMonitor.vue`）逻辑不动。
- **采样成本**：采样器 **2s** 一次（与 SSE 对齐，§3.1.1），`disk.IOCounters` + `net.IOCounters` +
  N 个 `Process.IOCounters`/`Percent(0)`，N = 在跑实例数（个位数），开销可忽略；
  比现在「每个 SSE 连接每 2s 一次 200ms 阻塞 CPU 采样」更省——**前提是 §3.2 说的
  `GetCPUInfo`/`GetProcessInfo` 真的从 handler 里撤掉了**，否则是净增开销。
- **Windows 优先**：Linux 上 `/proc/<pid>/io` 权限、磁盘盘符过滤、eBPF 前置条件都是已知待验证点，
  全部走 null 降级，不影响 Windows 行为与其它指标。
- **eBPF（`pkg/procnet`）专项风险**：
  - **构建工具链**：BPF C 源改动需要 `clang/llvm` + `bpf2go`（`go generate`）；产出 `.o` 提交进仓库后
    常规 `go build` 不受影响。CI 若要校验 BPF 编译需额外装 clang。
    ⚠️ 工具链的落点要定：`bpf2go` 装在哪（`tools.go` 还是 Makefile 目标）、`go:generate` 指令写在哪个文件。
  - **目标架构：只出 amd64（已确认）**。`bpf2go` 产出按 GOARCH 分文件（`*_bpfel.go` + 各自的 `.o`），
    本项目**整体只支持 amd64**——ASA 专用服务器本身在 arm64 上跑不起来，所以不存在
    「主程序能跑但 eBPF 不能」的场景。arm64 构建路径下 `pkg/procnet` 走 stub 返回 `unsupported` 即可，
    不生成也不提交 arm64 产物。
  - **运行时前置**：root（已满足）、`RLIMIT_MEMLOCK` 已解除（**5.4 必需**，见 §2.2）、
    内核 BTF（`/sys/kernel/btf/vmlinux`）、非受限容器（无 lockdown/seccomp 挡 `bpf(2)`）；
    缺 BTF 可试 btfhub 外部 BTF，仍不行 → 整体 `unsupported`。
  - **内核符号漂移**：kprobe 挂的函数名/签名跨内核版本可能变（尤其 UDP 路径）；用 CO-RE 读参数、
    对每个 kprobe 的 attach 失败**单独兜底**（缺一个探针不影响其它），整体加载失败**只降级不 panic**。
    基线锁 kernel 5.4（`tcp_sendmsg`/`tcp_cleanup_rbuf`/`udp_sendmsg`/`udpv6_sendmsg`/`udp_recvmsg`/
    `udpv6_recvmsg` 在 5.4 均存在）。
  - **cilium/ebpf 依赖体积**：纯 Go、无 CGO，可接受；`go.mod` 只在 Linux 构建路径实际拉起。
  - 卸载：`asa-server` 退出必须 `procnet.Close()` 卸 BPF、释放 map，避免残留 pinned 对象。
- **历史存储（§3.1.2）**：内存 30 分钟 < 1MB；Badger 侧每 5 分钟一个事务、十几个键、几百 KB，
  每天约 288 次写。风险点只有两个，都已在 §3.1.2 处理：**单值必须 < 1MB**（否则落 vlog，
  而仓库里没人跑 `RunValueLogGC`，磁盘只增不减）、**退出时的刷盘必须排在
  `CloseStateManager()` 之前**。另：`ClearStateDatabase()` 会连指标历史一起删掉，属预期行为。
- **前端内存**：每实例每曲线一个 450 长 `Float64Array`（15 分钟窗口），可忽略；页面切走时组件卸载即释放。
- **uPlot 体积**：~45KB，独立分包，不进主 bundle。

---

## 7. 已确认决策

1. **「变化率」= 每个时间点的采样值随时间铺开的曲线**，不是一阶导数（§1.1）。
2. **时间窗口可切**：3 / 6 / 15 分钟分段控件，默认 6 分钟，选择存 `localStorage`（§4.3 / §4.4）。
3. **实例级面板挂在 Overview「资源占用」卡片下方**，不新开 Tab（§4.5）。
4. **新增「服务器资源监控」页**（`ServerControl` 页已移除）：把顶栏 `ServerResourceMonitor.vue` 现有
   逻辑与新增趋势图都搬进该页；数据源统一走 `all-info` 的 `host`；顶栏弹窗降级为跳转入口（§4.5）。
5. 实例级磁盘指标文案按「进程 I/O」措辞（Windows 语义限制，§2.1）。
6. **实例级网络用 eBPF（`github.com/cilium/ebpf`）实现**，新增 `pkg/procnet/`；仅 Linux，
   Windows 恒 `null`（§2.2）。
7. **eBPF 基线内核 5.4**：`asa-server` 以 root 运行，不依赖 `CAP_BPF`；用 kprobe（非 fentry）、
   map 轮询（非 ringbuf）（§2.2 / §6）。
8. **BPF 采集点：分协议 kprobe/kretprobe**（`tcp_sendmsg` / `tcp_cleanup_rbuf` / `udp_sendmsg` /
   `udpv6_sendmsg` / `udp_recvmsg` / `udpv6_recvmsg`），不用统一 `sock_*` 挂点（§2.2）。
9. **`/api/server/info` 与 `streamServerInfo` 不改**；服务器级新数据只走 `all-info` 的 `host`（§3.2）。

10. **无自带 BTF 时从配置文件路径加载外部 BTF**：`appconfig` 新增 `linux.ebpf_btf_path`（默认空），
    文件用户自备；仍无则降级（§2.2）。
11. **Linux 磁盘设备过滤**：只统计 `/sys/block/<dev>` 存在且类型为 `disk` 的顶层设备
    （排除分区 / `dm-*` / `loop*` / `ram*` / 其它虚拟 block device），不靠名字含数字判断（§3.1）。
12. **新页面路由 `/server-resource`**（Hash 模式立即加载）+ `App.vue` 顶部导航入口（§4.5）。
13. **顶栏弹窗保留迷你 CPU + 内存 sparkline**（各一条无轴 `UPlotChart`），弹窗内按钮跳转新页面（§4.5）。
14. **`linux.ebpf_btf_path` 支持指向 btfhub 目录**：按 `uname -r` + 发行版拼路径自动选，也兼容单文件（§2.2）。
15. **后端保留 30 分钟历史，内存环形缓冲为真相**（900 点，列存，< 1MB），前端打开页面即有历史，
    不再是空图（§3.1.2）。
16. **每 5 分钟持久化到 Badger**：复用现有 `state_db` 实例、`metrics:` 键前缀、分块拆键、
    单值 < 1MB、退出时补刷、启动时恢复并裁掉过期点（§3.1.2）。
    否决了「每 2s 写一行」的方案——写放大两个数量级，且会污染自己测的磁盘 IOPS 曲线。
17. **回填走独立接口 `GET /api/server/metrics/history`，不是 SSE 首帧**：SharedWorker 只有一条
    长连接，中途挂载的面板收不到首帧（§3.2.1）。面板顺序固定为「先 GET 回填 → 再 SUBSCRIBE」。
18. **`pkg/serverinfo` 不 import `internal/state`**：只定义搬字节的 `HistoryStore` 接口，
    Badger 实现放 `internal/state`，与 §2.2 的 BTF 路径同一手法（§3.1.2）。
19. **host 与实例共用同一套历史机制**：同一个 30 分钟环形缓冲、同一次 5 分钟刷盘、同一个回填接口，
    只有键前缀和 `instance` 参数的区别（§3.1.2）。
20. **host 数据改走 `sharedResourceWorker` 的 `SUBSCRIBE_HOST`，`serverResourceWorker.js` 删除**：
    内网明文 HTTP 场景是 HTTP/1.1，同 origin 只有 **6 条并发连接**，而 SSE 一条占一个名额不放；
    合并后整个浏览器只剩一条 `all-info`（§4.6-5）。
21. **新页面加入 `KeepAlive :include`**，并用 `defineOptions({name})` 显式命名——
    `index.vue` 推断出的组件名是 `index`，include 会静默不匹配（§4.6-2）。
22. **只支持 amd64**：ASA 专用服务器在 arm64 上跑不起来，eBPF 产物不出 arm64，
    arm64 构建走 stub（§6）。
23. **实例 CPU 图画两条线**（`cpu_percent` + `cpu_total_percent`），先实现，样式后期再调（§4.4）。

### 7.1 审查订正（2026-09-04，对照 gopsutil v4.26.8 源码核实）

以下三条是**原方案写错的事实**，照抄会写出错误实现，已就地改在对应章节：

1. **Windows 的 `disk.IOCounters()` 返回盘符不是 `PhysicalDriveN`**
   （`disk/disk_windows.go:297`，且已只留 `DRIVE_FIXED`）——§3.1 / §3.3 已订正。
2. **复用 `Process` 对象也救不了 `CPUPercent()`**：它是「创建至今平均值」
   （`process/process.go:365-383`），瞬时值必须用 `p.Percent(0)`（`process/process.go:258`）——§3.1 已订正。
   量纲不变（`calculatePercent` 乘回 `numcpu`），`cpu_total_percent` 的换算不用改。
3. **`net.IOCounters(false)` 把 `lo`/`docker0`/`veth*` 一起求和**
   （`net/net_linux.go:135-140`），本机回环流量会计进网速——改用 `pernic=true` 自筛，§3.1 / §3.3 已订正。

## 8. 仍待明确

（暂无——2026-09-04 的审查遗留项已全部定案，见决策 15–23。实现期发现新问题再补。）

原先列在这里的五条及其去向：
- 「没有历史回填」→ 决策 15–18（§3.1.2 / §3.2.1）
- 「host 走共享流还是专用 Worker」→ 决策 20（§4.6-5）
- 「新页面是否 KeepAlive」→ 决策 21（§4.6-2）
- 「eBPF 目标架构」→ 决策 22（§6）
- 「CPU 画几条线」→ 决策 23（§4.4）

---

## 9. 实现记录（2026-09-04，P1–P6）

### 9.1 落地清单

**后端**

| 文件 | 内容 |
|---|---|
| `pkg/serverinfo/sampler.go` | 单例采样器：2s 周期、host 磁盘/网络速率、每目标进程 CPU/内存/IO、`Snapshot()`/`SetTargets()`/`StartSampler()`/`StopSampler()` |
| `pkg/serverinfo/history.go` | 30 分钟环形缓冲 + 每 5 分钟分块落盘 + 启动恢复 + `GetHistory()`；`HistoryStore` 接口在此定义 |
| `pkg/serverinfo/filter_{windows,linux}.go` | 磁盘/网卡筛选（Linux 走 `/sys/block`、`/sys/class/net` 的 realpath 判据） |
| `pkg/serverinfo/netsource.go` | `NetSource` 注入点，P7 的 eBPF 实现挂这里；未注入时实例网络恒 `null` |
| `internal/state/metrics.go` | `BadgerKV`：复用 `state_db` 实例、`metrics:` 前缀的字节读写，分批删除 |
| `internal/webapi/serverapi/metrics.go` | all-info 载荷组装 + `GET /api/server/metrics/history` |
| `internal/webapi/actions.go` | `StartSampler` 接在 `InitStateManager` 之后，`StopSampler` 接在 `CloseStateManager` 之前 |

**前端**

| 文件 | 内容 |
|---|---|
| `components/UPlotChart.vue` | uPlot 薄封装：`setData` 更新、`ResizeObserver` 跟随、自绘图例、`spanGaps:false` |
| `components/ResourceTrendPanel.vue` | 趋势面板（scope=host/instance）+ 3/6/15 分钟分段控件 + `localStorage` 记忆 |
| `composables/useResourceStream.js` | Worker 单例，`subscribeInstance` / `subscribeHost` / `subscribeStatus`（原 SharedWorker 端口单例，§10 改专用 Worker） |
| `composables/useResourceTrend.js` | 缓冲：先回填后订阅、单调性守卫、断档补空点、按时间切窗 |
| `views/ServerResourceMonitor/index.vue` | 新页面（摘要 + 整机趋势），`defineOptions` 显式命名 |
| `components/ServerResourceMonitor.vue` | 顶栏弹窗改为迷你 sparkline + 跳转入口，数据源换成共享流 |
| `workers/sharedResourceWorker.js` | 新增 `SUBSCRIBE_HOST`/`HOST_UPDATE`，透传 `disk_io`/`net_io`；**2026-09-06 改名 `resourceWorker.js` 并从 SharedWorker 改专用 Worker，见 §10** |
| `workers/serverResourceWorker.js` | **已删除**（决策 20） |

### 9.2 与方案的偏差（都是实现期发现的更优解，已在代码里注释）

1. **`SetTrackedPIDs(pids)` → `SetTargets([]Target{Name,PID})`，`Rates.ByPID` → `ByName`**：
   历史必须按**实例名**索引（实例重启换 PID 后曲线要连续），采样器索性统一按名字给结果，
   handler 也省掉一次 PID→名字的反查。`Target.Name` 对 `pkg/` 而言只是个标签，不破坏零领域依赖。
2. **handler 对「快照里还没有的实例」回退到一次性 `GetProcessInfo`**：实例刚启动的那一帧，
   目标是本轮才登记的，采样器要下一周期才有值。不回退的话载荷里会缺 `cpu_percent`，
   而前端 `formatInstanceData` 对它调了 `toFixed`，会当场抛异常。
3. **前端按时间切窗，不按点数**：中间有断档（重启、断线）时按点数取会让「6 分钟」实际跨十几分钟，
   与分段控件上的字对不上。
4. **前端在断档处插入一个空点**：否则 uPlot 会把断档两端连成一条斜线，看上去像「那几分钟一直在缓慢变化」。
   ——2026-09-04 真机复现过：服务重启造成的 2 分钟空档被画成了一条直线。
5. **`.layout-card-wrapper` 会覆盖页面根节点的 `display`**：全局样式里它是 flex 列（优先级压过 scoped），
   所以新页面的两栏网格必须放在内层容器，滚动也归内层。

### 9.3 验证结果

- `go build ./...`（Windows + `GOOS=linux`）、`go vet`、`npm run build` 全部通过。
- 新增单测：`pkg/serverinfo/history_test.go`（环形裁剪、中途出现的实例 NaN 补齐、
  消失后继续补 NaN、按窗口查询、落盘→恢复往返、过期点丢弃、计数回绕、键名解析）、
  `internal/state/metrics_test.go`（前缀隔离、分批删除、nil 存储不 panic）。全绿。
- 真机（Windows，32 核）冒烟：采样器数值合理（CPU 10.9% / 磁盘写 1.7MB/s / 35 IOPS /
  网络约 18KB/s），实例网络恒 `null`。
- 端到端：临时 BaseDir 起服务 → 浏览器打开新页面与实例详情页，5 张整机图 + 4 张实例图正常渲染，
  控制台无本功能相关报错；**重启服务后历史从 Badger 恢复成功**（曲线接得上，只在停机段留空洞）。

### 9.4 未完成

- ~~**P7（`pkg/procnet` eBPF）未开始**~~ —— **2026-09-06 已实现，见 §11**。当时留的决策
  「纯 kprobe 不需要 BTF/CO-RE，是否可以省掉决策 10/14」已定案：**仍按原方案带 BTF 支持**，
  作为将来要读内核结构体时的预留（§11.2 决策 24）。
- **P8 的深色适配**：项目当前只有浅色主题，`UPlotChart` 的轴线/网格色暂时写死，留了
  `palette` 的位置但没做主题切换。

---

## 10. 返工：SharedWorker → 专用 Worker（2026-09-06）

### 10.1 背景：SharedWorker 方案的实际问题

§4.6-5 / 决策 20 当时选了 `SharedWorker`，目标是「**整个浏览器**只有一条
`/api/server/all-info` SSE」。真机使用中暴露三个问题，合起来弊大于利：

1. **排障时看不到任何日志。** SharedWorker 运行在独立的共享上下文里，它的
   `console.log` / `console.error` **不进页面 devtools 控制台**，必须单独打开
   `chrome://inspect/#workers`（或 `about:debugging`）挂上去才看得到。表现为
   「`/api/server/all-info` 连不上，可前端控制台一条 `[ResourceStream]` 都没有」——
   排查方向完全被带偏。生产构建 `drop_console` 之后更是彻底无声。
2. **实例跨页面刷新存活，`SSE_URL` 被首个 `INIT` 钉死。** 只要还有一个标签页
   持有它，SharedWorker 就不随刷新重建；`startSSEConnection` 里 `if (!SSE_URL)`
   的幂等守卫意味着第一次拿到的 URL 永久不变。切了部署地址 / 子路径 / dev↔真机
   之后，后续所有页面都复用那个连不上的旧 URL，**刷新救不回来，只能关掉所有标签页**。
3. **少数浏览器不支持且无降级路径。** 部分移动端、某些隔离 / 策略环境没有
   `SharedWorker` 构造器，`useResourceStream.js` 里只有一句 `catch` 打日志然后
   `port = null`，资源监控直接静默失效。

跨标签页去重的收益是次要的：**默认部署是 HTTPS + HTTP/2**，多路复用下
「同 origin 6 条并发连接」的上限根本不适用；只有明文 HTTP + 多标签页才有意义，
而那是可以接受的轻微回归。

### 10.2 改动

把 `sharedResourceWorker.js` 改成与 `workers/wsWorker.js` 完全同构的**专用
（dedicated）Worker** `workers/resourceWorker.js`，`useResourceStream.js` 相应改用
`new Worker(...)`：

| 维度 | 改动 |
|---|---|
| 生命周期 | `self.onconnect` + `ports: Set` + `port.postMessage` → `self.onmessage` + `postMessage`；每标签页一个 Worker、一条 SSE |
| 订阅登记 | `subscribers: Map<instanceId, Set<port>>` / `hostSubscribers: Set<port>` → `subscribedInstances: Set<instanceId>` + `hostSubscribed: boolean`（单页面单消费者，只用来过滤要不要 `postMessage`） |
| `INIT` | 不再「只认第一次」：`payload.sseUrl` 与当前 `SSE_URL` 不同就**关旧连接、清退避、重连** —— 根治 10.1-2 的 URL 钉死 |
| `AUTH_RESUMED` | 收到后若当前无连接且未在重连，**主动拉起一次**（原实现只清标志，等下一次 CLOSED 才重连） |
| 日志 | `[ResourceWorker]` / `[ResourceStream]` 前缀的 `console.*` 现在直接进页面 devtools（Worker 线程下）；dev 可见，生产仍被 `drop_console` 去掉 |
| 协议 | `INIT` / `SUBSCRIBE` / `UNSUBSCRIBE` / `SUBSCRIBE_HOST` / `UNSUBSCRIBE_HOST` / `RESOURCE_UPDATE` / `HOST_UPDATE` / `SSE_CONNECTED` / `ERROR` / `SSE_CHECK_AUTH` / `AUTH_BLOCKED` / `AUTH_RESUMED` **保持不变**，组件侧 `subscribeInstance` / `subscribeHost` / `subscribeStatus` 签名不变 |

保留不动：指数退避 + 全抖动重连、`sseAuthGate` 鉴权闸门（`registerSSEWorker` 对
`Worker` 与 `MessagePort` 都通过 `target.postMessage` 工作）、`formatInstanceData` /
`formatMemory` / `getProgressColor`（原样搬运）、`disk_io`/`net_io` 透传。

### 10.3 顺带修掉的绕过点

`components/ResourceMonitor.vue` 此前**没走** `useResourceStream.js`，自带一份
`getSharedWorker()`（自建 SharedWorker、自发 `INIT`、自 `registerSSEWorker`），
且 `onUnmounted` 只发 `UNSUBSCRIBE`、从不 `close()`。首页 masonry 列表下每个实例
卡片一个，累积几十个僵尸 port。已改为 `subscribeInstance` / `subscribeStatus`，
`onUnmounted` 调退订函数（引用计数，最后一个订阅者走了才 `UNSUBSCRIBE`）。
顺手删掉 watch 里每 tick 打印的调试 `console.log`。

现在全部资源消费方（实例面板 `ResourceMonitor.vue`、趋势图 `ResourceTrendPanel.vue`、
顶栏弹窗 `ServerResourceMonitor.vue`、资源监控页 `views/ServerResourceMonitor/`）
统一从 `composables/useResourceStream.js` 订阅，每标签页只有一条 all-info。

### 10.4 验证

- `npm run build` 通过；`dist/assets/resourceWorker-*.js` 与 `wsWorker-*.js` 并列产出（同一打包路径）。
- 决策 20（host 数据走同一条流的 `SUBSCRIBE_HOST`、`serverResourceWorker.js` 删除）**继续有效**，本次只换了承载 Worker 的类型。

---

## 11. 实现记录（2026-09-06，P7：`pkg/procnet` eBPF）

> 状态：**代码完成，`go build`/`go vet`/`go test`（Windows + `GOOS=linux` amd64 + arm64 stub）全绿；
> Linux 5.4 真机验收未做**——本机没有 Linux 环境。Windows 侧已确认恒 `null` 且不报错。
> **Windows 的按进程网络计量（ETW）本期不实现**，方案由用户后续提供。

### 11.1 落地清单

| 文件 | 内容 |
|---|---|
| `pkg/procnet/bpf/bpf_min.h` | 自带的最小 BPF 头（~60 行）：`SEC`/`__uint`/`__type`、3 个 helper 声明、x86_64 `struct pt_regs` + `PT_REGS_PARM{1,2,3}`/`PT_REGS_RC` |
| `pkg/procnet/bpf/procnet.c` | 六个 kprobe/kretprobe + 两张 hash map（`procnet_targets` / `procnet_counters`） |
| `pkg/procnet/bpf/procnet_amd64.o` | **编译产物，已提交**（11KB，`llvm-strip -g` 去 DWARF 留 `.BTF`） |
| `pkg/procnet/procnet.go` | 无 build tag：包文档、`Options{BTFPath}`、`ErrUnsupported` |
| `pkg/procnet/btfhub.go` | 无 build tag：btfhub 候选路径构造 + `/etc/os-release` 解析（纯字符串逻辑，可在 Windows 单测） |
| `pkg/procnet/procnet_linux.go` | `linux && amd64`：`Load`/`Bytes`/`Close`/`Describe`、探针挂载、BTF 解析、TTL 淘汰、`//go:generate` |
| `pkg/procnet/procnet_windows.go` / `procnet_other.go` | stub，`Load` 恒返回 `ErrUnsupported` |
| `pkg/procnet/btfhub_test.go` | 候选顺序、空/重复发行版 ID、缺 `VERSION_ID`、os-release 引号与注释 |
| `pkg/procnet/spec_linux_test.go` | 解析内嵌 `.o`，钉死两张 map 的 key/value/max_entries 与六个程序名+挂点（不加载进内核，无需 root） |
| `Makefile`（新增，仓库根） | `make bpf` / `bpf-force` / `bpf-clean`：唯一带依赖判断的重新生成入口，`CLANG=` / `LLVM_STRIP=` 可覆盖 |
| `.github/workflows/bpf.yml`（新增） | BPF 源变更时自动重新生成 `.o`、跑契约测试、把结果提交回分支 |
| `internal/webapi/procnet.go` | 组合根：`startProcNet()`（`Load` + `serverinfo.SetNetSource`）/ `stopProcNet()`（先撤 NetSource 再 `Close`） |
| `internal/webapi/actions.go` | `startProcNet()` 接在 `StartSampler` 之后，`stopProcNet()` 接在 `StopSampler` 之后 |
| `internal/appconfig/{config,validate}.go` | 新增 `linux.ebpf_btf_path`（默认空，只做去空白校验） |

**后端载荷与前端一行没改**：`instances[].net_io` 的契约、`useResourceTrend.js` 的字段、
`ResourceTrendPanel.vue` 的「当前平台不支持按进程网络计量」占位在 P5/P6 就已就位，
P7 只是让这些字段在 Linux 上真的有值。`openapi.json` / `docs/API_REFERENCE.md` 同样无需改动。

### 11.2 与方案的偏差

24. **决策 10/14 保留：仍带 BTF 支持**（用户 2026-09-06 拍板）。当前这版 BPF 程序只读
    `pt_regs` 的寄存器参数、不访问任何内核结构体字段 ⇒ 没有 CO-RE 重定位 ⇒
    **加载时并不需要目标机 BTF**，`linux.ebpf_btf_path` 事实上是为「将来要按 socket 取
    地址/端口之类」预留的。代码里三处都写明了这一点，免得后人以为它是运行前置。
25. **不用 `bpf2go`，改「clang 直接编 + `//go:embed` + `ebpf.LoadCollectionSpecFromReader`」**。
    原因是硬的：`cmd/bpf2go` 的 `main.go` 带 `//go:build !windows`，**在本项目的开发机上根本
    编不出来**（`go tool bpf2go` 报 "build constraints exclude all Go files"）。
    `go:generate` 换成两条跨平台命令（`clang -target bpf` + `llvm-strip -g`），
    产物照旧提交进仓库、常规 `go build` 不需要 clang。少掉的只是 bpf2go 生成的类型绑定，
    而这里统共两张 map、六个程序，`coll.Maps[...]` / `coll.Programs[...]` 取一次就够。
26. **不 vendor libbpf 的 `bpf_helpers.h` / `bpf_tracing.h`，也不生成 `vmlinux.h`**，改写一份
    ~60 行的 `bpf_min.h`。同样是开发机决定的：`vmlinux.h` 要从目标内核的 BTF 生成、
    `bpf_tracing.h` 的 `PT_REGS_*` 又要靠 `vmlinux.h` 给出 `struct pt_regs`，Windows 上两样都没有。
    里面每个数字都是内核 ABI（helper 编号、`BPF_MAP_TYPE_HASH`、x86_64 的寄存器保存布局），不随版本漂。
27. **新增 `procnet_targets` map：探针先按目标过滤，没命中立刻返回**（原方案没有这一层）。
    没有它的话，机器上**每个**进程的每次收发都会往 `counters` 里插条目：map（`max_entries`）迟早
    被撑满，而那时插不进去的很可能正是我们要看的游戏进程；还得另挂 `sched_process_exit` 才能淘汰。
    加了这张表之后两张 map 的条目数被限死在「被跟踪的实例数」，**内核侧不用挂退出钩子**，
    淘汰交给用户态 TTL（30s 没人问就从两张 map 一起删，扫描最多 10s 一次）。
    副作用是首次问到某个 PID 时只做登记、以 0 为基线返回（0 不是猜的：登记之前内核根本
    没为这个 tgid 计数，计数器就是从 0 开始的，所以下一轮的差值恰好是这两次之间的流量）。
    调用方那一帧仍然没有 prev、算不出速率，与 §3.1.1「首帧速率为 null」一致。
28. **UDP 收方向用 kretprobe 取返回值**，不是入口取 `len`：`udp_recvmsg` 的 `len` 是缓冲区大小
    不是实收量。顺带躲开一个坑——`udp_recvmsg` 的形参在 5.19 删掉了 `noblock`，
    取返回值不受影响。TCP 收方向同理挂 `tcp_cleanup_rbuf(sk, copied)` 而非 `tcp_recvmsg`。
29. **每个探针单独兜底，挂上一个就算可用**：内核符号跨版本会漂（尤其 UDP 路径），
    缺一条只让那部分流量不计入，六个全挂不上才返回错误。`Describe()` 把「挂上了几个/哪几个/
    BTF 从哪来」拼成一行日志，真机排障时不用猜。
30. **`.btf.tar.xz` 用 `tar -xOJf` 解到标准输出**，不引 xz 解压依赖。Go 标准库没有 xz，
    为这一个场景加一个模块不划算，而 `tar` 本来就是 Linux 侧的既有前置（`pkg/linuxdeps` 在查它）。
31. **btfhub 目录按「最精确 → 最宽松」依次试，返回的是通配模式而不是单一路径**：
    btfhub-archive 的真实布局是 `<distro>/<version>/<arch>/<release>.btf.tar.xz`，
    而 §2.2 当初写的是 `<distro>/<arch>/<release>.btf`，用户也可能只解压了自己那一份。
    与其赌一种布局，不如依次 `filepath.Glob`，最后兜底 `<dir>/*/*/<arch>/` 与 `<dir>/<release>.btf`。

### 11.3 产物的再生成（2026-09-06 补）

`procnet_amd64.o` 是**编译产物却提交进了仓库**（决策 25），于是「改了 BPF 源忘了重新生成」
是这套东西最容易出的错——而且它不会在任何构建步骤上报错，只会在真机上表现为
「曲线一直是 0」或加载失败。三个入口，参数必须一致：

| 入口 | 场景 |
|---|---|
| `go generate ./pkg/procnet/...` | Windows 开发机（本机没有 make）。指令写在 `procnet.go`（**不带 build tag 的那个文件**——`go generate` 也遵守 build tag，写进 `procnet_linux.go` 在 Windows 上连指令都扫不到） |
| `make bpf` | Linux / 有 make 的环境。带依赖判断（源比产物新才编），`CLANG=` / `LLVM_STRIP=` 可指定工具链 |
| `.github/workflows/bpf.yml` | CI 自动兜底：`pkg/procnet/bpf/*.c`、`*.h` 或 `Makefile` 变更时触发，重新生成 → 跑契约测试 → 有变化就提交回分支 |

两个设计取舍：

- **CI 是「自动生成并提交」而不是「校验字节是否一致」**。这个 `.o` 在不同版本、不同发行版的
  clang 下编出来的字节并不相同（指令选择与 BTF 编码都可能变），做成 diff 校验会在开发机
  clang 与 CI clang 版本不同时长期红着。改成 CI 生成并提交回来，CI 就是这个产物的权威构建者。
  fork 来的 PR 推不回去，那种情况改为**失败 + 把生成好的产物挂成 artifact**，并提示作者本地重跑。
- **单独一个 workflow 而不是塞进 `ci.yml`**：需要原生 `paths:` 过滤（塞进 `ci.yml` 就得自己用
  `git diff` 判断改了哪些文件，而首次推分支 `github.event.before` 全零、强推、浅克隆几种情况
  都要额外兜底，容易漏跑或空跑）；另外只有这个 job 需要 `contents: write`，单独一个文件
  就不必给 `ci.yml` 整体放宽权限。`ci.yml` 里留了一行指路注释。

`Makefile` 只为这一件事存在——本仓库的常规构建（`go build` / `npm run build`）**不需要 make**。

### 11.4 仍待验证（需要 Linux 5.4 真机）

- 六个 kprobe 能否全部挂上；`tcp_cleanup_rbuf` 在 5.4 上是否被内联（若被内联则 attach 失败，
  收方向要改挂 `tcp_recvmsg` + 取返回值）。
- `rlimit.RemoveMemlock()` 之后 map 创建是否正常（5.4 的 locked-memory 配额路径）。
- 数值口径核对：与 `nethogs` / `ss -i` 或实例自己的流量统计对一遍，确认量级正确。
- 容器 / AppArmor / lockdown 环境下的降级是否真的只是 `net_io` 为 null、不影响其它指标。
- `linux.ebpf_btf_path` 指向 btfhub 目录时的路径命中（当前无 CO-RE，命不中也不影响加载，
  所以这条只是把预留路径走通，不是阻塞项）。

---

## 12. 返工：`ResourceMonitor.vue` 进度条 → 迷你 sparkline（2026-09-06）

原来的资源占用卡片是**只有瞬时值**的两个环形进度条（CPU 单核口径 / 整机占比）+ 一条
内存直线进度条 —— 与整个方案「看的是随时间的变化」这件事是脱节的：卡片上看不出
刚才那一分钟发生过什么。改成三块迷你 sparkline（各 44px、无坐标轴），
上面一行仍是当前值，下面一条是最近 5 分钟的曲线。

| 块 | 曲线 | 说明 |
|---|---|---|
| CPU 使用率 | `cpu_percent`（单核 100% 口径） | 当前值在标题行，**整机占比与核数落到图下方的小字**（见下） |
| 内存使用 | `memory_used`（字节） | 占用率与总量作为数字放在图下方，原「内存使用 / 内存总量」那一行 info-item 撤掉 |
| **进程 I/O 速度**（新增） | `disk_read_bytes_per_sec` / `disk_write_bytes_per_sec` 两条 | 标题带 `t-tooltip` 说明口径 |

### 12.1 几个决定

- **CPU 迷你图只画一条线**，而不是像 `ResourceTrendPanel` 那样画两条（决策 23）。
  两条挤在 44px 里时，整机占比那条会贴着底边看不见 —— 16 核机器上 `cpu_percent`
  能到 1600%，同一个 Y 轴下 `cpu_total_percent` 那 5%~15% 就是一条压在轴上的直线。
  需要两条线对照的完整版在同一页下方的「资源趋势」面板里，这里把整机占比作为数字给出。
- **I/O 的 tooltip 有两层，都要**：①UPlotChart 自带的浮动 tooltip（跟随光标显示该时刻
  的读/写值，所有图都有，不用额外接）；②标题旁的 `t-tooltip` 说明**口径** ——
  按 §2.1，Windows 的 `GetProcessIoCounters` 统计的是进程**全部** I/O（文件、管道、设备），
  不等于物理磁盘吞吐，这个差别不写出来一定会被误读成「磁盘读写」。
- **采不到与速率为 0 在卡片上必须能区分**：`disk_io` 整块为 null 时标题行显示 `-`
  （不是 `0 B/s`），曲线断开。这与 §4.4 的既有约定一致。
- **内存画的是占用字节（`memory_used`）而不是占用率**，且只画一张。两者的曲线形状
  **完全一样**（总量是常数，占用率就是它除以一个常数），画两张纯属重复；绝对值更直接，
  占用率与总量作为数字放在图下方。
- **内存图的纵轴下限不取 0**（CPU 与 I/O 那两张取 0）：ARK 的 RSS 常年稳在 6~10GB
  上下缓慢漂移，0 起点会把「6.1 → 6.3GB」这种真正要看的变化压成一条直线 ——
  换句话说，原来那张 0–100 的占用率图对内存本来就是不起作用的。改成取窗口内最小值
  再往下留 25% 余量。不存在「贴着底边 = 快没内存了」的误读：绝对值与占用率都在图外的文字里。

### 12.2 顺带给 `useResourceTrend` 加的两个选项

首页 masonry 下**每个实例卡片各挂一份** `ResourceMonitor`，直接复用这个 composable
会带来两个之前不存在的问题，各加一个选项解决：

- **`enabled`**（ref，缺省视为恒 true）：不加这个门，页面一加载就会为每个**没在跑**的
  实例各发一次 `GET /api/server/metrics/history` —— 而卡片本来连资源流都不订阅。
  现在把它绑到 `isMonitoring`，与卡片自身的订阅时机一致。
  实现上停下来**不清缓冲**（实例停了再起来曲线要接着画，中间那段由 push 的断档
  规则补一个空点），并用一个代次计数器兜住「stop→start 发生在回填还没返回的那一瞬间」
  —— 只靠一个 `started` 布尔量的话，两次 start 的续段都会通过检查，于是订阅两次、
  漏掉一次退订。
- **`backfillSeconds`**（缺省 900）：迷你图只画 5 分钟，没必要按 15 分钟拉 —— 首页有
  N 个实例时这是 N 个请求的体积差。

两个选项都是**加法**，`ResourceTrendPanel.vue` 与顶栏弹窗不传即维持原行为。
