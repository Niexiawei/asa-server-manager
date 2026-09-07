# 按进程网络计量：真机验证记录与操作手册

> 2026-09-07。本文件把两个平台的按进程网络计量（Windows ETW / Linux eBPF）
> 从「代码写完」到「真机跑通」这一段完整记下来，并给出**下一步实例验证的操作手册**。
>
> 与另外三份文档的分工：
>
> | 文档 | 是什么 |
> | --- | --- |
> | `docs/RESOURCE_RATE_CHART_PLAN.md` | 上位方案（P1–P7），资源趋势图整体设计 |
> | `docs/WINNET_ETW_PLAN.md` | Windows ETW 实现的设计档案（只增不改） |
> | `docs/WINNET_ETW_TODO.md` | ETW 的活动缺陷清单，逐条带修法 |
> | `docs/NETMON_CLI_AND_ETW_WIRING_PLAN.md` | 诊断命令与接线方案，含分轮实施记录 |
> | **本文件** | **验证状态 + 五轮排障的完整账 + 下一步怎么做** |
>
> 一句话现状：**两个平台的自测都已通过，实例级验证（N3）是最后一步，也是你接下来要做的。**

---

## 1. 当前状态

| 项 | Windows | Linux |
| --- | --- | --- |
| 机制 | ETW `Microsoft-Windows-Kernel-Network`（`pkg/winnetetw`） | eBPF 六个 kprobe/kretprobe（`pkg/procnet`） |
| 接线 | ✅ `procnet_windows.go` 委托 winnetetw | ✅ 早已接线 |
| 自测（`netmon --selftest`） | ✅ 捕获正常 | ✅ 捕获正常 |
| 回环 TCP | ✅ 计入 | ✅ 计入 |
| 回环 UDP | ❌ **不计入**（平台行为，非缺陷） | ✅ 计入 |
| 真实网卡 UDP 双向 | ✅ 收发都有 | ✅ 收发都有 |
| PID namespace（容器） | 不适用 | ✅ 已支持（`bpf_get_ns_current_pid_tgid`） |
| **ARK 实例端到端** | ☐ **待验证** | ☐ **待验证** |
| 前置条件不满足时 | 降级为 `net_io: null`，其余指标不受影响 | 同左 |

自测的实测数字（2026-09-07）：

```
Windows：回环 TCP 16 MB 收 / 16 MB 发；外发 DNS 1.2 KB 双向；判定 捕获正常
Linux  ：回环 TCP 16 MB / 回环 UDP 4 MB / 外发 DNS 约 400 B；判定 捕获正常
```

---

## 2. 五轮真机排障：查出来的 16 个问题

这一节是本文件的核心。**每一条都是真 bug**，不是环境问题。
按「先看到什么现象」排列，因为下次再遇到类似症状时是从现象查起的。

### 2.1 Windows（`pkg/winnetetw`）

| # | 现象 | 根因 | 为什么难查 |
| --- | --- | --- | --- |
| 1 | 打印一行状态后会话就没了，之后全是「采不到」 | **`EVENT_TRACE_CONTROL_QUERY` 与 `STOP` 写反**（正确：QUERY=0 / STOP=1） | 一个常量制造了下面 #2/#3/#4 三种完全不同的表象，追了三轮才定位 |
| 2 | `Load` 失败后 session 残留，`logman` 里一直 Running | 同 #1：`stop()` 发出去的其实是 QUERY，从来没停过任何东西 | 当时误判为「单发 STOP 不可靠」，还加了复核重试——重试之所以有效，是因为它多发的那一次才是真 STOP |
| 3 | 「STOP 成功之后也返回 4201」 | 同 #1：那是一次 QUERY 打在刚被停掉的会话上 | 写进了注释，成了错误知识 |
| 4 | 防抢占护栏从没拦住过东西 | 同 #1：`SessionActive()` 以为在查，**实际会把别人的会话停掉** | 一个本该保护线上的东西自己在破坏 |
| 5 | 回调线程上 `0xc0000005` 崩进程 | `TdhGetEventInformation` 最后一个参数是 `ULONG*`（in/out），**按值传了 `len(buffer)`**，TDH 把 4096 当指针解引用 | 异常地址 `0x1000` 就是 4096，但要先想到去看那个数 |
| 6 | 慢路径从来没成功过 | `TdhGetProperty` 是 **7 个参数**，代码传了 8 个，多出来的「实际写入字节数」出参根本不存在，于是那个变量恒为 0、调用方按 0 判失败 | 被快路径掩盖，正常情况下走不到 |
| 7 | 同上 | `TdhGetPropertySize` 压根没声明，取值前问不到宽度 | — |
| 8 | 事件收到了但计数永远不动 | `ETW_BUFFER_CONTEXT` 是 **4 字节**（写成了 2），导致 `UserDataLength` 读到的其实是 `ExtendedDataCount`（恒 0），payload 长度为 0、每个事件都因越界判解析失败 | 三个指针被 8 字节对齐兜住了，看起来一切正常 |
| 9 | 会话没了但上层只看到「没有事件」 | `ProcessTrace` 的返回码被 `_ =` 丢弃 | 不是 bug 的 bug：缺的是**可观测性** |
| 10 | 会话中途死掉后曲线是恒零实线 | `Bytes` 只在 `pid<=0`/已 Close 时返回采不到，会话死了仍报成功 | 恒 0 与「真的没流量」在图上一模一样 |
| 11 | 每次退出都打一行「卸载出错」 | `CloseTrace` 返回 `ERROR_CTX_CLOSE_PENDING`(7007) 是**成功**语义，被当失败 | 假报警会淹没真报警 |
| 12 | 同上 | 「查不到该 session」是 **4201** 不是 4200（4200 是 `ERROR_WMI_GUID_NOT_FOUND`） | 差一 |
| 13 | 普通用户看到的只有 `win32 error 5` | 实际降级点是 `EnableTraceEx2` 而不是 `StartTraceW`（**非提权也能建 session**），而只有后者有友好文案 | 方案 §4.8 的推断与事实相反 |
| 14 | 回调线程可能 fatal 掉整个进程 | `aggregator.add` 在锁**外**读 map，与锁内的插入/删除撞车 = `concurrent map read and map write`，那是 fatal 不是 panic，`recover` 拦不住 | 触发条件是常规路径：某 PID 首次被问到的同时它有流量 |
| 15 | 同上 | `Close()` 把 `c.sess` 置 nil，与采样器的 `Bytes` 抢同一个字段 | 组合根「先撤 NetSource 再 Close」挡不住已经取到接口值的那一次调用 |
| 16 | 「回环 TCP 采不到、回环 UDP 16 MB」 | 自测每段只等 700ms，而 ETW 会话的 **FlushTimer 是 1 秒**，第一段的事件在第二段窗口才到账 | 数字全对，只是归属错位，极易被读成「TCP 不支持」 |

另有一条**潜在**缺陷（不是已观测现象的成因）：`EVENT_TRACE_LOGFILEW` 原来是局部变量，
`startSession` 返回后无人引用，而 ETW 整个会话期间都要用它。改法保留。

### 2.2 Linux（`pkg/procnet`）

| # | 现象 | 根因 |
| --- | --- | --- |
| 17 | 探针命中数一直涨，计数却永远是 0 | `bpf_get_current_pid_tgid()` 给的是**初始 namespace** 的 tgid，容器里的 `os.Getpid()` 是命名空间内的号，两者永远匹配不上 |
| 18 | 用 `--pid` 观察别人时报「本进程不在列 ⇒ tgid 对不上」 | 诊断拿 `os.Getpid()` 去比，而不是**被跟踪的 PID**。`--selftest` 下两者恰好相同，所以一直没暴露 |

\#17 的修法是 `bpf_get_ns_current_pid_tgid`（内核 5.7+），把 tgid 换算到调用方所在的
PID namespace。因为该 helper 在 5.4 上会让整个程序**加载失败**，做成两个产物：

| 产物 | 编译参数 | 说明 |
| --- | --- | --- |
| `bpf/procnet_amd64.o` | 无 | 基础版，5.4 可用；无 namespace 的机器上完全正确 |
| `bpf/procnet_ns_amd64.o` | `-DPROCNET_NS_PID` | 命名空间感知版，需 5.7+ |

`Load` 先试后者、失败退回前者，用的是哪个会写进输出的「PID 口径」。
**两个产物必须一起重新生成**，`TestEmbeddedObjectSpec` / `TestEmbeddedNSObjectSpec`
把两个都钉住了。

### 2.3 三条方法论教训

1. **数值断言防不住「钉子和被钉的东西出自同一个错误认知」。**
   `TestFieldOffsets` 把 `UserDataLength` 钉在 84，而 84 正是**错误布局**下的值
   （正确是 86）；控制码同理，钉 `QUERY==1` 只会把错误固化。
   凡是「一组语义相反的魔数」或「按 C 布局推算的偏移」，**必须让真对象跑一遍**：
   - `TestControlCodeSemantics`：建真会话 → 连发两次 QUERY 要求会话还在 → 发 STOP 要求之后查不到；
   - `TestEventCallbackThunk`：调真回调指针 → 断言 1500 字节确实进了 TCP 发送方向；
   - `TestTdhGetEventInformationDoesNotCrash`：调真 TDH，调用约定写错时它会同样崩掉。

2. **「改了 A，现象变了」不等于「A 是根因」。**
   曾把「会话启动即退出」归因为 `EVENT_TRACE_LOGFILEW` 被 GC 回收，因为改完现象确实变了。
   真实原因是控制码写反，现象变化只是竞争窗口偏移。**没有独立验证就别把因果写进文档。**

3. **诊断能力本身是交付物。**
   这轮真正解决问题的不是某次灵光一现，是这几样东西：
   ETW 的 `ProcessTrace` 返回码、eBPF 的**探针命中次数**、**全捕获哨兵**、
   **每个 tgid 的累计字节**。没有它们，「探针没触发」「tgid 对不上」「目标进程没流量」
   三种完全不同的原因在外部长得一模一样。

---

## 3. 命令速查

```
asa-server netmon etw   [flags]     # 仅 Windows
asa-server netmon ebpf  [flags]     # 仅 Linux
```

| Flag | 默认 | 说明 |
| --- | --- | --- |
| `--pid N` | — | 观察指定 PID |
| `--instance <名字>` | — | 观察实例，PID 走实例的 pid 文件（与资源监控接口同源） |
| `--selftest` | false | 观察本进程并自己打流量：回环 TCP / 回环 UDP / 外发 DNS 三段 |
| `--seconds N` | 30 | 采样时长，`0` = 跑到 Ctrl+C |
| `--interval` | 2s | 采样间隔，默认与资源采样器同频 |
| `--force`（etw） | false | 已有 ETW 会话时也强行启动（会打掉对方的监控） |
| `--btf`（ebpf） | 配置值 | 外部 BTF 路径 |

退出码：`0` 捕获正常、`1` 加载失败、`2` 未捕获到流量、`3` 只采到一个方向。

**怎么读输出**：

- `[加载]` / `[结束]` 那两行是排障第一现场。Linux 上带探针命中次数、map 条目数、
  内核观察到的 tgid 及其字节数；Windows 上带事件数、解析丢弃、丢事件、会话是否存活。
- 首轮不打速率而打 `-`：首次 `Bytes` 是登记 + 0 基线，没有 prev 可差分，
  与面板「首帧速率为 null」是同一条规则。打成 `0 B/s` 会被误读成采不到。

---

## 4. 下一步：实例网络流量验证（N3）

**这是接线前最后一个未完成的判据。**

### 4.1 Windows

```powershell
# ⚠️ 先停掉 asa-server 服务，或接受护栏拦截：ETW 会话是系统级独占的，
#    诊断命令会把服务的实例网络监控打掉，且对方要重启才恢复。
asa-server netmon etw --instance <实例名> --seconds 60
```

**判据：UDP 两路都要有值。** ARK 的入站游戏流量是 UDP，输出里的 `[分项]` 那两行是全部答案：

```
[分项] TCP  RX ...   TX ...
       UDP  RX ...   TX ...      ← 这一行的两个数都必须动
```

三种结果怎么处理：

| 结果 | 含义 | 下一步 |
| --- | --- | --- |
| UDP RX/TX 都有值，判定 `捕获正常` | **通过**。Windows 侧可以正式启用 | 跑 §4.3 的端到端确认，然后把 N3 标记完成 |
| UDP TX 有、RX 恒零（退出码 3） | `WINNET_ETW_TODO.md` §4 那个风险成真 | **把 `procnet_windows.go` 退回 stub**，另议备选 provider（`Microsoft-Windows-TCPIP`）或退化为「只报发送方向」 |
| 两路全零（退出码 2） | 多半不是机制问题 | 先看 `[结束]` 行的事件数：为 0 说明没收到内核事件；不为 0 说明事件到了但没算到这个 PID 头上，核对 PID 是不是游戏进程本身 |

### 4.2 Linux

```bash
asa-server netmon ebpf --instance <实例名> --seconds 60
```

判据同上（Linux 没有 TCP/UDP 分项，看 RX/TX 两路），另外**与 `nethogs` 对一次量级**——
这是 `RESOURCE_RATE_CHART_PLAN.md` §11.4 剩下的最后一条待验证项。

若为零，先看 `[结束]` 行：

- 探针命中全为 0 → 内核没走到这些函数；
- 有命中、被跟踪的 PID 不在「内核观察到的 tgid」里 → 看该行「PID 口径」，
  若已是「命名空间感知」则基本可排除 PID 空间问题，转而怀疑
  **目标进程把网络活儿交给了别人**（下一节）。

### 4.3 端到端确认（两个平台都做）

1. 起 asa-server（Windows 用服务或管理员终端），启动一个实例；
2. 启动日志应出现 `实例级网络监控已启用：…`；
3. 打开实例详情页，「网络进/出速度」图应该渲染曲线而不是占位；
4. 与资源监控页的宿主机网络速率对一下量级（差值 = 回环 + 其它进程，方向一致即可）；
5. Windows 上停止服务后 `logman query -ets` 里不应再有 `AsaServerProcNet`。

### 4.4 ⚠️ 一个最容易的误判：目标进程把活儿交给了别人

真机上已经踩过一次：对着 `docker pull` 的 CLI 进程观察，结果全零。
那**不是采集漏了**——`docker` CLI 只通过 unix socket 指挥守护进程，真正下载的是 dockerd。
诊断行里字节数最大的那个 tgid 才是真正在收发的进程。

对 ARK 而言要盯的是**游戏进程本身**（`ArkAscendedServer.exe`；ArkApi 模式下是加载器
exec 之后的那个进程）。`--instance` 取的就是实例 pid 文件里的那个，
与 `/api/server/all-info` 用的是同一个调用，所以优先用 `--instance` 而不是手抄 PID。

---

## 5. 已知的平台行为差异

| 行为 | Windows | Linux |
| --- | --- | --- |
| 回环 UDP | **不计入** | 计入 |
| 回环 TCP | 计入 | 计入 |
| 采集会话 | **系统级独占**，同机只能一个消费进程；被抢后不自愈，要重启 | 各进程各挂各的探针，互不影响 |
| 权限 | 管理员 / Performance Log Users / 服务（LocalSystem） | root（BPF 加载） |
| 计数口径 | Windows 网络栈的进程数据量，≠ 网卡 wire bytes | socket 层字节数 |
| TCP/UDP 分项 | 有（诊断用） | 无（探针本就分协议挂，`Describe()` 给等价信息） |
| PID 空间 | 不适用 | 容器内需命名空间感知版（5.7+），否则采不到 |

---

## 6. 仍未验证

- **ARK 实例端到端**（§4），两个平台都缺——这是接下来要做的。
- **Linux 5.4 基线**：验证跑通的那台机器内核较新（BPF 运行统计需 5.8+），
  5.4 上的 kprobe 挂载与 memlock 路径仍未实测。
- **与 `nethogs` 的量级核对**（Linux）。
- **24 小时稳定性**：ETW 丢事件占比、内存是否有界、无 session 泄漏。
- **权限矩阵**：Windows 服务 / 管理员 / 普通用户三种形态各跑一次。
- **`linux.ebpf_btf_path` 指向 btfhub 目录**时的路径命中（当前无 CO-RE，非阻塞项）。

---

## 7. 排障速查：症状 → 先看哪一行

| 症状 | 先看 | 常见原因 |
| --- | --- | --- |
| Windows：`加载失败: EnableTraceEx2 权限不足` | — | 不是管理员。服务模式（LocalSystem）或管理员终端 |
| Windows：`已有 ETW 会话在运行` | — | asa-server 正在跑。停服务，或明知后果加 `--force` |
| Windows：`会话已终止` | 该行里 `ProcessTrace` 的返回码 | 被别的消费进程抢走；或消费侧压根没起来 |
| Windows：事件数在涨但计数为 0 | `解析丢弃` / `失败schema` | payload 解析出问题（历史上是结构体偏移） |
| Linux：`探针命中全为 0` | — | 内核没走到这些函数；换个有流量的目标先确认 |
| Linux：有命中但计数为 0 | 「PID 口径」+「内核观察到的 tgid」 | 命名空间（应显示「命名空间感知」）；或目标进程本身没走网络 |
| 两平台：曲线恒零直线而不是断点 | — | 采不到应该是 `null`；出现恒 0 实线说明 `ok` 判断有问题 |
| 首轮速率是 `-` | — | **正常**，首次调用只建基线 |
