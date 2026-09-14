# 用 simple-file-sync 替换 Syncthing 的可行性评估与实施计划

> 状态：**评估稿**，尚未开工。评估对象是同步库 **simple-file-sync**（本机工作副本 `D:\golang\ark-asa-file-sync`，目录名是历史遗留；
> go.mod 模块名 **`github.com/Niexiawei/simple-file-sync`**，远端 `git@github.com:Niexiawei/simple-file-sync.git`）
> 在本仓库 2026-09-12 时点的代码，以及本仓库 `internal/syncthingmanage/` 的现状。
>
> **2026-09-14 更新：同步库已去 ARK 化并统一改名**（破坏性变更，接入以 `v0.3.0`+ 为准，详见 §7-12）。
> 该库定位为与 syncthing 同类的**通用文件同步库**，不再包含任何 ARK 专属的命名、默认值或逻辑：
> proto 包 `filesync.v1`、环境变量 `FILESYNC_*`、指标 `filesync_*`、agent 元数据目录 `.filesync/`、协调端数据库 `filesync.db`。
> 本文里所有 ARK 专属的取舍——clusters 参数、存档 Root 与实例生命周期绑定、排除哪些 ARK 文件——
> 都是**本仓库 `internal/filesyncmanage` 的职责**，用同步库的通用 SDK 能力（`AddRoot`/`RemoveRoot`、`Exclude`、
> `MaxFileBytes`、`FlushNow`/`WaitApplied`）实现，**不要推回同步库**。
>
> ⚠️ 评估时**不要读同步库的 `CLAUDE.md` §"Known limitations"**——那一段严重过时
> （声称"无 TLS、无删除传播、无冲突策略、无周期扫描"，这四项全部已实现）。
> 该库的真实状态以 `docs/user-guide.md` + `docs/implementation-plan.md` 的里程碑记录为准，
> 两者显示 **M0～M4 全部完成**。本文所有结论以**读源码 + 本机实测**为准。

---

## 1. 结论先行

| 问题 | 结论 |
|---|---|
| **能不能替换？** | **能，且方向正确**——但不是"现在把 syncthing 摘掉换上去"，而是"先补 3 个阻断项，再按 §8 的阶段并存迁移"。 |
| **最大收益** | 干掉一个独立 exe 进程 + 一份 30MB 按需下载 + 让用户手改 XML 的配置方式；换成**库内调用 + 表单配置**，与 `frpmanage` 的既有形态一致。 |
| **最大代价** | 需要**自建并长期运营一个公网 Coordinator**，而且它是**数据面**（所有字节都经它中转、落盘），不是只做信令。这是与 syncthing（P2P 直连 + 打洞，中继只是兜底）的**根本架构差异**，也是唯一不可逆的决策点。 |
| **阻断性缺陷** | 三条，**全部在同步库侧**，见 §6：①新节点收不到组内历史文件；②没有任何忽略/包含规则；③向**运行中**实例的存档目录落盘会失败或损坏存档。 |
| **建议的首个落地范围** | **只同步 `{BaseDir}/clusters/<ClusterID>/`（集群传输数据）**，不碰存档。这正是你提出的诉求，也恰好避开了 ②③ 的大部分杀伤面，见 §5.3。 |

---

## 2. 现状：Syncthing 集成到底花了多少成本

`internal/syncthingmanage/`（4 个文件，约 600 行）+ `app/src/views/SyncthingManager.vue`（566 行）：

| 项 | 现状 | 痛点 |
|---|---|---|
| 二进制 | 首次使用时从 GitHub 下载 pin 住的 v2.0.11（`install.go`），解压进 `{BaseDir}/syncthing/` | 多一次外网依赖；国内要靠 `download.github_proxy` |
| 进程 | `exec.Command(syncthing, "serve", "--home", dir, "--no-browser", "--no-restart", "--no-upgrade")`，用 `pkg/proctree` 管进程树 | 独立进程：崩溃/端口占用/孤儿进程都要自己兜；`asyncStart` 里那套 `done/ctx/1s 轮询` 的状态机是为了补"进程可能立刻退出"而存在的 |
| 配置 | **前端 Monaco 直接编辑 `config.xml` 原文**，保存后重启进程 | 用户要手工填 device ID、folder 共享关系、rescan 间隔——这是你说的"配置复杂"的根源。对端设备 ID 还得从另一台机器的 syncthing GUI 里抄 |
| 状态 | 只有 `running` / `stopped`（进程在不在） | 同步进度、单文件传输、冲突、落后多少，全部**不可见**——要看只能另开 syncthing 自己的 8384 GUI，而本程序并没有把它接出来 |
| 日志 | stdout 打进 `logger`，前端按 `[syncthing]` 前缀过滤 | 唯一还算好用的部分，可原样复用 |
| 鉴权 | syncthing 的 GUI/API 有自己的一套，与本程序的 `auth` 完全无关 | 两套账号体系 |

**替换的价值判断**：值钱的不是"省一个进程"，而是**把"同步"变成本程序的一等公民**——
状态能进 `/api/server/all-info`、迁移能被 `countdown`/`batchmanage` 编排、配置能进 `config.yaml` 的权限体系。
这三件事在"外挂一个 syncthing 进程"的形态下都做不到。

---

## 3. simple-file-sync 现状盘点（实测）

**本机实测（2026-09-12，Windows）**：`go build ./...` 通过；
`go test ./pkg/... ./internal/agent/ ./internal/coordinator/ -count=1` 全部 `ok`
（agent 15.0s / coordinator 1.2s / client 5.4s）。不是"纸面项目"。

架构：**Coordinator（公网中心节点，中转 + 记账）↔ 多个 Agent（主动外联，可在 NAT 后）**。
一个 Node = 一台机器的身份 + **一条** gRPC 连接；一个 Node 可挂**多个 Root**；
每个 Root 属于一个 Group，同 Group 的 Root 内容互相同步。

已实现（M0–M4，逐条对过源码）：

- **mTLS 强制**（`RequireAndVerifyClientCert`）；`internal/pki` 提供 CA/证书的生成与热重载。
  ⚠️ **鉴权模型正在被替换**（2026-09-12 拍板，同步库 `docs/证书自动续期计划.md` M7-4）：
  原先是"集群共享客户端证书 + 集群 token"双重鉴权，新模型是**引导证书（人工分发，只能换证）
  + 每节点证书（协调端签发，绑 `node_id`）**，**集群 token 整体删除**。
  本文下面凡是提到 token 的地方都已按新模型改写。
- **删除传播**（墓碑）、**冲突裁决**（CAS + LWW + `*.sync-conflict-*` 副本，`MaxConflicts` 默认 3）、**周期性全量扫描兜底**（`ScanInterval`）、**变更事件聚合器**（`WatchDelay` 静默期 / `WatchTimeout` 硬上限）。
- **断点续传**：上传偏移量落 SQLite（`next_offset`），下载落本地 `.part`；**SHA-256 全文校验**后才原子替换。
- **崩溃自愈**：`store_epoch` 机制能识别"Coordinator 从旧备份恢复"，触发全量核对而不是静默覆盖。
- **任务生命周期**：租约超时重投、重试上限、死信（`dead`）、`tasks list/retry` 运维命令。
- **迁移 API**：`Root.FlushNow()` + `Root.WaitApplied()` / `WaitPathsApplied()`，`ErrTasksDead` 可 `errors.Is` 区分——**这正是本程序做"实例迁移到另一台机器"缺的那块**。
- **可观测**：Coordinator 侧 `/healthz` `/readyz` `/metrics`（Prometheus）+ Admin gRPC（`GetStats`/`WatchStats`，含每条在途传输的 bps/offset/ETA）；Agent 侧 `Node.Stats()`/`Root.Stats()` 纯本地读、可按 UI 刷新率轮询。
- **性能**：`HashWorkers` 有界哈希池、上下行限速、`{size,mtime}→hash` 持久化缓存（`EnableHashCache`）。
- **嵌入式优先**：`pkg/client` 就是 `agent.exe` 底下那套库，**显式设计成给管理程序嵌入的**（`Config.CertPEM` 允许凭据全程不落盘；`ScanInterval`/`StatsReportInterval` 默认关闭，"库不启动宿主没要求的后台行为"）。

---

## 4. 与 Syncthing 的能力对照

| 维度 | Syncthing | simple-file-sync | 对本项目的影响 |
|---|---|---|---|
| 拓扑 | P2P 直连 + NAT 打洞，中继兜底 | **必须**经 Coordinator 中转 | ⚠️ 要自建公网节点，带宽=同步量×(节点数-1) |
| 部署形态 | 独立进程 + XML 配置 | **库内调用**，配置由宿主定义 | ✅ 与 frpmanage 一致 |
| 增量算法 | 块级交换（只传变化块） | **整文件传输**（4 MiB chunk 只是分片，不是增量） | ⚠️ 大存档每次保存全量重传，见 §6.4 |
| 冲突 | `*.sync-conflict-*` | 同名同形（刻意抄的，且修正了扩展名位置） | ✅ |
| 忽略规则 | `.stignore`，成熟 | **完全没有** | ⚠️ 阻断项，见 §6.2 |
| 初始同步 | 新设备加入会拉全量 | **收不到历史文件** | ⚠️ 阻断项，见 §6.1 |
| 版本保留 | 多种 versioning 策略 | 只保留当前版本（GC 删历史对象） | 可接受：本程序自己有 `backup` |
| 进度可见性 | 自带 GUI | `Stats()` 结构化数据，可直接渲染 | ✅ 比现在强得多 |
| 鉴权 | 自成体系 | 引导证书人工分发一次，之后每节点一张协调端签发的证书（M7-4 后） | ✅ 可并入本程序 `auth`；表单只需粘一行 join blob |
| 迁移编排 | 无 | `FlushNow` + `WaitApplied` | ✅ 本项目独有需求，syncthing 给不了 |

---

## 5. 替换后在本仓库里的落点

### 5.1 可导入的只有 `pkg/`

同步库把 `coordapp`/`coordcli`/`agent`/`coordinator` 全放在 `internal/` 下，Go 的 `internal` 规则决定了
**本仓库只能 import `pkg/client` 与 `pkg/logger`**。

后果（必须接受，不是可选项）：**Coordinator 无法嵌进 asa-server**，只能作为独立二进制部署在公网机器上。
"asa-server 一个 exe 既当中心又当节点"这条路走不通，除非改那个库的目录结构。

### 5.2 同步 Root 只能挂"实例真实目录"，不能挂镜像

本仓库的镜像（`internal/mirror`）里 `ShooterGame/Saved/<SaveDir>`、`.../Config/WindowsServer`、`.../Logs`
都是 **NTFS junction**，指向 `{BaseDir}/instances/<name>/{Save,Config,Logs}`。
同步库**跳过符号链接、也不跟随**（`manifest.Scan` 只收 `Type().IsRegular()`），所以 Root 必须是：

| 用途 | Root 路径 | Group 建议 |
|---|---|---|
| **集群传输数据**（首选落地范围） | `{BaseDir}/clusters/<ClusterID>` | `cluster-<ClusterID>` |
| 单实例存档（高风险，见 §6.3） | `{BaseDir}/instances/<name>/Save` | `save-<迁移组名>` |
| 实例 INI 配置（可选） | `{BaseDir}/instances/<name>/Config` | `config-<组名>` |

`AddRoot` 会拒绝互相包含/重叠的 Root，上面三类天然不重叠。

### 5.3 为什么首个落地范围应该是 clusters/ 而不是存档

- **文件特征匹配**：集群传输文件（`*.arkprofile` / `*.arktribute` 等）是**小文件、写完即终态、写完很快被对端读走并删除**。整文件传输不吃亏，删除传播刚好用上。
- **避开运行中写入的雷**：ARK 对 clusters 目录是"放上去/取走"的用法，不像存档那样被进程长期持有。
- **收益最直接**：跨机器集群（不同物理机跑同一个 ClusterID）现在**根本做不到**，syncthing 那套手改 XML 的方式没人愿意配。
- **延迟要求要调参**：玩家在方尖碑等着，默认 `WatchDelay=10s`（SDK 默认是库常量 10s/60s）太慢。
  clusters 这个 Root 建议 `WatchDelay=1s / WatchTimeout=5s`，并显式打开 `ScanInterval`（如 5m）兜底。

---

## 6. 阻断性缺陷（P0，必须先在同步库里修）

### 6.1 新节点加入已有内容的组，收不到任何历史文件 🔴

`internal/coordinator/service.go` 的 `handleSubscribeRoot` 只做两件事：记订阅、冲刷**已排队**的任务。
**没有任何逻辑会在首次订阅时为组内已有文件补建 download 任务**。
现有场景能跑通，纯粹是因为测试里接收方总在发送方上传**之前**就订阅了。

（该库 `docs/implementation-plan.md` M2-7 状态说明已如实记录此缺口，自 M0-3 引入多根/组模型起就存在，**未修复、未排期**。）

**对本项目意味着什么**：这恰恰**摧毁了集群同步的核心用例**——
玩家在 A 服上传角色 → 之后 B 服才加入集群组 → **B 服永远看不到那个角色文件**，玩家在方尖碑下不来。

**修复方向**：订阅事件触发"按组当前 `files` 表补建 download 任务"，机制类似 `queueDownloadTx` 但由订阅而非上传触发；
要处理"对端已有同名同哈希文件"的幂等判断，否则每次重连都会全量重推。

### 6.2 没有任何忽略/包含规则 🔴

`manifest.Scan` 只排除三类：目录、非常规文件、`*.sync-conflict-*`；`watch()` 另外跳过 `.filesync` 元数据目录（`v0.3.0` 之前叫 `.ark-sync`）。
**没有 `.stignore` 等价物，没有 include/exclude，没有大小上限。**

**对本项目意味着什么**：
- 存档 Root 会把 `Save/` 下所有东西一起同步（含 ARK 自己的临时/备份产物）。
- 一旦某天想把 Root 上提到 `instances/<name>/`，`server.log`、`Logs/`、`ArkApi/` 全会被卷进去。
- 没有大小闸门 ⇒ 一次误配置就能把公网 Coordinator 的磁盘和带宽打满。

**修复方向**：`RootConfig` 加 `Exclude []string`（glob 或前缀），在 `manifest.Scan`、`watch()`、
以及**下载落盘前**三处同时生效（只在扫描侧过滤会被对端推来的文件绕过）。

**✅ 已于 2026-09-12 修复**（同步库 M5-2 + M5-3，见 `simple-file-sync/docs/功能迭代.md`）。
实际落地比这里写的多三处：除扫描/watch/落盘外，还有 watch 建 watcher 时对被排除目录 `SkipDir`、
单文件重扫、以及**删除上报**。最后那处是本节没想到的——漏掉它，被排除的文件被删除时仍会上报删除，
在协调端凭空生成墓碑，再扩散去删掉**其他节点上真实存在的同名文件**。
大小闸门同期完成：Agent 侧 `MaxFileBytes` + 协调端 `--max-file-bytes`，两道语义不同、都要。

### 6.3 向"运行中"实例的存档目录落盘会失败或损坏存档 🔴

`replaceFile` 就是 `os.Rename(source, destination)`（`internal/agent/root.go:1398`）。

- **Windows**：ARK 进程持有存档文件时，`os.Rename` 覆盖会 `ERROR_SHARING_VIOLATION` 失败 →
  任务重试 → 耗尽 `MaxTaskRetries` → 进 `dead`，需要人工 `tasks retry`。
- **两个平台共同的更严重问题**：即使替换成功，**运行中的 ARK 进程内存里仍是旧世界状态，下一次保存会把同步进来的内容整个覆盖回去**——
  同步"生效了"只是假象，且会与对端反复来回覆盖。

**修复方向（在本仓库侧，不必改库）**：存档类 Root 必须与实例生命周期绑定——
只在实例**已停止**时挂上 Root，启动前 `RemoveRoot`。这条规则应当由 `internal/filesyncmanage` 强制，
而不是写在文档里靠用户自觉。clusters Root 不受此限。

### 6.4 整文件传输 + 中心节点是数据面（成本模型，不是 bug，但必须先算账）🟠

- 无块级增量：一个 200 MB 的 `.ark` 存档每次保存**全量重传一次**。
- 所有字节经 Coordinator 中转并落对象库：**公网带宽 ≈ 存档大小 × 保存频率 × (组内节点数 - 1) × 2**（上行+下行各算一次）。
- 举例：200 MB 存档、15 分钟一存、2 节点 ⇒ **约 1.6 TB/月**的中转流量。普通 VPS 的流量包扛不住。

**结论**：这条直接支持 §1 的"首个落地范围只做 clusters"——集群文件是 KB 级，量级差三个数量级。

**存档同步的出路已立项**：同步库的 **M6 块级增量传输**，实施方案已定稿于同步库 `docs/M6块级增量传输实现方案.md`（参考 syncthing 的
分块 + `blockDiff` + 本地旧版本块复用，对象仍按整文件哈希寻址所以 GC/冲突/补发都不用改）。
~~注意 M6-0 是一道门禁~~——**2026-09-14 已改**：同步库按格式无关的通用库设计，M6-0 降为**非门禁**的基准测量，
不会因为某一类负载（包括 ARK 存档）复用率低就不做块级增量。补充一个事实：**ASA 的 `.ark` 是原地页更新的 SQLite 库**
（ASE 才是整体序列化），对块级复用偏利好；但运行中同步会拿到事务中途的库，所以存档同步在本仓库侧仍应走"停机迁移 + `WaitApplied`"那条路（与块级增量叠加，不是二选一）。**同步库的 M6-0 基准测量仍欢迎本程序提供真机存档样本**，作为"原地页更新"这一类负载的样本之一。

---

## 7. 重要但非阻断（P1）

| # | 事项 | 说明 |
|---|---|---|
| 1 | Coordinator 不可嵌入 | §5.1。要么独立部署，要么向该库提"把 coordinator 也提到 `pkg/`"。 |
| 2 | ~~依赖引入方式待定~~ ✅ 已定 | **GOPRIVATE**（同 `go-arkparser`）。同步库已打 `v0.1.0` / `v0.2.0`；去 ARK 化改名之后接入以 `v0.3.0`+ 为准（§7-12）。 |
| 3 | 新增依赖面 | 净新增 `google.golang.org/grpc` + `genproto`（protobuf 本仓已有 indirect，sqlite 1.57→1.58，fsnotify/urfave/cli 版本一致）。可接受。 |
| 4 | 日志接线 | 库走 `log/slog`，本仓 `pkg/logger` 是 zap 包级函数。需要一层 **slog.Handler → zap** 适配，并给记录打 `[filesync]` 前缀，前端才能复用现有日志过滤面板。 |
| 5 | ~~Windows 无 SIGHUP~~ ✅ 已解决 | 同步库 M5-5 已落地：凭据热重载改为**文件监视 + `ReloadCredentials` RPC**，Windows 上同样生效；agent 侧 `Config.CertificateSource` 支持不重建 `client.Node` 换证。 |
| 6 | ~~证书不自动续期~~ ✅ 已实现（同步库 M7-5，2026-09-13） | `EventCertificateExpiringSoon` 只在 `client.New` 检查一次这个缺陷**已修**（M7-0，分 90/30/7/1d 档位重复提醒）。真正的自动续期走新信任模型：节点证书有效期 **1 年**、到期前 90 天由库**自动**向协调端续期并重连（M7-5）。本程序仍需把该事件接进告警/前端红点。 |
| 7 | ~~单节点吊销能力弱~~ → M7-4 后解决 | 每节点一张绑定 `node_id` 的证书之后，`coordinator node revoke` 是**真正的单节点吊销**（协调端是唯一 relying party，下次连接即拒绝）。**新增运维负担**：机器换盘、丢私钥、或离线超过 1 年导致证书过期，都必须人工执行一次 `coordinator node reset <id>`——这是"不留身份抢占路径"的有意取舍。 |
| 8 | 目录批量删除不保证逐文件传播 | fsnotify 事件被聚合成目录级时只记一条 WARN。对 clusters 用例影响小。 |
| 9 | Linux 降权属主 | 同步写进 `instances/<name>/Save`、`clusters/<id>` 的新文件属主是 asa-server（root），**游戏以降权用户跑就写不了**。必须在落盘后走 `runner.ChownTreeForRuntime` / `pkg/shareacl` 那条既有链路。 |
| 10 | Coordinator 写路径单连接 | SQLite `MaxOpenConns(1)`，节点多时是瓶颈。小规模无所谓。 |
| 11 | ~~文档里的模块名过时~~ ✅ 已修（同步库 M5-4） | `user-guide.md` §5.1 写 `import "ark-asa-file-sync/pkg/client"`，实际 go.mod 是 `github.com/Niexiawei/simple-file-sync`。以 go.mod 为准。 |
| 12 | **同步库去 ARK 化改名**（2026-09-14，破坏性） | proto 包 `ark.sync.v1` → `filesync.v1`（生成包 import 路径 `github.com/Niexiawei/simple-file-sync/filesync/v1`）；环境变量 `ARK_SYNC_*` → `FILESYNC_*`；指标 `ark_sync_*` → `filesync_*`；agent 元数据目录 `.ark-sync/` → `.filesync/`；协调端数据库 `ark-sync.db` → `filesync.db`；CLI 名 `filesync-agent` / `filesync-coordinator`。**不做旧名兼容与迁移**：旧节点要重新 enroll、旧协调端数据目录不会被识别、新旧版本无法互通。本程序尚未写接入代码，所以影响只在本文与后续 P2 的命名：`filesyncmanage` 里的路径、指标、环境变量一律按新名写。 |

---

## 8. 实施计划（分阶段，syncthing 全程并存）

> 总原则：**syncthing 不下线，直到新链路在真机上跑过一个完整的"玩家跨服传输"闭环**。
> 两者可以同时存在（Root 路径不同、进程互不相干），没有必须二选一的技术约束。

### P0 — 决策与前置 ✅ 已定（2026-09-12）

- [x] **Coordinator 部署形态**：**Linux 独立机器，由用户自行部署运维**。Windows 无 SIGHUP 的证书/token
      热轮换限制（§7-5）因此不再挡路。
- [x] **依赖引入方式**：**GOPRIVATE**，与本仓库既有的 `github.com/Niexiawei/go-arkparser` 同一套路。
      本机实测 `go env GOPRIVATE` 已有该值，且 git 配了 `url.git@github.com:.insteadof https://github.com/`
      （私有库走 SSH），接入时把 GOPRIVATE 扩成 `github.com/Niexiawei/*` 即可。
- [x] **首个落地范围**：**只做 `clusters/`**。存档同步推迟，§6.3、§6.4 因此从"必须解决"降为"避开"。

### P1 — 修同步库的三个阻断项（在同步库 `simple-file-sync` 仓库里做）

> **详细实施计划已落地**：`D:\golang\ark-asa-file-sync\docs\功能迭代.md`（M5-0～M5-4，含协议改动、
> 插入点清单、影响文件与逐条验收用例）。本节只保留出口条件，具体设计以那份文档为准。

- [x] §6.1 订阅时补建全量 download 任务（含幂等：对端已有同哈希文件不重推）。**已完成 2026-09-12**
      （同步库 M5-1：新增 `RequestBackfill` 协议消息 + `Store.BackfillDownloads`；
      验收用例做过反向对照，关掉修复后确实失败）。
- [x] §6.2 `RootConfig.Exclude` + 扫描/watch/落盘三处生效。
      **验收**：被排除的路径既不上报、也拒绝被对端推入。**已完成 2026-09-12**
      （同步库 M5-2：新增 `internal/agent/exclude.go`，实际落到**六处**插入点而不是三处——扫描 walk、
      watch 建 watcher、watch 事件过滤、单文件重扫、删除上报，以及收方向唯一的 choke point
      `Root.localPath`。模式语义采用 gitignore 的锚定规则：不含 `/` 的模式匹配任意层级的文件名，
      否则 `*.tmp` 挡不住 `Saved/a.tmp`；坏模式让 `AddRoot` 直接失败，不静默忽略。
      **排除是本地策略不是组策略**——组内配置不一致时，排除方会拒收并最终产生死信任务，这是有意为之：
      宁可让配置错误可见，也不把用户明确排除的文件静默写进他的盘）。
- [x] （可选，提升可运维性）单文件大小上限 / 组总量上限，防误配置打满中心节点。
      **已完成 2026-09-12**（同步库 M5-3：Agent 侧 `RootConfig.MaxFileBytes` 在哈希**之前**用 stat 判断、
      超限记一条 WARN 跳过，不重试不算失败；协调端侧 `--max-file-bytes` 在 `QueueUpload` 之前拒收。
      两道语义不同：节点的限额由各自部署者设定、协调端不能信任它，**协调端那道才是真正的资源保护**）。
      **组总量上限有意未做**：需要持续记账与配额回收，复杂度远高于本期收益；磁盘水位用 `/metrics` 的
      `filesync_objects_bytes`（`v0.3.0` 之前叫 `ark_sync_objects_bytes`）监控（M3-3 已有）。
- [x] 全程 `go test -race ./...` 保持通过（该库的标准验证命令）。**2026-09-12 全绿**
      （另：`buf lint` 通过，`buf generate` 无未提交的生成物 diff）。

> P1 的阻断项全部完成，M5-4/M5-5 也已完成。信任模型变更（`docs/证书自动续期计划.md`
> M7-0/M7-1/M7-3/M7-4）已于 **2026-09-12 落地**：删 token、协调端启动自举 PKI、
> 引导证书 + 每节点证书 enroll，全量 `go test -race ./...` 通过。
>
> **P2 的剩余解锁条件：同步库打出 `v0.3.0` tag**。`v0.2.0` 已打；之后的去 ARK 化改名又是一次破坏性变更（§7-12），
> P2 直接按 `v0.3.0` 的命名接入，不要先对着 `v0.2.0` 写再返工。
> M7-5（节点证书自动续期）已于 2026-09-13 落地：配了 `NodeCertificates` 的节点到期前 90 天自动续期并重连。
> ⚠️ **协调端与 agent 必须同版本升级**，新旧之间无法通信。

### P2 — 本仓库新增 `internal/filesyncmanage`（照抄 frpmanage 的形态）

依赖层级：`filesyncmanage` 依赖 `config`(cfgpkg) + `appconfig` + `pkg/logger`；被 `webapi` 依赖。
**不**依赖 `instance`（避免成环）；"实例停了才挂存档 Root"那条规则由 `webapi`/`countdown` 侧调用它来实现。

- [ ] `config.go`：结构化配置落 `{BaseDir}/filesync/config.json`（0600，含引导证书私钥），
      与 `frpmanage` 同构——**表单不是文件编辑器**。字段（**已按 M7-4 新信任模型更新**）：
      `{server_addr, server_port, label, bootstrap_cert_pem/bootstrap_key_pem/ca_pem, roots:[{kind, target, group_id, watch_delay, scan_interval, exclude[]}]}`。
      **没有 `token` 字段了**；原 `cert_pem/key_pem` 语义从"共享客户端证书"变成"**引导**证书"
      （只用于首次接入换证，之后可以删掉）。沿用 `ErrNotConfigured` 哨兵：没配过就只记 INFO，不刷 ERROR。
- [ ] **节点证书存储**：实现同步库的 `NodeCertificateStore` 接口，落
      `{BaseDir}/filesync/node.crt` / `node.key`（0600）。这是本机 enroll 得到的**身份**凭据，
      与引导证书不是一回事，**不要放进 `config.json`**（它会被备份/导出，身份私钥不应跟着走）。
- [ ] `manager.go`：持有唯一 `*client.Node`；`Initialize/Start/Stop/Restart/Status`；
      `client.NewFileIdentityStore({BaseDir}/filesync/node-id)` 提供稳定 node id；
      `OnEvent` 落日志 + 推 WS 事件（尤其 `EventCertificateExpiringSoon`/`EventAuthenticationFailed`/
      `EventNodeIDConflict`/`EventEnrolled` 四个）。`EventNodeIDConflict` 在新模型下多一层含义：
      同一 `node_id` 两个活跃会话 = **节点私钥可能被复制到了另一台机器**，值得在前端标红而不只是记日志。
      **Root 增删走 `AddRoot`/`RemoveRoot` 热操作，不重启节点**（对照：改 frp 端口规则走 `UpdateConfigSource`）。
- [ ] `logbridge.go`：slog.Handler → `pkg/logger`，记录带 `[filesync]` 前缀（§7-4）。
- [ ] `perm_linux.go` 钩子：落盘后对受影响子树调 `runner.ChownTreeForRuntime`（§7-9）；Windows 空实现。
- [ ] 生命周期接线：`webapi/actions.go` 的 `InitializationBasicComponents` / `Start` / `Stop` 三处，与 syncthing 并列。

### P3 — API 与前端

- [ ] `internal/webapi/filesyncapi/`：`GET/PUT /api/filesync/config`（写操作要求管理员）、
      `GET /api/filesync/status`、`GET /api/filesync/status/stream`（SSE，推 `Node.Stats()`：连接状态、每个 Root 的
      pending/in-flight/failed、在途传输的 path/size/offset/bps/ETA）、`POST /api/filesync/{start,stop,restart}`、
      `POST /api/filesync/roots`（增删 Root）。
- [ ] `app/src/views/FileSyncManager.vue`：**表单 + 状态面板**（照 `FRPManager.vue`，不是 Monaco）。
      左栏：**粘贴一行 join blob**（协调端自举后打印，含地址 + CA + 引导证书，见 M7-3）——
      不再是"地址/token/证书三件套"分别填；Root 列表（实例/集群下拉选，不让用户手填绝对路径）；
      另加一行**接入状态**：未接入 / 已接入（节点证书剩余 N 天），过期或临期标红；
      右栏：实时传输进度 + 按 `[filesync]` 过滤的日志。
      别忘 `App.vue` 的**三处**联动（菜单项、`watch(route.path)` 高亮、`handleMenuClick` 分支）。

### P4 — 与实例生命周期/迁移编排打通（本项目独有价值）

- [ ] 存档类 Root 的守卫：实例启动前强制 `RemoveRoot`，停止后才 `AddRoot`（§6.3）。
- [ ] 迁移动作（`countdown`/`batchmanage` 之上）：**停实例 → `FlushNow` → `WaitApplied`（`ErrTasksDead` 单独报错）→ 通知目标节点**。
      目标侧同样要 `WaitApplied` 后才允许启动——库文档明确"源端收敛不代表目标端已收到"。

### P5 — 真机验收与 syncthing 退场

- [ ] 两台真实机器、同一 `ClusterID`、跨服角色传输闭环跑通（含"B 服后加入"这一步，正面验证 §6.1 的修复）。
- [ ] 断网/重连、Coordinator 重启、证书过期告警三条异常路径各验一次。
- [ ] 连续观察一段时间的中转流量，回填 §6.4 的实测数字。
- [ ] 以上全绿之后，再讨论是否移除 `internal/syncthingmanage` 与 `SyncthingManager.vue`（**独立提交**，便于回滚）。

---

## 9. 风险与回滚

| 风险 | 缓解 |
|---|---|
| 公网 Coordinator 挂了 ⇒ 全组停止同步 | Agent 自动重连（指数退避，上限 10s），本地文件不受影响；`Root.State().Failed` 接进前端红点。Coordinator 的 `filesync.db` + `objects/` 要**同盘一起备份**。 |
| 从旧备份恢复 Coordinator | 库有 `store_epoch` 机制，会触发全量重扫核对而不是静默丢数据——但会有一次全量开销，要在文档里写清这是正常现象。 |
| 双写同一文件 | LWW + `*.sync-conflict-*` 副本（`MaxConflicts` 建议保持 3）。ASA 存档几百 MB，副本数一定要有上限。 |
| 私有仓库依赖导致他人构建失败 | §7-2 在 P0 阶段就定下来，不要拖到 P2 才发现 CI 拉不到。 |
| **协调端与 agent 版本不匹配** | M7-4 是破坏性协议变更（删 token、换鉴权），**不能灰度**：升级协调端就必须同时升级本程序内嵌的库版本，反之亦然。锁死在 `go.mod` 的同一个 tag（`v0.3.0`+）上，并在前端状态面板把"连不上"与"版本不匹配"区分开（后者表现为握手成功但 `Hello` 被拒）。 |
| **节点证书过期需人工干预** | 离线超过 1 年（或换盘丢私钥）的机器回来后连不上，必须有人在协调端 `node reset`。前端要**提前**显示剩余天数，别等到连不上才发现。 |
| 回滚 | syncthing 全程在位；`filesync` 未配置时 `ErrNotConfigured` 短路，零副作用。回滚 = 前端不用那个页面。 |

---

## 10. 已定的四件事（2026-09-12）

1. **Coordinator**：Linux 独立机器，用户自行部署运维。
2. **依赖**：GOPRIVATE，同 `go-arkparser`。
3. **首期范围**：只做 `clusters/`，存档同步不在本期。
4. **信任模型**（本日新增，见同步库 `docs/证书自动续期计划.md`）：
   协调端启动时**自举 PKI**（M7-3）；**删除集群 token**；agent 用人工分发的**引导证书**首次接入，
   在 mTLS 连接上 enroll 一张**绑定 `node_id` 的节点证书**（M7-4，签 CSR、私钥不出本机）；
   节点证书 **1 年**有效、到期前 90 天**自动续期**（M7-5）。
   身份被占用的唯一恢复路径是人工 `coordinator node reset`——**不做"证书过期即可重新 enroll"的自愈**。

执行顺序：**先在同步库里做完 M7-3 / M7-1 / M7-4 / M7-5 并打出 `v0.2.0`（已完成；接入以去 ARK 化改名后的 `v0.3.0` 为准），再回到本文 P2/P3/P4
做 asa-server 侧的迭代**。M5 的三个阻断项与 M5-4/M5-5 都已完成（`v0.1.0` 已打 tag），
但 **P2 不要在 `v0.3.0` 之前开始**：M7-4 会改掉 `pkg/client.Config` 的字段与协议，
提前写的 `filesyncmanage` 配置结构与接线要整段返工。
