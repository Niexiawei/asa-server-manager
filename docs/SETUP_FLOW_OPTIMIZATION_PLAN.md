# Setup 流程优化方案

> 目标：让「基础环境未初始化」这件事在**用户第一眼就能看到**，而不是等到启动某个实例时在
> `runner.Run` 深处报一句 `umu-run not found`。同时把 `asa-server setup` 从「仅 Linux」扩成
> 两平台通用的环境初始化入口；Windows 用户以双击运行为主，因此在 Fyne GUI 里再实现一份
> 带**实时进度输出**的引导流程（§3.7）。
>
> 关联文档：`docs/LINUX_COMPATIBILITY_PLAN.md`（§4.2 依赖自检、§5.1 runner、§10 首次运行数据目录、
> §10.6 Linux 只有 CLI、§10.7 `asa-server api` 一等入口三条不变量）、`docs/LINUX_DEPLOYMENT.md`。
>
> 状态：**S1–S13 已实施**。两平台 `go build` / `go vet` 通过；新增单测
> `internal/installer/status_test.go`、`internal/actions/environment_test.go`、
> `internal/runner/runner_linux_test.go`（linux 侧，本机 Windows 只做交叉编译校验）。
> 真实 Linux 主机端到端仍待补（延续 `LINUX_COMPATIBILITY_PLAN.md` 的既有缺口）。

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

## 4. 分步实施清单（S1–S13 已实施）

| 步骤 | 内容 | 落点 |
|---|---|---|
| **S1** ✅ | `runner.CheckRuntime()`：`runner.go` 导出 + `runner_windows.go` no-op + `runner_linux.go` 真实现（复用 `umuRunPath`/`protonPath`/`prefixDir`），`umuCommandLine` 改为先调它、只额外留 per-`PrefixKey` 前缀检查 | `internal/runner/{runner,runner_windows,runner_linux}.go` |
| **S2** ✅ | `installer.CheckInstalled()` + `InstallStatus`（`SteamCmdReady`/`ServerBinaryReady`/`ServerConfigReady`/`Ready()`），判据路径与 `VerifyServerInstallation` 对齐 | `internal/installer/status.go` + `status_test.go` |
| **S3** ✅ | `actions.VerifyEnvironmentReady()` 组合器：运行时 + SteamCMD + 本体 + 首次配置，返回多行、末尾「请运行：asa-server setup」 | `internal/actions/environment.go` + `environment_test.go` |
| **S4** ✅ | `installer.VerifyServerInstallation` 加 `...io.Writer`（新增 `emit` 把关键 `logger.Info` 同时写 w）；`actions.InstallBaseEnvironment(ctx, w)` 抽出三步；`ActionUpdate` 调用点传 `os.Stdout` | `internal/installer/installer.go`、`internal/actions/{environment,actions}.go` |
| **S5** ✅ | `setup` 跨平台化：删 `GOOS!=linux` 早退、`runtime.GOOS=="linux"` 圈住 Preflight+EnsureRuntime、主体改调 `InstallBaseEnvironment`、`printPostSetupTips` 分平台、`Usage`/注释更新、新增 `--ignore-preflight` | `internal/actions/setup.go` |
| **S6** ✅ | `runLinuxPreflight`：Preflight 不通过默认 `return` error（打印每条 Fix），`--ignore-preflight` 逃生舱；`ensureRuntime` 的 `custom` 分支改为 `return checkRuntime()` | `internal/actions/setup.go`、`internal/runner/umu_linux.go` |
| **S7** ✅ | `api` 子命令 `Action: gatedActionAPI` + `--skip-env-check`；无参启动在 `main_linux.go/runDefaultAction` 里 gate；`svcmgr.program.Start` 里 `VerifyEnvironmentReady` 只 `logger.WithConsole().Warnf` 不阻断 | `main.go`、`main_linux.go`、`internal/svcmgr/service.go` |
| **S8** ✅ | `ActionServiceInstall` 未就绪则 `return` error 指向 setup，`--force` 逃生舱；`service install` 子命令加 `--force` flag | `internal/svcmgr/service.go`、`main.go` |
| **S9** ✅ | `startServerInternal` 在 `runner.Run` 前 `runner.CheckRuntime()`，失败 `无法启动实例：<人话>` | `internal/instance/server.go` |
| **S10** ✅ | Windows GUI：`showSetupProgress()`（独立窗口 + 步骤标签 + 进度条 + 只读日志 + 取消/关闭）、`guiProgressWriter`、`refreshEnvBanner()`、主窗口黄条 + 「初始化环境」按钮、`applyChosenBaseDir` 之后 `showConfirm` 链入 | `internal/gui/setup_progress.go`、`internal/gui/gui.go` |
| **S11** ✅ | `guiProgressWriter` 解析 SteamCMD `progress: NN.NN`，命中即把 `ProgressBarInfinite` 换成确定态 `ProgressBar` 百分比 | `internal/gui/setup_progress.go`（`steamProgressRe`） |
| **S12** ✅ | `GET /api/system/preflight` 增加 `runtimeReady` / `runtimeMessage` / `steamCmdReady` / `serverBinaryReady` / `serverConfigReady` / `environmentReady` | `internal/webapi/systemapi/systemapi.go` |
| **S13** ✅ | `docs/README.md` 文档索引收录本文件；本文件状态更新 | `docs/README.md`、本文件 |

**验证**：`go build ./...` / `go vet ./...` 两平台通过；`CGO_ENABLED=0 GOOS=linux` 同上。
新单测全绿（linux 侧 `runner_linux_test.go` 在 Windows 本机只做交叉编译校验）。`internal/instance`
仅剩 2 个 pre-existing 环境耦合失败（硬编码作者本机 `E:\asa_server_data`，CLAUDE.md 已注明非回归）。

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

---
---

# Part 2：`config init` —— 先配置、后初始化

> 状态：**C0–C7 已实施**（2026-09-29），C8（文档）待实施；GUI 向导的完整手测清单见 §P2-9 末尾。
> 实施记录与设计偏离见 §P2-7、§P2-8、§P2-9。
>
> 目标：把「生成 config.yaml」从「任意命令启动时的副作用」里拆出来，成为一个显式步骤，
> 让用户在 `setup` 下载几百 MB / 几十 GB **之前**就能把下载代理、端口、prefix 模式、降权用户
> 这些配置改好；顺带修掉 config.yaml 在部分环境下打开乱码的问题。Windows GUI 同步改成
> 「选配置位置 → 生成配置 → 检查/编辑 → 初始化环境」的引导。

## P2-1. 现状与问题

### P2-1.1 配置文件是启动副作用，时机太晚也太早

`main.go` 在构建 CLI **之前**就执行 `loadAppConfig()` → `appconfig.Load()`；三级查找
（`ASA_CFG` > exe 同级 > 系统固定目录）都没有时，`Load` 在 exe 同级 `writeDefaultConfig`
写出模板，`basedir: ""`。紧接着 `cfgpkg.EnsureDirectories(cfgpkg.BaseDir)` 在那个目录下建
`backups/instances/logs/server-files/steamcmd`。于是：

| 问题 | 表现 |
|---|---|
| **任何命令都会生成配置 + 建目录** | 实测 WSL：`ASA_CFG=/tmp/cfgtest asa-server --help` 之后 `/tmp/cfgtest` 下出现 `config.yaml` 与 5 个数据子目录。看一眼帮助就污染了目录 |
| **`setup` 里没有「改配置」的窗口** | `setup` 的 `resolveSetupBaseDir` 只问 basedir，`WriteInitialConfig` 写回后**立刻**进入 `EnsureRuntime`（umu + GE-Proton ~450MB）与 `InstallBaseEnvironment`（SteamCMD + ~25GB 本体）。`download.github_proxy` / `download.http_proxy` / `linux.prefix_mode` / `linux.umu_runtime_*` 这些**影响下载与安装本身**的配置，用户只能等整套跑完（或 Ctrl+C 中断）才有机会改 |
| **改了也不生效** | 即使用户在 `setup` 途中改了文件：`download.Configure` 只在 `main.go` 的 `applyAppConfig` 里调过一次，`setup.go` 与 `gui.go` 的 `applyChosenBaseDir` 重新 `Load` 之后都**只重做 `runner.Configure`、不重做 `download.Configure`**——代理改了照样直连 |
| **`runner.Configure` 字段映射抄了三份** | `main.go:283`、`internal/actions/setup.go:84`、`internal/gui/gui.go:437` 三处各自逐字段拼 `runner.Config`，`docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md §11` 已经因为漏字段出过一次事故 |
| `WriteInitialConfig` 目标写死 exe 同级 | 注释说「只在 `WasConfigAutoGenerated()` 时调用」，但 `ASA_CFG` 指向别处时 `Load` 生成的文件在 `ASA_CFG` 目录，`WriteInitialConfig` 却去读 exe 同级那份——路径不一致 |
| 报错路径不对 | `main.go:250` 提示「请修正 `{BaseDir}/config.yaml`」，而 basedir 字段非空时配置文件并不在 BaseDir 下 |

### P2-1.2 乱码：文件本身是对的，是「打开它的那一端」按非 UTF-8 解码

排查结论（2026-09-29 实测）：

- `internal/appconfig/template.go` 源文件是 UTF-8、LF、无 BOM（`git ls-files --eol` 为 `i/lf w/lf`）；
  模板里唯一的 `%` 就是 `%s` 占位，不存在 `Sprintf` 把 `%` 渲染成 `%!(NOVERB)` 的问题。
- WSL（`LANG=C.UTF-8`）里新生成的 `config.yaml`：`file` 报 `Unicode text, UTF-8 text`，`head` 显示正常。

即**写出去的字节没有问题**，乱码出在读取端把无 BOM 的 UTF-8 当成了别的编码：

| 平台 | 典型乱码场景 | 根因 |
|---|---|---|
| Linux | 最小化镜像 / 容器 / 部分 VPS 的 `LANG` 未设或为 `C`/`POSIX` | vim 此时 `encoding=latin1`、nano/less 按 ASCII 显示，中文注释变成 `<e4><bd>…` 或乱字符 |
| Linux | Xshell / SecureCRT / PuTTY 会话字符集设为 GBK（国内常见默认） | 终端把 UTF-8 字节按 GBK 渲染，`cat` 都是乱的 |
| Linux → Windows | 用 WinSCP / FinalShell 拉回本机，用默认 ANSI 的编辑器打开 | 按 CP936 解无 BOM 的 UTF-8 |
| Windows | Windows Server 2016/2019 自带记事本、部分老编辑器 | 对无 BOM 文件猜编码，容易猜成 ANSI(CP936)；Server 2016 的记事本还**不认 LF 换行**，整份文件挤成一行 |

所以修复不是改写入的字节，而是让文件**在这些读取端也不乱**：Windows 写 BOM（所有 Windows
编辑器都认 BOM）+ CRLF；Linux 在非 UTF-8 locale 下改用纯 ASCII 的英文注释模板（BOM 在
Linux 终端里无济于事——终端按什么解码与文件头无关，且 BOM 会干扰 `grep '^basedir'` 这类脚本）。

**报告者确认（2026-09-29）：`cat` 与 vim 都乱码。** 这把范围缩小到两种可能：

- `cat` 不做任何转码，只把字节交给终端，终端按**自己的**字符集渲染。只是服务器 `LANG=C` 的话，
  `cat` 显示正常、只有 vim 乱；两者都乱，说明**终端 / SSH 客户端按非 UTF-8（国内多为 GBK）解码**
  ——上表第 2 行；
- 或者那台机器上的文件**字节真的坏了**（与本机 WSL 复现结果矛盾，但没在现场看过字节之前不能排除）。

区分两者只需在出问题的机器上跑一次（C0 之前完成，结论回填本节）：

```bash
echo "LANG=$LANG LC_ALL=$LC_ALL"
file config.yaml                       # 期望 "UTF-8 text"
head -c 64 config.yaml | od -An -tx1   # 期望第二行起出现 e5 ba 94 e7 94 a8（「应用」）
printf '\xe4\xb8\xad\xe6\x96\x87\n'    # 打印 UTF-8 的「中文」两个字
asa-server config path 2>&1 | head -3  # 程序自己的中文输出（C5 之前可用 asa-server setup --help 代替）
```

| 观察 | 结论 | 处理 |
|---|---|---|
| `file` 报 UTF-8、`od` 字节正确，但 `printf` 那行也乱 | 终端 / SSH 客户端不是 UTF-8 | 本 Part 的方案；并且**程序自己的中文控制台输出（setup 进度、报错）在这个终端里同样是乱的**——换英文模板只救得了配置文件，根治是把客户端字符集改成 UTF-8（Xshell：会话属性 → 终端 → 编码；PuTTY：Window → Translation；FinalShell / MobaXterm 同理） |
| `printf` 那行正常，`file` / `od` 显示字节不对 | 文件确实被写坏或被改写过 | **不是本方案覆盖的问题**，单独排查写入路径（谁改写过这份文件：SFTP 客户端的文本模式转码、Windows 编辑器另存为 ANSI 后上传等） |

**现场诊断结果（2026-09-29，已结案）：终端字符集问题，文件无误。**

```
LANG=en_US.UTF-8 LC_ALL=
config.yaml: UTF-8 Unicode text, with CRLF line terminators
 23 20 41 53 41 20 53 65 72 76 65 72 20 4d 61 6e     # "# ASA Server Man"
 61 67 65 72 20 e5 ba 94 e7 94 a8 e9 85 8d e7 bd     # "ager 应用配置"
 ...
```

字节是正确的 UTF-8，服务器 locale 也是 UTF-8；SSH 客户端会话编码为 GBK。报告者把客户端切到 UTF-8
后 `cat` / vim 均恢复正常。

**「写入时能否指定按 UTF-8 读取」——不能，文件无法左右终端的解码方式：**

| 手段 | 作用范围 | 对 GBK 客户端有没有用 |
|---|---|---|
| UTF-8 BOM | 编辑器据此识别文件编码 | 无。`cat` 原样输出字节，终端不认 BOM；vim 认出文件是 UTF-8 后仍按服务器 locale（UTF-8）往终端写，客户端照样按 GBK 渲染 |
| vim modeline `# vim: set fileencoding=utf-8` / Emacs `-*- coding: utf-8 -*-` | 编辑器读写文件时的编码 | 无，同上，管不到终端 |
| 服务器 `LANG` / `LC_ALL` | 程序往终端写什么编码 | 无——服务器本来就是 UTF-8，错在客户端怎么解 |
| **纯 ASCII 内容** | 所有终端的公共子集 | **有，唯一有效的服务端手段**——即 P2-3.1(c) 的英文模板 |

所以本 Part 对乱码的定位是**兜底而非根治**：根治永远是客户端改 UTF-8（同一个终端里程序自身的中文
控制台输出也一样乱，换模板救不了）。交互式双语提问（P2-3.1(c) 第 2 条）保留，价值在于让用户**在第一次
看到乱码的那一刻就知道原因和改法**，而不是去怀疑程序写坏了文件。

> 未采纳的自动探测：先输出一个 UTF-8 宽字符再用 `ESC[6n` 查询光标列，UTF-8 终端前进 2 列、GBK 终端
> 把 3 个字节解成 1.5 个字符列数不同，据此可自动判断。在 tmux/screen、串口、非 VT 终端、stdin 被重定向
> 时都不可靠，而问一句人的成本极低，不值得。

> 本机 WSL 部署那份 `/opt/asa-server/config.yaml` 是 CRLF 换行，说明它是在 Windows 上生成/编辑后传上去的，
> 不是 Linux 上 `Load` 写出来的——「传输/编辑环节被转码」这一支要认真查，不能只看生成逻辑。

## P2-2. 目标

1. **新增 `asa-server config init`**：只生成 config.yaml（可顺带填好 basedir），**不建数据目录、
   不下载任何东西**。标准部署流程变为 `config init` → 编辑 → `setup`。
2. **`config` / `--help` / `--version` 不再有副作用**：不生成配置、不建目录。
3. **`setup` 在配置缺失时给出编辑窗口**：交互模式生成配置后停下来让用户改，回车后**重新加载
   并重新应用**（含 `download.Configure`），再开始下载。
4. **乱码**：Windows 写 UTF-8 BOM + CRLF；Linux 按 locale 选中文 / 英文（纯 ASCII）模板，
   `--lang` 可强制。
5. **Windows GUI**：首次启动向导改为「配置位置 → 数据目录 → 生成并检查配置 → 初始化环境」。
6. **收敛重复**：「把 appconfig 应用到各运行时包」抽成一个函数，三处调用点共用。
7. **兼容**：`api` / 无参（Linux）/ 服务模式在配置缺失时**仍自动生成**（`LINUX_COMPATIBILITY_PLAN.md
   §10.7` 不变量 3：`api` 不得要求预先存在的 config.yaml）；已有 config.yaml 的部署零感知。

## P2-3. 设计

### P2-3.1 `appconfig`：生成与加载解耦

**(a) `Load` 增加选项，不生成文件也能加载**

```go
type LoadOption func(*loadOptions)

// WithoutAutoGenerate：三级查找都没有 config.yaml 时，只用内存默认值，不写文件。
// `config *`、`setup`、`--help`、Windows GUI 用它；api / 服务模式保持旧行为。
func WithoutAutoGenerate() LoadOption

func Load(opts ...LoadOption) (string, error)
```

- 变参保持现有调用点源码兼容。
- 新增 `ConfigPath() string`：最近一次 `Load` **选中（或本应生成）**的 config.yaml 绝对路径。
  `main.go:250` 等提示改用它。
- 新增 `ConfigMissing() bool`：最近一次 `Load` 时三级都没有文件。`WasConfigAutoGenerated()`
  语义不变（= 缺失 **且** 这次写了），旧调用点逐个换成 `ConfigMissing()` 后删除——
  GUI/setup 关心的是「有没有配置」，不是「是不是我刚写的」。

**(b) 显式初始化入口 `InitConfig`**

```go
type InitOptions struct {
    Dir     string // 写到哪个目录；空 = DefaultInitDir()
    BaseDir string // 写进 basedir 字段；空 = 留空（= 与配置同目录）
    Lang    string // "zh" / "en" / ""(= DefaultTemplateLang())
    Force   bool   // 目标已存在时先备份为 config.yaml.bak-<时间戳> 再覆盖
}

// InitConfig 渲染模板并原子写入（同目录临时文件 + rename），返回写入的路径。
// 目标已存在且 !Force → 返回 ErrConfigExists（携带路径），不动文件。
func InitConfig(o InitOptions) (string, error)

// DefaultInitDir = ASA_CFG（非空时）> exe 同级。与 Load 的查找顺序对齐，
// 保证「init 写到哪」就是「下次 Load 会读哪」。
func DefaultInitDir() (string, error)
```

- `basedir` 直接在渲染时填进模板（`strconv.Quote`，同现有转义约定），**不再**走
  「先写空模板再字符串替换」。`WriteInitialConfig` 与 `basedirPlaceholder` 随之删除
  （调用点只有 `setup.go` 与 `gui.go`，都换成 `InitConfig`）。
- 目标目录用 `os.MkdirAll` 建（只建这一个目录，不建数据子目录）。
- `writeDefaultConfig`（`Load` 自动生成那条路径）改为调用同一个渲染 + 写入函数，保证两条路径
  产出完全一致的文件。

**(c) 模板渲染与编码**

```go
func renderTemplate(lang, baseDir string) []byte
```

- 模板拆成 `template_zh.go` / `template_en.go` 两个常量，平台差异块（`trust_local_ca`）
  两份各自一套。英文版逐条翻译中文版注释，**字段与默认值必须完全一致**。
- 语言选择，按可靠程度排序：
  1. `--lang zh|en` 显式指定；
  2. **交互模式问人**（`config init` 与 `setup` 缺配置分支）：终端的解码方式在服务器一侧**探测不到**
     ——服务器 `LANG=C.UTF-8`、客户端却是 GBK 正是「`cat` 也乱」的情形，看 locale 会误判为 zh。
     唯一可靠的判据是让用户看一眼：
     ```
     Config file comments language / 配置文件注释语言
       Can you read this line correctly?  ->  中文显示测试：数据目录、下载代理
       [1] Yes, Chinese comments / 能正常显示，用中文注释
       [2] No (garbled), English comments / 显示乱码，用英文注释
     Choose [1/2] (default 1 if your LANG is UTF-8, otherwise 2):
     ```
     提示与选项必须**中英双语、英文在前**：选 2 的用户恰恰看不懂中文部分。选 2 时追加一段
     纯 ASCII 的说明：终端/SSH 客户端字符集不是 UTF-8，程序其余中文输出也会乱，建议改成 UTF-8；
  3. 非交互：`DefaultTemplateLang()`——Windows 恒 `zh`；Linux 取 `LC_ALL` > `LC_CTYPE` > `LANG`
     第一个非空值，含 `UTF-8`/`utf8`（大小写不敏感）→ `zh`，否则（含未设置、`C`、`POSIX`）→ `en`。
     这只是尽力而为（上面那种客户端 GBK 的情况它判不出来），`LINUX_DEPLOYMENT.md` 的脚本化部署示例
     里写明 `--lang`。
- `api` / 服务模式自动生成（P2-3.2 表最后一行）没人可问，走第 3 条。
- 字节层：Windows 输出 `EF BB BF` + CRLF；Linux 输出无 BOM + LF。
- 待验证点（C0，实施第一步）：viper 底层 `go.yaml.in/yaml/v3` 能识别并跳过 UTF-8 BOM、
  接受 CRLF；`fileOnlyBaseDir` 走同一条解析路径。单测不通过则 Windows 只做 CRLF、BOM 方案作废。

**(d) 只读校验 `CheckFile`**

```go
// CheckFile 解析并 Validate 指定的 config.yaml，不改 current、不写任何文件。
// 给 `config validate` 与 GUI「重新校验」用。环境变量覆盖照常叠加（与 Load 一致），
// 返回的 error 直接可展示。
func CheckFile(path string) (*Config, error)
```

`Load` 内部的「viper 读文件 → Unmarshal → fileOnlyBaseDir → Validate」抽成它与 `Load` 共用。

### P2-3.2 `main.go`：按命令决定引导方式

`loadAppConfig()` 与 `EnsureDirectories` 发生在 CLI 解析之前，所以要先「偷看」本次要跑的命令：

```go
// bootstrapFor 决定启动引导的副作用。只识别顶层命令名；全局 flag 里取值的那几个
// （--api-port/--port、--cert-file、--key-file、--tls-domains、--trusted-proxies）
// 跳过其后的值，其余 -x/--x 当布尔 flag 跳过。
type bootstrap struct {
    autoGenerate bool // Load 是否在缺失时写模板
    ensureDirs   bool // 是否 EnsureDirectories + InitLoggerWithBaseDir
}

func bootstrapFor(args []string) bootstrap
```

| 命令 | autoGenerate | ensureDirs | 说明 |
|---|---|---|---|
| `config …` / `help` / `-h` / `--help` / `-v` / `--version` | ✗ | ✗ | 纯只读或只写 config.yaml；日志走 `pkg/logger` 的纯控制台兜底 |
| `setup` | ✗ | ✓（BaseDir 定下来之后才建，见 P2-3.4） | 配置缺失由 setup 自己处理 |
| Windows 无参（GUI） | ✗ | ✓ | 配置缺失由 GUI 向导处理（P2-3.5） |
| `api`、Linux 无参、服务模式、其余命令 | ✓ | ✓ | **旧行为不变** |

`bootstrapFor` 是纯函数，放 main 包的 `bootstrap.go`，配表驱动单测
（`--port 1 config init`、`--tls config init`、`setup --basedir x`、空参数等）。

> 为什么不把配置加载挪进 cli 的 `Before` 钩子：全局 flag 的 `Value` 取自配置，才让
> 「flag > 文件 > 默认值」由 cli 库天然保证（见 `main.go` 注释）；挪进 `Before` 要重做这套
> 优先级合并，改动面远大于一个偷看函数。

### P2-3.3 `asa-server config` 子命令组

新文件 `internal/actions/configcmd.go`，`ConfigCommand()` 注册进 `commonCommands`：

```
asa-server config init [--dir DIR] [--basedir DIR] [--lang zh|en] [--set-env] [--force] [--non-interactive]
asa-server config path
asa-server config validate [--file PATH]
```

**`config init`**

1. 目标目录：`--dir` > `DefaultInitDir()`。
2. 若**任一级**已有生效中的配置（`appconfig.ConfigMissing()==false`）：
   - 目标就是那份文件 → 无 `--force` 时报「已存在：<路径>，如需重新生成加 --force（旧文件会备份）」；
   - 目标是另一个目录 → 提示「新文件会遮蔽 <现有路径>」（exe 同级优先于系统目录），交互模式要求确认，
     非交互模式需 `--force`。
3. basedir：`--basedir` > 交互提示（回车 = 留空，即与配置文件同目录）> 非交互留空。非空时跑
   `appconfig.ValidateBaseDir`（可写、非网络盘、≥30GB；已有 config.yaml 视为接管跳过空间检查）。
   **只校验，不建数据子目录**——`ValidateBaseDir` 会 `MkdirAll` 这个目录本身，这可以接受
   （用户明确指定了它）。
4. `appconfig.InitConfig(...)` 写入。
   `--set-env`：写入成功后持久化 `ASA_CFG=<目标目录>`（Windows 写用户级环境变量，Linux 只打印提示，
   见 P2-3.5.1）。`--dir` 指向三级查找之外的目录而**没有** `--set-env` 时，输出里明确警告
   「下次启动找不到这份配置，请加 --set-env 或自行设置 ASA_CFG」。
5. 输出：
   ```
   已生成配置文件：/opt/asa-server/config.yaml（中文注释，UTF-8）
   数据目录：/data/asa

   开始安装前通常需要检查的配置：
     download.github_proxy / download.http_proxy   国内网络下载 umu / GE-Proton / SteamCMD 的代理
     server.port                                    管理面板端口（默认 19193）
     linux.prefix_mode                              多实例 Wine prefix 隔离方式（shared / per-instance / overlay）
     linux.umu_runtime_user                         降权运行游戏进程的系统用户
   改好后执行：asa-server config validate && asa-server setup
   ```
   （Windows 版去掉 `linux.*` 两行。）语言按 P2-3.1(c) 的顺序决定：`--lang` > 交互式双语提问 >
   `DefaultTemplateLang()`。选了英文模板时，这段输出**整段用英文**（用户的终端显示不了中文），并提示
   把终端 / SSH 客户端字符集改成 UTF-8，之后可 `config init --force --lang zh` 重新生成。

**`config path`**：打印 `ConfigPath()`、是否存在、解析出的 BaseDir 及其来源（字段 / `ASA_BASEDIR` / 配置同目录）。
排障用，一眼看清三级查找与 basedir 优先级到底落在哪。

**`config validate`**：`--file` > `ConfigPath()`，调 `appconfig.CheckFile`；通过打印「配置有效」+
BaseDir，失败打印错误并非零退出。文件不存在时提示先 `config init`。

### P2-3.4 `setup` 接入

`resolveSetupBaseDir` 改为按 `ConfigMissing()` 分支：

| 情况 | 行为 |
|---|---|
| 配置已存在（`config init` 过、或旧部署） | 沿用，同今天（「检测到已有配置，沿用当前数据目录」），并打印配置文件路径 |
| 缺失 + 交互 | 问 basedir（同今天）→ `InitConfig` → 打印路径与 P2-3.3 那张「通常需要检查的配置」表 → **「现在可以编辑该文件，改好后按回车继续（直接回车 = 使用默认值）」** → 重新加载（见下） |
| 缺失 + 非交互 | 有 `--basedir`：`InitConfig` 后直接继续（不停顿）。无 `--basedir`：报错改为「非交互模式下请先 `asa-server config init` 并按需编辑，或通过 --basedir 指定数据目录」 |

重新加载 = 新的 `bootstrap.Reload()`（P2-3.6）：`Load(WithoutAutoGenerate())` → `EnsureDirectories`
→ `InitLoggerWithBaseDir` → `Apply`（`download.Configure` + `runner.Configure`）。
校验失败时交互模式打印错误并**再次等待回车**（给用户修正的机会），非交互直接返回错误。

`setup` 顶部的 `runLinuxPreflight` 仍在最前面（不依赖配置中的 BaseDir），但 `linux.umu_python_bin`
等字段会影响 preflight 结果——配置缺失、用户在停顿处改了这类字段时，Reload 之后**再跑一次**
`runLinuxPreflight`，避免「自检用默认值通过、安装用新值失败」。

### P2-3.5 Windows GUI 首次启动向导

触发条件由 `WasConfigAutoGenerated()` 改为 `ConfigMissing()`（GUI 以 `WithoutAutoGenerate` 加载，
缺失时文件确实不存在）。新文件 `internal/gui/config_wizard.go`，独立 `fyne.Window`
（与 §3.7 引导面板一致的理由：多步、需要可调整大小），三页：

**第 1 页：配置文件位置**

```
┌─ 首次设置 · 1/3 配置文件位置 ─────────────────────────┐
│ 配置文件 config.yaml 保存在：                          │
│ (•) 程序目录  D:\ASA\                  （推荐，绿色部署）│
│ ( ) 系统目录  C:\ProgramData\ASAServerManager\         │
│     程序放在 Program Files 等无写权限的目录时选这个     │
│ ( ) 自定义目录 [ E:\ASA-Config\            ] [浏览…]   │
│     会为当前用户设置环境变量 ASA_CFG 指向该目录         │
│                                    [ 取消 ]  [ 下一步 ] │
└───────────────────────────────────────────────────────┘
```

- 程序目录不可写（探测写一个临时文件）时禁用第一项并注明原因，默认选中第二项。
- 「自定义目录」就是**持久化设置 `ASA_CFG`**：三级查找的第一级本来就是它，不引入新的查找机制。
  设置方式、服务侧怎么看见它、与已有 `ASA_CFG` 的关系见 P2-3.5.1。
- 「浏览…」调**系统原生目录选择器**（P2-3.5.2），不用 Fyne 自绘的 `dialog.NewFolderOpen`；
  输入框也允许直接粘贴路径。
- 自定义目录的校验：可写（探测文件）；**拒绝网络盘**（`fsutil.IsNetworkDrive`）——映射盘符是按
  登录会话挂的，LocalSystem 身份的服务根本看不到它，GUI 里能用、装成服务就找不到配置。
- 选中的目录里**已有 config.yaml** → 视为「接管已有配置」：不生成、跳过第 2 页，直接进第 3 页检查
  （重装系统 / 挪了 exe 位置后指回原配置的场景）。程序目录、系统目录两项同理。

**第 2 页：数据目录** —— 复用现有 `showBaseDirPicker` 的校验逻辑（`ValidateBaseDir`，失败重选），
选择器同样换成原生目录选择器；默认值为第 1 页选定的目录；显示「ARK 本体约 25GB，建议预留 30GB」。
点「生成配置」→ `appconfig.InitConfig{Dir, BaseDir, Lang:"zh"}`；第 1 页选的是自定义目录时，**写配置
成功之后**再持久化 `ASA_CFG`（顺序反过来的话，写失败会留下一个指向空目录的环境变量，下次启动
每次都弹向导）。

**第 3 页：检查配置**

```
┌─ 首次设置 · 3/3 检查配置 ─────────────────────────────┐
│ ✓ 已生成 D:\ASA\config.yaml                           │
│ 数据目录：E:\ASA-Data                                 │
│                                                       │
│ 开始下载前建议确认：                                    │
│  · download.github_proxy / http_proxy  下载代理        │
│  · server.port                         面板端口        │
│  · server.tls / auth                   HTTPS 与登录鉴权 │
│                                                       │
│ [ 用记事本打开 ]  [ 打开所在文件夹 ]  [ 重新校验 ]      │
│ 校验结果：配置有效                                      │
│                                                       │
│               [ 稍后再初始化 ]  [ 保存并初始化环境 ▶ ]  │
└───────────────────────────────────────────────────────┘
```

- 「用记事本打开」：`exec.Command("notepad.exe", path).Start()`——`.yaml` 在多数 Windows 上没有默认关联，
  走 ShellExecute 会弹「选择打开方式」；记事本所有版本都有，配合 BOM + CRLF 不会乱码/挤行。
- 「打开所在文件夹」：`explorer.exe /select,<path>`。
- 「重新校验」：`appconfig.CheckFile(path)`，结果行绿/红显示。
- 「保存并初始化环境」：`bootstrap.Reload()`；失败把错误显示在结果行、停留本页；成功 → 关向导 →
  `g.refreshEnvBanner()` → `g.showSetupProgress()`（§3.7 面板，此时 SteamCMD 下载已经吃到新代理）。
- 「稍后再初始化」：同样先 `Reload()`（配置已生成，要让本进程用上），关向导，主窗口黄条仍在。

**取消 / 关闭窗口**（第 1、2 页）：保留 §10.4 要求的退路——以默认值在程序目录生成配置
（`InitConfig{Dir: exeDir}`，等价于旧版 `Load` 的自动生成），然后 `Reload()`。否则下次启动还会弹向导，
而旧行为是「取消一次就不再打扰」。程序目录不可写时退路改为 ProgramData。

现有 `applyChosenBaseDir` 删除，改调 `bootstrap.Reload()`；主窗口「初始化环境」按钮不变。

> CLAUDE.md GUI 规则：`Reload()` 可能较慢（`EnsureDirectories` 在机械盘上），放 goroutine 执行、
> 结果经 `fyne.Do` 回填。第 3 页用 `container.NewVScroll` 包裹，不用 `GridWrap`/`HBox` 做左右布局。

#### P2-3.5.1 自定义目录 = 持久化 `ASA_CFG`

**写到哪一级**：当前用户的环境变量（`HKCU\Environment`，`REG_SZ`），**不写**系统级
（`HKLM\...\Session Manager\Environment`）。

- 用户级不需要管理员权限，双击运行的普通用户就能完成向导（与 §3.7「三步安装不需要管理员」同一立场）。
- 同名变量用户级覆盖系统级（`PATH` 之外都是这个规则），所以写用户级足以让本用户后续启动生效。
- 代价：同机其他 Windows 账户看不到；LocalSystem 身份的服务也看不到——后者由下面「服务侧」解决。

**生效**：

1. 写注册表后 `SendMessageTimeoutW(HWND_BROADCAST, WM_SETTINGCHANGE, 0, "Environment", SMTO_ABORTIFHUNG, 5000)`，
   让资源管理器刷新环境块——此后从资源管理器双击启动的新进程就带上 `ASA_CFG`，**不用注销**。
2. 本进程 `os.Setenv("ASA_CFG", dir)`，随后的 `bootstrap.Reload()` → `Load` 第一级就命中它。
3. 已经开着的终端窗口不会刷新（Windows 的通用行为），`config init` 的输出与向导第 3 页都注明这一点。

**服务侧**：`svcmgr.newServiceConfig` 在安装服务时，若当前进程 `ASA_CFG` 非空，就写进
`service.Config.EnvVars["ASA_CFG"]`：

- Windows：kardianos v1.3.0 的 `setEnvironmentVariablesInRegistry` 把 `EnvVars` 写进服务注册表项的
  `Environment`（`REG_MULTI_SZ`），SCM 启动服务时注入——LocalSystem 看不到 HKCU 也没关系。
  `service_windows.go` 的 `configurePlatform` 目前是空实现，这里是它第一次有内容。
- Linux：`service_linux.go` 目前 `cfg.EnvVars = map[string]string{"HOME": …}` **整体赋值**，改成在同一个
  map 里追加 `ASA_CFG`。这顺带修掉一个既有缺口：Linux 用户在 shell 里 `export ASA_CFG` 后
  `service install`，生成的 unit 里并没有它，服务读的是另一份配置。
- 装服务之后再在 GUI 里改配置位置：服务里烤进去的仍是旧值。第 1 页检测到服务已安装且
  `ASA_CFG` 将要变化时提示「需要重新安装服务才能让服务使用新位置」，重装本身走现有需管理员的路径，
  不在向导里自动做。

**与已存在 `ASA_CFG` 的关系**（第 1 页打开时判断）：

| 现状 | 第 1 页行为 |
|---|---|
| 没有 `ASA_CFG` | 三项可选，默认程序目录（不可写时默认系统目录） |
| 用户级 `HKCU` 里有（多半是上次向导写的） | 默认选中「自定义目录」并填入该值；改选程序目录 / 系统目录时**删除**这个用户级变量——否则它是第一级，选了也不生效 |
| 系统级 `HKLM` 里有、用户级没有 | 管理员统一设定的，本页只读显示该目录，不给选择（用户级写一个去覆盖它属于越权） |
| 只在进程环境里有（从设置了它的终端启动） | 只读显示并说明来源；不改注册表 |

用户级 / 系统级的判定直接读两个注册表位置，不看 `os.Getenv`（后者分不清来源）。

**落点**：`pkg/userenv`（`//go:build windows` 实现 `Get/Set/Unset(name)` + 广播；其余平台返回
`ErrUnsupported`）。零领域依赖、无全局状态，符合 `pkg/` 准入标准；GUI 与 `config init --set-env` 共用。

**CLI 等价物**（§10.7 不变量 2）：`config init --dir DIR --set-env`。Windows 上写用户级 `ASA_CFG` 并广播；
Linux 上不改任何 shell 配置文件（`.bashrc` / `.zshrc` / `/etc/environment` 选哪个都是替用户做主），
只打印 `export ASA_CFG=…` 与「`service install` 会把当前 `ASA_CFG` 写进 unit」两条提示。

#### P2-3.5.2 原生目录选择器

Fyne 的 `dialog.NewFolderOpen` 是在应用窗口里自绘的，没有快速访问、网络位置、地址栏粘贴，
Windows 用户不熟悉。改用系统的 `IFileOpenDialog` + `FOS_PICKFOLDERS`（Vista 起的标准「选择文件夹」对话框）：

- 落点 `pkg/folderpicker`：`Pick(title, initialDir string, owner uintptr) (path string, ok bool, err error)`，
  `//go:build windows` 实现，其余平台 `ErrUnsupported`。用 go-ole（已在依赖图里，gopsutil 间接引入）
  做 `CoCreateInstance(CLSID_FileOpenDialog)`，vtable 调用 `SetOptions/SetFolder/SetTitle/Show/GetResult`，
  结果 `IShellItem.GetDisplayName(SIGDN_FILESYSPATH)`。
- **线程**：`Show` 是模态阻塞调用，且要求 STA。放在独立 goroutine 里 `runtime.LockOSThread()` +
  `CoInitializeEx(COINIT_APARTMENTTHREADED)`，**不在 Fyne 主线程上跑**——否则 Fyne 的渲染循环停摆，
  主窗口「未响应」。结果经 `fyne.Do` 回填；选择器打开期间禁用向导按钮，防止重复打开。
- **owner**：Fyne 不暴露 HWND，点击「浏览…」时取 `GetForegroundWindow()` 作 owner（此刻前台必然是向导窗口），
  保证选择器浮在向导之上、不跑到后面。
- 用户取消返回 `ok=false`；COM 初始化或创建失败返回 `err`，调用方**回退到 `dialog.NewFolderOpen`**，
  不因选择器失败卡住向导。
- 不引入 zenity / sqweek/dialog 之类整包依赖：只需要这一个对话框，项目里 junction、ETW 也都是直接调系统 API。

### P2-3.6 新包 `internal/bootstrap`：配置 → 运行时的唯一应用点

```go
// Apply 把 appconfig 应用到各运行时包：download.Configure + runner.Configure（字段给齐）。
// runner.Configure 是整体覆盖，字段映射只在这里写一次。
func Apply(cfg *appconfig.Config, baseDir string)

// Reload 在已运行的进程里重新走一遍启动引导：
// Load(WithoutAutoGenerate) → cfgpkg.BaseDir → EnsureDirectories → InitLoggerWithBaseDir → Apply。
// 返回生效的 BaseDir 与配置；出错时不改动已生效的状态。
func Reload() (string, *appconfig.Config, error)
```

- 依赖：`appconfig`、`config`、`runner`、`pkg/download`、`pkg/logger`；被 `main`、`actions`、`gui` 依赖，
  无环（`runner` 不依赖任何上层包）。
- `main.go` 的 `applyAppConfig` 保留 `webapi.*` 那几个包级变量赋值，其余改调 `bootstrap.Apply`。
  `webapi` 变量不进 `bootstrap`：否则 `bootstrap` 要 import `webapi`，`actions` 再 import `bootstrap`
  就把 `webapi` 拖进了 `actions` 的依赖闭包。
- `setup.go:84`、`gui.go:437` 两处手写 `runner.Configure` 删除。

## P2-4. 分步实施清单

| 步骤 | 内容 | 落点 |
|---|---|---|
| **C0** ✅ | **先验证**：①现场诊断：客户端 GBK，见 P2-1.2；②BOM + CRLF 的 config.yaml 能被 `Load` / `fileOnlyBaseDir` 正确解析——**通过**，BOM 方案保留 | 现场机器；`internal/appconfig/encoding_test.go` |
| **C1** ✅ | `internal/bootstrap`：`Apply` / `Reload`；`main.go`、`setup.go`、`gui.go` 三处 `runner.Configure` 收敛，补上 setup/GUI 漏掉的 `download.Configure` | `internal/bootstrap/`、`main.go`、`internal/actions/setup.go`、`internal/gui/gui.go` |
| **C2** ✅ | 模板：拆 `template_zh.go` / `template_en.go`；`renderTemplate(lang, baseDir)`（Windows BOM+CRLF，Linux LF）；`DefaultTemplateLang()`；**zh/en 解析结果逐字段相等**的单测，外加两份都等于 `defaultConfig()` | `internal/appconfig/template*.go` + 测试 |
| **C3** ✅ | `Load(opts...)` + `WithoutAutoGenerate` + `ConfigPath()` + `ConfigMissing()`；`CheckFile`；`InitConfig` / `DefaultInitDir` / `ErrConfigExists`（原子写、`--force` 备份）；~~删 `WriteInitialConfig`~~（推迟到 C6/C7，见 §P2-7）；`writeDefaultConfig` 复用同一渲染/写入 | `internal/appconfig/{config,init}.go` + 测试 |
| **C4** ✅ | `startupModeFor(args)`（原名 `bootstrapFor`，见 §P2-8） + 表驱动单测；`main.go` 按其结果决定 autoGenerate / ensureDirs；`main.go:250` 提示改用 `ConfigPath()` | `startup.go`（main 包）、`main.go` |
| **C5** ✅ | `asa-server config init\|path\|validate`；交互式双语语言提问（`setup` 缺配置分支复用同一函数） | `internal/actions/configcmd.go`、`main.go`（注册） |
| **C6** ✅ | `setup` 接入：`ConfigMissing` 分支、交互停顿编辑、`bootstrap.Reload`、Reload 后重跑 Linux preflight、非交互报错文案 | `internal/actions/setup.go` |
| **C7a** ✅ | `pkg/userenv`（用户级 / 系统级环境变量读取、用户级 Set/Unset + `WM_SETTINGCHANGE` 广播）；`config init --set-env` | `pkg/userenv/`、`internal/actions/configcmd.go` |
| **C7b** ✅ | `pkg/folderpicker`（`IFileOpenDialog` + `FOS_PICKFOLDERS`，独立 STA 线程；直接走 vtable，未引入 go-ole） | `pkg/folderpicker/` |
| **C7c** ✅ | 服务安装注入 `ASA_CFG`：两平台共用 `injectConfigLocation`（在 `configurePlatform` 之后并入 `EnvVars`） | `internal/svcmgr/service.go` |
| **C7** ✅ | GUI 三页向导（含自定义目录、已有 `ASA_CFG` 四种情况、服务已安装时的重装提示）、原生选择器（失败回退 Fyne）、取消退路、删 `applyChosenBaseDir` | `internal/gui/config_wizard.go`、`internal/gui/gui.go` |
| **C8** | 文档：`LINUX_DEPLOYMENT.md` 部署步骤改为 `config init → 编辑 → config validate → setup`；`docs/README.md` 与本文件状态；CLAUDE.md Build & Run 补 `config init`，Project Structure 补 `pkg/userenv`、`pkg/folderpicker` | docs |

C1、C2、C4、C7a、C7b、C7c 互不依赖；C3 依赖 C2；C5/C6 依赖 C3+C4；C7 依赖 C1+C3+C7a+C7b。每步独立可提交。

## P2-5. 验证

- **单测（Windows）**：C0 BOM/CRLF 解析；C2 zh/en 同构 + 等于默认值；C3 `InitConfig` 已存在拒绝 / `--force`
  备份 / 原子写（写入中途失败不留半截文件）、`WithoutAutoGenerate` 不落盘且 `ConfigMissing()==true`、
  `ConfigPath()` 在 `ASA_CFG` / exe 同级 / 系统目录三档下都正确；C4 `bootstrapFor` 表驱动。
- **WSL**（`wsl -e zsh -lc 'cd /mnt/d/golang/asa-server && …'`）：
  - `LANG=C` 与 `LANG=C.UTF-8` 下分别 `config init --non-interactive`，前者生成的文件 `grep -P '[^\x00-\x7F]'` 无命中，后者为中文；
  - 交互式 `config init` 选 2：生成英文模板，且整段输出 `grep -P '[^\x00-\x7F]'` 无命中；
  - 用 `luit -encoding GBK` 或把 Windows Terminal/Xshell 会话切到 GBK 连上 WSL，复现「`cat` 也乱」，确认双语提问里的英文部分可读；
  - `ASA_CFG=/tmp/x asa-server --help` 之后 `/tmp/x` **不存在**（P2-1.1 第一行的回归）；
  - `config init --basedir /tmp/data` → 改 `download.github_proxy` → `setup --non-interactive`，日志里下载 URL
    带上代理前缀（验证 Reload 真的重做了 `download.Configure`）。
- **Windows 手测**：记事本打开生成的文件无乱码、换行正常；GUI 全新目录双击 → 三页向导 → 改代理 →
  初始化面板下载走代理；程序放 `C:\Program Files\` 下时第 1 页默认 ProgramData；第 1 页取消后重启不再弹向导。
- **自定义目录（Windows 手测）**：
  - 向导选自定义目录 → 关闭程序 → 从资源管理器重新双击：不弹向导、读的是自定义目录的配置（`config path` 核对）；
  - 选择器：原生对话框浮在向导之上、打开期间 Fyne 窗口仍在重绘；取消不报错；
  - 以自定义目录 `service install`（管理员）→ `reg query HKLM\SYSTEM\CurrentControlSet\Services\<服务名> /v Environment`
    含 `ASA_CFG`；服务启动后日志里的 BaseDir 与 GUI 一致；
  - 再次打开向导改选程序目录：`HKCU\Environment\ASA_CFG` 被删除；
  - 选映射网络盘：被拒绝并给出原因。
- **WSL**：`export ASA_CFG=/tmp/c && asa-server service install` 生成的 unit 里有 `Environment=ASA_CFG=/tmp/c`。
- `go build ./...`（PowerShell）/ `GOOS=linux CGO_ENABLED=0 go build ./...` / `go vet` 两平台。

## P2-6. 兼容性与风险

- **已有 config.yaml 的部署**：`config init` 不会被隐式调用；`setup`/GUI 走「已存在，沿用」分支，行为同今天。
  已有文件不补 BOM、不改换行——只影响新生成的文件。
- **`api` / 服务模式**：仍自动生成，§10.7 不变量 3 不受影响；生成的文件现在也带 BOM(Windows) / 按 locale
  选语言(Linux)。
- **不变量 2（向导做的事都有 CLI 等价物）**：GUI 三页 ⇔ `config init --dir --basedir [--set-env]` + 编辑 + `config validate` + `setup`。
- **BOM 风险**：Linux 不写 BOM，脚本不受影响；Windows 侧若 C0 不通过则放弃 BOM。
- **英文模板漂移**：字段/默认值靠 C2 的同构单测兜住；注释翻译的一致性靠评审。以后改模板**两份同改**，
  在两个文件头注释里互相指明。
- **自定义目录依赖一个外部状态（环境变量）**：配置位置不再能从「exe 旁边有没有文件」一眼看出来，
  排障先跑 `config path`。用户手动删了 `ASA_CFG` 或换了 Windows 账户，程序会回到「找不到配置 → 弹向导」，
  向导第 1 页选自定义目录并指回原目录即可接管（已有 config.yaml 不会被覆盖）。
- **服务里的 `ASA_CFG` 是安装时的快照**：之后改位置必须重装服务，向导会提示但不会代劳。
- **`bootstrapFor` 偷看参数**：只影响「要不要生成配置 / 建目录」这两个副作用，识别错的最坏结果是回到旧行为
  （生成模板 + 建目录），不会让命令失败。
- **不覆盖**：不做 GUI 内嵌的 YAML 编辑器（外部记事本 + 重新校验已足够；内嵌编辑器要处理语法高亮、
  撤销、CJK 输入法，收益不抵成本，留作后续）；不做配置表单化；不处理 frp / filesync 自己的 JSON 配置。

## P2-7. 实施记录（C0–C3，2026-09-29）

**落地文件**

| 文件 | 内容 |
|---|---|
| `internal/appconfig/render.go` | `LangZH/LangEN`、`NormalizeLang`、`DefaultTemplateLang`/`templateLangFor`、`renderTemplate`/`renderTemplateFor(goos, lang, baseDir)`（Windows BOM+CRLF、Linux LF）、`quoteYAML`、`utf8BOM` |
| `internal/appconfig/template_zh.go` | 原 `template.go` 改名；`templateZH` + `trustLocalCABlockZH(goos)`；`basedir` 行改为 `%s` 占位 |
| `internal/appconfig/template_en.go` | `templateEN`（纯 ASCII）+ `trustLocalCABlockEN(goos)` |
| `internal/appconfig/init.go` | `InitOptions`、`InitConfig`、`DefaultInitDir`、`ErrConfigExists`、`backupFile`、`writeFileAtomic` |
| `internal/appconfig/config.go` | `Load(opts ...LoadOption)`、`WithoutAutoGenerate`、`ConfigPath`、`ConfigMissing`、`CheckFile`、`decodeFile`、`fileOnlyBaseDirAt`；`writeDefaultConfig` 改走 `renderTemplate` + `writeFileAtomic` |
| `internal/bootstrap/bootstrap.go` | `Apply(cfg, baseDir)`、`Reload()` |
| `main.go` / `internal/actions/setup.go` / `internal/gui/gui.go` | 三处 `runner.Configure` 收敛到 `bootstrap.Apply` / `bootstrap.Reload` |

**测试**：新增 `encoding_test.go`（C0）、`template_test.go`（中英同构、等于内置默认值、英文纯 ASCII、
按平台的字节布局、`trust_local_ca` 按平台、basedir 往返、locale 判定表、自动生成与 `InitConfig` 产物逐字节相同）、
`init_test.go`（只写一个文件、拒绝覆盖、`Force` 备份、相对路径转绝对、非法语言、`DefaultInitDir`、
`WithoutAutoGenerate` 不落盘、`ConfigMissing`/`WasConfigAutoGenerated` 两态、`ConfigPath` 跟随查找层级、`CheckFile`）。
Windows `go test ./internal/appconfig/ ./internal/actions/ ./internal/installer/` 全绿；WSL 同样全绿（含 `LANG= LC_ALL=` 下重跑模板用例）；
`go build ./...` 与 `GOOS=linux CGO_ENABLED=0 go build ./...` 通过。

**真实二进制验证**（C4 之前 `--help` 仍会触发自动生成，正好拿来测渲染）：

- WSL `LANG=C`：`file` 报 `ASCII text`，非 ASCII 行数 0，首行 `# ASA Server Manager application config`；
- WSL `LANG=C.UTF-8`：`UTF-8 text`，首行为中文；
- Windows：文件头 `EF BB BF`，229 个换行全部是 CRLF。

**与设计的偏离**

1. **`WriteInitialConfig` 没有删**，推迟到 C6（setup）/ C7（GUI）换成 `InitConfig` 时一起删。C3 只修了它的目标路径：
   改用 `ConfigPath()`（未经 `Load` 时回落 exe 同级），`ASA_CFG` 指向别处时不再去改一份不存在的文件；
   替换时用 `quoteYAML`，原地替换保留 BOM 与 CRLF。
2. **`main.go` 鉴权致命错误的提示路径**（原计划 C4）顺手在本轮改成 `appconfig.ConfigPath()`。
3. **GUI 的 `applyChosenBaseDir` 在 C1 就删了**（原计划 C7），调用点直接 `bootstrap.Reload()`；setup 里
   「数据目录变了就重建目录 + 日志 + 手拼 `runner.Config`」那整段也删了，改为 `resolveSetupBaseDir` 新选数据目录后
   调 `bootstrap.Reload()`。已有配置的分支本来就由 `main.go` 启动时应用过，不需要再做一次。
4. **`Load` 刚生成的模板会立刻再读一遍**（以前生成后直接解码默认值，不读文件）：等于每次首次运行都验证一次
   模板本身能解析、能过校验。
5. **`Load` / `CheckFile` 改用 `viper.SetConfigFile(path)`** 精确指定文件，不再用 `SetConfigName("config")` +
   `AddConfigPath`——后者会按 viper 支持的全部扩展名查找，目录里有个 `config.json` 就可能读错文件。
   读取 / 解析失败的错误信息里因此带的是完整路径，不再只是文件名。
6. **`WasConfigAutoGenerated()` 现在只在模板真的写成功后才为 true**（以前是「文件缺失」就为 true，写失败也一样）。
7. 渲染逻辑放在 `render.go`，不叫 `template.go`：中英模板各占一个文件后，原名容易被误解成「模板本体」。

**实施中踩到的坑**：用编辑工具往 Go 源码里写 BOM 的 Unicode 转义（反斜杠 + u + FEFF）时，它被还原成了字面 BOM 字符，`go build` 报 `illegal byte order mark`。`utf8BOM` 常量因此写成三个十六进制字节转义（`\xef\xbb\xbf`，见 `render.go`）——源码和注释里都别出现字面 BOM。

## P2-8. 实施记录（C4–C5，2026-09-29）

**落地文件**

| 文件 | 内容 |
|---|---|
| `startup.go` / `startup_test.go`（main 包） | `startupMode{autoGenerate, ensureDirs, readOnly}`、`startupModeFor(os.Args)`、`globalValueFlags`、`readOnlyCommands`；19 条表驱动用例 |
| `main.go` | 按模式决定 `Load` 是否 `WithoutAutoGenerate`、是否 `EnsureDirectories` + 切文件日志；`loadAppConfig(mode)` 在 `readOnly` 下不报错不中止；注册 `actions.ConfigCommand()` |
| `internal/actions/prompt.go` | `prompter`（整条命令共用一个 `bufio.Reader`，可注入）、`Line` / `Confirm` / `askTemplateLang`（双语、英文在前）、`stdinIsTerminal` |
| `internal/actions/configcmd.go` / `configcmd_test.go` | `config init / path / validate`；10 条用例 |
| `internal/appconfig/config.go` | 新增 `ConfigSearchDirs()`、`ConfigPathAfterInit(target)`；`locateConfigDir` 抽出 `locateConfigDirWith(exists)`，预测与实际查找同一套规则 |
| `pkg/fsutil/samepath.go` | `SamePath(a, b)`：Abs + Clean，Windows 忽略大小写 |

**验证**

- 单测：Windows `go test . ./internal/actions/ ./internal/appconfig/ ./internal/installer/ ./pkg/fsutil/` 全绿，WSL 同样全绿；
  `go build ./...` 与 `GOOS=linux CGO_ENABLED=0 go build ./...` 通过。
- WSL 真实二进制（`ASA_CFG=/tmp/c5`）：`--help` / `api --help` / `config --help` 之后 `/tmp/c5` **不存在**（P2-1.1 第一行的回归已修）；
  `config path`（缺失时）三级查找与数据目录来源显示正确；`config init --non-interactive --basedir /tmp/c5data` 之后 `/tmp/c5` 下只有 `config.yaml`；
  再跑一次无 `--force` 退出码 1 并提示 `--force`；`config validate` 通过；把端口改成 70000 后 `config validate` 退出码 1 并指出字段。
- WSL `LANG=C`：`config init --non-interactive` 的输出与生成的文件非 ASCII 字符数均为 0。
- WSL 伪终端（`script -qec`）喂 `2` + 回车：出现双语提问，选 2 后后续提示全英文，生成英文模板。
- Windows 真实二进制：`--help` / `api --help` 后目标目录不存在；`config init` 生成的文件头 `EF BB BF`；`config validate` 通过。

**与设计的偏离**

1. **`setup` 与 Windows GUI 暂保持旧的启动行为**（自动生成 + 建目录），P2-3.2 表里它们那两行分别在 C6 / C7 生效。
   原因：它们现在仍靠 `WasConfigAutoGenerated()` + `WriteInitialConfig` 判断和处理首次安装，提前切到「不自动生成」
   会让 setup 误报「检测到已有配置」、GUI 首次启动向导不弹。等它们改用 `ConfigMissing()` + `InitConfig` 时一起切，
   保证每一步提交后程序都能正常使用。
2. **命名**：main 包里的函数叫 `startupModeFor`、文件叫 `startup.go`，不叫计划里的 `bootstrapFor` / `bootstrap.go`——
   main.go 已经 import 了 `internal/bootstrap`，同名类型会冲突。
3. **帮助识别范围比计划宽**：任意位置的 `-h` / `--help`（含子命令的，如 `service install -h`）都走只读模式；
   `-v` / `--version` 只在顶层命令名之前识别——子命令自己的 `-v` 可能另有含义。
4. **新增 `readOnly` 模式位**：`config` / 帮助 / 版本下，`loadAppConfig` 对配置错误**不报错也不中止**（包括开启了鉴权的
   致命分支），交给命令自己报告——否则 `config validate` 在开口之前就被 `log.Fatal` 截住了。这些命令不启动任何服务，
   不存在「鉴权悄悄失效」的风险。
5. **交互判定**：除 `--non-interactive` 外，**标准输入不是终端时自动按非交互处理**（`golang.org/x/term`），管道 / CI 不会卡在提问上。
6. **交互模式下覆盖已有文件改为当场确认**（计划只写了 `--force`）；非交互仍必须 `--force`。`--force` 同时放行
   「非交互模式下遮蔽已有配置」。
7. **`--dir` 指向三级查找之外**：交互模式要求确认，非交互照写并警告「请设置 `ASA_CFG=<目录>`」。`--set-env`
   留到 C7a 与 `pkg/userenv` 一起做。
8. **未显式给 basedir 时**（数据落在配置目录或 `ASA_BASEDIR`）：同样跑一次 `ValidateBaseDir`，但只提示不拦——
   那是默认值不是用户的显式选择。显式给的 basedir 校验失败时，交互模式重新询问，非交互直接报错。
9. **`config path` 额外做了一次 `CheckFile`**：配置有错时提示「程序会回落到默认配置运行」并指向 `config validate`——
   排障时最想知道的就是「我改的配置到底生效没有」。
10. **`config validate` 的输出**额外带上数据目录、面板地址 / 端口 / 鉴权开关、下载代理（非空时），方便改完一眼核对。

## P2-9. 实施记录（C6–C7，2026-09-29）

**落地文件**

| 文件 | 内容 |
|---|---|
| `startup.go` | 新增 `firstRunStartup{ensureDirs, deferDirsIfMissing}`，给 `setup`、`gui` 与 **Windows 无参**；`startupModeFor` 增加 `goos` 参数（Linux 无参仍是 api 的旧行为） |
| `main.go` | 目录变量总是 `cfgpkg.SetDirectories`；只有 `ensureDirs && !(deferDirsIfMissing && ConfigMissing())` 才建目录 + 切文件日志；webapi 变量改调 `webapi.ApplyConfig` |
| `internal/config/config.go` | 新增 `SetDirectories(baseDir)`（只赋值不建目录），`EnsureDirectories` 改为先调它 |
| `internal/actions/setup.go` / `setup_test.go` | `resolveSetupBaseDir(flagBaseDir, nonInteractive, prompter, tty) (baseDir, created, err)`：已有配置沿用；缺失时复用 `runConfigInit` 生成 → 摘要 → 停顿等回车 → `bootstrap.Reload`（失败可修正重试 / `q` 放弃）；新生成配置后 Linux 重跑 preflight。6 条用例 |
| `internal/actions/configcmd.go` | `runConfigInit` 改为返回 `configInitResult`、不再自己打印摘要；新增 `AskLang`（与 `Interactive` 分开）、`SetEnv`；`--set-env` flag；`printConfigInitSummary(out, res, forSetup)` |
| `pkg/userenv` | `Get(scope, name)`、`SetUser`、`UnsetUser`、`Broadcast`（Windows 注册表 + `WM_SETTINGCHANGE`；其余平台 `ErrUnsupported`）。单测真写 HKCU 一个一次性变量并删除 |
| `pkg/folderpicker` | `Pick(title, initialDir, owner)`、`ForegroundWindow()`；单测走一遍 Show 之前的全部 COM 调用 |
| `internal/svcmgr/service.go` / `service_test.go` | `injectConfigLocation`：`newServiceConfig` 在 `configurePlatform` 之后把 `ASA_CFG`（转绝对路径）并入 `EnvVars` |
| `internal/webapi/actions.go` | `ApplyConfig(cfg)`：main 与 GUI 向导共用 |
| `internal/gui/config_wizard.go` | 三页向导、`ASA_CFG` 四种现状、服务已安装提示、原生选择器（失败回退 Fyne）、取消兜底 |
| `internal/gui/gui.go` | 删 `showBaseDirPicker`；向导改在主窗口显示之后打开（见偏离 5） |
| `internal/appconfig` | 删 `WriteInitialConfig`、`basedirPlaceholder`、`WasConfigAutoGenerated` 及其测试 |

**验证**

- 单测：Windows `go test . ./internal/appconfig/ ./internal/actions/ ./internal/svcmgr/ ./internal/installer/ ./pkg/fsutil/ ./pkg/userenv/ ./pkg/folderpicker/` 全绿；
  WSL `go test . ./internal/appconfig/ ./internal/actions/ ./internal/svcmgr/ ./pkg/fsutil/` 全绿；两平台 `go build ./...` 通过。
- WSL 真实二进制 + 伪终端跑 `setup`：语言提问 → 数据目录 → 摘要 → 停顿；**停顿期间把 `linux.auto_download` 改成 `false`**，回车后
  「已应用配置」→ 重跑自检 → 在准备运行时一步报 `auto_download is disabled`——证明停顿期间的修改在下载之前生效。
  此前 `/tmp/c6` 里只有 `config.yaml`，没有提前建出的空目录。
- Windows GUI 冒烟：从空目录启动，程序目录里只有 exe（不再自动生成配置、不建目录）；截图确认第 1 页三个选项、自定义目录输入框
  随选项启用 / 禁用、按钮布局正确。

**与设计的偏离**

1. **`pkg/folderpicker` 没有用 go-ole**，直接用 `golang.org/x/sys/windows` + vtable 调用：只需要一个对话框的五六个方法，
   不值得把间接依赖升成直接依赖。
2. **服务注入放在 `service.go` 两平台共用**（`injectConfigLocation`），没有分别写进 `service_{windows,linux}.go`：逻辑完全相同，
   只要在 `configurePlatform` 之后执行，Linux 那边的 `HOME` 就不会被覆盖。
3. **新增 `webapi.ApplyConfig`**：GUI 向导重新加载配置后，GUI 内启动的 API 服务要用上新的端口 / TLS，否则得重启程序；
   main.go 原来那 7 行赋值挪进去，两处共用。
4. **`setup` 的交互判定保持旧语义**（只看 `--non-interactive`），另加 `tty` 只控制「问不问语言、停不停顿」：
   `echo /data | asa-server setup` 这种老用法第一行仍是数据目录，不会被语言问题吃掉。数据目录直接回车现在表示「与配置文件同目录」
   （以前要求非空）。
5. **向导改在主窗口显示之后打开**：原来 `runFirstLaunchWizardIfNeeded` 在 `ShowAndRun` 之前调用，新窗口会被后显示的主窗口
   压在下面（截图实测），首次启动的用户看不到它。现在放进原有的「延迟 100ms 刷新状态」那一步，并 `RequestFocus`。
6. **提示文字不用「→」**：Fyne 自动换行恰好断在它附近时渲染成乱码（截图实测）。
7. **`WasConfigAutoGenerated` 一并删除**（计划只写了「调用点换成 `ConfigMissing()` 后删除」，本轮调用点清零所以删了）。

**GUI 向导手测清单（未自动化，需人工点一遍）**

- [ ] 全新目录双击：向导在主窗口之上；选程序目录 → 数据目录选 E 盘 → 生成 → 第 3 页「用记事本打开」无乱码、换行正常 →
      改 `download.http_proxy` 保存 →「重新校验」→「保存并初始化环境」→ 初始化面板下载走代理。
- [ ] 「浏览…」：原生对话框浮在向导之上，打开期间向导与主窗口仍在重绘；取消不报错。
- [ ] 自定义目录：选完关闭程序 → 从资源管理器重新双击：不弹向导，`asa-server config path` 显示读的是自定义目录。
- [ ] 再次进入向导（删掉配置后）改选程序目录：`HKCU\Environment\ASA_CFG` 被删除。
- [ ] 自定义目录选映射网络盘：被拒绝并说明原因。
- [ ] 程序放在 `C:\Program Files\` 下：第 1 页默认选中系统目录，程序目录一项标注无写权限。
- [ ] 第 1 页点取消 / 关窗：程序目录生成默认配置，重启后不再弹向导。
- [ ] 以自定义目录 `service install`（管理员）：`reg query HKLM\SYSTEM\CurrentControlSet\Services\ASA-Server-Manager /v Environment`
      含 `ASA_CFG`，服务启动后日志里的 BaseDir 与 GUI 一致。

