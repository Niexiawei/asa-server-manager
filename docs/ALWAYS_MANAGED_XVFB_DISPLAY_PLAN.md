# 显示解析改为「自管 Xvfb 优先」：不再蹭宿主的 X（含 WSLg）

> 状态：**阶段 1 与阶段 2 已实施**（2026-09-01）。阶段 2b（W2′ overmount）**不做** ——
> W1 的 remount 已在 WSL2 真机上实测成功（§4.5），W2′/W3 存在的唯一理由（remount 被
> 内核拒绝）不成立。剩下的真机验证见 §7.2，其中 WSL 侧只剩「pressure-vessel 能不能把
> 自管 Xvfb 的 socket 带进容器」这一条。
>
> 落地文件：
>
> - `internal/runner/display_linux.go`：`displayKind` 拆出 `displayConfigured`；
>   `planDisplay` 返回**候选链** `[]displayPlan`；`acquireDisplay` 沿链回退并 WARN；
>   `x11SocketDirWritable` → `x11SocketDirState`（新增 `Fixable` 中间态）；
>   `stopManagedDisplay` 顺带还原挂载
> - `internal/runner/xvfb_linux.go`：`remountX11SocketDirRW` / `restoreX11SocketDirRO`，
>   由 `ensureX11SocketDir` 在 acquire 侧调用
> - `internal/runner/runner.go`：`Config.AllowX11Remount`、`DisplayInfo.Fallbacks`
> - `internal/appconfig`、`main.go`、`internal/actions/setup.go`、`internal/gui/gui.go`：
>   `linux.allow_x11_remount`（默认 true）+ **三个** `Configure` 调用点
> - `internal/runner/preflight_linux.go`、`internal/actions/verify_arkapi.go`：文案与回退可见性
> - 测试：`display_linux_test.go` 新增/改写 8 组，`xvfb_linux_test.go` 新增 3 组
>
> `go build ./...`、`go vet ./...`、`CGO_ENABLED=0 GOOS=linux go vet ./...`、
> `go test ./internal/runner/... ./internal/appconfig/... ./internal/actions/...` 均通过。
> 带 `//go:build linux` 的单测在 Windows 开发机上只做到编译期检查。
>
> 落地时改了本文一处设计：§4.3 推荐的解法 ① 落成了 `x11DirState{Writable, Fixable, Why}`
> 三态，而不是给 `x11SocketDirWritable` 加参数——判断侧仍然只读，动作仍然只在 acquire 侧。
>
> 关联：`docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md`（自管 Xvfb 的落地，本文改的是它的
> **顺序**与 WSL 那一格）、`docs/ARKAPI_LINUX_VCREDIST_PLAN.md` §9（显示为什么是硬依赖）、
> `docs/SHARED_PREFIX_MULTI_ARKAPI_PLAN.md`（本文会让那份文档的前提更结实，见 §5.3）。
>
> **最短路径**：§1 两个问题 → §3.1 新顺序表 → §4.2 WSL 三个候选的取舍。

---

## 0. 一句话

自管 Xvfb 明明是**唯一由我们完全掌控**的显示，却排在「宿主环境变量 `DISPLAY`」之后，
在 WSL 上更是因为 `/tmp/.X11-unix` 只读而**根本轮不到它**，必然落到 WSLg 的 `:0`。
本文把顺序改成「除非操作员点名，否则一律用自管 Xvfb」，并给 WSL 那格补上一条
**按能力触发**（不是按「是不是 WSL」）的可写化处置。

---

## 1. 现状与两个具体问题

### 1.1 现在的顺序，与它自己写下的理由不一致

`planDisplay`（`internal/runner/display_linux.go`）当前是三档：

| # | 档 | 触发条件 |
|---|---|---|
| 1 | `displayEnv` | `linux.display` **或**环境变量 `DISPLAY`，且无认证握手能过 |
| 2 | `displayManaged` | 有 `Xvfb` **且** `/tmp/.X11-unix` 可写 |
| 3 | `displayExisting` | 扫 `/tmp/.X11-unix/X<n>`，取第一个握手能过的 |

`XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` §3.2 给第 2 档排在第 3 档之上的理由是：

> 自管的那个显示不依赖任何桌面会话，用户注销、桌面重启都不会把游戏带走。

**这个理由对第 1 档同样成立，第 1 档却排在它上面。** 而且两者的差别不是理论上的：
第 1 档里塞着两种完全不同的东西——

- `linux.display`：操作员在 config.yaml 里**点名**的，是明确意图；
- 环境变量 `DISPLAY`：**捡来的**。asa-server 从桌面终端启动、从 `su -` 继承、
  在 WSL 里由 WSLg 自动导出……都会带上它。没有任何人表达过"请用这个显示"的意思。

把"捡来的"排在"自己管的"上面，是这一档里唯一说不通的地方。

### 1.2 WSL：自管 Xvfb 那一档**永远不成立**

已经在真机上钉死过（`ARKAPI_LINUX_VCREDIST_PLAN.md` §9.5，2026-08-30）：

```
$ mount | grep X11
none on /tmp/.X11-unix type tmpfs (ro,relatime)      ← WSLg 把它挂成只读
$ touch /tmp/.X11-unix/probe
touch: cannot touch '/tmp/.X11-unix/probe': Read-only file system
```

X 的 unix socket 路径 `/tmp/.X11-unix/X<n>` **写死在 xtrans 里**，没有任何环境变量
能改。于是在 WSL 上：

- 第 2 档：`x11SocketDirWritable()` 返回 false（`access(W_OK)` 拿到 EROFS）⇒ 走不通；
- 第 1 档：WSLg 会给用户会话导出 `DISPLAY=:0`，交互式启动时**直接命中**；
- 第 3 档：即使没有环境变量（systemd 服务那种），扫出来的第一个也是 WSLg 的 `:0`。

**三条路殊途同归，全都指向 WSLg。** `LINUX_DEPLOYMENT.md` L82-85 已经如实写着
"此时装不装 Xvfb 都一样"。

### 1.3 用 WSLg 的 `:0` 到底有什么问题

这不是洁癖，四条都有过对应的真实痕迹：

| # | 代价 | 说明 |
|---|---|---|
| 1 | **游戏窗口会真的弹到 Windows 桌面上** | WSLg 就是干这个的。`ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md` §2.1 记着"WSLg 里能看到游戏窗口"。一台服务器不该往用户桌面上丢窗口，更不该被人误手关掉 |
| 2 | **显示的生命周期不归我们管** | WSLg 的 X 服务活在 WSL 的系统发行版里，随 `wsl --shutdown`、Windows 登出、WSLg 自身重启而消失。我们的看门狗（`watchXvfb`）管不到它，`xvfb.state` 也认领不了它 |
| 3 | **它是共享的** | 同一台 Windows 上所有 WSL 发行版共用这个 `:0`。我们对它做的任何假设（`-noreset`、无认证、只有 Wine 的隐形窗口）在那里都不成立 |
| 4 | **它让"所有实例同一个显示"从必然变成偶然** | 见 §5.3 |

再加上一条一般性的：**同一份代码在开发机（WSL）和生产机（无头服务器）上走的是不同
的显示路径**。本仓库已经吃过太多次"在 A 上验证、在 B 上翻车"的亏
（`XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` §11 整节都是这个形状），能收敛成一条路就该收敛。

---

## 2. 目标与非目标

**目标**

1. 只要这台机器能跑起自管 Xvfb，**就用它** —— 不管宿主有没有 X、有没有 `DISPLAY`
   环境变量、是不是 WSLg。
2. WSL 上也要能跑起自管 Xvfb（§4），跑不起来时**行为与今天完全一致**（落回 WSLg），
   不能出现"为了追求一致性把唯一能用的路也堵死"。
3. 顺序变了之后，`planDisplay`（只读）与 `acquire`（动手）**仍然对同一件事给出同一个
   答案** —— 这条不变量是 §9.5 那次"自检通过、启动照死"的直接教训，不能因为加了回退
   链就松掉。

**非目标**

- **不改 `Options.NeedsDisplay` 的判据。** 显示只给 `AsaApiLoader.exe` 与
  `vc_redist.x64.exe`；`ArkAscendedServer.exe` 依旧不碰显示，纯 ARK 实例的启动路径
  一个字节都不变，也不会因此拉起一个 X 服务端。见 §5.4 对这个取舍的说明。
- **不引入 X 客户端库、不传 `XAUTHORITY`、不给 Xvfb 加 `-auth`。** 三者是一体的
  （`XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` §9 风险 3），本文不动它。
- **不做 VNC / X 转发 / 让人看见游戏画面。** 想看画面就 `linux.display=:0`，
  那正是逃生舱存在的意义。
- **Windows 侧零改动。**

---

## 3. 方案：候选链 + 顺序调整

### 3.1 新顺序

| # | 档 | 触发条件 | 相对今天 |
|---|---|---|---|
| 1 | `displayConfigured` | **`linux.display` 点名**且握手能过 | 从原第 1 档拆出来：只认配置，不认环境变量 |
| 2 | **`displayManaged`** | 有 `Xvfb` 且 `/tmp/.X11-unix` 可写（可写性见 §4） | **升到这里**，本文的核心 |
| 3 | `displayEnv` | 环境变量 `DISPLAY` 且握手能过 | 从第 1 档降到这里 |
| 4 | `displayExisting` | 扫 `/tmp/.X11-unix/X<n>` | 不变（仍是最后一档） |

一句话读法：**点名的 > 自己管的 > 捡来的 > 扫出来的。**

第 3、4 档保留而不是删掉，是为了目标 2：一台没有 `Xvfb`、或 `/tmp/.X11-unix` 死活
写不了的机器（WSL 是其中一种），仍然要能跑起 ArkApi，而不是拿到一条
"本机没有可用的 X 显示"。

### 3.2 `planDisplay` 改为返回**候选链**

顺序一改就必须同时解决一个新问题：**第 2 档从"能不能"变成了"多半会"，而它的
`acquire` 是可能失败的**（Xvfb 装了但缺字体、`/tmp` 满了、被 SELinux 拦了……）。
今天这种失败会让启动直接被拒绝——这在"第 2 档只服务无头机"时是对的，但改序之后，
一台**本来靠 `:0` 跑得好好的**机器会因为 Xvfb 起不来而启动失败。**这是纯粹的回归，
必须在设计里堵掉。**

做法不是在 `acquire` 里偷偷换一条路（那会立刻破坏 §2 目标 3 的不变量），而是把
"计划"本身从一个变成一串：

```go
// planDisplay 返回按优先级排好的**候选链**，全都不成立时 blocked 说明原因。
// preflight / DisplayStatus / --check-only 仍然只问它，语义不变：
// 链非空 ⇔ 这台机器有合理把握拿到显示。
func planDisplay(cfg Config) (plans []displayPlan, blocked string)

// acquireDisplay 沿链依次尝试，第一个成功的即为结果。
// 每一次回退都记一条 WARN，带上前一档失败的原文。
func acquireDisplay(cfg Config) (target displayTarget, blocked string, err error)
```

- **不变量升级版**：`blocked == ""` ⇔ 链里**至少有一档**能成。这与原来的
  "`blocked == ""` ⇔ `acquire` 有合理把握成功"是同一句话，只是把"合理把握"落到了实处。
- `TestCheckDisplayAgreesWithPlan` / `TestDisplayStatusMatchesPlan` 语义不变，
  改为断言链的**头一档**。
- 回退**必须大声**：`logger.Warnf` 一条 + `displayTarget.How` 里带上
  "（自管 Xvfb 起不来：<原因>，已回退到宿主的 X 显示 :0）"，让 `verify-arkapi` 的
  `[3]` 与 `GET /api/system/preflight` 都能看见。**静默回退等于把这次改动的收益
  变成一个只有读代码才知道的秘密。**

### 3.3 不新增"显示模式"开关

想过 `linux.display_mode: xvfb|host|auto`，**否掉**：

- `linux.display` 已经是完整的逃生舱。想用宿主的 `:0`？写 `linux.display: ":0"`，
  它是第 1 档，赢过一切。想调试时看画面？同一条。
- 多一个开关就多一组组合要测、要在文档里解释，而它能表达的东西 `linux.display`
  已经能表达。
- `UMU_PREFIX_PER_INSTANCE_PLAN.md` §5.2 拒绝把 `PROTON_VERB` 做成配置项时用的是
  同一条理由：**一个没有正确用途的开关，只会制造"配错了就全挂"的新坑。**

唯一新增的配置项在 §4.4，而且它管的是"允不允许我们动 mount"，不是"用哪个显示"。

---

## 4. WSL：让 `/tmp/.X11-unix` 变得可写

### 4.1 先说死一件事：换个目录是不可能的

X 的 unix socket 路径由 xtrans 在**编译期**写死（`display_linux.go` 里 `x11SocketDir`
的注释已经记着这条）。`-nolisten`、`-displayfd`、任何环境变量都改不了它。
所以"让 Xvfb 把 socket 建到别处"这条路不存在，只能让**那个目录**可写。

抽象 socket（`@/tmp/.X11-unix/X100`）同样不算数：Xvfb 会建，但 pressure-vessel
需要**文件系统里的**那个 socket 才能 bind 进容器，它自己的告警把这件事说得很清楚：

```
W: X11 socket /tmp/.X11-unix/X100 does not exist in filesystem,
   trying to use abstract socket instead.
```

### 4.2 候选：两个正交的轴，不是一条线

一开始我把候选排成了"W1 → W2 → W3"一条线，那个排法是错的：**"怎么让它可写"与
"在哪个 mount namespace 里做"是两个正交的轴**，而决定"修不修得好"的是前者。

| | 宿主 mount ns | 私有 mount ns（`unshare(CLONE_NEWNS)`） |
|---|---|---|
| **remount rw**（`MS_REMOUNT\|MS_BIND`） | **W1** | 与 W1 同生共死——ro 标志被锁 / 底层 superblock 只读时，换个 ns 一样失败 |
| **overmount**（盖一层新 tmpfs） | **W2′** | **W3** |

**namespace 那一列唯一的价值，是让 overmount 变得安全**：盖掉 `/tmp/.X11-unix`
会连带遮住 WSLg 的 `X0`，在宿主 ns 里这会影响整个发行版的 GUI 应用，在私有 ns 里
只影响我们自己的进程树。它**不会**让一个修不好的挂载点变得修得好。

| # | 做法 | 效果 | 代价 | 取舍 |
|---|---|---|---|---|
| **W1** | 宿主 ns 里 `mount -o remount,rw /tmp/.X11-unix` | 目录变可写，**WSLg 自己的 `X0` 原样保留**，Xvfb 的 `X100` 与它并存 | 改了宿主的 mount 表；WSL 上那是与 WSLg 系统发行版**共享**的 tmpfs，我们的 socket 在那边也看得见 | ✅ **首选**。一次 syscall、可逆、不遮挡任何现有 socket |
| **W2′** | 宿主 ns 里盖一层新 tmpfs，**再把原来的 `X0` 单独 bind 回去** | 同上 | 步骤多（mount → bind → 失败要回滚）；漏了那次 bind 就会**让整个发行版的 WSLg GUI 应用瞎掉** | ⚠️ **W1 的兜底**，仅在 §4.6 的 errno 表指向它时才做 |
| W3 | 私有 mount ns 里 overmount | 完全不碰宿主 | 见 §4.6：不是"多一个 syscall"，而是一个内部 daemon | ❌ 不做（§4.6） |

### 4.3 落点：仍然按**能力**触发，不是按"是不是 WSL"

**绝不新增 `isWSL()` 之类的判据来决定要不要动手。** 这正是本仓库反复踩的那个形状
（`XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` §10 与 §11.6：判据要落在能力上，不是落在
"这台机器长什么样"）。任何一台把 `/tmp/.X11-unix` 挂成只读的机器都该得到同样的处置，
WSL 只是其中最常见的一种。

处置挂在**已经存在**的 `ensureX11SocketDir(cfg)` 上（`xvfb_linux.go`），它本来就是
"acquire 那一侧、只在真要拉起 Xvfb 之前动手"的那个函数，新增一档：

```
ensureX11SocketDir:
  euid != 0                     → 直接返回（今天的行为）
  目录不存在                     → mkdir 1777（今天的行为）
  目录在、运行时用户写不进去      → chmod 1777（今天的行为）
  目录在、access(W_OK) == EROFS  → 【新增】尝试 remount rw；
                                   成功 → 记一条 INFO，继续
                                   失败 → 返回错误，由 §3.2 的候选链回退到下一档
```

判据是 `EROFS` 这个**错误码**，不是发行版、不是 `/proc/version` 里有没有 microsoft。
`x11SocketDirWritable()` 这个只读函数**保持不变**，继续在 `planDisplay` 里如实报告
"现在不可写"—— 判断与动作各归各位这条分界（§3.1 of the XVFB plan）不能因为本文而破。

> ⚠️ 于是有一个必须显式处理的后果：`planDisplay` 侧看到的仍是"不可写 ⇒ 第 2 档不成立"，
> 而 acquire 侧其实**可能修得好**。两种解法：
> ① `x11SocketDirWritable()` 增加一个"不可写但我们是 root 且可能 remount"的中间态，
> 让第 2 档进入候选链（但排在它后面仍挂着第 3、4 档兜底）；
> ② 保持只读判断不变，第 2 档在 WSL 上依然不进链。
> **推荐 ①**：否则本文在 WSL 上等于什么也没做——而 WSL 正是提出这个需求的场景。
> 代价是 preflight 的 `How` 文案要能表达"打算先把目录改可写再起 Xvfb"。

### 4.4 可逆性与开关

- **退出时还原**：`StopManagedDisplay()` 里把我们 remount 过的那个挂载点改回 `ro`
  （best-effort，失败只记日志）。已经建好的 socket 不受影响——连接一个已存在的
  socket 不需要对目录有写权限。
- **开关**：`linux.allow_x11_remount`，默认 **`true`**。
  - 默认开的理由与 `ensureX11SocketDir` 现有的自动 `chmod 1777` 一致：
    "能修就别只判"（§11.6 第 2 条）。而且它是**唯一**能让 WSL 走上自管 Xvfb 的手段，
    默认关掉等于这个方案在提出它的那个场景里默认不生效。
  - 提供关掉的理由：动 mount 表比 chmod 一个目录重，共享环境里的管理员有权拒绝。
    关掉之后行为与今天逐字相同（落回 WSLg）。
  - 无论开关如何，**动手时必须留一条 INFO 日志**，写明改了哪个挂载点、为什么。
- 新配置项要同时改**四处**：`appconfig`（结构体 + 模板 + 校验）、`runner.Config`，
  以及 `runner.Configure` 的**三个**调用点（`main.go` / `internal/actions/setup.go` /
  `internal/gui/gui.go`）——`Configure` 是**整体覆盖不是合并**，漏一处就静默失效，
  §11.4 刚为此翻过一次车。

### 4.5 remount 的两件真机确认

**第 1 条已确认成立**（2026-09-01，WSL2）：

```
➜ mount -o remount,rw /tmp/.X11-unix && touch /tmp/.X11-unix/probe && echo OK
OK
```

也就是说 WSLg 那个只读 bind **没有被 lock**，源超级块也是可写的，`MS_REMOUNT|MS_BIND`
这条路走得通。W1 因此是可行的，**W2′ 与 W3 都不必做** —— 它们存在的唯一理由是
「remount 被内核拒绝」，而这个前提在唯一已知的只读挂载环境里不成立。
（§4.6.3 的阶梯保留在文档里：将来若在别的环境上真的撞到 `EPERM`/`EROFS`，
那张 errno 表仍然是决定下一步的依据。）

> 探测留下的 `/tmp/.X11-unix/probe` 无害，可以删掉：`firstUsableX11Display` 只认
> `X<数字>` 形式的条目，别的名字直接跳过。

**第 2 条仍待实测**：目录变可写、Xvfb 的 `X<n>` 建出来之后，**pressure-vessel 能不能
把它 bind 进容器**、`AsaApiLoader.exe` 能不能加载。§9.5 的失败链条到这一步就该断了，
但那是推论。这要跑一次真正的 ArkApi 实例启动才能知道（§7.2 用例 6）。

**如果 2 失败**：不是 W1 的问题（socket 已经建出来了），而是容器那一侧 —— 届时看
`launcher.log` 里 pressure-vessel 还报不报 `X11 socket ... does not exist in filesystem`。
无论如何 WSL 都会落回 WSLg 的 `:0`（§3.1 的第 3/4 档），与此前行为一致。

### 4.6 W1 失败之后的阶梯：errno 说了算，namespace 不是兜底

**结论：私有 mount namespace（W3）不做自动兜底。** 不是因为它复杂，而是因为
**它救不了 W1 的多数失败原因**——`unshare` 换的是"谁看得见这次改动"，不是
"这次改动能不能成功"。

#### 4.6.1 按 errno 分类

| W1 的 errno | 原因 | remount 能救 | overmount 能救 | 私有 ns 的额外帮助 |
|---|---|---|---|---|
| `EPERM`，挂载带 `MNT_LOCKED` | ro 标志被锁死（多见于从别的 ns 传播来的挂载） | ❌ | ✅ | 仅让 overmount 安全 |
| `EROFS` / 底层 superblock 只读 | 源 tmpfs 本身就是 ro | ❌ | ✅ | 同上 |
| `EPERM`，无 `CAP_SYS_ADMIN` | asa-server 不是 root | ❌ | ❌ | ❌ **更糟**：`unshare(CLONE_NEWNS)` 同样要 `CAP_SYS_ADMIN`，得先开 user namespace；而 userns 里再 setuid 到 `asa-umu-runtime` 要配 uid 映射，与 `runtimeuser_linux.go` 整套降权逻辑冲突 |
| LSM（SELinux / AppArmor）拒绝 mount | 策略 | ❌ | ❌ | ❌ 策略在 namespace 里照样生效 |

**四种失败里没有一种是"只有 namespace 能救"的。** 能把前两种救回来的是 **overmount**，
而 overmount 在宿主 ns 里也做得了（W2′），只要记得把 `X0` bind 回去。

#### 4.6.2 W3 的真实代价：不是一个 syscall，是一个内部 daemon

即便将来真要走这条路，也得先认清它要付什么：

| # | 代价 | 说明 |
|---|---|---|
| 1 | **`setns(CLONE_NEWNS)` 在 Go 里做不到** | Go runtime 建线程带 `CLONE_FS`，而挂载命名空间的 `setns` 要求调用者不与其他线程共享 fs 状态，直接 `EINVAL`。runc 为此专门写了 C constructor（nsexec）。纯 Go 只剩两条路：调外部 `nsenter` 二进制——**又变成"这台机器有没有某个命令"这种判据，正是 `xvfb-run` 那一课**；或者自我 re-exec 并在 Go runtime 起线程之前 setns，同样要 cgo |
| 2 | **需要一个常驻的 namespace 持有进程 + 启动代理** | Xvfb 是**进程级单例**、游戏是**随后**才启动的，两者必须在同一个 ns 里。于是每次实例启动都得由那个持有进程来 fork |
| 3 | **PTY 要跨进程传** | ArkApi 实例是挂在 PTY 上起的（`instance/server.go` 里 PTY 与 `NeedsDisplay` 由同一个 `arkAsaApiRunning` 决定，go-pty 设 `Setctty`）。启动搬到持有进程之后，pty master 得靠 `SCM_RIGHTS` 传回来 |
| 4 | **给 umu 进程链重新加一层** | 那正是删掉 `xvfb-run` 时明确买到的东西（`XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` §3.3：删掉之后 PTY 的对端直接就是 `python3 umu-run`，信号与 KillTree 少一层间接） |
| 5 | 与 pressure-vessel 的嵌套 | bwrap 会在我们的 ns 之内再开一层。理论上没问题，但这是又一个未经验证的层，而本仓库每加一层都真的付过一晚上（`inheritedEnv` 注释里的 `DBUS_SESSION_BUS_ADDRESS`） |

**顺带否掉一个看起来更轻的变体**："每次启动时自己 `unshare` 一下"——那等于
per-launch Xvfb，`XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` §4.3 已经否过（`Handle.Wait`
等的是 umu-run 的退出，而游戏是加载器的孙子进程，关早了会把显示从活着的游戏脚下抽走），
而且会毁掉"所有实例同一个显示"这个性质（§5.3）。

#### 4.6.3 采纳的阶梯

```
W1  remount rw（宿主 ns）
 └─ 失败 → 看 errno
      ├─ EPERM(locked) / EROFS  → W2′ overmount + 把原 X0 bind 回去
      │                            （bind 失败必须整体回滚，见 §8 风险 3）
      └─ 其他（无 CAP_SYS_ADMIN、LSM 拒绝）→ 不再尝试
 └─ 都不成 → §3.2 的候选链回退到第 3/4 档（宿主 / WSLg 的 X 显示）
```

**W2′ 这一档同样是"探通了再写代码"**：先按 §4.5 拿到 W1 的真实 errno。
如果 W1 在 WSL 上本来就成功（很可能），这一整节都不必落地。

> **结论（2026-09-01）：W1 在 WSL2 上实测成功，因此 W2′ 不做**，本节整体转为存档。
> 它保留在文档里的价值是那张 errno 表：将来若在别的环境上真的撞到 remount 被拒，
> 不必从头再推一遍「namespace 是不是兜底」——答案是不是，而 overmount 才是。

#### 4.6.4 不救回来的代价有多大

**只是"WSL 保持今天的行为"**——而今天的行为是**已经真机验证过能跑的**
（`ARKAPI_LINUX_VCREDIST_PLAN.md` §9.6：WSLg 的 `:0`，52 秒起服）。
WSL 是开发环境，不是部署目标（`LINUX_DEPLOYMENT.md` 面向的是真服务器）。
为它引入一套 daemon 架构，投入产出比是负的；而本文的主体收益——
无头机与桌面机上不再蹭宿主显示——**完全不依赖 §4 的任何一档**。

> 什么情况下才该重开 W3：WSL 被正式列为受支持的部署形态，**且** §4.6.1 的 errno
> 表明 W1/W2′ 都过不去。两个条件缺一不可。到那时它也该是一份独立的计划文档，
> 而不是本文的一个小节。

---

## 5. 影响面

### 5.1 会变的行为

| 场景 | 今天 | 改后 |
|---|---|---|
| 无头服务器（无 X、无 `DISPLAY`） | 自管 Xvfb | **不变** |
| 无头服务器 + 运维在 SSH 里带了 `DISPLAY` 转发 | 用那个转发来的显示 | **自管 Xvfb**（转发的显示随 SSH 断开而消失，本来就不该用） |
| 桌面机 / 从桌面终端启动 asa-server | 用桌面会话的 `:0`，游戏窗口弹在用户桌面上 | **自管 Xvfb**，桌面上什么都不出现；注销也不影响实例 |
| WSL2 + WSLg | WSLg 的 `:0` | **自管 Xvfb**（若 §4 的 remount 可行）；否则仍是 WSLg，与今天一致 |
| `linux.display: ":0"` 已配置 | 用 `:0` | **不变**（第 1 档） |
| 装了 Xvfb 但它起不来（缺字体等） | 启动被拒绝 | 沿候选链**回退**到宿主/现成显示并 WARN；一台都没有才拒绝（§3.2） |

### 5.2 不会变的

- `ArkAscendedServer.exe` 的启动路径：不解析显示、不拉起 Xvfb、不多一个进程。
- Windows：`display_linux.go` / `xvfb_linux.go` 都带 `//go:build linux`，无影响。
- Xvfb 的生命周期三层保证、认领机制、看门狗：全部照旧。

### 5.3 与「共享 prefix 多 ArkApi」那份计划的关系

`SHARED_PREFIX_MULTI_ARKAPI_PLAN.md` §2.3 的关键论据是"所有显示路径都只会给出
同一个显示"。本文对它有**一正一反两个影响**，两个都已回填进那份文档。

**正面：常规路径上更结实了。** 候选链的头一档（自管 Xvfb）是**进程内单例**，
天生对所有实例同一个显示；而被降级的第 3、4 档虽然也各自是单值，靠的却是
"环境变量不会中途变""扫描结果稳定"这类偶然性质。改序之后，
"所有实例同一个显示"从**各条路径各自碰巧成立**，变成**头一档在结构上保证**。

**反面：候选链引入了一条"拿到不同显示"的新路径。** §3.2 的回退是为了不让 Xvfb
起不来变成启动失败，但它的代价是：先起的实例在自管的 `:100`、后起的实例回退到宿主的
`:0`。加上早就存在的另一条（Xvfb 中途死掉、看门狗**换号**补起），那份计划 §6.1 原本
设想的静态谓词 `SameDisplayForAllInstances()` **不成立**——它会在系统已经出过一次岔子
之后放行一次注定挂死三分钟的启动。已更正为**按次比对**（`CurrentDisplay()` 与本次
将拿到的显示相等才放行），见那份文档的 §2.4 与 §6.1。

两份计划互不阻塞，但如果两个都要做，**先做本文**：它让那个实验的前提更干净
（尤其在 WSL 上做实验时，改序之后 WSL 与生产走的是同一条显示路径）。

### 5.4 为什么不顺手给所有实例都配上显示

"把 umu 一律定向到 Xvfb"有一个更激进的读法：连 `ArkAscendedServer.exe` 也给
`DISPLAY`。**不采纳**，两条理由：

1. **它会让每一台部署都多一个常驻 X 服务端。** 绝大多数部署不开 ArkApi，
   `ArkAscendedServer.exe` 在无头机上 42 秒就开始监听——为一件不需要的事收一份常驻成本。
2. **`NeedsDisplay` 现在还兼着别的职责**：`instance/server.go` 里 PTY 与 `NeedsDisplay`
   由同一个 `arkAsaApiRunning` 决定，Xvfb 的生命周期论证（"需要显示的实例本来就活不过
   asa-server"）建立在这个耦合上。动它要重走那套论证，收益却是零。

如果将来确实想要（例如为了让所有实例的 Wine 环境完全一致），这是 `Options.NeedsDisplay`
一行的事——但请另开一份计划，把 §4.3 那套生命周期论证重新走一遍。

---

## 6. 改动清单

| 文件 | 改动 | 阶段 |
|---|---|---|
| `internal/runner/display_linux.go` | `displayKind` 拆出 `displayConfigured`；`planDisplay` 改为返回 `[]displayPlan`；`acquireDisplay` 沿链回退并 WARN；`How` 文案带上回退原因 | 1 |
| `internal/runner/display_linux.go` | `x11SocketDirWritable()` 增加"不可写但可能 remount"的中间态（§4.3 的解法 ①） | 2 |
| `internal/runner/xvfb_linux.go` | `ensureX11SocketDir` 新增"不可写 → W1 remount"一档；记录被改过的挂载点供还原 | 2 |
| `internal/runner/xvfb_linux.go` | 【条件】W1 的 errno 落在 §4.6.1 前两行时才做 W2′（overmount + `X0` bind + 失败整体回滚） | 2b |
| `internal/runner/xvfb_linux.go` | `stopManagedXvfb` / `StopManagedDisplay` 路径上还原 `ro`（best-effort） | 2 |
| `internal/runner/runner.go` | `Config` 加 `AllowX11Remount`；`DisplayInfo.How` 的示例文案更新 | 2 |
| `internal/appconfig/{config,template,validate}.go` | `linux.allow_x11_remount`（默认 true） | 2 |
| `main.go`、`internal/actions/setup.go`、`internal/gui/gui.go` | `runner.Configure` **三个**调用点补新字段（§4.4 末尾） | 2 |
| `internal/runner/preflight_linux.go` | `checkDisplay` 改问链的头一档；`Detail` 说明将使用哪一档 | 1 |
| `internal/actions/verify_arkapi.go` | `[3] 图形显示` 打印链的头一档 + 是否发生过回退 | 1 |
| `docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` | §3.2 的三级表标注"顺序已被本文调整"，并回链 | 3 |
| `docs/LINUX_DEPLOYMENT.md` | 三级解析表改为四级；L82-85 的 WSL 注意事项按 §4.5 的实测结果改写 | 3 |
| `docs/ARKAPI_LINUX_VCREDIST_PLAN.md` | §9.5 的三级表同样标注 | 3 |
| `CLAUDE.md` | `display_linux.go` 那段的三级说明改四级，并写明"点名的 > 自己管的 > 捡来的 > 扫出来的" | 3 |

**阶段 1（顺序调整）可独立交付**，不依赖 §4 的任何结论，也不需要 WSL 真机；
**阶段 2（WSL 可写化）依赖 §4.5 的实测**，探不通就整段不做。

---

## 7. 验证

### 7.1 单测（Windows 上以 `GOOS=linux go vet` 兜底）

| # | 用例 | 钉住什么 |
|---|---|---|
| 1 | `TestPlanDisplayPrefersManagedOverEnv` | 有 `Xvfb` + 目录可写 + 环境里有能用的 `DISPLAY` 时，链的头一档是 `managed` —— **本文的核心断言** |
| 2 | `TestPlanDisplayConfiguredWinsOverManaged` | `linux.display` 点名时它仍是第一（逃生舱不能被自己的新顺序吃掉） |
| 3 | `TestPlanDisplayChainOrder` | 四档齐全时链的顺序逐位相等 |
| 4 | `TestPlanDisplayBlockedOnlyWhenChainEmpty` | 一档都不成立才 `blocked` |
| 5 | `TestAcquireFallsBackAndReportsWhy` | 头一档 acquire 失败时用下一档，且 `How` 里带着失败原因 |
| 6 | `TestCheckDisplayAgreesWithPlanHead` / `TestDisplayStatusMatchesPlanHead` | 自检、诊断与启动仍问同一个函数（不变量升级版） |
| 7 | `TestDisplayStatusStartsNothing` | **回归**：改序之后 preflight 仍然不许拉起 Xvfb |
| 8 | `TestEnsureX11SocketDirRemountOnlyOnEROFS` | 只有 EROFS 才 remount；权限不足走 chmod、目录不存在走 mkdir，三条互不串门 |
| 9 | `TestEnsureX11SocketDirRespectsAllowRemount` | 开关关掉时不动 mount，只返回错误 |
| 10 | `TestRemountNeverKeyedOnDistro` | 代码里不存在"是不是 WSL"这种判据（§4.3 的原则，用 grep 式断言或代码审查项） |

### 7.2 真机矩阵

| # | 环境 | 场景 | 期望 |
|---|---|---|---|
| 1 | 无头 Linux（AlmaLinux / Ubuntu） | ArkApi 实例启动 | 与今天一致（本来就走自管 Xvfb），**回归项** |
| 2 | 无头 Linux + `DISPLAY=:0` 人为导出 | 同上 | 用**自管 Xvfb**，日志明确说明忽略了环境变量 |
| 3 | 桌面 Linux，从终端启动 asa-server | 同上 | 桌面上**不出现**游戏窗口；注销桌面会话后实例仍在 |
| 4 | 桌面 Linux + `linux.display: ":0"` | 同上 | 用 `:0`，窗口出现（逃生舱有效） |
| 5 | **WSL2 + WSLg** | 先手工探 §4.5 的两条 | ✅ **remount 已实测成功**（2026-09-01）；pressure-vessel 能否把 `X<n>` 带进容器仍未验，由用例 6 覆盖 |
| 6 | WSL2 + WSLg | ArkApi 实例启动 | 若 5 通过：走自管 Xvfb，窗口不再出现在 Windows 桌面；若 5 不通过：回退到 `:0`，与今天一致且日志说明原因 |
| 7 | WSL2 + `allow_x11_remount: false` | 同上 | 不动 mount，落回 WSLg，行为与今天逐字相同 |
| 8 | 故意让 Xvfb 起不来（`chmod -x`／删字体）+ 机器上有可用 `:0` | ArkApi 实例启动 | **回退**到 `:0` 并 WARN，**不是**启动失败（§3.2 要堵的那个回归） |
| 9 | 同上但机器上没有任何其他显示 | 同上 | 启动被拒绝，附 `xvfb.log` 末尾与针对性提示（今天的行为，不许因回退链而变软） |
| 10 | remount 之后重启 asa-server | `mount \| grep X11` | 挂载点被还原为 `ro`（§4.4） |

---

## 8. 风险

| # | 风险 | 影响 | 缓解 |
|---|---|---|---|
| 1 | **改序把一台原本好用的机器变成启动失败** | 桌面机/WSL 上的回归 | §3.2 的候选链回退 + 用例 8。这是本文里唯一必须做对的一条 |
| 2 | ~~remount 在 WSL 上根本不成功~~ | ~~§4 白做~~ | ✅ **已排除**：2026-09-01 WSL2 实测 `mount -o remount,rw` 成功（§4.5） |
| 3 | remount 影响 WSLg 系统发行版（共享 tmpfs） | 别人的 GUI 应用 | W1 只改挂载点的读写属性、不遮挡任何已有 socket；我们的 `X100` 对 WSLg 无意义。用例 5 要顺带确认 WSLg 应用仍正常 |
| 3b | **W2′ 的 overmount 盖住了 `X0`，而 bind 回去那一步失败** | 整个发行版的 GUI 应用瞎掉 | 这是 W2′ 唯一的重大风险，也是它排在 W1 之后的原因：**bind 失败必须立刻 `umount` 那层 tmpfs 整体回滚**，宁可回到"目录不可写、落回候选链"。落地时这一条要有专门的单测（模拟 bind 失败 → 断言 tmpfs 已被卸掉） |
| 4 | 改了宿主 mount 表且没还原（asa-server 被 `kill -9`） | 目录一直是 rw | 影响很小（1777 本来就是 X 的约定），且下次启动会再次 remount 而不是报错。可接受，不为它加第二层兜底 |
| 5 | 更多机器开始真的运行 Xvfb ⇒ 缺字体等失败面暴露 | 新的失败报告 | 这是**暴露**不是**引入**：`xvfbFailureHint` 已经认得这些模式。用例 8/9 覆盖 |
| 6 | 候选链让"到底用了哪个显示"更难说清 | 排障成本 | `How` 必须带回退原因（§3.2），`verify-arkapi [3]` 与 `/api/system/preflight` 都能看到 |

---

## 9. 一句话总结

自管 Xvfb 是这条链上**唯一由我们启动、由我们监控、随我们退出**的显示，它却排在
一个从环境里捡来的变量后面；在 WSL 上更是被一个只读挂载彻底挡在门外，于是开发机
和生产机走的从来不是同一条路。
**把顺序改成"点名的 > 自己管的 > 捡来的 > 扫出来的"，再按 EROFS 这个能力信号
（而不是"是不是 WSL"）把那个只读目录修好** —— 剩下的第 3、4 档只作为兜底，
保证这次收敛不会把任何一台今天能跑的机器变成跑不了。
