# `internal/runner` 拆包后续清单

> 依赖 `docs/RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md`（阶段 A–J 已于 2026-09-05 全部执行完成，
> 见该文档历史与提交 `a83c85a`..`6894907`）。本文档是**活动清单**，记录方案执行完之后盘点出的
> 真实缺口与可选卫生项；结论稳定后回填 PLAN，不在 PLAN 里直接改。
>
> **Gap A（`pkg/shareacl`）与 Gap B（vcredist 零散机制）均已于 2026-09-05 执行完成**——
> 见 §1、§2 末尾的落地说明。
>
> **2026-09-05 复评新增 Gap C/D/E 与结论 F**（§3–§6），**C/D/E 均已于同日执行完成**：起因是评估「vcredist 编排能否整体下沉」，
> 核对下来 PLAN 阶段 H 的否决理由已失效 3/4，并顺带发现三件更该先做的事，以及 §0 表格里一处记错。

---

## 0. 先回答"文件数比方案里多"是不是偏离

方案 §2 的目标目录结构把 `internal/runner` 最终画成 `runner.go` +
`runner_{windows,linux}.go` + `preflight.go` 四个文件。实际落地时**没有**把每个子系统的组合根
代码全部塞进 `runner_linux.go`，而是保留了一个文件对应一个子系统（`xvfb_linux.go`、
`python_linux.go`、`umu_linux.go`、`display_linux.go`、`runtimeuser_linux.go`、
`sharedaccess_linux.go`、`vcredist_linux.go`、`prefix_windows.go`）。这是执行时的有意选择，
不是漏拆——核对每个文件的内容：

| 文件 | 行数 | 内容 |
|---|---:|---|
| `python_linux.go` | 49 | 纯胶水：`pyfinder.New()` + 把 `Config.PythonBin` 接进去 |
| `xvfb_linux.go` | 49 | 纯胶水：`xvfb.New()` + `Reconfigure` |
| `umu_linux.go` | 209 | 组合根：umu/wineprefix 单例 + `ensureRuntime` 编排（无法再下沉，见 PLAN §"关键设计决策" 4.2 的 wineprefix→umu 单向依赖论证） |
| `display_linux.go` | 45 | 见 §3 Gap C（已执行，原 375 行） |
| `preflight_linux.go` | 141 | 聚合逻辑，方案设计如此 |
| `prefix_windows.go` | 24 | 六个入口在 Windows 上的空实现 |
| `runtimeuser_linux.go` | 248 | 见 §1（已执行，原 315 行） |
| `sharedaccess_linux.go` | 209 | 见 §1（已执行，原 385 行） |
| `vcredist_linux.go` | 310 | 见 §2/§4/§5（均已执行，原 486→419→310 行）、§6（核心编排有意保留） |

写这一节时认为真正的缺口只有 §1、§2 两项；2026-09-05 复评后增加 §3–§5 三项，其中 §3 是把
上表里记错的一行改正过来。

---

## 1. Gap A：`pkg/shareacl` 从未创建

方案 §2 原本规划了 `pkg/shareacl`（`shareacl.New() *Manager` + `.Prepare(path) error` +
`.Status(paths) Info`，把 `sharedaccess_linux.go` 的机制整体接走）。阶段 E 执行时**当场决定跳过**，
提交 `f2673eb` 的说明写得很清楚：

> shareacl 留在 internal/runner 未拆——见 sharedaccess_linux.go 现状，其目录清单与 ACL 判断业务
> 规则耦合更深，本阶段判断保留原状不强拆

回头核对代码，这个判断值得重新考虑。`sharedaccess_linux.go` 里：

- **纯机制**（不认识 ASA/实例/mirror，只认识"给一棵目录树 chgrp+setgid+POSIX 默认 ACL，
  没有 setfacl 就退化成 chown"）：`chgrpSetgidTree`、`applyDefaultACL`、`classifyACLError`、
  `defaultACLMissing`、`aclSupported`、`findAdminTool`、`runtimeGroupName`、`errACLUnsupported`——
  约 260 行，占全文件三分之二。
- **业务规则**（认识"哪些目录要共享写"、认识 `runner.Config`/`Problem` 类型、要拼中文提示文案）：
  `sharedSubtrees`（2 行路径清单）、`sharedTrees`、`sharedAccessStatus`（诊断聚合）、
  `prepareSharedTree`（编排入口）、`checkACLSupport`（Preflight 文案）。

另外 `runtimeuser_linux.go` 里的 `sharedAccessNeeded`（抽样判断要不要重新跑 ACL 全量）和
`ensureWorldReadExec`（把只读子树设成全员可读可穿越，用于 proton/umu-launcher 目录）同样是通用
机制，只是因为调用方在 `reconcileRuntimeOwnership` 里而被放错了文件。

**提议**：

1. 新建 `pkg/shareacl`，签名沿用方案原文：
   ```go
   func New() *Manager
   func (m *Manager) Prepare(root string, uid, gid int, group string) error  // chgrpSetgidTree + applyDefaultACL，降级 chown 走 sysuser.ChownTreeAs
   func (m *Manager) NeedsPass(root string, gid int) bool                    // 原 sharedAccessNeeded
   func (m *Manager) DefaultACLMissing(root, group string) bool             // 原 defaultACLMissing
   func (m *Manager) Supported(dir, group string) error                    // 原 aclSupported
   ```
   `findAdminTool`/`runtimeGroupName`/`errACLUnsupported`/`classifyACLError` 作为包内私有实现细节。
   不需要 `New(cfg)` 持有配置——这一层完全无状态，跟 `pkg/linuxdeps`/`pkg/steamrt` 一样是自由函数
   集合即可，`New()` 都不必要，直接导出包级函数。
2. `internal/runner/sharedaccess_linux.go` 瘦身到只剩 `sharedSubtrees`/`sharedTrees`/
   `sharedAccessStatus`/`prepareSharedTree`/`checkACLSupport`，全部改调 `shareacl.*`。
3. `ensureWorldReadExec` 挪到 `pkg/fsutil`（改名 `EnsureWorldReadable`）——它是"整棵目录树按需
   `chmod` 成全员可读可穿越"，跟 `fsutil.CopyDir`/`CopyFile` 是同一类"通用文件树操作"，不需要
   知道"运行时降权用户"这个概念，`runtimeuser_linux.go` 只需要调 `fsutil.EnsureWorldReadable(dir)`。
4. `sharedAccessNeeded` 随 `Prepare`/`NeedsPass` 一起搬进 `pkg/shareacl`，
   `reconcileRuntimeOwnership`（`runtimeuser_linux.go`）改调 `shareacl.NeedsPass(...)`。

预期效果：`sharedaccess_linux.go` 从 385 行降到 ~90 行（纯业务：目录清单+文案+编排），
`runtimeuser_linux.go` 从 315 行降到 ~260 行。

**落地说明（2026-09-05）**：与提议略有出入，均为执行时的具体化，不改变结论——

- `pkg/shareacl` 做成无状态包级自由函数（`Prepare`/`NeedsPass`/`DefaultACLMissing`/`Supported`/
  `GroupName`），没有 `New()`/`*Manager`：这一层确实不持有任何跨调用状态，连 `pkg/sysuser` 那种
  "配置值缓存"都没有，包级函数比强加一个空壳 `Manager`更直接。
  `Prepare` 的降级路径以 `ChownFallback func(root string, uid, gid int) error` 回调注入而非硬编码
  `sysuser.ChownTreeAs`——保持 `pkg/shareacl` 对 `pkg/sysuser` 零依赖，日志措辞（"POSIX ACLs
  unavailable on %s..."）也留给调用方决定。
- `findAdminTool` 确实各自留了一份小拷贝（`pkg/shareacl` 内部 + `internal/runner/sharedaccess_linux.go`
  诊断用），没有导出成 `shareacl.FindAdminTool`——理由同 `pkg/sysuser` 那份：三个用途不同，共享
  没有实际收益。
- `TestChgrpSetgidTree`/`TestDefaultACLMissing`/`TestNeedsPass` 三个测试原样搬进
  `pkg/shareacl/shareacl_test.go`，全部只在 `t.TempDir()` 内操作、真实调用 `setfacl`/`getfacl`
  （工具缺失时自行 skip）——不需要 §4 提到的 `ASA_TEST_SHAREACL` 门控：它们不像
  `pkg/sysuser`/`pkg/xvfb` 的真机集成测试那样创建系统级资源（用户账号、Xvfb 进程），风险不对等。

---

## 2. Gap B（可选，代码卫生，收益较小）：`vcredist_linux.go` 里几个零散的纯机制

`vcredist_linux.go` 的核心编排（`ensureVCRedist`/`runInPrefix`）留在 `internal/runner` 是阶段 H
就定好的**设计**，不是遗留——它深度依赖 `prefixDir`/`prefixKeyFor`/`acquireDisplay`/
`resolveRuntimeCredential`/`runtimeEnv`/`umuInterpreter`，这些全是还留在 `internal/runner` 的
业务概念，见 `docs/RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md` 阶段 H 一节与 `CLAUDE.md` 对应条目。
不建议现在硬拆这一块。

但文件里仍有几个**完全通用、零 ASA 知识**的小函数，属于"顺手清理"级别，优先级低于 Gap A：

| 函数 | 现状 | 建议去处 | 理由 |
|---|---|---|---|
| `downloadProgress`/`mib` | 把 `pkg/download.Options.Progress` 的字节回调节流成人可读的百分比行 | `pkg/download`，导出为 `download.ProgressLogger(label string, logf func(string, ...any)) func(done, total int64)` | `pkg/download` 本来就定义了 `Progress` 回调这个概念，节流格式化理应跟着走，其它调用 `download.Fetch` 的地方（目前只有这一处，未来 syncthing/frp 更新也可能用到）能直接复用 |
| `resolveFinalURL` | HTTP HEAD 跟随重定向拿最终 URL | `pkg/download`，导出为 `download.ResolveFinalURL(ctx, url) (string, error)` | 同上，通用 HTTP 机制，跟"VC++"毫无关系 |
| `exitCodeOf` | `exec.ExitError` → `int`，-1 表示被信号杀 | `pkg/umu`，作为 `umu.ExitCode(err) int` | 与 `umu.NewOutputCapture`（同文件、同"跑一个 exe 拿输出"的场景）放一起最自然 |
| `classifyDLL`/`nativeDLLPresent` | 读文件头转调 `vcredist.ClassifyHeader` | 保留 | 已经是两行的薄封装，搬不搬都行，优先级最低——本轮未动 |

**落地说明（2026-09-05）**：`downloadProgress`/`mib` → `download.ProgressLogger`、
`resolveFinalURL` → `download.ResolveFinalURL`（新文件 `pkg/download/progress.go`）、
`exitCodeOf` → `umu.ExitCode`（`pkg/umu/umu_linux.go`，紧邻 `NewOutputCapture`），均按上表建议落地，
签名不变。`classifyDLL`/`nativeDLLPresent` 按计划保留在 `internal/runner/vcredist_linux.go`。

---

## 3. Gap C：`pkg/display` 从未创建（PLAN 阶段 G 漏做）

§0 表格原先把 `display_linux.go` 记成「方案阶段 G 就设计成留在 internal，不是遗留」——**这句是错的**，
2026-09-05 复评时核对 PLAN 原文推翻：

- PLAN §6 阶段 G 的原文是「**阶段 G：`pkg/display`（依赖阶段 F 的 `pkg/xvfb`）**」，规划的是**下沉**；
- PLAN §2 的目标目录树连签名都画好了：
  ```
  ├── display/    # display.New(cfg Config, xvfbMgr *xvfb.Manager) *Resolver
  │               #   .Plan()（只读，供 preflight）/ .Acquire() / .Status() / .Stop()
  │               #   原 display_linux.go + runner_windows.go 里的 displayStatus 桩
  ```
- PLAN §3.1 的依赖图里有 `pkg/display ──depends──▶ pkg/xvfb`，§3.2 的
  `internal/runner ─depends─▶ pkg/{xvfb,display,vcredist,...}` 也把它列在内。

所以这与 Gap A 同类：**方案规划过、执行时跳过的补作业**，不是新想法。

### 3.1 耦合盘点

`display_linux.go`（375 行）对 `internal/runner` 的耦合只有五个点，全部可注入或已有先例：

| 现状 | 下沉后 |
|---|---|
| `Config`（**全文件只用到 `cfg.Display` 一个字段**） | `display.Config{Display string}` |
| `xvfbManager()` | `New(cfg, xvfbMgr *xvfb.Manager)` 注入；`pkg/display → pkg/xvfb` 无环（xvfb 是叶子） |
| `getConfig()`（仅 `displayStatus` 用） | 随 `Reconfigure` 消失 |
| `DisplayInfo`（带 JSON tag，API 层消费） | `type DisplayInfo = display.Info`，与 `VCRedistInfo`/`PrefixInfo` 同款（`runner.go` 已有先例） |
| `stopManagedXvfb()` | `mgr.Stop()` |

准入线（`docs/INTERNAL_LAYOUT_MIGRATION.md` §9）三条全中，且**比 vcredist 编排更干净**：代码里
零 ASA 字符串——不提 `asa-server`、不提配置键名、不提实例，所有文案只说 X / Xvfb / DISPLAY。
（包注释里提 ArkApi / ArkAscendedServer 是「为什么需要显示」的真机实测档案，属于文档不是依赖，照搬。）

顺带一处死代码：`func (p displayPlan) acquire(cfg Config)` 的 `cfg` 参数**从未被使用**
（`displayManaged` 那档走 `xvfbManager()`），下沉时自然消失。

### 3.2 三个承重点

1. **`xvfbMgr` 的持有权不能跟着搬。** PLAN §4.3 点名这是全方案「唯一有真实正确性风险的一步」：
   `xvfb.Manager` 里跑着一个 `LockOSThread` 且永不返回的 spawn-loop goroutine。`pkg/display` 必须
   **收一个现成的 `*xvfb.Manager`**（正如方案签名），绝不能自己 `xvfb.New()`——否则会出现第二个
   spawn-loop 和两个抢同一份状态文件的 Xvfb。
2. **`Plan()` 只读 / `Acquire()` 才动手的分界要跟着测试一起搬。** 这是文件里最承重的不变量
   （`GET /api/system/preflight` 不能顺手 fork 一个 X 服务端），现在由 `display_linux_test.go` 的
   `TestDisplayStatusHasNoSideEffects`（前后对比 `xvfbManager().Status()`）守着。
   `preflight_linux.go` 的 `checkDisplay` 也只许调 `Plan()`。
3. **Windows 桩按新包边界拆。** `displayStatus`/`stopManagedDisplay` 的 Windows 实现现在混在
   `runner_windows.go` 里（PLAN §7 已点名）。`pkg/display` 做成 linux-only（同 `pkg/umu`/`pkg/xvfb`/
   `pkg/sysuser`），`internal/runner` 保留这两个桩 + `DisplayInfo` 别名。

预期规模：`display_linux.go` 375 行 → `pkg/display` ~340 行 + `internal/runner/display_linux.go`
~35 行胶水；`display_linux_test.go` 整体搬走。

**落地说明（2026-09-05）**：与提议基本一致，三处具体化——

- `display_linux.go` 375 → **45 行**（`displayResolver()` 组合根 + `planDisplay`/`acquireDisplay`/
  `displayStatus`/`stopManagedDisplay` 四个签名不变的薄转发）；`pkg/display` 得到
  `display.go` 58 行（无 build tag：`Info` 要被 API 层在任何平台上引用，`Target.Apply`
  是纯切片操作）+ `display_linux.go` 374 行。
- `Plan`/`Kind` 连同常量一并导出（`KindConfigured`/`KindManaged`/`KindEnv`/`KindExisting`）：
  调用方只读 `plans[0].How` 与 `blocked`，但 `Plan` 作为返回值必须可命名。
- 测试三分：`Target` 的三条追加语义进 `pkg/display/display_test.go`（**无 build tag，
  Windows 上也跑**，以前只在 Linux 跑）；候选链/`Status` 的十一条进
  `pkg/display/display_linux_test.go`；只剩 `checkDisplay` 相关的四条留在
  `internal/runner/display_linux_test.go`（`Problem` 不归 pkg/display）。
  「`Plan()` 无副作用」那条不变量**两边各留一条**——pkg 侧用自建 Manager 测机制，
  internal 侧用进程唯一的 `xvfbMgr` 测接线，后者才覆盖得到 `checkDisplay`。
- `func (p displayPlan) acquire(cfg Config)` 那个从未被使用的 `cfg` 参数如期消失；
  `stopManagedXvfb`（xvfb_linux.go）随之删除，`Resolver.Stop()` 顶了它的位。

---

## 4. Gap D：`runInPrefix` 与 `umu.WarmPrefix` 是同一个机制写了两遍

`pkg/umu/umu_linux.go` 的 `WarmPrefix` 与 `internal/runner/vcredist_linux.go` 的 `runInPrefix`
逐项做同一件事：解析 Python 解释器 → 拼 `<python> <umu-run> <exe> <args>` → `InheritedEnv()` +
`WINEPREFIX`/`GAMEID`/`PROTONPATH`/`ProtonNoXalia` → `credential()` 降权 + `RuntimeEnv` →
`NewOutputCapture` → `WaitForWineserverDrain` → 后置条件判决。`runInPrefix` 只多四样：
`UMU_RUNTIME_UPDATE=0`、`PROTON_VERB=run`、硬超时、追加 display env。

也就是说「在 prefix 里跑一个 Windows exe」这个机制**早就通过准入线了**（`WarmPrefix` 就在 pkg 里），
现状是同一份机制在包内外各写了一遍。

**提议**：`pkg/umu` 导出

```go
type RunOptions struct {
    Timeout         time.Duration // 0 = 不设
    ExtraEnv        []string      // 追加在最后（显示就是从这里进来的）
    NoRuntimeUpdate bool          // UMU_RUNTIME_UPDATE=0
    Verb            string        // PROTON_VERB，空 = 不设（保留 umu 默认）
}
func (r *Runtime) RunInPrefix(ctx context.Context, prefix string, argv []string,
    opt RunOptions, logf func(string, ...any)) (tail string, err error)
```

`WarmPrefix` 改成它的调用方，`vcredist_linux.go` 的 `runInPrefix` 删除。

⚠️ **风险集中在 `WarmPrefix` 那两处刻意的差异上**，迁移时必须保住：

- wineboot 那一次**故意不带** `UMU_RUNTIME_UPDATE=0`——它是唯一必须被允许去拉运行时的调用；
- wineboot 那一次**故意不设** `PROTON_VERB`；而 vcredist 那两次**必须**设 `run`，否则共享 prefix 上
  只要有实例在跑，`wineserver -w` 就永不返回（见 `runInPrefix` 与 `umuCommandLine` 的注释）。

`pkg/umu` **不该**因此认识「显示」这个概念：显示以 `RunOptions.ExtraEnv []string` 进来即可，
让 `pkg/umu → pkg/display` 会给 `WarmPrefix` 加一个它根本用不到的依赖。

**落地说明（2026-09-05）**：签名如上，另有三处具体化——

- 多导出了一个 `umu.ErrNoInterpreter`：`RunInPrefix` 把「解释器都没解析出来」和「跑了但
  失败了」两种错误合并进同一个返回值，而调用方**必须**分得开——`applyVCRedistOverrides`
  会把后者喂给 `vcredist.ExitNote(umu.ExitCode(err))`，前者走到那里会得到 `-1`，
  报成一句「被信号杀了」的假线索。三个调用点都在包装前先 `errors.Is` 挡一道。
- 环境拼装抽成了 `(*Runtime).runEnv(prefix, opt)`，**只为可测**：
  `TestRunEnv_ZeroOptionsIsWarmPrefixShape` 钉住「零值 RunOptions 既不带
  UMU_RUNTIME_UPDATE 也不带 PROTON_VERB」，`TestRunEnv_VCRedistShape` 钉住反面，
  `TestRunEnv_DoesNotAliasInheritedEnv` 钉住连着跑两条命令不串味（vcredist 正是连着跑
  regedit 与安装器两条）。这三条正对着本节标的两处风险。
- `internal/runner` 侧留下 `vcRedistRunOptions(timeout, displayEnv)` 一个函数，
  两个调用点共用；原 `runInPrefix` 整段（含 `PROTON_USE_XALIA` 那段注释）删除，
  `vcredist_linux.go` 419 → 377 行。

---

## 5. Gap E：vcredist 的 DLL 判定 / 诊断，文件读取那半边

`pkg/vcredist` 已经拥有全部纯逻辑（`ClassifyHeader`/`CountOverrides`/`RegistryVersion`/`OverrideDLLs`），
但「给个路径读几个字节再交给它」的那半边还留在 `internal/runner`：`classifyDLL`、`nativeDLLPresent`、
`prefixSystem32`、`prefixHasVCRedistOverrides`，以及 `vcRedistStatus` 里扫 DLL 的循环。

§2 的表格当初把 `classifyDLL`/`nativeDLLPresent` 判为「已经是两行薄封装，搬不搬都行」，那是只看这
两个函数得出的结论；连同 `vcRedistStatus` 一起看，是约 50 行**零 build tag、零回调、可在 Windows
单测**的代码——`pkg/vcredist` 现有 `vcredist_test.go` 的风格可以直接续上。

**提议**（全部无 build tag）：

```go
func ClassifyFile(path string) DLLOrigin   // 原 classifyDLL
func InstalledIn(prefix string) bool       // 原 prefixHasVCRedistCfg（判据不变：探针 DLL 非 Wine 自带）
func OverridesApplied(prefix string) bool  // 原 prefixHasVCRedistOverrides
func Inspect(prefix, gameDir string) Info  // 原 vcRedistStatus 的只读部分
```

`Info.InstallerDisplay`/`InstallerBlocked` 两个字段**不**进 `Inspect`：它们来自 `planDisplay`，
是「诊断视图只问计划不动手」这条业务规则的产物，由 `internal/runner` 调完 `Plan()` 再填。

**落地说明（2026-09-05）**：按上表落地，新文件 `pkg/vcredist/inspect.go`（97 行，无 build tag），
`Managed` 同样留给调用方（本包不认识「custom 运行时」这回事）。另外两点——

- 包注释里那句「It does not know about umu, prefixes, or ...」改掉了 `prefixes`：现在它确实
  认识 Wine 前缀的目录布局（`drive_c/windows/system32`、`user.reg`、`system.reg`）。
  那是 Wine 机制不是 ASA 领域概念，与 `pkg/wineprefix` 同类；把判据和「读哪个文件」
  拆在两个包才是真的别扭。
- `prefixHasVCRedistOverrides` 整个删掉，`wineprefix.Config.HasVCRedistOverrides` 直接接
  `vcredist.OverridesApplied` —— 原来那层包装一行代码都没加。
- 新增 `pkg/vcredist/inspect_test.go`（155 行，**Windows 上也跑**），其中
  `TestClassifyFileShorterThanHeaderScan` 钉住 `io.ReadFull` 返回 `ErrUnexpectedEOF`
  但 n 有效那条边界 —— 原实现里那个 `if n == 0 && err != nil` 之前没有任何测试覆盖。
  `vcredist_linux.go` 377 → 310 行。

---

## 6. 结论 F（有意不做）：vcredist 编排整体下沉的再评估

PLAN 阶段 H 把 `ensureVCRedist`/`ensureVCRedistInstaller`/`applyVCRedistOverrides` 留在
`internal/runner`，理由是「深度依赖 `prefixDir`/`protonPath`/`umuInterpreter`/`acquireDisplay`，
这些全是还留在 internal 的概念」。2026-09-05 复评：**这四条已失效三条**——阶段 I 之后
`prefixDir`/`protonPath`/`umuRunPath`/`umuInterpreter` 都只是 `umu_linux.go` 里的一行转发；
只剩 `acquireDisplay` 是真业务规则，而它本来就设计成回调注入（PLAN §2：「显示获取以回调注入，
vcredist 包不 import display 包，两个机制包保持平级」）。Gap D 落地后，
`Credential`/`UserName`/`HomeDir`/`ChownPath`/`Interpreter` 也全归 `umu.Config`，
`vcredist.Config` 只需要 `{Dir, URL, SHA256, AutoDownload, Managed, *umu.Runtime, AcquireDisplay}`。

**但仍不执行**，理由换了一个，且这个理由搬不走：

> 这段编排里的 ASA 领域知识**全部集中在文案上**——「override 已经写好，普通实例不受影响；
> 但 **ArkApi 实例同样起不来**」、「请…，然后重跑 `asa-server setup`」、
> 「或设 `linux.install_vcredist: false`」。一个 `pkg/` 包不该知道本程序叫 `asa-server`、
> 有个 `setup` 子命令、配置键叫 `linux.install_vcredist`。把这些也做成注入的字符串，是为了过
> 准入线而把可读性交出去，属于 PLAN §7 警告的「看起来通用、实际上硬编码了领域字符串的假 pkg 包」
> 的变体。

次要风险两条，将来若执行需一并处理：把签名从 `prefixKey` 改成 `prefix` 路径本身是好事
（key→dir 的唯一转换点仍在 `wineprefix.Manager.Dir`），但 `CLAUDE.md` 明确写了 start 路径的
`EnsurePrefix`/`PrefixHasVCRedist`/`Options.PrefixKey` **三处必须同源**；以及 `vcredist.Info`
是 `runner.VCRedistInfo` 的类型别名、直接被 HTTP API 消费，改字段等于改接口契约。

**结论**：Gap D + Gap E 落地后，`vcredist_linux.go` 剩下的就是「两级安装的业务决策 + 面向用户的
中文文案」——那正是该留在 `internal/runner` 的东西。本条**不列入待办**，只作为「已评估、有意不做」
的记录，避免下次再从头评估一遍。

---

## 7. 建议执行顺序

1. **Gap D（`umu.RunInPrefix`）**——消除的是当下真实存在的重复，风险点集中、可独立验证
   （见 §4 的两处刻意差异），值得单独一个 commit。✅ 已执行
2. **Gap C（`pkg/display`）**——阶段 G 的补作业，性质同 Gap A。不依赖 Gap D。✅ 已执行
3. **Gap E（vcredist DLL 判定）**——零风险、零回调，可与 Gap C 合并进同一个 commit。✅ 已执行
4. 结论 F 不执行。

**执行后的净结果**（2026-09-05）：`internal/runner` 从 2828 行降到 2370 行（不含测试），
其中 `display_linux.go` 375→45、`vcredist_linux.go` 419→310；新增 `pkg/display`（432 行）
与 `pkg/vcredist/inspect.go`（97 行）。新增可在 **Windows 上运行**的单测 197 行
（`pkg/display/display_test.go` 42 + `pkg/vcredist/inspect_test.go` 155 —— 原先这些判据
只在 Linux 上有覆盖）。`go build`/`go vet` 双平台通过，
`go test ./pkg/... ./internal/runner/ ./internal/instance/`（除 `pkg/tail`）全绿。

> Gap C 与 Gap D/E 正交：显示在 Gap D 里只以 `RunOptions.ExtraEnv` 的形式出现，Gap E 根本不碰显示。
> 所以「先做 `pkg/display` 好让 D/E 更好做」这个直觉是不成立的——`pkg/display` 受益的是结论 F，
> 而 F 有意不做。

---

## 8. 验证方式（沿用 PLAN §8）

- `go build ./... && go vet ./...`（Windows）
- `GOOS=linux GOARCH=amd64 go build ./... && go vet ./...`（交叉编译）
- WSL2 真机：`go build ./... && go vet ./... && go test $(go list ./... | grep -v '^asa-server/pkg/tail')`
- `pkg/shareacl` 的单测已经在 `t.TempDir()` 内真实调用 `setfacl`/`chgrp`（工具缺失时 skip），
  不需要额外的 `ASA_TEST_*` 门控——见 §1 落地说明。
- Gap C 落地后，`pkg/display` 的测试要保住「`Plan()` 无副作用」那一条（原
  `TestDisplayStatusHasNoSideEffects`）；Gap D 落地后，需在 WSL2 真机跑一次 `asa-server setup`
  （覆盖 `WarmPrefix` 的 wineboot 路径）+ 一次 `asa-server verify-arkapi`
  （覆盖 `runInPrefix` 的两条路径）。
