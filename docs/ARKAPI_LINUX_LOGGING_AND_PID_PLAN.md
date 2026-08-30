# ArkApi 实例在 Linux 上的两个后续缺陷：日志串台与游戏 PID 找不到

> 前置：`docs/ARKAPI_LINUX_VCREDIST_PLAN.md`（VC++ 运行时 + §9 图形显示）。
> 那两条解决之后，ArkApi 实例**能真正跑起来了**，随之暴露出下面两个此前被
> 「根本起不来」掩盖住的问题。两个都是 **Linux 专属**，Windows 行为不受影响。
>
> 状态：**已取证、已定位、待实现**。本文是实现前的设计文档。

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
| Linux | `xvfb-run → python3 → umu-run → srt-bwrap → pv-adverb → proton → wine` | 这整条链的 stdout/stderr。加载器在最里面，而且**它不往控制台打业务日志** |

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
| B | 让 API 直接 tail 镜像里最新的 `ArkApi_*.log` | ✅ 拿到的是**真日志**；要处理「每次启动换文件名」与「文件还没出现」 |
| C | 由 asa-server 把 ArkApi 的日志转抄进 `arkAsaApi.log` | 多一个复制协程与一份重复数据，且要处理换文件；B 能做到的它都要做，收益为零 |

**选 B**，并把 PTY 那份**留下但改用途**：

1. **PTY 输出 → 启动器日志**。落到 `instances/<name>/launcher.log`（新文件名），
   语义变成「这次启动的启动链输出」。这份**必须留着** —— §9 那次
   「加载器退出码 3、零输出」的排障全靠它，`installer` 侧也早有同类先例
   （`logs/verify-arkapi-launch.log`）。同样每次启动清空。
   - Windows 上 PTY 就是加载器控制台，语义与今天一致；为不制造无谓差异，
     Windows 继续写 `arkAsaApi.log`，Linux 写 `launcher.log`。这一处平台差异是**真实存在**
     的（PTY 里装的东西不同），不该用同一个文件名假装它不存在。
2. **`GET /api/logs/:name/asaapi` 改为解析并 tail 最新的 `ArkApi_*.log`**：
   - 连接建立时在镜像的 `Win64/logs/` 里按 mtime 取最新的 `ArkApi_*.log`；
   - 一个都没有时（实例没启动过 / 刚启动还没写出来）→ 回落到 `launcher.log`（Linux）
     或 `arkAsaApi.log`（Windows），并先推一行说明「当前展示的是启动器输出，
     ArkApi 日志尚未生成」。**不能静默回落** —— 那正是本问题让人困惑的原因；
   - Windows 也走同一套解析（那边 `Win64/logs/` 同样存在），拿不到才回落到 PTY 那份。
     两平台的「插件日志」从此是同一样东西。
3. 前端不需要改：路由与 SSE 形状不变。

**不做的事**：不把 ArkApi 的日志目录从镜像里挪出来。它是 ArkApi 自己按 exe 目录算出来的，
改它要动 ArkApi 的配置，属于替用户改第三方组件的行为。

### 1.5 实现要点

- 新增 `instancepkg.GetAsaApiRuntimeLogPath(instanceName) (string, error)`：
  在 `mirror.InstanceMirrorDir(name)/ShooterGame/Binaries/Win64/logs/` 下取最新
  `ArkApi_*.log`，没有则返回哨兵错误 `ErrNoArkApiLog`。**不创建文件**
  （与 `GetAsaApiLogFilePath` 会建空文件的行为相反 —— 那是给 tail 兜底用的，
  这里需要「有没有」是个真实答案）。
- `GetAsaApiLogFilePath` 保留，语义收窄成「PTY 捕获的那一份」，Linux 上改名
  `launcher.log`。**旧文件不迁移**：它每次启动都被 `O_TRUNC` 重写，没有历史价值。
- 目录穿越：文件名必须用 `filepath.Base` 收敛后再拼，且只接受
  `^ArkApi_.*\.log$`（实例名已由 `apiresp.ValidateInstanceName` 把关，但这一层是别人写的文件名）。

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
| `internal/instance/gameproc_linux.go` | 候选集扩到两个 exe 名；用 `/proc/<pid>/comm == "GameThread"` 在候选里挑游戏进程；没命中时回落到今天的规则 |
| `internal/instance/gameproc_windows.go` | 不改（按镜像名匹配，本来就对） |
| `internal/instance/common.go` | `waitForGamePID` 增加超时参数与「启动器已退出」信号；两个超时常量命名化 |
| `internal/instance/server.go` | 传 ArkApi 专用超时；`waitForGamePID` 失败时 `KillTree` 收尾；PTY 落盘目标改为 `launcher.log`（Linux） |
| `internal/instance/common.go` 或新文件 | `GetAsaApiRuntimeLogPath()`：镜像 `Win64/logs/` 里最新的 `ArkApi_*.log` |
| `internal/webapi/logapi/logapi.go` | `streamAsaApiLogs` 优先 tail 真 ArkApi 日志，取不到时回落并**显式告知**回落原因 |
| `docs/LINUX_DEPLOYMENT.md` | 排障表加两行（插件日志看哪个文件；ArkApi 实例 30 秒超时） |
| `docs/ARKAPI_LINUX_VCREDIST_PLAN.md` | §9 末尾交叉引用本文 |
| `CLAUDE.md` | `instance/` 目录树补一句 ArkApi 下的进程形状 |

预计 ~150 行实现 + ~80 行测试。

---

## 4. 验证计划

### 4.1 单测（可在 Windows 上跑的部分）

- 候选筛选与 comm 挑选逻辑抽成纯函数（输入：`[]struct{cmdline, comm}`），
  用本文 §2.2 两张真机快照**逐字**作为用例：
  - ArkApi 形态的 3 个进程 → 选中 `comm=GameThread` 那个；
  - 非 ArkApi 形态的 2 个进程 → 选中 `comm=GameThread` 那个（回归）；
  - 把 `GameThread` 全部改名 → 回落到 `\ArkAscendedServer.exe` 那条（防 UE 改名）；
  - 只有加载器、没有游戏进程 → 空集（不能误把加载器当游戏，否则停不掉）。
- 日志路径解析：多个 `ArkApi_*.log` 取最新；目录不存在 → 哨兵错误；
  非 `ArkApi_*.log` 的文件不被选中；文件名带 `../` 时被 `filepath.Base` 收敛。

### 4.2 真机（Linux）

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
