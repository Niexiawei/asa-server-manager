# `prefix_mode: overlay` —— 共享 prefix 底层 + 每实例独立 wineserver

> 状态：**规划中**，未实施。
> 前置阅读：`docs/UMU_PREFIX_PER_INSTANCE_PLAN.md`（两道闸的定位与实测记录），
> 尤其是它的 §2.2（ArkApi 撞 Wine 会话）与 §11.4（可用组合表）。
> 关联：`docs/LINUX_COMPATIBILITY_PLAN.md` §6 风险 6、`docs/ACL_PERMISSION_HARDENING_PLAN.md`。
>
> **§10 是 2026-09-01 的回填**：显示解析已改为「自管 Xvfb 优先」
> （`docs/ALWAYS_MANAGED_XVFB_DISPLAY_PLAN.md`）。本方案的设计不受影响，
> 但 §4 方案 B 多了一个独立的否决理由，§9 的验收项要顺手多看两个数字。

---

## 0. 一句话

`shared` 的本意是**省盘、省初始化时间**，但它顺带把 Wine 会话也共享了 —— 而
「共享会话」正是 ArkApi 多实例跑不起来的原因。本方案用 **overlayfs** 把这两件事拆开：
底层（只读）继续共用一份已经预热好的 prefix，每个实例只带一个自己的可写层，
于是 **prefix 目录的 inode 不同 → wineserver 各自独立 → 隔离性等同 `per-instance`，
而磁盘与初始化开销接近 `shared`**。

| 模式 | 磁盘 | 新实例首启 | wineserver | ArkApi 多实例 |
|---|---|---|---|---|
| `shared` | 一份 | 0 | **共用一个** | ❌ |
| `per-instance` | 每实例一份（数百 MB，待实测） | 一次 wineboot + VC++（实测 ≈1 分钟） | 各自独立 | ✅ |
| **`overlay`（本方案）** | **一份 + 每实例一个可写层（估计数十 MB）** | **一次 mount（毫秒级）** | **各自独立** | ✅（待验证） |

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
