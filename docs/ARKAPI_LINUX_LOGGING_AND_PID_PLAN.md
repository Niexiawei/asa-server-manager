# ArkApi 实例在 Linux 上的两个后续缺陷：日志串台与游戏 PID 找不到

> 前置：`docs/ARKAPI_LINUX_VCREDIST_PLAN.md`（VC++ 运行时 + §9 图形显示）。
> 那两条解决之后，ArkApi 实例**能真正跑起来了**，随之暴露出下面两个此前被
> 「根本起不来」掩盖住的问题。两个都是 **Linux 专属**，Windows 行为不受影响。
>
> 状态：**已实现**（2026-08-30）。问题一按 §1.4 的**方案 C**落地（API 层零改动），
> 问题二连同 §2.5 的三条一并修复。落地清单见 §3，验证结果见 §4。

---

## 0. 摘要

| # | 现象 | 根因 | 影响面 |
|---|---|---|---|
| 1 | 实例的 `arkAsaApi.log` 里全是 umu/pressure-vessel/Proton 的输出，**没有一行 ArkApi 的内容** | Windows 上 PTY 里跑的就是 `AsaApiLoader.exe` 的控制台，Linux 上 PTY 里跑的是 `umu-run` 整条包装链，而 ArkApi 自己只写文件日志、不往控制台打 | 只影响 Linux 上启用 ArkApi 的实例的「插件日志」面板 |
| 2 | 实例明明起来了（WSLg 里能看到游戏窗口、端口也在监听），30 秒后仍被标记为启动失败/停止 | `gameproc_linux.go` 要求进程 cmdline 里出现 `\ArkAscendedServer.exe`，但 **ArkApi 下游戏进程的 cmdline 写的是 `\AsaApiLoader.exe`** | 只影响 Linux 上启用 ArkApi 的实例，且是**致命**的：启动必失败，且游戏进程被留成孤儿 |

两个问题有同一个共性：**已有实现是照着「非 ArkApi 的 Linux 启动」调通的，
而 ArkApi 把进程与输出的形状换掉了。**

---

## 1. 问题一：`arkAsaApi.log` 收到的是 umu 的日志

### 1.1 取证

真机（WSL2 Ubuntu，`/opt/asa-server`，实例 `jibian-pve`，2026-08-30 12:28 那次启动）：

```
$ wc -l /opt/asa-server/basedir/instances/jibian-pve/arkAsaApi.log
34
$ head -6 !$
INFO: umu-launcher version 1.4.4 (3.12.3 ...)
INFO: steamrt3 updates disabled, skipping
INFO: Running 'GE-Proton10-34' using runtime 'sniper'
INFO: Running 'steamrt3' using runtime 'host'
pressure-vessel-wrap[145751]: W: Using glibc from provider system for ...
pressure-vessel-wrap[145751]: W: Using libdrm from provider system for ...
```

34 行**全部**是启动器噪声。同一次启动，ArkApi 自己的日志在另一个地方，内容完全正确：

```
$ ls /opt/asa-server/basedir/server-files-tmp-jibian-pve/ShooterGame/Binaries/Win64/logs/
ArkApi_368_2026-08-30_12-28.log
$ cat …
ARK:SA Api V2.03 … Loading...
Added DLL search directory: Z:\…\Win64\ArkApi
Checking for a verified local cache for 66cc028c….zip
Reading cached offsets / Reading cached bitfields / Initialized hooks
API was successfully loaded
Loaded plugin Ark:SA Permissions V1.1 (Manage permissions groups)
AShooterGameMode::InitGame was called
```

### 1.2 根因：PTY 在两个平台上装的不是同一样东西

`internal/instance/server.go` 里这段（现状）：

```go
go func() {
    defer apiLogFile.Close()
    _ = console.CleanScreenOutput(handle.PTY, apiLogFile)
}()
```

代码本身没错，错在**前提在 Linux 上不成立**：

| | PTY 里跑的是谁 | 于是 `handle.PTY` 里流的是什么 |
|---|---|---|
| Windows | `AsaApiLoader.exe` 本体 | 加载器的控制台输出 —— 正是「插件日志」想要的 |
| Linux | `python3 → umu-run → srt-bwrap → pv-adverb → proton → wine`（早期版本前面还有一层 `xvfb-run`，改成自管 Xvfb 后没有了，见 `docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md`） | 这整条链的 stdout/stderr。加载器在最里面，而且**它不往控制台打业务日志** |

`runner` 包的设计取向是「对每个 exe 一视同仁」（见包注释），这是对的；
问题出在 `instance` 层沿用了 Windows 的语义解释 Linux 的 PTY。

补充一条实测：ArkApi **不是**「日志被噪声淹没」，而是**压根不往控制台写**业务日志 ——
手工跑加载器时控制台除了 Wine 的 fixme 之外什么都没有，全部内容都进了
`Win64/logs/ArkApi_*.log`。所以「过滤掉 umu 噪声」这条路是行不通的，过滤完是空的。

### 1.3 ArkApi 日志文件的实际形状

- 位置：`<游戏 exe 所在目录>/logs/`，实例场景下就是**镜像**里的
  `server-files-tmp-<instance>/ShooterGame/Binaries/Win64/logs/`；
- 命名：`ArkApi_<wine 侧 PID>_<YYYY-MM-DD_HH-MM>.log`，**每次启动一个新文件**；
- 轮转：由 ArkApi 自己按 `config.json` 的 `DeleteOldLogs.{Enable,MaxAge}` 清理（默认开、24 小时）；
- 属主：`asa-umu-runtime`（降权后的游戏进程创建），`0644`，asa-server 以 root 读没问题；
- 生命周期：镜像每次启动都会重建（实测目录 mtime 与启动时刻一致），所以 `logs/` 是**每次启动重来**的。

### 1.4 方案

三个候选：

| 方案 | 做法 | 评价 |
|---|---|---|
| A | 过滤 PTY 流里的 umu 噪声 | ❌ 不可行。ArkApi 不往控制台写，过滤完是空文件 |
| B | 让 API 直接 tail 镜像里最新的 `ArkApi_*.log` | 拿到的是真日志，但「每次启动换文件名」「文件还没出现」「镜像重建」这些复杂度全压在 HTTP 处理器上 |
| C | **由 asa-server 把 ArkApi 的日志转抄进 `arkAsaApi.log`** | ✅ **选它**：同样拿到真日志，而复杂度收在启动路径里一次解决，**API 层与前端零改动** |

**选 C**（用户决策）。B 与 C 拿到的内容完全相同，差别只在那三件麻烦事放在哪一层；
放在启动路径里更合适 —— 那里本来就知道「这次启动是什么时候开始的」（区分本次日志与
镜像里遗留的上几次所必需），而 HTTP 处理器每次连接都要重新推导一遍。

于是 `arkAsaApi.log` 的语义统一成「**ArkApi 的输出**」，两个平台一致，只是写入者不同：

| | 谁往 `arkAsaApi.log` 里写 | PTY 去哪 |
|---|---|---|
| Windows | PTY（里面跑的就是加载器本体，控制台即业务输出）—— **与今天逐字相同** | 就是 `arkAsaApi.log` |
| Linux | 转抄协程：镜像里本次的 `ArkApi_*.log` → `arkAsaApi.log` | `instances/<name>/launcher.log`（新文件） |

`launcher.log` **必须留着**：`ARKAPI_LINUX_VCREDIST_PLAN.md` §9 那次「加载器退出码 3、
零输出」的排障全靠它，`installer` 侧也早有同类先例（`logs/verify-arkapi-launch.log`）。
同样每次启动清空。

Windows 侧**刻意不改**成读 ArkApi 的文件日志：那条路今天是好的，而 Windows 是已交付
平台，不为对称性去动一个没坏的东西。

**不做的事**：不把 ArkApi 的日志目录从镜像里挪出来。它是 ArkApi 自己按 exe 目录算出来的，
改它要动 ArkApi 的配置，属于替用户改第三方组件的行为。

### 1.5 实现要点

- `internal/instance/arkapilog.go`（**无 build tag**，纯路径与时间比较，可在 Windows 上单测）：
  `newestArkApiLog(dir, notBefore)` 取本次启动的日志，没有则返回哨兵错误 `ErrNoArkApiLog`。
  - `notBefore` 不是可选的：**镜像是增量同步的**，上几次启动留下的 `ArkApi_*.log`
    还在原地（真机上一个目录里躺着三份）。没有这个闸门，转抄协程会在本次日志出现之前
    一直误认上一次那份，把陈旧内容当成实时输出贴给用户。
  - 文件名用 `filepath.Base` 收敛后再拼，且只接受 `ArkApi_*.log` —— 那是别人写的文件名。
- `internal/instance/asaapilog_{linux,windows}.go`：同一个 `startAsaApiLogging` 签名，
  平台差异关在这里，`server.go` 只有一行调用。
- 转抄协程用**轮询**而不是 `pkg/tail` 的 fsnotify：两端都是普通文件、需要的是字节级
  透传而不是按行分发，一个「读到 EOF 就歇 1 秒」的循环没有 watch 上限、文件替换、
  事件丢失这些边角问题。
- 协程生命周期绑在 `launcherExited` 上（就是 §2.5 b 新加的那个信号），
  启动链一结束就把尾巴读干净然后退出，不留常驻协程。
- **不静默**：等待期、找不到、找到了各写一行 `[asa-server] …` 说明进 `arkAsaApi.log`。
  「静默」正是这个问题最初难查的原因。

---

## 2. 问题二：`waitForGamePID` 必然 30 秒超时

### 2.1 现象

启用 ArkApi 的实例：WSLg 里游戏窗口已经出现、UDP 端口已经在监听，
30 秒后 `startServerInternal` 仍以 `failed to start server: ArkAscendedServer.exe did not
appear within 30 seconds` 失败，实例被标记为停止 —— 而**游戏进程还活着**，变成孤儿。

实例目录里的旁证（12:28 那次启动）：

```
-rw-rwxr--  6 Aug 30 12:28  asa_api_pid      ← 写了
-rw-rwxr--  6 Aug 30 12:28  launcher_pid     ← 写了
-rw-rwxr--  5 Aug 29 16:40  pid              ← 停留在昨天，SaveInstancePID 从没执行到
```

### 2.2 取证：进程快照

用与实例启动完全相同的参数形状手工拉起，55 秒后扫 `/proc`，
筛出 cmdline 里带 `AltSaveDirectoryName=<savedir>` **且**含反斜杠（即 Wine 侧）的进程：

**启用 ArkApi（`AsaApiLoader.exe`）：**

| pid | ppid | comm | threads | cmdline |
|---|---|---|---|---|
| 2634 | 2630 (python3) | `umu.exe` | 1 | `c:\windows\system32\umu.exe /opt/…/AsaApiLoader.exe …` |
| 2705 | 2589 (pv-adverb) | `AsaApiLoader.ex` | 1 | `Z:\…\Win64\AsaApiLoader.exe …` |
| **2722** | 2589 (pv-adverb) | **`GameThread`** | **36** | `Z:\…\Win64\AsaApiLoader.exe …` |

```
$ ss -lunp | grep 41778
UNCONN 0 0 0.0.0.0:41778 0.0.0.0:*  users:(("GameThread",pid=2722,...),("wineserver",pid=2636,...))
```

**不启用 ArkApi（直接 `ArkAscendedServer.exe`，今天工作正常的那条路）：**

| pid | ppid | comm | threads | cmdline |
|---|---|---|---|---|
| 3152 | 3148 (python3) | `umu.exe` | 1 | `c:\windows\system32\umu.exe /opt/…/ArkAscendedServer.exe …` |
| **3228** | 3107 (pv-adverb) | **`GameThread`** | **43** | `Z:\…\Win64\ArkAscendedServer.exe …` |

### 2.3 根因

`internal/instance/gameproc_linux.go` 现在的判据是：

```go
if strings.Contains(p.CommandLine, `\`+arkExeName) {   // arkExeName = "ArkAscendedServer.exe"
```

而上表第一行就说明了问题：**ArkApi 下游戏进程的 cmdline 里根本没有
`ArkAscendedServer.exe` 这个词**，它写的是 `AsaApiLoader.exe`。

原因是 `AsaApiLoader.exe` 创建游戏进程时**把自己的整条命令行原样传了过去**
（`CreateProcess(applicationName=ArkAscendedServer.exe, commandLine=<自己的命令行>)`
这种常见写法），于是新进程的 argv[0] 仍然是加载器。Windows 上这不成问题 ——
那边 `queryGameProcesses` 匹配的是 **`Win32_Process.Name`（镜像名）**，那确实是
`ArkAscendedServer.exe`；Linux 上镜像名是 `wine64-preloader`，用不了，只能看 cmdline，
于是撞上了这个差异。

也就是说，`gameproc_linux.go` 头部注释里记录的那次修复
（「只按 cmdline 匹配、用反斜杠区分 Wine 侧与包装器」）**在非 ArkApi 路径上是对的，
在 ArkApi 路径上失效**，而它落地时 ArkApi 还起不来，没有机会被发现。

### 2.4 新判据

要在上表里唯一地挑出游戏进程，可用的信号只有三个：

| 信号 | 能不能用 |
|---|---|
| ppid | ❌ 加载器与游戏的父进程**都是 pv-adverb**（Wine 会重挂父进程），没有区分度 |
| 线程数（36 vs 1） | ❌ 与启动进度相关，轮询早期两边都可能是个位数 |
| **`comm`（`GameThread` vs `AsaApiLoader.ex`）** | ✅ 两种形态下游戏进程的 comm **都是 `GameThread`**（UE 给主线程起的名字），且所有包装器都不是 |

**选 comm，但不让它单打独斗。** 判据分两层：

1. **候选集**：cmdline 含 `AltSaveDirectoryName=<savedir>` 标记，**且**含 Windows 形式
   （反斜杠）的已知 exe 路径 —— `\ArkAscendedServer.exe` **或** `\AsaApiLoader.exe`。
   这一层保留了现有实现「用路径形式把 Wine 侧与包装器分开」的正确内核，只是把
   ArkApi 那个 exe 名补进来。
2. **在候选里挑游戏**：取 `comm == "GameThread"` 的那个。
   - 没有任何候选命中 `GameThread` 时，**回落到「cmdline 里是 `\ArkAscendedServer.exe` 的
     那个候选」**，即今天的行为 —— 这样即使将来 UE 改了线程名，非 ArkApi 路径也不会跟着坏。

为什么必须挑准、不能凑合拿加载器的 PID：**加载器不是游戏的父进程**（两者的 ppid 都是
pv-adverb），所以 `killGameServer` 走 `procx.TerminateTree(loaderPID)` **杀不到游戏**。
拿错 PID 会把「启动失败」换成更隐蔽的「停不掉」。

`comm` 被内核截到 15 字节（`AsaApiLoader.ex` 就是被截过的），`GameThread` 只有 10 字节，
不受影响 —— 但比较必须是**精确相等**，不能用前缀匹配。

`procx.Win32Process` 目前只带 `Name/ProcessId/CommandLine`，其中 Linux 的 `Name` 已经是
「exe 的 basename，读不到时回落 `comm`」。游戏进程的 `/proc/<pid>/exe` **能**读到
（指向 `wine64-preloader`），所以 `Name` 拿不到 `GameThread`。因此需要在
`gameproc_linux.go` 里**单独读一次 `/proc/<pid>/comm`**，不动 `procx` 的公共结构
（那是给两个平台共用的 WMI 形状，为一个平台的细节加字段不划算）。

### 2.5 顺带暴露的两个问题（同一处修，别分两次）

**a) 30 秒这个常量对 ArkApi 太紧。**
ArkApi 在创建游戏进程之前要先下载 offsets cache（`Downloading cache archive
<sha256>.zip`，走第三方 CDN）。真机上从启动到端口监听是 44~52 秒，游戏进程本身出现在
20 多秒 —— **离 30 秒只差几秒**，CDN 慢一点就必然超时。
建议：把超时提到 `arkAsaApiRunning ? 180s : 30s`，并把它变成命名常量而不是字面量。
（不是无脑放大：非 ArkApi 路径 30 秒是够的，实测游戏进程 2~3 秒就出现。）

**b) 启动器已经退出时应当立刻失败，而不是等满超时。**
现在 `waitForGamePID` 只会等；加载器因为缺显示之类的原因**秒退**时，用户仍要盯着看
30 秒才拿到一句「没出现」。`handle.Wait()` 那个 goroutine 已经知道进程什么时候结束了，
把这个信号接进 `waitForGamePID`（多一个 `launcherExited <-chan struct{}`），
失败信息就能变成「启动器已退出（退出码 N），游戏进程从未出现」。

**c) 失败时留下孤儿进程。**
`waitForGamePID` 失败后直接 `return startErr`，而 `handle` 那条进程链还活着 ——
用户观察到的「游戏窗口还在，实例却是停止」就是这个。应当在这条失败路径上
`procx.KillTree(handle.LauncherPID)`。这与
`docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md` 记录过的孤儿问题是同一类，
只是当时没覆盖到这条分支。

### 2.6 不受影响、无需改动的地方（已核对）

- `internal/process/process_linux.go` 的 `isExpectedProcessPlatform`：它按 cmdline 匹配
  `expectedExecutables`，而那张表**已经同时包含** `arkascendedserver.exe` 与
  `asaapiloader.exe`，所以保存下来的 PID 的存活校验在 ArkApi 下本来就是通的。
- `findServerPIDBySaveDir` 与 `waitForGamePID` 共用 `queryGameProcesses`，
  改一处两处都好。
- Windows 侧 `gameproc_windows.go` 不动：那里按镜像名匹配，本来就是对的。

---

## 3. 改动清单

| 文件 | 改动 |
|---|---|
| `internal/instance/gameproc.go`（新增，**无 build tag**） | `isWineSideGameCmdline` + `pickGameProcess` + `gameCandidate` + `gameProcessComm`。不加约束是为了让这条规则能在 Windows 上跑单测 —— 它的用例就是真机快照 |
| `internal/instance/gameproc_linux.go` | 接线：候选集扩到两个 exe 名，读 `/proc/<pid>/comm`，交给 `pickGameProcess` |
| `internal/instance/gameproc_windows.go` | **不改**（按镜像名匹配，本来就对） |
| `internal/instance/arkapilog.go`（新增，无 build tag） | `arkApiLogDir` / `newestArkApiLog` / `ErrNoArkApiLog` |
| `internal/instance/asaapilog_linux.go`（新增） | PTY→`launcher.log`；转抄协程 `ArkApi_*.log`→`arkAsaApi.log`（等待/跟随/收尾/说明行） |
| `internal/instance/asaapilog_windows.go`（新增） | 今天的行为原样搬过来（PTY→`arkAsaApi.log`） |
| `internal/instance/common.go` | `asaApiLoaderExeName` 常量；`waitForGamePID` 加 `timeout` 与 `launcherExited` 参数；`gamePIDWaitTimeout{,ArkApi}` 常量；`ErrLauncherExited` |
| `internal/instance/server.go` | `launchedAt`；`launcherExited` 通道 + `launcherExitErr`；`startAsaApiLogging` 一行替换原来的内联 PTY 落盘；按 ArkApi 选超时；失败时 `procx.KillTree` 收尾；`GetLauncherLogFilePath` |
| `internal/webapi/logapi/logapi.go` | **不改**（方案 C 的全部意义所在） |
| `internal/instance/{gameproc,arkapilog}_test.go`（新增） | 13 条用例，见 §4.1 |
| `docs/LINUX_DEPLOYMENT.md` | 排障表加两行 |
| `CLAUDE.md` | `instance/` 目录树补 ArkApi 下的进程与日志形状 |

实际 ~230 行实现 + ~210 行测试。

---

## 4. 验证

### 4.1 单测 —— ✅ 13/13 通过（`go test ./internal/instance/`，Windows 上真跑）

规则被抽成纯函数（`gameproc.go` / `arkapilog.go` 都不带 build tag），用例直接引用
§2.2 的真机快照：

| 用例 | 断言 |
|---|---|
| `TestIsWineSideGameCmdline` | 包装器全排除；**umu.exe 也必须排除**（它命令行里有反斜杠 `c:\windows\system32\umu.exe`，只判「有没有反斜杠」会误收） |
| `TestPickGameProcessArkApi` | ArkApi 形态两个命令行逐字相同的进程 → 选中 `comm=GameThread` 的 2722 |
| `TestPickGameProcessPlain` | 非 ArkApi 形态 → 选中 3228（回归） |
| `TestPickGameProcessFallsBackWhenCommChanges` | 把 comm 改成别的名字 → 回落到 `\ArkAscendedServer.exe` 那条（防 UE 改名） |
| `TestPickGameProcessRefusesTheLoader` | 只有加载器时**返回空**，绝不把它当游戏 |
| `TestPickGameProcessPrefersCommOverOrder` | comm 的优先级高于顺序 |
| `TestNewestArkApiLog*`（5 条） | 取最新；**早于 launchedAt 的一律不认**；只认 `ArkApi_*.log`；目录不存在 → `ErrNoArkApiLog`；同名目录不被选中 |

### 4.2 规则在真进程上的验证 —— ✅ 已执行（2026-08-30）

用 shell 复刻两层规则跑在真进程上，再用「谁持有游戏端口」做**独立**交叉核对：

```
=== 候选集（marker + Windows 路径形式的已知 exe）===
  pid=3949 comm=AsaApiLoader.ex
  pid=3970 comm=GameThread
第1层（comm=GameThread）选中: 3970
第2层（cmdline 含 \ArkAscendedServer.exe）选中: <无>
最终选中: 3970

=== 独立交叉核对：谁真的持有端口 41779 ===
  UNCONN 0.0.0.0:41779  users:(("GameThread",pid=3970,fd=21),("wineserver",pid=3881,...))
```

**选中的 PID 与持有端口的 PID 一致。** 同一次还顺带证实了 `notBefore` 闸门的必要性 ——
镜像的 `logs/` 里当时躺着**三份**日志（11:48、12:43、13:06），只有 13:06 那份的 mtime
晚于本次 `launchedAt`；没有闸门，转抄协程会先抓住 12:43 那份陈旧内容。

### 4.3 真机端到端（待部署后执行）

1. 启用 ArkApi 的实例经 API 启动 → **实例状态变为 started**，`instances/<name>/pid`
   的 mtime 与本次启动一致，且内容等于 `comm=GameThread` 那个进程的 PID
   （用 `ss -lunp | grep <端口>` 交叉核对同一个 PID 持有端口）。
2. 停止该实例 → `ps` 里 `GameThread` / `AsaApiLoader` / `umu-run` / `Xvfb` 全部消失
   （这是「拿对 PID」的真正验收点）。
3. `GET /api/logs/<name>/asaapi` → 看到的是 `ARK:SA Api V2.03 … API was successfully
   loaded … Loaded plugin …`，**不是** `INFO: umu-launcher version …`。
4. `instances/<name>/launcher.log` 里仍能看到完整的 umu/Proton 输出（排障能力不倒退）。
5. **回归**：不启用 ArkApi 的实例启动/停止一切照旧，游戏 PID 仍在 2~3 秒内解析出来。
6. 故意制造失败（临时把 `AsaApiLoader.exe` 改名 / 关掉显示）→ **不等满超时**就报错，
   且失败后 `ps` 里没有残留进程。

---

## 5. 风险

| # | 风险 | 缓解 |
|---|---|---|
| 1 | `comm == "GameThread"` 依赖 UE 的线程命名，将来可能变 | 只作为**候选内的挑选规则**，不作为候选准入；没命中时回落到今天的路径规则，非 ArkApi 路径永不受影响；单测里专门有一条「GameThread 改名」用例 |
| 2 | 加载器与游戏 cmdline 完全相同，若某天两者都没有 `GameThread` 且都在候选里，可能选错 | 宁可选空也不选错：回落规则只认 `\ArkAscendedServer.exe`，ArkApi 形态下它不存在 → 返回空 → 超时报错。**「启动失败」比「停不掉的孤儿」好诊断得多** |
| 3 | ArkApi 日志文件名格式若上游改动，解析会落空 | 回落到 `launcher.log` 并显式说明，功能降级而非报错；匹配用宽松的 `ArkApi_*.log` 前缀而非精确解析 PID/时间戳 |
| 4 | ArkApi 超时提到 180 秒后，真正起不来的实例要等更久 | 由 §2.5 b 的「启动器退出即失败」抵消：真正的失败通常表现为加载器秒退，不会走到超时 |
| 5 | 镜像每次启动重建，`logs/` 目录在 tail 建立连接的瞬间可能不存在 | 解析失败即回落，前端始终有内容可看；不做重试轮询（连接是用户主动发起的，刷新即可） |

---

## 附录：复现命令

```bash
# 问题二：把带 savedir 标记、且 cmdline 是 Windows 形式的进程连同 comm 一起列出来
SAVEDIR=<实例的 AltSaveDirectoryName>
for p in /proc/[0-9]*; do
  cl=$(tr '\0' ' ' < "$p/cmdline" 2>/dev/null) || continue
  case "$cl" in *"AltSaveDirectoryName=$SAVEDIR"*) case "$cl" in *'\'*)
      echo "pid=${p#/proc/} comm=$(cat $p/comm) ppid=$(awk '/^PPid:/{print $2}' $p/status)"
      echo "    ${cl:0:120}" ;; esac ;;
  esac
done

# 谁真的持有游戏端口（拿来交叉核对选中的 PID 对不对）
ss -lunp | grep <Port>

# 问题一：对照两个文件
head -5 {BaseDir}/instances/<name>/arkAsaApi.log          # 现在是 umu 噪声
ls -t {BaseDir}/server-files-tmp-<name>/ShooterGame/Binaries/Win64/logs/ | head -1
```
