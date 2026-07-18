# 包结构重构方案：asaserver 神包拆分 + pkg 工具库

> 状态：待审阅 / 待执行
> 目标：按**单一职责**拆分领域包，**纯工具函数集中到 `pkg/`**，采用**扁平根目录 + `pkg/`** 约定。
> 原则：**分步渐进迁移，每一步都能独立编译通过并单独提交**，任一步出问题只回退该步。

---

## 1. 背景与问题

当前 `asaserver` 是一个"神包"：8 个文件、约 4700 行，把 6 类互不相关的职责揉在一起。

| 文件 | 行数 | 职责 |
|---|---:|---|
| `config.go` | 738 | 目录布局变量 + `InstanceConfig` + INI 读写 + 配置同步 |
| `server.go` | 965 | 服务器生命周期（start/stop/restart/RCON）+ 混入 `CopyDir` 等工具 |
| `mirror.go` | 1059 | 镜像 / NTFS junction 管理 |
| `state_manager.go` | 799 | BadgerDB 状态存储 |
| `installer.go` | 438 | SteamCMD 下载 / 更新 |
| `common.go` | 673 | 杂项：端口查询、日志文件查找、`FileExists`、进程等待、Mod 提取、存档、控制台清洗、ASA 版本 |

同时**通用工具函数散落各处、且存在重复实现**：

- `CopyDir` 在 `server.go:152`、`copyFile`/`fileMD5` 在 `mirror.go`、`FileExists` 在 `common.go` —— 文件工具三处分散。
- `FormatBytes` 在 `serverinfo`、DNS 解析在 `common`、tail 在 `common/tail`。
- **重复定义**：`GetPIDByPort`（`asaserver/common.go:27` 与 `win32api:154`）、`WaitGamePidExit`（`asaserver/common.go:122` 与 `common/common.go:14`）。

此外 `webapi/api.go` 单文件 1663 行、40 个 handler 全挤在一起。

---

## 2. 目标目录结构（扁平根 + pkg/）

```
asa-server/
├── main.go
├── pkg/                  # 纯工具，叶子包，只依赖标准库/第三方，不含领域逻辑
│   ├── fsutil/           # FileExists, CopyDir, CopyFile, FileMD5
│   ├── winproc/          # 窗口/进程/端口：合并 win32api + common 的 WMI/进程查询
│   ├── netutil/          # ResolveDomainToIP / v4 / v6
│   ├── tail/             # 从 common/tail 迁入
│   ├── console/          # ANSI 去除、arkApi/steamcmd 控制台输出清洗
│   └── humanize/         # FormatBytes（可选，来自 serverinfo）
├── config/               # 目录布局变量 + InstanceConfig + INI 读写 + 配置同步
├── process/              # PID 文件存储 + IsServerRunning 存活判断（解 state↔instance 环的关键层）
├── state/                # BadgerDB 状态存储（原 state_manager.go）
├── installer/            # SteamCMD 下载/更新（原 installer.go）
├── mirror/               # 镜像/junction 管理（原 mirror.go）
├── instance/             # 生命周期 start/stop/restart/RCON + ASA 版本（原 server.go + common.go）
├── webapi/               # HTTP API：按领域拆独立子包，各自 RegisterRouter（详见阶段 G）
│   ├── apiresp/          # StatusResponse + ValidateInstanceName（共享）
│   ├── instanceapi/      # 实例 CRUD + mod-info
│   ├── serverapi/        # start/stop/restart/force-stop + info + update
│   ├── backupapi/        # 世界存档备份/恢复
│   ├── configapi/        # Game.ini/GameUserSettings + 配置同步
│   ├── saveapi/          # 存档解析
│   ├── logapi/           # 日志 SSE 流
│   ├── iconapi/          # 图标
│   ├── actions.go        # APIServer 装配 + setupRoutes 调各包 RegisterRouter
│   └── state_dispatcher.go  # 状态变更 WS 推送（生命周期，非路由）
├── gui/ backup/ serverinfo/ parseserver/ frpmanage/ ...  # 现有，按需更新 import
```

---

## 3. 分层依赖（无环，迁移必须遵守此方向）

```
pkg/*            ← 叶子，不依赖任何领域包
config           ← 只依赖 pkg
process          ← 依赖 config + pkg/winproc          （PID 存储 + 存活判断）
state            ← 依赖 config + process
installer        ← 依赖 config + pkg
mirror           ← 依赖 config + pkg
instance         ← 依赖 config, process, state, mirror, installer, pkg
webapi / gui / backup / serverinfo / ...  ← 依赖上述领域包
```

### ⚠️ 关键：state ↔ instance 循环导入及其解除

- 现状：`state_manager.go` 调用 `IsServerRunning` / `GetInstancePID`（定义在 `server.go`），
  而 `server.go` 又调用 state 的写入函数（`WriteInstanceState`、`CompareAndSwapInstanceState` 等）。
- 若直接拆成 `state` 和 `instance` 两个包 → **形成 import 环**，`go build` 会报 `import cycle not allowed`。
- **解法**：把 **PID 文件存储 + 存活判断**下沉到独立的 `process` 包
  （`SaveInstancePID`、`GetInstancePID`、`IsServerRunning`、`IsServerRunningByPID`、
  state_manager 的 `isInstanceProcessAlive`），由 `state` 和 `instance` 共同 import，环即消除。

> 执行中若仍报环：优先判断"某函数归属层级放错"，按上表把它上移到更底层的包，**而非**引入接口绕开。

---

## 4. 迁移步骤

> 每一步统一节奏：**移动代码 → 改包名/更新 import → `go build ./... && go vet ./...` → 单独提交**。
> 顺序原则：先建叶子（pkg），再自底向上拆领域包。前期步骤零环风险、纯机械移动，最稳。

### 阶段 A：抽取 pkg/ 工具库（叶子包，零环风险）

- [ ] **A1 `pkg/fsutil`**：迁入 `FileExists`(common.go)、`CopyDir`(server.go:152)、`copyFile`/`fileMD5`(mirror.go)。
      **去重**：server.go 与 mirror.go 各有一份文件拷贝逻辑，统一为 `fsutil.CopyFile`。
- [ ] **A2 `pkg/tail`**：`common/tail` 整体移到 `pkg/tail`，更新 2 处引用（webapi、asaserver）。
- [ ] **A3 `pkg/netutil`**：迁入 `common/common.go` 的 `ResolveDomainToIP{,v4,v6}`。
- [ ] **A4 `pkg/console`**：迁入 `arkApiCleanConsoleOutput`、`steamcmdCleanConsoleOutput`、`removeANSIEscapes`。
- [ ] **A5 `pkg/humanize`**（可选）：`serverinfo.FormatBytes` → `humanize.FormatBytes`。

### 阶段 B：合并 win32api + common → pkg/winproc，消除重复

- [ ] 将 `win32api/*`（窗口/进程/端口 API）与 `common/common.go` 的 `Win32Process`/`QueryProcess`/`escapeWQL`
      合并为 **`pkg/winproc`**。
- [ ] **消除重复定义（先判定"同名是否同功能"，勿盲目合并）**：
  - ✅ `GetPIDByPort`：`asaserver/common.go:27` 与 `win32api/win32api.go:154` **逐行完全相同** → 安全去重，
    统一保留 `winproc.GetPIDByPort`，删另一份。
  - ⚠️ `WaitGamePidExit`：`asaserver/common.go:122`（**轮询 500ms**，服务 ARK 游戏进程，asaserver 内 2 处调用）
    与 `common/common.go:14`（**轮询 2s**，服务 syncthing 进程，`syncthingmanage/manager.go:156` 调用）
    —— **同名但行为不同（轮询间隔不同、服务对象不同），不可直接"统一保留一处"**。
    正确做法二选一：① 参数化为 `winproc.WaitProcessExit(ctx, pid, interval)`，两个调用方各传自己的间隔；
    ② 保留两份并按用途重命名（如游戏侧 500ms / syncthing 侧 2s），避免语义被悄悄改掉。
- [ ] 完成后删除已被掏空的 `common/`（tail→A2、DNS→A3、进程查询→本阶段）。

> **同名函数排查结论**（全仓核对）：真正"同名不同功能"的只有 `WaitGamePidExit` 一处（见上）。
> 其余同名均合法、非陷阱：不同结构体的方法（`Start`/`Stop`/`Write`/`Close`/`Subscribe`/`IsRunning`/
> `Restart`/`CheckStatus`/`Cleanup` 等）、跨包统一约定接口（`Initialize`/`GetGlobalManager` 在
> frpmanage/syncthingmanage/batchmanage 各一份）、同包内"方法 + 全局便捷函数"配对
> （httpserver 的 `(h *Hub) BroadcastServerXxxEvent` 与包级 `BroadcastServerXxxEvent`）。

### 阶段 C：抽取 `config`（神包的地基，先拆）

- [ ] `asaserver/config.go` 整体迁为 `config` 包：目录变量（`BaseDir`/`InstancesDir`/`ServerFilesDir` 等）、
      `InstanceConfig`、INI 读写、`SyncInstanceConfig*`、`SetMessageOfTheDay`。
- [ ] config.go 已核实**不调用** server/mirror 函数，是干净地基。
      **审计点**：若 `SetMessageOfTheDay` 需要 RCON/进程状态，则改放到阶段 F 的 `instance` 包。
- [ ] `asaserver` 被 11 处引用、多数是配置/目录变量，**先拆 config 可解锁后续所有步骤**。

### 阶段 D：抽取 `process`（解环关键层）

- [ ] 新建 `process` 包，迁入：`SaveInstancePID`、`GetInstancePID`、`IsServerRunning`、`IsServerRunningByPID`
      （原 server.go）+ `isInstanceProcessAlive`（原 state_manager.go）。
- [ ] 依赖 `config` + `pkg/winproc`。这是 `state`/`instance` 能各自独立的前提。

### 阶段 E：抽取 `state` / `installer` / `mirror`（三个并行的中间层）

- [ ] **E1 `state`**：`state_manager.go` → `state` 包，依赖 `config` + `process`。
- [ ] **E2 `installer`**：`installer.go` → `installer` 包，依赖 `config` + `pkg/fsutil`。
- [ ] **E3 `mirror`**：`mirror.go` → `mirror` 包，依赖 `config` + `pkg/fsutil` + `pkg/winproc`（`IsElevated` 等）。

### 阶段 F：抽取 `instance`（生命周期，顶层领域包）

- [ ] 剩余 `server.go` + `common.go` 中的服务器逻辑 → `instance` 包：`StartServer`/`StopServer`/
      `RestartServer`/`ForceStopServer`/`SendRCONCommand`/`SyncGameConfigToInstance`、启动/停止等待、
      `MonitorAndExtractModInfo`、`SaveWorldSafely`、`WaitArkApiRunServer`。
- [ ] **ASA 版本**：`GetAsaVersion`/`GetInstanceAsaVersion` 归入 `instance`（依赖 `config`+`mirror`）。
- [ ] 依赖 `config, process, state, mirror, installer, pkg/*`。至此 `asaserver` 包被完全拆解删除。

### 阶段 G：拆分 `webapi/api.go` —— 按领域拆成独立子包，各自 `RegisterRouter` 注册路由

> 现状：`webapi/api.go` 单文件 1663 行、40 个 handler 全是 `*APIServer` 方法；`task.go`（更新/启停任务）、
> `icons.go`（图标）、`state_dispatcher.go`（状态推送）分列。目标是**按领域拆到 `webapi/` 下的独立子包**，
> 每个包对外只暴露 `RegisterRouter`，由 `actions.go` 的 `setupRoutes` 统一装配。此步**零 import 环风险**
> （子包只依赖 asaserver/httpserver/parseserver 等下层，不反向依赖 `webapi`）。

#### G.0 三项既定决策（已确认）

1. **包位置/命名**：新包放在 `webapi/` 下，如 `webapi/instanceapi`、`webapi/configapi`；
   共享类型放 `webapi/apiresp`。`actions.go` 保留 `APIServer` 装配 + `setupRoutes`。
2. **拆分粒度（~7 包）**：`instanceapi` / `serverapi`（start/stop/restart/force-stop + info + update 合一）
   / `backupapi` / `configapi` / `saveapi` / `logapi` / `iconapi`。
3. **跨领域共享状态传递**：每包定义 `Handler` 结构体持有所需依赖，`NewHandler(deps...)` 构造，
   统一暴露 `(h *Handler) RegisterRouter(r *gin.Engine)`。无全局变量，签名统一。

#### G.1 共享层 `webapi/apiresp`（先建，被所有子包依赖）

- [ ] `StatusResponse`（全项目用了 **101 次**的响应信封）迁入 `apiresp`。
- [ ] `validateInstanceName` → `apiresp.ValidateInstanceName`（导出，供各子包复用）。

#### G.2 关键：跨领域共享状态的归属

`APIServer` 现有 4 项跨领域状态，拆分时按需下沉到对应子包的 `Handler`，`NewAPIServer` 在装配时注入：

| 共享状态 | 类型 | 现属 | 迁往 |
|---|---|---|---|
| `serverCtx` | `context.Context` | APIServer | 注入 `serverapi`/`logapi`/`saveapi`（SSE 退出信号） |
| `updateBroadcaster` | `*httpserver.TaskBroadcaster` | APIServer | 移入 `serverapi.Handler`（由 `NewHandler` 内部 `httpserver.NewTaskBroadcaster()` 创建） |
| `updateCancel` | `atomic.Pointer[context.CancelFunc]` | APIServer | 移入 `serverapi.Handler` |
| `saveDataManager` | `*parseserver.SaveDataManager` | APIServer | 注入 `saveapi.Handler` |

> 结论：`APIServer` 拆分后**可移除** `updateBroadcaster`、`updateCancel` 两个字段（仅 update 用），
> `serverCtx`、`saveDataManager` 仍由 `APIServer` 持有并在 `setupRoutes` 时注入各子包。

#### G.3 各子包职责、依赖与路由

- [ ] **`instanceapi`**（无状态）：health、list/create/get/delete/rename、get/updateInstanceConfig、getModInfo。
      类型 `InstanceInfo`/`ListResponse`/`CreateInstanceRequest`/`RenameInstanceRequest`。
      路由 `/health`、`/api/instances/*`、`/api/mod-info`。
- [ ] **`serverapi`**（持 `serverCtx`+`updateBroadcaster`+`updateCancel`）：start/stop/restart/force-stop
      （含 `runStartServerTask`/`runStopServerTask`/`runRestartServerTask`，原 task.go）、
      update（handle/status/cancel + `runUpdateTask`）、info（streamServerInfo/streamAllInstancesInfo）。
      路由 `/api/server/*`。依赖 asaserver、serverinfo、win32api、httpserver。
- [ ] **`backupapi`**（无状态）：backup/list/restore/delete world。类型 `RestoreWorldBackupRequest`。
      路由 `/api/backup/world/*`。依赖 backup、asaserver。
- [ ] **`configapi`**（无状态）：server/instance configs、game-ini/gus 读写上传、update、syncInstanceConfig。
      类型 `ConfigFileRequest`/`SyncInstanceConfigRequest`。路由 `/api/config/*`。依赖 asaserver。
- [ ] **`saveapi`**（持 `serverCtx`+`saveDataManager`）：save players/tribes/all、handleSaveParse、
      streamSaveData、findSaveFileByInstance。路由 `/api/save/*`。依赖 parseserver、asaserver。
- [ ] **`logapi`**（持 `serverCtx`）：streamInstanceLogs、streamSystemLogs。路由 `/api/logs/*`。
      依赖 asaserver、logger、common/tail。
- [ ] **`iconapi`**（无状态，原 icons.go）：getCreatureIcon、getItemIcon（+ `normalizeIconName`/`serveIcon`）。
      路由 `/api/icons/*`。依赖 icon。

#### G.4 `actions.go` 装配改造

`setupRoutes` 内原有的逐条 `s.engine.GET(...)` 替换为各子包的 `RegisterRouter`，
与现有 `frpmanage.RegisterFRPRoutes(s.engine)` 风格一致：

```go
instanceapi.NewHandler().RegisterRouter(s.engine)
serverapi.NewHandler(s.serverCtx).RegisterRouter(s.engine)   // 内部自建 updateBroadcaster
backupapi.NewHandler().RegisterRouter(s.engine)
configapi.NewHandler().RegisterRouter(s.engine)
saveapi.NewHandler(s.serverCtx, s.saveDataManager).RegisterRouter(s.engine)
logapi.NewHandler(s.serverCtx).RegisterRouter(s.engine)
iconapi.NewHandler().RegisterRouter(s.engine)
// 保留：WebSocket、frp/syncthing/batch 路由、静态资源、NoRoute
```

- [ ] `state_dispatcher.go` 保持在 `webapi`（属服务器生命周期，由 `Start()` 调用，非路由 handler）。
- [ ] 迁移完成后删除 `webapi/api.go`、`webapi/task.go`、`webapi/icons.go`（内容已全部迁出）。

#### G.5 分步落地顺序（每步 `go build ./webapi/... ` 验证）

1. 建 `apiresp` → `go build ./webapi/apiresp/`。
2. 逐个建子包（各自 `go build ./webapi/<pkg>/`，此时旧 `webapi` 仍完整、可并存编译）。
3. 全部子包编译通过后，一次性改造 `actions.go` + 删除 `api.go`/`task.go`/`icons.go` + 删 `APIServer` 冗余字段。
4. `go build ./...` + 启动冒烟。任一步失败只回退该步。

### 阶段 H：收尾

- [ ] 删除空目录 `common/`、`win32api/`（已并入 pkg）。
- [ ] 更新 `CLAUDE.md` 的「Project Structure / Key Packages / Key Data Flows」段落。
- [ ] 全量 `go build ./...`、`go vet ./...`、`go test ./...`。

---

## 5. 关键复用点（迁移时直接引用，勿重写）

| 内容 | 位置 | 去向 |
|---|---|---|
| 目录变量 + `InstanceConfig` | `asaserver/config.go:17-49` | `config` |
| `InstanceMirrorDir` | `asaserver/mirror.go:108` | `mirror`（版本读取依赖它） |
| `GetAsaVersion` / `GetInstanceAsaVersion` | `asaserver/common.go:564,644` | `instance`（已实现，含缓存） |
| 窗口/进程 API | `win32api/*.go` | `pkg/winproc` |
| WMI 查询 | `common/common.go:81-135` | `pkg/winproc` |

---

## 6. 验证方式（渐进、可回退）

1. **每个阶段单独一个 commit**；阶段内每移动一个包就跑一次 `go build ./... && go vet ./...`。
2. **import 环检测**：`go build` 直接报 `import cycle not allowed` —— 出现即说明某函数归属放错层，
   按第 3 节「分层依赖」把它上移（典型：被 `state` 和 `instance` 同时依赖的东西要放 `process`）。
3. 无测试的包用 `go vet` + 编译兜底；有测试的（`serverinfo`、`common`、`asaserver`、`tail`）迁移后跑对应 `go test`。
4. 全部完成后端到端冒烟：
   ```bash
   go build -o asa-server.exe && ./asa-server.exe api
   ```
   - 请求 `GET /api/instances`（确认 `asaVersion` 等字段正常）
   - 启动 / 停止一个实例
   - 触发一次 update
   - 确认生命周期、镜像、状态、SSE 流均工作正常。
5. 建议按 **A→B→C→D→E→F→G→H** 顺序逐步合入 `master`，任一步出问题只回退该步。

---

## 7. 风险与注意

- **循环导入**是本次唯一实质风险，已通过引入 `process` 中间层预先规避。
- 迁移以**移动 + 改包名/import**为主，尽量不改函数逻辑，降低 review 与回归成本。
- 阶段 **A、B、G 无环风险且收益立竿见影**，可优先落地；**C~F 是核心攻坚**，须严格按依赖顺序。
- Windows-only 项目：涉及 `win32api`/`winproc` 的改动务必在 Windows 上编译验证（含 `syscall`/`windows` 包）。
