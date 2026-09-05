# 拆包方案：`internal/runner` + `internal/instance` 单一职责重构

> 状态：阶段 A–J 已于 2026-09-05 全部执行完成并逐阶段提交（`a83c85a`..`6894907`），
> Windows + Linux（含 WSL2 真机）验证通过。执行时对方案的偏离与遗留缺口见
> `docs/RUNNER_INSTANCE_PACKAGE_SPLIT_TODO.md`（`pkg/shareacl` 未按计划创建等）。
> v2：把能下沉的机制尽量下沉到 `pkg/`，而不是止步于 `internal/` 平级拆分
> 目标：把两个已经膨胀成"神包"的领域包，按**单一职责**拆分——**机制**（不认识 ASA 是什么、
> 只认识"怎么管一个 Xvfb / 怎么装一个 VC++ Redist / 怎么找一个 Python 解释器"）下沉到 `pkg/`，
> **业务规则**（认识 InstanceConfig、认识"哪个实例该跟哪个实例冲突"）留在 `internal/`。
> 原则：沿用 `docs/PACKAGE_RESTRUCTURE_PLAN.md` 的方式——**分层无环、分步渐进、每步独立编译提交**。
> 本文档只定方案，不动代码。

---

## 0. v1 → v2 的关键改动：为什么这些包其实能下沉到 `pkg/`

v1 版本把 `xvfb`/`vcredist`/`steamrt`/`python`/`runtimeuser`/`permissions`/`preflight`/`gameproc`/
`arkapilog`/`launchgate` 全部留在了 `internal/runner`、`internal/instance` 下面，理由是"有全局态/
生命周期，不满足 `pkg/` 准入标准"。这个理由**只对了一半**：

- **"无全局状态"约束的是包级 `var`，不是"这个能力有没有状态"。** `xvfb` 的单例句柄、`python` 的
  `pyCache`、`runtimeuser` 的降权凭证解析，这些状态完全可以从"包级全局变量"改造成
  **`New(cfg) *Manager` 返回的实例字段**——包本身不持有任何包级可变状态（满足准入标准的字面
  要求），"进程里只应该有一个实例"这条约束改由调用方（`internal/runner`）持有唯一一份引用来保证，
  跟标准库 `*http.Client`、`*sql.DB` 是同一种模式。
- **真正拦住下沉的，是"认领域概念"，不是"有状态"。** 这些包里**混着**两层东西：
  1. 纯机制层——"怎么管一个 Xvfb 进程""怎么在 Wine prefix 里装 VC++""怎么找系统里的 Python
     解释器""怎么给一批目录做 chown/ACL""怎么用一个信号量+持有者名字互斥"。这一层不认识
     `InstanceConfig`、不 import 任何 `internal/*`，注入几个路径/名字/回调就能在任何用 Wine
     跑游戏服务端的项目里复用。
  2. 业务规则层——"这个实例是否因为共享前缀 + 都开了 ArkApi 而冲突""要保护哪些目录的共享写权限"
     （答案是 `server-files`/`instances`，这是 `internal/config` 的领域知识）。这一层永远走不出
     `internal/`，因为它的存在意义就是知道 asa-server 自己的配置结构。
- **解法是"薄注入"贯彻到底：把回调也当参数注入，而不只是把配置值当参数注入。** v1 已经在用
  "传路径而不是传 Config" 这种薄注入（例如 `vcredist.Ensure` 改成接收绝对路径）；v2 把同样的
  思路用在业务规则上——通用机制包对外暴露一个 `func(...) (bool, error)` 类型的插槽，
  `internal/` 侧提供闭包实现，机制包永远不知道闭包内部调了 `cfgpkg.LoadInstanceConfig`。

结论：v1 列出的 12 个"新增 internal 子包"里，**9 个整体下沉到 `pkg/`**，**3 个（launchgate、
runtimeuser 的目录清单部分、permissions 的目录清单部分）拆成"机制下沉 + 一小撮业务规则留在
internal"**，`gameproc`/`arkapilog` 甚至不需要单独的 `internal` 子包壳——机制搬到 `pkg/` 后，
`internal/instance` 里只剩几行"用 ASA 的参数实例化一下"的胶水代码，直接内联在调用它的文件里即可。

Go 本身并不禁止 `pkg/` import `internal/`（Go 的 `internal/` 可见性规则只限制"谁能 import
`internal/`"，`pkg/` 和 `internal/` 同在模块根下，互相 import 在编译器层面都合法）。这里的边界是
本项目自己定的架构约束（`docs/INTERNAL_LAYOUT_MIGRATION.md` §9），目的是让 `pkg/` 真正可以脱离
asa-server 复用；靠回调注入而不是直接 import 领域包，正是让这条约束"名副其实"而不是"为了过 lint
硬凑"的做法。

---

## 1. 背景与问题（沿用 v1 的问题清单）

### 1.1 `internal/runner`（34 个文件，约 6700 行）

`runner.go` 本身就是一个"总控神包"：`Config` 一个结构体塞了 9 套互不相关子系统的全部配置项
（umu/Proton、Wine 前缀、Xvfb、VC++ Redist、Python、运行时降权用户、共享目录 ACL），`Problem`/
`PrefixInfo`/`DisplayInfo`/`VCRedistInfo`/`RuntimeUserInfo`/`SharedAccessInfo` 等本该属于各子系统
自己的类型也全部定义在 `runner.go` 里，靠几十个一行转发函数把内部实现"导出"给调用方。

| 文件（组） | 大小 | 实际职责 | 认不认识 ASA 领域概念 |
|---|---:|---|---|
| `runner.go` + `runner_{linux,windows}.go` | ~45K | 进程启动本体（exec/pty/umu-run 拼命令行）+ 9 套子系统的门面转发 | 认识（拼 ArkApi/ASA 的命令行、降权环境变量） |
| `xvfb_linux.go`（+test） | 58K | 自管 Xvfb 虚拟显示：spawn/看门狗/认领/状态文件 | **不认识**——纯 X11/Xvfb 机制 |
| `display_linux.go`（+test） | 41K | 显示解析链 + X11 握手 | **不认识**——纯 X11 机制 |
| `vcredist.go` + `vcredist_{linux,windows}.go`（+test） | 28K | 装/查 Wine prefix 里的微软 VC++ 运行时 | **不认识**——任何 Wine 应用都要过这一步 |
| `prefix.go` + `prefix_{linux,windows}.go`（+test） | 21K | Wine 前缀路径解析、创建、状态、GC | **不认识**——纯 Wine prefix 机制 |
| `overlay.go` + `overlay_linux.go`（+test） | 29K | prefix_mode=overlay 的 overlayfs 挂载 | **不认识**——纯 overlayfs 机制 |
| `steamrt.go` + `steamrt_linux.go`（+test） | 23K | Steam Linux Runtime 变体映射 + 预下载 | **不认识**——umu 生态通用逻辑 |
| `python_linux.go`（+test） | 16K | umu-run 用哪个 Python 解释器 | **不认识**——通用解释器发现 |
| `umu_linux.go` | 27K | umu-launcher/GE-Proton 下载 + prefix 预热编排 | **不认识**——umu 生态通用逻辑 |
| `runtimeuser_{linux,windows}.go`（+test） | 25K | Linux 降权账号管理 + 属主 chown | 机制不认识；"chown 哪些目录"认识 |
| `sharedaccess_{linux}.go`（+test） | 18K | 共享目录 ACL/setgid | 机制不认识；"哪些目录要共享"认识 |
| `preflight_linux.go` | 12K | 汇总以上全部子系统的自检 | host 能力探测部分不认识；聚合谁去问是组合逻辑 |

### 1.2 `internal/instance`（18 个文件，约 5300 行）

| 文件（组） | 职责 | 认不认识 ASA 领域概念 |
|---|---|---|
| `server.go` | Start/Stop/Restart/ForceStop/Kill + 日志路径 + 配置同步 | 认识（核心编排，本就该留下） |
| `common.go`（部分） | `IsStoppable`/状态 reconcile/`SaveWorldSafely`/`waitServerStartup`/`waitServerStopped` | 认识（状态机相关，留下） |
| `common.go`（部分） | `GetAsaVersion`：从 exe 里抠 UTF-16 版本号字符串 | **不认识**——纯二进制解析 |
| `common.go`（部分） | `MonitorAndExtractModInfo`：tail 日志、正则提取 mod 列表、写 JSON | 机制（tail+正则+写 JSON）不认识；落盘路径认识 |
| `gameproc.go` + `gameproc_{linux,windows}.go`（+test） | "哪个 PID 才是真游戏进程" | **不认识**——只要注入 exe 名单 + comm 名，任何 Wine/Proton 游戏服务端都适用 |
| `arkapilog.go` | ArkApi 日志文件命名规则 + 找最新一份 | 机制（"目录里找最新匹配文件"）不认识；文件名规则/目录后缀认识 |
| `asaapilog_{linux,windows}.go` | 把 ArkApi 的独立日志转抄进控制台日志 | 机制（reader→writer 转发）不认识 |
| `launchgate.go` | 共享 Wine 前缀下的启动串行闸门 + ArkApi 单实例冲突检测 | 机制（信号量+持有者）不认识；冲突判定规则认识 |
| `arkcache.go` | `pkg/arkcache` 的实例侧适配器 | 认识（已是恰当粒度，不拆） |

---

## 2. 目标目录结构

```
pkg/
├── problem/                    # Problem{Level,Code,Message,...} + Blockers()/Advisories()
│                                #   纯数据结构 + 过滤函数，无状态，不需要 New()
│
├── asaversion/                  # asaversion.New() *Resolver
│                                #   .Get(exePath) (string, error)；内部持有 (path,mtime,size)→版本 缓存
│                                #   原 instance/common.go 的 GetAsaVersion + asaVersionCache
│
├── procmatch/                   # procmatch.New(exeNames []string, commName string) *Matcher
│                                #   .Find(cmdlineMarker string) (procx.Win32Process, bool, error)
│                                #   原 instance/gameproc.go + gameproc_{linux,windows}.go；
│                                #   Windows 走镜像名，Linux 走 comm+cmdline，机制本身与 ASA 无关，
│                                #   ArkAscendedServer.exe/AsaApiLoader.exe/GameThread 由调用方注入
│
├── tail/（已存在，扩展）          # 新增：WaitNewest(dir string, notBefore time.Time,
│                                #   match func(name string) bool, poll time.Duration) (string, error)
│                                #   原 instance/arkapilog.go 的"目录里找最新匹配文件"逻辑，
│                                #   本就是 tail 包"盯文件"这个主题的自然延伸，不必新开一个包
│
├── iox/（已存在，扩展）           # 新增：Relay(ctx, src io.Reader, dst io.Writer, note func(string)) 
│                                #   原 asaapilog_{linux,windows}.go 的 follow()/note() 转发逻辑
│
├── resourcegate/                 # resourcegate.New(capacity int) *Gate
│                                #   .Acquire(ctx, holder string) (release func(), err error)
│                                #   .Holder() string
│                                #   原 instance/launchgate.go 的信号量+持有者部分；
│                                #   ArkApi 冲突判定规则不在这里，见下面 internal/instance/launchgate
│
├── xvfb/                         # xvfb.New(cfg Config) *Manager（cfg 只含 StatePath/Bin/Screen/
│                                #   AllowX11Remount 等纯机制字段，不含 BaseDir 这种 ASA 命名）
│                                #   .Acquire() (display string, err error) / .Status() / .Stop()
│                                #   ⚠️ Reconfigure(cfg) 而不是重新 New()，见 §4.3
│                                #   原 xvfb_linux.go；无 Windows 变体（display 包在 Windows 上
│                                #   直接跳过，不构造它）
│
├── display/                      # display.New(cfg Config, xvfbMgr *xvfb.Manager) *Resolver
│                                #   .Plan()（只读，供 preflight）/ .Acquire() / .Status() / .Stop()
│                                #   原 display_linux.go + runner_windows.go 里的 displayStatus 桩
│
├── vcredist/                     # vcredist.New(cfg Config) *Installer
│                                #   .Ensure(ctx, prefixPath string, acquireDisplay func() (Target, error), logf) error
│                                #   .HasOverrides(prefixPath string) bool / .Status(prefixPath, gameDir) Info
│                                #   显示获取以回调注入，vcredist 包不 import display 包，两个机制包保持平级
│                                #   原 vcredist.go + vcredist_{linux,windows}.go
│
├── steamrt/                      # 保持无状态自由函数（已经是这个形态，不需要 New()）
│                                #   Prefetch(ctx, cacheDir string, logf) (Variant, error)
│                                #   原 steamrt.go + steamrt_linux.go，CacheDir 由调用方（umu 包）传入
│
├── umu/                          # umu.New(cfg Config) *Runtime
│                                #   .EnsureRuntime(ctx, progress io.Writer) error
│                                #   .CheckRuntime() error
│                                #   .WarmPrefix(ctx, prefixDir string, logf, prefetched bool) error
│                                #   依赖 steamrt（Prefetch）+ vcredist（Ensure，通过接口注入见下）
│                                #   原 umu_linux.go（不含 prefixDir 路径解析，那属于 wineprefix）
│
├── wineprefix/                   # wineprefix.New(cfg Config, umuRT *umu.Runtime, vc *vcredist.Installer) *Manager
│                                #   .KeyFor(instanceName) string / .Dir(key) string
│                                #   .EnsurePrefix(ctx, key, progress) error / .Status() []Info
│                                #   .Remove(key) error / .SharesPrefix() bool
│                                #   原 prefix.go + prefix_{linux,windows}.go + overlay*.go
│
├── pyfinder/                     # pyfinder.New() *Resolver（内部持有发现结果缓存，替代包级 pyCache）
│                                #   .Resolve(override string) (Info, error) / .Status() Info
│                                #   原 python_linux.go
│
├── sysuser/                      # sysuser.New(cfg Config) *Manager（RuntimeUser/UID/GID/RunAsRoot 等）
│                                #   .EnsureUser(ctx) error / .ResolveCredential() (*syscall.Credential, home string, err error)
│                                #   .ChownTree(paths []string) error（原 rwSubtrees/overlayRWSubtrees
│                                #   算出的目录列表由 internal/runner 传入，本包不认识"mirror"/"overlay"）
│                                #   .Status() Info / .Problems() []problem.Problem
│                                #   原 runtimeuser_{linux,windows}.go
│
├── shareacl/                     # shareacl.New() *Manager
│                                #   .Prepare(path string) error（组+setgid+POSIX 默认 ACL，无 setfacl 降级 chown）
│                                #   .Status(paths []string) Info（目录清单由调用方传入）
│                                #   原 sharedaccess_linux.go
│
└── linuxdeps/                    # 保持无状态自由函数
                                  #   Check() []problem.Problem（32位glibc/python/libzstd/tar/AppArmor userns）
                                  #   原 preflight_linux.go 里"探测宿主机能力"的部分——聚合逻辑
                                  #   （该问谁、怎么合并 Blockers/Advisories）留在 internal/runner，
                                  #   见下

internal/runner/
├── runner.go                     # 组合根：持有上面每个 Manager 的唯一实例（包级 var，仅存指针，
│                                 #   不重复各 Manager 内部状态）；Config/Configure 对外形状不变，
│                                 #   内部把字段切给各 pkg 的 Config 并调 .Reconfigure()
├── runner_windows.go              # Windows：exec/go-pty，各 Manager 直接不构造/不调用
├── runner_linux.go                # Linux：umu-run 拼命令行，调用 sysuser/display 等 Manager 方法
└── preflight.go                   # runner.Preflight()：linuxdeps.Check() + 各 Manager.Problems()/.Status()
                                   #   合并后调 problem.Blockers()/Advisories()——这是纯组合逻辑，
                                   #   不需要单独的 internal/runner/preflight 子包

internal/instance/
├── server.go                      # 不变：Start/Stop/Restart/ForceStop/Kill + 日志路径 + 配置同步
│                                 #   内部用 procmatch.New(...) 构造一个包级 var 替代原 gameproc.go
│                                 #   （不再需要 internal/instance/gameproc 这层壳）
├── common.go                      # 瘦身：IsStoppable/reconcile*/SaveWorldSafely/waitServerStartup/
│                                 #   waitServerStopped/findServerPIDBySaveDir；GetAsaVersion 移除，
│                                 #   GetInstanceAsaVersion 改调 asaversion.New()（包级 var）.Get(...)
├── arkcache.go                    # 不动
└── launchgate.go                  # 瘦身：内部持有一个 resourcegate.New(1)（共享前缀 case）；
                                   #   conflictingArkApiInstance/PrecheckStart 的判定逻辑保留在这——
                                   #   它们要调 cfgpkg.LoadInstanceConfig 判断"是否启用 ArkApi"，
                                   #   这是本文档里唯一"必须留在 internal"的业务规则
```

`arkapilog.go`/`asaapilog_{linux,windows}.go` 拆完后，`internal/instance` 里不再需要同名文件——
"找最新的 ArkApi 日志"调 `tail.WaitNewest(...)`，"转抄进控制台日志"调 `iox.Relay(...)`，两处调用
点分别内联在 `server.go` 里原来调用它们的地方（几行胶水代码，不值得单独立一个文件）。

---

## 3. 分层依赖（无环）

### 3.1 `pkg/` 内部

```
pkg/logger, pkg/download, pkg/archive, pkg/procx, pkg/problem, pkg/tail, pkg/iox   # 既有叶子
        │
pkg/asaversion, pkg/procmatch, pkg/resourcegate, pkg/pyfinder,
pkg/sysuser, pkg/shareacl, pkg/linuxdeps, pkg/steamrt        # 相互独立，零 pkg-to-pkg 依赖
        │
pkg/xvfb
        │
pkg/display  ──depends──▶ pkg/xvfb
        │
pkg/vcredist  # 显示获取通过注入的 func() (Target, error) 回调，不直接 import pkg/display
        │
pkg/umu       ──depends──▶ pkg/steamrt, pkg/vcredist（通过接口/回调注入，见 §4.2）
        │
pkg/wineprefix ─depends──▶ pkg/umu, pkg/vcredist
```

以上任意一个 `pkg/*` 包都**不 import `internal/*`**，也不 import 除上图箭头以外的其它 `pkg/*`
包——这是它们能被单独复用、单独单测的前提。

### 3.2 `internal/runner`（组合根）

```
internal/runner ─depends─▶ pkg/{xvfb,display,vcredist,umu,wineprefix,pyfinder,sysuser,
                             shareacl,linuxdeps,problem}
```

`internal/runner` 是**唯一**知道"这些 Manager 要按什么顺序初始化、Config 里哪个字段归谁"的地方；
`webapi`/`gui`/`actions` 一律只调 `runner.XXX()`，不直接 import `pkg/xvfb` 等实现细节
（否则又会退化成到处散落的门面）——这一点和 v1 一致。

### 3.3 `internal/instance`

```
pkg/asaversion, pkg/procmatch, pkg/resourcegate, pkg/tail, pkg/iox   # 叶子
        │
internal/instance（核心，含 launchgate.go 的业务规则）
        ─depends─▶ config, process, rconx, state, mirror, installer, runner（沿用现状）
```

`launchgate.go` 里"是否冲突"的判定要调 `internal/config`（读 ArkApi 是否启用）和
`internal/runner`（`SharesWinePrefix`/`PrefixKeyFor`，现在是 `runner` 转发到
`wineprefix.Manager` 的结果）——这两个 import 决定了它不可能下沉，`pkg/resourcegate` 只提供
它内部用到的信号量机制。

---

## 4. 关键设计决策

### 4.1 `Config`/`Configure` 保留在 `runner` 包做组合根，外部调用点零改动

`main.go`/`internal/actions/setup.go`/`internal/gui/gui.go` 三处已经在用一个字段齐全的
`runner.Config{...}` 字面量调 `runner.Configure(cfg)`。拆分后 `runner.Config` 的**外部形状不变**，
`runner.Configure(cfg)` 内部把字段切给各 `pkg/*` 包自己的 `Config`，再调用对应 Manager 的
`Reconfigure()`（首次调用时是 `New()`，见 §4.3）。

### 4.2 领域业务规则通过"注入回调"而不是"注入 import"传给机制包

两处需要这样处理：

- `pkg/umu` 的 `EnsureRuntime` 需要在装好 umu/GE-Proton 后装一次 VC++ Redist（共享前缀场景）。
  它不 import `pkg/vcredist`，而是在 `umu.Config` 里放一个字段
  `EnsureVCRedist func(ctx context.Context, prefixPath string, logf func(string, ...any)) error`，
  `internal/runner` 组装时把 `vcredistMgr.Ensure` 传进去。`pkg/wineprefix` 同理。
  这样 `pkg/umu`/`pkg/wineprefix` 和 `pkg/vcredist` 之间**没有编译期依赖**，谁先谁后完全由
  `internal/runner` 决定，三个包可以分别单测（用一个假的 `EnsureVCRedist` 桩）。
  > 备选方案：如果觉得回调字段太"函数式"、不好读，也可以让 `pkg/umu`/`pkg/wineprefix`
  > 直接依赖 `pkg/vcredist` 的具体类型（如上面 §3.1 图所示，`wineprefix -> umu, vcredist`）——
  > 两种做法都不违反"不认识 ASA 领域概念"，区别只是"要不要在 pkg 之间也做接口解耦"，
  > 可以在实现阶段按团队偏好二选一，不影响本方案的包边界。
- `internal/instance/launchgate.go` 的 ArkApi 冲突判定需要读某个实例是否启用了 ArkApi
  （`cfgpkg.LoadInstanceConfig`）。`pkg/resourcegate` 只提供 `Acquire`/`Holder`，冲突判定的
  分支逻辑（"持有者和我都要 ArkApi 且共享前缀"）留在 `launchgate.go` 里，`pkg/resourcegate`
  甚至不知道"ArkApi"这个词。

### 4.3 单例态的机制包用 `Reconfigure`，不要重复 `New()`

`xvfb.Manager` 内部跑着一个 `LockOSThread` 且永不返回的 spawn-loop goroutine（承接
Pdeathsig，详见现有 `xvfb_linux.go` 包注释）。`runner.Configure()` 在进程生命周期内可能被调用
不止一次（GUI 修改设置后重新应用）——如果每次都 `xvfb.New(cfg)`，会产生第二个 spawn-loop
goroutine，和第一个的 Xvfb 进程互相踩状态文件。所以 `internal/runner` 只在**第一次**
`Configure()` 时 `New()`，之后的调用改成 `mgr.Reconfigure(cfg)`（更新参数，不重启已经在跑的
Xvfb/看门狗）。`display`/`umu`/`wineprefix`/`sysuser` 这几个包如果内部也有"首次初始化只做一次"
的逻辑（`runtimeMu` 那类一次性初始化互斥），同样适用这条规则；`pyfinder`/`procmatch` 这类纯缓存
型的，重新 `New()` 只是丢一次缓存，允许直接替换。

### 4.4 `Problem` 类型下沉到 `pkg/problem`，是解耦的关键一步

现状里几乎每个子系统的自检函数都返回 `[]runner.Problem`，这个共享返回类型定义在最终要拆掉的
`runner.go` 里，是任何子包拆分都绕不开的循环依赖源头。搬到 `pkg/problem` 后，`Blockers()`/
`Advisories()` 这两个纯过滤函数也一并搬过去，所有新 `pkg/*` 包和 `internal/runner` 都改成
`import "asa-server/pkg/problem"`。

### 4.5 全局状态跟着实现走，不跨包共享

`xvfb` 的单例句柄、`pyfinder` 的缓存、`wineprefix` 的 `prefixLocks`/`prefixCreationSlots`、
`resourcegate` 持有的信号量、`pkg/asaversion` 的版本缓存——这些状态搬进对应 Manager/Resolver
的结构体字段即可，天然不跨包共享。`internal/runner`/`internal/instance` 各自只保留"持有唯一一份
引用"这一层包级 `var`（例如 `var xvfbMgr *xvfb.Manager`），不重新引入被下沉包内部的状态细节。

---

## 5. 外部调用点变更清单

### 5.1 依赖 `runner` 包的文件

这些文件调用的都是 `runner` 包**对外导出的稳定函数**（`SharedAccessStatus`/`PrefixStatus`/
`EnsurePrefixVCRedist`/`Preflight`/`Configure` 等），拆分后 `runner` 包继续对外暴露同名/同签名
函数（内部实现改成转发给对应 Manager），**这些调用点不需要改动**：

`internal/actions/perms.go`、`internal/actions/prefix.go`、`internal/actions/setup.go`、
`internal/actions/verify_arkapi.go`、`internal/gui/gui.go`、`internal/installer/*.go`、
`internal/svcmgr/service*.go`、`internal/webapi/systemapi/systemapi.go`、
`internal/webapi/instanceapi/instanceapi.go`、`main.go`。

唯一例外：如果拆分过程中决定顺便清理掉个别没什么人用的转发函数（例如某个只有一处调用的
`RuntimeXxxInfo` 结构体字段变化），需要单独在该阶段的提交里列出，不要和"纯搬家"的提交混在一起。

### 5.2 依赖 `instance` 包的文件

`internal/actions/{actions,arkapicache,environment}.go`、`internal/batchmanage/manager.go`、
`internal/countdown/run.go`、`internal/schedule/scheduler.go`、`internal/updatemanage/manager.go`、
`internal/webapi/{instanceapi,logapi,serverapi}/*.go` ——调用的都是 `instance` 包已导出的稳定 API
（`StartServer`/`StopServer`/`RestartServer`/`PrecheckStart`/`GetGameLogFilePath` 等），**零改动**。

---

## 6. 迁移步骤（分阶段，每阶段独立编译 + 提交）

> 顺序原则：先落 `pkg/` 叶子（零依赖），再落 `pkg/` 里有依赖关系的几个，再落两个组合根；
> 每一步跑 `go build ./... && go vet ./...`（Windows）+
> `GOOS=linux GOARCH=amd64 go build ./... && go vet ./...`（Linux 交叉编译）。

- **阶段 A：`pkg/problem`。** 剪切 `Problem`/`Blockers`/`Advisories`/`filterProblems`，`runner`
  包内用类型别名 `type Problem = problem.Problem` 过渡。
- **阶段 B：`pkg/asaversion`、`pkg/procmatch`。** 两者互相独立，可并行。`instance` 包内改成持有
  `var asaVersionResolver = asaversion.New()` / `var gameProcMatcher = procmatch.New(...)`。
- **阶段 C：`pkg/resourcegate`。** 从 `launchgate.go` 剪出信号量+持有者部分，`launchgate.go` 改成
  持有 `var sharedPrefixGate = resourcegate.New(1)`，业务判定逻辑不动。
- **阶段 D：`pkg/tail` 扩展 `WaitNewest`、`pkg/iox` 扩展 `Relay`。** 删掉
  `instance/arkapilog.go`/`asaapilog_{linux,windows}.go`，调用点内联进 `server.go`。
- **阶段 E：`pkg/pyfinder`、`pkg/sysuser`、`pkg/shareacl`、`pkg/steamrt`、`pkg/linuxdeps`。**
  五者互相独立，可并行拆。`sysuser`/`shareacl` 拆分时把"目录清单"这一半逻辑留在
  `internal/runner`（新增一两个小函数算路径），只把"chown/ACL 怎么做"搬进 pkg。
- **阶段 F：`pkg/xvfb`。** 单独一步，因为要顺带把 spawn-loop 单例改造成 `New`+`Reconfigure`
  两个入口，改动比纯搬家多一点，值得单独验证。
- **阶段 G：`pkg/display`（依赖阶段 F 的 `pkg/xvfb`）。**
- **阶段 H：`pkg/vcredist`。** 显示获取改成回调参数（见 §4.2），单独验证这处接口收敛不改变行为。
- **阶段 I：`pkg/umu`、`pkg/wineprefix`。** 两者互相依赖较紧（`prefixDir` 路径解析归
  `wineprefix`，`umu` 需要时调用 `wineprefix.Dir`），建议一次提交处理完这对边界，避免中间态。
- **阶段 J：瘦身 `internal/runner`。** 落地 `runner.go` 的组合根形态（持有各 Manager 单例 +
  `Preflight()` 聚合），删除所有已经全部转发出去的旧实现代码。跑一次全量
  `go build ./... && go vet ./...` + 现有测试套件。

---

## 7. 风险与注意事项

- **不要在拆包的同一次提交里"顺手"修 bug。** 发现的可疑代码记到 `docs/` 待办或 issue，单独提交修。
- **每一步都要跑 `//go:build` 两侧的编译检查**（本地是 Windows，Linux 专属文件平时不会被
  `go build` 覆盖到），交叉编译只能保证语法/类型正确，行为验证仍需 Linux 真机/CI。
- **`pkg/xvfb` 的 `New` vs `Reconfigure` 是本方案里唯一有真实正确性风险的一步**（见 §4.3），
  务必单独一个阶段、单独跑一次"改配置后 Xvfb 没有变成两个进程"的手工验证。
- **`sysuser`/`shareacl` 拆分时最容易把"目录清单"顺手搬进 pkg 包**（因为它们现在就在同一个
  文件里），要刻意把这一半逻辑留在 `internal/runner`，否则又会做出一个"看起来通用、实际上
  硬编码了 `instances`/`server-files` 字符串"的假 pkg 包。
- **Windows 桩函数要按新包边界拆开**：现状 `runtimeuser_windows.go` 混着 `sharedaccess` 的桩，
  `runner_windows.go` 混着 `display`/`python` 的桩，迁移时按新包各自建 `_windows.go`。
- **`docs/` 下引用具体文件路径的说明**（`XVFB_CROSS_DISTRO_DISPLAY_PLAN.md`、
  `ARKAPI_LINUX_VCREDIST_PLAN.md`、`UMU_PREFIX_OVERLAY_PLAN.md` 等）迁移完成后要同步更新路径；
  `CLAUDE.md` 的目录树段落也要整体重写。

---

## 8. 验证方式

1. 每个阶段：`go build ./... && go vet ./...`（Windows）+ Linux 交叉编译等价命令。
2. `go test ./pkg/... ./internal/runner/... ./internal/instance/...`（含新拆出的 `pkg/*` 路径）。
3. 阶段 J 结束后，全文 grep 一遍 `runner\.`/`instance\.` 调用点，确认没有遗留死引用。
4. 找一台 Linux 机器/WSL2 实测一次 `asa-server setup` + 启一个 ArkApi 实例，重点验证
   §4.3 的 Xvfb reconfigure 场景，对照 `docs/ARKAPI_LINUX_VCREDIST_PLAN.md`/
   `docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` 里记录的真机现象。
