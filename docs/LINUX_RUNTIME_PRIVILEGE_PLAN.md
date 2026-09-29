# Linux 运行时权限与降权运行时用户（合并文档）

> 本文由 `LINUX_RUNTIME_PRIVILEGE_PLAN.md`、`LINUX_RUNTIME_PRIVILEGE_PLAN.md`、`LINUX_RUNTIME_PRIVILEGE_PLAN.md` 于 2026-09-29 物理合并而成。三部分互补、均在使用中：Part 1 是降权与属主的主设计，Part 2 是共享写权限加固，Part 3 是 Python 解释器探测。
> ⚠️ 本文各「落地文件 / 实施清单」写于 `RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN` 重构之前，实际路径见文末「附录 Y」。
> ⚠️ 本文存在已核实的已知缺陷（含一处 P0 死锁），见下方「已知缺陷清单」。

## 已知缺陷清单（2026-09-29 代码审计同步）

以下为 2026-09-29 只读审计结论（基线 faf127c），尚未修复；其中 **P0-1 会让升级后的 asa-server 永久拒绝启动**，须优先处理。

### 2.2 发现

#### [P0] 启动自检检查镜像目录、reconcile 却不修 → 升级后 asa-server 永久拒绝启动

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

- **位置**：`internal/appconfig/validate.go:206-214`（仅去空白、判负，无 root 校验）；`pkg/sysuser/sysuser_linux.go:29-31`（`Managed()` 只看 `euid==0 && !RunAsRoot`）、`:58-72`、`:77-99`（`ResolveCredential` 直接取 `user.Lookup(name)`）
- **触发条件**：配置 `linux.umu_runtime_user: root`（或指向任意 uid=0 账号）。
- **后果**：`Managed()==true`、`Bypassed==false`；`ResolveCredential` 返回 `Uid:0`，游戏仍以 root 运行；启动自检（`checkOwnedDir` 对 `/root`、`sampleOwnerMismatch` 期望 uid=0）**全部通过**，preflight 的 `umuRuntimeUser.managed=true` 且 `ready=true`。即用户以为在降权，实际是 root，且无任何告警。与 `LINUX_RUNTIME_PRIVILEGE_PLAN.md §2`「宁可服务起不来，也不默默把公网游戏进程跑成 root」直接冲突。
- **修复建议**：`ResolveCredential` 中解析结果加硬校验：

```go
if uid == 0 {
    return nil, "", fmt.Errorf("配置的运行时用户 %s 解析为 uid=0（root）；"+
        "请改 linux.umu_runtime_user，或显式设 linux.umu_run_as_root: true 以承认以 root 运行", u.Username)
}
```

并在 `validate.go` 里对 `UmuRuntimeUser == "root"` 直接报错（跨平台静态拦截）。

#### [P1] Python 版本探测无超时且持锁执行，探测挂起会拖死启动与实例启动

- **位置**：`pkg/pyfinder/pyfinder.go:246-266`（`exec.Command(path, "-c", probeScript).Output()`，无 ctx/timeout）；`Resolve` 在探测期间持有 `r.mu`（`:86-87`）；调用点 `internal/runner/python_linux.go:18-20`、`pkg/umu/umu.go:169-171`
- **触发条件**：`PATH` 上某候选（`python3.10`…`python3.20`、`python3`、`python`）是等待 stdin/挂死的脚本或包装器。
- **后果**：`runner.Preflight()`（setup）与实例启动（`umuCommandLine` → `umuInterpreter`）都会无限期阻塞；由于 `pyfinder.Resolver` 持锁，同进程内所有解释器解析调用方一起卡死。
- **修复建议**：改用 `exec.CommandContext` + 5s 超时。

#### [P2] `Problems` 用 `user.Lookup().HomeDir`，与 `EnsureUser`/`HomeDir()` 的判空规则不一致

- **位置**：`pkg/sysuser/sysuser_linux.go:337`（`checkOwnedDir(u.HomeDir, ...)`）vs `:112-114`（`HomeDir == "" || == "/"` 时 `return nil`）与 `:38-47`
- **触发条件**：运行时用户（或 uid 指向的既有账号）passwd 家目录为空或 `/`（系统账号常见，如 `nobody`）。
- **后果**：`EnsureUser` 认为「无需处理」直接返回，但 `Problems` 会 `Stat("/")` 并因属主 uid=0 报 `umu-runtime-home-bad` → **阻断启动**；实际家目录并非 `/`。两侧结论相反。
- **修复建议**：`Problems` 也走统一解析，`filepath.Clean` 后判空/判 `/` 复用 `EnsureUser` 的分支。

#### [P2] 深度探测把写探针落进共享 prefix，overlay 模式下是对已挂载 lowerdir 的元数据写

- **位置**：`internal/runner/runtimeuser_linux.go:225`（`ProbeDir: wineprefixMgrFor(cfg).Dir("")`）；`pkg/sysuser/sysuser_linux.go:375-379, 440-457`（`deepProbeWrite` 真实 `touch`/`rm`）；实例门禁 `internal/instance/server.go:519`（`forceDeep=true`）
- **触发条件**：`prefix_mode: overlay`，`Dir("")` 返回共享前缀基目录 `{BaseDir}/umu-prefix`，它正被其它在跑实例当作 overlay 的 `lowerdir`。
- **后果**：每次实例启动都向「仍被挂载为 lowerdir 的目录」写入并删除文件。overlayfs 明确「对 lowerdir 写操作是未定义行为」；本仓库在别处（`PrepareSharedPrefixWrite`、`rwSubtrees` 排除 mounted 层）正是为规避它。深度探测绕过了这层保护。
- **修复建议**：探测目标改为该实例自己的可写层/私有目录（如 `wp.Dir(prefixKey)`），或在 overlay 下退化为已 chown 的等价可写目录；确需探测共享前缀时先走 `PrepareSharedPrefixWrite`。

#### [P2] GID 配置不与既有账号交叉校验（与文档 §4.2 不符）

- **位置**：`pkg/sysuser/sysuser_linux.go:126-137`（`lookupOrCreate` 只比 `cfg.UID`）；`cfg.GID` 仅在 `:179-212` 创建新账号时使用
- **触发条件**：账号已存在，`linux.umu_runtime_gid` 配置了与既有主组不同的 gid。
- **后果**：配置被静默忽略。共享写的 ACL/属组都以用户**实际**主组为 group（`sharedaccess_linux.go:93, 124`），用户按配置推断的组名与实际不符。文档 §4.2 明确写两者都交叉校验。
- **修复建议**：`lookupOrCreate` 补 GID 分支，与 UID 对称报错。

#### [P2] `checkACLSupport` 吞掉非 ACL 类错误，导致「自检无提示但启动被硬错误挡住」

- **位置**：`internal/runner/sharedaccess_linux.go:190-209`（仅 `errors.Is(err, shareacl.ErrUnsupported)` 时返回 Problem，其余 `return nil`）
- **触发条件**：`shareacl.Supported` 因非 ACL 原因失败（`os.MkdirTemp` 失败、`setfacl` 因权限/只读挂载以非「not supported」文本失败，`shareacl.go:147-164`）。
- **后果**：`/api/system/preflight` 与 `setup` 自检都不报告；而 `applySharedAccess`（`sharedaccess_linux.go:129-142`）对非 `ErrUnsupported` 直接 `return aclErr` 硬失败并拒绝启动。用户看到「自检通过但起不来」。
- **修复建议**：非 `ErrUnsupported` 也返回 Problem（`Warning:false` 以阻断 setup），或至少记日志。

#### [P2] `perms status` 在运行时用户不存在时把「用户缺失」渲染成「ACL 不可用」

- **位置**：`internal/runner/sharedaccess_linux.go:86-90`（Lookup 失败即 `info.ACLError = "运行时用户 ... 不存在"`）＋ `internal/actions/perms.go:56-64`（打印 `ACL 支持：不可用（%s）`）
- **触发条件**：尚未 `setup`/未创建运行时用户时执行 `asa-server perms status`。
- **后果**：把两件不同的事混成一句，误导排障（明明可能装了 acl）。
- **修复建议**：`sharedAccessStatus` 区分 `UserMissing` 与 `ACLUnavailable`；`actionPermsStatus` 先判用户是否存在，单独提示「请先运行 asa-server setup」。

#### [P2] Python 自动探测的候选去重/择优与文档不一致

- **位置**：`pkg/pyfinder/pyfinder.go:222-231`（仅按版本 `SortStableFunc`）、`:162-200`（`probed` 只记录**成功**执行版本脚本的候选）
- **触发条件**：① 文档 §3.1 声明「版本相同则优先带版本号的名字」，实现无按名择优，只靠 `EvalSymlinks` 去重；② 所有候选都存在但版本脚本都执行失败（noexec 挂载、坏包装器）时 `probed` 为空 → 报 `no Python interpreter found`，与事实不符。
- **修复建议**：比较器加 tie-break（同版本优先带版本号名）；`probed` 记录「发现但探测失败」以区分两种失败。

### 2.3 文档 vs 代码偏差

| 文档声明                                                          | 实际代码                                                                                                                                            | 判定             |
| ------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- | -------------- |
| `LINUX_RUNTIME_PRIVILEGE_PLAN.md §8/§9`：T1/T2a/T2b/T3 全部完成   | `rwSubtrees(cfg,true)` 作**阻断项**、`rwSubtrees(cfg,false)` 作**修复项**，差集（镜像）造成永久死锁（P0）                                                               | 文档推理缺口，代码确有缺陷  |
| `LINUX_RUNTIME_PRIVILEGE_PLAN.md §4.2`：幂等且廉价，「几次 stat 就返回」           | `reconcileRuntimeOwnership` 每次启动对 prefix/clusters/overlay 全量 `WalkDir` + 逐条 `d.Info()`（`runtimeuser_linux.go:108` → `sysuser_linux.go:238-259`） | 性能描述不符         |
| §4.2：UID/GID 都交叉校验                                            | 仅校验 UID，GID 静默忽略                                                                                                                                | 代码缺失           |
| §2/§4.1：专用**非 root** 用户                                       | 无任何校验阻止解析为 uid=0                                                                                                                                | 代码缺失           |
| `LINUX_RUNTIME_PRIVILEGE_PLAN.md §3.1`：同版本优先带版本号名                | 仅按版本排序                                                                                                                                          | 代码缺失（低危）       |
| §3.1：`umuInterpreter()` 是唯一改动点、缓存单一                           | 存在两个独立 `pyfinder.Resolver`（`internal/runner/python_linux.go:13`、`pkg/umu/umu.go:129,169-171`），各读各自 Config                                       | 实现偏差（低危，有漂移风险） |
| `LINUX_RUNTIME_PRIVILEGE_PLAN.md §3.4`：`perms fix` 用于修复共享写权限 | `SharedTrees()` 只含 `server-files`/`instances`，不含镜像与 Mods                                                                                        | 覆盖面小于文档暗示      |

---

# Part 1 — umu 运行时降权：以专用非 root 用户执行游戏实例（原 `UMU_RUNTIME_USER_PLAN.md`）

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
| 10 | **降权用户执行不到选定的 Python 解释器** | `linux.umu_python_bin`（或自动探测命中的解释器）若在某个用户 HOME 下（`/root/.pyenv`、`/home/foo/venv`）或权限 700，preflight 以 root 跑会「通过」，实际降权启动 `umu-run` 时 `EACCES`。系统解释器 `/usr/bin/python3.*`（0755）无此问题 | 文档要求把 venv/pyenv 放 `/opt` 或系统路径。二期：deep-probe 里以降权身份 `python -c 'pass'` 真跑一次，失败点名「解释器在 HOME 下，降权用户读不到」。见 `docs/LINUX_RUNTIME_PRIVILEGE_PLAN.md` §4 |
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

---

# Part 2 — Linux 权限方案加固执行文档（原 `ACL_PERMISSION_HARDENING_PLAN.md`）

# Linux 权限方案加固执行文档（ACL 告警 / setup 提示 / 上传后自愈）

> 状态：**T1 / T2a / T2b / T3 全部已实施**（实施记录见 §8 与 §9），
> 真机验证清单见 §8.4 与 §9.4。fsnotify 按 §3.1 的理由不做。
> 派生自 `docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md` §3.7 / §7.5.2。
> 前置背景：该文档确立了"方案 B（组 + setgid + 默认 ACL）+ 方案 A（chown）兜底"的
> 权限模型，并已在真机验证方案 B 生效（那份文档 §7.5.2.2）。
> 本文处理三件收尾：**ACL 缺失的可见性**、**setup 阶段的引导**、
> **上传文件后如何自愈**。

---

## 0. 结论速览

| # | 诉求 | 结论 |
|---|---|---|
| **P0** | （新发现）ACL 缺失会**阻断 `asa-server setup`** | **必须修**，这是上一轮改动引入的回归 |
| 1 | 用 fsnotify 监听上传、变更后重建 ACL | **建议不做**（§3.1）。审查这条时做了全量审计（§3.2）：同类路径共 10 处，**全部是 asa-server 自己以 root 创建的**，没有一处属于"外部改动"，所以没有任何一处需要监听。但审计查出 3 处（`Save/<MapName>`、`Logs/ShooterGame.log`）目前排在 prepare **之后**执行 —— 无 acl 的机器上游戏连自己的日志都写不了。正解是把 prepare 挪到 `runner.Run` 正前方（**T2a**，§3.3），一次覆盖全部 |
| 2 | Linux 下 acl 不存在时给出警告 | **做**，但要先有"警告"这个概念 —— 现在 `Problem` 只有一种严重级别，任何一条都会阻断 setup（§2） |
| 3 | setup 阶段给出安装提示 | **做**，§2 的严重级别落地后自然得到（§4） |

---

## 1. P0：ACL 缺失现在会阻断 setup（上一轮引入的回归）

### 1.1 问题

上一轮把 `checkACLSupport()` 接进了 `preflight()`：

```go
// internal/runner/preflight_linux.go
if p := checkACLSupport(); p != nil {
    problems = append(problems, *p)
}
```

而 `preflight()` 的两个消费方都把"有任何一条"等同于"环境不可用"：

```go
// internal/actions/setup.go:115
func runLinuxPreflight(ignore bool) error {
    problems := runner.Preflight()
    if len(problems) == 0 { ... return nil }
    fmt.Println("宿主运行时依赖不满足，setup 无法继续。...")
    ...
    return fmt.Errorf("宿主依赖缺失，已中止；...")   // ← 直接中止
}
```

```go
// internal/webapi/systemapi/systemapi.go:44
"healthy": len(problems) == 0,                      // ← 前端据此判定环境是否就绪
```

**后果**：一台没装 `acl` 包的 Linux 机器上，`asa-server setup` 会**直接失败**，
提示"宿主依赖缺失"。但缺 ACL 根本不是缺失依赖 —— 代码会降级到方案 A，
一切照常工作，只是少了增量保护。

这和 `glibc32` / `python3` 是两类东西：缺后者 setup 继续跑只会白下载几百 MB
（`setup.go:110-114` 的原始注释说得很清楚），缺 ACL 则完全不影响 setup 成功。

### 1.2 根因：`Problem` 没有严重级别

```go
type Problem struct {
	Name   string
	Detail string
	Fix    string
}
```

所有检查项被一视同仁。要表达"这条是建议不是阻断"，就得先有这个维度。

---

## 2. T1：给 `Problem` 加严重级别（P0，其余任务的前置）

### 2.1 结构变更

```go
// internal/runner/runner.go
type Problem struct {
	Name   string
	Detail string
	Fix    string
	// Warning 为 true 表示这是**建议**而非阻断项：功能仍然可用，
	// 只是降级或有更好的做法。消费方必须区分对待 —— setup 不因它中止，
	// preflight API 不因它判定 unhealthy。
	Warning bool
}
```

**不加 json tag**：`Problem` 的三个既有字段都没有 tag（序列化为
`Name`/`Detail`/`Fix`），新字段沿用同一约定，序列化为 `Warning`。
新增字段对消费方向后兼容。

### 2.2 消费方改造

| 文件 | 改动 |
|---|---|
| `internal/runner/sharedaccess_linux.go` | `checkACLSupport()` 返回的 Problem 置 `Warning: true` |
| `internal/actions/setup.go` | `runLinuxPreflight` 拆成阻断项与建议项两组分别打印；**只有阻断项非空才中止** |
| `internal/webapi/systemapi/systemapi.go` | `healthy` 改为"无阻断项"；`problems` 原样返回（含 `warning` 字段） |
| `internal/webapi/actions.go:398` | 启动日志按级别分流：阻断项 `Warnf`，建议项也 `Warnf` 但文案区分（"建议"而非"问题"） |

### 2.3 setup 的新输出形态

全绿：

```
宿主依赖自检：通过
```

只有建议：

```
宿主依赖自检：通过（1 项建议）
  - [posix-acl] /opt/asa-server/basedir 不支持 POSIX ACL（setfacl not found in PATH）。
    asa-server 会退回到「把 server-files/instances 整体 chown 给运行时用户」的兜底方案：
    当前能用，但之后以 root 上传的 ArkApi 插件、mod 文件，以及 SteamCMD 更新产生的新文件，
    游戏进程都会写不了，直到下次重启 asa-server 或重跑更新
      建议：apt install acl（Debian/Ubuntu）/ dnf install acl（Fedora）/ pacman -S acl（Arch），
            并确认所在文件系统挂载时启用了 acl
```

有阻断项时维持现有行为（打印后中止，`--ignore-preflight` 仍是逃生舱），
建议项一并列出但不参与中止判定。

### 2.4 验收

- 无 `acl` 的机器上 `asa-server setup` **能跑完**，且输出里出现 `[posix-acl]` 建议
- `GET /api/system/preflight` 在同样机器上返回 `healthy: true`，
  且 `problems[]` 里那条带 `"warning": true`
- 装上 `acl` 后重跑，该条消失
- 缺 `glibc32` 时 setup 仍然中止（不因这次改动被放行）

---

## 3. T2：诉求一 —— 不做 fsnotify

### 3.1 为什么不做 fsnotify

**理由一：方案 B 下它是冗余的。**

默认 ACL 的作用点就是**内核的文件创建路径**。`ShooterGame` 上挂着
`default:group:asa-umu-runtime:rwx` 之后，root 通过 SFTP 传进来的每个文件，
在 `open(O_CREAT)` 返回的那一刻就已经是属组正确、组可写的 —— 不需要任何用户态
进程知道这件事发生过。再加一层 fsnotify 去"发现并修复"，修的是一个不存在的问题。

**理由二：方案 A 下它是个坏解法。**

- inotify **不递归**。要覆盖 `server-files` 得给每个目录单独下 watch，
  这棵树有几千个目录；`fs.inotify.max_user_watches` 在不少发行版上仍是 8192。
- 新建目录需要动态补 watch，而"补 watch"与"目录里已经开始写文件"之间存在
  天然竞态 —— 恰恰是上传大量文件时最容易发生的场景。
- SFTP / rsync 的写法是「写临时文件 → rename」，事件量大且需要去重。
- 它是个**常驻机制**，为的是弥补一个 `apt install acl` 就能根治的缺失。

**理由三：真实暴露面比看上去小得多 —— 但其中一处根本不该由用户负责。**

ArkApi 插件位于 `ShooterGame/Binaries/Win64/ArkApi`，落在镜像的**完整复制区**
（`mirror.go:21-24` 的 `win64RelPath`）。实例启动时它被真实拷贝进
`server-files-tmp-<instance>`，随后 `ChownMirrorForRuntime` 整棵 chown ——
**即使在方案 A 降级模式下，上传的插件对实例也是可用的**。

方案 A 下真正暴露的是这两处：

| 路径 | 谁创建的 | 现状 |
|---|---|---|
| `Mods` / `ModsUserData`（镜像里 junction 回 server-files） | **asa-server 自己，以 root** | ❌ 缺口 —— 见下方 §3.1.1，**这不是 `perms fix` 该管的事** |
| `verify` 直接跑 server-files | — | ✅ `VerifyServerInstallation` 已**无条件**调 `PrepareSharedTree(ServerFilesDir)` |

#### 3.1.1 修正：`Mods`/`ModsUserData` 是程序自己造的，必须程序自己修

初稿把这一处归给了 `perms fix`，这是错的。看 `mirror.go:114-120`：

```go
// 指向源的 exception（Mods / ModsUserData）必须先在源里存在：
// createInstanceMirror 靠 Walk(ServerFilesDir) 发现条目，源目录缺失就永远走不到这个分支
if err := os.MkdirAll(
    filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64SharedRelPath)), 0755,
); err != nil {
```

**每次建/修镜像时，asa-server 都会以 root 在 `server-files` 下创建这个目录**，
随后游戏以 `asa-umu-runtime` 的身份往里写 `ModsUserData` ——
正是 `LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md` §3.6 那个 CFCore 报错的形状。

这件事有确定的时机（实例启动），程序完全知道它发生了，
**要求用户手动跑一条命令去修程序自己刚造出来的目录，是不合理的设计。**
`perms fix` 的定位应该只是"带外变更（SFTP 上传）的补救"，不能拿来兜程序自身的行为。

于是拆出 **T2a**（§3.3），它才是这一处的正解；`perms fix` 降为 T2b。
而且顺着这条线做了一次全量审计（§3.2），发现同类情况**还有更严重的**。

同时这也说明：即便装了 `acl`（方案 B），这一处仍值得显式修 ——
方案 B 的继承依赖"`server-files` 在更早某个时刻被 prepare 过"，
而 T2a 不依赖任何前置状态。

综上，留给 fsnotify 的地盘只剩"管理员用 SFTP 传了 mod 包"这一种带外场景，
为它引入几千个 inotify watch 的常驻监听，性价比不成立。

### 3.2 全量审计：还有哪些「程序以 root 造、游戏要写」的路径

把仓库里所有 `MkdirAll` / `Mkdir` / `WriteFile` / `Create` 过了一遍，
筛出落在「游戏进程需要写」的树里的：

| # | 位置 | 造出什么 | 时机 | 覆盖情况 |
|---|---|---|---|---|
| 1 | `mirror.go:116` | `server-files/.../Win64/ShooterGame`（Mods/ModsUserData 的 junction 目标） | 建/修镜像 | T2a |
| 2 | `mirror.go:230`、`:688` | 同上（同步路径补建源目录） | 镜像同步 | T2a |
| 3 | `mirror.go:290` | junction 目标（migrate 路径） | 镜像迁移 | T2a |
| 4 | **`common.go:316`** | **`instances/<name>/Save/<MapName>`** | **拼启动参数时** | ⚠️ **在现有 prepare 之后执行** |
| 5 | **`server.go:373` `GetGameLogFilePath`** | **`instances/<name>/Logs` + `ShooterGame.log`（0644）** | **启动流程中** | ⚠️ **同上** |
| 6 | **`server.go:386`** | **写空 `ShooterGame.log`** | **启动流程中** | ⚠️ **同上** |
| 7 | `server.go:837`、`config.go:186/389/407/577` | `instances/<name>/Config` + 两个 ini | 建实例 / 改配置 / 同步配置 | 下次启动前的 prepare |
| 8 | `backup.go:175/225/231` | `instances/<name>/{Config,Save}` 的恢复内容 | 恢复备份 | 下次启动前的 prepare（且 `backup.go:162` **运行中直接拒绝**恢复） |
| 9 | `installer.go:274/431` | `server-files`、`ShooterGame/Saved` | update / verify | ✅ 已无条件 `PrepareSharedTree` |
| 10 | `fixups_linux.go:66` | `~/.steam/sdk{32,64}` | fixups | ✅ 紧随其后 `ChownTreeForRuntime` |

#### 3.2.1 最要命的是 4/5/6：它们在 prepare **之后**执行

当前已落地的代码是这个顺序：

```
server.go:259  ChownMirrorForRuntime(mirrorDir)
server.go:269  PrepareSharedTree(instances/<name>)      ← 现有的 prepare 在这里
       ...
common.go:316  MkdirAll(instances/<name>/Save/<MapName>)   以 root 创建   ← 之后
server.go:373  MkdirAll(instances/<name>/Logs)             以 root 创建   ← 之后
server.go:386  WriteFile(ShooterGame.log, "", 0644)        以 root 创建   ← 之后
server.go:422  runner.Run(...)                              游戏启动
```

**三处 root 侧创建全部发生在 prepare 之后、启动之前。** 降级模式（无 acl）下：

- `Save/<MapName>` 属主 root → **换地图后新存档目录写不进去**
- `ShooterGame.log` 是 root:root 0644 → **游戏日志根本写不了**，
  而 `waitServerStartup` 正是靠 tail 这个日志判断"启动完成"
  —— 实例会永远停在 `starting`，症状和 §2.5 那个 `waitForGamePID` 超时几乎一样，
  但原因完全不同，非常难查

也就是说：**当前已合入的代码之所以能正常工作，纯粹是因为这台机器装了 `acl`**
（方案 B 的默认 ACL 让这三处在创建瞬间就继承了正确属组与组写权限）。
一台没装 `acl` 的机器会踩进上面两条。

这条比 Mods/ModsUserData 更值得修，而且它进一步说明 fsnotify 不是答案 ——
这些路径不是"某个时刻被外部改动"，而是**程序自己在启动流程里创建的**，
时机完全确定。

#### 3.2.2 结论：一个放对位置的 prepare 能关掉全部

第 1–8 项有一个共同点：**它们全都只在实例启动之后才被游戏使用**。

- 4/5/6 就在启动流程内
- 1/2/3 是建镜像时造的，同一次启动内
- 7/8 是离线改动（备份恢复在运行中会被直接拒绝），下一次启动前生效

所以只要把 prepare 挪到**所有 root 侧创建都完成之后、`runner.Run` 之前**，
这 8 项一次全覆盖。不需要监听，也不需要用户手动介入。

唯一的残留：**实例运行中通过 Web UI 改 Game.ini / GameUserSettings.ini**
（`configapi` 没有运行中拦截）。降级模式下，root 重写过的 ini 会让游戏在关服时
写回 GameUserSettings.ini 失败。方案 B 下不存在这个问题。
这一处窄到不值得为它引入任何常驻机制，记录在案即可。

### 3.3 T2a：junction 目标在实例启动时自动准备（无需用户介入）

**原则**：`Lchown` 不跟随软链，所以**镜像里每一条 junction 的目标目录，
都必须单独做共享写处理**。这个原则一旦成立，就不该逐个路径去记 ——
镜像本来就有这份清单。

`mirror.go:246` 的 `buildExceptionTargets` 已经枚举了全部 junction 目标：

```go
targets := map[string]string{
    "ShooterGame/Saved/Config/WindowsServer": instances/<name>/Config,
    "ShooterGame/Saved/Logs":                 instances/<name>/Logs,
}
targets["ShooterGame/Saved/"+saveDir]        = instances/<name>/Save
targets[win64SharedRelPath]                  = server-files/ShooterGame/Binaries/Win64/ShooterGame
```

把它导出，实例启动时逐个 `PrepareSharedTree`：

```go
// internal/mirror/mirror.go
// ExceptionTargets 返回镜像中各 junction 指向的**真实目录**。
// 以不同用户运行游戏的调用方必须把这些目录处理成可共享写 ——
// 镜像自身的 Lchown 只改到链接，改不到目标。
func ExceptionTargets(instanceName string, cfg *cfgpkg.InstanceConfig) []string
```

调用点**必须移到 `runner.Run` 正前方**（§3.2.1 的三处 root 侧创建都在它之前完成），
而不是现在的 259/269 行：

```go
// internal/instance/server.go —— 紧邻 runner.Run 之前，位置是本设计的一部分
//
// 这里是"asa-server 以 root 造完所有东西"与"游戏以降权用户接手"之间的唯一交界。
// 放在更早的位置会漏掉启动流程自身创建的 Save/<MapName> 与 ShooterGame.log
// （见 docs/LINUX_RUNTIME_PRIVILEGE_PLAN.md §3.2.1）。
for _, target := range mirror.ExceptionTargets(instanceName, config) {
    if err := runner.PrepareSharedTree(target); err != nil {
        startErr = fmt.Errorf("为降权运行时用户准备目录 %s 失败: %w", target, err)
        return startErr
    }
}
handle, err := runner.Run(context.Background(), arkExe, args, runner.Options{...})
```

这会**取代**现在 269 行那句 `PrepareSharedTree(instances/<name>)`：
位置更靠后（覆盖启动流程自身的创建），覆盖面更准
（三个实例子目录 + server-files 里那个共享目录），
且以后新增 junction 会自动被覆盖，不会再漏。

`ChownMirrorForRuntime(mirrorDir)`（259 行）**保持原位不动** ——
它处理的是镜像本身，而镜像在那之后不再被 root 写入。

分层不受影响：`mirror` 不需要 import `runner`，只多导出一个查询函数；
调用发生在 `instance`，而它本来就同时依赖两者。

成本：四个目录都很小（Config / Logs / Save / Mods），不是 5 万条目的 `server-files`，
每次启动跑一遍完全可接受。

#### 3.3.1 验收

在**卸载了 `acl`** 的环境下验证（这是唯一能暴露顺序问题的配置；
装了 acl 的机器上默认 ACL 会掩盖一切）：

- 删掉 `server-files/ShooterGame/Binaries/Win64/ShooterGame` 后启动实例，
  该目录被重建且属组为运行时用户、组可写
- `ShooterGame.log` 中不再出现
  `LogCFCore: Error: Unable to create a directory .../ModsUserData/83374`
- **`instances/<name>/Logs/ShooterGame.log` 属组为运行时用户且组可写**，
  且实例状态能推进到 `started`（验证第 5/6 项 —— 日志写不了时
  `waitServerStartup` 会永远停在 `starting`）
- **换一张新地图启动**，`instances/<name>/Save/<新地图名>` 组可写（验证第 4 项）
- 运行中恢复备份被拒绝（`backup.go:162` 既有行为不受影响）
- Windows 上 `PrepareSharedTree` 恒为 no-op，循环空转，行为不变

### 3.4 T2b：`asa-server perms` —— 只管带外变更

"我刚传了文件"是一个**用户知道、程序不知道**的离散事件。
与其让程序猜，不如给一条命令让用户说。

```
asa-server perms status     # 报告 server-files / instances 的权限模型现状
asa-server perms fix        # 重新施加共享写权限（方案 B，缺 acl 时降级方案 A）
```

`perms status` 的输出即用户此前手工用 `getfacl` 拼出来的那份诊断：

```
运行时用户：asa-umu-runtime (uid=997 gid=997)
ACL 支持：  可用 (setfacl: /usr/bin/setfacl)
权限模型：  方案 B（组 + setgid + 默认 ACL）

  /opt/asa-server/basedir/server-files
    属组      asa-umu-runtime          ✓
    setgid    已设                      ✓
    默认 ACL  default:group:asa-umu-runtime:rwx   ✓
  /opt/asa-server/basedir/instances
    ...
```

缺 ACL 时把"权限模型"打成 `方案 A（chown 兜底）`，并附上安装建议 ——
与 T1 的 preflight 建议同一份文案。

**实现基本是现成的**：`prepareSharedTree` / `defaultACLMissing` /
`sharedAccessNeeded` / `aclSupported` 都已存在，这个命令只是把它们组装起来对外暴露。

### 3.5 可选：`POST /api/system/perms/fix`

前端在"插件管理"类页面放一个"修复权限"按钮。
仅当 `runner.RuntimeUserStatus().Managed` 为真时展示 —— Windows 与不降权场景下无意义。

优先级低于 CLI：SFTP 上传的用户本来就在终端里。

### 3.6 什么情况下我会改主意去做 fsnotify

如果同时满足：
1. 明确不接受"要求安装 `acl` 包"这个前提，且
2. 要求带外上传的文件**无需任何命令**即刻生效

那么可以做一个**窄范围**的监听 —— 只 watch
`ShooterGame/Binaries/Win64/ShooterGame`（Mods/ModsUserData，§3.1 里那处真缺口），
而不是整棵 `server-files`。这个目录的子目录数量是十几个量级，
watch 数量可控，也不需要处理"几千个目录动态增删"的复杂度。

但即便如此，它仍然只是方案 A 的补丁。**先把方案 B 装上，性价比高得多。**

### 3.7 T2b 的验收

- `asa-server perms status` 在装了 acl 的机器上报告"方案 B"，卸载 acl 后报告"方案 A"
- 以 root 在 `server-files` 下新建文件后 `asa-server perms fix`，
  该文件变为属组 `asa-umu-runtime` 且组可写
- `perms fix` 可重复执行且幂等（第二次不改变任何 `getfacl` 输出）

---

## 4. T3：诉求三 —— setup 阶段的安装提示

T1 落地后，`asa-server setup` 会在自检阶段自动打印 `[posix-acl]` 建议
及对应的安装命令，诉求三即告完成。再补两处收尾：

### 4.1 `printPostSetupTips` 增加一行

当 `checkACLSupport()` 非空（即当前处于降级状态）时，在 setup 末尾的
"接下来可以"里追加：

```
  apt install acl && systemctl restart asa-server   # 启用权限继承，避免上传插件后需要重启
```

理由：自检的输出在长长的下载日志之前，setup 跑完几分钟后用户早就翻过去了；
末尾的提示才是他真正会看到的那一屏。

### 4.2 文档同步

| 文件 | 改动 |
|---|---|
| `docs/LINUX_DEPLOYMENT.md` | 依赖列表加入 `acl`，标注为"强烈建议"而非必需，并说明缺失时的降级行为 |
| `CLAUDE.md` | `runner` 包的说明里补一句共享写权限模型（现在只提了 prefix 与 mirror 的属主） |

注：`scripts/ark_instance_manager.sh` 的依赖数组**不改** ——
那个脚本不降权，压根不需要 ACL，加进去只会误导。

---

## 5. 实施顺序与工作量

| 任务 | 内容 | 依赖 | 规模 |
|---|---|---|---|
| **T1** | `Problem.Warning` + 三个消费方分级 | — | 小（4 个文件，~60 行） |
| **T2a** | `mirror.ExceptionTargets` + 实例启动逐个 `PrepareSharedTree` | — | 小（2 个文件，~25 行） |
| **T2b** | `asa-server perms status\|fix` | T1（复用文案） | 中（新增 1 个 actions 文件 + runner 导出 2 个查询函数） |
| **T3** | setup 末尾提示 + 文档同步 | T1 | 小 |

优先级：

1. **T1（P0）** —— 在它之前，任何一台没装 `acl` 的 Linux 机器都无法完成
   `asa-server setup`。这是上一轮改动引入的回归，应当尽快单独落地。
2. **T2a** —— 与 T1 无依赖关系，可并行。它补的是**程序自身行为**留下的缺口，
   优先级高于 T2b（后者只服务于带外场景）。
3. T3、T2b —— 依赖 T1 的文案与结构。

T1 与 T2a 都很小，建议合成一个 PR 一起验证。

---

## 6. 风险与注意事项

- **`Problem` 是跨平台共享结构**，Windows 上 `Preflight()` 恒返回 nil，
  新增字段不影响任何 Windows 路径。
- **前端兼容性**：`problems[].warning` 是新增字段。现有前端会把建议项当成
  问题项渲染（显示为红色），功能不受影响但观感不对。
  前端适配可以后置，但要在 T1 的 PR 里写明。
- **`healthy` 语义变更**：从"无任何检查项"变成"无阻断项"。
  这是有意为之，但要确认前端没有别的地方依赖旧语义（`environmentReady`
  是另一个字段，不受影响）。
- **`perms fix` 会遍历整棵 `server-files`**（约 5 万条目）。
  只改元数据，SSD 上秒级，但要在命令输出里给出进度提示，
  避免用户以为卡住了。
- **不要在 `perms fix` 里做 chown 到 root 的"归位"**：
  现网机器的属主已经是 `asa-umu-runtime`（早先手工 chown 的结果），
  改回 root 没有任何功能收益，却会在一台正在服役的机器上动几万个 inode。

---

## 7. 明确不做的事

- **fsnotify 全树监听**（§3.1 三条理由）
- **把 `acl` 变成硬依赖**：降级路径是有效的，不应把一个可用的环境判为不可用 ——
  这正是 §1 那个回归的教训
- **自动 `apt install acl`**：本项目不代替用户做包管理，
  与 `preflight` 现有的所有检查项保持一致（都是给命令、不代跑）

---

## 8. 实施记录：T1 + T2a（2026-08-29）

`go build` / `go vet` 在 `GOOS=windows` 与 `GOOS=linux` 下均通过。

### 8.1 T1：`Problem` 分级

| 文件 | 改动 |
|---|---|
| `internal/runner/runner.go` | `Problem` 新增 `Warning bool`；新增 `Blockers()` / `Advisories()` 两个过滤器，避免每个消费方各写一遍循环 |
| `internal/runner/sharedaccess_linux.go` | `checkACLSupport()` 返回的 Problem 置 `Warning: true` |
| `internal/actions/setup.go` | `runLinuxPreflight` 按级别分流；**只有阻断项参与中止判定**；抽出 `printProblems(problems, fixLabel)`，阻断项标"修复"、建议项标"建议" |
| `internal/webapi/systemapi/systemapi.go` | `healthy` 从 `len(problems)==0` 改为 `len(Blockers(problems))==0`；`problems` 原样返回（含 `Warning` 字段）供前端区分展示 |
| `internal/webapi/actions.go` | 启动日志分流：建议项打 `Linux runtime advisory: ... (suggested: ...)`，阻断项维持 `Linux runtime preflight: ... (fix: ...)` |
| `internal/runner/problem_test.go` | 新增：分组正确性、顺序保持、nil 输入、**全是建议项时 `Blockers` 为空**（这条直接对应 §1 的回归） |

关于 JSON 字段名：`Problem` 的既有字段都没有 tag（序列化为 `Name`/`Detail`/`Fix`），
新字段沿用同一约定，序列化为 `Warning`。前端目前**尚未消费**
`/api/system/preflight`（全仓搜索无引用），所以不存在兼容性负担。

### 8.2 T2a：junction 目标在启动前统一准备

| 文件 | 改动 |
|---|---|
| `internal/mirror/mirror.go` | 新增导出 `ExceptionTargets(instanceName, cfg) []string` —— 从 `buildExceptionTargets` 取值、去重、排序。清单集中在一处，以后新增 junction 会自动被权限处理覆盖 |
| `internal/instance/server.go` | **删掉** 269 行那句 `PrepareSharedTree(instances/<name>)`；改为在 `runner.Run` **正前方**遍历 `ExceptionTargets` 逐个 `PrepareSharedTree` |
| `internal/mirror/exception_targets_test.go` | 新增：四个目标齐全、**共享 Mods 目录对每个实例都在**、输出有序且无重复 |

`ChownMirrorForRuntime(mirrorDir)`（259 行）保持原位不动 ——
它处理镜像自身，而镜像在那之后不再被 root 写入。

**位置是这次改动的实质**，代码里也写明了原因：启动流程自己还会以 root 创建
`Save/<MapName>`、`Logs/` 和 `ShooterGame.log`（§3.2.1 的第 4/5/6 项），
放在它们之前就会漏掉 —— 而漏掉 `ShooterGame.log` 的后果最隐蔽：
游戏写不了自己的日志，`waitServerStartup` 靠 tail 它判断启动完成，实例会一直停在
`starting`。

### 8.3 Windows 影响面

- `Problem.Warning`：Windows 的 `preflight()` 恒返回 nil，`Blockers`/`Advisories`
  对空切片返回 nil，`setup.go` 走"通过"分支，输出与改动前逐字相同
- `PrepareSharedTree`：Windows 恒 `return nil`，新循环空转
- `ExceptionTargets`：纯路径计算，无平台差异；Windows 上镜像用的是 NTFS junction，
  目标目录同样是这四个

### 8.4 待验证

**必须在卸载了 `acl` 的环境下验证** —— 装了 acl 的机器上默认 ACL 会掩盖顺序问题，
测不出 T2a 的价值：

1. `asa-server setup` 能跑完，输出含 `[posix-acl]` 建议且不中止（T1，§2.4）
2. `GET /api/system/preflight` 返回 `healthy: true`，`problems[]` 里那条 `Warning: true`
3. 启动实例后 `instances/<name>/Logs/ShooterGame.log` 属组为运行时用户且组可写，
   状态能推进到 `started`（T2a，§3.3.1）
4. 换一张新地图启动，`instances/<name>/Save/<新地图名>` 组可写
5. 删掉 `server-files/ShooterGame/Binaries/Win64/ShooterGame` 后启动，该目录被重建且组可写

### 8.5 剩余任务

- **T2b**：`asa-server perms status|fix`（§3.4）—— 只服务真·带外场景（SFTP 传 mod 包）
- **T3**：setup 末尾提示 + `docs/LINUX_DEPLOYMENT.md` / `CLAUDE.md` 文档同步（§4）

---

## 9. 实施记录：T2b + T3（2026-08-29）

`go build` / `go vet` 在 `GOOS=windows` 与 `GOOS=linux` 下均通过，
`internal/actions` / `internal/runner` / `internal/mirror` 测试全绿。

### 9.1 T2b：`asa-server perms status|fix`

| 文件 | 改动 |
|---|---|
| `internal/runner/runner.go` | 新增 `SharedAccessInfo` / `TreeAccessInfo` 与 `Model()`（`"acl"` / `"chown"` / `"n/a"`）；导出 `SharedAccessStatus()`、`SharedTrees()` |
| `internal/runner/sharedaccess_linux.go` | 实现 `sharedAccessStatus()`（**只读**：属组/权限位采样 + 默认 ACL 检查 + ACL 可用性探测）与 `sharedTrees()` |
| `internal/runner/runtimeuser_windows.go` | 两个函数的空实现 |
| `internal/actions/perms.go` | 新增命令，两个子命令 |
| `main.go` | 注册 `actions.PermsCommand()` |
| `internal/runner/sharedaccess_test.go` | 新增：`Model()` 四种状态、未降权时报告为空 |

`perms status` 的实际输出（未降权时）：

```
当前不涉及降权运行：游戏进程与 asa-server 使用同一身份，无需共享写权限处理。
（Windows 恒是这种情况；Linux 上非 root 启动、或 linux.umu_run_as_root=true 时也是。）
```

降权且 ACL 可用时逐棵树打勾：

```
运行时用户：asa-umu-runtime (uid=997 gid=997，属组 asa-umu-runtime)
ACL 支持：  可用 (/usr/bin/setfacl)
权限模型：  方案 B（组 + setgid + 默认 ACL）—— 新文件在创建瞬间即继承，无需事后修复

  /opt/asa-server/basedir/server-files
    属组/权限位  ✓
    默认 ACL     ✓  (default:group:asa-umu-runtime:rwx)
  /opt/asa-server/basedir/instances
    ...

结论：全部就绪。
```

**两个子命令都不调 `VerifyEnvironmentReady()`**：那个检查要求 SteamCMD 与服务端
本体都已安装，而权限诊断恰恰经常发生在环境没装好的时候（安装过程本身就可能因为
权限失败）。BaseDir 与 runner 配置由 `main()` 在 CLI 分发前统一装配，
这两条命令需要的前提仅此而已。

`perms fix` 逐棵树打印「处理 … 完成（耗时）」——`server-files` 约 5 万条目要走几秒，
不打印用户会以为卡住。ACL 不可用时先说明"本次只能按方案 A 修复"，再给安装建议。

### 9.2 T3：setup 引导与文档

| 文件 | 改动 |
|---|---|
| `internal/actions/setup.go` | `printPostSetupTips()` 在降级（`Model() == "chown"`）时打一段醒目提示 + 安装命令；Linux 分支的"接下来可以"增加 `asa-server perms status` |
| `docs/LINUX_DEPLOYMENT.md` | 依赖表加入 `acl`（标注**强烈建议、非必需**）；新增「共享写权限与 `acl`」小节，说明降级行为与两条命令的分工 |
| `CLAUDE.md` | `runner` 目录树补 `sharedaccess_linux.go` 与 `preflight_linux.go` 的 Warning 语义；Key Packages 增加 runner 权限模型条目；`installer` 条目补上"verify 以端口监听为成功判据" |

末尾提示放在 `printPostSetupTips()` 而不是只靠自检输出，是因为自检那条排在几百 MB
下载日志之前 —— setup 跑完几分钟后早被刷走，末尾这一屏才是用户真正会看到的（§4.1）。

### 9.3 Windows 影响面

- `perms status` / `perms fix`：`SharedAccessStatus()` 恒返回零值，两条命令都打印
  "当前不涉及降权运行"后正常退出。已在 Windows 上实跑验证
- `printPostSetupTips()`：`info.Managed` 恒 false，新增的提示块不会触发，
  Windows 分支输出与改动前逐字相同
- 其余均为 Linux 专属文件或纯文档

### 9.4 待验证（真机，Linux）

1. **装了 `acl` 的机器**：`asa-server perms status` 报告"方案 B"，各树两项全 ✓
2. **卸载 `acl` 后**：报告"方案 A（chown 兜底）"，`asa-server setup` 末尾出现安装提示块
3. 以 root 在 `server-files` 下新建文件 → `perms status` 应显示属组/权限位 ✗ →
   `perms fix` → 复查恢复 ✓
4. `perms fix` 幂等：连跑两次，第二次 `getfacl` 输出无变化

### 9.5 四项任务全部完成

T1（`Problem` 分级，修 P0 回归）、T2a（junction 目标在启动前统一准备）、
T2b（`perms status|fix`）、T3（setup 引导 + 文档）均已落地。
**fsnotify 按 §3.1 的三条理由不做**；若日后确需，§3.6 记录了改主意的条件
与那时应采用的窄范围设计。

---

# Part 3 — UMU Python 解释器多版本探测与固定（原 `UMU_PYTHON_DISCOVERY_PLAN.md`）

# UMU Python 解释器多版本探测与固定 — 开发计划

> 状态：**已实现**（PR1–PR4 一次性落地）。二期待办：§4 风险 #5 的降权 deep-probe 真跑解释器检查。
> 关联：`docs/LINUX_COMPATIBILITY_PLAN.md` §4.2（运行时依赖自检）、`docs/LINUX_RUNTIME_PRIVILEGE_PLAN.md` §9 风险 10（降权运行）
> 影响范围：仅 Linux；Windows 无 Wine/Proton 运行时概念，本计划全部为 `//go:build linux`
>
> 落地文件：`internal/runner/python_linux.go`（新增，发现逻辑 + 缓存 + `runtimePython`）、
> `python_linux_test.go`（新增，13 个用例）、`preflight_linux.go`（`checkPython3` 改为调
> `pythonProblem`）、`runner_linux.go` `umuCommandLine`（`<python> <umu-run> <exe>`）、
> `umu_linux.go` `warmPrefix`、`runner_windows.go`（`runtimePython` 桩）、`runner.go`
> （`Config.PythonBin` + `RuntimePythonInfo`/`RuntimePython`）、`appconfig`
> （`LinuxConfig.UmuPythonBin` + 默认 + 校验 + template）、`main.go` / `internal/actions/setup.go`
> 装配、`webapi/systemapi` 响应加 `umuPython`。

---

## 1. 背景与问题

`internal/runner/preflight_linux.go` 的 `checkPython3()` 目前只做两件事：

```go
path, err := exec.LookPath("python3")          // 只找 PATH 里的 "python3"
out, _ := exec.Command(path, "-c",
    "import sys; print(sys.version_info >= (3, 10))").Output()
```

umu-launcher 的 zipapp 要求 **Python >= 3.10**。当前实现有两个兼容性缺陷：

1. **系统自带 `python3` 版本过低会直接卡死初始化。**
   RHEL 8 / CentOS 8（3.6）、Ubuntu 20.04（3.8）、Debian 11（3.9）等长期支持发行版，
   系统 `python3` 就是低于 3.10 的。此时 `checkPython3()` 返回 `python3-version`
   Problem，`asa-server` 启动自检不通过，实例无法初始化。

2. **强行升级系统 `python3` 有破坏系统的风险。**
   很多发行版的包管理器、`firewalld`、`dnf`/`apt` 辅助脚本等都绑定在系统自带的
   `python3` 上。替换 `/usr/bin/python3` 指向的版本可能导致系统组件崩溃。

社区（含 umu / Proton 相关项目）的通行做法是：**并行安装一个带版本号的解释器**
（如 `python3.14`），通过 `python3.14 xxx` 这种**显式版本名**调用，不动系统默认的
`python3`。用户反馈其车上服务器已用此方式装了 `python3.14`。

### 目标

- 依赖探测把范围从单一 `python3` 扩大到**一组带版本号的候选**：
  `python3`、`python3.10`、`python3.11` … `python3.14`（并对未来版本留冗余）。
- 机器上存在**多个**满足 `>= 3.10` 的解释器时，**选最高版本**。
- 探测到的解释器要被**固定下来**：之后所有 umu-run 调用（游戏启动、prefix 预热、
  安装校验）都用**同一个**解释器，而不是再走 zipapp 的 `#!/usr/bin/env python3`
  shebang（那会退回系统默认的低版本）。
- 提供配置项让用户**显式指定**解释器：
  - 一个系统解释器名字 / 路径（对应用户「指定 python3.14」的诉求）；
  - **也包括 venv / pyenv 的解释器路径**（如 `/opt/asa-venv/bin/python`、
    `~/.pyenv/versions/3.14.0/bin/python`）。
  - 一旦配置了这个项，就**完全跳过自动探测**，只认这一个。

### 非目标

- 不由 `asa-server` 去安装 Python（仍然是用户用发行版包管理器装）。
- **不自动发现 venv / pyenv 环境**。自动探测只扫系统级的 `python3` / `python3.10`…`python3.N`；
  venv / pyenv 必须由用户通过 `linux.umu_python_bin` **显式指定路径**才会被使用。
- 不改 Windows 任何行为。

---

## 2. 现状梳理（改造前）

### 2.1 umu-run 当前如何拿到 Python

`umu-run` 是一个 **zipapp**：文件头是 `#!/usr/bin/env python3` 的 shebang，后面跟 zip 数据。

- `internal/runner/umu_linux.go:151` `ensureUmu()` 只负责解压 + `chmod 0755`，不涉及 Python。
- `internal/runner/runner_linux.go:42` `run()` → `exec.CommandContext(ctx, bin, launchArgs...)`，
  `bin` 直接就是 `umu-run` 的绝对路径。**内核读 shebang → `/usr/bin/env python3`**，
  于是永远用系统默认 `python3`。
- `internal/runner/runner_linux.go:82` `runPTY()` 同理。
- `internal/runner/umu_linux.go:256` `warmPrefix()`：`exec.CommandContext(ctx, bin, "wineboot", "--init")` 同理。
- `internal/installer` 的 `VerifyServerInstallation` 走 `runner.Run()`，被上面覆盖。
- `internal/runner/runner_linux.go:101` `checkRuntime()`：纯文件系统存在性检查，**不 exec**，无需改。

要强制指定解释器，把调用形式从

```
exec.Command("/path/umu-launcher/umu-run", args...)
```

改成

```
exec.Command("/usr/bin/python3.14", append([]string{"/path/umu-launcher/umu-run"}, args...)...)
```

Python 的 zipapp 加载器会正常执行 zip 内 `__main__.py`；且 umu 内部 fork 子进程用的是
`sys.executable`，会**自动继承**我们选定的解释器，无需额外处理。

### 2.2 配置链路

- `internal/appconfig/config.go:145` `LinuxConfig`，`config.go:511` 默认值，
  `config.go:571` `v.SetDefault("linux.*", …)`，`internal/appconfig/validate.go:159` `(*LinuxConfig).validate()`。
- `internal/appconfig/template.go:147` 生成 `config.yaml` 的 `linux:` 段注释。
- `internal/runner/runner.go:166` `runner.Config`；`runner.go:245` `getConfig()` 补默认值。
- 装配点：`main.go:282` `runner.Configure(...)`（权威，含全部 linux 字段）；
  `internal/actions/setup.go:79`（**部分**字段，历史遗留）。
  `internal/gui/gui.go:435` 也有一处，但 **GUI 仅 Windows**（`CLAUDE.md`：Linux 无 GUI），
  且该文件是 `//go:build windows`，Linux 专属字段在那里没有意义 —— **不改 gui.go**。

### 2.3 自检对外暴露

- `internal/runner/runner.go:105` `Preflight() []Problem` → `preflight_linux.go:18` `preflight()`。
- `internal/webapi/systemapi/systemapi.go:32` 组装 `/api/system/preflight` 响应。

---

## 3. 设计

### 3.1 新文件 `internal/runner/python_linux.go`（`//go:build linux`）

集中解释器发现逻辑，供 `preflight_linux.go` 与 `runner_linux.go` / `umu_linux.go` 共用。

```go
package runner

// pythonInfo 是一个已解析、已验证 (>=3.10) 的 Python 解释器。
type pythonInfo struct {
    Path  string // 绝对路径（LookPath 结果，argv[0] 用它，与子进程 PATH 无关）
    Major int
    Minor int
}

func (p pythonInfo) Version() string { return fmt.Sprintf("%d.%d", p.Major, p.Minor) }
```

#### 候选名单（按优先级/版本从高到低扫描，实际选择仍按探测到的真实版本号排序）

```
python3.20 … python3.10        // 反向遍历 minor: 20 → 10（上界留足冗余）
python3                        // 发行版自带（可能是软链）
python                         // Arch 等把 python 指向 python3
```

- 上界取 `3.20`：写死一个够用的常量 `pythonMaxMinorProbe = 20`，避免无限扫描。
  新版本发布只需调这个常量（或用户走 3.3 的显式配置）。

#### 解析算法 `resolvePython() (pythonInfo, error)`

1. **显式配置优先**：`cfg := getConfig()`，若 `cfg.PythonBin != ""` —— **只认这一个，
   不做任何自动探测回退**：
   - 取值形态（都允许）：
     - 绝对路径：`/opt/asa-venv/bin/python`、`~/.pyenv/versions/3.14.0/bin/python`
       （`~` 先 `os.UserHomeDir()` 展开）；
     - 裸名字：`python3.14`（走 `exec.LookPath` 过 `PATH`）。
   - **不要求文件名长得像 `python3.x`** —— venv 里通常就叫 `python` / `python3`。
   - `exec.LookPath` 解析（绝对路径也过它，顺带校验存在且可执行）。
   - 跑版本探测脚本（下同），`>= 3.10` → 返回 `pythonInfo{Path: <解析出的绝对路径>}`；
     否则 **硬错误**。
   - 找不到 / 不可执行 → 硬错误，消息带上原始配置值。
   - **pyenv shim 注意**：`~/.pyenv/shims/python` 是个依赖 `PYENV_ROOT`/`PATH` 的
     分发脚本，直接 exec 行为不稳。文案里建议用户填 `versions/<x>/bin/python` 真实路径，
     而不是 shim。
   - **venv 说明**：venv 只是 `pyvenv.cfg` + 指回基础解释器的软链，用绝对路径直接
     调用即可正常工作；zipapp 在 venv 解释器下运行会带上 venv 的 site-packages，
     对 umu 无害。
   - **权限提醒**（写入 §4 风险表）：venv/pyenv 常在某个用户 HOME 下，降权运行时
     那个非 root 用户可能读/执行不到。
2. **自动探测**（仅当 `cfg.PythonBin == ""`）：对候选名单逐个 `exec.LookPath`：
   - 命中就跑 `exec.Command(path, "-c", pythonProbeScript)`，
     `pythonProbeScript = "import sys;print('%d %d'%sys.version_info[:2])"`。
   - 解析出 `(major, minor)`；`major==3 && minor>=10`（或 `major>3`）才是合格候选。
   - 用**解析后的真实路径 + 版本**去重（`python3` 软链到 `python3.11` 会命中两次）。
3. 合格候选按 `(major, minor)` 降序排序，取第一个：
   - 版本相同则**优先带版本号的名字**（`python3.14` 比裸 `python3` 稳定、可读）。
4. 一个合格的都没有 → 返回 `error`，消息里**列出实际探测到的名字→版本**，
   例如：`found python3 (3.9), python3.9 (3.9); need python3.10+`。

#### 缓存

- 进程内用 `sync.Mutex` 保护一个小缓存：`{overrideKey string, info pythonInfo, ok bool}`。
- **只缓存成功结果**，键为 `cfg.PythonBin`（override 变了就重解析，兼容 GUI 多次 `Configure`）。
- 失败不缓存：`/api/system/preflight` 的语义就是「用户装完 Python 点重试」，
  每次未解析时都重新扫描；一旦成功即冻结（setup 阶段扫 ~12 个 `LookPath` +
  少量 `python -c` 的开销可接受，成功后零开销）。

#### 对外辅助

```go
// pythonProblem 把 resolvePython 的错误转成 preflight 的 *Problem；成功则 nil。
func pythonProblem() *Problem

// umuInterpreter 返回用于执行 umu-run 的解释器；解析失败时返回 error，
// 调用方（umuCommandLine / warmPrefix）据此 fail-fast，不做 shebang 回退。
func umuInterpreter() (pythonInfo, error)
```

### 3.2 `internal/runner/preflight_linux.go`

- `checkPython3()` 整体替换为调用 `pythonProblem()`：

```go
func checkPython3() *Problem { return pythonProblem() }
```

- Problem 文案改进（在 `python_linux.go` 里构造）：
  - `Name`: 未找到任何 python → `"python3"`；找到但都 < 3.10 → `"python3-version"`；
    显式配置无效 → `"python3-config"`。
  - `Detail`: 带上探测到的清单，明确「系统自带的 `python3` **不用动**」。
  - `Fix`:
    `Debian/Ubuntu: sudo add-apt-repository ppa:deadsnakes/ppa && sudo apt install python3.12  |  `
    `RHEL/Alma/Rocky: sudo dnf install python3.12  |  Arch: sudo pacman -S python  |  `
    `装好后可与系统 python3 共存，无需替换；也可在 config.yaml 里 linux.umu_python_bin 显式指定`

### 3.3 `internal/runner/runner_linux.go` + `umu_linux.go`

在 `umuCommandLine()`（`umu_linux.go:123`）里把解释器包进去：

```go
func umuCommandLine(exePath string, args []string, opt Options) (bin string, launchArgs []string, env []string, err error) {
    if err := checkRuntime(); err != nil { return "", nil, nil, err }

    py, err := umuInterpreter()          // 新增：解析并固定解释器
    if err != nil {
        return "", nil, nil, err          // 硬错误，文案面向终端用户
    }
    cfg := getConfig()

    umuRun := umuRunPath(cfg)
    bin = py.Path
    launchArgs = append([]string{umuRun, exePath}, args...)
    // …env 组装不变…
}
```

- `run()` / `runPTY()` 本身不用改（它们已经用 `umuCommandLine` 的返回值）。
- **`Handle.LauncherPID` 语义不变**：现在它是 `python <umu-run>` 的 PID，
  仍然是 `Setsid` 后的 pgid；`launcherIsDirect()` 依旧 `false`；
  `procx.QueryProcess` 按 cmdline 找真实游戏进程的逻辑不受影响
  （`isExpectedProcess` 的 Linux 分支查 cmdline，见 `CLAUDE.md` process 包说明）。
- `warmPrefix()`（`umu_linux.go:256`）：

```go
py, err := umuInterpreter()
if err != nil { return fmt.Errorf("failed to resolve a Python interpreter for umu-run: %w", err) }
cmd := exec.CommandContext(ctx, py.Path, umuRunPath(cfg), "wineboot", "--init")
```

- 降权执行：系统解释器（`/usr/bin/python3.*`，`0755`）降权用户可执行，无影响。
  但用户若把 `linux.umu_python_bin` 指到某个 HOME 下的 venv/pyenv，降权用户可能读/执行不到 ——
  见 §4 风险表，`docs/LINUX_RUNTIME_PRIVILEGE_PLAN.md` §9 也补一条。

### 3.4 配置项 `linux.umu_python_bin`

| 文件 | 改动 |
|---|---|
| `internal/appconfig/config.go` | `LinuxConfig` 加 `UmuPythonBin string \`mapstructure:"umu_python_bin"\``；默认段留空 `""` |
| `internal/appconfig/config.go` | `v.SetDefault("linux.umu_python_bin", "")` |
| `internal/appconfig/validate.go` | `(*LinuxConfig).validate()` 里 `l.UmuPythonBin = strings.TrimSpace(l.UmuPythonBin)`；**不 stat / 不展开 `~`**（validate 跨平台跑），真实校验（存在、可执行、`~` 展开、版本 >=3.10）留给 runner 在 Linux 上做 |
| `internal/appconfig/template.go` | `linux:` 段加注释（见下方样例） |
| `internal/runner/runner.go` | `runner.Config` 加 `PythonBin string`；注释说明：留空=自动探测系统 `python3.x`；非空=只用这一个，支持绝对路径 / 裸名字 / venv / pyenv 的解释器路径 |
| `main.go:282` | `runner.Configure` 增加 `PythonBin: cfg.Linux.UmuPythonBin` |
| `internal/actions/setup.go:79` | 同步加 `PythonBin`（顺带对齐，其它 Runtime* 字段缺失是既有问题，不在本计划范围） |
| ~~`internal/gui/gui.go:435`~~ | **不改** —— GUI 仅 Windows（`//go:build windows`），Linux 专属字段在那里无意义 |

`template.go` 注释样例：

```yaml
  # umu-run（zipapp）用哪个 Python 解释器执行。
  #   留空  : 自动探测系统解释器，扫 python3 / python3.10 … python3.20，多个则取最高版本
  #           （不会动系统默认的 python3，也不会自动发现 venv/pyenv）
  #   非留空: 只用这一个，不再自动探测。可填：
  #           - 裸名字，如  python3.14        （走 PATH）
  #           - 绝对路径，如 /usr/bin/python3.14
  #           - venv 解释器，如 /opt/asa-venv/bin/python
  #           - pyenv 版本解释器，如 ~/.pyenv/versions/3.14.0/bin/python
  #             （用 versions/<x>/bin/python 真实路径，不要用 ~/.pyenv/shims/python）
  #   要求 Python >= 3.10；降权运行时该解释器需能被降权用户读取/执行。
  umu_python_bin: ""

### 3.5 `/api/system/preflight` 暴露已解析解释器（可选，建议做）

方便前端在「环境就绪」页显示实际用的是哪个 Python。

- `internal/runner/runner.go` 加：

```go
type RuntimePythonInfo struct {
    Resolved bool   `json:"resolved"`
    Path     string `json:"path"`
    Version  string `json:"version"`
    Source   string `json:"source"` // "config" | "auto" | ""
}
func RuntimePython() RuntimePythonInfo { return runtimePython() }
```

- `python_linux.go` 实现 `runtimePython()`；`runner_windows.go` 加桩 `func runtimePython() RuntimePythonInfo { return RuntimePythonInfo{Resolved: true} }`。
- `internal/webapi/systemapi/systemapi.go` 响应 `Data` 里加 `"umuPython": runner.RuntimePython()`。

---

## 4. 边界与风险

| # | 场景 | 处理 |
|---|---|---|
| 1 | `python3` 软链到 `python3.11`，同时 `python3.11` 也在 PATH | 按真实路径+版本去重，只保留一个 |
| 2 | 存在 `python3.13` 但它是坏的（import 失败 / 非 CPython） | 版本探测脚本执行失败 → 跳过该候选，不计入 |
| 3 | 只有 `python3` = 3.9，没有任何 3.10+ | 硬错误，Problem 文案列出「探测到 3.9」并给安装带版本号解释器的命令 |
| 4 | 用户 `linux.umu_python_bin` 填了个不存在/过低的 | 硬错误，不回退自动探测（尊重显式意图），信息带原值 |
| 4b | `linux.umu_python_bin` 指向 pyenv **shim**（`~/.pyenv/shims/python`） | 能跑但依赖 `PYENV_ROOT`/`PATH`，不稳。文案建议填 `versions/<x>/bin/python` 真实路径；不强制拦 |
| 5 | 降权用户无法执行选定的解释器：venv/pyenv 在某用户 HOME 下（`~/.pyenv`、`/home/foo/venv`），或非标准位置 + 权限 700 | preflight 探测以 root 跑会「通过」，实际降权启动失败。缓解：`verifyRuntimeAccess` 的 deep-probe 里，若已解析解释器，则以降权用户身份 `os.Stat` + 尝试 `python -c 'pass'`，失败给明确告警（点名「解释器在 HOME 下，降权用户读不到，请放到 `/opt` 或系统路径」）。二期可做 |
| 6 | umu 未来改用非 zipapp 形态 / 不再走 python | `umuInterpreter()` 是单一改动点；真出现再改。当前 umu 1.4.x 就是 zipapp |
| 7 | 之前靠 shebang 能跑的机器，现在因 `resolvePython` 判断偏差被硬拦 | 用 `linux.umu_python_bin: /usr/bin/python3` 显式回退；文案里提示这个逃生口 |
| 8 | `warmPrefix` 与首次 `run` 用了不同解释器 | 不会：都走同一个 `umuInterpreter()` + 同键缓存 |

---

## 5. 测试计划

### 5.1 单元测试 `internal/runner/python_linux_test.go`（`//go:build linux`）

- 用一个临时 `bin/` 目录塞若干**假 python 脚本**（`#!/bin/sh` 打印 `3 12` 之类），
  改 `PATH` 后验证：
  - 多版本时选最高：`python3.11` + `python3.14` → 选 3.14。
  - 版本相同优先带版本号名字。
  - 全部 < 3.10 → 返回 error，消息含探测清单。
  - 坏解释器（退出码非 0）被跳过。
  - `PythonBin` 显式指定命中 / 未命中（未命中报错且不回退）。
  - `PythonBin` 填**绝对路径**（模拟 venv：临时目录里放个假 `python` 脚本）→ 直接采用，
    不要求文件名像 `python3.x`；填带 `~` 的路径 → 正确展开。
  - `PythonBin` 非空时**完全不扫**候选名单（可用假脚本数量/调用计数断言）。
  - 去重：软链场景。
- 缓存：override key 变化触发重解析。

### 5.2 `go build` / `go vet`

- `GOOS=linux go build ./...`、`GOOS=windows go build ./...` 都要过
  （新增 `runtimePython` 的 windows 桩不能漏）。

### 5.3 真机冒烟（Linux，记录到 `docs/LINUX_DEPLOYMENT.md`）

1. 系统 `python3` = 3.9 + 并装 `python3.14`：`asa-server` 自检通过，
   `/api/system/preflight` 的 `umuPython.version == "3.14"`、`source == "auto"`。
2. `config.yaml` 填 `linux.umu_python_bin: python3.14`：`source == "config"`。
3. 启动一个实例：`ps` 里能看到 `python3.14 …/umu-run …/ArkAscendedServer.exe`，
   RCON 可连、`SaveWorld` 正常、`StopServer` 能干净结束进程树。
4. `linux.umu_python_bin` 指向一个 venv（`python -m venv /opt/asa-venv`，
   `linux.umu_python_bin: /opt/asa-venv/bin/python`）：能启动实例。
5. 降权模式（root 运行）下重复 3 与 4，确认降权用户能执行选定解释器；
   把 venv 放到 `/root/asa-venv` 验证会给出「HOME 下降权用户读不到」的告警。

---

## 6. 落地顺序（建议 PR 拆分）

1. **PR1**：`python_linux.go`（发现逻辑 + 缓存）+ 单测 + `preflight_linux.go` 接线。
   自检层面先支持多版本，行为对旧机器只增不减。
2. **PR2**：`umuCommandLine` / `warmPrefix` 改用 `umuInterpreter()`，
   真机冒烟；`Handle`/进程树语义回归确认。
3. **PR3**：`linux.umu_python_bin` 配置项全链路（含 venv/pyenv 路径形态 + `~` 展开）
   + `template.go` 注释 + `main.go`/`setup.go` 装配（**不含 gui.go**）。
4. **PR4（可选）**：`/api/system/preflight` 暴露 `umuPython` + 前端展示。

---

## 7. 需要同步更新的文档

- `docs/LINUX_COMPATIBILITY_PLAN.md` §4.2：把「python3 >= 3.10」改成
  「`python3` / `python3.10`…`python3.N` 任一满足即可，多版本取最高」。
- `docs/LINUX_DEPLOYMENT.md`：新增小节 ——「低版本系统如何并装带版本号的 Python」
  + `linux.umu_python_bin` 用法（系统解释器 / venv / pyenv 三种形态各给一个例子，
  并强调降权运行时解释器不要放在某个用户 HOME 下）。
- `docs/LINUX_RUNTIME_PRIVILEGE_PLAN.md` §9 风险表：补「降权用户需能读取/执行选定解释器；
  venv/pyenv 放 HOME 下会失败」。
- `CLAUDE.md` 的 `runner/` 描述：`preflight_linux.go` 五项自检里 python 一项
  改为「多版本探测 + 固定」，并提一句 `python_linux.go`。

---

# 附录 Y：文件路径对照（2026-09-29）

| 文档中的路径 | 实际路径（当前代码） |
|---|---|
| `internal/runner/runtimeuser_linux.go`（判定逻辑） | `pkg/sysuser/{sysuser.go,sysuser_linux.go}` |
| `internal/runner/sharedaccess_linux.go`（业务逻辑） | `pkg/shareacl/shareacl.go` |
| `internal/runner/python_linux.go`（业务逻辑） | `pkg/pyfinder/pyfinder.go` |
| 旧顶层包 `asaserver/` | 已整体迁入 `internal/` |

# 附录 Z：合并与同步记录（2026-09-29）

本文件由 `docs/UMU_RUNTIME_USER_PLAN.md`、`docs/ACL_PERMISSION_HARDENING_PLAN.md`、`docs/UMU_PYTHON_DISCOVERY_PLAN.md` 于 2026-09-29 逐字物理合并而成（方案甲）；「已知缺陷清单」同步自 `docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md` §2（只读审计，基线 `faf127c`）；三个源文件保持原样，未作删减或改写。
