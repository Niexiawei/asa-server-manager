# `internal/runner` + `internal/instance` 拆包（合并文档）

> 本文由 `RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md`（设计）、`RUNNER_INSTANCE_PACKAGE_SPLIT_TODO.md`（后续清单）、`RUNNER_INSTANCE_PACKAGE_SPLIT_REVIEW.md`（审阅报告）于 2026-09-29 物理合并而成（方案甲，逐字保留）。
> Part 1 是拆包方案；Part 2 是后续 Gap 清单（活文档，部分 Gap 已执行）；Part 3 是对拆包结果的代码审阅（含 P1/P3 发现）。
> ⚠️ 本文各「落地文件 / 迁移步骤」写于 `internal/` 迁移之前，部分路径已变；实际路径见文末「附录 Y」。
> 🔴 **本文 Part 3 §1 记录的 P1（`pkg/xvfb` watch 看门狗退避序列 + 诊断日志丢失）在 2026-09-29 的独立代码审计中被再次确认**，详见 `docs/XVFB_DISPLAY_PLAN.md` 的「已知缺陷清单」与 `docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md` §1.2（那里记为 P2：退避写成 `restartBackoff[0]`、`pkg/xvfb` 全包零日志）。同一缺陷两处独立命中，**尚未修复**。

## 本文内已知缺陷索引

> 仅做索引与交叉引用；条目原文与论证见对应 Part，不在此重写或压缩。

| 编号 | 标题 | 位置 | 状态 |
|---|---|---|---|
| Gap A | `pkg/shareacl` 从未创建（阶段 E 当场跳过） | Part 2 §1 | ✅ 已于 2026-09-05 执行完成 |
| Gap B | `vcredist_linux.go` 里几个零散的纯机制（`downloadProgress`/`mib`、`resolveFinalURL`、`exitCodeOf`） | Part 2 §2 | ✅ 已于 2026-09-05 执行完成 |
| Gap C | `pkg/display` 从未创建（PLAN 阶段 G 漏做） | Part 2 §3 | ✅ 已于 2026-09-05 执行完成 |
| Gap D | `runInPrefix` 与 `umu.WarmPrefix` 是同一个机制写了两遍 | Part 2 §4 | ✅ 已于 2026-09-05 执行完成 |
| Gap E | vcredist 的 DLL 判定 / 诊断，文件读取那半边 | Part 2 §5 | ✅ 已于 2026-09-05 执行完成 |
| Gap F | vcredist 编排整体下沉（结论被推翻过一次，见 §6.1） | Part 2 §6 | ✅ 已执行（代码）；⬜ **真机验证尚未进行**，见 Part 2 §7、§8 末条 |
| — | `pkg/umu` 与 `pkg/wineprefix` 四处面向用户的错误文本硬编码「请运行 `asa-server setup` 完成环境准备」（按同标准应改哨兵错误） | Part 2 §6.7 | ⬜ 本轮未改（记此供日后取舍） |
| **P1** | `pkg/xvfb` watch 看门狗退避序列退化为常量（`restartBackoff[0]`，5s/15s 永不生效）+ ①③④ 三条诊断日志全部丢失（行为回归） | Part 3 §1 | 🔴 **未修复**；2026-09-29 独立审计再次确认（`docs/XVFB_DISPLAY_PLAN.md`「已知缺陷清单」、`docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md` §1.2 记为 P2） |
| P3 | 三处重复的 `noSuchID` 常量（`pkg/xvfb`、`internal/runner/runtimeuser_linux.go`、`pkg/sysuser`） | Part 3 §2 | ⬜ 未修复（建议清理） |
| P3 | `findAdminTool` 三处重复实现（`pkg/shareacl`、`internal/runner`、`pkg/sysuser`） | Part 3 §3 | ⬜ 保留现状；建议加对位注释互引（尚未加） |
| P3 | 删除 `pkg/umu/umu_linux.go:305-315` 的死方法 `runtimeUserNameHint` | Part 3 §8.7、Part 3 §9 #4 | ⬜ 未修复（建议删除） |
| — | `pkg/iox.Relay` 文档补一句「不关闭 src/dst」 | Part 3 §8.2、Part 3 §9 #6 | ⬜ 未修复（文档项） |
| — | `internal/instance/asaapilog_linux.go` 的 ctx 取消语义细微差异 | Part 3 §4、Part 3 §9 #5 | ✅ 无需处理（对外行为等价） |

---

# Part 1 — 拆包方案：`internal/runner` + `internal/instance` 单一职责重构（原 `RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md`）

# 拆包方案：`internal/runner` + `internal/instance` 单一职责重构

> 状态：阶段 A–J 已于 2026-09-05 全部执行完成并逐阶段提交（`a83c85a`..`6894907`），
> Windows + Linux（含 WSL2 真机）验证通过。执行时对方案的偏离与遗留缺口见
> `本文 Part 2`（`pkg/shareacl` 未按计划创建等）。
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
- **`docs/` 下引用具体文件路径的说明**（`XVFB_DISPLAY_PLAN.md`、
  `ARKAPI_LINUX_VCREDIST_PLAN.md`、`UMU_PREFIX_PLAN.md` 等）迁移完成后要同步更新路径；
  `CLAUDE.md` 的目录树段落也要整体重写。

---

## 8. 验证方式

1. 每个阶段：`go build ./... && go vet ./...`（Windows）+ Linux 交叉编译等价命令。
2. `go test ./pkg/... ./internal/runner/... ./internal/instance/...`（含新拆出的 `pkg/*` 路径）。
3. 阶段 J 结束后，全文 grep 一遍 `runner\.`/`instance\.` 调用点，确认没有遗留死引用。
4. 找一台 Linux 机器/WSL2 实测一次 `asa-server setup` + 启一个 ArkApi 实例，重点验证
   §4.3 的 Xvfb reconfigure 场景，对照 `docs/ARKAPI_LINUX_VCREDIST_PLAN.md`/
   `docs/XVFB_DISPLAY_PLAN.md` 里记录的真机现象。

---

# Part 2 — `internal/runner` 拆包后续清单（原 `RUNNER_INSTANCE_PACKAGE_SPLIT_TODO.md`）

> 本部分为活文档：新 Gap 加到此处，结论回填 Part 1。

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
| `umu_linux.go` | 203 | 组合根：umu/wineprefix 单例 + `ensureRuntime` 编排（无法再下沉，见 PLAN §"关键设计决策" 4.2 的 wineprefix→umu 单向依赖论证）。原 209 行，薄转发层已按 `docs/RUNNER_GLUE_INLINE_PLAN.md` 删除 |
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

---

# Part 3 — runner / instance / pkg 拆分审阅报告（原 `RUNNER_INSTANCE_PACKAGE_SPLIT_REVIEW.md`）

# runner / instance / pkg 拆分审阅报告

> 只读审阅，**未修改任何代码**。审阅对象：
>
> - `internal/runner/`（xvfb、shareacl、umu、wineprefix、sysuser、display、runtimeuser、vcredist、prefix 等胶水）
> - `internal/instance/`（server.go、common.go、launchgate.go、asaapilog\_\*）
> - `pkg/` 下新拆子包（xvfb、shareacl、umu、wineprefix、sysuser、problem、procmatch、tail、iox、resourcegate、asaversion、fsutil、download、pyfinder、vcredist、linuxdeps、steamrt）
>
> 配套拆分计划：`docs/RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md` 与 `本文 Part 2`。
>
> 验证手段：双平台 `go build ./...` 与 `go vet ./...` 全过；针对性单元测试（problem / resourcegate / tail / xvfb）通过；关键行为用 `git show <迁移前提交>:<file>` 与迁移后文件做了逐行 diff。

---

## 0. 总体结论

迁移整体质量**良好**：API 表面稳定、import 路径收敛、双平台 build/vet 零错误、跨包依赖方向单向无环（runner/instance → pkg），Configure() 的字段扩展已经被所有三个调用点（`main.go`、`internal/actions/setup.go`、`internal/gui/gui.go`）同步更新到位。

**但发现 1 处**迁移引入的真实行为回归（P1），以及若干处死代码 / 文档问题。P1 见 §1，是这次审阅唯一必须处理的发现。

整体迁移质量评分：**B+**（若 §1 修复后升 A）。

---

## 1. 【P1】`pkg/xvfb` watch 看门狗退避序列与诊断日志丢失（行为回归）

**文件**：`pkg/xvfb/xvfb_linux.go` `L286-L301`

**迁移前**（`git show 46efc28~1:internal/runner/xvfb_linux.go`，单文件 ~951 行）：

```go
func watchXvfb(cfg Config, bin string, x *managedXvfb) {
    x.waitExit()
    if x.intentional.Load() || xvfbCurrent.Load() != x {
        return
    }
    logger.Errorf("runner: 自管 Xvfb %s（pid %d）意外退出（%s）。%s 的末尾输出：\n%s",
        x.display, x.pid, x.unusableReason(), x.log, x.logTail())        // ← ① 错误日志

    for i, backoff := range xvfbRestartBackoff {                         // ← ② 遍历序列
        time.Sleep(backoff)                                              //    2s / 5s / 15s
        if x.intentional.Load() || xvfbCurrent.Load() != x {
            return
        }
        if _, err := ensureXvfb(cfg, bin); err == nil {
            return
        } else {
            logger.Warnf("runner: 第 %d 次补起 Xvfb 失败: %v", i+1, err) // ← ③ 每次失败日志
        }
    }
    logger.Errorf("runner: 连续 %d 次拉不起 Xvfb，放弃自动恢复；"+      // ← ④ 最终放弃日志
        "下一次需要显示的启动会再试一次并报出原因", len(xvfbRestartBackoff))
}
```

**迁移后**（`pkg/xvfb/xvfb_linux.go` `L286-L301`）：

```go
func (m *Manager) watch(x *managedXvfb) {
    x.waitExit()
    if x.intentional.Load() || m.current.Load() != x {
        return // 我们自己停的，或者已经被换掉了 —— — 不该由这里插手
    }

    for range restartBackoff {
        time.Sleep(restartBackoff[0])   // ← 始终 2 秒，5s/15s 永不生效
        if x.intentional.Load() || m.current.Load() != x {
            return
        }
        if _, err := m.ensure(); err == nil {
            return // ensure 会为新的那个另起一只看门狗
        }
    }
    // — ① ③ ④ 三条诊断日志全部丢失
}
```

**严重程度**：中-高。语义与可观察性同时退化：

1. **退避序列退化为常量**：`for range restartBackoff` 把循环次数交给序列长度（3 次），但 `time.Sleep(restartBackoff[0])` 每次都拿 `[0]=2s`。结果是 Xvfb 死掉之后**永远**只睡 2 秒就重试一次，三次循环总共只睡 6 秒。原意是「2s → 5s → 15s」让重试间隔越来越长、把连续崩溃的时间窗拉大，从而减少刷屏和 CPU 空转。
2. **三条诊断日志丢失**：① 退出原因与 xvfb.log 末尾、③ 每次补起失败原因、④ 最终放弃告警，全部不在了。Xvfb 反复死亡时只看得到每 2 秒一次的「无 Xvfb」症状，根因无迹可循。

**建议修复**（diff 形状，供参考）：

```go
// watch 是 Xvfb 的看门狗：它一死就补起一个。
//
// 它**救不了正在跑的那个调用方** —— 那个进程的 X 连接已经断了，新起的还是
// 另一个显示号。看门狗的价值有两条：① 把「显示莫名其妙没了」变成日志里
// 一句「Xvfb 于某时退出，原因是…」；② 让下一次调用不必现等一个冷启动的 X 服务。
func (m *Manager) watch(x *managedXvfb) {
    x.waitExit()
    if x.intentional.Load() || m.current.Load() != x {
        return
    }
    logger.Errorf("pkg/xvfb: 自管 Xvfb %s（pid %d）意外退出（%s）。%s 的末尾输出：\n%s",
        x.display, x.pid, x.unusableReason(), x.log, x.logTail())

    for i, backoff := range restartBackoff {
        time.Sleep(backoff)
        if x.intentional.Load() || m.current.Load() != x {
            return
        }
        if _, err := m.ensure(); err == nil {
            return
        } else {
            logger.Warnf("pkg/xvfb: 第 %d 次补起 Xvfb 失败: %v", i+1, err)
        }
    }
    logger.Errorf("pkg/xvfb: 连续 %d 次拉不起 Xvfb，放弃自动恢复；"+
        "下一次需要显示的启动会再试一次并报出原因", len(restartBackoff))
}
```

迁移后 `pkg/xvfb` 是新包，原来调用 `logger.Errorf("runner: …")` 的 tag 应改为 `pkg/xvfb:`，与新包的注释/日志一致。也可以保留 `runner:`，但 `pkg/xvfb:` 便于在混部日志里定位来源。

**验证**：`go vet ./...` 双平台通过，迁移后功能上不会让 asa-server 起不来；但在「Xvfb 不停死」的故障场景下，操作者会失去排障线索，并且重试间隔不再有退避语义。

---

## 2. 【P3】冗余的 `noSuchID` 常量

**文件**：

- `pkg/xvfb/xvfb_linux.go:97`：`const noSuchID = ^uint32(0)`
- `internal/runner/runtimeuser_linux.go:37`：`const noSuchID = ^uint32(0)`
- `pkg/sysuser/sysuser_linux.go:25`：`const noSuchID = ^uint32(0)`（未导出）

**问题**：

- `pkg/xvfb/xvfb_linux.go` 的 `noSuchID` 没有任何运行时引用，只被 `pkg/xvfb/xvfb_linux_test.go:395/422/425` 用。runtimeuser_linux.go 的注释说「xvfb_linux.go compares runtimeChildIDs' result against it directly」—— 但 xvfb 实际并不跨包引用它，而是自己在测试里复制了一份。
- `internal/runner/runtimeuser_linux.go` 的 `noSuchID` 也没有任何外部引用，注释里的「xvfb_linux.go compares against it」目前并不成立。
- 三处同名常量、含义相同，运行时未引用、测试各自为政，下次有人想加新比较时容易撞名。

**严重程度**：低。是迁移过程切分时漏掉的清理，不是 bug。

**建议修复**：

- `pkg/xvfb/xvfb_linux.go` 删去 `const noSuchID`，测试改用 `sysuser.noSuchID`（需导出，见下条）或自带常量。
- `internal/runner/runtimeuser_linux.go` 删去 `const noSuchID` 与注释。
- `pkg/sysuser/sysuser_linux.go` 将 `noSuchID` 导出（改 `NoSuchID`），或保留私有但只在本包内使用，与注释「Mirrors the identically-named sentinel in pkg/sysuser (unexported there)」的现状保持一致即可。

---

## 3. 【P3】`pkg/shareacl` 的 `findAdminTool` 重复实现

**文件**：

- `pkg/shareacl/shareacl.go:270` —— ACL 二进制用
- `internal/runner/sharedaccess_linux.go:153` —— `setfacl` 诊断用
- `pkg/sysuser` 中也存在同形态的 `findAdminTool`

**问题**：三处独立实现，逻辑完全相同（LookPath → /usr/sbin → /sbin → /usr/local/sbin → /usr/bin → ""），共约 10 行。`sharedaccess_linux.go:143-152` 的注释承认了这一重复，理由是「每个包都希望 stand alone」。这是设计选择，迁移没有引入新重复，但审阅时应当注意：未来如果要加新 PATH（典型例子：nixOS 的 `/etc/profiles/per-user/$USER/bin`、CoreOS toolbox 的 `/usr/bin/toolbox`），需要**三处同步修改**，否则 `setfacl` 能找到而 `useradd` 找不到、或者反之。

**严重程度**：低。**建议**：保留现状，但在 `pkg/shareacl/findAdminTool.go` 顶部加一行 `// Keep in sync with: pkg/sysuser/findAdminTool, internal/runner.findAdminTool` 的对位提醒，避免「改了 A 忘了 B/C」。

---

## 4. 【P3】`internal/instance/asaapilog_linux.go` 的 ctx 取消语义细微差异

**文件**：`internal/instance/asaapilog_linux.go:117-138`

**迁移前**（`git show a9c0000~1:internal/instance/asaapilog_linux.go`）：

```go
// done 关闭返回 "启动链已结束，仍未生成 ArkApi 日志"
// deadline 超时返回 "等待超过 %s"
// 两条路径各自独立，措辞精确区分
```

**迁移后**：

```go
ctx, cancel := context.WithTimeout(context.Background(), arkApiLogAppearTimeout)
defer cancel()
go func() {
    select {
    case <-done:
        cancel()
    case <-ctx.Done():
    }
}()
srcPath, err := tail.WaitNewest(ctx, dir, launchedAt, isArkApiLogName, arkApiLogPollInterval)
if err != nil {
    reason := "启动链已结束，仍未生成 ArkApi 日志"
    if errors.Is(err, context.DeadlineExceeded) {
        reason = fmt.Sprintf("等待超过 %s", arkApiLogAppearTimeout)
    }
    // …
}
```

**差异**：

- 迁移前：done 与 deadline 是 select 里的两条独立 case，分别走不同的错误信息。
- 迁移后：done 通过 `cancel()` 把 ctx 变成 Canceled（不是 DeadlineExceeded），然后用 `errors.Is(err, context.DeadlineExceeded)` 二分。
- **差异点**：done 和 deadline **同时**到达时的优先级与措辞有微小变化（迁移前两者看 `select` 的随机性，迁移后同样看 `select` 的随机性 + `time.AfterFunc` 的精确时刻）。用户感知不到差异；日志、API、HTTP 返回都一样。
- `tail.WaitNewest` 在 ctx.Done 时**再查一次**目录（`waitnewest.go:31-33`），与迁移前 done case 里的「再看最后一眼」语义一致。✓

**严重程度**：极低。行为对外完全等价，仅取消路径表达形式不同。

**建议**：无需修改。如果后续要让 deadline 与 done 在措辞上彻底分得开，可在迁移后的代码里加 `errors.Is(err, context.Canceled)` 分支独立处理，但当前没必要。

---

## 5. 跨包依赖图核查（无环）

迁移后的导入方向：

```
internal/instance  →  internal/runner
                  →  pkg/procmatch, pkg/procx, pkg/asaversion, pkg/tail, pkg/iox, pkg/logger
                  →  pkg/resourcegate
                  →  pkg/shareacl (经 internal/runner)

internal/runner   →  pkg/xvfb, pkg/shareacl, pkg/sysuser, pkg/umu, pkg/wineprefix,
                     pkg/vcredist, pkg/display, pkg/linuxdeps, pkg/download, pkg/pyfinder,
                     pkg/steamrt, pkg/problem, pkg/console, pkg/fsutil

pkg/xvfb          →  stdlib only
pkg/shareacl      →  stdlib only
pkg/sysuser       →  pkg/problem
pkg/umu           →  stdlib only
pkg/wineprefix    →  pkg/umu
pkg/problem       →  stdlib only
pkg/procmatch     →  pkg/procx
pkg/resourcegate  →  stdlib only
pkg/tail          →  stdlib only
pkg/iox           →  stdlib only
pkg/asaversion    →  stdlib only
```

**核查结论**：

- `pkg/*` 之间**无循环**（vcredist 例外是 cfg → runner 顺序，无反向边）。
- `internal/instance` 与 `internal/runner` 都依赖 `pkg/*`，但**不**互相依赖导致 instance ↔ runner 形成新边——目前 instance 依赖 runner（启动路径），runner 不依赖 instance，符合「runner 不认识实例、instance 负责调用 runner」的设计约束。
- `internal/runner` **不再依赖** `internal/config`（`runner.go` 的 `Config` 现在是 `pkg/umu/wineprefix/shareacl` 配置的扁平组装），避免了与 `config` 包的潜在循环（`runner.go:596-601` 的注释明确说了这一点）。

---

## 6. 双平台编译验证

| 检查 | Windows | Linux (cross) |
| --- | --- | --- |
| `go build ./...` | ✅ 无输出 | ✅ 无输出 |
| `go vet ./...` | ✅ 无输出 | ✅ 无输出 |
| `go vet ./internal/runner/... ./internal/instance/...` | ✅ | n/a（cross） |
| `go vet ./pkg/...` | ✅ | n/a（cross） |

**遗留项**：`pkg/shareacl` 因 `//go:build linux` 在 Windows 上 `go test` 报「build constraints exclude all Go files」（包内无 Windows 文件），是预期行为。完整 Linux 测试在交叉环境跑不动（需 Wine/proton 才能验证），本机只跑了 Windows 子集（problem / resourcegate / tail），全部通过。

---

## 7. `runner.Configure` 调用点字段同步核查

`runner.Configure(cfg runner.Config)` 走的是 wholesale 替换（`runner.go:644` 的注释特别强调了这一点），任何加进 `Config` 的字段**必须**在每个调用点都补上，否则会被静默清零。

| 字段 | `main.go:282` | `internal/actions/setup.go:84` | `internal/gui/gui.go:437` |
| --- | --- | --- | --- |
| `PrefixMode` | ✓ | ✓ | ✓ |
| `XvfbBin` | ✓ | ✓ | ✓ |
| `XvfbScreen` | ✓ | ✓ | ✓ |
| `AllowX11Remount` | ✓ | ✓ | ✓ |
| `RuntimeUser` | ✓ | ✓ | ✓ |
| `RuntimeUID` | ✓ | ✓ | ✓ |
| `RuntimeGID` | ✓ | ✓ | ✓ |
| `RunAsRoot` | ✓ | ✓ | ✓ |
| `RuntimeDeepProbe` | ✓ | ✓ | ✓ |

**结论**：三处调用点同步更新到位，未见漏字段。`runner.go:643-645` 的注释专门讲了这条规矩（曾经的 bug：`asa-server setup` 漏字段导致 preflight 与 setup 之后的运行时状态不一致），迁移过程严格遵守了。

---

## 8. 其他值得留意但不需立即修复的点

1. **`pkg/asaversion/asaversion.go:122-127` 的类型断言**：`entry := v.(cacheEntry)` 没有 `.()` 双值断言保护。`sync.Map` 在误用（外部写非 `cacheEntry`）时会 panic。当前 `pkg/asaversion` 是唯一生产者，攻击面 0。**建议**：保持现状，但若未来 `pkg/asaversion` 的 `Resolver` 暴露给外部库使用，需改为 `value, ok := r.cache.Load(...); if !ok { ... }` 的安全形式。
2. **`pkg/iox.Relay` 调用方负责关闭 `dst`**：`Relay` 自身不关闭（注释 `relay.go:10-19` 没说，但实现也没关）。`asaapilog_linux.go:107/148` 用 `defer dst.Close()` 与 `defer src.Close()` 正确处理。**建议**：在 `Relay` 文档明确「不关闭 src/dst」，避免下一个调用方误解。
3. **`pkg/procmatch.Matcher.isWineSideCmdline`** 用反斜杠 + exe 名判定 Wine-side cmdline（`procmatch.go:55-62`）。Wine 通过 umu-run 启动时 `cmdline` 确实是 Windows path 形式，这条判据保留正确**且与迁移前一致**——审阅时确认 `instance/common.go:36` 的 `gameProcMatcher = procmatch.New([arkExeName, asaApiLoaderExeName], "GameThread")` 调用方式与原 `procmatch` 模块的入口契约吻合。
4. **`pkg/xvfb/manager.go:99` 的 `current atomic.Pointer[managedXvfb]` + `mu`**：读路径（`Status()`）无锁、写路径（`Acquire/Stop/ensure`）拿锁。`adoptTried` 仅由 `mu` 守护，未声明 atomic——这是有意的（外部读 `adoptTried` 不存在），与迁移前一致。
5. **`internal/runner/umu_linux.go:49` 的 `ChildIDs` 签名**：`func() (uint32, uint32, bool)` 与 `pkg/umu.Config.ChildIDs` 完全一致。`func() string` 的 `UserName` 同样匹配（`umu_linux.go:55`）。`Credential` 字段经 `resolveRuntimeCredential` 适配 `*syscall.Credential` 也对得上。导入路径、调用约定经全文 grep 确认无不一致。
6. **`pkg/wineprefix/wineprefix_linux.go`**（未列出全文件，但 grep 过导出符号）：`Manager.Dir / KeyFor / Ensure / Remove / Status / PrepareSharedWrite / Reconcile / CheckSharedReady / LowerNeedsWork / OverlayRoot / UnmountedOverlayDirs` 全部存在；`umu_linux.go:39-77` 的 `umuRuntimeFor` 与 `wineprefixMgrFor` 转译对得上。
7. **`pkg/umu/umu_linux.go:305-315` 的 `runtimeUserNameHint` 是死方法**：

    **文件**：`pkg/umu/umu_linux.go:305-315`

    ```go
    // runtimeUserNameHint is a best-effort label for RuntimeEnv's USER/LOGNAME
    // rewrite. Runtime doesn't own the runtime user's name (sysuser does) — the
    // caller only injected uid/gid/credential resolution, not the name — so this
    // falls back to a generic placeholder rather than needing a fifth callback
    // for a cosmetic env var.
    func (r *Runtime) runtimeUserNameHint() string {
        if uid, _, managed := r.config().childIDs(); managed {
            return fmt.Sprintf("uid%d", uid)
        }
        return ""
    }
    ```

    **验证**：

    - `grep -rn "runtimeUserNameHint" .` 全仓只命中两行：注释（L305）与定义（L310）；`r.runtimeUserNameHint(...)` / `Runtime(...).runtimeUserNameHint` 均无调用方。
    - `RuntimeEnv`（`pkg/umu/umu_linux.go:591`）的实际签名是 `func RuntimeEnv(base []string, home, userName string) []string`，第三个参数直接是 `userName`，不向 Runtime 要 hint。
    - 唯一调用点 `pkg/umu/umu_linux.go:279` `cmd.Env = RuntimeEnv(cmd.Env, cfg.homeDir(), cfg.userName())` 走的是 `Config.userName()`（`umu.go:93`），后者读 `Config.UserName func() string` 回调。
    - 该回调确实存在（`Config.UserName`，`umu.go:62`），由调用方（`internal/runner/runtimeuser_linux.go:111-113` 的 `runtimeUserName`）注入。所以「avoiding a fifth callback for a cosmetic env var」这条动机已经不成立——**第五个回调本来就在 `Config` 里**。

    **来历**：`git log -S runtimeUserNameHint -- pkg/umu/` 单点命中 `df54111 refactor(runner): 阶段I——umu 运行时与 Wine 前缀布局下沉到 pkg/umu、pkg/wineprefix`。自引入第一天起就没人调用，是迁移过程中留下的草稿。

    **严重程度**：低。无运行时影响、不影响测试、不影响可观察性，纯属代码卫生。

    **建议**：直接删除该方法与其上方整段注释。`Config.UserName` + `Config.userName()` + `RuntimeEnv` 的现有链路已经覆盖了所有使用场景。

---

## 9. 修复优先级总结

| # | 级别 | 一句话 |
| --- | --- | --- |
| 1 | **P1** | 修复 `pkg/xvfb/xvfb_linux.go` watch 的退避序列与三条诊断日志（见 §1 diff） |
| 2 | P3 | 清理三处重复的 `noSuchID` 常量（§2） |
| 3 | P3 | `findAdminTool` 三处重复实现加注释互引（§3） |
| 4 | P3 | 删除 `pkg/umu/umu_linux.go:305-315` 的死方法 `runtimeUserNameHint`（§8.7） |
| 5 | - | `asaapilog_linux.go` 语义差异无需处理（§4） |
| 6 | - | `pkg/iox.Relay` 文档补一句「不关闭 src/dst」（§8.2） |

---

## 10. 整体迁移质量评估

- **编译与 vet**：✅ 双平台零错误。
- **依赖方向**：✅ 单向、无环、runner 不依赖 instance、pkg 不依赖 internal。
- **API 稳定性**：✅ runner 顶层签名（`Run / EnsureRuntime / EnsurePrefix / SharesWinePrefix / StopManagedDisplay / DisplayStatus / Preflight / EnsureRuntimeUser / SharedAccessStatus` 等）原样保留；alias（`Problem / DLLOrigin / VCRedistInfo / VCRedistDLLInfo / PrefixInfo / Info`）类型定义正确。
- **测试覆盖**：迁移后保留的测试（`xvfb_linux_test.go / display_linux_test.go / runtimeuser_linux_test.go / runner_linux_test.go / runner_windows_test.go / sharedaccess_test.go / xvfb_linux_test.go / pkg/shareacl/shareacl_test.go / pkg/asaversion/waitnewest_test.go` 等）覆盖了被迁移函数的关键分支；新增的 `pkg/tail/waitnewest_test.go` 与 `pkg/iox/relay_test.go`（grep 命中）专门覆盖拆出来的纯函数。
- **行为保真**：除 §1 外全部一致。
- **文档质量**：每个新拆包的 `// Package …` 注释都清楚地声明了「认识什么、不认识什么」，并指向上游决策文档（`docs/LINUX_RUNTIME_PRIVILEGE_PLAN.md / ARKAPI_LINUX_VCREDIST_PLAN.md / XVFB_DISPLAY_PLAN.md` 等），符合既有风格。

**唯一必修**：§1 的 watch 看门狗。其余皆为可清理项，可随下次动该区域代码时一并处理。

---

# 附录 Y：文件路径对照（2026-09-29）

| 文档中的路径 | 实际路径 / 现状（核对于 2026-09-29） |
|---|---|
| 顶层包 `config/`、`process/`、`certmgr/`、`rconx/`、`realtime/`、`state/`、`installer/`、`mirror/`、`instance/`、`countdown/`、`batchmanage/`、`schedule/`、`updatemanage/`、`webapi/`、`gui/`、`svcmgr/`、`actions/`、`backup/`、`frpmanage/`、`syncthingmanage/`、`parseserver/` | 已全部迁入 `internal/`（同名子目录），见 `docs/INTERNAL_LAYOUT_MIGRATION.md` |
| `pkg/` 下包 | 与文档一致：`archive,arkcache,asaversion,console,display,download,fsutil,iox,linuxdeps,logger,netutil,problem,procmatch,procnet,proctree,procx,pyfinder,resourcegate,serverinfo,shareacl,steamrt,sysuser,tail,umu,vcredist,wineprefix,winnetetw,xvfb` |
| 旧顶层包 `asaserver/` | 已整体迁入 `internal/` |
| `internal/runner/` 文件 | 现为 `display_linux.go`、`prefix_windows.go`、`preflight_linux.go`、`python_linux.go`、`runner.go`、`runner_linux.go`、`runtimeuser_linux.go`、`runtimeuser_windows.go`、`sharedaccess_linux.go`、`umu_linux.go`、`vcredist_linux.go`、`vcredist_windows.go`、`xvfb_linux.go`（+ 测试） |

# 附录 Z：合并与同步记录（2026-09-29）

本文件由 `docs/RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md`、`docs/RUNNER_INSTANCE_PACKAGE_SPLIT_TODO.md`、`docs/RUNNER_INSTANCE_PACKAGE_SPLIT_REVIEW.md` 于 2026-09-29 逐字物理合并而成（方案甲）；三个源文件正文未作删减或改写；指向旧文档名的引用已改指本文。
