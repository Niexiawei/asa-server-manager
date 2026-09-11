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
> [`ARKAPI_PLUGIN_DATA_PLAN.md`](./ARKAPI_PLUGIN_DATA_PLAN.md)（现行的「启停搬运」式数据隔离。本方案**取代**它的核心机制，
> 见 §15）、
> [`ARKAPI_CACHE_PREFETCH_PLAN.md`](./ARKAPI_CACHE_PREFETCH_PLAN.md)（`ArkApi/Cache` 的归属）、
> [`ACL_PERMISSION_HARDENING_PLAN.md`](./ACL_PERMISSION_HARDENING_PLAN.md)（Linux 下 junction 目标的权限处理）。

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

`ARKAPI_PLUGIN_DATA_PLAN.md` §6 当初否掉「整目录 junction」的两条理由，现在都不成立了（§3.4）。

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

`ARKAPI_PLUGIN_DATA_PLAN.md` 的机制是：配置与数据存放在 `instances/{name}/plugins/{P}/`，
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

`ARKAPI_PLUGIN_DATA_PLAN.md` §6：

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
（同 `ARKAPI_PLUGIN_DATA_PLAN.md` §10.4）。

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
这和 Linux 上「整体静默」的做法同一个思路（`ARKAPI_PLUGIN_DATA_PLAN.md` §11），不依赖任何标志位，漏不掉。

- **判定函数**：新增 `fsutil.IsLink(path)`，就是「`os.Readlink` 成功」；`mirror.isJunctionOrSymlink` 改为调用它，两边共用一份实现。
  放在 `pkg/fsutil` 是因为 `plugindata` 不能依赖 `mirror`（依赖方向是反过来的，见 `ARKAPI_PLUGIN_DATA_PLAN.md` §10.1）。
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
   5. **改写指向旧目录的 `DbPathOverride`**。`ARKAPI_PLUGIN_DATA_PLAN.md` §4.8 把「指向实例插件目录内」列为等价形态，
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
     所以新版本新增的配置键会立刻出现在用户面前，`ARKAPI_PLUGIN_DATA_PLAN.md` §10.2 那个「新配置到不了实例」的缺口也随之消失；
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
  所以本方案的面板、接口、迁移在 Linux 上**照常工作**。`ARKAPI_PLUGIN_DATA_PLAN.md` §11 表格第 3 条
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
| **P8** | 文档：`API_REFERENCE.md`；`CLAUDE.md` 目录树与数据流（加入 `arkapimanage`、`pkg/archive/zip.go`、Plugins 例外 junction）；在 `ARKAPI_PLUGIN_DATA_PLAN.md` 末尾追加一节说明被取代的部分（§15） | 全部 |
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

## 15. 与 `ARKAPI_PLUGIN_DATA_PLAN.md` 的关系

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

按「PLAN 文档只增不改」的惯例，不修改 `ARKAPI_PLUGIN_DATA_PLAN.md` 的原文，只在 P8 阶段于其末尾追加一节，指向本文。

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
