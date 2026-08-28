# umu 运行时降权：以专用非 root 用户 `asa-umu-runtime` 执行游戏实例

> 状态：**首轮实现已落地**（`GOOS=linux`/`GOOS=windows` 双平台 `go build`/`go vet` 通过，
> 新增跨平台可跑单测）。**真实 Linux 主机上的端到端验证仍待补**——降权后 umu/bwrd/wine 能否
> 正常拉起、PTS 属主坑（§9 风险 1）、SELinux/NFS 交互等只做到编译与逻辑走查。
> §10 验收判据即真机待办清单。
>
> 关联文档：
> - `LINUX_COMPATIBILITY_PLAN.md` §4.1（目录布局）、§5.1（`runner` 抽象）、§5.4（停止/`kill(-pgid)`）、
>   §5.5（ASA-on-Wine 三项 fixups）、**§5.8（"不自动创建/切换专用用户"的既有结论——本方案要在更窄的范围内推翻它）**、
>   §6 风险 2 / 风险 6
> - `SETUP_FLOW_OPTIMIZATION_PLAN.md`（`asa-server setup` 引导流程）
> - `LINUX_DEPLOYMENT.md`（部署指南，落地后需要补一节）

---

## 1. 背景与问题

### 1.1 `asa-server` 进程为什么以 root 运行

Linux 上 `asa-server` 常见的两种跑法都会落到 root：

| 跑法 | 为什么是 root |
|---|---|
| `systemd` 服务（`asa-server service install`） | `svcmgr/service_linux.go` 里 `cfg.UserName` 留空，等价于 `User=root`（对齐 Windows 的 LocalSystem 默认）。§5.8 已明确**不自动切换**服务运行身份，只在 `install` 时打印警告。 |
| 交互式 `sudo asa-server api` | 用户为了让 `cert install` 写系统信任库、让 `service` 子命令操作 systemd，习惯性整个 `sudo`。 |

`asa-server` 自身**确实需要** root 的部分：写 `/usr/local/share/ca-certificates/` + `update-ca-certificates`（§5.7）、
`systemctl` 安装/启停服务、必要时收紧 `{BaseDir}` 下敏感文件属主。这些不在本方案的改动范围内。

### 1.2 umu / Proton / pressure-vessel 为什么**必须**非 root

`runner_linux.go` 通过 `umu-run` 拉起 Windows 版 `ArkAscendedServer.exe`，调用链是：

```
asa-server(root) → umu-run(python zipapp) → pressure-vessel / bwrap（建非特权 user namespace）
                                          → GE-Proton(wine) → ArkAscendedServer.exe
```

- **pressure-vessel 在 root 下走的是另一条代码路径**。非 root 时它用 unprivileged user namespace + `bwrap` 建容器；
  root 下它检测到自己有特权，改用不同机制，社区几乎没人在这个组合上测过。「照抄
  `scripts/ark_instance_manager.sh`」的前提是那份脚本的运行环境——普通用户——被复现出来。
- **`kernel.apparmor_restrict_unprivileged_userns`（§6 风险 2）** 这个 Ubuntu 23.10+ 默认开启的开关，
  限制的正是「非特权进程创建 user namespace」。以 root 跑时这条限制的行为与非 root 跑时不一致，
  自检和修复提示（`sysctl -w ...=0`）都是按「非特权进程」的语义写的。降权到真正的非 root 子进程，
  才让这套自检的假设成立。
- **安全**：ARK 服务端历史上出过 RCE。游戏进程暴露在公网，一旦被打穿，进程身份 = root =
  整机沦陷。降权后攻击者拿到的是 `asa-umu-runtime`，能破坏的只有存档和 prefix（都可重建），
  碰不到 `config.yaml` / `auth.db` / CA 私钥 / 其它系统用户。

### 1.3 现状：只有一句警告

`svcmgr/service_linux.go` 的 `warnBeforeInstall()` 在 `service install` 时检测 `os.Geteuid() == 0`，
打印「建议 `useradd -r -m asa` + `systemctl edit` 加 `User=asa`」然后什么都不做。§5.8 记录的不做的理由是：

> 自动切换**服务**运行身份意味着要迁移 `{BaseDir}` 及其下所有实例目录的属主/权限——对一次已经在跑的
> root 安装做这个，风险显著高于「告诉用户怎么做」。

### 1.4 本方案的定位：比 §5.8 的目标**窄**

§5.8 拒绝的是「让整个 `asa-server` 进程改成非 root」。本方案不动 `asa-server` 进程身份，
**只在启动单个游戏实例时，把那一个子进程（及其整棵 umu/wine 进程树）降权到专用用户 `asa-umu-runtime`**。

因此 §5.8 的核心顾虑大部分不成立：

| §5.8 的顾虑 | 本方案 |
|---|---|
| 要 chown 整个 `{BaseDir}` | 只 chown **运行时产物子树**（prefix、runtime HOME、实例镜像/存档目录、clusters），且这些大多可重建 |
| 动一个正在跑的 root 安装风险高 | `asa-server` 本身仍是 root，行为不变；降权只影响新启动的实例子进程 |
| systemd unit 要改 `User=` | unit 不动，仍 `User=root` |

代价是新引入一块「文件属主协调」逻辑（§5），以及 PTY（ArkApi）路径的一个已知坑（§9）。

---

## 2. 目标与非目标

### 目标

1. Linux 上，`umu-run` 及其派生的 `bwrap` / `wineserver` / `ArkAscendedServer.exe` 全部以
   `asa-umu-runtime`（非 root、无登录 shell 的系统用户）运行，`ps -o user=` 永远看不到 root。
2. 该用户由程序**按需自动创建**（`asa-server setup` / `service install` / 首次启动实例时），幂等，
   创建失败有明确报错而不是静默降级成 root 跑。
3. 降权对应的文件属主协调也自动完成：runtime 用户对它需要读写的子树有权限，对只读子树有读+执行权限。
4. **Windows 侧零影响**：所有新增代码在 `//go:build linux` 下；Windows 的 `runner` 实现一行不改。
5. **非 root 交互式跑 `asa-server api` 时不做任何降权尝试**：`euid != 0` 时子进程就以当前用户运行
   （本来就是非 root，已经是目标状态）。
6. **`asa-server` 每次启动（`api` / systemd 服务）都自检**：确认 `asa-umu-runtime` 用户仍然存在，
   且它对 §5.1 列出的相关目录**确实有**读/写/执行权限（不是「我们建过一次就假设它对」——要实际核对
   属主与模式位）。
   **不满足则阻断 `asa-server` 启动**（非零退出，同时把原因写日志 + `GET /api/system/preflight`），
   **唯一例外**是显式配置 `linux.umu_run_as_root: true` —— 那种情况下游戏进程有意以 root 运行，
   自检整体跳过。
   这一条**有意偏离** `LINUX_COMPATIBILITY_PLAN.md` §4.2「缺依赖不拦 API」的既有取向，理由见下。

### 非目标

| 项 | 原因 |
|---|---|
| 让 `asa-server` 进程本身以非 root 运行 | §5.8 已定案，本方案不碰。 |
| 每个实例一个独立用户（`asa-umu-<instance>`） | 隔离更强但用户管理 + chown 复杂度翻倍，且共享 prefix（§6 风险 6）不再可共享。列入"以后再说"，见 §11。 |
| 用 `systemd-run --uid=... --scope` 起实例 | 把启动绑死在 systemd 上，破坏交互式 / 非 systemd 路径，且 scope 嵌在服务 cgroup 下层级别扭。见 §11。 |
| 自己再套一层 `bwrap` / podman 做容器 | pressure-vessel 已经在做容器化，双重嵌套脆弱，超出范围。 |
| Windows 上的等价能力（降权子进程） | Windows 用 LocalSystem，游戏进程降权是另一套 API（`CreateProcessAsUser` + 受限令牌），不在本方案范围。 |

### 为什么这一条要「硬阻断」，与 §4.2 的软化取向相反

`LINUX_COMPATIBILITY_PLAN.md` §4.2 把宿主依赖自检从「拒绝启动」软化成「告警不阻断」，
理由是「一个 Wine 依赖缺失不该让整个 API 服务起不来」。这里反过来选硬阻断，因为**失败的后果不同**：

- 依赖缺失（§4.2）：结果是「某个实例起不来」，API / 前端 / 其它实例都正常，用户看日志能自己修，
  期间损失可控 → 软化合理。
- 降权环境不满足（本条）：如果放行，结果是**游戏进程以 root 跑**——一个暴露在公网、历史上有过 RCE 的
  进程拿到 root。这不是「少个功能」，是**安全等级悄悄降了一级且没人察觉**。默认必须挡住。

`umu_run_as_root: true` 就是那个「我知道我在做什么」的显式开关：配了它，等于用户书面同意以 root 运行，
自检不再有意义、整体跳过。没配它而环境又不满足，就是配置/环境错误，`asa-server` 拒绝带病启动。

> systemd 场景注意：自检失败时 `asa-server` 以退出码 **`78`（`EX_CONFIG`）** 退出，unit 里
> `RestartPreventExitStatus=78`，所以 systemd **不重试**、服务直接进 `failed`，journal 只留 1 条
> 带修复建议的错误。`Restart=on-failure` 仍对真崩溃（其它退出码）生效。修好后 `systemctl start`。
> 该行通过 kardianos 的 `Option["SystemdScript"]` 自定义模板注入，**不写 drop-in 文件**——详见 §9.3b。

---

## 3. 总体设计

### 3.1 一句话

`asa-server` 继续 root。`runner_linux.go` 在 `exec.Cmd` 上设置
`SysProcAttr.Credential = &syscall.Credential{Uid, Gid, Groups}`，把 `umu-run` 子进程降到
`asa-umu-runtime`；配套把 umu 运行时产物子树 chown 给这个用户，并把子进程的 `HOME` 指向它的家目录。

### 3.2 降权点：只有 `runner_linux.go` 的子进程创建处

需要设置 `Credential` 的地方**穷举**如下，全部在 `internal/runner` 内：

| 位置 | 进程 | 是否降权 |
|---|---|---|
| `runner_linux.go` `run()` —— 非 PTY | `umu-run ArkAscendedServer.exe ...`（普通实例） | **是** |
| `runner_linux.go` `runPTY()` —— PTY | `umu-run AsaApiLoader.exe ...`（ArkApi 实例） | **是**（注意 PTS 属主坑，见 §9） |
| `umu_linux.go` `warmPrefix()` —— `umu-run wineboot --init` | prefix 预热 | **是**（否则 prefix / SLR 缓存以 root 建出来，之后 runtime 用户写不了） |
| `installer/steamcmd_linux.go` —— `steamcmd.sh +quit` | SteamCMD | **否**：SteamCMD 是原生 ELF、不经 umu，装在 `{BaseDir}/steamcmd`，由 `asa-server`(root) 跑；产物只读共享即可 |
| `installer` 里 `VerifyServerInstallation` 经 `runner.Run()` 起的验证进程 | 首次配置生成 | **是**（它就是走 `runner.Run`，自动继承降权） |

其余所有对进程的操作——写 PID 文件、`kill(-pgid)`、读 `/proc/<pid>/cmdline`、gopsutil 枚举 socket——
都由 root 的 `asa-server` 完成，root 操作任意 uid 的进程/文件不受影响，**不需要改**（详见 §6）。

### 3.3 降权机制：`syscall.Credential`，不是 `sudo -u`

```go
// internal/runner/runtimeuser_linux.go（新增）
//
// resolveRuntimeCredential 返回把子进程降到 asa-umu-runtime 所需的 Credential，
// 以及该用户的 HOME。仅在 os.Geteuid()==0 时返回非 nil；否则返回 (nil, "", nil)，
// 调用方原样以当前用户启动。
func resolveRuntimeCredential(cfg Config) (*syscall.Credential, string, error) {
    if os.Geteuid() != 0 || cfg.RunAsRoot {
        return nil, "", nil
    }
    u, err := lookupOrCreateRuntimeUser(cfg) // §4
    if err != nil {
        return nil, "", err
    }
    uid, _ := strconv.Atoi(u.Uid)
    gid, _ := strconv.Atoi(u.Gid)
    return &syscall.Credential{
        Uid:    uint32(uid),
        Gid:    uint32(gid),
        Groups: []uint32{uint32(gid)}, // 显式设置，避免 setgroups([]) 语义歧义
    }, u.HomeDir, nil
}
```

调用点（`run()` / `runPTY()` / `warmPrefix()`）：

```go
cred, home, err := resolveRuntimeCredential(cfg)
if err != nil {
    return nil, fmt.Errorf("准备 umu 运行时用户失败: %w", err)
}
cmd.SysProcAttr = &syscall.SysProcAttr{
    Setsid:     true,   // 已有
    Credential: cred,   // 新增；nil 时 exec 包忽略，行为与现在完全一致
}
```

**为什么不用 `sudo -u asa-umu-runtime umu-run ...`**：

- 多一个 `sudo` 依赖 + 一条 sudoers 规则要维护；
- 丢掉对 `*exec.Cmd` 的直接控制（PTY 绑定、`Setsid`、`Env` 拼装、`cmd.Wait` 语义都要绕）；
- 拿进程组 id（`kill(-pgid)` 需要）要多一跳。

`syscall.Credential` 让内核在 `execve` 前依次 `setgroups` → `setgid` → `setuid`，
real / effective / saved 三个 uid 一起落到目标值，**无残留特权**，比 `sudo` 干净。

### 3.4 何时降权：仅当 `euid == 0`

- `sudo asa-server api` / systemd `User=root` → `euid==0` → 降权到 `asa-umu-runtime`。
- 普通用户 `./asa-server api` → `euid!=0` → **不降权**，子进程就以该用户跑。这本来就是期望的终态，
  也没有 `setuid` 的权限，强行做只会 `EPERM`。
- 逃生舱 `linux.umu_run_as_root: true` → 即使 root 也不降权，恢复当前行为（给排障用）。

### 3.5 `HOME` 迁移

umu 需要 `$HOME/.local/share/umu`（Steam Linux Runtime 缓存），lsteamclient 需要
`$HOME/.steam/sdk{32,64}/steamclient.so`（§5.5 fixup 3）。降权后 `$HOME` 必须指向
`asa-umu-runtime` 的家目录，否则运行时缓存会写进 root 的 `/root`（子进程无权），或干脆崩在 steamclient。

改动三处：

1. **`runner_linux.go` `umuCommandLine`** 拼 env 时，若 `resolveRuntimeCredential` 返回了 `home`，
   追加 / 覆盖 `HOME=<home>`、`USER=asa-umu-runtime`、`LOGNAME=asa-umu-runtime`，
   并剔除继承自 root 的 `XDG_*`、`HOME=/root`。
2. **`umu_linux.go` 的就绪检查**——`steamLinuxRuntimeReady()` 现在用 `os.UserHomeDir()`
   （root 跑时是 `/root`），必须改成「runtime 用户的 HOME」。`warmPrefix()` 里 `wineboot --init`
   的 env 同理。新增 `runtimeHomeDir(cfg) string` 统一解析：`euid==0` 时查 `asa-umu-runtime` 的
   home，否则 `os.UserHomeDir()`。
3. **`installer/fixups_linux.go` `symlinkSteamSDK()`**——见 §5.4。

> `svcmgr/service_linux.go` 的 `EnvVars["HOME"]` 是给 `asa-server` **进程自己**设的，
> 它仍是 root、HOME 仍是 `/root`，**这一处不用改**。要区分「asa-server 进程的 HOME」和
> 「被降权的游戏子进程的 HOME」——本方案只改后者。

---

## 4. 专用用户 `asa-umu-runtime`

### 4.1 用户与组

| 属性 | 值 | 说明 |
|---|---|---|
| 用户名 | `asa-umu-runtime`（`linux.umu_runtime_user` 可改） | |
| 类型 | 系统用户 `useradd -r` | 不占普通 uid 段 |
| 登录 shell | `/usr/sbin/nologin`（或 `/bin/false`） | 不允许交互登录 |
| 密码 | 无（`!`） | |
| 主组 | 同名 `asa-umu-runtime` | |
| 家目录 | `{BaseDir}/runtime-home`（`useradd -d`） | **放 BaseDir 内**，与 umu/prefix 等产物同盘、同一套备份/迁移边界；不用 `/var/lib/...` 免得散落 |
| 附加组 | 无（单实例共享用户，暂不需要） | |

创建命令（程序 shell 出去执行，`asa-server` 是 root，有权限）：

```sh
groupadd -r asa-umu-runtime
useradd  -r -g asa-umu-runtime -s /usr/sbin/nologin \
         -d {BaseDir}/runtime-home -m asa-umu-runtime
```

`linux.umu_runtime_uid` / `linux.umu_runtime_gid` 非零时，追加 `-u` / `-g <gid>` 固定数值——
见 §9「uid 漂移」。

### 4.2 创建时机与幂等

统一入口 `runner.EnsureRuntimeUser(ctx) error`（Windows 空实现返回 nil）：

1. `user.Lookup("asa-umu-runtime")` 成功 → 已存在，跳到属主协调（§5.2）。
2. 不存在 → 跑上面的 `groupadd` / `useradd`；`groupadd` 报「已存在」当成功。
3. 无论新建还是已存在，都跑一次 §5.2 的属主协调（幂等）。

调用点：

| 调用方 | 时机 | 语义 |
|---|---|---|
| **`asa-server` 每次启动**（`main` 启动序列里，**同步**、在 `api` 开始监听 / systemd 汇报 ready **之前**；仅 Linux + `euid==0` + 未设 `umu_run_as_root`） | 每次 `api` / systemd 服务起来 | **建用户（若缺）+ 属主协调（§5.2）+ 访问自检（§4.4）**。整个动作幂等且廉价，正常系统上是几次 `stat` 就返回。**任一步失败 → 打印带修复建议的错误、非零退出，`asa-server` 不启动。** 想跳过只能配 `umu_run_as_root: true` |
| `runner.EnsureRuntime()` | 下载完 umu/GE-Proton、预热 prefix **之前** | 建用户（预热要降权跑）；与上一行调的是同一个 `EnsureRuntimeUser`，只是入口不同 |
| `actions/setup.go` `ActionSetup`（Linux 分支） | `setup` 引导 | `EnsureRuntime` 已包含，无需额外加；`setup` 阶段失败本来就中止 |
| `svcmgr` `service install`（Linux） | 装服务时 | `warnBeforeInstall()` 从「只警告」升级为「调 `EnsureRuntimeUser`，失败则回退到警告 + 手动步骤」（`install` 不是运行时，不阻断——阻断在服务真正 `start` 时发生） |
| `instance.startServerInternal` 里 `runner.CheckRuntime()` 门禁附近 | 每次启动实例 | 第二道网：真启动前再跑一次 §4.4 的访问自检（deep-probe 开），不通过就阻断**这一个实例**，报错进 SSE |

> **为什么每次启动都做，而不是只在 install / 首启时做一次**：runtime 用户和它的目录权限是
> 「带外可变状态」——管理员可能手工 `userdel` 过、改过 `{BaseDir}` 的属主、把数据目录整体
> `rsync` 到新机器（uid 对不上，见 §9 风险 2）、或换了 `linux.umu_runtime_uid`。这些都不会经过
> 本程序。所以每次启动重新核对一遍「用户在不在、目录它读不读得了」，是把静默失败（实例起不来、
> 存档写不进、且日志里只有一句 Wine 的天书）提前变成一条明确的 preflight 告警。

### 4.3 创建 / 自检失败时的行为

`useradd` 在极简容器 / 只读 `/etc` / 非常规发行版上可能没有或失败；或者用户在、但目录属主/权限
带外被破坏且 reconcile 修不回来（SELinux、NFS root_squash、只读挂载）。两种都算「降权环境不满足」：

- `EnsureRuntimeUser` / `verifyRuntimeAccess` 返回带修复建议的错误，三条出路写进错误文本：
  1. 手动 `useradd -r asa-umu-runtime`（或修 `chown`）后重启；
  2. `config.yaml` 设 `linux.umu_runtime_uid` 指向一个已存在的非 root 账号；
  3. `config.yaml` 设 `linux.umu_run_as_root: true` —— 明确接受以 root 运行游戏，自检整体跳过。
- **`asa-server` 启动路径上：以退出码 `78`（`EX_CONFIG`）退出，服务不起。** systemd 侧靠
  自定义 unit 模板里的 `RestartPreventExitStatus=78` **不重试**、直接进 `failed`（§9.3b）。
  这与 `LINUX_COMPATIBILITY_PLAN.md` §4.2 的宿主依赖自检（告警不阻断）**不同**，
  理由见 §2「为什么这一条要硬阻断」。
- `GET /api/system/preflight` 仍然加一项 `umu_runtime_user`——但由于服务在这种情况下根本起不来，
  这个字段主要服务于「配了 `umu_run_as_root: true` 因而放行、但仍想让前端提示一句『当前以 root 运行游戏』」
  的场景，以及实例级门禁失败（服务在跑、单个实例起不来）的展示。

### 4.4 启动自检：`verifyRuntimeAccess(cfg) []Problem`

只读、幂等、**同步**跑（不进后台协程——它的结论决定 `asa-server` 要不要继续启动，
必须在 `api` 开始监听之前有答案）。仅 Linux 且 `euid==0` 且未设 `umu_run_as_root` 时执行；
`runtime` 为 `umu` 或 `custom` 都要跑——降权在两种模式下都发生。它很便宜（几次 `stat` +
少量抽样 `Lstat`），同步跑不构成启动延迟问题；真正耗时的 `EnsureRuntime`（下载 GE-Proton 等）
仍然是后台异步，两者分开。返回 `[]runner.Problem`，**非空 = `asa-server` 拒绝启动**（见 §4.3）。逐项：

| 检查 | 方法 | 不通过时的 `Problem` |
|---|---|---|
| **用户存在** | `user.Lookup(cfg.RuntimeUser)`；`cfg.RuntimeUID!=0` 时再核对解析出的 uid 与配置一致 | `umu-runtime-user-missing` / `...-uid-mismatch`，Fix = `asa-server` 重启会自动重建，或手动 `useradd`；uid 冲突则提示改配置或迁移属主 |
| **家目录可用** | `Stat(home)` 存在、是目录、`Sys().Uid == 运行时 uid`、mode `0700`/`0755` | `umu-runtime-home-bad`，Fix 提示 `chown -R <user> <home>` |
| **读写子树属主正确** | 对每个存在的 RW 子树（`umu-prefix*`、`runtime-home`、`clusters`、已存在的 `server-files-tmp-*`）：`Lstat` 子树根 + **抽样**其下最多 N 个较深的条目，核对 `Uid == 运行时 uid`。不做全量 `WalkDir`（prefix 有几十万文件，启动路径上跑不起） | `umu-runtime-owner-drift`，列出前几个属主不对的路径，Fix = 重启自动 `chown`，或手动 |
| **只读子树可读+执行** | 对 `proton/<ver>`、`umu-launcher`：根目录与关键入口（`proton`、`umu-run`）`o+rx`（或 `g+rx` 且运行时用户在该组） | `umu-runtime-ro-perm`，Fix = `chmod -R a+rX <dir>` |
| **`{BaseDir}` 可穿过** | `Stat(BaseDir).Mode()` 有 `o+x` | `basedir-not-traversable`，Fix = `chmod o+x <BaseDir>` |
| **（可选、更强）实际探测** | fork 一个极短命的降权子进程（带 `Credential`），在 `umu-prefix` 下 `faccessat(W_OK)` + `touch`/`unlink` 一个探针文件 | `umu-runtime-probe-failed`，说明「属主看着对但实际写不了」——多半是 SELinux / ACL / 挂载选项。默认关，`linux.umu_runtime_deep_probe: true` 打开 |

**为什么属主检查用「抽样」而不是「实测每个文件」**：启动路径要快；`chown` 的正确性由
§5.2 的 `reconcileRuntimeOwnership`（同一次启动动作里先跑）保证，`verifyRuntimeAccess` 只是
在它之后做一次便宜的抽查，抓「reconcile 没覆盖到 / 带外被改回去」的漏网情况。真正逐文件的
保证是 reconcile 的 `WalkDir`，不是这里。

**与 `instance` 门禁的关系**：`startServerInternal` 里也调 `verifyRuntimeAccess`，
但那里 `deep_probe` 默认**开**（就要起一个实例了，多花几十毫秒做真实探测值得），
且不通过就阻断这一个实例的启动，错误直接进 SSE 返回给发起方。

---

## 5. 文件属主与权限（本方案最重的一块）

降权后，子进程以 `asa-umu-runtime` 身份读写文件。下面把「它要碰哪些路径、要什么权限、怎么给」讲清楚。

### 5.1 路径清单与目标属主

| 路径 | 子进程需要 | 目标属主 / 模式 | 给法 |
|---|---|---|---|
| `{BaseDir}` 本身 | 遍历（`x`） | `root:root` `0755`（`o+x` 即可穿过） | `EnsureDirectories` 已是 0755，确认不回退 |
| `{BaseDir}/umu-launcher/`（含 `umu-run`） | 读 + 执行 | `root:root`，文件 `a+rx` | 解压时 `chmod 0755`，已满足；不 chown |
| `{BaseDir}/proton/GE-Proton10-34/` | 读 + 执行 | `root:root`，`a+rX` | 确认 `pkg/archive.ExtractTar` 保留 tar 内模式位；不满足则 reconcile 时 `chmod -R a+rX` |
| `{BaseDir}/steamcmd/linux{32,64}/steamclient.so` | 读（软链目标） | `root:root` `a+r` | SteamCMD 自带即 world-readable |
| `{BaseDir}/umu-prefix/`、`umu-prefix-<key>/` | **读写**（wine 重度写入） | **`asa-umu-runtime`** `0700` | reconcile 时 `chown -R` |
| `{BaseDir}/runtime-home/`（`.local/share/umu`、`.steam`、`.cache`、`.wine`…） | **读写** | **`asa-umu-runtime`** `0700` | `useradd -m` 建出来就是；reconcile 兜底 `chown -R` |
| `{BaseDir}/server-files/` | 读 + 执行（运行时视为只读） | `root:root` `a+rX` | 不 chown；fixups（`steam_appid.txt` / 重命名 sentry）由 root 提前写好 |
| `{BaseDir}/server-files-tmp-<instance>/`（镜像目录，即 `exeWorkDir` 的父级） | **读写**（游戏在此下 `ShooterGame/Saved/<AltSaveDirectoryName>/` 写存档、日志、崩溃 dump） | **`asa-umu-runtime`** `0755`；对目录加 `setgid` 位让新文件继承组 | reconcile 时 `chown -R` + `chmod g+s` 到镜像目录 |
| `{BaseDir}/clusters/<ClusterID>/`（`-ClusterDirOverride`，跨服传角色数据） | **读写** | **`asa-umu-runtime`** `0755` | reconcile 时 `chown -R`（目录不存在则先建） |
| `{BaseDir}/instances/<instance>/server.log` 等游戏日志落点 | 写 | 视日志路径而定：若落在镜像目录内 → 已随镜像 chown；若单独在 `instances/<name>/` → 该文件/目录也要 chown | 见 §5.2 备注 |
| `/tmp`（umu 临时文件、`/tmp/dumps` 崩溃 dump） | 读写 | 系统 `1777` | 无需处理 |

> **敏感子树反向收紧（建议一并做）**：`{BaseDir}/certs/`（CA 私钥）、`{BaseDir}/database_file/`
> （`auth.db`、BadgerDB）、`{BaseDir}/config.yaml` 收成 `root:root` `0600`/`0700`，
> 明确挡在 `asa-umu-runtime` 之外。这不是本方案必需，但既然要动属主，一起做防御更彻底。

### 5.2 reconcile 步骤：`reconcileRuntimeOwnership(cfg)`

在 `EnsureRuntimeUser` 内、用户就绪后执行，纯幂等：

```
for 每个「读写」子树 in {umu-prefix*, runtime-home, clusters, 所有 server-files-tmp-*}:
    若存在: chown -R asa-umu-runtime:asa-umu-runtime <子树>
镜像目录再补: chmod g+s <server-files-tmp-*>          // 新文件继承组
for 每个「只读」子树 in {proton/<ver>}:
    若解压后不是 a+rX: chmod -R a+rX <子树>
```

用 Go 实现（`filepath.WalkDir` + `os.Lchown` / `os.Chmod`），不 shell 出去，避免 `chown` 命令
在软链上的语义分歧（prefix 里有大量软链，必须 `Lchown` 不跟随）。

> **`server-files-tmp-<instance>` 的时机**：镜像目录是 `mirror.SyncInstanceMirror()` 在
> **每次启动时**重建/校验的。所以对镜像目录的 chown 不能只在 `EnsureRuntimeUser` 做一次，
> 要在 `startServerInternal` 里「镜像同步完成之后、`runner.Run()` 之前」补一次
> `chownTree(mirrorDir, uid, gid)`。镜像里绝大多数条目是软链（指向 root 拥有的 `server-files`），
> `Lchown` 软链本身即可，软链的**目标**仍是 root 只读——正确。真实副本文件（§CLAUDE.md 提到的
> 根目录 11 个文件等）需要 chown 到 runtime 用户，`WalkDir` 会覆盖到。

### 5.3 `EnsureRuntime` 下载物的属主

`ensureUmu` / `ensureGEProton` 由 root 的 `asa-server` 下载、解压 → 产物 `root:root`。
`umu-launcher` 和 `proton` 只需 runtime 用户能**读+执行**，world 权限够了，**不 chown**
（保持 root 拥有反而更安全——游戏进程改不了自己的运行时）。只有 `warmPrefix` 产生的
`umu-prefix/` 和 `runtime-home/.local/share/umu/` 需要归 runtime 用户——而 `warmPrefix`
本身已经降权跑（§3.2），产物属主天然正确，reconcile 只是兜底。

### 5.4 fixups 的 Steam SDK 软链（`installer/fixups_linux.go`）

`symlinkSteamSDK()` 当前用 `os.UserHomeDir()`（root 跑时 = `/root/.steam/sdk{32,64}`）——
降权后游戏进程按**自己的** `$HOME`（`{BaseDir}/runtime-home`）去 `dlopen`，找不到就崩。

改动：

1. 目标目录从 `os.UserHomeDir()` 换成 `runtimeHomeDir()`（§3.5 的统一解析函数，
   为此 `installer` 需要拿到 runtime 用户信息——通过 `runner` 暴露一个 `runner.RuntimeHomeDir()` 或
   把值经 `installer` 的调用参数传入，避免 `installer → runner` 反向依赖变复杂，倾向后者）。
2. 建完软链后 `os.Lchown` 软链、`os.Chown` 新建的 `.steam` / `sdk32` / `sdk64` 目录到 runtime 用户
   （因为这次是 root 在建，不 chown 的话 runtime 用户进不去 `0755` 里 root 拥有的目录……其实
   `0755` 能进，但 `.steam` 若 `useradd -m` 没建、这里 `MkdirAll` 出来是 root:root，
   统一 chown 最省心）。

---

## 6. 停止 / 信号 / 进程可见性——几乎不用改

| 操作 | 谁来做 | 降权后是否受影响 |
|---|---|---|
| 写 `pid` / `launcher_pid` / `asa_api_pid` 文件到 `instances/<name>/` | `asa-server`(root) | 否（root 写自己的目录） |
| `procx.TerminateTree` / `KillTree` = `kill(-pgid, SIG…)` | `asa-server`(root) | 否——**root 可向任意 uid 的进程组发信号**。`Setsid` 保证 pgid 独立，`kill` 前的 `pgid>1 && pgid!=os.Getpid()` 断言照旧 |
| `waitForGamePID` 扫 `/proc/*/cmdline` 匹配 `AltSaveDirectoryName=` | `asa-server`(root) | 否——root 读任意 `/proc/<pid>/cmdline`。（反倒比非 root 的 asa-server 更可靠：非 root 时别的用户的 cmdline 可能读不全） |
| `process.isExpectedProcessPlatform`（Linux 按 cmdline） | `asa-server`(root) | 否 |
| gopsutil `net.Connections("all")`（端口→PID） | `asa-server`(root) | 否——root 看得到所有 socket 的 inode↔pid 映射 |
| `handle.Wait()` 回收子进程 | `asa-server`(root) | 否——`umu-run` 仍是 `asa-server` 直接 `fork` 的直接子进程，`wait4` 正常；降权只改子进程 uid，不改父子关系 |

**结论**：`instance` / `process` / `procx` / `countdown` 里与停止、存活判定相关的代码**零改动**。
降权是「子进程 uid 变了」，不是「asa-server 权限变小了」——asa-server 还是 root。

---

## 7. 配置项

`{BaseDir}/config.yaml` 的 `linux:` 段（Windows 下整段忽略，见 §7 of `LINUX_COMPATIBILITY_PLAN.md`）新增：

```yaml
linux:
  # ... 现有字段 ...

  # 以专用非 root 用户运行游戏实例的 umu/wine 进程树（仅当 asa-server 以 root 运行时生效）
  umu_runtime_user: "asa-umu-runtime"   # 用户名；不存在则自动 useradd -r
  umu_runtime_uid: 0                     # 非 0 时固定 uid（BaseDir 跨系统迁移/重装时保持属主稳定）
  umu_runtime_gid: 0                     # 非 0 时固定 gid
  umu_run_as_root: false                # true = 有意以 root 运行游戏进程：不降权、跳过全部自检。
                                        #   这是「降权环境不满足时 asa-server 拒绝启动」的唯一绕过开关（§2 / §4.3）。
                                        #   默认 false —— 宁可服务起不来，也不默默把公网游戏进程跑成 root。
  umu_runtime_deep_probe: false         # 启动自检（§4.4）时是否 fork 降权子进程做真实写探测；
                                        #   实例启动门禁处恒为开，此项只管 asa-server 启动那一次
```

沿用 `appconfig` 现有的 **flag > 环境变量 `ASA_*` > 文件 > 默认值** 优先级，
环境变量形如 `ASA_LINUX_UMU_RUNTIME_USER`。`applyAppConfig()` 里把这些值推给
`runner.Configure()`（`Config` 结构新增 `RuntimeUser` / `RuntimeUID` / `RuntimeGID` / `RunAsRoot` /
`RuntimeDeepProbe` 字段）——注意**服务模式下 `app.Run()` 不执行**，`applyAppConfig` 是唯一推送点，
别漏（`CLAUDE.md` 记过的坑）。且因为启动自检是**硬阻断**，这些配置必须在自检之前就推给 `runner`：
`main` 里的顺序是 `Load 配置 → applyAppConfig（含 runner.Configure）→ EnsureRuntimeUser + VerifyRuntimeAccess → 起 api`。

---

## 8. 实施清单（按文件）

| 文件 | 改动 |
|---|---|
| `internal/runner/runner.go` | `Config` 加 `RuntimeUser string` / `RuntimeUID,RuntimeGID int` / `RunAsRoot,RuntimeDeepProbe bool`；导出 `EnsureRuntimeUser(ctx) error`、`VerifyRuntimeAccess() []Problem`、`RuntimeHomeDir() string`（Windows 全部空实现 / 返回 nil） |
| `internal/runner/runtimeuser_linux.go`（新） | `resolveRuntimeCredential` / `lookupOrCreateRuntimeUser` / `reconcileRuntimeOwnership` / `verifyRuntimeAccess`（§4.4，只读抽样自检，返回 `[]Problem`） / `runtimeHomeDir` / `chownTree`（`Lchown`，不跟随软链） |
| `internal/runner/runtimeuser_windows.go`（新） | 全部空实现：`EnsureRuntimeUser` 返回 nil，`VerifyRuntimeAccess` 返回空，`resolveRuntimeCredential` 返回 `(nil,"",nil)` |
| `internal/runner/runner_linux.go` | `run()` / `runPTY()` 在 `SysProcAttr` 里加 `Credential`；`umuCommandLine` 拼 env 时注入 `HOME/USER/LOGNAME`、剔除 root 的 `HOME=/root`、`XDG_*` |
| `internal/runner/umu_linux.go` | `steamLinuxRuntimeReady` / `warmPrefix` / 其它读 `os.UserHomeDir()` 处 → `runtimeHomeDir(cfg)`；`warmPrefix` 的 `exec.Command` 加 `Credential`；`ensureRuntime` 开头调 `EnsureRuntimeUser` |
| `internal/runner/preflight_linux.go` | `Preflight()` 里合并 `verifyRuntimeAccess()` 的结果，作为「宿主依赖自检」的一部分一起返回（供 `setup` 与 preflight API 复用） |
| `main.go` / `main_linux.go` 的启动序列（`api` 与 systemd `RunService` 共用的那段） | `applyAppConfig` 之后、起 `api` 之前，**同步**调 `runner.EnsureRuntimeUser(ctx)` + `runner.VerifyRuntimeAccess()`；任一返回错误/非空 → 打印带修复建议的错误并 **`os.Exit(78)`**（`const exitRuntimeUserUnsatisfied = 78`，`EX_CONFIG`；systemd 侧靠 `RestartPreventExitStatus=78` 不重试，见 §9.3b）。仅当 `runner` 判定「Linux && euid==0 && !RunAsRoot」时才真正执行，否则两个调用都是 no-op。**放 `main` 不放 `webapi`**：`asa-server` 的 Linux 无参默认动作也是 `api`，且 systemd 服务模式 `app.Run()` 不执行——`main` 是唯一两条路都过的点 |
| `internal/webapi/actions.go` | 现有 `EnsureRuntime` 后台异步任务保持不变（下载 GE-Proton 等，耗时，仍异步）；用户创建 + reconcile + 自检已在 `main` 同步做完，这里不重复 |
| `internal/instance/server.go` | `startServerInternal` 的 `runner.CheckRuntime()` 门禁之后，加一道 `runner.VerifyRuntimeAccess()`（deep-probe 开）；非空则 `startErr` 返回、阻断这一个实例（第二道网，防「服务起来后属主被带外改坏」） |
| `internal/installer/fixups_linux.go` | `symlinkSteamSDK` 目标 HOME → runtime 用户家目录；新建目录 + 软链 `Chown`/`Lchown` 到 runtime 用户。runtime HOME 通过函数参数从 `installer` 上层传入（不加深 `installer→runner` 依赖） |
| `internal/instance/server.go` | `startServerInternal` 里镜像同步完成后、`runner.Run()` 之前，调 `runner.ChownMirrorForRuntime(mirrorDir)`（内部 `euid!=0` 时 no-op）；`-ClusterDirOverride` 目标目录若不存在先建再 chown |
| `internal/svcmgr/service_linux.go` | ① `warnBeforeInstall()` → 调 `runner.EnsureRuntimeUser(ctx)`：成功打印「游戏实例将以 asa-umu-runtime 运行」；失败降级为原来的警告文本 + 手动步骤。unit 仍 `User=root` 不变。② `configurePlatform` 里 `cfg.Option["SystemdScript"] = umuRuntimeSystemdScript`——一份 fork 自 kardianos v1.3.0 内置 `systemdScript` 的包级常量，仅加一行 `RestartPreventExitStatus=78`（见 §9.3b 的 diff）。**不写 `/etc/systemd/system/*.d/` drop-in** |
| `internal/svcmgr/systemd_script_linux.go`（新） | `umuRuntimeSystemdScript` 常量 + 顶部注释「fork 自 kardianos vX.Y.Z，只改带 `# asa-server:` 标记的行；升级 kardianos 时 re-diff」 |
| `internal/webapi/systemapi/systemapi.go` | preflight 响应加 `umu_runtime_user` 状态项 |
| `internal/appconfig/*` | `Linux` 配置结构体 + 默认值 + `config.yaml` 模板注释 + 环境变量绑定 |
| `main.go` `applyAppConfig` | 把新增的 `umu_runtime_*` / `umu_run_as_root` 字段推给 `runner.Configure()` |
| `docs/LINUX_DEPLOYMENT.md` | 新增「游戏实例以专用用户运行」一节：默认行为、`id asa-umu-runtime`、如何排障、如何用 `umu_runtime_uid` 固定、卸载时 `userdel` 的注意事项 |
| `docs/LINUX_COMPATIBILITY_PLAN.md` | §5.8 补一段：指向本文，说明「服务进程仍 root，但游戏子进程已自动降权」这个更窄的方案已单列设计；并注明「§5.8 当初『不自定义 kardianos 模板』的取舍，在本方案里为了一行 `RestartPreventExitStatus=78` 被有意推翻，drift 风险已按 §9.3c 接受」 |
| `docs/README.md` | 文档索引「Linux 兼容」表加一行 |

**新增测试**（跨平台可跑的部分）：

- `resolveRuntimeCredential` 在 `euid!=0` / `RunAsRoot=true` 时返回 nil（Windows/普通用户 CI 上即可验证）。
- `chownTree` 对软链只改软链本身、不动目标（造一个软链指向只读文件，chownTree 后目标属主不变）——
  需 root，放 `//go:build linux` + `testing.Short()` 跳过，或 CI 里以 root 容器跑。
- env 拼装：注入 `HOME` 后 root 的 `HOME=/root` 不再出现在最终 `Env` 里。
- `umuRuntimeSystemdScript`：用 `text/template` 按 kardianos 同样的 FuncMap（`cmd`/`cmdEscape`）
  对一个假的 `service.Config` 渲染一遍不报错，输出是合法 unit（`[Unit]`/`[Service]`/`[Install]`
  三段齐全），且含且仅含一行 `RestartPreventExitStatus=78`。kardianos 的内置 `systemdScript`
  不可导入，无法做程序化 diff——改为在测试文件里 `//go:embed` 一份「已知对应 kardianos vX.Y.Z」
  的模板副本作基线，升级 kardianos 时人工把这份副本和 `go.mod` 版本一起更新（这一步本身就是
  §9.3c 的 re-diff）。

---

## 9. 关键风险与已知坑

| # | 风险 | 影响 | 应对 |
|---|---|---|---|
| 1 | **PTY（ArkApi）路径的 PTS 属主** | `runPTY()` 里 `/dev/pts/N` 由 root（asa-server）打开，属主是 root。降权后的 `AsaApiLoader.exe` 子进程 uid 不同，写 pts 可能 `EACCES`，或拿不到控制终端 | 落地时在 `pp.Start()` 前 `os.Chown` pts slave 到 runtime uid（`login`/`su` 就是这么做的）。若 go-pty 不暴露 slave fd/path，退而：ArkApi-on-Linux 本就是「实验性、用户自负」（§6 风险 11），文档标注「ArkApi + 降权」组合未验证，用户可临时 `umu_run_as_root: true` |
| 2 | **uid 漂移** | `useradd -r` 分配的动态系统 uid，在「BaseDir 保留、OS 重装」后可能对应到别的账号甚至不存在 → 存档目录属主悬空 | `linux.umu_runtime_uid/gid` 固定数值；部署文档强调迁移前记下 `id asa-umu-runtime` |
| 3 | **首次切换的属主迁移**（已有 root 安装升级到本版本） | 老实例的 `server-files-tmp-*`、`clusters/`、prefix 都是 root 拥有，第一次 `reconcileRuntimeOwnership` 要 `chown -R` 一大片；**中途失败会让升级后的 `asa-server` 直接起不来**（硬阻断），比「告警继续」更容易吓到人 | reconcile 幂等可重跑，大目录 chown 记进度日志；失败错误里明说「这是首次降权迁移，重跑本命令 / 修 `chown` 后重启即可，数据没动」，并提示 `umu_run_as_root: true` 可临时先把服务顶起来再慢慢迁。prefix 迁移失败可直接删了重建（§6 风险 5，prefix 无用户数据） |
| 3b | **systemd 重启循环**（自检失败 + `Restart=on-failure`） | 硬退出触发 systemd 重启，不设约束就是无限循环刷 journal | **不重试**：自检失败用专属退出码 **`78`（`EX_CONFIG`，`sysexits.h`）**，systemd unit 里 `RestartPreventExitStatus=78` → 这类退出直接进 `failed`、systemd 不拉起，journal 只 1 条错误。其它退出码（真崩溃）仍照 `Restart=on-failure` 自愈。「瞬时原因（NFS 挂载滞后等）」不给宽限——那种情况 `systemctl start` 重来一次即可，代价远小于「循环几次里刚好有一次侥幸起来、之后一直带病跑」。**实现见下方「§9.3b 的 systemd 模板改法」**——通过 kardianos 的 `Option["SystemdScript"]` 传一份自定义 unit 模板，**不自己写 `/etc/systemd/system/*.d/` drop-in 文件** |
| 4 | **`pkg/archive.ExtractTar` 是否保留 GE-Proton 的模式位** | 若解压后 `proton` 入口或 `files/bin/*` 不是 `a+rx`，降权进程执行失败 | reconcile 里对 `proton/<ver>` 兜底 `chmod -R a+rX`；同时给 `ExtractTar` 补个「保留 tar header 模式」的单测 |
| 5 | **SELinux enforcing（RHEL/Fedora）** | chown + 跨 uid 执行 + Proton 访问，可能被 SELinux label 挡 | 文档列为已知限制，建议 `setenforce 0` 验证或写自定义 policy；本方案不自带 SELinux policy |
| 6 | **root_squash 的网络文件系统上的 BaseDir** | root 身份 `chown` 在 NFS root_squash 下被降级，失败 | `LINUX_COMPATIBILITY_PLAN.md` 已建议 BaseDir 不放网络盘；此处再报一条明确错误 |
| 7 | **`asa-server` 将来若真的改成非 root**（§5.8 万一翻案） | 那时 runtime 产物属主是 `asa-umu-runtime`，非 root 的 asa-server（另一个 uid）可能读不到日志/存档做备份 | 届时让 asa-server 进程加入 `asa-umu-runtime` 组，或本方案改用「共享组 + setgid」而非纯 chown。当前不预先复杂化 |
| 8 | **极简容器无 `useradd`** | 建用户失败 → `asa-server` 拒绝启动 | §4.3：报错给三条出路（手动建 / 指定既有 uid / 显式 `umu_run_as_root: true`）。容器场景本就常以非 root 跑整个进程，那时 `euid!=0`、自检 no-op，不受影响 |
| 9 | **启动自检是抽样，可能漏报属主漂移** | `verifyRuntimeAccess` 为了不拖慢启动只抽查 prefix 等大目录里的少量深层条目，带外把某个中间子目录 `chown root` 可能抽不到 | 逐文件的正确性由**同一次启动动作里先跑的** `reconcileRuntimeOwnership` 全量 `WalkDir` 保证；自检只是它之后的便宜复查。实例门禁处的 deep-probe（真实 `touch`/`unlink`）是第二道网。真要根治得靠 reconcile，不是靠自检 |
| 3c | **自定义 systemd 模板随 kardianos 升级漂移** | 用 `Option["SystemdScript"]` 传的是一份**基于 kardianos v1.3.0 内置模板**的副本，上游改了内置模板我们不会自动跟上（`LINUX_COMPATIBILITY_PLAN.md` §5.8 当初正是为了躲这个才没自定义模板） | 模板常量集中放一处、注释标明「fork 自 kardianos vX.Y.Z 的 `systemdScript`，仅加/改了带 `# asa-server:` 标记的行」；`go.mod` 升 kardianos 时把这个常量列入必查项。改动面小（见 §9.3b 的 diff，仅一行），re-diff 成本低。收益（`RestartPreventExitStatus` 这个 key kardianos 的 `Option` 表根本没有，不 fork 模板就表达不了）值这个代价 |

### 9.3b 的 systemd 模板改法

kardianos/service 的 systemd 后端支持 `Option["SystemdScript"]` 覆盖**整份** unit 模板
（`s.Option.string("SystemdScript", systemdScript)`）。做法：把 kardianos v1.3.0 的内置
`systemdScript` **原样抄成一个包级常量**，只加/改带 `# asa-server:` 标记的行，然后在
`configurePlatform` 里 `cfg.Option["SystemdScript"] = umuRuntimeSystemdScript`。

相对内置模板，**只加两行**，插在无条件的 `RestartSec=120` 之后（不放进任何 `{{if}}` 守卫，
保证一定渲染出来）：

```diff
 {{end}}{{if SuccessExitStatus}}SuccessExitStatus={{SuccessExitStatus}}
 {{end}}RestartSec=120
+# asa-server: exit 78 (EX_CONFIG) = drop-privileges runtime user unavailable; retrying cannot fix it
+RestartPreventExitStatus=78
 EnvironmentFile=-/etc/sysconfig/{{Name}}
```

内置模板里 `RestartSec=120`、`StartLimitInterval=5` / `StartLimitBurst=10` 全部保留不动
（`LINUX_COMPATIBILITY_PLAN.md` §5.8 已接受这套默认值），`EnvVars["HOME"]` 经
`{{range EnvVars}}` 照常渲染。单测 `TestUmuRuntimeSystemdScript_IsKardianosPlusExactlyTheForkLines`
把这两行剔掉后与 kardianos v1.3.0 的 `systemdScript` 逐字比对，升级 kardianos 时它会红。

- 退出码 `78` 定义成 `const exitRuntimeUserUnsatisfied = 78`（`main` 包），
  `main` 里自检失败时 `os.Exit(78)`；其它错误路径不用这个码。
- 交互式 `asa-server api`（无 systemd）：一样 `os.Exit(78)`，用户看到错误即可，没有「重试」概念。
- `Option["SystemdScript"]` 只在 Linux 的 `configurePlatform` 设；Windows 分支不碰。

---

## 10. 验收判据

真机（一台干净的 Ubuntu 24.04 / root 装 systemd 服务）上：

1. `asa-server setup` 后 `id asa-umu-runtime` 存在，家目录 `{BaseDir}/runtime-home` 属主正确、`0700`。
2. 启一个普通实例，`ps -o pid,user,args` 里 `umu-run` / `pv-bwrap` / `wineserver` /
   `ArkAscendedServer.exe` 的 USER **全部**是 `asa-umu-r+`，**没有 root**。
3. `find {BaseDir}/umu-prefix ! -user asa-umu-runtime` 为空（prefix 里没有 root 拥有的文件）。
4. `{BaseDir}/runtime-home/.steam/sdk64/steamclient.so` 软链存在、runtime 用户可读，服务器不崩在
   `FSteamServerInstanceHandler`。
5. 玩家可连入、RCON 可用、`saveworld` 写出的存档文件属主是 `asa-umu-runtime`。
6. `asa-server` 停止实例：`kill(-pgid)` 把整棵 umu/wine 树收干净，无 `bwrap`/`wineserver` 孤儿。
7. 双实例并发启动（共享 prefix）互不干扰。
8. `linux.umu_run_as_root: true` 时：`asa-server` 正常启动、跳过全部自检、游戏进程以 root 跑，
   日志有一条「当前以 root 运行游戏（umu_run_as_root=true）」的提示，`preflight` 的
   `umu_runtime_user` 项标注为「bypassed」。
9. 普通用户（非 root）`./asa-server api` 起实例：游戏进程以该用户跑，不做降权尝试，
   自检为 no-op，日志无「降权失败」类报错。
10. `GOOS=windows go build ./...` + `go vet` 两平台无回归；`grep -rn "syscall.Credential" --include=*.go`
    命中只在 `//go:build linux` 文件里。
11. `EnsureRuntimeUser` 连续跑两次，第二次是纯 no-op（无 `useradd`、无多余 chown 日志）。
12. **启动硬阻断生效**：root 跑、`umu_run_as_root` 未设的前提下——
    a. 卸掉 `useradd`（`PATH` 里移走）或 `userdel asa-umu-runtime` 且让重建失败，
       启动 `asa-server`：进程以 **`78`** 退出、不监听端口，stderr / journal 有带三条出路的错误。
       systemd 下 `systemctl show -p Result,ExecMainStatus` 显示 `exit-code` / `78`，
       服务**直接 `failed`、不重启**（`RestartPreventExitStatus=78` 生效），`journalctl` 里
       **只有 1 条**该错误，没有重启循环。
    b. `chown -R root {BaseDir}/umu-prefix` 后启动：同一次启动里 reconcile 先把属主 `chown` 回
       `asa-umu-runtime`，`verifyRuntimeAccess` 随之通过，`asa-server` **正常启动**（自愈）。
    c. 把 `{BaseDir}/umu-prefix` 挂成只读 / `chattr +i` 让 reconcile 修不回来：启动**被阻断**，
       错误指出「属主不对且无法自动修复」。
    d. 上述任一被阻断的情形，加 `linux.umu_run_as_root: true` 后再启动：正常起，转判据 8。
13. **实例门禁生效**（服务已在跑）：把 `{BaseDir}/umu-prefix` `chmod 000` 后启动某实例，
    SSE 返回带修复建议的错误、实例不进入 starting，**其它实例与 API 本身不受影响**。

---

## 11. 备选方案与取舍

| 方案 | 为什么不选（作默认） |
|---|---|
| **`sudo -u asa-umu-runtime umu-run …`** | 多 `sudo` + sudoers 依赖；丢失对 `*exec.Cmd` 的直接控制（PTY / `Setsid` / env / `Wait`）；`syscall.Credential` 更干净、无残留特权。 |
| **`systemd-run --uid=asa-umu-runtime --scope umu-run …`** | 把每次启动绑死在 systemd 上，破坏交互式与非 systemd 路径；transient scope 嵌在 `asa-server.service` 的 cgroup 下层级别扭；进程组/`Wait` 语义要重做。**可作为「以后想要 per-instance cgroup 资源限额」时的演进方向**，那时收益才够抵成本。 |
| **每实例独立用户 `asa-umu-<instance>`** | 隔离更强（实例间也隔离），但用户/属主管理复杂度随实例数线性增长，且共享 prefix（§6 风险 6，磁盘友好）不再成立。留作可选增强：将来加 `linux.umu_runtime_user_per_instance: true`，与 `prefix_mode: per-instance` 配套。 |
| **整个 `asa-server` 改非 root**（§5.8 的原命题） | 系统信任库写入、`systemctl` 操作、敏感文件属主收紧都要 root；且要迁移整个 BaseDir 属主，动到活跃用户数据。§5.8 已定案不做，本方案是它的「窄化替代」。 |
| **共享组 + `setgid` 位，不 chown** | 让 root 的 asa-server 和 runtime 用户通过公共组共享读写，属主不变。更"可逆"，但 prefix 里几十万个文件都要正确的组 + `g+rwX` + 目录 `setgid`，wine 新建文件的 umask 还可能把 `g+w` 抹掉，脆弱面比「直接 chown 运行时产物子树」大。运行时产物本就该归运行时用户，chown 语义更直白。§9 风险 7 那种未来情形真发生时再切到这个模型。 |
