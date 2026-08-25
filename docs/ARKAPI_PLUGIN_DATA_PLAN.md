# ArkApi 插件数据与配置隔离 —— 改造方案

> 问题：ArkApi 插件把**运行期数据**和**插件二进制**放在同一个目录里。以 Permissions 插件为例，
> 它的 SQLite 库存着玩家在本服的权限组，被镜像同步当成普通文件对待，
> 导致每次同步都被源目录的版本覆盖；而且它落在临时的镜像目录里，随镜像清理一起消失。
>
> 状态：**已实施**（P1–P7 全部落地，见 §7）。实现落在 `internal/plugindata/`
> （搬运 / 合并 / 快照）、`internal/webapi/pluginapi/`（HTTP 接口）、
> `app/src/components/PluginDataPanel.vue`（前端），并在 `internal/mirror` 与
> `internal/instance` 上各接了几处钩子。实施中与本文不一致的地方记在 §10。
> 关联文档：[`MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md`](./MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md)
> （第一部分已去掉管理员提权，本方案必须在无特权前提下成立）、
> [`V2_MIRROR_STARTUP_ARCHITECTURE.md`](./V2_MIRROR_STARTUP_ARCHITECTURE.md)、
> [`LINUX_COMPATIBILITY_PLAN.md`](./LINUX_COMPATIBILITY_PLAN.md) §5.12（本方案在 Linux 上应整体静默，见 §11）。

---

## 1. 结论先行

**采纳：实例插件目录 + 启停搬运，作为唯一机制。配置与数据都走双向搬运。**

```
instances/{name}/plugins/{Plugin}/      ← 每实例的插件配置与运行期数据，持久
        │  启动前注入                    ▲  停止后回收（配置按键合并、保序）
        ▼                                │
镜像 .../Win64/ArkApi/Plugins/{Plugin}/  ← 临时，随镜像清理
```

| 文件类别 | 方向 | 说明 |
|---|---|---|
| **配置**（`config.json`） | **双向** | 已验证：插件更新时会往 config.json 写入新增项，所以不能只注入不回收。回收时按键合并、**实例侧值恒优先**、**保持原有键顺序**，见 §4.6 |
| **SQLite 数据**（按文件头识别） | **双向 + 运行期快照** | 整组替换，见 §4.5；运行期定时在线快照，见 §4.9 |
| **其他运行期数据** | 双向 | 整组替换 |
| **二进制/说明**（`*.dll`、`*.pdb`、`PluginInfo.json`…） | 不搬 | 维持现状，随 Win64 整棵复制 |

**不把插件的可选配置项当作机制。** Permissions 的 `DbPathOverride`
（已验证：接受的是**目录**）确实能让 SQLite 直接写实例目录、消除崩溃窗口，
但它是某个插件的可选字段 —— 不同插件有没有、叫什么、语义如何都不保证，
拿它当底座会得到一个按插件分叉的系统。**搬运是唯一机制**，`DbPathOverride`
只作为「用户已手工设置时必须识别并让路」的输入处理（§4.8）。

**原设想「用软链接把 db 注入镜像」不可行**：Windows 上文件符号链接需要
`SeCreateSymbolicLinkPrivilege`（提权逻辑已随镜像去管理员化删除），
NTFS junction 只能链目录；硬链接虽免特权，但 SQLite 的 `-wal`/`-shm` 会被动态删除重建，链接随即失效。

搬运方案**有一个崩溃窗口**，靠 §4.5 的「回收优先」规则兜底、§4.9 的在线快照收窄。

---

## 2. 现状：问题的真实机制

`server-files\ShooterGame\Binaries\Win64\ArkApi\Plugins\Permissions\` 实际内容：

```
    4,096  ArkDB.db          ← 主库，几乎是空的
   32,768  ArkDB.db-shm      ← WAL 共享内存索引
1,973,512  ArkDB.db-wal      ← 写前日志，数据实际都在这
5,099,008  Permissions.dll
17,649,664 Permissions.pdb
      347  config.json
      132  PluginInfo.json
      475  notes.txt
           ONLY FOR DEVELOPERS/
```

三点要害：

1. **一个库的相关文件必须整组搬。** 主库只有 4 KB，1.9 MB 的数据全压在 `-wal` 里还没 checkpoint。
   只搬 `ArkDB.db` 等于丢掉几乎全部数据。
2. **数据和二进制混在同一目录**，所以不能把 `Permissions/` 整个 junction 到实例目录 —— DLL 会跟着走，插件更新断链。
3. **静止状态下就存在 1.9 MB 的 `-wal`**，说明上次退出并没有干净 checkpoint。
   ARK 服务端崩溃退出是常态，这一条直接决定了 §4.5 与 §4.9 都必须存在。

问题出在同步的**回写**上：`reconcileEntry` 对真实文件做 MD5 比对，不一致就 `CopyFile(源 → 镜像)`。
实例运行期写了 db → 与源版本 MD5 不同 → **下次同步被源版本覆盖**。
所以现象不是"几个服的权限串了"，而是**每次重启，权限被重置回源目录那一份**。

第二个隐患：**镜像目录是临时的**。`CleanupInstanceMirror` 在仓库里有 **7 个调用点**
（`server.go:618` 的 `ForceStopServer`、`mirror.go:136` 同步失败重建、`mirror.go:220/233` 创建失败回滚、
`mirror.go:532/548`），任何一个先于回收执行，数据就没了。

---

## 3. 可选机制对照（无管理员权限前提）

| 机制 | 能否作用于文件 | 需要特权 | 对 SQLite WAL 安全 | 崩溃窗口 | 结论 |
|---|---|---|---|---|---|
| 文件符号链接 | ✅ | ❌ **需要** | ❌ 悬空 | 无 | 出局 |
| 硬链接 | ✅ | ✅ 不需要 | ❌ WAL 删建后失效 | 无 | 出局 |
| NTFS junction | ❌ 仅目录 | ✅ 不需要 | ✅ | 无 | 只能整目录，见 §6 |
| **启停搬运（复制）** | ✅ | ✅ 不需要 | ✅（关库后整组拷） | ⚠️ 有，靠 §4.9 收窄 | **唯一机制** |
| 插件路径重定向 | — | ✅ 不需要 | ✅ | 无 | 不作机制，仅识别（§4.8） |
| 同步例外（不回写） | — | ✅ 不需要 | ✅ | — | 必需的配套（§5） |

---

## 4. 采纳设计：实例插件目录 + 启停搬运

### 4.1 目录布局

```
{BaseDir}/instances/{name}/plugins/
├── Permissions/
│   ├── config.json
│   ├── config.json.bak      # 每次合并前留一份镜像侧原文，出问题能回溯
│   ├── ArkDB.db
│   ├── ArkDB.db-wal
│   └── snapshots/
│       └── ArkDB.db         # 运行期在线快照，见 §4.9
└── CrosschatAscended/
    └── config.json
```

### 4.2 文件分类规则

ArkApi 的约定是每个插件一个 `config.json`（`ExtendedRcon`、`UnicodeRCONASA` 没有配置文件；
`CrosschatAscended` 另有 `config_help.json`、`NativeReusables` 另有 `commented_config.jsonc` ——
**那两个是说明文档不是配置**）。

| 类别 | 判定方式 | 处理 |
|---|---|---|
| 配置 | 文件名恰为 `config.json` | 双向搬运 + 键合并（§4.6） |
| **SQLite 数据** | **读文件头 16 字节 == `SQLite format 3\0`** | 双向搬运 + 在线快照（§4.9） |
| 其他数据 | `*.db`、`*.db-*`、`*.sqlite*`，外加每插件可扩展的额外清单 | 双向搬运 |
| 其余 | 一切其他 | 不搬 |

**SQLite 用文件头识别而不是扩展名**：插件把库命名成 `.dat`、`.bin` 都有可能，
按魔数判定才不会漏掉，也才能让 §4.9 的快照对所有 SQLite 库一视同仁。
识别到主库后，它的伴随文件按 `<主库名>-wal` / `-shm` / `-journal` 推导，构成一个**文件组**。

### 4.3 注入（启动前）

挂在 `instance.StartServer` 里 `SyncInstanceMirror` / `VerifyAndRepairInstanceMirror` **之后**、
构建命令行**之前**。放在同步之后是必须的：放在之前会被同步的 MD5 回写覆盖掉。

```
SyncInstanceMirror() → VerifyAndRepair() → rescuePluginFiles() → injectPluginFiles() → 启动
                                                  ↑ 见 §4.5
```

首次启动（实例目录下没有该插件目录）：从**镜像**目录整份播种配置与数据文件，
即"以源服务端自带的那一份为初值"，之后实例目录成为真相。

### 4.4 回收（停止后）

挂在 `instance.StopServer` 确认进程完全退出之后 —— `waitServerStopped` 已提供这个时机。
**不要在进程还活着时拷 db**：文件组之间会撕裂，拷出来的是损坏的快照。
（运行期要拿数据只能走 §4.9 的在线快照。）

以及 —— 更重要的 —— 挂在所有会销毁镜像的路径之前，见 §4.7。

### 4.5 ⚠️ 崩溃窗口与「回收优先」规则

**回收不执行的情况**：ARK 进程崩溃、机器断电、管理器自身被杀、服务停止超时，
以及 `mirror.go:136` 那条"同步失败 → 清理重建"的路径（它可能在**启动阶段**就把上一轮数据清掉）。

**规则：任何时候要覆盖或销毁镜像里的插件文件之前，先做一次抢救性回收。**

```go
// 注入之前 / 清理镜像之前都要先跑
func rescuePluginFiles(instanceName string) {
    for each 插件, each 文件组 {
        if 镜像侧该组不存在 { continue }
        if 实例侧该组不存在 || max(镜像侧组内 mtime) > max(实例侧组内 mtime) {
            // 上一轮没能正常回收（崩溃 / 强杀），镜像里的才是新的
            整组替换(镜像侧 → 实例侧)   // 配置走 §4.6 的合并
        }
    }
}
```

三条细则：

- **判定以组内最新的 mtime 为准**（`-wal` 通常比 `.db` 新得多），整组一起判定、一起搬。
- **是「整组替换」不是「逐文件覆盖」**：先删掉目标侧该组的全部文件再拷。
  否则可能出现"新的 `.db` + 残留的旧 `-wal`"这种互不匹配的组合，SQLite 打开时会拿旧 WAL 去重放。
- **崩溃后拷未 checkpoint 的文件组是正确做法**，不要因为"看起来不干净"就改用快照。
  SQLite 本来就能从未 checkpoint 的 `-wal` 恢复，整组拷过去等于把恢复现场原样搬走，
  能保住到崩溃那一刻的数据；而快照只到上次快照时间。**快照是兜底，不是首选。**

**绝不能无条件把实例侧拷进镜像。** 否则上一轮崩溃后，启动时会用陈旧的实例副本覆盖掉镜像里更新的数据，
而且**不报任何错** —— 这是最难排查的一类数据丢失。

### 4.6 配置的键合并（保序）

已验证：插件更新时会往 `config.json` 写入新增项，所以配置必须双向。
但整体覆盖会踩另一个坑：用户可能在运行期通过管理器改了实例侧配置，
此时镜像侧是"旧值 + 插件新增项"，整体拷回会把用户的改动冲掉。

**合并规则（已定，无例外名单）**：

| 键的来源 | 取值 |
|---|---|
| 两侧都有 | **恒取实例侧的值** |
| 仅镜像侧有（插件新增的默认项） | 并入 |
| 仅实例侧有（插件已删除的旧项） | 保留（无害，插件会忽略） |

> 实测未观察到插件回写已有键的值，但不排除存在。**一旦出现，按上表实例侧优先，直接覆盖掉插件的回写** ——
> 这是明确的取舍：用户在管理器里配的东西是权威，插件运行期算出来的值不保留。
> 不再维护"例外插件名单"。

**键顺序：保持原有配置文件的顺序。** 实例侧已有的键按其原本顺序输出，镜像侧新增的键追加在末尾。
这意味着**不能用 `map[string]any` + `encoding/json`**（Go 的 map 无序，序列化会重排）。
需要一个保序的 JSON 表示 —— 用 `json.Decoder` 的 token 流解析成
`[]struct{Key string; Raw json.RawMessage}`，合并后按序写回即可，不必引入新依赖。

**合并要递归。** CrosschatAscended 的配置有近 8 KB，多半是嵌套结构；
插件新增的项可能落在某个嵌套对象里，只做顶层合并会漏掉。
对象递归合并，**数组整体当作一个值**（实例侧优先），不做逐元素合并。

合并前把镜像侧原文另存为 `config.json.bak`。

### 4.7 必须先回收的调用点

`CleanupInstanceMirror` 的 7 个调用点里：

| 位置 | 场景 | 处理 |
|---|---|---|
| `server.go:618` | `ForceStopServer` 强杀后清镜像 | **必须**先回收 |
| `mirror.go:136` | 同步失败 → 清理重建 | **必须**先回收（此时可能还没走过注入） |
| `mirror.go:220/233` | 创建镜像失败回滚 | 镜像刚建到一半，无数据，可跳过 |
| `mirror.go:532/548` | 包内清理入口 | 按调用来源判断 |

实现上更稳妥的做法是**把回收做进 `CleanupInstanceMirror` 的开头**，而不是散在各调用点 —— 少一处漏掉的风险。
但 `mirror` 包不该反向依赖 `instance`，所以应给 `mirror` 加一个"清理前回调"钩子，由上层注入具体策略
（见 `docs/PACKAGE_RESTRUCTURE_PLAN.md` 的分层约束）。

### 4.8 如何对待用户手工设置的 `DbPathOverride`

不把它当机制，但**必须识别** —— 否则用户设了它之后，我们的搬运会对着一个空目录做无用功，
而真实的数据在别处不受保护，且不报任何错。

启动前读取实例侧 `config.json`：

- 为空（默认）→ 正常搬运
- 非空且指向实例插件目录内 → 正常搬运（等价形态）
- **非空且指向别处** → **跳过该插件的数据搬运与快照**，在日志与前端明确提示
  「该插件的数据库路径已由用户接管，管理器不再为其做隔离、回收与快照」

若将来崩溃窗口在实战中确实造成困扰，把 `DbPathOverride` 指向实例目录是一条现成的逃生路径 ——
但那应当是**用户显式选择的每实例选项**，不是默认机制。

### 4.9 运行期在线快照（对所有 SQLite 库生效）

**不绑定任何具体插件。** 只要 §4.2 按文件头认出某个文件是 SQLite 库，就为它做定时在线快照，
把最坏损失从"整个会话"收窄到"一个快照周期"。

- **必须用 SQLite 自己的在线备份**：`VACUUM INTO '<目标>'`，或备份 API。
  仓库里已有 `modernc.org/sqlite`（`auth.db` 在用），不引入新依赖。
- 快照落到 `instances/{name}/plugins/{P}/snapshots/<库名>`，写临时文件后重命名覆盖，保留 1–2 代。
- 周期做成实例配置项，默认给一个保守值（如 5 分钟）；库很大时自动拉长或跳过。

> ⚠️ **绝不能用朴素的定时文件复制来实现这个。** 运行期文件组一直在变，
> 逐文件拷会拷出互不一致的组合，得到的是**损坏的快照**，比没有更糟。
> 这也是为什么必须走 SQLite 的备份接口而不是 `fsutil.CopyFile`。

两个实现注意点：

1. **WAL 模式下的只读连接仍需要写权限**（读者要挂上 `-shm` 共享索引）。
   管理器与服务端同用户运行，实际不成问题，但不要试图用 `immutable=1` 之类的标志绕开 —— 那会读到过期数据。
2. **快照只在恢复时兜底使用**：优先按 §4.5 整组搬运真实文件组，
   只有文件组缺失或 SQLite 打不开时才回退到快照。

---

## 5. 配套：同步例外

仓库里已有现成的模式 —— `isUnderArkApiCache`（`mirror.go:54`）把 `ArkApi/Cache` 标成运行期缓存，
在 diff 的 `Insert` 与 `Match` 分支跳过删除与回写（`mirror.go:628`、`mirror.go:652`）。

照抄它，把**插件配置与数据文件**排除出同步的回写与删除：

- 否则注入进去的实例配置会在下一轮同步被源版本覆盖
- 否则实例运行期写的 db 会被源版本覆盖（正是 §2 的原始 bug）

这一条是注入能生效的**前提**，不是可选项。

---

## 6. 未采纳：整目录 junction

把 `Plugins/{P}` 整个 junction 到实例目录，免特权、无崩溃窗口，技术上完全可行。
不采纳的原因：插件二进制会一并落到实例目录，每实例多存一份（当前 pdb 合计约 60 MB/实例），
且插件更新时要专门回灌非数据文件，复杂度并不比搬运低。

若将来出现"数据文件多且散、按名字分不出来"的插件，这仍是可选的退路。

---

## 7. 分阶段实施

| 阶段 | 内容 | 验收 |
|---|---|---|
| **P1 同步例外** | 按 §5 把插件配置与数据排除出回写/删除 | 实例里的 db 与 config 不再被源版本覆盖 |
| **P2 搬运框架** | 实例插件目录、文件分类（含 SQLite 魔数识别与文件组推导）、注入与回收、首次播种 | 两实例各自改配置互不影响；正常停止后数据落在实例目录 |
| **P3 抢救规则** | §4.5 的 mtime 判定、整组替换、§4.7 的钩子接入 | **强杀实例后重启，数据不丢**；同步失败重建也不丢 |
| **P4 配置合并** | §4.6 的保序递归合并 + `.bak` 备份 | 插件更新新增的项能进来；用户改的值不被冲掉；键顺序不变 |
| **P5 在线快照** | §4.9 对所有 SQLite 库定时 `VACUUM INTO` | 运行中强断电，重启后最多丢一个快照周期 |
| **P6 `DbPathOverride` 识别** | §4.8 的三分支判定与提示 | 用户手工设了 override 时有明确提示而不是静默失效 |
| **P7 前端** | 实例详情页提供插件配置编辑入口、快照周期设置 | — |

七个阶段均已实施：

| 阶段 | 落地位置 |
|---|---|
| P1 | `plugindata.IsProtectedRelPath` + `mirror.syncMirrorEntries` 的 Insert / Match 两个分支 |
| P2 | `plugindata/classify.go`（魔数识别 + 文件组）、`plugindata.Inject` / `Reclaim` |
| P3 | `plugindata.Rescue`，接在 `CleanupInstanceMirror` 开头与 `startServerInternal` 里 |
| P4 | `plugindata/configmerge.go`（保序递归合并） |
| P5 | `plugindata/snapshot.go`（`VACUUM INTO`），周期取自实例配置 `PluginSnapshotInterval` |
| P6 | `plugindata/override.go` |
| P7 | `webapi/pluginapi` + `PluginDataPanel.vue`（挂在实例详情页的折叠面板里） |

**P3 是本方案的成败所在** —— 没有它，搬运在崩溃场景下会静默丢数据，比现在"每次被源覆盖"好不了多少。

---

## 8. 风险与未决项

| # | 项 | 状态 |
|---|---|---|
| 1 | `DbPathOverride` 取值形态 | ✅ 已验证：接受目录。不作机制，仅按 §4.8 识别 |
| 2 | 插件是否运行期改写 `config.json` | ✅ 已验证：插件更新时会写入。故配置必须双向 + 合并 |
| 3 | 插件是否会回写**已有键**的值 | ✅ 已定策：实测未见，若出现则**实例侧优先直接覆盖**，不维护例外名单 |
| 4 | JSON 键顺序 | ✅ 已定策：**保持原有顺序**，新增键追加末尾；需保序 JSON 处理，不能用 map |
| 5 | **从现状迁移** | 已在跑的服，数据在 `server-files-tmp-*` 里，**文件组必须整体搬**，只搬 `.db` 会丢 WAL |
| 6 | 实例重命名 / 删除 | 插件目录要跟着走；删实例时一并清理 |
| 7 | 集群共享权限 | `ClusterSyncTime` + `UseMysql` 说明插件设计上支持多服共享。若用户要共享，应引导用 MySQL —— 多进程并发写同一个 SQLite 文件不可靠 |
| 8 | 搬运耗时 | 当前 WAL 约 2 MB，可忽略；若某插件的库涨到数百 MB，停止流程会被拉长，届时该插件应改用 §6 的 junction |
| 9 | 快照与游戏进程争用 | ✅ 已处理：超过 512 MB 的库直接跳过快照并告警（`maxSnapshotDBBytes`），停服前先 `StopSnapshots` |
| 10 | **源侧配置更新到不了镜像** | ⚠️ 实施中发现的新缺口，见 §10.2 |

---

## 9. 附：顺带记录的观察

- **CrosschatAscended 的 `config.json` 有 7958 字节**，同样是"每服应当不同"的配置（聊天转发目标、频道等），
  且多半是嵌套结构 —— 这是 §4.6 合并必须递归的直接原因。
- **pdb 体积可观**：Permissions 17 MB + CrosschatAscended 24 MB + NativeReusables 13 MB
  + UnicodeRCONASA 6 MB ≈ **60 MB/实例**，纯调试符号。镜像时跳过 `.pdb` 是个独立优化项，
  但 `ArkApi/pdbignores.txt` 的存在暗示 AsaApi 确实会读 pdb，需先确认再动。

---

## 10. 实施记录：与本文不一致之处

### 10.1 §4.7 的「清理前回调」改成了直接依赖

本文建议给 `mirror` 加一个钩子、由上层注入回收策略，理由是 `mirror` 不该反向依赖 `instance`。
实际实现让 `plugindata` **不认识 mirror**（镜像目录一律由调用方以参数传入），
于是 `mirror` 可以直接 `import plugindata` 而不成环，`CleanupInstanceMirror` 开头一行调用即可。

分层没被破坏，而且比钩子更稳：钩子要有人负责注册，注册漏了或注册晚了都会静默退化成「不抢救」，
那正是本方案最怕的失败模式。依赖是编译期的，漏不掉。

### 10.2 同步例外带来的新缺口：源侧配置更新到不了镜像

§5 把插件配置排除出同步的**回写**之后，出现一个本文没预料到的后果：
用户在 `server-files` 里换了新版插件、新版自带一份改过的 `config.json`，
这份新配置**不会**再被同步带进镜像 —— 只有镜像被整体重建时才会重新播种。

没有当场修，因为两种修法都有明显代价：

- 按 mtime 决定要不要回写：与 §4.5 的抢救规则用同一个信号，两套逻辑对同一组 mtime 做相反的判断，很难推理。
- 干脆不排除配置的回写：注入排在同步之后，配置确实盖得回来 —— 但**数据**的排除必须保留，
  于是配置与数据走两套规则，§4.2 的分类就得在同步侧再分一次叉。

实际影响有限：插件运行期自己写入的新增项仍会被 §4.6 合并进来，
真正丢的只是「用户手工替换了源目录里的 config.json」这一种情形，
而那种情形下用户本来就是在改源服务端的默认值，不是在改某个实例的配置。

### 10.3 快照周期落在实例配置里

`PluginSnapshotInterval`（单位：分钟）加进了 `InstanceConfig` 与 `instance_config.ini`：
**0 = 用默认值（5 分钟），负数 = 关闭**。写在 `MessageOfTheDay` 之前 ——
那一项是自由文本且解析器按行读，必须留在文件末尾。

### 10.4 §8.6「实例重命名 / 删除」不需要额外工作

重命名走的是 `os.Rename(instances/{old}, instances/{new})`，删除走 `os.RemoveAll(instances/{name})`，
`plugins/` 在这两个目录之内，天然跟着走。

### 10.5 测试覆盖

三条核心规则都做了变异验证（改坏实现确认用例会红）：

- 抢救的 mtime 判定改成无条件 → `TestRescueKeepsNewerInstanceData` 失败
- 整组替换退化成逐文件覆盖 → `TestReplaceGroupRemovesStaleCompanions` 失败
- SQLite 魔数识别失效 → 三个用例失败，含 `IsProtectedRelPath` 的兜底分支
- 同步例外去掉 → `TestSyncKeepsPluginDataButStillUpdatesBinaries` 精确复现原始 bug
  （配置与权限库都被源版本覆盖、`-wal` 被当成多余条目删掉）

在线快照用**真库**验证：WAL 模式下写 50 行不 checkpoint、连接不关，
`VACUUM INTO` 出来的快照能读到全部 50 行，且不带 `-wal`。

---

## 11. Linux 兼容：编译得过，但应当整体静默

`internal/plugindata` 已核对为**跨平台**：无 `golang.org/x/sys/windows` 与 `syscall` 引用；
相对路径一律以 forward slash 为规范形式、落盘前过 `filepath.FromSlash`；
`slashBase` 而非 `filepath.Base`（`plugindata.go:323` 有注释说明）；
`modernc.org/sqlite` 是纯 Go 驱动，不破坏 Linux 侧 `CGO_ENABLED=0` 的静态编译目标。

但 `LINUX_COMPATIBILITY_PLAN.md` §1 已把 ArkApi / `AsaApiLoader.exe` 列为 **Linux 不支持**
（Wine 下的进程注入与 DLL hook 不可靠）。所以本方案在 Linux 上的正确形态是**什么都不做**。

**默认就是静默的，而且是结构性的**：`listMirrorPlugins`（`plugindata.go:57`）以镜像里
实际存在的插件目录为准，`os.ReadDir` 失败即返回空 —— Linux 上
`ShooterGame/Binaries/Win64/ArkApi/Plugins` 根本不存在，`Inject` / `Reclaim` / `Rescue`
全部退化成空循环，`StartSnapshots` 不起 goroutine，`IsProtectedRelPath` 第一行前缀判断就返回 false。

四条要在 Linux 落地时显式确认（已登记进 `LINUX_COMPATIBILITY_PLAN.md` §5.12）：

| # | 项 | 说明 |
|---|---|---|
| 1 | `pluginsRelPath` 硬编码大小写 | 常量是 `ShooterGame/Binaries/Win64/ArkApi/Plugins`。大小写敏感文件系统上一旦与 SteamCMD 落盘的大小写不符，前缀匹配静默失效。当前「本来就不该匹配」所以无害，但支持 ArkApi 后这是第一个要改的地方 |
| 2 | `override.go:85` 的 `strings.ToLower` | 路径包含判定折叠了大小写，Linux 上会把 `/a/DB` 与 `/a/db` 判为同一路径，导致 `DbPathOverride` 被误判成「指向实例目录内」而继续搬运。同样只在支持 ArkApi 后成为真 bug |
| 3 | `webapi/pluginapi` 与 `PluginDataPanel.vue` | Linux 上应回执明确的「本平台不支持 ArkApi」而**不是空数据** —— 空数据会让用户以为是自己配错了。前端据此隐藏整个面板 |
| 4 | `PluginSnapshotInterval` | Linux 上读写正常但永不生效。**保持存在不要删** —— 实例配置在两平台间迁移时字段消失更难解释 |
