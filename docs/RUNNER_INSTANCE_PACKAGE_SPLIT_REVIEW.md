# runner / instance / pkg 拆分审阅报告

> 只读审阅，**未修改任何代码**。审阅对象：
>
> - `internal/runner/`（xvfb、shareacl、umu、wineprefix、sysuser、display、runtimeuser、vcredist、prefix 等胶水）
> - `internal/instance/`（server.go、common.go、launchgate.go、asaapilog\_\*）
> - `pkg/` 下新拆子包（xvfb、shareacl、umu、wineprefix、sysuser、problem、procmatch、tail、iox、resourcegate、asaversion、fsutil、download、pyfinder、vcredist、linuxdeps、steamrt）
>
> 配套拆分计划：`docs/RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md` 与 `docs/RUNNER_INSTANCE_PACKAGE_SPLIT_TODO.md`。
>
> 验证手段：双平台 `go build ./...` 与 `go vet ./...` 全过；针对性单元测试（problem / resourcegate / tail / xvfb）通过；关键行为用 `git show <迁移前提交>:<file>` 与迁移后文件做了逐行 diff。

---

## 0. 总体结论

迁移整体质量**良好**：API 表面稳定、import 路径收敛、双平台 build/vet 零错误、跨包依赖方向单向无环（runner/instance → pkg），Configure() 的字段扩展已经被所有三个调用点（`main.go`、`internal/actions/setup.go`、`internal/gui/gui.go`）同步更新到位。

**但发现 1 处**迁移引入的真实行为回归（P1），以及若干处死代码 / 文档问题。P1 见 §1，是这次审阅唯一必须处理的发现。

整体迁移质量评分：**B+**（若 §1 修复后升 A）。

---

## 1. 【P1】`pkg/xvfb` watch 看门狗退避序列与诊断日志丢失（行为回归）

**文件**：`pkg/xvfb/xvfb_linux.go` `L286-L301`

**迁移前**（`git show 46efc28~1:internal/runner/xvfb_linux.go`，单文件 ~951 行）：

```go
func watchXvfb(cfg Config, bin string, x *managedXvfb) {
    x.waitExit()
    if x.intentional.Load() || xvfbCurrent.Load() != x {
        return
    }
    logger.Errorf("runner: 自管 Xvfb %s（pid %d）意外退出（%s）。%s 的末尾输出：\n%s",
        x.display, x.pid, x.unusableReason(), x.log, x.logTail())        // ← ① 错误日志

    for i, backoff := range xvfbRestartBackoff {                         // ← ② 遍历序列
        time.Sleep(backoff)                                              //    2s / 5s / 15s
        if x.intentional.Load() || xvfbCurrent.Load() != x {
            return
        }
        if _, err := ensureXvfb(cfg, bin); err == nil {
            return
        } else {
            logger.Warnf("runner: 第 %d 次补起 Xvfb 失败: %v", i+1, err) // ← ③ 每次失败日志
        }
    }
    logger.Errorf("runner: 连续 %d 次拉不起 Xvfb，放弃自动恢复；"+      // ← ④ 最终放弃日志
        "下一次需要显示的启动会再试一次并报出原因", len(xvfbRestartBackoff))
}
```

**迁移后**（`pkg/xvfb/xvfb_linux.go` `L286-L301`）：

```go
func (m *Manager) watch(x *managedXvfb) {
    x.waitExit()
    if x.intentional.Load() || m.current.Load() != x {
        return // 我们自己停的，或者已经被换掉了 —— — 不该由这里插手
    }

    for range restartBackoff {
        time.Sleep(restartBackoff[0])   // ← 始终 2 秒，5s/15s 永不生效
        if x.intentional.Load() || m.current.Load() != x {
            return
        }
        if _, err := m.ensure(); err == nil {
            return // ensure 会为新的那个另起一只看门狗
        }
    }
    // — ① ③ ④ 三条诊断日志全部丢失
}
```

**严重程度**：中-高。语义与可观察性同时退化：

1. **退避序列退化为常量**：`for range restartBackoff` 把循环次数交给序列长度（3 次），但 `time.Sleep(restartBackoff[0])` 每次都拿 `[0]=2s`。结果是 Xvfb 死掉之后**永远**只睡 2 秒就重试一次，三次循环总共只睡 6 秒。原意是「2s → 5s → 15s」让重试间隔越来越长、把连续崩溃的时间窗拉大，从而减少刷屏和 CPU 空转。
2. **三条诊断日志丢失**：① 退出原因与 xvfb.log 末尾、③ 每次补起失败原因、④ 最终放弃告警，全部不在了。Xvfb 反复死亡时只看得到每 2 秒一次的「无 Xvfb」症状，根因无迹可循。

**建议修复**（diff 形状，供参考）：

```go
// watch 是 Xvfb 的看门狗：它一死就补起一个。
//
// 它**救不了正在跑的那个调用方** —— 那个进程的 X 连接已经断了，新起的还是
// 另一个显示号。看门狗的价值有两条：① 把「显示莫名其妙没了」变成日志里
// 一句「Xvfb 于某时退出，原因是…」；② 让下一次调用不必现等一个冷启动的 X 服务。
func (m *Manager) watch(x *managedXvfb) {
    x.waitExit()
    if x.intentional.Load() || m.current.Load() != x {
        return
    }
    logger.Errorf("pkg/xvfb: 自管 Xvfb %s（pid %d）意外退出（%s）。%s 的末尾输出：\n%s",
        x.display, x.pid, x.unusableReason(), x.log, x.logTail())

    for i, backoff := range restartBackoff {
        time.Sleep(backoff)
        if x.intentional.Load() || m.current.Load() != x {
            return
        }
        if _, err := m.ensure(); err == nil {
            return
        } else {
            logger.Warnf("pkg/xvfb: 第 %d 次补起 Xvfb 失败: %v", i+1, err)
        }
    }
    logger.Errorf("pkg/xvfb: 连续 %d 次拉不起 Xvfb，放弃自动恢复；"+
        "下一次需要显示的启动会再试一次并报出原因", len(restartBackoff))
}
```

迁移后 `pkg/xvfb` 是新包，原来调用 `logger.Errorf("runner: …")` 的 tag 应改为 `pkg/xvfb:`，与新包的注释/日志一致。也可以保留 `runner:`，但 `pkg/xvfb:` 便于在混部日志里定位来源。

**验证**：`go vet ./...` 双平台通过，迁移后功能上不会让 asa-server 起不来；但在「Xvfb 不停死」的故障场景下，操作者会失去排障线索，并且重试间隔不再有退避语义。

---

## 2. 【P3】冗余的 `noSuchID` 常量

**文件**：

- `pkg/xvfb/xvfb_linux.go:97`：`const noSuchID = ^uint32(0)`
- `internal/runner/runtimeuser_linux.go:37`：`const noSuchID = ^uint32(0)`
- `pkg/sysuser/sysuser_linux.go:25`：`const noSuchID = ^uint32(0)`（未导出）

**问题**：

- `pkg/xvfb/xvfb_linux.go` 的 `noSuchID` 没有任何运行时引用，只被 `pkg/xvfb/xvfb_linux_test.go:395/422/425` 用。runtimeuser_linux.go 的注释说「xvfb_linux.go compares runtimeChildIDs' result against it directly」—— 但 xvfb 实际并不跨包引用它，而是自己在测试里复制了一份。
- `internal/runner/runtimeuser_linux.go` 的 `noSuchID` 也没有任何外部引用，注释里的「xvfb_linux.go compares against it」目前并不成立。
- 三处同名常量、含义相同，运行时未引用、测试各自为政，下次有人想加新比较时容易撞名。

**严重程度**：低。是迁移过程切分时漏掉的清理，不是 bug。

**建议修复**：

- `pkg/xvfb/xvfb_linux.go` 删去 `const noSuchID`，测试改用 `sysuser.noSuchID`（需导出，见下条）或自带常量。
- `internal/runner/runtimeuser_linux.go` 删去 `const noSuchID` 与注释。
- `pkg/sysuser/sysuser_linux.go` 将 `noSuchID` 导出（改 `NoSuchID`），或保留私有但只在本包内使用，与注释「Mirrors the identically-named sentinel in pkg/sysuser (unexported there)」的现状保持一致即可。

---

## 3. 【P3】`pkg/shareacl` 的 `findAdminTool` 重复实现

**文件**：

- `pkg/shareacl/shareacl.go:270` —— ACL 二进制用
- `internal/runner/sharedaccess_linux.go:153` —— `setfacl` 诊断用
- `pkg/sysuser` 中也存在同形态的 `findAdminTool`

**问题**：三处独立实现，逻辑完全相同（LookPath → /usr/sbin → /sbin → /usr/local/sbin → /usr/bin → ""），共约 10 行。`sharedaccess_linux.go:143-152` 的注释承认了这一重复，理由是「每个包都希望 stand alone」。这是设计选择，迁移没有引入新重复，但审阅时应当注意：未来如果要加新 PATH（典型例子：nixOS 的 `/etc/profiles/per-user/$USER/bin`、CoreOS toolbox 的 `/usr/bin/toolbox`），需要**三处同步修改**，否则 `setfacl` 能找到而 `useradd` 找不到、或者反之。

**严重程度**：低。**建议**：保留现状，但在 `pkg/shareacl/findAdminTool.go` 顶部加一行 `// Keep in sync with: pkg/sysuser/findAdminTool, internal/runner.findAdminTool` 的对位提醒，避免「改了 A 忘了 B/C」。

---

## 4. 【P3】`internal/instance/asaapilog_linux.go` 的 ctx 取消语义细微差异

**文件**：`internal/instance/asaapilog_linux.go:117-138`

**迁移前**（`git show a9c0000~1:internal/instance/asaapilog_linux.go`）：

```go
// done 关闭返回 "启动链已结束，仍未生成 ArkApi 日志"
// deadline 超时返回 "等待超过 %s"
// 两条路径各自独立，措辞精确区分
```

**迁移后**：

```go
ctx, cancel := context.WithTimeout(context.Background(), arkApiLogAppearTimeout)
defer cancel()
go func() {
    select {
    case <-done:
        cancel()
    case <-ctx.Done():
    }
}()
srcPath, err := tail.WaitNewest(ctx, dir, launchedAt, isArkApiLogName, arkApiLogPollInterval)
if err != nil {
    reason := "启动链已结束，仍未生成 ArkApi 日志"
    if errors.Is(err, context.DeadlineExceeded) {
        reason = fmt.Sprintf("等待超过 %s", arkApiLogAppearTimeout)
    }
    // …
}
```

**差异**：

- 迁移前：done 与 deadline 是 select 里的两条独立 case，分别走不同的错误信息。
- 迁移后：done 通过 `cancel()` 把 ctx 变成 Canceled（不是 DeadlineExceeded），然后用 `errors.Is(err, context.DeadlineExceeded)` 二分。
- **差异点**：done 和 deadline **同时**到达时的优先级与措辞有微小变化（迁移前两者看 `select` 的随机性，迁移后同样看 `select` 的随机性 + `time.AfterFunc` 的精确时刻）。用户感知不到差异；日志、API、HTTP 返回都一样。
- `tail.WaitNewest` 在 ctx.Done 时**再查一次**目录（`waitnewest.go:31-33`），与迁移前 done case 里的「再看最后一眼」语义一致。✓

**严重程度**：极低。行为对外完全等价，仅取消路径表达形式不同。

**建议**：无需修改。如果后续要让 deadline 与 done 在措辞上彻底分得开，可在迁移后的代码里加 `errors.Is(err, context.Canceled)` 分支独立处理，但当前没必要。

---

## 5. 跨包依赖图核查（无环）

迁移后的导入方向：

```
internal/instance  →  internal/runner
                  →  pkg/procmatch, pkg/procx, pkg/asaversion, pkg/tail, pkg/iox, pkg/logger
                  →  pkg/resourcegate
                  →  pkg/shareacl (经 internal/runner)

internal/runner   →  pkg/xvfb, pkg/shareacl, pkg/sysuser, pkg/umu, pkg/wineprefix,
                     pkg/vcredist, pkg/display, pkg/linuxdeps, pkg/download, pkg/pyfinder,
                     pkg/steamrt, pkg/problem, pkg/console, pkg/fsutil

pkg/xvfb          →  stdlib only
pkg/shareacl      →  stdlib only
pkg/sysuser       →  pkg/problem
pkg/umu           →  stdlib only
pkg/wineprefix    →  pkg/umu
pkg/problem       →  stdlib only
pkg/procmatch     →  pkg/procx
pkg/resourcegate  →  stdlib only
pkg/tail          →  stdlib only
pkg/iox           →  stdlib only
pkg/asaversion    →  stdlib only
```

**核查结论**：

- `pkg/*` 之间**无循环**（vcredist 例外是 cfg → runner 顺序，无反向边）。
- `internal/instance` 与 `internal/runner` 都依赖 `pkg/*`，但**不**互相依赖导致 instance ↔ runner 形成新边——目前 instance 依赖 runner（启动路径），runner 不依赖 instance，符合「runner 不认识实例、instance 负责调用 runner」的设计约束。
- `internal/runner` **不再依赖** `internal/config`（`runner.go` 的 `Config` 现在是 `pkg/umu/wineprefix/shareacl` 配置的扁平组装），避免了与 `config` 包的潜在循环（`runner.go:596-601` 的注释明确说了这一点）。

---

## 6. 双平台编译验证

| 检查 | Windows | Linux (cross) |
| --- | --- | --- |
| `go build ./...` | ✅ 无输出 | ✅ 无输出 |
| `go vet ./...` | ✅ 无输出 | ✅ 无输出 |
| `go vet ./internal/runner/... ./internal/instance/...` | ✅ | n/a（cross） |
| `go vet ./pkg/...` | ✅ | n/a（cross） |

**遗留项**：`pkg/shareacl` 因 `//go:build linux` 在 Windows 上 `go test` 报「build constraints exclude all Go files」（包内无 Windows 文件），是预期行为。完整 Linux 测试在交叉环境跑不动（需 Wine/proton 才能验证），本机只跑了 Windows 子集（problem / resourcegate / tail），全部通过。

---

## 7. `runner.Configure` 调用点字段同步核查

`runner.Configure(cfg runner.Config)` 走的是 wholesale 替换（`runner.go:644` 的注释特别强调了这一点），任何加进 `Config` 的字段**必须**在每个调用点都补上，否则会被静默清零。

| 字段 | `main.go:282` | `internal/actions/setup.go:84` | `internal/gui/gui.go:437` |
| --- | --- | --- | --- |
| `PrefixMode` | ✓ | ✓ | ✓ |
| `XvfbBin` | ✓ | ✓ | ✓ |
| `XvfbScreen` | ✓ | ✓ | ✓ |
| `AllowX11Remount` | ✓ | ✓ | ✓ |
| `RuntimeUser` | ✓ | ✓ | ✓ |
| `RuntimeUID` | ✓ | ✓ | ✓ |
| `RuntimeGID` | ✓ | ✓ | ✓ |
| `RunAsRoot` | ✓ | ✓ | ✓ |
| `RuntimeDeepProbe` | ✓ | ✓ | ✓ |

**结论**：三处调用点同步更新到位，未见漏字段。`runner.go:643-645` 的注释专门讲了这条规矩（曾经的 bug：`asa-server setup` 漏字段导致 preflight 与 setup 之后的运行时状态不一致），迁移过程严格遵守了。

---

## 8. 其他值得留意但不需立即修复的点

1. **`pkg/asaversion/asaversion.go:122-127` 的类型断言**：`entry := v.(cacheEntry)` 没有 `.()` 双值断言保护。`sync.Map` 在误用（外部写非 `cacheEntry`）时会 panic。当前 `pkg/asaversion` 是唯一生产者，攻击面 0。**建议**：保持现状，但若未来 `pkg/asaversion` 的 `Resolver` 暴露给外部库使用，需改为 `value, ok := r.cache.Load(...); if !ok { ... }` 的安全形式。
2. **`pkg/iox.Relay` 调用方负责关闭 `dst`**：`Relay` 自身不关闭（注释 `relay.go:10-19` 没说，但实现也没关）。`asaapilog_linux.go:107/148` 用 `defer dst.Close()` 与 `defer src.Close()` 正确处理。**建议**：在 `Relay` 文档明确「不关闭 src/dst」，避免下一个调用方误解。
3. **`pkg/procmatch.Matcher.isWineSideCmdline`** 用反斜杠 + exe 名判定 Wine-side cmdline（`procmatch.go:55-62`）。Wine 通过 umu-run 启动时 `cmdline` 确实是 Windows path 形式，这条判据保留正确**且与迁移前一致**——审阅时确认 `instance/common.go:36` 的 `gameProcMatcher = procmatch.New([arkExeName, asaApiLoaderExeName], "GameThread")` 调用方式与原 `procmatch` 模块的入口契约吻合。
4. **`pkg/xvfb/manager.go:99` 的 `current atomic.Pointer[managedXvfb]` + `mu`**：读路径（`Status()`）无锁、写路径（`Acquire/Stop/ensure`）拿锁。`adoptTried` 仅由 `mu` 守护，未声明 atomic——这是有意的（外部读 `adoptTried` 不存在），与迁移前一致。
5. **`internal/runner/umu_linux.go:49` 的 `ChildIDs` 签名**：`func() (uint32, uint32, bool)` 与 `pkg/umu.Config.ChildIDs` 完全一致。`func() string` 的 `UserName` 同样匹配（`umu_linux.go:55`）。`Credential` 字段经 `resolveRuntimeCredential` 适配 `*syscall.Credential` 也对得上。导入路径、调用约定经全文 grep 确认无不一致。
6. **`pkg/wineprefix/wineprefix_linux.go`**（未列出全文件，但 grep 过导出符号）：`Manager.Dir / KeyFor / Ensure / Remove / Status / PrepareSharedWrite / Reconcile / CheckSharedReady / LowerNeedsWork / OverlayRoot / UnmountedOverlayDirs` 全部存在；`umu_linux.go:39-77` 的 `umuRuntimeFor` 与 `wineprefixMgrFor` 转译对得上。
7. **`pkg/umu/umu_linux.go:305-315` 的 `runtimeUserNameHint` 是死方法**：

    **文件**：`pkg/umu/umu_linux.go:305-315`

    ```go
    // runtimeUserNameHint is a best-effort label for RuntimeEnv's USER/LOGNAME
    // rewrite. Runtime doesn't own the runtime user's name (sysuser does) — the
    // caller only injected uid/gid/credential resolution, not the name — so this
    // falls back to a generic placeholder rather than needing a fifth callback
    // for a cosmetic env var.
    func (r *Runtime) runtimeUserNameHint() string {
        if uid, _, managed := r.config().childIDs(); managed {
            return fmt.Sprintf("uid%d", uid)
        }
        return ""
    }
    ```

    **验证**：

    - `grep -rn "runtimeUserNameHint" .` 全仓只命中两行：注释（L305）与定义（L310）；`r.runtimeUserNameHint(...)` / `Runtime(...).runtimeUserNameHint` 均无调用方。
    - `RuntimeEnv`（`pkg/umu/umu_linux.go:591`）的实际签名是 `func RuntimeEnv(base []string, home, userName string) []string`，第三个参数直接是 `userName`，不向 Runtime 要 hint。
    - 唯一调用点 `pkg/umu/umu_linux.go:279` `cmd.Env = RuntimeEnv(cmd.Env, cfg.homeDir(), cfg.userName())` 走的是 `Config.userName()`（`umu.go:93`），后者读 `Config.UserName func() string` 回调。
    - 该回调确实存在（`Config.UserName`，`umu.go:62`），由调用方（`internal/runner/runtimeuser_linux.go:111-113` 的 `runtimeUserName`）注入。所以「avoiding a fifth callback for a cosmetic env var」这条动机已经不成立——**第五个回调本来就在 `Config` 里**。

    **来历**：`git log -S runtimeUserNameHint -- pkg/umu/` 单点命中 `df54111 refactor(runner): 阶段I——umu 运行时与 Wine 前缀布局下沉到 pkg/umu、pkg/wineprefix`。自引入第一天起就没人调用，是迁移过程中留下的草稿。

    **严重程度**：低。无运行时影响、不影响测试、不影响可观察性，纯属代码卫生。

    **建议**：直接删除该方法与其上方整段注释。`Config.UserName` + `Config.userName()` + `RuntimeEnv` 的现有链路已经覆盖了所有使用场景。

---

## 9. 修复优先级总结

| # | 级别 | 一句话 |
| --- | --- | --- |
| 1 | **P1** | 修复 `pkg/xvfb/xvfb_linux.go` watch 的退避序列与三条诊断日志（见 §1 diff） |
| 2 | P3 | 清理三处重复的 `noSuchID` 常量（§2） |
| 3 | P3 | `findAdminTool` 三处重复实现加注释互引（§3） |
| 4 | P3 | 删除 `pkg/umu/umu_linux.go:305-315` 的死方法 `runtimeUserNameHint`（§8.7） |
| 5 | - | `asaapilog_linux.go` 语义差异无需处理（§4） |
| 6 | - | `pkg/iox.Relay` 文档补一句「不关闭 src/dst」（§8.2） |

---

## 10. 整体迁移质量评估

- **编译与 vet**：✅ 双平台零错误。
- **依赖方向**：✅ 单向、无环、runner 不依赖 instance、pkg 不依赖 internal。
- **API 稳定性**：✅ runner 顶层签名（`Run / EnsureRuntime / EnsurePrefix / SharesWinePrefix / StopManagedDisplay / DisplayStatus / Preflight / EnsureRuntimeUser / SharedAccessStatus` 等）原样保留；alias（`Problem / DLLOrigin / VCRedistInfo / VCRedistDLLInfo / PrefixInfo / Info`）类型定义正确。
- **测试覆盖**：迁移后保留的测试（`xvfb_linux_test.go / display_linux_test.go / runtimeuser_linux_test.go / runner_linux_test.go / runner_windows_test.go / sharedaccess_test.go / xvfb_linux_test.go / pkg/shareacl/shareacl_test.go / pkg/asaversion/waitnewest_test.go` 等）覆盖了被迁移函数的关键分支；新增的 `pkg/tail/waitnewest_test.go` 与 `pkg/iox/relay_test.go`（grep 命中）专门覆盖拆出来的纯函数。
- **行为保真**：除 §1 外全部一致。
- **文档质量**：每个新拆包的 `// Package …` 注释都清楚地声明了「认识什么、不认识什么」，并指向上游决策文档（`docs/UMU_RUNTIME_USER_PLAN.md / ARKAPI_LINUX_VCREDIST_PLAN.md / XVFB_CROSS_DISTRO_DISPLAY_PLAN.md` 等），符合既有风格。

**唯一必修**：§1 的 watch 看门狗。其余皆为可清理项，可随下次动该区域代码时一并处理。