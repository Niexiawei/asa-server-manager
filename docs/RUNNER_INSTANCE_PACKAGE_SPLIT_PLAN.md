# 拆包方案：`internal/runner` + `internal/instance` 单一职责重构

> 状态：待审阅 / 待执行
> 目标：把两个已经膨胀成"神包"的领域包，按**单一职责**拆成若干子包；纯逻辑、零领域依赖、
> 无全局状态的部分下沉到 `pkg/`（薄注入：不导入 `internal/*`，靠调用方传参数/小型 Config 结构体）。
> 原则：沿用 `docs/PACKAGE_RESTRUCTURE_PLAN.md` 的方式——**分层无环、分步渐进、每步独立编译提交**。
> 本文档只定方案，不动代码。

---

## 1. 背景与问题

### 1.1 `internal/runner`（34 个文件，约 6700 行）

`runner.go` 本身就是一个"总控神包"：`Config` 一个结构体塞了 9 套互不相关子系统的全部配置项
（umu/Proton、Wine 前缀、Xvfb、VC++ Redist、Python、运行时降权用户、共享目录 ACL），`Problem`/
`PrefixInfo`/`DisplayInfo`/`VCRedistInfo`/`RuntimeUserInfo`/`SharedAccessInfo` 等本该属于各子系统
自己的类型也全部定义在 `runner.go` 里，靠几十个一行转发函数（`func Xxx() T { return xxx() }`）
把内部实现"导出"给调用方。

| 文件（组） | 大小 | 实际职责 | 与"启动一个 exe"的关系 |
|---|---:|---|---|
| `runner.go` + `runner_{linux,windows}.go` | ~45K | 进程启动本体（exec/pty/umu-run 拼命令行）+ **9 套子系统的门面转发** | 核心 |
| `xvfb_linux.go`（+test） | 58K | 自管 Xvfb 虚拟显示：spawn/看门狗/认领/状态文件 | 无关——只是"给 Wine 一块显示" |
| `display_linux.go`（+test） | 41K | 显示解析链（点名/自管/环境变量/扫描）+ X11 握手 | 间接——Run 需要它决定要不要显示 |
| `vcredist.go` + `vcredist_{linux,windows}.go`（+test） | 28K | 装/查 Wine prefix 里的微软 VC++ 运行时 | 无关——ArkApi 的前置依赖 |
| `prefix.go` + `prefix_{linux,windows}.go`（+test） | 21K | Wine 前缀路径解析、创建、状态、GC | 间接——Run 要知道用哪个前缀 |
| `overlay.go` + `overlay_linux.go`（+test） | 29K | prefix_mode=overlay 的 overlayfs 挂载 | 是 prefix 的一种实现细节 |
| `steamrt.go` + `steamrt_linux.go`（+test） | 23K | Steam Linux Runtime 变体映射 + 预下载 | 无关——umu 内部下载的加速旁路 |
| `python_linux.go`（+test） | 16K | umu-run 用哪个 Python 解释器 | 无关——umu-run 的一个启动参数来源 |
| `umu_linux.go` | 27K | umu-launcher/GE-Proton 下载 + prefix 预热编排 | 间接——EnsureRuntime 的实现 |
| `runtimeuser_{linux,windows}.go`（+test） | 25K | Linux 降权账号管理 + 属主 chown | 无关——安全模型，不是"怎么启动" |
| `sharedaccess_{linux}.go`（+test） | 18K | 共享目录 ACL/setgid | 无关——同上，另一种机制 |
| `preflight_linux.go` | 12K | 汇总以上**全部**子系统的自检 | 无关——诊断工具，不是启动路径 |

结果：改一行 Xvfb 的日志格式，也要在"进程启动"这个包里重新编译链接；`Problem`/`Config` 这两个
被 9 套子系统共用的类型，成了任何人想把某个子系统拆出去都绕不开的锚点。

### 1.2 `internal/instance`（18 个文件，约 5300 行）

`instance` 的核心职责是"编排一个实例的生命周期"（`server.go` 的 Start/Stop/Restart/ForceStop/
Kill），但混进了几块自成一体、可以独立测试和理解的子问题：

| 文件（组） | 职责 | 是否认识"生命周期编排" |
|---|---|---|
| `server.go` | Start/Stop/Restart/ForceStop/Kill + 日志路径 + 配置同步 | 核心 |
| `common.go`（部分） | `IsStoppable`/状态 reconcile/`SaveWorldSafely`/`waitServerStartup`/`waitServerStopped` | 核心（状态机相关） |
| `common.go`（部分） | `GetAsaVersion`：从 exe 里抠 UTF-16 版本号字符串，纯二进制解析 | **不**——零领域知识 |
| `common.go`（部分） | `MonitorAndExtractModInfo`：tail 日志、正则提取 mod 列表、写 JSON | **不**——独立的后台旁路任务 |
| `gameproc.go` + `gameproc_{linux,windows}.go`（+test） | "哪个 PID 才是真游戏进程"，按平台完全不同的规则 | **不**——纯进程识别，文件头注释已把它当成独立子问题写 |
| `arkapilog.go` | ArkApi 日志文件命名规则 + 找最新一份 | **不**——零依赖，纯路径/文件名逻辑 |
| `asaapilog_{linux,windows}.go` | 把 ArkApi 的独立日志转抄进控制台日志（PTY 流处理） | **不**——只认 `console`/`logger`，不认领域概念 |
| `launchgate.go` | 共享 Wine 前缀下的启动串行闸门 + ArkApi 单实例冲突检测 | 半独立——是一条正交的并发控制策略，不是"这一台怎么启停" |
| `arkcache.go` | `pkg/arkcache` 的实例侧适配器（路径解析 + 进度格式化） | 已经是恰当粒度的单一职责，无需拆 |

`gameproc`/`arkapilog`/`asaapilog` 三组文件的头部注释已经明确把自己描述成独立子问题
（"挑错不是少个 PID""ArkApi 的业务日志不走控制台"），只是历史上没有物理拆包。

---

## 2. 目标目录结构

延续项目现有约定：`webapi` 用"父包 + 子包各自 `RegisterRouter`"的方式管理关联子域，本方案对
`runner`/`instance` 采用同样的**父目录嵌套子包**布局（而不是把十个新名字全部铺到 `internal/`
顶层），因为这些子系统本质上都是"Linux runner 这一个大主题"下的实现细节，只有 `Problem` 和
ASA 版本解析这两块是真正零领域依赖、可安全复用的叶子，下沉到 `pkg/`。

```
pkg/
├── problem/                  # 【新增，pkg 达标】诊断结果的通用载体
│   └── problem.go            #   Problem{Level,Code,Message,...} + Blockers()/Advisories()
│                              #   零领域依赖、无全局状态——纯数据结构 + 过滤函数
├── asaversion/                # 【新增，pkg 达标】从 exe 里解析 ASA 版本号
│   └── asaversion.go          #   GetVersion(exePath) (string, error) + (path,mtime,size)→版本 缓存
│                              #   原 instance/common.go 的 GetAsaVersion + asaVersionCache

internal/runner/                        # 保留：核心 Run/Options/Handle + 组合根 Config/Configure
│   ├── runner.go                       #   Options/Handle/Run/GamePath/LauncherIsDirect/
│   │                                   #   EnsureRuntime/CheckRuntime/SharesWinePrefix/Config/Configure
│   ├── runner_windows.go               #   Windows: exec/go-pty，各子系统 no-op 分派
│   ├── runner_linux.go                 #   Linux: umu-run 拼命令行、降权、显示注入（调用下面各子包）
│   │
│   ├── xvfb/                           # 【新增】自管 Xvfb 虚拟显示（进程内单例）
│   │   ├── xvfb_linux.go               #   原 xvfb_linux.go 原样迁入，unexported → 收敛成小 API：
│   │   │                               #   Acquire(cfg) (display string, err error) / Status() / Stop()
│   │   └── xvfb_linux_test.go
│   │
│   ├── display/                        # 【新增】显示解析链（点名/自管/环境变量/扫描）+ X11 握手
│   │   ├── display_linux.go            #   依赖 xvfb 包；原 planDisplay/acquireDisplay 收敛为
│   │   │                               #   Plan(cfg)（只读，供 preflight 用）/ Acquire(cfg) / Status() / Stop()
│   │   ├── display_linux_test.go
│   │   └── display_windows.go          #   原 runner_windows.go 里的 displayStatus/stopManagedDisplay 搬来
│   │
│   ├── vcredist/                       # 【新增】Wine prefix 里的微软 VC++ 运行时
│   │   ├── vcredist.go                 #   纯逻辑（下载地址解析/成功判据/DLL override 生成），无 build tag
│   │   ├── vcredist_linux.go           #   落盘/联网/跑 umu-run；依赖 display 包（装它需要显示）
│   │   ├── vcredist_windows.go
│   │   └── vcredist_test.go
│   │   ⚠️ 接口收敛：Ensure(ctx, cfg, prefixPath string, logf) 接收**已解析好的绝对路径**，
│   │   而不是 prefixKey——这样 vcredist 包不需要认识 wineprefix 包，避免和 wineprefix
│   │   互相调用形成循环导入（wineprefix.EnsurePrefix 内部会调 vcredist.Ensure）。
│   │
│   ├── wineprefix/                     # 【新增】Wine 前缀的路径/创建/状态/GC，含 overlay 模式
│   │   ├── prefix.go                   #   PrefixInfo、KeyFor()、EnsurePrefix()、Status()、Remove()
│   │   ├── prefix_linux.go             #   原 prefixDir()（现从 umu_linux.go 迁入并导出为 Dir()）+
│   │   │                               #   instancePrefixDir/ensurePrefix/prefixLocks/prefixCreationSlots
│   │   ├── prefix_windows.go
│   │   ├── prefix_test.go / prefix_linux_test.go
│   │   ├── overlay.go                  #   原样迁入（本就是"跟 steamrt.go 一样纯"的设计）
│   │   ├── overlay_linux.go
│   │   └── overlay_test.go
│   │   依赖：umu 包（CheckRuntime/WarmPrefix，见下）、vcredist 包（装 VC++）。
│   │
│   ├── umu/                            # 【新增】umu-launcher + GE-Proton 二进制供给 + 运行时就绪判定
│   │   ├── umu.go                      #   umuDir/umuRunPath/protonBaseDir/protonPath/ensureUmu/ensureGEProton
│   │   ├── umu_linux.go                #   EnsureRuntime()/CheckRuntime()/WarmPrefix()（供 wineprefix 调用）
│   │   │                               #   /steamLinuxRuntimeReady；依赖 steamrt 包 + vcredist 包
│   │   └── umu_windows.go              #   EnsureRuntime/CheckRuntime no-op
│   │
│   ├── steamrt/                        # 【新增，可下沉 pkg/ 但先留在此，见 §4 备注】
│   │   ├── steamrt.go                  #   变体映射/归档名/SHA256SUMS 解析，零领域依赖（无 build tag）
│   │   ├── steamrt_linux.go            #   落盘/HTTP；CacheDir 由调用方（umu 包）传入，不认识 BaseDir
│   │   └── steamrt_test.go
│   │
│   ├── python/                         # 【新增】umu-run 解释器发现
│   │   ├── python_linux.go             #   原 python_linux.go；pyCache 全局态原样保留（因此不进 pkg/）
│   │   └── python_linux_test.go
│   │
│   ├── runtimeuser/                    # 【新增】Linux 降权账号管理 + 独占目录属主
│   │   ├── runtimeuser_linux.go
│   │   ├── runtimeuser_windows.go      #   no-op：EnsureRuntimeUser/VerifyRuntimeAccess/Info 返回空值
│   │   └── runtimeuser_linux_test.go
│   │
│   ├── permissions/                    # 【新增】共享目录 ACL/setgid（原 sharedaccess）
│   │   ├── sharedaccess_linux.go
│   │   ├── sharedaccess_windows.go     #   no-op：现在混在 runtimeuser_windows.go 里的那几个桩函数搬来独立
│   │   ├── sharedaccess_linux_test.go
│   │   └── sharedaccess_test.go
│   │
│   └── preflight/                      # 【新增】跨子系统自检聚合（原 preflight_linux.go）
│       ├── preflight_linux.go          #   汇总 python/wineprefix/runtimeuser/permissions/vcredist/display
│       └── preflight_windows.go        #   恒为空
│
internal/instance/
│   ├── server.go                       # 保留：Start/Stop/Restart/ForceStop/Kill + 日志路径 + 配置同步
│   ├── common.go                       # 保留但瘦身：IsStoppable/reconcile*/SaveWorldSafely/
│   │                                   #   waitServerStartup/waitServerStopped/findServerPIDBySaveDir
│   ├── arkcache.go                     # 不动——已是恰当粒度的适配器
│   │
│   ├── gameproc/                       # 【新增】"哪个 PID 是真游戏进程"
│   │   ├── gameproc.go                 #   ArkExeName/AsaApiLoaderExeName（原 common.go 里的两个常量导出到这）
│   │   ├── gameproc_linux.go
│   │   └── gameproc_windows.go
│   │
│   ├── arkapilog/                      # 【新增】ArkApi 独立日志：发现 + 转抄进控制台日志
│   │   ├── arkapilog.go                #   Dir()/Newest()/ErrNoLog（原 arkApiLogDir/newestArkApiLog）
│   │   ├── asaapilog_linux.go          #   StartLogging()（原 startAsaApiLogging，Linux 转抄实现）
│   │   └── asaapilog_windows.go        #   StartLogging()（Windows：PTY 本体即业务日志，直接落盘）
│   │
│   └── launchgate/                     # 【新增】共享前缀启动闸门 + ArkApi 单实例冲突检测
│       └── launchgate.go               #   Acquire()/Precheck()/Conflicting()
│                                       #   依赖：cfgpkg、internal/installer、procpkg、
│                                       #   internal/runner/wineprefix（KeyFor/SharesPrefix）
│
│   （可选、优先级低，见 §6）
│   └── modinfo/                        # MonitorAndExtractModInfo + ModInfo，BaseDir 改为参数注入
```

---

## 3. 分层依赖（无环）

### 3.1 `runner` 子树

```
pkg/logger, pkg/download, pkg/archive, pkg/procx, pkg/problem   # 叶子
        │
runner/steamrt        # 只需一个缓存目录路径（调用方传入），零 internal 依赖
runner/python          # 只需 pyCache 自己的状态 + 可选 PythonBin 覆盖
runner/xvfb            # 只需 BaseDir 等几个字段 + pkg/logger
runner/runtimeuser     # 只需 RuntimeUser/UID/GID 等字段 + pkg/logger
runner/permissions     # 只需共享目录列表 + pkg/logger
        │
runner/display  ──depends──▶ runner/xvfb
runner/vcredist ──depends──▶ runner/display          （装 VC++ 需要一块能用的显示）
runner/umu      ──depends──▶ runner/steamrt, runner/vcredist
runner/wineprefix ─depends─▶ runner/umu（CheckRuntime/WarmPrefix）, runner/vcredist（装 VC++）
        │
runner/preflight ─depends─▶ runner/python, runner/wineprefix, runner/runtimeuser,
                             runner/permissions, runner/vcredist, runner/display, pkg/problem
        │
runner（核心）   ─depends─▶ runner/umu, runner/wineprefix, runner/display, runner/xvfb（间接）,
                             runner/runtimeuser, runner/python
                             ⚠️ 核心 runner 包**不**依赖 preflight（preflight 在它之上，调用方
                             各自按需 import：webapi/systemapi、gui、actions/setup 直接
                             `import ".../runner/preflight"`，不经 runner 转发）
```

关键点：`vcredist` 包不依赖 `wineprefix`（接口收敛为接收绝对路径，见 §2），所以
`wineprefix ⇄ vcredist` 不构成环；`umu` 与 `wineprefix` 之间也是单向（`wineprefix` 依赖 `umu`，
反过来 `umu` 从不引用 `wineprefix` 的类型，`prefixDir`/`Dir()` 这个路径解析函数本体迁到
`wineprefix` 包，`umu` 包需要时调用 `wineprefix.Dir(cfg, key)`）。

### 3.2 `instance` 子树

```
pkg/procx, pkg/console, pkg/logger, pkg/asaversion, pkg/problem   # 叶子
        │
instance/gameproc     # 只需 pkg/procx + 自己的两个 exe 名常量
instance/arkapilog    # 只需 pkg/console + pkg/logger
        │
instance/launchgate ──depends──▶ config, installer(procpkg), runner/wineprefix
        │
instance（核心）─depends─▶ instance/gameproc, instance/arkapilog, instance/launchgate,
                            instance/arkcache, pkg/asaversion,
                            config, process, rconx, state, mirror, installer, runner
```

`instance` 核心包对 `runner` 的依赖不变（`server.go` 已经在用 `runner.Run`/`runner.SharesWinePrefix`
等），只是 `runner` 内部变薄了。`instance/launchgate` 绕过核心 `instance` 包直接依赖
`runner/wineprefix`——这是刻意的：launchgate 关心的是"这个实例用哪个前缀 key、是否共享前缀"，
和"怎么启停一个实例"（`instance` 核心）是两件事，不需要经过 `instance` 包中转。

---

## 4. 关键设计决策

1. **`Config`/`Configure` 保留在 `runner` 包做组合根，外部调用点零改动。**
   `main.go`/`internal/actions/setup.go`/`internal/gui/gui.go` 三处已经在用一个字段齐全的
   `runner.Config{...}` 字面量调 `runner.Configure(cfg)`（且各自的注释都写明"整体覆盖，字段必须
   给齐"）。拆包后 `runner.Config` 的**外部形状不变**，`runner.Configure(cfg)` 内部把字段切片
   分发给各子包自己的 `Configure()`（例如 `xvfb.Configure(xvfb.Config{BaseDir: cfg.BaseDir,
   XvfbBin: cfg.XvfbBin, ...})`）。这就是"pkg 层允许薄注入"的具体做法：子包不导入
   `internal/appconfig`/`internal/config`，只认自己那份小 Config，由上层组合根负责搬运字段。
   代价是 `runner.go` 里会多出一段"字段搬运" glue code，但换来的是每个子包能独立编译、独立测试、
   独立被非 runner 的代码引用（例如 CLI 子命令）而不用拖着整个 `Config`。

2. **`Problem` 类型下沉到 `pkg/problem`，是解耦的关键一步。**
   现状里几乎每个子系统的自检函数都返回 `[]runner.Problem`，这个共享返回类型定义在最终要拆掉的
   `runner.go` 里，是任何子包拆分都绕不开的循环依赖源头。搬到 `pkg/problem` 后，`Blockers()`/
   `Advisories()` 这两个纯过滤函数也一并搬过去；`runner` 包和 `runner/preflight` 包都改成
   `import "asa-server/pkg/problem"`，`runner.Problem` 变成 `= problem.Problem` 的类型别名
   （过渡期保留，见 §6 阶段划分）或直接删除、调用方改用 `problem.Problem`。

3. **`vcredist` 接口从"传 prefixKey"改成"传绝对路径"，切断和 `wineprefix` 的双向依赖。**
   现状 `ensureVCRedist(ctx, cfg, prefixKey, logf)` 内部自己再调 `prefixDir(cfg, prefixKey)` 算出
   路径；改造后 `vcredist.Ensure(ctx, cfg, prefixPath string, logf)` 直接接收调用方（`wineprefix`
   或 `umu`）已经算好的绝对路径。这是本方案里**唯一**需要动函数签名而不是单纯搬文件的地方，
   其余子系统之间目前都没有双向调用，原样搬迁即可。

4. **`steamrt`/`python` 的缓存目录同理，由调用方传入而不是自己拼 `cfg.BaseDir`。**
   `steamrt_linux.go` 现在的 `umuCacheDir(cfg Config)` 拼的其实是 umu 的缓存目录，语义上属于
   `umu` 包，不属于 `steamrt` 包自己。迁移后 `steamrt.Prefetch(ctx, cacheDir string, logf)` 只认
   一个路径参数，`umu` 包负责算出这个路径再传进去——这样 `steamrt` 包可以在 `go build` 层面完全
   不知道"BaseDir"这个概念，为将来它下沉到 `pkg/steamrt` 铺路（本方案第一步先放在
   `internal/runner/steamrt`，见 §6 备注，不强求一步到位）。

5. **Windows 桩函数要按新包边界拆开，不能整块照搬。**
   现状 `runtimeuser_windows.go` 里混着 `sharedaccess_*` 的 no-op 桩（`prepareSharedTree`/
   `sharedTrees`/`sharedAccessStatus`），`runner_windows.go` 里混着 `display`/`python` 的 no-op 桩
   （`displayStatus`/`stopManagedDisplay`/`runtimePython`）。拆包时要把这些桩函数**按它们真正
   服务的新包**分别归位（`display_windows.go`、`permissions/sharedaccess_windows.go`、
   `python/python_windows.go` 等），而不是跟着物理文件名走。这是最容易漏、编译期报"缺函数"才会
   发现的一类问题，迁移时逐包核对 `//go:build windows` 与 `//go:build linux` 两侧函数签名一一对应。

6. **全局状态跟着实现走，不要跨包共享。**
   `xvfb` 的单例句柄、`python` 的 `pyCache`、`wineprefix` 的 `prefixLocks`/`prefixCreationSlots`、
   `instance/launchgate` 的 `launchGate`、`pkg/asaversion` 的版本缓存——这些包级全局变量随各自的
   实现文件搬到新包即可，天然不跨包共享，无需改造成显式传参。这也是为什么 `xvfb`/`python`/
   `runtimeuser`/`permissions` 这几个包虽然零 `internal` 依赖，仍然放在 `internal/runner/` 下而不是
   `pkg/`——项目对 `pkg/` 的准入标准明确要求"无全局状态与生命周期"（见 `docs/INTERNAL_LAYOUT_MIGRATION.md`
   §9），这几个包都不满足。

---

## 5. 外部调用点变更清单

### 5.1 依赖 `runner` 包的文件（需要按新函数归属改 import + 调用前缀）

| 文件 | 现状调用 | 迁移后 |
|---|---|---|
| `internal/actions/perms.go` | `runner.SharedAccessStatus/SharedTrees/PrepareSharedTree` | `permissions.Status/Trees/Prepare` |
| `internal/actions/prefix.go` | `runner.PrefixStatus/PrefixInfo/RemoveInstancePrefix` | `wineprefix.Status/Info/Remove` |
| `internal/actions/setup.go` | `runner.Configure(runner.Config{...})`（+ 可能调用 preflight） | `runner.Configure` 签名不变；若直接读 Problem，改 `preflight.Run()` |
| `internal/actions/verify_arkapi.go` | `runner.EnsurePrefixVCRedist/PrefixHasVCRedist/VCRedistStatus` | `vcredist.Ensure/HasVCRedist/Status` |
| `internal/gui/gui.go` | `runner.Configure(runner.Config{...})` | 不变（组合根签名不变） |
| `internal/installer/*.go` | `runner.Run`/`EnsureRuntime` 等核心启动 API | 不变（留在核心 `runner` 包） |
| `internal/svcmgr/service*.go` | 需确认具体调用（多半是 `CheckRuntime`） | 不变 |
| `internal/webapi/systemapi/systemapi.go` | `runner.Preflight/CheckRuntime/Blockers/RuntimeUserStatus/RuntimeUserProblems/RuntimePython/DisplayStatus` | `preflight.Run()` 聚合 + `problem.Blockers()`；`runtimeuser.Status/Problems`、`python.Status`、`display.Status` |
| `internal/webapi/instanceapi/instanceapi.go` | 需确认具体调用 | 按上表规则对应改 |
| `main.go` | `runner.Configure(runner.Config{...})` | 不变 |

### 5.2 依赖 `instance` 包的文件

`internal/actions/actions.go`、`arkapicache.go`、`environment.go`、`internal/batchmanage/manager.go`、
`internal/countdown/run.go`、`internal/schedule/scheduler.go`、`internal/updatemanage/manager.go`、
`internal/webapi/{instanceapi,logapi,serverapi}/*.go` ——这些调用的都是 `instance` 包**已导出的
稳定 API**（`StartServer`/`StopServer`/`RestartServer`/`PrecheckStart`/`GetGameLogFilePath` 等），
拆包只动包内部实现，这些调用点**不需要改动**。唯一例外：如果 `webapi/serverapi` 或别处曾经
直接引用过 `instance` 包里即将搬走的非导出细节（不太可能，Go 不允许跨包用非导出标识符），
可以确认为零改动。

---

## 6. 迁移步骤（分阶段，每阶段独立编译 + 提交）

> 顺序原则：先落叶子（零依赖），再落中间层，最后落组合根；每一步跑一次
> `go build ./... && go vet ./...`，Linux 专属代码额外在 CI 的 Linux 交叉编译或真机上跑一次
> `GOOS=linux go build ./...`。

- **阶段 A：抽取 `pkg/problem`。**
  把 `runner.go` 里的 `Problem`/`Blockers`/`Advisories`/`filterProblems` 剪切过去，`runner` 包内
  用类型别名 `type Problem = problem.Problem` 过渡，所有 `[]Problem` 返回值不用动。这一步不引入
  任何新目录以外的调用点改动，风险最低，验证"提取共享类型"这条路径本身是通的。

- **阶段 B：抽取 `pkg/asaversion`。**
  搬 `GetAsaVersion`/`asaVersionCache`/`asaVersionCacheEntry` 出 `instance/common.go`，
  `instance` 包里的 `GetInstanceAsaVersion` 改成调用 `asaversion.GetVersion(exePath)`。零外部调用点
  改动（`GetAsaVersion` 本来就没被 `instance` 包以外的代码直接引用，`GetInstanceAsaVersion` 签名
  不变）。

- **阶段 C：抽取 `instance/gameproc`、`instance/arkapilog`。**
  两组文件互相独立，可并行做。`server.go`/`common.go` 里对应的调用点改成
  `gameproc.Query(...)`/`arkapilog.Dir(...)` 等。这一步验证"跨平台 build tag 文件搬家"的流程。

- **阶段 D：抽取 `instance/launchgate`。**
  依赖 `internal/runner`（还是老的未拆包状态也行——`launchgate` 只用
  `runner.SharesWinePrefix`/`runner.PrefixKeyFor` 这两个稳定导出函数，不关心 `runner` 内部怎么拆），
  所以这一步可以在阶段 E~L（runner 内部拆分）之前或之后做，顺序自由。

- **阶段 E：`runner` 内部抽取零依赖叶子——`xvfb`、`python`、`runtimeuser`、`permissions`、
  `steamrt`。** 五个包互相独立，可并行。每个包对外先只暴露迁移前 `runner.go` 已经导出的那组
  函数（`xvfb.Status`/`Stop`，`python.Status`，`runtimeuser.Status`/`Problems`/`Ensure`，
  `permissions.Status`/`Trees`/`Prepare`），`runner.go` 里原来的转发函数改成调用这些新包，
  外部调用点（§5.1 表格）在这一步同步改掉。

- **阶段 F：抽取 `vcredist`（先改签名再搬家）。**
  先在原地把 `ensureVCRedist(ctx, cfg, prefixKey, logf)` 改成
  `ensureVCRedist(ctx, cfg, prefixPath string, logf)`，调用方（`prefix_linux.go`、
  `umu_linux.go`）在原地改成"自己算路径再传参"，跑一遍测试确认行为不变，再整体搬进
  `runner/vcredist` 包。把"改签名"和"搬包"拆成两步，出问题时更容易定位是逻辑变了还是搬家搬错了。

- **阶段 G：抽取 `display`（依赖阶段 E 的 `xvfb`）。**

- **阶段 H：抽取 `umu`（依赖阶段 F 的 `vcredist`、阶段 E 的 `steamrt`）。**
  `prefixDir`/`instancePrefixDir` 这两个函数现状分别在 `umu_linux.go`/`prefix_linux.go`，这一步
  统一迁到下一阶段的 `wineprefix` 包，`umu` 包改成调用 `wineprefix.Dir(cfg, key)`——也就是说
  阶段 H 和阶段 I 有一个函数需要协调着搬，建议合并成一次提交处理这两个包之间的边界，或者阶段 H
  先把 `prefixDir` 保留在 `umu` 包本地一份（临时重复），阶段 I 落地后再删掉重复定义。

- **阶段 I：抽取 `wineprefix`（含 overlay，依赖阶段 H 的 `umu`、阶段 F 的 `vcredist`）。**
  外部调用点（`actions/prefix.go`、`actions/verify_arkapi.go`、`instance/launchgate`）在这一步改。

- **阶段 J：抽取 `preflight`（依赖 E/F/G/I 的全部产出）。**
  `webapi/systemapi`、`gui`、`actions/setup` 改成直接 `import ".../runner/preflight"`。

- **阶段 K：瘦身 `runner.go`。**
  删掉已经全部转发出去、不再被外部引用的旧函数和类型定义，`Config` 拆分成"核心字段 + 各子包
  Configure 调用"，跑一次全量 `go build ./... && go vet ./...` 加现有测试套件，确认没有遗留的
  死代码或悬空 import。

- **阶段 L（可选，低优先级）：`instance/modinfo`。**
  `MonitorAndExtractModInfo` 改造成接收 `baseDir string` 参数而不是直接读 `cfgpkg.BaseDir`，
  搬进 `instance/modinfo` 包。这一步收益较小（只有一个调用点），可以放到最后视时间决定是否做。

---

## 7. 风险与注意事项

- **不要在拆包的同一次提交里"顺手"修复发现的 bug。** 拆包过程中大概率会看到一些可疑代码
  （例如 `runtimeuser_windows.go` 里桩函数散落到不相关文件这种组织问题本身），一律记录到
  `docs/` 下的待办文档或 issue，单独提交修，避免"这次改动到底是搬家出的问题还是本来就有的问题"
  说不清楚。
- **每一步都要跑 `//go:build` 两侧的编译检查**（本地是 Windows，Linux 专属文件平时不会被
  `go build` 覆盖到）。至少用 `GOOS=linux GOARCH=amd64 go vet ./...` 做交叉编译级别的语法/类型
  检查；真正的行为验证仍然需要在 Linux 真机或 CI 上跑（参考 CLAUDE.md 里"Windows 优先，Linux
  在建"的定位，不能只凭交叉编译通过就判定 Linux 行为不变）。
- **`runner.Configure`/`Config` 是三处调用点共享的"整体覆盖"契约**（`main.go`/`actions/setup.go`/
  `gui/gui.go` 的注释都强调过这一点）。拆分成"组合根 + 各子包 Configure"后，一定要在
  `runner.Configure` 里保证：任何一个子包没被正确 `Configure()` 到，就应该表现为"取默认值"而不是
  panic 或用零值——因为历史上这三处调用点里，字段列表如果漏填一个就会导致该字段悄悄变成
  Go 零值，这个语义在拆分后必须原样保留（各子包的 `defaultConfig()` 要和现在 `runner.go` 里的
  `defaultConfig()` 对应字段的默认值逐一核对）。
- **测试文件必须跟着实现一起搬，且搬完要确认没有测试互相踩全局状态。** 例如 `python_linux_test.go`
  操纵的是 `pyCache`（包级全局），搬进新包后仍然是包级全局，测试之间的隔离方式不变；但如果
  测试之间靠"同一个 `runner` 包"隐式共享了什么 fixture（不太可能，当前测试风格看下来都是各自
  独立的），拆包后要重新确认。
- **`docs/` 目录下引用了具体文件路径的说明**（例如
  `docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md`、`docs/ARKAPI_LINUX_VCREDIST_PLAN.md`、
  `docs/UMU_PREFIX_OVERLAY_PLAN.md`、`docs/UMU_PREFIX_OVERLAY_TODO.md`）在文件搬家后要同步更新
  路径引用，避免文档和代码位置对不上。`CLAUDE.md` 的目录树段落也要在迁移完成后整体重写。

---

## 8. 验证方式

1. 每个阶段结束跑 `go build ./... && go vet ./...`（Windows 侧）+
   `GOOS=linux GOARCH=amd64 go build ./... && GOOS=linux GOARCH=amd64 go vet ./...`（Linux 交叉编译）。
2. 跑现有测试：`go test ./internal/runner/... ./internal/instance/... ./pkg/...`（含新拆出的子包路径）。
3. 阶段 K 结束后，全文 grep 一遍 `runner\.` / `instance\.` 的调用点，人工核对每一处调用的函数
   确实存在于新包里、旧名字没有残留死引用。
4. 找一台 Linux 机器（或已有的 WSL2 环境）实测一次 `asa-server setup` + 启一个 ArkApi 实例，
   对照 `docs/ARKAPI_LINUX_VCREDIST_PLAN.md`/`docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` 里记录的
   真机现象，确认拆包前后行为一致（这是本方案里唯一交叉编译无法替代的验证）。
