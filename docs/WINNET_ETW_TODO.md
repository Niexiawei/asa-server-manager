# `pkg/winnetetw` 迭代清单（活动文档）

> 状态：**§2 的九项缺陷已全部修完并验证（2026-09-07）**；仍缺的是需要管理员终端的
> 真机验收（§5 的 7 与 8），其中 #3（ARK 实例 UDP 双向）是决定 T1 能否接线的那一项。
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
- **更要紧的一条实测**：`ControlTraceW(0, name, STOP)` 把会话**成功停掉之后返回的也是
  4201**——调用前 QUERY 得 0（在），调用后 QUERY 得 4201（没了），而 STOP 自己报 4201。
  所以这个码一律不能当失败。
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
| 9 | T1：`procnet_windows.go` stub 改委托（PLAN §14） | 接线 | ☐ 待 #8，归入上述方案的 N4 |

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
