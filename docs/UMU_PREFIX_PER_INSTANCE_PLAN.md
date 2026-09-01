# Linux 多实例并发启动失败：两道闸（`PROTON_VERB` + ArkApi 撞 Wine 会话）

> 状态：**已完成**。根因两条，均已定位、修复并在真机验证通过
> （取证 2026-08-30，修复与验收 2026-08-31）。剩余未验项见 §11.3。
> 关联：`docs/LINUX_COMPATIBILITY_PLAN.md` §6 风险 6 与 P2 行的核对失误、
> `docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md`（本次证据顺带复验了它的修复）、
> `docs/ARKAPI_LINUX_VCREDIST_PLAN.md` §2.2 / 风险 7。
>
> **读这份文档的最短路径**：§0 结论速览 → §11.4 可用组合表。
> 中间各节按「取证 → 定位 → 修法 → 验收」的时间顺序保留了推理过程，
> 包括**两处被后续证据推翻的中途判断**（§5 开头的定位更正、§6 的两条证伪），
> 刻意不删——下一次遇到类似问题时，错误的路径与正确的结论同样有参考价值。

---

## 0. 结论速览

| # | 结论 | 判定 |
|---|---|---|
| 1 | **第一道闸：我们没设 `PROTON_VERB`，umu 默认用 `waitforexitandrun`，第二个实例卡在 `wineserver -w` 上等第一个实例退出，游戏进程从未被启动。** | ✅ **已确认**（§1 进程快照 + §2 日志） |
| 2 | 参考脚本的解法是 `export PROTON_VERB=run`（`ark_instance_manager.sh:884`，`start_server()` 第一行）+ 实例间 `sleep 30` 错开（L1197）。 | ✅ 已确认 |
| 3 | 我们丢掉这一行是一次**核对失误**：只对拍了启动那条 `env ...` 命令行，漏了函数开头的 `export`。`runner_linux.go:143-146` 的注释与 `LINUX_COMPATIBILITY_PLAN.md` P2 的"③"都因此写反了。 | ✅ 已确认 |
| 4 | **`PROTON_VERB` 只是第一道闸。** 补上它之后第二个实例不再排队，但换成卡在 `umu.exe` 之后、`AsaApiLoader.exe` 之前，依旧静默挂满 3 分钟。 | ✅ 已确认（§2.1） |
| 5 | **第二道闸是 ArkApi：共享 prefix = 共享 Wine 会话，而 Wine 的显示子系统每会话只初始化一次，第二个 `AsaApiLoader.exe` 因此起不来。** 关掉 ArkApi 后共享模式下多实例**正常可用**（2026-08-31 对照实验）。 | ✅ 已确认（§2.2） |
| 6 | 因此 **F1 与 F2 都是必需的**：F1（`PROTON_VERB=run`）修好共享模式下的多实例，F2（`per-instance`）是**同时用 ArkApi 跑多实例的唯一办法**。 | — |
| 7 | 脚本能共享运行的真正原因：**它完全不支持 ArkApi**，只跑 `ArkAscendedServer.exe`（全文件搜 `AsaApiLoader`/`ArkApi` 零命中）。它跑的正是「共享 + 无 ArkApi」这个可用组合。 | ✅ 已确认 |
| 8 | 原先怀疑的"进程组跨实例误伤"与"两个 wineserver 抢注册表"**均被证伪**（见 §6）。 | ❌ 排除 |
| 9 | 验证过程中另外发现并修掉两个缺陷：ArkApi 冲突缺少阻断（原为 3 分钟静默超时）、`.created-by-proton` 由 root 写入导致 per-instance 首次启动被属主自检拦下。 | ✅ 已修（§11.2） |

---

## 1. 真机证据（2026-08-30）

两个实例：`jibian-pve`（先启动，端口 7001）与 `meijue-pve`（后启动，端口 7003），
`prefix_mode: shared`，均启用 ArkApi（`AsaApiLoader.exe`）。

### 1.1 进程快照

| PID | PPID | PGID | SID | 角色 |
|---|---|---|---|---|
| 6981 | 6746 | 6981 | 6981 | **实例A** `umu-run`（= `launcher_pid`） |
| 6985 | 6981 | 6985 | 6985 | `srt-bwrap` ← setsid 断开 ① |
| 7036 | 6985 | 7036 | 7036 | `pv-adverb` ← 断开 ② |
| 7077 | 7036 | 7036 | 7036 | `python3 …/proton **waitforexitandrun** …` |
| 7086 | 7077 | 7036 | 7036 | `c:\windows\system32\umu.exe …` |
| 7088 | 7036 | 7088 | 7088 | **`wineserver`（服务端，A 拉起）** ← 断开 ③ |
| 7155 | 7036 | 7155 | 7155 | `AsaApiLoader.exe`（游戏进程）← 断开 ④ |
| 7174 | 7036 | 7155 | 7155 | `AsaApiLoader.exe` |
| 7287 | 6746 | 7287 | 7287 | **实例B** `umu-run` |
| 7291 | 7287 | 7291 | 7291 | `srt-bwrap` |
| 7331 | 7291 | 7331 | 7331 | `pv-adverb` |
| 7372 | 7331 | 7331 | 7331 | `python3 …/proton **waitforexitandrun** …` |
| 7375 | 7372 | 7331 | 7331 | **`wineserver -w` ← 实例B 停在这里** |

**实例 B 的进程链到 `wineserver -w` 就断了**：没有 `umu.exe`，没有 `AsaApiLoader.exe`。
游戏根本没被 exec 出来。

两个 wineserver 的环境变量都是 `WINEPREFIX=/opt/asa-server/basedir/umu-prefix/pfx/`
（umu 把 `WINEPREFIX` 重写成了 `<prefix>/pfx/`，与 `umu_linux.go:495` 的注释一致）。
7088 无参数 = 真正的服务端；7375 带 `-w` = 等待器。**是"一个 server + 一个排队者"，
不是"两个 server 抢注册表"**——顺带排除了原方案 §3.2(c) 设想的私有 `/tmp` 分支：
7375 能一直等下去，说明它找到了 7088 的 lock，容器间 `/tmp` 是共享的。

### 1.2 asa-server 自己的日志

```
21:04:18  Starting server for instance: meijue-pve
21:04:18  Instance mirror created successfully at .../server-files-tmp-meijue-pve
21:07:19  ERROR  failed to start server 'meijue-pve': 游戏进程在 3m0s 内没有出现
22:45:32  Starting server for instance: meijue-pve
22:48:34  ERROR  failed to start server 'meijue-pve': 游戏进程在 3m0s 内没有出现
```

两次启动、两次同样的超时。**不是崩溃、不是端口冲突、不是权限**——
`waitForGamePID` 在等一个永远不会出现的进程，因为 Proton 还没走到 exec 那一步。

---

## 2. 第一道闸：`waitforexitandrun` 是 umu 的默认动词

Proton 的启动动词只有两个与本问题相关：

| 动词 | 行为 |
|---|---|
| `run` | 直接启动目标 exe |
| `waitforexitandrun` | **先跑 `wineserver -w` 等该 prefix 的 wineserver 客户端清零**，再启动 exe |

`waitforexitandrun` 是 Steam 的用法：保证"上一次游戏完全退出后再启动新的"。
它的隐含前提是**一个 prefix 同时只跑一个游戏**。

umu-launcher 在未显式指定时默认取 `waitforexitandrun`。我们从不设 `PROTON_VERB`
（`internal/runner/runner_linux.go:179-186` 只设 `WINEPREFIX`/`GAMEID`/`PROTONPATH`/`UMU_RUNTIME_UPDATE`），
于是：

> **实例 A 在跑 → 它的 wineserver 有客户端 → 实例 B 的 `wineserver -w` 永远不返回 → 实例 B 永远起不来。**

这解释了用户观察到的"新实例把旧实例覆盖"：实际发生的是**新实例根本起不来**，
3 分钟后失败；期间 UI 上两个实例的状态互相打架，看起来就像后者顶掉了前者。

### 2.1 补上 `PROTON_VERB=run` 之后：换了个地方卡（2026-08-31 实测）

F1 生效，两个实例的 argv 都变成了 `proton run`，`wineserver -w` 彻底消失。
但第二个实例仍然超时，卡点前移到：

```
11911  proton run …AsaApiLoader.exe …meijue-pve
11912    └─ umu.exe  …AsaApiLoader.exe …meijue-pve     ← 到此为止，没有 AsaApiLoader.exe
```

`launcher.log` 的最后两行把交接点钉死了：

```
Proton: /opt/…/server-files-tmp-meijue-pve/…/AsaApiLoader.exe
Proton: Executable a unix path, launching with /unix option.
```

之后一个字都没有。对照实例 A 能跑通的那次，链条是
`proton → umu.exe → AsaApiLoader.exe(comm=GameThread)`。所以 B 的 Wine 起来了
（`umu.exe` 是 Windows 进程），但**加载器从未被 exec 出来**。

`waitForGamePID` 等满了整整 3 分钟才超时，而它同时监听 `launcherExited` ——
启动链没有崩，是真的挂住了。

### 2.2 第二道闸：ArkApi + 共享 Wine 会话（2026-08-31 对照实验确认）

**决定性实验**：保持 `prefix_mode: shared`，把两个实例的 `EnableAsaPlugin` 都关掉
→ **两个实例同时正常运行**。

于是三件事同时得到解释：

| 现象 | 解释 |
|---|---|
| 卡点恰好在 `umu.exe` 之后、`AsaApiLoader.exe` 之前 | 那是脚本从来不会走到的一步 |
| 关掉 ArkApi 就好了 | `ArkAscendedServer.exe` 根本不碰显示 |
| 参考脚本共享运行一直没事 | 它**完全不支持 ArkApi**，只跑 `ArkAscendedServer.exe` |

~~**机制**（推断，与全部观测一致，但未直接观测 winex11.drv 内部）：
一个 prefix = 一个 wineserver = **一个 Wine 会话**，而 Wine 的显示子系统
（`winex11.drv` / explorer 桌面）**每个会话只初始化一次**。第一个
`AsaApiLoader.exe` 起来时，会话已经绑定在它那个 X 显示上；第二个加载器带着
自己的 `DISPLAY` 加入同一个会话，
在创建窗口这一步静默挂住 —— 不报错、不退出、什么都不打。~~

> **2026-09-01 更正：上面这段机制已被实测否掉，划掉但保留原文。**
> 带 `WINEDEBUG=+x11drv,+win,+explorer` 复测，卡住那条链的 `launcher.log` 显示
> 它**每一句都说反了**：
>
> - 它不"绑定自己的显示"，而是**加入了先来那条链的 desktop**（窗口父级就是对方
>   explorer 建的桌面窗口 / Message 窗口，日志里没有 `started explorer`）；
> - 它的 x11drv **完整初始化了两次，零错误**；
> - 它没有"在创建窗口这一步挂住"——它一路走到 **Wine conhost 把控制台窗口建出来**
>   （`WineConsoleClass` + 对应 X 窗口），**建成功了**；
> - 它也不"静默"：41KB 日志且还在涨，只是没人接过它的 stderr 看。
>
> 真正的形状是：**控制台建好之后、exec 目标 exe 之前**，`umu.exe` 停在
> `futex_waitv` 不动了。**结论（闸真实存在）成立，但它在等谁至今未知**，
> 详见 `docs/SHARED_PREFIX_MULTI_ARKAPI_PLAN.md` §12。别再拿一个听起来合理的
> 解释把这个洞填上 —— 这份文档已经因此被挖开过一次了。

`AsaApiLoader.exe` 是本项目里**唯一**对 X 显示有硬性要求的东西
（`Options.NeedsDisplay` 只给它设），所以只有它会撞。

> 注：显示改成「每个 asa-server 进程一个自管 Xvfb、所有实例共用」之后，这条结论
> **不变**。~~（推断）~~ —— **2026-09-01 已复测，这句话现在是观测**：三轮、
> 两次对调先后顺序，全程 `DISPLAY=:0`，后起的那个每次都止步于 `umu.exe`、
> 每次都跑满 3 分钟被清。统一显示解决不了这个问题。
> 复测过程与全部采证见 `docs/SHARED_PREFIX_MULTI_ARKAPI_PLAN.md`
> （那份文档就是专门为了检验这条注而写的）。

**结论：`per-instance` 是「同时用 ArkApi 跑多实例」的唯一办法。**
这条以前被记在 §7 残余风险 2 里、标着"未知"，现在证实成立。

---

## 3. 参考脚本是怎么做的

```bash
# scripts/ark_instance_manager.sh:883
start_server() {
    export PROTON_VERB=run          # ← L884，函数第一行
    ...
    setsid nohup env \              # ← L992，启动命令行里看不到 PROTON_VERB，
        WINEPREFIX="$UMU_PREFIX_DIR" \  #    它是靠 export 继承进去的
        GAMEID="$UMU_GAMEID" \
        PROTONPATH="$UMU_PROTONPATH" \
        UMU_RUNTIME_UPDATE=0 \
        "$UMU_RUN_BIN" ... &
}
```

```bash
# scripts/ark_instance_manager.sh:1181 start_all_instances()
if start_server "$instance_name"; then
    echo "Waiting 30 seconds before starting the next instance..."
    sleep 30                        # ← L1197，错开首次启动
fi
```

**脚本确实是单 prefix、单 wineserver 跑多实例的**，靠的就是这两件事：

1. `PROTON_VERB=run` —— 取消排队。
2. 实例之间 30 秒错开 —— 避开并发首次触碰 prefix 的竞争。

脚本对 `PROTON_VERB=run` 没有写任何注释，这也是它容易被漏掉的原因之一。

---

## 4. 我们为什么丢了这一行

`LINUX_COMPATIBILITY_PLAN.md` 的 P2 验收记录里写着：

> ③`PROTON_VERB=run` 从设计阶段的示意代码中去掉——**参考脚本的实际调用从不设它**

`runner_linux.go:143-146` 的函数注释同样写着：

> matching `scripts/ark_instance_manager.sh`'s proven env var set exactly
> (notably: **no PROTON_VERB — the reference script doesn't set it**, and
> umu-run's default is already correct for running a game exe)

两处都错。核对时看的是 L992 那条 `setsid nohup env ...` 命令行——那里确实没有
`PROTON_VERB`，因为它在 130 行之前就 `export` 了。**逐字对拍了启动那一行，
漏了函数开头的 export。**

> 教训值得留在文档里：对拍 shell 脚本时，"这条命令行上有哪些变量"和
> "这个进程实际继承了哪些变量"是两回事。以后核对 env 应以**函数整体**
> 或直接读运行中进程的 `/proc/<pid>/environ` 为准。

---

## 5. 修法 F1：补上 `PROTON_VERB=run` — ✅ 已实施

> 定位更正：本节起初写作"主修法"。实测证明它**必要但不充分**——它拆掉的是第一道闸
> （排队），第二道闸（ArkApi 撞 Wine 会话，§2.2）要靠 F2 的 `per-instance`。
> 两者都要有：F1 让共享模式下的**纯 ARK** 多实例可用，F2 让 **ArkApi** 多实例可用。

### 5.1 落点

| # | 文件 | 改动 |
|---|---|---|
| 1 | `internal/runner/runner_linux.go` `umuCommandLine` | env 追加 `PROTON_VERB=run`；**同时订正 L143-146 的注释**（写明脚本在 `start_server()` 第一行 export，以及不设它会导致多实例排队） |
| 2 | `internal/runner/vcredist_linux.go` `runInPrefix` | 同样追加。它自建 env、不走 `umuCommandLine`，**有实例在跑时 `verify-arkapi` / `EnsurePrefixVCRedist` 会挂满 15 分钟超时** |
| 3 | `internal/runner/umu_linux.go` `warmPrefix` | **不加**。它是首次初始化，此时不该有别的实例在跑；万一有，`wineserver -w` 的等待反而是正确行为（不能对着活 prefix 跑 `wineboot --init`） |

追加位置放在 `WINEPREFIX`/`GAMEID` 之后、`cfg.WineDLLOverrides` 之前即可；
`launchEnvAllowed` 已放行 `PROTON_` 前缀，用户若显式设了环境变量，exec 取最后一次出现，
我们显式追加的这个会赢——这是有意的（想覆盖请走 `config.yaml`，不是环境变量）。

### 5.2 是否做成可配置

**不做。** 理由：`waitforexitandrun` 对本项目**没有任何正确用途**——我们从不"重启同一个 prefix 里的同一个游戏"，
实例之间的编排由状态机 CAS 与 §8 的启动闸门负责。
留一个开关只会制造"配错了就多实例全挂"的新坑。真要临时改，`PROTON_VERB` 环境变量本来就在白名单里。

### 5.3 验证

改完后 `ps` 里应看到 `…/proton **run** …`（而非 `waitforexitandrun`），
且第二个实例不再出现 `wineserver -w` 进程。

---

## 6. 已订正的两条判断

### 6.1 "进程组跨实例误伤" —— 证伪，且早已修复

本次快照证实了 `LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md` §2.2 的结论：
`srt-bwrap`(6985)、`pv-adverb`(7036)、`wineserver`(7088)、`AsaApiLoader.exe`(7155)
**各自 `pgid == sid == pid`**，即从 launcher 往下 setsid 了四次。
`umu-run` 的进程组 6981 里**只有它自己**。

所以 `kill(-6981)` 既不会误伤别的实例，也**根本杀不到自己的游戏**——
后者正是 2026-08-29 已修的问题（`pkg/procx/procx_linux.go:149` 现在走真正的
`/proc` ppid 进程树，进程组只作兜底）。本次数据是那次修复前提的再次确认，无需改动。

### 6.2 "两个 wineserver 抢同一份注册表" —— 排除

见 §1.1：是一个服务端 + 一个等待器，容器间 `/tmp` 共享。

---

## 7. 共享 wineserver 的残余风险（F1 之后仍在，风险 2 已证实并处理）

F1 让我们回到**与参考脚本完全一致**的状态。脚本在这个状态下经过验证，
但"经过验证"不等于"没有风险"，以下几条要如实记在案：

| # | 风险 | 说明 |
|---|---|---|
| 1 | 一个 wineserver 服务所有实例 | wineserver 崩溃 = 所有实例同时挂。单实例场景没有这个耦合 |
| 2 | **ArkApi 多实例不可行** —— ✅ **已证实**（原为"未知"，见 §2.2） | 共享会话下第二个 `AsaApiLoader.exe` 静默挂死。已在 `startServerInternal` 里做成**阻断**（`conflictingArkApiInstance`），当场报错并指出改 `per-instance`，不再让用户干等 3 分钟。纯 `ArkAscendedServer.exe` 多实例**不受影响**，共享模式下正常可用 |
| 3 | **并发启动的竞争** | 批量启动本来就是串行的（`manager.go:690`），但**单实例启动 API 会真并发**——`serverActionsLock` 已不存在。§8 已定案：`shared` 模式加启动闸门串行化，`per-instance` 保持并发 |
| 4 | `EnsureRuntime` / `verify-arkapi` 撞上运行中的实例 | F1 的第 2 项落点解决了"挂死"，但"在活着的 prefix 里改注册表"本身仍不干净 |

风险 1、2 只有 F2（每实例 prefix）能真正消除。

---

## 8. 并发启动的闸门（已定案）

**决策**：

| `prefix_mode` | 启动策略 |
|---|---|
| `shared`（默认） | **串行**。等上一个实例到达 `start_initialization_successful` 再放行下一个；**上一个失败也照样放行下一个**（不是"整批中止"） |
| `per-instance` | **并发**，与 Windows 行为一致，不加任何闸门 |

不抄脚本的 `sleep 30`：固定时长既可能不够（大地图初始化超过 30 s），
又可能白等（小地图 10 s 就好了）。**以状态为准，不以时长为准。**

### 8.1 现状核对：批量启动已经满足要求

| 要求 | 现状 | 结论 |
|---|---|---|
| 串行 | `batchmanage` 阶段二是严格串行 `for` 循环（`internal/batchmanage/manager.go:690`），逐个调 `instancepkg.StartServer` | ✅ 已满足 |
| 等到 `start_initialization_successful` | `startServerInternal` 阻塞在 `select { case <-initFailed; case <-initSuccessful }`（`internal/instance/server.go:635-641`），而 `initSuccessful` 正是写完 `StatusStartStartInitializationSuccessful` 之后发出的（`server.go:616-619`） | ✅ 已满足（`StartServer` 返回 == 初始化成功） |
| 失败继续下一个 | `executeInstance` 记 `InstanceFailed` 后循环进入下一轮，不中断整批（`manager.go:875`） | ✅ 已满足 |

**所以 `start-all` / 批量重启这条路不需要改。** 已有的 `DelayBetween`（可选的额外间隔）
保持不变，它是用户显式要求的额外缓冲，与本闸门不冲突。

### 8.2 缺口：单实例启动 API 会真并发

`CLAUDE.md` 写着"The API server uses a mutex (`serverActionsLock`) to prevent concurrent
start/stop operations"，但**代码里已经没有这个锁了**（全仓 grep 无命中）。
于是两次并发的 `POST /api/server/:name/start`（例如用户在 UI 上先点 A、不等它好就点 B）
会真的并发进入启动流程——**这正是本次故障的触发方式**。

`schedule`（定时任务）同理：两个定时任务撞在同一分钟也会并发拉起。

### 8.3 设计：共享 prefix 启动闸门

在 `internal/instance` 加一把**进程内启动闸门**，语义就是 §8 表格那两行：

```go
// runner 侧（instance 不该自己判断平台/模式）
// SharedLaunchGate 返回本次启动是否需要与其他实例串行。
// Windows 恒为 false（prefix 概念不存在，行为完全不变）。
// Linux 下仅当 prefix_mode == "shared" 时为 true。
func SharedLaunchGate() bool
```

`startServerInternal` 里：

- **加锁位置**：`runner.Run` 之前（`server.go:501`），与 §5 的 `PrepareSharedTree` 循环相邻。
- **解锁位置**：`select` 落定之后（`server.go:635-641`），**两个分支都解锁**
  ——`initFailed` 也必须放行，这是"失败也继续下一个"的落点。用 `defer` 覆盖早退路径。
- **等锁时要有反馈**：拿不到锁时先发一条 SSE/日志
  （`正在等待实例 X 初始化完成后再启动（prefix_mode=shared）`），
  否则用户看到的又是"点了启动没反应"——本次故障的体感就是这样来的。
- **等锁要可取消**：走 `ctx`，用户取消启动或超时能退出等待，不能死等。
- **上界**：闸门持有时间天然被 `waitForGamePID` 的 3 分钟 + 启动等待封顶，
  不需要单独设超时；但等锁方需要自己的超时（建议复用启动流程的整体超时预算）。

### 8.4 为什么不放在 `runner` 里

`runner.Run` 一返回就结束了（它只负责把进程拉起来），而闸门必须**持有到初始化成功**
——那是 `instance` 才知道的事实（`initSuccessful` 通道）。放 `runner` 里会退化成
"只串行化 exec 那一瞬间"，挡不住 Proton 的 prefix setup 阶段。

### 8.5 Windows 回归自由

`SharedLaunchGate()` 在 Windows 上恒 `false`，闸门代码整段短路，
`startServerInternal` 的时序与今天逐字相同。这条必须在 review 时逐句核对
（与 `LINUX_COMPATIBILITY_PLAN.md` 一贯的"Windows 行为优先"一致）。

---

## 9. F2（次）：把 `prefix_mode: per-instance` 接线 — ✅ 已实施

**与本次故障解耦，但仍是一个应当修的真 bug。以下 9.1 描述的是实施前的状态。**

### 9.1 它曾经是死代码（已修）

`prefixDir` 的按 key 分目录逻辑写好了（`internal/runner/umu_linux.go:39`）、
`Options.PrefixKey` 定义了（`runner.go:41`）、`umuCommandLine` 也用上了（`runner_linux.go:167`），
**唯独没有人传它**：

| 调用点 | 传的 key |
|---|---|
| `internal/instance/server.go:501` `runner.Run(...)` | `Options` 里**没有 `PrefixKey` 字段** |
| `internal/instance/server.go:420` `PrefixHasVCRedist("")` | `""` |
| `internal/runner/umu_linux.go:284` `warmPrefix` | `""` |
| `internal/runner/umu_linux.go:114` `ensureVCRedist(…, "", …)` | `""` |
| `internal/runner/runner_linux.go:136` `checkRuntime` | `""` |
| `internal/actions/verify_arkapi.go:56` | `""` |

而 `appconfig/validate.go:164` 校验它、`LINUX_DEPLOYMENT.md:239` 把它写成
"多实例互相影响时的处置手段"。**一个被文档推荐、被配置校验、改了却毫无效果的开关，
本身就是 bug**，与本次根因无关也要修。

### 9.2 设计要点

- **key = 实例名原样**，目录 `{BaseDir}/umu-prefix-<name>`。实例名已被
  `apiresp.ValidateInstanceName` 拒掉 `..` `/` `\` NUL，且本来就在当目录名用。不引入 hash 层。
- **按需创建**：新增 `runner.EnsurePrefix(ctx, prefixKey, progress)`（Windows no-op），
  在 `startServerInternal` 的 `runner.CheckRuntime()`（`server.go:451`）之后、
  `VerifyRuntimeAccessForLaunch` 之前调用。
  实现 = `warmPrefix` 参数化到 key；`runtimeMu` 从包级单锁改为**按 prefix 路径的锁表**
  （否则并发启动 N 个实例会被串成串行）。
- **成本**：贵的部分（GE-Proton ~450 MB、Steam Linux Runtime ~150–190 MB）已全局共享，
  新 prefix 只付一次 `wineboot --init` 加一次 VC++ 装入。
  **实测（2026-08-31）**：VC++ 装入段 `00:34:44 → 00:35:18` = **34 秒**；wineboot 段未单独计时，
  整体在一分钟量级，与设计预期一致。**占盘仍未实测**（`du -sh`），§11.3 挂着。
- **VC++**：`ensureVCRedist` 已按 key 设计。承重项是 **DLL override（一次 regedit，
  不需要显示、不需要下载）**，`vc_redist.exe` 安装是补充项——所以"每个 prefix 都要装"
  的实际成本远低于字面。`server.go:420` 的 `PrefixHasVCRedist("")` 要改传 key。
- **权限**：prefix 是独占目录，走 `chownPathForRuntime`（`warmPrefix` 里已有，
  参数化后自动对每个 prefix 生效），**不是** `PrepareSharedTree`。
- **生命周期**：删除实例时删对应 prefix；重命名**不 mv、直接删让其重建**
  （prefix 内含指向自身的绝对路径，`mv` 未必安全；prefix 里没有用户数据）。
  新增 `asa-server prefix status | gc` 处理孤儿。
- **默认值维持 `shared`**：F1 之后共享模式是可用的（与脚本一致），
  per-instance 定位为"要更强隔离、愿意付磁盘"的选项。

---

## 10. 分步实施清单

| 步骤 | 内容 | 落点 |
|---|---|---|
| **F1-1** ✅ | `umuCommandLine` 追加 `PROTON_VERB=run`，订正函数注释 | `internal/runner/runner_linux.go` |
| **F1-2** ✅ | `runInPrefix` 追加同一变量（否则有实例在跑时 `verify-arkapi` 挂满 15 分钟超时） | `internal/runner/vcredist_linux.go` |
| **F1-3** ✅ | 新增 `runner.SharesWinePrefix()`（Windows 恒 `false`，Linux 判 `PrefixMode != "per-instance"`）+ `internal/instance/launchgate.go` + `startServerInternal` 接线（§8.3） | `internal/runner/{runner,runner_windows,runner_linux}.go`、`internal/instance/{launchgate,server}.go` |
| **F1-4** ✅ | 订正 `LINUX_COMPATIBILITY_PLAN.md` §5.1 与 P2 的"③"、§6 风险 6 回链；订正 `CLAUDE.md`/`AGENTS.md`/`docs/README.md`/`docs/ARCHITECTURE.md` 里已不存在的 `serverActionsLock` 说法 | `docs/`、`CLAUDE.md`、`AGENTS.md` |
| **F1-5** ✅ | 真机验证 —— 见 §11 验收记录 | — |
| **F3-1** ✅ | 【实测追加】ArkApi 冲突阻断 `conflictingArkApiInstance`：共享模式下已有另一个 ArkApi 实例在跑时当场报错并给出改法，替掉原来的 3 分钟静默超时（§2.2） | `internal/instance/{launchgate,server}.go` |
| **F3-2** ✅ | 【实测追加】`writePrefixMarker` 写完 chown 给运行时用户，修 per-instance 首次启动被 `umu-runtime-owner-drift` 拦下（§11.2） | `internal/runner/umu_linux.go` |

**F1 已写的测试**（`go vet` 两平台通过，Windows 侧已实跑）：

| 测试 | 守住什么 |
|---|---|
| `runner.TestUmuCommandLine_PinsProtonVerbToRun`（linux） | 生效的 `PROTON_VERB` 必须是 `run`；故意先 `t.Setenv` 一个 `waitforexitandrun`，验证我们追加的那个排在后面（exec 取最后一次出现）——**这正是本次 bug 的回归测试** |
| `instance.TestLaunchGate_SharedSerializesLaunches`（linux） | 共享模式下 B 必须等 A 放行 |
| `instance.TestLaunchGate_ReleaseIsIdempotent`（linux） | 双重释放不得多放一个许可（显式放行 + defer 兜底必然调两次） |
| `instance.TestLaunchGate_PerInstanceDoesNotSerialize`（linux） | per-instance 下不排队 |
| `instance.TestLaunchGate_WaitIsCancellable`（linux） | 等锁可被 ctx 取消 |
| `instance.TestLaunchGate_NoOpOnWindows`（windows，**已实跑通过**） | Windows 行为零回归的可执行版本 |

**F2 步骤**：

| 步骤 | 内容 | 落点 |
|---|---|---|
| **F2-1** ✅ | `warmPrefix` 参数化到 key（`reconcilePrefixVersion`/chown/drain/marker 本来就是按传入 prefix 走的，跟着自动生效）；新增按 prefix 路径的锁表 `prefixLocks`，`runtimeMu` 保留给 `EnsureRuntime`（它还要下载全局的 umu/GE-Proton） | `internal/runner/umu_linux.go`、`prefix_linux.go` |
| **F2-2** ✅ | 新增 `runner.{PrefixKeyFor,EnsurePrefix,RemoveInstancePrefix,PrefixStatus}` + Windows 全 no-op；`ensureVCRedist` 已按 key 设计，直接传 | `internal/runner/prefix{,_linux,_windows}.go` |
| **F2-3** ✅ | `startServerInternal` 求出 `prefixKey` 并同源用于 `EnsurePrefix`/`PrefixHasVCRedist`/`Options.PrefixKey`；`CheckRuntime` 前移到 ArkApi 前置检查之前（Windows 上是 no-op，移动无影响） | `internal/instance/server.go` |
| **F2-4** ✅ | 删除/重命名实例时清理其 prefix（只告警不失败）；新增 `asa-server prefix status\|gc`（`gc` 默认预演，`--apply` 才删） | `internal/webapi/instanceapi/`、`internal/actions/prefix.go`、`main.go` |
| **F2-5** ✅ | `appconfig` 的字段注释与 `config.yaml` 模板、`LINUX_DEPLOYMENT.md` 排障表、`CLAUDE.md` | `internal/appconfig/`、`docs/`、`CLAUDE.md` |

**F2 已写的测试**：

| 测试 | 守住什么 |
|---|---|
| `actions.TestGCCandidates`（**已实跑**） | 逐条钉死"什么不能删"：共享 prefix、实例仍在的、wineserver 占用中的；孤儿与 `.bak-*` 才是候选 |
| `actions.TestHumanSize`（**已实跑**） | 占盘数字的可读格式 |
| `runner.TestPrefixKeyFor_FollowsMode`（linux） | 模式→key 的唯一转换点。它错了，start 路径三处会一起静默错位 |
| `runner.TestPrefixDir_KeyOnlyAppliesUnderPerInstance`（linux） | shared 下 key 必须被忽略；空 key 在任何模式下都是共享 prefix |
| `runner.TestInstancePrefixDir_IgnoresMode`（linux） | 切回 shared 后仍能定位到 per-instance 时期的残留目录（清理的前提） |
| `runner.TestRemoveInstancePrefix_NeverTouchesShared`（linux） | 空实例名不得误删共享 prefix |
| `runner.TestPrefixMarker_RoundTrips`（linux） | 标记的写方（`umu_linux.go`）与读方（`prefix_linux.go`）在两个文件里各自拼路径，拼错了不报错、只会让 `ensurePrefix` 快速路径永不命中，每次启动白重建一遍 prefix |

**遗留运维项**：

| 步骤 | 内容 | 落点 |
|---|---|---|
| **X-1** ⏳ | 清掉 `server-files/ShooterGame/Saved/` 下残留的 `pidprobe2`、`rulecheck` 两个目录（见 §12） | 运维操作 |

**可单测（无需 Linux 真机）**：`umuCommandLine` 产出的 env 含 `PROTON_VERB=run`
且位置正确（现有 `runner_linux_test.go` 就是干这个的）；`prefixDir` 的 key 组合；
锁表并发行为；`prefix gc` 的删除判定。

---

## 11. 真机验收记录（2026-08-30 ~ 08-31）

环境：Ubuntu，`/opt/asa-server/basedir`，GE-Proton10-34，umu 1.4.4，降权用户 `asa-umu-runtime`，
两个实例 `jibian-pve`(7001/7002) 与 `meijue-pve`(7003/7004)，二者均启用 ArkApi。

### 11.1 已验证通过

| # | 项 | 证据 |
|---|---|---|
| 1 | `PROTON_VERB=run` 生效 | `ps` 里两个实例的 argv 均为 `…/proton run …`；`wineserver -w` 进程彻底消失 |
| 2 | 启动闸门排队 | `00:19:20` "实例 meijue-pve 正在等待实例 jibian-pve 初始化完成后再启动"；`00:20:13` "已获得共享 Wine prefix 的启动许可" —— 等待 53 秒，与 A 到达 `start_initialization_successful` 的时刻吻合 |
| 3 | 闸门在失败时也放行 | B 于 `00:23:13` 超时失败后，后续启动未被阻塞 |
| 4 | **对照实验：共享模式 + 关闭 ArkApi + 两实例** | **两个实例先后启动、同时在线** —— 这是 §2.2 的决定性证据，也证明共享模式对纯 `ArkAscendedServer.exe` 完全可用 |
| 5 | per-instance 建 prefix | `umu-prefix-jibian-pve` 创建成功，wineboot 通过，VC++ 装入成功（`00:34:44 → 00:35:18`，**34 秒**，这一段是 vc_redist 安装；wineboot 部分未单独计时） |
| 6 | **per-instance 下两实例同时运行** | `pgrep -x wineserver` → **15953 / 16748 两个独立 wineserver**；两个 ArkApi 实例正常在线 |
| 7 | Windows 回归（单测） | `TestLaunchGate_NoOpOnWindows` 通过：闸门在 Windows 上不串行化任何东西 |

### 11.2 验证中发现并修复的缺陷

| # | 缺陷 | 修复 |
|---|---|---|
| 1 | 补上 `PROTON_VERB=run` 后第二个 ArkApi 实例仍然静默挂死 | 定位为 §2.2 的第二道闸；`conflictingArkApiInstance` 做成阻断，当场报错而非等 3 分钟超时 |
| 2 | per-instance 首次启动被 `umu-runtime-owner-drift` 拦下：`.created-by-proton` 归 root | `writePrefixMarker` 写完后 chown 给运行时用户（与同文件 `writeVCRedistMarker` 一致）。共享模式下从未暴露：prefix 在 `setup` 期间创建，实例启动前 asa-server 早已重启，`reconcileRuntimeOwnership` 顺手就修了 |

### 11.3 尚未验证

| # | 项 | 备注 |
|---|---|---|
| 1 | 有实例在跑时 `asa-server verify-arkapi --check-only` 不再挂死 | F1-2（`runInPrefix` 的 `PROTON_VERB=run`）的专项验证 |
| 2 | `start-all` 拉起 3 个以上实例 | 回归确认，批量串行本来就是既有行为 |
| 3 | 单个 per-instance prefix 的占盘（`du -sh`） | §9.2 的数字仍是估计，**未实测** |
| 4 | 删除 / 重命名实例时 prefix 被清理 | F2-4 |
| 5 | `asa-server prefix status \| gc` 的实际输出 | F2-4 |
| 6 | 改回 `shared` 后的行为与残留清理 | F2-5 |
| 7 | Windows 上"A 初始化中点 B"的人工回归 | 单测已覆盖闸门短路，人工路径未走 |

### 11.4 结论

**可用组合**：

| 场景 | `prefix_mode` | 状态 |
|---|---|---|
| 单实例（用不用 ArkApi 都一样） | `shared` | ✅ 可用，省盘 |
| 多实例 + 纯 `ArkAscendedServer.exe` | `shared` | ✅ 已实测可用（启动自动串行） |
| **多实例 + ArkApi** | **`per-instance`** | ✅ 已实测可用，**且是唯一办法** |
| 多实例 + ArkApi | `shared` | ❌ 不可行，已做成启动时阻断并给出改法 |

默认值维持 `shared`：单实例与纯 ARK 多实例都没问题，而需要 ArkApi 多开的用户会在
第一次尝试时就拿到一条指名道姓的错误信息，而不是三分钟的静默超时。

---

## 12. 顺带发现（与本问题无关）

`server-files/ShooterGame/Saved/` 下残留了 `pidprobe2` 和 `rulecheck` 两个**目录**
（看形态是之前排障留下的探针），镜像同步每次都把它们当文件去 copy 然后报：

```
Failed to reconcile entry ShooterGame/Saved/pidprobe2: ... copy_file_range: is a directory
Failed to reconcile entry ShooterGame/Saved/rulecheck: ... copy_file_range: is a directory
```

只是噪音，删掉即可。**但值得记一笔**：`mirror` 在源侧是目录、镜像侧被当文件处理时
只打 WARN 继续，行为本身没问题，不过错误信息（`copy_file_range: is a directory`）
指向的是底层 syscall 而不是"源是目录、期望文件"，排障时容易被带偏。可考虑改进措辞。
