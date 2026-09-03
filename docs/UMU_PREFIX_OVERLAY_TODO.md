# `prefix_mode: overlay` 待办与缺陷跟踪

> 这份文档是 **overlay 模式的活动清单**，与 `docs/UMU_PREFIX_OVERLAY_PLAN.md` 分工如下：
>
> - **方案文档**（PLAN）记「为什么这么设计」「实测观测到了什么」——只增不改，是档案。
> - **本文**记「还差什么」「已知哪里不对」——**会被反复勾掉和重写**，是工作台。
>
> 后续所有 overlay 相关的缺陷、验收、待定项都往这里加。改动落地后：勾掉条目，
> 把**结论**（尤其是真机观测）补进 PLAN 的对应小节，本文只留一行指向它。
>
> 现状：**已实施，核心验收通过**（两个 ArkApi 实例 / 两个独立 wineserver /
> 一个 Xvfb / 可写层 63.1 MiB，见 PLAN §13.6）。默认仍是 `shared`。

---

## 0. 怎么用这份文档

- 每条都带 **判据**：怎样才算做完，尽量写成一条能跑的命令或一个能看的数字。
  没有判据的条目等于没有条目 —— 这是这个项目反复吃过亏的地方
  （`XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` §10：判据要落在**能力**上，不是落在
  「某个东西存不存在」上）。
- 优先级只分三档：**P0 会出事** / **P1 会让人困惑或浪费** / **P2 想做**。
- 条目里的推断一律标注「推断」。**真机观测才写进 PLAN**，本文不承担档案职责。
- ⚠️ 写判据时注意：`start_initialization_successful` 等**状态**写在 BadgerDB 里、
  经 WS 推给前端（`server_start_initialization_successful`），**从不落日志**。
  拿它 grep `asaServer.log` 永远是空的 —— 要看状态时间线请用 WebUI 或 WS 事件，
  日志里能 grep 的是「已挂载」「已卸载」「无法使用 overlayfs」这类动作行。

---

## 1. P0 —— 会出事的

### 1.1 ☑ 降级路径（`cp -a` 播种）—— 2026-09-02 WSL2 实跑通过

制造的是**真实的**挂载失败，不是改代码：把 `{BaseDir}/umu-prefix-overlay`
bind 到一个 overlayfs 挂载点上，于是 upperdir 落在 overlay 上，内核按文档拒绝
（`EINVAL`，"filesystem on ... not supported as upperdir"）。全程只动挂载，
复制出来的 690 MiB 全部落在 `/tmp` 里的那个 overlay 中，卸载后 basedir 零残渣。

观测到的（20:24:57 起 `jibian-pve`）：

- 降级告警带上了可执行的原因：「overlayfs 拒绝了这个可写层位置（invalid argument）；
  …… 所在的文件系统可能不支持作为 upperdir —— xfs 需要 ftype=1，NFS 与
  「已经是 overlay 的目录」都不行」，紧接着「正在从 …/umu-prefix 复制 Wine 前缀到
  …/jibian-pve/merged」。
- 实例正常启动并到达初始化成功。
- `prefix status`：`已复制（overlayfs 未生效）、使用中`，占用 690.5 MiB
  （量的是 merged，不是空的 upper）；`du -sh merged` = 696M = 底层。
- `mount | grep -c "<key>/merged"` 为 0 —— 确认走的是复制而不是挂载。
- **`cp -a` 没有跟着软链跑**：`dosdevices/z: -> /` 仍是软链，`pfx -> .` 正确，
  `.lower-stamp` = `GE-Proton10-34`。
- 复原（卸掉 bind + 那个假 overlay）后，原来的两个可写层原封不动地回来，
  20:28:49 `jibian-pve` 恢复正常挂载启动。

### 1.2 ☑ `kill -9` 之后的僵尸挂载对账（`reconcileOverlays`）—— 2026-09-02 WSL2 通过

`reconcileOverlays` 只卸载「upper 已经不在、却还挂着」的挂载点。这个判据是**故意
保守**的：挂载跨重启存活是设计的一部分，见多不怪。但它从没被真机验证过，而它
误删的后果是把一个**正在跑的实例**的可写层卸掉。

**判据**：①两个实例跑着 → `kill -9 $(pidof asa-server)` → 重启 asa-server →
`mount | grep umu-prefix-overlay` 两条都还在，实例（被 SIGHUP 带走后）能重新启动；
②手工 `rm -rf <某个 key>/upper` 再重启 → 那一条被卸载并记日志，另一条不受影响。

2026-09-02 WSL2 两条都按预期：硬杀后挂载存活、重启不误卸（无「崩溃残留」），
删掉 upper 的那一条被卸载并记日志，另一条不受影响，随后重建可写层正常启动。

### 1.3 ☑ 底层失效 → 可写层重建（`.lower-stamp`）—— 2026-09-02 WSL2 通过

Proton 版本升级或重装 VC++ 之后，旧 upper 里可能留着基于旧底层的 copy-up。
`.lower-stamp` 不一致就清空 upper 重挂 —— 这条逻辑写了，没在真机跑过。

**判据**：把 stamp 改成别的值 → 启动实例 → ①日志里出现「基于旧的底层前缀
建立的……正在重建」②`upper` 被清空 ③实例正常起来 ④`.lower-stamp` 与底层一致。

2026-09-02 WSL2 通过（改的是可写层的 `.lower-stamp`，命中同一分支；改底层的
`.created-by-proton` 会额外触发 `lowerNeedsWork` → 底层整个重建，测这条不必付那个代价）。

### 1.4 ☑ `prepareSharedPrefixWrite` 卸载空闲层与「实例正在启动」的竞争（已加日志，不上锁）

现状（PLAN §13.1 第 3 行）：要动底层时先卸载所有**空闲**的可写层，只有被
wineserver 持有的才拒绝。**推断**：窗口只有毫秒级，且 verify 路径有
server-files 锁挡着实例启动，EnsureRuntime 只在底层真要变时才走到这里。

但这是推断，没有观测。真出问题的形态是：卸载完、还没写完底层，一个实例把它
重新挂上去 —— 于是底层在被引用期间被修改，症状随机且会落在**实例**身上。

**已做**（2026-09-02）：`prepareSharedPrefixWrite(op)` 现在接受一个操作名，
成功时打开一段有始有终的「修改窗口」并返回关闭它的闭包（`defer` 调用）：

```
共享 Wine 前缀 <lower> 的修改窗口已打开（asa-server verify 服务端启动验证）；……
共享 Wine 前缀的修改窗口已关闭（asa-server verify 服务端启动验证），持续 1m2.481s
```

三个调用点都带上了名字：`环境准备 EnsureRuntime`、`asa-server verify 服务端启动验证`、
`asa-server verify-arkapi 启动验证`。只在 `prefix_mode: overlay` 下打印 —— 别的模式下
没有东西把共享前缀当 lowerdir，这一对只会变成每次 API 启动的噪音。
配合原有的「已卸载 N 个空闲的可写层（……）」，事后可以拿某个实例的「已挂载」时刻
去卡这段区间。

**仍然不做的**：不上锁。真出问题时先看有没有实例的「已挂载」落在窗口内，
有观测再谈加锁 —— 一把横跨整个操作的锁比现有证据支撑的改动大得多。


---

## 2. P1 —— 会让人困惑或白白浪费的

### 2.1 ☑ 三个改完但没在真机跑过的修复（2026-09-02 WSL2 全部验过）

它们都是从真机输出反推出来的，当时改完之后**没有再回到真机验证**：

| # | 改动 | 判据 |
|---|---|---|
| a | ☑ `fixPfxSymlink` 改用 `os.SameFile` 比对（原来跟 umu 来回打架，每次启动都重建软链） | 2026-09-02 WSL2 通过：`jibian-pve` 停后再启，`upper` 条目数 71 → 71，`readlink merged/pfx` 仍是 `.` |
| b | ☑ `PrefixInfo.Current` + `gc` 用它当判据（回收换模式后的残留） | 2026-09-02 WSL2 通过：两个 `umu-prefix-*-pve` 标「旧模式残留，可回收」，`gc --apply` 回收 1.3 GiB，实例照常启动 |
| c | ☑ 版本备份目录 `umu-prefix.bak-*` 现在能被列出并删除（原来 glob 匹配不上，是条走不到的代码路径） | 2026-09-02 WSL2 通过：`umu-prefix.bak-test` 归属为「旧版本备份」，`gc --apply` 后消失 |

### 2.2 ☐ `chownTree` 跳过已正确条目：启动耗时有没有变化

改动动机有两个（PLAN §13.5 末）：避免在挂载期间对 lowerdir 做无谓元数据写，
以及省掉每次启动对一个 ~700 MiB 前缀的整棵写。**第二个动机的收益没测过。**

**判据**：`systemctl restart asa-server` 前后各记一次日志时间戳，比对
「服务起来」到「运行时对账完成」之间的耗时。**推断**是从秒级降到亚秒级，
但没数据。

### 2.3 ☐ 剩余的 §9 验收项

PLAN §9 的 11 项里，第 3、5 项已过（§13.6），下列还没跑：

- ☑ **2. 首启耗时应在秒级**（对照 per-instance 的约一分钟）。2026-09-02 WSL2 通过：
  新建实例 `ovtest` 第一次启动，20:03:03 开始初始化 → 20:03:15 ArkApi 已被拉起
  → 20:03:49 初始化成功。**建可写层没有可测量的成本**（12 秒里还含镜像同步与进程
  拉起），46 秒全花在 ArkApi/游戏自身初始化上 —— 与 `verify-arkapi` 在同一台机器上
  的 46s 监听耗时一致。对照 per-instance 的一次 `wineboot --init`（约一分钟）。
- ☑ **4. 不排队**：2026-09-02 WSL2 通过 —— 两个 ArkApi 实例先后启动（19:32 / 19:35），
  日志里没有任何「正在等待实例 X 初始化完成后再启动」，唯一一条是 8-31 shared 模式留下的。
  同一份快照顺带复核了 §12.8（`0:91`/`0:108` 两个挂载点对应
  `server-5b-c5a0`/`server-6c-1e3711` 两个 wineserver）、§12.8.1（两条 `pfx -> .`，
  两个 wineserver 的 `WINEPREFIX` 各指各的 `merged/pfx/`）与一个共用 Xvfb。
- ☑ **6. 停 A → B 完全不受影响；重启 A → 秒级复用已有挂载**（不重新 mount）。
  2026-09-02 WSL2 通过：停 `jibian-pve` 期间 `meijue-pve` 照常运行、A 的挂载点仍在
  （§3.3「停止不卸载」的收益）；重启 A 后 `merged` 的 dev/ino 仍是 `91 2129325`、
  「Wine 前缀已挂载」日志计数保持 7 —— 即复用了已有挂载，没有重新 mount。
- ☑ **7. 删除实例 → 先卸载再删干净**：2026-09-02 WSL2 通过（`ovtest`）——
  `mount | grep -c ovtest` 为 0，`umu-prefix-overlay/ovtest` 不存在，
  `prefix status` 里没有它。**但同一次操作暴露了 §2.6：镜像目录没被删。**
- ☐ **11. Windows 回归**：`prefix_mode` 三个值都不改变任何行为。
  单测已覆盖闸门短路与 `prefixDir`，人工路径没走过。
- ☑ **§12.7-13**：2026-09-02 WSL2 通过 —— 两个实例跑着时底层 `umu-prefix` 那行是
  「就绪」而非「使用中」，两个可写层各 63.2 MiB「已挂载、使用中」（与 PLAN §13.6
  的 63.1 MiB 对上）。
- ☑ **§12.7-15**：2026-09-02 WSL2 两半都过了。
  ①**实例运行中**执行 `verify` / `verify-arkapi`，被拒绝并点名
  `jibian-pve, meijue-pve` —— 注意拦下它的是更靠前的 `beginServerFilesUpdate`
  （server-files 锁），不是 overlay 的守卫。
  ②**实例已停、可写层还挂着**（§3.3 停止不卸载）时，server-files 锁放行，
  轮到 `PrepareSharedPrefixWrite`：20:22:01 日志「为修改共享 Wine 前缀，已卸载
  2 个空闲的可写层（jibian-pve、meijue-pve）」，`verify-arkapi` 随后正常跑完
  （48s 监听成功），`mount | grep -c umu-prefix-overlay` 归零。

### 2.4 ☐ 可写层会不会随时间长大

63.1 MiB 是**刚跑起来**的数字。copy-up 只增不减，长期跑下来会涨到多少没人知道，
而这是 overlay 相对 per-instance 的全部优势所在。

**判据**：同一批实例连续跑一周后再看一次 `prefix status`，与 63.1 MiB 对比。
如果涨到接近 690 MiB，这个模式就只剩「首启快」一个卖点了。

### 2.5 ☑ 未挂载的可写层被 `prefix status` 报成「0 B、未初始化」（已修，2026-09-02 复测通过）

2026-09-02 在 WSL2 上发现：宿主机重启后（挂载不跨 mount namespace 存活），
`prefix status` 把两个内容完好的可写层报成 `0 B` / `未初始化`，而它们的 `upper`
里各有 64 MiB。原因是 `overlayStatus` 把「没挂载」直接当成了「降级复制」，
于是去量空的挂载点；实际有三种形态，第三种（**没挂载，内容在 upper 里**）才是
重启后的常态。这一栏正是用来判断「这台机器上 overlay 有没有在省盘」的，
报错方向会让人得出相反的结论。

已改：占用默认量 `upper`，只有 `prefixInitialized(merged)` 为真（降级复制）时才量
`merged`；状态列补第三种「未挂载，下次启动时自动挂载」，并且不再对可写层打
「未初始化」。同时修了 `TestGCCandidates` 里过时的夹具（`Current` 引入后
「实例还在 ⇒ 不回收」已不成立）。

**判据**：停掉全部实例并重启宿主机（或 `umount` 掉可写层）后 `prefix status`，
两行应显示约 64 MiB + 「未挂载，下次启动时自动挂载」，启动实例后回到「已挂载」。
2026-09-02 复测通过：`verify-arkapi` 自动卸载空闲层之后，两行显示
63.2 MiB +「未挂载，下次启动时自动挂载」，启动后回到「已挂载、使用中」。

### 2.6 ☑ 删除/重命名实例不清理镜像目录（已修，2026-09-02 复测通过）

2026-09-02 删除测试实例 `ovtest` 时发现：`server-files-tmp-ovtest` 还在。
**与 overlay 无关，一直如此** —— 镜像目录只有 `ForceStopServer` 会清，正常
`StopServer` 是**故意保留**的（下次秒起），而 `deleteInstance` 只删了
`instances/<name>/` 与 Wine 前缀，从没碰过镜像。于是每删一个实例就留下一个
几百 MB 的真实文件拷贝 + 一堆指向已删实例目录的链接，且没有任何东西会报告或
回收它（`prefix gc` 只管 Wine 前缀）。`renameInstance` 同理，旧名字的镜像永远留着。

已改：`deleteInstance` / `renameInstance` 调用早就存在的 `mirror.CleanupInstanceMirror`
（只删链接不删目标，并先把插件数据抢救回实例目录）。两处都必须在**删除/改名实例
目录之前**调用 —— 抢救的目的地是 `instances/<name>/`，顺序反了会把那个目录重新建出来。

**判据**：新建实例 → 启动一次（生成镜像）→ 正常停止 → 删除 →
`ls -d {BaseDir}/server-files-tmp-<name>` 为空，且 `instances/<name>` 没有被重新创建；
重命名同理，旧名字的镜像目录消失、新名字下次启动重新同步出来。
2026-09-02 WSL2 复测通过（删除路径）。

---

## 3. P2 —— 想做但不急

### 3.1 ☐ §12.4 方案 2：verify 走临时 overlay

现状是**守卫**（方案 1）：有实例挂着就拒绝执行 `verify` / `verify-arkapi`。
危险已经消掉了，剩下的是**不便**（得先停实例）。

方案 2 需要一个保留的 prefix key，而实例名几乎不受限
（`ValidateInstanceName` 只挡 `..` 和路径分隔符），所以要么冒名字冲突的险、
要么再开一个目录命名空间。做之前先想清楚这一点。

### 3.2 ☐ `overlay` 要不要成为 Linux 默认（PLAN §11.1）

结论已经定：**先作为可选模式跑一段时间**。重开这个话题的前提是
§1、§2 的 P0/P1 条目基本清空，尤其是 1.1（降级路径）—— 因为一旦成为默认，
降级路径就会在各种没人预料到的机器上被触发。

### 3.3 ☐ 停止实例时要不要卸载（PLAN §11.2）

现状：**不卸载**，下次启动零成本。代价是长期挂着 N 个 overlay。
`prepareSharedPrefixWrite` 现在会自动卸载空闲层，已经削掉了这条的大部分痛点。
除非实测发现僵尸挂载难管，否则保持现状。

### 3.4 ☐ SELinux enforcing 机器上的行为

PLAN §12.6 列了这条前提，没测过。目标机是 AlmaLinux —— **SELinux 大概率是
enforcing**，而 overlay 挂载成功了，所以至少这台机器上没问题。
但没确认过 `getenforce` 的值，所以还不能说这条已经验证。

**判据**：`getenforce` 的输出；如果是 `Enforcing`，这一条就可以直接勾掉并记进
PLAN §12.6。

### 3.5 ☐ `prefix status` 的合计口径

现在「合计占用」是把所有行的 `SizeBytes` 直接相加，而可写层那几行是**独占**占用。
数字本身没错（底层只计一次），但一个不看脚注的人会以为它是「磁盘上一共占了这么多」。
考虑分成「底层 / 可写层增量 / 旧模式残留」三栏合计。

---

## 4. 已关闭（只留结论与去向）

| 项 | 结论 | 去向 |
|---|---|---|
| §2 的机制：dev/ino 决定 wineserver | ✅ 成立，真机对上了 `server-820-1fe958` | PLAN §12.8 |
| 🔴 `merged/pfx` 指向底层，会让方案静默退化 | ✅ 确认存在；umu 启动时会重写成 `.`，但窗口真实存在，`fixPfxSymlink` 保留 | PLAN §12.8.1、§13.6.1 |
| 两个 ArkApi 实例 + 两个独立 wineserver | ✅ 成立 | PLAN §13.6 |
| N 个独立 Wine 会话共用**一个** Xvfb | ✅ 成立，`XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` §9 风险 5 一并关闭 | PLAN §13.6.2 |
| 可写层实际占用 | ✅ 63.1 MiB/实例 vs 完整前缀 690 MiB | PLAN §0、§13.6 |
| `work/work` 挡住启动 | ✅ 已修：overlay 的三个目录都不进属主对账清单（挂载形态下） | PLAN §13.5 |
| `wineserverHoldsPrefix` 字符串前缀比较 | ✅ 已修为路径边界比较 | PLAN §12.2 |
