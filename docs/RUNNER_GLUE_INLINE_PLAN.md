# `internal/runner` 薄转发函数清理方案

> 起因：拆包（`docs/RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md` 阶段 A–J +
> `docs/RUNNER_INSTANCE_PACKAGE_SPLIT_TODO.md` Gap A–F）把机制全部下沉进 `pkg/*` 之后，
> `internal/runner` 里留下了一批「签名不变的薄转发函数」。它们当初是**为了让拆包这一步
> 不必同时改所有调用点**才保留的过渡层，拆包完成后大部分已经没有存在理由。
>
> 本文档只做一件事：删掉不再承担判断的转发层，让调用点直接问持有对象。
> **零行为变更**、不动 `pkg/*`、不搬文件、不改任何导出 API。

---

## 1. 先纠正问题前提

题面是「这些胶水层只会被 `runner_linux.go` 调用」。核对下来**不成立**，这一点直接决定
哪些能删、哪些不能删：

| 转发函数 | 调用点分布 |
|---|---|
| `prefixDir` | `runner_linux.go`×3、`runtimeuser_linux.go`×3、`vcredist_linux.go`×3 |
| `protonPath` | `runner_linux.go`×2、`runtimeuser_linux.go`×2 |
| `umuRunPath` | `runner_linux.go`×2 |
| `umuDir` | `runtimeuser_linux.go`×1 |
| `prefixKeyFor` / `ensurePrefix` / `removeInstancePrefix` / `prefixStatus` / `prepareSharedPrefixWrite` / `reconcilePrefixes` | **`runner.go`（无 build tag）** |
| `planDisplay` | `preflight_linux.go`、`vcredist_linux.go`、测试 |
| `acquireDisplay` | `runner_linux.go`、`vcredist_linux.go` |
| `protonBaseDir` / `waitForWineserverDrain` | **零** |

第 5 行那一组是**平台接缝**：`runner.go` 不带 build tag，它调的每个非导出名字必须在
Windows 上也有定义（`prefix_windows.go` 的六个空实现就是为它们存在的）。删掉 Linux 侧的
定义，等于把 build tag 推到 `runner.go` 的调用点上——那正是接缝要消灭的东西。所以
**这一组一个都不能动**，与它们有多薄无关。

---

## 2. 判据

> **绑定 = 留，转发 = 删。**
>
> 把 `runner.Config` 接到某个 `pkg` 构造器上的函数（`umuRuntimeFor`、`wineprefixMgrFor`、
> `xvfbManager`、`displayResolver`、`sysUserFor`、`vcRedistInstallerFor`、`umuInterpreter`），
> 做的是组合根**自己的**工作——哪些字段怎么映射、单例还是现建、要不要先 `Reconfigure`。留。
>
> 在已经绑定好的对象上再转发**一次方法调用**的函数（`umuDir` = `umuRuntimeFor(cfg).Dir()`），
> 只是给一个方法换了个名字，不含任何判断。删——除非它是①平台接缝，或②函数名/注释本身
> 承载着一条学费换来的规则。

例外②不是托辞，下面 §4 会逐个点名，并说明那条规则是什么。

---

## 3. 要做的改动

### 3.1 Tier 0：死代码，直接删（2 个）

`internal/runner/umu_linux.go`：

```go
func protonBaseDir(cfg Config) string { return umuRuntimeFor(cfg).ProtonBaseDir() }
func waitForWineserverDrain(prefix string) { umu.WaitForWineserverDrain(prefix) }
```

全仓库零调用点。Go 不报未使用的包级函数，所以它们在拆包时被留下后一直没人发现——
调用方早已改成直接用 `pkg/umu`（`Runtime.ProtonBaseDir` 在 `pkg/umu` 内部自用，
`WaitForWineserverDrain` 由 `pkg/wineprefix` 直接调）。

删掉后确认 `umu_linux.go` 仍需要 `umu` 与 `syscall` 导入（`umu.New`/`umu.Config` 与
`Credential` 回调仍在用），无孤儿导入。

### 3.2 Tier 1：内联到调用点，删转发（5 个）

| 删除 | 调用点改为 |
|---|---|
| `umuDir(cfg)` | `umuRuntimeFor(cfg).Dir()` |
| `umuRunPath(cfg)` | `umuRuntimeFor(cfg).RunPath()` |
| `protonPath(cfg)` | `umuRuntimeFor(cfg).ProtonPath()` |
| `prefixDir(cfg, key)` | `wineprefixMgrFor(cfg).Dir(key)` |
| `inheritedEnv()` | `umu.InheritedEnv()`（`runner_linux.go` 唯一调用点） |

**内联时一律把持有对象提到函数顶部**，而不是每处各调一次 `umuRuntimeFor`：

```go
// checkRuntime / umuCommandLine / rwSubtrees / reconcileRuntimeOwnership / verifyRuntimeAccess
cfg := getConfig()
umuRT := umuRuntimeFor(cfg)
wp := wineprefixMgrFor(cfg)
```

这不只是省字。`umuRuntimeFor`/`wineprefixMgrFor` 是**带副作用的**（每次调用做一次
`Reconfigure`），今天这个副作用被 `prefixDir(cfg, ...)` 这样的名字藏住了：
`rwSubtrees` 现在一次调用会 `Reconfigure` 四次（`prefixDir`×2 + `wineprefixMgrFor`×2），
`verifyRuntimeAccess` 三次。提取变量之后每个函数各一次，并且「整个函数用的是同一份
`cfg` 绑出来的同一个 manager」变成代码里看得见的事实——`ensureRuntime` 早就是这么写的
（`umuRT := umuRuntimeFor(cfg)` / `wpMgr := wineprefixMgrFor(cfg)`），本次是把其余五个
函数拉齐到同一写法。

逐处：

- `runner_linux.go`
  - `checkRuntime`（L134-148）：`umuRT`/`wp` 各提一次，三处替换。
  - `sharesWinePrefix`（L166-169）：`wp := wineprefixMgrFor(cfg)`，
    `return wp.Dir("instance-a") == wp.Dir("instance-b")`。注释里「asks prefixDir the
    question directly」要改成 `wineprefix.Manager.Dir`——**这条注释本身是承重的**
    （解释为什么不能写成 `PrefixMode != "per-instance"` 的模式判断），只改指代对象。
  - `umuCommandLine`（L177-243）：`umuRT` 提一次（L192 `ProtonPath`、L203 `RunPath`），
    `wp` 提一次（L197），L207 `inheritedEnv()` → `umu.InheritedEnv()`。
  - L245-250 的注释块目前同时覆盖 `inheritedEnv` 与 `gamePath`，改成只讲 `gamePath`
    （它是平台接缝，见 §4）。
- `runtimeuser_linux.go`
  - `reconcileRuntimeOwnership`（L132/L137）：注意 L137 现在的局部变量名叫 `umu`，
    本文件没有导入 `pkg/umu` 所以不冲突，但内联后建议改名为 `umuHome`/直接用 `umuRT.Dir()`。
  - `rwSubtrees`（L151/L155/L154/L175）：四次 `Reconfigure` 合并成一次。
  - `verifyRuntimeAccess`（L212/L213）。
- `vcredist_linux.go`（L88/L119/L132）：三处 `prefixDir(...)` → `wineprefixMgrFor(...).Dir(...)`。
  L86-87 那条「全程用同一份 cfg，中途 `Configure` 换了指针会导致装到 A 前缀、校验 B 前缀」的
  注释与本次的提取变量写法是同一个理由，保留。

**无新增重入**：`wineprefixMgrFor` 的 `Reconfigure` 里注入的 `EnsureVCRedist` 回调会调
`ensureVCRedist`，而 `ensureVCRedist` 内部又会调 `wineprefixMgrFor`——这个环今天就通过
`prefixDir` 存在，且只是「装配闭包」不会真的递归执行。内联不改变这一点。

### 3.3 Tier 1b：可选，`runtimeUserManaged`（连带改测试）

`runtimeUserManaged(cfg)` = `sysUserFor(cfg).Managed()`，纯转发，无 Windows 对应物
（`sharedaccess_linux.go` 与它的三个调用点都带 linux tag）。按 §2 的判据该删，但
`runtimeuser_linux_test.go` 有 3 处引用，删它要顺手改测试。

建议**与 Tier 1 分开一个 commit**，或干脆不做：收益是少一个名字，代价是动到测试文件。
按 PLAN §7「不在同一次提交里顺手改别的」的既有约定，倾向于单独一个小 commit。

---

## 4. 明确不动的部分，以及原因

1. **平台接缝六件套** `prefixKeyFor` / `ensurePrefix` / `removeInstancePrefix` /
   `prefixStatus` / `prepareSharedPrefixWrite` / `reconcilePrefixes`，以及同性质的
   `displayStatus` / `stopManagedDisplay` / `runtimePython` / `preflight` /
   `ensurePrefixVCRedist` / `prefixHasVCRedist` / `vcRedistStatus` / `gamePath` /
   `launcherIsDirect` / `run` / `checkRuntime` / `sharesWinePrefix` / `ensureRuntime` /
   `runtimeHomeDir` / `runtimeUserName` / 整套 `runtimeuser_windows.go` 对应项。
   理由见 §1：`runner.go` 无 build tag，删掉 Linux 侧定义就得在调用点加 tag。

2. **绑定函数** `umuRuntimeFor` / `wineprefixMgrFor` / `xvfbManager` / `displayResolver` /
   `sysUserFor` / `vcRedistInstallerFor` / `umuInterpreter`。这些**就是**组合根本身，
   本方案要做的正是让调用点直接用它们。

3. **`planDisplay` / `acquireDisplay`**（例外②）。两者确实各只是
   `displayResolver().Plan()` / `.Acquire()`，但这对名字承载的规则是：
   **只读判断与「可能拉起一个 X 服务端」必须是两个名字**。`preflight` /
   `DisplayStatus` / `verify-arkapi --check-only` 只许问前者。这条规则是踩过的坑
   （见 `pkg/display` 包注释与 TODO §3.2），保住两个短名字比省两行更值。
   `pkg/display.Resolver` 侧已经有同名方法，但调用点写 `displayResolver().Acquire()`
   会让「这一行可能 fork 一个进程」变得不显眼。

4. **`xvfbStatePath(cfg)`**：不是转发，是一段真实计算（`BaseDir` 为空要返回空串）。留。

5. **`pythonProblem`**：作为函数值传给 `linuxdeps.Check(pythonProblem)`，且做了
   `pyfinder.AsError` → `Problem` 的翻译。不是转发。留。

6. **`pkg/*` 一律不动。** 本次不碰 TODO §6.7 记的那四处「pkg 里写着 `asa-server setup`」
   的一致性问题。

---

## 5. 预期结果

- `internal/runner` 少 7 个包级名字（Tier 0 两个 + Tier 1 五个），Tier 1b 再少 1 个。
- `umu_linux.go` 210 → 约 195 行（`prefixDir` 那一组连同上方 5 行注释块整体消失）。
- `internal/runner` 总行数（不含测试）约 -15。
- 副作用密度：`rwSubtrees` 4→1 次 `Reconfigure`，`verifyRuntimeAccess` 3→1，
  `checkRuntime` 3→2，`umuCommandLine` 3→2。
- 导出 API、行为、日志文案：**零变化**。

## 6. 需要同步更新的文档

1. `CLAUDE.md` 的 `umu_linux.go` 条目——现在写着「`umuDir`/`protonPath`/`prefixDir` 等
   签名不变的薄转发函数也在这」，删完就不成立了。
2. `docs/RUNNER_INSTANCE_PACKAGE_SPLIT_TODO.md` §0 的行数表（`umu_linux.go` 209 那一行）。
3. 本文档执行完后在文末回填落地记录（沿用 TODO 的写法：结论稳定后回填，PLAN 只增不改）。

> 备选：本方案也可以直接作为 TODO 的「Gap G」写进
> `RUNNER_INSTANCE_PACKAGE_SPLIT_TODO.md`，与 Gap A–F 同一份活动清单。独立成篇是因为
> 它与 Gap A–F 性质不同——那六项是「机制该不该下沉到 `pkg/`」的包边界问题，本方案
> 纯粹是拆包完成后的收尾，不涉及任何边界判断。

## 7. 验证

- Windows：`go build ./... && go vet ./...`（覆盖不到本次改的任何一行，只保证没把
  `runner.go`/`*_windows.go` 碰坏）
- 交叉编译：`GOOS=linux GOARCH=amd64 go build ./... && go vet ./...`
  ——**这一条是本次的主要验证手段**，因为改动全在 linux tag 内。
- WSL2 真机：`go test ./internal/runner/ ./pkg/...`（排除 `pkg/tail`），
  外加一次 `asa-server setup` + 一次实例启动，覆盖 `checkRuntime`/`umuCommandLine`
  这两个被改到的启动路径函数。
- 因为是纯内联，**不新增测试**；现有 `runner_linux_test.go` / `display_linux_test.go` /
  `runtimeuser_linux_test.go` 保持通过即可（Tier 1b 除外，它要改 3 处测试引用）。

## 8. 建议提交拆分

1. `refactor(runner): 删除两个零调用点的转发函数`（Tier 0，可独立回滚）
2. `refactor(runner): 薄转发内联为直接调用持有对象`（Tier 1 + 文档同步）
3. （可选）`refactor(runner): 内联 runtimeUserManaged`（Tier 1b + 测试）

---

## 9. 落地记录（2026-09-05，Tier 0 + Tier 1 已执行）

提交：`5387ddf`（Tier 0）+ 本次（Tier 1）。**Tier 1b（`runtimeUserManaged`）未做**，
仍是可选项。

### 9.1 实际改动

| 文件 | 改动 |
|---|---|
| `umu_linux.go` | 209 → 203。删 `protonBaseDir`/`waitForWineserverDrain`（Tier 0）与 `umuDir`/`umuRunPath`/`protonPath`/`prefixDir` 及其上方 5 行注释块 |
| `runner_linux.go` | `checkRuntime`/`umuCommandLine` 各提一个 `umuRT`，`sharesWinePrefix` 提一个 `wp`；`inheritedEnv()` → `umu.InheritedEnv()`，转发函数删除 |
| `runtimeuser_linux.go` | `reconcileRuntimeOwnership` 提 `umuRT`，`rwSubtrees` 提 `wp`，`verifyRuntimeAccess` 两处直接调用 |
| `vcredist_linux.go` | 三处 `prefixDir(...)` → `wineprefixMgrFor(...).Dir(...)` |

`internal/runner` 净 -5 行、少 6 个包级名字。`Reconfigure` 次数按 §5 预期下降
（`rwSubtrees` 4→1、`verifyRuntimeAccess` 3→1、`checkRuntime` 3→2、`umuCommandLine` 3→2）。

### 9.2 与方案的两处偏离

1. **`umu_linux.go` 只降到 203 行，不是预估的约 195**——删掉的 9 行之外，给保留下来的
   六个平台接缝函数**补了一段 9 行注释**。补的原因是删除动作本身制造了一个新的误读面：
   原本上下两块转发函数长得一模一样，上面那块的注释解释了「为了拆包不改调用点而保留」，
   删掉之后剩下的那块看起来就成了同一批残留。写清楚「它们是平台接缝，删了就得在
   `runner.go` 的调用点加 build tag」，比省下 9 行值。

2. **`runtimeuser_linux.go` 里局部变量 `umu` 改名为 `umuDir`**（`if umuDir := umuRT.Dir()`）。
   本文件没有导入 `pkg/umu`，原名不冲突，但内联后同一个函数里同时出现 `umuRT` 和 `umu`
   容易误读。

### 9.3 验证

- `go build ./... && go vet ./...`（Windows）✅
- `GOOS=linux GOARCH=amd64 go build ./... && go vet ./...` ✅ ——本次改动全在 linux tag 内，
  这条是主要验证手段；`go vet` 同时类型检查了 `*_linux_test.go`。
- `grep` 确认 7 个被删名字在全仓库零残留引用。
- **WSL2 真机未跑**（`asa-server setup` + 实例启动仍待验证）。纯内联无逻辑变更，风险主要
  在「提取变量后 `Reconfigure` 次数减少」这一条——但同一函数内 `cfg` 本来就是固定的，
  减少的都是重复的等价调用。

---

## 10. 第二轮：只服务 Linux-only 调用方的空实现（2026-09-05）

起因是一个观察：`asa-server perms` 与 `asa-server prefix` 两条命令**只在
`main_linux.go` 的 `platformCommands` 里注册**，Windows 上根本不存在。那么它们背后那些
「Windows 恒为空操作」的实现是不是也没有存在理由？

§4.1 给平台接缝定的判据是「`runner.go` 无 build tag，删了就得在调用点加 tag」。这一轮把它
**反过来用**：调用点**本来就带 tag** 时，Windows 侧的空实现一样什么都不换来。

### 10.1 已执行：四个只被 linux-only 文件调用的导出 API

| 导出 API | 唯一调用方 | 处理 |
|---|---|---|
| `runner.RuntimeHomeDir()` | `internal/installer/fixups_linux.go:47` | 从 `runner.go` 移进 `runtimeuser_linux.go` |
| `runner.RuntimeUserName()` | `internal/svcmgr/service_linux.go:95` | 删掉 `runtimeuser_windows.go` 里的那份 |
| （连带）`runtimeHomeDir` / `runtimeUserName` 的 Windows 实现 | 只被上面两个用 | 删除；`runtimeuser_windows.go` 的 `os` 导入随之消失 |

判据是「返回值有没有人读」：Windows 上 `RuntimeHomeDir()` 返回一个真实的
`os.UserHomeDir()`、`RuntimeUserName()` 返回 `""`，但**没有任何 Windows 代码路径读它们**。
一个没人读的返回值不省 build tag，只是对外宣称了一个在本平台没有意义的 API。

需要两平台都能拿到运行时用户名的调用方（`webapi/systemapi`）读的是
`RuntimeUserStatus().Name` —— 那个是真跨平台的，不受影响。

### 10.2 已执行：`prefix.go` 的两段不可达代码

`actionPrefixStatus` / `actionPrefixGC` 开头各有一段
`if runtime.GOOS != "linux" { 打印"Windows 上没有 Wine 前缀"; return nil }`。
**两边都到不了**：Windows 上这条命令不存在，Linux 上条件恒假。连同 `runtime` 导入删除，
并在两个文件的命令注释里写清楚「本文件不需要也不该有 `runtime.GOOS` 判断」，免得下次
有人照着 `setup.go` / `verify_arkapi.go`（那两条**是** `commonCommands`，GOOS 判断是真的）
的样子再加回来。

`perms.go` 里「（Windows 恒是这种情况；…）」那半句同样是对一个 Windows 上不存在的命令
说话，改成只讲 Linux 的两种情形。

### 10.3 有意不做：`PrefixStatus` / `SharedTrees`

这两个确实**只被 `prefix.go` / `perms.go` 调用**，但要删掉它们的 Windows 空实现，得先给
这两个文件加 `//go:build linux`，而那有两个前置障碍：

1. **会直接编译失败**：`existingInstances()` 定义在 `prefix.go`，却被 `arkapicache.go` 使用，
   而 `ArkApiCacheCommand` 在 `commonCommands` 里（Windows 也注册）。
2. **会丢掉 Windows 侧的测试覆盖**：`prefix_test.go` 测的 `gcCandidates`（其注释自称
   「这套命令里唯一有可能造成数据损失的地方」）与 `humanSize` 都是纯逻辑，现在在 Windows 上跑。
   文件加 tag 后测试也得加 tag —— 这与 TODO §7「让判据能在 Windows 上单测」的方向相反。

绕开要拆文件 + 挪 helper，代价大于「少两个 Windows 空函数」的收益。**结论：不做。**

其余看着像候选的都有真实的 Windows 调用方，不能动：`RemoveInstancePrefix`（`instanceapi`）、
`SharedAccessStatus`（`actions/setup.go`）、`PrepareSharedTree`（`installer` 3 处 +
`instance/server.go`）。

### 10.4 顺带修正与遗留

- `runtimeuser_linux.go` 里 `RuntimeUserName` 的注释原写着「svcmgr / systemapi 需要」——
  **systemapi 并不调它**（读的是 `RuntimeUserStatus().Name`）。已改。
- `docs/ACL_PERMISSION_HARDENING_PLAN.md` §（Windows 行为）里有一句「Windows 上
  `perms status`/`fix` 会打印…」，描述的是一条 Windows 上不存在的命令。**未改** ——
  PLAN 是只增不改的档案，记在这里。

### 10.5 验证

`go build`/`go vet` 双平台通过；`go test ./internal/actions/` 通过（`prefix_test.go`
仍在 Windows 上跑，正是 §10.3 保住的那部分）。净 -2 行，少 4 个包级名字。
