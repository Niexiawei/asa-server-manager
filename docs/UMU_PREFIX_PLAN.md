# Linux Wine prefix 模式、启动闸门与故障排查（合并文档）

> 本文由 `UMU_PREFIX_PLAN.md`、`UMU_PREFIX_PLAN.md`、`UMU_PREFIX_PLAN.md`、`UMU_PREFIX_PLAN.md` 于 2026-09-29 物理合并而成。
> Part 1 是两道闸与启动闸门的定案；Part 2 是 overlay（第三种 prefix 模式）设计；Part 3 是 overlay 的缺陷跟踪（活文档，结论回填 Part 2）；Part 4 是 prefix 初始化失败的排查与修复记录。
> ⚠️ 本文各「落点 / 分步实施清单」写于 `RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN` 重构之前（文档指向的 `internal/runner/overlay_linux.go`、`prefix_linux.go`、`prefix.go` 已不存在），实际路径见文末「附录 Y」。
> ⚠️ 本文存在已核实的已知缺陷（含一处 P0：无守卫写共享底层），见下方「已知缺陷清单」。

## 已知缺陷清单（2026-09-29 代码审计同步）

以下为 2026-09-29 只读审计结论（基线 faf127c），尚未修复。**P0：`verify-arkapi --install-vcredist` 无守卫写共享底层 lower**，必须先修。

### 3.2 发现

#### [P0] `verify-arkapi --install-vcredist` 无守卫地写共享底层 lower

> ✅ **已修复（2026-09-29，`36862d8`）**：写共享前缀的守卫下沉进 `umuruntime.Host.Provision` 本身（即这里「更彻底的做法」），`verify-arkapi --install-vcredist` 改走 `runner.Provision(..., runner.CapMSVCRT)`，`--check-only` 不写任何东西。见 `docs/UMU_RUNTIME_PLUGIN_PLAN.md` §5.3。

- **位置**：`internal/actions/verify_arkapi.go:53-60`（`runner.EnsurePrefixVCRedist(ctx, "", os.Stdout)`）；实现 `internal/runner/vcredist_linux.go:67-69`，落点 `:88`（`wineprefixMgrFor(cfg).Dir("")` 即共享底层）
- **触发条件**：`prefix_mode: overlay`，且存在实例的可写层正把 lower 当 `lowerdir`（实例运行中，或实例已停但挂载仍在——§3.3 有意不卸载）。执行 `asa-server verify-arkapi --install-vcredist`。**带 `--check-only` 时更糟**：`PrepareSharedPrefixWrite` 只在 `installer.VerifyArkApiInstallation`（`internal/installer/verify_arkapi.go:74`）里，`--check-only` 根本不会走到，这一步全程零守卫。
- **后果**：`Ensure` 向共享底层写 DLL override 注册表（`pkg/vcredist/install_linux.go:317-344` 的 `applyOverrides`），可能还写 `system32`。对正被 `lowerdir` 引用的目录写入是 overlayfs 明确的未定义行为，症状随机且落在**实例**身上。文档 `UMU_PREFIX_PLAN.md §12.4` 称守卫覆盖三处，**这是未覆盖的第四处**。
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

- **位置**：`internal/actions/prefix.go:170-174`（`if strings.HasPrefix(p.Key, "bak-") { os.RemoveAll(p.Path) }`）；`Key` 由 `pkg/wineprefix/wineprefix_linux.go:314-316` 从路径剥出
- **触发条件**：实例名本身以 `bak-` 开头（`ValidateInstanceName` 只挡 `..` 与路径分隔符）。其 per-instance 前缀 `umu-prefix-bak-xxx` 的 `Key` 被剥成 `bak-xxx`，于是 `prefix gc --apply` 把它当版本备份直接 `os.RemoveAll`。若该前缀在快照之后被启动/被 wineserver 占用，就删掉在用目录——**其它所有前缀都有 `RemoveInstancePrefix` 的 `WineserverHoldsPrefix` 二次确认，唯独它们没有**。
- **修复建议**：用结构性判据替代字符串推断——`wineprefix.Info` 增 `Backup bool`（在 `Status()` 里按 `<shared>.bak-` 前缀设置），`gc` 改 `if p.Backup { ... }`，删除一律经过会重新确认 wineserver 的入口。

#### [P2] `rwSubtrees`/`UnmountedOverlayDirs` 的 TOCTOU：可能对已挂载的 merged 做 chownTree

- **位置**：`internal/runner/runtimeuser_linux.go:160-194`（构建列表）+ `:103-110`（`su.ChownTree`）；快照来自 `pkg/wineprefix/wineprefix_linux.go:660-678`
- **触发条件**：列表生成后、chown 走到某层之前，实例启动并把该层挂上。
- **后果**：对挂载中的 merged 做元数据写会触发 copy-up，把整个共享 lower 复制进该实例私有层——本模式唯一卖点被抹掉。
- **修复建议**：chown 前对每个候选再复核 `overlayMounted`；更稳妥是 overlay 层不做全树 chown，只 chown 我们自己创建的目录。

#### [P2] `EnsureRuntime` 无条件调用 `PrepareSharedWrite`，无改动时也会卸载空闲层

- **位置**：`internal/runner/umu_linux.go:168-176`
- **触发条件**：每次 API 启动时的后台 `EnsureRuntime`（`internal/webapi/actions.go:501-505`）。
- **后果**：即使 `LowerNeedsWork()` 为假，`PrepareSharedWrite` 也先把空闲层全部 `umount` 才返回；与 §3.3「挂载跨重启存活」意图相悖。文档 §13.1 称「只在底层确实还有事要做时才可能拒绝」，实际是「先卸载再判断」。
- **修复建议**：`LowerNeedsWork()` 前移，为假时直接返回，不碰任何挂载。

#### [P2] 启动闸门可能被永久持有（`select` 无 ctx/超时兜底）

- **位置**：`internal/instance/server.go:708-712` 的 `select { case <-initFailed: case <-initSuccessful: }`（无 `ctx.Done()`/超时）；上游 `internal/instance/common.go:467-515` 的 `waitServerStartup` 也只由「进程退出」或日志行结束，**没有超时**。
- **触发条件**：`waitForGamePID` 成功后，`waitServerStartup` 既等不到日志行、进程也不退出。
- **后果**：`shared` 模式下闸门永不释放，后续所有实例启动在 `acquireLaunchGate` 上永久排队。文档 §8.3「闸门持有时间天然被封顶」不成立。
- **修复建议**：给该 `select` 加 `case <-ctx.Done(): startErr = ctx.Err(); return startErr` 与显式启动总超时（`defer releaseLaunchGate()` 已在 `:555` 兜底）。

#### [P2] overlay 残留层与在用 per-instance 前缀同名时，gc 会「部分删除后报失败」

- **位置**：`pkg/wineprefix/wineprefix_linux.go:254-280`（`Manager.Remove` 同时删「overlay 层 + per-instance 前缀」）；`internal/actions/prefix.go:199-211`
- **触发条件**：实例 A 在 `per-instance` 模式运行，盘上还留着同名 overlay 层 `umu-prefix-overlay/A/`。
- **后果**：`RemoveInstancePrefix("A")` 先删 overlay 层，再走到 per-instance 前缀被 `WineserverHoldsPrefix` 拒绝 → 该行报「失败」但 layer 已消失，「报失败却已删一半」。
- **修复建议**：`Manager.Remove` 按「当前形态」只删对应项，或把两半的成功/失败分别返回。

### 3.3 文档 vs 代码偏差

1. **文件落点整体失效**：`UMU_PREFIX_PLAN.md §7/§13.3` 指向的 `internal/runner/{overlay_linux.go,prefix_linux.go,prefix.go}` **在仓库中不存在**，实现位于 `pkg/wineprefix/`、`pkg/umu/`。
2. ✅（2026-09-29 已实现，见 §8.1）**`.lower-stamp` 的「感知 VC++ 补装」未实现**：`PLAN §3.3`/`§6.1` 与 `wineprefix.go:105-108` 都声称可检测「重装了 VC++」，实现只比 Proton 版本（见 §8.1）。
3. **写保护覆盖范围不足**：`PLAN §12.4`/`§13.1` 说守卫覆盖三处；`verify-arkapi --install-vcredist`（`internal/actions/verify_arkapi.go:56`）是未覆盖的第四处。
4. **`prefix gc` 判据**：`PLAN §3.3` 原写「拒绝删除仍处于挂载状态的」，实现改为只按 wineserver 占用（§13.1 已回填），但 `actions/prefix.go:170` 的 `bak-` 直接删除又额外绕过了这唯一确认，文档未记录。
5. **闸门持有上界**：`UMU_PREFIX_PLAN.md §8.3` 与 `waitServerStartup` 无超时的实现不符。
6. **「挂载跨重启存活」**：`§3.3`、`§11.2` 与 `overlayStatus`（「未挂载，下次启动时自动挂载」）都假定重挂载会复用 `upper`；但 `ensureOverlayPrefix` 在未挂载且 `merged` 为空的形态下会静默擦除 `upper`。

---

### 4.2 发现

> 注：本模块的 overlay 可写层生命周期、`.lower-stamp` 语义两条与 §3、§5 是同一根因，已合并到 §8 统一处理。以下仅列本模块独有项。

#### [P2] D4 未修复：`EnsureWorldReadable` 仍在下拉解压之前执行，且被 D2 的双次调用掩盖

- **位置**：`internal/runner/runtimeuser_linux.go:142-152`（`reconcileRuntimeOwnership` 内的 `fsutil.EnsureWorldReadable(proton/umuDir)`）→ 由 `ensureRuntimeUser`（`:94-101`）调用 → 由 `internal/runner/umu_linux.go:126` 调用，**早于** `EnsureUmu`(`:144`) / `EnsureGEProton`(`:147`)
- **触发条件**：全新安装且只跑一轮 `EnsureRuntime`（即 D2 若被修掉）。此时 `pathExists(proton)`/`pathExists(umuDir)` 为 false，补权限整体落空；随后 `WarmPrefix` 以降权用户执行 `wineboot`，需读取刚解压出来的 `proton/` 与 `umu-launcher/`。
- **后果**：若解压产物中存在非 world-readable/executable 的条目（`pkg/archive/archive.go:71` 直接采用 tar 头 mode），降权用户 EACCES，wineboot 失败——现在会（得益于 D1 修复）当场报错，但根因未除。当前未爆发纯粹因为 D2 让第二次 `EnsureRuntime` 在解压后补跑了一遍权限。
- **修复建议**：把 `EnsureWorldReadable` 移到 `EnsureUmu()`/`EnsureGEProton()` 之后、`WarmPrefix` 之前；或在二者成功后各自补一次。**与 D2 修复强耦合：两者必须同批修改，只删冗余调用会让 D4 立刻咬人。**

#### [P2] D3 未修复：无跨进程互斥，CLI setup 与服务并存的并发缺口依旧

- **位置**：`internal/runner/umu_linux.go:109`（仅进程内 `sync.Mutex runtimeMu`）；`internal/webapi/actions.go:501` 无条件 `go runner.EnsureRuntime`；`internal/actions/setup.go:112` 前台 `EnsureRuntime`
- **触发条件**：CLI `asa-server setup` 与 systemd 服务（或两个 CLI 进程）并存，二者同时 `WarmPrefix` 同一前缀。
- **后果**：两个进程并发对同一 WINEPREFIX 跑 wineboot / `reconcilePrefixVersion` 的 `os.Rename` / regedit，出现半成品前缀或前缀被搬走后另一进程按旧路径继续写。全仓库无任何 `flock`（`pkg/arkcache` 用的是另一套 `O_EXCL` 自旋锁，未覆盖 prefix）。
- **修复建议**：按文档 §7.5，在 `WarmPrefix` 前对 `{BaseDir}/.umu-prefix.lock`（或 per-prefix `{prefix}.lock`）做 `flock(LOCK_EX)`，进程崩溃由内核自动释放。

#### [P2] `EnsurePrefix` 快路径在读判据后、无锁地调用 `EnsureVCRedist`（TOCTOU）

- **位置**：`pkg/wineprefix/wineprefix_linux.go:203-220`
- **触发条件**：同一 key 的两次 `EnsurePrefix` 并发，且前缀已初始化、marker 匹配但 override 缺失。
- **后果**：`PrefixInitialized && PrefixMarker==版本` 的判定与 `EnsureVCRedist` 都在 `lockPrefix(prefix)`（`:200`）之前/之外，两个 goroutine 可同时跑 regedit 容器写同一 `user.reg`。
- **修复建议**：把判据读取与 `EnsureVCRedist` 一并纳入 `lockPrefix(prefix)` 临界区（快路径也持锁，代价仅一次 mutex）。

#### [P2] `reconcilePrefixVersion`：marker 缺失即整体搬走可用前缀；`writePrefixMarker` 失败会让可用前缀被上报为失败

- **位置**：`pkg/umu/umu_linux.go:484-505`（搬走逻辑）、`:511-518`（marker 写入）、`:238-240`/`:280`（调用点）
- **触发条件**：① 一个 `system.reg`/`drive_c` 完好、只是 `.created-by-proton` 缺失的前缀；② marker 写入失败（如 `chownPath` 失败）。
- **后果**：① 整个前缀被 `os.Rename` 到 `.bak-unknown` 并重建——共享前缀下会连带使所有 overlay 实例丢失 lower 基线，代价大且无必要；② `WarmPrefix` 在前缀实际可用时返回错误，`setup` 被误中止、实例被误判无法启动。
- **修复建议**：① 用前缀内 `system.reg` 记录的 Proton 版本作为"未知来源"兜底判据，仅确实无法判定时才搬走；② marker 写失败降级为 warn，不反向否决成功。

#### [P2] D0 白名单存在逃逸口：`Options.Env` 完全绕过白名单，`RuntimeEnv` 又不剥 D-Bus

- **位置**：`internal/runner/runner_linux.go:207-210`（`baseEnv = opt.Env` 时不走 `InheritedEnv`）；`pkg/umu/umu_linux.go:685-708`（`RuntimeEnv` 只剥 `XDG_*`/HOME/USER/LOGNAME）；`pkg/sysuser/sysuser_linux.go:462-482`
- **触发条件**：任何调用方给 `runner.Options.Env` 传入携带 `DBUS_SESSION_BUS_ADDRESS`/`SESSION_MANAGER`/`XAUTHORITY` 的环境（当前仓库内调用点均为 nil，属潜在漏洞）。
- **后果**：文档 §9 称「`runtimeEnv` 的 XDG 剥离是第二道防线，也仍覆盖调用方通过 `Options.Env` 显式传入的环境」——该保证对 D-Bus 类变量**不成立**，D0 原始故障可原样复现。
- **修复建议**：让 `RuntimeEnv` 复用 `launchEnvAllowed` 做白名单过滤，或对最终 env 统一过一次白名单；并修正 §9 表述。

#### [P2] 白名单用 `WINE*` 前缀通配，放行了 `WINEARCH`/`WINEPREFIX`/`WINEDLLOVERRIDES` 等宿主变量

- **位置**：`pkg/umu/umu_linux.go:674`（`strings.HasPrefix(key, "WINE")`）
- **触发条件**：宿主存在 `WINEARCH=win32`，或 `WINEPREFIX` 指向别处。
- **后果**：`wineboot --init` 会把 `WINEARCH` 带进去，可能建出 32 位前缀，而 `ArkAscendedServer.exe` 是 x64，后续静默失败；`WINEPREFIX` 虽被 `runEnv` 末尾覆盖（`:331`）侥幸无害，但白名单形状不该如此宽。
- **修复建议**：改成精确集合（`WINEDEBUG`/`WINEDLLOVERRIDES`/`WINEDLLPATH` 等），明确排除 `WINEARCH`、`WINEPREFIX`。

#### [P2] `arkcache` 跨进程锁的 stale 判定（30 分钟）短于大包下载（详见 §5）

- **位置**：`pkg/arkcache/arkcache.go:266-321`
- **说明**：与 §5 的「陈旧锁阈值」是同一处代码，详见 §5.2。

#### [P2] 实例名以 `bak-` 开头时被 `prefix gc --apply` 当作「版本备份」绕过 wineserver 检查（详见 §3）

- **位置**：`internal/actions/prefix.go:75-76`、`:170-173`
- **说明**：与 §3 的 `bak-` GC 问题同源，详见 §3.2。

#### [P2] D2 未修复：一次 setup 仍调用 `EnsureRuntime` 两次

- **位置**：`internal/actions/setup.go:112`（第一次）＋ `internal/actions/environment.go:57` → `internal/installer/installer.go:279`（第二次）
- **后果**：第二次除重复 `reconcileRuntimeOwnership` 的整树遍历外，还会重跑 `PrepareSharedWrite`（又一次全量挂载卸载），纯冗余；也正是它掩盖了 D4。
- **修复建议**：按 §7.6 去掉 `DownloadAndUpdateArkServer` 里的那次（改为断言 `CheckRuntime()`），与 D4 修复同批进行。

#### [P3] `WarmPrefix` 无超时，`RunInPrefix` 超时/取消只杀直接子进程

- **位置**：`pkg/umu/umu_linux.go:263`（`RunOptions{}` 零值＝无超时）；`:363-397`（`exec.CommandContext` 只 Kill python，不 Kill bwrap/wine 树）
- **触发条件**：wineboot 挂起；或 `vcredist` 的 15 分钟超时触发（`pkg/vcredist/install_linux.go:197-205`）。
- **后果**：setup 可能永久挂起；超时路径下被杀只是 `umu-run`（python），其下 bwrap/wineserver 成为孤儿并继续持有 prefix，随后 `WaitForWineserverDrain` 空等 90 秒，且孤儿残留导致后续 `PrepareSharedWrite` 误判「live」拒绝写。
- **修复建议**：给 `WarmPrefix` 的 wineboot 设硬上限；`RunInPrefix` 在 ctx 结束时对整棵进程树 `procx.KillTree`。

#### [P3] 死代码：`Runtime.runtimeUserNameHint` 定义后从未被调用

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
3. ✅（2026-09-29 已实现，见 §8.1）**`.lower-stamp` 的注释声称能检测「reinstalled VC++」** — 代码只写/比 Proton tag（见 §8.1）。
4. **overlay 层「跨重启存活、内容还在 upper」** — 与 `ensureOverlayPrefix` 对未挂载层的擦拭重建矛盾（见 §3、§8.3）。
5. **§4 的 D0 剥离清单位置与 D1 位置** — 重构后已失效，实际在 `pkg/sysuser/sysuser_linux.go:462-482` 与 `pkg/umu/umu_linux.go:226-281`。
6. **§3.3 的行号（`runner/umu_linux.go:285` 的 "ready"）** — 现为 `pkg/umu/umu_linux.go:279`。

---

### 8.1 `.lower-stamp` 只记录 Proton 版本，不感知 VC++ 补装（模块 3 / 4 / 5 三处独立命中）

> ✅ **已修复（2026-09-29，`2edd91c`）**：`.lower-stamp` 现为「Proton 标记;组件指纹」，组件指纹由各 `PrefixProvisioner.Fingerprint` 按插件名排序拼成（VC++ 插件报 override 是否齐、system32 是否原生、安装包校验值），经 `wineprefix.Config.ProvisionFingerprint` 钩子现读现算；底层后来补装 VC++ 时已有可写层重建。旧格式的层升级后一次性重建。回归用例 `TestOverlayLayerFollowsLowerProvisioning`。见 `docs/UMU_RUNTIME_PLUGIN_PLAN.md` §5.4。

- **位置**：注释 `pkg/wineprefix/wineprefix.go:105-108`；实现 `pkg/wineprefix/wineprefix_linux.go:423`（`want := umu.PrefixMarker(lower)`）、`:483`（写 stamp）；Proton 标记写入 `pkg/umu/umu_linux.go:511-518`（只按 `cfg.ProtonVersion`），`internal/runner/vcredist_linux.go`、`pkg/vcredist/install_linux.go` 全无对该文件的写入。
- **后果**：注释与文档 `PLAN §3.3`/`§6.1` 都声称可检出「reinstalled VC++」，实际检测能力为零。旧 `upper` 里已 copy-up 的 `system.reg`/`system32` 遮蔽新 lower，补装的 VC++ override 对已有实例不生效；ArkApi 起不来且无提示。
- **修复建议**：把 VC++ 有效状态纳入 stamp：`want := umu.PrefixMarker(lower) + "|" + vcredistLowerStamp(lower)`，其中指纹可用 `vcredist.OverridesApplied(lower)` + `user.reg` 摘要。补装成功后 fingerprint 变化 → 命中 `readOverlayStamp != want` → 按现有逻辑清理重建。

### 8.2 ArkApi 冲突检查在启动闸门**之前**，形成 check-then-act 窗口（模块 3 / 6 两处独立命中）

- **位置**：`internal/instance/server.go:467`（检查）vs `:548`（闸门）；判活用 `procpkg.IsServerRunning`（端口）。
- **修复**：见 §3.2 同名条目——判活改 `IsInstanceProcessAlive`，并把冲突检查移进闸门内 `runner.Run` 之前。

### 8.3 overlay `upper` 在宿主机重启后被静默擦除（模块 3 / 4 两处独立命中）

- **位置**：`pkg/wineprefix/wineprefix_linux.go:425-447`。
- **修复**：见 §3.2 同名条目——补 `hasLayer` 形态，stamp 一致时保留 `upper` 只重挂载。

### 8.4 `PrepareSharedWrite` 与实例启动竞态（模块 3 / 4 两处独立命中）

- **位置**：`pkg/wineprefix/wineprefix_linux.go:799-833` vs `:408-420`。
- **修复**：见 §3.2 同名条目——按 key 取同一把 `lockPrefix` 锁。

### 8.5 启动闸门可能被永久持有（模块 3 / 6 两处独立命中）

- **位置**：`internal/instance/server.go:708-712`（无 ctx/超时）；`launchgate.go` 无超时。
- **修复**：见 §3.2 / §6.2 同名条目——`select` 加 `ctx.Done()`，并给启动总流程加上限超时。

---

# Part 1 — Linux 多实例并发启动失败：两道闸（原 `UMU_PREFIX_PER_INSTANCE_PLAN.md`）

# Linux 多实例并发启动失败：两道闸（`PROTON_VERB` + ArkApi 撞 Wine 会话）

> 状态：**已完成**。根因两条，均已定位、修复并在真机验证通过
> （取证 2026-08-30，修复与验收 2026-08-31）。剩余未验项见 §11.3。
> 关联：`docs/LINUX_COMPATIBILITY_PLAN.md` §6 风险 6 与 P2 行的核对失误、
> `docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md`（本次证据顺带复验了它的修复）、
> `docs/ARKAPI_LINUX_VCREDIST_PLAN.md` §2.2 / 风险 7。
>
> **读这份文档的最短路径**：§0 结论速览 → §11.4 可用组合表。
> 中间各节按「取证 → 定位 → 修法 → 验收」的时间顺序保留了推理过程，
> 包括**两处被后续证据推翻的中途判断**（§5 开头的定位更正、§6 的两条证伪），
> 刻意不删——下一次遇到类似问题时，错误的路径与正确的结论同样有参考价值。

---

## 0. 结论速览

| # | 结论 | 判定 |
|---|---|---|
| 1 | **第一道闸：我们没设 `PROTON_VERB`，umu 默认用 `waitforexitandrun`，第二个实例卡在 `wineserver -w` 上等第一个实例退出，游戏进程从未被启动。** | ✅ **已确认**（§1 进程快照 + §2 日志） |
| 2 | 参考脚本的解法是 `export PROTON_VERB=run`（`ark_instance_manager.sh:884`，`start_server()` 第一行）+ 实例间 `sleep 30` 错开（L1197）。 | ✅ 已确认 |
| 3 | 我们丢掉这一行是一次**核对失误**：只对拍了启动那条 `env ...` 命令行，漏了函数开头的 `export`。`runner_linux.go:143-146` 的注释与 `LINUX_COMPATIBILITY_PLAN.md` P2 的"③"都因此写反了。 | ✅ 已确认 |
| 4 | **`PROTON_VERB` 只是第一道闸。** 补上它之后第二个实例不再排队，但换成卡在 `umu.exe` 之后、`AsaApiLoader.exe` 之前，依旧静默挂满 3 分钟。 | ✅ 已确认（§2.1） |
| 5 | **第二道闸是 ArkApi：共享 prefix = 共享 Wine 会话，而 Wine 的显示子系统每会话只初始化一次，第二个 `AsaApiLoader.exe` 因此起不来。** 关掉 ArkApi 后共享模式下多实例**正常可用**（2026-08-31 对照实验）。 | ✅ 已确认（§2.2） |
| 6 | 因此 **F1 与 F2 都是必需的**：F1（`PROTON_VERB=run`）修好共享模式下的多实例，F2（`per-instance`）是**同时用 ArkApi 跑多实例的唯一办法**。 | — |
| 7 | 脚本能共享运行的真正原因：**它完全不支持 ArkApi**，只跑 `ArkAscendedServer.exe`（全文件搜 `AsaApiLoader`/`ArkApi` 零命中）。它跑的正是「共享 + 无 ArkApi」这个可用组合。 | ✅ 已确认 |
| 8 | 原先怀疑的"进程组跨实例误伤"与"两个 wineserver 抢注册表"**均被证伪**（见 §6）。 | ❌ 排除 |
| 9 | 验证过程中另外发现并修掉两个缺陷：ArkApi 冲突缺少阻断（原为 3 分钟静默超时）、`.created-by-proton` 由 root 写入导致 per-instance 首次启动被属主自检拦下。 | ✅ 已修（§11.2） |

---

## 1. 真机证据（2026-08-30）

两个实例：`jibian-pve`（先启动，端口 7001）与 `meijue-pve`（后启动，端口 7003），
`prefix_mode: shared`，均启用 ArkApi（`AsaApiLoader.exe`）。

### 1.1 进程快照

| PID | PPID | PGID | SID | 角色 |
|---|---|---|---|---|
| 6981 | 6746 | 6981 | 6981 | **实例A** `umu-run`（= `launcher_pid`） |
| 6985 | 6981 | 6985 | 6985 | `srt-bwrap` ← setsid 断开 ① |
| 7036 | 6985 | 7036 | 7036 | `pv-adverb` ← 断开 ② |
| 7077 | 7036 | 7036 | 7036 | `python3 …/proton **waitforexitandrun** …` |
| 7086 | 7077 | 7036 | 7036 | `c:\windows\system32\umu.exe …` |
| 7088 | 7036 | 7088 | 7088 | **`wineserver`（服务端，A 拉起）** ← 断开 ③ |
| 7155 | 7036 | 7155 | 7155 | `AsaApiLoader.exe`（游戏进程）← 断开 ④ |
| 7174 | 7036 | 7155 | 7155 | `AsaApiLoader.exe` |
| 7287 | 6746 | 7287 | 7287 | **实例B** `umu-run` |
| 7291 | 7287 | 7291 | 7291 | `srt-bwrap` |
| 7331 | 7291 | 7331 | 7331 | `pv-adverb` |
| 7372 | 7331 | 7331 | 7331 | `python3 …/proton **waitforexitandrun** …` |
| 7375 | 7372 | 7331 | 7331 | **`wineserver -w` ← 实例B 停在这里** |

**实例 B 的进程链到 `wineserver -w` 就断了**：没有 `umu.exe`，没有 `AsaApiLoader.exe`。
游戏根本没被 exec 出来。

两个 wineserver 的环境变量都是 `WINEPREFIX=/opt/asa-server/basedir/umu-prefix/pfx/`
（umu 把 `WINEPREFIX` 重写成了 `<prefix>/pfx/`，与 `umu_linux.go:495` 的注释一致）。
7088 无参数 = 真正的服务端；7375 带 `-w` = 等待器。**是"一个 server + 一个排队者"，
不是"两个 server 抢注册表"**——顺带排除了原方案 §3.2(c) 设想的私有 `/tmp` 分支：
7375 能一直等下去，说明它找到了 7088 的 lock，容器间 `/tmp` 是共享的。

### 1.2 asa-server 自己的日志

```
21:04:18  Starting server for instance: meijue-pve
21:04:18  Instance mirror created successfully at .../server-files-tmp-meijue-pve
21:07:19  ERROR  failed to start server 'meijue-pve': 游戏进程在 3m0s 内没有出现
22:45:32  Starting server for instance: meijue-pve
22:48:34  ERROR  failed to start server 'meijue-pve': 游戏进程在 3m0s 内没有出现
```

两次启动、两次同样的超时。**不是崩溃、不是端口冲突、不是权限**——
`waitForGamePID` 在等一个永远不会出现的进程，因为 Proton 还没走到 exec 那一步。

---

## 2. 第一道闸：`waitforexitandrun` 是 umu 的默认动词

Proton 的启动动词只有两个与本问题相关：

| 动词 | 行为 |
|---|---|
| `run` | 直接启动目标 exe |
| `waitforexitandrun` | **先跑 `wineserver -w` 等该 prefix 的 wineserver 客户端清零**，再启动 exe |

`waitforexitandrun` 是 Steam 的用法：保证"上一次游戏完全退出后再启动新的"。
它的隐含前提是**一个 prefix 同时只跑一个游戏**。

umu-launcher 在未显式指定时默认取 `waitforexitandrun`。我们从不设 `PROTON_VERB`
（`internal/runner/runner_linux.go:179-186` 只设 `WINEPREFIX`/`GAMEID`/`PROTONPATH`/`UMU_RUNTIME_UPDATE`），
于是：

> **实例 A 在跑 → 它的 wineserver 有客户端 → 实例 B 的 `wineserver -w` 永远不返回 → 实例 B 永远起不来。**

这解释了用户观察到的"新实例把旧实例覆盖"：实际发生的是**新实例根本起不来**，
3 分钟后失败；期间 UI 上两个实例的状态互相打架，看起来就像后者顶掉了前者。

### 2.1 补上 `PROTON_VERB=run` 之后：换了个地方卡（2026-08-31 实测）

F1 生效，两个实例的 argv 都变成了 `proton run`，`wineserver -w` 彻底消失。
但第二个实例仍然超时，卡点前移到：

```
11911  proton run …AsaApiLoader.exe …meijue-pve
11912    └─ umu.exe  …AsaApiLoader.exe …meijue-pve     ← 到此为止，没有 AsaApiLoader.exe
```

`launcher.log` 的最后两行把交接点钉死了：

```
Proton: /opt/…/server-files-tmp-meijue-pve/…/AsaApiLoader.exe
Proton: Executable a unix path, launching with /unix option.
```

之后一个字都没有。对照实例 A 能跑通的那次，链条是
`proton → umu.exe → AsaApiLoader.exe(comm=GameThread)`。所以 B 的 Wine 起来了
（`umu.exe` 是 Windows 进程），但**加载器从未被 exec 出来**。

`waitForGamePID` 等满了整整 3 分钟才超时，而它同时监听 `launcherExited` ——
启动链没有崩，是真的挂住了。

### 2.2 第二道闸：ArkApi + 共享 Wine 会话（2026-08-31 对照实验确认）

**决定性实验**：保持 `prefix_mode: shared`，把两个实例的 `EnableAsaPlugin` 都关掉
→ **两个实例同时正常运行**。

于是三件事同时得到解释：

| 现象 | 解释 |
|---|---|
| 卡点恰好在 `umu.exe` 之后、`AsaApiLoader.exe` 之前 | 那是脚本从来不会走到的一步 |
| 关掉 ArkApi 就好了 | `ArkAscendedServer.exe` 根本不碰显示 |
| 参考脚本共享运行一直没事 | 它**完全不支持 ArkApi**，只跑 `ArkAscendedServer.exe` |

~~**机制**（推断，与全部观测一致，但未直接观测 winex11.drv 内部）：
一个 prefix = 一个 wineserver = **一个 Wine 会话**，而 Wine 的显示子系统
（`winex11.drv` / explorer 桌面）**每个会话只初始化一次**。第一个
`AsaApiLoader.exe` 起来时，会话已经绑定在它那个 X 显示上；第二个加载器带着
自己的 `DISPLAY` 加入同一个会话，
在创建窗口这一步静默挂住 —— 不报错、不退出、什么都不打。~~

> **2026-09-01 更正：上面这段机制已被实测否掉，划掉但保留原文。**
> 带 `WINEDEBUG=+x11drv,+win,+explorer` 复测，卡住那条链的 `launcher.log` 显示
> 它**每一句都说反了**：
>
> - 它不"绑定自己的显示"，而是**加入了先来那条链的 desktop**（窗口父级就是对方
>   explorer 建的桌面窗口 / Message 窗口，日志里没有 `started explorer`）；
> - 它的 x11drv **完整初始化了两次，零错误**；
> - 它没有"在创建窗口这一步挂住"——它一路走到 **Wine conhost 把控制台窗口建出来**
>   （`WineConsoleClass` + 对应 X 窗口），**建成功了**；
> - 它也不"静默"：41KB 日志且还在涨，只是没人接过它的 stderr 看。
>
> 真正的形状是：**控制台建好之后、exec 目标 exe 之前**，`umu.exe` 停在
> `futex_waitv` 不动了。**结论（闸真实存在）成立，但它在等谁至今未知**，
> 详见 `docs/SHARED_PREFIX_MULTI_ARKAPI_PLAN.md` §12。别再拿一个听起来合理的
> 解释把这个洞填上 —— 这份文档已经因此被挖开过一次了。

`AsaApiLoader.exe` 是本项目里**唯一**对 X 显示有硬性要求的东西
（`Options.NeedsDisplay` 只给它设），所以只有它会撞。

> 注：显示改成「每个 asa-server 进程一个自管 Xvfb、所有实例共用」之后，这条结论
> **不变**。~~（推断）~~ —— **2026-09-01 已复测，这句话现在是观测**：三轮、
> 两次对调先后顺序，全程 `DISPLAY=:0`，后起的那个每次都止步于 `umu.exe`、
> 每次都跑满 3 分钟被清。统一显示解决不了这个问题。
> 复测过程与全部采证见 `docs/SHARED_PREFIX_MULTI_ARKAPI_PLAN.md`
> （那份文档就是专门为了检验这条注而写的）。

**结论：`per-instance` 是「同时用 ArkApi 跑多实例」的唯一办法。**
这条以前被记在 §7 残余风险 2 里、标着"未知"，现在证实成立。

---

## 3. 参考脚本是怎么做的

```bash
# scripts/ark_instance_manager.sh:883
start_server() {
    export PROTON_VERB=run          # ← L884，函数第一行
    ...
    setsid nohup env \              # ← L992，启动命令行里看不到 PROTON_VERB，
        WINEPREFIX="$UMU_PREFIX_DIR" \  #    它是靠 export 继承进去的
        GAMEID="$UMU_GAMEID" \
        PROTONPATH="$UMU_PROTONPATH" \
        UMU_RUNTIME_UPDATE=0 \
        "$UMU_RUN_BIN" ... &
}
```

```bash
# scripts/ark_instance_manager.sh:1181 start_all_instances()
if start_server "$instance_name"; then
    echo "Waiting 30 seconds before starting the next instance..."
    sleep 30                        # ← L1197，错开首次启动
fi
```

**脚本确实是单 prefix、单 wineserver 跑多实例的**，靠的就是这两件事：

1. `PROTON_VERB=run` —— 取消排队。
2. 实例之间 30 秒错开 —— 避开并发首次触碰 prefix 的竞争。

脚本对 `PROTON_VERB=run` 没有写任何注释，这也是它容易被漏掉的原因之一。

---

## 4. 我们为什么丢了这一行

`LINUX_COMPATIBILITY_PLAN.md` 的 P2 验收记录里写着：

> ③`PROTON_VERB=run` 从设计阶段的示意代码中去掉——**参考脚本的实际调用从不设它**

`runner_linux.go:143-146` 的函数注释同样写着：

> matching `scripts/ark_instance_manager.sh`'s proven env var set exactly
> (notably: **no PROTON_VERB — the reference script doesn't set it**, and
> umu-run's default is already correct for running a game exe)

两处都错。核对时看的是 L992 那条 `setsid nohup env ...` 命令行——那里确实没有
`PROTON_VERB`，因为它在 130 行之前就 `export` 了。**逐字对拍了启动那一行，
漏了函数开头的 export。**

> 教训值得留在文档里：对拍 shell 脚本时，"这条命令行上有哪些变量"和
> "这个进程实际继承了哪些变量"是两回事。以后核对 env 应以**函数整体**
> 或直接读运行中进程的 `/proc/<pid>/environ` 为准。

---

## 5. 修法 F1：补上 `PROTON_VERB=run` — ✅ 已实施

> 定位更正：本节起初写作"主修法"。实测证明它**必要但不充分**——它拆掉的是第一道闸
> （排队），第二道闸（ArkApi 撞 Wine 会话，§2.2）要靠 F2 的 `per-instance`。
> 两者都要有：F1 让共享模式下的**纯 ARK** 多实例可用，F2 让 **ArkApi** 多实例可用。

### 5.1 落点

| # | 文件 | 改动 |
|---|---|---|
| 1 | `internal/runner/runner_linux.go` `umuCommandLine` | env 追加 `PROTON_VERB=run`；**同时订正 L143-146 的注释**（写明脚本在 `start_server()` 第一行 export，以及不设它会导致多实例排队） |
| 2 | `internal/runner/vcredist_linux.go` `runInPrefix` | 同样追加。它自建 env、不走 `umuCommandLine`，**有实例在跑时 `verify-arkapi` / `EnsurePrefixVCRedist` 会挂满 15 分钟超时** |
| 3 | `internal/runner/umu_linux.go` `warmPrefix` | **不加**。它是首次初始化，此时不该有别的实例在跑；万一有，`wineserver -w` 的等待反而是正确行为（不能对着活 prefix 跑 `wineboot --init`） |

追加位置放在 `WINEPREFIX`/`GAMEID` 之后、`cfg.WineDLLOverrides` 之前即可；
`launchEnvAllowed` 已放行 `PROTON_` 前缀，用户若显式设了环境变量，exec 取最后一次出现，
我们显式追加的这个会赢——这是有意的（想覆盖请走 `config.yaml`，不是环境变量）。

### 5.2 是否做成可配置

**不做。** 理由：`waitforexitandrun` 对本项目**没有任何正确用途**——我们从不"重启同一个 prefix 里的同一个游戏"，
实例之间的编排由状态机 CAS 与 §8 的启动闸门负责。
留一个开关只会制造"配错了就多实例全挂"的新坑。真要临时改，`PROTON_VERB` 环境变量本来就在白名单里。

### 5.3 验证

改完后 `ps` 里应看到 `…/proton **run** …`（而非 `waitforexitandrun`），
且第二个实例不再出现 `wineserver -w` 进程。

---

## 6. 已订正的两条判断

### 6.1 "进程组跨实例误伤" —— 证伪，且早已修复

本次快照证实了 `LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md` §2.2 的结论：
`srt-bwrap`(6985)、`pv-adverb`(7036)、`wineserver`(7088)、`AsaApiLoader.exe`(7155)
**各自 `pgid == sid == pid`**，即从 launcher 往下 setsid 了四次。
`umu-run` 的进程组 6981 里**只有它自己**。

所以 `kill(-6981)` 既不会误伤别的实例，也**根本杀不到自己的游戏**——
后者正是 2026-08-29 已修的问题（`pkg/procx/procx_linux.go:149` 现在走真正的
`/proc` ppid 进程树，进程组只作兜底）。本次数据是那次修复前提的再次确认，无需改动。

### 6.2 "两个 wineserver 抢同一份注册表" —— 排除

见 §1.1：是一个服务端 + 一个等待器，容器间 `/tmp` 共享。

---

## 7. 共享 wineserver 的残余风险（F1 之后仍在，风险 2 已证实并处理）

F1 让我们回到**与参考脚本完全一致**的状态。脚本在这个状态下经过验证，
但"经过验证"不等于"没有风险"，以下几条要如实记在案：

| # | 风险 | 说明 |
|---|---|---|
| 1 | 一个 wineserver 服务所有实例 | wineserver 崩溃 = 所有实例同时挂。单实例场景没有这个耦合 |
| 2 | **ArkApi 多实例不可行** —— ✅ **已证实**（原为"未知"，见 §2.2） | 共享会话下第二个 `AsaApiLoader.exe` 静默挂死。已在 `startServerInternal` 里做成**阻断**（`conflictingArkApiInstance`），当场报错并指出改 `per-instance`，不再让用户干等 3 分钟。纯 `ArkAscendedServer.exe` 多实例**不受影响**，共享模式下正常可用 |
| 3 | **并发启动的竞争** | 批量启动本来就是串行的（`manager.go:690`），但**单实例启动 API 会真并发**——`serverActionsLock` 已不存在。§8 已定案：`shared` 模式加启动闸门串行化，`per-instance` 保持并发 |
| 4 | `EnsureRuntime` / `verify-arkapi` 撞上运行中的实例 | F1 的第 2 项落点解决了"挂死"，但"在活着的 prefix 里改注册表"本身仍不干净 |

风险 1、2 只有 F2（每实例 prefix）能真正消除。

---

## 8. 并发启动的闸门（已定案）

**决策**：

| `prefix_mode` | 启动策略 |
|---|---|
| `shared`（默认） | **串行**。等上一个实例到达 `start_initialization_successful` 再放行下一个；**上一个失败也照样放行下一个**（不是"整批中止"） |
| `per-instance` | **并发**，与 Windows 行为一致，不加任何闸门 |

不抄脚本的 `sleep 30`：固定时长既可能不够（大地图初始化超过 30 s），
又可能白等（小地图 10 s 就好了）。**以状态为准，不以时长为准。**

### 8.1 现状核对：批量启动已经满足要求

| 要求 | 现状 | 结论 |
|---|---|---|
| 串行 | `batchmanage` 阶段二是严格串行 `for` 循环（`internal/batchmanage/manager.go:690`），逐个调 `instancepkg.StartServer` | ✅ 已满足 |
| 等到 `start_initialization_successful` | `startServerInternal` 阻塞在 `select { case <-initFailed; case <-initSuccessful }`（`internal/instance/server.go:635-641`），而 `initSuccessful` 正是写完 `StatusStartStartInitializationSuccessful` 之后发出的（`server.go:616-619`） | ✅ 已满足（`StartServer` 返回 == 初始化成功） |
| 失败继续下一个 | `executeInstance` 记 `InstanceFailed` 后循环进入下一轮，不中断整批（`manager.go:875`） | ✅ 已满足 |

**所以 `start-all` / 批量重启这条路不需要改。** 已有的 `DelayBetween`（可选的额外间隔）
保持不变，它是用户显式要求的额外缓冲，与本闸门不冲突。

### 8.2 缺口：单实例启动 API 会真并发

`CLAUDE.md` 写着"The API server uses a mutex (`serverActionsLock`) to prevent concurrent
start/stop operations"，但**代码里已经没有这个锁了**（全仓 grep 无命中）。
于是两次并发的 `POST /api/server/:name/start`（例如用户在 UI 上先点 A、不等它好就点 B）
会真的并发进入启动流程——**这正是本次故障的触发方式**。

`schedule`（定时任务）同理：两个定时任务撞在同一分钟也会并发拉起。

### 8.3 设计：共享 prefix 启动闸门

在 `internal/instance` 加一把**进程内启动闸门**，语义就是 §8 表格那两行：

```go
// runner 侧（instance 不该自己判断平台/模式）
// SharedLaunchGate 返回本次启动是否需要与其他实例串行。
// Windows 恒为 false（prefix 概念不存在，行为完全不变）。
// Linux 下仅当 prefix_mode == "shared" 时为 true。
func SharedLaunchGate() bool
```

`startServerInternal` 里：

- **加锁位置**：`runner.Run` 之前（`server.go:501`），与 §5 的 `PrepareSharedTree` 循环相邻。
- **解锁位置**：`select` 落定之后（`server.go:635-641`），**两个分支都解锁**
  ——`initFailed` 也必须放行，这是"失败也继续下一个"的落点。用 `defer` 覆盖早退路径。
- **等锁时要有反馈**：拿不到锁时先发一条 SSE/日志
  （`正在等待实例 X 初始化完成后再启动（prefix_mode=shared）`），
  否则用户看到的又是"点了启动没反应"——本次故障的体感就是这样来的。
- **等锁要可取消**：走 `ctx`，用户取消启动或超时能退出等待，不能死等。
- **上界**：闸门持有时间天然被 `waitForGamePID` 的 3 分钟 + 启动等待封顶，
  不需要单独设超时；但等锁方需要自己的超时（建议复用启动流程的整体超时预算）。

### 8.4 为什么不放在 `runner` 里

`runner.Run` 一返回就结束了（它只负责把进程拉起来），而闸门必须**持有到初始化成功**
——那是 `instance` 才知道的事实（`initSuccessful` 通道）。放 `runner` 里会退化成
"只串行化 exec 那一瞬间"，挡不住 Proton 的 prefix setup 阶段。

### 8.5 Windows 回归自由

`SharedLaunchGate()` 在 Windows 上恒 `false`，闸门代码整段短路，
`startServerInternal` 的时序与今天逐字相同。这条必须在 review 时逐句核对
（与 `LINUX_COMPATIBILITY_PLAN.md` 一贯的"Windows 行为优先"一致）。

---

## 9. F2（次）：把 `prefix_mode: per-instance` 接线 — ✅ 已实施

**与本次故障解耦，但仍是一个应当修的真 bug。以下 9.1 描述的是实施前的状态。**

### 9.1 它曾经是死代码（已修）

`prefixDir` 的按 key 分目录逻辑写好了（`internal/runner/umu_linux.go:39`）、
`Options.PrefixKey` 定义了（`runner.go:41`）、`umuCommandLine` 也用上了（`runner_linux.go:167`），
**唯独没有人传它**：

| 调用点 | 传的 key |
|---|---|
| `internal/instance/server.go:501` `runner.Run(...)` | `Options` 里**没有 `PrefixKey` 字段** |
| `internal/instance/server.go:420` `PrefixHasVCRedist("")` | `""` |
| `internal/runner/umu_linux.go:284` `warmPrefix` | `""` |
| `internal/runner/umu_linux.go:114` `ensureVCRedist(…, "", …)` | `""` |
| `internal/runner/runner_linux.go:136` `checkRuntime` | `""` |
| `internal/actions/verify_arkapi.go:56` | `""` |

而 `appconfig/validate.go:164` 校验它、`LINUX_DEPLOYMENT.md:239` 把它写成
"多实例互相影响时的处置手段"。**一个被文档推荐、被配置校验、改了却毫无效果的开关，
本身就是 bug**，与本次根因无关也要修。

### 9.2 设计要点

- **key = 实例名原样**，目录 `{BaseDir}/umu-prefix-<name>`。实例名已被
  `apiresp.ValidateInstanceName` 拒掉 `..` `/` `\` NUL，且本来就在当目录名用。不引入 hash 层。
- **按需创建**：新增 `runner.EnsurePrefix(ctx, prefixKey, progress)`（Windows no-op），
  在 `startServerInternal` 的 `runner.CheckRuntime()`（`server.go:451`）之后、
  `VerifyRuntimeAccessForLaunch` 之前调用。
  实现 = `warmPrefix` 参数化到 key；`runtimeMu` 从包级单锁改为**按 prefix 路径的锁表**
  （否则并发启动 N 个实例会被串成串行）。
- **成本**：贵的部分（GE-Proton ~450 MB、Steam Linux Runtime ~150–190 MB）已全局共享，
  新 prefix 只付一次 `wineboot --init` 加一次 VC++ 装入。
  **实测（2026-08-31）**：VC++ 装入段 `00:34:44 → 00:35:18` = **34 秒**；wineboot 段未单独计时，
  整体在一分钟量级，与设计预期一致。**占盘仍未实测**（`du -sh`），§11.3 挂着。
- **VC++**：`ensureVCRedist` 已按 key 设计。承重项是 **DLL override（一次 regedit，
  不需要显示、不需要下载）**，`vc_redist.exe` 安装是补充项——所以"每个 prefix 都要装"
  的实际成本远低于字面。`server.go:420` 的 `PrefixHasVCRedist("")` 要改传 key。
- **权限**：prefix 是独占目录，走 `chownPathForRuntime`（`warmPrefix` 里已有，
  参数化后自动对每个 prefix 生效），**不是** `PrepareSharedTree`。
- **生命周期**：删除实例时删对应 prefix；重命名**不 mv、直接删让其重建**
  （prefix 内含指向自身的绝对路径，`mv` 未必安全；prefix 里没有用户数据）。
  新增 `asa-server prefix status | gc` 处理孤儿。
- **默认值维持 `shared`**：F1 之后共享模式是可用的（与脚本一致），
  per-instance 定位为"要更强隔离、愿意付磁盘"的选项。

---

## 10. 分步实施清单

| 步骤 | 内容 | 落点 |
|---|---|---|
| **F1-1** ✅ | `umuCommandLine` 追加 `PROTON_VERB=run`，订正函数注释 | `internal/runner/runner_linux.go` |
| **F1-2** ✅ | `runInPrefix` 追加同一变量（否则有实例在跑时 `verify-arkapi` 挂满 15 分钟超时） | `internal/runner/vcredist_linux.go` |
| **F1-3** ✅ | 新增 `runner.SharesWinePrefix()`（Windows 恒 `false`，Linux 判 `PrefixMode != "per-instance"`）+ `internal/instance/launchgate.go` + `startServerInternal` 接线（§8.3） | `internal/runner/{runner,runner_windows,runner_linux}.go`、`internal/instance/{launchgate,server}.go` |
| **F1-4** ✅ | 订正 `LINUX_COMPATIBILITY_PLAN.md` §5.1 与 P2 的"③"、§6 风险 6 回链；订正 `CLAUDE.md`/`AGENTS.md`/`docs/README.md`/`docs/ARCHITECTURE.md` 里已不存在的 `serverActionsLock` 说法 | `docs/`、`CLAUDE.md`、`AGENTS.md` |
| **F1-5** ✅ | 真机验证 —— 见 §11 验收记录 | — |
| **F3-1** ✅ | 【实测追加】ArkApi 冲突阻断 `conflictingArkApiInstance`：共享模式下已有另一个 ArkApi 实例在跑时当场报错并给出改法，替掉原来的 3 分钟静默超时（§2.2） | `internal/instance/{launchgate,server}.go` |
| **F3-2** ✅ | 【实测追加】`writePrefixMarker` 写完 chown 给运行时用户，修 per-instance 首次启动被 `umu-runtime-owner-drift` 拦下（§11.2） | `internal/runner/umu_linux.go` |

**F1 已写的测试**（`go vet` 两平台通过，Windows 侧已实跑）：

| 测试 | 守住什么 |
|---|---|
| `runner.TestUmuCommandLine_PinsProtonVerbToRun`（linux） | 生效的 `PROTON_VERB` 必须是 `run`；故意先 `t.Setenv` 一个 `waitforexitandrun`，验证我们追加的那个排在后面（exec 取最后一次出现）——**这正是本次 bug 的回归测试** |
| `instance.TestLaunchGate_SharedSerializesLaunches`（linux） | 共享模式下 B 必须等 A 放行 |
| `instance.TestLaunchGate_ReleaseIsIdempotent`（linux） | 双重释放不得多放一个许可（显式放行 + defer 兜底必然调两次） |
| `instance.TestLaunchGate_PerInstanceDoesNotSerialize`（linux） | per-instance 下不排队 |
| `instance.TestLaunchGate_WaitIsCancellable`（linux） | 等锁可被 ctx 取消 |
| `instance.TestLaunchGate_NoOpOnWindows`（windows，**已实跑通过**） | Windows 行为零回归的可执行版本 |

**F2 步骤**：

| 步骤 | 内容 | 落点 |
|---|---|---|
| **F2-1** ✅ | `warmPrefix` 参数化到 key（`reconcilePrefixVersion`/chown/drain/marker 本来就是按传入 prefix 走的，跟着自动生效）；新增按 prefix 路径的锁表 `prefixLocks`，`runtimeMu` 保留给 `EnsureRuntime`（它还要下载全局的 umu/GE-Proton） | `internal/runner/umu_linux.go`、`prefix_linux.go` |
| **F2-2** ✅ | 新增 `runner.{PrefixKeyFor,EnsurePrefix,RemoveInstancePrefix,PrefixStatus}` + Windows 全 no-op；`ensureVCRedist` 已按 key 设计，直接传 | `internal/runner/prefix{,_linux,_windows}.go` |
| **F2-3** ✅ | `startServerInternal` 求出 `prefixKey` 并同源用于 `EnsurePrefix`/`PrefixHasVCRedist`/`Options.PrefixKey`；`CheckRuntime` 前移到 ArkApi 前置检查之前（Windows 上是 no-op，移动无影响） | `internal/instance/server.go` |
| **F2-4** ✅ | 删除/重命名实例时清理其 prefix（只告警不失败）；新增 `asa-server prefix status\|gc`（`gc` 默认预演，`--apply` 才删） | `internal/webapi/instanceapi/`、`internal/actions/prefix.go`、`main.go` |
| **F2-5** ✅ | `appconfig` 的字段注释与 `config.yaml` 模板、`LINUX_DEPLOYMENT.md` 排障表、`CLAUDE.md` | `internal/appconfig/`、`docs/`、`CLAUDE.md` |

**F2 已写的测试**：

| 测试 | 守住什么 |
|---|---|
| `actions.TestGCCandidates`（**已实跑**） | 逐条钉死"什么不能删"：共享 prefix、实例仍在的、wineserver 占用中的；孤儿与 `.bak-*` 才是候选 |
| `actions.TestHumanSize`（**已实跑**） | 占盘数字的可读格式 |
| `runner.TestPrefixKeyFor_FollowsMode`（linux） | 模式→key 的唯一转换点。它错了，start 路径三处会一起静默错位 |
| `runner.TestPrefixDir_KeyOnlyAppliesUnderPerInstance`（linux） | shared 下 key 必须被忽略；空 key 在任何模式下都是共享 prefix |
| `runner.TestInstancePrefixDir_IgnoresMode`（linux） | 切回 shared 后仍能定位到 per-instance 时期的残留目录（清理的前提） |
| `runner.TestRemoveInstancePrefix_NeverTouchesShared`（linux） | 空实例名不得误删共享 prefix |
| `runner.TestPrefixMarker_RoundTrips`（linux） | 标记的写方（`umu_linux.go`）与读方（`prefix_linux.go`）在两个文件里各自拼路径，拼错了不报错、只会让 `ensurePrefix` 快速路径永不命中，每次启动白重建一遍 prefix |

**遗留运维项**：

| 步骤 | 内容 | 落点 |
|---|---|---|
| **X-1** ⏳ | 清掉 `server-files/ShooterGame/Saved/` 下残留的 `pidprobe2`、`rulecheck` 两个目录（见 §12） | 运维操作 |

**可单测（无需 Linux 真机）**：`umuCommandLine` 产出的 env 含 `PROTON_VERB=run`
且位置正确（现有 `runner_linux_test.go` 就是干这个的）；`prefixDir` 的 key 组合；
锁表并发行为；`prefix gc` 的删除判定。

---

## 11. 真机验收记录（2026-08-30 ~ 08-31）

环境：Ubuntu，`/opt/asa-server/basedir`，GE-Proton10-34，umu 1.4.4，降权用户 `asa-umu-runtime`，
两个实例 `jibian-pve`(7001/7002) 与 `meijue-pve`(7003/7004)，二者均启用 ArkApi。

### 11.1 已验证通过

| # | 项 | 证据 |
|---|---|---|
| 1 | `PROTON_VERB=run` 生效 | `ps` 里两个实例的 argv 均为 `…/proton run …`；`wineserver -w` 进程彻底消失 |
| 2 | 启动闸门排队 | `00:19:20` "实例 meijue-pve 正在等待实例 jibian-pve 初始化完成后再启动"；`00:20:13` "已获得共享 Wine prefix 的启动许可" —— 等待 53 秒，与 A 到达 `start_initialization_successful` 的时刻吻合 |
| 3 | 闸门在失败时也放行 | B 于 `00:23:13` 超时失败后，后续启动未被阻塞 |
| 4 | **对照实验：共享模式 + 关闭 ArkApi + 两实例** | **两个实例先后启动、同时在线** —— 这是 §2.2 的决定性证据，也证明共享模式对纯 `ArkAscendedServer.exe` 完全可用 |
| 5 | per-instance 建 prefix | `umu-prefix-jibian-pve` 创建成功，wineboot 通过，VC++ 装入成功（`00:34:44 → 00:35:18`，**34 秒**，这一段是 vc_redist 安装；wineboot 部分未单独计时） |
| 6 | **per-instance 下两实例同时运行** | `pgrep -x wineserver` → **15953 / 16748 两个独立 wineserver**；两个 ArkApi 实例正常在线 |
| 7 | Windows 回归（单测） | `TestLaunchGate_NoOpOnWindows` 通过：闸门在 Windows 上不串行化任何东西 |

### 11.2 验证中发现并修复的缺陷

| # | 缺陷 | 修复 |
|---|---|---|
| 1 | 补上 `PROTON_VERB=run` 后第二个 ArkApi 实例仍然静默挂死 | 定位为 §2.2 的第二道闸；`conflictingArkApiInstance` 做成阻断，当场报错而非等 3 分钟超时 |
| 2 | per-instance 首次启动被 `umu-runtime-owner-drift` 拦下：`.created-by-proton` 归 root | `writePrefixMarker` 写完后 chown 给运行时用户（与同文件 `writeVCRedistMarker` 一致）。共享模式下从未暴露：prefix 在 `setup` 期间创建，实例启动前 asa-server 早已重启，`reconcileRuntimeOwnership` 顺手就修了 |

### 11.3 尚未验证

| # | 项 | 备注 |
|---|---|---|
| 1 | 有实例在跑时 `asa-server verify-arkapi --check-only` 不再挂死 | F1-2（`runInPrefix` 的 `PROTON_VERB=run`）的专项验证 |
| 2 | `start-all` 拉起 3 个以上实例 | 回归确认，批量串行本来就是既有行为 |
| 3 | ~~单个 per-instance prefix 的占盘~~ | ✅ **2026-09-01 实测：约 690 MiB/实例**（共享底层 690.9 MiB，两个 per-instance 前缀 690.0 / 689.8 MiB）。对照 overlay 可写层 63.1 MiB |
| 4 | 删除 / 重命名实例时 prefix 被清理 | F2-4 |
| 5 | `asa-server prefix status \| gc` 的实际输出 | ⚠️ 2026-09-01 真机跑过 `status`，并因此查出两个既有缺陷：`gc` 用"实例还存不存在"当判据，回收不了换模式后的残留（真机上 1.38 GiB）；版本备份目录 `umu-prefix.bak-*` **从来没被列出来过**（glob 匹配不上）。均已修，见 `docs/UMU_PREFIX_PLAN.md` §13.6.3 |
| 6 | 改回 `shared` 后的行为与残留清理 | F2-5 |
| 7 | Windows 上"A 初始化中点 B"的人工回归 | 单测已覆盖闸门短路，人工路径未走 |

### 11.4 结论

**可用组合**：

| 场景 | `prefix_mode` | 状态 |
|---|---|---|
| 单实例（用不用 ArkApi 都一样） | `shared` | ✅ 可用，省盘 |
| 多实例 + 纯 `ArkAscendedServer.exe` | `shared` | ✅ 已实测可用（启动自动串行） |
| **多实例 + ArkApi** | **`per-instance`** | ✅ 已实测可用 |
| 多实例 + ArkApi | `shared` | ❌ 不可行，已做成启动时阻断并给出改法 |
| 多实例 + ArkApi，且不想为此多占几百 MB | `overlay` | ✅ 2026-09-01 实现并通过核心真机验收：两个 ArkApi 实例 + 两个独立 wineserver，每实例只多占 **63.1 MiB**（`docs/UMU_PREFIX_PLAN.md` §13.6） |

> ⚠️ 上表第三行原来写的是「**且是唯一办法**」。那句话在 2026-09-01 之前是对的，
> 现在**不对了**：`overlay` 用一份只读底层 + 每实例一个 overlayfs 可写层拿到了
> 同样的独立 wineserver，磁盘与首启开销却接近 `shared`。原文保留在这里，是因为
> 它记录了当时的判断依据；要改配置请看 `UMU_PREFIX_PLAN.md` 的验收状态。

默认值维持 `shared`：单实例与纯 ARK 多实例都没问题，而需要 ArkApi 多开的用户会在
第一次尝试时就拿到一条指名道姓的错误信息，而不是三分钟的静默超时。`overlay` 虽然
在三个维度上都不劣于 `shared`，但它依赖 root 与内核 overlayfs，而 `shared`
不依赖任何东西 —— 先作为可选模式发布，跑一段时间再考虑改默认（overlay 方案 §11.1）。

---

## 12. 顺带发现（与本问题无关）

`server-files/ShooterGame/Saved/` 下残留了 `pidprobe2` 和 `rulecheck` 两个**目录**
（看形态是之前排障留下的探针），镜像同步每次都把它们当文件去 copy 然后报：

```
Failed to reconcile entry ShooterGame/Saved/pidprobe2: ... copy_file_range: is a directory
Failed to reconcile entry ShooterGame/Saved/rulecheck: ... copy_file_range: is a directory
```

只是噪音，删掉即可。**但值得记一笔**：`mirror` 在源侧是目录、镜像侧被当文件处理时
只打 WARN 继续，行为本身没问题，不过错误信息（`copy_file_range: is a directory`）
指向的是底层 syscall 而不是"源是目录、期望文件"，排障时容易被带偏。可考虑改进措辞。

---

# Part 2 — `prefix_mode: overlay`：共享 prefix 底层 + 每实例独立 wineserver（原 `UMU_PREFIX_OVERLAY_PLAN.md`）

# `prefix_mode: overlay` —— 共享 prefix 底层 + 每实例独立 wineserver

> 状态：**已实施并通过核心真机验收（2026-09-01）**。§9 的第 3、5 项与 §12.7 的
> 第 12 项已在目标机上跑过并回填（§13.6）。
>
> 📋 **还差什么、已知哪里不对，一律看 `docs/UMU_PREFIX_PLAN.md`。**
> 那份是会被反复勾掉重写的工作台；本文是只增不改的档案，记「为什么这么设计」
> 与「真机观测到了什么」。新缺陷加到 TODO，结论回填到本文。代码落点与设计的偏差、以及
> 实施过程中发现的三件本文没写的事，都在 §13。`prefix_mode` 默认仍是 `shared`
> —— 按 §11.1 的结论，overlay 先作为可选模式发布。
> 前置阅读：`docs/UMU_PREFIX_PLAN.md`（两道闸的定位与实测记录），
> 尤其是它的 §2.2（ArkApi 撞 Wine 会话）与 §11.4（可用组合表）。
> 关联：`docs/LINUX_COMPATIBILITY_PLAN.md` §6 风险 6、`docs/LINUX_RUNTIME_PRIVILEGE_PLAN.md`。
>
> **§10 是 2026-09-01 的回填**：显示解析已改为「自管 Xvfb 优先」
> （`docs/XVFB_DISPLAY_PLAN.md`）。本方案的设计不受影响，
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
ArkApi 实例（`UMU_PREFIX_PLAN.md` §11.1 第 6 项）。本方案不改变
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
| B | **每实例私有 `/tmp`**（`unshare -m` + tmpfs） | ✅ server 目录路径变了 | 这会造成**两个 wineserver 服务同一个 prefix 目录**——Wine 明确不支持：两边各持一份注册表内存镜像，退出时各自回写，后退出者覆盖先退出者。这恰好是 `UMU_PREFIX_PLAN.md` §3.2(c) 当初设想、后来被排除的那个损坏场景，不能主动把它造出来 |
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
  （见 `LINUX_RUNTIME_PRIVILEGE_PLAN.md` §1 那次"缺 acl 让 setup 整个跑不起来"的教训）。

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
（`UMU_PREFIX_PLAN.md` §11.2 缺陷 2）：overlay 路径上凡是 root
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
| **P9** | 文档：本文件转记录、`UMU_PREFIX_PLAN.md` §11.4 组合表补一行、`LINUX_DEPLOYMENT.md`、`CLAUDE.md` | `docs/`、`CLAUDE.md` |

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
   补上 `UMU_PREFIX_PLAN.md` §11.3 欠的那一项。
6. 停 A → B 完全不受影响；重启 A → 秒级复用已有挂载。
7. 删除实例 B → 先卸载再删干净，`prefix status` 里消失。
8. 底层失效：手工改底层的 `.created-by-proton` → 下次启动 upper 被清空重挂，实例正常起。
9. 崩溃恢复：`kill -9` asa-server 后重启 → `reconcileOverlays` 不误删活着的挂载。
10. 降级路径：临时把 `/proc/filesystems` 的 overlay 支持挡掉（或在无 overlay 的机器上）
    → 降级到 per-instance 语义 + 告警，实例仍能启动。
11. **Windows 回归**：`prefix_mode` 在 Windows 上无意义，三个值都不改变任何行为。

---

## 10. 显示解析改为「Xvfb 优先」之后（2026-09-01 回填）

`docs/XVFB_DISPLAY_PLAN.md` 已落地：显示解析顺序改成
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

值得注意的是：`per-instance` 那次已验证的两 ArkApi 实例（`UMU_PREFIX_PLAN.md`
§11.1 第 6 项，2026-08-31 上午）跑在**旧的 `xvfb-run -a` 代码**上，
也就是**两个实例各有一个私有 Xvfb**。所以"N 个独立 Wine 会话 + 一个共享 X 服务"
这个组合**至今没有被真机验证过**。

风险不高——X 服务端本来就是为多客户端设计的，而这些会话之间没有共享的 Wine 状态
（这正是 overlay 与 `shared` 的区别）。但它是个事实缺口，不该默认它成立。
它与 `XVFB_DISPLAY_PLAN.md` §7.3 用例 6 / §9 风险 5 是同一件事，
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
  见 `LINUX_RUNTIME_PRIVILEGE_PLAN.md`），所以这一条天然满足；但 `linux.umu_run_as_root`
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

**清单已移到 `docs/UMU_PREFIX_PLAN.md`**，本节不再维护副本 —— 两份待办
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

这是 §10.4 记的唯一开放项，也是 `XVFB_DISPLAY_PLAN.md` §9 风险 5 /
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

---

# Part 3 — overlay 待办与缺陷跟踪（原 `UMU_PREFIX_OVERLAY_TODO.md`）

> 本部分为活文档：新缺陷加到此处，结论回填 Part 2。保留其 P0/P1/P2/已关闭 的分区结构。

# `prefix_mode: overlay` 待办与缺陷跟踪

> 这份文档是 **overlay 模式的活动清单**，与 `docs/UMU_PREFIX_PLAN.md` 分工如下：
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
  （`XVFB_DISPLAY_PLAN.md` §10：判据要落在**能力**上，不是落在
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
| N 个独立 Wine 会话共用**一个** Xvfb | ✅ 成立，`XVFB_DISPLAY_PLAN.md` §9 风险 5 一并关闭 | PLAN §13.6.2 |
| 可写层实际占用 | ✅ 63.1 MiB/实例 vs 完整前缀 690 MiB | PLAN §0、§13.6 |
| `work/work` 挡住启动 | ✅ 已修：overlay 的三个目录都不进属主对账清单（挂载形态下） | PLAN §13.5 |
| `wineserverHoldsPrefix` 字符串前缀比较 | ✅ 已修为路径边界比较 | PLAN §12.2 |

---

# Part 4 — umu Wine 前缀初始化失败排查记录（原 `UMU_PREFIX_INIT_TROUBLESHOOTING.md`）

# umu Wine 前缀初始化失败排查记录（2026-08-29）

> 状态：**根因已确认，D0 / D1 / D5 已修复**（见 §9）。用户已用绕过方式验证
> "可以正常启动了"，修复后的代码待在真机上再跑一次全新 setup 复验。
> 相关文档：`docs/LINUX_RUNTIME_PRIVILEGE_PLAN.md`、`docs/LINUX_COMPATIBILITY_PLAN.md`、
> `docs/SETUP_FLOW_OPTIMIZATION_PLAN.md`、`docs/LINUX_RUNTIME_PRIVILEGE_PLAN.md`

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

---

# 附录 Y：文件路径对照（2026-09-29）

> ⚠️ 本表之后路径又经 `docs/UMU_RUNTIME_PLUGIN_PLAN.md`（2026-09-29）调整：`pkg/display` 整体迁入 `pkg/umuruntime/plugins/xdisplay`；`internal/runner/{display,xvfb}_linux.go` 已删除（显示解析器由 `umu_linux.go` 持有，并注册为宿主插件）；VC++ 的编排改由 `pkg/umuruntime/plugins/vcrt` 接入（`internal/runner/vcredist_linux.go` 只剩配置映射与文案）；`internal/runner/vcredist_windows.go` 更名为 `plugins_windows.go`；环境准备/就绪检查/启动命令拼装在 `pkg/umuruntime/host_linux.go`。

| 文档中的路径 | 实际路径（当前代码） |
|---|---|
| `internal/runner/overlay_linux.go` | `pkg/wineprefix/{wineprefix.go,wineprefix_linux.go}` |
| `internal/runner/prefix_linux.go`、`internal/runner/prefix.go` | `pkg/wineprefix/`、`internal/runner/prefix_windows.go`（Windows no-op） |
| `internal/runner/umu_linux.go`（wineboot/wineserver 业务逻辑） | `pkg/umu/umu{,_linux}.go`（`internal/runner/umu_linux.go` 只剩组合根与平台缝） |
| `internal/runner/vcredist_linux.go`（安装逻辑） | `pkg/vcredist/{vcredist.go,inspect.go,install_linux.go}` |
| 旧顶层包 `asaserver/` | 已整体迁入 `internal/` |

# 附录 Z：合并与同步记录（2026-09-29）

本文件由 `docs/UMU_PREFIX_PER_INSTANCE_PLAN.md`、`docs/UMU_PREFIX_OVERLAY_PLAN.md`、`docs/UMU_PREFIX_OVERLAY_TODO.md`、`docs/UMU_PREFIX_INIT_TROUBLESHOOTING.md` 于 2026-09-29 逐字物理合并而成（方案甲）；「已知缺陷清单」同步自 `docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md` §3/§4/§8（只读审计，基线 `faf127c`）；四个源文件保持原样，未作删减或改写。
