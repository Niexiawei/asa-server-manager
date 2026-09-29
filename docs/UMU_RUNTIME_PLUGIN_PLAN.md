# `pkg/umuruntime`：Wine/Proton 运行时独立成包 + 运行时组件插件化

> 状态：🚧 **阶段 0–2b 已完成**（2026-09-29，分支 `refactor/umuruntime-plugins`，`6f36c3b`..`654c841`）；
> 阶段 3–6 未开始。实施中与设计的偏离见 §13「实施记录」。
> 相关文档：`docs/UMU_PREFIX_PLAN.md`（prefix 模式与已知缺陷）、`docs/XVFB_DISPLAY_PLAN.md`（显示解析与自管 Xvfb）、
> `docs/ARKAPI_LINUX_VCREDIST_PLAN.md`（VC++ 运行时）、`docs/RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md`（上一轮拆包，本文是它的续篇）。
> 用户原话里的包名是 `pkg/umnruntime`，按拼写错误处理，本文统一写作 **`pkg/umuruntime`**（见 §12 第 1 条）。
>
> **v2（2026-09-29 审阅后）**：① `pkg/display` 不再作为独立包保留，**整体并入 `plugins/xdisplay`**，
> 且插件自己持有 `*xvfb.Manager`（§3.1、§4.4）；② §12 的五个问题全部按推荐方案定案。

---

## 0. 一句话

上一轮拆包把**机制**（Xvfb、VC++ 安装、prefix 布局、umu 下载）下沉到了 `pkg/`，但**把这些机制串起来的那层编排**
仍然留在 `internal/runner`：「先建用户 → 装 umu → 装 Proton → 预取 SLR → 预热 prefix → 装 VC++」是写死的一串，
「VC++ 要显示」是一个手写闭包，「ArkApi 要显示、要 VC++」散在 `instance`、`installer`、`actions` 三处。
本方案把这层编排整体搬进新包 `pkg/umuruntime`，并把「显示」和「VC++ 运行时」改成**声明依赖的插件**：
插件说「我提供什么能力、我需要什么能力」，宿主按依赖图排序、兜底、诊断。
以后游戏需要新运行时（`d3dcompiler_47`、`dotnet48`、新版 VC++、字体……）= **写一个插件 + 注册一行**，
不再改 `wineprefix`、`runner`、`instance` 里的任何流程代码。

---

## 1. 现状：耦合在哪里

### 1.1 `internal/runner` 的对外面积

`internal/runner/runner.go` 导出 **45** 个函数/类型。按主题拆开看，只有启动那三个是「runner」本身的职责：

| 主题 | 导出符号 | 真正的归属 |
|---|---|---|
| 启动 | `Run` `GamePath` `LauncherIsDirect` `Options` `Handle` | runner ✅ |
| 运行时就绪 | `EnsureRuntime` `CheckRuntime` `Preflight` `Problem` `Blockers` `Advisories` `RuntimePython` | umu 运行时 |
| Wine prefix | `PrefixKeyFor` `EnsurePrefix` `RemoveInstancePrefix` `PrefixStatus` `PrepareSharedPrefixWrite` `ReconcilePrefixes` `SharesWinePrefix` `PrefixInfo` | umu 运行时 |
| **VC++** | `EnsurePrefixVCRedist` `PrefixHasVCRedist` `VCRedistStatus` `VCRedistInfo` `VCRedistDLLInfo` `DLLOrigin` `DLLMissing/Wine/Native` | **某一个组件** |
| **显示** | `DisplayStatus` `DisplayInfo` `StopManagedDisplay` + `Options.NeedsDisplay` | **某一个组件** |
| 降权用户/共享写 | `EnsureRuntimeUser` `VerifyRuntimeAccess*` `Chown*ForRuntime` `SharedAccess*` `PrepareSharedTree` `SharedTrees` `RuntimeUser*` | 身份/权限（本方案不动，见 §2.2） |

「VC++」和「显示」各占了一整组专属 API——每多一个运行时组件，这张表就多一组，调用方就多一处要改。

### 1.2 业务代码直接点名具体组件

| 位置 | 做了什么 | 问题 |
|---|---|---|
| `internal/instance/server.go:478` | `runner.DisplayStatus()` 不可用就阻断 ArkApi 启动 | 业务层知道「ArkApi 需要显示」这个**实现细节** |
| `internal/instance/server.go:488` | `runner.PrefixHasVCRedist()` 不满足就告警 | 同上；并且「阻断还是告警」的判断也写在业务层 |
| `internal/instance/server.go:578` | `Options{NeedsDisplay: arkAsaApiRunning}` | 启动选项是**某个组件**的开关，不是「这个 exe 需要什么」 |
| `internal/installer/verify_arkapi.go:138` | `NeedsDisplay: true`，注释「与实例启动同一个开关」 | 同一条规则抄了两份，靠注释保持一致 |
| `internal/actions/verify_arkapi.go:56/124/150` | 按名字调 `EnsurePrefixVCRedist` / `DisplayStatus` / `VCRedistStatus` | CLI 诊断按组件硬编码 |
| `internal/webapi/systemapi/systemapi.go:70` | 响应里单开一个 `"display"` 键 | 同上（前端**没有**消费它，已核对 `app/src`） |
| `internal/svcmgr/service.go:75`、`internal/webapi/actions.go:559` | 退出时 `runner.StopManagedDisplay()` | 生命周期按组件点名 |

### 1.3 编排写死，机制包也被迫认识 VC++

- `internal/runner/umu_linux.go:116` `ensureRuntime`：七步顺序写死在一个函数里，VC++ 在第 `:189` 行被**点名**调用。
- `internal/runner/vcredist_linux.go:46`：「VC++ 安装器要显示」是一个手写的 `AcquireDisplay` 闭包——依赖关系只存在于这段胶水里，
  无法被枚举、诊断或复用。
- **`pkg/wineprefix` 本应不认识 VC++，但它的 `Config` 里有四个 VC++ 专用字段**（`pkg/wineprefix/wineprefix.go:37-53`）：
  `Runtime`、`InstallVCRedist`、`EnsureVCRedist`、`HasVCRedistOverrides`；
  `EnsurePrefix` 的快路径（`wineprefix_linux.go:213`）、新建路径（`:243`）、`LowerNeedsWork`（`:773`）都按名字处理 VC++。
  再加一个运行时组件，这三处全部要改。

### 1.4 这种耦合已经造成了两个已知缺陷

两条都登记在 `docs/UMU_PREFIX_PLAN.md` 的「已知缺陷清单」里，**根因都是「每个组件自己带守卫/指纹，漏了就漏了」**：

1. **[P0] `verify-arkapi --install-vcredist` 无守卫地写共享底层**：`PrepareSharedPrefixWrite` 是调用方自觉调用的，
   `EnsurePrefixVCRedist` 这个入口没调。第四个写 lower 的入口漏了守卫。
2. **§8.1 `.lower-stamp` 只记 Proton 版本、不感知 VC++ 补装**：overlay 可写层的「底层变没变」指纹里没有 VC++ 的份，
   补装后已有实例的 `upper` 遮蔽新 lower，ArkApi 起不来且无提示。

插件化之后，这两件事由宿主**对所有插件统一做**（§5.3、§5.4），结构上不可能再漏。

---

## 2. 目标与非目标

### 2.1 目标

1. **`pkg/umuruntime`**：umu/Proton 运行时的编排（环境准备、就绪检查、prefix 生命周期、启动命令拼装、自检、诊断）整体成包，
   零 `internal/` 依赖、零 ASA 文案，满足 `pkg/` 准入标准（`docs/INTERNAL_LAYOUT_MIGRATION.md` §9）。
2. **插件接入**：显示（自管 Xvfb 及其候选链）、VC++ 运行时改为插件；插件**声明**依赖，宿主解析依赖图。
3. **业务层只说「这个 exe 需要什么能力」**：`instance`、`installer` 不再出现 `Display`、`VCRedist` 字样，
   ArkApi 的需求在一处定义、两处（实例启动、`verify-arkapi`）共用。
4. **新运行时零侵入**：新增一个 prefix 组件不改 `umuruntime`、`wineprefix`、`runner` 流程代码、`instance`。
5. **顺带修掉 §1.4 的两个缺陷**，且修法是结构性的。
6. **Windows 零回归**：Windows 上整个插件体系不存在，所有能力视为平台原生满足，行为逐字不变。

### 2.2 非目标

- **不做运行时动态加载**。这里的「插件」是**编译期注册**的 Go 接口实现，不是 Go 标准库的 `plugin`（`.so`）：
  后者不支持 Windows、要求宿主与插件用完全相同的工具链和依赖版本构建，在本项目里不可用。也不做外部进程插件。
- **降权用户 / 共享写 ACL（`pkg/sysuser`、`pkg/shareacl`）不插件化**。它们是**横切的身份与权限**，每个组件都要用，
  不是「某个能力的提供者」。本方案只把它们对 `umu`/`xvfb`/`vcredist` 重复注入的那组回调收拢成一个 `Identity`（§4.5）。
- **Steam Linux Runtime 预取（`pkg/steamrt`）不插件化**：它是 umu 自身下载的加速手段，不是 prefix 里的组件。
- **不改 prefix 三种模式的语义**、不改 launchgate、不改 ArkApi 冲突判定（`conflictingArkApiInstance` 仍是业务规则）。
- **不借机修 `docs/XVFB_DISPLAY_PLAN.md` 已知缺陷清单里的 Xvfb 自身问题**（`-ac`、看门狗退避、零日志……）。
  它们在 `pkg/xvfb` 内部，与包边界无关，另行处理；本方案只保证迁移不让它们变得更糟。

---

## 3. 目标形态

### 3.1 包布局

```
pkg/
├── umu/                      # 不变：umu-launcher/GE-Proton 下载、WarmPrefix、RunInPrefix、wineserver 探测
├── wineprefix/               # 小改：去掉四个 VC++ 专用字段，换成三个通用钩子（§5.2）
├── xvfb/                     # 不变：自管 Xvfb 进程机制（拉起/看门狗/认领/socket 目录 remount）
├── display/                  # ❌ 删除：整体并入 umuruntime/plugins/xdisplay（见下）
├── vcredist/                 # 小改：不动编排，仅 AcquireDisplay 回调的语义对齐 §4.4
└── umuruntime/               # 🆕 运行时宿主 + 插件框架
    ├── capability.go         #   Capability / Need / Phase / Readiness（无 build tag，runner.go 要引用）
    ├── graph.go              #   依赖图：校验、拓扑排序、环检测（无 build tag，Windows 上单测）
    ├── plugin_linux.go       #   插件接口（引用 pkg/umu 的类型，故 linux only）
    ├── host_linux.go         #   Host：Ensure / Check / EnsurePrefix / Provision / Command / Preflight / Status / Close
    ├── stamp_linux.go        #   底层 prefix 指纹（§5.4）
    ├── errors.go             #   NotReadyError、ErrCapabilityUnavailable 等哨兵/类型化错误（无 tag）
    └── plugins/
        ├── xdisplay/         #   🆕 显示插件 = 原 pkg/display 的全部逻辑 + 插件接口，自持 *xvfb.Manager，提供 win32.gui
        │   ├── xdisplay.go         # 原 display.go（Info/Target/Apply，**仍无 build tag**）
        │   ├── xdisplay_linux.go   # 原 display_linux.go（候选链 Plan/Acquire/Status/Stop）+ Plugin/EnvProvider/… 方法
        │   └── *_test.go           # 原 display_test.go / display_linux_test.go 随迁，只改包名
        └── vcrt/             #   🆕 VC++ 插件：适配 pkg/vcredist，提供 win32.msvcrt，软依赖 win32.gui
internal/
└── runner/                   # 瘦身为：跨平台启动门面 + 组合根（Config 映射、插件注册、ASA 文案、降权）
                              #   display_linux.go、xvfb_linux.go 两个胶水文件删除（§4.4）
```

**为什么 `pkg/display` 整体并入插件，`pkg/vcredist` 却只做适配器** —— 判据是「这个包离开插件框架还有没有独立的用处」：

- `pkg/display` 回答的唯一问题是「Wine 进程的显示从哪来」，这**就是** `win32.gui` 这个能力本身。它的唯一使用者是
  `internal/runner`（已核对：只有 `display_linux.go` 与 `runner.go` 的 `DisplayInfo` 别名 import 它），
  插件化之后唯一使用者变成插件。留着它，插件就只剩一层把 `Resolver` 的方法原样转发成接口方法的壳 ——
  那正是 `docs/RUNNER_GLUE_INLINE_PLAN.md` 删过一轮的那种薄转发。并入后 `Resolver` 直接实现插件接口，没有适配层。
- `pkg/vcredist` 则不同：`inspect.go` 是可独立使用的只读诊断（`verify-arkapi` 的 DLL 表），`vcredist.go` 是
  无 tag 的纯逻辑（URL 里的 sha256、PE 头判定、override `.reg` 生成）且有大量 Windows 上可跑的单测，
  安装编排还有两步顺序、成功判据这类自成体系的规则。它是「怎么装 VC++」的机制，插件只是「什么时候装、装进哪」。
- `pkg/xvfb` 也**不**并入：它是「管理一个 Xvfb 进程」的通用机制，自带一整张已知缺陷清单（`docs/XVFB_DISPLAY_PLAN.md`），
  修那些缺陷时不该牵动插件框架。

`vcrt` 放在 `umuruntime/plugins/` 下而不是改造 `pkg/vcredist` 本身，是为了让机制包**继续不认识插件框架**：
接口定义在消费方（`umuruntime`），适配器是薄的一层（预计 100~150 行），`pkg/vcredist` 的既有单测一行不动。

### 3.2 依赖图（无环；`A ──► B` 表示 A import B）

```
pkg/wineprefix ──► pkg/umu
pkg/umuruntime ──► pkg/wineprefix, pkg/umu, pkg/problem
plugins/xdisplay ──► pkg/umuruntime, pkg/xvfb, pkg/logger
plugins/vcrt     ──► pkg/umuruntime, pkg/vcredist ──► pkg/umu
internal/runner（组合根）──► pkg/umuruntime + plugins/* + pkg/sysuser, pkg/shareacl, pkg/linuxdeps
```

- `umuruntime` **不 import 任何插件**；插件 import `umuruntime` 只为拿接口与类型。
- 插件之间**不互相 import**：`vcrt` 需要显示，但它只认识能力 `win32.gui`，不认识 `xdisplay` 包。
- `pkg/vcredist` 不依赖 `umuruntime`（它仍然是纯机制包，可脱离插件框架单独使用/测试）。
- `xdisplay.go` 保持**无 build tag**：`Info` 仍要被 `internal/runner`（Windows 上返回「总是可用」）与 API 层在任何平台引用，
  `Target.Apply` 仍在 Windows 上单测 —— 这是原 `pkg/display/display.go` 不带 tag 的同一个理由，随文件一起迁过来。

---

## 4. 插件模型

### 4.1 能力（Capability）按「需要什么」命名，不按「谁来提供」命名

```go
// pkg/umuruntime/capability.go（无 build tag）
type Capability string

const (
	// CapGUI：被启动的 Windows exe 能创建 Win32 窗口。
	// Windows 上由窗口站原生满足；Linux 上 = Wine 能连上一个 X 显示。
	CapGUI Capability = "win32.gui"
	// CapMSVCRT：微软原生 VC++ 2015-2022 运行时可被加载。
	// Windows 上是系统级组件（用户按 ArkApi 官方要求自装）；Linux 上 = prefix 里装了/override 了。
	CapMSVCRT Capability = "win32.msvcrt"
)
```

用户原话是「VC++ 依赖 xvfb」。本方案落地为 **VC++ 插件依赖能力 `win32.gui`，由显示插件提供**，这比直接依赖 xvfb 更可插拔：

- 显示本来就不只来自 xvfb——显示插件（原 `pkg/display`）的候选链是「`linux.display` 点名 > 自管 Xvfb > `DISPLAY` 环境变量 > 扫描现成的」；
  依赖 xvfb 这个名字，在一台用宿主 `:0` 的机器上就是错的。
- 将来若换成别的显示方案（比如 Xwayland、headless Wayland 合成器），只要新插件也提供 `win32.gui`，VC++ 插件不用改。
- **能力名描述的是 Windows exe 的需要**，因此在 Windows 上天然有答案（平台原生满足），`runner.go` 这个无 build tag 的文件
  可以直接用它表达需求，不需要任何「Windows 上恒为 true」的桩函数（今天 `vcredist_windows.go`、`prefix_windows.go` 里的那些）。

### 4.2 插件接口：一个必选接口 + 按需实现的可选接口

```go
// pkg/umuruntime/plugin_linux.go
type Plugin interface {
	Name() string              // "xdisplay"、"vcrt"：日志、诊断、Status 的键
	Provides() []Capability
	Needs() []Need
}

type Need struct {
	Cap   Capability
	Phase Phase // PhaseProvision（往 prefix 里装东西时）| PhaseLaunch（每次启动时）
	// Soft：缺了照样执行，由插件自己降级。
	// vcrt 对 win32.gui 就是软依赖：DLL override（承重项）不需要显示，只有安装器那一步需要。
	Soft bool
}

// —— 以下按需实现，宿主用类型断言发现（io.WriterTo 式）——

// EnvProvider 提供「启动时往环境里加点东西」型的能力（显示）。
// Probe 与 Acquire 必须分开：自检/诊断只许问 Probe，绝不能顺手拉起进程
// （docs/XVFB_DISPLAY_PLAN.md 反复强调的不变量，此处写进接口形状）。
type EnvProvider interface {
	Probe() (ok bool, why string)                 // 只读、离线、无副作用
	Acquire(ctx context.Context) (Lease, error)   // 可能起进程
}

type Lease interface {
	Apply(env []string) []string // 显示插件即 display.Target.Apply（替换而非追加 DISPLAY/XAUTHORITY）
	How() string                 // 「本次使用 :99（自管 Xvfb）」，进日志
	Release()                    // 显示是进程级单例，今天为空操作；给将来的按次租用留口子
}

// PrefixProvisioner 提供「装进 Wine prefix 里」型的能力（VC++，以及将来的 d3dcompiler/dotnet/字体）。
type PrefixProvisioner interface {
	// Pending：这个 prefix 还有没有**值得做**的事。廉价、离线。
	// EnsurePrefix 的快路径与 LowerNeedsWork 只问它（今天是 hasVCRedistOverrides 的位置）。
	// 注意它与 Satisfied 不是一回事：VC++ 在无头机上永远装不上安装器那半，
	// 若用「装全了没有」当 Pending，每次启动都会重跑一遍 regedit 容器。
	Pending(prefix string) bool
	// Satisfied：能力在这个 prefix 里是否可用。Definitive=false 表示判据是启发式的
	// （VC++ 的 PE 头标记判断就是），宿主据此只告警不阻断。
	Satisfied(prefix string) Readiness
	// Fingerprint：prefix 里本插件相关内容的指纹，底层 prefix 指纹由它们合成（§5.4）。
	Fingerprint(prefix string) string
	Provision(ctx context.Context, pc *ProvisionContext) (Outcome, error)
}

type Preflighter interface{ Preflight() []problem.Problem } // 只读
type Statuser interface{ Status() Status }                  // 只读；Status.Data 放插件自己的结构化详情
type Closer interface{ Close() }                            // 进程退出时调用（逆拓扑序）
```

```go
type Readiness struct {
	OK         bool
	Definitive bool   // false = 启发式判据，调用方不应据此阻断
	Detail     string // 机制层原因，不含 ASA 文案
}

type ProvisionContext struct {
	Key    string // prefix 键（"" = 共享/底层）
	Prefix string // prefix 目录
	Umu    *umu.Runtime
	Logf   func(string, ...any)
	// Acquire 拿一个依赖能力的租约。没有提供者、或提供者 Probe 为否 → ErrCapabilityUnavailable
	// （包着原因）；有提供者但这次拿不到 → 原样返回提供者的错误。
	// 这正是今天 vcredist.ErrNoDisplay 与「有能力但这次没拿到」的两档区分，挪到框架层统一表达。
	Acquire func(ctx context.Context, c Capability) (Lease, error)
}

type Outcome struct {
	Plugin string
	Result OutcomeKind // Done | AlreadySatisfied | Skipped | Degraded
	Cause  error       // Skipped/Degraded 的原因；类型化，供组合根翻成人话
	Detail any         // 插件自己的结构化结果（vcrt 放 vcredist.Result）
}
```

### 4.3 依赖解析规则

在 `umuruntime.New(cfg, plugins...)` 时一次性校验，失败即返回 error（组合根里是编程错误，`MustNew` 直接 panic）：

1. 每个**硬**依赖（`Soft=false`）必须至少有一个已注册插件 `Provides` 它，否则报错。软依赖可以没有提供者。
2. 以「消费者 → 提供者」为边建图，**有环报错**（错误里列出环上的插件名）。
3. 同一能力多个提供者：按**注册顺序**取第一个 `Probe()` 为真的（今天用不到，规则先定下来，免得以后各插件自己发明）。
4. 拓扑序用于：Provision 的先后（提供者先于消费者）、Lease 叠加到环境变量的先后、`Close` 的**逆序**。

运行期的降级规则：

| 情况 | 行为 |
|---|---|
| 硬依赖的提供者 `Probe()` 为否 | 消费者本次**跳过**，`Outcome{Skipped, Cause: *DependencyUnavailableError}` |
| 软依赖不可用 | 消费者照常执行，`pc.Acquire` 返回 `ErrCapabilityUnavailable`，由插件自己降级 |
| 插件 `Provision` 返回 error | 按注册时的 `Criticality` 决定：`Optional`（VC++ 就是）只记录、不让 `Ensure` 失败；`Required` 让 `Ensure` 失败 |

`Criticality` 是**注册时**由组合根给的（`umuruntime.With(p, umuruntime.Optional)`），不是插件自己声明的：
「VC++ 装不上不算环境准备失败」是**本程序**的判断（不开 ArkApi 的用户占绝大多数，`docs/ARKAPI_LINUX_VCREDIST_PLAN.md` §3.2），
换一个用这套机制的程序可能有相反的判断。

### 4.4 两个插件的形状

**`plugins/xdisplay`**（提供 `win32.gui`；无依赖）—— 原 `pkg/display` 整体迁入，`Resolver` 更名为 `Plugin` 并**直接**实现接口

| 接口 | 实现 | 来源 |
|---|---|---|
| `EnvProvider.Probe` | 原 `Resolver.Plan()` 的候选链有候选即为真。原 `Plan()` **保留原名原签名**（返回完整候选链，`verify-arkapi` 要列备选），接口方法因此另起名 `Probe` 避免同名冲突 | `pkg/display/display_linux.go:122` |
| `EnvProvider.Acquire` | 原 `Resolver.Acquire()`；`blocked` → `ErrCapabilityUnavailable`，`err` → 原样；`Target` 即 `Lease` | `display_linux.go:275` + `internal/runner/vcredist_linux.go:46` 的归一逻辑 |
| `Preflighter` | Probe 为否 → 一条 `Warning:true` 的 Problem，**Detail 只含机制原因** | `internal/runner/preflight_linux.go` `checkDisplay`（ASA 文案留在组合根，见 §6） |
| `Statuser` | 原 `Resolver.Status()` 的 `Info` 放进 `Status.Data` | `display_linux.go:322` |
| `Closer` | 原 `Resolver.Stop()` → `xvfb.Manager.Stop()` | `display_linux.go:315` |

迁移是**搬家不是重写**：候选链顺序（点名 > 自管 Xvfb > 环境变量 > 扫描）、Plan/Probe 与 acquire 分离、失败沿链回退并 WARN、
`Target.Apply` 的替换语义全部原样保留；原 `pkg/display` 的两份单测随迁，除包名与构造方式外不改断言。

**`*xvfb.Manager` 改由插件自己持有**，不再由组合根 New 好注入：

- 今天注入的唯一理由是「进程内只有一个自管显示」要靠「组合根只持有一份 Manager」保证，而 `xvfbMgr` 除了喂给
  `display.New` 之外**没有任何其他使用者**（已核对：`internal/runner` 里其余 `xvfb.` 引用只有 `InstallHint`/`SocketDir` 两个常量）。
  插件化后组合根持有的是**唯一一份插件**，插件持有唯一一份 Manager —— 不变量由同一个机制保证，只是少了一层。
- 于是 `xdisplay.Config` = 原 `display.Config{Display}` + 原 `xvfb.Config` 的 `Bin/Screen/StatePath/AllowX11Remount` + `Identity`；
  `xdisplay.Plugin.Reconfigure(cfg)` 一次刷新两者（原来是 `displayResolver()` 先调 `xvfbManager()` 再 Reconfigure 的两步，
  漏掉前一步就会拿着旧 Xvfb 配置判断 —— 这个顺序陷阱随之消失）。
- 仍然是 `Reconfigure` 而非重新 `New`（`docs/RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md` §4.3 的承重不变量：
  重新 New 会泄漏一个 LockOSThread 的 spawn-loop 看门狗）。插件包注释要把这条写在 `New` 上：**每进程只许调一次**。
- `internal/runner/display_linux.go`（45 行）与 `xvfb_linux.go`（47 行）两个胶水文件删除，合并为组合根里的一段插件注册。

**`plugins/vcrt`**（提供 `win32.msvcrt`；**软依赖** `win32.gui @ PhaseProvision`）

| 接口 | 实现 | 来源 |
|---|---|---|
| `Pending` | `!vcredist.OverridesApplied(prefix)`（插件被配置为 inspect-only 时恒 false） | `wineprefix.Config.HasVCRedistOverrides` |
| `Satisfied` | `vcredist.InstalledIn(prefix)`，**`Definitive: false`** | `prefixHasVCRedist` + `server.go:488` 「只告警不阻断」 |
| `Fingerprint` | overrides 是否齐 + 原生 DLL 是否在 + 安装标记里的 sha256 | 新增（修 §8.1） |
| `Provision` | `vcredist.New(cfg{AcquireDisplay: 包一层 pc.Acquire(CapGUI)}).Ensure(...)`；`Result.Skip` → `Outcome{Degraded}` | `vcredist_linux.go` `ensureVCRedist` |
| `Statuser` | `vcredist.Inspect(...)` 放进 `Status.Data` | `vcRedistStatus` |

两步顺序（override 先、安装器后）与「成功只看 `InstalledIn` 不看退出码」都在 `pkg/vcredist.Installer.Ensure` 内部，**插件不碰**。
`linux.install_vcredist: false` 不是「不注册插件」，而是注册为 **inspect-only**：不下载不安装，但 `Satisfied`/`Status` 照常可问——
今天关掉它之后 `PrefixHasVCRedist` 仍会检查磁盘，这个行为要保留。

### 4.5 `Identity`：把重复注入的那组回调收拢

今天 `umuRuntimeFor`（`umu_linux.go:40`）和 `xvfbManager`（`xvfb_linux.go:24`）各写了一遍同样的五个闭包，
`vcRedistInstallerFor` 又注入了一次 `ChownPath`。收成一个值类型，由组合根构造一次，宿主分发给 `umu` 与各插件：

```go
type Identity struct {
	HomeDir    func() string
	ChildIDs   func() (uid, gid uint32, managed bool)
	Credential func() (*syscall.Credential, error)
	ChownPath  func(path string) error
	UserName   func() string
}
```

`pkg/umu.Config`、`pkg/xvfb.Config` 的对应字段**先不改**（改了就要动两个机制包的单测），由宿主/插件把 `Identity` 拆开填进去。

---

## 5. 宿主（`umuruntime.Host`）

### 5.1 API

```go
func New(cfg Config, plugins ...Registered) (*Host, error)
func (h *Host) Reconfigure(cfg Config)

// 环境准备（原 runner.ensureRuntime），见 §5.3 的步骤。
func (h *Host) Ensure(ctx context.Context, logf func(string, ...any)) error
// 离线就绪检查（原 runner.checkRuntime）。返回 *NotReadyError（类型化，不含「请运行 asa-server setup」）。
func (h *Host) Check() error

// prefix 生命周期：直通 wineprefix.Manager，但 Provision 类钩子由宿主填（§5.2）。
func (h *Host) Prefixes() *wineprefix.Manager
func (h *Host) EnsurePrefix(ctx context.Context, key string, logf func(string, ...any)) error
// 显式往某个 prefix 补装（原 runner.EnsurePrefixVCRedist）；only 为空 = 全部 provisioner。
// 共享底层时**内部**先过 PrepareSharedWrite（修 P0，§5.3）。
func (h *Host) Provision(ctx context.Context, key string, logf func(string, ...any), only ...string) ([]Outcome, error)

// 启动前检查某组能力在某个 prefix 下是否满足（原 DisplayStatus + PrefixHasVCRedist 两处）。
func (h *Host) CheckNeeds(key string, caps []Capability) []Unmet
// 拼一次启动（原 runner.umuCommandLine + run() 里的显示获取），返回 argv/env/凭证，exec 仍由 runner 做。
func (h *Host) Command(ctx context.Context, exe string, args []string, spec LaunchSpec) (*Command, error)

func (h *Host) Preflight() []problem.Problem // 各 Preflighter 汇总（linuxdeps 仍由组合根拼进来）
func (h *Host) Status() []PluginStatus      // 各 Statuser 汇总，含 Provides/Needs/Readiness
func (h *Host) Close()                      // 逆拓扑序调 Closer
```

`Unmet{Cap, Plugin, Readiness}`：`Readiness.Definitive` 为真才该阻断。这把今天写在 `instance/server.go` 注释里的
「显示是二值事实所以阻断、VC++ 是启发式所以只告警」变成了**数据**，而不是调用方要记住的规矩。

### 5.2 `pkg/wineprefix.Config`：四个 VC++ 字段 → 三个通用钩子

```go
// 删除：Runtime、InstallVCRedist、EnsureVCRedist、HasVCRedistOverrides
// 新增：
Provision        func(ctx context.Context, key, prefix string, logf func(string, ...any)) error // 新建/补装后调
Pending          func(prefix string) bool   // 快路径与 LowerNeedsWork 用；nil = 无事可做
LowerFingerprint func(lower string) string  // overlay 的 .lower-stamp 用；nil = 退回 PrefixMarker（今天的行为）
```

宿主把它们实现为「对所有 PrefixProvisioner 依拓扑序循环」。`custom` 运行时（prefix 归用户管）下宿主不装任何钩子，
与今天 `ensureVCRedist` 的 `cfg.Runtime != "umu"` 早退等价，只是判断从 VC++ 专用代码挪到了框架里一处。

### 5.3 `Ensure` 的步骤（与今天逐项对照）

| # | 今天（`umu_linux.go:116`） | 之后（`Host.Ensure`） |
|---|---|---|
| 1 | `ensureRuntimeUser` | `cfg.BeforeEnsure` 钩子（组合根注入，降权不进 pkg） |
| 2 | `custom` → `CheckRuntime` + `CheckSharedReady` | 同 |
| 3 | `AutoDownload=false` → 报错 | 同（类型化错误） |
| 4 | `EnsureUmu` / `EnsureGEProton` | 同 |
| 5 | `PrefetchSteamRuntime`（失败只降级） | 同 |
| 6 | `PrepareSharedWrite`；失败时 `LowerNeedsWork` 为否则跳过 | 同；`LowerNeedsWork` 里的 VC++ 分支换成「任一 provisioner `Pending`」 |
| 7 | `WarmPrefix(lower)` | 同 |
| 8 | `ensureVCRedist(lower)`（失败只记录） | **按拓扑序对 lower 跑所有 provisioner**，按 Criticality 处理失败，每个 `Outcome` 交 `cfg.OnOutcome` |
| 9 | — | 🆕 写 lower 的 `.provision-stamp`（§5.4） |

**P0 的结构性修法**：`Host.Provision(key="")` 与 `Ensure` 第 6 步走同一个私有函数，守卫在**函数内部**。
写底层 prefix 的入口从「四个调用点各自记得调守卫」变成「只有一个入口，守卫是它的第一行」。

### 5.4 底层指纹：修 `UMU_PREFIX_PLAN.md` §8.1

```
lower 指纹 = PrefixMarker(lower)                         // Proton 版本，今天的全部内容
           + ";" + 按插件名排序的 name "=" Fingerprint(lower)
```

`wineprefix` 通过 `LowerFingerprint` 钩子拿到它，替换 `ensureOverlayPrefix` 里的 `want := umu.PrefixMarker(lower)`。
底层补装了 VC++（或任何将来的组件）→ 指纹变 → 已有可写层命中 `readOverlayStamp != want` → 按现有逻辑重建。

⚠️ **升级时的一次性代价**：指纹格式变了，所有现存 overlay 可写层在升级后第一次启动时会重建一次（每个几秒到几十秒）。
可写层里没有用户数据（存档在 `instances/<名>/Save`），这是可接受的，但要写进 CHANGELOG。
**不做**「旧格式兼容比较」：那等于继续信任一个已知不感知 VC++ 的指纹。

### 5.5 `Command`：环境变量叠加顺序不能变

今天的顺序（`runner_linux.go` + `umuCommandLine`）是承重的——`display.Target.Apply` 的注释写明它必须在 `runtimeEnv` 之后。
`Host.Command` 保持：

```
base(InheritedEnv 或 Options.Env)
→ WINEPREFIX / GAMEID / PROTONPATH / UMU_RUNTIME_UPDATE=0 / PROTON_VERB=run / PROTON_USE_XALIA=0
→ WINEDLLOVERRIDES（逃生舱，最后一个同名变量生效）
→ runtimeEnv（仅降权时）
→ 各 Lease.Apply（按拓扑序）
```

`PhaseLaunch` 的需求里：`EnvProvider` 类能力在这里 `Acquire`，拿不到即返回错误（今天 `NeedsDisplay` 的 fail-fast）；
`PrefixProvisioner` 类能力只在 `Satisfied` **Definitive 为否定**时才拒绝，启发式的不在这里重复告警（告警是 `CheckNeeds` 的调用方的事，避免同一条日志打两遍）。

---

## 6. 文案边界：ASA 的话只在 `internal/runner` 里说

沿用 `docs/RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md` Part 2 §6 定下的规矩：pkg 侧返回**类型**，文案在组合根拼。本方案顺手把
那份文档记为「本轮未改」的一项（Part 2 §6.7：`pkg/umu`、`pkg/wineprefix` 四处硬编码「请运行 `asa-server setup`」）一起收掉。

| 今天的 ASA 文案 | 之后的 pkg 侧 | 之后的组合根（`internal/runner`） |
|---|---|---|
| `checkRuntime` 的「请运行 asa-server setup」 | `*NotReadyError{Component, Path}` | `describeNotReady(err)` |
| `checkDisplay` 里的「ArkApi 的 AsaApiLoader.exe…」「linux.display」「linux.allow_x11_remount」 | xdisplay 的 Problem 只含机制原因 | 注册插件时附 `Hints{Fix, Affects}`，或 Preflight 结果按插件名改写 |
| `ensureVCRedist` 的「ArkApi 实例同样起不来」「重跑 asa-server setup」 | `Outcome{Degraded, Cause}` | `OnOutcome` 里按 `Cause` 的类型翻译 |
| `server.go:478` 「AsaApiLoader.exe 在 Wine 下没有图形显示会静默退出」 | `Unmet{Cap: CapGUI, Readiness}` | `runner.DescribeUnmet(u)`，instance 再加上实例名 |

**Preflight 严重级别的来源**也顺带理顺：`checkDisplay` 那段长注释的结论是「严重级别要按**什么会坏**来判，不按检查对象本身」。
插件化后这可以做成数据：组合根登记「谁需要这个能力」（`runner` 知道 ArkApi 需要 `win32.gui`），宿主据此把一个能力缺失判成
「只影响可选功能 → 建议项」。今天这个判断是写死在 `Warning: true` 上的。

---

## 7. 业务层：只说「这个 exe 需要什么」

### 7.1 需求在一处定义

```go
// internal/installer/arkapi.go（installer 已持有 AsaApiLoaderPath/ArkApiInstalled，instance 已 import installer）
// ArkApiNeeds 是 AsaApiLoader.exe 对运行环境的全部要求。实例启动与 verify-arkapi 共用这一份 ——
// 今天它们靠 verify_arkapi.go:138 的一句注释「与实例启动同一个开关」保持一致。
var ArkApiNeeds = []runner.Capability{runner.CapGUI, runner.CapMSVCRT}
```

放 `installer` 而不是 `runner`：`runner` 的包注释明确说它「不区分 ArkAscendedServer.exe 与 AsaApiLoader.exe」，这条要守住。

### 7.2 实例启动（`internal/instance/server.go`）

```go
// 之前：① DisplayStatus 阻断 ② PrefixHasVCRedist 告警 ③ Options{NeedsDisplay: ...}
var needs []runner.Capability
if arkAsaApiRunning {
	needs = installer.ArkApiNeeds
}
for _, u := range runner.CheckNeeds(prefixKey, needs) {
	if u.Readiness.Definitive {
		return fmt.Errorf("实例 %s 启用了 ArkApi，但%s，已中止启动", instanceName, runner.DescribeUnmet(u))
	}
	logger.Warnf("实例 %s 启用了 ArkApi，但%s", instanceName, runner.DescribeUnmet(u))
}
...
handle, err := runner.Run(ctx, arkExe, args, runner.Options{Dir: ..., PTY: arkAsaApiRunning, Needs: needs, PrefixKey: prefixKey})
```

「ArkApi 冲突」（⓪）不动：它是业务规则（要读别的实例的配置），不是能力。

### 7.3 `runner` 对外 API 前后对照

| 之前 | 之后 |
|---|---|
| `Options.NeedsDisplay bool` | `Options.Needs []Capability` |
| `DisplayStatus()` `DisplayInfo` | `CapabilityStatus(CapGUI)` / `PluginStatus()`；`xdisplay.Info`（原 `display.Info`）在 `Status.Data` 里 |
| `StopManagedDisplay()` | `Close()`（关闭所有插件，逆拓扑序） |
| `EnsurePrefixVCRedist(ctx, key, w)` | `Provision(ctx, key, w, "vcrt")`（内含共享写守卫） |
| `PrefixHasVCRedist(key)` | `CheckNeeds(key, []Capability{CapMSVCRT})` |
| `VCRedistStatus(key, gameDir)` + `DLLOrigin`/`VCRedistInfo`/`VCRedistDLLInfo` 别名 | `PluginStatus()` 取 `vcrt` 的 `Data`（`vcredist.Info`）；别名删除，`actions` 直接 import `pkg/vcredist` 取类型 |
| — | 🆕 `CheckNeeds`、`DescribeUnmet`、`Capability`/`CapGUI`/`CapMSVCRT` |
| `EnsureRuntime` `CheckRuntime` `Preflight` 及 prefix 七件套 | **签名不变**，实现改为转调 `Host`（调用方零改动） |

导出符号从 45 个降到约 33 个，更重要的是**再加运行时组件不再增加导出符号**。

### 7.4 其余调用方

| 文件 | 改动 |
|---|---|
| `internal/installer/verify_arkapi.go:138` | `NeedsDisplay: true` → `Needs: ArkApiNeeds` |
| `internal/actions/verify_arkapi.go` | `[3] 图形显示`、`[4] VC++` 两节改为遍历 `ArkApiNeeds` 取 `PluginStatus`；已知插件（xdisplay 的备选链、vcrt 的 DLL 表）保留专门渲染，未知插件走通用一行 |
| `internal/actions/verify_arkapi.go:56` | `EnsurePrefixVCRedist` → `runner.Provision(..., "vcrt")`，**守卫随之自动生效（P0）** |
| `internal/webapi/systemapi/systemapi.go:70` | `"display"` 键换成 `"runtimePlugins": runner.PluginStatus()`（前端未消费，已核对） |
| `internal/svcmgr/service.go:75`、`internal/webapi/actions.go:559` | `StopManagedDisplay` → `Close` |
| `main.go` / `internal/actions/setup.go` / `internal/gui/gui.go` | `runner.Configure(...)` 字段不变（本方案不新增配置项） |

---

## 8. 必须保持的行为不变量（迁移时逐条核对）

| # | 不变量 | 出处 | 本方案里的落点 |
|---|---|---|---|
| 1 | 自检/诊断**绝不**拉起 X 服务端 | `XVFB_DISPLAY_PLAN` | `EnvProvider.Probe` 与 `Acquire` 分离；`Preflight`/`Status` 只许调 `Probe`/`Plan` |
| 2 | 进程内只有一个 `*xvfb.Manager`，配置变化走 `Reconfigure` | `RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN` §4.3 | 组合根持有唯一一份 xdisplay 插件，插件持有唯一一份 Manager；`xdisplay.New` 注释写明每进程只调一次，配置变化走插件的 `Reconfigure` |
| 3 | VC++：override 先、安装器后；成功只看 `InstalledIn` | `ARKAPI_LINUX_VCREDIST_PLAN` §2.7 | 在 `pkg/vcredist` 内部，插件不碰 |
| 4 | VC++ 失败不让环境准备失败，但必须响亮 | 同上 §3.2 | 注册为 `Optional`；`OnOutcome` 打 WARN |
| 5 | 无显示 → ArkApi 启动 fail-fast；VC++ 启发式 → 只告警 | 同上 §9、§3.6 | `Readiness.Definitive` |
| 6 | 写共享底层前必须过 `PrepareSharedWrite` | `UMU_PREFIX_PLAN` §12.4 | 宿主内唯一入口（修 P0） |
| 7 | 显示 env 在 `runtimeEnv` 之后叠加 | `display.Target.Apply` 注释 | §5.5 顺序表 |
| 8 | 普通启动 `PROTON_VERB=run` + `UMU_RUNTIME_UPDATE=0`；wineboot 用零值 `RunOptions` | `UMU_PREFIX_PLAN` Part 1 §2 | `Command` 照搬；`WarmPrefix` 不动 |
| 9 | `SharesWinePrefix` 由 `Dir` 推导，不写模式判断 | `runner_linux.go` `sharesWinePrefix` | 照搬 |
| 10 | `EnsurePrefix` 快路径只有几次 stat | `wineprefix_linux.go:203` | `Pending` 契约写明「廉价、离线」 |
| 11 | `custom` 运行时不改用户的 prefix | `vcredist_linux.go:82` | 宿主在 custom 下不装 provision 钩子 |
| 12 | `install_vcredist: false` 时仍可诊断磁盘现状 | `prefixHasVCRedist` | vcrt 的 inspect-only 模式 |
| 13 | `Configure` 整体覆盖 | `runner.go:621` | 不新增 Config 字段，调用点不动 |
| 14 | Windows 行为逐字不变 | `CLAUDE.md` | Windows 不构造 Host；`CheckNeeds` 恒空、`Needs` 忽略 |

---

## 9. 分阶段实施

每个阶段独立编译、独立提交；每阶段结束跑 §10 的验证。**阶段 1–3 对外行为零变化**，阶段 4 才动业务层。

| 阶段 | 内容 | 影响文件 | 行为变化 |
|---|---|---|---|
| **0** | `umuruntime` 的无 tag 部分：`Capability`/`Need`/`Readiness`/错误类型/依赖图 + 单测（缺提供者、环、软依赖、多提供者、拓扑序） | 新增 `pkg/umuruntime/{capability,graph,errors}.go` + `_test` | 无 |
| **1** | `Host` 骨架：把 `ensureRuntime`、`checkRuntime`、`umuCommandLine`、`umuRuntimeFor`/`wineprefixMgrFor` 的组合逻辑搬进 `Host`；`Identity` 收拢；`NotReadyError` 取代 pkg 侧硬编码文案。此阶段 VC++ 与显示**仍经临时钩子**以旧方式接入 | 新增 `host_linux.go`；改 `internal/runner/{umu_linux,runner_linux}.go`；`pkg/umu`、`pkg/wineprefix` 的文案点 | 无 |
| **2a** | **纯搬家**：`git mv pkg/display pkg/umuruntime/plugins/xdisplay`（保留 blame），改包名与 import；`*xvfb.Manager` 改由它内部 New，`Config` 吸收 xvfb 的四个字段 + `Identity`；删 `internal/runner/{display,xvfb}_linux.go`，组合根直接持有插件。**不加任何插件接口**，这一提交的 diff 应当只有移动与构造方式 | `pkg/display/*` → `plugins/xdisplay/*`；`internal/runner/{display,xvfb,preflight,vcredist}_linux.go`、`runner.go` | 无 |
| **2b** | 显示插件化：实现 `Plugin`/`EnvProvider`(`Probe`/`Acquire`)/`Preflighter`/`Statuser`/`Closer`；`run()` 的显示获取改走 `LaunchSpec`；`checkDisplay` 的机制部分下沉、ASA 文案留组合根。`Options.NeedsDisplay` **暂留**，内部翻译成 `Needs: {CapGUI}` | `plugins/xdisplay`；`internal/runner/{runner_linux,preflight_linux}.go` | 无 |
| **3** | VC++ 插件：`plugins/vcrt`；`wineprefix.Config` 四字段换三钩子；`ensureRuntime` 第 8 步改为遍历 provisioner；`Host.Provision` 带守卫 | 新增 `plugins/vcrt`；改 `pkg/wineprefix`、`vcredist_linux.go`、`umu_linux.go` | **修 P0**（守卫） |
| **4** | 业务层：`Options.Needs`、`CheckNeeds`、`DescribeUnmet`、`installer.ArkApiNeeds`；迁移 §7.4 全部调用方；删 `NeedsDisplay` 与 VC++/显示专属导出、`vcredist_windows.go`/`prefix_windows.go` 中不再需要的桩 | `internal/{instance,installer,actions,webapi,svcmgr}`、`internal/runner/runner*.go` | 无（文案可能微调） |
| **5** | 底层指纹：`LowerFingerprint` 钩子 + `.provision-stamp`；overlay 比较换指纹 | `pkg/umuruntime/stamp_linux.go`、`pkg/wineprefix` | **修 §8.1**；升级后 overlay 层一次性重建 |
| **6** | 文档：`CLAUDE.md` 的目录树与依赖图（删 `pkg/display` 条目、`internal/runner` 的 display/xvfb 胶水说明，新增 `pkg/umuruntime` 与两个插件）、`XVFB_DISPLAY_PLAN.md` 附录 Y 的路径映射、本文回填「实施记录 / 与设计的偏离」、`UMU_PREFIX_PLAN.md` 缺陷清单里 P0 与 §8.1 标记已修、CHANGELOG | 文档 | — |

阶段 5 单独成阶段而不并入 3：它是唯一有用户可见副作用（可写层重建）的一步，单独提交便于回滚与在 CHANGELOG 里单独说明。

---

## 10. 验证

### 10.1 每阶段必跑

```bash
# Windows（PowerShell）
go build ./... ; go vet ./internal/runner/... ./pkg/umuruntime/...
$env:GOOS="linux"; $env:CGO_ENABLED="0"; go build ./... ; Remove-Item Env:GOOS, Env:CGO_ENABLED
# WSL2（Linux 行为）
wsl -e zsh -lc 'cd /mnt/d/golang/asa-server && go test ./pkg/umuruntime/... ./pkg/wineprefix/ ./pkg/vcredist/ ./pkg/xvfb/ ./internal/runner/ ./internal/instance/ -count=1'
wsl -e zsh -lc 'cd /mnt/d/golang/asa-server && go test -race ./pkg/umuruntime/... ./internal/runner/ -count=1'
```

### 10.2 新增单测

- `graph_test.go`（无 tag，Windows 可跑）：缺硬依赖报错、软依赖可缺、环报错且列出环、拓扑序稳定、多提供者取第一个可用。
- `host_linux_test.go`：用**假插件**验证——Provision 顺序、Optional 失败不让 Ensure 失败、硬依赖不可用时消费者被跳过、
  `Provision(key="")` 在有活跃可写层时被守卫拒绝（P0 回归用例）、Close 逆序、Status/Preflight **不调用** `Acquire`（不变量 1）。
- `Command` 环境变量顺序：断言 `DISPLAY` 出现在 `HOME`（runtimeEnv）之后、`WINEDLLOVERRIDES` 在 umu 变量之后（不变量 7）。
- 指纹：底层补一个假 provisioner 的内容后，overlay stamp 不再相等（§8.1 回归用例）。
- 既有 `pkg/vcredist`、`pkg/xvfb`、`pkg/wineprefix` 单测**不改就应通过**——它们是本次迁移的回归护栏；
  原 `pkg/display` 的两份单测随 2a 迁到 `plugins/xdisplay`，**只许改包名与构造方式，不许改断言**；
  哪条需要改，要在实施记录里说明原因。

### 10.3 真机（Linux）

| 场景 | 期望 |
|---|---|
| 全新机器 `asa-server setup`（无 Xvfb） | 与迁移前日志逐行对照：VC++ 走 SkipNoDisplay 分支，override 写入，setup 成功 |
| 全新机器 `asa-server setup`（有 Xvfb） | VC++ 安装器跑通，`verify-arkapi --check-only` 全绿 |
| ArkApi 实例启动（shared / per-instance / overlay 各一次） | 能起来；无显示的机器上 fail-fast 文案等价 |
| overlay 模式下实例运行中执行 `verify-arkapi --install-vcredist` | **被拒绝**并说明原因（迁移前会直接写底层） |
| 升级后首次启动 overlay 实例 | 可写层重建一次，之后秒起 |
| 服务停止 | 自管 Xvfb 被关闭（`Close` 生效） |

---

## 11. 风险

| 风险 | 缓解 |
|---|---|
| 抽象过度：两个插件撑一个框架 | 接口只收今天真用到的四种可选行为；**不**预留 Setup/全局下载阶段、不做配置化插件列表（§12 问题 3）。第三个插件出现时再看要不要扩 |
| 迁移中环境变量顺序、守卫位置这类「承重细节」丢失 | §8 不变量表逐条对应单测；阶段 1–3 要求日志与迁移前逐行可对照 |
| 指纹格式变更导致一次性重建被误报为故障 | 阶段 5 独立提交 + CHANGELOG + 重建日志里写明「升级后的一次性重建」 |
| `Criticality` 在注册处给，将来有人把 VC++ 改成 Required 导致无头机 setup 失败 | 组合根的注册处写明理由并引用 `ARKAPI_LINUX_VCREDIST_PLAN` §3.2 |
| xdisplay 插件与 `pkg/xvfb` 已知缺陷（`-ac`、零日志）叠加 | 本方案不改 xvfb 行为；那些缺陷另行修复，插件化后修复点不变（仍在 `pkg/xvfb`） |

---

## 12. 已定问题（2026-09-29 审阅：全部按推荐方案）

1. **包名**：`pkg/umuruntime`（原话 `pkg/umnruntime` 按笔误处理）。
2. **`pkg/umu`、`pkg/wineprefix` 保留为下层机制包**，不并入 `pkg/umuruntime`：它们已各自有完整单测、边界清楚
   （下载/预热 vs 目录布局），并入只会让新包变大而不减少耦合。`umuruntime` 是它们之上的**编排层**。
   —— 与 `pkg/display` 的处理相反，判据见 §3.1「离开插件框架还有没有独立的用处」。
3. **插件清单不进 `config.yaml`**：插件的开关已经有语义化的配置项（`install_vcredist`、`display`/`xvfb_*`），
   再加一个清单会出现两处开关互相矛盾；新运行时插件本来就需要改代码，注册一行不构成额外负担。
4. **能力按需求命名**（`win32.gui`），理由见 §4.1。**不**提供 `Need.Provider` 钉死提供者的字段 ——
   等真出现「必须由某个特定插件提供」的需求再加。
5. **`ArkApiNeeds` 放 `internal/installer`**（零新包）。以后出现第二个带特殊需求的 exe，再抽出 `internal/launchprofile`。

---

## 13. 实施记录

### 13.1 阶段 0–2b（2026-09-29）

| 阶段 | 提交 | 内容 |
|---|---|---|
| 0 | `6f36c3b` | `pkg/umuruntime/{capability,plugin,errors,graph}.go` + `graph_test.go` |
| 1 | `a56b9d2` | `Host`（`host_linux.go`）+ `host_linux_test.go`；`internal/runner` 改为组合根；`NotReadyError` |
| 2a | `b4798ea` | `git mv pkg/display → pkg/umuruntime/plugins/xdisplay`；删 `internal/runner/{display,xvfb}_linux.go` |
| 2b | `654c841` | xdisplay 实现插件接口并注册进 Host；启动、VC++ 安装器、preflight、退出改走 Host |

验证：Windows `go build ./...` / `go vet` / `go test`；WSL2 `go vet ./internal/... ./pkg/umuruntime/...`、
`go test`（umuruntime、xdisplay、xvfb、vcredist、wineprefix、umu、runner、instance、installer、actions）与 `-race`（umuruntime、runner）全过。
另用一个一次性用例在 WSL2 上走了一遍**真实**路径：`Host.Acquire(win32.gui)` → 插件起了自管 Xvfb（`:0`，
经 `/tmp/.X11-unix` 只读 remount）→ `stopManagedDisplay`（即 `Host.Close`）后 Xvfb 退出、挂载还原为 `ro`、无残留进程。
**尚未做 §10.3 的真机验收**（setup / ArkApi 实例启动），留到阶段 3 之后一起做。

### 13.2 与设计的偏离

1. **插件接口放在无 tag 的 `plugin.go`**，而不是 §3.1 写的 `plugin_linux.go`：`Plugin`/`EnvProvider`/`Lease`/
   `Preflighter`/`Statuser`/`Closer` 都不引用 `pkg/umu` 的类型，放无 tag 文件后依赖图可以用假插件在 Windows 上单测。
   阶段 3 的 `PrefixProvisioner`/`ProvisionContext` 要引用 `*umu.Runtime`，届时放 linux 文件。
2. **三个方法改名**，都是为了让 `xdisplay.Resolver` 直接实现接口而不撞已有方法：
   - `Statuser.Status()` → **`Report()`**：`Resolver.Status() Info` 被原 `pkg/display` 的单测直接调用，按 2a
     「不改断言」的约束保留原名原签名。
   - `Lease.How()` → **`Describe()`**：`Target` 已有字段 `How`，同名方法不合法。
   - 原 `Resolver.Acquire() (Target, string, error)` → 未导出的 **`acquireChain()`**，把 `Acquire` 让给
     `EnvProvider.Acquire(ctx) (Lease, error)`。它原来只有 `internal/runner` 的胶水在调，已随 2b 删除。
3. **`xdisplay` 保留类型名 `Resolver`**（§4.4 写的是改名 `Plugin`）：改名会动到原单测的 `testResolver` 签名，没有收益。
4. **`xdisplay.Config` 内嵌 `xvfb.Config`**，而不是「xvfb 的四个字段 + `Identity`」：插件因此不依赖 `umuruntime.Identity`，
   身份回调由组合根从 `runtimeIdentity` 填进 `xvfb.Config`。
5. **`Identity.Credential` 返回 `(cred, home, err)`**（§4.5 写的是两值）：启动时 HOME 改写用的是这个 home，
   与原来 `resolveRuntimeCredential` 的语义逐字一致；传给 `pkg/umu`/`pkg/xvfb` 时丢掉 home。
6. **`NotReadyError` 定义在 `pkg/umu`**，`umuruntime.NotReadyError` 是它的别名：`pkg/umu`、`pkg/wineprefix` 要产生它，
   而它们在 `umuruntime` 之下，不能反向 import。`internal/runner` 的 `withSetupHint` 在 `CheckRuntime`/`EnsurePrefix`/
   `EnsureRuntime`/`Run` 四个出口统一追加「请运行 asa-server setup 完成环境准备」，文案与原来逐字相同。
   `RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md` Part 2 §6.7 记为「本轮未改」的那四处硬编码随之清掉。
7. **`EnvProvider.Acquire` 自己报「不可用」**，宿主不先调 `Probe`：否则候选链每次启动要算两遍。
   `Probe` 只给只读调用方用。
8. **`AutoDownload` 关闭时返回哨兵 `ErrAutoDownloadDisabled`**，原来那句带 `GET /api/system/preflight` 的英文错误由 runner 拼。
9. **启动时 per-instance/overlay prefix 目录不存在**的错误措辞改了：`runner: Wine prefix not found at … (call EnsureRuntime first)`
   → `umuruntime: Wine prefix not found at … (call EnsurePrefix first)`。这条正常路径上碰不到（启动前总会先 `EnsurePrefix`），
   原来建议的 `EnsureRuntime` 也不对（它只建共享前缀）。
10. **顺手去掉了 runner 里的第二个 Python 解析器**：`python_linux.go` 原有一个独立的 `pyfinder.Resolver`，
    与 `umu.Runtime` 内那个用同一个 `PythonBin`、各自缓存。现在 preflight 与启动问的是同一个。
11. **显示插件的 `Preflight` 不设 `Warning`**，交给组合根的 `describePluginProblem` 决定（连同 ArkApi 文案）：
    插件不知道谁需要显示。`TestDisplayProblemIsAdvisory` 继续钉住「是建议项」。
12. **`planDisplay` 保留**（`vcRedistStatus` 与 runner 的接线单测在用），`acquireDisplay` 删除；`stopManagedDisplay`
    改为 `Host.Close()`。`runner.DisplayStatus`/`StopManagedDisplay`/`Options.NeedsDisplay` 按计划留到阶段 4。
13. `hostFor(cfg)` 先刷新显示插件的配置再刷新宿主：宿主从不配置别人交给它的插件，组合根是唯一让它们跟上 `cfg` 的地方。

---

## 附录 A：以后加一个新运行时长什么样

假设某次游戏更新后需要 `d3dcompiler_47.dll`（UE 系游戏的常见需求）：

1. 新建 `pkg/umuruntime/plugins/d3dcompiler/`，实现 `Plugin` + `PrefixProvisioner`（下载 → `pc.Umu.RunInPrefix` 放 DLL + 写 override → 指纹）。
   不需要显示就不声明 `Needs`；需要就写 `Need{Cap: CapGUI, Phase: PhaseProvision, Soft: true}`。
2. 在 `umuruntime/capability.go` 加 `CapD3DCompiler Capability = "win32.d3dcompiler_47"`。
3. 组合根注册一行：`umuruntime.With(d3dcompiler.New(...), umuruntime.Optional)`。
4. 若只有 ArkApi 需要它：`installer.ArkApiNeeds` 加一项；若所有实例都需要：在实例启动的 `needs` 里无条件加上。

**不需要改的**：`pkg/wineprefix`（per-instance 补装、overlay 指纹、`LowerNeedsWork` 全部自动覆盖新插件）、
`Host.Ensure` 的步骤、共享写守卫、`runner` 导出 API、`systemapi`（`runtimePlugins` 自动多一项）、`verify-arkapi`（通用渲染自动多一行）。
今天做同样的事，要改的正是上面这张「不需要改」的清单。
