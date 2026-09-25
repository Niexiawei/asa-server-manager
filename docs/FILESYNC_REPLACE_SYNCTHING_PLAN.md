# 用 simple-file-sync 替换 Syncthing 的可行性评估与实施计划

> 状态：**P2 设计已细化（§8），决策已拍板（§10、§11）**。下一步：同步库补 M7-6 join blob 并发 `v0.4.0`，然后开始 P2。
> 评估对象是同步库 **simple-file-sync**（本机工作副本 `D:\golang\ark-asa-file-sync`，目录名是历史遗留；
> go.mod 模块名 **`github.com/Niexiawei/simple-file-sync`**，远端 `git@github.com:Niexiawei/simple-file-sync.git`），
> 以及本仓库 `internal/syncthingmanage/` 的现状。
>
> **接入版本：`v0.4.0`**（M7-6 join blob 发布后；截至 2026-09-25 的最新版是 `v0.3.1`）。**不要用 `v0.3.0`**——它有一个回归：新增的同步根可能永远收不到组内已有内容
> （订阅与补发请求走两条发送队列、可能乱序，见 §3.2）。各版本变化见同步库根目录的 `CHANGELOG.md`。
>
> 同步库定位为与 syncthing 同类的**通用文件同步库**，不含任何 ARK 专属的命名、默认值或逻辑：
> proto 包 `filesync.v1`、环境变量 `FILESYNC_*`、指标 `filesync_*`、agent 元数据目录 `.filesync/`、协调端数据库 `filesync.db`。
> 本文里所有 ARK 专属的取舍——clusters 参数、存档 Root 与实例生命周期绑定、排除哪些 ARK 文件——
> 都是**本仓库 `internal/filesyncmanage` 的职责**，用同步库的通用 SDK 能力实现，**不要推回同步库**。
>
> 同步库的现状以它的 `CHANGELOG.md`、`docs/user-guide.md` 与各里程碑文档为准。
> 本文结论以**读源码 + 本机实测**为准（最近一次：2026-09-25，Windows 与 WSL2 Linux 全量 `go test -race ./...` 通过）。

---

## 1. 结论先行

| 问题 | 结论 |
|---|---|
| **能不能替换？** | **能**。原先的三个阻断项里，同步库侧的两个已修复（§6.1、§6.2）；剩下的 §6.3 本来就由本仓库负责，而首期只做 `clusters/`，碰不到它。 |
| **最大收益** | 干掉一个独立 exe 进程、一份 30MB 的按需下载，以及让用户手改 XML 的配置方式；换成**库内调用 + 表单配置**，与 `frpmanage` 的既有形态一致。 |
| **最大代价** | 需要**自建并长期运营一个公网 Coordinator**，而且它是**数据面**（所有字节都经它中转、落盘）。M6 块级增量把流量成本降了一个量级（§6.4），但"要自己运维一台公网机器"这件事没变，这也是唯一不可逆的决策点。 |
| **首期落地范围** | **只同步 `{BaseDir}/clusters/<ClusterID>/`**（集群传输数据），不碰存档。 |
| **开工前还差什么** | 同步库补上 join blob（M7-6，§7-13）。接入凭据两种方式都做、页面上切换（§10-7）。 |

---

## 2. 现状：Syncthing 集成到底花了多少成本

`internal/syncthingmanage/`（4 个文件，约 600 行）+ `app/src/views/SyncthingManager.vue`（566 行）：

| 项 | 现状 | 痛点 |
|---|---|---|
| 二进制 | 首次使用时从 GitHub 下载 pin 住的 v2.0.11（`install.go`），解压进 `{BaseDir}/syncthing/` | 多一次外网依赖；国内要靠 `download.github_proxy` |
| 进程 | `exec.Command(syncthing, "serve", "--home", dir, "--no-browser", "--no-restart", "--no-upgrade")`，用 `pkg/proctree` 管进程树 | 独立进程：崩溃、端口占用、孤儿进程都要自己兜；`asyncStart` 里那套 `done/ctx/1s 轮询` 的状态机，是为了应付"进程可能立刻退出"而写的 |
| 配置 | **前端 Monaco 直接编辑 `config.xml` 原文**，保存后重启进程 | 用户要手工填 device ID、folder 共享关系、rescan 间隔——这就是"配置复杂"的根源。对端设备 ID 还得从另一台机器的 syncthing GUI 里抄 |
| 状态 | 只有 `running` / `stopped`（进程在不在） | 同步进度、单文件传输、冲突、落后多少，全部**不可见** |
| 日志 | stdout 打进 `logger`，前端按 `[syncthing]` 前缀过滤 | 唯一还算好用的部分，可原样复用 |
| 鉴权 | syncthing 的 GUI/API 有自己的一套，与本程序的 `auth` 完全无关 | 两套账号体系 |

**替换的价值判断**：值钱的不是"省一个进程"，而是**把"同步"变成本程序的一等公民**——
状态能进 `/api/server/all-info`，迁移能被 `countdown`/`batchmanage` 编排，配置能进本程序的权限体系。
这三件事在"外挂一个 syncthing 进程"的形态下都做不到。

---

## 3. simple-file-sync 现状盘点

### 3.1 能力（截至 `v0.3.1`）

架构：**Coordinator（公网中心节点，中转 + 记账）↔ 多个 Agent（主动外联，可在 NAT 后）**。
一个 Node = 一台机器的身份 + **一条** gRPC 连接；一个 Node 可挂**多个 Root**；
每个 Root 属于一个 Group，同 Group 的 Root 内容互相同步。

- **信任模型**（`v0.2.0` 起）：协调端首启**自举 PKI**；agent 用人工分发的**引导证书**首次接入，
  在本机生成密钥、协调端签 CSR，换得一张**绑定 `node_id` 的节点证书**；节点证书有效期 1 年，到期前 90 天自动续期并重连。
  集群 token 已删除。
- **同步语义**：删除传播（墓碑，补发时也会下发）、冲突裁决（CAS + LWW + `*.sync-conflict-*` 副本，**默认保留 10 份**，
  与 syncthing 一致）、周期全量扫描兜底、事件聚合（`WatchDelay` 静默期 / `WatchTimeout` 硬上限）、
  删除后同名重建可正常同步、订阅补发（新节点能收到组内历史）。
- **传输**：**块级增量**（`v0.3.0`，M6）——上传与下载都只传变化的块，并从本地旧版本与未完成的临时文件复用块；
  通过能力协商，旧端退回整文件传输。整文件 SHA-256 校验后原子替换。
- **本地策略**：`RootConfig.Exclude`（gitignore 风格锚定，收发两个方向都生效）、`MaxFileBytes`；协调端另有一道大小闸门。
- **迁移 API**：`Root.FlushNow()` + `WaitApplied()` / `WaitPathsApplied()`，`ErrTasksDead` 可以用 `errors.Is` 区分。
- **可观测**：`Node.Stats()` / `Root.Stats()` 纯本地读取（含在途传输的 bps/offset/ETA 与块级计数）；
  `NodeState` 暴露实际连到的协调端地址与地址变化次数（DDNS 诊断）。
- **事件**（`Config.OnEvent`）：连接/断开、根被拒/失败、任务失败、文件已应用、证书临期、节点 ID 冲突、鉴权失败、
  已接入（enroll）、证书已续期、**冲突副本已保存**（附原因）、**远程指令**。
- **远程指令**（`v0.3.0`，M10-7）：协调端可请求 agent 执行 status / rescan / request_backfill / list_conflict_copies，
  **默认全部关闭**，由宿主用 `Config.RemoteCommands` 开启。本程序首期只开 `status` 与 `rescan`（§10-5）。
- **协调端运维**（`v0.3.0`，M10）：YAML 配置文件启动（`coordinator -c config.yaml`，配置类命令行参数已移除）、
  `service install` 以系统服务运行、自带网页界面（概览/节点/任务/传输/冲突/版本历史/审计，可重试任务、吊销节点、恢复版本），
  以及**版本历史**（M9）。⚠️ 这批功能的**人工验收一项都没做过**（同步库 M10 文档「未闭环项」）——
  在 Linux 上部署协调端时，实际上就是在做它的 systemd 验收。
- **嵌入式优先**：`pkg/client` 是给宿主嵌入用的；`ScanInterval`/`StatsReportInterval` 默认关闭，库不会启动宿主没要求的后台行为。

### 3.2 `v0.3.x` 的质量核对（2026-09-25）

接入前在同步库里做了一轮 Windows + Linux 全量 `-race` 测试，查出并修复了四个真实缺陷，都在 `v0.3.1` 里：

| 缺陷 | 对 clusters 场景的影响 | 修复 |
|---|---|---|
| 一个 `WatchDelay` 窗口内**先改后删**的文件，删除被吞掉 | 一台服务器删掉了传输档，另一台还留着 → **有复制角色的风险** | `c973a7e` |
| 后加入的节点自己的较新版本，被补发的旧版本覆盖 | 违背 LWW，新内容只留在冲突副本里 | `f984997` |
| 同步程序写入或删除某文件的那一刻，本地对同一文件的修改被丢弃 | 未开 `ScanInterval` 时永远不会上报 | `53256b4` |
| **`v0.3.0` 回归**：新增同步根的订阅与补发乱序 | 该根**永远收不到**组内已有文件 | `7154895` |

另外修了 5 处测试本身的时序问题（其中 2 处在 Linux 上约有一半概率失败）。
**教训**：同步库此前的测试主要在 Windows 上跑，Linux 上的时序差异暴露出了前两个缺陷。协调端要部署在 Linux 上，
**以后升级同步库版本，都要在 WSL 里跑一遍全量 `-race`**，再改本仓库的 `go.mod`。

---

## 4. 与 Syncthing 的能力对照

| 维度 | Syncthing | simple-file-sync `v0.3.1` | 对本项目的影响 |
|---|---|---|---|
| 拓扑 | P2P 直连 + NAT 打洞，中继兜底 | **必须**经 Coordinator 中转 | ⚠️ 要自建公网节点 |
| 部署形态 | 独立进程 + XML 配置 | **库内调用**，配置由宿主定义 | ✅ 与 frpmanage 一致 |
| 增量算法 | 块级交换 | **块级增量**（M6，下标对齐比较，不做滚动哈希） | ✅ 与 syncthing 同一思路 |
| 冲突 | `*.sync-conflict-*` | 同名同形，默认保留 10 份 | ✅ |
| 忽略规则 | `.stignore` | `RootConfig.Exclude`（收发两个方向都生效） | ✅ |
| 初始同步 | 新设备加入会拉全量 | 订阅补发（含删除） | ✅ |
| 版本保留 | 每台设备本地 `.stversions/` | 协调端中心化版本历史 | ✅ 比 syncthing 更适合做恢复入口 |
| 进度可见性 | 自带 GUI | `Stats()` 结构化数据，可直接渲染；协调端另有网页界面 | ✅ 比现在强得多 |
| 鉴权 | 自成体系 | 引导证书人工分发一次，之后每节点一张证书，自动续期 | ✅ 可并入本程序 `auth` |
| 迁移编排 | 无 | `FlushNow` + `WaitApplied` | ✅ 本项目独有需求，syncthing 给不了 |

---

## 5. 替换后在本仓库里的落点

### 5.1 可导入的只有 `pkg/`

同步库把 `coordapp`/`coordcli`/`agent`/`coordinator` 全放在 `internal/` 下，按 Go 的 `internal` 规则，
**本仓库只能 import `pkg/client`、`pkg/logger`、`pkg/manifest`**。
后果：**Coordinator 无法嵌进 asa-server**，只能作为独立二进制部署（已定：Linux 独立机器，§10-1）。
同步库有守卫测试锁住 `pkg/client` 的依赖面：它不会把 gin、viper、websocket、kardianos/service 带进本仓库。

### 5.2 同步 Root 只能挂"真实目录"，不能挂镜像

本仓库镜像（`internal/mirror`）里的 `Saved/<SaveDir>`、`Config/WindowsServer`、`Logs` 都是 junction，
而同步库**跳过符号链接、也不跟随**。首期唯一的 Root 形态：

| 用途 | Root 路径 | Group ID |
|---|---|---|
| **集群传输数据**（首期唯一） | `{BaseDir}/clusters/<ClusterID>` | `cluster-<ClusterID>` |
| 单实例存档（推迟，§6.3） | `{BaseDir}/instances/<name>/Save` | `save-<迁移组名>` |

`{BaseDir}/clusters/<ClusterID>` 就是实例启动时经 `-ClusterDirOverride` 交给游戏的目录（`internal/instance/server.go:386` 起），
同机多实例本来就共用它；同步只是把它延伸到另一台机器。

### 5.3 为什么首期是 clusters/ 而不是存档

- **文件特征匹配**：集群传输文件是**小文件、写完即终态、很快被对端读走并删除**，删除传播刚好用得上。
- **避开运行中写入的雷**：游戏对 clusters 目录是"放上去/取走"的用法，不像存档那样被进程长期持有。
- **收益最直接**：跨机器集群现在**根本做不到**。
- **延迟要求**：玩家在方尖碑前等着，默认 `WatchDelay=10s` 太慢，clusters 用 `1s / 5s`，并显式打开 `ScanInterval=5m` 兜底（§8 P2）。

---

## 6. 原阻断项的处理结果

### 6.1 新节点收不到组内历史文件 —— ✅ 已修复（同步库 M5-1，`v0.1.0`）

新增 `RequestBackfill` 协议消息与 `Store.BackfillDownloads`：agent 在初始扫描上报**之后**请求补发。
`v0.3.0` 起补发也会下发保留期内的墓碑删除；`v0.3.1` 修掉了补发的两处顺序漏洞（§3.2）。
**P5 真机验收仍需正面验证一次**："B 服后加入集群组，能看到 A 服先前上传的角色"。

### 6.2 没有忽略/包含规则 —— ✅ 已修复（同步库 M5-2/M5-3）

`RootConfig.Exclude`（六个插入点：扫描、watch 建 watcher、watch 事件、单文件重扫、删除上报、收方向 `localPath`）
与大小闸门（agent 侧 `MaxFileBytes` + 协调端配置项 `limits.max_file_bytes`）。
**排除是本地策略**：同组节点的排除列表不一致时，排除方会拒收，直到任务进入 dead——
所以本程序应当**对所有节点下发同一套排除规则**（写死在 `filesyncmanage` 里，不让用户逐台配）。

### 6.3 向"运行中"实例的存档目录落盘会失败或损坏存档 —— 由本仓库负责，首期避开

同步库的替换就是 `os.Rename`：Windows 上被游戏进程占用会失败，进入 dead；即使成功，运行中的游戏下次保存也会把同步进来的内容覆盖回去。
**存档类 Root 必须与实例生命周期绑定**：实例已停止才挂上，启动前 `RemoveRoot`。这条规则由 `internal/filesyncmanage`
强制执行（P4），clusters Root 不受此限。

### 6.4 中心节点是数据面：成本模型 —— M6 之后大幅改善

- `v0.3.0` 起是**块级增量**，一个大文件只改了一部分时，跨网字节数与改动量同量级。旧估算"200 MB 存档、15 分钟一存、2 节点
  ≈ 1.6 TB/月"是整文件传输时代的数字，**已不适用**；实际数字取决于存档两次保存之间的块复用率，
  同步库的 M6-0 基准测量**仍缺真实样本**——本程序可以在 P4 前提供真机 `.ark` 连续两次保存的样本。
- clusters 文件是 KB 级，首期无论如何都不构成成本问题。
- 存档同步仍应走"停机迁移 + `WaitApplied`"（§6.3），块级增量只是让它便宜，不是让运行中同步变得安全。

---

## 7. 重要但非阻断（P1）

| # | 事项 | 说明 |
|---|---|---|
| 1 | Coordinator 不可嵌入 | §5.1，已接受：独立部署。 |
| 2 | 依赖引入方式 ✅ 已定 | **GOPRIVATE**（同 `go-arkparser`），`go env -w GOPRIVATE=github.com/Niexiawei/*`，然后 `go get github.com/Niexiawei/simple-file-sync@v0.4.0`。 |
| 3 | 新增依赖面 | 净新增 `google.golang.org/grpc` + `genproto`；**`modernc.org/sqlite` 会从 1.57 升到 1.58**，而本仓库的 `auth.db` 在用它——升级后要回归 `internal/auth` 的测试与 `asa-server db verify`。 |
| 4 | 日志接线 | 库走 `log/slog`，本仓库 `pkg/logger` 是 zap 包级函数。需要一层 `slog.Handler` → `pkg/logger` 适配，记录带 `[filesync]` 前缀，前端复用现有日志过滤。通过 `client.Config.Logger` 按节点注入，**不调用**同步库的 `logger.SetDefault`（那是进程级的）。 |
| 5 | ~~Windows 无 SIGHUP~~ ✅ | 凭据热重载已改为文件监视 + `ReloadCredentials` RPC。 |
| 6 | ~~证书不自动续期~~ ✅ | 节点证书到期前 90 天自动续期（M7-5）。本程序仍需把 `EventCertificateExpiringSoon` 接进告警/前端红点——续期一直失败时，它就是升级信号。 |
| 7 | 单节点吊销 ✅ | 协调端网页界面或 `node revoke`。**运维负担**：机器换盘、丢私钥或离线超过 1 年，都要在协调端人工 `node reset` 一次。 |
| 8 | 目录批量删除不保证逐文件传播 | 事件被聚合成目录级时只记一条 WARN。对 clusters 用例影响小（传输档不在子目录里批量删）。 |
| 9 | **Linux 降权属主** | `clusters/` 在 `runner` 的独占目录清单里（`runtimeuser_linux.go:164`），整棵交给运行时用户。同步库以 asa-server 身份（root）落盘的文件属主是 root，**降权运行的游戏删不掉、改不了**。P2 必须在每次落盘后对该文件补 chown（§8 P2 `perm_*.go`）。 |
| 10 | Coordinator 写路径单连接 | SQLite `MaxOpenConns(1)`，小规模无所谓。 |
| 11 | ~~模块名文档过时~~ ✅ | |
| 12 | 同步库去 ARK 化改名 ✅ | `v0.3.0` 起的命名，本程序按新名写，不存在迁移问题（尚未写过接入代码）。 |
| 13 | ⚠️ **join blob 未实现** | 原计划前端"粘贴一行 join blob（地址 + CA + 引导证书）"就能接入。同步库 `docs/证书自动续期计划.md` M7-3 把它推迟到 M7-4，**M7-4 落地时也没有做**（`docs/user-guide.md` 末尾仍写着"M7-3 的 join blob 仍未实现"）。协调端现在只会在 `<data_dir>/client-bundle/` 下生成 `ca.crt`、`client.crt`、`client.key` 三个文件。**已决定**：同步库补 M7-6，同时保留三文件方式，页面上切换（§10-7、§11）。 |
| 14 | `MaxConflicts` 零值语义变了 | `v0.3.0` 起零值 = 默认保留 10 份（原来零值是直接丢弃）。本程序不设这个字段，用默认值。 |
| 15 | 冲突副本落在 clusters 目录里 | 冲突副本命名为 `<名字>.sync-conflict-<时间>-<节点>.<扩展名>`，扩展名保留在末尾。**游戏会不会把它当成一个可下载的角色**，需要在 P5 真机验证。会的话，本程序要在收到 `EventConflictCopySaved` 后把副本挪出 clusters 目录（例如挪到 `{BaseDir}/filesync/conflicts/`）。 |

---

## 8. 实施计划（分阶段，syncthing 全程并存）

> 总原则：**syncthing 不下线，直到新链路在真机上跑通一个完整的"玩家跨服传输"闭环**。
> 两者可以同时存在（Root 路径不同、进程互不相干）。

### P0 — 决策与前置 ✅（2026-09-12）

见 §10。

### P1 — 同步库侧 ✅（2026-09-12 起，2026-09-25 收尾）

- [x] 订阅补发（M5-1）、排除规则（M5-2）、大小上限（M5-3）、交付契约（M5-4）、跨平台凭据热轮换（M5-5）。
- [x] 新信任模型（M7-0/M7-1/M7-3/M7-4/M7-5），`v0.2.0`。
- [x] 去 ARK 化改名、块级增量（M6）、冲突与版本历史（M9）、配置文件启动 / 服务模式 / 网页界面 / 远程指令（M10），`v0.3.0`。
- [x] 接入前质量核对：Windows + Linux 全量 `-race`，修复 §3.2 的四个缺陷，新增 `CHANGELOG.md`，**`v0.3.1`**。
- [ ] **M7-6 join blob**（§10-7）：设计已写进同步库 `docs/证书自动续期计划.md` M7-6——新包 `pkg/joinblob`（零依赖编解码）
      + `coordinator join-blob` 子命令，发 **`v0.4.0`**（纯新增）。P2 接入 `v0.4.0`。

### P2 — 本仓库新增 `internal/filesyncmanage`（照 `frpmanage` 的形态）

**依赖层级**：`filesyncmanage` 依赖 `config`(cfgpkg)、`runner`（Linux 属主）、`realtime`（WS 事件）、`pkg/logger`，被 `webapi` 依赖。
**不**依赖 `instance`（会成环）；P4 的"实例停了才挂存档 Root"由 `webapi`/`countdown` 侧调用它来实现。
`runner` 与 `realtime` 都在 `instance` 之下，不会引入新环。

**进程内对象**：包级单例 `*Manager`（`Initialize` / `GetGlobalManager`，与 `frpmanage` 同构），持有至多一个 `*client.Node`。

#### P2-1 `config.go`：结构化配置，落 `{BaseDir}/filesync/config.json`（0600）

```go
type Config struct {
    Enabled bool   `json:"enabled"`           // 关闭时 Start 不连协调端，但保留配置
    Address string `json:"address"`           // 协调端 host:port
    Label   string `json:"label,omitempty"`   // 显示名，只给协调端界面看
    // 引导凭据：只用于首次接入换取节点证书。接入成功后可以清掉，节点照常工作。
    // 两种填法（§10-7）落到同样这三个字段 + Address：join blob 在 API 层用 pkg/joinblob 解开，不单独存。
    CAPEM            string `json:"ca_pem"`
    BootstrapCertPEM string `json:"bootstrap_cert_pem,omitempty"`
    BootstrapKeyPEM  string `json:"bootstrap_key_pem,omitempty"`
    UploadLimitKBps   int64 `json:"upload_limit_kbps,omitempty"`   // 0 = 不限；同一台机器还在跑游戏服务器
    DownloadLimitKBps int64 `json:"download_limit_kbps,omitempty"`
    Clusters []ClusterRoot `json:"clusters"`
}

type ClusterRoot struct {
    ClusterID string `json:"cluster_id"` // Root 路径与 Group ID 都由它推出，用户不填路径
}
```

- **路径与 Group ID 不进配置**，由 `ClusterID` 推出（§5.2）——用户不应该能把 Root 指到任意目录。
  `ClusterID` 校验：非空，不含路径分隔符与 `..`，列表内不重复。
- **同步参数不进配置**，写死成 clusters 的推荐值：`WatchDelay=1s`、`WatchTimeout=5s`、`ScanInterval=5m`、
  `EnableHashCache=false`、`MaxConflicts` 取默认、`Exclude` 取本程序统一的一份（首期为空，§6.2 说明了为什么必须统一）。
- 没配过 → `ErrNotConfigured`，与 `frpmanage` 相同：只记 INFO，不刷 ERROR。
- `GET` 接口**永不回传** `bootstrap_key_pem`，只回 `has_bootstrap: true/false`；`PUT` 时字段为空表示"不修改"。
- `PUT` 的请求体支持两种凭据写法，**二选一，同时给就报 400**：
  - `join_blob`：服务端 `joinblob.Decode`，解出的 `Address` 与三份 PEM 覆盖进配置（表单里的地址框随之更新）；
  - `ca_pem` / `bootstrap_cert_pem` / `bootstrap_key_pem`：直接写入，与 `address` 字段一起提交。
    服务端做与 `joinblob.Decode` 相同的结构校验（密钥对配对、证书由 CA 签发），两种写法的出错信息一致。
- 另给一个只读的 `POST /api/filesync/join-blob/inspect`：解码但不保存，返回地址、CA 指纹、引导证书到期时间，
  供前端在"保存"前预览"将连接到哪里"。

#### P2-2 身份与凭据存储

| 文件 | 内容 | 来源 |
|---|---|---|
| `{BaseDir}/filesync/node-id` | 稳定的 node id | `client.NewFileIdentityStore` |
| `{BaseDir}/filesync/node.crt` / `node.key`（0600） | 本机 enroll 得到的**身份**凭据 | `client.NewFileNodeCertificateStore`（库自带，不用自己实现接口） |

身份凭据**不放进 `config.json`**：配置可能被备份或导出，身份私钥不应该跟着走。
**UI 提供"重置本机身份"**（删掉 `node.crt`/`node.key` 后重连），配合协调端的 `node reset`，用于换机或私钥泄露。

#### P2-3 `manager.go`：生命周期

- `Start`：`client.New(client.Config{Address, Label, CAPEM, CertPEM/KeyPEM(引导), NodeCertificates, Identity,
  Logger: 适配器, OnEvent, StatsReportInterval: 5s, RemoteCommands: {status, rescan}, 上下行限速})` →
  `go node.Run(ctx)` → 对每个 `ClusterRoot`：`MkdirAll` 目录、Linux 上交给运行时用户（与 `instance/server.go:399` 同一套调用），然后 `AddRoot`。
- `Node.Run` 返回（`EventAuthenticationFailed` / `EventNodeIDConflict` 这两种终态）→ 状态置为 `failed` 并记下原因，**不自动重启**
  （库文档明确：这两种是配置问题，不是瞬时故障）。
- `Stop`：`node.Shutdown()`。库约定 Shutdown 之后节点不可复用，**Restart = 新建一个 Node**。
- `SetConfig` 的差量策略（对照 `frpmanage` 用 `UpdateConfigSource` 热更新）：
  **只有 `Clusters` 变了** → 对差集 `AddRoot`/`RemoveRoot`，不断连；**连接相关字段变了**（地址、凭据、限速、Label）→ 重建 Node。
- 状态模型：`not_configured | disabled | connecting | connected | failed`，外加 `node_id`、证书到期时间
  （`Node.CertificateExpiry()`）、`NodeState`（`LastConnectErr`、`RemoteAddr`、`AddressChanges`）、每个 Root 的
  `RootState` 与 `RootStats`（在途传输、块级计数）。

#### P2-4 `logbridge.go`：slog → `pkg/logger`

一个 `slog.Handler`：按级别转调 `logger.Debugf/Infof/Warnf/Errorf`，消息前缀 `[filesync]`，属性拼成 `k=v`。
通过 `client.Config.Logger` 注入。

#### P2-5 `events.go`：`OnEvent` 的去向

| 事件 | 日志级别 | 推前端（`realtime`，事件类型 `filesync`） |
|---|---|---|
| `EventSessionConnected` / `Disconnected` | INFO / WARN | ✅ 连接状态 |
| `EventEnrolled` / `EventCertificateRenewed` | INFO | ✅ |
| `EventCertificateExpiringSoon` | WARN | ✅ 红点（续期一直失败才会升级到这里） |
| `EventAuthenticationFailed` / `EventNodeIDConflict` | ERROR | ✅ 标红。后者在新信任模型下意味着**节点私钥可能被复制到了另一台机器** |
| `EventRootRejected` / `EventRootFailed` / `EventTaskFailed` | WARN | ✅ |
| `EventConflictCopySaved` | WARN | ✅（另见 §7-15） |
| `EventRemoteCommand` | INFO | ❌（只记日志，作为审计痕迹） |
| `EventFileApplied` | DEBUG | ❌；Linux 上触发 P2-6 的 chown |

`OnEvent` 在库的 goroutine 里调用，**不能阻塞**：推送用非阻塞投递。

#### P2-6 `perm_linux.go` / `perm_windows.go`：落盘后的属主

`EventFileApplied` → 对 `{BaseDir}/clusters/<ClusterID>/<Path>` 调用 `runner` 已有的降权 chown（单文件，不遍历整棵树）；
Windows 是空实现。`runner` 目前只导出整棵树的 `ChownTreeForRuntime(root)` 与 `ChownMirrorForRuntime`，没有单文件入口——
在 `runner` 里补一个薄封装 `ChownPathForRuntime(path)`，转调 `sysUserFor(cfg).ChownOne(path)`（`pkg/sysuser` 已有），
Windows 上恒为空操作。
同步库在 Root 下创建的 `.filesync/` 元数据目录与 `.part` 临时文件属主是 root，这不影响游戏。

#### P2-7 生命周期接线

`internal/webapi/actions.go` 的三处，与 syncthing 并列：`InitializationBasicComponents`（`Initialize`）、`Start`
（`ErrNotConfigured` 记 INFO）、`Stop`。

#### P2-8 依赖与测试

- `go.mod`：`github.com/Niexiawei/simple-file-sync v0.4.0`；回归 `internal/auth`（sqlite 1.58，§7-3）。
- 单元测试：配置校验与 JSON 往返、`GET` 不回传私钥、差量策略（哪些变化热更新、哪些重建）、日志适配器、状态映射。
- **集成测试写不了进程内协调端**：`coordapp` 在同步库的 `internal/` 下，本仓库 import 不到。端到端放到 P5，
  用真实部署的协调端验证；如果以后需要自动化，就在测试里拉起同步库的 `coordinator` 二进制。

### P3 — API 与前端

- [ ] `internal/webapi/filesyncapi/`（按领域拆子包，与 `pluginapi` 等一致；不照抄 `frpmanage` 把路由放在领域包里的做法）：
      `GET/PUT /api/filesync/config`、`GET /api/filesync/status`、`GET /api/filesync/status/stream`（SSE，2s）、
      `POST /api/filesync/{start,stop,restart}`、`POST /api/filesync/identity/reset`、
      `GET /api/filesync/clusters`（从各实例配置里收集已有的 `ClusterID`，供下拉选择）。
      **写操作挂 `authapi.RequireAdmin()`**（`frp` 的写路由目前没挂，那是另一个问题，不在本期范围）。
- [ ] `app/src/views/FileSyncManager.vue`：**表单 + 状态面板**（照 `FRPManager.vue`，不用 Monaco）。
      左栏：**接入方式切换**（`t-radio-group`，§10-7）——
      「粘贴接入字符串」：一个多行输入框，失焦时调 `inspect` 预览地址与 CA 指纹；
      「上传证书文件」：协调端地址 + `ca.crt` / `client.crt` / `client.key` 三个文件选择（前端 `FileReader` 读成文本，
      随 JSON 提交，不走 multipart）。已保存过凭据时两种方式都显示"已配置，留空则不修改"。
      然后是集群下拉多选 + 限速；**接入状态**一行：
      未接入 / 已接入（节点证书剩余 N 天），临期或失败标红。
      右栏：连接状态、每个集群的待处理/在途/失败、实时传输进度 + 按 `[filesync]` 过滤的日志。
      别忘了 `App.vue` 的**三处**联动（菜单项、`watch(route.path)` 高亮、`handleMenuClick` 分支）。

### P4 — 与实例生命周期/迁移编排打通（本项目独有价值，首期之后）

- [ ] 存档类 Root 的守卫：实例启动前强制 `RemoveRoot`，停止后才 `AddRoot`（§6.3）。
- [ ] 迁移动作（在 `countdown`/`batchmanage` 之上）：**停实例 → `FlushNow` → `WaitApplied`（`ErrTasksDead` 单独报错）→ 通知目标节点**。
      目标侧同样要 `WaitApplied` 后才允许启动——库文档明确"源端收敛不代表目标端已收到"。
- [ ] 给同步库的 M6-0 基准提供真机 `.ark` 样本（§6.4）。

### P5 — 真机验收与 syncthing 退场

- [ ] 在 Linux 机器上部署协调端（`service install` + systemd），顺带完成同步库 M10-2 的 Ubuntu 验收项。
- [ ] 两台真实机器、同一 `ClusterID`、跨服角色传输闭环跑通，**含"B 服后加入"**（§6.1）。
- [ ] 冲突副本是否会被游戏识别为角色（§7-15）。
- [ ] Linux 降权运行时，游戏能读写、删除同步进来的文件（§7-9）。
- [ ] 断网/重连、协调端重启、证书临期告警三条异常路径各验一次。
- [ ] 以上全部通过之后，再讨论是否移除 `internal/syncthingmanage` 与 `SyncthingManager.vue`（**独立提交**，便于回滚）。

---

## 9. 风险与回滚

| 风险 | 缓解 |
|---|---|
| 公网 Coordinator 挂了 ⇒ 全组停止同步 | Agent 自动重连（指数退避），本地文件不受影响；`RootState.Failed` 接进前端红点。协调端的 `filesync.db` + `objects/` 要**同盘一起备份**。 |
| 从旧备份恢复 Coordinator | 库有 `store_epoch` 机制，会触发全量核对而不是静默丢数据——但会有一次全量开销，这是正常现象。 |
| 双写同一文件 | LWW + `*.sync-conflict-*` 副本（默认 10 份）；游戏是否识别副本见 §7-15。 |
| 私有仓库依赖导致他人构建失败 | GOPRIVATE，P0 已定。 |
| **协调端与 agent 版本不匹配** | `v0.2.0`、`v0.3.0` 都是不能灰度的破坏性协议变更。锁死在 `go.mod` 的同一个 tag 上，协调端部署同一版本；前端状态面板把"连不上"与"版本不匹配/鉴权失败"区分开。 |
| **同步库回归** | `v0.3.0` 就出过一次（§3.2）。升级同步库版本前，先在 WSL 里跑它的全量 `-race`，再改 `go.mod`。 |
| **节点证书过期需人工干预** | 离线超过 1 年或换盘丢私钥的机器，必须在协调端 `node reset`。前端**提前**显示剩余天数。 |
| 回滚 | syncthing 全程在位；`filesync` 未配置时 `ErrNotConfigured` 短路，零副作用。回滚 = 前端不用那个页面。 |

---

## 10. 已定的事

1. **Coordinator**：Linux 独立机器，用户自行部署运维。（2026-09-12）
2. **依赖**：GOPRIVATE，同 `go-arkparser`。（2026-09-12）
3. **首期范围**：只做 `clusters/`，存档同步不在本期。（2026-09-12）
4. **信任模型**：协调端自举 PKI；引导证书首次接入，换取绑定 `node_id` 的节点证书，1 年有效、到期前 90 天自动续期；
   身份被占用的唯一恢复路径是人工 `node reset`。（2026-09-12）
5. **远程指令**：`Config.RemoteCommands` 首期**只开 `status` 与 `rescan`**。`request_backfill` 与 `list_conflict_copies`
   不开：前者有排序约束，后者会把本机的文件清单暴露给协调端。（2026-09-25）
6. **接入版本**：~~`v0.3.1`~~ → **`v0.4.0`**（M7-6 join blob 发布后）。（2026-09-25）
7. **接入凭据两种方式都做，页面上切换**：粘贴 join blob（同步库补 M7-6），或上传三个证书文件。两者落到同一份配置。（2026-09-25）
8. **冲突副本先不挪**，只记日志、推前端；是否挪出 clusters 目录等 P5 验证游戏是否识别副本后再定（§7-15）。（2026-09-25）

---

## 11. 开工前的决策（已拍板，2026-09-25）

- **11-1 接入凭据**：原先在"A. 先在同步库补 join blob"与"B. 表单直接填三个 PEM"之间二选一，
  结论是**两种都做，页面上切换**（§10-7）。两者落到同一份 `Config`，受影响的只有 P3 的表单与 P2-1 的 `PUT` 请求体。
  同步库侧的 join blob 设计见其 `docs/证书自动续期计划.md` M7-6，与原设想有两处不同：
  编解码放在零依赖的新包 `pkg/joinblob` 而不是 `pkg/client`；协调端**自举时不打印** blob（会把引导私钥写进日志文件），
  改为提示运行 `coordinator join-blob`。
- **11-2 冲突副本**：**先不挪**，只记日志、推前端（§10-8）。挪动本身会被同步库视为一次本地删除；冲突副本本来就不参与同步，
  所以挪走应当是安全的，但要等 P5 有结论、真要挪时再用测试证实。

**执行顺序**：同步库 M7-6 → 打 `v0.4.0` → 本仓库 P2（`go get ...@v0.4.0`）→ P3。
