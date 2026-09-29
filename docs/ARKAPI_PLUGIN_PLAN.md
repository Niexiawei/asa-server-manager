# ArkApi 插件：每实例独立安装、更新、卸载与数据隔离（合并文档）

> 本文由 `ARKAPI_PLUGIN_PLAN.md` 与 `ARKAPI_PLUGIN_PLAN.md` 于 2026-09-29 物理合并而成。
> Part 1 是现行方案（每实例插件目录 + 主程序/插件安装更新卸载）；Part 2 是前身设计（「启停搬运」式数据隔离），其核心机制已被 Part 1 取代，保留用于追溯。
> ⚠️ 本文各「落地文件 / 后端设计」写于 `RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN` 重构之前，实际路径见文末「附录 Y」。
> ⚠️ 本文存在已核实的已知缺陷，见下方「已知缺陷清单」。

## 已知缺陷清单（2026-09-29 代码审计同步）

以下为 2026-09-29 只读审计结论（基线 faf127c），尚未修复。

### 7.2 发现

#### [P1] 启动迁移用「端口是否监听」判断实例在跑，会漏判 starting 阶段/残留进程，对活库做收割

- **位置**：`internal/instance/pluginlayout.go:33`（`if running, _ := procpkg.IsServerRunning(name); running {`）
- **触发条件**：asa-server 重启时，某实例的游戏/加载器进程已存在但端口尚未绑定（ARK 启动到绑定端口常需数分钟；或上次强杀后 Wine 进程树残留）。`procpkg.IsServerRunning` 只查端口（`internal/process/process.go:116-126`，注释明确「port-only 会漏掉 starting 阶段」）。
- **后果**：`MigrateInstance` 对**运行中**的实例执行第 1 步 `harvest`，整组拷贝正在被写入的 SQLite 文件组（`layout.go:153-157`），拿到撕裂副本；随后镜像真实 `Plugins` 目录会被 `migrateExceptionJunctions` 的 `RemoveAll` 删除（`mirror.go:380`），活数据被破坏。同一文件里 `installer.ListAliveInstances` 用的是 `IsInstanceProcessAlive`，此处判据不一致。
- **修复建议**：改用 `procpkg.IsInstanceProcessAlive(name)`（或 `ListAliveInstances`）；并在 `MigrateInstance` 内部加一道「实例状态非 stopped/进程存活即返回错误」的护栏。

#### [P1] `MirrorPluginsDir` 硬编码大小写，Linux 上使迁移抢救与旧布局搬运静默失效

- **位置**：`internal/plugindata/plugindata.go:43`（`pluginsRelPath = win64RelPath + "/ArkApi/Plugins"`）、`:76-78`（`MirrorPluginsDir`）；使用点 `internal/plugindata/layout.go:154`、`plugindata.go:83/126/176`
- **触发条件**：大小写敏感文件系统（Linux）上 server-files 落盘为 `arkapi/`（手工解压/SteamCMD 变体）。镜像里的路径按盘上实际大小写建成 `arkapi/Plugins`（`SourcePluginsRelPath`，`inspect.go:53-58` 正确），而 `MirrorPluginsDir` 拼的是 `ArkApi/Plugins` → `os.ReadDir` 失败。
- **后果**：`listMirrorPlugins` 返回 nil，`migrateInstance` 第 1 步的 `harvest`、旧布局的 `Rescue`/`Reclaim` 全变空操作；紧接着 `migrateExceptionJunctions` 会 `RemoveAll(mirrorPath)`（用的却是实际大小写，`mirror.go:380`）——镜像里上一轮崩溃遗留、还没被抢回的新数据被直接删除（静默丢数据）。这正是 `LINUX_COMPATIBILITY_PLAN.md §5.12` 第 1 条，但代码只做只读告警（`casecheck_linux.go:23`），没修判定本身。
- **修复建议**：`MirrorPluginsDir` 改为复用 `SourcePluginsRelPath()`（或按 `actualChildName` 动态解析 ArkApi/Plugins 两级）。

#### [P1] 更新插件的两次 rename 不具崩溃原子性，崩溃窗口内插件「消失」

- **位置**：`internal/arkapimanage/plugin.go:185-198`
- **触发条件**：更新已安装插件时，`os.Rename(oldDir, bak)` 成功后、`os.Rename(staged, finalDir)` 前进程被杀/崩溃（或第二次 rename 失败但回滚也失败）。注意 `defer os.RemoveAll(tmp)`（`:158`）在失败路径会连新组装的内容一起删掉。
- **后果**：`Plugins/N` 不存在，插件显示为「未安装」（数据与配置仍在 `ArkApi/Backups/N-<ts>`，只能靠备份恢复）。
- **修复建议**：改成「staged→临时新名（同目录）→ old→bak → 新名→finalDir」三步，保证任一时刻 `finalDir` 要么旧版要么新版；或落可恢复 journal 并在下次启动补完。

#### [P1] 禁用/启用只写配置不落位时，`instanceBusy` 之外还有「另一个插件操作占用锁」的情况，返回原因与提示不符

- **位置**：`internal/arkapimanage/arkapimanage.go:65-68` 与 `internal/webapi/pluginapi/pluginapi.go:69-72`
- **触发条件**：另一插件安装/卸载正持锁，此时拨开关 → `TryLockInstance` 失败 → 返回 `(false, nil)`。
- **后果**：响应统一文案「实例运行中或正在启动，将在下次启动生效」，实际是「另一插件操作正在进行」，可能误导。
- **修复建议**：区分 `TryLock` 失败与 `instanceBusy` 两种情况，返回不同 message / `reason` 字段。

#### [P2] 卸载插件不清理该插件的在线快照目录，重装后展示陈旧快照

- **位置**：`internal/arkapimanage/plugin.go:244-278`
- **后果**：`instances/{name}/ArkApi/PluginSnapshots/<plugin>/` 残留，重装后新旧快照混在一起。
- **修复建议**：卸载时把 `InstanceSnapshotsDir/<plugin>` 一并移入该插件备份目录。

#### [P2] 内部 API 不校验实例名，安全完全依赖调用方

- **位置**：`internal/arkapimanage/plugin.go:104-125`、`:224-242`、`:284-301`（`lockForPluginWrite` 只 `instanceExists`）
- **修复建议**：在 `applyPluginTo`/`uninstallFrom`/`SetPluginEnabled` 开头调用 `apiresp.ValidateInstanceName`，并把「实例存在」校验放进被锁保护的函数内。

#### [P2] `ValidatePluginName` 未覆盖 Windows 保留名与尾随点/空格

- **位置**：`internal/plugindata/inspect.go:277-285`
- **触发条件**：上传包目录名/dll 主名为 `CON`、`NUL`、`LPT1`、`Foo.`、` Foo`。
- **后果**：Windows 上 `os.Rename`/`MkdirAll` 失败或名字被规范化 → `FindInstancePlugin` 找不到目录、`dll_missing` 误报。
- **修复建议**：拒绝保留设备名（含带扩展名形式）、尾随 `.`/空格、路径长度超限。

#### [P2] `apiresp.ValidateInstanceName` 放行 `.`、`:` 等，且 `SetPluginEnabled` 缺少 `instanceExists` 检查

- **位置**：`internal/webapi/apiresp/apiresp.go:18-26`；`internal/arkapimanage/arkapimanage.go:47-79`
- **修复建议**：`ValidateInstanceName` 增加 `.`/`:`/控制字符/纯空白与保留名检查；`SetPluginEnabled` 补 `instanceExists`。

#### [P2] `installCore` 覆盖游戏文件时的「原件残留」处理会丢历史原件

- **位置**：`internal/arkapimanage/core.go:341-350`（stale-originals 处理）、`:399-422`（`placeConfig`）
- **后果**：`stale-originals` 被挪进**本次**备份，而备份只保留 3 份，几次更新后最初的游戏原件可能被裁掉（与 `originals/` 要留到卸载的初衷相悖）；`placeConfig` 不装新配置时只发 warning。
- **修复建议**：`originals/` 重复时不挪走（保留最早那份）；`placeConfig` 在 `Errors` 而非 `Warnings` 里明确「本次未安装新 config.json」。

#### [P2] `RetireLegacyServerPlugins` 会把用户重新放进 server-files 的插件无条件移走

- **位置**：`internal/plugindata/layout.go:352-370`；调用点 `internal/instance/pluginlayout.go:46-48`
- **触发条件**：`MigratePluginLayouts` 里 `pending==0`；或某实例目录缺 `instance_config.ini`（`pluginlayout.go:30`）被跳过、未计入 pending。
- **后果**：全局插件被提前退役；该实例下次启动 `migrateInstance` 从已空的 `SourcePluginsDir` 拷不到插件。
- **修复建议**：退役前枚举所有实例目录（含缺配置的）做一次「是否已迁移」硬校验，任一未迁移就跳过退役并 WARN。

#### [P2] 迁移对 `DbPathOverride` 的改写不覆盖 `PluginsDisabled` / 嵌套键

- **位置**：`internal/plugindata/layout.go:245-299`
- **后果**：顶层以外的形态不被改写；旧目录改名后路径悬空，Permissions 在原路径新建空库，权限静默清零。
- **修复建议**：对每个已知键做递归查找；或对指向 `plugins.legacy-*` 的路径统一报错提示。

#### [P2] `harvest`/`replaceGroup` 的整组判定依赖 `scanPluginDir` 分类，未识别的运行期数据会被弃

- **位置**：`internal/plugindata/classify.go:120-191`、`plugindata.go:223-244`
- **修复建议**：`extraDataFiles` 目前为空（`classify.go:28`），需要时补上；迁移前把「镜像独有且未被分类的文件」列入报告/日志。

#### [P2] 镜像里 `arkApiInstalled()` 与 `installer.ArkApiInstalled()` 判据不一致

- **位置**：`internal/mirror/mirror.go:96-102`（只看 `Win64/ArkApi` 目录存在）
- **修复建议**：统一改为「存在 `AsaApiLoader.exe`」。

#### [P2] `movePath` 跨卷回退用 `fsutil.CopyDir/CopyFile`，会解引用插件目录里的符号链接

- **位置**：`internal/arkapimanage/core.go:737-766`；`pkg/fsutil/fsutil.go:37-96`
- **修复建议**：回退路径用 `Lstat` 判断链接并原样 `os.Symlink`/跳过，或直接报错要求手工处理。

#### [P2] 清单哈希缓存与 `lookupFold` 的性能/陈旧问题（小）

- **位置**：`internal/arkapimanage/manifest.go:120-152`、`:99-109`
- **修复建议**：`lookupFold` 调用点先建一次小写索引；哈希缓存加容量上限或按 mtime 变化失效。

### 7.3 文档 vs 代码偏差

1. **迁移判据**：文档 §4.4 写「`ArkApi/Plugins` 目录已存在即视为已迁移」，代码用标记文件 `ArkApi/.plugin-layout`（`layout.go:41-42、87-90`）。已在实施记录 §16.3 第 1 条说明。
2. **停止路径保留 `Reclaim`**：文档 §4.3/§7 写「删除 Reclaim 调用」，代码仍保留（`server.go:858`）。§16.3 第 2 条已说明。
3. **`shuttleRetired` 双判据**：文档只写「镜像里 Plugins 是链接」，代码另加「实例已迁移」（`plugindata.go:71-73`）。§16.3 第 3 条已说明。
4. **插件临时组装目录**：文档 §6.3 说「同级临时目录」，代码放在 `ArkApi/.install-*`（`plugin.go:154`）。§16.6 第 6 条已说明。
5. **未登记的偏差**：① 文档 §7/§4.6 只说「未运行才迁移」未规定判据，代码用**端口监听**（`pluginlayout.go:33`）而非进程存活（见 P1）；② 文档 §8.2 的 `GET /api/plugins/:name` 返回示例无 `plugins_dir` 字段，代码新增（`pluginapi.go:102`）；③ 文档 §6.4 卸载语义未提快照处理；④ 文档 §5.3 插件名规则未覆盖 Windows 保留名/尾随点。

---

# Part 1 — ArkApi 插件改为每实例独立 + 主程序/插件的安装、更新、卸载（原 `ARKAPI_PLUGIN_INSTALL_PLAN.md`）

# ArkApi 插件改为每实例独立 + 主程序/插件的安装、更新、卸载 —— 改造方案

> 状态：**方案已定稿，未实施**。§1 的决策已于 2026-09-11 确认，全部按建议执行；操作范围规则见 §1.1。
>
> 范围（六件事）：
> 1. 修复插件面板恒显示「未检测到 ArkApi 插件」的 bug；
> 2. **历史遗留问题**：V2 镜像模式下所有实例共用 server-files 里的同一份插件。改为**每个实例一份独立的插件目录**，
>    镜像里的 `ArkApi/Plugins` 整体链接到实例目录；
> 3. ArkApi **主程序** zip 上传安装 / 更新 / 卸载（主程序仍然全局一份，由各实例的 `EnableAsaPlugin` 决定用不用）；
> 4. ArkApi **插件** zip 上传安装 / 更新 / 卸载 / 启用禁用，**全部按实例**进行（可以一次选多个实例）；
> 5. 插件列表返回 `PluginInfo.json` 里的版本号、描述等元数据；
> 6. 「启用 ASA 插件」开关从基础配置 Tab 移入插件配置面板。
>
> 关联文档：
> [`V2_MIGRATION_PLAN.md`](./V2_MIGRATION_PLAN.md)（镜像启动模式的由来；插件变成全局共享是这次迁移的副作用，
> 两份 V2 文档里都没有涉及插件）、
> [`ARKAPI_PLUGIN_PLAN.md`](./ARKAPI_PLUGIN_PLAN.md)（现行的「启停搬运」式数据隔离。本方案**取代**它的核心机制，
> 见 §15）、
> [`ARKAPI_CACHE_PREFETCH_PLAN.md`](./ARKAPI_CACHE_PREFETCH_PLAN.md)（`ArkApi/Cache` 的归属）、
> [`LINUX_RUNTIME_PRIVILEGE_PLAN.md`](./LINUX_RUNTIME_PRIVILEGE_PLAN.md)（Linux 下 junction 目标的权限处理）。

---

## 0. 结论先行：路线选择

有两条路可以让插件变成按实例的：

| | **A. 共享一份，镜像时按实例过滤** | **B. 插件装在实例目录，镜像里链接过去**（采纳） |
|---|---|---|
| 做法 | server-files 里仍然只有一份插件；同步镜像时跳过本实例禁用的插件 | 每个实例有自己的 `instances/{name}/ArkApi/Plugins/`，镜像里的 `Win64/ArkApi/Plugins` 是指向它的 junction（Linux 上是 symlink） |
| 按实例启用/禁用 | ✅ | ✅ |
| 按实例安装/卸载、各实例用不同版本 | ❌ 只有一份二进制，装卸必然是全局的 | ✅ |
| 插件数据 | 仍靠「启动注入 / 停止回收」来回搬，**崩溃窗口**依旧存在 | 插件直接读写实例目录，**不需要搬运，也就没有崩溃窗口** |
| 实现代价 | 同步阶段要加排除规则（因为 `CopyFile` 不保留 mtime，只能在同步时排除，不能同步后再删，否则 Rescue 会拿种子库覆盖真实数据）；搬运机制全部保留 | 在已有的「例外 junction」清单里加一条（Save/Logs/Config 就是这么链到实例目录的）；搬运机制退役；需要一次性迁移 |

A 只能解决「禁用」，你要的按实例安装、卸载、更新它做不到，而且保留了现有方案最脆弱的那部分。
**B 是更彻底、代码量反而更少的解法**，采纳 B。

`ARKAPI_PLUGIN_PLAN.md` §6 当初否掉「整目录 junction」的两条理由，现在都不成立了（§3.4）。

---

## 1. 决策（2026-09-11 已确认，全部按建议执行）

| # | 问题 | 实测事实 / 背景 | 结论 |
|---|---|---|---|
| **D1** | 需求要求「`PluginInfo.json` 的 `FullName` 与 dll/pdb 文件名一致」 | AsaApi 2.03 官方包**自带**的 Permissions：`FullName` 是 `"Ark:SA Permissions"`，文件却是 `Permissions.dll`。按字面硬校验，**官方插件会被拒** | 硬规则改为「**目录名 == dll 主名 == pdb 主名**」（ArkApi 按 `Plugins/<目录名>/<目录名>.dll` 加载，真正起作用的就是这个名字）；`FullName` 不一致**只警告** |
| **D2** | 实例运行中时，能不能对它的插件做安装/更新/卸载 | Windows 上运行中的 ArkApi 直接从实例目录加载 `X.dll`，dll 被占用，无法覆盖或删除，所在目录也无法改名 | 首版：安装/更新/卸载**要求实例已停止**，运行中的实例在目标列表里置灰并注明原因。**启用/禁用例外**：只写配置，运行中也能切换，下次启动时落位（§4.5）。「排队到下次启动执行」列为后续增强 |
| **D3** | 主程序包里的 `msvcp140.dll` 与游戏自带文件同名 | 包里的是 575,056 字节；本机 `server-files\...\Win64\msvcp140.dll` 是 557,136 字节，属游戏文件。解压安装**必然覆盖**游戏原件 | 照包覆盖，但**覆盖前备份原件**、记入清单，卸载时还原。对非本程序安装的 ArkApi（没有清单），卸载时**不删** `msvcp140.dll` |
| **D4** | 主程序版本号从哪来 | 实测 `AsaApi.dll`、`AsaApiLoader.exe` **都没有 PE 版本资源**，二进制里也搜不到 `2.03` 字样 | 从上传文件名提取（`AsaApi_2.03.zip` → `2.03`），确认时可以修改，写入清单；手工装的显示「未知」。插件版本读 `PluginInfo.json` |
| **D5** | 迁移时，现有的全局插件怎么分给各实例 | 迁移前，每个实例加载的都是 server-files 里的全部插件，再叠加自己隔离出来的配置和数据 | **每个已有实例各拷一份当前的全部插件，再叠加该实例自己的配置和数据**。迁移前后每个实例加载的插件和数据完全不变，用户无感。所有实例都迁移完后，server-files 里那份移入备份 |
| **D6** | 新建实例默认有哪些插件 | — | **空**。主程序仍然在（全局），是否用加载器由 `EnableAsaPlugin` 决定 |

### 1.1 操作范围规则（已确认）

| 操作 | 作用范围 |
|---|---|
| 插件**安装 / 更新 / 卸载** | 以当前实例为准，确认时可以**手动勾选其他实例**，一并执行同一个动作。其他实例**默认不勾选** |
| 插件**启用 / 禁用** | **只作用于当前实例**，不提供批量入口，也不提供同步到其他实例的入口 |
| 主程序安装 / 更新 / 卸载 | 全局（主程序本来就只有一份） |

「一并执行」是**一次性动作**，不是持续的同步关系：

- 执行完之后，各实例的插件彼此独立。以后在某个实例上做的任何操作都不会波及其他实例，除非再次手动勾选。
- **不提供任何自动或持续的插件同步**：禁用列表 `DisabledArkApiPlugins` **不参与**实例间配置同步
  （`/api/config/sync-instance`）；也不提供「从某个实例复制插件」这类功能。

---

## 2. Bug：插件面板恒显示「未检测到 ArkApi 插件」

### 2.1 根因

`app/src/utils/http.js:63` 的响应拦截器已经 `return response.data`，组件拿到的 `res` **就是响应体本身**。

- `internal/webapi/pluginapi/pluginapi.go:48` 返回的是裸对象 `{"plugins": [...], "count": 2}`；
- `app/src/components/PluginDataPanel.vue:143` 读的却是 `res.data?.plugins`，**恒为 `undefined`**，经 `?? []`
  变成空列表，命中空态。

同一个错位还在 `PluginDataPanel.vue:157-158`：「编辑配置」读 `res.data?.content` / `res.data?.seeded`，
打开的编辑器是空的。

项目里其他接口大多套了 `apiresp.StatusResponse{Success, Data}` 信封（例如 `backupapi.go:73`，
对应前端 `InstanceBackupTab.vue:58` 读 `data.data?.backups`），只有 pluginapi 没有套。

### 2.2 修法

pluginapi 的读接口**改为套 `StatusResponse` 信封**，前端现有写法随即正确；本方案新增的接口也一律用这个信封。
空态文案拆成「主程序未安装」和「本实例还没有插件」两种。

**这一步与路线选择无关，应当先单独合入**（§13 P1）。

---

## 3. 现状与实测事实

### 3.1 V2 镜像模型与插件

- 镜像 `{BaseDir}/<前缀><实例名>/` 里，`ShooterGame/Binaries/Win64` 整棵是**真实拷贝**，其余目录是指回
  server-files 的 junction（`internal/installer/installer.go:27` 注释）。
  `Win64/ArkApi/Plugins` 位于 Win64 之下，所以**每个实例的镜像里各有一份插件的真实拷贝**，内容全部来自 server-files。
  这就是「插件是全局的」的根源。
- **已有「例外 junction」机制**：`buildExceptionTargets`（`mirror.go:302`）列出镜像里需要链到别处的路径：

  ```
  ShooterGame/Saved/Config/WindowsServer → instances/{name}/Config
  ShooterGame/Saved/Logs                 → instances/{name}/Logs
  ShooterGame/Saved/{SaveDir}            → instances/{name}/Save
  Win64 下的 Mods/ModsUserData           → server-files 里同一位置（全实例共享）
  ```

  配套设施都是现成的：
  - `collectSourceEntries` / `collectMirrorEntries` 遇到 junction 就 `SkipDir`，同步**不会穿过链接**去比对或删除目标里的内容；
  - `CleanupInstanceMirror` 只删链接、不删目标（`mirror.go:474`）；
  - `migrateExceptionJunctions`（`mirror.go:330`）把存量镜像里仍是真实目录的例外路径就地换成 junction，
    而且必须赶在 `collectMirrorEntries` **之前**执行；
  - `ExceptionTargets()` 导出的清单被 Linux 降权逻辑自动拿去处理权限。
- 所有实例的镜像同步由全局锁 `mirrorSyncMu`（`mirror.go:118`）串行化。

### 3.1.1 去管理员化之后的 junction 事实

来源：[`MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md`](./MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md) 第一部分（已实施）。

- **免特权**：Windows 上 `createJunction` 是真 NTFS junction（`DeviceIoControl` + `FSCTL_SET_REPARSE_POINT`，`junction_windows.go`），
  普通用户就能创建（该文 §1.2 实测）；Linux 上是 `os.Symlink`。两边的语义一致：目标用绝对路径，链接路径已存在时报错、不覆盖。
  所以本方案「多加一条 junction」**不引入任何特权需求**。
- **识别只能用 `os.Readlink`**（该文 §1.3 实测表）：Go 1.23 起，Windows 上的 junction 报 `ModeIrregular` 而不是 `ModeSymlink`，
  而且 `os.Lstat` 对它返回 `IsDir() == false`。因此「是不是链接」「是不是真实目录」这两个判断都必须**先过 `Readlink`**，
  不能用 `ModeSymlink`，也不能只看 `IsDir()`。`mirror.isJunctionOrSymlink`（`mirror.go:469`）就是这么实现的。
- **现有的删除保护**：`filepath.Walk` 不会递归进 junction；`CleanupInstanceMirror` 先摘掉所有链接，再删真实文件（`mirror.go:517-530`）；
  `removeMirrorEntry` 对链接只 `os.Remove` 链接本身（`mirror.go:986`）。
- ⚠️ **本方案带来一个新的暴露面**：现有的例外 junction 都挂在 `ShooterGame/Saved/` 之下，它们的父目录从来不会被当作多余条目删掉。
  本方案的 `Plugins` junction 挂在 `Win64/ArkApi/` 之下，**主程序卸载后，`Win64/ArkApi/` 这个真实目录会被 `removeMirrorEntry`
  以 `os.RemoveAll` 整棵删除**（`mirror.go:995`）。这是第一次出现「对一个内含『指向活数据的 junction』的真实目录执行 RemoveAll」。
  今天它不会穿透链接，靠的是标准库 `RemoveAll` 对每个子条目先尝试 `os.Remove` 的实现细节（对 junction/symlink，这一步就成功了）。
  处理方式见 §4.2 约束 4。

### 3.2 现行的插件数据隔离（将被取代）

`ARKAPI_PLUGIN_PLAN.md` 的机制是：配置与数据存放在 `instances/{name}/plugins/{P}/`，
启动时注入镜像、停止后收回，崩溃靠 Rescue 按 mtime 抢救，另有在线快照兜底。调用点：

| 位置 | 调用 |
|---|---|
| `instance/server.go:294-295` | `plugindata.Rescue` + `plugindata.Inject`（同步之后、启动之前） |
| `instance/server.go:662` | `plugindata.StartSnapshots` |
| `instance/server.go:777`、`:862` | `plugindata.StopSnapshots` |
| `instance/server.go:851` | `plugindata.Reclaim`（停止之后） |
| `mirror/mirror.go:486` | `plugindata.Rescue`（`CleanupInstanceMirror` 开头） |
| `mirror/mirror.go:704`、`:737` | `plugindata.IsProtectedRelPath`（同步时不删、不回写插件数据） |

另外，**`fsutil.CopyFile` 不保留 mtime**（`pkg/fsutil/fsutil.go:48`），拷出来的文件 mtime 是拷贝时刻。

### 3.3 两个样例包的结构

`AsaApi_2.03.zip`（主程序，文件**直接平铺在 zip 根**）：

```
AsaApiLoader.exe            331,264
AsaApiLoader.pdb          3,035,136
config.json                     687   ← ArkApi 自身配置
libcrypto-3-x64.dll       6,342,144
libssl-3-x64.dll          1,171,456
msdia140.dll              1,940,512
msvcp140.dll                575,056   ← 与游戏自带文件同名（D3）
ArkApi/AsaApi.dll         7,308,288
ArkApi/AsaApi.pdb        52,678,656
ArkApi/pdbignores.txt           975
ArkApi/Plugins/Permissions/…          ← 附带插件
Lib/AsaApi.lib              122,422
```

`TidyDamsASA.zip`（插件，**外面包了一层目录**，目录名即插件名）：

```
TidyDamsASA/PluginInfo.json   {"FullName":"TidyDamsASA","Description":"No more only wood in beaver dams!",
                               "Version":1.3,"MinApiVersion":2}
TidyDamsASA/TidyDamsASA.dll
TidyDamsASA/TidyDamsASA.pdb
TidyDamsASA/config.json
```

`Version` / `MinApiVersion` 是 JSON **数字**，按 float 解析会把 `1.10` 读成 `1.1`，必须保留原文（§5.3）。

### 3.4 旧方案否掉「整目录 junction」的理由为什么不再成立

`ARKAPI_PLUGIN_PLAN.md` §6：

> 插件二进制会一并落到实例目录，每实例多存一份（当前 pdb 合计约 60 MB/实例），
> 且插件更新时要专门回灌非数据文件，复杂度并不比搬运低。

1. **「每实例多存一份」**：镜像的 Win64 本来就是每实例一份真实拷贝，插件现在就已经在每个镜像里各存了一份。
   改成链接后，这份拷贝只是从镜像挪到了实例目录，**磁盘占用不变**；镜像同步还省掉了每次对插件文件做 MD5 比对。
2. **「更新时要回灌非数据文件」**：这正是本方案要做的「按实例安装、更新插件」，是功能本身，不是额外负担。

另外一个当初没有的前提：当时插件的安装与卸载不在管理器的职责范围内，「一份共享」算不上问题。
现在要按实例管理插件，共享一份就成了障碍。

### 3.5 本机已装 ArkApi 的 Win64（`E:\asa_server_data`）

- **游戏自带**：`msvcp140.dll`（557,136 字节）、`msvcp140_1/_2/…`、`vcruntime140*.dll`、`concrt140.dll`……
- **ArkApi 的**：`AsaApiLoader.exe/.pdb`、`config.json`（与 2.03 包**逐字节相同**）、`libcrypto-3-x64.dll`、
  `libssl-3-x64.dll`、`msdia140.dll`、`ArkApi/`、`Lib/AsaApi.lib`。
- 已装的 `ArkApi/AsaApi.dll` 与 2.03 包的**不同**，版本无从得知（D4）。
- `ArkApi/Plugins/` 下有 CrosschatAscended、ExtendedRcon、NativeReusables、Permissions、UnicodeRCONASA，
  它们就是 §4.4 迁移的输入。
- Win64 根目录还有 `AsaApi.pdb`、`Permissions.pdb`、`CrosschatAscended.pdb`、`UnicodeRCONASA.pdb`，来源未确认，**本方案不碰**。

---

## 4. 每实例插件目录：布局与机制

### 4.1 目录布局

```
{BaseDir}/instances/{name}/
├── Config/  Logs/  Save/  instance_config.ini      （原有）
├── ArkApi/
│   ├── Plugins/                    ← 镜像里 Win64/ArkApi/Plugins 的 junction 目标，ArkApi 实际加载这里
│   │   ├── Permissions/            ←   dll、pdb、PluginInfo.json、config.json、ArkDB.db* 全在一起
│   │   └── TidyDamsASA/
│   ├── PluginsDisabled/            ← 被禁用的插件（不在 junction 之下，ArkApi 看不到）
│   │   └── CrosschatAscended/
│   ├── PluginSnapshots/            ← SQLite 在线快照（原 plugins/{P}/snapshots/）
│   │   └── Permissions/ArkDB.db
│   └── Backups/                    ← 被更新或卸载替换下来的插件目录，每个插件保留最近 3 份
│       └── TidyDamsASA-20260911-100000/
└── plugins.legacy-20260911-093000/ ← 迁移前的 plugins/ 目录，原样保留，不再读写
```

放在实例目录下的好处：实例重命名（`os.Rename`）和删除（`os.RemoveAll`）时，插件目录天然跟着走，无需额外处理
（同 `ARKAPI_PLUGIN_PLAN.md` §10.4）。

### 4.2 镜像：新增一条例外 junction

在 `buildExceptionTargets` 里加一条：

```
ShooterGame/Binaries/Win64/ArkApi/Plugins → instances/{name}/ArkApi/Plugins
```

它的**父目录** `Win64/ArkApi/` 仍然是真实拷贝，所以主程序（`AsaApi.dll` 等）照旧从 server-files 同步，保持全局一份。
现有代码已经能处理「例外路径位于 Win64 之下」这种情况：遇到有例外子路径的目录会建成真实目录并继续下钻。

五条必须满足的约束：

1. **只在主程序已安装时添加这条例外**（server-files 里存在 `AsaApiLoader.exe`）。
   否则 `createInstanceMirror` / `syncMirrorEntries` 末尾「补建缺失的例外」那段（`mirror.go:257`、`:750`）
   会在 server-files 里 `MkdirAll` 出 `ArkApi/Plugins`，导致 `mirror.arkApiInstalled()`（只看 `ArkApi` 目录是否存在）误判为已安装。
2. **源里对应的目录必须事先存在**，与 `win64SharedRelPath` 的处理相同（`mirror.go:147`）。
   否则每次增量同步时，diff 会把镜像里这条 junction 判为「源里没有的多余条目」先删掉，再被补建逻辑重建，每次启动都白折腾一轮。
   做法是：主程序已安装时，同步前 `MkdirAll(server-files/.../ArkApi/Plugins)`。这个目录在新布局下保持为空（§4.4 第 6 步）。
3. **junction 的目标必须事先存在**：在 `ensureInstanceDirs`（`mirror.go:192`）里加上 `instances/{name}/ArkApi/Plugins`。
   Windows 的 `createJunction` 不会替你创建目标目录，目标不存在就会得到一条悬空的链接。
4. **`removeMirrorEntry` 删除真实目录之前，先摘掉其中的链接**（§3.1.1 的新暴露面）：
   照 `CleanupInstanceMirror` 第一步的做法（Walk + `Readlink` + `os.Remove`），先删掉目录里的所有链接，再 `RemoveAll`。
   实例插件数据的安全不能押在标准库的实现细节上。改动只有几行，对 Save/Logs 等既有 junction 也是一次加固。
5. **例外路径的大小写要取盘上的实际大小写**：例外清单按字符串**精确匹配**相对路径，而这个相对路径来自对 server-files 的 Walk，
   反映的是盘上的实际大小写。如果手工解压出来的是 `arkapi/`，这条例外在**两个平台上都匹配不上**：
   - Windows：`arkapi/Plugins` 被当成普通 Win64 子目录复制进镜像；补建例外时 `os.Stat` 大小写不敏感，认为 `ArkApi/Plugins`
     已经存在，于是不再建 junction。结果是**悄悄退回全局插件**。
   - Linux：镜像里同时出现一个复制来的 `arkapi/` 和一个 `ArkApi/Plugins` 链接，Wine 下哪一个生效不确定。

   做法：复用 `plugindata.detectPluginsCaseMismatch`（纯逻辑、无平台依赖，`casecheck.go:17`）找到盘上实际的目录名，
   **用实际大小写拼出例外的相对路径**。Windows 的 NTFS 与 Linux 上 Wine 的路径解析都不区分大小写，
   链接建在 `arkapi/Plugins` 上照样能被 ArkApi 找到。本程序安装的主程序一律按常量的大小写落盘，不会触发这种情况。
   （`isUnderArkApiCache` 等既有常量有同样的暴露，属于既有问题，不在本方案范围内。）

主程序被卸载后，这条例外不再添加。下次同步时，镜像里的 junction 被当作多余条目移除，只删链接，实例目录里的插件完好无损。
这条路径**必须有用例钉住**（§12）。

### 4.3 退役「启停搬运」

链接之后，插件直接读写实例目录，Rescue / Inject / Reclaim 都失去了存在的意义。
更重要的是，它们**必须停下来**，否则会主动造成破坏：

- `listMirrorPlugins` 读的是镜像里的 `Plugins`，此时它已经穿过 junction、指向实例的活数据目录。
  `CleanupInstanceMirror` 开头的 Rescue 会把活数据拷进 `instances/{name}/plugins/`，把已经退役的旧目录重新建出来；
- Inject 会拿旧目录里过期的副本，**整组覆盖**实例的活数据。

（`listMirrorPlugins` 用 `os.ReadDir` 打开的是链接路径本身，**会跟随** junction；只有 `filepath.Walk` 遇到链接时才不下钻。
所以不能指望「Walk 不穿透」来保护这里。）

所以做**结构性关断**：`harvest` 与 `Inject` 开头判断镜像里的 `Plugins` 是不是链接，是就直接返回。
这和 Linux 上「整体静默」的做法同一个思路（`ARKAPI_PLUGIN_PLAN.md` §11），不依赖任何标志位，漏不掉。

- **判定函数**：新增 `fsutil.IsLink(path)`，就是「`os.Readlink` 成功」；`mirror.isJunctionOrSymlink` 改为调用它，两边共用一份实现。
  放在 `pkg/fsutil` 是因为 `plugindata` 不能依赖 `mirror`（依赖方向是反过来的，见 `ARKAPI_PLUGIN_PLAN.md` §10.1）。
  各写一份的话，迟早会有一份退化成 `ModeSymlink`。
- **不能用 `ModeSymlink`**：在 Windows 上会漏判真 junction，关断**失效**，而且只在 Windows 上失效（§3.1.1）。
- **不能用 `!IsDir()`**：会把「`Plugins` 不存在」也判成链接。

这条关断要做**变异验证**：去掉判断，或者把判定换成 `ModeSymlink` 后，对应用例在 Windows 上都必须失败（§12）。

各部件的去留：

| 部件 | 去留 |
|---|---|
| `Rescue` / `harvest` | 保留，**只供 §4.4 迁移使用**；已迁移的实例由结构性关断自动跳过 |
| `Inject` / `Reclaim` | 从启动、停止路径上**删除调用**，函数本身带着关断保留一个版本周期后删除 |
| `IsProtectedRelPath`（同步排除） | 保留，无害：同步走到 junction 就 `SkipDir`，根本不会进入 Plugins。随搬运代码一起删除 |
| 在线快照 | **保留**，改为直接扫描实例的 `ArkApi/Plugins/`，写入 `ArkApi/PluginSnapshots/{P}/`。崩溃窗口没有了，但快照仍能防插件 bug 或库损坏，成本也很低 |
| 配置键合并 `MergeConfigJSON` | 保留，改由插件**更新**时使用（§6.3） |
| `DbPathOverride` 识别 | 保留，仅用于展示。「是否指向实例内部」的判定根目录从旧的 `plugins/{P}` 改为 `ArkApi/Plugins/{P}`；两平台的大小写处理（`override_windows.go` / `override_linux.go`）不变。旧值的迁移见 §4.4 第 2 步第 5 项 |
| 读/写插件配置 | 改为直接读写 `instances/{name}/ArkApi/Plugins/{P}/config.json`。「seeded（默认值）」这个概念随之消失 |

### 4.4 一次性迁移

**触发**：

- **程序启动时**：遍历所有**未运行**的实例，逐个迁移，这样面板上马上就能看到每个实例自己的插件。
- **`StartServer` 同步镜像之前**：兜底覆盖「升级时正在运行」的实例。启动前实例必然处于停止状态。

**运行中的实例绝不迁移**：它的活数据在镜像的真实 `Plugins` 目录里，运行中复制 SQLite 会复制出互相撕裂的文件组。

**判定**：`instances/{name}/ArkApi/Plugins` 目录已存在，就视为已迁移（或是新布局下新建的实例），直接返回。

**步骤**（函数 `plugindata.MigrateInstance(name, mirrorDir)`，全程幂等）：

1. 镜像存在、且其中的 `Plugins` 还是真实目录时（判定是 `!fsutil.IsLink(p) && IsDir`，顺序不能反，见 §3.1.1），
   先跑一次 `plugindata.Rescue`，把上一轮崩溃遗留在镜像里的新数据收回旧的 `instances/{name}/plugins/`。
2. 组装 `instances/{name}/ArkApi/Plugins.migrating/`：
   1. server-files 里的每个插件 X，整个目录复制过来；
   2. 用旧的 `instances/{name}/plugins/X/` 里的 `config.json` 与数据文件组**整组覆盖**（语义同现有的 `replaceGroup`）。
      旧目录里没有 X 的数据时，保留 server-files 那一份作为初值，与今天首次启动的播种行为一致；
   3. 旧目录里的 `snapshots/` 挪到 `ArkApi/PluginSnapshots/X/`；
   4. 只在旧 `plugins/` 里有、server-files 里已经没有的插件（二进制已卸载）**不迁移**，数据留在 legacy 目录里。
   5. **改写指向旧目录的 `DbPathOverride`**。`ARKAPI_PLUGIN_PLAN.md` §4.8 把「指向实例插件目录内」列为等价形态，
      还把它推荐为「逃生路径」，所以可能有用户把 Permissions 的 `DbPathOverride` 设成了旧的 `instances/{name}/plugins/Permissions`
      或它的子目录。第 4 步把旧目录改名之后，这个路径就悬空了，Permissions 会在原路径新建一个空库，**权限静默清零**。
      所以迁移时按下表处理（判定复用 `override.go` 的 `pathWithin`，两平台的大小写规则不变）：

      | 原值指向 | 处理 |
      |---|---|
      | 旧 `plugins/X` 本身 | 清空（默认位置就是插件目录，也就是新的实例插件目录） |
      | 旧 `plugins/X` 的子目录 | 把前缀改写到新目录（数据已经随本步第 2 项整组复制过来） |
      | 实例目录之外 | 不动，仍按「用户接管」对待 |

      改写要用 `configmerge.go` 的保序表示，不打乱键顺序。
3. `Plugins.migrating` → `Plugins`（**原子提交点**）。
4. 旧的 `plugins/` 改名为 `plugins.legacy-<时间戳>/`，保留，不删除。
5. 镜像侧不需要专门处理：随后的 `SyncInstanceMirror` 会先调用 `migrateExceptionJunctions`，
   它先把镜像里真实 `Plugins` 目录中「目标缺少的条目」补进实例目录（有冲突时保留目标那份），
   再删除这个真实目录、建成 junction。第 1 步已经收回数据，第 2 步已经组装完整，所以它只会补进一些零散文件。
6. **全局收尾**：程序启动时，如果所有实例都已迁移，就把 server-files 里的 `ArkApi/Plugins/*` 移入
   `{BaseDir}/arkapi/backups/legacy-server-plugins-<时间戳>/`，只留下一个空的 `Plugins` 目录（§4.2 约束 2 需要它）。
   在此之前，这些插件一直留着，供尚未迁移的实例使用。

**中断安全**：

- 第 3 步之前中断：残留的 `.migrating` 在下次迁移开始时删掉重来；
- 第 3 步之后、第 4 步之前中断：判定已迁移，但旧的 `plugins/` 还在。检测到这种情况时补做第 4 步。
  即使补做之前又启动了实例，Inject 也已经被结构性关断（§4.3），旧数据不会被注入。

**新建实例**：创建时直接建出空的 `ArkApi/Plugins/`（D6），因此永远不会走迁移。
没有这一步的话，在全局收尾之前新建的实例会被当成「未迁移」，把 server-files 里的全部插件都拷一份过去。

### 4.5 按实例启用 / 禁用

- **存储**：`InstanceConfig.DisabledArkApiPlugins []string`，在 `instance_config.ini` 里写一行逗号分隔的列表。
  这一行必须写在 `MessageOfTheDay` **之前**（那一项是自由文本，解析器按行读，必须留在末尾，同 `PluginSnapshotInterval`）。
  更新请求用指针字段，保持「不传 = 不改」。
- **只作用于当前实例**（§1.1）：没有批量切换入口；这个字段也**不加入**实例间配置同步
  （`SyncInstanceConfig` 与 `sync_enable_asa_plugin` 选项都不碰它）。
- **落位（Reconcile）**：按这个列表，把插件目录在 `ArkApi/Plugins/` 与 `ArkApi/PluginsDisabled/` 之间移动：
  - 实例已停止时，**立刻**落位；
  - 实例运行中时只写配置（D2），由 `StartServer` 在同步镜像之前落位。
  - 配置是唯一的真相，目录位置由它推导。
- 被禁用的插件在镜像里根本看不到（它不在 junction 目标之下），ArkApi 自然不会加载，快照也自然跳过。
- 安装一个处于禁用列表里的插件：装进 `PluginsDisabled/`，保持禁用状态。卸载时从列表里一并移除。

### 4.6 并发与互斥

- **按实例的插件操作**（安装、更新、卸载、落位、迁移）持**实例级锁** `plugindata.LockInstance(name)`。
  `StartServer` 在「迁移 → 落位 → 同步镜像」期间持有同一把锁；插件操作用 `TryLock`，拿不到就报「实例正在启动」。
  除此之外，安装、更新、卸载还要求实例处于非活动状态（不在 starting/started 等状态，且进程不存活，D2）。
- **主程序操作**（改写 server-files）：
  - 与 Steam 更新互斥：复用 installer 的 `updateMu` / `updateInFlight`。操作期间置位，StartServer 已有的
    `IsUpdatingServerFiles()` 检查会拒绝启动；Steam 更新进行中时拒绝主程序操作。
    与 Steam 更新不同的是，主程序操作**不要求**所有实例停止（镜像里的 Win64 是真拷贝），
    所以要新增一个 `beginArkApiWrite()`，只做互斥与置位，不检查存活实例；
  - 与正在进行的镜像同步互斥：导出 `mirror.WithSyncLock(fn)`，让主程序的换位步骤在 `mirrorSyncMu` 下执行。
    这把锁本来就在串行化所有实例的同步，换位只需要几次 rename，持锁时间极短。
- 上传、解压、校验不持任何锁。

---

## 5. 包校验规则

### 5.1 通用规则（两类包都适用，放进 `pkg/archive`）

- 上传体上限 **256 MiB**（`http.MaxBytesReader`），先落临时文件。
- 条目数 ≤ 4096；**实际解压出的**总字节数 ≤ 1 GiB，按实际写出计数，不信 zip 头里声明的大小。
- 拒绝的条目：绝对路径、盘符、含 `..` 的路径、符号链接、仅大小写不同的重复路径。
- 忽略的条目：`__MACOSX/`、`.DS_Store`、`Thumbs.db`。
- 未置 UTF-8 标志、且含非 ASCII 字符的条目名：首版直接拒绝，提示「请用 UTF-8 文件名重新打包」。

### 5.2 主程序包

- **定位包根**：包内**唯一**一个 `AsaApiLoader.exe`（不区分大小写），它所在的目录就是包根。找不到或找到多个都拒绝。
- **必需文件**（相对包根）：`AsaApiLoader.exe`、`AsaApiLoader.pdb`、`ArkApi/AsaApi.dll`、`ArkApi/AsaApi.pdb`、`Lib/AsaApi.lib`。
- **PE 校验**：`AsaApiLoader.exe`、`ArkApi/AsaApi.dll` 必须是合法 PE，且 `Machine == AMD64`（用 `debug/pe`）。
- 包根以外的条目忽略，并在报告里列出。
- 包内 `ArkApi/Plugins/*` 是**附带插件**，逐个按 §5.3 校验，结果列在报告里，由用户决定要不要装进哪些实例（§6.1）。
- 版本号：用 `(\d+(?:\.\d+)+)` 从上传文件名提取，可以修改。

### 5.3 插件包

- **定位插件根**：包内**唯一**一个 `PluginInfo.json`（不区分大小写），它所在的目录就是插件根。
  没有：「不是 ArkApi 插件包」；多于一个：「一个包只能包含一个插件」。
- **插件名 N**：插件根是一个目录时，N 就是目录名；插件根就是 zip 根（平铺）时，N 是「有同名 `.pdb` 的那个 `.dll`」的主名，
  没有或多于一个都算歧义，拒绝。
- **硬规则**：
  1. 存在 `N.dll`，大小写完全一致；
  2. 存在 `N.pdb`；
  3. `PluginInfo.json` 能解析为 JSON 对象（先剥掉 BOM），`FullName` 是非空字符串；
  4. N 是合法插件名：通过 `ValidatePluginName`，不含逗号（禁用列表用逗号分隔），不以 `.` 开头；
  5. 包里没有 `AsaApiLoader.exe` / `AsaApi.dll`，否则提示「这是主程序包」；
  6. `N.dll` 是合法的 AMD64 PE；
  7. 主程序已安装。
- **警告**（不阻断）：
  - `FullName ≠ N`（D1）；
  - `MinApiVersion` 高于已安装主程序的版本（仅在版本已知时检查）；
  - 某个目标实例已装同名插件，且新版本号 ≤ 已装版本号（重装或降级）；
  - `Dependencies` 中列出的插件在某个目标实例里没有安装。
- **版本号**：`json.Decoder.UseNumber()` 保留原文；字符串形式的版本号原样接受。
- 从某一行点「更新」时带 `expect=<插件名>`，包里解析出的 N 与之不同就拒绝。

---

## 6. 安装 / 更新 / 卸载的语义

### 6.0 公共机制

- **两段式**：上传 → 暂存区 `{BaseDir}/arkapi/staging/<token>/` 解压并校验 → 返回报告 → 用户确认（插件包在这一步选择目标实例）→ apply。
  暂存 30 分钟过期，进程启动时清空；用户不确认就关掉对话框时，立即清掉。
- **先组装、后换位**：新内容先在暂存区组装成最终形态，再 rename 到位；跨盘时退化为复制。
- **不直接删**：被换下来的内容进备份。主程序的备份在 `{BaseDir}/arkapi/backups/`，插件的备份在各实例的 `ArkApi/Backups/`，
  每个对象保留最近 3 份。
- **主程序安装清单**：`{BaseDir}/arkapi/manifest.json`（§6.5）。插件不需要清单，版本直接读 `PluginInfo.json`。

### 6.1 主程序：安装 / 更新（全局）

| 包内路径（相对包根） | 目标（相对 Win64） | 行为 |
|---|---|---|
| `AsaApiLoader.exe/.pdb`、根目录下的 `*.dll` | `./` | 覆盖。目标已存在且**不在清单里**时，先备份原件并记入 `overwritten`（D3） |
| `config.json` | `./config.json` | 目标不存在时直接落地；存在时用 `MergeConfigJSON(旧, 新)` 合并：旧值优先，新增键并入，保序 |
| `ArkApi/` 下除 `Plugins/`、`Cache/` 以外的文件 | `ArkApi/` | 覆盖 |
| `ArkApi/Plugins/<X>/` | — | **不写进 server-files**（新布局下那里只保留一个空目录）。报告里列出附带插件，确认时可以勾选要装进哪些实例（默认不勾），按 §6.3 安装 |
| `Lib/AsaApi.lib` | `Lib/` | 覆盖 |

- 更新时，旧清单里有、新包里没有的文件移入备份；手工装的（没有清单）只覆盖、不删除。
- Win64 根目录无法整体换位，采用**逐文件替换 + 失败回滚**；`ArkApi/` 下的内容可以整目录组装后换位。
  换位在 `mirror.WithSyncLock` 下执行（§4.6）。
- 运行中的实例不受影响（镜像里是拷贝），各实例下次启动时生效。
- 清单记录每个文件的 sha256，状态接口据此报告「被外部改动过的文件」。

### 6.2 主程序：卸载（全局）

- **有清单**：`files` 中的文件移入备份；`overwritten` 中的文件从备份还原；`ArkApi/`（此时只剩主程序文件和 `Cache/`）移入备份；
  `Lib/` 空了就删掉。
- **无清单**：按固定清单移除 `AsaApiLoader.exe/.pdb`、`libcrypto-3-x64.dll`、`libssl-3-x64.dll`、`msdia140.dll`、`ArkApi/`、
  `Lib/AsaApi.lib`。`config.json` 只有看起来是 ArkApi 的才移除（顶层有 `settings`，且其中含 `AutomaticPluginReloading`
  或 `AutomaticCacheDownload`）。**`msvcp140.dll` 保留**，并在结果里提示可以用 Steam 校验还原游戏原版。
- **各实例的插件目录完全不动**。镜像里的 `Plugins` junction 在下次同步时被移除（§4.2）。
  主程序重新装回来之后，junction 自动恢复，插件原样可用。
- 开着 `EnableAsaPlugin` 的实例下次启动会静默退回原版 exe（`server.go:419`），面板上显示警告。

### 6.3 插件：安装 / 更新（当前实例 + 手动勾选的其他实例）

确认对话框里有一张**目标实例表**，列出所有实例及其状态（已装版本、将执行的动作、是否运行中）：

- **只默认勾选当前实例**，其他实例都需要手动勾选（§1.1）。
- 从某一行点 [更新] 时，已经装了该插件的实例排在表格前面，方便勾选，但同样默认不勾选。
- 没装该插件的实例被勾选时，动作是「新装」；装了的是「更新」。
- 运行中的实例不可选（D2）。

apply 对每个目标实例**独立**执行（各自持实例级锁），逐个返回结果：一个实例失败不影响其他实例。
执行完之后各实例彼此独立，不留下任何同步关系。

对于每个目标实例，目标目录是 `instances/{name}/ArkApi/Plugins/N/`（在禁用列表里的装进 `PluginsDisabled/N/`）：

- **新装**：插件根从暂存区复制过去（多个实例共用一份暂存，所以是复制而不是 rename），
  先写到同级的临时目录，再 rename 到位。
  如果该实例的 `ArkApi/Backups/` 里有 N 上次卸载时留下的备份，报告里提供「从上次卸载的备份恢复配置与数据」选项，默认勾选。
- **更新**：在临时目录里组装最终形态，然后「旧目录 → 备份、新目录 → 到位」两次 rename：
  1. 放入新包的全部文件；
  2. `config.json`：用 `MergeConfigJSON(旧, 新)` 合并。**这一份就是该实例正在使用的配置**，
     所以新版本新增的配置键会立刻出现在用户面前，`ARKAPI_PLUGIN_PLAN.md` §10.2 那个「新配置到不了实例」的缺口也随之消失；
  3. 旧目录里的数据文件（SQLite 文件组、`extraDataFiles`）原样带过来；
  4. 旧目录有、新包没有的其余文件丢弃（随旧目录进备份）。

### 6.4 插件：卸载（当前实例 + 手动勾选的其他实例）

- 确认对话框列出**装有 N 的全部实例**（处于禁用状态的也算），只默认勾选当前实例，其他手动勾选；运行中的实例不可选（D2）。
- 对每个选中的实例独立执行（各自持实例级锁），逐个返回结果：
  - `ArkApi/Plugins/N/`（或 `PluginsDisabled/N/`）整个移入该实例的 `ArkApi/Backups/`，配置与数据随之保留，
    重新安装时可以从备份恢复（§6.3）；
  - 从该实例的禁用列表里移除 N。

### 6.5 主程序安装清单

```json
{
  "core": {
    "version": "2.03",
    "source": "AsaApi_2.03.zip",
    "installed_at": "2026-09-11T10:00:00+08:00",
    "files": {
      "AsaApiLoader.exe": "sha256:…",
      "ArkApi/AsaApi.dll": "sha256:…",
      "msvcp140.dll": "sha256:…"
    },
    "overwritten": {
      "msvcp140.dll": "backups/core-20260911-100000/overwritten/msvcp140.dll"
    }
  },
  "layout": {
    "legacy_server_plugins_retired_at": "2026-09-11T09:31:00+08:00"
  }
}
```

---

## 7. 新的启动与停止流程

```
StartServer
  ├─ PrepareArkApiCache                           （不变）
  ├─ plugindata.LockInstance(name)  ─┐
  │    ├─ plugindata.MigrateInstance            §4.4，已迁移时立即返回
  │    ├─ plugindata.Reconcile(disabled)        §4.5，启用/禁用落位
  │    ├─ mirror.SyncInstanceMirror              建立/修复 Plugins junction
  │    └─ mirror.VerifyAndRepairInstanceMirror
  │  unlock ─────────────────────────┘
  ├─ （删除）plugindata.Rescue / Inject
  ├─ … 构建命令行、runner.Run …
  └─ plugindata.StartSnapshots(name)             直接扫描实例目录

StopServer
  ├─ plugindata.StopSnapshots(name)              （不变）
  └─ （删除）plugindata.Reclaim
```

---

## 8. 后端设计

### 8.1 代码落点

| 位置 | 内容 |
|---|---|
| `pkg/archive/zip.go`（新） | `ExtractZip(zipPath, dest string, lim Limits) ([]Entry, error)`，实现 §5.1。通用、零领域依赖，与 `ExtractTar` 并列 |
| `pkg/fsutil/` | 新增 `IsLink(path) bool`（`os.Readlink` 成功即为链接），`mirror` 与 `plugindata` 共用（§4.3） |
| `internal/mirror/` | `buildExceptionTargets` 新增 Plugins 例外，相对路径按盘上实际大小写拼出（§4.2 约束 1、2、5）；`ensureInstanceDirs` 建出 junction 目标（约束 3）；`removeMirrorEntry` 删除真实目录前先摘掉其中的链接（约束 4）；`isJunctionOrSymlink` 改为调用 `fsutil.IsLink`；导出 `WithSyncLock` |
| `internal/plugindata/` | `layout.go`（新）：实例插件目录路径、`LockInstance`、`MigrateInstance`、`Reconcile`、`RetireLegacyServerPlugins`；`meta.go`（新）：`ReadPluginMeta` 解析 `PluginInfo.json`；`inspect.go`：列表改为扫描实例目录（`Plugins` + `PluginsDisabled`）并带上元数据；`plugindata.go`：`harvest`/`Inject` 加结构性关断；`snapshot.go`：改为扫描实例目录；导出 `ValidatePluginName` 与「列出目录里的数据文件」的函数 |
| `internal/arkapimanage/`（新包） | `validate.go`（§5）、`stage.go`（暂存与 token）、`core.go`（§6.1、§6.2）、`plugin.go`（§6.3、§6.4，多实例 apply）、`manifest.go`、`backup.go` |
| `internal/config/` | `DisabledArkApiPlugins` 字段、ini 读写、`UpdateInstanceConfig`。**不加入**实例间配置同步（§1.1） |
| `internal/installer/` | `beginArkApiWrite` / 对应的结束函数（§4.6） |
| `internal/instance/server.go` | 按 §7 调整启动与停止流程 |
| 程序启动（webapi `Start` 与服务模式共用的初始化处） | 迁移所有未运行的实例 + `RetireLegacyServerPlugins` + 清空暂存区 |
| 实例创建（instanceapi 的创建路径） | 建出空的 `ArkApi/Plugins/`（§4.4 新建实例） |
| `internal/webapi/pluginapi/` | `pluginapi.go`：信封（§2.2）、按实例的插件操作；`arkapi.go`（新）：全局主程序与上传接口 |

**依赖方向**：`arkapimanage` 依赖 `config`、`plugindata`、`mirror`、`installer`、`process`、`pkg/archive`，被 `webapi` 依赖；
`instance` 依赖 `plugindata`（本来就依赖）。无环。

### 8.2 API

所有写操作挂 `authapi.RequireAdmin()`：往服务器上放 dll 等同于在服务器上执行代码。
唯一的例外是启用/禁用开关，它和编辑实例配置同级。

| 方法 | 路径 | 说明 | 权限 |
|---|---|---|---|
| GET | `/api/arkapi` | 主程序状态 | 登录 |
| DELETE | `/api/arkapi` | 卸载主程序 | 管理员 |
| POST | `/api/arkapi/packages?kind=core\|plugin[&expect=N]` | 上传 zip，暂存并校验，返回报告 | 管理员 |
| POST | `/api/arkapi/packages/:token/apply` | 确认。插件：`{"targets": ["a", "b"], "restore_from_backup": true}`；主程序：`{"version": "2.03", "bundled": {"Permissions": ["a"]}}` | 管理员 |
| DELETE | `/api/arkapi/packages/:token` | 放弃暂存 | 管理员 |
| GET | `/api/plugins/:name` | 实例插件列表（**改为信封** + 新字段） | 登录 |
| GET | `/api/arkapi/plugins/:plugin/instances` | 各实例对插件 N 的安装情况（是否已装、版本、启用与否、是否运行中），供卸载对话框使用 | 登录 |
| POST | `/api/arkapi/plugins/:plugin/uninstall` | 从所选实例卸载，body `{"targets": ["a", "b"]}`，结果按实例分别报告 | 管理员 |
| PUT | `/api/plugins/:name/:plugin/enabled` | `{"enabled": false}` | 登录 |
| GET / PUT | `/api/plugins/:name/:plugin/config` | 读写实例目录里的 `config.json`（**改为信封**） | 登录 |

**插件包的校验报告**（校验失败返回 422，`data` 形状相同、`token` 为空）：

```json
{"success": true, "data": {
  "token": "…", "expires_at": "…",
  "kind": "plugin",
  "name": "TidyDamsASA",
  "full_name": "TidyDamsASA",
  "version": "1.3",
  "description": "No more only wood in beaver dams!",
  "min_api_version": "2",
  "errors": [],
  "warnings": [],
  "files": [{"path": "TidyDamsASA.dll", "size": 179712}],
  "targets": [
    {"instance": "meijue-pve", "running": true,  "installed_version": "1.2", "action": "update",  "blocked": "实例运行中，请先停止"},
    {"instance": "meijue-2",   "running": false, "installed_version": "",    "action": "install", "backup_available": true}
  ]
}}
```

**apply 的结果**：按实例分别报告，例如 `{"results": [{"instance": "meijue-2", "ok": true}, {"instance": "x", "ok": false, "error": "…"}]}`。

**`GET /api/plugins/:name`**：

```json
{"success": true, "data": {
  "layout": "instance",
  "plugins": [{
    "name": "Permissions",
    "full_name": "Ark:SA Permissions",
    "version": "1.1",
    "description": "Manage permissions groups",
    "min_api_version": "1.19",
    "enabled": true,
    "dll_missing": false,
    "has_config": true,
    "data_files": [],
    "snapshots": [],
    "external_db_path": ""
  }]
}}
```

- `layout: "legacy"` 表示该实例尚未迁移（升级时它正在运行）。这时列表只读、来自 server-files，面板提示「实例停止后将自动迁移为独立插件目录」。
- 原来的 `isolated` 字段删除：新布局下所有插件都是每实例独立的。

---

## 9. 前端设计

### 9.1 PluginDataPanel 新布局

```
┌ ArkApi 主程序（全局，所有实例共用）───────────────────────────┐
│ [已安装] 2.03 · 本程序安装 · 2026-09-11 10:00                 │
│                                        [上传更新]  [卸载]     │
└──────────────────────────────────────────────────────────────┘
┌ 本实例 ─────────────────────────────────────────────────────┐
│ 启用 ArkApi  (●)        数据库在线快照周期 [ 5 ] 分钟 (?)     │
│ ⓘ 实例运行中：开关修改在下次启动时生效；安装/更新/卸载插件需先停止实例 │
└──────────────────────────────────────────────────────────────┘
本实例的插件（2）                                     [上传插件]
┌──────────────────┬─────┬──────────────┬──────┬────────┬──────┬──────────────────┐
│ 插件             │ 版本│ 描述         │ 启用 │实例数据│最近快照│ 操作             │
├──────────────────┼─────┼──────────────┼──────┼────────┼──────┼──────────────────┤
│ Permissions      │ 1.1 │ Manage perm… │ (●)  │3个/200K│09:00 │编辑配置 更新 卸载│
│ Ark:SA Permiss…  │     │              │      │        │      │                  │
│ TidyDamsASA      │ 1.3 │ No more only…│ (○)  │ 无     │ 无   │编辑配置 更新 卸载│
└──────────────────┴─────┴──────────────┴──────┴────────┴──────┴──────────────────┘
```

- 「插件」列：第一行是目录名，`FullName` 不同时在第二行用次要色显示；描述截断，悬停显示全文。
- 各种状态：
  - 主程序未安装：[上传插件] 禁用，并提示「安装主程序后才能添加插件」；本实例开着 `EnableAsaPlugin` 时额外显示警告；
  - `layout: legacy`：列表只读，显示迁移提示；
  - `dll_missing`：该行显示「插件文件不完整」；
  - 实例运行中：更新、卸载按钮禁用，悬停说明原因。
- 非管理员隐藏上传与卸载按钮，判断方式复用路由 `meta.requiresAdmin` 那一套。
- 主程序卡片上的操作，确认对话框里写明「影响所有实例」；插件上的操作写明作用于哪个或哪些实例。
- 「启用」列的开关只作用于本实例（§1.1），没有批量或同步入口。

### 9.2 上传流程

1. 点 [上传主程序] 或 [上传插件]（行内 [更新] 会带上 `expect`）→ 选择 `.zip` → 显示上传进度。
2. 弹出**校验报告对话框**：
   - 有 `errors`：红色列表，只有 [关闭]。
   - 插件包：显示 `FullName`、描述、`MinApiVersion`、可折叠的文件清单、`warnings`，以及**目标实例勾选表**
     （实例名、已装版本 → 新版本、动作，运行中的置灰并显示原因，当前实例默认勾选），还有「从备份恢复」选项。
   - 主程序包：显示动作、会被覆盖的非 ArkApi 文件、可修改的版本号；附带插件表中每一行都可以勾选要装进哪些实例。
3. [确认] → apply → 按实例显示结果 → 刷新。
4. 没有确认就关闭：`DELETE /api/arkapi/packages/:token`。

**卸载**：点行内 [卸载] → 调 `GET /api/arkapi/plugins/:plugin/instances` → 对话框列出装有该插件的实例
（当前实例默认勾选，其他手动勾选，运行中的置灰）→ [确认卸载] → 按实例显示结果 → 刷新。

### 9.3 「启用 ASA 插件」开关的移动

- `InstanceBasicConfigTab.vue`：删掉 `:186-194` 的「其他设置」分组，以及 `FIELDS`、`projectConfig`、`buildPayload` 中的
  `EnableAsaPlugin`。后端是 `*bool`（`config.go:273`），不传就不改，基础配置保存时不会把它清掉。
- `PluginDataPanel.vue`：新增 prop `enableAsaPlugin` 与 emit `update:enableAsaPlugin`；`InstanceDetail/index.vue` 仿照
  `saveSnapshotInterval`（`index.vue:414`）即时保存。运行中也允许修改。
- `ConfigEditModal.vue`（新建实例弹窗）中的开关、`InstanceOverviewTab.vue` 中的展示保留。

### 9.4 `api.js` 新增函数

`getArkApiStatus`、`uploadArkApiPackage(kind, file, {expect, onProgress})`、`applyArkApiPackage(token, body)`、
`discardArkApiPackage(token)`、`uninstallArkApi()`、`getPluginInstances(plugin)`、`uninstallPlugin(plugin, targets)`、
`setInstancePluginEnabled(name, plugin, enabled)`。

⚠️ `http.js` 把默认 `Content-Type` 设成了 `application/json`，axios 1.x 在这个头下会**把 `FormData` 序列化成 JSON**。
上传请求必须显式设置 `headers: {'Content-Type': 'multipart/form-data'}`。

---

## 10. Linux 注意事项

- 在 Linux 上 `createJunction` 就是 `os.Symlink`（`junction_linux.go:20`），已有的 Save/Logs 链接就是这么工作的，
  Wine 看到的是一个普通目录。
- **权限自动跟上**：新增的 junction 目标会出现在 `mirror.ExceptionTargets()` 的清单里，runner 在 `runner.Run()` 之前
  会自动把它处理成「root 与降权游戏进程都能写」（`mirror.go:276` 的注释讲的就是这个设计）。
  所以按实例新装的插件不需要单独处理权限；`PluginsDisabled/`、`PluginSnapshots/`、`Backups/` 游戏进程用不到，也不必处理。
- 主程序文件落在 server-files 里，而暂存区在 `{BaseDir}/arkapi/staging`，rename 过去的文件**不会**继承 server-files 的默认
  ACL / setgid。apply 之后要对落位的子树调用 `runner.PrepareSharedTree(root)`（`runner.go:458`，Windows 上是空操作）。
- 路径常量的大小写与 `plugindata` / `installer` 保持一致；对手工安装、大小写与常量不同的情况，例外路径按盘上实际大小写拼出（§4.2 约束 5）。
- **ArkApi 在 Linux 上已经不是非目标**（`LINUX_COMPATIBILITY_PLAN.md` §0 修订记录、§1）：`EnableAsaPlugin` 在两个平台上走同一条启动路径。
  所以本方案的面板、接口、迁移在 Linux 上**照常工作**。`ARKAPI_PLUGIN_PLAN.md` §11 表格第 3 条
  「Linux 上 pluginapi 应回执『本平台不支持』」已被那次决定推翻，**不要照做**。
  同一张表的第 1 条（大小写告警，`casecheck_linux.go`）与第 2 条（`override_linux.go` 不折叠大小写）都已实施，本方案沿用。

---

## 11. 与既有功能的相互影响

| 功能 | 影响 |
|---|---|
| 存档备份（`backup`，只备份世界存档） | 插件数据（如 Permissions 的权限库）现在在实例目录里，但**仍然不在备份范围内**，与现状一致。纳入备份列为后续项 |
| 实例间配置同步 `/api/config/sync-instance` | **不受影响**：`DisabledArkApiPlugins` 不参与同步，插件本身也不复制（§1.1） |
| 实例重命名 / 删除 | 插件目录在实例目录内，天然跟随 |
| ArkApi offsets cache 预取 | 不受影响：`ArkApi/Cache` 仍在 server-files 里，随镜像同步分发 |
| Steam 更新 | 不受影响：它不碰 `ArkApi/`；两者之间的互斥见 §4.6 |
| `verify-arkapi` | 直接从 server-files 拉起加载器。新布局下 server-files 的 `Plugins` 是空的，验证时**不加载任何插件**，恰好验证的是主程序本身 |

---

## 12. 测试计划

- **镜像**：
  - 主程序已安装时，`Win64/ArkApi/Plugins` 建成 junction，而 `Win64/ArkApi/` 本身是真实目录；
  - 存量镜像里的真实 `Plugins` 目录被正确换成 junction，里面的零散文件补进了实例目录；
  - 增量同步**不会**删掉再重建 junction（§4.2 约束 2），也不会穿过 junction 做比对或删除；
  - `CleanupInstanceMirror` 之后实例插件目录完好；
  - **卸载主程序后，下次同步移除 junction，实例插件目录完好**；主程序装回后 junction 恢复；
  - 主程序未安装时，server-files 里不会被凭空建出 `ArkApi/Plugins`；
  - server-files 里是 `arkapi/`（大小写不同）时，junction 建在实际大小写的路径上，镜像里不会出现两份插件目录；
  - **镜像相关用例在 Windows 上必须用真 NTFS junction 跑**（直接调 `createJunction`），不能只在 Linux/symlink 上跑：
    两者在 `Lstat`/`Mode` 上的表现不同（§3.1.1），只测 symlink 会漏掉只在 Windows 上出现的问题。
- **实例插件目录零误删**（对应 `MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md` §1.5 第 3 条的验收思路）：
  依次走一遍新建镜像、增量同步、`VerifyAndRepairInstanceMirror` 触发的清理重建、正常停止、`ForceStopServer`（会清镜像）、
  卸载主程序后的同步，每一步之后都把实例插件目录与跑之前**逐文件比对**，必须一致。
  其中「卸载主程序后的同步」专门覆盖 §4.2 约束 4：`RemoveAll` 删除一个内含 junction 的真实目录。
- **迁移**：
  - 用旧 `plugins/` 的配置与数据覆盖 server-files 的种子，结果与迁移前该实例实际加载的内容逐文件一致；
  - 镜像里有上一轮崩溃遗留的新数据时，先经 Rescue 收回，再参与迁移；
  - 在各个步骤之间模拟中断，重跑之后结果一致（幂等）；
  - 运行中的实例被跳过；只在 legacy 目录里有的插件不迁移、数据保留；
  - 所有实例迁移完后才退役 server-files 里的插件；新建实例不走迁移；
  - `DbPathOverride` 指向旧的 `plugins/Permissions` 时，迁移后被清空，而且权限库能读出迁移前的数据；指向子目录时前缀被改写；
    指向实例目录之外时原样不动；改写后配置的键顺序不变。
- **结构性关断**（变异验证，在 Windows 上用真 junction 跑）：以下两种改法，都必须让
  「Cleanup 重新建出 `plugins/`」和「Inject 用过期副本覆盖活数据」两个用例失败：
  - 去掉 `harvest`/`Inject` 开头的链接判断；
  - 把 `fsutil.IsLink` 换成 `ModeSymlink` 判定。
- **既有用例的调整**：
  - `internal/mirror/plugin_data_sync_test.go` 与 `arkapi_cache_sync_test.go` 的 fixture 在源目录里放了 ArkApi 插件，
    fixture 里一旦有 `AsaApiLoader.exe`，`Plugins` 就会变成 junction，用例的前提随之改变，需要逐个核对：
    覆盖旧布局的用例保留（它们现在守护的是迁移路径上的 Rescue），另外补上新布局的用例；
  - `plugindata` 的 `TestRescueKeepsNewerInstanceData`、`TestReplaceGroupRemovesStaleCompanions` 保留不动，迁移仍然依赖这两条规则。
- **启用/禁用**：停止时立即落位；运行中只写配置，下次启动时落位；禁用后镜像里看不到该插件；
  执行一次实例间配置同步（勾选或不勾选「同步启用 ASA 插件」）之后，目标实例的禁用列表保持不变。
- **安装/更新/卸载**：更新时 `config.json` 合并（旧值保留、新键出现）、数据文件保留、旧的多余文件被清走；
  多实例 apply 中一个实例失败不影响其他实例；运行中的实例被拒；卸载后可以从备份恢复；
  多实例卸载只动勾选的实例，未勾选的实例插件目录逐文件不变；未显式传入 `targets` 的实例绝不会被操作（接口不存在「默认全部」）。
- **校验器**：用两个样例包的**结构**构造 fixture（假 PE，不提交真实 dll）。AsaApi 结构通过，附带 Permissions 报 `FullName` 警告；
  TidyDams 结构和平铺结构都通过；以下各种错误分别被拒且报错可读：缺文件、目录名 ≠ dll 名、两个 `PluginInfo.json`、
  插件包里带加载器、改后缀的伪 dll、`expect` 不匹配。`1.10` 保留原文；带 BOM 能解析。
- **主程序**：`msvcp140.dll` 覆盖前备份、卸载后还原；无清单卸载不删它；根目录逐文件替换失败时回滚。
- **`pkg/archive`**：zip slip、绝对路径、符号链接、条目数与字节数超限、大小写重复、非 UTF-8 条目名。
- 竞态检查用 PowerShell 跑 `go test -race ./internal/plugindata/... ./internal/mirror/... ./internal/arkapimanage/... ./pkg/archive/...`。
- **真机**：在截图里的 meijue-pve 上迁移，确认迁移后 Permissions 的权限数据还在、游戏内插件照常加载；再建一个新实例，
  确认它没有插件，装上 TidyDamsASA 后只有它加载。

---

## 13. 分阶段实施

| 阶段 | 内容 | 依赖 |
|---|---|---|
| **P1** | Bug 修复：pluginapi 套信封 + 空态文案 | 无，**先单独合入** |
| **P2** | 「启用 ASA 插件」开关移入插件面板 | 无 |
| **P3** | **每实例插件目录**：`fsutil.IsLink`、Plugins 例外 junction（§4.2 五条约束，含 `removeMirrorEntry` 加固）、迁移（含 `DbPathOverride` 改写）、结构性关断、快照改扫实例目录、配置读写改到实例目录、列表带元数据 | 无。这是历史遗留问题的修复本体，合入后行为对用户透明（D5） |
| **P4** | 启用/禁用（配置字段 + 落位） | P3 |
| **P5** | `pkg/archive.ExtractZip` + 校验器（纯函数，先写测试） | 无 |
| **P6** | 插件安装/更新/卸载（当前实例 + 手动勾选的其他实例、备份与恢复、实例级锁；后端 + 前端） | P3、P5 |
| **P7** | 主程序安装/更新/卸载（清单、`overwritten` 还原、附带插件分发） | P5、P6 |
| **P8** | 文档：`API_REFERENCE.md`；`CLAUDE.md` 目录树与数据流（加入 `arkapimanage`、`pkg/archive/zip.go`、Plugins 例外 junction）；在 `ARKAPI_PLUGIN_PLAN.md` 末尾追加一节说明被取代的部分（§15） | 全部 |
| 后续 | 运行中实例的操作排队到下次启动执行；插件数据纳入备份；删除退役的搬运代码 | — |

---

## 14. 风险与未决项

1. D1–D6 已于 2026-09-11 确认，全部按建议执行（§1）。
2. **迁移是不可逆的布局变更**，数据正确性全靠 §4.4 的步骤与 §12 的迁移用例。缓解措施：旧的 `plugins/` 与 server-files 里的插件
   都**只改名、不删除**，出了问题可以手工回退。
3. ArkApi 的 `AutomaticPluginReloading`（默认开启）在 junction 下的行为未经验证。本方案不依赖它：
   对已装插件的改动都要求实例已停止，启用/禁用在启动前落位。
4. `migrateExceptionJunctions` 的「目标缺失才补」是为共享 Mods 目录设计的语义。用在插件迁移上时，
   它可能把镜像里「源已卸载的插件」残留下的 config/数据补进实例目录，表现为一个 `dll_missing` 的插件，用户可以直接卸载。
   这是可以接受的：宁可多留，不能丢数据。
5. 主程序被 Steam 校验换回游戏版 `msvcp140.dll` 后，ArkApi 能否正常工作未知。本方案只通过 `modified_files` 把它显示出来。
6. 备份占用磁盘：主程序一份约 100 MB，插件备份按实例、按插件计算。首版只按份数限制。
7. Win64 根目录那几个 `*.pdb` 来源未确认，不动。
8. **链接识别是全方案的单点**：结构性关断、迁移判定、镜像同步、镜像清理，全都依赖「这个路径是不是链接」这一个判断。
   Go 在 1.23 已经改过一次 junction 的 `Mode` 语义（§3.1.1），以后还可能再变。
   缓解措施：只保留 `fsutil.IsLink` 一处实现，并由 §12 在 Windows 真 junction 上做的变异验证钉住。
9. 旧镜像里可能还有去管理员化之前用 `os.Symlink` 建的**目录符号链接**。`Plugins` 以前从来不是链接，不受影响；
   `Readlink` 对这两种链接都能识别，其他既有链接也不受本方案影响。

---

## 15. 与 `ARKAPI_PLUGIN_PLAN.md` 的关系

本方案**取代**该文 §1、§4.3–§4.5、§4.7、§5 的机制（启停搬运、Rescue 抢救规则、同步例外），**推翻**它 §6「不采纳整目录 junction」的结论
（理由见 §3.4）。以下内容**继续有效**，并被本方案复用：

- §4.2 的文件分类（SQLite 按文件头识别、文件组推导）：用于插件更新时决定哪些数据文件要保留；
- §4.6 的保序递归配置合并：用于插件更新与主程序 `config.json` 更新；
- §4.8 的 `DbPathOverride` 识别：仅用于展示；
- §4.9 的在线快照：改为扫描实例目录。

该文中另外几处与本方案直接相关、实施时要记住的：

- §4.8 把「`DbPathOverride` 指向实例目录」推荐为逃生路径。已经这么设置过的用户，迁移时路径会悬空，由 §4.4 第 2 步第 5 项改写；
- §8 第 8 条原本就预见了「某插件的库大到搬运不可接受时，改用 §6 的 junction」，本方案等于把这条退路提前成了默认；
- §2 列出的 `CleanupInstanceMirror` 7 个调用点（`ForceStopServer`、同步失败重建等）在旧方案下都得先抢救数据；
  在新布局下这些路径**本身就不碰实例数据**（清理只摘链接），崩溃和强杀不再有丢数据的风险；
- §11 表格第 3 条已被 `LINUX_COMPATIBILITY_PLAN.md` 推翻（见 §10）。

`MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md` 第一部分是本方案的**前提**，而不是被取代的对象：
免特权的真 junction（§3.1.1）让「多一条链接」没有任何代价，基于 `Readlink` 的识别规则被本方案原样沿用，并下沉到 `pkg/fsutil`。
那份文档不需要追加任何内容。

按「PLAN 文档只增不改」的惯例，不修改 `ARKAPI_PLUGIN_PLAN.md` 的原文，只在 P8 阶段于其末尾追加一节，指向本文。

---

## 16. 实施记录

### 16.1 P1、P2（2026-09-11 已实施）

| 阶段 | 落地位置 |
|---|---|
| P1 | `internal/webapi/pluginapi/pluginapi.go`：列表与读配置两个接口套上 `StatusResponse` 信封；列表另外返回 `arkapi_installed`（取自 `installer.ArkApiInstalled()`）。`PluginDataPanel.vue` 的空态拆成两种：「未安装主程序」（本实例开着开关时升级为警告）和「没有插件」 |
| P2 | `InstanceBasicConfigTab.vue` 删除「其他设置 / 启用ASA插件」。`PluginDataPanel.vue` 顶部新增「本实例」设置块（启用ASA插件 + 快照周期 + 运行中提示），开关是受控的，保存成功才翻转。`InstanceDetail/index.vue` 新增 `saveEnableAsaPlugin`，只提交这一个字段 |

`docs/API_REFERENCE.md` 原本就没有插件接口的章节，按计划留到 P8 一并补上。

### 16.2 实施中发现并修复的既有 bug：部分更新会清空服务器密码和 Mod 列表

`cfgpkg.UpdateInstanceConfig` 对 `ServerPassword`、`ModIDs` 是**无条件赋值**。其他字符串字段按「非空才更新」处理，
这两个字段为了允许清空而单独拎出来，结果变成了「没传就清空」。

受影响的是既有的两处部分更新：
- 插件面板保存快照周期；
- 服务器规则 Tab 单独保存启动参数。

这两处每次都会把服务器密码和 Mod 列表写成空串。P2 的开关同样是部分更新，不修的话每拨一次开关都会清掉这两项，
所以作为 P2 的前置一起修了。

- **修法**：请求结构体里这两个字段改为 `*string`，用 nil 区分「没传」和「传了空串」。
  基础配置 Tab 与 `ConfigEditModal` 每次都会显式提交这两个字段，所以清空功能不受影响。
  `UpdateInstanceConfigRequest` 只有 `instanceapi` 一个使用方。
- **回归用例**：`internal/config/config_update_test.go`，走 JSON 解码，覆盖「没传不改」和「传空串清空」两条。
  做过变异验证：恢复旧的赋值语义后用例失败，准确报出密码与 Mod 列表被清空。

### 16.3 P3（2026-09-11 已实施）

| 位置 | 落地内容 |
|---|---|
| `pkg/fsutil` | `IsLink`（`os.Readlink`）。`mirror.isJunctionOrSymlink` 改为调用它，两边共用一份实现 |
| `internal/plugindata/layout.go`（新） | 目录布局（`ArkApi/Plugins`、`ArkApi/PluginSnapshots`）、`IsMigrated` / `LayoutOf`、`MigrateInstance`（含 `DbPathOverride` 改写）、`InitInstanceLayout`、`RetireLegacyServerPlugins`、实例级锁 |
| `internal/plugindata/meta.go`（新） | `ReadPluginMeta`：`PluginInfo.json` 文件名不区分大小写、剥 BOM、数字版本号保留原文 |
| `internal/plugindata` 其余 | `plugindata.go`：旧目录改名 `legacyPluginsDir`，`harvest` / `Inject` 开头加 `shuttleRetired` 关断。`inspect.go`：列表、读写配置按布局分流，列表带元数据与 `dll_missing`；`SourcePluginsRelPath` 按盘上实际大小写拼路径。`snapshot.go`：改为扫描实例目录，写进 `PluginSnapshots`，`StartSnapshots` 去掉 `mirrorDir` 参数 |
| `internal/mirror` | `buildExceptionTargets` 新增 Plugins 例外（只在主程序已安装时，路径按实际大小写）；`ensureArkApiPluginDirs` 备好源侧目录与 junction 目标；`removeMirrorEntry` 删除真实目录前先 `removeLinksUnder` |
| `internal/instance` | `StartServer` 在同步镜像前调用 `MigrateInstance`，迁移失败即中止启动；删除 Rescue / Inject 调用；新增 `pluginlayout.go`：`MigratePluginLayouts` 迁移全部未运行的实例，全部迁完才退役全局插件 |
| `internal/webapi` | `APIServer.Start` 在调度器启动之前调用 `MigratePluginLayouts`；新建实例时调用 `InitInstanceLayout`；列表接口增加 `layout`、`plugins_dir` |
| 其他 | `backup` 恢复备份时若需要新建实例，同样调用 `InitInstanceLayout`；`verify-arkapi` 的提示改为「只验证加载器本身，不加载插件」 |
| 前端 | `PluginDataPanel.vue`：新增版本、描述列，`FullName` 与目录名不同时副行显示，`dll_missing` 显示为「文件不完整」；增加旧布局提示；空状态显示本实例插件目录路径；去掉「已隔离」列 |

**与方案不一致之处**：

1. **迁移标记改用文件 `ArkApi/.plugin-layout`**，不再以「`ArkApi/Plugins` 目录存在」作为判据（方案 §4.4 原写法）。
   原因：`ensureArkApiPluginDirs` 会在同步镜像时先把 junction 的目标目录建出来，拿目录当判据会让迁移被永久跳过。
   变异验证 M3 实测证实了这一点。
2. **停止路径保留 `Reclaim` 调用**，方案 §4.3 原本写的是删除。
   原因：升级那一刻正在运行的实例还是旧布局，它停止时照旧要把数据收回旧目录，下次启动再迁移。
   对已迁移的实例，`Reclaim` 在关断下是空操作。
3. **`shuttleRetired` 有两条判据**：镜像里的 Plugins 是链接，**或者**实例已迁移。方案只写了前者。
   后者覆盖「已迁移、但镜像还没同步成 junction」的窗口，例如迁移后、同步前就调用了 `CleanupInstanceMirror`。
4. **实例级锁在 P3 中只由 `MigrateInstance`、`InitInstanceLayout`、`WritePluginConfig` 内部持有**，
   `StartServer` 没有在整个同步过程中持锁。那是 P6（安装、卸载要与启动互斥）才需要的，届时再扩展。
5. **恢复备份时新建实例**的路径也调用了 `InitInstanceLayout`（方案 §8.1 只提到面板上的新建）。

**变异验证**（每项都实测失败后恢复）：

| # | 改坏什么 | 失败的用例 | 失败表现 |
|---|---|---|---|
| M1 | `shuttleRetired` 的链接判定换成 `ModeSymlink` | `TestShuttleDoesNotRunThroughPluginsJunction`（Windows 真 junction） | `Inject` 穿过 junction，用旧副本覆盖了活数据 |
| M2 | 去掉「已迁移」判据 | `TestShuttleRetiredAfterMigration` | 已迁移实例的镜像内容被收回了旧目录 |
| M3 | `IsMigrated` 改为以目录存在为判据 | `TestMigrateResumesAfterInterruption` | 迁移被跳过，插件一个都没迁进来 |

`removeLinksUnder` 这一层加固**无法用变异验证证明其必要性**：Go 1.27 的 `RemoveAll` 本身就不会穿透链接，去掉加固后用例依然通过。
它是纵深防御，由 `TestUninstallingCoreKeepsInstancePlugins` 守住最终结果。

**P3 到 P6 之间的已知缺口**：面板上还没有安装入口。

- 新建的实例没有插件，要么等 P6，要么手工把插件目录放进 `instances/<实例名>/ArkApi/Plugins`（面板空状态会显示这个路径）。
- 所有实例迁移完成后，server-files 里的全局插件会在程序启动时被移入 `{BaseDir}/arkapi/backups/`。
  之后再有人按老习惯把插件放进 server-files，下次程序启动时它也会被移走（日志里有 WARN 提示）。
- **真机验证尚未进行**：迁移、junction 在真实 ArkApi 下能否正常加载插件，都还没在 meijue-pve 上实际跑过。

### 16.4 对镜像既有逻辑的影响核查（2026-09-11，P3 提交之后）

**方法**：

1. 逐行复核提交 0e1709f 里 `mirror.go` 的 diff。
2. **真实数据只读预演**：对 `E:\asa_server_data` 的两个真实镜像（jibian、meijue），分别用改动前后的例外清单
   算出增量同步的完整动作清单，逐条比对。预演复刻了 `syncMirrorEntries` 的全部分支判定，但只计算、不执行。
   预演用的是临时测试文件，用完已删除，没有提交。
3. 补测试，并做变异验证。

**预演结论**：Plugins 之外**没有任何差异**。

| 实例 | 改动前 | 改动后 | 只在改动前出现的动作 | 只在改动后出现的动作 | Plugins 之外的差异 |
|---|---|---|---|---|---|
| jibian | CHECK 415 | CHECK 388 | 27 条，全部在 Plugins 下 | 0 | 0 |
| meijue | ADD 5 / CHECK 415 / REMOVE 2 | ADD 3 / CHECK 388 / REMOVE 1 | 30 条，全部在 Plugins 下 | 0 | 0 |

- 改动后少掉的都是 Plugins 下插件文件的比对与补拷：这些文件现在在 junction 那一头，本来就不该再同步。
- 改动后唯一的删除动作，是 meijue 镜像里一个过期的 ArkApi Cache generation。它在改动前同样存在，属于既有的 Cache 接管逻辑。
- `Win64/ArkApi` 的类型前后都是真实目录，不会被删掉重建。
- `ExceptionTargets` 的唯一下游是启动前的 `runner.PrepareSharedTree`（Linux 上施加共享 ACL，Windows 上为空操作）。
  多出的实例插件目录因此自动获得了降权游戏进程所需的写权限，这正是预期。

**发现并修复的问题**：

1. **迁移会复活已被移除的插件文件**（在真实数据上发现）。
   - 现象：meijue 的镜像里还留着 `CrosschatAscended/CrosschatAscended.dll`，而 server-files 里已经没有这个 dll
     （jibian 的镜像同步过，也已经没有）。改动前，下一次启动的同步会把它从镜像删掉；改动后，
     `migrateExceptionJunctions` 的 `mergeMissingInto` 会把它补进实例目录，**迁移后 CrosschatAscended 在 meijue 上会重新被加载**。
   - 修复：Plugins 这条例外**不做「镜像独有内容晋升」**。数据文件已由迁移第一步抢救并迁走；镜像独有的其余文件，
     与改动前一样丢弃。
   - 本条**取代 §4.4 第 5 步与 §14 第 4 条的描述**。
2. **junction 的建立条件缺了「实例已迁移」**。
   - 原先只要主程序已安装就建。若有任何路径绕过迁移直接同步，junction 会指向一个空目录，
     镜像真实 Plugins 目录里的活数据会被 `migrateExceptionJunctions` / `reconcileEntry` 连同目录一起删除。
     正常启动路径总是先迁移再同步，不会触发，但这是一条没有防护的路径。
   - 修复：新增 `pluginsExceptionFor`，条件改为「主程序已安装 **且** 实例已迁移」。
     `buildExceptionTargets` 与 `ensureArkApiPluginDirs` 都改用它；未迁移的实例完全维持旧的镜像行为。
3. 次要：`removeLinksUnder` 遍历出错时改为跳过，不再中断。这样删除失败时的表现与改动前直接 `RemoveAll` 一致。

**用例调整**：

- 新增 `TestUnmigratedInstanceKeepsLegacyMirrorBehavior`、`TestArkApiCacheRulesUnaffectedByPluginsJunction`。
  后者覆盖 Plugins junction 在场时 Cache 的接管与未接管两种情况；原有的两个 Cache 用例用的是手工拼的例外清单，覆盖不到这里。
- 修正 `TestLegacyMirrorPluginsMigratedIntoInstanceDir`：原先断言镜像独有的 `stray.txt` 会被带进实例目录，
  这恰好把问题 1 的错误行为固化成了预期。现在改为按真实场景构造「server-files 删掉了 dll、旧镜像里还留着」，
  断言 dll 不会被复活。

**变异验证**（每项都实测失败后恢复）：

| # | 改坏什么 | 失败的用例 | 失败表现 |
|---|---|---|---|
| M4 | 对 Plugins 也做镜像独有内容晋升 | `TestLegacyMirrorPluginsMigratedIntoInstanceDir` | server-files 里已删除的 dll 被复活进实例目录；非数据文件被带进实例目录 |
| M5 | `pluginsExceptionFor` 不看实例是否已迁移 | `TestUnmigratedInstanceKeepsLegacyMirrorBehavior` | 未迁移的实例被建出 Plugins junction |

**对真实数据的预期**：meijue 迁移后，CrosschatAscended 的目录里没有 dll，面板上会显示「文件不完整」。
这与改动前「同步后镜像里留下一个没有 dll 的目录」的结果一致。

### 16.5 Linux（WSL2）验证（2026-09-11）

**方式**：`wsl -e zsh -lc 'cd /mnt/d/golang/asa-server && go test ...'`，已写入项目 CLAUDE.md 的「Linux 测试（WSL2）」一节。
环境：WSL2 内核 6.18，Go 1.27.0 linux/amd64，`/tmp` 为 ext4，运行身份为 root。

**测试调整**：镜像布局用例原先带 `//go:build windows`，在 Linux 上根本不编译，所以 Linux 的 symlink 路径等于完全没测过。现改为两个平台都跑：

- `plugin_layout_windows_test.go` 改名为 `plugin_layout_test.go`，去掉平台标签。
- 「盘上是不是链接」的判断改用 `isLinkOnDisk`，按平台拆在 `linkattr_{windows,linux}_test.go`：
  Windows 读 `FILE_ATTRIBUTE_REPARSE_POINT`，Linux 用 `Lstat` 的 `ModeSymlink`。两者都不经过被测的 `fsutil.IsLink`。
- 共用的测试辅助函数移到 `helpers_test.go`。
- `plugin_data_sync_test.go` 与 `arkapi_cache_sync_test.go` 也去掉了 windows 标签。那个标签是 mirror 在 Linux 上还编译不过时留下的，
  去掉后它们在 Linux 上也全部通过。`sync_safety_test.go` 直接调用 Windows API，保留 windows 标签。

**结果**：

- Linux：mirror（19 个用例）、plugindata、fsutil 全部通过，`-race` 也通过。其中 7 个镜像布局用例是第一次在 Linux 上实际运行。
- Windows：同样全部通过。

**变异验证 M6**：去掉 `shuttleRetired` 里「镜像里的 Plugins 是链接」这条判据后，
`TestShuttleDoesNotRunThroughPluginsJunction` 在 Linux 与 Windows 上**都失败**（Inject 穿过链接，用旧副本覆盖了活数据）。
这证明该用例在 Linux 的 symlink 上同样有效，不是空转。

作为对照，§16.3 的 M1（把判据换成 `ModeSymlink`）只会在 Windows 上失败，这是预期：
Linux 上 `ModeSymlink` 判断 symlink 本来就是对的，这个 bug 只存在于 Windows 的 junction 上。

**顺带修正一个既有的测试 bug**：`override_linux_test.go` 的 `TestPathWithin_CaseSensitiveOnLinux` 断言写错了。
`/instances/foo/DB` 本来就在 `/instances/foo` 之内，与大小写无关，所以这个用例在 Linux 上恒失败。
它在 P3 之前就存在（由 7661f37 引入，P3 没有改动相关文件），现改为测试「根目录大小写不同」的情形。

**仍未覆盖的部分**：

- WSL 里是 root，降权与权限相关的路径（junction 目标的共享 ACL 等）在 root 下通过，不代表在降权的普通用户下也成立。
- 在 Linux 上经 Wine/Proton 真实拉起 ArkApi 并加载实例目录里的插件，尚未验证。

### 16.6 P4、P5、P6（2026-09-11 已实施）

| 阶段 | 落地位置 |
|---|---|
| P4 | `internal/config`：`DisabledArkApiPlugins`（ini 里一行逗号分隔，写在 `MessageOfTheDay` 之前）；新增 `ModifyInstanceConfig`（实例级锁下读-改-写），`UpdateInstanceConfig` 改为经它执行。`internal/plugindata/enable.go`（新）：`PluginsDisabled` / `Backups` 目录、`TryLockInstance`、`PrepareForStart`、`ReconcileLocked`、`FindInstancePlugin`、`DisabledPlugins`。`inspect.go`：列表扫描两处目录，新增 `enabled` / `pending`；读写配置对禁用的插件同样有效；`ValidatePluginName` 导出并加严（不许逗号、不许以 `.` 开头）。`internal/instance/server.go`：「迁移 → 落位 → 同步镜像 → 校验镜像」改由 `PrepareForStart` 在实例级锁下完成。`internal/arkapimanage`（新包）：`SetPluginEnabled`；接口 `PUT /api/plugins/:name/:plugin/enabled`。前端：插件表新增「启用」列与「待生效」标记 |
| P5 | `pkg/archive/zip.go`（新）：`ExtractZip`。`internal/arkapimanage/validate.go`（新）：插件包与主程序包的校验器。主程序包的校验器已写好并有用例，供 P7 使用 |
| P6 | `arkapimanage/stage.go`（暂存与 token）、`plugin.go`（多实例 apply、卸载、目标实例表）、`backup.go`（备份，每个插件保留 3 份）。`plugindata/carryover.go`（新）：`CarryOverPluginState`，更新与「从备份恢复」共用。`internal/webapi/pluginapi/arkapi.go`（新）：五个路由；`webapi.Start` 启动时清空暂存区。前端：`PluginInstallDialog.vue`、`PluginUninstallDialog.vue`、`PluginResultList.vue`（新）；`PluginDataPanel.vue` 增加「上传插件」和行内「更新」「卸载」；`api.js` 新增 6 个函数；`http.js` 的错误对象带上 `data`（422 的校验报告要用） |

**与方案不一致之处**：

1. **`DisabledArkApiPlugins` 不在 `UpdateInstanceConfigRequest` 里**（方案 §4.5 原写法是「更新请求用指针字段」）。
   唯一的写入口是专用的启用/禁用接口和卸载。通用的配置 PATCH 如果能改它，就会绕过落位，出现「配置说禁用、插件照样加载」的状态。
   用例 `TestPartialUpdateKeepsDisabledPlugins` 钉住：请求里带了这个字段也不生效。
2. **启用/禁用时，「只写配置、不落位」的条件扩大了**：实例级锁拿不到、实例不在可启动状态、或进程存活，三者任一成立都算。
   正在启动的实例同样只写配置，由它这一次或下一次的 `PrepareForStart` 落位。响应里的 `applied` 区分两种情况。
3. **落位失败会中止启动**（方案只说「由 StartServer 落位」）。该禁用的插件挪不出去还照常启动，等于加载了用户明确禁用的插件。
4. **旧布局实例**（升级时正在运行、尚未迁移）一律不能启用、禁用、安装、卸载，报 `ErrLegacyLayout`。方案只说列表只读。
5. **API 版本按十进制小数比较，插件版本按点分段比较**。`MinApiVersion` 是 JSON 数字（1.19、2），主程序是 2.03，
   所以 2.1 表示 2.10、高于 2.03；按点分段比较会得出相反的结论。首轮测试 `TestValidatePluginWarnings` 抓到了这个错误。
   插件版本只用于「重装或降级」的提示，判断错了也不阻断。
6. **安装时的临时组装目录放在实例的 `ArkApi/.install-*` 下**，不放在 `Plugins/` 里：放在 `Plugins/` 里的话，中途崩溃留下的半成品会被 ArkApi 当成一个插件去加载。
7. 上传用 `MultipartReader` 把 file 字段直接流进暂存区，不经 gin 的 `FormFile`（那会先在系统临时目录落一份）。
8. `kind=core` 的上传返回 400，主程序安装留给 P7。
9. **`MinApiVersion` 警告目前不会出现**：主程序版本要等 P7 的安装清单才知道，`StagePlugin` 传的 `CoreVersion` 为空。
10. `ExtractZip` 的错误一律作为校验错误返回（422），不区分「zip 本身损坏」和「条目不安全」。

**变异验证**（每项都实测失败后恢复）：

| # | 改坏什么 | 失败的用例 | 失败表现 |
|---|---|---|---|
| M7 | `PrepareForStart` 在调用 `syncMirror` 之前释放锁 | `TestPrepareForStartReconcilesThenSyncsUnderLock` | 同步镜像期间实例级锁没被持有 |
| M8 | 备份目录名只看「插件名-」前缀，不严格匹配时间戳 | `TestBackupsPrunedWithoutTouchingOtherPlugins` | 插件 `P-1` 的备份被当成 `P` 最旧的一份删掉 |
| M9 | `take` 不从登记里摘掉暂存包 | `TestStagingTokenLifecycle` | 同一个 token 可以 apply 第二次 |
| M10 | `lockForPluginWrite` 去掉「实例是否在运行」的检查 | `TestApplyFailureIsolatedPerInstance`、`TestUninstallRejectsRunningInstance` | 运行中的实例被装上插件、被卸载插件 |

M8 第一次**没有失败**：原用例里另一个插件叫 `P-Bar`，它的备份按名字排在所有时间戳之后，被当成「最新的一份」，裁剪永远轮不到它。
用例改为 `P-1`（按名字排在最前）之后，变异才失败。

**测试**：

- 新增用例：`pkg/archive/zip_test.go`、`internal/arkapimanage/{validate,plugin}_test.go`、`internal/plugindata/{enable,carryover}_test.go`、
  `internal/config/config_plugins_test.go`、`internal/webapi/pluginapi/arkapi_test.go`（HTTP 层：multipart 流式接收、422 带报告、413、targets 必须显式给出）。
- Windows（PowerShell，`-race`）与 WSL Linux（`-race`）：archive、arkapimanage、plugindata、mirror、pluginapi 全部通过；config 在 Windows 上全部通过。
- config 在 Linux 上有一个既有失败 `Test_SetMessageOfTheDay`：它读 `ASA_BASEDIR` 下写死的实例 `ces99`，属于 CLAUDE.md 记录的环境耦合用例，与本次改动无关。
  本次新增的 config 用例在 Linux 上通过。
- 前端 `npm run build` 通过。

**仍未覆盖的部分**：

- 前端没有自动化测试。三个对话框和面板的交互只经过构建检查，还没有在浏览器里实际操作过。
- 真机：在 meijue-pve 上上传 TidyDamsASA、更新、卸载、从备份恢复，以及在游戏内确认启用/禁用确实生效，都还没有做。
- 落位用的是 `os.Rename`。Windows 上插件目录里有文件被占用（杀毒软件、资源管理器）时会失败，启动随之中止并报出原因。

### 16.7 P7（2026-09-11 已实施）

| 位置 | 落地内容 |
|---|---|
| `internal/installer` | `BeginArkApiWrite`：与 Steam 更新共用「更新中」标记，只做互斥与置位、不检查存活实例（§4.6）。`beginServerFilesUpdate` 也改为标记已置位时拒绝（见下第 8 条） |
| `internal/mirror` | `WithSyncLock`：主程序换位在 `mirrorSyncMu` 下执行 |
| `internal/arkapimanage/manifest.go`（新） | 清单读写（原子写，`Core` 为空时删除文件）、`Status`（按 sha256 比对，哈希按路径+大小+修改时间缓存）、`installedCoreVersion` |
| `internal/arkapimanage/core.go`（新） | `StageCore` / `ApplyCore` / `UninstallCore`；`coreTxn`：每一次搬动记日志，失败时倒序搬回，新建的空目录一并撤掉 |
| `internal/arkapimanage` 其余 | `stage.go`：暂存包增加 `core` 类型，新增 `takeIf`、`StagedKind`，`ErrStageGone` 导出。`plugin.go`：`Result.Plugin`；`StagePlugin` 传入主程序版本，§16.6 第 9 条的 `MinApiVersion` 警告自此生效 |
| `internal/webapi/pluginapi/arkapi.go` | `GET /api/arkapi`、`DELETE /api/arkapi`；上传接受 `kind=core`；apply 按 token 登记的类型分派 |
| 前端 | `ArkApiCoreDialog.vue`（新）：上传、校验报告、可改的版本号、会被覆盖的游戏文件、附带插件逐个选择目标实例。`PluginDataPanel.vue` 顶部新增主程序卡片（状态、上传、卸载确认，写明「影响所有实例」），「未安装主程序」的提示只在本实例开着「启用ASA插件」时出现。`PluginResultList.vue` 显示插件名。`api.js` 新增 `getArkApiStatus`、`uninstallArkApi` |

**与方案不一致之处**：

1. **被覆盖的游戏原件存在 `{BaseDir}/arkapi/originals/`**，不放在 §6.5 示例里的 `backups/core-<时间戳>/overwritten/`。
   `backups` 只保留最近 3 份，原件却要一直留到卸载：放在一起的话，更新几次之后唯一的原件就被裁掉了。
   更新时原件路径从旧清单原样带过去，不重复备份。
2. **`ArkApi/` 下也是逐文件替换**，没有按 §6.1「整目录组装后换位」。`ArkApi/` 里还有 `Cache/`（缓存预取，可达数百 MB）
   和 `Plugins/`（junction 的源侧目录，未迁移实例的全局插件也在这里），整目录换位得把它们搬进新目录。
   逐文件搬动加日志回滚，对 Win64 根目录与 `ArkApi/` 一视同仁。
3. **卸载时，被覆盖过的游戏文件如果安装之后又被外部换过，就保留现状**，原件移入这次的备份（§6.2 原文是无条件还原）。
   这种情况多半是 Steam 校验已经还原了游戏版本，拿旧原件覆盖只会把游戏文件退回旧版。判据是当前文件的哈希与清单不同。
4. **卸载时 server-files 的 `ArkApi/Plugins/` 不空就保留不动**（§6.2 原文是 `ArkApi/` 整体移入备份）：
   不空说明还有实例没迁移、正从这里加载插件。空的照常移走。
5. **安装范围**：只装包根下的文件、`ArkApi/`（除 `Plugins/`、`Cache/`）、`Lib/`。包根下的其他目录不装，在报告里警告。
   §6.1 的表格没有覆盖这些路径，照搬的话，包里一个 `ShooterGame/` 目录就会写进 Win64 下共享的 Mods 目录。
6. **`ArkApi/`、`Lib/` 两级目录名取盘上的实际大小写**，把 §4.2 约束 5 延伸到了安装：否则在 Linux 上，
   手工解压出来的 `arkapi/` 旁边会再多出一个 `ArkApi/`，镜像里出现两份主程序。卸载同样按实际大小写找。
7. **清单里不记 `layout.legacy_server_plugins_retired_at`**（§6.5 示例）。退役全局插件的是 `plugindata`，
   而 `plugindata` 不能依赖 `arkapimanage`。退役时间已经体现在备份目录名 `legacy-server-plugins-<时间戳>` 和 WARN 日志里。
8. **`beginServerFilesUpdate` 也在标记已置位时拒绝**，原来不检查。两个写者共用一个布尔，先结束的一方会把标记清掉，
   后一方就在「没有标记」的状态下改写 server-files，启动侧的 `IsUpdatingServerFiles` 随之失效。
   这是 §4.6 互斥成立的前提；Steam 更新与 `VerifyServerInstallation` 之间原本也有同样的问题，一并修掉。
9. **主程序 apply 在两种情况下保留暂存包**：附带插件的选择不合法（`takeIf` 在锁内先检查再取出），以及 server-files 正忙
   （先拿写锁再取暂存包）。用户改了选择或等空闲后可以再确认。插件包 apply 仍然是「用过即删」。
10. **apply 仍是一个接口**，按 token 登记的类型分派（`StagedKind`），请求体里两种包的字段并存，各取所需。
11. 状态接口的 `modified_files` 不含 `config.json`：它本来就是给用户改的。
12. 首次安装的结果里 `backup` 为空：没有换下任何 ArkApi 文件，游戏原件在 `originals/`，不在备份里。

**变异验证**（每项都实测失败后恢复）：

| # | 改坏什么 | 失败的用例 | 失败表现 |
|---|---|---|---|
| M11 | 覆盖游戏文件前不存原件 | `TestCoreInstallBacksUpGameFileAndUninstallRestoresIt` | 卸载后 msvcp140.dll 不是游戏原版 |
| M12 | 失败时不回滚 | `TestCoreInstallRollsBackOnFailure` | server-files 停在半新半旧的状态 |
| M13 | 卸载时不看原件是否被外部换过 | `TestCoreUninstallKeepsGameFileReplacedAfterInstall` | Steam 还原过的游戏文件被旧原件覆盖 |
| M14 | `BeginArkApiWrite` 不检查标记 | `TestArkApiWriteExcludesServerFilesUpdate` | 两个主程序操作并行；Steam 更新期间允许主程序操作 |
| M15 | `beginServerFilesUpdate` 不检查标记 | `TestArkApiWriteExcludesServerFilesUpdate` | 主程序操作期间允许开始 Steam 更新 |
| M16 | 附带插件的选择被拒时也取走暂存包 | `TestCoreBundledPluginsOnlyIntoListedInstances` | 改了选择再确认时报「暂存的安装包不存在或已过期」 |
| M17 | 卸载时连非空的全局 `Plugins/` 一起移走 | `TestCoreUninstallLeavesLegacyServerPlugins` | 未迁移实例正在用的全局插件被移走 |
| M18 | 目录名不取盘上大小写 | `TestCoreInstallFollowsOnDiskCaseOfArkApiDir` | **只在 Linux（WSL）上失败**：多出第二个 `ArkApi/` 目录 |

M18 在 Windows 上存活是预期的：NTFS 不区分大小写，`ArkApi/` 与 `arkapi/` 本来就是同一个目录。

**测试**：

- 新增 `internal/arkapimanage/core_test.go`（11 个用例：安装与还原、更新合并配置、两种回滚、外部改动、无清单卸载、
  保留全局插件、附带插件只装进所列实例、server-files 正忙、版本号与 `MinApiVersion`、状态、盘上大小写），
  `internal/installer/arkapi_write_test.go`，`pluginapi` 的 `TestCoreLifecycleOverHTTP`。
  `TestUploadRequestErrors` 随之调整：`kind=core` 已开放，改为用未知类型测 400，另测非 zip 的主程序包返回 422。
- Windows（PowerShell，`-race`）与 WSL Linux（`-race`）：arkapimanage、pluginapi、mirror、plugindata、installer（相关用例）全部通过；
  `go build ./...` 与 `GOOS=linux CGO_ENABLED=0 go build ./...` 通过；前端 `npm run build` 通过。
- **真实安装包**：用一个临时用例（跑完即删，没有提交），在 Windows 与 WSL 上把真实的 `AsaApi_2.03.zip`、`TidyDamsASA.zip`
  走了一遍完整链路：主程序包 17 个文件，装进 Win64 11 个，没有被忽略的；`msvcp140.dll` 列为会被覆盖的游戏文件；
  附带的 Permissions 校验通过，带 `FullName` 警告（D1）；安装时 Permissions 只装进所列实例 a，TidyDamsASA 只装进实例 b；
  卸载主程序时移除 10 个文件、还原 1 个游戏文件，Win64 与安装前**逐文件一致**，两个实例的插件完好。

**仍未覆盖的部分**：

- 前端的主程序卡片、安装对话框、卸载确认都只经过构建检查，还没有在浏览器里实际操作过。
- 真机：在 `E:\asa_server_data` 上用面板更新已有的手工安装、卸载再装回，确认各实例的插件 junction 随之移除和恢复、
  游戏内插件照常加载，都还没有做。
- 「先手工装过 ArkApi、再用面板更新」时，如果 `msvcp140.dll` 已经是 ArkApi 的版本，它会被当成游戏原件存下，
  卸载时还原的也是它（与 D3 对无清单卸载「不删」的效果相同）。本机的 `msvcp140.dll` 是游戏版（557,136 字节，§3.5），不受影响。
- 「更新中」标记只在进程内有效：另开一个进程跑 CLI（例如 `verify-arkapi`）看不到服务进程的标记。这与 Steam 更新的既有行为相同。

### 16.8 P8（2026-09-11 已实施）

| 文档 | 内容 |
|---|---|
| `docs/API_REFERENCE.md` | 新增「ArkApi 插件」一节：`/api/plugins/*` 4 个、`/api/arkapi/*` 7 个接口的请求、返回、权限与错误码；目录与端点统计随之更新（REST 45 → 56，合计 56 → 67） |
| `CLAUDE.md` | 目录树加入 `plugindata/`、`arkapimanage/`、`webapi/pluginapi/`，`pkg/archive` 补上 `ExtractZip`；分层依赖加入 `plugindata`、`arkapimanage`；运行时目录加入 `instances/{name}/ArkApi/` 与 `{BaseDir}/arkapi/`；接口表加入两组路由 |
| `docs/ARKAPI_PLUGIN_PLAN.md` | 末尾追加 §12，逐条对照被取代、被推翻、继续有效的部分（§15） |

§13 的「后续」三项仍未开始：运行中实例的插件操作排队到下次启动执行；插件数据纳入备份；删除已退役的搬运代码。

---

# Part 2 — ArkApi 插件数据与配置隔离（原 `ARKAPI_PLUGIN_DATA_PLAN.md`）

> ⚠️ 本部分为前身设计；其核心机制（启停搬运）已被 Part 1 取代（见 Part 1 §15、本部分 §12）。保留全文用于追溯。

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

---

## 12. 已被 `ARKAPI_PLUGIN_PLAN.md` 取代的部分（2026-09-11 追加）

本文的核心机制已被 [`ARKAPI_PLUGIN_PLAN.md`](./ARKAPI_PLUGIN_PLAN.md) 取代，并已实施：插件整个放进实例目录
`instances/{name}/ArkApi/Plugins/`，镜像里的 `Win64/ArkApi/Plugins` 是指向它的例外 junction，插件直接读写实例目录，
不再需要启停搬运，也就没有崩溃窗口。按「PLAN 文档只增不改」的惯例，上文原样保留，逐条对照如下：

| 本文内容 | 现状 |
|---|---|
| §1、§4.3–§4.5、§4.7 启停搬运（Inject / Reclaim / Rescue） | **取代**。启动路径不再调用 Inject；Rescue 只供一次性迁移使用；`harvest` / `Inject` 开头有结构性关断（镜像里的 Plugins 是链接，或实例已迁移，就直接返回）。Reclaim 仍在停止路径上，只为升级那一刻正在运行、尚未迁移的实例服务，对已迁移的实例是空操作 |
| §5 同步例外 `IsProtectedRelPath` | 保留但不再起作用：同步走到 junction 就 `SkipDir`，进不到 Plugins。随搬运代码一起删除 |
| §6「不采纳整目录 junction」 | **推翻**，理由见该文 §3.4：镜像的 Win64 本来就是每实例一份真实拷贝，磁盘占用不变；「更新时回灌非数据文件」正是按实例安装、更新插件这个功能本身 |
| §4.2 文件分类（SQLite 按文件头识别、文件组推导） | 继续有效：插件更新与「从备份恢复」时据此决定哪些数据文件要带过去 |
| §4.6 保序递归配置合并 | 继续有效：插件更新与 ArkApi 主程序 `config.json` 更新时使用 |
| §4.8 `DbPathOverride` 识别 | 继续有效，仅用于展示；「指向实例内部」的判定根目录改为 `ArkApi/Plugins/{P}`。指向旧 `plugins/{P}` 的值在迁移时被清空或改写（该文 §4.4 第 2 步第 5 项） |
| §4.9 在线快照 | 继续有效，改为扫描实例的 `ArkApi/Plugins/`，写进 `ArkApi/PluginSnapshots/{P}/` |
| §8 第 8 条「库大到搬运不可接受时改用 junction」 | 已经提前成为默认 |
| §10.2「源侧配置更新到不了镜像」 | 随之消失：插件更新时 `config.json` 在实例目录里就地合并，新版本的配置键立刻可见 |
| §11 表格第 3 条「Linux 上 pluginapi 应回执本平台不支持」 | 已被 `LINUX_COMPATIBILITY_PLAN.md` 推翻（ArkApi 在 Linux 上已经是目标），**不要照做**；第 1、2 条已实施 |

迁移之后，实例目录里旧的 `plugins/` 被改名为 `plugins.legacy-<时间戳>/` 保留，不再读写；server-files 里的全局插件在所有实例都
迁移完之后，移入 `{BaseDir}/arkapi/backups/legacy-server-plugins-<时间戳>/`。

---

# 附录 Y：文件路径对照（2026-09-29）

| 文档中的路径 | 实际路径（当前代码） |
|---|---|
| 旧顶层包 `asaserver/` | 已整体迁入 `internal/` |
| `internal/arkapimanage/`、`internal/plugindata/` | 与文档一致（本文真实落点） |
| `internal/instance/pluginlayout.go` | 与文档一致 |

# 附录 Z：合并与同步记录（2026-09-29）

本文件由 `docs/ARKAPI_PLUGIN_INSTALL_PLAN.md` 与 `docs/ARKAPI_PLUGIN_DATA_PLAN.md` 于 2026-09-29 逐字物理合并而成（方案甲）；「已知缺陷清单」同步自 `docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md` §7（只读审计，基线 `faf127c`）；两个源文件保持原样，未作删减或改写。