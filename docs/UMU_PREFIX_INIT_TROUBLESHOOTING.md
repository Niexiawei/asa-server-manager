# umu Wine 前缀初始化失败排查记录（2026-08-29）

> 状态：**根因已确认，D0 / D1 / D5 已修复**（见 §9）。用户已用绕过方式验证
> "可以正常启动了"，修复后的代码待在真机上再跑一次全新 setup 复验。
> 相关文档：`docs/UMU_RUNTIME_USER_PLAN.md`、`docs/LINUX_COMPATIBILITY_PLAN.md`、
> `docs/SETUP_FLOW_OPTIMIZATION_PLAN.md`、`docs/UMU_PYTHON_DISCOVERY_PLAN.md`

---

## 0. 结论（一句话）

**降权到 `asa-umu-runtime` 时继承了 root 的 `DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/0/bus`，
pressure-vessel 试图把这个 root 私有的 D-Bus socket bind 进容器，bwrap 以
`Permission denied` 当场退出，Wine 从未启动 —— 而 `warmPrefix()` 把这个失败吞掉，
连续 8 次都宣布"umu runtime and Wine prefix ready"。**

立即可用的绕过（旧二进制仍需要）：

```bash
env -u DBUS_SESSION_BUS_ADDRESS -u SESSION_MANAGER -u XAUTHORITY -u DISPLAY \
    asa-server setup
```

**用户已用此方式验证：可以正常启动。根因坐实。** 代码修复见 §9。

---

## 1. 现象

`asa-server setup` 全程无报错，直到最后一步「生成首次配置」失败：

```
First installation detected. Running server to generate configuration files...
Running server verification on port 39797 (this can take up to 3 minutes on first run)...
2026/08/29 00:09:12 生成首次配置失败: failed to start server for verification:
  Wine 前缀尚未初始化：/opt/asa-server/basedir/umu-prefix。请运行 asa-server setup 完成环境准备
```

用户视角的荒谬点：**报错让你去跑 setup，而这条报错正是 setup 自己打出来的**。
用户先后跑了 5 次 setup（日志里 `warmPrefix` 一共被调用 8 次），每次都是同样的结局。

`scripts/umu_debug.sh` 手工重跑同一条命令（`<python> <umu-run> wineboot --init`，
同样降权到 `asa-umu-runtime`）**成功**。

环境：**WSL2 / Ubuntu 24.04**（`STEAM_RUNTIME_LIBRARY_PATH` 里的 `/usr/lib/wsl/lib` 是标记），
asa-server 以 root 身份从交互式 shell 启动。

---

## 2. 根因链

```
root 的交互式 shell 里有 DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/0/bus
        │
        │  warmPrefix() / run() 用 os.Environ() 作为子进程环境基底
        ↓
runtimeEnv() 只剥掉 HOME / USER / LOGNAME / XDG_*        ← runtimeuser_linux.go:507-529
        │  DBUS_SESSION_BUS_ADDRESS 不以 XDG_ 开头，逃过一劫
        ↓
子进程降权到 uid=999 (asa-umu-runtime)，但仍指着 root 的 session bus
        ↓
pressure-vessel 解析该变量，要把 /run/user/0/bus bind 进容器
        │  /run/user/0 是 0700 root:root，uid 999 连穿都穿不过去
        ↓
bwrap: Can't find source path /run/user/0/bus: Permission denied   ← 进程在此终止
        │  从 "Running 'GE-Proton10-34'" 到死亡只有 0.9 秒，Wine 完全没跑
        ↓
warmPrefix() 里 `_ = cmd.Run()` 丢弃退出码，事后不校验 system.reg，        ← umu_linux.go:281-286
无条件 logf("umu runtime and Wine prefix ready") + writePrefixMarker()
        ↓
EnsureRuntime() 返回 nil → setup 一路绿灯 → 8 分钟后
VerifyServerInstallation() 里 checkRuntime() 才发现没有 system.reg
        ↓
报错措辞："请运行 asa-server setup"（用户正在运行 setup）
```

**为什么调试脚本能成功**：它用 `runuser -u ... -- env -i ...` 构造了一个**干净环境**，
`DBUS_SESSION_BUS_ADDRESS` 压根不存在，pressure-vessel 于是跳过 session bus，一切正常。
脚本相对于失败路径的差异不是它 `mv` 走了 prefix（那条已被 §5 D6 证伪），
**而是它清空了环境**。

---

## 3. 证据

### 3.1 决定性日志行

`{BaseDir}/logs/asaServer.log`，四次 wineboot 尝试**逐字相同**：

| 时间 | 行 | 内容 |
|------|----|------|
| 00:01:44.905 | 52 | `bwrap: Can't find source path /run/user/0/bus: Permission denied` |
| 00:01:44.948 | 53 | `umu runtime and Wine prefix ready` ← **43 毫秒后宣布成功** |
| 00:01:51.100 | 65 | `bwrap: Can't find source path /run/user/0/bus: Permission denied` |
| 00:01:51.119 | 66 | `umu runtime and Wine prefix ready` |
| 00:04:48.976 | 77 | `bwrap: Can't find source path /run/user/0/bus: Permission denied` |
| 00:04:48.998 | 78 | `umu runtime and Wine prefix ready` |
| 00:04:56.347 | 91 | `bwrap: Can't find source path /run/user/0/bus: Permission denied` |
| 00:04:56.366 | 92 | `umu runtime and Wine prefix ready` |

启动到失败的耗时：`00:04:55.504`（`Running 'GE-Proton10-34' using runtime 'sniper'`）
→ `00:04:56.347`（bwrap 报错），**843 毫秒**。对比成功一次约需 30 秒。

### 3.2 失败现场（`umu-prefix.broken.*` 的内容）

```
drwxr-xr-x  drive_c          Aug 29 00:01     ← umu/Proton 建的空壳
drwxr-xr-x  gstreamer-1.0    Aug 29 00:01
-rw-r--r--  pfx.lock     0B  Aug 29 00:01
drwxr-xr-x  shadercache      Aug 29 00:01
lrwxrwxrwx  pfx -> .         Aug 29 00:04
-rw-r--r--  tracked_files 0B Aug 29 00:04
-rw-r--r--  .created-by-proton 14B Aug 29 00:04   ← 我们自己写的假标记（D5）
```

**没有 `system.reg` / `user.reg` / `version` / `config_info`** —— 与"Wine 一次都没启动过"
完全吻合。而 `.created-by-proton` 明晃晃地躺在一个从未建成的 prefix 里，是 D5 的实证。

### 3.3 D1 最极端的一次表现

23:42:11（日志第 14-16 行），umu-run **抛出 Python 异常并以
`RuntimeError: umu has not been setup for the user` 终止**（steamrt3 下载失败），
完整 traceback 都进了日志——紧接着的下一行是：

```json
{"ts":"2026-08-28T23:42:11.147+0800","caller":"runner/umu_linux.go:285",
 "msg":"umu runtime and Wine prefix ready"}
```

一个硬异常都能被宣布成 ready，这不是"退出码宽容"的问题，是**根本没看结果**。

### 3.4 完整时间线

| 时刻 | 事件 |
|------|------|
| 23:28:00 | 下载 umu-launcher 1.4.4 |
| 23:28:06 | 开始下 GE-Proton10-34；**23:28 / 23:30 / 23:32 三次重试**（网络差） |
| 23:38:03 | GE-Proton 解压完成 |
| 23:38:04 | **warmPrefix #1** → steamrt3 下载失败（连接中断 ×2）→ 23:42:11 Python 异常退出 → **宣布 ready** |
| 23:42–23:44 | SteamCMD 下载安装 |
| 23:44:39 | **warmPrefix #2**（`installer.go:256` 的第二次 EnsureRuntime）→ 断点续传中被打断 |
| 23:47:01 | **warmPrefix #3**（用户重跑 setup） |
| 23:50:54 | **warmPrefix #4** |
| 23:58:11 | **warmPrefix #5** → 断点续传 |
| 00:01:28 | steamrt3 终于下完（SHA256 OK，mtree OK），累计 **23 分钟 / 5 次尝试** |
| 00:01:44 | Proton 首次真正启动 → **bwrap 权限失败** → 宣布 ready |
| 00:01:51 | **warmPrefix #6** → 同样 bwrap 失败 → 宣布 ready |
| 00:04:48 | **warmPrefix #7** → 同样 → ready |
| 00:04:56 | **warmPrefix #8** → 同样 → ready |
| 00:04:56–00:09:12 | SteamCMD 安装 ARK 本体 |
| **00:09:12** | `VerifyServerInstallation` → `checkRuntime()` → **报错** |
| 00:17 | 手工 `umu_debug.sh`（`env -i`）→ **成功** |
| 00:46 | E3 实验：对着脏 prefix 用 `env -i` 跑 → **成功**，D6 证伪 |

---

## 4. 已确认的代码缺陷

### D0 — `runtimeEnv()` 的剥离清单漏了 D-Bus（**根因**）

`internal/runner/runtimeuser_linux.go:507-529`：

```go
// runtimeEnv rewrites HOME/USER/LOGNAME to the dropped user and strips
// root-inherited XDG_* so the child's runtime cache lands under the right home.
func runtimeEnv(base []string, home, userName string) []string {
	...
		switch {
		case k == "HOME", k == "USER", k == "LOGNAME":
			continue
		case strings.HasPrefix(k, "XDG_"):     // ← 想到了这一类，但没想全
			continue
		}
	...
}
```

作者显然意识到了"root 的会话环境不能带进降权子进程"这个问题（所以剥了 `XDG_*`），
但**指向 root 私有 socket 的变量不止 `XDG_RUNTIME_DIR` 一个**。至少还有：

| 变量 | 典型值 | 后果 |
|------|--------|------|
| **`DBUS_SESSION_BUS_ADDRESS`** | `unix:path=/run/user/0/bus` | **本次故障的直接原因**：bwrap 拒绝启动 |
| `SESSION_MANAGER` | `local/host:@/tmp/.ICE-unix/NNN` | 同类 socket 泄漏 |
| `XAUTHORITY` | `/root/.Xauthority` | 0600 root，读不到 |
| `DISPLAY` / `WAYLAND_DISPLAY` | `:0` / `wayland-0` | 指向 root 的显示会话 |
| `PULSE_SERVER` / `PULSE_COOKIE` | `/run/user/0/pulse/...` | 同上 |
| `SSH_AUTH_SOCK` | `/tmp/ssh-XXX/agent.N` | 不该带给游戏进程 |
| `JOURNAL_STREAM` / `INVOCATION_ID` / `LISTEN_FDS` | systemd 注入 | 混淆子进程的 systemd 上下文 |

**黑名单在这里是错误的形状**：一个无头游戏服务器需要的环境变量是有限且已知的，
应该改成**白名单**（`PATH`、`TERM`、`LANG`/`LC_*`、代理相关的 `*_PROXY`，加上我们自己设的
`WINEPREFIX`/`GAMEID`/`PROTONPATH`/`HOME`/`USER`/`LOGNAME`），其余一律不传。

**影响面不止 setup**：`runner.run()`（`runner_linux.go:35`）用的是同一个 `runtimeEnv`，
所以**每一次实例启动都会踩同一个坑**——只要 asa-server 是从带 D-Bus 会话的 root shell
（`sudo -i` / 直接以 root 登录）启动的。systemd system service 不注入
`DBUS_SESSION_BUS_ADDRESS`，所以走服务方式反而不会触发——这解释了为什么这个 bug
能活到现在：**它只在"手工 setup"这条路径上必现**。

### D1 — `warmPrefix()` 把失败当成功（故障放大器，危害仅次于根因）

`internal/runner/umu_linux.go:227-287`：

```go
	// Best-effort like the reference script (`|| true`): a non-zero exit
	// from wineboot doesn't necessarily mean the prefix wasn't created.
	_ = cmd.Run()                    // ← 退出码丢弃，也不打印

	waitForWineserverDrain(prefix)

	logf("umu runtime and Wine prefix ready")          // ← 无条件宣布成功
	return writePrefixMarker(prefix, cfg.ProtonVersion) // ← 无条件写版本标记
```

注释里"非零退出不代表 prefix 没建好"这个前提本身没错（照抄
`ark_instance_manager.sh` 的 `|| true`），**但它缺了另一半**：函数开头已经有现成的判据

```go
	prefixReady := fileExists(filepath.Join(prefix, "system.reg")) &&
		dirExists(filepath.Join(prefix, "drive_c", "windows", "system32"))
```

却**没有在 wineboot 之后再跑一遍**。代价：

* 8 次失败无一被察觉，用户反复重跑 setup 是在做无用功；
* `bwrap: ... Permission denied` 这条**一眼就能定位的错误**，被埋在几百行输出里，
  且**后面紧跟着一句 "ready"**——反向误导；
* 故障点（00:01:44）与报错点（00:09:12）相距 8 分钟、跨 3 个包，
  报错文案还把用户指回它自己刚做过的事。

顺带：连 exit code 都不记，`UMU_LOG` / `PROTON_LOG` 也没开，
出事时日志的信息量远低于手工调试脚本。

### D2 — 一次 setup 里 `EnsureRuntime` 被调两次

`internal/actions/setup.go:93` 一次，`internal/installer/installer.go:256`
（`DownloadAndUpdateArkServer` 开头）又一次。日志里 8 次 `warmPrefix` = 用户跑的
4~5 次 setup × 2。在 setup 语境下第二次是纯冗余。

副作用（好的一面）：它顺带证伪了 D4，见 §5。

### D3 — `runtimeMu` 只锁进程内，跨进程无保护

`umu_linux.go:50-54` 是进程内 `sync.Mutex`，而 `webapi/actions.go:406-410` 在 API 服务
启动时无条件 `go runner.EnsureRuntime(...)`。CLI setup 与 systemd 服务并存时可以并发
预热同一个 prefix；umu 自己的 `umu.lock` 只保护 runtime 安装目录，不保护 WINEPREFIX。
**本次故障未触发**（用户全程没起过服务），但缺口真实存在。

### D4 — `a+rX` 补权限跑在下载解压之前

`runtimeuser_linux.go:215-224` 的 `ensureWorldReadExec(proton/umu)` 在
`ensureRuntime()` 里的执行位置早于 `ensureUmu()` / `ensureGEProton()`，
全新安装时两个 `pathExists` 都是 false，补权限完全落空。
**本次已排除**（见 §5），但在"全新安装 + 只跑一轮 EnsureRuntime"的路径上仍会咬人。

### D5 — 给从未建成的 prefix 写版本标记

`writePrefixMarker()` 在 D1 的路径上无条件执行。实证见 §3.2：
`umu-prefix.broken.*` 里躺着一个 14 字节的 `.created-by-proton`，
而同目录下连 `system.reg` 都没有。这让该标记失去了"prefix 可用"的语义。

### D6 — ~~脏 prefix 永远不会被重建~~（**已证伪**）

v2 曾把 `reconcilePrefixVersion()` 的
`if !fileExists(system.reg) { return nil }` 早退列为头号嫌疑，推断"半成品 prefix
无法原地续建"。**E3 实验证伪**：把 `umu-prefix.broken.*` 原样复制一份、用 `env -i`
跑 wineboot，**成功**生成了 `system.reg`（00:46）。

Proton 能正确处理"已存在但没有 `version`"的目录（日志：`Upgrading prefix from None to
GE-Proton10-34`）。这条早退在语义上仍然把"没建过"和"建坏了"混为一谈，属于可以顺手
改好的健壮性问题，但**不是本次故障的原因**。

> 保留这条记录是为了留住那次推断被推翻的过程：当时"多次失败 / 手工一次成功"的唯一
> 已知差异被误判成了 `mv` prefix，实际差异是 `env -i`。**两个变量同时变化的对照实验
> 不能定因**——这是这次排查里最该记住的教训。

---

## 5. 已排除的假设及依据

| 假设 | 排除依据 |
|------|----------|
| Python 版本 / 选错解释器 | 日志与手工脚本都是 `python3.12`（3.12.3）；umu 自报版本一致 |
| 32 位 glibc / libzstd / tar / AppArmor userns | preflight 通过；手工运行整条 bwrap 链路跑通 |
| 运行时用户没建好 / HOME 不可写 | `~/.local/share/umu/` 下文件属主全是 `asa-umu-runtime`，且由失败那次运行创建 |
| prefix 目录不可写 | 同上；`chownPathForRuntime()` 生效 |
| GE-Proton / umu-run 权限不足（D4） | D2 导致第二轮 `EnsureRuntime` 在解压**之后**补了 `a+rX`，wineboot 照样失败；用户又跑了多次 setup，这条路走通过很多遍 |
| Proton 版本不匹配触发误删 | `reconcilePrefixVersion` 无 `system.reg` 时早退，不干扰 |
| GE-Proton 下载损坏 | 走 `.sha512sum` 强校验；同一份文件后来跑通 |
| 并发（D3） | 全程没起过 asa-server 服务；日志里 8 次 `warmPrefix` 时间上完全不重叠 |
| 脏 prefix 不能续建（D6） | E3 实验直接证伪，见上 |

---

## 6. 次要发现：steamrt3 的下载不走任何代理

日志 23:38–00:01 这 23 分钟里，steamrt3（`SteamLinuxRuntime_sniper.tar.xz`）从
`repo.steampowered.com` 下载失败了 4 次（`Connection broken` / `ReadTimeoutError` /
`Temporary failure in name resolution`），靠断点续传熬到第 5 次才成功。

问题在于：**这个下载是 umu-launcher 自己发起的**（`umu_runtime.py` 里的 urllib3），
不经过 `pkg/download`，因此 `config.yaml` 里的
`download.github_proxy` 与 `download.http_proxy` **对它完全无效**。
国内网络下这是个必然的痛点，而且失败时的表现就是 §3.3 那种"抛异常但被宣布 ready"。

可行方向：把配置里的 `download.http_proxy` 转成 `HTTPS_PROXY`/`HTTP_PROXY` 注入
umu-run 的子进程环境（urllib3 认这两个变量）。**注意这与 D0 的白名单方案要一起设计**：
白名单里必须放行 `*_PROXY`。

---

## 7. 修复方向

1–3 已实施，见 §9；4 已被 1 的白名单方案取代（结构上不再可能发生）；
5–10 仍待办。

1. ~~**`runtimeEnv()` 改白名单**（对应 D0，**根因修复**）~~ ✅ 已实施：
   只放行 `PATH`、`TERM`、`LANG`/`LC_*`、`*_PROXY`/`NO_PROXY`，
   加上我们显式设置的 `HOME`/`USER`/`LOGNAME`/`WINEPREFIX`/`GAMEID`/`PROTONPATH`
   （以及将来可能需要的 `UMU_*`/`PROTON_*` 调试开关）。
   黑名单永远漏得掉——这次漏的就是 `DBUS_SESSION_BUS_ADDRESS`。
   同时覆盖 `warmPrefix()` 与 `runner.run()` 两条路径（它们共用这个函数）。
2. ~~**`warmPrefix` 加后置校验 + 失败即报错**（对应 D1、D5）~~ ✅ 已实施。
3. ~~**失败要在 setup 当场炸**~~ ✅ 由 2 自动达成：`warmPrefix` 返回错误 →
   `EnsureRuntime` 返回错误 → `setup.go:93-95` 本来就会中止。
4. ~~preflight 增加"环境里有指向 `/run/user/<非目标uid>/` 的变量"检查~~ —
   **不再需要**：白名单让这类变量根本传不进子进程，检查一个已不可能发生的状态属于
   多余的活件。
5. **跨进程互斥**（对应 D3）：`warmPrefix` 前对 `{BaseDir}` 下的锁文件上 `flock`。
6. **调用去重**（对应 D2）：setup 路径上 `DownloadAndUpdateArkServer` 里那次
   `EnsureRuntime` 是冗余的。
7. **补权限顺序**（对应 D4）：`ensureWorldReadExec(proton/umu)` 挪到
   `ensureUmu()` / `ensureGEProton()` 之后。
8. **代理注入**（对应 §6）：把 `download.http_proxy` 透传给 umu-run。
9. **`reconcilePrefixVersion` 区分"没建过"与"建坏了"**（对应 D6，健壮性）。
10. `scripts/umu_debug.sh` 增加 `--inherit-env` / `--keep-prefix` 开关，
    让它能复现**失败**形态。这次它"太干净"（`env -i` + `mv` prefix 两个变量同时变），
    既掩盖了真因，又制造了一个错误的嫌疑人（D6）。

---

## 8. 修复后的验证清单

1. 在**带 D-Bus 会话的 root shell** 里（`echo $DBUS_SESSION_BUS_ADDRESS` 非空）
   跑 `asa-server setup`，全新 BaseDir → 应当成功建出 `system.reg`。
2. 人为制造失败（例如临时 `chmod 000` 掉 `proton`），确认 setup **当场报错**、
   错误文本里带 wineboot 的退出码与最后几行输出，且 **不写** `.created-by-proton`。
3. 同一条件下 `asa-server api` + 从前端启动实例，确认 `runner.run()` 路径同样正常
   （D0 的修复必须覆盖它）。
4. `GET /api/system/preflight` 在故意保留 `DBUS_SESSION_BUS_ADDRESS` 时报出新增的检查项。
5. Windows 回归：`runtimeuser_windows.go` 是空实现，确认 `go build`/`go vet` 与
   实例启动不受影响。

---

## 9. 已实施的修复（2026-08-29）

### `internal/runner/runner_linux.go` — 新增 `inheritedEnv()` / `launchEnvAllowed()`

启动子进程的环境基底从 `os.Environ()` 换成 **白名单过滤后的** `inheritedEnv()`，
`umuCommandLine()`（实例启动）与 `warmPrefix()`（首次预热）两条路径都改了。

放行：`PATH` `TERM` `TZ` `HOME` `USER` `LOGNAME` `LANG` `LC_*`
`*_PROXY`/`*_proxy` `UMU_*` `PROTON_*` `WINE*`。其余一律丢弃——
`DBUS_SESSION_BUS_ADDRESS`、`XDG_*`、`SESSION_MANAGER`、`XAUTHORITY`、
`DISPLAY`、`WAYLAND_DISPLAY`、`PULSE_*`、`SSH_AUTH_SOCK`、`JOURNAL_STREAM` 全部不再传递。

两点设计说明：

* `HOME`/`USER`/`LOGNAME` 必须在白名单里。降权时 `runtimeEnv()` 会覆盖它们，
  但**不降权**时（`euid != 0` 或 `umu_run_as_root: true`）它们得原样活下来，
  否则 umu 找不到自己的 runtime 缓存目录。
* `*_PROXY` 放行顺带解决了 §6 的一半：操作者 `export HTTPS_PROXY=...` 后，
  umu 下载 steamrt3 就能走代理了（把 `config.yaml` 的 `download.http_proxy`
  自动转过去仍待办）。
* `runtimeEnv()` 的 `XDG_*` 剥离**保留不动**：它现在是第二道防线，
  也仍然覆盖调用方通过 `Options.Env` 显式传入的环境。

### `internal/runner/umu_linux.go` — `warmPrefix()` 后置校验

* 抽出 `prefixInitialized(prefix)`，**前置检查与后置检查共用同一个判据**，杜绝漂移；
* `_ = cmd.Run()` 改为保留 `runErr`；wineboot 之后重新判定，
  **未生成 `system.reg` 就返回错误**，错误文本里带上 wineboot 的退出状态
  和**最后 8 行输出**（`progressWriter` 现在维护一个环形 tail），
  操作者不必再去 `asaServer.log` 里翻；
* `writePrefixMarker()` 移到成功分支之后 —— 不再给建不成的 prefix 盖章（D5）。

"非零退出码可以容忍"这个原始意图**保留**了：容忍的是退出码，不再容忍**结果**。

### `internal/runner/runner_linux_test.go` — 回归测试

`TestInheritedEnv_DropsSessionScopedVariables`：断言 9 个会话相关变量被丢弃、
9 个必要变量被保留。在 WSL 的真 Linux 下与既有用例一并通过。

验证：`GOOS=linux`/`GOOS=windows` 双平台 `go build` + `go vet` 通过；
`internal/runner` 全部测试在 Linux 下通过。

---

## 10. 版本记录

* **v1（2026-08-29 初版）**：从源码定位 D1–D5，根因待定，首要取证为读日志。
* **v2**：用户补充"未重启 asa-server / 跑过多次 setup"后，新增 D6 并列为头号嫌疑；
  D4 由推断排除升级为源码事实排除；首要取证改为 E3 实验。
* **v3（结案）**：E3 证伪 D6；`asaServer.log` 第 52/65/77/91 行的
  `bwrap: Can't find source path /run/user/0/bus: Permission denied`
  确认根因为 D0（`runtimeEnv` 未剥离 `DBUS_SESSION_BUS_ADDRESS`）。
  补充 §6 代理发现与 §7/§8 的修复与验证方案。
