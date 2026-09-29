# 跨发行版虚拟显示与自管 Xvfb（合并文档）

> 本文由 `XVFB_DISPLAY_PLAN.md` 与 `XVFB_DISPLAY_PLAN.md` 于 2026-09-29 物理合并而成。
> Part 1 是现行结论；Part 2 提供自管 Xvfb 的机制基础与真机回填，其中 §3 的候选链顺序表已被 Part 1 取代。
> ⚠️ 本文各「改动清单 / 落地文件」写于 `RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN` 重构之前，实际路径见文末「附录 Y」。
> ⚠️ 本文存在已核实的已知缺陷，见下方「已知缺陷清单」。

## 已知缺陷清单（2026-09-29 代码审计同步）

以下缺陷为 2026-09-29 只读审计结论（基线 `faf127c`），尚未修复；级别 P0/P1/P2 沿用审计报告。

#### [P1] `Stop()` 在 `current == nil` 时提前 return，跳过只读 mount 还原，宿主 `/tmp/.X11-unix` 被永久改成可写

- **位置**：`pkg/xvfb/manager.go:262-279`（提前 return 在 267-269，还原在 274）；写入点 `pkg/xvfb/xvfb_linux.go:411`（`m.remounted.Store(true)`）与 `:221-228`（死亡后 `m.current.Store(nil)`）
- **触发条件**：WSLg 等把 `/tmp/.X11-unix` 挂成只读的机器上、以 root 运行：① 首次 `Acquire()` 成功 remount 为 rw 并起 Xvfb（`remounted=true`）；② Xvfb 中途被 OOM/kill，`ensure()` 走 `cur.stop()` → `m.current.Store(nil)`，随后重起失败 → `current` 保持 `nil`；③ 进程退出调用 `runner.StopManagedDisplay()` → `x == nil` → **直接 return**，`restoreSocketDirRO()` 永不执行。
- **后果**：宿主 mount 表被持久改成 rw，违反 `XVFB_DISPLAY_PLAN.md §4.4`「退出时还原」。在 WSL 上那是与 WSLg 系统发行版**共享**的 tmpfs；进程重启后再也回不到 ro（除非手工 `mount -o remount,ro`）。
- **修复建议**：让还原与 `current` 是否存在解耦，并在 `ensure()` 失败分支也补一次还原：

```go
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	x := m.current.Load()
	if x == nil {
		// 没有 current 不代表没改过 mount：起失败/中途死亡都会把 current 清空。
		m.restoreSocketDirRO()
		return
	}
	x.intentional.Store(true)
	m.current.Store(nil)
	m.restoreSocketDirRO()
	if x.adopted {
		return
	}
	x.stop()
}
```

#### [P1] `-ac` 且不传 `-auth`：自管 X server 的 unix socket 全局可连，无任何认证

- **位置**：`pkg/xvfb/xvfb_linux.go:191-198`（`commonArgs` 里的 `-ac`，无 `-auth`）、`:179-181` 注释、`:669-675`（root 时 `Credential=nil`）
- **触发条件**：任意部署；同机存在其他本地用户，或 WSL 下与其它发行版共享 `/tmp/.X11-unix`。尤其 `linux.umu_run_as_root: true` 时 `Xvfb` 以 **root** 身份常驻。
- **后果**：socket 位于 1777 的 `/tmp/.X11-unix`，任何人可 `connect`。未认证 X server 允许同机用户截取窗口内容、注入输入事件、读取剪贴板；X server 以 root 运行时其扩展/字体路径的历史漏洞直接是本地提权面。文档 `XVFB_DISPLAY_PLAN.md §9` 风险 3 已承认此取舍，但代码里目前**零缓解、零告警**。
- **修复建议**：文档要求三处同改，不可只改一处：① `commonArgs` 去掉 `-ac`，改 `-auth <path>`，auth 文件 0600、属主为运行时用户；② `runtimeEnv` 白名单加入 `XAUTHORITY` 指向该文件；③ `DisplayUsable` 改为读 cookie 后握手。在完成前，至少保证 `Xvfb` 不跑 root（即便 `RunAsRoot=true` 也给专用非 root uid），并在 `Acquire` 成功后 `logger.Warnf` 一条显式告警。

#### [P2] 看门狗退避写成 `restartBackoff[0]`（三次都 2s），且从不记录 Xvfb 死亡原因

- **位置**：`pkg/xvfb/xvfb_linux.go:292-300`（`for range restartBackoff { time.Sleep(restartBackoff[0]) ... }`）；`restartBackoff` 定义在 `:86`；`x.waitErr` 在 `:624` 捕获后无消费者
- **触发条件**：Xvfb 异常退出、看门狗补起失败。
- **后果**：① 退避数组 `{2s,5s,15s}` 形同虚设，实际固定 2s×3；② 文档承诺的「日志里一句『Xvfb 于某时退出，原因是…』」**根本不存在**——`pkg/xvfb` 全包不 import logger。
- **修复建议**：`for _, d := range restartBackoff` 用元素；补一条 `logger.Warnf("xvfb: Xvfb(pid=%d, display=%s) 已退出（%v），连续 %d 次补起失败：%v", ...)`。若要保持包解耦，可在 `Config` 上加 `Logf func(string, ...any)` 回调。

#### [P2] `pkg/xvfb` 全包零日志：remount、chmod 1777、restore 全部静默执行

- **位置**：`pkg/xvfb/xvfb_linux.go:340-372`（`ensureSocketDir`）、`:393-413`（`remountSocketDirRW`）、`:420-425`（`restoreSocketDirRO`，`_ = syscall.Mount(...)` 连失败都吞掉）
- **触发条件**：只要走到 remount 或 chmod 分支。
- **后果**：文档 §4.4 要求「无论开关如何，动手时必须留一条 INFO 日志」；`runner.go:548` 的注释也写着「动手会留日志，退出时还原」——**两句都不成立**。一个改宿主 mount 表、把系统目录改成 1777 的常驻服务静默改环境，是本模块最危险的问题。
- **修复建议**：remount 成功后 `logger.Infof("xvfb: %s 是只读挂载，已重新挂载为可写…退出时会还原", SocketDir)`；restore 失败 `logger.Warnf`；chmod 分支 `logger.Infof("xvfb: 把 %s 权限扶正为 1777…", SocketDir)`。

#### [P2] `getConfig()` 的零值回填漏掉 `AllowX11Remount`，「默认 true」只靠三个调用点自觉

- **位置**：`internal/runner/runner.go:628-647`（只回填 5 个字符串/枚举字段）、`:588-600`（`defaultConfig` 里 `AllowX11Remount: true` 在 597 行）
- **触发条件**：任何构造 `runner.Config` 却未显式写 `AllowX11Remount` 的调用方。
- **后果**：bool 零值是 `false`，`getConfig()` 无法区分「未设置」与「显式关掉」，`defaultConfig()` 的 `true` 在这条路径上**不生效**，WSL 上自管 Xvfb 静默退回 WSLg。当前三处调用点都带了该字段（已核对），故今天不发作；但把「默认 true」建立在调用点齐全是脆的。
- **修复建议**：语义反转——内部字段改 `DisallowX11Remount bool`（零值即允许），接线处取反；或改三态 `*bool` / `RemountPolicy` 枚举。

#### [P2] `HOME` 为空时 `xvfb.log` 落到相对路径（CWD），且 env 缺少文档承诺的 TMPDIR/LANG

- **位置**：`pkg/xvfb/xvfb_linux.go:679-685`（env 只给 HOME/PATH）、`:834-836`（`logPath`）、`pkg/sysuser/sysuser_linux.go:38-47`（`HomeDir()` 失败返回 `""`）
- **触发条件**：systemd 服务未设置 `HOME` 且非降权，或 `BaseDir` 为空。
- **后果**：`filepath.Join("", "xvfb.log")` = `"xvfb.log"`，日志落在**进程 CWD**；传给 Xvfb 的 `HOME=` 为空。文档 §4.5 写的是 HOME/PATH/TMPDIR/LANG，实现只给了两项。
- **修复建议**：`logPath` 在 `home == ""` 时退 `os.TempDir()` 或 `cfg.BaseDir`；`env` 补 `TMPDIR`/`LANG`（非空才追加）。

#### [P2] `linux.display` 握手失败时被静默丢弃，用户得不到任何提示

- **位置**：`pkg/display/display_linux.go:127-130`、`:184-189`（`namedPlan` 握手不过即 `false`）、`:173-175`（只对 `KindManaged` 缺失补 note）
- **触发条件**：用户写 `linux.display: ":0"`，但该显示需要 xauth cookie（返回 `2`）或 socket 暂不在。
- **后果**：唯一表达「请用这个显示」的一档被无声丢弃，`How` 变成「自管 Xvfb」，没有任何说明。与 `XVFB_DISPLAY_PLAN.md §11.6` 第 3 条「算出来的原因不许扔」同类。
- **修复建议**：`cfg.Display != ""` 且链里无 `KindConfigured` 时，把拒绝原因缀进头一档 `How`。

#### [P2] 孤儿认领盲信 `{BaseDir}/xvfb.state`，无属主与可执行文件归属校验

- **位置**：`pkg/xvfb/xvfb_linux.go:896-905`（`adopt`）、`:920-933`（`writeState`，`0644`）
- **触发条件**：`BaseDir` 落在非 root 可写目录/共享盘，或残留 state 指向一个「恰好叫 Xvfb」的进程。
- **后果**：`adopt` 只校验 pid 活着、`comm == "Xvfb"`、能握手，不校验 state 属主、也不校验 `/proc/<pid>/exe` 是否就是本程序的 Xvfb → 可能认领一个**非本程序、甚至他人控制的 X server**，把游戏窗口与输入交到它手上。
- **修复建议**：`adopt` 内先校验 state 文件属主（`Stat_t.Uid` ∈ {euid, 0}），再 `os.Readlink("/proc/<pid>/exe")` 与 `binaryPath(cfg)` 比对。

#### [P2] `ensure()` 复用时可能把「已死的 managed」当成可用（显示号被其它 X server 复用时）

- **位置**：`pkg/xvfb/xvfb_linux.go:221-228`
- **触发条件**：本进程的 Xvfb 死亡后，其显示号 `:N` 被机器上另一个 X server 复用，`DisplayUsable(cur.display)` 返回 `true`。
- **后果**：`ensure` 直接 `return cur`，而 `cur.cmd/exited` 指向已死进程；`Status()` 报 `Running` 但 pid 不存在，`watch` 也不会补起；`Acquire` 返回的显示其实属于别人。
- **修复建议**：复用前加「仍是本进程活的子进程」判断：`if !cur.dead() && DisplayUsable(cur.display) { return cur, nil }`。

#### [P2] `Stop()` 先清 `current` 再杀进程，存在并发 `Acquire` 起第二个 Xvfb 的窗口

- **位置**：`pkg/xvfb/manager.go:272-278`
- **触发条件**：关停路径与某次实例启动的 `Acquire` 并发。
- **后果**：`m.current.Store(nil)` 先执行时旧 Xvfb 仍在运行，并发 `ensure()` 看到 `current==nil` 会再起一个 Xvfb，与正被 `SIGTERM` 的旧进程短暂并存（`xvfb.state` 也被覆写）。设计目标是单例。
- **修复建议**：先 `x.stop()` 再 `m.current.Store(nil)`（`intentional` 标记保持先置）。


### 文档 vs 代码偏差（原审计 §1.3）

1. **落地文件路径整体失真**（见附录 A）。
2. **「动手必留 INFO 日志」未实现**：`XVFB_DISPLAY_PLAN.md:276` 与 `runner.go:548` 均承诺，`pkg/xvfb` 全包无 logger 调用。
3. **「退出时还原 mount」存在漏洞**：`Stop()` 的 `current==nil` 提前 return 使承诺在「起失败/中途死亡」路径落空（P1）。
4. **「看门狗记录 Xvfb 退出原因」未实现**：`watch` 只重试不打印，`x.waitErr` 无消费者。
5. **`env` 组成不符**：文档写 HOME/PATH/TMPDIR/LANG，代码只有 HOME/PATH。
6. **`AllowX11Remount` 默认值在 runner 层不可靠**：`appconfig` 确为 true，但 `runner.getConfig()` 不回填 bool。
7. **测试名与覆盖不符**：文档列出的 `TestPlanDisplayPrefersManagedOverEnv`、`TestAcquireFallsBackAndReportsWhy`、`TestEnsureX11SocketDirRemountOnlyOnEROFS` 等不存在（实为 `TestPlanPrefersManagedOverEnv`、`TestRemountRespectsAllowX11Remount`）。**没有任何非 opt-in 测试钉住「Stop 路径必须还原 mount」与「看门狗退避顺序」**，正是 P1-1 与退避缺陷漏网的原因。
8. **残留死注释**：`pkg/xvfb/xvfb_linux.go:952-953` 的 `// atomicBoolImpl is sync/atomic.Bool...` 指向包内不存在的符号。

---

# Part 1 — 显示解析改为「自管 Xvfb 优先」（原 `ALWAYS_MANAGED_XVFB_DISPLAY_PLAN.md`）
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
> 关联：`docs/XVFB_DISPLAY_PLAN.md`（自管 Xvfb 的落地，本文改的是它的
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

`XVFB_DISPLAY_PLAN.md` §3.2 给第 2 档排在第 3 档之上的理由是：

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
（`XVFB_DISPLAY_PLAN.md` §11 整节都是这个形状），能收敛成一条路就该收敛。

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
  （`XVFB_DISPLAY_PLAN.md` §9 风险 3），本文不动它。
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
- `UMU_PREFIX_PLAN.md` §5.2 拒绝把 `PROTON_VERB` 做成配置项时用的是
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
（`XVFB_DISPLAY_PLAN.md` §10 与 §11.6：判据要落在能力上，不是落在
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
| 4 | **给 umu 进程链重新加一层** | 那正是删掉 `xvfb-run` 时明确买到的东西（`XVFB_DISPLAY_PLAN.md` §3.3：删掉之后 PTY 的对端直接就是 `python3 umu-run`，信号与 KillTree 少一层间接） |
| 5 | 与 pressure-vessel 的嵌套 | bwrap 会在我们的 ns 之内再开一层。理论上没问题，但这是又一个未经验证的层，而本仓库每加一层都真的付过一晚上（`inheritedEnv` 注释里的 `DBUS_SESSION_BUS_ADDRESS`） |

**顺带否掉一个看起来更轻的变体**："每次启动时自己 `unshare` 一下"——那等于
per-launch Xvfb，`XVFB_DISPLAY_PLAN.md` §4.3 已经否过（`Handle.Wait`
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
| `docs/XVFB_DISPLAY_PLAN.md` | §3.2 的三级表标注"顺序已被本文调整"，并回链 | 3 |
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

---

# Part 2 — 跨发行版的虚拟显示：从 `xvfb-run` 改为自管 `Xvfb`（原 `XVFB_CROSS_DISTRO_DISPLAY_PLAN.md`）
> ⚠️ 本部分中 §3 的候选链顺序表已被 Part 1 §3 取代；其余（自管 Xvfb 机制、Xvfb 生命周期、真机回填）仍是实现依据，保留全文用于追溯。
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

> **⚠️ 先读 §11。** 第一次真机（AlmaLinux）暴露出四个缺陷，其中 §11.1 是**本方案自己
> 引入的**：`Xvfb` 的存在性改成了按能力判（对的），紧接着那条 `/tmp/.X11-unix` 的
> `o+w` 判据却仍在按权限位的形状判，而那个 0755 目录正是**上一次成功运行自己留下的**
> —— 第一次成功把后续每一次都毒死了。四个缺陷已全部修复，§11 是事实回填，
> 下文正文保持提案原貌（§3.2 那张表第 2 条的前提以 §11.1 为准）。

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

这正是 `docs/LINUX_RUNTIME_PRIVILEGE_PLAN.md` §1 记过一次的错误形状：
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

> ⚠️ **这张表的顺序已被 `docs/XVFB_DISPLAY_PLAN.md` 取代**（2026-09-01）：
> 第 1 档拆成了「`linux.display` 点名的」与「`DISPLAY` 环境变量捡来的」两档，
> 后者降到自管 Xvfb **之后**，`planDisplay` 也从返回一个答案改成返回**候选链**。
> 下文保留原貌 —— 本节末尾那句「自管的那个显示不依赖任何桌面会话」正是取代它的理由，
> 当时只把它用在了第 3 档上。

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
| `docs/UMU_PREFIX_PLAN.md` | L141 那句「`xvfb-run` 时那是它私有的一个 Xvfb」改为「自管 Xvfb 是全进程共用的一个显示」——**并注意这不改变结论**：一个 prefix 只能跑一个 ArkApi，卡点是 Wine 会话不是显示 |
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
| 6 | 任意 + `prefix_mode: per-instance` | 两个 ArkApi 实例并发启动 | 共用**同一个** Xvfb 显示，两个都能加载 ArkApi；`ps` 里只有一个 Xvfb —— ✅ **2026-09-01 在 `prefix_mode: overlay` 下验证通过**（同样是两个独立 wineserver 共用一个 Xvfb，与 per-instance 在这一点上等价） |
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
| 5 | ~~**单例显示被多个 ArkApi 实例共用是否可靠**【待实测】~~ → ✅ **2026-09-01 已验证成立**：overlay 模式下两个 ArkApi 实例、两个独立 wineserver、**一个** Xvfb，都正常在线（`docs/UMU_PREFIX_PLAN.md` §13.6.2）。下面那条「每 prefix 一个 Xvfb」的退路不需要了 | `per-instance` 模式下第二个实例可能出问题 | §7.3 用例 6 专门验。若不行，退回「每 prefix 一个 Xvfb」，键与 `PrefixKeyFor` 同源（注意：`shared` 模式下本来就只允许一个 ArkApi 实例，所以这个风险只在 `per-instance` 模式存在） |
| 6 | **删掉 `xvfb-run` 造成 Debian 侧回归** | 已验证过的路径变了 | §7.3 用例 4 是专门的回归项。底层跑的是同一个 `Xvfb` 二进制，参数还更明确 |
| 7 | Xvfb 在 pressure-vessel 容器外，显示要经 `/tmp/.X11-unix` 被 bind 进容器 | 与现状相同的约束 | `x11SocketDirWritable()` 保持不变，WSLg 只读挂载那条路仍然靠第 3 条兜底 |

---

## 10. 一句话总结

`xvfb-run` 是 Debian 的一个便利脚本，我们却把它当成了「本机能不能开虚拟显示」的判据 ——
和当初把 `xvfb-run` 存在当成「显示一定可用」（§9.5）是同一类错误的两次犯法。
判据应该落在**能力**上：机器上有没有 `Xvfb`、它起不起得来、起来之后握不握得上手。
把 Xvfb 自己管起来，这三件事就都能直接测出来，而不用靠一个发行版给不给某个脚本。

---

## 11. 落地后的第一次真机反馈（2026-08-31，AlmaLinux）——§8 三步之外的第四步

§8 的前两步交付之后，第一台真机（AlmaLinux，无头，root 运行，降权用户
`asa-umu-runtime`）**仍然**卡在 `setup`：

```
[root@niexiawei asa-server]# ./asa-server-linux setup
宿主运行时依赖不满足，setup 无法继续。请按下面的建议手动安装后重试：
  - [x11-display] no usable X display: ...
      修复：安装 Xvfb（Debian/Ubuntu: sudo apt install xvfb | ...）
[root@niexiawei asa-server]# which Xvfb
/usr/bin/Xvfb          ← 装了
```

一共暴露出四个缺陷，前两个是本方案引入/未修完的，后两个是它顺带照出来的。
四个都已修复，本节是事实回填。

### 11.1 缺陷 A（根因）：`x11SocketDirWritable` 的 `o+w` 判据把自己毒死了

现场：

```
[root@niexiawei asa-server]# ls -ld /tmp /tmp/.X11-unix
drwxrwxrwt. 11 root            root            /tmp
drwxr-xr-x.  2 asa-umu-runtime asa-umu-runtime /tmp/.X11-unix     ← 0755
[root@niexiawei asa-server]# findmnt -T /tmp
/  /dev/mapper/almalinux-root  xfs  rw,relatime,...                ← 不是只读挂载
```

`x11SocketDirWritable()` 的最后一行是 `fi.Mode().Perm()&0o002 != 0`，想用
「目录是不是 world-writable」模拟「降权之后的 Xvfb 写不写得进去」。两处错：

1. **属主/属组两条路被完全忽略。** `asa-umu-runtime` 是这个目录的**属主**、
   `rwx` 俱全、写得进去，`o+w` 却判它不行。内核判的是「属主位优先，其次属组位，
   都不沾边才看 other 位」，拿 other 一位当近似就是错的。
2. **它是自我毒化的。** 目录**不存在**时这个函数只看 `/tmp`（1777）返回 true，
   于是第一次启动成功；而非 root 的 X 服务端建不出 `1777`（那一步 chmod 会失败），
   落到 umask 022 就是 `0755`、属主正是那个降权用户 —— **第一次成功把后续每一次
   都毒死了**。目录的 mtime（8-28）比这次故障早好几天，正是那一次留下的。

这就是为什么 §1.3「故障一」在这台机器上换了个形状复活：判据从「有没有
`xvfb-run`」改成了「有没有 `Xvfb`」（对的），却在下一行留了另一个**按形状而不是
按能力**判断的条件。§10 那句总结原样适用于它自己。

**修法**（`display_linux.go` / `xvfb_linux.go`）：

- `x11SocketDirWritable()` → `(bool, string)`，判据只剩两条，都是能力：
  `access(2)` 的 `W_OK`（root 绕得过权限位、绕不过只读挂载的 EROFS，非 root 时它
  本身就是完整答案），目录不存在时看 `/tmp`。`o+w` 那一行删掉。
- 降权那一档不在判断侧猜了：`runtimeUserManaged` 蕴含 `euid == 0`，也就是说凡是
  要降权的场合**这个目录的权限我们都改得动**。新增 `ensureX11SocketDir(cfg)`，
  在真要拉起 Xvfb 之前（`ensureXvfb` 里，acquire 侧）把它按 X 的约定扶正到
  `1777`：缺了就建，存在但那个身份写不进去才 `chmod`，并留一条日志——
  `/tmp/.X11-unix` 是系统共享路径，放宽权限不该悄悄发生。非 root 时空操作。
- 判断与动作因此各归各位，§3.1 的 `planDisplay` 只读 / `acquire` 动手这条分界，
  现在也覆盖了 socket 目录。
- 新增 `statWritableBy(fi, uid, gid)`（POSIX 顺序：属主位优先，其次属组位，
  最后 other）与只读的 `runtimeChildIDs(cfg)`。

> ⚠️ **`runtimeChildIDs` 必须是只读的那一个**：`resolveRuntimeCredential` 会经
> `lookupOrCreateRuntimeUser` **`useradd` 出一个系统用户**。在权限判断这类
> 「只是问一句」的路径上调它，与「preflight 顺手起一个 X 服务」是同一类错误。
> 用户还不存在时 `runtimeChildIDs` 返回 `(uid_t)-1`，让权限判断自然落到保守分支。

### 11.2 缺陷 B：真实原因被算出来又扔掉

`checkDisplay` 是这样写的：

```go
if _, blocked := planDisplay(getConfig()); blocked == "" { return nil }
return &Problem{Name: "x11-display", Detail: <写死>, Fix: <写死>}
```

`planDisplay` 明明算出了区分度很高的 `blocked` 原文，`checkDisplay` 只把它当布尔用。
于是不管真实原因是「没装 Xvfb」「`linux.xvfb_bin` 指错」还是「socket 目录权限不对」，
屏幕上永远是同一句「请安装 Xvfb」—— 而这台机器**早就装好了**。判断得对却说不清，
等于没判断，用户唯一能做的事就是去装一个已经装了的包。

**修法**：`Detail` 改为携带 `blocked` 原文；`planDisplay` 末尾的兜底文案拆成
`xvfbUnavailableReason(xvfbErr, dirWhy)`，三种原因分开说，只有 `errNoXvfb` 才给
安装提示，`linux.xvfb_bin` 指错时原样交出错误文本（哪个路径、不存在还是不可执行）。
`xvfbUnavailableNote` 同步改成三分支的短版本。

### 11.3 缺陷 C：`x11-display` 的严重级别放错了

`checkDisplay` 是**阻断级**，而 `runLinuxPreflight` 只要有阻断项就中止 `setup` ——
在选 BaseDir、下 SteamCMD、装 ARK 本体**之前**。可是：

- `ArkAscendedServer.exe` 本身不需要显示（`display_linux.go` 文件头自己写着）；
- ArkApi 是**每实例可选**的（`instance/server.go` 的 `NeedsDisplay: arkAsaApiRunning`）；
- vc_redist 那一步**早就做了降级**（`vcredist_linux.go` 里 `blocked != ""` 只跳过
  安装、明说「不阻断 setup」）。

也就是说显示是**功能级、可延后**的依赖，preflight 却把它当成**安装级**依赖，
于是一台永远用不到 ArkApi 的机器连装都装不上。当初那句辩护——「缺 ACL 能降级到
chown，缺显示什么都不剩」——只对用 ArkApi 的人成立。

**修法**：降为 `Warning: true`（建议级，与 `posix-acl` 同级），`Detail` 里明说
「不启用 ArkApi 的实例不受影响」。把关点回到真正需要它的地方，那里本来就有：
`runner.Run` 的 `NeedsDisplay` 分支当场拒绝启动，`verify-arkapi` 的 `[3]` 明确报出，
`ensureVCRedist` 跳过安装并说明代价。测试
`TestDisplayProblemIsBlocking` → `TestDisplayProblemIsAdvisory`，断言反向。

### 11.4 缺陷 D：`runner.Configure` 是整体覆盖，而调用点在手抄字段列表

`Configure` 直接 `current.Store(&cfg)`，**替换**而非合并。`internal/actions/setup.go`
与 `internal/gui/gui.go` 各自手抄了一份字段列表，§5.7 新增的三项
（`Display`/`XvfbBin`/`XvfbScreen`）只加进了 `main.go`，那两处漏了（`umu_runtime_*`
是更早就漏的）。后果是同一条 `setup` 命令里前后两种行为：

| 时刻 | 用的是哪份配置 |
|---|---|
| `runLinuxPreflight`（`setup.go:59`） | `main.go` 的 `applyAppConfig` 灌进去的**完整**配置，`linux.display` 有效 |
| 此后（`setup.go:79` 之后的 `EnsureRuntime` → `ensureVCRedist` → `acquireDisplay`） | 被那次 `Configure` 换成的**残缺**配置，`linux.display` 已被清空 |

`checkDisplay` 的 `Fix` 让用户「用 config.yaml 的 `linux.display` 指定它」，而这个
逃生舱恰恰在它出现的那个命令里失效——自相矛盾。

**修法**：两处调用点补齐全部字段；`Configure` 的文档注释写明「替换而非合并，
新增 Config 字段时 grep `Configure(` 更新每一个调用点」。

### 11.5 改动清单（本节）

| 文件 | 改动 |
|---|---|
| `internal/runner/display_linux.go` | `x11SocketDirWritable()` 重写为 `(bool, string)`，删 `o+w`；新增 `xvfbUnavailableReason`，`xvfbUnavailableNote` 改三分支 |
| `internal/runner/xvfb_linux.go` | 新增 `x11SocketDirMode`（`os.ModeSticky\|0777`）、`ensureX11SocketDir`、`statWritableBy`；`ensureXvfb` 在 `startXvfb` 前调用前者 |
| `internal/runner/runtimeuser_linux.go` | 新增只读的 `runtimeChildIDs` 与 `noSuchID`（**不**走 `useradd`） |
| `internal/runner/preflight_linux.go` | `checkDisplay` 降为 `Warning: true`；`Detail` 携带 `blocked` 原文；注释重写 |
| `internal/runner/vcredist_linux.go` | 「走到这里说明用了 `--ignore-preflight`」的注释作废，改为「这是常规路径」 |
| `internal/runner/runner.go` | `Configure` 注释写明整体覆盖的契约 |
| `internal/actions/setup.go`、`internal/gui/gui.go` | `runner.Configure` 补齐 display/xvfb/runtime-user 全部字段 |
| `internal/runner/display_linux_test.go` | `…IsBlocking` → `…IsAdvisory`；新增 Detail 带原因、三种原因可区分、拒绝必带原因三组 |
| `internal/runner/xvfb_linux_test.go` | 新增 6 组：属主位/属组位优先级、`x11SocketDirMode` 含 sticky、`ensureX11SocketDir` 非 root 空操作、`runtimeChildIDs` 无副作用 |
| `docs/LINUX_DEPLOYMENT.md` | 依赖表标注「只有 ArkApi 才需要」；阻断级 → 建议级；解析表第 2 条的前提改写；排障表新增 0755 那一行 |

`go build ./...`（Windows）、`go vet ./...`、`CGO_ENABLED=0 GOOS=linux go vet ./...`、
`go test ./internal/runner/... ./internal/appconfig/... ./internal/actions/...` 均通过。

### 11.6 这一节的教训

§10 说「判据应该落在能力上」，而 §11.1 表明**这句话在同一个函数里就没贯彻到底**：
`Xvfb` 的存在性改成了按能力判，紧接着的 socket 目录却仍然在按权限位的形状判。
按形状判断的代价还不止判错——它**判错的方向恰好与自己造成的副作用相反**：
第一次成功运行留下的那个 0755 目录，正是后续每一次失败的原因。

三条可复用的检查项：

1. **拿权限位当权限判**：想知道「某个身份能不能写」，要么真的按 uid/gid 算
   （`statWritableBy`），要么让它自己去写；`&0o002` 这种单位判据一定漏掉属主与属组。
2. **能修就别只判**：`euid == 0` 的时候，「目录权限不对」不是一个结论，是一件待办事项。
3. **算出来的原因不许扔**：`if _, blocked := f(); blocked == ""` 这种写法要警惕 ——
   把诊断信息算出来又丢掉，等于让用户自己去猜你已经知道的答案。

---

## 12. 关掉 Xalia（`PROTON_USE_XALIA=0`）

§11 修复之后的同一台真机上，`setup` 的日志里出现了这一段：

```
正在写入 11 条 VC++ DLL override（native,builtin）
...
Proton: /opt/asa-server/basedir/umu-prefix/drive_c/windows/regedit.exe
fsync: up and running.
System.PlatformNotSupportedException: Video driver  not supported
  at Xalia.Sdl.WindowingSystem.Create () ...
  at Xalia.Ui.UiMain..ctor () ...
```

### 12.1 它不是故障

Xalia 是 GE-Proton 附带的**无障碍 / 手柄 UI 覆盖层**，是与被启动的程序**并行**的
另一个进程。它初始化 SDL 的窗口系统失败就抛这个异常然后自己退出，被启动的程序
不受影响。同一次运行的结果可以证明：

```
✔ DLL override: 11/11 条已写入 prefix 注册表（native,builtin）
✔ system32 里的 vcruntime140.dll 是微软原生版本
```

（`vcamp140.dll` / `vcomp140.dll` 在 system32 里也变成了「原生」，而它们在游戏目录里
是缺失的 —— 只可能来自微软安装器，即 §11 之后 Xvfb 真的起来了、第二步真的跑完了。）

注意报错里 `Video driver  not supported` 是**两个空格**：驱动名是空的，也就是 SDL
压根没有可用的 video driver ——「这次运行没有 DISPLAY」的直接体现，而不是
「Xvfb 起了但坏了」。`docs/ARKAPI_LINUX_VCREDIST_PLAN.md` §9 记过同一条 trace，
但那次真正失败的是 `AsaApiLoader.exe`（它需要窗口），不是 Xalia 自己。

出现的位置也符合设计：这条出自 `ensureVCRedist` 的**第一步**（写 DLL override），
而第一步**故意不带显示**跑 —— override 是承重项，必须在完全没有显示的机器上也能
写进去，给它绑上 Xvfb 等于把唯一无条件可用的那一项也变成有条件的。

### 12.2 但它值得关掉，理由不是「难看」

一台专用服务器没有屏幕、也没有人坐在屏幕前，这个覆盖层在**任何**一次启动里都
无事可做。而没有显示时它不是闲着，是**必崩**：

- `ensureVCRedist` 第一步：每次 `setup` 都在「正在写入 11 条 VC++ DLL override」
  **正下方**留一段 .NET 栈，看着像 override 失败了，其实 11/11 全写进去了；
- **普通实例启动**（最常见的那条路）：`ArkAscendedServer.exe` 的 `NeedsDisplay`
  是 false，从不给 DISPLAY，于是**每一次启动**都往 `launcher.log` 里塞同一段栈。

**一个模仿故障的诊断噪音，比它所属的那个进程更贵。** 排障的人要先花时间确认它
不是问题，才能继续找真正的问题——而这件事每次启动都要重来一遍。

### 12.3 做法

GE-Proton 的 proton 脚本默认 `PROTON_USE_XALIA=1`，但**尊重外部传入的值**：

```python
if "PROTON_USE_XALIA" not in self.env:      # GE-Proton10-34 proton, L2093
    ...
    self.env["PROTON_USE_XALIA"] = "1"
```

（L2246 处 winewayland 那条分支自己就是这么关的，属于上游认可的用法。）

于是在**三处**拼 umu/Proton 命令行的地方各加一个 `protonNoXalia` 常量：

| 文件 | 位置 | 覆盖的场景 |
|---|---|---|
| `internal/runner/runner_linux.go` | `umuCommandLine` 的 env | 所有实例启动（含 ArkApi 与安装校验，两者都走 `runner.Run`） |
| `internal/runner/umu_linux.go` | `warmPrefix` 的 `wineboot --init` | 首次建 prefix |
| `internal/runner/vcredist_linux.go` | `runInPrefix` 的 env | regedit 写 override、vc_redist 安装、`verify-arkapi` |

常量与全部理由集中在 `umu_linux.go` 的 `protonNoXalia`，三处只引用不复述。
排在 env 末尾：`launchEnvAllowed` 放行 `PROTON_*`，而 exec 取同名变量的**最后一个**,
所以外面导出的 `PROTON_USE_XALIA=1` 压不过我们这一份 —— 与 `PROTON_VERB=run` 同一个
处理方式，测试 `TestUmuCommandLine_DisablesXalia` 钉的正是这条（导出 `1` 仍须得 `0`）。

### 12.4 不做的事

- **不做成配置项。** 服务端不存在「想要无障碍覆盖层」的场景，加个开关只是把一个
  没有用户的决定推给用户。真要排障，`PROTON_USE_XALIA` 是 GE-Proton 自己的变量，
  从 systemd unit 或 shell 里导出即可（我们的值在 env 末尾，会赢；要让外部值赢
  需要先删掉这一行——排障时改代码是可以接受的代价）。
- **不借此声称显示不再必要。** ArkApi 仍然需要真显示（§11.3），关掉 Xalia 只是
  少了一个和显示无关的旁路进程。

---

## 13. `verify-arkapi` 失败时报了**别的命令**的日志

§12 落地后，真机上跑完整的 `verify-arkapi`（不带 `--check-only`）仍然看到那段
Xalia 栈。原因有两个，第二个才是需要改代码的那个。

### 13.1 现象里的自相矛盾

```
启动输出: /opt/asa-server/basedir/logs/verify-arkapi-launch.log      ← 这次写的是它
...
--- last lines of /opt/asa-server/basedir/logs/verify-launch.log --- ← 打印的却是它
Proton: /opt/.../ShooterGame/Binaries/Win64/ArkAscendedServer.exe    ← 而且是另一个 exe
08/31 14:59:50 ...                                                   ← 时间也对不上
```

三条线索互相矛盾：这条命令**明说**自己用 `AsaApiLoader.exe` 启动，输出写进
`verify-arkapi-launch.log`；而被打印出来的那份日志里跑的是 `ArkAscendedServer.exe`，
落在 `verify-launch.log`，时间戳还早了好几分钟。

### 13.2 原因：失败报告的日志路径是写死的

```go
func reportVerificationFailure(logsDir string, emit func(string)) {
	if tail := tailLines(verifyLaunchLogPath(), 20); tail != "" {   // ← 写死
```

`verifyLaunchLogPath()` 是 `VerifyServerInstallation`（`asa-server verify`）的日志。
`VerifyArkApiInstallation` 复用了这个函数，于是**它失败时打印的是另一条命令留下的、
可能是几小时前的文件**。§34 那句注释「两条命令诊断的是不同的东西，互相覆盖会让
『刚才那次到底是谁的输出』变成一个需要猜的问题」——路径分开了，报告却没跟上。

这类缺陷最难发现的地方在于：**每一行都是真的**，只是拼在一起指向了错误的结论。
用户据此以为 Xalia 让 ArkApi 起不来，而那段栈根本来自另一次、早已结束的运行。

### 13.3 修法

`reportVerificationFailure(launchLog, logsDir, emit, extraLogDirs...)`：

- `launchLog` 改为参数，由调用方传**这次**写下的那个文件；
- 新增 `extraLogDirs`，`verify-arkapi` 传 `Win64/logs/`——ArkApi 的业务日志不走控制台，
  只写 `ArkApi_*.log`（每次启动换名），而被验证的就是加载器，那才是第一手证据。

于是失败报告现在是三份：本次启动输出 → ArkApi 自己的日志 → ShooterGame.log。

### 13.4 顺带澄清：Xalia 与 ArkApi 起不来无关

§12 已经证明它对被启动的程序无害（同一次运行里 override 11/11、vc_redist 装成功）。
它在这里再次出现，只说明**那份被误报的旧日志**是在 §12 的
`PROTON_USE_XALIA=0` 之前产生的。ArkApi 到底为什么没起来，要看 §13.3 新加的那两份。

---

# 附录 Y：文件路径对照（2026-09-29）

| 文档中的路径 | 实际路径（当前代码） |
|---|---|
| `internal/runner/display_linux.go`（业务逻辑） | `pkg/display/{display.go,display_linux.go}`（`internal/runner/display_linux.go` 只剩 45 行组合根胶水） |
| `internal/runner/xvfb_linux.go`（业务逻辑） | `pkg/xvfb/{manager.go,xvfb_linux.go}`（`internal/runner/xvfb_linux.go` 只剩 47 行胶水） |
| 旧顶层包 `asaserver/` | 已整体迁入 `internal/` |
| `internal/runner/runtimeuser_linux.go` | `pkg/sysuser/sysuser_linux.go` |
| `internal/runner/umu_linux.go` | `pkg/umu/umu_linux.go` |
| `internal/runner/vcredist_linux.go` | `pkg/vcredist/` + `internal/runner/vcredist_linux.go` |

# 附录 Z：合并与同步记录（2026-09-29）

本文件由 `docs/ALWAYS_MANAGED_XVFB_DISPLAY_PLAN.md` 与 `docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` 于 2026-09-29 逐字物理合并而成（方案甲：内容真并进目标文件，历史/被取代章节降级为附录）；「已知缺陷清单」同步自 `docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md` §1（只读审计，基线 `faf127c`）；两个源文件保持原样，未作任何删减或改写。
