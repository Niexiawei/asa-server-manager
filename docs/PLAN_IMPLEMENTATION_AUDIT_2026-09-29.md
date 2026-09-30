# asa-server 计划落地审查报告（只读审计）

> 审查日期：2026-09-29  
> 审查基线：`faf127c docs: CHANGELOG 定版 0.1.0`（工作区干净）  
> 审查方式：**只读**。全程未修改、创建、删除任何业务代码或计划文档；本报告为唯一新增文件。  
> 审查范围：下列 14 份设计计划文档所对应的**实际实现代码**。
>
> 📌 **文档合并说明（2026-09-29 后续）**：本报告审查的 14 份文档已按「方案甲（物理合并）」合并为 7 份：
> - `ALWAYS_MANAGED_XVFB_DISPLAY_PLAN.md` + `XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` → **`XVFB_DISPLAY_PLAN.md`**
> - `UMU_RUNTIME_USER_PLAN.md` + `ACL_PERMISSION_HARDENING_PLAN.md` + `UMU_PYTHON_DISCOVERY_PLAN.md` → **`LINUX_RUNTIME_PRIVILEGE_PLAN.md`**
> - `UMU_PREFIX_PER_INSTANCE_PLAN.md` + `UMU_PREFIX_OVERLAY_PLAN.md` + `UMU_PREFIX_OVERLAY_TODO.md` + `UMU_PREFIX_INIT_TROUBLESHOOTING.md` → **`UMU_PREFIX_PLAN.md`**
> - `ARKAPI_PLUGIN_INSTALL_PLAN.md` + `ARKAPI_PLUGIN_DATA_PLAN.md` → **`ARKAPI_PLUGIN_PLAN.md`**
> - `ARKAPI_CACHE_PREFETCH_PLAN.md`、`ARKAPI_LINUX_VCREDIST_PLAN.md`、`ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md`：保持独立。
>
> **本报告正文保持审查当时的原文**（旧文档名仅作历史记录，文件已删除）；各模块的「已知缺陷清单」已同步进对应新文档的附录「2026-09-29 代码审计同步」。
>
> 🔎 **2026-09-29 复核（基线 `caca2db`：umuruntime 插件化与 Setup Part 2 合入之后）**
>
> 逐条对照**当前代码**重新核实了下面每一条发现，结论直接写在各条标题下方的「🔎 复核」块里。约定：
>
> - 标题被 ~~划去~~ 的：问题**不存在**——要么原判断有误，要么已在基线之后修复。复核块给出依据（代码位置 / 修复提交）。
> - 未划去的：问题**成立**。复核块给出（可能调整过的）**定级**与**以此为准的修复方案**；原文「修复建议」保留作历史，两者冲突时以复核块为准。
> - 原文行号在基线之后已漂移（`pkg/display` 迁入 `pkg/umuruntime/plugins/xdisplay`、`Host` 收编编排等），复核块里的位置按当前代码给出。
> - 本次**只出方案，不改代码**。汇总与排期见文末 §11。

---

## 0. 审查说明

### 0.1 被审查的计划文档

| #  | 文档                                     | 对应功能模块                   | 本报告章节 |
| -- | -------------------------------------- | ------------------------ | ----- |
| 1  | `ALWAYS_MANAGED_XVFB_DISPLAY_PLAN.md`  | 显示解析 / 自管 Xvfb           | §1    |
| 2  | `XVFB_CROSS_DISTRO_DISPLAY_PLAN.md`    | 跨发行版显示                   | §1    |
| 3  | `ACL_PERMISSION_HARDENING_PLAN.md`     | 共享写权限加固                  | §2    |
| 4  | `UMU_RUNTIME_USER_PLAN.md`             | 降权运行时用户                  | §2    |
| 5  | `UMU_PYTHON_DISCOVERY_PLAN.md`         | Python 探测                | §2    |
| 6  | `UMU_PREFIX_OVERLAY_PLAN.md`           | overlay prefix           | §3    |
| 7  | `UMU_PREFIX_OVERLAY_TODO.md`           | overlay prefix 待办        | §3    |
| 8  | `UMU_PREFIX_PER_INSTANCE_PLAN.md`      | per-instance prefix / 闸门 | §3    |
| 9  | `UMU_PREFIX_INIT_TROUBLESHOOTING.md`   | prefix 初始化排查（D0~D6）      | §4    |
| 10 | `ARKAPI_CACHE_PREFETCH_PLAN.md`        | offsets cache 预取         | §5    |
| 11 | `ARKAPI_LINUX_VCREDIST_PLAN.md`        | VC++ 运行时补装               | §5    |
| 12 | `ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md` | 日志转抄 / 游戏 PID 识别         | §6    |
| 13 | `ARKAPI_PLUGIN_INSTALL_PLAN.md`        | 插件按实例安装                  | §7    |
| 14 | `ARKAPI_PLUGIN_DATA_PLAN.md`           | 插件数据隔离                   | §7    |

### 0.2 一个必须先说明的全局偏差

代码已经过 `RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN` 重构，逻辑从 `internal/runner/` 下沉到 `pkg/`。**14 份计划文档中的「落地文件清单」几乎全部失效**（详见附录 A）。因此本次审查一律**以实际代码为准**，文档仅用于还原设计意图。所有行号均来自当前工作区源码，未做臆测。

重构后的真实落点：

| 原文档路径                                           | 实际路径                                                          |
| ----------------------------------------------- | ------------------------------------------------------------- |
| `internal/runner/display_linux.go`（业务）          | `pkg/display/{display.go,display_linux.go}`                   |
| `internal/runner/xvfb_linux.go`（业务）             | `pkg/xvfb/{manager.go,xvfb_linux.go}`                         |
| `internal/runner/overlay_linux.go`              | `pkg/wineprefix/{wineprefix.go,wineprefix_linux.go}`          |
| `internal/runner/vcredist.go`                   | `pkg/vcredist/{vcredist.go,inspect.go,install_linux.go}`      |
| `internal/runner/runtimeuser_linux.go`（判定）      | `pkg/sysuser/{sysuser.go,sysuser_linux.go}`                   |
| `internal/instance/arkapilog.go`、`gameproc*.go` | `pkg/tail/waitnewest.go`、`pkg/iox/relay.go`、`pkg/procmatch/*` |
| `asaserver/`（旧顶层包）                              | `internal/`（已整体迁入）                                            |

> `internal/runner/{display_linux.go, xvfb_linux.go, python_linux.go}` 现在只剩 45~49 行的**组合根胶水**，不含业务逻辑。

---

## 1. 显示解析与自管 Xvfb

### 1.1 功能概述

`pkg/display`（只读判断 `Plan` + 动手 `Acquire`）与 `pkg/xvfb`（`Manager` 单例：拉起 / 看门狗 / 孤儿认领 / socket 目录 remount）实现四级候选链：**点名的 `linux.display` > 自管 Xvfb > 环境变量 `DISPLAY` > 扫 `/tmp/.X11-unix`**，`internal/runner` 只做胶水。设计承诺的不变量是：链非空 ⇔ 拿得到显示、判断只读动作只在 acquire、退出时还原 mount、动手必留 INFO 日志。经核实，前三档顺序、单例、Pdeathsig 专用 fork 线程等核心设计**落地正确**；缺陷集中在资源回收、日志与布尔默认值。

### 1.2 发现

#### [P1] `Stop()` 在 `current == nil` 时提前 return，跳过只读 mount 还原，宿主 `/tmp/.X11-unix` 被永久改成可写

> 🔎 **复核：成立，维持 P1。** `Stop` 现位于 `pkg/xvfb/xvfb_linux.go:262-279`（不在 `manager.go`），`x == nil` 时仍直接 `return`；`ensure()` 在 `:221-228` 收尸后清空 `current`、在 `:245-251` 起失败时也不还原，两条路径都会让 `remounted=true` 残留到进程退出。
>
> **修复方案**
> 1. `Stop()`：`x == nil` 分支先 `m.restoreSocketDirRO()` 再返回（还原与是否持有 Xvfb 解耦，`restoreSocketDirRO` 本身已用 CAS 保证幂等）。
> 2. `ensure()`：`m.start()` 失败且 `m.current == nil` 时立即 `m.restoreSocketDirRO()`——下一次 `Acquire` 需要时会再 remount（成本只是一次 `mount(2)`），不必让 rw 状态挂一整个进程生命周期。
> 3. 被 SIGKILL 时没有任何代码能执行还原：在 `xvfb.state` 里加 `remounted_socket_dir: true`，下一轮进程启动 `ensure()` 认领/起新 Xvfb 之前若读到该标记且当前不需要 remount（如 `AllowX11Remount=false` 或已不走自管），执行一次还原并清标记。属于 best-effort，写进注释。
> 4. 测试：把 `syscall.Mount`/`syscall.Access` 收成包级函数变量（仅测试替换），在 `xvfb_linux_test.go` 加非 opt-in 用例：`remounted=true、current=nil` 时 `Stop()` 必须调用一次 `MS_RDONLY` 的 remount；`start` 失败路径同样断言还原。这正是原文 §1.3 第 7 条指出的「没有测试钉住」。

- **位置**：`pkg/xvfb/manager.go:262-279`（提前 return 在 267-269，还原在 274）；写入点 `pkg/xvfb/xvfb_linux.go:411`（`m.remounted.Store(true)`）与 `:221-228`（死亡后 `m.current.Store(nil)`）
- **触发条件**：WSLg 等把 `/tmp/.X11-unix` 挂成只读的机器上、以 root 运行：① 首次 `Acquire()` 成功 remount 为 rw 并起 Xvfb（`remounted=true`）；② Xvfb 中途被 OOM/kill，`ensure()` 走 `cur.stop()` → `m.current.Store(nil)`，随后重起失败 → `current` 保持 `nil`；③ 进程退出调用 `runner.StopManagedDisplay()` → `x == nil` → **直接 return**，`restoreSocketDirRO()` 永不执行。
- **后果**：宿主 mount 表被持久改成 rw，违反 `ALWAYS_MANAGED_XVFB_DISPLAY_PLAN.md §4.4`「退出时还原」。在 WSL 上那是与 WSLg 系统发行版**共享**的 tmpfs；进程重启后再也回不到 ro（除非手工 `mount -o remount,ro`）。
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

> 🔎 **复核：成立，下调为 P2。** `-ac`、无 `-auth` 属实（`pkg/xvfb/xvfb_linux.go:191-198`），但「零缓解」不准确：`spawn()`（`:586-608`）以运行时用户的 `Credential` 起 Xvfb，降权模式下它**不是 root**，且 `-nolisten tcp` 只留本机 unix socket。只有 `umu_run_as_root: true` 或 asa-server 本身非 root 时，Xvfb 才跟随进程身份（前者即 root）。暴露面是「本机其他账号可连接 X、向 Wine 控制台窗口注入输入」，不是远程面。
>
> **修复方案**（分两步，第二步才真正关掉无认证）
> 1. 立即可做：`spawn()` 取到的 `cred == nil && os.Geteuid() == 0` 时，经注入的日志回调（见下一条的 `Config.Logf`）打一条 WARN：「自管 Xvfb 以 root 运行且未启用认证（linux.umu_run_as_root=true）」；preflight 在同条件下给一条建议项。
> 2. 启用 MIT-MAGIC-COOKIE-1：
>    - `pkg/xvfb` 每次 `start` 生成 16 字节随机 cookie，写入 `<运行时用户 HOME>/.asa-xvfb.auth`（0600、属主运行时用户；用 Xauthority 二进制格式，family=FamilyLocal/地址=hostname/显示号=N）。放在运行时用户 HOME 下是**有意的**：pressure-vessel 会 bind 这个目录，不会重蹈 `XAUTHORITY=/run/user/0/...` 让容器起不来的覆辙（`xdisplay_linux.go` 注释所说的那次）。
>    - `commonArgs` 去掉 `-ac`，加 `-auth <file>`。
>    - `xvfb` 新增 `DisplayUsableWithCookie(display string, cookie []byte) bool`：现有握手代码的 connection setup 请求里填 `authorization-protocol-name="MIT-MAGIC-COOKIE-1"` 与 data 即可；`waitReady`、`ensure` 的健康检查、`adopt` 改用它（cookie 从 auth 文件读）。
>    - `xdisplay` 的 `KindManaged` 候选在 `Target.Apply` 时追加 `XAUTHORITY=<file>`；其余三档（点名 / 环境变量 / 扫描）保持「只认无需 cookie 的显示」不变。
>    - 测试：握手请求编码的纯单测（Windows 上也能跑，放无 tag 文件）；真 Xvfb 的 opt-in 集成测试断言「无 cookie 连不上、有 cookie 连得上」。

- **位置**：`pkg/xvfb/xvfb_linux.go:191-198`（`commonArgs` 里的 `-ac`，无 `-auth`）、`:179-181` 注释、`:669-675`（root 时 `Credential=nil`）
- **触发条件**：任意部署；同机存在其他本地用户，或 WSL 下与其它发行版共享 `/tmp/.X11-unix`。尤其 `linux.umu_run_as_root: true` 时 `Xvfb` 以 **root** 身份常驻。
- **后果**：socket 位于 1777 的 `/tmp/.X11-unix`，任何人可 `connect`。未认证 X server 允许同机用户截取窗口内容、注入输入事件、读取剪贴板；X server 以 root 运行时其扩展/字体路径的历史漏洞直接是本地提权面。文档 `XVFB_CROSS_DISTRO_DISPLAY_PLAN.md §9` 风险 3 已承认此取舍，但代码里目前**零缓解、零告警**。
- **修复建议**：文档要求三处同改，不可只改一处：① `commonArgs` 去掉 `-ac`，改 `-auth <path>`，auth 文件 0600、属主为运行时用户；② `runtimeEnv` 白名单加入 `XAUTHORITY` 指向该文件；③ `DisplayUsable` 改为读 cookie 后握手。在完成前，至少保证 `Xvfb` 不跑 root（即便 `RunAsRoot=true` 也给专用非 root uid），并在 `Acquire` 成功后 `logger.Warnf` 一条显式告警。

#### [P2] 看门狗退避写成 `restartBackoff[0]`（三次都 2s），且从不记录 Xvfb 死亡原因

> 🔎 **复核：成立，维持 P2。** `pkg/xvfb/xvfb_linux.go:292-293` 仍是 `for range restartBackoff { time.Sleep(restartBackoff[0]) }`；`x.waitErr` 唯一的消费者 `unusableReason()`（`:730`）本身也无人调用，是死代码。
>
> **修复方案**
> 1. `for _, d := range restartBackoff { sleep(d); ... }`，其中 `sleep` 是包级变量（默认 `time.Sleep`），测试替换后断言调用序列为 `2s,5s,15s`。
> 2. `xvfb.Config` 新增 `Logf func(format string, args ...any)`（nil 时静默）。**不直接 import `pkg/logger`**：CLAUDE.md 约定 `pkg/xvfb` 除 `pkg/problem` 外零 pkg 依赖；由唯一持有 Manager 的 `xdisplay.Resolver` 在 `Reconfigure` 时注入 `logger.Warnf/Infof` 的包装。
> 3. `watch()`：`waitExit()` 返回且非计划内时，先 `Logf("xvfb: Xvfb(pid=%d, display=%s) 已退出：%s", x.pid, x.display, x.unusableReason())`（顺带让 `unusableReason` 不再是死代码）；三次补起都失败后再记一条带最后一次错误原文的 WARN。

- **位置**：`pkg/xvfb/xvfb_linux.go:292-300`（`for range restartBackoff { time.Sleep(restartBackoff[0]) ... }`）；`restartBackoff` 定义在 `:86`；`x.waitErr` 在 `:624` 捕获后无消费者
- **触发条件**：Xvfb 异常退出、看门狗补起失败。
- **后果**：① 退避数组 `{2s,5s,15s}` 形同虚设，实际固定 2s×3；② 文档承诺的「日志里一句『Xvfb 于某时退出，原因是…』」**根本不存在**——`pkg/xvfb` 全包不 import logger。
- **修复建议**：`for _, d := range restartBackoff` 用元素；补一条 `logger.Warnf("xvfb: Xvfb(pid=%d, display=%s) 已退出（%v），连续 %d 次补起失败：%v", ...)`。若要保持包解耦，可在 `Config` 上加 `Logf func(string, ...any)` 回调。

#### [P2] `pkg/xvfb` 全包零日志：remount、chmod 1777、restore 全部静默执行

> 🔎 **复核：成立，维持 P2。** `pkg/xvfb` 至今没有任何日志调用；`xdisplay` 只在 `Plan` 的 `How` 里预告「需先重新挂载」（`xdisplay_linux.go:152-157`），动手那一刻没有记录。
>
> **修复方案**（与上一条共用 `Config.Logf`）
> - `remountSocketDirRW` 成功后：`Logf("xvfb: %s 是只读挂载，已重新挂载为可写（AllowX11Remount=true）；退出时会还原", SocketDir)`。
> - `restoreSocketDirRO`：成功 `Logf("xvfb: 已把 %s 还原为只读")`；失败不再 `_ =` 吞掉，`Logf("xvfb: 还原 %s 为只读失败：%v；可手动执行 mount -o remount,ro %s")`。
> - `ensureSocketDir` 的 `Mkdir`/`Chmod` 两个动手分支各记一条 INFO（原权限 → 1777）。
> - 同步修正 `internal/runner` 与 `xdisplay` 里「动手会留日志」的注释，使之与实现一致。

- **位置**：`pkg/xvfb/xvfb_linux.go:340-372`（`ensureSocketDir`）、`:393-413`（`remountSocketDirRW`）、`:420-425`（`restoreSocketDirRO`，`_ = syscall.Mount(...)` 连失败都吞掉）
- **触发条件**：只要走到 remount 或 chmod 分支。
- **后果**：文档 §4.4 要求「无论开关如何，动手时必须留一条 INFO 日志」；`runner.go:548` 的注释也写着「动手会留日志，退出时还原」——**两句都不成立**。一个改宿主 mount 表、把系统目录改成 1777 的常驻服务静默改环境，是本模块最危险的问题。
- **修复建议**：remount 成功后 `logger.Infof("xvfb: %s 是只读挂载，已重新挂载为可写…退出时会还原", SocketDir)`；restore 失败 `logger.Warnf`；chmod 分支 `logger.Infof("xvfb: 把 %s 权限扶正为 1777…", SocketDir)`。

#### [P2] `getConfig()` 的零值回填漏掉 `AllowX11Remount`，「默认 true」只靠三个调用点自觉

> 🔎 **复核：成立，下调为 P3。** `internal/runner/runner.go:609-628` 仍不回填任何 bool；但 `runner.Configure` 现在**只有一个调用点** `internal/bootstrap/bootstrap.go:35`（字段映射收敛后的唯一应用点），且它显式映射了该字段，今天不会发作。同样的性质也适用于 `AutoDownload`、`SteamRTPrefetch`、`InstallVCRedist` 三个默认 true 的 bool——不是 `AllowX11Remount` 独有。
>
> **修复方案**（不做语义反转，那会把四个字段的配置面一起改掉）
> - 在 `internal/bootstrap` 加一条表驱动单测：用 `appconfig` 的默认配置走一遍 `Apply`，再用 `reflect` 遍历 `runner.Config` 的所有 bool 字段，断言与 `runner.defaultConfig()` 的同名字段相等。新增 bool 字段若忘了在 `Apply` 里映射，测试直接失败。
> - `getConfig()` 注释写明「bool 不回填，零值即 false；默认值只由 bootstrap.Apply 保证」。

- **位置**：`internal/runner/runner.go:628-647`（只回填 5 个字符串/枚举字段）、`:588-600`（`defaultConfig` 里 `AllowX11Remount: true` 在 597 行）
- **触发条件**：任何构造 `runner.Config` 却未显式写 `AllowX11Remount` 的调用方。
- **后果**：bool 零值是 `false`，`getConfig()` 无法区分「未设置」与「显式关掉」，`defaultConfig()` 的 `true` 在这条路径上**不生效**，WSL 上自管 Xvfb 静默退回 WSLg。当前三处调用点都带了该字段（已核对），故今天不发作；但把「默认 true」建立在调用点齐全是脆的。
- **修复建议**：语义反转——内部字段改 `DisallowX11Remount bool`（零值即允许），接线处取反；或改三态 `*bool` / `RemountPolicy` 枚举。

#### [P2] `HOME` 为空时 `xvfb.log` 落到相对路径（CWD），且 env 缺少文档承诺的 TMPDIR/LANG

> 🔎 **复核：成立，下调为 P3。** 非降权时 `HomeDir()` 取 `os.UserHomeDir()`（`pkg/sysuser/sysuser_linux.go:38-42`），只有 `$HOME` 未设置才为空；systemd 服务的 unit 已注入 `HOME`（`svcmgr/service_linux.go`），实际只影响手工 `env -i` 之类的启动。`logPath`/`env` 的问题本身属实（`xvfb_linux.go:679-685, 834-836`）。
>
> **修复方案**
> - 新增 `func (c Config) logDir() string`：`homeDir()` 非空用它；否则用 `filepath.Dir(c.StatePath)`（即 BaseDir）；再否则 `os.TempDir()`。`logPath` 与 `env` 的 `HOME=` 都用这个结果，杜绝相对路径与空 `HOME=`。
> - `env` 补 `TMPDIR`（宿主非空才传）；`LANG` 对 Xvfb 无意义，不加，改为修正文档里的承诺。

- **位置**：`pkg/xvfb/xvfb_linux.go:679-685`（env 只给 HOME/PATH）、`:834-836`（`logPath`）、`pkg/sysuser/sysuser_linux.go:38-47`（`HomeDir()` 失败返回 `""`）
- **触发条件**：systemd 服务未设置 `HOME` 且非降权，或 `BaseDir` 为空。
- **后果**：`filepath.Join("", "xvfb.log")` = `"xvfb.log"`，日志落在**进程 CWD**；传给 Xvfb 的 `HOME=` 为空。文档 §4.5 写的是 HOME/PATH/TMPDIR/LANG，实现只给了两项。
- **修复建议**：`logPath` 在 `home == ""` 时退 `os.TempDir()` 或 `cfg.BaseDir`；`env` 补 `TMPDIR`/`LANG`（非空才追加）。

#### [P2] `linux.display` 握手失败时被静默丢弃，用户得不到任何提示

> 🔎 **复核：成立，下调为 P3（可观测性）。** 代码已迁至 `pkg/umuruntime/plugins/xdisplay/xdisplay_linux.go`：`namedPlan`（`:199-206`）握手不过即返回 `false`，`Plan()` 只对「自管档缺席」补说明（`:191-193`）。
>
> **修复方案**
> - `namedPlan` 改为返回 `(Plan, reason string, ok bool)`，`reason` 区分「socket 不存在」与「握手被拒（多半需要 xauth cookie，而本程序刻意不传 XAUTHORITY）」。
> - `Plan()`：`cfg.Display != "" && !containsKind(plans, KindConfigured)` 时，把「配置的 linux.display=%s 不可用：%s，已改用下一档」缀进 `plans[0].How`；链为空时并入返回的原因。
> - `Acquire` 路径在同条件下 `logger.Warnf` 一次（按 display 值去重，避免每次启动刷屏）。
> - `xdisplay_linux_test.go` 加用例：点名一个不存在的 socket，断言 `How` 含拒绝原因。

- **位置**：`pkg/display/display_linux.go:127-130`、`:184-189`（`namedPlan` 握手不过即 `false`）、`:173-175`（只对 `KindManaged` 缺失补 note）
- **触发条件**：用户写 `linux.display: ":0"`，但该显示需要 xauth cookie（返回 `2`）或 socket 暂不在。
- **后果**：唯一表达「请用这个显示」的一档被无声丢弃，`How` 变成「自管 Xvfb」，没有任何说明。与 `XVFB_CROSS_DISTRO_DISPLAY_PLAN.md §11.6` 第 3 条「算出来的原因不许扔」同类。
- **修复建议**：`cfg.Display != ""` 且链里无 `KindConfigured` 时，把拒绝原因缀进头一档 `How`。

#### [P2] 孤儿认领盲信 `{BaseDir}/xvfb.state`，无属主与可执行文件归属校验

> 🔎 **复核：成立，下调为 P3。** `adopt`（`xvfb_linux.go:896-905`）确实只看 pid 存活、`comm=="Xvfb"`、能握手；`writeState` 以 0644 写（`:932`）。BaseDir 通常 root 所有，需要 BaseDir 被放在他人可写的位置才可被利用。
>
> **修复方案**
> - `writeState` 改 0600。
> - `adopt`：①`os.Stat(StatePath)` 的 `Uid` 必须是 0 或 `os.Geteuid()`，且 `Mode()&0o022 == 0`，否则拒绝认领并删除该 state；②`os.Readlink("/proc/<pid>/exe")` 去掉 ` (deleted)` 后缀、双方 `EvalSymlinks`，必须等于 `binaryPath(cfg)`；③若实施了上面的 cookie 方案，握手改用 cookie（别人的 X server 不会接受我们的 cookie，这一条本身就是最强的归属证明）。

- **位置**：`pkg/xvfb/xvfb_linux.go:896-905`（`adopt`）、`:920-933`（`writeState`，`0644`）
- **触发条件**：`BaseDir` 落在非 root 可写目录/共享盘，或残留 state 指向一个「恰好叫 Xvfb」的进程。
- **后果**：`adopt` 只校验 pid 活着、`comm == "Xvfb"`、能握手，不校验 state 属主、也不校验 `/proc/<pid>/exe` 是否就是本程序的 Xvfb → 可能认领一个**非本程序、甚至他人控制的 X server**，把游戏窗口与输入交到它手上。
- **修复建议**：`adopt` 内先校验 state 文件属主（`Stat_t.Uid` ∈ {euid, 0}），再 `os.Readlink("/proc/<pid>/exe")` 与 `binaryPath(cfg)` 比对。

#### [P2] `ensure()` 复用时可能把「已死的 managed」当成可用（显示号被其它 X server 复用时）

> 🔎 **复核：成立，下调为 P3**（需要显示号恰好在死亡后被别的 X server 占用）。`xvfb_linux.go:221-224` 只看 `DisplayUsable`。
>
> **修复方案**：复用条件改为「进程仍是那一个 **且** 能握手」：
> ```go
> alive := !cur.dead() && (cur.exited != nil || processIsXvfb(cur.pid)) // 认领来的没有 exited 通道
> if alive && DisplayUsable(cur.display) { return cur, nil }
> ```
> 若实施 cookie 方案，握手本身就能排除别人的 X server，此条随之加固。

- **位置**：`pkg/xvfb/xvfb_linux.go:221-228`
- **触发条件**：本进程的 Xvfb 死亡后，其显示号 `:N` 被机器上另一个 X server 复用，`DisplayUsable(cur.display)` 返回 `true`。
- **后果**：`ensure` 直接 `return cur`，而 `cur.cmd/exited` 指向已死进程；`Status()` 报 `Running` 但 pid 不存在，`watch` 也不会补起；`Acquire` 返回的显示其实属于别人。
- **修复建议**：复用前加「仍是本进程活的子进程」判断：`if !cur.dead() && DisplayUsable(cur.display) { return cur, nil }`。

#### ~~[P2] `Stop()` 先清 `current` 再杀进程，存在并发 `Acquire` 起第二个 Xvfb 的窗口~~

> 🔎 **复核：不成立。** `Stop()`（`xvfb_linux.go:262-264`）与 `ensure()`（`:214-216`）都在函数入口 `m.mu.Lock()` 并 `defer Unlock()`，`Stop` 持锁直到 `x.stop()` 返回（旧进程已退出或被 Kill）。并发的 `Acquire → ensure` 会阻塞在 `m.mu` 上，拿到锁时旧 Xvfb 已经不在，不存在两个 Xvfb 并存的窗口。不持锁读 `current` 的只有 `Status()`（只读）与 `watch()`（见到 `intentional=true` 立即返回），都不会起进程。无需修改。

- **位置**：`pkg/xvfb/manager.go:272-278`
- **触发条件**：关停路径与某次实例启动的 `Acquire` 并发。
- **后果**：`m.current.Store(nil)` 先执行时旧 Xvfb 仍在运行，并发 `ensure()` 看到 `current==nil` 会再起一个 Xvfb，与正被 `SIGTERM` 的旧进程短暂并存（`xvfb.state` 也被覆写）。设计目标是单例。
- **修复建议**：先 `x.stop()` 再 `m.current.Store(nil)`（`intentional` 标记保持先置）。

### 1.3 文档 vs 代码偏差

1. **落地文件路径整体失真**（见附录 A）。
2. **「动手必留 INFO 日志」未实现**：`ALWAYS_MANAGED_XVFB_DISPLAY_PLAN.md:276` 与 `runner.go:548` 均承诺，`pkg/xvfb` 全包无 logger 调用。
3. **「退出时还原 mount」存在漏洞**：`Stop()` 的 `current==nil` 提前 return 使承诺在「起失败/中途死亡」路径落空（P1）。
4. **「看门狗记录 Xvfb 退出原因」未实现**：`watch` 只重试不打印，`x.waitErr` 无消费者。
5. **`env` 组成不符**：文档写 HOME/PATH/TMPDIR/LANG，代码只有 HOME/PATH。
6. **`AllowX11Remount` 默认值在 runner 层不可靠**：`appconfig` 确为 true，但 `runner.getConfig()` 不回填 bool。
7. **测试名与覆盖不符**：文档列出的 `TestPlanDisplayPrefersManagedOverEnv`、`TestAcquireFallsBackAndReportsWhy`、`TestEnsureX11SocketDirRemountOnlyOnEROFS` 等不存在（实为 `TestPlanPrefersManagedOverEnv`、`TestRemountRespectsAllowX11Remount`）。**没有任何非 opt-in 测试钉住「Stop 路径必须还原 mount」与「看门狗退避顺序」**，正是 P1-1 与退避缺陷漏网的原因。
8. **残留死注释**：`pkg/xvfb/xvfb_linux.go:952-953` 的 `// atomicBoolImpl is sync/atomic.Bool...` 指向包内不存在的符号。

> 🔎 **复核（§1.3）**：第 2、3、4、6、7 条随上面各条修复一并解决；第 1 条路径已由 `CLAUDE.md` 与 `XVFB_DISPLAY_PLAN.md` 附录的路径映射更正（`pkg/display` 现为 `pkg/umuruntime/plugins/xdisplay`）；第 8 条仍在（`:953`），修复时删除该注释。第 5 条按 §1.2「HOME 为空」一条修正文档而非补 `LANG`。

---

## 2. 降权运行时用户 / 共享写权限 / Python 探测

### 2.1 功能概述

`pkg/sysuser` 创建/复用专用非 root 用户并以 `SysProcAttr.Credential` 降权子进程；`pkg/shareacl` 实现「组 + setgid + 默认 ACL」共享写模型（无 ACL 时退回整棵 chown）；`internal/runner` 把二者接到 umu 启动链路与启动自检；`pkg/pyfinder` 做多版本 Python 探测。设计意图：asa-server 自身仍 root，仅游戏进程树降权，且带外以 root 新建的文件也能被降权用户写入。**整体链路成立，但启动自检的覆盖范围与 reconcile 的修复范围不一致，存在一个必然导致服务永久起不来的死锁缺陷。**

### 2.2 发现

#### [P0] 启动自检检查镜像目录、reconcile 却不修 → 升级后 asa-server 永久拒绝启动

> 🔎 **复核：成立，维持 P0。** 现状完全一致：`reconcileRuntimeOwnership` 用 `rwSubtrees(cfg, false)`（`internal/runner/runtimeuser_linux.go:108`），`verifyRuntimeAccess` 用 `rwSubtrees(cfg, true)`（`:222`，含全部 `server-files-tmp-*`）；门禁 `enforceRuntimeUserGate`（`main.go:309-333`）在 `api`、默认动作与**服务模式**（`main.go:205`）都会 `os.Exit(78)`。`sampleOwnerMismatch` 用 `d.Info()`（等价 Lstat），镜像根目录或第一条 root 属主的链接就会命中。`perms fix` 只处理 `SharedTrees()`（`internal/actions/perms.go:101`），救不了镜像；`Fix` 文案「重启 asa-server 会自动 chown 修复」（`pkg/sysuser/sysuser_linux.go:349`）是错的，而且把本程序的操作写进了 `pkg/`。
>
> **修复方案**（不变量：**启动门禁能报的 ⊆ 同一次启动能修的**）
> 1. **门禁不再判镜像**：`verifyRuntimeAccess` 拆成两个调用形态：
>    - 启动门禁 `VerifyRuntimeAccess()` → `OwnershipDirs: rwSubtrees(cfg, false)`，与 reconcile 同一集合；
>    - 实例启动 `VerifyRuntimeAccessForLaunch(mirrorDir string)` → `rwSubtrees(cfg, false)` 加**本实例**的 `mirrorDir`。`ChownMirrorForRuntime(mirrorDir)` 在 `internal/instance/server.go:306` 先于该检查（`:521`）执行，所以这里报出来的一定是 chown 之后仍不对（SELinux / 只读挂载 / NFS root_squash），阻断是正确的。
> 2. **其余实例镜像改为建议项**：Preflight（`preflight_linux.go`）对 `server-files-tmp-*` 做同样的采样，命中时产出 `Warning: true` 的 `umu-runtime-mirror-drift`，经 `/api/system/preflight` 展示，**不进门禁**。实例下次启动时会被 `ChownMirrorForRuntime` 自动修好，所以它本来就只是提示。
> 3. **修复入口真的能修**：`runner` 新增 `FixRuntimeOwnership(ctx) error` = `ensureRuntimeUser` + 对 `rwSubtrees(cfg, true)` 全量 `ChownTree`；`asa-server perms fix` 在处理 `SharedTrees()` 之后调用它，并打印每棵树的耗时。`perms fix` 是 CLI 子命令，不经过 `enforceRuntimeUserGate`，服务起不来时也能跑。
> 4. **文案回到调用方**：`sysuser.AccessCheck` 新增 `DriftFix string`，`Problems` 用它填 `umu-runtime-owner-drift` 的 `Fix`（为空时只写机制性描述「chown 给运行时用户后重试」）；`internal/runner` 传「运行 `asa-server perms fix`；仍不行多半是 SELinux / 只读挂载 / NFS root_squash」。
> 5. **回归测试**（WSL，root）：BaseDir 下建一个 root 属主的 `server-files-tmp-x` → `EnsureRuntimeUser()` 后 `VerifyRuntimeAccess()` 不得返回阻断项；再加一条不变量测试：门禁返回的每个 `Problem.Name` 都属于「reconcile 可修」集合。

- **位置**：`internal/runner/runtimeuser_linux.go:108`（reconcile 用 `false`）与 `:222`（verify 用 `true`）；报错点 `pkg/sysuser/sysuser_linux.go:341-353`；错误文案 `:349`；门禁 `main.go:329-343`
- **触发条件**：
  - `EnsureRuntimeUser`（`main.go:323`）先跑 `reconcileRuntimeOwnership`，chown 范围是 `rwSubtrees(cfg, false)`，**明确排除 `server-files-tmp-*` 镜像目录**（`runtimeuser_linux.go:103-110`）。
  - 紧接着 `VerifyRuntimeAccess`（`main.go:329`）的 `OwnershipDirs` 是 `rwSubtrees(cfg, true)`，**包含所有已存在的 `server-files-tmp-*`**（`:188-192, 219-227`）。
  - 只要 BaseDir 下存在属主非运行时用户的镜像目录（从降权功能之前的版本升级、uid 漂移、被管理员 `chown -R root`、或上次 `ChownMirrorForRuntime` 失败后崩溃），`sampleOwnerMismatch`（`sysuser_linux.go:412-435`）第一次 `WalkDir` 就在镜像根命中 `St.Uid == 0 != 运行时 uid`，返回 `umu-runtime-owner-drift`。
  - 该 Problem **未置 `Warning`**，属阻断项 → `enforceRuntimeUserGate` `os.Exit(78)`。
- **后果**：**死锁**。修镜像的唯一路径是实例启动时的 `runner.ChownMirrorForRuntime(mirrorDir)`（`internal/instance/server.go:306`），而服务起不来就永远到不了那一步；`asa-server perms fix` 只遍历 `SharedTrees()`＝`server-files`＋`instances`（`sharedaccess_linux.go:50-55, 59-71`），**救不了镜像**。故障提示本身还是错的：`Fix` 写着「重启 asa-server 会自动 chown 修复」（`sysuser_linux.go:349`），但重启恰恰是触发阻断的那一步。文档 §8/§9 宣称「四项任务全部完成」时漏掉了这一点。
- **修复建议**：让「verify 能报的」⊆「同一次启动 reconcile 能修的」。推荐最小改动——把启动阻断项收敛到 reconcile 覆盖集：


```go
// internal/runner/runtimeuser_linux.go: verifyRuntimeAccess
// 只把「本次启动 reconcileRuntimeOwnership 能修好」的目录列为阻断项；
// 镜像目录由实例启动时的 ChownMirrorForRuntime 负责，不能在启动门禁里判死。
OwnershipDirs: rwSubtrees(cfg, false),
ProbeDir:      wineprefixMgrFor(cfg).Dir(""),
```

同时把 `sysuser_linux.go:349` 的 `Fix` 改成可执行命令（如 `asa-server perms fix`），并让它真的覆盖镜像。若要保留 verify 对镜像的检查，则必须让 reconcile 也修镜像（搭配 `SampleOwnerMismatch` 采样，避免每次启动遍历数万条链接），并把 `perms fix` 的 `SharedTrees()` 扩展到镜像，否则它仍不是有效修复入口。

#### [P1] `linux.umu_runtime_user: root`（或任何 uid=0 账号）可静默绕过降权

> 🔎 **复核：成立，维持 P1。** `lookupOrCreate`（`pkg/sysuser/sysuser_linux.go:126-137`）、`ResolveCredential`（`:77-99`）、`ChildIDs`（`:58-72`）、`Problems`（`:309-381`）都不排斥 uid 0；`appconfig` 校验只做去空白与判负。
>
> **修复方案**
> 1. `lookupOrCreate` 在 `user.Lookup` 成功后统一判定（`EnsureUser` 与 `ResolveCredential` 都经过它，一处即覆盖）：`u.Uid == "0"` → 返回错误「运行时用户 %s 解析为 uid=0（root）。要以 root 运行游戏请显式设 linux.umu_run_as_root: true」。
> 2. `Problems` 在 uid 解析后加阻断项 `umu-runtime-user-is-root`（与上面同一文案），保证门禁与 preflight 也看得见，而不只是在建凭证时报错。
> 3. `appconfig` 的 Linux 校验对 `umu_runtime_user == "root"` 直接报错（跨平台静态拦截，`config validate` 即可发现）。`umu_runtime_uid: 0` 在本项目表示「不指定」，不能据此拦截，由第 1 条兜住。
> 4. 测试（WSL）：`Name: "root"` 时 `ResolveCredential` 返回错误、`Problems` 含该项；`appconfig` 校验单测。

- **位置**：`internal/appconfig/validate.go:206-214`（仅去空白、判负，无 root 校验）；`pkg/sysuser/sysuser_linux.go:29-31`（`Managed()` 只看 `euid==0 && !RunAsRoot`）、`:58-72`、`:77-99`（`ResolveCredential` 直接取 `user.Lookup(name)`）
- **触发条件**：配置 `linux.umu_runtime_user: root`（或指向任意 uid=0 账号）。
- **后果**：`Managed()==true`、`Bypassed==false`；`ResolveCredential` 返回 `Uid:0`，游戏仍以 root 运行；启动自检（`checkOwnedDir` 对 `/root`、`sampleOwnerMismatch` 期望 uid=0）**全部通过**，preflight 的 `umuRuntimeUser.managed=true` 且 `ready=true`。即用户以为在降权，实际是 root，且无任何告警。与 `UMU_RUNTIME_USER_PLAN.md §2`「宁可服务起不来，也不默默把公网游戏进程跑成 root」直接冲突。
- **修复建议**：`ResolveCredential` 中解析结果加硬校验：

```go
if uid == 0 {
    return nil, "", fmt.Errorf("配置的运行时用户 %s 解析为 uid=0（root）；"+
        "请改 linux.umu_runtime_user，或显式设 linux.umu_run_as_root: true 以承认以 root 运行", u.Username)
}
```

并在 `validate.go` 里对 `UmuRuntimeUser == "root"` 直接报错（跨平台静态拦截）。

#### [P1] Python 版本探测无超时且持锁执行，探测挂起会拖死启动与实例启动

> 🔎 **复核：成立，下调为 P2。** `versionOf`（`pkg/pyfinder/pyfinder.go:246-248`）确实无超时、`Resolve` 持 `r.mu` 执行。但「等待 stdin 的脚本」不会挂：`exec.Cmd.Stdin == nil` 时子进程读到的是 `/dev/null`（立即 EOF）。只有真正卡死的包装器才会触发，且结果按 override 值缓存，只影响首次解析。另：原文 §2.3 所说「两个独立 Resolver」已不存在（见 §2.3 复核）。
>
> **修复方案**
> - `versionOf` 改 `exec.CommandContext(ctx, …)`，`ctx` 为 5 秒超时；同时设 `cmd.WaitDelay = time.Second`（防止孙进程持有 stdout 管道让 `Output()` 等不到 EOF）。
> - 超时视为「该候选探测失败」（见 §2.2 最后一条对失败候选的记录），不影响其他候选。
> - 最坏耗时由「无限」收敛为「候选数 × 5 秒」；持锁语义保留（它保证 preflight 与启动拿到同一答案）。

- **位置**：`pkg/pyfinder/pyfinder.go:246-266`（`exec.Command(path, "-c", probeScript).Output()`，无 ctx/timeout）；`Resolve` 在探测期间持有 `r.mu`（`:86-87`）；调用点 `internal/runner/python_linux.go:18-20`、`pkg/umu/umu.go:169-171`
- **触发条件**：`PATH` 上某候选（`python3.10`…`python3.20`、`python3`、`python`）是等待 stdin/挂死的脚本或包装器。
- **后果**：`runner.Preflight()`（setup）与实例启动（`umuCommandLine` → `umuInterpreter`）都会无限期阻塞；由于 `pyfinder.Resolver` 持锁，同进程内所有解释器解析调用方一起卡死。
- **修复建议**：改用 `exec.CommandContext` + 5s 超时。

#### [P2] `Problems` 用 `user.Lookup().HomeDir`，与 `EnsureUser`/`HomeDir()` 的判空规则不一致

> 🔎 **复核：成立，维持 P2（会阻断启动）。** 而且不一致的不止两处：`HomeDir()`（`:38-47`）对空 / `/` 回退到 `HomeFallback`，`EnsureUser`（`:112-114`）对空 / `/` **直接返回、也不创建 `HomeFallback`**，`ResolveCredential`（`:98`）返回原始 `u.HomeDir`，`Problems`（`:337`）检查原始 `u.HomeDir`。于是子进程拿到的 `HOME=HomeFallback` 可能根本不存在或属主不对。
>
> **修复方案**：新增唯一判定 `func (m *Manager) effectiveHome(u *user.User) string`——`filepath.Clean(u.HomeDir)` 为空或 `/` 时返回 `m.cfg.HomeFallback`，否则返回它。四处统一使用：
> - `HomeDir()`、`ResolveCredential` 的第二个返回值；
> - `EnsureUser`：对 `effectiveHome` 做 `MkdirAll(0700)` + `ChownTreeAs`（回退目录也要建好并交给运行时用户）；
> - `Problems`：`checkOwnedDir(m.effectiveHome(u), …)`。
> - 单测：Home 为 `/` 与空串的账号（WSL 里用 `useradd -d /` 建临时账号）四处结论一致，门禁不阻断。

- **位置**：`pkg/sysuser/sysuser_linux.go:337`（`checkOwnedDir(u.HomeDir, ...)`）vs `:112-114`（`HomeDir == "" || == "/"` 时 `return nil`）与 `:38-47`
- **触发条件**：运行时用户（或 uid 指向的既有账号）passwd 家目录为空或 `/`（系统账号常见，如 `nobody`）。
- **后果**：`EnsureUser` 认为「无需处理」直接返回，但 `Problems` 会 `Stat("/")` 并因属主 uid=0 报 `umu-runtime-home-bad` → **阻断启动**；实际家目录并非 `/`。两侧结论相反。
- **修复建议**：`Problems` 也走统一解析，`filepath.Clean` 后判空/判 `/` 复用 `EnsureUser` 的分支。

#### [P2] 深度探测把写探针落进共享 prefix，overlay 模式下是对已挂载 lowerdir 的元数据写

> 🔎 **复核：成立，维持 P2。** `ProbeDir` 仍是 `hostFor(cfg).Prefixes().Dir("")`（`runtimeuser_linux.go:225`），实例启动时强制深探（`server.go:521`）。overlay 下这是往活的 lowerdir 里 touch+rm；per-instance 下探的根本不是这次要用的前缀，结论也没有意义。
>
> **修复方案**
> - `pkg/wineprefix` 新增 `func (m *Manager) ProbeDir(key string) string`：`shared` → `Dir("")`；`per-instance` → `Dir(key)`；`overlay` → `overlayInstanceDir(key)`（`{BaseDir}/umu-prefix-overlay/<key>/`，它是 overlay 之外的普通目录，已在 `ensureOverlayPrefix` 里 chown 给运行时用户；**不能**探 `upper`——对挂载中的 upperdir 直接写同样是未定义行为）。
> - `VerifyRuntimeAccessForLaunch` 增加 `prefixKey` 参数（与 §2.2 P0 的 `mirrorDir` 参数一起加），用 `ProbeDir(prefixKey)`；启动门禁（非 launch）不做深探，保持现状。

- **位置**：`internal/runner/runtimeuser_linux.go:225`（`ProbeDir: wineprefixMgrFor(cfg).Dir("")`）；`pkg/sysuser/sysuser_linux.go:375-379, 440-457`（`deepProbeWrite` 真实 `touch`/`rm`）；实例门禁 `internal/instance/server.go:519`（`forceDeep=true`）
- **触发条件**：`prefix_mode: overlay`，`Dir("")` 返回共享前缀基目录 `{BaseDir}/umu-prefix`，它正被其它在跑实例当作 overlay 的 `lowerdir`。
- **后果**：每次实例启动都向「仍被挂载为 lowerdir 的目录」写入并删除文件。overlayfs 明确「对 lowerdir 写操作是未定义行为」；本仓库在别处（`PrepareSharedPrefixWrite`、`rwSubtrees` 排除 mounted 层）正是为规避它。深度探测绕过了这层保护。
- **修复建议**：探测目标改为该实例自己的可写层/私有目录（如 `wp.Dir(prefixKey)`），或在 overlay 下退化为已 chown 的等价可写目录；确需探测共享前缀时先走 `PrepareSharedPrefixWrite`。

#### [P2] GID 配置不与既有账号交叉校验（与文档 §4.2 不符）

> 🔎 **复核：成立，下调为 P3。** `lookupOrCreate` 与 `Problems` 只比 UID（`sysuser_linux.go:129-135, 329-335`）。后果是配置被静默忽略，实际用的是账号真实主组——功能不坏，只是与配置不符。
>
> **修复方案**：与 UID 对称——`lookupOrCreate` 在 `m.cfg.GID != 0 && u.Gid != cfg.GID` 时返回错误；`Problems` 加阻断项 `umu-runtime-gid-mismatch`。这会让「配置写错了 gid 但一直能跑」的现网部署在升级后被门禁拦下，CHANGELOG 需写明，文案给出两个修正方向（改配置 / `usermod -g`）。

- **位置**：`pkg/sysuser/sysuser_linux.go:126-137`（`lookupOrCreate` 只比 `cfg.UID`）；`cfg.GID` 仅在 `:179-212` 创建新账号时使用
- **触发条件**：账号已存在，`linux.umu_runtime_gid` 配置了与既有主组不同的 gid。
- **后果**：配置被静默忽略。共享写的 ACL/属组都以用户**实际**主组为 group（`sharedaccess_linux.go:93, 124`），用户按配置推断的组名与实际不符。文档 §4.2 明确写两者都交叉校验。
- **修复建议**：`lookupOrCreate` 补 GID 分支，与 UID 对称报错。

#### [P2] `checkACLSupport` 吞掉非 ACL 类错误，导致「自检无提示但启动被硬错误挡住」

> 🔎 **复核：成立，维持 P2。** `checkACLSupport`（`sharedaccess_linux.go:190-209`）非 `ErrUnsupported` 一律 `return nil`；而 `shareacl.Prepare`（`pkg/shareacl/shareacl.go:60-66`）对同类错误直接返回，经 `reconcileRuntimeOwnership` 让启动失败。
>
> **修复方案**：非 `ErrUnsupported` 返回 `Problem{Name: "posix-acl-probe", Warning: false, Detail: "探测 %s 的 ACL 支持失败：%v", Fix: "检查 BaseDir 所在文件系统是否可写、setfacl/getfacl 是否可执行（SELinux 可能拦截）"}`。定为**阻断**与启动时的真实后果一致：setup 在这里停下，比装完几百 MB 再在启动时失败好。`api` 路径的 preflight 本来只告警不阻断，不受影响。

- **位置**：`internal/runner/sharedaccess_linux.go:190-209`（仅 `errors.Is(err, shareacl.ErrUnsupported)` 时返回 Problem，其余 `return nil`）
- **触发条件**：`shareacl.Supported` 因非 ACL 原因失败（`os.MkdirTemp` 失败、`setfacl` 因权限/只读挂载以非「not supported」文本失败，`shareacl.go:147-164`）。
- **后果**：`/api/system/preflight` 与 `setup` 自检都不报告；而 `applySharedAccess`（`sharedaccess_linux.go:129-142`）对非 `ErrUnsupported` 直接 `return aclErr` 硬失败并拒绝启动。用户看到「自检通过但起不来」。
- **修复建议**：非 `ErrUnsupported` 也返回 Problem（`Warning:false` 以阻断 setup），或至少记日志。

#### [P2] `perms status` 在运行时用户不存在时把「用户缺失」渲染成「ACL 不可用」

> 🔎 **复核：成立，下调为 P3（仅诊断输出）。** `sharedaccess_linux.go:86-90` 把用户缺失塞进 `ACLError`，`perms.go:56-64` 按「ACL 不可用」打印。
>
> **修复方案**：`SharedAccessInfo` 加 `UserMissing bool`（JSON `user_missing`），`sharedAccessStatus` 在 `Lookup` 失败时置位并返回；`Model()` 对它返回 `"unknown"`；`actionPermsStatus` 先判 `UserMissing`，打印「运行时用户 %s 尚不存在：先运行 asa-server setup（或启动一次 asa-server，它会自动创建）」后返回，不再进入 ACL 分支。前端 preflight 面板同步读这个字段。

- **位置**：`internal/runner/sharedaccess_linux.go:86-90`（Lookup 失败即 `info.ACLError = "运行时用户 ... 不存在"`）＋ `internal/actions/perms.go:56-64`（打印 `ACL 支持：不可用（%s）`）
- **触发条件**：尚未 `setup`/未创建运行时用户时执行 `asa-server perms status`。
- **后果**：把两件不同的事混成一句，误导排障（明明可能装了 acl）。
- **修复建议**：`sharedAccessStatus` 区分 `UserMissing` 与 `ACLUnavailable`；`actionPermsStatus` 先判用户是否存在，单独提示「请先运行 asa-server setup」。

#### [P2] Python 自动探测的候选去重/择优与文档不一致

> 🔎 **复核：部分成立，下调为 P3。**
> - ~~① 同版本不优先带版本号的名字~~：**不成立**。`CandidateNames()`（`pyfinder.go:236-243`）先列 `python3.N…python3.10` 再列 `python3`/`python`，`found` 按这个顺序追加，而排序用的是 `slices.SortStableFunc`（`:222`）——同版本的候选保持插入顺序，带版本号的名字天然排在前面。
> - ② 成立：`probed` 只记录探测成功的候选，全部探测失败时报「no Python interpreter found」，与事实不符。
>
> **修复方案（只针对②）**：`resolveAuto` 另记 `failed []string`（`"name (path): err"`）；`found` 与 `probed` 都为空而 `failed` 非空时，返回 `Name: "python3-probe"`、`Msg: "找到 %d 个候选但都无法执行版本探测：…"`，并在 `FixHint` 里提示 noexec 挂载 / 包装脚本。单测用一个 `exit 1` 的假 `python3` 覆盖。

- **位置**：`pkg/pyfinder/pyfinder.go:222-231`（仅按版本 `SortStableFunc`）、`:162-200`（`probed` 只记录**成功**执行版本脚本的候选）
- **触发条件**：① 文档 §3.1 声明「版本相同则优先带版本号的名字」，实现无按名择优，只靠 `EvalSymlinks` 去重；② 所有候选都存在但版本脚本都执行失败（noexec 挂载、坏包装器）时 `probed` 为空 → 报 `no Python interpreter found`，与事实不符。
- **修复建议**：比较器加 tie-break（同版本优先带版本号名）；`probed` 记录「发现但探测失败」以区分两种失败。

### 2.3 文档 vs 代码偏差

| 文档声明                                                          | 实际代码                                                                                                                                            | 判定             |
| ------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- | -------------- |
| `ACL_PERMISSION_HARDENING_PLAN.md §8/§9`：T1/T2a/T2b/T3 全部完成   | `rwSubtrees(cfg,true)` 作**阻断项**、`rwSubtrees(cfg,false)` 作**修复项**，差集（镜像）造成永久死锁（P0）                                                               | 文档推理缺口，代码确有缺陷  |
| `UMU_RUNTIME_USER_PLAN.md §4.2`：幂等且廉价，「几次 stat 就返回」           | `reconcileRuntimeOwnership` 每次启动对 prefix/clusters/overlay 全量 `WalkDir` + 逐条 `d.Info()`（`runtimeuser_linux.go:108` → `sysuser_linux.go:238-259`） | 性能描述不符         |
| §4.2：UID/GID 都交叉校验                                            | 仅校验 UID，GID 静默忽略                                                                                                                                | 代码缺失           |
| §2/§4.1：专用**非 root** 用户                                       | 无任何校验阻止解析为 uid=0                                                                                                                                | 代码缺失           |
| `UMU_PYTHON_DISCOVERY_PLAN.md §3.1`：同版本优先带版本号名                | 仅按版本排序                                                                                                                                          | 代码缺失（低危）       |
| ~~§3.1：`umuInterpreter()` 是唯一改动点、缓存单一~~                           | ~~存在两个独立 `pyfinder.Resolver`~~                                       | ~~实现偏差~~ → **已修复**：`umuInterpreter()` 现转调 `hostFor(cfg).Umu().Interpreter()`（`internal/runner/python_linux.go:17-19`），全仓唯一的 `pyfinder.New()` 在 `pkg/umu/umu.go:154` |
| `ACL_PERMISSION_HARDENING_PLAN.md §3.4`：`perms fix` 用于修复共享写权限 | `SharedTrees()` 只含 `server-files`/`instances`，不含镜像与 Mods                                                                                        | 覆盖面小于文档暗示 → 由 §2.2 P0 修复方案第 3 条（`FixRuntimeOwnership`）补齐 |

> 🔎 **复核（§2.3）**：第 1、3、4 行随 §2.2 对应条目修复；第 2 行（每次启动全量 `WalkDir` prefix）属实，性能描述应改为「每次启动遍历一次独占目录树」，或在 `reconcileRuntimeOwnership` 前用 `sampleOwnerMismatch` 采样、命中才全量 chown（与共享树已有的 `NeedsPass` 采样同一做法）；第 5 行（同版本择优）**不成立**，见 §2.2 最后一条复核。



---

## 3. Wine Prefix 三模式与启动闸门

### 3.1 功能概述

Linux 上三种 `linux.prefix_mode`：`shared`（全局一份）、`per-instance`（每实例一份）、`overlay`（共享只读 lower + 每实例 overlayfs 可写层）。机制代码在 `pkg/wineprefix/wineprefix{,_linux}.go`（目录布局、`/proc/self/mountinfo` 解析、挂载/卸载、`.lower-stamp`、降级复制、`PrepareSharedWrite`、`Reconcile`），`pkg/umu/umu_linux.go` 负责 wineboot/wineserver 探测，`internal/runner/umu_linux.go` 只作组合根。启动闸门与 ArkApi 冲突阻断在 `internal/instance/{launchgate.go,server.go}`，GC 在 `internal/actions/prefix.go`。

**核心结论：overlay 的「写保护」在两条路径上被击穿（`verify-arkapi` 的 VC++ 补装、`PrepareSharedWrite` 与启动的竞态），且 `.lower-stamp` 在「宿主机重启后」这条最常见路径上完全失效并静默擦除可写层。**


### 3.2 发现

#### ~~[P0] `verify-arkapi --install-vcredist` 无守卫地写共享底层 lower~~

> 🔎 **复核：已修复（`36862d8`，umuruntime 阶段 3），问题不再存在。** 写共享前缀的守卫已下沉进 `umuruntime.Host.Provision` 本身（`pkg/umuruntime/host_linux.go:279-296`：`key` 解析到共享前缀时先 `PrepareSharedWrite`），`verify-arkapi --install-vcredist` 改走 `runner.Provision(ctx, "", os.Stdout, runner.CapMSVCRT)`（`internal/actions/verify_arkapi.go:55-62`），`--check-only` 只读不写。这正是原文「更彻底的做法」，结构上不会再有漏守卫的第五个调用点。

- **位置**：`internal/actions/verify_arkapi.go:53-60`（`runner.EnsurePrefixVCRedist(ctx, "", os.Stdout)`）；实现 `internal/runner/vcredist_linux.go:67-69`，落点 `:88`（`wineprefixMgrFor(cfg).Dir("")` 即共享底层）
- **触发条件**：`prefix_mode: overlay`，且存在实例的可写层正把 lower 当 `lowerdir`（实例运行中，或实例已停但挂载仍在——§3.3 有意不卸载）。执行 `asa-server verify-arkapi --install-vcredist`。**带 `--check-only` 时更糟**：`PrepareSharedPrefixWrite` 只在 `installer.VerifyArkApiInstallation`（`internal/installer/verify_arkapi.go:74`）里，`--check-only` 根本不会走到，这一步全程零守卫。
- **后果**：`Ensure` 向共享底层写 DLL override 注册表（`pkg/vcredist/install_linux.go:317-344` 的 `applyOverrides`），可能还写 `system32`。对正被 `lowerdir` 引用的目录写入是 overlayfs 明确的未定义行为，症状随机且落在**实例**身上。文档 `UMU_PREFIX_OVERLAY_PLAN.md §12.4` 称守卫覆盖三处，**这是未覆盖的第四处**。
- **修复建议**：写之前过同一道守卫，并把它作为唯一入口：

```go
if cmd.Bool("install-vcredist") {
    fmt.Println("\n正在准备 VC++ 运行时...")
    if done, err := runner.PrepareSharedPrefixWrite("asa-server verify-arkapi --install-vcredist"); err != nil {
        fmt.Printf("跳过 VC++ 运行时安装：%v\n", err)
    } else {
        if err := runner.EnsurePrefixVCRedist(ctx, "", os.Stdout); err != nil {
            fmt.Printf("VC++ 运行时安装失败: %v\n", err)
        }
        done()
    }
}
```

更彻底的做法是把守卫下沉进 `runner.EnsurePrefixVCRedist` 本身（按 prefixKey 是否共享前缀决定是否 `PrepareSharedWrite`），避免下一个调用点再漏。

#### [P1] 未挂载但内容完好的可写层，下次启动被静默擦除（upper 跨重启丢失）

> 🔎 **复核：成立，下调为 P2。** 现位于 `pkg/wineprefix/wineprefix_linux.go:426-449`，逻辑未变：宿主机重启后 `mounted=false`、`merged` 是空挂载点所以 `seeded=false`，stamp 判断整段跳过，`upper` 被 `RemoveAll`，且因为 `:435` 的日志只在 `mounted||seeded` 时打印，这次重建完全无声。下调理由：`upper` 按设计「不是用户数据」（存档在别处），重新挂载本身毫秒级；实际损失是 Wine 运行期写进可写层的状态，以及「`prefix status` 说下次会自动挂载」与实际行为不符。
>
> **修复方案**
> ```go
> mounted := overlayMounted(merged)
> seeded := !mounted && umu.PrefixInitialized(merged)
> // 挂载形态的静息态：宿主重启 / 被 PrepareSharedWrite 卸载后，upper 还在盘上
> resting := !mounted && !seeded && dirNonEmpty(overlayUpperDir(cfg, key))
> stamp := readOverlayStamp(cfg, key)
>
> if stamp == want {
>     switch {
>     case mounted, seeded:
>         return fixPfxSymlink(merged, cfg.chownPath, logf)
>     case resting:
>         if err := os.MkdirAll(merged, 0755); err == nil {
>             if err := mountOverlay(cfg, key, logf); err == nil {
>                 return fixPfxSymlink(merged, cfg.chownPath, logf)
>             } else {
>                 logf("实例 %s 的可写层重新挂载失败（%v），改为重建", key, err)
>             }
>         }
>     }
> }
> if mounted || seeded || resting {
>     logf("实例 %s 的 Wine 可写层需要重建（记录 %q，当前 %q）", key, orUnknown(stamp), orUnknown(want))
> }
> // …以下保持现有的卸载 / 清理 / 挂载 / 降级复制流程
> ```
> 要点：只有 stamp 一致才复用 `upper`（与 §8.1 的指纹语义一致——底层变了照样重建）；复用时不清 `work`（内核挂载时自己会整理 `work/work`）；任何重建路径都打日志。
>
> **测试**（WSL，`pkg/wineprefix/wineprefix_linux_test.go`）：`EnsurePrefix` → `umount merged`（模拟重启）→ 在 `upper` 放一个标记文件 → 再 `EnsurePrefix` → 断言已挂载且标记文件仍在；再改底层指纹后断言会重建。

- **位置**：`pkg/wineprefix/wineprefix_linux.go:425-447`

```go
mounted := overlayMounted(merged)
seeded := !mounted && umu.PrefixInitialized(merged)               // :428
if (mounted || seeded) && readOverlayStamp(cfg, key) == want {    // :430
    return fixPfxSymlink(merged, cfg.chownPath, logf)
}
if mounted || seeded { logf("...正在重建", ...) }                  // :434-437
if mounted { unmountOverlay(merged) }                              // :438-442
for _, d := range []string{overlayUpperDir(cfg, key), work, merged} { // :447
    os.RemoveAll(d)
}
```

- **触发条件**：宿主机重启（或任何 `umount`）之后首次启动该实例。此时挂载消失，`merged` 是空挂载点 → `PrefixInitialized(merged)` 为假 → `seeded=false`；`(mounted||seeded)` 为假，**stamp 复用判断被整段跳过**，直接走进 `RemoveAll(upper/work/merged)`。
- **后果**：`upper` 里 63 MiB、内容完好的可写层被删除（`prefix status` 此时正显示「未挂载，下次启动时自动挂载」63.2 MiB）。因为是「静默重建」（连 `:434` 的「正在重建」日志也不打），运维看不到异常。这使文档 §3.3「实例停止不卸载、下次启动零成本」与 `.lower-stamp` 复用语义在**最常见路径**上失效。注意降级复制形态（`seeded`）反而不受影响，恰好说明该分支漏写了挂载形态的静息态。
- **修复建议**：补第三种形态，stamp 一致时保留 `upper`、只重新挂载：

```go
mounted := overlayMounted(merged)
seeded := !mounted && umu.PrefixInitialized(merged)
hasLayer := !mounted && !seeded && dirExists(overlayUpperDir(cfg, key)) // 挂载形态静息态

if (mounted || seeded || hasLayer) && readOverlayStamp(cfg, key) == want {
    if !mounted {
        if err := os.MkdirAll(merged, 0755); err != nil { return err }
        if err := mountOverlay(cfg, key, logf); err != nil { /* 走 seedFromLower 降级 */ }
    }
    return fixPfxSymlink(merged, cfg.chownPath, logf)
}
```

或把 `readOverlayStamp == want` 的判定与 `mounted` 解耦：只要 stamp 一致就绝不 `RemoveAll(upper)`。

#### [P1] ArkApi 冲突检查用「端口已监听」判活，且位于启动闸门之前（check-then-act 窗口）

> 🔎 **复核：成立，维持 P1。** `conflictingArkApiInstance`（`internal/instance/launchgate.go:102-131`）仍用 `procpkg.IsServerRunning`（纯端口）；启动路径里的检查在 `server.go` 的 `acquireLaunchGate` 之前。更具体的失效时序：A 持闸门到 `start_initialization_successful` 就放行（`server.go:716-718`），此时 A 的端口通常**还没绑定**（端口在 `Server has completed startup` 前后才出现），B 在闸门外已经通过检查，拿到闸门后直接进同一个 Wine 会话——闸门反而把两者的时序排成了必然撞车的样子。
>
> **修复方案**
> 1. **判据改为「在启动或在运行」**，由两部分并集组成：
>    - 进程内登记表：`instance` 包新增 `arkApiLaunches`（`map[string]struct{}` + `sync.Mutex`）。`claimArkApiSlot(self string) (other string)` 在**同一把锁内**完成「查登记表 + 查存活 + 把 self 登记进去」，消除 A、B 同时检查的竞态；`startServerInternal` 在 ArkApi 分支调用它，`defer` 在函数返回时注销（成功返回时 PID 已落盘，之后由存活判定接手）。
>    - 存活判定：`procpkg.IsInstanceProcessAlive(name)`（端口 **或** 已校验的保存 PID，覆盖端口未绑定的阶段）。实施 §6.2 的 PID 身份校验后，这里用带实例标记的版本，避免把别的实例的进程算进来。
> 2. **权威检查挪进闸门内**：`acquireLaunchGate` 之后、`runner.Run` 之前再调一次 `claimArkApiSlot`（闸门前那次保留作快速失败，`PrecheckStart` 仍是 HTTP 层提示，只读登记表与存活，不登记）。
> 3. 三处共用 `arkApiConflictError`，措辞不变。
> 4. 测试：`launchgate_test.go` 用假的存活函数（包级变量注入）覆盖「A 已登记但未监听端口时 B 被拒」「A 失败返回后 B 可进」。

- **位置**：`internal/instance/server.go:467`（`conflictingArkApiInstance`）在 `:548`（`acquireLaunchGate`）**之前**；`launchgate.go:121` 用 `procpkg.IsServerRunning(name)`，其实现 `internal/process/process.go:116-126` 是 `PIDByPort(config.Port)`——**只有端口已监听才算「在跑」**。
- **触发条件**：`shared` 模式下两个 ArkApi 实例先后/并发启动。先启动者从 `StartServer` 到端口监听要数分钟（ArkApi 档 `gamePIDWaitTimeoutArkApi = 3 * time.Minute`），这段时间 `IsServerRunning` 恒 false。后启动者的检查在闸门外先跑，此时先启动者尚未监听 → 检查通过。
- **后果**：两个 ArkApi 一起进入同一 Wine 会话 → 第二个静默挂死 3 分钟并留下孤儿进程树——正是该检查存在的全部理由。闸门只串行化了 `runner.Run` 之后的一段，拦不住这个窗口。
- **修复建议**：两处一起改。① 判活改进程存活：`launchgate.go:121` → `if running, _ := procpkg.IsInstanceProcessAlive(name); !running { continue }`（与同文件 `ListAliveInstances` 口径一致）。② 把冲突检查移到闸门**之内**，在 `acquireLaunchGate` 之后、`runner.Run` 之前复查一次：

```go
releaseLaunchGate, err := acquireLaunchGate(ctx, instanceName)
if err != nil { startErr = err; return startErr }
defer releaseLaunchGate()

if arkAsaApiRunning {
    if other := conflictingArkApiInstance(instanceName); other != "" {
        startErr = arkApiConflictError(instanceName, other)
        return startErr
    }
}
```

#### [P1] `PrepareSharedWrite` 与「实例正在启动」的竞态：卸载正在启动/即将启动的层

> 🔎 **复核：成立，维持 P1。** `PrepareSharedWrite`（`wineprefix_linux.go:811-845`）仍基于一次 `mountinfo` 快照、不取任何锁；调用方现在是 `Host.Ensure`（每次 API 启动后台跑）、`Host.Provision`、`installer` 的两处 verify。原文给的「按 key 取 `lockPrefix`」只能解决情形①的一部分：`ensureOverlayPrefix` 返回后、wineserver 起来之前，锁早已释放；情形②（快照之后才挂载）完全不覆盖。
>
> **修复方案**：给「底层正在被修改」和「某个可写层正在被启动使用」各一个显式信号。
> 1. `wineprefix.Manager` 新增 `lowerMu sync.RWMutex` 与 `leases map[string]int`（受 `leaseMu` 保护）。
> 2. `PrepareSharedWrite`：先 `lowerMu.Lock()`（此后不会再有新挂载），再逐个已挂载 key：`lockPrefix(instDir)` → 若 `leases[key] > 0 || WineserverHoldsPrefix(merged)` 判为 live，否则卸载。有 live 时立即 `Unlock` 并返回错误；成功时返回的关闭函数负责 `lowerMu.Unlock()`。
> 3. `ensureOverlayPrefix`：入口 `lowerMu.TryRLock()`，失败即返回「共享 Wine 前缀正在被修改（%s），请稍后重试」（记下当前修改窗口的 `op` 名称用于提示）。**不阻塞等待**：修改窗口可能长达数分钟（wineboot + VC++），让启动请求悬挂那么久比明确失败更糟，且 `RWMutex` 不响应 ctx。挂载完成后 `RUnlock`。
> 4. 新增 `func (m *Manager) HoldLayer(key string) (release func())`：`leases[key]++`。`runner` 暴露 `HoldPrefix(prefixKey)`，`startServerInternal` 在 `EnsurePrefix` **之前**取得、在函数返回时释放——正常路径返回时游戏已运行，wineserver 接手「占用」的判定；失败路径直接释放。非 overlay 模式下是空操作。
> 5. 跨进程（CLI `verify` 与服务并存）不在本条范围内，由 §4.2 D3 的 flock 覆盖。
> 6. 测试（WSL）：持有 `HoldLayer("a")` 时 `PrepareSharedWrite` 必须报 live 且不卸载；`PrepareSharedWrite` 窗口打开期间 `EnsurePrefix(overlay)` 必须立即失败。

- **位置**：`pkg/wineprefix/wineprefix_linux.go:799-833`（`PrepareSharedWrite` 遍历 `overlayKeysMounted`，`unmountOverlay` 空闲层）；对端 `:408-420`（`ensureOverlayPrefix` 持 `m.lockPrefix(instDir)` 后 mount）。两侧**不共用锁**，`overlayKeysMounted` 只是一次 `mountinfo` 快照。
- **触发条件**：`EnsureRuntime`/`verify` 调 `PrepareSharedWrite` 与某实例 `ensureOverlayPrefix` 并发。① 实例 B 已 mount、`runner.Run` 尚未起 wineserver（窗口内 `WineserverHoldsPrefix(merged)` 为假）→ 被判空闲并 `umount`，B 随后对着**已卸载的空 merged** 启动；② 实例 B 尚未 mount → 快照看不到 → 守卫放行修改 lower，B 随后才挂载，lower 在**被引用期间**被改。
- **后果**：① 实例拿到空前缀；② lower 修改是 UB。`TODO §1.4` 以「窗口毫秒级、不上锁」接受此风险，但 `EnsureRuntime` 的修改窗口实际横跨 `warmPrefix` + VC++ 安装（分钟级）。
- **修复建议**：`PrepareSharedWrite` 对每个 key 先取实例的同一把锁再判定+卸载：

```go
for _, key := range overlayKeysMounted(cfg) {
    merged := overlayMergedDir(cfg, key)
    unlock := m.lockPrefix(overlayInstanceDir(cfg, key)) // 与 ensureOverlayPrefix 同锁
    if umu.WineserverHoldsPrefix(merged) {
        unlock(); live = append(live, key); continue
    }
    err := unmountOverlay(merged)
    unlock()
    ...
}
```

#### [P2] prefix gc 对 `bak-` 名字的判定绕过 wineserver 二次确认

> 🔎 **复核：成立，下调为 P3。** `internal/actions/prefix.go:170-174` 与 `:75-76` 仍按 `strings.HasPrefix(p.Key, "bak-")` 分流。触发需要实例名以 `bak-` 开头、其前缀**不是当前模式在用的**（当前在用的已被 `gcCandidates` 的 `p.Current && instances[p.Key]` 排除）、且在快照后被占用，窗口很窄。
>
> **修复方案**
> - `wineprefix.Info` 增 `Backup bool`，`Status()` 在 `shared + ".bak-*"` 那条 glob 命中时置位（判据是**匹配到哪个 pattern**，而不是事后从名字推断）；备份行的 `Current` 恒为 false。
> - `wineprefix.Manager` 新增 `RemoveBackup(path string) error`：校验 `path` 确实匹配 `<shared>.bak-*`，`WineserverHoldsPrefix` 二次确认后再 `RemoveAll`。
> - `prefix gc` 与 `prefix status` 的归属标签一律改用 `p.Backup`。

- **位置**：`internal/actions/prefix.go:170-174`（`if strings.HasPrefix(p.Key, "bak-") { os.RemoveAll(p.Path) }`）；`Key` 由 `pkg/wineprefix/wineprefix_linux.go:314-316` 从路径剥出
- **触发条件**：实例名本身以 `bak-` 开头（`ValidateInstanceName` 只挡 `..` 与路径分隔符）。其 per-instance 前缀 `umu-prefix-bak-xxx` 的 `Key` 被剥成 `bak-xxx`，于是 `prefix gc --apply` 把它当版本备份直接 `os.RemoveAll`。若该前缀在快照之后被启动/被 wineserver 占用，就删掉在用目录——**其它所有前缀都有 `RemoveInstancePrefix` 的 `WineserverHoldsPrefix` 二次确认，唯独它们没有**。
- **修复建议**：用结构性判据替代字符串推断——`wineprefix.Info` 增 `Backup bool`（在 `Status()` 里按 `<shared>.bak-` 前缀设置），`gc` 改 `if p.Backup { ... }`，删除一律经过会重新确认 wineserver 的入口。

#### [P2] `rwSubtrees`/`UnmountedOverlayDirs` 的 TOCTOU：可能对已挂载的 merged 做 chownTree

> 🔎 **复核：成立，下调为 P3。** 同一进程内确实可达：`reconcileRuntimeOwnership` 不仅在启动门禁里跑，也作为 `Host.Ensure` 的 `BeforeEnsure` 在 API 启动后台执行（`internal/runner/umu_linux.go:145-150`），可与实例启动并发。但需要恰好在列表生成后、chown 到该层前完成挂载。
>
> **修复方案**：列举与 chown 不再分离。`wineprefix.Manager` 新增 `ChownUnmountedLayers(chown func(string) error) error`：逐层 `lockPrefix(instDir)` → 复核 `!overlayMounted(merged)` → `chown(merged)` → 解锁（与 `ensureOverlayPrefix` 同一把锁，挂载与 chown 互斥）。`reconcileRuntimeOwnership` 改用它；`rwSubtrees` 只在只读的 verify 采样路径继续使用 `UnmountedOverlayDirs`（采样是 stat，不触发 copy-up）。

- **位置**：`internal/runner/runtimeuser_linux.go:160-194`（构建列表）+ `:103-110`（`su.ChownTree`）；快照来自 `pkg/wineprefix/wineprefix_linux.go:660-678`
- **触发条件**：列表生成后、chown 走到某层之前，实例启动并把该层挂上。
- **后果**：对挂载中的 merged 做元数据写会触发 copy-up，把整个共享 lower 复制进该实例私有层——本模式唯一卖点被抹掉。
- **修复建议**：chown 前对每个候选再复核 `overlayMounted`；更稳妥是 overlay 层不做全树 chown，只 chown 我们自己创建的目录。

#### [P2] `EnsureRuntime` 无条件调用 `PrepareSharedWrite`，无改动时也会卸载空闲层

> 🔎 **复核：成立，维持 P2。** 编排已迁入 `Host.Ensure`（`pkg/umuruntime/host_linux.go:242-251`），顺序仍是「先 `PrepareSharedWrite`（卸载所有空闲层），失败后才问 `LowerNeedsWork`」。每次 API 启动都会把所有空闲的可写层卸掉，再叠加 §3.2 第二条的擦除问题，「挂载跨重启存活」基本名存实亡。
>
> **修复方案**：`EnsureUmu`/`EnsureGEProton`/SLR 预取之后，先问 `h.prefixes.LowerNeedsWork()`；为 false 时记一行「共享 Wine 前缀已是最新，不需要修改」并跳到收尾（**不调用** `PrepareSharedWrite`、不跑 `WarmPrefix`/`provision`）；为 true 时才 `PrepareSharedWrite`，失败直接返回错误。`LowerNeedsWork` 已覆盖未初始化、Proton 版本、SLR 就绪、插件 `pending` 四种「有活要干」的情形，与 `WarmPrefix` 的快路径判据一致。测试放 `host_linux_test.go`：底层已就绪且有空闲挂载时，`Ensure` 后挂载仍在。

- **位置**：`internal/runner/umu_linux.go:168-176`
- **触发条件**：每次 API 启动时的后台 `EnsureRuntime`（`internal/webapi/actions.go:501-505`）。
- **后果**：即使 `LowerNeedsWork()` 为假，`PrepareSharedWrite` 也先把空闲层全部 `umount` 才返回；与 §3.3「挂载跨重启存活」意图相悖。文档 §13.1 称「只在底层确实还有事要做时才可能拒绝」，实际是「先卸载再判断」。
- **修复建议**：`LowerNeedsWork()` 前移，为假时直接返回，不碰任何挂载。

#### [P2] 启动闸门可能被永久持有（`select` 无 ctx/超时兜底）

> 🔎 **复核：成立，与 §6.2「`launchgate` 可能被永久持有」是同一问题，按 P1 处理，修复方案见那一条。**

- **位置**：`internal/instance/server.go:708-712` 的 `select { case <-initFailed: case <-initSuccessful: }`（无 `ctx.Done()`/超时）；上游 `internal/instance/common.go:467-515` 的 `waitServerStartup` 也只由「进程退出」或日志行结束，**没有超时**。
- **触发条件**：`waitForGamePID` 成功后，`waitServerStartup` 既等不到日志行、进程也不退出。
- **后果**：`shared` 模式下闸门永不释放，后续所有实例启动在 `acquireLaunchGate` 上永久排队。文档 §8.3「闸门持有时间天然被封顶」不成立。
- **修复建议**：给该 `select` 加 `case <-ctx.Done(): startErr = ctx.Err(); return startErr` 与显式启动总超时（`defer releaseLaunchGate()` 已在 `:555` 兜底）。

#### [P2] overlay 残留层与在用 per-instance 前缀同名时，gc 会「部分删除后报失败」

> 🔎 **复核：成立，维持 P2，且比原文描述的更糟。** `Manager.Remove`（`wineprefix_linux.go:255-281`）不分形态，先删 overlay 层再删 per-instance 前缀。除了原文的「删了一半报失败」，还有反方向：当前模式是 overlay、实例 A 已停止，`gc` 列出的候选是 A 的**旧 per-instance 前缀**，调用 `RemoveInstancePrefix("A")` 会顺手把 A **当前在用**的 overlay 可写层也删掉（`gc` 只对这一行做了 `Current` 判断，对另一种形态毫不知情）。
>
> **修复方案**
> - `wineprefix.Manager` 拆出 `RemoveLayer(key)`（现 `removeOverlayPrefix`）与 `RemovePrefix(key)`（per-instance 那一半），各自持锁与 wineserver 二次确认。
> - `Remove(key)` 保留给**实例删除 / 重命名**（`internal/webapi/instanceapi/instanceapi.go:295, 371`）：依次调两者，用 `errors.Join` 汇总并在错误里分别说明哪一半成功。
> - `prefix gc` 按行的形态调用：`p.Overlay` → `RemoveLayer`，`p.Backup` → `RemoveBackup`（见上一条），其余 → `RemovePrefix`。`runner` 相应导出三个函数。

- **位置**：`pkg/wineprefix/wineprefix_linux.go:254-280`（`Manager.Remove` 同时删「overlay 层 + per-instance 前缀」）；`internal/actions/prefix.go:199-211`
- **触发条件**：实例 A 在 `per-instance` 模式运行，盘上还留着同名 overlay 层 `umu-prefix-overlay/A/`。
- **后果**：`RemoveInstancePrefix("A")` 先删 overlay 层，再走到 per-instance 前缀被 `WineserverHoldsPrefix` 拒绝 → 该行报「失败」但 layer 已消失，「报失败却已删一半」。
- **修复建议**：`Manager.Remove` 按「当前形态」只删对应项，或把两半的成功/失败分别返回。

### 3.3 文档 vs 代码偏差

1. **文件落点整体失效**：`UMU_PREFIX_OVERLAY_PLAN.md §7/§13.3` 指向的 `internal/runner/{overlay_linux.go,prefix_linux.go,prefix.go}` **在仓库中不存在**，实现位于 `pkg/wineprefix/`、`pkg/umu/`。
2. **`.lower-stamp` 的「感知 VC++ 补装」未实现**：`PLAN §3.3`/`§6.1` 与 `wineprefix.go:105-108` 都声称可检测「重装了 VC++」，实现只比 Proton 版本（见 §8.1）。
3. **写保护覆盖范围不足**：`PLAN §12.4`/`§13.1` 说守卫覆盖三处；`verify-arkapi --install-vcredist`（`internal/actions/verify_arkapi.go:56`）是未覆盖的第四处。
4. **`prefix gc` 判据**：`PLAN §3.3` 原写「拒绝删除仍处于挂载状态的」，实现改为只按 wineserver 占用（§13.1 已回填），但 `actions/prefix.go:170` 的 `bak-` 直接删除又额外绕过了这唯一确认，文档未记录。
5. **闸门持有上界**：`UMU_PREFIX_PER_INSTANCE_PLAN.md §8.3` 与 `waitServerStartup` 无超时的实现不符。
6. **「挂载跨重启存活」**：`§3.3`、`§11.2` 与 `overlayStatus`（「未挂载，下次启动时自动挂载」）都假定重挂载会复用 `upper`；但 `ensureOverlayPrefix` 在未挂载且 `merged` 为空的形态下会静默擦除 `upper`。

> 🔎 **复核（§3.3）**：第 2 条 ~~`.lower-stamp` 不感知 VC++~~ 与第 3 条 ~~写保护覆盖不足~~ **已修复**（`2edd91c`、`36862d8`，见 §8.1 与 §3.2 首条）；第 1 条路径已由 `UMU_PREFIX_PLAN.md` 路径对照更正；第 4、5、6 条随 §3.2 的 `bak-`、闸门、upper 三条修复一并回填文档。

---

## 4. UMU Prefix 初始化排查项（D0~D6）

### 4.1 功能概述

`UMU_PREFIX_INIT_TROUBLESHOOTING.md` 的根因 D0（降权子进程继承 root 的 `DBUS_SESSION_BUS_ADDRESS`，bwrap 拒绝启动）及放大器 D1（`warmPrefix` 把失败当成功）、D5（给失败前缀写标记）在当前代码中**确已修复且实现正确**：`pkg/umu.InheritedEnv()`（白名单）+ `WarmPrefix` 的后置 `PrefixInitialized` 校验 + 标记后置写入，三条链路共用同一判据。但 **D2、D3、D4、D6 及附录 §7 的待办项仍未落地**。

### 4.2 发现

> 注：本模块的 overlay 可写层生命周期、`.lower-stamp` 语义两条与 §3、§5 是同一根因，已合并到 §8 统一处理。以下仅列本模块独有项。

#### [P2] D4 未修复：`EnsureWorldReadable` 仍在下拉解压之前执行，且被 D2 的双次调用掩盖

> 🔎 **复核：成立，维持 P2。** 编排迁入 `Host.Ensure` 后顺序不变：`BeforeEnsure`（= `ensureRuntimeUser` → `reconcileRuntimeOwnership`，含 `EnsureWorldReadable`，`internal/runner/runtimeuser_linux.go:142-152`）在 `EnsureUmu`/`EnsureGEProton`（`host_linux.go:218-223`）**之前**；`pkg/umu` 解压后只 `chmod` 了 `umu-run` 一个文件（`umu_linux.go:133`）。
>
> **修复方案**：让「解压的一方负责可读」。`pkg/umu` 的 `EnsureUmu` 与 `EnsureGEProton` 在解压成功、落位之后各调一次 `fsutil.EnsureWorldReadable(<解压目录>)`（`pkg/umu` → `pkg/fsutil` 是 pkg 内叶子依赖，符合准入标准），已存在而跳过下载时不调用（避免每次启动遍历 Proton 整棵树）。`reconcileRuntimeOwnership` 里的两次调用保留，作为带外改动（手工解压、`chmod -R`）的修复；它现在对首装是空操作，这一点在注释里写明。**必须与下方 D2 同批合入，且先于 D2。**

- **位置**：`internal/runner/runtimeuser_linux.go:142-152`（`reconcileRuntimeOwnership` 内的 `fsutil.EnsureWorldReadable(proton/umuDir)`）→ 由 `ensureRuntimeUser`（`:94-101`）调用 → 由 `internal/runner/umu_linux.go:126` 调用，**早于** `EnsureUmu`(`:144`) / `EnsureGEProton`(`:147`)
- **触发条件**：全新安装且只跑一轮 `EnsureRuntime`（即 D2 若被修掉）。此时 `pathExists(proton)`/`pathExists(umuDir)` 为 false，补权限整体落空；随后 `WarmPrefix` 以降权用户执行 `wineboot`，需读取刚解压出来的 `proton/` 与 `umu-launcher/`。
- **后果**：若解压产物中存在非 world-readable/executable 的条目（`pkg/archive/archive.go:71` 直接采用 tar 头 mode），降权用户 EACCES，wineboot 失败——现在会（得益于 D1 修复）当场报错，但根因未除。当前未爆发纯粹因为 D2 让第二次 `EnsureRuntime` 在解压后补跑了一遍权限。
- **修复建议**：把 `EnsureWorldReadable` 移到 `EnsureUmu()`/`EnsureGEProton()` 之后、`WarmPrefix` 之前；或在二者成功后各自补一次。**与 D2 修复强耦合：两者必须同批修改，只删冗余调用会让 D4 立刻咬人。**

#### [P2] D3 未修复：无跨进程互斥，CLI setup 与服务并存的并发缺口依旧

> 🔎 **复核：成立，维持 P2。** 进程内互斥现为 `Host.ensureMu`（`host_linux.go:190-191`），全仓仍无 `flock`；服务启动后台 `EnsureRuntime`（`internal/webapi/actions.go:518`）与 CLI `setup`（`internal/actions/setup.go:85`）可并发。
>
> **修复方案**
> - `pkg/umuruntime` 新增 `lock_linux.go`：`lockRuntime(ctx, baseDir) (unlock func(), err error)`，对 `{BaseDir}/.umu-runtime.lock` 做 `syscall.Flock(LOCK_EX|LOCK_NB)`，未拿到则每 500ms 重试并响应 `ctx.Done()`；进程崩溃由内核释放。拿锁超过 2 秒仍在等时 `logf` 一行「另一个 asa-server 进程正在准备运行时，等待中」。
> - 取锁位置：`Host.Ensure` 与 `Host.Provision`（在 `ensureMu` 之后），以及 `wineprefix` 的 per-instance 新建与 overlay 首建路径（经 Host 注入同一把锁的回调，`pkg/wineprefix` 不直接依赖 umuruntime）。锁序固定为 `ensureMu → flock → lockPrefix`，避免死锁。
> - `installer` 的两处 verify 在 `PrepareSharedPrefixWrite` 之前经 `runner` 取同一把锁。

- **位置**：`internal/runner/umu_linux.go:109`（仅进程内 `sync.Mutex runtimeMu`）；`internal/webapi/actions.go:501` 无条件 `go runner.EnsureRuntime`；`internal/actions/setup.go:112` 前台 `EnsureRuntime`
- **触发条件**：CLI `asa-server setup` 与 systemd 服务（或两个 CLI 进程）并存，二者同时 `WarmPrefix` 同一前缀。
- **后果**：两个进程并发对同一 WINEPREFIX 跑 wineboot / `reconcilePrefixVersion` 的 `os.Rename` / regedit，出现半成品前缀或前缀被搬走后另一进程按旧路径继续写。全仓库无任何 `flock`（`pkg/arkcache` 用的是另一套 `O_EXCL` 自旋锁，未覆盖 prefix）。
- **修复建议**：按文档 §7.5，在 `WarmPrefix` 前对 `{BaseDir}/.umu-prefix.lock`（或 per-prefix `{prefix}.lock`）做 `flock(LOCK_EX)`，进程崩溃由内核自动释放。

#### ~~[P2] `EnsurePrefix` 快路径在读判据后、无锁地调用 `EnsureVCRedist`（TOCTOU）~~

> 🔎 **复核：已修复，问题不再存在。** 当前 `EnsurePrefix`（`pkg/wineprefix/wineprefix_linux.go:206-226`）先 `unlock := m.lockPrefix(prefix); defer unlock()`，再做 `PrefixInitialized && PrefixMarker == 版本` 的快路径判定与 `cfg.pending`/`cfg.provision` 补装，判据读取与补装都在同一临界区内。VC++ 也已改为插件经 `provision` 钩子接入，不再有名为 `EnsureVCRedist` 的直接调用。

- **位置**：`pkg/wineprefix/wineprefix_linux.go:203-220`
- **触发条件**：同一 key 的两次 `EnsurePrefix` 并发，且前缀已初始化、marker 匹配但 override 缺失。
- **后果**：`PrefixInitialized && PrefixMarker==版本` 的判定与 `EnsureVCRedist` 都在 `lockPrefix(prefix)`（`:200`）之前/之外，两个 goroutine 可同时跑 regedit 容器写同一 `user.reg`。
- **修复建议**：把判据读取与 `EnsureVCRedist` 一并纳入 `lockPrefix(prefix)` 临界区（快路径也持锁，代价仅一次 mutex）。

#### ~~[P2] `reconcilePrefixVersion`：marker 缺失即整体搬走可用前缀；`writePrefixMarker` 失败会让可用前缀被上报为失败~~

> 🔎 **复核：不成立（两点都是有意的设计，不是缺陷）。**
> - ① marker 缺失即搬走（`pkg/umu/umu_linux.go:486-507`）照抄参考脚本 `ark_instance_manager.sh`：来源不明的前缀不可信任，重建一次约一分钟、且只发生一次。原文担心的「连带 overlay 实例丢失基线」不会以不一致的方式发生：共享底层的 `WarmPrefix` 只在 `PrepareSharedWrite` 成功后执行（此时没有任何挂载中的可写层），底层重建后 Proton 标记与指纹变化，各可写层按 `.lower-stamp` 规则整体重建。原文建议的「从 `system.reg` 读 Proton 版本」不可行：Wine 的注册表里没有可靠的 Proton 构建号。
> - ② marker 写失败时返回错误（`:279-280`）是**正确的**：marker 是承重件，不写成功的话下一次 `WarmPrefix` 会把这个可用前缀当成「来源不明」搬走。现在就报失败让操作者处理 chown/磁盘问题，比「这次说成功、下次无故重建」好。
>
> 无需修改。

- **位置**：`pkg/umu/umu_linux.go:484-505`（搬走逻辑）、`:511-518`（marker 写入）、`:238-240`/`:280`（调用点）
- **触发条件**：① 一个 `system.reg`/`drive_c` 完好、只是 `.created-by-proton` 缺失的前缀；② marker 写入失败（如 `chownPath` 失败）。
- **后果**：① 整个前缀被 `os.Rename` 到 `.bak-unknown` 并重建——共享前缀下会连带使所有 overlay 实例丢失 lower 基线，代价大且无必要；② `WarmPrefix` 在前缀实际可用时返回错误，`setup` 被误中止、实例被误判无法启动。
- **修复建议**：① 用前缀内 `system.reg` 记录的 Proton 版本作为"未知来源"兜底判据，仅确实无法判定时才搬走；② marker 写失败降级为 warn，不反向否决成功。

#### [P2] D0 白名单存在逃逸口：`Options.Env` 完全绕过白名单，`RuntimeEnv` 又不剥 D-Bus

> 🔎 **复核：成立（潜在），下调为 P3。** 逃逸口现位于 `pkg/umuruntime/host_linux.go:515-518`（`LaunchSpec.Env` 非 nil 时直接作基底）与 `internal/runner/runner_linux.go:128`（`Env: opt.Env` 透传）；全仓 Linux 侧没有任何调用方设置 `Options.Env`，今天不可达。
>
> **修复方案**：`pkg/umu` 导出 `FilterLaunchEnv(env []string) []string`（即现在 `InheritedEnv` 对 `os.Environ()` 做的过滤，复用 `launchEnvAllowed`），`Host.Command` 对 `LaunchSpec.Env` 也过一遍——调用方可以**替换**继承来的环境，但不能绕过白名单。显式追加的变量（`WINEDLLOVERRIDES`、显示租约、`ExtraEnv`）在过滤之后追加，不受影响。同步把 `UMU_PREFIX_PLAN.md` §9 那句「XDG 剥离也覆盖 `Options.Env`」改为「白名单同样作用于 `LaunchSpec.Env`」。

- **位置**：`internal/runner/runner_linux.go:207-210`（`baseEnv = opt.Env` 时不走 `InheritedEnv`）；`pkg/umu/umu_linux.go:685-708`（`RuntimeEnv` 只剥 `XDG_*`/HOME/USER/LOGNAME）；`pkg/sysuser/sysuser_linux.go:462-482`
- **触发条件**：任何调用方给 `runner.Options.Env` 传入携带 `DBUS_SESSION_BUS_ADDRESS`/`SESSION_MANAGER`/`XAUTHORITY` 的环境（当前仓库内调用点均为 nil，属潜在漏洞）。
- **后果**：文档 §9 称「`runtimeEnv` 的 XDG 剥离是第二道防线，也仍覆盖调用方通过 `Options.Env` 显式传入的环境」——该保证对 D-Bus 类变量**不成立**，D0 原始故障可原样复现。
- **修复建议**：让 `RuntimeEnv` 复用 `launchEnvAllowed` 做白名单过滤，或对最终 env 统一过一次白名单；并修正 §9 表述。

#### [P2] 白名单用 `WINE*` 前缀通配，放行了 `WINEARCH`/`WINEPREFIX`/`WINEDLLOVERRIDES` 等宿主变量

> 🔎 **复核：成立，下调为 P3。** `pkg/umu/umu_linux.go:674-676` 仍是 `HasPrefix(key, "WINE")`。`WINEPREFIX`、`WINEDLLOVERRIDES` 会被后续追加的同名变量覆盖（exec 取最后一次出现），真正有害的是 `WINEARCH`（可能建出 32 位前缀）以及原文没提到的 `WINESERVER`、`WINELOADER`（指向别的 wine 二进制）。systemd 服务的环境是干净的，只有从带 Wine 环境的交互 shell 跑 CLI 时才会带进来。
>
> **修复方案**：保留 `WINE*` 通配（运营者用 `WINEDEBUG`、`WINEESYNC`/`WINEFSYNC` 调参是有意支持的），但加显式拒绝集：`WINEARCH`、`WINEPREFIX`、`WINESERVER`、`WINELOADER`、`WINEDLLPATH`。在 `launchEnvAllowed` 的通配分支前判断；`umu_linux_test.go` 为五个名字各加一条断言。

- **位置**：`pkg/umu/umu_linux.go:674`（`strings.HasPrefix(key, "WINE")`）
- **触发条件**：宿主存在 `WINEARCH=win32`，或 `WINEPREFIX` 指向别处。
- **后果**：`wineboot --init` 会把 `WINEARCH` 带进去，可能建出 32 位前缀，而 `ArkAscendedServer.exe` 是 x64，后续静默失败；`WINEPREFIX` 虽被 `runEnv` 末尾覆盖（`:331`）侥幸无害，但白名单形状不该如此宽。
- **修复建议**：改成精确集合（`WINEDEBUG`/`WINEDLLOVERRIDES`/`WINEDLLPATH` 等），明确排除 `WINEARCH`、`WINEPREFIX`。

#### [P2] `arkcache` 跨进程锁的 stale 判定（30 分钟）短于大包下载（详见 §5）

> 🔎 **复核：与 §5.2「陈旧锁阈值」为同一条，见那里。**

- **位置**：`pkg/arkcache/arkcache.go:266-321`
- **说明**：与 §5 的「陈旧锁阈值」是同一处代码，详见 §5.2。

#### [P2] 实例名以 `bak-` 开头时被 `prefix gc --apply` 当作「版本备份」绕过 wineserver 检查（详见 §3）

> 🔎 **复核：与 §3.2 `bak-` 一条为同一条（已下调 P3），见那里。**

- **位置**：`internal/actions/prefix.go:75-76`、`:170-173`
- **说明**：与 §3 的 `bak-` GC 问题同源，详见 §3.2。

#### [P2] D2 未修复：一次 setup 仍调用 `EnsureRuntime` 两次

> 🔎 **复核：成立，维持 P2。** `internal/actions/setup.go:85` 一次，`InstallBaseEnvironment` → `installer.DownloadAndUpdateArkServer`（`internal/installer/installer.go:279`）又一次。实施 §3.2「`EnsureRuntime` 无条件 `PrepareSharedWrite`」的修复后，第二次不再卸载挂载，但仍会重跑一遍 `reconcileRuntimeOwnership` 的整树 chown。
>
> **修复方案**（**先合 D4，再合本条**）
> - `DownloadAndUpdateArkServer` 删除 `runner.EnsureRuntime`，改为只读的 `runner.CheckRuntime()`，失败返回「Linux 运行时未就绪：…，请运行 asa-server setup」。
> - 真正需要「顺便准备运行时」的入口显式调用一次：CLI `update`（`internal/actions/actions.go`）与 API 更新（`internal/updatemanage/manager.go`，服务刚启动时后台 `EnsureRuntime` 可能还没跑完）。`setup` 维持它自己那一次。
> - 回归：WSL 全新 BaseDir 跑一次 `setup`，日志中「first-time umu setup」与 reconcile 各只出现一次，且降权 `wineboot` 成功（验证 D4 已不依赖第二次调用兜底）。

- **位置**：`internal/actions/setup.go:112`（第一次）＋ `internal/actions/environment.go:57` → `internal/installer/installer.go:279`（第二次）
- **后果**：第二次除重复 `reconcileRuntimeOwnership` 的整树遍历外，还会重跑 `PrepareSharedWrite`（又一次全量挂载卸载），纯冗余；也正是它掩盖了 D4。
- **修复建议**：按 §7.6 去掉 `DownloadAndUpdateArkServer` 里的那次（改为断言 `CheckRuntime()`），与 D4 修复同批进行。

#### [P3] `WarmPrefix` 无超时，`RunInPrefix` 超时/取消只杀直接子进程

> 🔎 **复核：成立，维持 P3。** `WarmPrefix` 传零值 `RunOptions{}`（`pkg/umu/umu_linux.go:263`）；`RunInPrefix`（`:365-397`）用 `exec.CommandContext`，取消时只 Kill python。
>
> **修复方案**
> - `RunInPrefix`：设置 `cmd.Cancel = func() error { return procx.KillTree(cmd.Process.Pid) }`（整棵树按 ppid 快照杀，能跨过 pressure-vessel 的 setsid 边界）与 `cmd.WaitDelay = 10 * time.Second`（孙进程持有输出管道时不至于让 `Wait` 永远等不到 EOF）。
> - `WarmPrefix`：`RunOptions{Timeout: 30 * time.Minute}`。上限要宽：首次运行 umu 可能还要自己下载 SLR（预取失败的降级路径）。同步修改 `RunOptions` 注释里「wineboot 必须是零值」的表述——超时不改变 umu 的行为，零值约束只针对 `NoRuntimeUpdate` 与 `Verb`。

- **位置**：`pkg/umu/umu_linux.go:263`（`RunOptions{}` 零值＝无超时）；`:363-397`（`exec.CommandContext` 只 Kill python，不 Kill bwrap/wine 树）
- **触发条件**：wineboot 挂起；或 `vcredist` 的 15 分钟超时触发（`pkg/vcredist/install_linux.go:197-205`）。
- **后果**：setup 可能永久挂起；超时路径下被杀只是 `umu-run`（python），其下 bwrap/wineserver 成为孤儿并继续持有 prefix，随后 `WaitForWineserverDrain` 空等 90 秒，且孤儿残留导致后续 `PrepareSharedWrite` 误判「live」拒绝写。
- **修复建议**：给 `WarmPrefix` 的 wineboot 设硬上限；`RunInPrefix` 在 ctx 结束时对整棵进程树 `procx.KillTree`。

#### [P3] 死代码：`Runtime.runtimeUserNameHint` 定义后从未被调用

> 🔎 **复核：成立，维持 P3。** 仍在 `pkg/umu/umu_linux.go:401-411`，全仓无调用。**修复方案**：删除该函数及其注释（`RuntimeEnv` 的 USER/LOGNAME 来源已是 `cfg.userName()`，即 `Identity.UserName` 回调）。

- **位置**：`pkg/umu/umu_linux.go:399-409`
- **后果**：注释声称它是 `RuntimeEnv` 的 USER/LOGNAME 来源，实际 `RunInPrefix`（`:389`）传的是 `cfg.userName()`，属重构残留，易误导。
- **修复建议**：删除，或真正接入并在注释中更新。

### 4.3 已核实正确的修复（正向结论）

- **D0**：`pkg/umu/umu_linux.go:641-678` 白名单已替换 `os.Environ()`；`runEnv`（`:325-343`）与 `runner.umuCommandLine`（`runner_linux.go:207-215`）均以 `umu.InheritedEnv()` 为基底，`DBUS_SESSION_BUS_ADDRESS` 被丢弃。缺陷仅在 `Options.Env` 逃逸口。
- **D1/D5**：`WarmPrefix`（`:226-281`）保留 `runErr`、wineboot 后重跑**同一**判据 `PrefixInitialized`（`:411-416`，前置 `:238`/后置 `:274` 共用），失败即返回带退出状态与末 8 行 tail 的错误；`writePrefixMarker` 已移到成功分支之后（`:279-280`），不再给失败前缀盖章。
- **D6 症状线**：`reconcilePrefixVersion` 对无 `system.reg` 的 prefix 早退（`:486-488`），与 E3 实验结论一致；`WaitForWineserverDrain`（`:533-541`）在前后置校验之间执行，无顺序问题。

### 4.4 文档 vs 代码偏差

1. **§9 声称 `runtimeEnv` 的 XDG 剥离「也仍覆盖调用方通过 `Options.Env` 显式传入的环境」** — 对 `DBUS_SESSION_BUS_ADDRESS` 等不成立（`RuntimeEnv` 只剥 `XDG_*`）。
2. **§7 将 D4 列为「仍待办」** — 与代码一致，但文档未提示其与 D2 的强耦合：**单独修 D2 会立即引爆 D4**。
3. **`.lower-stamp` 的注释声称能检测「reinstalled VC++」** — 代码只写/比 Proton tag（见 §8.1）。
4. **overlay 层「跨重启存活、内容还在 upper」** — 与 `ensureOverlayPrefix` 对未挂载层的擦拭重建矛盾（见 §3、§8.3）。
5. **§4 的 D0 剥离清单位置与 D1 位置** — 重构后已失效，实际在 `pkg/sysuser/sysuser_linux.go:462-482` 与 `pkg/umu/umu_linux.go:226-281`。
6. **§3.3 的行号（`runner/umu_linux.go:285` 的 "ready"）** — 现为 `pkg/umu/umu_linux.go:279`。

> 🔎 **复核（§4.4）**：第 3 条 ~~`.lower-stamp` 不感知 VC++~~ **已修复**（`2edd91c`）；第 1、2、4 条随 §4.2 的 D0、D4、§3.2 upper 修复回填文档；第 5、6 条为路径/行号漂移，已由 `UMU_PREFIX_PLAN.md` 的路径对照覆盖。

---

## 5. ArkApi 缓存预取与 VC++ 运行时

### 5.1 功能概述

`pkg/arkcache` 在 ArkApi 加载器启动前，把与 `ArkAscendedServer.exe` 哈希绑定的 offsets cache 从多 CDN 下好、按 ArkApi 的 `validateSerializedMap` 格式校验、以 `generations/<hash>-<pid>-<ms>-0` 命名提交，并原子写出 `cached_key.cache` 指针；随后交给镜像同步分发到各实例。`pkg/vcredist`（Linux）在共享 Wine prefix 里写 11 条 `native,builtin` DLL override 并条件性运行微软安装器。`serialized.go`/`zip.go` 对上游客观格式的复刻**准确无误**（含 `keySize==0`、越界、尾部残字节、重复 key、条目数上限、zip-slip/路径穿越/符号链接逃逸/zip-bomb 防护均无缺口）；但**并发提交与清理的互斥范围不足**是最严重缺陷。


### 5.2 发现

#### [P0] 跨哈希并发时 `pruneGenerations` 会删掉 metadata 正指向的 generation

> 🔎 **复核：成立，下调为 P2。** 代码与交错序列属实（`pkg/arkcache/arkcache.go:121-123` 按哈希分片加锁，`:205` 清理「非本哈希」的全部 generation）。但**两个不同哈希同时预取**只有一条现实路径：实例 A 在启动链上对旧 exe 预取（下载可达数分钟）期间，ARK 更新换掉了 exe 并触发更新后预取。这条路之所以走得通，是因为更新前的守卫只看 `procpkg.ListAliveInstances()`（`internal/installer/installer.go:46`），而 A 此时还没有进程——这是比 prune 本身更上游的缺口。后果是 metadata 指向不存在的 generation，ArkApi 判缓存无效后**自己整包重下**（即方案 §4.4 规定的降级路径），「加载器行为异常」没有依据。
>
> **修复方案**
> 1. **堵住上游**：更新前守卫在 `ListAliveInstances` 之外，再拒绝任何状态处于 `start_initialization`/`start_initialization_successful`/`starting`/`restart(ing)` 的实例（`statepkg.GetInstanceStateIsStart` 或同义判断），提示「实例 X 正在启动，请等它完成后再更新」。
> 2. **提交与清理原子化**：`arkcache` 新增包级 `commitMu sync.Mutex`，`writeMetadata` + `pruneGenerations` 在它之下执行（下载与解压仍按哈希并行，不受影响）。
> 3. **清理不删被引用者**：`pruneGenerations` 在删除前**现读** `cached_key.cache`（`Inspect(cacheRoot, "")`），其 `CacheDirectory` 与 `keepRel` 一并视为受保护——跨进程（CLI `arkapi-cache fetch` 与服务）时 `commitMu` 管不到，这一条兜住。
> 4. generation 的原子出现见本节「源缓存写入与镜像同步不互斥」一条的 staging 方案。
> 5. 单测：建 `h1-*`、`h2-*` 两代并令 metadata 指向 `h2`，以 `hash=h1` 调 `pruneGenerations`，断言 `h2` 那代仍在。

- **位置**：`pkg/arkcache/arkcache.go:205`、`:121-123`、`pkg/arkcache/generation.go:256-277`
- **触发条件**：进程内互斥是**按哈希**的（`hashMutex(hash)`），而 `pruneGenerations` 的行为是「**非当前哈希的 generation 一律删**」。两个不同 hash 的 `Prepare` 重叠时（实例 A 在 ARK 更新前算得旧 hash `h1` 正在下载，更新流程/实例 B 随后以新 hash `h2` 预取）会交错成：`A.writeMetadata(h1)` → `B.writeMetadata(h2)`（指针现指向 `h2`）→ `A.pruneGenerations(h1)` 把 `h2` 的 generation 目录整棵删除。现有单测 `TestPruneGenerationsKeepsCurrentHash`（`generation_test.go:177`）只验证「删别的哈希」，无并发用例。
- **后果**：产生方案 §4.4 明令禁止、C++ 侧最难诊断的形态——**metadata 指向不存在的 generation**。ArkApi 读到后判缓存失效并整包重下（预取白做），严重时加载器行为异常。
- **修复建议**：① 把「提交 metadata + 清理」放进同一把**全局**互斥（不能按哈希分片）；② don't-delete-if-referenced：

```go
var commitMu sync.Mutex // 包级，覆盖所有哈希的提交/清理

commitMu.Lock()
cur, _ := Inspect(req.CacheRoot, "")
if err := writeMetadata(...); err != nil { commitMu.Unlock(); ... }
pruneGenerations(req.CacheRoot, hash, genRel, req.Keep, false, cur.Generation)
commitMu.Unlock()
```

`pruneGenerations` 加 `protected string` 参数，循环里 `if generationRelPath(name) == protected { continue }`。③ generation 先写临时目录再 `os.Rename` 进 `generations/`。

#### [P1] `writeMetadata` 的 `.tmp` 文件名跨哈希共享，可写出撕裂的 JSON

> 🔎 **复核：成立，下调为 P3。** 触发条件与上一条完全相同（同哈希已被 `hashMutex` + 文件锁串行化），上一条的 `commitMu` 已覆盖进程内情形。**修复方案**：临时文件改用 `os.CreateTemp(cacheRoot, "cached_key.cache.*.tmp")`，写入 + `Sync` + `Close` 后 `os.Rename` 到最终名（同目录，原子），失败路径 `os.Remove` 该临时文件；覆盖跨进程的剩余情形。

- **位置**：`pkg/arkcache/generation.go:203-204`（`tmp := final + ".tmp"`）
- **触发条件**：两个不同哈希的 `Prepare` 并发进入 `writeMetadata`，同开 `cached_key.cache.tmp` 写、`Sync`、`Close`、`Rename`，互相截断/覆盖。
- **后果**：`cached_key.cache` 可能是非法 JSON 或字段错乱，ArkApi 直接判缓存无效。
- **修复建议**：临时名带唯一后缀且同目录 rename：`tmp := fmt.Sprintf("%s.%d.%d.tmp", final, os.Getpid(), time.Now().UnixNano())`；配合 P0 的 `commitMu` 双保险。

#### [P1] 下载体没有真正的字节上限，可打满磁盘

> 🔎 **复核：成立，下调为 P2。** `download.Options` 无上限字段、`fetchOnce` 直接 `io.Copy`（`pkg/download/download.go:29-35, 113-124`）。前提是 CDN / 镜像返回的 body 远大于 HEAD 声明的长度，属于对端异常。`pkg/download` 同时被 umu、GE-Proton、SLR、syncthing 使用，加上限对它们同样有益。
>
> **修复方案**
> - `download.Options` 增 `MaxBytes int64`（0 = 不限，保持现有调用方行为）。
> - `fetchOnce`：`resp.ContentLength > 0 && startOffset+resp.ContentLength > MaxBytes` 时不开始写，直接返回 `ErrTooLarge`；否则 body 包 `io.LimitReader(body, MaxBytes-startOffset+1)`，写完若 `written > MaxBytes` 删除 `.part` 返回 `ErrTooLarge`。`Fetch` 对 `ErrTooLarge` 不重试（重试也是同一结果）。
> - `arkcache.fetchZip` 传 `MaxBytes: info.contentLength`（HEAD 声明的精确长度，已先校验 ≤ `MaxSize`），比 `MaxSize` 更严。
> - 单测用 `httptest` 返回超长 body，断言 `.part` 被删、返回 `ErrTooLarge`。

- **位置**：`pkg/download/download.go:29-35`、`:113-124`、`pkg/arkcache/fetch.go:175`、`:207`
- **触发条件**：`download.Options` **没有** `MaxBytes`，`fetchOnce` 用 `io.Copy(out, resp.Body)` 原样落盘；arkcache 仅用 HEAD 声明的 `Content-Length` 做前置判断，下载后再比对 `fi.Size()`。若 GET 实际 body 大于 HEAD 声称长度（CDN 配置错误、重定向到错误大文件、恶意镜像），`fetchOnce` 会一直写到磁盘满；`download.Client()` 刻意不设 `client.Timeout`（`proxy.go:90-92`），启动路径 ctx 通常无 deadline；`Resume=true` 时 `.part` 为 `O_APPEND`，重试继续追加。
- **后果**：磁盘被 `.part` 打满，且无时间上限。文档 §14 的 `max_size` 并未真正约束下载体。
- **修复建议**：给下载器加硬上限 `MaxBytes`，`fetchOnce` 用 `io.LimitReader(resp.Body, opt.MaxBytes+1)` 并在 `written > MaxBytes` 时报错删 `.part`；由 arkcache 传 `MaxBytes: req.MaxSize`。

#### [P1] 陈旧锁阈值（30 分钟）短于可能的下载时长，会夺锁并造成双写

> 🔎 **复核：成立，下调为 P2。** `staleLockAge = 30min` 且只在创建时写一次时间戳（`arkcache.go:269-288`）；`release` 无条件删除（`:287`）。触发需要另一进程（CLI `arkapi-cache fetch` 与服务）在下载超过 30 分钟时恰好来抢——注意等待方只等 `lockWaitLimit = 60s` 就放弃，所以只有「30 分钟后才到达」的进程会夺锁。
>
> **修复方案**
> 1. 锁文件内容改为 `pid token unix秒`，`token` 为 16 字节随机十六进制。
> 2. **心跳**：持锁期间起一个 goroutine 每 60 秒 `os.Chtimes(lock, now, now)`，`release` 时停止。
> 3. **陈旧判据**：锁文件 **mtime** 超过 5 分钟，**或**锁内 pid 在本机已不存在（`procx.IsProcessExited`）。不再依赖创建时刻。
> 4. `release`：读回锁内容，`token` 与自己一致才删除。
> 5. 夺锁的原子性见下一条。

- **位置**：`pkg/arkcache/arkcache.go:269-273`、`:292-295`（锁文件仅在创建时写一次时间戳，`:285`）
- **触发条件**：`staleLockAge = 30*time.Minute`，而 `max_size` 默认 768 MiB；低带宽链路上一次预取超过 30 分钟完全可能。另一进程的 `lockIsStale` 判为陈旧，`os.Remove` 后重新 `O_EXCL` 拿锁。
- **后果**：两进程同时写同一 `<hash>.part` → ZIP 字节交错、损坏；并放大 P0/P1。`release()` 无条件 `os.Remove(path)` 还会删掉对方后来建的锁。
- **修复建议**：锁加心跳（`download.Options.Progress` 回调周期性 `os.Chtimes`），陈旧判据改为「锁文件 mtime 超过 N 分钟」；`release()` 前先读锁内容确认 PID 是自己再删。

#### [P1] 陈旧锁存在 TOCTOU，两个等待者可同时持锁

> 🔎 **复核：成立，下调为 P3**（需要两个等待者在同一轮同时判定陈旧，且都晚于 30 分钟到达）。`arkcache.go:292-295` 判定与删除分离。
>
> **修复方案**：夺锁改为「改名再建」：判定陈旧后 `os.Rename(lock, lock+".stale."+自己的token)`——同一时刻只有一个改名者成功，失败者（`ENOENT`）回到循环重新 `O_EXCL` 竞争；成功者删除改名后的旧锁再 `O_EXCL` 建新锁。`lockIsStale` 保持只读。配合上一条的 token 校验，`release` 也不会误删别人的锁。

- **位置**：`pkg/arkcache/arkcache.go:289-295`、`:307-321`
- **触发条件**：A、B 同时读到同一份陈旧内容，A `Remove` 后立刻 O_EXCL 建新锁，B 随后仍执行已决策的 `Remove`，把 A 的**新锁**删掉再建自己的。
- **后果**：与上一条相同的双写损坏，且更难复现。
- **修复建议**：夺锁做成原子操作（rename 到唯一名后校验；或建锁后回读内容确认 PID 是自己）。`lockIsStale` 只做只读判断。

#### [P1] 源缓存写入与镜像同步**不是**互斥，镜像可能同步到半成品 generation

> 🔎 **复核：成立，下调为 P2。** `extractCacheZip` 直接写最终 generation 目录（`arkcache.go:167-171`），managed 模式下镜像同步对 `generations/` 跳过内容比对（`internal/mirror/mirror.go:846`）。但窗口比原文说的窄：启用 ArkApi 的实例在同步前都会先进 `PrepareArkApiCache`，同哈希时会在 `hashMutex` 上等前一个预取完成，所以不会同步到半成品；真正暴露的是**未启用 ArkApi 的实例**（不预取、直接同步整棵 server-files），它镜像里的半成品在以后该实例启用 ArkApi 时才会被用到。
>
> **修复方案**（不引入跨包锁，让 generation 原子出现即可）
> 1. `Prepare` 解压到 `generations/.staging-<genName>`，两个 `.cache` 通过 `validateSerializedMap` 后 `os.Rename` 到 `generations/<genName>`（同目录，原子），然后才 `writeMetadata`。任何失败 `RemoveAll` staging 目录。
> 2. `internal/mirror` 收集源条目时跳过 `generations/.staging-*`（`isUnderArkApiGenerations` 旁加一个 `isArkApiStaging` 判断），镜像永远看不到半成品。
> 3. `pruneGenerations`/`GC` 顺带清理超过 1 小时的 `.staging-*` 残留（进程崩溃遗留）。
> 4. 已有的半成品无法事后识别；在 CHANGELOG 里提示「如遇 ArkApi 反复下载 offsets，可删除实例镜像后重启」。

- **位置**：`internal/instance/server.go:273`、`internal/instance/arkcache.go:100-120`、`internal/mirror/mirror.go:118`、`:137-138`、`:846`
- **触发条件**：`mirrorSyncMu` 只保护 `SyncInstanceMirror` 自身；而 `PrepareArkApiCache`（`server.go:273`）在**进入** `mirrorSyncMu` 之前写源目录，`PrefetchArkApiCacheAfterUpdate`（更新/CLI 路径）完全独立触发、不加锁。`extractCacheZip` 直接把 47MB 的 `cached_offsets.cache` 写进**最终** generation 目录（`arkcache.go:169-171`），存在被同步 Walk 读到的中间态。
- **后果**：镜像里被复制进**截断/不完整**的 `.cache`；managed 模式下 `syncMirrorEntries` 对 `generations/` 下文件**跳过 MD5 对账**（`mirror.go:846`），损坏**永不被修复**，直到 ARK 更新换代。文档 §11.4「与镜像同步互斥」的论断对更新路径与跨实例不成立。
- **修复建议**：① 预取写源阶段包进 `mirror.WithSyncLock`；② generation 先写临时目录再原子改名进 `generations/`：

```go
stageDir := genDir + ".staging"
extractCacheZip(out.zipPath, stageDir, req.MaxSize)
os.Rename(stageDir, genDir) // 同文件系统，原子
```

#### ~~[P1] VC++ override 写进共享 lower 却被 overlay 实例 upper 里的旧 `user.reg` 遮蔽（`.lower-stamp` 不感知 VC++ 变更）~~

> 🔎 **复核：已修复（`2edd91c`，umuruntime 阶段 5），问题不再存在。** `.lower-stamp` 现为「Proton 标记;组件指纹」（`pkg/wineprefix/wineprefix_linux.go:730-736` 的 `lowerStamp` → `composeLowerStamp`），组件指纹由各 `PrefixProvisioner.Fingerprint` 现读现算，VC++ 插件报告 override 是否齐全、system32 是否原生等；底层补装 VC++ 后指纹变化，已有可写层按 stamp 不符整体重建，旧 `user.reg` 不再遮蔽。`LowerNeedsWork` 改用插件的 `pending`（`:785-786`），不再反复进入重建。回归用例 `TestOverlayLayerFollowsLowerProvisioning`。

- **位置**：`pkg/wineprefix/wineprefix_linux.go:423`、`pkg/wineprefix/wineprefix.go:105-108`、`pkg/wineprefix/wineprefix_linux.go:195-197`、`internal/runner/umu_linux.go:189`
- **触发条件**：`ensureRuntime` 把 override 装进**共享 lower**（prefixKey 为空）；overlay 模式下实例 `merged` 里已存在 `user.reg`（跑过一次后 Wine 会 copy-up 进 `upper`），则 merged 看到的是 upper 旧文件，lower 新写的 override 被遮蔽。而 overlay 分支 `EnsurePrefix` 直接 `return m.ensureOverlayPrefix(...)`（`:195-197`），**没有** per-instance 分支的 `hasVCRedistOverrides` 补装检查（`:204-220`）。`.lower-stamp` 内容只是 `umu.PrefixMarker(lower)`（Proton 版本，`:423`），VC++ 重装不改变它 → 可写层不被重建。
- **后果**：`PrefixHasVCRedist(merged)`/`OverridesApplied(merged)` 恒 false，ArkApi 仍加载 Wine 内建 DLL，加载失败或行为异常；启动路径只给一条告警（`server.go:488`）。同时 `LowerNeedsWork`（`:773`）每次 API 启动都可能因读不到 lower 期望状态而反复进入「重建」。
- **修复建议**：把 VC++ override 状态纳入 `.lower-stamp`（记录 `<proton>\n<overrides-fingerprint>`，`want` 也带指纹）；或给 overlay 分支补上与 per-instance 相同的 `hasVCRedistOverrides(merged)` 快路径补装。指纹可用 `vcredist.CountOverrides(user.reg)`。

#### [P2] `hashMutex` 无生命周期管理，且 `sync.Mutex.Lock` 不响应 ctx

> 🔎 **复核：成立，维持 P2**（内存增长可忽略，问题在于不可取消的无上限等待）。`arkcache.go:96-101, 121-123` 未变。
>
> **修复方案**：`hashMutexes` 改为 `map[string]*hashSlot` + 一把普通 `sync.Mutex`，`hashSlot{ ch chan struct{} (cap 1); refs int }`。获取：登记 `refs++` 后 `select { case slot.ch <- struct{}{}: case <-ctx.Done(): refs--; return ctx.Err() }`；释放：`<-slot.ch`、`refs--`，`refs == 0` 时从 map 删除。`Prepare` 拿不到时返回 `Result{Reason: "等待同一 exe 的另一次预取时被取消"}`（永不致命，启动继续由 ArkApi 自行下载）。

- **位置**：`pkg/arkcache/arkcache.go:96-101`、`:121-123`
- **触发条件**：`hashMutexes sync.Map` 每次新 hash 就永久留下一个 `*sync.Mutex`；同一 hash 的第二个实例启动会在 `mu.Lock()` 上**无超时、不响应 ctx** 地等待第一个实例的整段下载（可达数十分钟），而该等待发生在 `mirror.SyncInstanceMirror` 与 `acquireLaunchGate` 之前（`server.go:273`）。
- **后果**：轻微内存增长；实例 B 启动被无上限拖延，用户取消无法打断。
- **修复建议**：改用带 ctx 的信号量（`golang.org/x/sync/semaphore` 或 `chan struct{}` + `select { case <-ctx.Done(): }`），空闲后清理 `hashMutexes`。

#### [P2] 复用同尺寸的遗留 `<hash>.zip` 而不校验来源

> 🔎 **复核：成立，维持 P2。** `pkg/arkcache/fetch.go` 在 sidecar 与本次 HEAD 不同源时只删 `.part`，不删已完成的 `<hash>.zip`；随后仅凭大小相等就复用它。
>
> **修复方案**：`!readSidecar(metaPath).sameSource(info)` 分支同时 `os.Remove(zipPath)`；另外「`zipPath` 存在但 sidecar 缺失 / 读不出」也视为不同源一并删除。之后才 `writeSidecar`。单测：预置同尺寸但 sidecar 的 `ETag`/`Last-Modified` 不同的 zip，断言会重新下载。

- **位置**：`pkg/arkcache/fetch.go:186-200`、`:215`
- **触发条件**：`if fi.Size() != info.contentLength` 才重下——只要遗留 ZIP 字节数等于当前 HEAD 长度就直接跳过下载并提取，该 ZIP 可能来自另一 CDN/另一版内容；而 metadata 的 `last_modified` 取自当前 `info`。
- **后果**：低概率但严重的「内容与来源/时间戳不一致」：ArkApi 的 HEAD 匹配 metadata 于是采用这份缓存，实际 offsets 可能不对应。
- **修复建议**：复用无条件要求 sidecar 与当前 `info` 同源同版本，否则删除重下。

#### [P2] `GC` 可删除正在进行的刷新所需的 `.lock`/`.part`

> 🔎 **复核：成立，下调为 P3**（只在手动 `asa-server arkapi-cache gc --apply` 恰好撞上一次刷新时发生）。`arkcache.go:246-262` 的跳过条件只有 `HasPrefix(name, hash) && !current.Ready`。
>
> **修复方案**：`GC` 对每个哈希的中转物先非阻塞地尝试同一把文件锁（`acquireFileLock` 增一个 `tryOnly` 形态）：拿不到说明有人在下载，整组跳过并在输出里说明；拿到则在锁内删除后释放。`*.lock` 文件本身永不由 GC 删除（陈旧锁由 §5.2 的夺锁逻辑处理）。

- **位置**：`pkg/arkcache/arkcache.go:246-262`、`internal/actions/arkapicache.go:191`
- **触发条件**：跳过当前哈希中转物的条件是 `strings.HasPrefix(name, hash) && !current.Ready`。当缓存有效（`current.Ready==true`）但正处于 `FromRefresh` 重下（锁已持有、`.part` 正在增长）时，`gc --apply` 把该 `.part`、`.meta.json`、`.lock` 一并删除。
- **后果**：破坏进行中的下载，并让锁被删后其他进程可进入。
- **修复建议**：GC 对属于当前哈希的 `*.lock`/`*.part`/`*.meta.json` 一律跳过。

#### ~~[P2] `writeMetadata` 失败时遗留几百 MB 的 `<hash>.zip`~~

> 🔎 **复核：不成立（不是泄漏，是可复用的中间产物）。** 该 zip 已完整下载并通过了结构校验，保留它意味着下一次 `Prepare` 可以跳过下载（`fetchZip` 按 sidecar 同源 + 尺寸相等复用，见上面「复用遗留 zip」一条的修复后仍然成立），而下一次成功提交时 `:204` 会删除它；`arkapi-cache gc` 也会在缓存就绪后回收同哈希的中转物。`writeMetadata` 失败最常见的原因是暂时性的（权限、磁盘瞬时满），立刻删掉几百 MB 的有效下载反而更糟。无需修改。

- **位置**：`pkg/arkcache/arkcache.go:198-201`
- **触发条件**：`writeMetadata` 返回错误时只 `os.RemoveAll(genDir)`，未删 `out.zipPath`。
- **后果**：偶发的中转 ZIP 磁盘泄漏。
- **修复建议**：该分支同样 `os.Remove(out.zipPath); os.Remove(out.zipPath+".part")`。

#### [P2] 历史「裸 64 位哈希」metadata 会被误判为「我们接管的缓存」，翻转镜像守卫

> 🔎 **复核：成立，下调为 P3。** `sourceCacheManaged`（`internal/mirror/mirror.go:80-88`）只看 `res.Ready`，而 `Inspect` 对裸哈希格式也会置 `Ready=true`、`Generation=""`。前提是 **server-files** 里出现 ArkApi 自己写的旧格式缓存（ArkApi 平时写的是实例镜像，不是源目录），较少见。**修复方案**：`sourceCacheManaged` 返回 `err == nil && res.Ready && res.Generation != ""`——我们写出的 metadata 一定带 generation，这正是「是不是我们备的」的结构性判据。

- **位置**：`pkg/arkcache/generation.go:52-54`、`:137-177`、`internal/mirror/mirror.go:80-88`、`:807`
- **触发条件**：`parseMetadata` 对裸哈希返回 `CacheDirectory=""`，`Inspect` 随后在 Cache 根找两个 `.cache` 并置 `Ready=true`；`sourceCacheManaged()` 只看 `Ready`，于是把 ArkApi 自己留下的历史格式缓存认成「我们备的」，使 `arkApiCache` 守卫失效。
- **后果**：权威性判断被错误翻转，可能删掉 ArkApi 运行期写入镜像的文件。
- **修复建议**：`sourceCacheManaged` 额外要求 `res.Generation != ""`。

#### [P2] 下载器不校验 206 的 `Content-Range` 起始偏移，错位续传会静默拼接

> 🔎 **复核：成立，维持 P2**（`pkg/download` 被 umu / GE-Proton / SLR / syncthing / arkcache 共用，影响面大于缓存本身）。`download.go:102-111` 收到 206 即按本地 `.part` 长度追加。
>
> **修复方案**：206 时解析 `Content-Range: bytes <start>-<end>/<total>`；头缺失、格式错误或 `start != startOffset` 时关闭 body、删除 `.part`，**不带 Range** 重发一次完整请求（在同一次 `fetchOnce` 内处理，不消耗重试次数）；`total` 可解析时用它校正进度的 `total`。单测用 `httptest` 返回错位的 `Content-Range`，断言最终文件内容正确。

- **位置**：`pkg/download/download.go:102-111`
- **触发条件**：收到 206 时直接按本地 `.part` 大小继续 `O_APPEND`，不校验 `Content-Range` 的 start。
- **后果**：字节被拼到错误偏移。
- **修复建议**：解析 `Content-Range` 并断言起点；不符则按 200 截断重下。

#### [P2] 快路径不清理源目录里其他哈希的旧代

> 🔎 **复核：成立，下调为 P3（仅占用磁盘）。** `arkcache.go:143-145` 命中即返回。**修复方案**：快路径返回前在 `commitMu` 下调用一次 `pruneGenerations(req.CacheRoot, hash, existing.Generation, req.Keep, false)`（一次 `ReadDir`，没有可删的就是空操作）。源目录的旧代删掉后，managed 模式的镜像同步会在下次启动时顺带删掉各实例镜像里的对应目录。

- **位置**：`pkg/arkcache/arkcache.go:140-145`、`:205`
- **触发条件**：`Prepare` 命中快路径直接 `return existing`，只有走到下载提交才 `pruneGenerations`。
- **后果**：源目录可能长期保留上一版 ARK 的 generation（数十到数百 MB）。
- **修复建议**：快路径命中后也执行一次 `pruneGenerations`（成本极低）。

### 5.3 文档 vs 代码偏差

1. **文档 §11.4「与镜像同步互斥」不成立**：`mirrorSyncMu` 只串行化同步本身；`PrepareArkApiCache` 在进入该锁之前写源，`PrefetchArkApiCacheAfterUpdate` 完全在锁外。
2. **文档 §5/§8 提到的落点与现状不符**：`internal/runner/vcredist.go` 等已迁到 `pkg/vcredist/`；`pkg/arkcache/loaderconfig.go` 已按 §22 移除（代码中确实无此文件）。
3. **`.lower-stamp` 注释与实现不一致**（见 §8.1）。
4. **overlay 模式的 VC++ 补装缺失**：`ARKAPI_LINUX_VCREDIST_PLAN.md §2.2` 强调的「快路径加 `prefixHasVCRedistOverrides` 判断」只在 per-instance 分支实现，overlay 分支提前返回未覆盖。
5. **`max_size` 的语义被高估**：文档 §14 称是「下载体与解压总量的上限」，下载器本身无硬上限。
6. **`Inspect` 的「裸哈希历史格式」破坏 `sourceCacheManaged` 语义**（见 P2）。

> 🔎 **复核（§5.3）**：第 3、4 条 ~~已修复~~（`2edd91c`，见 §8.1）；第 1 条随 §5.2 staging 方案修正为「generation 原子出现，镜像跳过 staging」而不是「与镜像同步互斥」；第 5 条随 `MaxBytes` 修复成立；第 2、6 条见对应条目。

---

## 6. ArkApi Linux 日志转抄与游戏 PID 识别

### 6.1 功能概述

(1) Linux 下把 ArkApi 自己写的 `ShooterGame/Binaries/Win64/logs/ArkApi_*.log` 增量转抄进实例的 `arkAsaApi.log`（Windows 由 PTY 直接落盘）。(2) 在 Wine/Proton 包装链里用「cmdline 含 `AltSaveDirectoryName=<savedir>` 且为 Windows 路径形式的已知 exe」筛候选，再用 `/proc/<pid>/comm == "GameThread"` 挑出真正的游戏进程，以 `launcherExited` 提前失败、以 `procx.KillTree` 收尾。进程判定在 `pkg/procmatch`，找/转抄在 `pkg/tail/waitnewest.go` 与 `pkg/iox/relay.go`。


### 6.2 发现

#### [P1] 日志转抄协程被 5 分钟超时 ctx 强制终止（相对原实现是回归）

> 🔎 **复核：成立，维持 P1。** `internal/instance/asaapilog_linux.go:119-151` 未变：同一个 `WithTimeout(5min)` 的 `ctx` 先给 `tail.WaitNewest`、再原样给 `iox.Relay`；`Relay` 在 `ctx.Done()` 后读完尾巴即退出（`pkg/iox/relay.go:43-47`）。
>
> **修复方案**
> - 拆成两个 ctx：`appearCtx`（`WithTimeout(arkApiLogAppearTimeout)`，只给 `WaitNewest`）与 `relayCtx`（`WithCancel(Background)`，只由 `done`——即 `launcherExited`——触发取消）。监听 `done` 的 goroutine 同时取消两者。
> - `Relay` 返回后写一行说明（见下方「转抄失败/超时后静默停止」一条）。
> - 把 `arkApiLogAppearTimeout` 改为包级 `var`，测试里设为 50ms：断言日志在「出现超时」之后仍被持续转抄，`done` 关闭后才停止。

- **位置**：`internal/instance/asaapilog_linux.go:119`、`:129`、`:149`
- **触发条件**：任何启用了 ArkApi 的 Linux 实例启动。`copyArkApiLog` 用 `context.WithTimeout(..., arkApiLogAppearTimeout)`（5 分钟）建 ctx，既传给 `tail.WaitNewest`（等文件出现），**又原样传给 `iox.Relay`（持续转抄）**。`iox.Relay` 在 `ctx.Done()` 后跑完最后一轮就读到 EOF 退出（`pkg/iox/relay.go:43-49`）。
- **后果**：一个正常运行的 ArkApi 实例（数小时）在启动约 5 分钟后，`arkAsaApi.log` 就再也收不到新行，「插件日志」面板从此冻结，用户只能看到开服前 5 分钟日志，且无任何提示。原始实现（提交 `2e01756`）里 `follow()` 只由 `done` 驱动、无 deadline，这是重构 `a9c0000` 引入的**行为退化**。
- **修复建议**：把「等出现」与「持续跟随」的取消信号分开：

```go
appearCtx, appearCancel := context.WithTimeout(context.Background(), arkApiLogAppearTimeout)
defer appearCancel()
srcPath, err := tail.WaitNewest(appearCtx, dir, launchedAt, isArkApiLogName, arkApiLogPollInterval)
...
relayCtx, relayCancel := context.WithCancel(context.Background())
defer relayCancel()
go func() { select { case <-done: case <-ctx.Done(): }; relayCancel() }()
iox.Relay(relayCtx, src, dst, arkApiLogPollInterval, ...)
```

#### [P1] marker 用 `AltSaveDirectoryName=` 前缀子串匹配，跨实例串扰；SaveDir 默认等于实例名，极易命中

> 🔎 **复核：成立，维持 P1。** `waitForGamePID`（`internal/instance/common.go:156`）与 `findServerPIDBySaveDir`（`:520`）都拼 `AltSaveDirectoryName=<saveDir>` 做子串匹配；Linux `procx.QueryProcess` 用 `strings.Contains`（`pkg/procx/procx_linux.go:80`），Windows 用 WQL `LIKE '%…%'`；`SaveDir` 默认等于实例名（`internal/config/config.go:408`）。参数由 `server.go:314-320` 生成，`AltSaveDirectoryName=<saveDir>` 是地图 URL 参数串的**最后一段**，后面紧跟参数分隔（Linux 的 cmdline 为空格/NUL，Windows 为空格或引号）。
>
> **修复方案**：建立**唯一的「这个进程属于哪个实例」判定**，本节后面几条都复用它。
> 1. `internal/instance` 新增 `saveDirToken(saveDir string) string` 与 `cmdlineHasSaveDir(cmdline, saveDir string) bool`：找出每一处 `AltSaveDirectoryName=<saveDir>`，要求紧随其后的字符是串尾、空白、`\x00`、`"` 或 `?`，任一处满足即为真。
> 2. `pkg/procmatch.Matcher.Find(cmdlineMarker)` 增加一个过滤参数：`Find(prefilter string, accept func(cmdline string) bool)`——`prefilter` 仍交给 `QueryProcess` 做廉价的子串/WQL 预筛，`accept` 做精确的边界判定。`internal/instance` 两处调用都传 `cmdlineHasSaveDir`。
> 3. 单测（`pkg/procmatch/procmatch_test.go` 与 `internal/instance`）：`srv` 与 `srv2` 两条真机形态的 cmdline，`srv` 只能匹配自己；带引号、带 `?` 后缀、位于串尾三种形态都要覆盖。

- **位置**：`internal/instance/common.go:156`（`marker := fmt.Sprintf("AltSaveDirectoryName=%s", saveDir)`）、`pkg/procmatch/procmatch_linux.go:24`（`procx.QueryProcess("", cmdlineMarker)` 做 `strings.Contains`）
- **触发条件**：两个实例名互为前缀，如 `srv` 与 `srv2`。`SaveDir` 默认值就是实例名（`internal/appconfig/config.go:398`），于是 `srv2` 的进程 cmdline 里的 `AltSaveDirectoryName=srv2` **包含**子串 `AltSaveDirectoryName=srv`，会被实例 `srv` 的 `waitForGamePID`/`findServerPIDBySaveDir` 当成自己的进程收下。
- **后果**：`srv` 启动时把 `srv2` 的游戏 PID 存进自己的 pid 文件；随后对 `srv` 的停止/强制停止会去 kill `srv2` 的进程，造成**杀错实例**。Windows 侧同样按子串匹配，问题等价。
- **修复建议**：匹配要求 token 边界（正则 `AltSaveDirectoryName=srv(?:\?|\s|$)` 替代 `strings.Contains`）；或改用每次启动唯一的随机 token 作标记。

#### [P1] 失败收尾对「已被 Wait 回收」的 launcher PID 调 `KillTree`，PID 复用时会杀掉无关进程

> 🔎 **复核：成立，下调为 P2；另发现一个更实际的同源问题。** 代码未变（`server.go` 中 `handle.Wait()` 后 `close(launcherExited)`，失败分支 `procx.KillTree(handle.LauncherPID)`）。Linux 默认 `pid_max` 为 4194304，毫秒级复用几乎不可能；Windows 复用 PID 快得多，风险主要在那边。**更实际的问题**：launcher 已退出时它的后代早已被 reparent，`processTree(deadPID)` 只返回 `[root]`、`Getpgid` 也失败，`KillTree` 对**真正的残留进程树**无能为力——「加载器秒退后留下孤儿 Wine 进程」恰恰没被收掉。
>
> **修复方案**
> 1. `ErrLauncherExited` 分支不再 `KillTree(handle.LauncherPID)`，改为按实例身份清场：`QueryProcess` 找出 cmdline 满足 `cmdlineHasSaveDir(…, config.SaveDir)`（上一条）的**所有**进程（loader、游戏、umu 包装链都带这串参数），逐个 `KillTree`。这既不依赖已失效的 PID，也不会误伤别的实例。
> 2. 其余失败分支（launcher 仍在）保留 `KillTree(LauncherPID)`，但先校验身份：`procx` 新增 `ProcessStartTime(pid) (uint64, error)`（Linux 读 `/proc/<pid>/stat` 第 22 字段，Windows 用 `GetProcessTimes`），`runner.Handle` 在 `Run` 成功后记录 `LauncherStart`，kill 前比对不一致就跳过并记 WARN。

- **位置**：`internal/instance/server.go:607-613`（`handle.Wait()` 回收后 `close(launcherExited)`）、`:641`、`:649`（`procx.KillTree(handle.LauncherPID)`）
- **触发条件**：加载器/umu-run 秒退（ArkApi 常见失败形态）。`handle.Wait()` 返回即表示子进程已被 `wait` 回收，其 PID 立即可能被内核复用；随后 `ErrLauncherExited` 分支执行 `KillTree(handle.LauncherPID)`。`procx.processTree` 对不存在的 pid 只返回 `[root]`（`pkg/procx/procx_linux.go:211-239`），`signalTree` 会直接对这个**已被复用**的 PID 发 SIGKILL（`:194-201`）。
- **后果**：在繁忙主机上可能连带 SIGKILL 一个完全无关的新进程及其子树。代码中没有任何 starttime/cmdline/PID 归属校验（全仓 `starttime` 仅出现在文档/改名，**无实现**）。
- **修复建议**：启动时记录 launcher 的 `/proc/<pid>/stat` 第 22 字段 starttime（或 `pidfd_open`），kill 前比对；或在 `launcherExited` 已触发时不 `KillTree(LauncherPID)`，改为按保存的游戏 PID/procmatch 再查一次。

#### [P1] 停止/强制停止对未校验的保存 PID 直接 kill，PID 复用会误杀

> 🔎 **复核：成立。`ForceStopServer` 维持 P1，正常停止收尾那次 `Kill(pid2)` 为 P3。** `ForceStopServer` 对 `GetAsaServerApiPID`/`GetInstancePID`/`GetLauncherPID` 无条件 `killGameServer`，而 PID 文件从不清理——强制停止往往发生在崩溃很久之后，Windows 上 PID 早已被复用。正常停止收尾（`stopServerInternal` 的 `procx.Kill(pid2)`）发生在进程刚退出之后，复用概率低，而且此时游戏已退出，这一刀本身多余。另外 `isExpectedProcess`（`internal/process/process.go:146`，Linux 版看 cmdline 是否含 `arkascendedserver.exe`/`asaapiloader.exe`）只能证明「是个游戏进程」，证明不了「是**这个实例**的」。
>
> **修复方案**
> 1. `internal/process` 新增 `VerifiedPID(instanceName, kind) (int, bool)`（`kind` ∈ game / launcher / asaapi）：读 PID 文件 → 进程存活 → `isExpectedProcess` → cmdline 满足该实例的 `cmdlineHasSaveDir`（边界判定函数下沉到 `process` 包，`instance` 复用；Linux 的 umu-run 与 loader 的 cmdline 同样带这串参数）。
> 2. `ForceStopServer` 与 `stopServerInternal` 一律经 `VerifiedPID` 取 PID，校验不过就跳过并记 INFO。
> 3. 删除 `stopServerInternal` 收尾那次 `Kill(pid2)`（游戏已确认退出）；如确需清理残留 loader，用 `VerifiedPID(…, asaapi)`。
> 4. 停止成功（正常或强制）后调用新增的 `procpkg.ClearInstancePIDs(name)` 删除三个 PID 文件，从源头消除陈旧 PID。
> 5. `IsInstanceProcessAlive` 的方法 2 同样改用 `VerifiedPID`，否则别的实例的游戏进程会让本实例被误判为存活。

- **位置**：`internal/instance/server.go:849-851`（`procx.Kill(pid2)`）、`:877`/`:882`/`:888`（`ForceStopServer` 对 `findServerPIDBySaveDir`、`GetInstancePID`、`GetLauncherPID` 无条件 `killGameServer`）
- **触发条件**：实例正常停止或崩溃后 PID 文件不清理（`process.SaveInstancePID` 无删除逻辑），号码被内核复用给新进程。
- **后果**：误杀无关进程。项目其它路径已用 `isExpectedProcess`（`internal/process/process.go:146-148`）防复用，唯独这两处收尾 kill 没有。
- **修复建议**：kill 前套用同一判据 `if isExpectedProcess(pid) { procx.Kill(pid) }`；停止成功后清空 `pid`/`launcher_pid`/`asa_api_pid` 文件。

#### [P1] ArkApi 下 `PIDByPort` 可能归属到共享 wineserver，停止时会误伤整个 Wine 会话

> 🔎 **复核：成立，维持 P1，而且不限于 ArkApi。** `stopServerInternal` 用 `procx.PIDByPort(config.Port)`，`KillServer` 同样如此。`PIDByPort`（`pkg/procx/port.go:17-28`）返回第一个持有该端口 socket 的进程；wineserver 会持有 Wine 进程 socket 的副本，而 gopsutil 按 `/proc` 目录（按名字排序）遍历建立 inode→pid 映射，wineserver 通常比游戏先启动、PID 更小，因此**更可能被选中**。只要是经 Wine 运行（Linux 上所有实例），shared 模式下停 A 就可能在 5 分钟超时后 `KillTree(wineserver)` 带走 B。
>
> **修复方案**
> 1. 停止路径的 PID 解析顺序：`gameProcMatcher.Find(…, cmdlineHasSaveDir)`（Linux 以 `comm == GameThread` 选出真正的游戏进程）→ `VerifiedPID(name, game)` → 都失败才报「找不到游戏进程」。**不再用端口定位要杀的进程**；`IsServerRunning` 仍可用端口判断「是否在服务」，那只是只读判断。
> 2. `KillServer` 同样改用上述解析。
> 3. 兜底：`procx` 新增 `PIDsByPort(port) []int`，确需按端口定位时只接受 `isExpectedProcess` 且满足实例标记的那一个，永远排除 comm 为 `wineserver`/`wineserver64` 的进程。
> 4. WSL 回归：shared 模式起两个实例，停止其中一个，另一个必须存活，日志里的停止 PID 必须是 `GameThread`。

- **位置**：`internal/instance/server.go:791`（`procx.PIDByPort(config.Port)`）、`:811`（`Terminate(pid)`）、`:843`（超时后 `KillTree(pid)`）；`pkg/procx/port.go:17-28`（返回第一个命中者）
- **触发条件**：文档 §2.2 的真机 `ss -lunp` 显示游戏端口 `41778` 同时被 `("GameThread", pid=2722)` 与 `("wineserver", pid=2636)` 持有。gopsutil 遍历 `/proc/*/fd` 匹配 socket inode 时归属顺序不确定，可能返回 wineserver。`prefix_mode=shared` 下这个 wineserver 是所有实例共用的。
- **后果**：停止实例 A 时若 `pid` 取到共享 wineserver，`waitServerStopped` 会等 A 的日志 `Log file closed` **且** 该 pid 退出；wineserver 因 B 仍在运行不会退出 → 5 分钟超时 → `KillTree(wineserver)` 把共享 wineserver 杀掉，导致**实例 B 一起挂掉**。
- **修复建议**：停止路径改用已保存且经校验的游戏 PID（`GetInstancePID` + `isExpectedProcess`），或复用 `gameProcMatcher.Find(marker)`（comm=GameThread）定位；至少在终止前校验该 PID 的 comm 是 `GameThread`。

#### [P1] `launchgate` 可能被永久持有：`startServerInternal` 的等待 select 没有 ctx/超时，且闸门本身无超时

> 🔎 **复核：成立，维持 P1（shared 模式下阻塞此后所有实例的启动）。** `select { case <-initFailed: case <-initSuccessful: }` 仍无 `ctx.Done()`/超时；`waitServerStartup`（`common.go:467-515`）的失败回调只在进程退出时触发；此外它在 `tail.WithCallback` **立即失败**时只记 WARN，然后在 `<-startup` 上一直等到进程退出——同样不会发出失败信号。API 启动传入的是 `context.Background()`，单加 `ctx.Done()` 解决不了问题，**必须有超时**。
>
> **修复方案**
> 1. 新增配置 `server.startup_init_timeout`（默认 20 分钟，大地图 + 首次 mod 下载留足余量），`startServerInternal` 的等待改为：
>    ```go
>    select {
>    case err := <-initFailed: return err
>    case <-initSuccessful:
>    case <-ctx.Done(): return ctx.Err()
>    case <-time.After(initTimeout):
>        logger.Warnf("实例 %s 在 %s 内没有到达 start_initialization_successful，放行启动闸门；进程保留，由用户决定是否停止", …)
>        releaseLaunchGate()
>        return fmt.Errorf("启动初始化超时（%s）", initTimeout)
>    }
>    ```
>    超时**不杀进程**（可能只是慢），只放闸门并让启动调用返回；状态写 `start_failed` 加说明。`waitServerStartup` 的回调仍在后台运行，之后若真的到达初始化/完成会照常把状态改成 `started`。
> 2. `opts.WaitServerCompleted` 的 `<-startupSuccess` 同样加 `ctx.Done()` 与一个独立的完成超时。
> 3. `waitServerStartup` 中 `tail.WithCallback` 返回错误时调用 `callback(false, "无法跟踪游戏日志：…")` 并 `safeCloseStartup()`，让失败立刻可见。
> 4. 闸门本身（`pkg/resourcegate`）不加超时：持有时长由上面的调用方超时封顶，这与 `UMU_PREFIX_PLAN.md` §8.3 的「持有时间天然被封顶」承诺对齐，文档随之改为「由 startup_init_timeout 封顶」。

- **位置**：`internal/instance/server.go:708-712`（`select { case <-initFailed: case <-initSuccessful: }` 无 `ctx.Done()`）、`:548-555`；`internal/instance/launchgate.go:42-52`；`internal/webapi/serverapi/serverapi.go:407-414`（未传 `WithCtx`，`ParentCtx` 默认 `context.Background()`，见 `server.go:213`）
- **触发条件**：游戏进程起来了，但 `ShooterGame.log` 始终没有 `Initialize Primal Game Data Override`，且进程一直活着。此时 `initSuccessful`/`initFailed` 都不触发（`waitServerStartup` 的失败回调只在进程退出时发，`common.go:482-493`）。
- **后果**：该实例的 `startServerInternal` 永久阻塞，`defer` 的释放永不执行；ctx 是 `Background` 派生也不会取消，后续所有共享 prefix 的启动在 `acquireLaunchGate` 上**永久排队并泄漏协程**。
- **修复建议**：`select` 中加 `case <-ctx.Done(): return ctx.Err()`，并给整段启动加有上限的 timeout；`opts.WaitServerCompleted` 的 `<-startupSuccess`（`:720`）同样要带 ctx。

#### [P2] `killGameServer` 的 SIGTERM→SIGKILL 升级是假的

> 🔎 **复核：成立（仅 Linux），维持 P2。** Linux 的 `TerminateTree` 只有投递失败才返回错误（`pkg/procx/procx_linux.go:149-203`），升级分支永远走不到。Windows 不受影响：`taskkill /T` 不带 `/F` 对无窗口的控制台进程会返回错误，恰好触发 `KillTree`。
>
> **修复方案**：`procx` 新增 `TerminateTreeGracefully(pid int, grace time.Duration) error`：**先快照一次进程树**，对快照发 SIGTERM，轮询至多 `grace`（默认 15 秒），对快照中仍存活的成员发 SIGKILL。必须复用同一份快照——父进程先退出后子进程被 reparent，第二次 `processTree(pid)` 就找不到它们了。Windows 实现为 `taskkill /T` → 等待 → `taskkill /F /T`。`killGameServer` 改调它。

- **位置**：`internal/instance/common.go:113-118`
- **触发条件**：`procx.TerminateTree(pid)` 当 SIGTERM 投递成功时返回 nil（`pkg/procx/procx_linux.go:165-203`，只有发送失败才 error），即使目标忽略 SIGTERM。于是分支 `if err != nil { procx.KillTree(pid) }` 永远不走。
- **后果**：忽略/延迟 SIGTERM 的 Wine 进程存活成孤儿，「强杀」不生效，端口与 Wine 会话被占用。
- **修复建议**：`TerminateTree` 后用 `procx.WaitProcessExit(ctx, pid, 间隔)` 轮询，超时再无条件 `KillTree`。

#### [P2] 多个 `GameThread` 候选时按 `/proc` 字典序取首个，可能命中旧失败残留进程

> 🔎 **复核：成立，下调为 P3。** 修复 §6.2 的边界匹配后，多个候选只可能是**同一实例**上次失败留下的残留（上面「launcher 已退出时收不掉残留」一条修复后也会大幅减少）。**修复方案**：`pick` 在同一 tier 有多个候选时，优先选择 `handle.LauncherPID` 的后代（`waitForGamePID` 把 launcher PID 传入 `Find`）；都不是后代时选 `ProcessStartTime` 最新者；并对被放弃的候选记一条 WARN（「发现实例 X 的残留游戏进程 PID …」）。

- **位置**：`pkg/procmatch/procmatch.go:77-82`、`pkg/procmatch/procmatch_linux.go:23-39`（候选顺序来自 `os.ReadDir("/proc")` 字典序）
- **后果**：可能选中陈旧进程 PID，`SaveInstancePID` 存错，停止时杀错对象。
- **修复建议**：候选多于一个时用 starttime 最新者或与 `handle.LauncherPID` 进程树就近判定。

#### ~~[P2] `Relay` 只持有 fd、不跟踪 inode，ArkApi 日志被替换/轮转后不再跟随~~

> 🔎 **复核：不成立（前提不存在）。** 被转抄的是本次启动新建的 `ArkApi_<pid>_<时间>.log`：文件名每次启动都不同，ArkApi 在一次进程生命周期内不做轮转；它位于实例镜像内，而镜像只在启动前同步，运行期间不会被重建或替换。`pkg/tail` 的 `fileKey` 处理的是长期存在、会被外部轮转的日志，与这里的场景不同。若将来观测到 ArkApi 版本引入了进程内轮转，再按原文方案处理。

- **位置**：`internal/instance/asaapilog_linux.go:142-149`（`os.Open` 一次后 `iox.Relay`）、`pkg/iox/relay.go:20-50`
- **后果**：转抄停止更新。项目里 `pkg/tail` 有 `fileKey`（inode+dev）做轮转检测，这里未复用。
- **修复建议**：转抄循环定期 `Stat` 当前路径并按 `fileKey` 比对 inode，变化则重新 `Open`。

#### [P2] `launcher.log` 的读取可能晚于 PTY 关闭，快速退出的加载器会丢诊断输出

> 🔎 **复核：成立，维持 P2，但机理与原文不同。** 读取协程与 `Wait` 协程几乎同时启动，「读取晚于关闭」不是主要问题；真正的问题是 **`Wait` 协程在 launcher 退出后立刻 `handle.PTY.Close()`**，而此时 PTY 主端的内核缓冲里可能还有读取协程没读完的输出——秒退的加载器最后几行（恰恰是最有诊断价值的）会被丢弃。
>
> **修复方案**：PTY 的关闭权交给**读取方**。`Wait` 协程不再关闭 PTY；`startAsaApiLogging` 的清洗协程在 `CleanScreenOutput` 返回后（从端全部关闭、缓冲读尽后主端读到 EIO/EOF）`defer` 关闭 PTY。没有读取方的路径（见下方「launcher.log 打开失败」一条）由排空协程关闭。Windows 侧（`asaapilog_windows.go`）同样把关闭挪到读取方，保持两平台一致。

- **位置**：`internal/instance/server.go:607-613`（`handle.Wait()` 后 `handle.PTY.Close()`）与 `:619`（之后才起读取协程）
- **后果**：恰在最需要它的「加载器零输出/退出码 3」排障场景下 `launcher.log` 可能是空的。
- **修复建议**：把 PTY 读取协程提前到 `runner.Run` 返回后、`Wait` goroutine 之前；或不要把 PTY 关闭放在 `Wait` goroutine 里。

#### [P2] `conflictingArkApiInstance` 以端口判「在运行」，漏掉 starting 窗口

> 🔎 **复核：成立，与 §3.2「ArkApi 冲突检查用端口判活且位于闸门之前」为同一问题（P1），修复方案见那一条。**

- **位置**：`internal/instance/launchgate.go:121`、`internal/instance/server.go:467`
- **修复建议**：改用 `procpkg.IsInstanceProcessAlive` / `ListAliveInstances`（覆盖 starting 阶段，`process.go:172-192`）。

#### [P2] 转抄失败/超时后静默停止，无说明行

> 🔎 **复核：成立，下调为 P3（可观测性）。** `asaapilog_linux.go:149-151` 之后没有任何 `note`。**修复方案**：`iox.Relay` 增加返回值 `(reason string)`（`"done"` / `"write: …"` / `"read: …"`），`copyArkApiLog` 据此写一行：`done` → 「[asa-server] 启动链已结束，停止转抄」；其余 → 「[asa-server] 转抄中断：…」并保留现有的 logger WARN。修复 §6.2 首条后不会再有「超时结束」这种情形。

- **位置**：`internal/instance/asaapilog_linux.go:149-151`
- **修复建议**：Relay 返回后向 `dst` 写一行 `[asa-server] …` 说明（区分「启动链结束」「等待/转抄超时」）。

#### ~~[P2] `processComm` 读失败时静默丢候选~~

> 🔎 **复核：不成立。** `processComm` 读失败返回空串（`pkg/procmatch/procmatch_linux.go:47-53`），但候选**仍被加入** `candidates`（`:31-34`）；`pick` 第一层按 comm 匹配不到它，会落到第二层「cmdline 含 `\ArkAscendedServer.exe`」继续判定（`procmatch.go:77-86`）。没有任何候选被丢弃。无需修改。

- **位置**：`pkg/procmatch/procmatch_linux.go:47-53`
- **修复建议**：读失败时记一条 debug 日志，或区分「读不到 comm」与「comm 不匹配」。

#### ~~[P2] `waitForGamePID` 的轮询 goroutine 与 `time.After` 计时器未及时回收~~

> 🔎 **复核：不成立。** 轮询协程每轮检查 `ctx.Err()` 并在 `select` 里等 `ctx.Done()`（`internal/instance/common.go:168-194`）；传入的 `ctx` 是 `startServerInternal` 的 `WithCancel` 子 ctx，函数返回时 `defer cancel()` 即取消，协程随之退出。`time.After` 创建的计时器自 Go 1.23 起在不再被引用时即可回收（本项目 `go.mod` 为 1.27），不存在泄漏。无需修改。

- **位置**：`internal/instance/common.go:168-194`、`:210`
- **修复建议**：用可取消子 ctx 显式终止轮询 goroutine；超时改用 `time.NewTimer` + `defer Stop()`。

#### [P2] `launcher.log` 路径解析/打开失败时整段丢弃启动输出，无兜底

> 🔎 **复核：成立，维持 P2。** `asaapilog_linux.go:79-89` 两个失败分支都不读 PTY；PTY 缓冲写满后启动链的输出写入会阻塞，可能把整条启动链卡住。**修复方案**：两个失败分支启动排空协程 `go func() { _, _ = io.Copy(io.Discard, ptyStream); closePTY() }()`（关闭权按上面「PTY 关闭」一条交给读取方），并在 WARN 里说明「启动链输出已丢弃」。

- **位置**：`internal/instance/asaapilog_linux.go:79-89`
- **后果**：PTY 流没有读取者，启动输出完全丢失（且 PTY 不读可能导致缓冲写满、阻塞子进程输出）。
- **修复建议**：打开失败时至少启动 `io.Copy(io.Discard, ptyStream)` 的排空协程。

#### [P2] `instanceLogFilePath` 的 Stat+WriteFile 有竞态且会为从未启动的实例创建空文件

> 🔎 **复核：部分成立，下调为 P3。** 「为从未启动的实例创建空文件」是**有意的**（日志 SSE 端点 tail 前需要文件存在），不算缺陷。竞态属实但影响极小：`Stat` 与 `WriteFile(nil)`（带 `O_TRUNC`）之间若另一方已经写入内容，会被截断。**修复方案**：改为 `f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0644); f.Close()`——不存在就创建、存在不截断，同时把被忽略的 `WriteFile` 错误改为返回。

- **位置**：`internal/instance/server.go:72-82`
- **修复建议**：改为 `OpenFile(O_CREATE)` 后立即关闭。

#### ~~[P2] `signalTree` 的进程组清扫先于叶优先顺序~~

> 🔎 **复核：不成立。** `signalTree`（`pkg/procx/procx_linux.go:165-203`）在发出任何信号**之前**就完成了 `processTree` 快照，之后的组清扫与逐个发送都基于这份快照，顺序不影响被信号覆盖的集合；组清扫只针对「由快照成员领头」的进程组，不会越界。叶优先只是缩小 reparent 窗口的额外保险（注释里已说明即使顺序相反也会按 PID 补发）。无需修改。

- **位置**：`pkg/procx/procx_linux.go:180-187`
- **修复建议**：把进程组清扫放到叶优先循环之后。

### 6.3 文档 vs 代码偏差

1. **文件布局**：文档 §3 声称的 `internal/instance/arkapilog.go`、`gameproc.go`、`gameproc_linux.go`、`gameproc_windows.go` **都不存在**——实际在 `pkg/procmatch/`、`pkg/tail/waitnewest.go`、`pkg/iox/relay.go`。
2. **函数/类型名**：`isWineSideGameCmdline`/`pickGameProcess`/`gameCandidate`/`gameProcessComm` → 实际 `procmatch.Matcher.isWineSideCmdline`/`pick`/`candidate`/`processComm`。
3. **哨兵错误**：`newestArkApiLog`/`ErrNoArkApiLog` 不存在，实际靠 `errors.Is(err, context.DeadlineExceeded)` 二分（`asaapilog_linux.go:133`）。
4. **协程生命周期描述不符（且是回归）**：文档说「协程生命周期绑在 `launcherExited` 上」，实际绑在**带 5 分钟超时的 ctx** 上（见 P1）。
5. **单测名与位置**：文档列的 `TestNewestArkApiLog*`/`TestPickGameProcess*` 实际为 `pkg/tail/waitnewest_test.go` 与 `pkg/procmatch/procmatch_test.go`，**均不在 `internal/instance/`**。
6. **未记载的交互面**：文档完全没提 `internal/instance/launchgate.go`，而它是本功能最重要的相邻交互面。
7. **「不静默」部分成立**：等待/找不到/找到了有 `note`，但转抄正常结束或因超时结束时没有说明行。

> 🔎 **复核（§6.3）**：第 4、7 条随 §6.2 对应修复消除；第 1、2、3、5 条为重构后的命名/路径漂移，`CLAUDE.md` 已按实际路径（`pkg/procmatch`、`pkg/tail`、`pkg/iox`）描述，`ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md` 的审计附录需同步；第 6 条在该文档补一节「与 launchgate 的交互」，内容即 §3.2 冲突判定与 §6.2 闸门超时两条的结论。

---

## 7. ArkApi 插件按实例安装与插件数据

### 7.1 功能概述

插件从「server-files 全局一份」改为「每实例一份」：装在 `instances/{name}/ArkApi/Plugins`，镜像里 `Win64/ArkApi/Plugins` 建成指向它的 junction（Linux 为 symlink）；禁用插件挪到 `PluginsDisabled`；zip 上传经暂存区校验后按实例安装/更新/卸载。代码分布在 `internal/arkapimanage`（编排）、`internal/plugindata`（布局/迁移/锁/分类）、`internal/mirror`（例外 junction）、`internal/webapi/pluginapi`（HTTP），接在 `internal/instance/server.go` 的启动/停止路径上。**整体设计（原子提交、结构性关断、每实例锁、备份保留 3 份）基本落地**；缺陷如下。

### 7.2 发现

#### [P1] 启动迁移用「端口是否监听」判断实例在跑，会漏判 starting 阶段/残留进程，对活库做收割

> 🔎 **复核：成立，维持 P1。** `internal/instance/pluginlayout.go:33` 仍是 `procpkg.IsServerRunning(name)`（纯端口）。asa-server 重启时正在启动（端口未绑）或已脱离管理仍在运行的实例会被当作已停止并迁移。
>
> **修复方案**
> 1. 判据改为 `procpkg.IsInstanceProcessAlive(name)`（采用 §6.2 的 `VerifiedPID` 版本，避免被别的实例的进程误判），**或**状态处于任一启动中 / 运行中状态（`statepkg.GetInstanceStateIsStart` 或同义判断）——两者任一成立即推迟迁移并计入 `pending`。
> 2. `plugindata` 不能依赖 `process`/`state`（它在两者之下），在 `MigrateInstance` 上加一个 `isRunning func() bool` 形式的护栏参数由调用方注入：`PrepareForStart` 路径传「恒 false」（启动前必然已停止，这是唯一允许迁移的时机），`MigratePluginLayouts` 传上面的判据。这样护栏在包内生效，未来新增的调用方也必须显式表态。

- **位置**：`internal/instance/pluginlayout.go:33`（`if running, _ := procpkg.IsServerRunning(name); running {`）
- **触发条件**：asa-server 重启时，某实例的游戏/加载器进程已存在但端口尚未绑定（ARK 启动到绑定端口常需数分钟；或上次强杀后 Wine 进程树残留）。`procpkg.IsServerRunning` 只查端口（`internal/process/process.go:116-126`，注释明确「port-only 会漏掉 starting 阶段」）。
- **后果**：`MigrateInstance` 对**运行中**的实例执行第 1 步 `harvest`，整组拷贝正在被写入的 SQLite 文件组（`layout.go:153-157`），拿到撕裂副本；随后镜像真实 `Plugins` 目录会被 `migrateExceptionJunctions` 的 `RemoveAll` 删除（`mirror.go:380`），活数据被破坏。同一文件里 `installer.ListAliveInstances` 用的是 `IsInstanceProcessAlive`，此处判据不一致。
- **修复建议**：改用 `procpkg.IsInstanceProcessAlive(name)`（或 `ListAliveInstances`）；并在 `MigrateInstance` 内部加一道「实例状态非 stopped/进程存活即返回错误」的护栏。

#### [P1] `MirrorPluginsDir` 硬编码大小写，Linux 上使迁移抢救与旧布局搬运静默失效

> 🔎 **复核：成立，下调为 P2。** `MirrorPluginsDir`（`internal/plugindata/plugindata.go:76-78`）仍拼常量 `ArkApi/Plugins`，而 `SourcePluginsRelPath`（`inspect.go:53-58`）与镜像例外 junction（`mirror.pluginsExceptionRelPath`）按盘上实际大小写，两者不一致时的后果如原文所述。下调理由：需要 Linux 上 server-files 的目录大小写与官方包不同（官方 zip 与 `arkapimanage` 安装都写 `ArkApi/Plugins`），只出现在手工解压的非常规安装上。
>
> **修复方案**：`MirrorPluginsDir(mirrorDir)` 改为 `filepath.Join(mirrorDir, filepath.FromSlash(SourcePluginsRelPath()))`——镜像的大小写由同步从 server-files 复制而来，按构造与源一致；`pluginsRelPath` 常量只保留给 `actualChildName` 找不到时的默认名。WSL 单测：server-files 下建 `arkapi/plugins`（全小写），断言 `listMirrorPlugins` 能列出、`migrateInstance` 能抢救到镜像里的数据。

- **位置**：`internal/plugindata/plugindata.go:43`（`pluginsRelPath = win64RelPath + "/ArkApi/Plugins"`）、`:76-78`（`MirrorPluginsDir`）；使用点 `internal/plugindata/layout.go:154`、`plugindata.go:83/126/176`
- **触发条件**：大小写敏感文件系统（Linux）上 server-files 落盘为 `arkapi/`（手工解压/SteamCMD 变体）。镜像里的路径按盘上实际大小写建成 `arkapi/Plugins`（`SourcePluginsRelPath`，`inspect.go:53-58` 正确），而 `MirrorPluginsDir` 拼的是 `ArkApi/Plugins` → `os.ReadDir` 失败。
- **后果**：`listMirrorPlugins` 返回 nil，`migrateInstance` 第 1 步的 `harvest`、旧布局的 `Rescue`/`Reclaim` 全变空操作；紧接着 `migrateExceptionJunctions` 会 `RemoveAll(mirrorPath)`（用的却是实际大小写，`mirror.go:380`）——镜像里上一轮崩溃遗留、还没被抢回的新数据被直接删除（静默丢数据）。这正是 `LINUX_COMPATIBILITY_PLAN.md §5.12` 第 1 条，但代码只做只读告警（`casecheck_linux.go:23`），没修判定本身。
- **修复建议**：`MirrorPluginsDir` 改为复用 `SourcePluginsRelPath()`（或按 `actualChildName` 动态解析 ArkApi/Plugins 两级）。

#### [P1] 更新插件的两次 rename 不具崩溃原子性，崩溃窗口内插件「消失」

> 🔎 **复核：成立，下调为 P2。** `internal/arkapimanage/plugin.go:184-196` 的两次 rename 之间崩溃会让 `Plugins/<N>` 缺失；窗口只有两次系统调用之间，且旧版本（含配置与数据）完整留在 `ArkApi/Backups/<N>-<ts>`，可恢复。原文的「三步 rename」仍有同样的窗口，不能解决问题。
>
> **修复方案**：落一份可恢复的 journal，在下次拿实例锁时补完。
> 1. 第一次 rename 前写 `ArkApi/.plugin-txn.json`：`{plugin, finalDir, backup, staged}`（`writeFileAtomic`）。
> 2. 两次 rename 都成功后删除 journal，再清理临时目录。失败路径的 `defer os.RemoveAll(tmp)` 只在「已回滚」或「已完成」时执行——journal 仍在（即崩溃）时 `tmp` 必须保留，因为 `staged` 就在里面。
> 3. `plugindata.TryLockInstance`/`LockInstance` 拿锁后调用 `recoverPluginTxn(instance)`：`finalDir` 不存在且 `staged` 存在 → `rename(staged, finalDir)`（完成更新）；`staged` 也不存在而 `backup` 存在 → `rename(backup, finalDir)`（回滚）；`finalDir` 已存在 → 只清理 journal 与 `tmp`。每一步记 INFO。启动路径 `PrepareForStart` 也经过这把锁，因此崩溃后第一次启动前一定会被补完。
> 4. 单测：在两次 rename 之间注入 panic（测试钩子），重新拿锁后断言插件目录恢复为新版本。

- **位置**：`internal/arkapimanage/plugin.go:185-198`
- **触发条件**：更新已安装插件时，`os.Rename(oldDir, bak)` 成功后、`os.Rename(staged, finalDir)` 前进程被杀/崩溃（或第二次 rename 失败但回滚也失败）。注意 `defer os.RemoveAll(tmp)`（`:158`）在失败路径会连新组装的内容一起删掉。
- **后果**：`Plugins/N` 不存在，插件显示为「未安装」（数据与配置仍在 `ArkApi/Backups/N-<ts>`，只能靠备份恢复）。
- **修复建议**：改成「staged→临时新名（同目录）→ old→bak → 新名→finalDir」三步，保证任一时刻 `finalDir` 要么旧版要么新版；或落可恢复 journal 并在下次启动补完。

#### [P1] 禁用/启用只写配置不落位时，`instanceBusy` 之外还有「另一个插件操作占用锁」的情况，返回原因与提示不符

> 🔎 **复核：成立，下调为 P3（仅提示文案）。** `SetPluginEnabled`（`internal/arkapimanage/arkapimanage.go:64-71`）在 `TryLockInstance` 失败时返回 `applied=false`，HTTP 层统一说「实例运行中或正在启动」。行为本身正确：配置已写入，下次启动由 `PrepareForStart` 落位，与提示的「下次启动生效」一致；只有原因说错了。
>
> **修复方案**：`SetPluginEnabled` 返回 `(applied bool, pending string, err error)`，`pending` ∈ `"running"`（`instanceBusy` 非空，附带其原文）、`"locked"`（拿不到锁）；`pluginapi` 分别提示「实例运行中或正在启动」「另一个插件操作正在进行」，两者都说明「设置已保存，将在下次启动该实例时生效」，并把 `pending` 放进 `data` 供前端区分。

- **位置**：`internal/arkapimanage/arkapimanage.go:65-68` 与 `internal/webapi/pluginapi/pluginapi.go:69-72`
- **触发条件**：另一插件安装/卸载正持锁，此时拨开关 → `TryLockInstance` 失败 → 返回 `(false, nil)`。
- **后果**：响应统一文案「实例运行中或正在启动，将在下次启动生效」，实际是「另一插件操作正在进行」，可能误导。
- **修复建议**：区分 `TryLock` 失败与 `instanceBusy` 两种情况，返回不同 message / `reason` 字段。

#### [P2] 卸载插件不清理该插件的在线快照目录，重装后展示陈旧快照

> 🔎 **复核：成立，下调为 P3。** `uninstallFrom`（`plugin.go:244-278`）只挪插件目录；快照在 `InstanceSnapshotsDir(instance)/<plugin>/`（`layout.go:74`、`snapshot.go:136`），不受影响。**修复方案**：卸载时把 `InstanceSnapshotsDir/<plugin>` 一并 `movePath` 进同一个备份目录的 `.snapshots/` 子目录；`install` 走 `restoreFromBackup` 时若备份里有 `.snapshots/` 则挪回，否则保持无快照。挪动失败只记 warning（快照不是权威数据）。

- **位置**：`internal/arkapimanage/plugin.go:244-278`
- **后果**：`instances/{name}/ArkApi/PluginSnapshots/<plugin>/` 残留，重装后新旧快照混在一起。
- **修复建议**：卸载时把 `InstanceSnapshotsDir/<plugin>` 一并移入该插件备份目录。

#### [P2] 内部 API 不校验实例名，安全完全依赖调用方

> 🔎 **复核：成立（纵深防御缺失），下调为 P3。** 当前唯一的外部入口 `pluginapi` 已对 `targets`、`bundled`、路径参数逐个 `ValidateInstanceName`（`internal/webapi/pluginapi/arkapi.go:158, 173, 239, 252-257`），今天没有可利用路径。**修复方案**：把实例名校验下沉到 `internal/config`（`cfgpkg.ValidateInstanceName`，`apiresp.ValidateInstanceName` 改为转调它——`arkapimanage` 不能依赖 `webapi`），`lockForPluginWrite` 与 `SetPluginEnabled` 开头各调用一次；`SetPluginEnabled` 另补 `instanceExists`，把现在「实例不存在却报 ErrLegacyLayout」的误导性错误改正。

- **位置**：`internal/arkapimanage/plugin.go:104-125`、`:224-242`、`:284-301`（`lockForPluginWrite` 只 `instanceExists`）
- **修复建议**：在 `applyPluginTo`/`uninstallFrom`/`SetPluginEnabled` 开头调用 `apiresp.ValidateInstanceName`，并把「实例存在」校验放进被锁保护的函数内。

#### [P2] `ValidatePluginName` 未覆盖 Windows 保留名与尾随点/空格

> 🔎 **复核：成立，下调为 P3（Windows 上报错不友好，无安全后果）。** `inspect.go:277-285` 只拒绝 `/\:,`、`..` 与前导 `.`。**修复方案**：`pkg/fsutil` 新增跨平台的 `ValidPortableName(name string) error`：拒绝 `CON/PRN/AUX/NUL/COM1-9/LPT1-9`（大小写不敏感、带不带扩展名都算，含 `COM¹²³`）、尾随 `.` 或空格、前导空格、控制字符与 `<>:"/\|?*`、长度 > 100。`ValidatePluginName` 在现有规则之外调用它；下一条的实例名校验复用同一个函数，保证 Linux 上建出的名字搬到 Windows 也合法。

- **位置**：`internal/plugindata/inspect.go:277-285`
- **触发条件**：上传包目录名/dll 主名为 `CON`、`NUL`、`LPT1`、`Foo.`、` Foo`。
- **后果**：Windows 上 `os.Rename`/`MkdirAll` 失败或名字被规范化 → `FindInstancePlugin` 找不到目录、`dll_missing` 误报。
- **修复建议**：拒绝保留设备名（含带扩展名形式）、尾随 `.`/空格、路径长度超限。

#### [P2] `apiresp.ValidateInstanceName` 放行 `.`、`:` 等，且 `SetPluginEnabled` 缺少 `instanceExists` 检查

> 🔎 **复核：成立，维持 P2。** `internal/webapi/apiresp/apiresp.go:18-26` 放行 `.`（`filepath.Join(InstancesDir, ".")` 就是 `instances/` 本身）与 `:`（Windows 上 `name:x` 是 NTFS 备用数据流）；这个函数被 12 处 HTTP 处理器使用，包括实例创建与重命名。**修复方案**：按上一条下沉到 `cfgpkg.ValidateInstanceName`，规则 = 现有规则 + `fsutil.ValidPortableName` + 拒绝 `.`；已存在的非法实例名不做迁移，只在创建 / 重命名时拦截，并在 `GET /api/instances` 里对不合规的名字附带一条 warning 供用户手动改名。`SetPluginEnabled` 的 `instanceExists` 见「内部 API 不校验实例名」一条。

- **位置**：`internal/webapi/apiresp/apiresp.go:18-26`；`internal/arkapimanage/arkapimanage.go:47-79`
- **修复建议**：`ValidateInstanceName` 增加 `.`/`:`/控制字符/纯空白与保留名检查；`SetPluginEnabled` 补 `instanceExists`。

#### [P2] `installCore` 覆盖游戏文件时的「原件残留」处理会丢历史原件

> 🔎 **复核：成立，下调为 P3（需要上一次操作在保存原件后、写清单前崩溃）。** `core.go:341-350`：清单里没有记录、但 `originals/` 下已有同名文件时，把它挪进**本次**备份（只保留 3 份，会被裁剪），再把当前 Win64 里来历不明的文件存为新的「原件」——真正的游戏原件反而可能丢失。**修复方案**：该分支改为**保留已有的原件**（最早的那份最可能是真正的游戏原件），直接把它登记进 `next.Overwritten`；当前 Win64 里被替换的文件放进本次备份的 `replaced/`（它的来历不明，不能冒充原件）。`placeConfig` 未合并时，`Result` 增加 `ConfigMerged bool=false` 字段，文案改为「本次**未安装**新版 config.json：现有文件不是 JSON 对象，已原样保留」，仍放在 warnings（不是失败）。

- **位置**：`internal/arkapimanage/core.go:341-350`（stale-originals 处理）、`:399-422`（`placeConfig`）
- **后果**：`stale-originals` 被挪进**本次**备份，而备份只保留 3 份，几次更新后最初的游戏原件可能被裁掉（与 `originals/` 要留到卸载的初衷相悖）；`placeConfig` 不装新配置时只发 warning。
- **修复建议**：`originals/` 重复时不挪走（保留最早那份）；`placeConfig` 在 `Errors` 而非 `Warnings` 里明确「本次未安装新 config.json」。

#### ~~[P2] `RetireLegacyServerPlugins` 会把用户重新放进 server-files 的插件无条件移走~~

> 🔎 **复核：不成立（与设计一致）。** ① 每实例布局下 server-files 里的插件**不会再被加载**，把后来放进去的插件移到 `arkapi/backups/legacy-server-plugins-<ts>` 并打 WARN（`layout.go:352-370`）正是预期行为，且只移不删。② 缺 `instance_config.ini` 的目录**不是实例**：`LoadInstanceConfig` 失败，它根本无法启动，也就不存在「以后从已清空的 `SourcePluginsDir` 迁移」的情形；正在运行而未迁移的实例已被计入 `pending`（`pluginlayout.go:33-38`），此时不会退役。无需修改（§7.2 首条修复判据后 `pending` 的统计会更准确）。

- **位置**：`internal/plugindata/layout.go:352-370`；调用点 `internal/instance/pluginlayout.go:46-48`
- **触发条件**：`MigratePluginLayouts` 里 `pending==0`；或某实例目录缺 `instance_config.ini`（`pluginlayout.go:30`）被跳过、未计入 pending。
- **后果**：全局插件被提前退役；该实例下次启动 `migrateInstance` 从已空的 `SourcePluginsDir` 拷不到插件。
- **修复建议**：退役前枚举所有实例目录（含缺配置的）做一次「是否已迁移」硬校验，任一未迁移就跳过退役并 WARN。

#### ~~[P2] 迁移对 `DbPathOverride` 的改写不覆盖 `PluginsDisabled` / 嵌套键~~

> 🔎 **复核：不成立（无实据的推测）。** ① 迁移的输入是旧布局的 `instances/<name>/plugins/`，那时还没有启用/禁用之分；`PluginsDisabled` 是迁移之后由 `Reconcile` 整目录搬出来的，被禁用的插件不加载，重新启用时整目录搬回 `Plugins/<X>`，改写后的路径（`layout.go:187-189` 统一指向 `Plugins/<X>`）届时依然有效。② 已知插件（Permissions 等）的 `DbPathOverride` 都是顶层键，没有嵌套形态的实例。若日后出现嵌套键的插件，把键名加入 `dbPathOverrideKeys` 并改为递归查找即可，届时再处理。

- **位置**：`internal/plugindata/layout.go:245-299`
- **后果**：顶层以外的形态不被改写；旧目录改名后路径悬空，Permissions 在原路径新建空库，权限静默清零。
- **修复建议**：对每个已知键做递归查找；或对指向 `plugins.legacy-*` 的路径统一报错提示。

#### [P2] `harvest`/`replaceGroup` 的整组判定依赖 `scanPluginDir` 分类，未识别的运行期数据会被弃

> 🔎 **复核：成立，维持 P2。** `scanPluginDir`（`internal/plugindata/classify.go:160-173`）只收 `config.json`、带 SQLite 魔数的文件与「名字像数据库」的文件，其余一律 `continue`；插件在运行期写到自己目录里的 `players.json`、`*.txt` 之类状态文件不会被抢救，随后镜像里的真实 `Plugins` 目录被 `migrateExceptionJunctions` 整个 `RemoveAll`（`internal/mirror/mirror.go:380`），这些文件就丢了。`extraDataFiles` 为空，等于没有兜底。
>
> **修复方案**：迁移第 1 步在现有分组之外，再对每个插件计算「**镜像独有文件**」：镜像插件目录中存在、而 server-files 同一插件目录中**不存在**的相对路径，排除 `.dll/.pdb/.exe` 与 `snapshots/`。这些文件按原相对路径复制进实例插件目录（目标已存在则不覆盖），每个都记一条 INFO；迁移报告里列出清单。判据是「源里没有」而不是「猜它是不是数据」，与分类规则互补。单测：镜像插件目录里放一个 `state.json`，迁移后它出现在实例目录。

- **位置**：`internal/plugindata/classify.go:120-191`、`plugindata.go:223-244`
- **修复建议**：`extraDataFiles` 目前为空（`classify.go:28`），需要时补上；迁移前把「镜像独有且未被分类的文件」列入报告/日志。

#### [P2] 镜像里 `arkApiInstalled()` 与 `installer.ArkApiInstalled()` 判据不一致

> 🔎 **复核：成立，下调为 P3。** `internal/mirror/mirror.go:96-102` 看 `Win64/ArkApi` 目录，同文件的 `pluginsExceptionRelPath`（`:514-520`）与 `installer.ArkApiInstalled` 看 `AsaApiLoader.exe`。它只决定 Cache 目录的同步守卫（`:764-765`），主程序卸载后残留 `ArkApi/` 目录时守卫仍开着，后果只是 Cache 不被对账。**修复方案**：`arkApiInstalled()` 改为与 `pluginsExceptionRelPath` 相同的 loader 判据，并抽成一个 `loaderInstalled()` 供两处共用。

- **位置**：`internal/mirror/mirror.go:96-102`（只看 `Win64/ArkApi` 目录存在）
- **修复建议**：统一改为「存在 `AsaApiLoader.exe`」。

#### [P2] `movePath` 跨卷回退用 `fsutil.CopyDir/CopyFile`，会解引用插件目录里的符号链接

> 🔎 **复核：成立，下调为 P3**（只在 `arkapi/` 与 `server-files` 位于不同卷、且被搬动的目录里有符号链接时触发）。`internal/arkapimanage/core.go:737-766`。**修复方案**：回退复制前先 `filepath.WalkDir` + `Lstat` 检查源树，发现符号链接（或 Windows junction，用 `fsutil.IsLink`）即放弃复制，返回「跨卷移动遇到链接 %s，请手工处理」——在管理员上传的包里出现链接本身就不正常（`archive.ExtractZip` 也拒绝链接），不值得为它实现链接的原样重建。

- **位置**：`internal/arkapimanage/core.go:737-766`；`pkg/fsutil/fsutil.go:37-96`
- **修复建议**：回退路径用 `Lstat` 判断链接并原样 `os.Symlink`/跳过，或直接报错要求手工处理。

#### ~~[P2] 清单哈希缓存与 `lookupFold` 的性能/陈旧问题（小）~~

> 🔎 **复核：不成立（可忽略）。** 清单只覆盖 ArkApi 主程序的几十个文件，`lookupFold` 的线性查找在这个规模上没有可测量的开销；哈希缓存以文件路径为键，条目数以同一批文件为上限，不会无界增长。不值得为此增加复杂度，无需修改。

- **位置**：`internal/arkapimanage/manifest.go:120-152`、`:99-109`
- **修复建议**：`lookupFold` 调用点先建一次小写索引；哈希缓存加容量上限或按 mtime 变化失效。

### 7.3 文档 vs 代码偏差

1. **迁移判据**：文档 §4.4 写「`ArkApi/Plugins` 目录已存在即视为已迁移」，代码用标记文件 `ArkApi/.plugin-layout`（`layout.go:41-42、87-90`）。已在实施记录 §16.3 第 1 条说明。
2. **停止路径保留 `Reclaim`**：文档 §4.3/§7 写「删除 Reclaim 调用」，代码仍保留（`server.go:858`）。§16.3 第 2 条已说明。
3. **`shuttleRetired` 双判据**：文档只写「镜像里 Plugins 是链接」，代码另加「实例已迁移」（`plugindata.go:71-73`）。§16.3 第 3 条已说明。
4. **插件临时组装目录**：文档 §6.3 说「同级临时目录」，代码放在 `ArkApi/.install-*`（`plugin.go:154`）。§16.6 第 6 条已说明。
5. **未登记的偏差**：① 文档 §7/§4.6 只说「未运行才迁移」未规定判据，代码用**端口监听**（`pluginlayout.go:33`）而非进程存活（见 P1）；② 文档 §8.2 的 `GET /api/plugins/:name` 返回示例无 `plugins_dir` 字段，代码新增（`pluginapi.go:102`）；③ 文档 §6.4 卸载语义未提快照处理；④ 文档 §5.3 插件名规则未覆盖 Windows 保留名/尾随点。

> 🔎 **复核（§7.3）**：第 1–4 条已在 `ARKAPI_PLUGIN_PLAN.md` 实施记录里说明，属已登记偏差；第 5 条的 ①③④ 随 §7.2 对应修复回填文档，② 在文档 §8.2 的示例里补上 `plugins_dir` 字段即可。

---

## 8. 跨模块同源问题（去重汇总）

以下问题在多个模块的独立审查中被**分别命中**，说明其根因横跨多个包，修复时须一次覆盖所有受害面，不能只补一处。

### ~~8.1 `.lower-stamp` 只记录 Proton 版本，不感知 VC++ 补装（模块 3 / 4 / 5 三处独立命中）~~

> 🔎 **复核：已修复（`2edd91c`），问题不再存在。** 修法与本节建议一致且更通用：指纹不专属 VC++，而是所有 `PrefixProvisioner` 的 `Fingerprint` 按插件名排序拼接，经 `wineprefix.Config.ProvisionFingerprint` 钩子现读现算；stamp 格式「Proton 标记;组件指纹」，旧格式的层升级后一次性重建。见 `UMU_RUNTIME_PLUGIN_PLAN.md` §5.4。

- **位置**：注释 `pkg/wineprefix/wineprefix.go:105-108`；实现 `pkg/wineprefix/wineprefix_linux.go:423`（`want := umu.PrefixMarker(lower)`）、`:483`（写 stamp）；Proton 标记写入 `pkg/umu/umu_linux.go:511-518`（只按 `cfg.ProtonVersion`），`internal/runner/vcredist_linux.go`、`pkg/vcredist/install_linux.go` 全无对该文件的写入。
- **后果**：注释与文档 `PLAN §3.3`/`§6.1` 都声称可检出「reinstalled VC++」，实际检测能力为零。旧 `upper` 里已 copy-up 的 `system.reg`/`system32` 遮蔽新 lower，补装的 VC++ override 对已有实例不生效；ArkApi 起不来且无提示。
- **修复建议**：把 VC++ 有效状态纳入 stamp：`want := umu.PrefixMarker(lower) + "|" + vcredistLowerStamp(lower)`，其中指纹可用 `vcredist.OverridesApplied(lower)` + `user.reg` 摘要。补装成功后 fingerprint 变化 → 命中 `readOverlayStamp != want` → 按现有逻辑清理重建。

### 8.2 ArkApi 冲突检查在启动闸门**之前**，形成 check-then-act 窗口（模块 3 / 6 两处独立命中）

- **位置**：`internal/instance/server.go:467`（检查）vs `:548`（闸门）；判活用 `procpkg.IsServerRunning`（端口）。
- **修复**：见 §3.2 同名条目——判活改 `IsInstanceProcessAlive`，并把冲突检查移进闸门内 `runner.Run` 之前。

> 🔎 **复核：成立（P1）。** 以 §3.2 复核块为准：判据是「进程内启动登记表 ∪ 已校验的进程存活」，登记与检查在同一把锁内完成，闸门内再权威复查一次。

### 8.3 overlay `upper` 在宿主机重启后被静默擦除（模块 3 / 4 两处独立命中）

- **位置**：`pkg/wineprefix/wineprefix_linux.go:425-447`。
- **修复**：见 §3.2 同名条目——补 `hasLayer` 形态，stamp 一致时保留 `upper` 只重挂载。

> 🔎 **复核：成立，下调为 P2。** 以 §3.2 复核块为准；另须同时修 §3.2「`EnsureRuntime` 无条件 `PrepareSharedWrite`」，否则每次 API 启动仍会把空闲层卸掉，保留 `upper` 的意义大打折扣。

### 8.4 `PrepareSharedWrite` 与实例启动竞态（模块 3 / 4 两处独立命中）

- **位置**：`pkg/wineprefix/wineprefix_linux.go:799-833` vs `:408-420`。
- **修复**：见 §3.2 同名条目——按 key 取同一把 `lockPrefix` 锁。

> 🔎 **复核：成立（P1）。** 仅按 key 取 `lockPrefix` 不够（锁在 wineserver 起来之前就释放了，也覆盖不了「快照后才挂载」）；以 §3.2 复核块的 `lowerMu` + `HoldLayer` 租约方案为准。

### 8.5 启动闸门可能被永久持有（模块 3 / 6 两处独立命中）

- **位置**：`internal/instance/server.go:708-712`（无 ctx/超时）；`launchgate.go` 无超时。
- **修复**：见 §3.2 / §6.2 同名条目——`select` 加 `ctx.Done()`，并给启动总流程加上限超时。

> 🔎 **复核：成立（P1）。** API 启动的 ctx 是 `Background`，关键是超时；以 §6.2 复核块为准（超时放闸门、不杀进程、`waitServerStartup` 跟踪日志失败时立即报失败）。

### 8.6 （复核新增）「这个进程属于哪个实例」没有唯一判定

复核时发现，§6.2 的 marker 子串串扰、启动失败清场、停止 / 强停的 PID 校验、`PIDByPort` 误中 wineserver、§3.2 的冲突判定、§7.2 的迁移判活，**根因都是同一件事**：代码里有四种互不一致的「进程归属」判据（端口、PID 文件、`isExpectedProcess` 的 exe 名、`AltSaveDirectoryName=` 子串），没有一个能同时回答「是不是游戏进程」与「是不是**这个实例的**」。

- **修复**：以 §6.2 marker 一条的 `cmdlineHasSaveDir`（带边界的实例标记）为核心，下沉到 `internal/process`，建立 `VerifiedPID(instance, kind)`；所有「要对某个 PID 动手」的路径（停止、强停、失败清场、`KillServer`）与「要判断某实例是否活着」的路径（`IsInstanceProcessAlive`、冲突判定、迁移判活）一律经过它。端口只用于「是否在对外服务」这一只读判断。**这是第一批修复的公共前置，必须先做。**

---

## 9. 发现汇总表（按优先级）

### 9.1 P0（阻断级：会导致服务/实例永久不可用或静默数据损坏）

| #    | 模块     | 标题                                                   | 位置                                                   | 🔎 复核 |
| ---- | ------ | ---------------------------------------------------- | ---------------------------------------------------- | ----- |
| P0-1 | 权限     | 启动自检检查镜像目录、reconcile 却不修 → 升级后永久拒绝启动（死锁）             | `internal/runner/runtimeuser_linux.go:108` vs `:222` | 成立，**P0** |
| ~~P0-2~~ | ~~prefix~~ | ~~`verify-arkapi --install-vcredist` 无守卫写共享底层 lower~~    | ~~`internal/actions/verify_arkapi.go:53-60`~~            | **已修复**（`36862d8`） |
| P0-3 | 缓存     | 跨哈希并发 `pruneGenerations` 删掉 metadata 正指向的 generation | `pkg/arkcache/arkcache.go:205`                       | 成立，**↓P2**（仅「启动中预取 + 更新」交错可达，后果是降级为 ArkApi 自行下载） |

### 9.2 P1（严重：功能失效、安全面或跨实例误伤）

| #     | 模块     | 标题                                            | 位置                                                          | 🔎 复核 |
| ----- | ------ | --------------------------------------------- | ----------------------------------------------------------- | ----- |
| P1-1  | 显示     | `Stop()` 提前 return 跳过 mount 还原，宿主目录永久改 rw     | `pkg/xvfb/xvfb_linux.go:262-279`（原文误作 `manager.go`）         | 成立，P1 |
| P1-2  | 显示     | `-ac` 无 `-auth`，X socket 全局可连、无认证             | `pkg/xvfb/xvfb_linux.go:191-198`                            | 成立，↓P2（降权模式下 Xvfb 非 root；仅本机暴露） |
| P1-3  | 权限     | `umu_runtime_user: root` 可静默绕过降权              | `pkg/sysuser/sysuser_linux.go:77-99`                        | 成立，P1 |
| P1-4  | 权限     | Python 探测无超时且持锁，挂起会拖死启动                       | `pkg/pyfinder/pyfinder.go:246-266`                          | 成立，↓P2（stdin 为 /dev/null，不会因等输入挂起） |
| P1-5  | prefix | 未挂载但内容完好的可写层被静默擦除（upper 跨重启丢失）                | `pkg/wineprefix/wineprefix_linux.go:426-449`                | 成立，↓P2（upper 非用户数据） |
| P1-6  | prefix | ArkApi 冲突检查用端口判活且位于闸门之前                       | `internal/instance/launchgate.go:102-131` / `server.go`     | 成立，P1 |
| P1-7  | prefix | `PrepareSharedWrite` 与实例启动竞态，卸载正在启动的层         | `pkg/wineprefix/wineprefix_linux.go:811-845`                | 成立，P1（原文的按 key 加锁不足，见复核块） |
| P1-8  | 缓存     | `writeMetadata` 的 `.tmp` 名跨哈希共享，可写撕裂 JSON     | `pkg/arkcache/generation.go:203-204`                        | 成立，↓P3 |
| P1-9  | 缓存     | 下载体无字节上限，可打满磁盘                                | `pkg/download/download.go:29-35`                            | 成立，↓P2 |
| P1-10 | 缓存     | 陈旧锁阈值 30 分钟短于下载时长，夺锁双写                        | `pkg/arkcache/arkcache.go:269-288`                          | 成立，↓P2 |
| P1-11 | 缓存     | 陈旧锁 TOCTOU，两等待者同时持锁                           | `pkg/arkcache/arkcache.go:289-295`                          | 成立，↓P3 |
| P1-12 | 缓存     | 源缓存写入与镜像同步不互斥，镜像同步到半成品                        | `pkg/arkcache/arkcache.go:167-171` / `mirror.go:846`         | 成立，↓P2（仅未启用 ArkApi 的实例暴露） |
| ~~P1-13~~ | ~~缓存~~     | ~~VC++ override 被 upper 旧 `user.reg` 遮蔽（同 §8.1）~~ | ~~`pkg/wineprefix/wineprefix_linux.go:195-197`~~                | **已修复**（`2edd91c`） |
| P1-14 | 日志     | 日志转抄协程被 5 分钟超时 ctx 终止（回归）                     | `internal/instance/asaapilog_linux.go:119-151`          | 成立，P1 |
| P1-15 | 日志     | marker 前缀子串匹配，跨实例串扰、杀错实例                      | `internal/instance/common.go:156,520` / `procx_linux.go:80` | 成立，P1 |
| P1-16 | 日志     | 对已回收 launcher PID 调 `KillTree`，PID 复用误杀       | `internal/instance/server.go`（`ErrLauncherExited` 分支）                   | 成立，↓P2；另发现该分支收不掉已 reparent 的残留树 |
| P1-17 | 日志     | 停止对未校验保存 PID 直接 kill，PID 复用误杀                 | `internal/instance/server.go`（`stopServerInternal` / `ForceStopServer`）               | 成立，强停 P1 / 正常停止收尾 P3 |
| P1-18 | 日志     | `PIDByPort` 可能归属共享 wineserver，停 A 挂 B         | `internal/instance/server.go`（`stopServerInternal` / `KillServer`）                   | 成立，P1（不限于 ArkApi） |
| P1-19 | 日志     | `launchgate` 可能被永久持有（同 §8.5）                  | `internal/instance/server.go`（初始化等待 `select`）                       | 成立，P1 |
| P1-20 | 插件     | 启动迁移用端口判活，对活库做收割                              | `internal/instance/pluginlayout.go:33`                      | 成立，P1 |
| P1-21 | 插件     | `MirrorPluginsDir` 硬编码大小写，Linux 迁移抢救失效        | `internal/plugindata/plugindata.go:43/76-78`                | 成立，↓P2（仅非常规手工安装） |
| P1-22 | 插件     | 更新插件两次 rename 不具崩溃原子性                         | `internal/arkapimanage/plugin.go:184-196`                   | 成立，↓P2（有备份可恢复） |
| P1-23 | 插件     | 禁用/启用返回原因与提示不符                                | `internal/arkapimanage/arkapimanage.go:64-71`               | 成立，↓P3（仅文案） |

### 9.3 P2 / P3（健壮性、可观测性、边界加固）

> 数量较多，按模块归类列于对应章节（§1.2 共 8 条、§2.2 共 6 条、§3.2 共 5 条、§4.2 共 9 条、§5.2 共 7 条、§6.2 共 11 条、§7.2 共 10 条）。代表性条目：



| 模块          | 代表条目                                                                                   |
| ----------- | -------------------------------------------------------------------------------------- |
| 显示          | `pkg/xvfb` 全包零日志；看门狗退避写成 `[0]`；`AllowX11Remount` 零值回填缺失；孤儿认领无属主校验                      |
| 权限          | `Problems` 家目录判据自相矛盾；深度探测写 lowerdir；GID 不校验；`perms status` 语义混淆                        |
| prefix      | `PrepareSharedWrite` 无改动也卸载；`bak-` GC 绕过 wineserver 检查；GC 部分删除后报失败                     |
| prefix init | D3 无跨进程 flock；D4 补权限顺序（与 D2 强耦合）；`reconcilePrefixVersion` marker 缺失即搬走前缀；`WINE*` 白名单过宽 |
| 缓存          | `hashMutex` 无回收不响应 ctx；复用同尺寸遗留 zip 不校验来源；GC 删活跃 `.part`；206 不校验 `Content-Range`        |
| 日志          | SIGTERM→SIGKILL 升级是假的；多候选取字典序首个；`Relay` 不跟踪 inode；PTY 关闭早于读取                           |
| 插件          | 卸载不清理快照；Windows 保留名未覆盖；`ValidateInstanceName` 放行 `.`；跨卷回退解引用符号链接                       |

> 🔎 **复核（§9.3）**：P2/P3 中**不成立或已修复**、已在正文划去的共 11 条（另有 §2 Python 一条划去半条、§2.3 偏差表划去一行）——§1 `Stop` 并发起第二个 Xvfb；§4 `EnsurePrefix` 快路径 TOCTOU（已修）、`reconcilePrefixVersion`；§5 `writeMetadata` 失败遗留 zip；§6 `Relay` 不跟 inode、`processComm` 丢候选、`waitForGamePID` 泄漏、`signalTree` 顺序；§7 `RetireLegacyServerPlugins`、`DbPathOverride` 嵌套键、清单哈希性能。上表「`reconcilePrefixVersion` marker 缺失即搬走」与「`Relay` 不跟踪 inode」两项据此作废。其余条目的定级调整见各条复核块，汇总与排期见 §11。

---

## 10. 建议修复排期

> 🔎 **复核**：本节为审查当时的排期，P0-2、P1-13 已修复，多条定级已调整；**以 §11 的复核后排期为准**。

### 第一批（P0，建议作为发布阻断项，一次性修完并补回归测试）

1. **P0-1 权限死锁**：让 `verifyRuntimeAccess` 的 `OwnershipDirs` 与 `reconcileRuntimeOwnership` 的修复范围一致；修正错误提示文案。补一条回归测试：断言「`VerifyRuntimeAccess` 报出的每一项都能被同一次启动的 `EnsureRuntimeUser` 修复」。
2. **P0-2 写穿 lower**：给 `verify-arkapi --install-vcredist` 加守卫，并把守卫下沉进 `EnsurePrefixVCRedist`，防止下一个调用点再漏。
3. **P0-3 跨哈希并发删代**：提交 metadata + 清理进同一把全局 `commitMu`；`pruneGenerations` 加 `protected` 参数保护当前指针；generation 用 staging 目录 + 原子 rename 提交。

### 第二批（P1，安全与跨实例误伤优先）

- **安全面**：P1-2（`-ac` 未认证 X server）、P1-3（root 绕过降权）。
- **跨实例误伤 / 杀错进程**：P1-15、P1-16、P1-17、P1-18、P1-6。
- **数据丢失**：P1-5（upper 跨重启丢失）、P1-21（大小写导致静默丢数据）、P1-20（对活库收割）。
- **功能失效**：P1-14（日志 5 分钟冻结）、P1-19（闸门永久持有）、P1-13 + §8.1（VC++ 不生效）、P1-22（插件更新非原子）。
- **并发正确性**：P1-8、P1-9、P1-10、P1-11、P1-12、P1-7。
- **启动阻塞**：P1-4（Python 探测无超时）。

### 第三批（P2/P3，可观测性与边界加固）

建议优先补上被文档反复强调、但当前完全缺失的**可观测性**：`pkg/xvfb` 的 remount/chmod/restore 日志与 Xvfb 退出原因、日志转抄结束时的说明行、`perms status` 的语义区分。其余按模块各自排期，并在 README/`docs/` 中同步修正与代码不符的表述。

---

## 11. 复核结论与修复排期（2026-09-29，基线 `caca2db`，以此为准）

### 11.1 结论统计

原文 §1–§7 共列 85 条发现，其中 4 条是跨章节重复（§3 闸门 ↔ §6 闸门、§6 冲突判定 ↔ §3 冲突判定、§4 陈旧锁 ↔ §5、§4 `bak-` ↔ §3），去重后 **81 条**：

| 结论 | 条数 | 说明 |
| --- | --- | --- |
| ~~已修复~~ | 3 | P0-2（`36862d8`）、P1-13 / §8.1（`2edd91c`）、§4 `EnsurePrefix` 快路径 TOCTOU |
| ~~不成立~~ | 10 | §1 `Stop` 并发、§4 `reconcilePrefixVersion`、§5 `writeMetadata` 遗留 zip、§6 `Relay` inode / `processComm` / `waitForGamePID` / `signalTree`、§7 `RetireLegacy` / `DbPathOverride` / 清单性能 |
| 成立 | 68 | 其中 §2 Python 一条为部分成立；另新增一条跨模块根因 §8.6 |

成立项的**复核后定级**：**P0 × 1**（P0-1）；**P1 × 10**（P1-1、P1-3、P1-6、P1-7、P1-14、P1-15、P1-17 的强停部分、P1-18、P1-19、P1-20）；其余均为 P2/P3。原 P0-3 与 13 条 P1 已下调，理由见各条复核块。

### 11.2 公共前置（先做，后面多条依赖它们）

| 前置 | 内容 | 被哪些条目依赖 |
| --- | --- | --- |
| A. 实例进程身份 | §8.6：带边界的 `cmdlineHasSaveDir` + `procpkg.VerifiedPID(instance, kind)` + `ClearInstancePIDs`；`procmatch.Find` 增加精确过滤参数 | P1-15、P1-16、P1-17、P1-18、P1-6、P1-20、§6「多候选」 |
| B. 可写层租约 | `wineprefix` 的 `lowerMu` + `HoldLayer(key)` + `ProbeDir(key)` + `ChownUnmountedLayers` | P1-7、§2 深探、§3 `rwSubtrees` TOCTOU |
| C. xvfb 日志回调 | `xvfb.Config.Logf`，由 `xdisplay` 注入 | §1 零日志、看门狗、P1-1 的可观测性、P1-2 的告警 |

### 11.3 排期

**第一批（发布阻断：服务起不来 / 杀错进程 / 永久卡死）**

1. **P0-1** 权限门禁死锁：门禁集合 = reconcile 集合，镜像改为实例级检查 + 建议项，`perms fix` 覆盖独占目录，`Fix` 文案回到调用方。
2. **前置 A** + **P1-15 / P1-17 / P1-18 / P1-16**：统一实例进程身份，停止与强停不再按端口或裸 PID 动手，失败清场按实例标记收树。
3. **P1-6** ArkApi 冲突：进程内启动登记表 ∪ 已校验存活，闸门内权威复查。
4. **P1-19** 闸门永久持有：`startup_init_timeout`，超时放闸门不杀进程，`waitServerStartup` 日志跟踪失败立即报错。

**第二批（数据与功能正确性）**

5. **P1-14** 日志转抄 5 分钟冻结（回归，改动小，可提前）。
6. **P1-20** 迁移判活 + `MigrateInstance` 的护栏参数（依赖前置 A）。
7. **P1-3** root 绕过降权；**§2** `effectiveHome` 统一、`checkACLSupport` 不吞错误。
8. **P1-1** + 前置 C + §1 看门狗 / 零日志（同一批改 `pkg/xvfb`）。
9. **前置 B** + **P1-7** + §3 `EnsureRuntime` 先判 `LowerNeedsWork` + §3 upper 跨重启保留（三者都改 `wineprefix`/`Host.Ensure`，同批做、同批测）。
10. **D4 → D2**（必须先 D4 后 D2）+ **D3** flock。

**第三批（P2 健壮性）**

11. 缓存：P0-3（更新前拒绝启动中实例 + `commitMu` + 清理保护指针）、staging 原子提交（P1-12）、陈旧锁心跳与原子夺锁（P1-10/11）、`hashMutex` 可取消、同尺寸 zip 同源校验、`download` 的 `MaxBytes` 与 `Content-Range` 校验（后两者在 `pkg/download`，惠及所有下载）。
12. 进程：`TerminateTreeGracefully`（Linux 升级失效）、PTY 关闭权交给读取方、`launcher.log` 打开失败时排空。
13. 插件：更新 journal 恢复（P1-22）、大小写（P1-21）、镜像独有文件抢救、实例名 / 插件名可移植性校验（`.`、`:`、保留名）。
14. 其他：P1-2 cookie 认证（先上告警，再上 cookie）、P1-4 Python 探测超时、§3 `gc` 按形态删除、§2 深探目标。

**第四批（P3，可观测性与小修）**

`linux.display` 拒绝原因、`perms status` 区分用户缺失、`AllowX11Remount` 等 bool 映射的反射测试、`HOME` 为空的日志落点、孤儿认领校验、`ensure` 复用判定、GID 交叉校验、`bak-` 结构化判据、`WINE*` 拒绝集、`LaunchSpec.Env` 过白名单、`WarmPrefix` 超时与整树取消、死代码（`runtimeUserNameHint`、`atomicBoolImpl` 注释）、缓存 GC/快路径清理/裸哈希判据、多候选择优、转抄结束说明行、`instanceLogFilePath`、禁用/启用文案、卸载快照、`installCore` 原件、`arkApiInstalled` 判据、`movePath` 链接。

### 11.4 验证要求

- Linux 行为一律在 WSL 实测（`CLAUDE.md`「Linux 测试（WSL2）」）；涉及降权的用例在 root 下通过不代表普通用户下成立，P0-1、P1-3、§2 家目录三条需额外用非 root 运行时用户手测一次。
- 第一批的 2、3、4 三项需要一次 shared 模式双实例（其中至少一个 ArkApi）的真机回归：并发启动、停止其一、强停其一、人为让一个实例卡在初始化前，四个场景各跑一次。
- 修复合入后，把各条结论回填到对应计划文档的「2026-09-29 代码审计同步」附录（`XVFB_DISPLAY_PLAN.md`、`LINUX_RUNTIME_PRIVILEGE_PLAN.md`、`UMU_PREFIX_PLAN.md`、`ARKAPI_*`），并更新 CHANGELOG（P0-1、GID 校验、实例名校验三条对现网部署有可见影响，需单独说明）。

### 11.5 第一批实施记录（2026-09-29，分支 `fix/audit-batch1`）

**状态：代码与单测已完成；§11.4 要求的 shared 模式双实例真机回归尚未进行。**

| 项 | 落地位置 | 测试 |
| --- | --- | --- |
| P0-1 门禁死锁 | `internal/runner/runtimeuser_linux.go`（`verifyRuntimeAccess(forceDeep, extraOwnership...)`、`fixRuntimeOwnership`、`checkMirrorOwnership`）；`pkg/sysuser`（`AccessCheck.DriftFix`、`OwnerDrift`）；`runner.VerifyRuntimeAccessForLaunch(mirrorDir)`、`runner.FixRuntimeOwnership`；`perms fix` 先修独占目录与全部镜像再修共享树；Preflight 增建议项 `umu-runtime-mirror-drift` | `TestOwnerDrift*`；`TestRuntimeUser_CreateReconcileVerify`（opt-in，WSL root 实跑通过）新增「root 属主镜像不阻断门禁 / 报为建议项 / 实例级检查能看见 / `fixRuntimeOwnership` 修好」 |
| 前置 A 实例进程身份 | `internal/process/identity.go`（`SaveDirMarker`、`CmdlineHasSaveDir`、`VerifiedPID`、`PIDBelongsTo`、`ClearInstancePIDs`）；`IsInstanceProcessAlive` 改用 `VerifiedPID`；`procmatch.Matcher.Find(prefilter, accept)` | `TestCmdlineHasSaveDir`（真机命令行形态、`srv`/`srv2`、Windows 引号、串尾、NUL）、`TestClearInstancePIDs`、`TestPIDBelongsTo_*`、`TestAccepts` |
| P1-15 前缀串扰 | `waitForGamePID`、`findServerPIDBySaveDir` 经 `ownedBy(saveDir)` 精确过滤 | 同上 |
| P1-18 停止误中 wineserver | `resolveGamePID`（进程表按实例标记 → 已校验的保存 PID），用于 `stopServerInternal` 与 `KillServer` | — |
| P1-17 陈旧 PID 误杀 | `ForceStopServer` 全部改 `VerifiedPID` + 按实例标记扫尾 + `ClearInstancePIDs`；正常停止删除了收尾的 `Kill(pid2)` 并清 PID 文件 | — |
| P1-16 已回收的启动器 PID | 启动失败清场：启动器未被回收才按树收，之后一律 `killInstanceProcesses(saveDir)` 扫尾 | — |
| P1-6 ArkApi 冲突 | `launchgate.go`：`claimArkApiSlot`（查冲突与登记同锁）+ 闸门内复查；判据 = 进程内启动登记表 ∪ `IsInstanceProcessAlive` | `TestClaimArkApiSlot_*`（含 8 协程并发只允许 1 个成功，`-race`） |
| P1-19 闸门永久持有 | `awaitInitialization`；新配置 `linux.launch_gate_timeout`（默认 20m，≥1m，中英模板同改）；`WaitServerCompleted` 同时等 `initFailed`/`ctx` | `TestAwaitInitialization_*`；appconfig 校验用例 |

**与复核方案的偏离**

1. **P1-19 超时后不返回错误、不写 `start_failed`，只放行闸门并继续等待。** 复核方案原写「放闸门并返回启动超时错误」。实施时发现 `StartServer` 会把 `startServerInternal` 返回的任何错误记成 `stopped`：一台只是慢、进程仍在跑的实例会被标成已停止，用户再点启动就撞上仍被占用的端口。继续等待没有泄漏：进程退出时 `waitServerStartup` 的退出监视照常发出 `initFailed`。
2. **P1-19 配置项是 `linux.launch_gate_timeout` 而不是 `server.startup_init_timeout`。** `server` 段是 HTTP 服务本身的配置；闸门只存在于 Linux shared 模式，放在 `linux` 段 Windows 自然忽略。名字也改为描述它真正控制的东西（放行闸门），而不是「启动超时」。
3. **P1-19 未改 `waitServerStartup` 在日志跟踪失败时的行为。** 原方案要求此时立即报失败；但那同样会把一个活着的实例标成失败。跟踪失败只记 WARN 的现状保留，由闸门超时兜住排队问题。
4. **P1-16 未引入 `ProcessStartTime` 身份比对。** 改用更直接的事实：`launcherExited` 未关闭 ⇔ 子进程尚未被 `Wait` 回收 ⇔ 它的 PID 不可能被复用，此时按树收是安全的；已回收时不再碰那个 PID，改由实例标记扫尾（这同时解决了「已 reparent 的残留树收不到」）。
5. **正常停止不再对启动器收进程树。** 复核方案曾提议停止收尾时 `VerifiedPID(…, asaapi)` 后清理残留加载器。实施时判断这是新增风险：shared 模式下共用的 wineserver 可能正是先启动的实例启动链的后代，对它的启动器收树会带走同一 Wine 会话的其他实例。原代码在正常停止时也从未碰过启动器树，因此只删除了那次无效的 `Kill(pid2)`。
6. **`RunningInstances` 未改用 `VerifiedPID`。** 它是资源采样器每 2 秒调用一次的目标源，Windows 上 `VerifiedPID` 要经 WMI 取命令行，频率太高；它仍用「存活 + 镜像名」判定，已在停止时清 PID 文件的前提下风险显著降低。留待第四批评估。
7. **强制停止与启动失败清场仍可能触及共享 wineserver**：`killInstanceProcesses` 与 `ForceStopServer` 对启动链收树，若 wineserver 是该启动链的后代则会被一并杀掉。这与修复前 `ForceStopServer` 对 `launcher_pid` 收树的行为相同（强停本身是破坏性操作），未扩大影响；是否需要在收树时排除 `wineserver` 进程，留给真机回归时观察 wineserver 的实际父进程后再定。

**验证**：Windows `go build ./...`、`go vet`、`go test -race`（process / instance / appconfig / runner / procmatch / actions）通过；WSL `go build ./...`、`go vet`、`go test -race`（上述 + sysuser，`ASA_TEST_RUNTIME_USER=1`）通过。改动文件 `gofmt` 干净（`pkg/sysuser/sysuser.go` 的 `Info` 结构体注释对齐是基线遗留，未动）。

---

## 附录 A：文档路径 vs 实际代码 对照表

| 文档                                          | 文档声称的落地路径                                                                             | 实际代码位置                                                                |
| ------------------------------------------- | ------------------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| ALWAYS_MANAGED_XVFB / XVFB_CROSS_DISTRO     | `internal/runner/display_linux.go`、`internal/runner/xvfb_linux.go`                    | `pkg/display/`、`pkg/xvfb/`（`internal/runner/*_linux.go` 仅 45~49 行胶水）  |
| ACL_PERMISSION_HARDENING / UMU_RUNTIME_USER | `internal/runner/runtimeuser_linux.go`、`asaserver/`                                   | `pkg/sysuser/`、`pkg/shareacl/`、`internal/runner/*_linux.go`           |
| UMU_PYTHON_DISCOVERY                        | `internal/runner/python_linux.go`                                                     | `pkg/pyfinder/`                                                       |
| UMU_PREFIX_OVERLAY / PER_INSTANCE           | `internal/runner/overlay_linux.go`、`prefix_linux.go`、`prefix.go`                      | `pkg/wineprefix/`、`pkg/umu/`、`internal/actions/prefix.go`             |
| UMU_PREFIX_INIT_TROUBLESHOOTING §4          | `internal/runner/runtimeuser_linux.go:507-529`、`internal/runner/umu_linux.go:227-287` | `pkg/sysuser/sysuser_linux.go:462-482`、`pkg/umu/umu_linux.go:226-281` |
| ARKAPI_LINUX_VCREDIST                       | `internal/runner/vcredist.go`、`vcredist_windows.go`                                   | `pkg/vcredist/`、`internal/runner/vcredist_linux.go`                   |
| ARKAPI_LINUX_LOGGING_AND_PID                | `internal/instance/arkapilog.go`、`gameproc{,_linux,_windows}.go`（**均不存在**）            | `pkg/tail/waitnewest.go`、`pkg/iox/relay.go`、`pkg/procmatch/`          |
| ARKAPI_PLUGIN_INSTALL / DATA                | `asaserver/` 顶层包                                                                      | `internal/arkapimanage/`、`internal/plugindata/`                       |
| （全部）                                        | `asaserver/`（旧顶层目录）                                                                   | `internal/`（已整体迁入，见 `docs/INTERNAL_LAYOUT_MIGRATION.md`）              |

> `pkg/arkapi/` 目录当前为**空**；`docs/` 下计划文档的「落地文件清单」需按上表整体重写，否则后续维护者会持续被误导。

---

## 附录 B：关于「正向结论」

为避免只呈现问题，以下被核实为**实现正确、无需修改**的部分：

- `pkg/arkcache/serialized.go`：对上游 `validateSerializedMap` 的复刻与文档 §2.2 规则表逐条一致（`keySize==0`、`> maxKeySize`、越界、尾部残字节、重复 key、条目数上限、必须恰好读完且 `entryCount>0`）。
- `pkg/arkcache/zip.go`：白名单 / 条目数 / 单条目与总量上限 / `io.LimitReader` 回比与文档 §10 一致，**未发现 zip-slip、路径穿越、符号链接逃逸或 zip-bomb 缺口**。
- UMU prefix 初始化 **D0 / D1 / D5** 修复：`InheritedEnv()` 白名单 + `WarmPrefix` 前后置同判据校验 + 标记后置写入，三条链路共用同一判据，实现正确（缺陷仅在 `Options.Env` 逃逸口）。
- 显示候选链前三档顺序、Xvfb 单例、`Pdeathsig` 专用 fork 线程：落地正确。

---

*本报告为纯只读审计产出，未修改、创建、删除任何业务代码或既有计划文档。所有行号基于 `faf127c` 工作区实际源码核实。*
