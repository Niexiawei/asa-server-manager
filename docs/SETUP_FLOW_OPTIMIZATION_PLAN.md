# Setup 流程优化方案

> 目标：让「基础环境未初始化」这件事在**用户第一眼就能看到**，而不是等到启动某个实例时在
> `runner.Run` 深处报一句 `umu-run not found`。同时把 `asa-server setup` 从「仅 Linux」扩成
> 两平台通用的环境初始化入口；Windows 用户以双击运行为主，因此在 Fyne GUI 里再实现一份
> 带**实时进度输出**的引导流程（§3.7）。
>
> 关联文档：`docs/LINUX_COMPATIBILITY_PLAN.md`（§4.2 依赖自检、§5.1 runner、§10 首次运行数据目录、
> §10.6 Linux 只有 CLI、§10.7 `asa-server api` 一等入口三条不变量）、`docs/LINUX_DEPLOYMENT.md`。
>
> 状态：**方案，待实施**。

---

## 1. 背景与现象

### 1.1 Linux：`asa-server api` 能起、实例起不来

在一台只跑过 `asa-server api`（或无参启动、或 `service install` + `service start`）的全新 Linux 机器上：

- API 服务正常监听、SPA 正常加载、鉴权/证书/frp/syncthing 全部就绪；
- 但**任何实例的启动都会失败**，因为三样东西都没装：
  1. **Wine/Proton 运行时**（`{BaseDir}/umu-launcher/umu-run`、`{BaseDir}/proton/GE-Proton10-34/`、`{BaseDir}/umu-prefix/`）
  2. **SteamCMD**（`{BaseDir}/steamcmd/steamcmd.sh`）
  3. **ARK 服务端本体**（`{BaseDir}/server-files/ShooterGame/Binaries/Win64/ArkAscendedServer.exe`）

失败点很深、报错很不友好：`internal/runner/runner_linux.go:82-94` 的 `umuCommandLine` 在
umu-run / proton / prefix 任一缺失时返回 `runner: umu-run not found at ... (call EnsureRuntime first)`，
而这条错误要一路冒泡穿过 `instance.StartServer` → SSE task broadcaster 才到前端。

### 1.2 现状梳理（代码事实）

| 位置 | 现在的行为 | 问题 |
|---|---|---|
| `internal/webapi/actions.go:398-400` | `runner.Preflight()` 结果只 `logger.Warnf`，不阻断 | 缺 glibc32/python3 时服务照常起，用户不会去翻日志 |
| `internal/webapi/actions.go:406-410` | `runner.EnsureRuntime` 丢进 fire-and-forget goroutine，不等待、失败只告警 | 实例启动时运行时可能还没下载完（GE-Proton ~450MB），或已失败 |
| `internal/webapi/actions.go` 整个 `InitializationBasicComponents` | **完全不碰 SteamCMD 和 ARK 本体** | `api` 启动路径本就不负责装本体，但也没有任何提示 |
| `internal/instance/server.go:385` | `runner.Run(...)` 前**没有任何运行时就绪校验** | 依赖 §1.2 第 2 行那个后台 goroutine 已经跑完，否则底层报错 |
| `internal/actions/setup.go:46` | `runtime.GOOS != "linux"` 直接报错退出 | Windows 上 `setup` 完全不可用 |
| `internal/actions/setup.go:55-66` | `runner.Preflight()` 打印问题后**继续往下走** | 缺 32 位 glibc 时，SteamCMD（32 位 ELF）必然失败；缺 python3 时 umu 必然失败——白下载 450MB |
| `main.go:184-189` | 无参启动直接 `runDefaultAction`（Linux = `webapi.ActionAPI`） | 不检查基础环境 |
| `internal/svcmgr/service.go:188-190` | `ActionServiceInstall` 直接装服务 | 装完的服务一个实例都跑不起来，比一次性报错更糟 |

### 1.3 关于「Linux 下 `ensureRuntime` 未实现」的澄清

`ensureRuntime` **已实现**，在 `internal/runner/umu_linux.go:60`：下载 umu-launcher zipapp、
下载并校验 GE-Proton、`wineboot --init` 预热 prefix、`.created-by-proton` 版本标记与迁移，
逐行照抄 `scripts/ark_instance_manager.sh` 的验证过实现。

真正的缺口是**接线**，不是实现：

1. `instance.StartServer`（`server.go:385`）直接调 `runner.Run`，中间没有 `runner.EnsureRuntime`
   或任何就绪检查，只能指望 `InitializationBasicComponents` 里的后台 goroutine 已完成。
2. `runner` 没有一个「纯本地、零网络」的快速就绪检查供业务层调用——`umuCommandLine` 内联的三处
   `os.Stat` 才是判据，但它藏在启动命令拼装里，拿不到、复用不了。
3. `Runtime == "custom"` 时 `ensureRuntime` 只打一行日志就 `return nil`，不校验用户自己配的
   `PROTONPATH`/`PrefixDir` 是否真的存在。

本方案把这三条一起补上。

---

## 2. 目标

1. **Linux**：环境未初始化时，交互式 `asa-server api` / 无参启动 / `service install` 明确提示并引导到
   `asa-server setup`，而不是静默起一个「废」服务。
2. **Linux**：`asa-server setup` 的 `Preflight` 从「告警不阻断」改为「不通过就停，并告诉用户手动补齐」。
3. **两平台**：`asa-server setup` 成为通用的环境初始化入口。Windows 上执行 BaseDir 选择 +
   SteamCMD + ARK 本体安装 + 验证（不涉及 umu/GE-Proton/preflight）。
4. **Windows GUI**：Fyne 首次启动向导在选完 BaseDir 后，接一个带**实时日志/进度**的环境初始化
   面板，双击运行的用户不用碰命令行也能装好本体（§3.7）。
5. **接线**：给 `runner` 补一个零网络的就绪检查，接进实例启动路径，把底层报错换成人话。
6. **不破坏** `docs/LINUX_COMPATIBILITY_PLAN.md §10.7` 的三条不变量（见 §4.3）。
7. **Windows 零回归**：Windows 上 `setup` 此前就是「直接报错」，改成可用是纯增量；GUI 新增面板
   不改动任何现有面板行为；其余路径不动。

---

## 3. 设计

### 3.1 新增「基础环境就绪」检测

#### 3.1.1 `runner.CheckRuntime() error` —— 纯本地、零网络

把 `runner_linux.go` 里 `umuCommandLine`（`runner_linux.go:82-94`）内联的三处 `os.Stat` 提炼成
一个可复用的导出函数：

```go
// runner.go —— 跨平台入口
//
// CheckRuntime 检查启动运行时是否已就绪，只做本地文件检查，绝不触网。
// Windows 恒返回 nil。Linux 下按 Config.Runtime 分支：
//   - "umu"    ：umu-run / GE-Proton 的 `proton` / Wine prefix 的 system.reg 三者都在
//   - "custom" ：PROTONPATH 指向的目录 + 其下的 `proton` + PrefixDir 都在
// 返回的 error 措辞面向最终用户，直接可展示（同 appconfig.ValidateBaseDir 的约定）。
func CheckRuntime() error { return checkRuntime() }
```

- `runner_windows.go`：`func checkRuntime() error { return nil }`
- `runner_linux.go`：复用 `umuRunPath` / `protonPath` / `prefixDir`，检查
  - `umu` 模式：`umu-run` 可执行、`protonPath(cfg)/proton` 是文件、`prefixDir(cfg,"")/system.reg` 存在
  - `custom` 模式：`cfg.PrefixDir` 存在、`$PROTONPATH`（从 env 或 cfg 推断）目录及其 `proton` 存在
  - 任一缺失 → `fmt.Errorf("Wine/Proton 运行时尚未初始化：%s。请运行 asa-server setup 完成环境准备", 具体缺哪一项)`
- `umuCommandLine` 改为先调 `checkRuntime()`，命中同一份判据，消除重复。

#### 3.1.2 `installer.CheckInstalled() InstallStatus`

`internal/installer` 新增（`installer` 已经知道 `cfgpkg.ServerFilesDir` / `SteamCmdDir`，判据现成）：

```go
type InstallStatus struct {
    SteamCmdReady    bool // {SteamCmdDir}/steamcmd.exe (win) 或 steamcmd.sh (linux)
    ServerBinaryReady bool // {ServerFilesDir}/ShooterGame/Binaries/Win64/ArkAscendedServer.exe
    ServerConfigReady bool // {ServerFilesDir}/ShooterGame/Saved/Config/WindowsServer/ 目录存在
}

func (s InstallStatus) Ready() bool { return s.SteamCmdReady && s.ServerBinaryReady && s.ServerConfigReady }

func CheckInstalled() InstallStatus
```

- SteamCMD 可执行文件名走已有的 `steamCmdBinaryName`（`steamcmd_windows.go` / `steamcmd_linux.go`）。
- `ArkAscendedServer.exe` 路径与 `VerifyServerInstallation`（`installer.go:371`）、`configDir`
  （`installer.go:357`）完全一致，避免判据漂移。

#### 3.1.3 `actions.VerifyEnvironmentReady() error` —— 面向用户的组合器

放在 `internal/actions`（它已依赖 `installer`；`webapi` / `svcmgr` 依赖 `actions` 无环——
`actions` 不 import `webapi`/`svcmgr`）：

```go
// VerifyEnvironmentReady 汇总运行时 + SteamCMD + ARK 本体的就绪状态。
// 全部就绪返回 nil；否则返回一段多行、可直接打印给用户的 error，
// 末尾固定一句「请运行 asa-server setup」。
func VerifyEnvironmentReady() error {
    var missing []string
    if err := runner.CheckRuntime(); err != nil {           // Windows 恒过
        missing = append(missing, "  - "+err.Error())
    }
    st := installer.CheckInstalled()
    if !st.SteamCmdReady    { missing = append(missing, "  - SteamCMD 未安装") }
    if !st.ServerBinaryReady { missing = append(missing, "  - ARK 服务端本体未安装") }
    if !st.ServerConfigReady { missing = append(missing, "  - ARK 首次配置文件未生成") }
    if len(missing) == 0 { return nil }
    return fmt.Errorf("基础环境尚未初始化，检测到以下缺失：\n%s\n\n请先运行：asa-server setup",
        strings.Join(missing, "\n"))
}
```

### 3.2 `asa-server setup` 改造

#### 3.2.1 跨平台化

- 删除 `setup.go:46-48` 的 `runtime.GOOS != "linux"` 早退。
- 更新 `SetupCommand()` 的 `Usage` 与 `ActionSetup` 的文档注释（不再写「Windows 请用 GUI」；
  改为「两平台通用；Windows 上不涉及 Wine/Proton」）。
- 保留一处 `runtime.GOOS == "linux"` 分支，只圈住 Linux 独有的 `Preflight` + `EnsureRuntime`。

改造后 `ActionSetup` 主干：

```
=== ASA Server Manager 首次引导 ===
[linux] 宿主依赖自检 (runner.Preflight)     ← §3.2.2 改为阻断
BaseDir 解析 (resolveSetupBaseDir，不变)
EnsureDirectories
runner.Configure(appCfg.Linux + BaseDir)
[linux] runner.EnsureRuntime(ctx, os.Stdout)   ← Windows 上 no-op，连提示都不打
actions.InstallBaseEnvironment(ctx, os.Stdout)  ← §3.2.4 抽出的共享三步
=== 引导完成 ===  ← 收尾提示按平台区分（Linux 提 systemd/cert sudo；Windows 提 service/GUI）
```

Windows 上等价于「BaseDir 向导 + `asa-server update` + verify」一把梭，也是 Windows 的**无头/脚本化**
安装路径（CLI 场景）；双击运行的用户走 §3.7 的 GUI 面板。

#### 3.2.4 抽出平台无关的三步安装 `actions.InstallBaseEnvironment`

CLI 的 `ActionSetup` 与 §3.7 的 GUI 面板**共用同一段安装逻辑**，避免两处漂移：

```go
// actions/envinstall.go
//
// InstallBaseEnvironment 执行与平台无关的本体安装三步：SteamCMD → ARK 本体 → 首次配置验证。
// 进度统一写入 w：CLI 传 os.Stdout，GUI 传 UI 绑定的 io.Writer（§3.7）。
// Linux 侧的 Preflight / EnsureRuntime 由调用方在前面单独完成，不在本函数内。
func InstallBaseEnvironment(ctx context.Context, w io.Writer) error {
    if err := installer.DownloadAndExtractSteamCmd(ctx, w); err != nil {
        return fmt.Errorf("安装 SteamCMD 失败: %w", err)
    }
    if err := installer.DownloadAndUpdateArkServer(ctx, w); err != nil {
        return fmt.Errorf("安装 ARK 服务端本体失败: %w", err)
    }
    if err := installer.VerifyServerInstallation(ctx, false, w); err != nil { // 见下：加 io.Writer
        return fmt.Errorf("生成首次配置失败: %w", err)
    }
    return nil
}
```

配套改动：`installer.VerifyServerInstallation` 签名从 `(ctx, force bool)` 加一个
`outputCallback ...io.Writer`，与同包 `DownloadAndExtractSteamCmd` / `DownloadAndUpdateArkServer`
对齐；把它现有 `logger.Info` 的关键节点（"First installation detected..."、"Running server
verification on port..."、"Server verification completed."）同时写一份到 w，让 GUI/CLI 能看到进度。

#### 3.2.2 `Preflight` 改为阻断

`setup.go:55-66` 改为：

```go
if problems := runner.Preflight(); len(problems) > 0 {
    fmt.Println("宿主运行时依赖不满足，setup 无法继续。请按下面的建议手动安装后重试：")
    for _, p := range problems {
        if p.Fix != "" {
            fmt.Printf("  - [%s] %s\n      修复：%s\n", p.Name, p.Detail, p.Fix)
        } else {
            fmt.Printf("  - [%s] %s\n", p.Name, p.Detail)
        }
    }
    if !cmd.Bool("ignore-preflight") {
        return fmt.Errorf("宿主依赖缺失，已中止；补齐后重跑 asa-server setup（或加 --ignore-preflight 强行继续）")
    }
    fmt.Println("--ignore-preflight 已指定，忽略上述问题继续。")
}
```

理由：

- 这些是 **OS 级包**（`libc6:i386` / `glibc.i686`、`python3`、`libzstd1`、`tar`、
  `kernel.apparmor_restrict_unprivileged_userns`），装它们要 root，且各发行版命令不同——程序
  **不应该**替用户跑 `sudo apt install`（与 §5.8「不自动切换专用用户」同一立场）。
- 在 `setup` 语境下用户已明确表达「我要初始化环境」，缺 32 位 glibc 还继续，只会在 SteamCMD
  （32 位 ELF）或 umu（需 python3）那里失败，白下载几百 MB。
- 新增 `--ignore-preflight` bool flag 作逃生舱：某些非主流发行版上检查可能误报
  （如 libzstd 装在检查列表外的路径且 `ldconfig` 不可用）。
- **`asa-server api` 启动路径不变**（仍是 `logger.Warnf`，见 §3.3）——那里阻断面太大，
  与 §4.2「§4.2 落地时已刻意弱化为不阻断」的既有决定一致。

#### 3.2.3 `EnsureRuntime` 补 `custom` 模式校验

`umu_linux.go:67-70`：`custom` 分支在 `return nil` 前加一次 `checkRuntime()`（§3.1.1），
用户 `PROTONPATH`/`PrefixDir` 配错时当场报错，而不是留到实例启动。

### 3.3 `asa-server api` / 无参启动 的重定向

**分场景处理，严守 §10.7 三条不变量**（门禁的是「运行时/本体」，不是 BaseDir 解析）：

| 场景 | 行为 |
|---|---|
| 交互式（stdin 是 TTY）`asa-server api` 或无参启动，且 `VerifyEnvironmentReady()` 失败 | 打印该 error（含「请运行 asa-server setup」），**非零退出**。加 `--skip-env-check` 逃生舱跳过 |
| 服务模式（`isRunningAsService()` / `RunService`） | **永不硬退出**（systemd 会 restart-loop）。`log.Printf` + `logger.WithConsole().Warn` 打一条醒目告警后照常起 API |
| 非 TTY 但非服务（CI、`nohup`、pipe） | 同交互式：非零退出 + 提示（脚本应显式加 `--skip-env-check` 或先跑 `setup`） |

落点：

- `main.go`：无参分支（`main.go:184-189`）在 `runDefaultAction` 前插入 gate。
- `api` 子命令：`main.go:122-125` 的 `Action` 从 `webapi.ActionAPI` 换成一个薄包装
  `gatedActionAPI`（同文件内），先 gate 再调 `webapi.ActionAPI`。`webapi.ActionAPI` 本身保持纯净。
- `RunService`（`svcmgr/service.go:171`）内部不 gate；改为在 `program.Start`
  （`service.go:41`）里，`webapi` 起来前 `logger.WithConsole().Warn` 一次
  `actions.VerifyEnvironmentReady()` 的结果（非 nil 时）。
- Windows 上 `installer.CheckInstalled()` 仍会检查 SteamCMD/本体——若 Windows 用户也没跑过
  `update`/`setup`/GUI 向导，同样会被提示，这是合理的（Windows 现状是实例启动时才报
  `ArkAscendedServer.exe not found`）。

> **不变量核对**：
> 1. 「BaseDir 解析必须保留 exe 目录兜底」——不受影响，gate 在 BaseDir 已解析之后才跑。
> 2. 「向导做的事都有 CLI 等价物」——`setup` 就是那个等价物，`--skip-env-check` 保留强行起服的能力。
> 3. 「`api` 不得依赖 GUI，也不得要求 `config.yaml` 里有 `basedir` 字段或环境变量」——gate 检查的是
>    运行时/本体文件，与 `basedir` 字段/环境变量无关；服务模式下更是完全不阻断。

### 3.4 `service install` 的重定向

`svcmgr.ActionServiceInstall`（`service.go:188-190`）在 `InstallService()` 前：

```go
func ActionServiceInstall(ctx context.Context, cmd *cli.Command) error {
    if !cmd.Bool("force") {
        if err := actions.VerifyEnvironmentReady(); err != nil {
            return fmt.Errorf("%w\n\n装成服务前请先完成环境初始化；确需强行安装可加 --force", err)
        }
    }
    return InstallService()
}
```

- `main.go:131-135` 的 `service install` 子命令加一个 `--force` bool flag。
- 理由：装一个「起不来任何实例」的 systemd/SCM 服务，比一次性命令报错更难排查
  （服务会静默 restart-loop 或空转），且和 §10.7「注册服务是 setup 的一个步骤」一致。
- `svcmgr` import `actions`：`actions` 依赖 `installer`+`state`，不依赖 `svcmgr`，无环。

### 3.5 实例启动路径接线

`internal/instance/server.go`，`runner.Run`（`server.go:385`）之前：

```go
if err := runner.CheckRuntime(); err != nil {
    startErr = fmt.Errorf("无法启动实例：%w", err)   // Windows 恒过；Linux 给「运行时未初始化，请运行 asa-server setup」
    return startErr
}
```

- 只做本地检查，不触网、不阻塞（不在这里同步跑 `EnsureRuntime`——那是几百 MB 下载，不该塞进
  一个 start 请求里；就绪工作归 `setup` 和 `InitializationBasicComponents` 的后台 warm）。
- 效果：把 `umu-run not found at /.../umu-run (call EnsureRuntime first)` 换成一句用户能看懂、
  且带下一步操作的错误。

### 3.6（可选）`GET /api/system/preflight` 扩展

`internal/webapi/systemapi/systemapi.go:28` 目前只返回 `runner.Preflight()`。可扩成：

```json
{
  "preflight": [ { "name": "...", "detail": "...", "fix": "..." } ],
  "runtimeReady": false,
  "steamCmdReady": false,
  "serverBinaryReady": false,
  "serverConfigReady": false
}
```

前端在 Linux 的引导页/设置页据此显示「环境未就绪，请在终端运行 `asa-server setup`」。
本项可独立于前面几条，排在最后做。

### 3.7 Windows GUI 引导面板（双击运行场景）

Windows 用户绝大多数是双击 exe 直接进 Fyne GUI，不会去开命令行跑 `asa-server setup`。因此在
GUI 里实现一份等价引导，并满足「实时输出安装进度」的要求。

#### 3.7.1 触发时机

| 场景 | 行为 |
|---|---|
| 全新安装：`runFirstLaunchWizardIfNeeded` → `showBaseDirPicker` 选完目录、`applyChosenBaseDir` 写完 `config.yaml` | 不再只弹「数据目录已设置」，接着弹 §3.7.2 的引导面板 |
| GUI 启动时 `actions.VerifyEnvironmentReady()` 返回非 nil（老用户没装过本体，或装了一半） | 主窗口「服务管理」区顶部显示一条黄色提示条 + 「初始化环境」按钮，点了进 §3.7.2 |
| 用户点「启动 API 服务器」/ 实例启动失败且原因是缺本体 | 弹确认框："基础环境尚未初始化，是否现在初始化？" → §3.7.2 |
| 「服务管理」区常驻一个「环境初始化 / 修复本体」按钮 | 任何时候可手动重跑（幂等：SteamCMD/本体已在则各步快速跳过） |

#### 3.7.2 引导面板 `showSetupProgress()`

一个独立 `fyne.Window`（不用 modal dialog——安装耗时长、日志多，需要可调整大小、可滚动）：

```
┌─ ASA Server Manager · 环境初始化 ──────────────────┐
│ 数据目录: D:\ASA-Data                               │
│ 当前步骤: 正在下载 ARK 服务端本体（约 25 GB）...     │
│ [======================              ]  (进度条)    │
│ ┌────────────────────────────────────────────────┐ │
│ │ [12:03:01] 开始下载 SteamCMD...                 │ │
│ │ [12:03:04] SteamCMD 解压完成                    │ │
│ │ [12:03:05] Update state (0x61) downloading,     │ │
│ │            progress: 42.34 (10.6GB / 25.1GB)    │ │  ← 实时滚动
│ │ ...                                            │ │
│ └────────────────────────────────────────────────┘ │
│                              [ 取消 ]   [ 完成 ]     │
└────────────────────────────────────────────────────┘
```

组件：

- **当前步骤** `*widget.Label`：三步之间切换文案（"正在下载 SteamCMD..." / "正在下载 ARK
  服务端本体（约 25 GB，视网速可能较久）..." / "正在生成首次配置文件..."）。
- **进度条**：默认 `widget.NewProgressBarInfinite()`（不确定态）。可选增强：解析 SteamCMD 输出里的
  `progress: NN.NN` 行，切成确定态 `widget.NewProgressBar()` 显示百分比（v1 可不做）。
- **日志视图**：`widget.NewMultiLineEntry()`，`entry.Wrapping = fyne.TextWrapWord`，创建后
  `entry.Disable()` 之外用只读处理（或改用 `container.NewVScroll(widget.NewLabel(...))`）；每次追加后
  把光标移到末行让它自动滚到底。放进 `container.NewVScroll` 占满剩余空间。
- **按钮**：运行中显示「取消」（调 `context.CancelFunc`）；成功后「取消」变「完成」（关窗 +
  `g.updateStatus()` 刷新主窗口）；失败显示「重试」+「关闭」，并把错误行以红色 append 到日志。

#### 3.7.3 实时输出的机制

关键是一个把字节流搬上 UI 线程的 `io.Writer`——`installer` 的两个下载函数本来就接收
`...io.Writer` 并往里写进度（SteamCMD 的 PTY 输出、下载百分比都会流过来），`VerifyServerInstallation`
按 §3.2.4 补上 `io.Writer` 后同理：

```go
type guiProgressWriter struct {
    mu     sync.Mutex
    append func(line string) // 内部用 fyne.Do 调度
}

func (w *guiProgressWriter) Write(p []byte) (int, error) {
    text := string(p)
    w.mu.Lock()
    defer w.mu.Unlock()
    for _, line := range strings.Split(strings.ReplaceAll(text, "\r", "\n"), "\n") {
        if s := strings.TrimSpace(line); s != "" {
            w.append(s)
        }
    }
    return len(p), nil
}

// append 的实现（闭包捕获日志 widget 与 scroll 容器）：
appendLine := func(line string) {
    fyne.Do(func() {
        logEntry.SetText(logEntry.Text + "\n" + time.Now().Format("15:04:05") + " " + line)
        logEntry.CursorRow = strings.Count(logEntry.Text, "\n")
        logScroll.ScrollToBottom()
    })
}
```

> **CLAUDE.md GUI 规则**：所有来自 goroutine 的 UI 更新必须包在 `fyne.Do()` 里——`guiProgressWriter`
> 每一行都经 `fyne.Do` 调度，安装 goroutine 本身不直接碰 widget。

#### 3.7.4 执行流

```go
func (g *GUIApp) showSetupProgress() {
    ctx, cancel := context.WithCancel(context.Background())
    // ...建窗口、logEntry、progress、按钮，"取消"按钮 OnTapped = cancel...

    go func() {
        // Windows：无 Preflight / EnsureRuntime（runner 上是 no-op），直接三步。
        err := actions.InstallBaseEnvironment(ctx, writer)
        fyne.Do(func() {
            if err != nil {
                if ctx.Err() != nil {
                    setState("已取消", /* 重试/关闭 */)
                } else {
                    appendLine("错误: " + err.Error())
                    setState("失败", /* 重试/关闭 */)
                }
                return
            }
            setState("完成", /* 完成按钮：关窗 + g.updateStatus() */)
        })
    }()
}
```

- **不需要管理员权限**：三步都只往用户在向导里选定、且已通过 `appconfig.ValidateBaseDir`
  （可写校验）的 `{BaseDir}` 写文件，与 `installService` 需要 admin 不同——双击运行的普通用户
  可直接完成。
- **可取消**：`installer` 三个函数都已接收 `ctx`，`cancel()` 会让 steamcmd 子进程 / 下载 /
  验证进程按 §5.4 的 `KillTree` 语义收干净。
- **幂等**：SteamCMD 已解压、`server-files` 已存在、`Saved/Config/WindowsServer/` 已生成时，
  对应步骤各自快速返回（`VerifyServerInstallation` 的 `configDir` 已存在检查、下载函数的存在性
  短路），所以「修复本体」按钮可安全重复点。

#### 3.7.5 与 CLI `asa-server setup` 的关系

| | 入口 | 适用 |
|---|---|---|
| GUI 引导面板（§3.7） | 双击 exe → 首次启动向导 / 「初始化环境」按钮 | Windows 桌面用户主路径 |
| `asa-server setup`（§3.2） | 命令行 | Windows 无头/脚本化；Linux 唯一路径 |
| `asa-server update`（现有） | 命令行 | 只重装本体，不含 BaseDir 向导 |

三者共用 `actions.InstallBaseEnvironment`（§3.2.4），行为一致。

---

## 4. 分步实施清单

| 步骤 | 内容 | 验收 |
|---|---|---|
| **S1** | `runner.CheckRuntime()`：`runner.go` 导出 + `runner_windows.go` no-op + `runner_linux.go` 真实现（复用 `umuRunPath`/`protonPath`/`prefixDir`），`umuCommandLine` 改为调它去重 | 两平台 `go build`/`go vet`；`runner_linux` 单测：缺 umu-run / 缺 proton / 缺 system.reg 三种输入各返回非 nil 且文案含「setup」；`custom` 模式校验 PROTONPATH/PrefixDir |
| **S2** | `installer.CheckInstalled()` + `InstallStatus`，判据与 `VerifyServerInstallation`/`configDir` 对齐 | 单测：空 BaseDir → 三个 false；构造出对应文件后逐个转 true |
| **S3** | `actions.VerifyEnvironmentReady()` 组合器 | 单测：全缺 → error 含 4 行 + 「asa-server setup」；全有 → nil |
| **S4** | `installer.VerifyServerInstallation` 加 `...io.Writer` 参数（对齐同包两个下载函数，关键节点同时写 w）；`actions.InstallBaseEnvironment(ctx, w)` 抽出三步共享逻辑 | 既有 `ActionUpdate` 调用点适配（传 `os.Stdout`）后 `go build`/`go vet` 过；单测：w 收到三步的阶段行 |
| **S5** | `setup` 跨平台化：删 `GOOS!=linux` 早退、`runtime.GOOS=="linux"` 圈住 Preflight+EnsureRuntime、主体改调 `actions.InstallBaseEnvironment`、收尾提示分平台、更新 `Usage`/注释 | Windows：全新目录 `asa-server setup --non-interactive --basedir X` 跑通 SteamCMD+本体+verify；Linux 行为不变 |
| **S6** | `setup` 的 `Preflight` 改阻断 + `--ignore-preflight` flag；`custom` 模式补 `checkRuntime` | Linux：模拟缺 python3 → `setup` 非零退出且打印 Fix；加 `--ignore-preflight` 继续 |
| **S7** | `asa api`/无参启动 gate（`main.go` + `gatedActionAPI` + `--skip-env-check`）；`RunService`/`program.Start` 只告警不阻断 | 交互式未初始化 → 提示 + 非零退出；`--skip-env-check` 可绕过；`service start` 起的服务只在日志告警、API 正常监听 |
| **S8** | `service install` gate + `--force` flag（`svcmgr` import `actions`） | 未初始化 `service install` → 拒绝 + 指向 setup；`--force` 可装 |
| **S9** | 实例启动路径 `runner.CheckRuntime()` 前置（`server.go`） | Linux 未初始化时 start 实例 → SSE 错误文案含「asa-server setup」，不再是 `umu-run not found` |
| **S10** | Windows GUI 引导面板（§3.7）：`showSetupProgress()` + `guiProgressWriter` + 触发点接线（`applyChosenBaseDir` 之后链入、主窗口提示条 + 「初始化环境」按钮）。`internal/gui` 新增 import `installer`/`actions`（均在 `gui` 之下，无环） | 全新 Windows 机器双击 exe → 选目录 → 面板自动弹出 → 日志区实时滚动 SteamCMD/下载输出 → 完成后主窗口状态刷新；中途「取消」能中断且子进程收干净；已装好时重点「初始化环境」各步快速跳过 |
| **S11**（可选） | GUI 进度条从不确定态升级为百分比（解析 SteamCMD `progress: NN.NN`） | 下载 ARK 本体时进度条随百分比推进 |
| **S12**（可选） | `GET /api/system/preflight` 带就绪位 | 响应含 `runtimeReady` 等四个布尔 |
| **S13** | 文档更新：本文件链入 `docs/README.md`；`LINUX_COMPATIBILITY_PLAN.md §10.5/§10.6` 注明 setup 现在阻断 preflight、GUI 引导面板；`docs/LINUX_DEPLOYMENT.md` 故障排查表加「api 起了实例起不来 → 跑 setup」；`CLAUDE.md` 命令区/GUI 区、`README*.md` 补 Windows 的 `setup` 与 GUI 引导 | 文档索引可跳转 |

**合计约 3–4 人日**（S10 GUI 面板约占 1 人日；不含真实 Linux 主机端到端验证——延续 `LINUX_COMPATIBILITY_PLAN.md` 开头的既有缺口）。

---

## 5. 兼容性与风险

### 5.1 Windows 零回归

- `setup` 在 Windows 上此前是「直接报错退出」，改成可用是**纯增量**，没有任何现有 Windows 行为被改。
- `runner.CheckRuntime()` / `installer.CheckInstalled()` 在 Windows 上分别恒 nil / 只查两个已有路径，
  无新副作用。
- `asa api` gate 在 Windows 上会检查 SteamCMD/本体：若用户从没跑过 `update`/GUI 向导，会被提示先
  `setup`——这与 Windows 现状（实例启动时报 `ArkAscendedServer.exe not found`）相比是**更早、更清楚**的
  同一类提示，且 `--skip-env-check` 保留旧行为。
- GUI 引导面板（§3.7）是**新增窗口**，现有面板（服务管理/资源监控/API 服务器/实例列表）行为不动；
  `installer.VerifyServerInstallation` 加的是**变参** `...io.Writer`，旧调用点 `ActionUpdate` 不传即可，
  签名兼容。
- GUI 三步安装**不需要管理员权限**（只写用户选定且校验过可写的 `{BaseDir}`），双击运行的普通用户
  可直接完成——与需要 admin 的 `installService` 是两条独立路径。

### 5.2 逃生舱清单（所有硬阻断都可绕过）

| 阻断点 | 逃生舱 |
|---|---|
| `setup` Preflight 不通过 | `--ignore-preflight` |
| `asa api` / 无参启动 环境未就绪 | `--skip-env-check` |
| `service install` 环境未就绪 | `--force` |
| 服务运行模式 | 不阻断，仅日志告警 |

### 5.3 已知不覆盖

- 不自动安装 OS 级依赖（32 位 glibc / python3 等）——设计如此，交给用户 + 文档。
- 不在实例启动请求里同步下载运行时——避免把几百 MB 下载塞进一次 start。
- 真实 Linux 主机端到端验证仍待补（与 `LINUX_COMPATIBILITY_PLAN.md` 一致）。
- GUI 引导面板只做本体安装（SteamCMD + ARK 本体 + verify），不做「注册服务 / 装本地 CA / 建管理员账号」
  ——那三项在现有 GUI 服务管理面板与 CLI（`cert install` / `user add`）里已有等价功能，不重复造引导页
  （与 `LINUX_COMPATIBILITY_PLAN.md §10.1` 对 Fyne 向导「范围只到选目录」的既有取舍一致，这里只多加
  一步本体安装）。
- GUI 进度条百分比（S11）、`/api/system/preflight` 就绪位（S12）列为可选增强，不阻塞主线。
- Fyne 日志 widget 用 `MultiLineEntry` 追加大量文本时的性能未压测；SteamCMD 输出量可控（几百行），
  必要时截断保留末 N 行。
