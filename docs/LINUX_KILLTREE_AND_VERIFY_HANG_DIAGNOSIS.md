# Linux：KillTree 杀不掉游戏进程 + verify 启动后卡死不监听端口（2026-08-29）

> 状态：**全部结案**。Q1–Q7 全部有结论（§6），修复已落地（§7）并在真机验证通过
> （§7.5），方案 B 的 ACL 继承也已确认生效（§7.5.2.2）。无遗留项。
> 所有"待确认"项标了编号（Q1…Q5），每项都给了 30 秒内能跑完的验证命令。
> 相关文档：`docs/UMU_RUNTIME_USER_PLAN.md`、`docs/LINUX_COMPATIBILITY_PLAN.md`、
> `docs/UMU_PREFIX_INIT_TROUBLESHOOTING.md`、`scripts/ark_instance_manager.sh`

---

## 0. 结论速览

| # | 现象 | 根因 | 置信度 |
|---|---|---|---|
| 1 | `KillTree(50660)` 只杀死 launcher，游戏进程 50855 存活 | `kill(-pgid)` 的前提不成立：从 `srt-bwrap` 往下换了 **3 次**进程组/会话，游戏进程自己还是独立会话首进程 | **已确认**（Q1，见 §2.2） |
| 2 | 端口不监听、`Saved/Config/WindowsServer` 180s 不出现 | 降权后的 `asa-umu-runtime` **对 `server-files/` 没有写权限**。缺口不止一处：`Saved`（Q3）、`Binaries/Win64/…/ModsUserData`（Q4）、Sentry 数据目录…… | **已确认**（Q3+Q4，见 §3.6） |
| 3 | （尚未爆发）实例存档/日志/Mods 写不进去 | `instances/<name>/{Save,Logs,Config}` 与 `Mods/ModsUserData` junction 的**目标**都还是 root 属主，`Lchown` 不跟随软链 | **高**（Q4 已证明 junction 目标确实 root 属主） |
| 4 | **`QueryProcess("ArkAscendedServer.exe", …)` 在 Linux 上恒返回空** | Wine 进程的 `/proc/<pid>/exe` 是 `wine64-preloader`、`comm` 是 `GameThread`，name 过滤永远不匹配 → `waitForGamePID` 必然 30s 超时 | **已确认**（Q2，见 §2.5） |
| 5 | verify 启动后 2 秒就宣布通过 | `waitForConfigDir` 在配置目录已存在时立刻返回，而 `verify` 走的是 `force=true`，目录本来就在 —— 判据改为「端口真的监听」 | **已修复**（见 §8） |

> **最终结论：4 个问题全部修复并在真机验证通过（§7.5）。**
> 网络自始至终不是原因 —— 服务端卡住的每一处都是权限。

**不是网络问题。** 理由见 §2.2。

---

## 1. 现象与原始证据

### 1.1 日志

```
{"level":"INFO","caller":"installer/installer.go:413","msg":"Running server verification on port 39862 (this can take up to 3 minutes on first run)..."}
{"level":"INFO","caller":"actions/verify.go:34","msg":"Server process started (launcher PID: 50660). Monitoring log file..."}
{"level":"WARN","caller":"actions/verify.go:34","msg":"Warning: could not find log file initially - open /opt/asa-server/basedir/server-files/ShooterGame/Saved/Logs: no such file or directory"}
{"level":"INFO","caller":"actions/verify.go:34","msg":"Stopping server for verification..."}
```

10:44:55 启动 → 10:48:00 停止，正好是 `waitForConfigDir` 的 180s 超时。

### 1.2 htop 进程链

```
50660  python3 umu-run                          ← Handle.LauncherPID，Setsid，pgid=50660
 └ 50692  srt-bwrap --args 23 /usr/lib/pres...  ← pressure-vessel 在这里进容器
    └ 50730  pv-adverb --generate-locales
       └ 50773  python3 .../GE-Proton10-34/proton waitforexitandrun
          └ 50775  c:\windows\system32\umu.exe
             └ 50855  Z:\...\ArkAscendedServer.exe TheIsland_WP?listen -Port=39862 ...
                      USER=asa-umu-ru  VIRT=3867M  RES=702M  CPU=0.6%  TIME+=0:02.69
```

`KillTree(50660)` 之后：50660 消失，**50692 及其以下整棵树全部存活**。

两个关键读数：

- **`Saved/Logs` 目录不存在**（代码自己打出来的 WARN）。
- **50855 只消耗了 2.69s CPU 就停住了**，之后 3 分钟 ~0% CPU、702M RES 不再增长。

---

## 2. 问题一：KillTree 杀不掉游戏进程

### 2.1 现在的实现

`pkg/procx/procx_linux.go:156`：

```go
func signalGroup(pid int, sig syscall.Signal) error {
	pgid, err := syscall.Getpgid(pid)   // ← 取的是 umu-run(python3) 自己的 pgid
	if err != nil { ... }
	if pgid <= 1 || pgid == os.Getpid() { ... }
	return syscall.Kill(-pgid, sig)
}
```

`internal/runner/runner_linux.go:52` 用 `Setsid: true` 让 launcher 成为组长；
`internal/runner/runner.go:44-46` 的注释断言：

> *"It is, however, the process group id (Run sets Setsid), which is what Close-by-tree operations need"*

`internal/installer/installer.go:417-420` 也复述了同一个假设：

> *"handle.LauncherPID is what procx.KillTree needs below: ... umu-run's PID (== the whole launch's process group leader) on Linux"*

**这个假设在 umu / pressure-vessel 链路上是错的。**

### 2.2 为什么错

`srt-bwrap` 进容器时会 `setsid()`（bubblewrap 的 `--new-session`，pressure-vessel
默认开启，用于防 TIOCSTI 注入）。从这一层往下，**SID 和 PGID 全部是新的**，与
50660 再无任何关系。于是 `kill(-50660)` 只打到 python3 自己，50692 以下原封不动 ——
正是观察到的现象。

同一个盲区还影响 `exec.CommandContext`：ctx 取消时 Go 只 kill launcher 一个进程。

> **Q1 ✅ 已确认（2026-08-29）** 实测输出（另一次启动，PID 不同但结构一致）：
>
> ```
>   PID    PPID    PGID     SID  USER      COMMAND
> 53433   53423   53433   53433  asa-umu+  python3.12        ← umu-run，Setsid 生效
> 53437   53433   53437   53437  asa-umu+  srt-bwrap         ← 断开 ①：新 pgid + 新 sid
> 53475   53437   53475   53475  asa-umu+  pv-adverb         ← 断开 ②
> 53518   53475   53475   53475  asa-umu+  python3 (proton)
> 53520   53518   53475   53475  asa-umu+  umu.exe
> 53596   53475   53596   53596  asa-umu+  GameThread        ← 断开 ③：游戏自己又是独立会话
> ```
>
> **比预想的还糟：不是断开一次，是断开三次。**
> `kill(-53433)` 覆盖的进程组里**只有 python3.12 一个成员**，
> 下游 53437 / 53475 / 53596 三个组一个都碰不到。
>
> 另注意 `53596` 的 PPID 是 `53475`（pv-adverb）而不是 `53520`（umu.exe）——
> 游戏进程是被 wine 侧重新挂上去的，父子关系也不能想当然。

> **Q1b ✅ 已确认** namespace 实测：
>
> ```
> pid: launcher=pid:[4026532222]   game=pid:[4026532222]    ← 相同
> mnt: launcher=mnt:[4026532220]   game=mnt:[4026532784]    ← 不同
> ```
>
> **PID namespace 相同** → 宿主看到的 PID 就是真 PID，`kill(pid)` 直接有效，
> 不需要处理"信号被容器 PID 1 吞掉"的情况。隔离纯粹来自 `setsid()`。
> mnt namespace 不同只是 pressure-vessel 的容器挂载视图，不影响发信号。
>
> 这条结论很关键：**修法可以简单很多**——按 PID 逐个杀就行，
> 不需要 cgroup，也不需要 nsenter。

### 2.3 参考脚本为什么没这个问题

`scripts/ark_instance_manager.sh` 从来不按进程组杀，一律按 **cmdline 匹配**：

| 位置 | 命令 |
|---|---|
| `:690`（初始化验证收尾） | `pkill -f "ArkAscendedServer.exe.*TheIsland_WP"` |
| `:1102` / `:1111`（stop_server） | `pkill -f "ArkAscendedServer.exe.*AltSaveDirectoryName=$SAVE_DIR"` |

这不是脚本图省事，而是唯一能穿透容器边界的办法。

### 2.4 建议的修法：把 Linux 的 `KillTree` 改成真正的"树"

Q1b 证明 PID namespace 是共享的，所以**不需要 cgroup、不需要 nsenter**，
按 PID 逐个杀就行。现在的实现之所以失败，只是因为它把"进程组"当成了"进程树"——
而在 umu 链路上这两者完全不是一回事。

正确做法与 Windows 的 `taskkill /T` 对齐：**先从 `/proc` 的 ppid 图上算出后代集合，
再逐个发信号**。注意顺序——必须先快照再杀，杀完 launcher 后代就被 reparent 到 init 了。

```go
// pkg/procx/procx_linux.go

// descendants 快照 /proc 的 ppid 图，返回 root 的全部后代（含自身）。
// 必须在发任何信号之前调用：一旦父进程死掉，子进程会被 reparent 到 init，
// ppid 链就断了。
func descendants(root int) []int {
	children := map[int][]int{}          // ppid -> []pid
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil { continue }
		if ppid, ok := readPPID(pid); ok { // /proc/<pid>/stat 第 4 字段
			children[ppid] = append(children[ppid], pid)
		}
	}
	var out []int
	stack := []int{root}
	for len(stack) > 0 {
		p := stack[len(stack)-1]; stack = stack[:len(stack)-1]
		out = append(out, p)
		stack = append(stack, children[p]...)
	}
	return out
}

func signalTree(pid int, sig syscall.Signal) error {
	if pid <= 1 { return fmt.Errorf("procx: refusing to signal pid %d", pid) }
	self := os.Getpid()
	targets := descendants(pid)

	// 进程组仍然当兜底用（覆盖已经 reparent、ppid 链断掉的残骸），
	// 但它现在是补充手段，不再是主要手段。
	seen := map[int]bool{}
	for _, p := range targets { seen[p] = true }
	for _, p := range targets {
		if pgid, err := syscall.Getpgid(p); err == nil && pgid > 1 && pgid != self {
			_ = syscall.Kill(-pgid, sig)
		}
	}
	// 叶子优先，避免杀掉中间层导致后续 PID 复用误伤
	for i := len(targets) - 1; i >= 0; i-- {
		if targets[i] == self { continue }
		_ = syscall.Kill(targets[i], sig)
	}
	return nil
}
```

`TerminateTree` = `signalTree(pid, SIGTERM)`，`KillTree` = `signalTree(pid, SIGKILL)`。
签名和语义都不变，所有调用点（`installer.go:453/458`、`instance/common.go:107-109`、
`instance/server.go:659`）不用动。

**配套要点：**

1. **停止顺序要讲究。** 直接 SIGKILL 整棵树会让 UE 没机会落盘。建议：
   先 `SIGTERM` 游戏进程本体（§2.5 给出如何找到它），等 grace 后再
   `KillTree(launcherPID)` 收尾。参考脚本的 `stop_server` 也是先 RCON `DoExit`
   再 `pkill`。

2. **收尾要等 `wineserver` 退干净**，否则下次启动撞上仍占着 prefix 的旧 wineserver。
   `umu_linux.go:413` 的 `waitForWineserverDrain` 已经在做这件事，但它用的是
   `QueryProcess("wineserver", prefix)` —— `wineserver` 的 cmdline 里**不含**
   WINEPREFIX（那是环境变量），这个查询很可能恒返回空、函数直接秒退。
   建议改成读 `/proc/<pid>/environ` 里的 `WINEPREFIX=`，或干脆放宽成"有没有
   wineserver 在跑"。**（待验证，见 Q6）**

3. 顺手修掉 `runner.go:44-46` 和 `installer.go:417-420` 两处已被 Q1 证伪的注释——
   它们明确断言 LauncherPID 是"整个启动的进程组组长"，这是错的。

---

### 2.5 【Q2 挖出的新问题】`QueryProcess` 的 name 过滤在 Linux 上恒不匹配

> **Q2 ✅ 已确认（2026-08-29）** 实测输出：
>
> ```
> $ readlink /proc/53596/exe
> /opt/asa-server/basedir/proton/GE-Proton10-34/files/bin/wine64-preloader
> $ cat /proc/53596/comm
> GameThread
> $ tr '\0' ' ' < /proc/53596/cmdline
> Z:\opt\asa-server\basedir\server-files\ShooterGame\Binaries\Win64\ArkAscendedServer.exe \
>   TheIsland_WP?listen -Port=39228 -NoBattlEye -crossplay -server -log -nosteamclient -game
> ```

把这个结果代进 `pkg/procx/procx_linux.go:75-78`：

```go
procName := processImageBaseName(pid)          // → "wine64-preloader"
if nameLower != "" && !strings.Contains(strings.ToLower(procName), nameLower) {
	continue                                   // "wine64-preloader" 不含 "arkascendedserver.exe" → 恒 continue
}
```

- `exe` 解析成功 → `procName = "wine64-preloader"`，**不含** `arkascendedserver.exe`
- 就算 `exe` 读不到退化到 `comm`，也是 `GameThread`，同样不含

**结论：`procx.QueryProcess("ArkAscendedServer.exe", …)` 在 Linux 上永远返回空数组。**

受影响的调用点：

| 位置 | 后果 |
|---|---|
| `internal/instance/common.go:137`（`waitForGamePID`） | **必然 30s 超时** → `StartServer` 返回 `"ArkAscendedServer.exe did not appear within 30 seconds"`，但游戏进程其实已经起来了 → **每次启动都留下一棵孤儿树** |
| `internal/instance/common.go:477` | 同样恒空 |

这解释了 §1.2 第一张截图里**同一个 exe 堆了几十个进程**的现象：
每次"启动失败"都留一棵，而失败的原因不是启动失败，是**找不到自己刚起的进程**。

> ⚠️ 这也意味着"实例已经可以启动了"这个前提需要重新确认 ——
> 见 Q5。进程起来 ≠ `StartServer` 成功返回。

**修法：**

1. `QueryProcess` 的 `name` 参数在这些调用点一律传 `""`，只用 cmdline 匹配。

2. **marker 必须能把游戏本体和 5 个包装进程区分开。** 从 Q2 的输出可以看到，
   下面这些进程的 cmdline 里**都含** `ArkAscendedServer.exe`：

   ```
   53433 python3.12 .../umu-run /opt/.../ArkAscendedServer.exe ...
   53475 pv-adverb  ... /opt/.../ArkAscendedServer.exe ...
   53518 python3 .../proton waitforexitandrun /opt/.../ArkAscendedServer.exe ...
   53520 c:\windows\system32\umu.exe /opt/.../ArkAscendedServer.exe ...
   53596 Z:\opt\...\ArkAscendedServer.exe ...        ← 只有这个是真身
   ```

   **判别式：只有游戏本体用反斜杠路径。** 包装进程一律是 Unix 正斜杠
   `/…/ArkAscendedServer.exe`，游戏本体是 `Z:\…\ArkAscendedServer.exe`。
   所以 marker 用 `` `\ArkAscendedServer.exe` ``（**带前导反斜杠**）即可唯一锁定，
   而且这个写法在 Windows 上（`C:\…\ArkAscendedServer.exe`）同样成立，
   两个平台可以共用一个常量。

3. 再叠加本次启动独有的片段做二次确认：
   - verify → `fmt.Sprintf("-Port=%d", port)`（随机空闲端口，天然唯一）
   - instance → `"AltSaveDirectoryName="+saveDir`（与参考脚本 `:1102` 一致）

4. `QueryProcess` 扫的是 `/proc` 的顶层目录项，只包含线程组组长，
   所以截图里 53597 及以后那几十个线程不会造成重复命中 —— 这一点是安全的。

---

## 3. 问题二：端口不监听 / 配置目录不生成

### 3.1 真正的线索不是"端口"，是那行 WARN

```
could not find log file initially - open .../ShooterGame/Saved/Logs: no such file or directory
```

UE4 在引擎初始化的**头几秒**就会建 `Saved/Logs/ShooterGame.log`，
远早于任何网络、Steam、地图加载动作。3 分钟后它还不存在，
说明进程压根没走过"能往磁盘写东西"这一关。

再对上 htop：50855 占了 702M RES，但 `TIME+` 只有 `0:02.69`，CPU 0.6%。
**跑了两秒多然后彻底停住** —— 典型的 Wine 下弹了一个无人可点的模态错误框
（无头环境永远等不到点击）。

### 3.2 为什么排除网络

如果是网络卡住（Steam 登录、`api.arkdedicated.com`），UE 早就把
`Saved/Logs/ShooterGame.log` 建出来了，你会看到一个**有内容的日志文件停在某一行**。
现在是**目录本身都不存在**，这只能是文件系统层的失败。

### 3.3 根因：`server-files/` 从未交给降权用户

`internal/runner/runtimeuser_linux.go:232` 的 `rwSubtrees()`：

```go
func rwSubtrees(cfg Config, includeMirrors bool) []string {
	out := []string{
		prefixDir(cfg, ""),                        // umu-prefix
		filepath.Join(cfg.BaseDir, "clusters"),    // clusters
	}
	// + umu-prefix-*，+（可选）server-files-tmp-*
}
```

`server-files` **不在里面**。而 SteamCMD 是以 asa-server 自身身份（root）跑的
（`internal/installer/installer.go:289`，没有降权），所以：

```
/opt/asa-server/basedir/server-files/ShooterGame/   root:root  drwxr-xr-x
```

游戏进程被降到 `asa-umu-runtime`（htop 里 USER 列的 `asa-umu-ru`），
**无法在 `ShooterGame/` 下 mkdir `Saved`**。于是：

- ❌ `Saved/Logs` 建不出来 → 日志文件找不到（那行 WARN）
- ❌ `Saved/Config/WindowsServer` 建不出来 → `waitForConfigDir` 180s 超时
- ❌ 引擎初始化失败 / 弹框 → 端口永远不监听

**三个现象一个原因，全部对得上。**

> **Q3 ✅ 已确认（2026-08-29）** 用户实测输出：
>
> ```
> $ sudo -u asa-umu-runtime mkdir -p /opt/asa-server/basedir/server-files/ShooterGame/Saved \
>     && echo WRITABLE || echo DENIED
> mkdir: cannot create directory ‘/opt/asa-server/basedir/server-files/ShooterGame/Saved’: Permission denied
> DENIED
> ```
>
> **问题二根因坐实：降权用户对 `server-files/ShooterGame/` 无写权限。**
> 后续 §3.5 的修法可以直接实施，不再依赖其他确认项。

### 3.4 次要嫌疑：`-nosteamclient`

`internal/installer/installer.go:428` 给验证启动加了 `-nosteamclient`，而

- 参考脚本的初始化启动（`ark_instance_manager.sh:653-659`）**没有**
- 本项目的实例启动（`internal/config/config.go:345` 的
  `CustomStartParameters = "-NoBattlEye -crossplay -NoHangDetection"`）**也没有**

这是"实例能起、verify 起不来"的另一个差异点。

但它解释不了 `Saved/Logs` 缺失（那发生在引擎初始化更早的位置），
所以排在权限之后。修完权限如果还卡，第二个动作就是去掉它、与脚本严格对齐。

参数对比：

| | 参考脚本初始化 | 本项目 verify |
|---|---|---|
| map | `TheIsland_WP?listen` | `TheIsland_WP?listen` |
| 端口 | 不指定（默认 7777） | `-Port=<随机空闲端口>` |
| 其他 | `-NoBattlEye -crossplay -server -log -game` | `-NoBattlEye -crossplay -server -log **-nosteamclient** -game` |

### 3.5 建议的修法

**(a) 立刻可验证的临时手法**

```bash
mkdir -p /opt/asa-server/basedir/server-files/ShooterGame/Saved
chown -R asa-umu-runtime:asa-umu-runtime /opt/asa-server/basedir/server-files/ShooterGame/Saved
asa-server verify
```

**(b) 代码修正**

1. `VerifyServerInstallation` 在 `runner.Run` 之前显式准备可写目录：

   ```go
   savedDir := filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Saved")
   if err := os.MkdirAll(savedDir, 0o755); err != nil { ... }
   if err := runner.ChownTreeForRuntime(savedDir); err != nil {   // Windows 上是 no-op
       logger.Warnf("failed to hand %s to the runtime user: %v", savedDir, err)
   }
   ```

2. `rwSubtrees()` 加上 `server-files/ShooterGame/Saved`，让启动时的
   `reconcileRuntimeOwnership` 一劳永逸兜住。
   **不要整个 `server-files` 递归 chown** —— 几十 GB，太贵；只需 `Saved` 子树。

3. `deepProbeWrite`（`runtimeuser_linux.go:466`）目前只探 `umu-prefix`，
   建议把 `server-files/ShooterGame/Saved` 也纳入。
   这次的故障本可以在 preflight 阶段秒报，而不是烧掉 180s 超时。

**(c) 可诊断性（强烈建议）**

`internal/runner/runner_linux.go:42-45`：

```go
cmd := exec.CommandContext(ctx, bin, launchArgs...)
cmd.Dir = opt.Dir
cmd.Env = env
cmd.Stdin = nil
// Stdout / Stderr 未设置 → 全进 /dev/null
```

参考脚本是 `> "$init_log" 2>&1`（`ark_instance_manager.sh:646,660`）
和 `>> "$server_log" 2>&1`（`:1009`）。

这次之所以只能靠 htop 猜，就是因为 umu / proton / wine 打出来的所有报错 ——
包括那个大概率存在的"无法创建目录"—— 全进了黑洞。

建议给 `runner.Options` 加 `Stdout io.Writer`：

- verify 路径 → `{BaseDir}/logs/verify-launch.log`
- 实例路径 → `instances/<name>/server.log`（与脚本一致）

---

### 3.6 【Q4 挖出的下一层挡板】只 chown `Saved` 不够，`Binaries/Win64` 也要写

手工 `chown -R asa-umu-runtime Saved` 之后重跑，**游戏前进了一大步**：
`Saved/Logs/ShooterGame.log` 生成了，引擎跑到了 `ARK Version: 93.7`。
但紧接着停在这里：

```
[03.38.15:970] LogSentrySdk: Warning: failed to create database directory or there is no write access to this directory
[03.38.17:714] LogCFCore: Error: Unable to create a directory ../../../ShooterGame/Binaries/Win64/ShooterGame/ModsUserData/83374/local
[03.38.17:715] LogCFCore: Error: Unable to create a directory ../../../ShooterGame/Binaries/Win64/ShooterGame/ModsUserData/83374
[03.38.17:748] LogCFCore: Warning: Failed to save UserContextInfo to disk
[03.38.17:748] LogCFCore: Warning: Failed to save SharedContextInfo to disk
[03.38.18:414] LogCFCore: Warning: Failed to save SharedContextInfo to disk
                                    ← 日志到此为止，进程仍在，CPU ~0%
```

对上目录属主：

```
ShooterGame/
  drwxr-xr-x root            root             Binaries      ← ModsUserData 要写在这下面
  drwxr-xr-x root            root             Content
  drwxr-xr-x root            root             Plugins
  drwxr-xr-x asa-umu-runtime asa-umu-runtime  Saved         ← 手工 chown 过的，已经能写
```

**同一个根因的第二层：`ShooterGame/Binaries/Win64/ShooterGame/ModsUserData/`
（CurseForge mod 管理器的用户数据目录）在 root 手里，降权进程建不出来。**

#### 这不是 verify 独有的 —— 实例走的是同一条死路

`internal/mirror/mirror.go:46` 和 `:261`：

```go
const win64SharedRelPath = win64RelPath + "/ShooterGame"   // = ShooterGame/Binaries/Win64/ShooterGame
...
// Mods / ModsUserData 指回源目录：……全实例共享
targets[win64SharedRelPath] = filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64SharedRelPath))
```

镜像里这条是 **junction 指回 root 拥有的 `server-files`**，
而 `chownMirrorForRuntime` 用的是 `Lchown`（不跟随软链）——
**实例启动时 CFCore 会撞上完全一样的墙**，报的是完全一样的错。
错误信息里的相对路径 `../../../ShooterGame/Binaries/Win64/ShooterGame/ModsUserData/83374`
与这个常量逐字对应。

#### 结论：别再打地鼠，直接把 `server-files` 整体交出去

已经踩到的写入点：`Saved`（Q3）、`Binaries/Win64/ShooterGame/ModsUserData`（Q4）、
Sentry 的 database 目录（同一条日志里的 warning）。还没踩到但同类的：
`ShooterGame/Mods`、`ShooterGame/Plugins`（ArkApi）、Win64 下的崩溃转储。

参考脚本从来没遇到这些，是因为**它压根不降权**——server-files 的属主和跑游戏的用户
天然是同一个。本项目引入了 `asa-umu-runtime` 却只 chown 了 prefix 和 clusters，
这个缺口注定要一层层地暴露。

**建议改成：SteamCMD 更新完成后，把整个 `server-files` 递归 chown 给 runtime 用户。**

```go
// installer.go：DownloadAndUpdateArkServer 末尾、ApplyLinuxFixups 之后
if err := runner.ChownTreeForRuntime(cfgpkg.ServerFilesDir); err != nil {
    logger.Warnf("failed to hand server-files to the runtime user: %v", err)
}
```

- 成本可接受：chown 只改元数据，不动内容。ASA 的 server-files 约 5 万个文件，
  SSD 上 `WalkDir + Lchown` 是秒级，而且只在每次更新后跑一次。
- asa-server 自己是 root，chown 之后照样读写，管理端功能不受影响。
- `rwSubtrees()` 里也要加上 `server-files`，让启动时的 `reconcileRuntimeOwnership`
  兜住"用户手工动过属主"或"从别的机器搬 BaseDir 过来"的情况。
- 更彻底的方案是**让 SteamCMD 自己以 runtime 用户身份跑**，文件生下来属主就是对的，
  省掉每次 chown。但那要求 `steamcmd/` 目录也归它，改动面更大，建议作为后续优化。

> **Q7（待确认）** chown 掉 `Binaries` 之后如果**还是**停在 CFCore 那几行，
> 那就轮到网络嫌疑了：CFCore 初始化会连
> `https://83374.api.curseforge.com` 和 `https://analyticsnew.overwolf.com`，
> 配置里 `httpFileDownloadTimeoutSecs: 1200`（20 分钟）。
> 国内网络到 overwolf/curseforge 大概率不通，卡在这里完全说得通。
>
> ```bash
> curl -sS -m 10 -o /dev/null -w 'curseforge=%{http_code} %{time_total}s\n' https://83374.api.curseforge.com
> curl -sS -m 10 -o /dev/null -w 'overwolf=%{http_code}   %{time_total}s\n' https://analyticsnew.overwolf.com/analytics/Counter
> ```
>
> 两个都超时 → 参考脚本能跑通说明这不该是硬阻塞，但值得作为
> "为什么冷启动特别慢"的解释保留。

---

### 3.7 属主方案的选型：`chown -R` vs 用户组 vs 不降权

> 提出的问题：**"如果 server-files 和实例目录都交给 asa-umu-runtime，
> 那以后直接在服务器上传 ArkApi 插件会不会又出新问题？能不能把
> asa-umu-runtime 和 root 放进同一个组，组内可读写，这样简单很多？"**

#### 先回答"上传插件会不会再炸"

分两条路径，结论不同：

| 路径 | 会不会炸 | 原因 |
|---|---|---|
| **实例启动** | **不会，自愈** | ArkApi 在 `ShooterGame/Binaries/Win64/ArkApi`，落在 `win64RelPath` 的**完整复制区**（`mirror.go:21-24`）。每次启动重建镜像时把它真实拷贝一份，再由 `chownMirrorForRuntime` 整棵 `Lchown` 给 runtime 用户。上传的 root 属主文件只是"源"，asa-server 以 root 读它做拷贝，读没有障碍 |
| **verify / 任何直接跑 `server-files` 的路径** | **会炸** | 没有镜像这一层，游戏直接在 root 属主的树上写 |
| **`Mods` / `ModsUserData`** | **会炸** | 它在镜像里是 junction **指回 server-files**（`mirror.go:261`），`Lchown` 不跟随软链，目标永远是 root 属主 —— 这就是 §3.6 里 CFCore 报的那个错 |

所以担心的方向是对的：**任何"以 root 身份新增文件到 runtime 需要写的目录"都会重新破**，
`chown -R` 只是把当下修好，下一次 SFTP 上传、下一次 SteamCMD 更新又回到原点
（SteamCMD 也是 root 跑的，`update` 之后整棵树的新文件又是 root 属主）。

#### 用户组方案：方向对，但有三个必须修正的点

**1. root 不需要加进任何组。**

root 持有 `CAP_DAC_OVERRIDE`，无视一切属主与权限位。把 root 加进组不会带来任何新能力。
真正要解决的命题不是"root 能不能访问"，而是
**"root 创建出来的文件，asa-umu-runtime 能不能写"**。

**2. 光有组不够，关键在"继承"。** 三件事缺一不可：

```bash
G=asa-umu-runtime          # 用它的主组，理由见第 3 点

# ① 存量：改组 + 给组读写（X = 只给目录和已有可执行文件加 x）
chgrp -R "$G" /opt/asa-server/basedir/server-files
chmod -R g+rwX             /opt/asa-server/basedir/server-files

# ② 新建条目继承组：setgid 位，只加在目录上
find /opt/asa-server/basedir/server-files -type d -exec chmod g+s {} +

# ③ 新建条目继承"组可写"：setgid 只继承组，不继承权限位！
#    root 用默认 umask 022 上传出来的文件是 rw-r--r--，组仍然只读。
#    必须靠默认 ACL 补这一刀：
setfacl -R  -m  g:"$G":rwX /opt/asa-server/basedir/server-files
setfacl -R -d -m g:"$G":rwX /opt/asa-server/basedir/server-files
```

**②③ 缺任何一个都会漏**：只有 setgid → 新文件组对但只读；
只有 ACL 没 setgid → 也能工作（默认 ACL 本身就带组条目），
但两个一起上更稳，且 `ls -l` 能直接看出来。

**3. 必须用 `asa-umu-runtime` 的主组，不要新建一个组。**

`internal/runner/runtimeuser_linux.go:76-80`：

```go
cred := &syscall.Credential{
	Uid:    uint32(uid),
	Gid:    uint32(gid),
	Groups: []uint32{uint32(gid)}, // explicit, avoids setgroups([]) ambiguity
}
```

**附加组没有被填进去。** 如果新建一个 `asa-server` 组、把 asa-umu-runtime 加进去，
降权后的进程**不会**带上这个组，所有组权限全部落空 —— 而且现象会是
"权限位看着完全正确但就是写不了"，极难排查。

用主组就没有这个问题，零代码改动。如果确实想用独立组，
必须同时把它加进 `Groups`，并且 `useradd` 之后要 `usermod -aG`。

#### 三个方案的取舍

| 方案 | 上传插件后 | 更新后 | 代码改动 | 依赖 |
|---|---|---|---|---|
| **A. `chown -R` 到 runtime 用户** | 破，需重跑 | 破，需重跑 | 小（installer 里加一次调用 + `rwSubtrees()`） | 无 |
| **B. 组 + setgid + 默认 ACL** | **不破** | **不破** | 中（`reconcileRuntimeOwnership` 改成设组/ACL，需 shell out `setfacl`） | 文件系统 ACL 支持 + `acl` 包 |
| **C. `linux.umu_run_as_root: true`** | 不破 | 不破 | **零** | 无 |

**推荐：B 作为目标形态，A 作为兜底。**

- B 是唯一能真正终结这个问题的方案。**它就是提出的组方案，只是要补上
  setgid + 默认 ACL 这两条继承规则，并且别把 root 加进组。**
- A 仍然要保留：`reconcileRuntimeOwnership` 在每次 asa-server 启动时跑，
  作为"有人手工动过属主 / 从别的机器搬了 BaseDir 过来"的自愈路径。
  两者不冲突 —— A 修存量，B 管增量。
- C 是完全正当的选择，也正是 `scripts/ark_instance_manager.sh` 的做法
  （它压根不降权，server-files 属主与跑游戏的用户天生同一个，
  所以整类问题在参考实现里根本不存在）。
  单用途游戏服务器上这个取舍很合理，代价是游戏进程以 root 运行。
  项目已经留了这个开关，文档里应该把它写成一等公民而不是"逃生舱"。

#### 实施要点

- ACL 需要文件系统支持。ext4 / xfs / btrfs 默认开启，
  但 `setfacl` 属于 `acl` 包，容器精简镜像里常常缺。
  应当在 `preflight_linux.go` 里加一项检查：`setfacl` 是否存在、
  目标文件系统是否真的支持（`setfacl` 试写一个探针再读回）。
- Go 标准库没有 ACL API，只能 shell out `setfacl` ——
  这与现在 shell out `useradd` / `groupadd`（`runtimeuser_linux.go:134-173`）是同一个路子，
  不引入新的架构负担。
- 缺 `setfacl` 时**自动降级到方案 A**，并在 preflight 里给出提示，
  而不是直接失败。

---

## 4. 问题三：同类风险，还没爆发但会爆发

`instances/<name>/{Save,Logs,Config}` 也**从未 chown 给运行时用户**：

- `internal/mirror/mirror.go:248-257` 把
  `ShooterGame/Saved/Config/WindowsServer`、`Saved/Logs`、`Saved/<SaveDir>`
  做成软链，指向 `instances/<name>/...`
- `chownMirrorForRuntime` 用的是 `Lchown`，**不跟随软链**
  （`runtimeuser_linux.go:248-249` 的注释里写明了："their targets in
  server-files stay root-owned"）—— 所以链接本身改了属主，**目标目录还是 root:root**
- `rwSubtrees()` 里也没有 `instances/`

也就是说现在"能启动"的实例，很可能**写不了存档、也写不了 `ShooterGame.log`**。

> **Q4（待确认）**
>
> ```bash
> ls -l /opt/asa-server/basedir/instances/<实例名>/Logs/
> ls -l /opt/asa-server/basedir/instances/<实例名>/Save/
> ls -ld /opt/asa-server/basedir/instances/<实例名>/{Logs,Save,Config}
> ```
>
> 如果 `Logs` 是空的、`Save` 里没有随时间增长的 `.ark`，属主又是 root ——
> 那就是同一个根因的第二个现场，"实例启动成功"只是进程起来了而已。

修法与 §3.5 一致：把 `instances/` 加进 `rwSubtrees()`
（asa-server 自己是 root，chown 之后照样能写，无副作用）。

> **Q5（待确认）** 实例究竟"成功"到哪一步？`StartServer` 里
> `waitServerStartup(pid, gameLogPath, ...)` 是靠读游戏日志判断启动完成的。
> 如果日志写不出来，状态会一直停在 `starting` 而不会推进到 `started`。
> 请确认 BadgerDB 里该实例的最终状态，以及 RCON 是否真的能连上。

---

## 5. 建议的落地顺序

Q1/Q1b/Q2/Q3 已确认，下面按"能立刻止血 → 能防止再次踩坑"排序：

1. ✅ ~~证伪 Q3~~（已确认 DENIED）。
2. **修 §2.5 的 `QueryProcess` name 过滤** —— 这是优先级最高的一条。
   不修的话每次实例启动都会 30s 超时并留一棵孤儿树，
   后面所有的验证现场都会被污染，排查成本翻倍。
3. **修 §2.4 的 `KillTree`** —— 让 stop 真的能停掉，与 2 是一对。
4. **修 §3.6：SteamCMD 更新后 chown 整棵 `server-files`**，
   `rwSubtrees()` 同步补上（`Saved` 单点 chown 已被 Q4 证明不够）。
   deepProbe 也扩展到 `server-files`。
5. 加启动日志落盘（§3.5 c）—— 这次全程靠 htop 猜，就是因为 stdout 进了 `/dev/null`。
6. 处理 §4 的实例存档/日志权限（待 Q4 确认范围）。
7. 若第 4 步之后 verify 仍卡，再去掉 `-nosteamclient` 与脚本对齐（§3.4）。

改动集中在 `procx_linux.go` / `instance/common.go` / `runner_linux.go` /
`runtimeuser_linux.go` / `installer.go` 五个文件，
Windows 侧全部走 no-op 或既有分支，预期无回归。

---

## 6. 待确认清单（供逐条回复）

| 编号 | 问题 | 验证命令 | 你的回答 |
|---|---|---|---|
| Q1 | 游戏进程的 SID/PGID 是否与 launcher 断开？ | 见 §2.2 | ✅ **断开 3 次**，launcher 的进程组里只有它自己 |
| Q1b | 是 setsid 还是 PID namespace？ | 见 §2.2 | ✅ **PID ns 相同**，纯 setsid → 按 PID 直接 kill 即可 |
| Q2 | Wine 进程的 `/proc/<pid>/exe` 和 `comm` 是什么？ | 见 §2.5 | ✅ `wine64-preloader` / `GameThread` → **name 过滤恒不匹配** |
| Q3 | `asa-umu-runtime` 能否在 `ShooterGame/` 下建 `Saved`？ | 见 §3.3 | ✅ **DENIED** —— 不能。根因坐实 |
| Q4 | 日志/配置生成了吗？卡在哪？ | 见 §3.6 | ✅ **生成了**，但卡在 `ModsUserData` 建不出来 → 挡板下移一层 |
| Q5 | 端口监听了吗？ | 见 §7.5 | ✅ **修复后正常启动**，引擎跑到 `Initialize Primal Game Data Override.` |
| Q6 | `waitForWineserverDrain` 是不是空转？ | 见下 | ✅ **是空转**，见下方实测 |
| Q7 | CFCore 的 curseforge/overwolf 能连通吗？ | 见 §3.6 | ✅ **不需要了** —— 权限修好后 CFCore 直接越过，网络从来不是阻塞点 |
**Q6 ✅ 已确认** 实测：

```
$ pgrep -a wineserver
54387 /opt/asa-server/basedir/proton/GE-Proton10-34/files/bin/wineserver
$ tr '\0' '\n' < /proc/54387/environ | grep -i wineprefix
WINEPREFIX=/opt/asa-server/basedir/umu-prefix/pfx/
```

cmdline 里**只有二进制路径**，prefix 只存在于环境变量。
所以 `umu_linux.go:416` 的 `QueryProcess("wineserver", prefix)` 恒返回空，
`waitForWineserverDrain` 一进去就 return —— **90 秒的等待逻辑从来没生效过**。

修法：改成读 `/proc/<pid>/environ` 匹配 `WINEPREFIX=`。
注意 umu 实际写进去的是 `<prefix>/pfx/`（多一层 `pfx` 和结尾斜杠），
所以匹配要用配置里的 prefix 作**前缀**，不能要求全等。

**Q5 的验证命令**（§2.5 之后这条变得关键 —— 需要确认 `StartServer` 到底是
返回成功还是 30s 超时）：

```bash
# 起一个实例，然后立刻看 asa-server 自己的日志有没有这行
journalctl -u asa-server -n 100 --no-pager | grep -i "did not appear\|Server started for instance"
# 或直接看日志文件
grep -n "did not appear\|Server started for instance" /opt/asa-server/basedir/logs/asaServer.log | tail -20
```

打出 `ArkAscendedServer.exe did not appear within 30 seconds` → §2.5 完全坐实。

**Q6 的验证命令**：

```bash
pgrep -a wineserver
# 若有进程，看它的 cmdline 里到底有没有 prefix 路径
for p in $(pgrep wineserver); do echo "--- $p"; tr '\0' ' ' < /proc/$p/cmdline; echo; \
  tr '\0' '\n' < /proc/$p/environ | grep -i wineprefix; done
```

如果 cmdline 里没有 prefix 路径（只有环境变量里有），
那 `umu_linux.go:416` 的 `QueryProcess("wineserver", prefix)` 就是恒空、秒退，
`waitForWineserverDrain` 从来没起过作用。


---

## 7. 实施记录（2026-08-29）

方案 **B（组 + setgid + 默认 ACL）+ A（chown）兜底**，已全部落地。
`go build` / `go vet` 在 `GOOS=windows` 与 `GOOS=linux` 下均通过。

### 7.1 改动清单

| 文件 | 改动 | 对应 |
|---|---|---|
| `pkg/procx/procx_linux.go` | `TerminateTree`/`KillTree` 从 `kill(-pgid)` 改为**真正的进程树**：`processTree()` 先快照 `/proc` 的 ppid 图算出全部后代，再叶子优先逐个发信号；进程组只作为兜底，且**只扫由树内成员领导的组**。新增 `readPPID`/`parsePPIDFromStat` | §2.4 |
| `pkg/procx/procx_linux_test.go` | 新增：`stat` 里 comm 含空格与右括号的解析、`readPPID` 与 `Getppid` 对齐、**自建 setsid 子会话的孙进程仍能被 `processTree` 找到**（本次问题的回归测试）、拒绝对 pid 1 发信号 | 新增 |
| `internal/instance/gameproc_{linux,windows}.go` | 新增：游戏进程查找按平台拆分。Linux 丢掉 name 过滤（`exe` 是 `wine64-preloader`、`comm` 是 `GameThread`），改用 cmdline + **前导反斜杠**区分游戏本体与 4 个包装进程；**Windows 原样保留 WMI name 过滤** | §2.5 |
| `internal/instance/common.go` | `waitForGamePID` 与 `findServerPIDBySaveDir` 改调 `queryGameProcesses`；新增 `arkExeName` 常量 | §2.5 |
| `internal/runner/sharedaccess_linux.go` | 新增：`prepareSharedTree` / `applySharedAccess` / `chgrpSetgidTree` / `applyDefaultACL` / `aclSupported` / `checkACLSupport`。整套方案 B，含 `errACLUnsupported` 降级到方案 A | §3.6 §3.7 |
| `internal/runner/runtimeuser_linux.go` | `reconcileRuntimeOwnership` 增加 `sharedSubtrees`（`server-files`、`instances`）一轮，前置 `sharedAccessNeeded` 采样避免每次启动全树遍历 | §3.6 |
| `internal/runner/runner.go` | 新增 `PrepareSharedTree` 导出；`Options.Log` 字段；**改正 `Handle` 上"LauncherPID 是整个启动的进程组组长"这句已被 Q1 证伪的注释** | §2.4 §3.5c |
| `internal/runner/runtimeuser_windows.go` | `prepareSharedTree` 空实现 | 跨平台 |
| `internal/runner/runner_{linux,windows}.go` | `opt.Log != nil` 时接管 stdout/stderr；为 nil 时行为与之前**完全一致** | §3.5c |
| `internal/runner/umu_linux.go` | `waitForWineserverDrain` 改走 `/proc/<pid>/environ` 的 `WINEPREFIX`（前缀匹配，因为 umu 导出的是 `<prefix>/pfx/`） | Q6 |
| `internal/runner/preflight_linux.go` | 接入 `checkACLSupport()` | §3.7 |
| `internal/installer/installer.go` | 更新后无条件 `PrepareSharedTree(server-files)`；verify 前建 `ShooterGame/Saved` + 再跑一次；启动输出落 `{BaseDir}/logs/verify-launch.log`；**改正 KillTree 相关注释** | §3.5 §3.6 |
| `internal/instance/server.go` | 启动时对 `instances/<name>` 跑 `PrepareSharedTree` —— 镜像里 Config/Logs/Save 是软链，`Lchown` 改不到目标 | §3.6 §4 |
| `pkg/procx/port.go` | 新增 `PortInUse`：只回答「端口是否被占用」，不要求能归属到 PID（容器里的进程 gopsutil 常常归属不出来）。**明确不用「试着 bind」实现**——那会和正在启动的服务端抢端口 | §8 |
| `internal/installer/installer.go` | `waitForConfigDir` → `waitForVerificationServer`：成功判据改为**端口真的监听**；进程提前死亡立刻失败；失败时打印两份日志尾部；总预算 180s → 5min | §8 |
| `internal/installer/wait_startup_test.go` | 替换 `wait_config_dir_test.go`：覆盖「配置目录已存在不算成功」「端口打开才算成功」「进程死了立刻失败」「探测报错不算成功」「两种超时文案可区分」 | §8 |

### 7.2 与原设计的差异

- §2.4 里叫 `descendants()` 的函数实现为 `processTree()`，语义一致。
- **没有实现 `stopLaunch(marker)`。** 把 `KillTree` 修成真正的树之后它就多余了：
  游戏进程本来就是 launcher 的后代，`KillTree(LauncherPID)` 直接覆盖到。
  marker 匹配只在"找 PID"（`queryGameProcesses`）这一侧需要，杀进程侧不需要。
- 停止顺序不变：仍是 RCON `DoExit` → 等待 → 兜底 `KillTree`。
  注意 `killGameServer` 收到的是**游戏进程 PID** 而非 launcher，
  所以树的范围就是游戏及其子进程，不会把 bwrap/umu-run 一起打掉 ——
  它们会在游戏退出后由 `proton waitforexitandrun` 自行收尾，
  与参考脚本 `pkill -f ArkAscendedServer.exe` 的语义一致。

### 7.3 Windows 影响面

逐条核对，唯一的行为变化是 verify 的启动输出现在会落到
`{BaseDir}/logs/verify-launch.log`（以前丢弃）。这条在 Windows 上同样有诊断价值，
且文件打不开时 `launchLog` 保持 nil、回到旧行为。其余：

- `procx_windows.go`、`wmi_windows.go`：**未改动**
- `gameproc_windows.go`：与改动前逐字等价的 WMI 查询
- `runner_windows.go`：只多了 `opt.Log != nil` 分支，现有调用方全部传 nil
- `PrepareSharedTree` / `prepareSharedTree`：Windows 恒 `return nil`
- `os.MkdirAll(ShooterGame/Saved)`：Windows 上提前建一个空目录，
  与 `configDir`（`Saved/Config/WindowsServer`）的存在性判断无关

### 7.5 真机验证结果（2026-08-29，全部通过）

#### 7.5.1 四项验证

| 项 | 结果 |
|---|---|
| 服务端能否越过 CFCore | ✅ `ShooterGame.log` 一路跑到 `UShooterEngine::LoadGameMods with 0 mods` → `Initialize Primal Game Data Override.` → `Primal Game Data Took 0.02 seconds`。**`Initialize Primal Game Data Override` 正是 `waitServerStartup` 用的启动完成标记** |
| 启动日志有没有落盘 | ✅ `verify-launch.log` 完整记录了 umu 1.4.4 → pressure-vessel → ProtonFixes → `Proton: ... launching with /unix option` → `fsync: up and running` 全链路 |
| `waitForGamePID` 是否还超时 | ✅ `grep -c "did not appear within 30 seconds" asaServer.log` = **0**（§2.5 生效） |
| 停止后有没有孤儿树 | ✅ `pgrep -af 'Z:.*ArkAscendedServer\.exe'` **无输出**（§2.4 生效） |

**Q7 因此不需要回答了**：CFCore 之所以"卡住"，从头到尾都是写不了
`ModsUserData` 导致的，不是连不上 curseforge。修好权限后它自己就过去了
（`Couldn't load mods library from disk` 降级成一条 Warning，不再阻塞）。

#### 7.5.2 曾经的遗留：这台机器一度跑在方案 A 兜底上（**已解决，见 7.5.2.2**）

```
$ ls -ld .../ShooterGame/Binaries
drwxrwsr-x 3 asa-umu-runtime asa-umu-runtime ...      ← setgid 有了（那个 s）
$ getfacl -p .../ShooterGame 2>/dev/null | head -20
                                                       ← 空输出
```

`getfacl` 存在的话，即使目录没有扩展 ACL 也会打印基础条目
（`# file:` / `user::rwx` / `group::rwx` / `other::r-x`）。**完全没有输出 =
`getfacl` 没安装**，也就意味着 `setfacl` 同样没有 —— `applyDefaultACL` 走了
`findAdminTool("setfacl") == ""` 这条路，返回 `errACLUnsupported`，
`applySharedAccess` 降级成 `chownTree`。

属主是 `asa-umu-runtime` 而不是 root，也印证了这一点（方案 B 会把属主留在 root）。

**当前状态能用，但缺了增量保护**：

| 场景 | 方案 B（有 ACL） | 当前（方案 A 兜底） |
|---|---|---|
| 现在跑起来 | ✅ | ✅ |
| SteamCMD 更新后 | ✅ 自动继承 | ✅ 更新流程里会重跑一次 chown |
| **以 root 上传 ArkApi 插件 / mod** | ✅ 立即可用 | ❌ **要重启 asa-server 或重跑更新**才会被 chown 到 |

装上就能升级到方案 B，无需改配置、无需重装：

```bash
apt install acl          # Debian/Ubuntu
# dnf install acl        # Fedora
# pacman -S acl          # Arch
systemctl restart asa-server
```

之后 `getfacl .../ShooterGame` 应该能看到 `default:group:asa-umu-runtime:rwx`。

#### 7.5.2.1 补丁：光靠采样发现不了「缺 ACL」

上面这条"重启即可"在最初的实现里**是不成立的**，值得单独记一笔。

`reconcileRuntimeOwnership` 为了避免每次启动都遍历 5 万个文件，先用
`sharedAccessNeeded` 采样。但它只看三样东西：属组、`g+rw`、目录 setgid ——
而**跑过兜底路径的树，这三样全是对的**（`chgrpSetgidTree` 在 ACL 那步之前就跑完了），
只是一条 ACL 都没有。于是采样返回 false，直接跳过，装了 `acl` 也永远补不上。

一句话说：**便宜的检查看不见它要修的东西。**

补了 `defaultACLMissing(root, group)`：`setfacl`/`getfacl` 都在时，
`getfacl -c <root>` 查一眼有没有 `default:group:<组名>:` 条目。两个条件任一成立就跑完整流程：

```go
if !sharedAccessNeeded(dir, gid) && !defaultACLMissing(dir, group) {
    continue
}
```

ACL 工具不存在时它返回 false ——否则降级模式下每次启动都会白走一遍全树。

另外两条路径本来就是**无条件**调 `PrepareSharedTree` 的，不受采样影响：

| 入口 | 是否受采样影响 |
|---|---|
| `asa-server update`（SteamCMD 更新后） | ❌ 无条件执行 |
| `asa-server verify`（拉起验证前） | ❌ 无条件执行 |
| 重启 asa-server | ✅ 走采样 —— 就是这次补的地方 |

所以在补丁之前，让 ACL 生效的办法是跑 `asa-server verify`（比 `update` 便宜得多，
不下载任何东西）；补丁之后，重启就够了。

#### 7.5.2.2 ✅ 方案 B 已确认生效（装 acl + `asa-server update` 后）

```
$ getfacl -p .../server-files/ShooterGame
# owner: asa-umu-runtime
# group: asa-umu-runtime
# flags: -s-                              ← setgid：新条目继承属组
user::rwx
group::rwx
group:asa-umu-runtime:rwx                 ← 访问 ACL（setfacl -R -m）
mask::rwx                                 ← 掩码没有裁掉组权限
other::r-x
default:user::rwx
default:group::rwx
default:group:asa-umu-runtime:rwx         ← 可继承条目（setfacl -d -m）
default:mask::rwx
default:other::r-x
```

四个机制齐全，属主留在 `asa-umu-runtime`（早先手工 chown 的结果）**无害** ——
root 有 `CAP_DAC_OVERRIDE`，不受属主约束。

**为什么最后两行才是关键**：目录一旦有默认 ACL，新建条目就**绕过 umask** ——
内核用「创建模式 ∩ 默认 ACL」算权限，而不是「创建模式 & ~umask」。
root 用默认 umask 022 建出的文件，掩码是 `rw-`（0666 的组位与 `default:mask::rwx` 取交），
具名组条目的有效权限就是 `rw-`，**游戏进程可写**。
这正是 §3.7 里说「setgid 只继承属组、不继承权限位，所以必须叠默认 ACL」的落地证据。

**唯一会破坏它的操作**：日后对这棵树跑 `chmod -R`。
`chmod` 会重算 ACL 掩码，`chmod -R g-w` 之类会把 `mask` 压成 `r-x`，
于是 `group:asa-umu-runtime:rwx` 的**有效**权限被静默削成 `r-x` ——
`getfacl` 会在该行后面标 `#effective:r-x`。真遇到了，重启 asa-server
即可（`sharedAccessNeeded` 采样能看出 `g+rw` 没了）。

`GET /api/system/preflight` 现在也会把这条报出来（`posix-acl`），
装好 acl 后该项消失。

#### 7.5.3 两条无害但可优化的观察

摘自 `verify-launch.log`：

```
08/29 14:11:50 /tmp/dumps: is not owned by us - delete and recreate.
08/29 14:11:50 /tmp/dumps: could not delete, skipping.
08/29 14:11:50 minidumps folder is set to /tmp/dumps01
```

`/tmp/dumps` 是早先以 root 跑时留下的，降权用户删不掉，于是 Steam 的崩溃处理
退到了 `/tmp/dumps01`。功能无碍，`rm -rf /tmp/dumps` 即可清掉。

```
pv-locale-gen: Missing locale en_US.UTF-8 ... Generating locale
pv-adverb[...]: W: Container startup will be faster if missing locales are created at OS level
```

每次启动都要在容器里现生成 locale，纯粹是启动耗时。宿主上
`locale-gen en_US.UTF-8` 一次即可省掉。两条都不值得写进代码。

---

## 8. verify 的成功判据：从「配置目录出现」改成「端口真的监听」

### 8.1 问题

`waitForConfigDir` 的第一件事就是：

```go
if _, err := os.Stat(configDir); err == nil {
    return nil          // 目录已存在 → 立刻返回
}
```

而 `asa-server verify` 走的是 `force=true`，**配置目录来自上一次运行、本来就在**。
于是整个"验证"变成：拉起进程 → `waitForConfigDir` 秒返回 → 2 秒后把进程杀掉 →
宣布验证通过。什么都没验证到。

即使是首次安装（目录确实不存在），这个判据也太松：
配置目录在引擎初始化后几十秒就落盘，**远早于地图加载完成、远早于端口监听**。
§3.6 那次卡死正是这样——目录建出来了，服务端却永远不会开始服务。

### 8.2 改法

`waitForVerificationServer` 取代 `waitForConfigDir`，三条判据：

| 事件 | 处理 |
|---|---|
| **端口被绑定** | **成功**，唯一的成功信号 |
| launcher 进程消失 | **立刻失败**，不再干等到超时（对应参考脚本的 `kill -0 "$init_pid"` 提前退出） |
| 超时（5 分钟） | 失败，且**区分两种文案**：「配置目录从未建出来」vs「配置已生成但端口一直没起」 |

配置目录仍然被跟踪，但降级成两个用途：进度提示，以及让超时文案能说清卡在哪一段 ——
这两种情况指向完全不同的原因（前者是权限/启动链路问题，后者是启动后挂死）。

失败时调 `reportVerificationFailure`，把 `verify-launch.log` 的最后 20 行和
`ShooterGame.log` 的最后 30 行一起 emit 出来，对齐参考脚本
`ark_instance_manager.sh:677-678` 的做法。

### 8.3 端口探测为什么不用「试着 bind」

最省事的写法是自己去 bind 一下那个 UDP 端口，`EADDRINUSE` 就说明服务端起来了。
**但这会和正在启动的服务端抢端口**：轮询恰好落在 ARK 调 `bind()` 之前的那一刻，
它就会因为端口被我们占住而启动失败——一个探测手段把被探测的对象弄挂了。

所以走 `procx.PortInUse`（gopsutil 读 `/proc/net/*` 与 Windows 的 UDP/TCP 表），
纯只读。它和既有的 `PIDByPort` 的区别是**不要求能归属到 PID**：
gopsutil 靠扫 `/proc/*/fd` 匹配 socket inode 来定位进程，
对 pressure-vessel 容器里的进程经常归属失败，而我们只需要知道端口起没起。

### 8.4 超时从 180s 提到 5 分钟

原来的 180s 只覆盖第一个里程碑（配置目录，Wine 冷启动约 30-60s，
`docs/LINUX_COMPATIBILITY_PLAN.md` §5.5）。加载地图并开始监听还要再 30-90s，
首次运行还要更久。5 分钟留足慢路径的余量，同时不至于让真挂死的服务端无限期占住进程。

### 8.5 Windows 影响（已确认：两个平台保持同一套逻辑）

**这是一处有意的跨平台行为变更**：Windows 上 verify 现在也要等到端口监听才算通过，
以前只要配置目录在就算过。也就是说，一台 Windows 机器如果 5 分钟内起不来服务端，
`verify` 从"通过"变成"失败"。

**已确认按此保留，不加平台分支。** 一个不验证任何事情的验证步骤在哪个平台上都是
bug；两边判据一致也意味着以后只有一套语义要维护。
