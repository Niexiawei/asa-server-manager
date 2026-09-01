# 共享 prefix 下的多 ArkApi 实例：统一 DISPLAY 之后那道闸还在不在

> 状态：**已验证（2026-09-01）——假说不成立，走分支 B**。实验结果见 **§12**。
> 全文保留，包括被证伪的部分：错误的路径与正确的结论同样有参考价值。
>
> **一句话结果**：统一 DISPLAY **没有**拆掉那道闸——H1 的**结论**成立。但同一批日志
> **把 H1 自己给出的机制也否掉了**：后起那条链并没有"带着自己的显示去抢"，它顺利
> **加入了先来那个的 desktop**，x11drv 干净地初始化了两次，一路走到 **conhost 把控制台
> 窗口建出来**——然后 `umu.exe` 才停在 `futex_waitv` 上，从此不再 exec 目标 exe。
>
> 所以：**「闸真实存在」是观测，「卡点不在显示」也是观测，「它到底在等谁」仍是空白。**
> §5.2 那个实验开关 `linux.allow_shared_arkapi` 已按 §7.3 删除，§7.2 的
> `explorer /desktop=` 方案随之出局（见 §12.4）。
>
> 想直接看结果的：**§12**。想知道当初为什么要做这个实验的：§2 → §4。
>
> 关联：`docs/UMU_PREFIX_PER_INSTANCE_PLAN.md` §2.2（那道闸的定位与推断）、
> `docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md`（自管 Xvfb 落地）、
> `internal/instance/launchgate.go` 的 `conflictingArkApiInstance`（当前的阻断）。
>
> **最短路径**：§2 事实核对 → §4 候选假说表 → §5 实验。

---

## 0. 一句话

`shared` 模式下"第二个 ArkApi 实例静默挂死"这条结论，是在**每个实例各自带一个私有
Xvfb、显示号互不相同**的代码上测出来的；三小时后落地的自管 Xvfb 把这件事改成了
**全进程共用一个显示**，而这个新组合**从未被测过**。原文里那句"结论不变"是推理，
不是观测。要么用一次实验把它升级成观测，要么发现那道闸本来就已经不在了。

---

## 1. 提出的假说

> 之前的验证中，`shared` 模式会导致 Wine 去绑定多个 DISPLAY 显示输出，
> 导致后续 ArkApi 启动都被挂死。使用自托管 Xvfb 之后 DISPLAY 都是同一个，
> 是否可以判断之前是否已经有实例启动、是否已经绑定过 DISPLAY，
> 如果已经绑定过，新的 ArkApi 启动就不再去绑定 —— 这样是否就能在 `shared`
> 模式下启动多个 ArkApi 实例？

拆成两半分别判：

| # | 半截 | 判定 | 见 |
|---|---|---|---|
| A | "之前的验证里，每个实例带着自己的 DISPLAY 进同一个 Wine 会话" | ✅ **属实**，有代码考古为证 | §2 |
| B | "让第二个实例不再去绑定 DISPLAY" | ❌ **不是一个可实现的动作**（Wine 里没有这个可跳过的步骤） | §3 |

A 属实这件事比 B 不成立重要得多：**它意味着当初那个决定性实验的前提，今天已经不
存在了**。B 不成立只是说"不用写那段判断逻辑"——因为该统一的东西已经被自管 Xvfb
顺手统一掉了，剩下的唯一问题是**统一之后够不够**。

---

## 2. 事实核对：假说的前提在历史上确实成立

### 2.1 时间线

`git log` 把顺序钉得很死（同一天，相差三个多小时）：

| 时间 | 提交 | 事件 |
|---|---|---|
| 08-31 09:53 | `bf5d49c` `b33355f` | `PROTON_VERB=run` + 启动闸门 + **`conflictingArkApiInstance` 阻断** |
| 08-31 09:54 | `5f993a3` | `UMU_PREFIX_PER_INSTANCE_PLAN.md` 定稿，§2.2 写下"第二道闸是 ArkApi" |
| **08-31 14:13** | `f0051c0` | **自管 Xvfb 落地**，`xvfb-run` 分支删除 |
| 08-31 14:14 | `507f79b` `67cf4b4` | 退出路径收 Xvfb + Xvfb 方案文档 |

也就是说：**决定性实验在上午，显示模型换血在下午。**

### 2.2 上午那次实验里，两个实例拿到的是两个不同的显示

`git show b33355f:internal/runner/display_linux.go` 里的第 2 档是：

```go
return displayTarget{Wrapper: xvfbRunArgs(cfg, xvfbRun), How: "xvfb-run（虚拟显示）"}, ""
...
func xvfbRunArgs(cfg Config, xvfbRun string) []string {
	return []string{
		xvfbRun,
		"-a", // 自动挑一个没被占用的显示号，多实例并发启动时不会互相撞车
		"-e", filepath.Join(home, "xvfb.log"),
		"-f", filepath.Join(home, ".Xauthority-xvfb"),
	}
}
```

`-a` 的语义就是**每次调用挑一个当时没被占用的号**。实验环境是 Ubuntu 无头机
（`xvfb-run` 存在、`/tmp/.X11-unix` 可写、服务进程无 `DISPLAY`），所以两个实例
必然走第 2 档，各自 fork 出**一个私有 Xvfb、一个私有显示号**，然后**带着不同的
`DISPLAY` 进入同一个 wineserver**。

这正是假说描述的那个形状。

### 2.3 今天的形状：四条路，常规情况下都只会给出同一个显示

自管 Xvfb（2026-08-31）与显示解析改序（2026-09-01，
`docs/ALWAYS_MANAGED_XVFB_DISPLAY_PLAN.md`）之后：

| 档 | 显示来自 | 多实例是否同一个显示 |
|---|---|---|
| 1 `displayConfigured` | `linux.display` **点名**的 | ✅ 同一个（一个固定值） |
| 2 `displayManaged` | 自管 Xvfb，`xvfbMu` **进程内单例** + `{BaseDir}/xvfb.state` 跨进程认领 | ✅ 同一个 |
| 3 `displayEnv` | 环境变量 `DISPLAY` **捡来**的 | ✅ 同一个（一个固定值） |
| 4 `displayExisting` | `firstUsableX11Display()`，按号数值升序取第一个能握手的 | ✅ 同一个（同一台机器每次选中同一个） |

改序还顺带把这件事**从偶然变成了结构性的**：头一档现在几乎总是自管 Xvfb，而它是
进程内单例——「所有实例同一个显示」是它的定义，不再依赖「环境变量不会中途变」
「扫描结果稳定」这类恰好成立的性质。

### 2.4 ⚠️ 但「不可能拿到不同显示」是**错的** —— 本文初稿的一处更正

初稿在这里写过一句"'两个实例拿到不同显示'这件事，在当前代码里已经没有产生路径了"。
**那句话过头了**，而且它恰好是本文最不该说满的地方 —— 假说的实质就是显示是否相同。
实际上有两条路径能让两个实例落在不同的显示上，都与 prefix 模式无关：

| # | 路径 | 何时发生 |
|---|---|---|
| 1 | **Xvfb 中途死掉、被看门狗补起** | 补起来的是**新的显示号**（`-displayfd` 重新挑）。先起的实例还连在旧显示上（那个 X 服务已经没了），后起的实例拿到新号 |
| 2 | **候选链回退**（2026-09-01 引入） | 自管 Xvfb 这次拉不起来（缺字体、`/tmp` 满了），`acquireDisplay` 回退到宿主的 `:0`。于是先起的在 `:100`、后起的在 `:0` |

两条都是**罕见但真实**的，而且第 2 条正是改序为了避免启动失败而特意引入的。
它们不影响 §5 的实验（实验环境里一次也不会发生），但**直接影响 §6.1 的落地设计**——
见那一节的更正。

### 2.5 那条"结论不变"的注是推理

`UMU_PREFIX_PER_INSTANCE_PLAN.md` §2.2 末尾：

> 注：显示改成「每个 asa-server 进程一个自管 Xvfb、所有实例共用」之后，这条结论
> **不变** —— 卡点是 Wine 会话，不是显示本身。

同一节开头对机制本身的措辞是诚实的：

> **机制**（推断，与全部观测一致，但未直接观测 winex11.drv 内部）

**推断 + 推断 = 仍然是推断。** 那条注是在机制假设成立的前提下推出来的，而机制本身
从未被直接观测。这不是指责当时写错了——在没有新证据时那是最合理的默认；但显示模型
已经换过一次血，默认值该重新过一遍秤。

---

## 3. 澄清："绑定 DISPLAY"不是一个可跳过的步骤

假说的 B 半截建立在一个直觉模型上："Wine 会话里有一次显示绑定，谁先来谁做，做完
别人就别再做了。" 这个模型对不上 Wine 的实际结构：

- `winex11.drv`（win32u 的用户驱动）是**每个 Windows 进程各自加载**的，每个进程
  自己 `XOpenDisplay(getenv("DISPLAY"))`，各连各的 X。没有"会话级的那一次绑定"
  可供第二个进程跳过。
- 因此**"不绑定"在实现上只有一种含义：不给这个进程 `DISPLAY`**。而这条路已经实测
  过了，结果写在 `display_linux.go` 的文件头上：`AsaApiLoader.exe` 没有显示时
  **5 秒后退出码 3，一个字都不打**，连 `Win64/logs/` 目录都不建。
  所以"让第二个实例不绑定"= 让第二个实例必然启动失败。

真正在会话级只有一份、可能构成瓶颈的，是 **wineserver 里的 window station /
desktop 对象**与它背后的 `explorer.exe` 桌面进程。第二个进程不会"重新绑定"它，
它会**加入**它。加入一个由**别的 X 连接**创建的 desktop，与加入一个由**同一个 X
连接**创建的 desktop，是不是同一回事 —— 这才是本文要测的那一个问题，也是自管
Xvfb 可能顺手改变的那个变量。

> 换句话说：假说问的"能不能跳过绑定"没有对应的动作，
> 但它背后那个直觉——"两个不同的显示进同一个会话有问题"——指向的是一个**真实的、
> 已经被改掉的**变量。问题因此从"要不要加一段判断"变成"那道闸是不是已经自己没了"。

---

## 4. 原实验一次动了三个变量

§2.2 的决定性实验是"保持 `shared`，把两个实例的 `EnableAsaPlugin` 都关掉 → 两个
实例同时正常运行"。这个对照很有说服力，但它切换的**不止是 ArkApi 一件事**：

| 变量 | 开 ArkApi 的那次 | 关 ArkApi 的那次 |
|---|---|---|
| 1 启动的 exe | `AsaApiLoader.exe` | `ArkAscendedServer.exe` |
| 2 `Options.NeedsDisplay` | `true` → 整条命令带 `DISPLAY` | `false` → **完全没有 `DISPLAY`** |
| 3 进程链 | `xvfb-run`（shell）→ python3 → umu-run → … | 直接 python3 → umu-run → … |
| 4 X 认证文件 | 两个实例的 `xvfb-run -f` 指向**同一个** `.Xauthority-xvfb` | 不涉及 |

四条一起变，所以"关掉 ArkApi 就好了"能证明的是**"这四条里至少有一条是原因"**，
不足以单独钉死第 1 条或第 2 条。今天的代码已经把 3 和 4 从世界上删掉了
（`xvfb-run` 分支整段移除，自管 Xvfb 不带 `-auth`、不传 `XAUTHORITY`），
把 2 从"每实例一个私有显示"变成了"全体共用一个显示"。

### 4.1 仍然活着的候选假说

| # | 假说 | 若为真，§5 的实验会看到 | 统一 DISPLAY 后是否自愈 |
|---|---|---|---|
| **H1** | Wine 会话的 desktop / 显示子系统每会话只初始化一次，第二个 GUI 进程加入时挂住（§2.2 的原推断） | 第二个实例**仍然**卡在 `umu.exe` 之后、加载器之前，症状逐字相同 | ❌ 否 |
| **H2** | 卡的是"同一会话里两个不同的 X 连接/显示"，不是"第二个 GUI 进程" | 第二个实例**正常起来**，两个 ArkApi 同时在线 | ✅ 是（**假说的实质**） |
| **H3** | 卡的是 `xvfb-run` 那一层自身：多插一层 shell、两个实例并发对**同一个** `-f` auth 文件做 `xauth`（xauth 用 `<file>-l` 锁文件 + 重试） | 第二个实例正常起来（与 H2 同象，靠 §5.3 的采证区分） | ✅ 是（已随 `xvfb-run` 删除而消失） |
| **H4** | 与显示无关：两个加载器在同一 prefix 里的并发副作用（offsets cache、`Win64/logs/`、某个文件锁） | 第二个实例仍然挂，但**卡点位置或日志形状与原记录不同** | ❌ 否 |

H2 与 H3 在结果上同象、在处置上也同象（都是"闸可以拆"），所以实验不需要把它们分开
就能决策；区分它们只影响文档里写下的原因，采证清单（§5.3）顺手就能拿到。

---

## 5. Phase 0：决定性实验（其余全部依赖它）

### 5.1 环境

| 项 | 值 |
|---|---|
| 平台 | Linux 真机（AlmaLinux 或 Ubuntu 均可，两台都跑一遍更好） |
| `linux.prefix_mode` | `shared` |
| 实例 | 两个，**都** `EnableAsaPlugin: true`，端口不冲突 |
| 显示 | 走自管 Xvfb（第 2 档）。跑之前先 `asa-server verify-arkapi --check-only` 确认 `[3]` 报的是"自管 Xvfb" |
| 代码 | 当前 `master` + §5.2 的临时旁路 |

### 5.2 需要一个临时旁路

`conflictingArkApiInstance` 现在会当场拒掉第二个实例（这是 §2.2 之后加的阻断），
所以实验必须先把它让开。**做成一个显式的、默认关闭的实验开关，不要改代码后再改回来**
——后者会让"这次测的到底是哪份代码"变成需要猜的问题（`XVFB_CROSS_DISTRO_DISPLAY_PLAN.md`
§13 刚吃过一次同形状的亏）。

```yaml
linux:
  # 实验用：允许 shared 模式下同时跑多个 ArkApi 实例。
  # 默认 false。见 docs/SHARED_PREFIX_MULTI_ARKAPI_PLAN.md §5。
  allow_shared_arkapi: false
```

- 落点：`appconfig.LinuxConfig` + `runner.Config` + `runner.AllowSharedArkApi()`，
  `conflictingArkApiInstance` 开头多一条 `if runner.AllowSharedArkApi() { return "" }`。
- 打开时**必须打一条 WARN**（"已按实验开关放行 shared 模式下的第二个 ArkApi 实例，
  此组合尚未验证"），否则将来有人打开了它、撞上 H1、然后来报一个三分钟静默超时的 bug。
- 实验结论落定后：H2/H3 成立则这个开关升级为正式行为（§6），H1/H4 成立则**连同开关一起删掉**，
  只留文档里的结论。

### 5.3 步骤与采证

```bash
# 0) 基线：确认显示这一档，并记下显示号
asa-server verify-arkapi --check-only            # 期望 [3] = 自管 Xvfb
ls -l /tmp/.X11-unix/                            # 记下 X<n>

# 1) 启动实例 A，等它到 started
#    采证：A 的 DISPLAY、游戏进程、ArkApi 是否加载
tr '\0' '\n' < /proc/<A_game_pid>/environ | grep -E '^DISPLAY='
grep -c 'API was successfully loaded' <镜像A>/ShooterGame/Binaries/Win64/logs/ArkApi_*.log

# 2) 启动实例 B（开关已打开），无论成败都跑满 3 分钟
#    采证 ①：进程链——有没有 AsaApiLoader.exe，还是止步于 umu.exe
ps -eo pid,ppid,pgid,sid,comm,args --forest | grep -E 'umu|wine|Asa|Ark'
pgrep -x wineserver                              # 期望恒为 1 个（shared）

#    采证 ②：两个实例是不是真的同一个 DISPLAY（本实验的前提）
tr '\0' '\n' < /proc/<B_pid>/environ | grep -E '^DISPLAY='

#    采证 ③：X 服务端那边有几个客户端连着
ss -xp | grep -c "X<n>"                          # 或 lsof /tmp/.X11-unix/X<n>

#    采证 ④：卡住时它在等什么（不需要 gdb）
cat /proc/<卡住的 pid>/wchan; echo
cat /proc/<卡住的 pid>/stack 2>/dev/null         # 需要 root
cat /proc/<卡住的 pid>/status | grep -E 'State|Threads'

#    采证 ⑤：三份日志
tail -50 <basedir>/logs/launcher.log             # umu/proton 链
ls -lt <镜像B>/ShooterGame/Binaries/Win64/logs/  # ArkApi 自己的日志建没建出来
tail -50 <basedir>/xvfb.log
```

**卡住时的加料重跑**（只有第一次挂了才需要）：给 B 的启动加
`WINEDEBUG=+x11drv,+win,+explorer`（`launchEnvAllowed` 已放行 `WINEDEBUG`？
不放行就临时加），输出落 `launcher.log`。这是把 §2.2 的"未直接观测 winex11.drv 内部"
补上的唯一低成本手段。

### 5.4 判定表

| 观测 | 结论 | 下一步 |
|---|---|---|
| B 正常起来，两个 ArkApi 同时在线，且 A/B 的 `DISPLAY` 相同 | **H2/H3**：闸是"两个显示"造成的，已被自管 Xvfb 拆掉 | 走 §6 |
| B 仍卡在 `umu.exe` 之后、加载器之前，症状与 §2.2 逐字相同 | **H1**：闸在 Wine 会话本身 | 走 §7 |
| B 卡住，但卡点/日志形状与原记录**不同** | **H4** 或新问题 | 先按 §7.1 采证，再定性 |
| B 起来了但不稳定（见 §5.5） | 部分成立，**不足以拆闸** | 走 §7，并把不稳定的形状记进文档 |

### 5.5 就算 B 起来了也必须补的三条

"两个都在线"不等于"这个组合可用"。以下三条任一不过，都按 §5.4 最后一行处理：

| # | 用例 | 为什么必须测 |
|---|---|---|
| 1 | **停掉 A（先启动的那个），B 是否活下来** | desktop / explorer 由 A 那个进程首次触发创建。创建者退出会不会把 desktop 带走，是 H1 与 H2 的分水岭上最容易翻车的一处。若 B 跟着死，这个组合等于"不能单独重启任一实例"，不可用 |
| 2 | **B 的游戏进程能否被正确识别与杀掉** | `gameproc_linux.go` 靠 `comm == "GameThread"` + cmdline 挑游戏进程。同一会话下现在有**两套** `AsaApiLoader.exe`/`GameThread`，候选集翻倍。挑错的代价不是少个 PID —— 是 `KillTree` 杀不到（见 `CLAUDE.md` 的 `gameproc*.go` 一段） |
| 3 | **B 的 ArkApi 日志转抄没有串台** | `arkapilog.go` 按 `<镜像>/…/Win64/logs/ArkApi_*.log` 取最新一份。两个实例各有各的镜像目录，理应不串，但这是第一次有两份同时在写 |

### 5.6 成本

一台真机、两个实例、一次启动加一次停止。**估计 30 分钟以内**，其中大部分是等两次
3 分钟超时（最坏情况）。相对于它决定的东西（是否要为多 ArkApi 强制 `per-instance`、
是否要多付一个 prefix 的磁盘与一分钟建 prefix 时间），这个价格可以忽略。

---

## 6. 分支 A：实验证明可行（H2/H3）

### 6.1 阻断怎么改

**不需要新增"判断是否已绑定显示"的逻辑**（§3 说明了那不是一个可实现的动作），
但**需要一个判断"这一次会不会拿到与在跑的那台不同的显示"的逻辑**。要做的是把
`conflictingArkApiInstance` 从**无条件阻断**改成**有前提的放行**。

> **这一节按 §2.4 更正过。** 初稿设想的是一个静态谓词
> `SameDisplayForAllInstances() bool`，理由是"当前实现下恒真"。**那是错的**：
> Xvfb 被看门狗换号重起、以及候选链回退到宿主显示，都会让两个实例落在不同的显示上。
> 一个恒真的静态谓词会在那两种情况下**放行一次注定挂死三分钟的启动**——
> 而它恰恰只在系统已经出过一次岔子（Xvfb 死了）之后才发生，是最不该雪上加霜的时刻。

判据必须是**动态的、按次比对的**：

```go
// runner.CurrentDisplay 报告本进程当前正在用的那个 X 显示（自管 Xvfb 已经起来时
// 是它的 ":100"；用宿主/点名显示时是那个值；一次都还没解析过时为空）。只读，
// 绝不拉起 Xvfb —— 与 DisplayStatus 同一条纪律。
func CurrentDisplay() string
```

放行的三个前提，缺一不可：

| # | 前提 | 为什么 |
|---|---|---|
| 1 | §5 的实验证明「同一显示 + 同一 Wine 会话」下第二个 ArkApi 能起来 | 整件事的根据 |
| 2 | **本次将拿到的显示 == 已在跑的那台正在用的显示** | §2.4 的两条路径。取不到、或不相等，就退回阻断并说明原因（"实例 X 正在用 :100，本次只能拿到 :0"——这句话本身就是一条好的排障线索） |
| 3 | `launchGate` 仍然生效 | 第二个实例要在第一个到达 `start_initialization_successful` **之后**才进场：desktop 由先来的那个建立，并发进场等于把实验条件之外的竞争请回来。`shared` 下闸门本来就开着，只需在注释里写明它现在**多担了一份责任**，不能因为"看起来只是防 Proton 并发"而被将来某次优化摘掉 |

前提 2 有一个附带好处：它对「将来出现每实例一个显示的路径」（例如按 prefix 分 Xvfb，
XVFB 方案 §9 风险 5 的退路）**自动免疫** —— 静态谓词在那种改动下会静默失真，
按次比对不会。

### 6.2 连带更新

| 文件 | 改什么 |
|---|---|
| `internal/instance/launchgate.go` | `conflictingArkApiInstance` 的整段注释是按 H1 写的（"每个会话只初始化一次"），要按实测结论重写，别留一段与代码行为相反的解释 |
| `docs/UMU_PREFIX_PER_INSTANCE_PLAN.md` | §0 第 5 条、§2.2、§7 风险 2、§11.4 的可用组合表——**四处**，都写着"per-instance 是唯一办法"。改的时候保留原文并注明"在每实例私有显示的代码上成立"，与该文档一贯的"错误路径也留着"风格一致 |
| `CLAUDE.md` | `runner` 的 prefix 模型那一段（"同时只能有一个 ArkApi 实例"） |
| `docs/LINUX_DEPLOYMENT.md` | 排障表里"多 ArkApi 请改 per-instance"那条 |
| `internal/appconfig` | `allow_shared_arkapi` 实验开关**删掉**（行为已成默认），或保留为反向逃生舱 `force_arkapi_exclusive`（倾向于删：一个没人会去开的开关只是维护成本） |
| 测试 | 现有 `TestLaunchGate_*` 之外，新增「本次显示与在跑那台不一致时仍然阻断」一组（§6.1 前提 2），以及「取不到当前显示时保守阻断」一组 |

### 6.3 仍然不变的东西

- **默认 `prefix_mode` 维持 `shared`**，本分支只是让它多支持一种组合。
- **`per-instance` 仍然值得推荐**给要强隔离的用户：`UMU_PREFIX_PER_INSTANCE_PLAN.md`
  §7 风险 1（一个 wineserver 崩 = 所有实例一起挂）与本文无关，它不会因为显示统一而消失。
- **不碰 `NeedsDisplay` 的判据**：`ArkAscendedServer.exe` 依旧不要显示。

---

## 7. 分支 B：实验证明仍然挂死（H1/H4）

### 7.1 先把推断升级成观测

带 `WINEDEBUG=+x11drv,+win,+explorer` 重跑一次，目标只有一个：**拿到第二个进程
停在哪一步的第一手输出**，把 §2.2 那句"（推断，未直接观测 winex11.drv 内部）"
换成一行日志。有了它，这条结论才算封盘，不会每隔几个月被一个合理的新直觉重新挖开
——本文本身就是那样一次挖掘。

### 7.2 候选缓解：每实例一个 Wine 虚拟桌面（仅在 H1 成立时评估，**倾向于不做**）

Wine 支持 `explorer /desktop=<名字>,<宽>x<高> <exe> <args...>`，为被启动的程序
开一个独立的 desktop 对象。如果 H1 的机制真是"desktop 每会话一个"，给每个实例
一个独立 desktop 名在理论上能绕开。

但它要付的代价很实在，写在前面免得看着像免费午餐：

| # | 代价 | 说明 |
|---|---|---|
| 1 | **改 argv = 改游戏进程的 cmdline** | `gameproc_linux.go` 在 Linux 上**只能看 cmdline**（`/proc/<pid>/exe` 是 wine64-preloader），而启用 ArkApi 时两个候选的 cmdline 本来就逐字相同、只能靠 `comm == GameThread` 区分。前面插一层 `explorer /desktop=…` 会再加一个 `explorer.exe` 候选。这是本项目**踩过且写进 `CLAUDE.md` 的坑**，不能顺手改 |
| 2 | 多一层 Windows 进程 | 加载器变成 explorer 的子进程，`waitForGamePID` / `KillTree` / `launcher_pid` 三处的父子假设都要重新核 |
| 3 | 收益未经验证 | "desktop 每会话一个"本身就是待证明的那条推断；在它之上再叠一个推断去做实现，是把赌注加倍 |

**结论**：只有在 §7.1 的日志**明确指向 desktop 对象**时才值得做一次一次性的手工验证
（直接在命令行上拼一条 `explorer /desktop=` 试跑，不改代码）。手工验证通过之前不动代码。

### 7.3 把结论钉死 —— ✅ 已执行，逐条落点见 §12.3

> 实际执行时比下面的第 2 条多做了一件事：**新的事实不止「依旧挂死」，还包括
> 「原来那个机制解释是错的」**（§12.2）。第 2、3 条因此都比原计划多写了一段。

H1 成立时要做的事其实很少，但每一件都是防止第三次绕回来的：

1. §5.2 的实验开关**删掉**，不留。
2. `conflictingArkApiInstance` 的注释补一句**新的**事实：
   "2026-09-xx 在自管 Xvfb（全实例同一 DISPLAY）下复测，第二个加载器**依旧**挂死，
   所以卡点确实是 Wine 会话而不是显示——这条以前是推断，现在是观测。"
3. `UMU_PREFIX_PER_INSTANCE_PLAN.md` §2.2 那条注从"推断"改注为"已复测"，并链回本文。
4. 本文状态改为"已验证：假说不成立"，保留全文——**错误的路径与正确的结论同样有参考价值**
   （该文档开头自己写的规矩）。

---

## 8. 非目标

- **不改默认 `prefix_mode`**。无论分支 A 还是 B，默认仍是 `shared`。
- **不引入任何 X 客户端库、不传 `XAUTHORITY`**。现有 12 字节握手探测够用，
  改动它会同时波及 `planDisplay` / `runtimeEnv` / `launchEnvAllowed` 三处
  （`XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` §9 风险 3）。
- **不做"每 prefix 一个 Xvfb"**。那是 XVFB 方案 §9 风险 5 的退路，方向与本文相反：
  它会让不同实例拿到不同显示，正好把 §6.1 的前提 2 变成恒假。真走那条路的话，
  §6.1 的按次比对会**自动**把这些启动挡回去（这正是选按次比对而非静态谓词的收益），
  但那也意味着 `shared + ArkApi 多实例` 重新变成不可用 —— 两件事要一起决定。
- **不为 `ArkAscendedServer.exe` 加显示**。它不需要，`shared` 下的纯 ARK 多实例
  已经实测可用。

---

## 9. 改动清单

**Phase 0（无条件做，实验前提）—— ✅ 已实施（2026-09-01 上午），并已按 §7.3 全部撤销（同日下午）**

> 开关只活了几个小时，正如 §5.2 要求的那样：结论落定就不留悬置。下表记录的是它
> 当时改过哪些地方，删的时候按同一张表反着走了一遍（`grep AllowSharedArkApi` 归零，
> 两平台 `go build` + `go vet` 通过）。

| 文件 | 改动 |
|---|---|
| `internal/appconfig/{config,template}.go` | 新增 `linux.allow_shared_arkapi`（默认 `false`）。`validate.go` 不需要改：布尔项没有非法值 |
| `internal/runner/runner.go` + `runner_{linux,windows}.go` | `Config.AllowSharedArkApi` + `AllowSharedArkApi()`（Windows 恒 `false`） |
| `main.go` / `internal/actions/setup.go` / `internal/gui/gui.go` | 三处 `runner.Configure` 调用点同步补字段——**`Configure` 是整体覆盖**，漏一处就静默失效（XVFB 方案 §11.4 的原委） |
| `internal/instance/launchgate.go` | `conflictingArkApiInstance` 的实验旁路 + WARN |

实施时的一处偏离：旁路**没有**放在 `conflictingArkApiInstance` 开头，而是放在「已经找到
一个冲突实例」之后。放开头的话开关一打开就恒定短路，那条 WARN 要么每次启动都刷、要么
干脆写不出「和谁冲突」；放在里面则只在**真的绕过了一次阻断**时打，日志里带着两个实例名，
正好是排障时要看的那一行。对外行为完全相同。

**用户文档（`docs/LINUX_DEPLOYMENT.md`）刻意没动**：那张排障表现在仍然只写
「多 ArkApi 请改 `per-instance`」。这是个实验开关不是功能开关，写进用户文档等于
邀请用户去踩 §10 风险 1；等实验结论落定（转正或删除）再一次性回填。

**Phase 1（分支 A，仅当实验通过）** —— 见 §6.1 / §6.2。
**Phase 1'（分支 B，仅当实验失败）** —— 见 §7.3，主要是删开关 + 三份文档回填。

---

## 10. 风险

| # | 风险 | 缓解 |
|---|---|---|
| 1 | 实验开关被用户在生产上打开、撞上 H1，回到三分钟静默超时 | 默认 `false`；打开时 WARN 写明"未验证"；结论落定后**必须删掉或转正**，不留悬置的开关 |
| 2 | 实验"通过"但只是侥幸（时序恰好、A 先建好了 desktop） | §5.5 的三条补测，尤其第 1 条（停掉 A 之后 B 是否活着） |
| 3 | 分支 A 落地后，运行期出现「两个实例落在不同显示」（Xvfb 被看门狗换号重起、候选链回退），而闸已经拆了 | **这是 §2.4 更正掉的那个风险，也是把判据从静态谓词改成按次比对的原因**：`CurrentDisplay()` 与本次将拿到的显示不相等就退回阻断，并把两个显示号写进错误信息。配一组测试钉住「不相等时仍然阻断」「取不到时保守阻断」 |
| 4 | 两个 ArkApi 实例共用一个 wineserver 的其他耦合（崩溃连坐、注册表并发） | 与本文无关、也不会被本文消除。`UMU_PREFIX_PER_INSTANCE_PLAN.md` §7 风险 1 保持有效，`per-instance` 仍是强隔离的推荐 |
| 5 | 实验占用真机、期间实例不可用 | 单次 30 分钟以内（§5.6），可在维护窗口做 |

---

## 11. 一句话总结

假说问的"能不能让第二个实例不再绑定 DISPLAY"没有对应的动作——Wine 里没有这一步；
但它背后的直觉指向了一个**真实的、已经被改掉的**变量：当初判死这条路的那次实验，
两个实例带的是两个不同的显示，而今天它们必然带同一个。
**该统一的东西已经统一了，剩下的唯一问题是够不够——而这只能测，不能推。**

> **测完了：不够。** 但这一趟没白走 —— 它顺手拆掉的不是那道闸，而是那道闸底下
> 一个从没被验证过、却已经被抄进四份文档的解释。见 §12。

---

## 12. 实测记录（2026-09-01，AlmaLinux/WSL2 + Ubuntu 24.04.4）

### 12.1 判定：H1 成立，假说不成立，走 §7

按 §5.4 的表：**「B 仍卡在 `umu.exe` 之后、加载器之前，症状与 §2.2 逐字相同」**。

采证脚本 `scripts/diag/shared-arkapi-probe.sh`（本次为它而写，已入库）跑了三轮：

| 轮次 | 先起 A | 后起 B | 两者 `DISPLAY` | B 的结局 |
|---|---|---|---|---|
| 17:22:25 | jibian-pve | meijue-pve | 均 `:0` | 止步 `umu.exe`，无 `AsaApiLoader.exe` |
| 17:45:00 | meijue-pve | jibian-pve | 均 `:0` | 同上 |
| 17:55:53 | meijue-pve | jibian-pve | 均 `:0` | 同上，跑满 3 分钟超时后被清 |

**两次对调了先后顺序**，症状不变 —— 排除了「某个实例自己有问题」这类解释
（§4.1 的 H4 一支）。18:05 的第二次采证里 B 的整条链已经消失，只剩 A 一个
`ok`，这是「挂」而非「慢」的直接证据（§5.4 要求的那一刀）。

现场其余关键项：`wineserver` 恒为 1 个；`explorer.exe /desktop` 全场只有一个，
挂在 A 的 `pv-adverb` 下；B 的 `umu.exe` 单线程、`State: S`、
`wchan = futex_wait_multiple`，内核栈 `__do_sys_futex_waitv`。

§5.5 的三条补测**不需要做**：它们的前提是「B 起得来」，而 B 一次也没起来。

### 12.2 ⚠️ 但 H1 给出的**机制**同时被否掉了

这是本次实验最有价值、也最出乎意料的一条：它推翻的不是假说，而是**原结论的理由**。

§2.2 的解释是「Wine 的显示子系统每会话只初始化一次，第二个加载器加入会话后在
**创建窗口**这一步**静默挂住 —— 不报错、不退出、什么都不打**」。带
`WINEDEBUG=+x11drv,+win,+explorer` 跑完，A/B 两份 `launcher.log` 对照下来，
这句话**每一个分句都是错的**。

**A（起成功的那条）**——它是建 desktop 的那个：

```
0024:trace:win:get_desktop_window started explorer          ← A 拉起 explorer
00d4:trace:explorer:manage_desktop display guid {fc0ec2f1-…}
00d4:trace:x11drv:init_pixmap_formats / init_visuals / xinerama_init / XRandR 1.4 / Xfixes / XComposite
00d4:trace:explorer:load_graphics_driver display {…} driver L"winex11.drv"
00d4:trace:win:NtUserCreateWindowEx created window 0x10020   ← 1280x1024 桌面窗口
00d4:trace:x11drv:X11DRV_CreateWindow winstation name L"WinSta0".
00d4:trace:win:NtUserCreateWindowEx created window 0x10028   ← explorer 的 Message 窗口
```

**B（卡住的那条）**——注意它**没有** `started explorer`：

```
02ac:trace:x11drv:init_pixmap_formats … XRandR 1.4 … XComposite is up and running   ← x11drv 完整初始化，零错误
02ac:trace:win:WIN_CreateWindowEx (null) L"OleMainThreadWndClass" …
02ac:trace:win:invalidate_dce 0x100f0 parent 0x10028          ← ★ 父窗口是 A 的 Message 窗口
02bc:trace:x11drv:init_pixmap_formats … XComposite is up and running                ← 第二次，同样干净
02bc:trace:win:GetProcessDefaultLayout found description L"Wine conhost"             ← 是 conhost
02bc:trace:win:WIN_CreateWindowEx (null) L"WineConsoleClass" …
02bc:trace:x11drv:X11DRV_create_win_data win 0x200fa/2800001 …                       ← ★ 控制台窗口建出来了
02bc:trace:win:invalidate_dce 0x200fa parent 0x10020          ← ★ 父窗口是 A 的桌面窗口
02bc:trace:x11drv:X11DRV_WindowPosChanged win 0x200fa/2800001 … flags 0000003c
…此后只剩 window_surface_flush / set_caret_pos 的重绘循环，直到超时被清…
```

逐条对照 §2.2 那句话：

| §2.2 的说法 | 实际 |
|---|---|
| 第二个加载器「带着自己的 DISPLAY」进会话 | ❌ 它**加入**了 A 的 desktop —— 两个窗口的父级分别是 A 的 `0x10028` 和 `0x10020` |
| 显示子系统每会话只初始化一次 | ❌ B 的 x11drv **完整初始化了两次**（`02ac`/`02bc`），零错误 |
| 卡在**创建窗口**这一步 | ❌ 窗口**建成功了**（`X11DRV_create_win_data` + `WindowPosChanged` 都过了） |
| **静默**挂住，什么都不打 | ❌ 41 KB 日志且还在涨，只是从来没人接过它的 stderr 看 |

**真正的形状**：B 一路走到 **Wine conhost 为 `umu.exe` 建出控制台窗口之后**，
`umu.exe` 停在 `futex_waitv` 上不动了，**从此不再 exec `AsaApiLoader.exe`**；
conhost 那个窗口还在一秒一次地重绘、光标还在闪，直到 3 分钟超时被清掉。

| 命题 | 状态 |
|---|---|
| 共享 Wine 会话下第二个 ArkApi 实例起不来 | ✅ **观测**（三轮，两次对调，统一显示） |
| 统一 DISPLAY 能解决它 | ❌ **已证伪**（本文假说） |
| 原因是"显示子系统每会话只初始化一次 / 卡在创建窗口" | ❌ **已证伪**（见上表四条） |
| 卡点位置 | ✅ **观测**：conhost 控制台窗口建好之后、exec 目标 exe 之前 |
| `umu.exe` 在等哪个同步对象 | ⬜ **未知** |

**最后一行是有意留空的。** 这份文档存在的原因，就是上一次有人在这一行填了个
听起来合理的推断，然后它被抄进四个地方当成事实用了三个月。要填就拿观测填。

### 12.2.1 一个尚未解释的观测（不要顺手编答案）

B 的 conhost **在写日志、在画窗口**，但 `ps` 里 **B 的进程树中没有 conhost 进程**
（A 的树里有 `conhost.exe`）。可能是采证时序（脚本按 ppid 走树），也可能有别的原因。
下次挂住时两条命令就能定：

```bash
ps -ef | grep -c '[c]onhost'          # 全机几个 conhost，对得上几条链？
ls -l /proc/<卡住的 umu.exe pid>/task/ # umu.exe 到底几个线程（本次 status 报 Threads: 1）
```

### 12.3 §7.3 收尾（已完成）

| # | 事项 | 落点 |
|---|---|---|
| 1 | 删掉 §5.2 的实验开关，不留 | `appconfig/{config,template}.go`、`runner/{runner,runner_linux,runner_windows}.go`、三处 `runner.Configure`、`launchgate.go` 的旁路 —— `grep AllowSharedArkApi` 已归零 |
| 2 | `conflictingArkApiInstance` 注释按实测重写 | 写明「复测过两轮、结论成立」**以及「机制未知，旧解释是错的」**；比 §7.3 原计划多了后半句，因为 12.2 |
| 3 | `UMU_PREFIX_PER_INSTANCE_PLAN.md` §2.2 回填 | 那段机制**划掉但保留原文**并加更正框；那条「结论不变（推断）」的注改注为已复测 |
| 4 | 本文状态改为「已验证：假说不成立」，保留全文 | 文首 |
| 5 | 连带 | `CLAUDE.md` 的 prefix 模型段、`docs/LINUX_DEPLOYMENT.md` 排障表、`runner.SharesWinePrefix()` 的后果 2 —— 这三处都在复述那个已被证伪的机制 |

### 12.4 §7.2 出局，下一步该查什么

**`explorer /desktop=` 彻底不用做了。** §7.2 本来就"倾向于不做"，理由是收益建立在
一条未验证的推断上。12.2 把那条推断判死了，而且判得比预期更彻底：**B 从来没有
desktop 问题** —— 它顺利加入了 A 的 desktop，窗口都建出来了。给每个实例一个独立
desktop，治的是一个**已被证明不存在**的病，代价（改 argv = 改游戏进程 cmdline，
撞 `gameproc_linux.go` 那个写进 `CLAUDE.md` 的坑）却一分没少。

真要往下查，方向已经从"显示"换到了**"`umu.exe` 在 exec 目标 exe 之前等谁"**：

| # | 该做的对照 | 为什么 |
|---|---|---|
| 1 | 拿 **A 的 `launcher.log` 里 conhost 那一段**（`Wine conhost` / `WineConsoleClass` 前后）与 B 逐行对拍 | 两条链走到同一个位置，A 过去了 B 没过。差异就在这几十行里，这是成本最低、信息量最大的一步 |
| 2 | 给 B 加 `WINEDEBUG=+seh,+sync,+server`（去掉 `+win`，它把日志淹了） | `futex_waitv` 只说明在等 Wine 同步对象；`+sync,+server` 才能说出**等的是哪一个句柄** |
| 3 | 12.2.1 那两条 `ps`/`/proc/*/task` | 先把"conhost 在哪"这个观测缺口补上，免得后面基于半张进程图推理 |
| 4 | 换掉 `umu.exe` 这一环（例如 `PROTON_VERB` 之外直接跑 `AsaApiLoader.exe`）看还挂不挂 | 判断卡的是 umu 的 shim 逻辑还是 Wine 本身。**只手工试，不改代码** |

在这些做完之前，`per-instance` 就是答案 —— 它不是绕路，是这条路唯一走得通的形态。

### 12.5 顺手记下的两件小事（与本实验无关）

- `xvfb.log` 里有 `_XSERVTransmkdir: Mode of /tmp/.X11-unix should be set to 1777`
  （实际 755）。X 只是警告，不影响自管 Xvfb 启动。
- `/tmp/.X11-unix/probe` 是显示握手探测留下的 root 空文件，没有清理。
