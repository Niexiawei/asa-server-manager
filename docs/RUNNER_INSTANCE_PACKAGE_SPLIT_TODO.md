# `internal/runner` 拆包后续清单

> 依赖 `docs/RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md`（阶段 A–J 已于 2026-09-05 全部执行完成，
> 见该文档历史与提交 `a83c85a`..`6894907`）。本文档是**活动清单**，记录方案执行完之后盘点出的
> 真实缺口与可选卫生项；结论稳定后回填 PLAN，不在 PLAN 里直接改。
>
> **Gap A（`pkg/shareacl`）与 Gap B（vcredist 零散机制）均已于 2026-09-05 执行完成**——
> 见 §1、§2 末尾的落地说明。

---

## 0. 先回答"文件数比方案里多"是不是偏离

方案 §2 的目标目录结构把 `internal/runner` 最终画成 `runner.go` +
`runner_{windows,linux}.go` + `preflight.go` 四个文件。实际落地时**没有**把每个子系统的组合根
代码全部塞进 `runner_linux.go`，而是保留了一个文件对应一个子系统（`xvfb_linux.go`、
`python_linux.go`、`umu_linux.go`、`display_linux.go`、`runtimeuser_linux.go`、
`sharedaccess_linux.go`、`vcredist_linux.go`、`prefix_windows.go`）。这是执行时的有意选择，
不是漏拆——核对每个文件的内容：

| 文件 | 行数 | 内容 |
|---|---:|---|
| `python_linux.go` | 49 | 纯胶水：`pyfinder.New()` + 把 `Config.PythonBin` 接进去 |
| `xvfb_linux.go` | 49 | 纯胶水：`xvfb.New()` + `Reconfigure` |
| `umu_linux.go` | 209 | 组合根：umu/wineprefix 单例 + `ensureRuntime` 编排（无法再下沉，见 PLAN §"关键设计决策" 4.2 的 wineprefix→umu 单向依赖论证） |
| `display_linux.go` | 375 | **业务规则本身**（候选链选择逻辑），方案阶段 G 就设计成留在 internal，不是遗留 |
| `preflight_linux.go` | 141 | 聚合逻辑，方案设计如此 |
| `prefix_windows.go` | 24 | 六个入口在 Windows 上的空实现 |
| `runtimeuser_linux.go` | 315 | 见下 §1，掺了不该在这的机制 |
| `sharedaccess_linux.go` | 385 | 见下 §1，几乎整个文件都是通用机制 |
| `vcredist_linux.go` | 486 | 见下 §2，核心编排该留，但有零星纯机制没搬 |

真正的缺口只有 §1、§2 两项。

---

## 1. Gap A：`pkg/shareacl` 从未创建

方案 §2 原本规划了 `pkg/shareacl`（`shareacl.New() *Manager` + `.Prepare(path) error` +
`.Status(paths) Info`，把 `sharedaccess_linux.go` 的机制整体接走）。阶段 E 执行时**当场决定跳过**，
提交 `f2673eb` 的说明写得很清楚：

> shareacl 留在 internal/runner 未拆——见 sharedaccess_linux.go 现状，其目录清单与 ACL 判断业务
> 规则耦合更深，本阶段判断保留原状不强拆

回头核对代码，这个判断值得重新考虑。`sharedaccess_linux.go` 里：

- **纯机制**（不认识 ASA/实例/mirror，只认识"给一棵目录树 chgrp+setgid+POSIX 默认 ACL，
  没有 setfacl 就退化成 chown"）：`chgrpSetgidTree`、`applyDefaultACL`、`classifyACLError`、
  `defaultACLMissing`、`aclSupported`、`findAdminTool`、`runtimeGroupName`、`errACLUnsupported`——
  约 260 行，占全文件三分之二。
- **业务规则**（认识"哪些目录要共享写"、认识 `runner.Config`/`Problem` 类型、要拼中文提示文案）：
  `sharedSubtrees`（2 行路径清单）、`sharedTrees`、`sharedAccessStatus`（诊断聚合）、
  `prepareSharedTree`（编排入口）、`checkACLSupport`（Preflight 文案）。

另外 `runtimeuser_linux.go` 里的 `sharedAccessNeeded`（抽样判断要不要重新跑 ACL 全量）和
`ensureWorldReadExec`（把只读子树设成全员可读可穿越，用于 proton/umu-launcher 目录）同样是通用
机制，只是因为调用方在 `reconcileRuntimeOwnership` 里而被放错了文件。

**提议**：

1. 新建 `pkg/shareacl`，签名沿用方案原文：
   ```go
   func New() *Manager
   func (m *Manager) Prepare(root string, uid, gid int, group string) error  // chgrpSetgidTree + applyDefaultACL，降级 chown 走 sysuser.ChownTreeAs
   func (m *Manager) NeedsPass(root string, gid int) bool                    // 原 sharedAccessNeeded
   func (m *Manager) DefaultACLMissing(root, group string) bool             // 原 defaultACLMissing
   func (m *Manager) Supported(dir, group string) error                    // 原 aclSupported
   ```
   `findAdminTool`/`runtimeGroupName`/`errACLUnsupported`/`classifyACLError` 作为包内私有实现细节。
   不需要 `New(cfg)` 持有配置——这一层完全无状态，跟 `pkg/linuxdeps`/`pkg/steamrt` 一样是自由函数
   集合即可，`New()` 都不必要，直接导出包级函数。
2. `internal/runner/sharedaccess_linux.go` 瘦身到只剩 `sharedSubtrees`/`sharedTrees`/
   `sharedAccessStatus`/`prepareSharedTree`/`checkACLSupport`，全部改调 `shareacl.*`。
3. `ensureWorldReadExec` 挪到 `pkg/fsutil`（改名 `EnsureWorldReadable`）——它是"整棵目录树按需
   `chmod` 成全员可读可穿越"，跟 `fsutil.CopyDir`/`CopyFile` 是同一类"通用文件树操作"，不需要
   知道"运行时降权用户"这个概念，`runtimeuser_linux.go` 只需要调 `fsutil.EnsureWorldReadable(dir)`。
4. `sharedAccessNeeded` 随 `Prepare`/`NeedsPass` 一起搬进 `pkg/shareacl`，
   `reconcileRuntimeOwnership`（`runtimeuser_linux.go`）改调 `shareacl.NeedsPass(...)`。

预期效果：`sharedaccess_linux.go` 从 385 行降到 ~90 行（纯业务：目录清单+文案+编排），
`runtimeuser_linux.go` 从 315 行降到 ~260 行。

**落地说明（2026-09-05）**：与提议略有出入，均为执行时的具体化，不改变结论——

- `pkg/shareacl` 做成无状态包级自由函数（`Prepare`/`NeedsPass`/`DefaultACLMissing`/`Supported`/
  `GroupName`），没有 `New()`/`*Manager`：这一层确实不持有任何跨调用状态，连 `pkg/sysuser` 那种
  "配置值缓存"都没有，包级函数比强加一个空壳 `Manager`更直接。
  `Prepare` 的降级路径以 `ChownFallback func(root string, uid, gid int) error` 回调注入而非硬编码
  `sysuser.ChownTreeAs`——保持 `pkg/shareacl` 对 `pkg/sysuser` 零依赖，日志措辞（"POSIX ACLs
  unavailable on %s..."）也留给调用方决定。
- `findAdminTool` 确实各自留了一份小拷贝（`pkg/shareacl` 内部 + `internal/runner/sharedaccess_linux.go`
  诊断用），没有导出成 `shareacl.FindAdminTool`——理由同 `pkg/sysuser` 那份：三个用途不同，共享
  没有实际收益。
- `TestChgrpSetgidTree`/`TestDefaultACLMissing`/`TestNeedsPass` 三个测试原样搬进
  `pkg/shareacl/shareacl_test.go`，全部只在 `t.TempDir()` 内操作、真实调用 `setfacl`/`getfacl`
  （工具缺失时自行 skip）——不需要 §4 提到的 `ASA_TEST_SHAREACL` 门控：它们不像
  `pkg/sysuser`/`pkg/xvfb` 的真机集成测试那样创建系统级资源（用户账号、Xvfb 进程），风险不对等。

---

## 2. Gap B（可选，代码卫生，收益较小）：`vcredist_linux.go` 里几个零散的纯机制

`vcredist_linux.go` 的核心编排（`ensureVCRedist`/`runInPrefix`）留在 `internal/runner` 是阶段 H
就定好的**设计**，不是遗留——它深度依赖 `prefixDir`/`prefixKeyFor`/`acquireDisplay`/
`resolveRuntimeCredential`/`runtimeEnv`/`umuInterpreter`，这些全是还留在 `internal/runner` 的
业务概念，见 `docs/RUNNER_INSTANCE_PACKAGE_SPLIT_PLAN.md` 阶段 H 一节与 `CLAUDE.md` 对应条目。
不建议现在硬拆这一块。

但文件里仍有几个**完全通用、零 ASA 知识**的小函数，属于"顺手清理"级别，优先级低于 Gap A：

| 函数 | 现状 | 建议去处 | 理由 |
|---|---|---|---|
| `downloadProgress`/`mib` | 把 `pkg/download.Options.Progress` 的字节回调节流成人可读的百分比行 | `pkg/download`，导出为 `download.ProgressLogger(label string, logf func(string, ...any)) func(done, total int64)` | `pkg/download` 本来就定义了 `Progress` 回调这个概念，节流格式化理应跟着走，其它调用 `download.Fetch` 的地方（目前只有这一处，未来 syncthing/frp 更新也可能用到）能直接复用 |
| `resolveFinalURL` | HTTP HEAD 跟随重定向拿最终 URL | `pkg/download`，导出为 `download.ResolveFinalURL(ctx, url) (string, error)` | 同上，通用 HTTP 机制，跟"VC++"毫无关系 |
| `exitCodeOf` | `exec.ExitError` → `int`，-1 表示被信号杀 | `pkg/umu`，作为 `umu.ExitCode(err) int` | 与 `umu.NewOutputCapture`（同文件、同"跑一个 exe 拿输出"的场景）放一起最自然 |
| `classifyDLL`/`nativeDLLPresent` | 读文件头转调 `vcredist.ClassifyHeader` | 保留 | 已经是两行的薄封装，搬不搬都行，优先级最低——本轮未动 |

**落地说明（2026-09-05）**：`downloadProgress`/`mib` → `download.ProgressLogger`、
`resolveFinalURL` → `download.ResolveFinalURL`（新文件 `pkg/download/progress.go`）、
`exitCodeOf` → `umu.ExitCode`（`pkg/umu/umu_linux.go`，紧邻 `NewOutputCapture`），均按上表建议落地，
签名不变。`classifyDLL`/`nativeDLLPresent` 按计划保留在 `internal/runner/vcredist_linux.go`。

---

## 3. 建议执行顺序

1. **先做 Gap A**（`pkg/shareacl` + `ensureWorldReadExec` → `pkg/fsutil`）——这是方案里明确
   规划过、执行时却跳过的缺口，属于"补作业"而非"新想法"，值得单独一个 commit。
2. Gap B 可以合并进同一个 commit，也可以跳过不做——它不影响任何架构判断标准
   （`docs/INTERNAL_LAYOUT_MIGRATION.md` §9 的三条准入线），纯粹是"哪个文件更好找"的偏好。

---

## 4. 验证方式（沿用 PLAN §8）

- `go build ./... && go vet ./...`（Windows）
- `GOOS=linux GOARCH=amd64 go build ./... && go vet ./...`（交叉编译）
- WSL2 真机：`go build ./... && go vet ./... && go test $(go list ./... | grep -v '^asa-server/pkg/tail')`
- `pkg/shareacl` 的单测已经在 `t.TempDir()` 内真实调用 `setfacl`/`chgrp`（工具缺失时 skip），
  不需要额外的 `ASA_TEST_*` 门控——见 §1 落地说明。
