# `prefix_mode: overlay` —— 共享 prefix 底层 + 每实例独立 wineserver

> 状态：**已实施并通过核心真机验收（2026-09-01）**。§9 的第 3、5 项与 §12.7 的
> 第 12 项已在目标机上跑过并回填（§13.6）。
>
> 📋 **还差什么、已知哪里不对，一律看 `docs/UMU_PREFIX_OVERLAY_TODO.md`。**
> 那份是会被反复勾掉重写的工作台；本文是只增不改的档案，记「为什么这么设计」
> 与「真机观测到了什么」。新缺陷加到 TODO，结论回填到本文。代码落点与设计的偏差、以及
> 实施过程中发现的三件本文没写的事，都在 §13。`prefix_mode` 默认仍是 `shared`
> —— 按 §11.1 的结论，overlay 先作为可选模式发布。
> 前置阅读：`docs/UMU_PREFIX_PER_INSTANCE_PLAN.md`（两道闸的定位与实测记录），
> 尤其是它的 §2.2（ArkApi 撞 Wine 会话）与 §11.4（可用组合表）。
> 关联：`docs/LINUX_COMPATIBILITY_PLAN.md` §6 风险 6、`docs/ACL_PERMISSION_HARDENING_PLAN.md`。
>
> **§10 是 2026-09-01 的回填**：显示解析已改为「自管 Xvfb 优先」
> （`docs/ALWAYS_MANAGED_XVFB_DISPLAY_PLAN.md`）。本方案的设计不受影响，
> 但 §4 方案 B 多了一个独立的否决理由，§9 的验收项要顺手多看两个数字。
>
> **§12 是 2026-09-01 动工前对着代码的复核**：设计仍然成立，但有六件事本文原来没写，
> 其中 §12.1 是**唯一一个会让整套方案静默退化成 `shared` 而表面看不出来**的失败模式，
> 必须并进 P0；§12.2 是复核时顺带查出的一个**既有 bug**，overlay 会正面踩到它。
> 开工前请先读 §12。

---

## 0. 一句话

`shared` 的本意是**省盘、省初始化时间**，但它顺带把 Wine 会话也共享了 —— 而
「共享会话」正是 ArkApi 多实例跑不起来的原因。本方案用 **overlayfs** 把这两件事拆开：
底层（只读）继续共用一份已经预热好的 prefix，每个实例只带一个自己的可写层，
于是 **prefix 目录的 inode 不同 → wineserver 各自独立 → 隔离性等同 `per-instance`，
而磁盘与初始化开销接近 `shared`**。

| 模式 | 磁盘 | 新实例首启 | wineserver | ArkApi 多实例 |
|---|---|---|---|---|
| `shared` | 一份（实测 **690.9 MiB**） | 0 | **共用一个** | ❌ |
| `per-instance` | 每实例一份（实测 **≈690 MiB**） | 一次 wineboot + VC++（实测 ≈1 分钟） | 各自独立 | ✅ |
| **`overlay`（本方案）** | **一份 + 每实例一个可写层（实测 63.1 MiB，约 1/11）** | **一次 mount（毫秒级）** | **各自独立** | ✅ **已实测** |

> 数字来自 2026-09-01 的真机（两个实例，均启用 ArkApi），见 §13.6。
> 两实例合计：overlay 690.9 + 2×63.1 = **817 MiB**，per-instance 690.9 + 2×690 = **2.07 GiB**。

如果验收通过，`overlay` 在三个维度上都不劣于 `shared`，**应当成为 Linux 默认**，
`shared` 退化为「overlayfs 不可用时的兼容选项」。

---

## 1. 为什么今天只有两个极端

`prefix_mode` 现在把两件本来独立的事绑在了一起：

| 想要的 | `shared` | `per-instance` |
|---|---|---|
| 省磁盘 | ✅ | ❌ |
| 省首启时间 | ✅ | ❌（约一分钟） |
| 独立 Wine 会话（ArkApi 多实例的前提） | ❌ | ✅ |

用户要的是**前两行来自 `shared`、第三行来自 `per-instance`**。这不是折中，
因为三者之间并没有真正的取舍关系——绑在一起纯粹是「一个目录 = 一个 wineserver」
这条 Wine 实现细节造成的。

---

## 2. 关键机制：Wine 凭什么决定用哪个 wineserver

Wine 的服务端 socket 目录是：

```
/tmp/.wine-<uid>/server-<dev>-<ino>/socket
```

其中 `<dev>` / `<ino>` 是对 **`WINEPREFIX` 目录做 `stat()`** 得到的设备号与 inode 号
（`dlls/ntdll/unix/server.c` 的 `init_server_dir`）。含义：

- **同一个 prefix 目录 → 同一个 dev/ino → 同一个 wineserver。** 这就是今天的耦合。
- 想要独立 wineserver，**不需要**不同的目录内容，只需要一个**不同的 inode**。

这条是本方案的全部立足点，实施前必须先在真机上确认（§7 P0）：

```bash
# 取运行时用户的 uid
id -u asa-umu-runtime

# 现有共享 prefix 的 dev/ino（十进制）
stat -c '%d %i' /opt/asa-server/basedir/umu-prefix

# 实际的 server 目录名（十六进制的 dev-ino）
ls /tmp/.wine-$(id -u asa-umu-runtime)/
```

`printf '%x-%x\n' <dev> <ino>` 应当与 `server-` 后面那串对上。
**对不上就说明机制理解有误，本方案作废**，改走 §4 的备选。

> 注意：umu 实际导出的 `WINEPREFIX` 是 `<prefix>/pfx/`（一个指回自身的软链，
> 见 `umu_linux.go` 的 `wineserverHoldsPrefix` 注释）。`stat` 跟随软链，
> 所以最终落在 prefix 目录本身上，结论不变——但 §7 P0 要把这一层也一并核对。

---

## 3. 方案：overlayfs

### 3.1 目录布局

```
{BaseDir}/
├── umu-prefix/                     # 底层：唯一一份，setup 预热，运行期只读
└── umu-prefix-overlay/
    └── <实例名>/
        ├── upper/                  # 该实例的私有可写层（copy-up 的文件落这里）
        ├── work/                   # overlayfs 要求的工作目录，必须与 upper 同一文件系统
        ├── merged/                 # 挂载点 = 该实例实际使用的 WINEPREFIX
        └── .lower-stamp            # 记录挂载时底层的 .created-by-proton，用于失效判断
```

挂载命令（概念上）：

```
mount -t overlay overlay \
  -o lowerdir={BaseDir}/umu-prefix,upperdir=…/upper,workdir=…/work \
  …/merged
```

`merged` 是一个 overlay 超级块上的新目录 → **dev/ino 与底层不同，也与其他实例不同**
→ 每个实例拿到自己的 wineserver。

### 3.2 为什么隔离性等同 `per-instance`

overlayfs 的写语义是 **copy-up**：任何对底层文件的写入都会先把该文件复制到 upper，
之后所有读写都只看 upper。因此：

- 实例之间**没有任何共享的可写状态**——注册表（`system.reg` / `user.reg`）在第一次
  写入时就各自私有化了。
- 底层在运行期是**只读**的，一个实例的崩溃、注册表损坏、`drive_c` 污染都影响不到别人。
- 唯一共享的是**从未被修改过的文件**，而那些按定义是只读内容。

也就是说：**运行期的隔离性与 `per-instance` 逐条等价，差别只在"初始内容是共享的"。**

而 `per-instance` 已经在真机上验证了「独立 wineserver + 等价内容」可以跑通两个
ArkApi 实例（`UMU_PREFIX_PER_INSTANCE_PLAN.md` §11.1 第 6 项）。本方案不改变
这两个条件中的任何一个，只改变**私有 prefix 是怎么被造出来的**——
从「跑一次 wineboot 现建」变成「在共享底层上挂一个可写层」。

> 因此本方案的**假设风险比看上去低**：需要验证的不是"独立 wineserver 能不能解决
> ArkApi 冲突"（已验证），而是"overlay 挂出来的 prefix 在 Wine/Proton/pressure-vessel
> 下是否与真实目录等效"。后者是个具体的、一次就能测完的问题。

### 3.3 生命周期

| 时机 | 动作 |
|---|---|
| `EnsureRuntime`（setup） | 照旧只预热底层 `umu-prefix`，包括 VC++ override。**overlay 模式下底层的价值更大了**：它是所有实例的共同起点 |
| `EnsurePrefix(key)`（实例启动前） | ① 底层就绪校验 ② 比对 `.lower-stamp` 与底层 `.created-by-proton`，不一致则**清空 upper/work**（底层换了 Proton 版本或重装了 VC++） ③ 建目录、chown 给运行时用户 ④ `mount -t overlay` ⑤ 写 `.lower-stamp` |
| 实例运行中 | 什么都不做。挂载保持 |
| 实例停止 | **不卸载**。留着能让下次启动是纯粹的零成本，且避免"停服瞬间还有残留进程持有挂载"这一类竞争 |
| asa-server 启动 | 对账：清理 upper 已被删除但挂载还在的僵尸挂载（崩溃残留） |
| 删除 / 重命名实例 | 先 `umount`，再删 `umu-prefix-overlay/<实例名>` |
| `prefix gc` | 认识 overlay 目录；**拒绝删除仍处于挂载状态的**，与现有的 wineserver 占用检查同源 |

---

## 4. 备选方案与否决理由

| # | 方案 | 能否拿到独立 wineserver | 否决理由 |
|---|---|---|---|
| A | **硬链接农场**（`cp -al` 底层到每实例目录） | ✅ 新目录 = 新 inode | 省盘效果比 overlay 还好，但**不安全**：硬链接下任何**原地写**（不是先写临时文件再 rename）会直接改到所有实例共享的那份数据。Wine 的注册表保存是 rename 安全的，但 `drive_c` 里其他文件由游戏与插件写，无法逐一保证。**跨实例静默损坏**的代价远高于省下的那点盘 |
| B | **每实例私有 `/tmp`**（`unshare -m` + tmpfs） | ✅ server 目录路径变了 | 这会造成**两个 wineserver 服务同一个 prefix 目录**——Wine 明确不支持：两边各持一份注册表内存镜像，退出时各自回写，后退出者覆盖先退出者。这恰好是 `UMU_PREFIX_PER_INSTANCE_PLAN.md` §3.2(c) 当初设想、后来被排除的那个损坏场景，不能主动把它造出来 |
| C | **bind mount** 底层到每实例路径 | ❌ | bind mount 保留原 inode 与 st_dev，`stat` 结果不变，拿不到独立 wineserver |
| D | **软链接** 每实例路径 → 底层 | ❌ | `stat` 跟随软链，同上 |
| E | **`cp -a` 种子**：per-instance 但用复制底层代替 wineboot | ✅ | **不省盘**（只省时间：几秒的 I/O 取代一分钟的 wineboot）。作为 overlay 不可用时的**降级路径**有价值（§6.3），但达不到用户要的"省磁盘" |

---

## 5. 落点设计

### 5.1 配置

`internal/appconfig`：`prefix_mode` 增加合法值 `overlay`（`validate.go:165` 的白名单）。

```yaml
linux:
  # shared       全部实例共用一个 Wine prefix 与一个 wineserver。省盘，但启动串行，
  #              且同时只能有一个 ArkApi 实例。
  # per-instance 每实例一个完整 prefix。完全隔离，代价是磁盘与约一分钟的首启。
  # overlay      共用底层 prefix + 每实例一个可写层（overlayfs）。隔离性同
  #              per-instance，磁盘与首启开销接近 shared。需要 root 与 overlayfs 支持。
  prefix_mode: shared
```

### 5.2 `runner` 侧

现有的三个判定点各自需要知道 `overlay` 属于哪一边：

| 函数 | overlay 下的行为 | 理由 |
|---|---|---|
| `SharesWinePrefix()` | **`false`** | 这是全部意义所在：不共享会话，因而既不需要启动闸门，也没有 ArkApi 冲突 |
| `PrefixKeyFor(instance)` | 返回实例名 | 与 per-instance 同 |
| `prefixDir(cfg, key)` | 返回 `…/umu-prefix-overlay/<key>/merged` | 挂载点才是 `WINEPREFIX` |
| `EnsurePrefix(ctx, key, w)` | 走 §3.3 的挂载流程而非 `warmPrefix` | |
| `RemoveInstancePrefix(name)` | 先 umount 再删 | |
| `PrefixStatus()` | 额外报告：挂载状态、upper 实际占用、底层 stamp 是否一致 | |

**`SharesWinePrefix()` 的语义要顺势收紧**：它现在的实现是
`PrefixMode != "per-instance"`（刻意反着写，让零值配置也拿到闸门）。加入第三个值后
这个写法会把 `overlay` 误判成共享，必须改成**白名单**：只有 `shared`（以及空值）
才返回 `true`。这一处改错的后果是 overlay 模式下白白串行 + 误报 ArkApi 冲突，
且不会有任何报错——`runner.TestSharesWinePrefix_*` 要把三个值都钉死。

新增平台文件 `internal/runner/overlay_linux.go`（Windows 无对应实现，
所有入口经既有的 `prefix_windows.go` no-op 短路）：

```go
// mountOverlay 为 key 挂上「共享底层 + 私有可写层」，返回挂载点。幂等：
// 已挂载则直接返回。
func mountOverlay(cfg Config, key string, logf func(string, ...any)) (string, error)

// unmountOverlay 卸载 key 的挂载点。未挂载时是 no-op。
func unmountOverlay(cfg Config, key string) error

// overlayMounted 判断挂载点当前是否是一个 overlay 挂载（读 /proc/self/mountinfo，
// 不 shell out）。
func overlayMounted(path string) bool

// reconcileOverlays 清理崩溃残留：upper 已不存在却仍挂着的挂载点。
// asa-server 启动时调一次。
func reconcileOverlays(cfg Config) error
```

挂载走 `syscall.Mount` 直接调用而不是 `exec.Command("mount", …)`：省一次外部依赖，
错误也更明确（`EINVAL` 通常意味着 upper/work 不同文件系统，`ENODEV` 意味着内核没有
overlay 模块——这两条要翻译成人能看懂的话）。

### 5.3 `instance` 侧

`startServerInternal` 不需要任何改动：它已经在调
`runner.PrefixKeyFor` / `EnsurePrefix` / `Options.PrefixKey`，overlay 的差异全部
封装在 `runner` 内部。这是 F2 那次把三处调用点统一到一个 key 上的直接收益。

`conflictingArkApiInstance` 也不用改——它的前置条件是 `runner.SharesWinePrefix()`，
overlay 下为 `false`，整段短路。

> ⚠️ 写这份方案时顺带核对出的真 bug（**已修**）：`conflictingArkApiInstance` 起初
> 没有看 `SharesWinePrefix()`，而 `startServerInternal` 对它是无条件调用的
> （只要 `arkAsaApiRunning`）。结果是 **`per-instance` 下第二个 ArkApi 实例会被误拦**，
> 而且报错还会建议用户去改一个他已经改好了的配置项。已加上模式判断，
> 并补了回归测试 `TestConflictingArkApiInstance_SilentUnderPerInstance`。
> overlay 模式因此也天然免疫（它的 `SharesWinePrefix()` 同样为假）。

### 5.4 preflight

`preflight_linux.go` 增加一项 `overlayfs`：

- 判据：`/proc/filesystems` 里有 `nodev\toverlay`，且 euid==0。
- **`Warning: true`（建议级，不阻断）**：只有配了 `prefix_mode: overlay` 才需要它，
  其他模式下缺它完全无所谓。这与 `acl` 那一项的定位一致
  （见 `ACL_PERMISSION_HARDENING_PLAN.md` §1 那次"缺 acl 让 setup 整个跑不起来"的教训）。

### 5.5 CLI

`asa-server prefix status` 增加两列：**挂载状态**、**upper 占用**。
底层单列一行，各实例只显示自己的增量——这正是这个模式的卖点，报告要能直接体现出来。

---

## 6. 需要处理的边界

### 6.1 底层变更导致的失效

底层被改动的场景：重跑 `setup`、Proton 版本升级（`reconcilePrefixVersion` 移开重建）、
补装 VC++。这些之后，已存在的 upper 里可能留着**基于旧底层 copy-up 的文件**，
与新底层混在一起就是未定义状态。

处理：`.lower-stamp` 记录挂载时底层的 `.created-by-proton`；`EnsurePrefix` 发现
不一致就**清空 upper/work 重新挂载**。upper 里没有用户数据（存档在
`instances/<name>/Save`，插件数据在镜像里），清空是安全的。

**同时**：`EnsureRuntime` 在动底层之前应当拒绝执行（或至少响亮告警），
如果此刻还有 overlay 实例挂着。改一个正在被 N 个挂载引用的 lowerdir 是
overlayfs 明确的未定义行为。

### 6.2 权限

upper/work/merged 都要归运行时用户（独占目录语义，走
`chownPathForRuntime`，不是 `PrepareSharedTree`）。底层已经是运行时用户所有。

注意 `writePrefixMarker` 那类"root 在 chown 之后写文件"的老坑
（`UMU_PREFIX_PER_INSTANCE_PLAN.md` §11.2 缺陷 2）：overlay 路径上凡是 root
往 upper 里写的东西，都要跟着 chown。

### 6.3 overlayfs 不可用时怎么办

可能原因：内核没编 overlay、upper 落在不支持的文件系统（NFS、部分网络盘）、
非 root 运行、SELinux/AppArmor 拦截。

三个选项：

| 选项 | 行为 | 评价 |
|---|---|---|
| a | 启动直接失败，提示改配置 | 太硬。一次内核升级就能让所有实例起不来 |
| **b** | **降级到 `per-instance` 语义并响亮告警** | **推荐**。结果是功能正确的，只是多占盘；服务器继续跑，运维第二天看日志再决定 |
| c | 降级到 `shared` | 不行。ArkApi 多实例会重新变成不可用，等于静默削功能 |

选 b。降级路径复用 §4 的方案 E（`cp -a` 底层做种子）而不是跑 wineboot：
既然底层已经预热好了，复制它比重新 wineboot 更快也更一致。

### 6.4 pressure-vessel / bwrap

umu 会把 `WINEPREFIX` bind 进容器。bind mount 一个 overlay 挂载点在容器内的
`stat` 结果与宿主一致，因此 wineserver 的选择逻辑在容器内外一致——
**但这一条必须实测**（§9 第 3 项），它是整个方案能否成立的第二个支点。

### 6.5 与 `asa-server perms fix` 的关系

`perms` 管的是 `server-files` / `instances` 这两棵共享树，与 prefix 无关。
overlay 目录属于独占目录，不进 `sharedSubtrees`，但要进 `rwSubtrees`
（`runtimeuser_linux.go:257`）——否则启动时的属主对账会漏掉它们。
现有那行 glob 是 `prefixDir(cfg,"") + "-*"`，**匹配不到新的
`umu-prefix-overlay/` 布局**，必须一起改。

---

## 7. 分步实施清单

| 步骤 | 内容 | 落点 |
|---|---|---|
| **P0** | **真机验证 §2 的机制**：`stat` 的 dev/ino 与 `/tmp/.wine-<uid>/server-*` 目录名对得上；再手工挂一个 overlay，确认它的 dev/ino 与底层不同。**不通过则整个方案作废** | 无代码 |
| **P1** | `SharesWinePrefix()` 从"非 per-instance"改成**白名单**（只有 `shared`/空值为真）+ 三值单测 | `internal/runner/runner_linux.go` |
| **P2** | `appconfig` 接受 `overlay`；配置注释与 `config.yaml` 模板 | `internal/appconfig/` |
| **P3** | `overlay_linux.go`：`mountOverlay`/`unmountOverlay`/`overlayMounted`/`reconcileOverlays` | `internal/runner/` |
| **P4** | `prefixDir`/`EnsurePrefix`/`RemoveInstancePrefix`/`PrefixStatus` 认识 overlay；`rwSubtrees` 的 glob 覆盖新布局 | `internal/runner/prefix_linux.go`、`runtimeuser_linux.go` |
| **P5** | `.lower-stamp` 失效判断；`EnsureRuntime` 在有挂载时拒绝动底层 | `internal/runner/` |
| **P6** | overlayfs 不可用时降级到 `cp -a` 种子（§6.3 方案 b） | `internal/runner/` |
| **P7** | preflight 的 `overlayfs` 建议项；`prefix status` 增列 | `internal/runner/preflight_linux.go`、`internal/actions/prefix.go` |
| **P8** | asa-server 启动时 `reconcileOverlays` | `main.go` / `webapi` 初始化 |
| **P9** | 文档：本文件转记录、`UMU_PREFIX_PER_INSTANCE_PLAN.md` §11.4 组合表补一行、`LINUX_DEPLOYMENT.md`、`CLAUDE.md` | `docs/`、`CLAUDE.md` |

**可单测（无需真机）**：`SharesWinePrefix` 的三值判定、`prefixDir` 在三种模式下的路径、
`.lower-stamp` 的失效判断、`/proc/self/mountinfo` 的解析、`prefix gc` 对挂载中目录的拒绝。
挂载本身要 root，只能真机验。

---

## 8. 风险

| # | 风险 | 影响 | 缓解 |
|---|---|---|---|
| 1 | §2 的机制理解有误（server 目录不是按 dev/ino 取的） | 方案不成立 | P0 先验证，一条 `stat` + 一条 `ls` 就能判 |
| 2 | pressure-vessel 里 overlay 的 `stat` 与宿主不一致 | 容器内又退回共用 wineserver，且**表面看不出来** | §9 第 3 项直接数 wineserver 个数，不看推理 |
| 3 | overlay 上跑 Wine 有未知行为（mmap、`O_DIRECT`、xattr） | 难排查的偶发问题 | 先当**可选模式**发布，默认仍 `shared`；跑够时间再考虑改默认 |
| 4 | 崩溃留下僵尸挂载，累积到卸载不掉 | 需要人工 `umount` | P8 的启动对账；`prefix status` 显示挂载状态 |
| 5 | 底层被改而 upper 未失效 | 未定义状态，症状随机 | `.lower-stamp` + `EnsureRuntime` 的拒绝执行 |
| 6 | upper 与 work 不在同一文件系统 | `mount` 报 `EINVAL` | 两者都放在 `umu-prefix-overlay/<实例>/` 下，天然同盘；错误信息要翻译 |
| 7 | 省盘效果不及预期（copy-up 比想象的多） | 卖点打折 | §9 第 5 项实测 upper 占用；数字不好看就在文档里如实写 |

---

## 9. 验收清单

1. **P0 机制验证**：`stat -c '%d %i' <prefix>` 的十六进制与
   `/tmp/.wine-<uid>/server-<dev>-<ino>` 对得上。
2. `prefix_mode: overlay`，起实例 A → `umu-prefix-overlay/A/merged` 被挂载，
   **首启耗时应在秒级**（对照 per-instance 的约一分钟）。
3. **A 运行中起 B（两者都开 ArkApi）→ 两个都正常在线，`pgrep -x wineserver` 是
   两个，且它们的 `WINEPREFIX` 分别指向各自的 `merged`。** 这是本方案的核心验收。
   顺手把 §10.4 的缺口一起补上：`pgrep -x Xvfb` 应当**只有一个**，两个实例的
   `DISPLAY`（`/proc/<pid>/environ`）应当**相同** —— 这就验证了"多个独立 Wine 会话
   共用一个 X 服务"这个至今没被真机测过的组合。
4. B 不排队（`SharesWinePrefix()` 为假 → 闸门短路），日志里**不出现**"正在等待实例 A"。
5. `du -sh` 对比：底层一份 vs 各实例 upper。**把真实数字填回 §0 的表**
   （现在写的是估计）。同时把 per-instance 单个 prefix 的占用一并测了，
   补上 `UMU_PREFIX_PER_INSTANCE_PLAN.md` §11.3 欠的那一项。
6. 停 A → B 完全不受影响；重启 A → 秒级复用已有挂载。
7. 删除实例 B → 先卸载再删干净，`prefix status` 里消失。
8. 底层失效：手工改底层的 `.created-by-proton` → 下次启动 upper 被清空重挂，实例正常起。
9. 崩溃恢复：`kill -9` asa-server 后重启 → `reconcileOverlays` 不误删活着的挂载。
10. 降级路径：临时把 `/proc/filesystems` 的 overlay 支持挡掉（或在无 overlay 的机器上）
    → 降级到 per-instance 语义 + 告警，实例仍能启动。
11. **Windows 回归**：`prefix_mode` 在 Windows 上无意义，三个值都不改变任何行为。

---

## 10. 显示解析改为「Xvfb 优先」之后（2026-09-01 回填）

`docs/ALWAYS_MANAGED_XVFB_DISPLAY_PLAN.md` 已落地：显示解析顺序改成
**点名的 > 自己管的 > 捡来的 > 扫出来的**，`planDisplay` 返回候选链，
只读挂载的 `/tmp/.X11-unix`（WSL）在 root 下会被 remount 成可写。

**结论：本方案的设计一条都不用改。** 但有四处交互值得记下来，其中第 2 条给 §4 的
方案 B 增加了一个独立的否决理由。

### 10.1 验收结果的可迁移性变好了（正面）

改序之前，在**有宿主显示的机器上**（开发机、WSL）跑 §9 的验收，实例拿到的是宿主的
`:0`；到了无头生产机上却是自管 Xvfb。同一份验收结论在两种显示拓扑下未必等价。
改序之后两边都走自管 Xvfb，§9 的第 3 项（两个 ArkApi 实例 + 两个 wineserver）
**在开发机上测出来的结果可以直接搬到生产**。

### 10.2 §4 方案 B（每实例私有 `/tmp`）现在有了第二个否决理由

原来的理由是 Wine 侧的：两个 wineserver 服务同一个 prefix 目录 = 注册表互相覆盖。
现在还有一条与 prefix 完全无关的：

> **X 的 socket 就在 `/tmp/.X11-unix` 下，而那个路径写死在 xtrans 里。**
> 给实例一个私有 `/tmp` 会把它与自管 Xvfb 的 socket 切断——ArkApi 实例当场
> 变成"没有显示"，也就是那个退出码 3、零输出的失败模式。

两个理由互相独立：就算将来有人解决了注册表覆盖问题，方案 B 仍然不成立。
一并记下，免得下次再评估到它时只想起一半。

### 10.3 并发启动与 Xvfb 单例：没有新问题

overlay 的核心收益之一是 `SharesWinePrefix()` 为假 ⇒ **启动不再串行**（§5.2）。
于是多个实例会**并发**走到 `acquireDisplay`。这一条已经是安全的，不需要额外设计：

- `ensureXvfb` 全程持 `xvfbMu`，`/tmp/.X11-unix` 的扶正与 remount 都在这把锁**之内**
  （`ensureX11SocketDir` 由 `ensureXvfb` 调用）；
- 后到的启动看到 `xvfbCurrent` 已就绪、握手能过，直接复用，不会起第二个 X 服务端。

落地时**顺手加一条并发单测**即可（N 个 goroutine 同时 `ensureXvfb`，断言只起一个）——
这是 §7 P3 之外的一个小项，不值得单列步骤。

### 10.4 唯一的新开放项：多个独立 Wine 会话共用**一个** X 服务

这不是改序引入的（自管 Xvfb 单例从 2026-08-31 就是如此），但 overlay 会**放大**它：
本方案的卖点正是"多个 ArkApi 实例同时跑"，而它们现在共用一个 Xvfb。

值得注意的是：`per-instance` 那次已验证的两 ArkApi 实例（`UMU_PREFIX_PER_INSTANCE_PLAN.md`
§11.1 第 6 项，2026-08-31 上午）跑在**旧的 `xvfb-run -a` 代码**上，
也就是**两个实例各有一个私有 Xvfb**。所以"N 个独立 Wine 会话 + 一个共享 X 服务"
这个组合**至今没有被真机验证过**。

风险不高——X 服务端本来就是为多客户端设计的，而这些会话之间没有共享的 Wine 状态
（这正是 overlay 与 `shared` 的区别）。但它是个事实缺口，不该默认它成立。
它与 `XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` §7.3 用例 6 / §9 风险 5 是同一件事，
**§9 的第 3 项顺手就能覆盖**：那一步本来就要数 wineserver 个数，同时确认
`pgrep -x Xvfb` **只有一个**、两个实例的 `DISPLAY` 相同即可。

> 万一它不成立（两个会话共用一个 X 服务出问题），退路是 XVFB 方案 §9 风险 5 写过的
> "每 prefix 一个 Xvfb"，键与 `PrefixKeyFor` 同源。overlay 模式下这条退路**天然可行**
> （每实例本来就有自己的 key），代价是每实例多一个 X 服务端进程。
> 但那会让 `SHARED_PREFIX_MULTI_ARKAPI_PLAN.md` §6.1 的前提 2 变成恒假 ——
> 两件事要一起决定。

---

## 11. 未决问题

1. **默认值。** 如果 §9 全绿，`overlay` 在磁盘、时间、隔离三个维度上都不劣于 `shared`，
   逻辑上应当成为 Linux 默认。但它依赖 root + overlayfs，而 `shared` 不依赖任何东西。
   建议：**先作为可选模式发布，跑一段时间再改默认**（风险 3）。
2. 停止实例时**要不要卸载**。§3.3 选了"不卸载"（下次启动零成本）。代价是长期挂着
   N 个 overlay。如果实测发现僵尸挂载难管，改成"停止即卸载"也可以，
   代价是每次启动多一次 mount（毫秒级，其实无所谓）。**倾向保持不卸载，实测后再定。**
3. 是否顺带给 `shared` 模式补一条提示：检测到 overlayfs 可用且实例数 >1 时，
   建议改用 `overlay`。有用，但要小心别变成每次启动都刷屏的噪音。
4. `linux.prefix_dir` 被显式指定时，overlay 的三个子目录放哪。
   倾向：底层仍用 `prefix_dir`，overlay 结构固定放 `{BaseDir}/umu-prefix-overlay/`，
   并在文档里写明——让一个配置项同时控制两个布局只会更难解释。

---

## 12. 动工前的代码复核（2026-09-01）——本文原来漏掉的六件事

§0–§11 的设计经复核仍然成立，下面六条都是**落点层面**的补充：五条是本文原来没写的
前提或坑，一条（§12.2）是复核时顺带查出来的既有 bug。

| # | 事项 | 性质 | 并入 |
|---|---|---|---|
| 12.1 | umu 的 `pfx` 软链是**绝对路径** | 🔴 会让方案静默退化成 `shared` | **P0** |
| 12.2 | `wineserverHoldsPrefix` 用字符串前缀比路径 | 🟠 既有 bug —— **已单独修掉** | ✅ 2026-09-01 |
| 12.3 | `dirSize(merged)` 把底层也算进去 | 🟡 status 会谎报省盘效果 | P4/P7 |
| 12.4 | 写底层的不止 `EnsureRuntime`，还有两条 `PrefixKey=""` 的 verify 路径 | 🟠 底层被写 = 未定义行为 | P5 |
| 12.5 | 挂载必须发生在**宿主 mount namespace** | 🟠 装错单元 = CLI 看不见挂载 | P8 + 文档 |
| 12.6 | 文件系统与 LSM 前提（xfs `ftype=1` / SELinux / 不能嵌套） | 🟡 决定降级路径触发频率 | P6/P7 |

### 12.1 🔴 `pfx` 那条软链是绝对路径，它可能把 `WINEPREFIX` 指回底层

§2 的注里写着「umu 实际导出的 `WINEPREFIX` 是 `<prefix>/pfx/`（一个指回自身的软链），
`stat` 跟随软链，所以最终落在 prefix 目录本身上，**结论不变**」。

**在 overlay 下，「prefix 目录本身」这句话有两个候选**：`merged` 和底层。而 umu 建这条
软链用的是 `pfx.symlink_to(Path(path).resolve(strict=True))` —— **一个解析过的绝对路径**。
底层是 setup 时预热出来的，它里面那条 `pfx` 因此写死指向 `{BaseDir}/umu-prefix`。
挂上 overlay 之后，`merged/pfx` 如果还是底层那条（尚未被 copy-up 覆写），那么：

```
WINEPREFIX=…/merged/pfx/  --readlink-->  {BaseDir}/umu-prefix  --stat-->  底层的 dev/ino
```

**所有实例又回到同一个 wineserver**，也就是本方案要消灭的那件事 —— 而且
`mount` 成功、目录看着对、日志里一个字的异常都没有。这是整套方案里唯一一个
**表面完全正常的失败模式**。

好消息是它大概率会自愈：umu 每次启动都会重跑 `setup_pfx()`，把这条软链按本次
传入的 `WINEPREFIX` 重新 `symlink_to`，于是它被 copy-up 成 `merged/pfx → …/merged`。
**但这句话是从 umu 源码读出来的推断，不是观测** —— 本仓库在这上面栽过一次
（`launchgate.go` 里那段「听起来合理的推断被抄进四个地方当事实用了三个月」），
所以它只能作为「预期」，不能作为前提。

**P0 因此多两条命令**（在挂载完、起第一个实例之后立刻跑）：

```bash
# 1) merged 与底层的 dev/ino 必须不同
stat -c '%d %i' {BaseDir}/umu-prefix {BaseDir}/umu-prefix-overlay/A/merged

# 2) 🔴 决定性的一条：merged/pfx 到底指向谁
readlink -f {BaseDir}/umu-prefix-overlay/A/merged/pfx
#    期望 …/umu-prefix-overlay/A/merged
#    若是 …/umu-prefix，方案在这一步就已经失效

# 3) 反过来验：wineserver 自己认的是哪个 dev/ino
ls -d /tmp/.wine-$(id -u asa-umu-runtime)/*/socket   # 带 socket 的才是活的
pgrep -ax wineserver
tr '\0' '\n' < /proc/$(pgrep -x wineserver | head -1)/environ | grep WINEPREFIX
```

第 3 条是**独立于推理的判据**：带 `socket` 的 `server-*` 目录数就是活着的 Wine 会话数。
两个实例跑着却只有一个，无论 §2 的机制解释得多好，方案都没成立。

> ⚠️ **不能只数目录**：wineserver 退出后 `server-<dev>-<ino>/` 目录常常留着
> （2026-09-01 的真机上一次就看到三个，见 §12.8），把目录数当会话数会得到一个
> 好看但假的结论。判据是 `socket` 文件在不在，或者直接数 `wineserver` 进程。

> 如果自愈不发生，补救是现成的且很小：`mountOverlay` 挂完之后，
> **主动把 `merged/pfx` 重写成指向 `merged` 自己**（`os.Remove` + `os.Symlink`，
> 落在 upper 里）。代价是一个 copy-up，收益是不再依赖 umu 的内部行为。
> 倾向：**不管 P0 结果如何都写上这一步**，并在注释里说明它防的是什么 ——
> 它是幂等的，而少了它的失败形式是静默的。

### 12.2 🟠 `wineserverHoldsPrefix` 是字符串前缀比较（既有 bug，✅ 已修）

> **2026-09-01 已修**，与 overlay 无关，先行单独落地。判据抽成 `prefix.go` 里的
> `wineprefixValueUnder(value, prefix)`（无平台约束，纯路径比较，可跨平台单测），
> 下表三行连同「反向」「尾斜杠」「空值」都钉进了 `prefix_test.go`。
> 本节保留原文，因为它解释的是**为什么**这个比较必须落在路径边界上。
> **仍然开着的是本节末尾那半条**：`prefixStatus()` 的 `filepath.Glob(shared + "-*")`
> 会把 `umu-prefix-overlay` 当成一个名为 overlay 的实例前缀 —— 那个目录今天还不存在，
> 所以留在 P4 与 `rwSubtrees` 的 glob 一起改。

原来的判据是：

```go
if strings.HasPrefix(strings.TrimRight(v, "/"), want) { return true }
```

`want` 是 prefix 路径，`v` 是某个 wineserver 的 `WINEPREFIX`。问题在于这是**字符串**
前缀而不是**路径**前缀：

| `want` | 活着的 `WINEPREFIX` | 现在的结果 | 应该 |
|---|---|---|---|
| `…/umu-prefix` | `…/umu-prefix-jibian/pfx/` | ✅ 命中 | ❌ 不该命中 |
| `…/umu-prefix-A` | `…/umu-prefix-AB/pfx/` | ✅ 命中 | ❌ 不该命中 |
| `…/umu-prefix` | `…/umu-prefix-overlay/A/merged/pfx/` | ✅ 命中 | ❌ 不该命中 |

也就是说**今天在 `per-instance` 模式下就已经错了**：只要任意一个实例在跑，
`prefix status` 里共享前缀那一行的 `InUse` 就是 `true`，`prefix gc` 也会因此
拒绝清理本来可以清理的东西。症状轻，所以一直没被发现。

到了 overlay 下它会变重：§6.1 打算用「底层是否被占用」来决定
**`EnsureRuntime` 要不要拒绝动底层**，而这个判据在 overlay 模式下**恒为真**
（每个实例的 merged 路径都以底层路径开头，这是 §3.1 的目录布局决定的）。
拿一个恒真的信号做守卫，等于把 setup 永久锁死。

修法是一行（已落地为 `wineprefixValueUnder`）：

```go
v := strings.TrimRight(value, "/")
want := strings.TrimRight(prefix, "/")
return v == want || strings.HasPrefix(v, want+"/")
```

四个调用点全部受益，其中两个是本来就在错的：`waitForWineserverDrain` 在
per-instance 实例跑着时预热共享前缀，会白等满 90 秒；`removeInstancePrefix`
会拒绝删除一个没人持有的前缀。**顺带**：`prefixStatus()` 里 `filepath.Glob(shared + "-*")`
同样是字符串拼接，它会把 `umu-prefix-overlay` 整个目录当成一个「名为 overlay 的
实例前缀」列出来 —— §6.5 提到过 `rwSubtrees` 那条 glob 要改，`prefixStatus` 这条
是同一个问题的第二处，别只改一处。

### 12.3 🟡 `dirSize(merged)` 量的是底层 + upper

`PrefixInfo.SizeBytes` 现在是 `dirSize(p)`，`p` 在 overlay 下是 `merged` ——
而 merged 里看得见的是合并视图，`WalkDir` 会把底层那几百 MB 一并算进去。
于是 `prefix status` 会报「每个实例数百 MB」，正好把本方案的卖点报成了反面。

`PrefixInfo` 需要区分两个数：**独占占用**（overlay 下是 `upper` 的实际占用，
其他模式下就是 prefix 本身）与**共享底层**（只在底层那一行报一次）。§5.5 说的
"增量" 就是前者，这里只是把它落到具体字段上：报告口径错了比没有报告更糟。

### 12.4 🟠 会写底层的不止 `EnsureRuntime`

§6.1 只点了 `EnsureRuntime`。但复核代码后：**`Options.PrefixKey` 全仓库只有
`instance.startServerInternal` 一处设值**，其余所有经 `runner.Run()` 的路径都用空值，
也就是**直接跑在底层 prefix 上**：

- `installer.VerifyServerInstallation()`（`asa-server verify`）
- `installer.VerifyArkApiInstallation()`（`asa-server verify-arkapi`）

这两条都会在底层里起一个真正的 wineserver、写注册表、写 `drive_c`。而它们恰恰是
**出问题时管理员最可能在实例还跑着的时候敲的两条命令**。overlayfs 对
「lowerdir 在被挂载期间被修改」的表述是明确的：未定义行为，且症状随机
（overlay 侧读到的可能是新内容、旧内容，或者一个不存在的 inode）。

因此 P5 的守卫要覆盖的是「**任何对底层的写**」而不只是 setup：

1. `EnsureRuntime` / `verify` / `verify-arkapi` 在动底层之前先问一句
   「现在有没有 overlay 挂在它上面」（`reconcileOverlays` 那套 mountinfo 解析
   顺手就能答），有就**拒绝并说明要先停哪些实例**；
2. 或者让这两条 verify 命令也走一个**临时 overlay**（挂 → 跑 → 卸 → 删 upper），
   这样它们既不碰底层，又比今天更干净。**倾向 2**：verify 的语义本来就是
   「不影响现场地验一次」，今天它污染底层其实一直是个隐患，只是 `shared` 模式下
   底层就是大家共用的那个，看不出来。

### 12.5 🟠 挂载必须在宿主 mount namespace 里

两个方向都要成立：

- **对外可见**：`asa-server prefix status|gc` 是**另一个进程**。asa-server 服务
  在自己的 mount namespace 里挂的东西，CLI 读 `/proc/self/mountinfo` 是看不见的，
  于是 status 报「未挂载」、gc 直接把正在用的 upper 删掉。
- **能被卸载/对账**：崩溃残留要能被下一次启动的 `reconcileOverlays` 看到。

现状是**满足的**：`internal/svcmgr/systemd_script_linux.go` 那份单元模板里没有
`PrivateTmp` / `PrivateMounts` / `ProtectSystem` / `ProtectHome` 中的任何一个，
服务跑在宿主 namespace 里。但这是个**沉默的前提**，必须写下来：

> ⚠️ 那份单元模板里**永远不要**加 `PrivateTmp=yes`、`PrivateMounts=yes`、
> `ProtectSystem=strict`、`ProtectHome=yes`。前两个会让 overlay 挂载对外不可见，
> 而 `PrivateTmp` 还会同时切断 `/tmp/.X11-unix`（自管 Xvfb 的 socket 就在那里，
> 见 §10.2 —— 这与 §4 方案 B 被否决的第二个理由是同一件事）。
> 那份模板本来就带着「DRIFT: 与 kardianos 上游逐字对拍」的维护说明，
> 这条禁令写在它旁边。

顺带两条落在同一处的事实：

- 挂载**跨越服务重启存活**（宿主 namespace 里的挂载不随进程走）。所以 §3.3 的
  「实例停止不卸载」在服务重启后依然成立，`reconcileOverlays` 面对的是
  「挂载还在、upper 还在、但实例已经不在了」这种正常局面，**不能见到就删**；
  判据只能是 §5.2 写的「upper 已不存在却仍挂着」。
- 挂载需要 `CAP_SYS_ADMIN`。asa-server 服务本身是 root（降权只发生在游戏进程树上，
  见 `UMU_RUNTIME_USER_PLAN.md`），所以这一条天然满足；但 `linux.umu_run_as_root`
  与它无关，别把两件事混在一起解释。

### 12.6 🟡 文件系统与 LSM 的前提

§6.3 只写了「overlayfs 不可用」这一个笼统的原因。落到 P6 要能判别的具体条件：

| 前提 | 不满足时 | 怎么判 |
|---|---|---|
| 内核有 overlay 模块 | `mount` 返回 `ENODEV` | `/proc/filesystems` 含 `nodev\toverlay` |
| upper 与 work 同一文件系统 | `EINVAL` | §3.1 的布局天然满足，不必检测 |
| upper 所在 fs 支持 xattr 与 `d_type` | `EINVAL`，或运行期怪异 | **xfs 必须 `ftype=1`**（`xfs_info` 看；老的 `mkfs.xfs` 默认是 0，RHEL7 时代格式化的盘常见）。ext4/btrfs 默认可用 |
| upper 不在 NFS / 另一层 overlay 上 | `EINVAL` | 容器里跑 asa-server 时 `{BaseDir}` 很可能已经在 overlay 上 —— 这不是假设场景，**降级路径要能干净地接住它** |
| SELinux 不拦 | 挂上了但访问被拒 | enforcing 下 overlay 需要 `context=`/正确标签。判据只能是**挂完真读一次**，不能只看 `mount` 的返回值 |

**这些都不改变 §6.3 选的方案 b（降级到 `cp -a` 种子 + 响亮告警）**，只是让降级触发得
有理有据、日志里说得出是哪一条不满足。preflight 那项（§5.4）也照这张表出提示。

另有一条**对我们有利**的 overlayfs 语义，值得写下来免得将来有人重新担心一遍：
**copy-up 保留原文件的属主与权限位**。底层是 `warmPrefix` 里 chown 给运行时用户的，
所以从底层 copy-up 上来的文件天然还是运行时用户所有 —— overlay 不会像
`writePrefixMarker` 那个老坑（§6.2）一样凭空造出 root 属主的文件。需要 chown 的
只有我们自己创建的 `upper` / `work` / `merged` 三个目录本身。

### 12.7 §9 验收清单的增补（汇总）

在 §9 现有 11 项之外加四条，都是上面几节的直接产物：

12. **`readlink -f merged/pfx` 指向 merged**（§12.1）；且带 `socket` 的
    `server-*` 目录数等于在跑的实例数（**数 socket，不数目录** —— 残留目录会留着，
    见 §12.8）。这两条比「数 wineserver 进程」更早、更直接地判死或判活整个方案，
    应当排在 §9 第 3 项**之前**做。
13. 两个实例在跑时，`prefix status` 里**底层那一行的 `InUse` 不因此变成 true**
    （§12.2 修完的回归）。
14. `prefix status` 报的每实例占用是 **upper 的占用**，不是 merged 的（§12.3）。
15. 有实例挂着 overlay 时执行 `asa-server verify` / `verify-arkapi` —— 按 §12.4
    选定的方案，要么被明确拒绝并说清停哪台，要么走临时 overlay 且**事后底层的
    `.created-by-proton` 与 mtime 都没变**。

### 12.8 P0 真机结果（2026-09-01，逐步回填）

| 检查 | 结果 |
|---|---|
| §2 的机制：`WINEPREFIX` 的 dev/ino 决定用哪个 wineserver | ✅ **成立**。共享 prefix `stat -c '%d %i'` = `2080 2091352`，换成十六进制是 `820` / `1fe958`，而 `/tmp/.wine-999/` 下确有 `server-820-1fe958` |
| §12.1 `merged` 与底层的 dev/ino 不同 | ✅ **成立**，见下面的实测 |
| §12.1 🔴 `merged/pfx` 指向谁 | 🔴 **指向底层 —— 预判的静默失败模式确实存在**，见下面的实测 |
| §12.6 upper 所在文件系统能否承载 overlay | ⏳ 仍需在目标机上测（实测是在 WSL2 的 ext4 上做的） |

顺带记两条现场事实：

- **`/tmp/.wine-<uid>/` 下会有多于当前会话数的目录。** 那台机器上一次就看到三个
  `server-820-*`：`1fe958` 已确认是共享 prefix，另两个（`201232`、`20648e`）的 inode
  **还没对回具体是哪个目录**——可能是 per-instance 前缀，也可能是已退出会话的残留，
  两者都会留下目录。三个都在 `dev=820`（major 8 / minor 32）这同一个文件系统上。
  结论与是哪一种无关：验收只能数 `socket` 或数 `wineserver` 进程，不能数目录 ——
  §12.1 与 §12.7 第 12 条已按此订正。
- **overlay 的 `st_dev` 主设备号恒为 0**（匿名块设备，内核的 `get_anon_bdev`）。
  于是 merged 的 `stat -c '%d'` 会是个两位数级别的小数字，它的 server 目录名形如
  `server-2f-…`，与底层的 `server-820-…` **一眼可分**。这让 §12.1 第 ① 条不用换算
  也能判读，是个免费的好判据。

#### 12.8.1 🔴 `pfx` 软链实测（2026-09-01，WSL2 内核 6.18 / ext4）

不需要等目标机：这条问的是 **overlayfs 与软链的语义**，任何一台有 overlay 的
Linux 都能给出答案。造一个与真 prefix 同形状的底层（`system.reg` +
`drive_c/windows/system32` + umu 那条 `pfx -> <绝对路径>` 软链），挂上 overlay，
然后 `stat -L`（跟随软链，与 Wine 调的 `stat(2)` 一致）：

```
lower                                    dev=2080 ino=44213  → server-820-acb5
merged                                   dev=106  ino=44359  → server-6a-ad47
merged/pfx   ← umu 导出的 WINEPREFIX     dev=2080 ino=44213  → server-820-acb5   ← 🔴
merged/pfx/  （带尾斜杠，umu 就是这么写的） dev=2080 ino=44213  → server-820-acb5   ← 🔴
```

**§12.1 的预判成立，而且比预判更糟：不是"可能"，是必然。** 挂载完全成功、目录内容
完全正确、日志一个字都没有，但 `WINEPREFIX` 解析出来的 dev/ino **与底层逐位相同** ——
所有实例会拿到同一个 `server-820-acb5`，也就是同一个 wineserver。整套方案在这一步
静默退化成 `shared`。

执行 `fixPfxSymlink`（`os.Remove` + `os.Symlink` 指向 merged 自己）之后：

```
merged/pfx                               dev=106  ino=44359  → server-6a-ad47   ✅
merged/pfx/                              dev=106  ino=44359  → server-6a-ad47   ✅
底层的 pfx                                仍是 /…/umu-prefix，没被动过           ✅
```

最后一行同样重要：重写发生在 upper 里（`ls -A upper` 只有 `pfx` 一项），底层那条
软链**没有被修改** —— 这正是 copy-up 该有的行为，也说明这个修正不会污染共享底层。

两个可写层同时挂着时 dev 各不相同（`0:106` 与 `0:108`），`server-6a-…` /
`server-6c-…`，与 §12.8 记的「overlay 的主设备号恒为 0」一致。

> **结论：`fixPfxSymlink` 不是保险，是承重件。** 本文 §12.1 原来写的是
> 「umu 大概率会自愈，所以这条是防御性的」。实测把因果关系倒过来了：**在 umu 跑起来
> 之前，WINEPREFIX 就已经指向底层了**。umu 会不会在启动时重写这条软链仍然未知，
> 但那已经不重要 —— 我们在挂载后立刻改，就不依赖它。

---

## 13. 实施记录（2026-09-01）

§7 的 P1–P9 全部落地。下面只记**与本文设计不一致的地方**和**本文没写、写代码时才
发现的事**；一致的部分不重复。

### 13.1 与设计不同的四处

| # | 本文原来写的 | 实际实现 | 为什么 |
|---|---|---|---|
| 1 | `SharesWinePrefix()` 改成**白名单**（只有 `shared`/空值为真） | 改成**问 `prefixDir` 本人**：`prefixDir(cfg,"a") == prefixDir(cfg,"b")` | 白名单和黑名单都要靠人记得同步。而这个函数问的本来就是「两个实例会不会落到同一个目录」，那正是 `prefixDir` 的定义。派生出来就不可能漂移，而且**失败方向是安全的那一侧**：未知模式在 `prefixDir` 里回落到共享前缀，于是这里返回 true，多排一次队；反过来漏判会换来三分钟静默挂死 |
| 2 | 降级路径（§6.3 方案 b）是「降级到 per-instance 语义」 | 降级后**占用同一个 `merged` 路径**，只是从挂载点变成一个真目录 | 路径不变意味着 `prefixDir`、`runner.Run`、`wineserverHoldsPrefix`、`prefix status` 一个都不用知道这次拿到的是哪一种。`overlayMounted()` 是唯一需要区分的地方，而它只服务于报告与卸载 |
| 3 | §6.1「`EnsureRuntime` 在有挂载时拒绝执行」 | 两处收紧：①只在「底层确实还有事要做」时才可能拒绝（`lowerNeedsWork`）；②真要动底层时**先卸载所有空闲的可写层**，只有被 wineserver 持有的才构成拒绝（`prepareSharedPrefixWrite`） | ① 无条件拒绝会**炸掉每一次重启**：`EnsureRuntime` 每次 API 启动都后台跑一遍，而挂载是刻意跨重启存活的。② 更糟的是，光拒绝会**死锁**：挂载在实例停止后仍然留着（§3.3 有意为之），所以「停掉实例」并不能解除拒绝 —— 第一次装 VC++ 或升 Proton 就会被永久挡住，只能人工 `umount`。卸载空闲层不丢任何东西：upper 还在，下次启动重新挂；底层真变了的话 `.lower-stamp` 会让它重建，那本来就该发生 |
| 4 | §3.3「`prefix gc` 拒绝删除仍处于挂载状态的」 | 拒绝条件仍是 **wineserver 占用**，挂载本身不算 | 同上：挂载是**静息状态**，不是「正在用」。按挂载拒绝的话，一个实例被手工删掉后留下的孤儿层永远清不掉，只能人工 `umount`。`removeOverlayPrefix` 会先查 wineserver、再卸载、再删 |

§12.4 的两条 verify 路径按**方案 1（守卫）**而不是倾向的方案 2（临时 overlay）落地：
`runner.PrepareSharedPrefixWrite()` 在 `EnsureRuntime` / `verify` / `verify-arkapi`
三处先卸空闲层、再拦下真正在跑的那些，并报出是哪几个实例。方案 2 需要一个保留的 prefix key，而实例名
几乎不受限（`ValidateInstanceName` 只挡 `..` 和路径分隔符），要么冒名字冲突的险、要么
再开一个目录命名空间；而方案 1 已经把**危险**（写被引用的 lowerdir）完全消掉了，方案 2
多消的只是**不便**（得先停实例）。方案 2 仍然值得做，留作开放项。

### 13.2 本文没写、实现时才发现的四件事

**a) 整个 overlay 层都不该进属主对账清单（挂载形态下）。**
§6.5 只说了那条 glob 匹配不到新布局、要一起改。真正的坑在改法上：
`reconcileRuntimeOwnership` 对每个 rwSubtree 做 `chownTree`，而
**chown 是元数据写，元数据写会触发 copy-up** —— 走一遍挂载着的 `merged`
就等于把整个共享底层复制进那个实例的私有层，每次启动对账一次，每个实例一份。
这会把本模式唯一的卖点原地抹掉，而且不报任何错。
**初版据此只登记了 `upper` 与 `work` —— 那是错的，而且第一次上真机就被挡住了**，
详见 §13.5。最终 `overlayRWSubtrees` 只登记**没挂载的** `merged`（降级复制形态，
里面是真文件），挂载形态下一个目录都不登记：

| 目录 | 为什么不登记 |
|---|---|
| `merged`（已挂载） | chown 会 copy-up，等于把整个底层复制进私有层 |
| `upper` | 挂载期间从旁边直接改 upper 是 overlayfs 明确不支持的；而且它不需要 —— copy-up 保留底层属主（本来就是运行时用户的），新文件由游戏进程自己创建 |
| `work` | 内核的私有暂存区，它在里面建的 `work/work` 是 **root 所有、mode 000**，userspace 不该碰其中任何东西 |

**b) `prefixStatus` 与 `rwSubtrees` 的那条 glob 会把 `umu-prefix-overlay` 整个目录
当成一个「名为 overlay 的实例前缀」。** §12.2 末尾提过半句，落地时确认两处都要按
**精确路径**排除 —— 不排除的话 `prefix status` 会多出一行假前缀，而 `prefix gc`
会把它当孤儿，`--apply` 一下就是所有实例的可写层。

**c) 降级形态的占用要量 `merged` 而不是 `upper`。** §12.3 只说了「量 merged 会把
共享底层按实例数重复计」，那是**挂载**形态的结论。降级复制形态下 `upper` 是空的，
而 `merged` 里躺着真的一整份拷贝 —— 继续量 upper 会报出 `0 B`，正好在唯一能看出
「这台机器上 overlay 到底有没有在省盘」的那一栏上给出反向结论。

**d) 挂载参数里的逗号和冒号。** overlayfs 的 `-o lowerdir=A,upperdir=B,workdir=C`
按逗号切，冒号又是多层 lower 的分隔符，而**实例名会进这三个路径**
（`ValidateInstanceName` 不挡逗号冒号）。带这两个字符的路径拼进去不会报错，只会
**表示成别的意思**。`mountOptionsSafe` 先判一次，命中就走降级复制并说明原因。

### 13.3 落点清单

| 文件 | 内容 |
|---|---|
| `internal/runner/overlay.go` | 目录布局、`/proc/self/mountinfo` 解析、`overlayKeyFromMerged`、`mountOptionsSafe`（无平台约束，可跨平台单测） |
| `internal/runner/overlay_linux.go` | `ensureOverlayPrefix` / `mountOverlay` / `seedFromLower` / **`fixPfxSymlink`** / `unmountOverlay` / `reconcileOverlays` / `removeOverlayPrefix` / `lowerNeedsWork` / `prepareSharedPrefixWrite` |
| `internal/runner/umu_linux.go` | `prefixDir` 认识 overlay；`ensureRuntime` 在动底层前过守卫 |
| `internal/runner/runner_linux.go` | `sharesWinePrefix` 改为派生自 `prefixDir` |
| `internal/runner/prefix_linux.go` | `prefixKeyFor`/`ensurePrefix`/`removeInstancePrefix`/`prefixStatus` + `overlayStatus` |
| `internal/runner/prefix.go` | `PrefixInfo` 增 `Overlay`/`Mounted`，`SizeBytes` 语义改为**独占占用**；新增 `PrepareSharedPrefixWrite`、`ReconcilePrefixes` |
| `internal/runner/runtimeuser_linux.go` | `overlayRWSubtrees`（见 13.2a） |
| `internal/runner/preflight_linux.go` | `checkOverlayfs`（建议级，只在 `prefix_mode: overlay` 时出现） |
| `internal/installer/{installer,verify_arkapi}.go` | 两条 verify 路径过守卫 |
| `internal/webapi/actions.go` | 启动时 `runner.ReconcilePrefixes()` |
| `internal/appconfig/{config,validate,template}.go` | 接受 `overlay`，三种模式的说明 |
| 测试 | `overlay_test.go`（mountinfo 解析/键映射/挂载参数安全）、`prefix_linux_test.go`（`SharesWinePrefix` 五种取值、`prefixDir` 三模式、`prefix_dir` 只搬底层） |

### 13.4 还没做的

**清单已移到 `docs/UMU_PREFIX_OVERLAY_TODO.md`**，本节不再维护副本 —— 两份待办
互相抄的下场是它们会分叉，然后没人知道哪份是准的。

写这一节时它是「§9 的 11 项一项都没跑」；现在第 3、5 项已过（§13.6），
剩下的按 P0/P1/P2 分好了级。其中最要紧的一条**不是**验收项而是覆盖缺口：
**`seedFromLower` 降级路径一次都没被真正执行过**（目标机上 overlayfs 好使），
而一条从没跑过的错误处理路径等价于没有错误处理。

### 13.5 第一次真机启动就翻车：`work/work`（2026-09-01，已修）

部署后第一次启动实例，拿到的是：

```
Server 'meijue-pve' failed to start: 无法启动实例：降权运行时环境自检未通过：
  - [umu-runtime-owner-drift] …/umu-prefix-overlay/meijue-pve/work 下存在非
    asa-umu-runtime 拥有的条目（例：…/meijue-pve/work/work）
      修复：重启 asa-server 会自动 chown 修复；修不回来多半是 SELinux / 只读挂载 / NFS root_squash
```

挂载本身是成功的（`work/work` 存在就是证据 —— 它是内核在 `mount` 时建的）。
翻车的是 13.2a 那条改法：`overlayRWSubtrees` 把 `work` 登记成了「我们拥有、要保持
属主正确」的目录，于是 `verifyRuntimeAccess` 的属主漂移抽样走进去，看见内核自己的
`work/work`，判定漂移并**阻断启动**。而它给出的修复建议（"重启 asa-server 会自动
chown 修复"）**永远不可能生效**：那个目录不归我们管，chown 它既没意义、还可能干扰
overlayfs。本地复现确认：

```
$ ls -la <workdir>
d--------- 2 root root 4096  work        ← 内核建的，mode 000
```

修法：`work` 与 `upper` 都退出清单（理由见上表），并且**创建时也不再 chown `work`**。
回归测试 `TestOverlayRWSubtrees_NeverListsUpperOrWork` 直接造出 `work/work` 来钉这条。

顺带修掉一个更早就存在、被 overlay 放大的问题：`chownTree` 原来对每个条目
**无条件** `lchown`，即便属主已经正确 —— 那也是一次元数据写。这棵树在 overlay 模式下
就是若干可写层的 lowerdir，而挂载期间修改 lower 是未定义行为；何况每次启动对一个
上 GB 的前缀做整棵写本身就是浪费。现在先比对属主，已经对了就跳过。

> 教训与本文 §12 的基调一致：**"这个目录是我们的"是个需要检查的假设，不是默认。**
> `upper`/`work`/`merged` 三个目录是我们创建的，但只有 `upper` 的**内容**归我们，
> `work` 的内容归内核，`merged` 的内容归 overlayfs。

### 13.6 真机验收（2026-09-01，AlmaLinux，两个实例均启用 ArkApi）

修掉 §13.5 之后一次通过。**§9 的核心验收项（第 3 项）成立。**

| §9 验收项 | 结果 |
|---|---|
| 3. 两个 ArkApi 实例同时在线，`pgrep -x wineserver` 是两个 | ✅ `137320` / `137722` |
| 3.（顺带）`pgrep -x Xvfb` 只有一个 | ✅ `137214` —— 见下面 13.6.2 |
| 5. 可写层实际占用 | ✅ **63.1 MiB / 实例**，底层 690.9 MiB，per-instance 前缀 ≈690 MiB |
| 12.7-12. `merged/pfx` 指向 merged | ✅ 但形式与预期不同，见 13.6.1 |

```
前缀                    归属                    Proton          独占占用   状态
umu-prefix            共享（全部实例）          GE-Proton10-34  690.9 MiB  就绪
umu-prefix-jibian-pve 实例 jibian-pve         GE-Proton10-34  690.0 MiB  就绪
umu-prefix-meijue-pve 实例 meijue-pve         GE-Proton10-34  689.8 MiB  就绪
jibian-pve/merged     实例 jibian-pve · 可写层  GE-Proton10-34   63.1 MiB  已挂载、使用中
meijue-pve/merged     实例 meijue-pve · 可写层  GE-Proton10-34   63.1 MiB  已挂载、使用中
```

#### 13.6.1 umu **确实**会重写 `pfx`，但写的是 `.`

真机上 `readlink merged/pfx` 返回的是 **`.`**，不是 `fixPfxSymlink` 写进去的绝对路径。
也就是说 umu 在启动时把它重写了一遍，而且用的是**相对**形式（相对于软链所在目录
= merged，解析结果正确）。

两条结论：

1. §12.8.1 那个「静默退化」的窗口是**真实存在但短暂**的：从挂载到 umu 跑起来之间，
   `merged/pfx` 指向底层。`fixPfxSymlink` 现在的定位从"承重件"回落到"不依赖 umu 的
   内部行为"—— 但仍然保留，因为那个窗口内任何 `stat` 都会拿到错的答案，而且我们
   无法保证未来的 umu 版本还会重写它。
2. **顺手修掉一个自己造的缺陷**：`fixPfxSymlink` 原本比对的是"链接文本是否等于
   merged"，而 umu 写的是 `.` —— 于是每次启动都判定不符、删掉重建，跟 umu 来回打架，
   每次都在 upper 里制造一次无谓改动。判据改成 `os.SameFile`（解析后是不是同一个
   目录），那也正是 Wine 关心的语义。

#### 13.6.2 「N 个独立 Wine 会话共用一个 X 服务」—— 成立

这是 §10.4 记的唯一开放项，也是 `XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` §9 风险 5 /
§7.3 用例 6 的同一件事：`per-instance` 那次验证跑在旧的 `xvfb-run -a` 代码上，
两个实例各有一个私有 Xvfb，所以这个组合从没被真机测过。

现在测到了：**两个独立 wineserver + 一个 Xvfb，两个 ArkApi 实例都正常在线。**
§10.4 那条退路（每 prefix 一个 Xvfb）不需要了。

#### 13.6.3 报表暴露的两个既有缺陷（已修）

真机的 `prefix status` 里，两个 per-instance 时期留下的前缀（合计 **1.38 GiB**）
显示为「就绪」，而 `prefix gc` **不肯回收它们** —— 它的判据是"实例还存不存在"，
而实例当然还在。判据本身就错了：该问的是「**当前模式**还会不会用到这个目录」。

- `PrefixInfo.Current`（= `prefixDir(cfg, key) == path`）成为 gc 的新判据，
  `status` 里对应显示「旧模式残留，可回收」。定义直接派生自 `prefixDir`，
  与 `sharesWinePrefix` 同一个手法，不会随模式增加而漂移。
- 顺带查出**版本备份目录从来就没被列出来过**：`prefixStatus` 只 glob 了
  `<shared>-*`，而备份是 `<shared>.bak-<版本>`，两者匹配不上。于是
  `actions/prefix.go` 那段"`umu-prefix.bak-*` 同样归这里管"的注释描述的是一条
  **走不到的代码路径**，一个 Proton 版本升级留下的 ~700 MiB 就那么静静躺着。
  现在 glob 两个模式，Key 的剥前缀也一并修正（原来剥出来是 `.bak-X`，
  调用方的 `HasPrefix(Key, "bak-")` 永远不成立，即使能列出来也会去删一个
  不存在的路径然后报「完成」）。
