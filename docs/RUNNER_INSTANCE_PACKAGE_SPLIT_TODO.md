# `internal/runner` 拆包后续清单

> 依赖 `docs/RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md`（阶段 A–J 已于 2026-09-05 全部执行完成，
> 见该文档历史与提交 `a83c85a`..`6894907`）。本文档是**活动清单**，记录方案执行完之后盘点出的
> 真实缺口与可选卫生项；结论稳定后回填 PLAN，不在 PLAN 里直接改。
>
> **Gap A（`pkg/shareacl`）与 Gap B（vcredist 零散机制）均已于 2026-09-05 执行完成**——
> 见 §1、§2 末尾的落地说明。
>
> **2026-09-05 复评新增 Gap C/D/E/F**（§3–§6），**四项均已于同日执行完成**：起因是评估
> 「vcredist 编排能否整体下沉」，核对下来 PLAN 阶段 H 的否决理由已失效 3/4，并顺带发现三件
> 更该先做的事，以及 §0 表格里一处记错。**Gap F 的结论被推翻过一次**（先判「有意不做」，
> 理由是文案耦合；被指出后推翻——那是接口切错了，不是包边界问题），推翻过程保留在 §6.1。

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
| `vcredist_linux.go` | 141 | 见 §2/§4/§5/§6（均已执行，486→419→377→310→141 行） |

写这一节时认为真正的缺口只有 §1、§2 两项；2026-09-05 复评后增加 §3–§6 四项，其中 §3 是把
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

## 6. Gap F：vcredist 编排整体下沉（已执行）

> 本节记录了一次**结论被推翻**的过程，故意保留原始理由与推翻它的论证 ——
> 后来者要能看出「文案耦合」为什么不构成包边界，而不是只看到最终结果。

### 6.1 原结论与它的两次修订

PLAN 阶段 H 把 `ensureVCRedist`/`ensureVCRedistInstaller`/`applyVCRedistOverrides` 留在
`internal/runner`，理由是「深度依赖 `prefixDir`/`protonPath`/`umuInterpreter`/`acquireDisplay`，
这些全是还留在 internal 的概念」。

**第一次修订（2026-09-05 复评）**：这四条已失效三条 —— 阶段 I 之后
`prefixDir`/`protonPath`/`umuRunPath`/`umuInterpreter` 都只是 `umu_linux.go` 里的一行转发；
只剩 `acquireDisplay` 是真业务规则，而它本来就设计成回调注入（PLAN §2：「显示获取以回调注入，
vcredist 包不 import display 包，两个机制包保持平级」）。Gap D 落地后，
`Credential`/`UserName`/`HomeDir`/`ChownPath`/`Interpreter` 也全归 `umu.Config`。
当时给出的新反对理由是：

> 这段编排里的 ASA 领域知识**全部集中在文案上**——「override 已经写好…但 **ArkApi 实例
> 同样起不来**」、「请…，然后重跑 `asa-server setup`」、「或设 `linux.install_vcredist: false`」。
> 一个 `pkg/` 包不该知道本程序叫 `asa-server`、有个 `setup` 子命令、配置键叫
> `linux.install_vcredist`。把这些也做成注入的字符串，是为了过准入线而把可读性交出去。

**第二次修订（同日，被指出后推翻）**：这条理由站不住。Go 的类型化错误存在的意义正是这个 ——
「这段代码里唯一的领域知识是给用户看的句子」不说明它不可拆，只说明**接口切错了**：
`ensureVCRedist` 把「发生了什么」和「怎么跟用户讲」揉进了同一个 `logf`。

而且拆完比原状**更好**，不只是打平：「VC++ 没装成，因为这台机器没有显示」这个事实原先
只活在一行日志文本里，诊断接口拿不到；做成 `Result.Skip` 之后它是可检视的结构化结果。

### 6.2 落地形态

**`pkg/vcredist/install_linux.go`（387 行，新增）** —— 全部编排。三类跨界信息都类型化：

| 情形 | 以前 | 现在 |
|---|---|---|
| 本机没有显示能力 | `logf("跳过…：%s。", blocked)` + 三行 ArkApi 指引 | `Result{Skip: SkipNoDisplay, SkipCause: err}` |
| 有能力但这次没拿到 | 另外两行文案 | `Result{Skip: SkipDisplayUnavailable, SkipCause: err}` |
| 已经装好了 | 静默 `return nil` | `Result{Skip: SkipAlreadyInstalled, Installed: true}` |
| auto_download 关了 | 一句带 `linux.install_vcredist` 的错误 | `*AutoDownloadDisabledError{Dest, URL}` |
| 下载没有校验值 | 一句带 `linux.vcredist_sha256` 的警告 | `Config.OnUnverifiedDownload(url)` 钩子 |

方向相反的那一条也是类型：`Config.AcquireDisplay` 返回的 error 若
`errors.Is(err, vcredist.ErrNoDisplay)` 即「本机压根没有显示能力」，否则是「有能力但没拿到」。
**哪种算哪种是调用方的判断**（`checkDisplay` 把缺显示定为建议项，所以一台没装 Xvfb 的机器
走到 blocked 是常规路径），本包只负责分开这两档 —— `classifySkip` 一个函数，单测钉住。

`Config` 最终形状：`{Dir, URL, SHA256, AutoDownload, Umu *umu.Runtime, AcquireDisplay,
ChownPath, OnUnverifiedDownload}` —— 3 个回调 + 1 个运行时指针 + 4 个标量，与
`umu.Config`（5 回调）、`xvfb.Config`（3 回调）同量级。

**`internal/runner/vcredist_linux.go`（310 → 141 行）** —— 只剩三样：
`vcRedistInstallerFor`（组合根，把回调接上）、`ensureVCRedist`（把 `Result.Skip` 与
`*AutoDownloadDisabledError` 翻成人话）、`prefixHasVCRedist`/`vcRedistStatus`（诊断）。
`asa-server` / `setup` / `linux.install_vcredist` / `linux.vcredist_sha256` 四个本程序自己的
名字**只出现在这个文件里**。

### 6.3 三个执行时的判断

1. **`OnUnverifiedDownload` 做成钩子而不是 `Result` 里的字段**：顺序有意义 ——
   放进 `Result` 就变成事后才说，那时 24 MiB 已经无校验地下完了。也没有采用
   「`Config.SHA256Key string` 把配置键名传进去让 pkg 拼句子」的写法：那还是 pkg 在写
   面向本程序用户的话，只是短了点。
2. **`Installer` 每次现 New，不做包级单例**：它不持有任何跨调用状态，同
   `sysUserFor`/`pkg/sysuser.Manager`；与必须 `Reconfigure` 的 `umuRuntime`/`xvfbMgr` 相反
   （那两个持有活进程）。
3. **`pkg/vcredist` 从此有了一个带 build tag 的文件**：`install_linux.go` 依赖 `pkg/umu`，
   而 umu/Wine/Proton 没有 Windows 对应物。`vcredist.go` 与 `inspect.go` 仍无 tag、
   全平台可单测。`pkg/vcredist → pkg/umu` 无环 —— `pkg/umu` 与 `pkg/wineprefix` 对
   vcredist 都是**回调注入**，没有编译期依赖。

### 6.4 新增测试

`pkg/vcredist/install_linux_test.go`（117 行）。真跑一次安装需要 umu-run + GE-Proton +
已初始化的前缀，不适合单测；钉住的是**类型化契约**那一层，也正是本次唯一改了形状的东西：

- `TestClassifySkipSeparatesTheTwoCauses` —— 两种「没显示」不许混成一档；
- `TestAcquireDisplayDefaultsToNoDisplay` —— 没接回调 = 没有显示，不是 panic 也不是「有」；
- `TestAutoDownloadDisabledCarriesBothPaths` —— `Dest`/`URL` 都要带出来，否则文案又被锁死；
- `TestEnsureRequiresUmu` / `TestEnsureRejectsUninitializedPrefix` —— 这两种是 **error 不是
  Skip**：调用方编排顺序搞反了，不是环境不具备；
- `TestRunOptionsDifferFromWarmPrefix` —— 与 `umu.WarmPrefix` 的三处刻意差异，
  其中 `Verb: "run"` 与 `NoRuntimeUpdate` 是正确性而非偏好（见 §4）。

### 6.5 仍然留在 internal 的两条，以及为什么

- `cfg.InstallVCRedist` / `cfg.Runtime != "umu"` 两个前置开关：本程序的策略，不是 VC++ 的
  机制。调用方不想装就别调 `Ensure`。
- `vcRedistStatus` 里补的 `Managed` 与 `InstallerDisplay`/`InstallerBlocked`：同 §5 的理由。

### 6.6 一处行为变化

新增一行日志：拿到显示后打 `VC++ 运行时安装将使用 <How>`。以前 `disp.How` 在这条路径上被
丢掉了，而启动路径（`runner_linux.go`）一直是打的 —— 排障时「这次用的是自管 Xvfb 还是
宿主的 :0」是第一个要知道的事。除此之外没有行为改变。

### 6.7 顺手发现的一致性问题（本轮未改）

把「pkg 不得出现本程序自己的名字」这条规则应用到 vcredist 之后，`grep` 一遍发现
**`pkg/umu` 与 `pkg/wineprefix` 里还有四处面向用户的错误文本写着「请运行 `asa-server setup`
完成环境准备」**（`umu_linux.go` 两处、`wineprefix_linux.go` 两处）。按同一条标准，它们同样
应当是哨兵错误（如 `umu.ErrRuntimeMissing`）由 `internal/runner` 翻成指引。

本轮**没改** —— PLAN §7：不在拆包的同一次提交里「顺手」改别的。且它与 Gap F 不同：那四句
都是**阻断性错误**的文本，不像 vcredist 那两个 Skip 分支那样携带「该给用户看哪一条指引」的
分歧信息，收益小得多。记在这里供日后取舍。

---


## 7. 建议执行顺序

1. **Gap D（`umu.RunInPrefix`）**——消除的是当下真实存在的重复，风险点集中、可独立验证
   （见 §4 的两处刻意差异），值得单独一个 commit。✅ 已执行
2. **Gap C（`pkg/display`）**——阶段 G 的补作业，性质同 Gap A。不依赖 Gap D。✅ 已执行
3. **Gap E（vcredist DLL 判定）**——零风险、零回调，可与 Gap C 合并进同一个 commit。✅ 已执行
4. **Gap F（vcredist 编排整体下沉）**——原判「有意不做」，被推翻后执行；依赖 Gap D
   （`umu.RunInPrefix` 把注入面从 5 个降到 3 个回调）。✅ 已执行，且是**在 D/C/E 通过
   WSL2 真机验证之后**才动的 —— 这条路径在 Windows 上完全无法验证，不该让两层未验证的
   重构叠在一起，出问题二分不出是哪层。

**执行后的净结果**（2026-09-05）：`internal/runner` 从 2828 行降到 2370 行（不含测试），
其中 `display_linux.go` 375→45、`vcredist_linux.go` 419→141；新增 `pkg/display`（432 行）、
`pkg/vcredist/inspect.go`（97 行）与 `pkg/vcredist/install_linux.go`（387 行）。
新增可在 **Windows 上运行**的单测 197 行（`pkg/display/display_test.go` 42 +
`pkg/vcredist/inspect_test.go` 155 —— 原先这些判据只在 Linux 上有覆盖），
另有 Linux 侧新单测 187 行（`install_linux_test.go` 117 + `umu_linux_test.go` 新增 67 + 3）。`go build`/`go vet` 双平台通过，
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
  （覆盖 `runInPrefix` 的两条路径）。**D/C/E 已于 2026-09-05 真机验证：实例可正常启动。**
- **Gap F 的真机验证尚未进行**，需要覆盖的是三条从未在 Windows 上跑过的路径：
  ①无显示机器上 `asa-server setup` 打出的是 `SkipNoDisplay` 那三行（而不是另一档）；
  ②`linux.auto_download: false` + 本地无安装包时报的是带 `linux.install_vcredist` 的那句；
  ③有显示机器上安装成功且新增的「将使用 <How>」一行内容正确。
