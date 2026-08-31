# 跨发行版的虚拟显示：从 `xvfb-run` 改为自管 `Xvfb`

> 目标：让 Fedora / RHEL / Rocky / Arch 这类**不随包提供 `xvfb-run` 包装脚本**的发行版
> 也能过 `x11-display` 自检、也能给 `AsaApiLoader.exe`（ArkApi）与微软 VC++ 安装器
> 提供显示。
>
> 定位：`docs/ARKAPI_LINUX_VCREDIST_PLAN.md` §9 的**第三轮修正**。§9 解决的是「Wine 下
> 没有显示 ⇒ 加载器零输出退出」，§9.5 解决的是「装了 xvfb 也可能没用（`/tmp/.X11-unix`
> 只读）」，本文解决的是「**这台机器压根没有 `xvfb-run` 这个命令**」。
>
> 一句话方案：**不再依赖 `xvfb-run` 这个 Debian shell 脚本，改为由 asa-server 自己
> 拉起并托管一个 `Xvfb` 服务端进程。**

---

## 0. 状态

**§8 的第一步与第二步已实现**（2026-08-31），第三步（真机验证与事实回填）待做。

落地文件：

- `internal/runner/xvfb_linux.go`（新增）：Xvfb 的发现 / 启动 / 就绪判定 / 单例 /
  孤儿认领 / 失败诊断
- `internal/runner/display_linux.go`：`resolveDisplay` 拆成只读的 `planDisplay` 与
  会动手的 `acquire`（外加二合一的 `acquireDisplay`）；删掉 `xvfb-run` 分支与
  `displayTarget.Wrapper`，`wrap()` 简化为 `applyTo(env)`
- `internal/runner/preflight_linux.go`：`xvfbInstallHint` 改为各发行版**提供 Xvfb 的包**，
  新增 `xvfbFontHint`；`checkDisplay` 改问 `planDisplay`
- `internal/runner/runner.go`：`Config` 加 `Display`/`XvfbBin`/`XvfbScreen`，
  `DisplayInfo` 加 `Managed`/`Display`
- `internal/runner/runner_linux.go` / `vcredist_linux.go`：调用点切换，
  并区分「本机没有显示能力」与「有能力但这次没拿到」两种失败
- `internal/appconfig`（`config.go` + `template.go`）、`main.go`：三个新配置项
- `internal/runner/runner.go` / `runner_windows.go`：新增 `StopManagedDisplay()`（Windows 空实现）
- `internal/webapi/actions.go`（`ActionAPI` 收到信号之后）、`internal/svcmgr/service.go`
  （service `Stop`）：进程退出路径调 `StopManagedDisplay()`
- 测试：`xvfb_linux_test.go`（新增 17 组）+ `display_linux_test.go`（改写）

`go build ./...`（Windows）、`CGO_ENABLED=0 GOOS=linux go vet ./...`、
`go test ./internal/runner/... ./internal/appconfig/...` 均通过。
带 `//go:build linux` 的单测在 Windows 开发机上只做到编译期检查（`go vet`），
真正跑起来要等 §7.3 的真机验证。

**§4.3（生命周期）在实现中反复过一次，最终结论是原方案的「退出时停」+ 两层兜底**，
中途那次「不杀」的理由建立在一个错误前提上（以为需要显示的实例活得比 asa-server 久，
实际上它们挂在 PTY 上，随 asa-server 一起被 SIGHUP）。原委记在该节。

文中标 **【待实测】** 的条目是我在 Windows 开发机上无法验证的发行版事实，
需要在真机上按 §7.1 给的命令核一遍，结果回填本文。

---

## 1. 问题

### 1.1 现状：解析器把「有没有 `xvfb-run`」当成能不能开虚拟显示

`internal/runner/display_linux.go` 的 `resolveDisplay` 是三级解析：

| # | 路径 | 前提 |
|---|---|---|
| 1 | 显式 `DISPLAY` | 变量非空 + socket 文件在 + **X11 握手能过** |
| 2 | `xvfb-run` 现开一个虚拟显示 | **`exec.LookPath("xvfb-run")` 成功** 且 `/tmp/.X11-unix` 可写 |
| 3 | 系统里已在跑的 X 显示 | 扫 `/tmp/.X11-unix/X<n>` 逐个握手，取第一个能过的 |

第 2 条的实现方式是**命令前缀**（`displayTarget.Wrapper`）：把 `xvfb-run -a -e … -f …`
拼在 `python3 umu-run …` 前面，靠 `xvfb-run` 这个脚本代管 Xvfb 的起停。

### 1.2 `xvfb-run` 是 Debian 的东西，不是 X 的东西

`Xvfb` 是 X.Org 的服务端二进制（`xserver` 源码树里的 `hw/vfb`）。
`xvfb-run` 是 **Debian 打包时自带的一个 shell 脚本**（`/usr/bin/xvfb-run`，
Debian 的 `xvfb` 包里维护），上游 X.Org 从不发布它。因此各发行版给不给这个脚本，
纯看打包者心情：

| 发行版 | 提供 `Xvfb` 的包 | 是否随包给 `xvfb-run` |
|---|---|---|
| Debian / Ubuntu | `xvfb` | ✅ 有（脚本就是 Debian 维护的） |
| Fedora / RHEL / Rocky / Alma | `xorg-x11-server-Xvfb` | ⚠️ 用户反馈**没有**（不同版本可能不一致）【待实测】 |
| Arch / Manjaro | `xorg-server-xvfb` | ⚠️ 用户反馈**没有**【待实测】 |
| openSUSE | `xorg-x11-server-extra`【待实测】 | ⚠️ 未知【待实测】 |
| Alpine | `xvfb` + `xvfb-run`（独立包）【待实测】 | 需要单独装 |

**不需要把这张表核到十分准确才能动工** —— 结论已经确定：`xvfb-run` 的存在性
在发行版之间不一致，而 `Xvfb` 的存在性是一致的。这张表只影响提示文案（§5.8）。

### 1.3 于是有两个具体故障

**故障一：自检把能用的机器挡在门外。**
`checkDisplay()` 直接问 `resolveDisplay`，是**阻断级**检查。Fedora 上装好了
`xorg-x11-server-Xvfb`、`Xvfb` 就在 `/usr/bin/Xvfb`，但 `LookPath("xvfb-run")`
失败 ⇒ 第 2 条不成立；无头机上也没有现成 X 服务 ⇒ 第 3 条不成立 ⇒
`asa-server setup` 直接中止，报「本机没有可用的 X 显示，也没有 xvfb-run」。
**机器明明有能力开虚拟显示，程序却说它没有。**

这正是 `docs/ACL_PERMISSION_HARDENING_PLAN.md` §1 记过一次的错误形状：
「按包名/命令名判断能力」，而不是「按能力本身判断能力」。
`preflight_linux.go` 的包注释里写着这条原则（"a working loader/library/interpreter
matters here, not which package happened to provide it"），显示这一项是唯一的例外。

**故障二：拿不到 `xvfb-run` 就没有第二种开虚拟显示的办法。**
`Xvfb` 与 `xvfb-run` 的**命令形态完全不同**，不能互相顶替：

```bash
xvfb-run -a -e /path/xvfb.log -f /path/.Xauthority-xvfb  <要跑的命令> <参数...>
#         ↑ 包装器：自己挑显示号、起 Xvfb、设 DISPLAY/XAUTHORITY、跑命令、收尾

Xvfb :99 -screen 0 1280x1024x24 -nolisten tcp
#    ↑ 服务端：前台常驻，不接受「要跑的命令」，退出即显示消失
```

所以 `displayTarget.Wrapper`（命令前缀）这个抽象对 `Xvfb` **根本不成立** ——
`Xvfb` 不是包装器，它是个要被单独管起来的服务进程。这也是本方案的主要工作量所在。

---

## 2. 目标与非目标

**目标**

1. 只要机器上有 `Xvfb`（任何发行版、任何包名），`x11-display` 自检就应通过，
   ArkApi 实例与 VC++ 安装器就应拿得到显示。
2. Xvfb 起不来时，失败**可见**且**可诊断**：拿到它的 stderr、给出针对性提示，
   绝不出现「自检说好了、启动照样死」。
3. 不回归 Debian/Ubuntu 与 WSLg 两条已经在真机上验证过的路径（§9.6 那张表）。

**非目标**

- 不承诺 ArkApi 在 Wine 下稳定可用（`LINUX_COMPATIBILITY_PLAN.md` §1 目标 5 不变）。
- 不给 `ArkAscendedServer.exe` 加显示。它在无头机上 42 秒就开始监听，
  `NeedsDisplay` 依旧只对 `AsaApiLoader.exe` 与 vc_redist 安装器为真。
- 不引入任何 X 客户端库（`libX11`/`xdpyinfo`/`xauth`）。现有的 12 字节握手探测
  已经够用，且零依赖。
- 不做 X 转发、不做 VNC、不做 GPU/GLX 加速。

---

## 3. 方案总览

### 3.1 把「解析」与「获取」拆开

现在的 `resolveDisplay` 既是**判断**（preflight / `DisplayStatus()` / API 用）
又是**动作**（启动路径用）。一旦第 2 条从「拼个命令前缀」变成「真的 fork 一个
Xvfb 进程」，这两件事就必须分家 —— 否则 `GET /api/system/preflight` 会**顺手起一个
X 服务**，`asa-server setup` 的自检也会。

```go
// 只读、无副作用：一次 LookPath + 一次 stat + 至多几次本地握手。
// preflight / DisplayStatus / verify-arkapi --check-only 用这个。
func planDisplay(cfg Config) (displayPlan, string)

// 真的把显示拿到手：必要时启动 Xvfb 并等它就绪。启动路径用这个。
func (p displayPlan) acquire(cfg Config) (displayTarget, error)
```

`displayPlan` 只记「打算走哪条路」（`kindEnvDisplay` / `kindManagedXvfb` /
`kindExistingDisplay`）与人类可读的 `How`；`displayTarget` 仍是「怎么把显示施加到
一条命令上」，`wrap()` 保持不变。

**不变量**：`planDisplay` 返回 `blocked == ""` ⇔ `acquire` 有合理把握能成功。
现有的 `TestCheckDisplayAgreesWithResolve`（自检与启动必须问同一个函数）改成钉
`planDisplay`，语义不变。

### 3.2 修正后的解析顺序

| # | 路径 | 前提 | 变化 |
|---|---|---|---|
| 1 | 显式显示 | `linux.display` 配置项 **或** 环境变量 `DISPLAY`，且握手能过 | **新增配置项**（服务进程没有 `DISPLAY` 环境变量，见 §5.7） |
| 2 | **自管 Xvfb** | `Xvfb` 可执行（`linux.xvfb_bin` 或 PATH）**且** `/tmp/.X11-unix` 可写 | **本方案的核心改动**：判据从 `xvfb-run` 换成 `Xvfb`，实现从命令前缀换成托管进程 |
| 3 | 系统里已在跑的 X 显示 | 扫 `/tmp/.X11-unix/X<n>` 逐个握手 | 不变（WSLg 那条路径靠它） |

顺序保持不变（自管 Xvfb 优先于蹭现成显示）：自管的那个显示不依赖任何桌面会话，
用户注销、桌面重启都不会把游戏带走。

### 3.3 `xvfb-run` 分支：删掉，不保留

**决定：完全移除 `xvfb-run` 代码路径，Debian/Ubuntu 也走自管 `Xvfb`。**

理由：

- `xvfb-run` 内部跑的就是**同一个 `Xvfb` 二进制**（它从 PATH 找 `Xvfb`）。
  自管路径是它的超集，没有任何一台机器只能走前者。
- 两条路做同一件事，必然慢慢漂开 —— §9.5 已经吃过一次亏（preflight 与
  `resolveDisplay` 分家，结果自检通过、启动照死）。
- `xvfb-run` 有三个我们本来就在跟它较劲的毛病：
  ① Xvfb 起不来时**照样执行命令**（§9.5 的放大器）；
  ② 默认把 Xvfb 的输出丢 `/dev/null`（现在靠 `-e` 覆盖）；
  ③ 默认把 auth 文件写进**游戏工作目录**（现在靠 `-f` 覆盖）。
  自管之后这三条从「覆盖默认值」变成「压根不存在」。
- 它还在进程链里多插一层 shell：
  `xvfb-run → python3 → umu-run → srt-bwrap → … → wine`
  （`docs/ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md` §3 那张表）。删掉之后 PTY 的对端
  直接就是 `python3 umu-run`，信号与 KillTree 少一层间接。
- 它给出的显示带 xauth cookie，而本项目**刻意不传 `XAUTHORITY`**
  （理由见 `inheritedEnv` 与 §9.4）。也就是说 `xvfb-run` 分支是三条路里**唯一
  没被握手探测验证过**的一条 —— 它的显示按我们自己的标准是「不可用」的，
  只是因为 `xvfb-run` 顺手把 `XAUTHORITY` 塞进了子进程环境才能用。删掉它，
  三条路就都统一在「无认证握手能过」这一个判据上了。

---

## 4. 自管 Xvfb 的设计

### 4.1 启动参数

```
Xvfb -displayfd <fd> -screen 0 1280x1024x24 -nolisten tcp -noreset -ac
```

| 参数 | 为什么 |
|---|---|
| `-displayfd <fd>` | **让 X 服务端自己挑一个空闲显示号**，并把号码写回我们给的管道 fd。这是唯一没有 TOCTOU 的挑号方式：自己扫 `/tmp/.X11-unix/X<n>` 再启动，两个实例并发时会撞车（`xvfb-run -a` 就是靠 lock 文件 + 重试硬扛这个）。Go 里用 `cmd.ExtraFiles` 把管道写端交给子进程，它固定落在 fd 3。**注意：用了 `-displayfd` 就不能再给显示号参数**。X server ≥ 1.13 支持，2012 年之后的发行版都有【待实测：老 RHEL】 |
| `-screen 0 1280x1024x24` | 一块屏幕就够。24 位色是最保守的选择（帧缓冲 ≈ 5 MB）。可由 `linux.xvfb_screen` 覆盖 |
| `-nolisten tcp` | 不开 TCP 监听。显示只经 `/tmp/.X11-unix` 的 unix socket 暴露 |
| `-noreset` | 最后一个客户端断开时**不重置服务端**。默认行为会在 Wine 短暂断开重连的间隙把显示状态清掉，且 X 的 reset 语义在无人持有时可能让服务端退出 |
| `-ac` | 显式关闭访问控制。我们不传 `XAUTHORITY`，靠的就是「无认证握手能过」这条判据（§3.3 最后一段），`-ac` 让这件事变成明写的意图而不是默认值的巧合 |

**不传 `-auth`**：一旦带 cookie，我们自己的 `x11DisplayUsable()` 探测就连不上了 ——
那正是判断显示可用与否的唯一手段。安全代价见 §9 风险 3。

### 4.2 就绪判定：握手，不是 sleep

```
启动 Xvfb
  ├─ 从 displayfd 管道读一行显示号（带超时）
  │    读不到 / 进程已退出 ⇒ 立刻失败，附上 xvfb.log 的末尾几行
  └─ 轮询 x11DisplayUsable(":<n>")，直到成功或超时（建议 5s，间隔 50ms）
```

复用现成的 `x11DisplayUsable()`（12 字节 setup 请求，看回包首字节是不是 `1`）。
**这一步是本方案相对 `xvfb-run` 的最大收益**：`xvfb-run` 在 Xvfb 起不来时照跑命令，
我们则在 Xvfb 没就绪时**直接让启动失败**，并把 Xvfb 自己的错误原文交到用户手里。

Xvfb 的 stdout/stderr 落 `{运行时用户 HOME}/xvfb.log`（路径与现在一致，
`xvfbRunArgs` 的 `-e` 指的就是这个文件），追加写、由 lumberjack 之外的简单
截断策略管理（超过 1 MiB 时重建，避免无限增长）。

**已知的第一手失败**：最小化安装的发行版可能没有字体包，Xvfb 会以
`Fatal server error: could not open default font 'fixed'` 直接退出【待实测】。
识别这条错误并给出针对性提示（§5.8），比给一句「Xvfb 起不来」有用得多。

### 4.3 生命周期：进程内单例，不做 per-launch

**决定：整个 asa-server 进程共用一个自管 Xvfb，懒启动、用前健康检查，
不随单次启动创建/销毁。**

对照被否掉的方案：

| 方案 | 问题 |
|---|---|
| **per-launch**（每次 `Run` 起一个，`Handle.Wait` 返回后关掉） | ❌ **可能把显示从活着的游戏脚下抽走**。`Handle.Wait` 等的是 `umu-run` 的退出，而 ArkApi 那档真正的游戏进程是加载器的孙子进程，`umu-run` 退出不代表游戏没了（`waitForGamePID` 整套逻辑存在的原因就是这个）。关早了 = X 连接断 = 未定义行为 |
| **per-launch + 引用计数** | 复杂度换不来收益：Xvfb 常驻的代价是一个进程 + 几 MB 帧缓冲，而 ArkApi 实例本来就长期在跑 |
| **单例常驻**（采纳） | ✅ 归属清晰：显示是「主机的一项设施」，不是「某次启动的私有资源」。多个 ArkApi 实例（`per-instance` prefix 模式下可以并发）共用同一个显示，各自的 wineserver 各开各的窗口，互不相干【待实测：§7.2 用例 6】 |

实现要点：

```go
var (
    xvfbMu      sync.Mutex
    xvfbCurrent *managedXvfb   // nil = 还没起过
)

// ensureXvfb 返回一个**当下确实能连**的自管显示。
func ensureXvfb(cfg Config, bin string) (*managedXvfb, error) {
    xvfbMu.Lock()
    defer xvfbMu.Unlock()
    if xvfbCurrent != nil && x11DisplayUsable(xvfbCurrent.display) {
        return xvfbCurrent, nil          // 复用
    }
    if xvfbCurrent != nil {
        xvfbCurrent.stop()               // 死了：收尸后重开
        xvfbCurrent = nil
    }
    s, err := startXvfb(cfg, bin)
    if err != nil {
        return nil, err
    }
    xvfbCurrent = s
    return s, nil
}
```

「用前握手」这一下让 Xvfb 中途死掉变成可自愈的：下一次 ArkApi 启动会重开一个。

**不用 `Pdeathsig`。** 直觉上应该给 Xvfb 设 `SysProcAttr.Pdeathsig = SIGKILL`
让它随 asa-server 一起走，但 Linux 的 parent-death signal 跟的是**创建它的那个线程**
而不是进程；Go 的 M 会在空闲时退出，届时 Xvfb 会被**无缘无故杀掉** ——
而它一死，正在跑的 ArkApi 实例的显示就没了。宁可留一个孤儿进程，也不能冒这个险。
孤儿由 §4.4 处理。

停止时机：**Xvfb 跟着 asa-server 的生命周期走，进程退出时一起退出。**

> 这一节改过两次，两次的分歧点都在同一个事实上，记下来免得第三次又绕回去。
>
> 中途我曾按「实例活得比 asa-server 久，所以不能收显示」否掉过显式停止。
> **那个前提是错的**：需要显示的那批实例**恰恰是活不过 asa-server 的那批**。
> `internal/instance/server.go` 里 `PTY` 与 `NeedsDisplay` 由**同一个**
> `arkAsaApiRunning` 决定，而 go-pty 会给子进程设 `Setctty`
> （`cmd_unix.go:45-46`）—— pts 是整条 umu/wine 链的**控制终端**。asa-server 一退出，
> PTY master 关闭，内核就把 SIGHUP 发给该会话的前台进程组，整条链跟着走。
> 于是留下 Xvfb 什么也保不住，只会每重启一次攒一个。
>
> （不带 PTY 的普通实例确实能活过 asa-server —— 它们是 `Setsid` 出去的。
> 但它们压根不碰显示，所以与这条决定无关。）

三层保证，从软到硬：

| # | 手段 | 覆盖 | 说明 |
|---|---|---|---|
| 1 | **显式停** `runner.StopManagedDisplay()` | 正常退出 | `webapi.ActionAPI` 收到 SIGINT/SIGTERM 之后、`svcmgr` 的 service `Stop`。确定性，且留得下日志 |
| 2 | **`Pdeathsig=SIGTERM`** | SIGKILL / panic / OOM | 第 1 层没机会执行时由内核代劳。用 SIGTERM 而非 SIGKILL，好让 X 服务端自己清掉 `/tmp/.X11-unix/X<n>` 与 `/tmp/.X<n>-lock`（留下 lock 会让回退挑号逻辑白白跳过那个号） |
| 3 | **认领**（§4.4） | 前两层都没生效，或同机另有一个 asa-server 进程 | 下次启动把它认回来而不是再起一个 |

**第 2 层有个 Go 特有的坑，必须专门处理**：Linux 的 parent-death signal 跟的是
**创建子进程的那个线程**，不是进程 —— 那个线程一退出，子进程立刻收到信号。而 Go 的
调度器会在 M 空闲时回收线程，于是「随手 fork 一个带 Pdeathsig 的进程」是个定时炸弹：
某个与 Xvfb 毫无关系的时刻，某个线程退出，正在服务 ArkApi 实例的 X 服务端被杀。

解法是给 fork 这件事一个**专属的、永不退出的线程**：`xvfbSpawnLoop` 这个 goroutine
`runtime.LockOSThread()` 之后永不 Unlock、永不 return，所有 Xvfb 都由它 fork。
Pdeathsig 的语义于是从「某个线程死了」收敛成「asa-server 进程死了」，正是要的那个保证。
（另外 Go 把 Pdeathsig 设在切换 Credential **之后**——setuid 会清掉这个设置——
并且会复查父进程是否已先一步死掉，所以降权与它可以并存。）

**认领来的那个不归我们杀**：`stop()` 对 `adopted` 的目标是空操作。它是另一个
asa-server 进程 fork 的，杀了会把对方正在服务的实例弄死；对方自己的第 1、2 层会管它。

### 4.4 孤儿与复用

asa-server 被 `kill -9` 之后，Xvfb 会活下来。不做处理的话每次重启都会多一个。

处理办法：在 `{BaseDir}/xvfb.state` 里记 `display` + `pid` + 启动时间。
`ensureXvfb` 第一次被调用时先读它：

- 记录里的 pid 还活着、`/proc/<pid>/comm` 是 `Xvfb`、且那个显示握手能过
  ⇒ **认领它**，不再新起；
- 否则忽略记录，起新的并覆盖写。

顺带的好处：`per-instance` 模式下并发启动多个 ArkApi 实例时，它们天然复用同一个
显示（`xvfbMu` 保证进程内只起一个，state 文件保证跨进程不重复）。

> 有了 §4.3 的前两层，这一节是**第三层兜底**，不是主力：正常情况下 Xvfb 已经随
> asa-server 一起走了，认领只在「两层都没生效」或「同机上另有一个 asa-server 进程
> 已经起过一个」时才派上用场 —— 后者是真实场景：服务在跑，用户又敲了一条
> `asa-server verify-arkapi`，那条 CLI 应该复用而不是另起一个。

### 4.5 以什么身份运行

跟游戏进程同一个身份：`resolveRuntimeCredential(cfg)` 拿到的 credential
（降权时是 `asa-umu-runtime`，`umu_run_as_root=true` 或非 root 启动时为 nil）。
与 `runInPrefix`/`warmPrefix` 的做法一致。

理由与约束：

- Xvfb 要在 `/tmp/.X11-unix` 建 socket、在 `/tmp` 建 `.X<n>-lock`。两者都是 1777，
  降权用户写得进去 —— `x11SocketDirWritable()` 现有的 `o+w` 检查正是为这一条准备的
  （注释里写着「跑 Xvfb 的是降权用户，root 能写不代表它能写」），继续有效。
- `xvfb.log` 落运行时 HOME，属主天然正确。
- 也可以用 root 跑（socket 建出来是 0777，降权的游戏进程照样连得上），
  但没理由让一个常驻的、无认证的 X 服务端跑在 root 下。

环境：只给 `HOME` / `PATH` / `TMPDIR` / `LANG`，不继承 `os.Environ()`。
Xvfb 不进 pressure-vessel 容器，本来没有 `inheritedEnv` 那类顾虑，
但给最小集合更省事。`Setsid: true`，让它脱离 asa-server 的控制终端。

---

## 5. 详细改动

### 5.1 新文件 `internal/runner/xvfb_linux.go`

```go
//go:build linux

// managedXvfb 是本进程拉起并托管的一个 Xvfb 服务端。
type managedXvfb struct {
    display string     // ":100"
    pid     int
    cmd     *exec.Cmd
    log     string     // xvfb.log 路径，失败诊断用
}

func xvfbBinary(cfg Config) (string, error)       // cfg.XvfbBin 优先，其次 PATH，其次 xvfbExtraPaths
func startXvfb(cfg Config, bin string) (*managedXvfb, error)
func (x *managedXvfb) stop()
func ensureXvfb(cfg Config, bin string) (*managedXvfb, error)
func xvfbReadDisplayFD(r *os.File, timeout time.Duration) (string, error)
func waitDisplayUsable(display string, timeout time.Duration) bool
func xvfbFailureHint(logTail string) string       // 字体缺失等已知模式 → 针对性提示
```

`xvfbExtraPaths = []string{"/usr/bin/Xvfb", "/usr/X11R6/bin/Xvfb", "/usr/local/bin/Xvfb"}`
—— 与 `glibc32LoaderPaths` / `libzstdPaths` 同一个套路：PATH 之外再兜一层，
因为 systemd 服务的 PATH 可能被裁剪过。

### 5.2 改 `internal/runner/display_linux.go`

- 新增 `displayPlan` 与 `planDisplay()`（纯判断），`acquire()`（可能起进程）。
- `resolveDisplay` 的三处调用点改为 `planDisplay(...)` + `acquire(...)`：
  `runner_linux.go:run`、`vcredist_linux.go:ensurePrefixVCRedist`、
  `vcredist_linux.go:186`（`vcRedistStatus` 的诊断字段，**只用 plan**）。
- 删除 `xvfbRunArgs` 与 `displayTarget.Wrapper`：自管路径只需要追加一个
  `DISPLAY=`，命令前缀这个抽象没有第二个用户了，留着正是漂移的温床。
  `wrap(bin, argv, env) (string, []string, []string)` 因此简化成
  `applyTo(env) []string`，两个调用点（`runner_linux.go` / `runInPrefix`）同步改。
  **已按此实现。**
- 顶部包注释与 `x11SocketDirWritable` 的注释里凡是写 `xvfb-run` 的地方，
  改为 `Xvfb`。

### 5.3 改 `internal/runner/preflight_linux.go`

- `xvfbInstallHint` 重写（§5.8），措辞从「装 xvfb-run」改为「装 Xvfb」。
- `checkDisplay()` 改问 `planDisplay`，保持**阻断级**不变。理由不变
  （缺显示没有降级路径，与 acl 不同）。

### 5.4 改 `internal/runner/runner.go`

- `Options.NeedsDisplay` 的注释：`xvfb-run` → 「自管的 Xvfb 虚拟显示」。
- `DisplayInfo` 增加两个字段并更新示例文案：

```go
type DisplayInfo struct {
    Available bool   `json:"available"`
    How       string `json:"how"`     // "宿主的 X 显示 :0" / "自管 Xvfb 虚拟显示" / "系统里已在运行的 X 显示 :0"
    Blocked   string `json:"blocked"`
    Managed   bool   `json:"managed"` // 这个显示是不是我们自己起的
    Display   string `json:"display"` // 已经起来时的 ":100"，未起时为空
}
```

`DisplayStatus()` 依旧**只读**：它报告 plan + 当前单例的状态，绝不启动 Xvfb。

### 5.5 改 `internal/runner/runner_linux.go`

`run()` 里 `NeedsDisplay` 那一段从「解析 → wrap」变成「解析 → 获取 → wrap」，
错误文案区分两种失败：

- `planDisplay` 就 blocked（本机没有这个能力）→ 现有文案 + 安装提示；
- `acquire` 失败（有 `Xvfb` 但起不来）→ 新文案，附 `xvfb.log` 末尾几行与
  `xvfbFailureHint` 的针对性建议。

第二种是新增的失败面，也正是自管方案买到的东西：以前这种情况是**静默**的。

### 5.6 改 `internal/runner/vcredist_linux.go`

`ensurePrefixVCRedist` 的 `resolveDisplay` → `planDisplay` + `acquire`；
跳过安装时的文案里 `请%s` 那句仍指 `xvfbInstallHint`（内容已按 §5.8 更新）。
`runInPrefix(..., display ...displayTarget)` 的签名不变。

### 5.7 配置项（`internal/appconfig` 的 `LinuxConfig` + `runner.Config`）

| 键 | 默认 | 作用 |
|---|---|---|
| `linux.display` | 空 | 显式指定要用的 `DISPLAY`（如 `:0`）。**服务进程没有 `DISPLAY` 环境变量**（真机 `/proc/<pid>/environ` 里只有 `HOME=/root`），这是把「桌面会话里能用的显示」告诉后台服务的唯一办法。仍然要过握手检查，过不了就继续往下找 |
| `linux.xvfb_bin` | 空 | `Xvfb` 的显式路径。PATH 被裁剪、或装在非常规位置时用 |
| `linux.xvfb_screen` | `1280x1024x24` | 传给 `-screen 0` 的规格。排障用逃生舱（比如某些环境要 16 位色） |

三项都走既有的 `appconfig → applyAppConfig → runner.Configure` 通路，
无新机制。**优先级**：flag > `ASA_*` 环境变量 > config.yaml > 默认值，与现状一致。

### 5.8 提示文案

```go
// xvfbInstallHint 是各发行版装 Xvfb 的提示。注意包名给的是**提供 Xvfb 的包**，
// 不是 Debian 那个包装脚本 —— 后者只有 Debian 系才有，而我们不再需要它。
const xvfbInstallHint = "安装 Xvfb（Debian/Ubuntu: sudo apt install xvfb  |  " +
    "Fedora/RHEL: sudo dnf install xorg-x11-server-Xvfb  |  " +
    "Arch: sudo pacman -S xorg-server-xvfb  |  " +
    "openSUSE: sudo zypper install xorg-x11-server-extra）"

// xvfbFontHint: 最小化安装的系统常常没有字体，Xvfb 会直接 fatal 退出。
const xvfbFontHint = "Xvfb 缺少基础字体。请安装（Debian/Ubuntu: xfonts-base  |  " +
    "Fedora/RHEL: xorg-x11-fonts-misc  |  Arch: xorg-fonts-misc）"
```

【待实测】openSUSE 的包名与「Xvfb 无字体是否真的 fatal」两条，按 §7.1 核实后回填。

### 5.9 文档同步

| 文件 | 改什么 |
|---|---|
| `docs/ARKAPI_LINUX_VCREDIST_PLAN.md` | §9 末尾加 §9.7 指向本文（第三轮修正）；§9.5 那张三级表标注「已被本文替换」 |
| `docs/LINUX_DEPLOYMENT.md` | 依赖表的 `xvfb` 行给全发行版包名；§「为什么无头服务器也要装 xvfb」的三级表同步；故障排查表里两条 `xvfb-run` 相关说明改写 |
| `docs/ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md` | §3 进程链去掉 `xvfb-run →` 一层 |
| `docs/UMU_PREFIX_PER_INSTANCE_PLAN.md` | L141 那句「`xvfb-run` 时那是它私有的一个 Xvfb」改为「自管 Xvfb 是全进程共用的一个显示」——**并注意这不改变结论**：一个 prefix 只能跑一个 ArkApi，卡点是 Wine 会话不是显示 |
| `CLAUDE.md` | `display_linux.go` 那段说明里的 xvfb-run 表述 |

---

## 6. 改动清单（汇总）

| 文件 | 类型 | 说明 |
|---|---|---|
| `internal/runner/xvfb_linux.go` | **新增** | Xvfb 托管：发现、启动、就绪等待、单例、孤儿认领、停止 |
| `internal/runner/xvfb_linux_test.go` | **新增** | 见 §7.2 |
| `internal/runner/display_linux.go` | 改 | `planDisplay`/`acquire` 拆分；删 `xvfb-run` 分支；文案 |
| `internal/runner/preflight_linux.go` | 改 | `xvfbInstallHint` 重写；`checkDisplay` 改问 `planDisplay` |
| `internal/runner/runner.go` | 改 | `DisplayInfo` 加 `Managed`/`Display`；`Options.NeedsDisplay` 注释；`Config` 加三项 |
| `internal/runner/runner_linux.go` | 改 | `NeedsDisplay` 分支改为 plan + acquire，新增「起不来」失败文案 |
| `internal/runner/vcredist_linux.go` | 改 | 两处 `resolveDisplay` 调用点 |
| `internal/runner/display_linux_test.go` | 改 | 删 `xvfbRunArgs` 用例，改 `TestCheckDisplayAgreesWithResolve` → `…WithPlan` |
| `internal/runner/runner.go` + `runner_windows.go` | 改 | `StopManagedDisplay()`（Windows 空实现） |
| `internal/webapi/actions.go` + `internal/svcmgr/service.go` | 改 | 进程退出路径调 `StopManagedDisplay()`（§4.3 第 1 层） |
| `internal/appconfig/config.go` + 默认配置模板 | 改 | `linux.display` / `linux.xvfb_bin` / `linux.xvfb_screen` |
| `main.go`（`applyAppConfig`） | 改 | 三个新配置项接到 `runner.Configure` |
| `internal/actions/verify_arkapi.go` | 改 | `[3] 图形显示` 一节的文案（会显示 `How`，自管时打印显示号） |
| `docs/*`、`CLAUDE.md` | 改 | 见 §5.9 |

`internal/webapi/systemapi` **无需改动** —— 它直接序列化 `DisplayInfo`，新字段自动带出。

---

## 7. 验证

### 7.1 先把发行版事实核实（落地前，10 分钟）

在 Fedora / Arch / RHEL 各跑一遍，结果回填 §1.2 与 §5.8：

```bash
command -v Xvfb xvfb-run; echo "---"
# 包名与文件清单
rpm -q --whatprovides /usr/bin/Xvfb 2>/dev/null || pacman -Qo /usr/bin/Xvfb
rpm -ql xorg-x11-server-Xvfb 2>/dev/null | grep -i xvfb
Xvfb -help 2>&1 | grep -- -displayfd        # 老版本没有 -displayfd 就要走扫号回退
# 无字体环境下会不会 fatal
Xvfb :101 -screen 0 1280x1024x24 -nolisten tcp -noreset -ac & sleep 1; \
  ls -l /tmp/.X11-unix/X101; kill %1
```

### 7.2 单测（Windows 上跑不了带 `//go:build linux` 的部分，用 `GOOS=linux go vet` 兜底）

以下均已写好（`xvfb_linux_test.go` 新增，`display_linux_test.go` 改写）：

| # | 用例 | 钉住什么 |
|---|---|---|
| 1 | `TestXvfbArgsShape` | 必须有 `-displayfd`/`-nolisten tcp`/`-noreset`/`-ac`；**不含**显示号位置参数（与 `-displayfd` 互斥）；**不含** `-auth`（带 cookie 我们自己的握手探测就连不上自己了） |
| 2 | `TestXvfbArgsForDisplay` | 回退形态显示号在第一位、不带 `-displayfd`，且 `linux.xvfb_screen` 生效 |
| 3 | `TestParseXvfbDisplayFD` | `"100\n"` → `"100"`；空/非数字 → 错误；多行只取第一行 |
| 4 | `TestXvfbRejectedDisplayFD` | 只有「不认识 `-displayfd`」才触发回退；缺字体、号被占都不算 |
| 5 | `TestXvfbFailureHintFonts` | `could not open default font` → 给出三家的字体包名 |
| 6 | `TestXvfbDisplayInUse` | 换号重试只对「号被占了」有意义 |
| 7 | `TestXvfbInstallHintCoversDistros` | apt/dnf/pacman 三家包名齐全，且**不再提** `xvfb-run` |
| 8 | `TestXvfbBinaryRejectsBadConfig` | `linux.xvfb_bin` 指错要报错，不许悄悄退回 PATH |
| 9 | `TestXvfbStateRoundTrip` / `TestXvfbStateWithoutBaseDir` / `TestAdoptXvfbRejectsDeadPID` | 认领的三道关，以及没有 BaseDir 时安全降级 |
| 10 | `TestXvfbLogTailOnlyThisRun` / `TestOpenXvfbLogTruncatesOversized` | 诊断只看本次启动的输出；日志不无限长 |
| 11 | `TestDisplayStatusStartsNothing` | `displayStatus`/`checkDisplay` 前后 `currentManagedXvfb()` 不变（自检不许起进程） |
| 12 | `TestCheckDisplayAgreesWithPlan` / `TestDisplayStatusMatchesPlan` | 自检、诊断视图与启动路径问同一个函数 |
| 13 | `TestDisplayBlockedMessageNamesXvfbNotXvfbRun` | 拿不到显示时的提示不许再指向 `xvfb-run` |
| 14 | `TestDisplayApplyTo*` | 追加在最后、返回新切片、零值是恒等变换 |

### 7.3 真机矩阵

| # | 环境 | 场景 | 期望 |
|---|---|---|---|
| 1 | **Fedora/Rocky 无头** | `asa-server setup` | `x11-display` **通过**（当前是失败）；日志说明将使用自管 Xvfb |
| 2 | 同上 | 启用 ArkApi 的实例启动 | Xvfb 起来 → `ArkApi_*.log` 出现 `API was successfully loaded` → 端口监听 |
| 3 | **Arch 无头** | 同 1、2 | 同上 |
| 4 | **Ubuntu 无头**（回归） | 同 1、2 | 与 §9.6 的 44 秒那次等价，不因删掉 `xvfb-run` 而回归 |
| 5 | **WSL2 + WSLg**（回归） | `env -u DISPLAY` 完整启动 | `/tmp/.X11-unix` 只读 ⇒ 落到第 3 条，用 `:0`，与 §9.6 的 52 秒那次一致 |
| 6 | 任意 + `prefix_mode: per-instance` | 两个 ArkApi 实例并发启动 | 共用**同一个** Xvfb 显示，两个都能加载 ArkApi；`ps` 里只有一个 Xvfb |
| 7 | 故意制造失败 | `chmod -x $(command -v Xvfb)` 或删字体 | 启动被**拒绝**并给出 `xvfb.log` 末尾与针对性提示；**不出现**「实例假装启动成功」 |
| 8 | 生命周期（正常退出） | 实例跑起来后 `systemctl restart asa-server` | Xvfb 与 ArkApi 实例**一起消失**（后者本来就挂在 PTY 上跟着走）；重启后 `ps` 里没有残留的 Xvfb，再启动实例时新起一个 |
| 8b | 生命周期（硬杀） | `kill -9 $(pidof asa-server)` | Pdeathsig 生效：Xvfb 在同一瞬间消失，`/tmp/.X11-unix/X<n>` 与 `/tmp/.X<n>-lock` 都被清掉（用 SIGTERM 而非 SIGKILL 就是为了这个） |
| 8c | 生命周期（长跑） | 一个 ArkApi 实例连续跑数小时 | Xvfb 一直在，**不会**因为某个 Go 线程退出而被误杀（`xvfbSpawnLoop` 的 LockOSThread 保证）——这条是 Pdeathsig 最容易翻车的地方，必须真机盯久一点 |
| 9 | 生命周期 | `kill -9` Xvfb，再启动一个 ArkApi 实例 | 健康检查发现死了 → 自动重开，启动成功 |
| 10 | `--check-only` | `asa-server verify-arkapi --check-only` | `[3]` 报「自管 Xvfb（未启动，将在需要时拉起）」，且**没有** Xvfb 进程被拉起 |

---

## 8. 实施顺序

**第一步（核心，可独立交付）—— ✅ 已完成**
`xvfb_linux.go` 的托管实现 + `planDisplay`/`acquire` 拆分 + 删 `xvfb-run` 分支 +
文案/配置项 + 单测。做完这一步，§1 的两个故障就都没了。

**第二步（健壮性）—— ✅ 已完成**
`{BaseDir}/xvfb.state` 的孤儿认领（§4.4）+ `xvfb.log` 的大小截断与
「只读本次启动之后的输出」。~~优雅退出时 `stop()`~~ —— 见 §4.3 的更正，这条被否掉了。

**第三步（文档与回填）—— 文档同步已完成，回填待真机**
§5.9 的文档同步 ✅ + §7.1 的发行版事实回填 ⬜ + §7.3 真机结果回填本文 ⬜。

---

## 9. 风险

| # | 风险 | 影响 | 缓解 |
|---|---|---|---|
| 1 | **老 X server 没有 `-displayfd`**（< 1.13，RHEL 7 一类）【待实测】 | Xvfb 起不来 | 回退到「扫空闲显示号 + 冲突重试」：从 `:100` 起找一个既没有 `/tmp/.X11-unix/X<n>` 也没有 `/tmp/.X<n>-lock` 的号，起失败就换下一个，最多 10 次。检测方式：`-displayfd` 那次失败的日志里有 `Unrecognized option` |
| 2 | **最小化系统缺字体导致 Xvfb fatal**【待实测】 | 显示起不来 | 识别日志模式 → `xvfbFontHint`（§5.8）。这是**新暴露**的问题，不是新引入的：`xvfb-run` 下同样会失败，只是被 `/dev/null` 吞了 |
| 3 | **常驻一个无认证 X 服务** | 同机其他本地用户可以连上它（截屏/发假输入） | ① `-nolisten tcp`，只走 unix socket；② 显示上只有 Wine 的隐形窗口，无剪贴板、无用户输入；③ 这与之前 `xvfb-run` 的差别只是「有没有 cookie」，而我们本来就不传 `XAUTHORITY`（§3.3）。**若将来要收紧**：改为带 `-auth`，同时把 `XAUTHORITY` 加进 `runtimeEnv` 与 `launchEnvAllowed` 白名单，并把探测改成读 cookie 后握手 —— 三处一起改，不能只改一处 |
| 4 | **孤儿 Xvfb 累积** | 进程/内存泄漏 | §4.3 的三层：显式停 + Pdeathsig + 认领。真机要专门验 8/8b/8c 三条 |
| 4b | **Pdeathsig 误杀**（Go 的 M 退出把 Xvfb 带走） | 正在跑的 ArkApi 实例突然没显示 | `xvfbSpawnLoop` 那个 LockOSThread 且永不返回的 goroutine 是唯一的 fork 入口。**别给它加退出条件**，也别在别处直接 `cmd.Start()` 一个带 Pdeathsig 的进程。用例 8c 盯这条 |
| 5 | **单例显示被多个 ArkApi 实例共用是否可靠**【待实测】 | `per-instance` 模式下第二个实例可能出问题 | §7.3 用例 6 专门验。若不行，退回「每 prefix 一个 Xvfb」，键与 `PrefixKeyFor` 同源（注意：`shared` 模式下本来就只允许一个 ArkApi 实例，所以这个风险只在 `per-instance` 模式存在） |
| 6 | **删掉 `xvfb-run` 造成 Debian 侧回归** | 已验证过的路径变了 | §7.3 用例 4 是专门的回归项。底层跑的是同一个 `Xvfb` 二进制，参数还更明确 |
| 7 | Xvfb 在 pressure-vessel 容器外，显示要经 `/tmp/.X11-unix` 被 bind 进容器 | 与现状相同的约束 | `x11SocketDirWritable()` 保持不变，WSLg 只读挂载那条路仍然靠第 3 条兜底 |

---

## 10. 一句话总结

`xvfb-run` 是 Debian 的一个便利脚本，我们却把它当成了「本机能不能开虚拟显示」的判据 ——
和当初把 `xvfb-run` 存在当成「显示一定可用」（§9.5）是同一类错误的两次犯法。
判据应该落在**能力**上：机器上有没有 `Xvfb`、它起不起得来、起来之后握不握得上手。
把 Xvfb 自己管起来，这三件事就都能直接测出来，而不用靠一个发行版给不给某个脚本。
