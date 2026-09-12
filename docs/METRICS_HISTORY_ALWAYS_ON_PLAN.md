# 指标历史「无人观看也持续保留」方案

> 状态：**已实施（2026-09-12），真机验收未做（见 §6「真机」三步）**
> 关联：`docs/RESOURCE_RATE_CHART_PLAN.md` §3.1.1（采样器契约）、§3.1.2（30 分钟环形缓冲 + Badger 持久化）
> 涉及文件：`pkg/serverinfo/sampler.go`、`internal/process/process.go`、
> `internal/webapi/actions.go`、`internal/webapi/serverapi/metrics.go`

---

## 1. 现象与根因

**现象**：只要没有客户端连在 `GET /api/server/all-info` 上，30 分钟历史里**实例级曲线是空的**。
打开「服务器资源监控」页或实例详情页时，回填接口 `GET /api/server/metrics/history?instance=xxx`
只能返回从「上一次有人打开页面」那段时间的数据，之前的全是 null。

**根因链**（无人观看时逐级断掉）：

1. `SetTargets`（`pkg/serverinfo/sampler.go:206`）是采样器**唯一**的目标来源，
   而它的唯一调用方是 SSE handler 的每轮组装（`internal/webapi/serverapi/metrics.go:45`）。
2. 没有连接 → 没人调 `SetTargets` → `targets` 在 `targetTTL`（3 × 2s = 6s，`sampler.go:29`）后清空
   （`sampler.go:315` 的过期判据）→ `sampleTargetsLocked` 每轮返回**空 map**。
3. `history.append` 对**已存在**的实例条目补 NaN（`history.go:242-252`），30 分钟全 NaN 后整条丢弃
   （`history.go:246`）；**从没出现过的实例根本不会建条目**。
4. 落盘时全 NaN 的实例块被跳过（`history.go:333-336` 的 `allNaN`），所以盘上也没有——
   重启恢复自然也恢复不出东西。
5. 顺带：按进程网络计量的内核侧登记也是「被问到才发生」（`pkg/procnet/procnet_linux.go:406` 的
   `Bytes` 首次调用才 `targets.Put`，Windows 的 `pkg/winnetetw` 同构）。无人观看时，
   实例的收发字节**连计数都没开始**。

**明确不是什么问题**：

- 采样器本身一直在跑。`StartSampler` 挂在 `APIServer.Start()`（`internal/webapi/actions.go:165`），
  而 api / GUI（`internal/gui/gui.go:597`）/ Windows 服务（`internal/svcmgr/service.go:62`）
  三条启动路径都经过它，不存在「某种模式下采样器不启动」。
- **host（整机）曲线一直是完整的**——它不依赖任何外部输入（`sampleHostLocked`）。
  本方案要修的只有**实例级**那一半。
- Badger 落盘、过期裁剪、启动恢复的逻辑都正常，没有 bug，只是没有数据可落。

---

## 2. 目标与非目标

**目标**：只要 API server 在跑，实例级指标就持续采集 → 进环形缓冲 → 每 5 分钟落盘 → 重启后可恢复，
**与有没有浏览器连着完全无关**。打开页面时能看到过去 30 分钟里完整的实例曲线。

**非目标**（本方案一律不动）：

- 采样周期（2s）、历史窗口（30 分钟）、刷盘周期（5 分钟）、落盘键格式与 schema 版本。
- `/api/server/all-info` 的载荷结构、`/api/server/metrics/history` 的响应结构。
- 任何前端代码。
- 已停止实例的淘汰规则（连续 30 分钟无数据即整条丢弃）——「持续保留」指的是**采集不中断**，
  不是「已删除的实例永久留在内存里」。

---

## 3. 方案：目标发现从「SSE 推」改为「采样器拉」

### 3.1 机制

在 `pkg/serverinfo` 增加一个**注入式目标源**，采样器每个周期自己拉一次：

```go
// pkg/serverinfo/sampler.go
// TargetSource 回答「现在有哪些进程需要盯着」。由组合根注入——
// 采样器不认识「实例」「PID 文件」这些领域概念，同 NetSource（netsource.go）。
type TargetSource func() []Target

type Options struct {
    History  HistoryStore
    Interval time.Duration
    Targets  TargetSource // 为 nil 时退回「无目标」，只采 host
}
```

`run()` 的每个 tick 在 `sample()` **之前**先 `applyTargets(src())`，并集 + TTL 的语义
与现在的 `SetTargets` **逐字不变**——变的只是谁来调用。

### 3.2 `pkg/` 纯度

与 `NetSource`（`pkg/serverinfo/netsource.go`）同一套路：`pkg/serverinfo` 只定义接口，
实现与接线都在组合根。`Target.Name` 对它而言仍然只是个标签。
`pkg/serverinfo` 不新增任何 import。

### 3.3 领域侧：枚举逻辑收口成一个函数

现在「哪些实例在跑、PID 是多少」这段逻辑写在 handler 里
（`internal/webapi/serverapi/metrics.go:31-44`：`GetAvailableInstances` → 逐个读 pid 文件
→ `procx.IsProcessExited`）。搬进 `internal/process`：

```go
// internal/process/process.go
type RunningInstance struct {
    Name string
    PID  int
}

// RunningInstances 列出当前在跑的实例及其游戏进程 PID。
func RunningInstances() []RunningInstance
```

`internal/process` 已经 import `cfgpkg` 与 `pkg/procx`，无新依赖、不成环。

**必须同源**：handler 与采样器都调这一个函数。两处各写一份判据的话，
会出现「载荷里显示在跑、曲线却是空洞」这种只能靠对时间轴才发现的错位。

### 3.4 接线

```go
// internal/webapi/actions.go（APIServer.Start）
serverinfo.StartSampler(s.serverCtx, serverinfo.Options{
    History: statepkg.MetricsStore(),
    Targets: func() []serverinfo.Target { /* procpkg.RunningInstances() → []Target */ },
})
```

### 3.5 `SetTargets` 的去留：删掉

导出的 `SetTargets` 与 handler 里那一行调用一起删除。理由是**两个真相来源没有价值**：
拉取频率就是采样周期，handler 每轮推的内容与采样器自己拉到的完全一致。
包内保留 `applyTargets`（原 `SetTargets` 的函数体）供采样器与单测使用。

⚠️ 同时要改 `sampler.go:29` 那条 `targetTTL` 的注释——「多个 SSE 连接会各自调用 SetTargets」
这个理由不再成立。TTL 保留，但含义变成「目标源某一轮漏报（读 pid 文件失败等）时不要立刻丢弃跟踪状态」。

---

## 4. 落地清单

| 文件 | 改动 |
|---|---|
| `pkg/serverinfo/sampler.go` | 新增 `TargetSource` + `Options.Targets`；`run()` 每 tick 先拉一次；`SetTargets` → 包内 `applyTargets`；订正 `targetTTL` 注释 |
| `internal/process/process.go` | 新增 `RunningInstance` / `RunningInstances()` |
| `internal/webapi/actions.go` | `StartSampler` 传入 `Targets` |
| `internal/webapi/serverapi/metrics.go` | 枚举改调 `procpkg.RunningInstances()`，删掉 `serverinfo.SetTargets(...)` |
| `pkg/serverinfo/sampler_test.go`（新增） | 见 §6 |
| `docs/RESOURCE_RATE_CHART_PLAN.md` | §3.1.1 「无客户端时列表为空，采样器只保留 host 指标的采集」这条**已被本方案推翻**，回填一条订正 |
| `CLAUDE.md` | `pkg/serverinfo` 段落补一句「目标由组合根注入、与前端连接无关」 |

---

## 5. 承重细节与易错点

1. **拉取频率必须等于采样周期**。不要为了「省一点」改成每 N 个 tick 拉一次：`targetTTL` 只有
   3 个周期，拉取慢于 TTL 会让目标在两次发现之间过期，曲线变成断续锯齿。真要降频，
   就必须同步调大 TTL，并接受新启动的实例最多晚一个发现周期才上图。
2. **目标发现跑在采样器 goroutine 上，不能阻塞**。它一慢，整轮 host 采样跟着晚点，
   时间轴就不均匀了。`RunningInstances()` 里只许有「读目录 + 读 pid 文件 + 存活判断」，
   **不许**加 RCON、镜像目录扫描、网络请求这类判据。
3. **总成本反而下降**。以前是「每个 SSE 连接每 2s 各做一遍」，现在是「全进程每 2s 一遍」——
   开多个标签页时更省；无人观看时多出的那一遍是一次 `ReadDir` + N 次小文件读 + N 次
   `IsProcessExited`（Windows 是 `OpenProcess`，Linux 是 `/proc` 探测），可忽略。
4. **顺带修掉「打开页面第一帧 CPU 恒为 0」**：`procStateLocked` 建立状态时那次
   `Percent(0)` 必然返回 0（`sampler.go:362`），以前每次打开页面都要吃一帧。
   现在跟踪是常驻的，基线早就建好了。
5. **`procnet` / ETW 的登记随之常驻**：`Bytes()` 每轮都会被问到，内核侧 map 的条目数
   仍被限死在「在跑实例数」（`procnet_linux.go:446` 的 `pruneLocked` 按 TTL 淘汰），
   **没有新的资源风险**。但要有心理准备：登记失败的 `Debugf` 会从「打开页面时偶发」
   变成「常驻」，那不是回归。
6. **实例名含冒号不受影响**：落盘键是 `metrics:i:<实例名>:<ts>`，解析从右边切
   （`history.go:584` 的 `nameFromInstKey`）。
7. **容量**：每实例约 65KB 常驻内存、每 5 分钟约 10KB 落盘。10 个实例常驻 ≈ 0.7MB 内存、
   每天约 3MB 写入，单值远低于 Badger 的 1MB 内联阈值（不会落进无人回收的 value log）。
8. **停机实例的语义不变**：继续补 NaN，连续 30 分钟无数据后整条丢弃（`history.go:246`）。
   实例重启换了 PID 时曲线仍按名字连续，中间是可见的空洞。

---

## 6. 验证

**单测**（`pkg/serverinfo`，不依赖真机）：

- 注入一个返回固定 `Target` 的假 `TargetSource`，手动驱动一次 `sample()`，
  断言 `Snapshot().ByName` 与 `GetHistory` 里都有该实例——**核心回归**，
  它就是「无 SSE 连接也有数据」这句话的可执行形式。
- 目标源返回 nil / 空切片时不 panic，host 指标照常。
- 目标源在两轮之间「漏报」一次（TTL 内）时跟踪状态不被丢弃。

**真机**（Windows 为准）：

1. 起服务，**全程不开浏览器**，等 5 分钟。
2. `curl` 回填接口，断言 `instance` 列存在且不是全 null。
3. 再等一个刷盘周期（5 分钟）后重启服务，重复步骤 2——验证落盘 + 恢复这条路真的通了
   （这一步是关键：以前根本没有实例块可落，恢复路径在实例维度上从未被真实数据走过）。

**构建**：`go build ./...`、`GOOS=linux CGO_ENABLED=0 go build ./...`、`go vet ./...`，
`-race` 按仓库规则用 PowerShell 跑（`go test -race ./pkg/serverinfo/...`）。

---

## 7. 实现记录（2026-09-12）

### 7.1 落地清单

| 文件 | 改动 |
|---|---|
| `pkg/serverinfo/sampler.go` | 新增 `TargetSource` + `Options.Targets` + `Sampler.targetSrc`；`sample()` 开头 `refreshTargets()`；导出的 `SetTargets` 改为包内 `applyTargets`；订正 `targetTTL` 注释 |
| `internal/process/process.go` | 新增 `RunningInstance` / `RunningInstances()` |
| `internal/webapi/actions.go` | 新增 `runningInstanceTargets()`，`StartSampler` 传 `Targets` |
| `internal/webapi/serverapi/metrics.go` | 改调 `procpkg.RunningInstances()`，删掉 `SetTargets` 调用与 `cfgpkg`/`procx` 两个 import |
| `pkg/serverinfo/sampler_test.go`（新增） | 4 个用例，见 §7.3 |
| `docs/RESOURCE_RATE_CHART_PLAN.md` | §3.1.1 两条（所有权与并发、空闲策略）补 2026-09-12 订正；§9.1 表格标注 `SetTargets` 已删 |
| `CLAUDE.md` | `pkg/serverinfo` 段落补「目标也是注入的、与前端连接无关」 |

### 7.2 与计划的偏差

1. **`refreshTargets()` 放在 `sample()` 开头，而不是 `run()` 的 ticker 分支里**：`run()` 会在进入
   循环前先 `s.sample()` 一次（让第一个接入的客户端不用等一个周期），放在 ticker 分支里的话
   **那一帧没有目标**，实例曲线会比 host 晚一个点起步。
2. **`RunningInstances()` 比原 handler 的判据多一道 `isExpectedProcess`**（镜像名 / Linux 下 cmdline
   核对）。原来 `metrics.go` 只看「PID 文件存在 + 进程没退出」，而 PID 文件从不清理、系统又会
   复用 PID号码——崩溃后凑巧复用了旧 PID 的无关进程会被当成实例，现在既然要**长期**画进曲线，
   这个误判就从「面板上闪一下」变成「30 分钟历史里一条假曲线」。判据直接取
   `IsInstanceProcessAlive` 的方法 2，不是新发明的。**这是 all-info 载荷的一处行为收紧**：
   同样的进程以前会出现在 `instances[]` 里，现在不会。

### 7.3 验证结果

- 新增 `pkg/serverinfo/sampler_test.go`：①**核心回归**——没有任何人推送目标，仅靠注入的目标源，
  实例数据要进 `Snapshot().ByName` **与**历史（这条挂掉就是 bug 回来了）；②目标源为 nil 时不 panic、
  host 照采；③TTL 之内漏报一轮不丢跟踪状态；④超过 TTL 的目标连同 `procs` 里的状态一起被回收。
- `go test -race ./pkg/serverinfo/... -count=1` 通过（PowerShell）；
  `go test ./internal/process/... ./internal/state/... -count=1` 通过。
- `go build ./...`、`GOOS=linux CGO_ENABLED=0 go build ./...`、
  `go vet ./internal/webapi/... ./internal/process/... ./pkg/serverinfo/...`、`gofmt -l` 全部干净。
- **真机验收未做**：§6「真机」那三步（不开浏览器等 5 分钟 → 查回填 → 重启后再查）需要在实际
  部署环境上跑一遍，尤其第 3 步——实例块以前从没被真实数据走过落盘 + 恢复这条路。

---

## 8. 回滚

单点：`actions.go` 里不传 `Targets` 即退回「无目标」的旧行为（曲线回到只有 host）。
若要完整还原，再把 `metrics.go` 那一行 `SetTargets` 调用与导出函数恢复即可。
落盘数据不需要迁移——schema 没变，多出来的只是本来就该有的实例块。
