# 镜像去管理员化 与 WebAuthn 移除 —— 改造计划

> 两块**互相独立**的工作项，可并行也可分开排期，彼此无依赖：
>
> 1. **镜像去管理员化**：把 `createJunction` 换成真正的 NTFS junction，消除整套 Windows 提权逻辑。
> 2. **移除 WebAuthn**：只保留「密码 + TOTP 两步验证 + 恢复码」。
>
> 状态：**两部分均已实施完成**。
> 第一部分（镜像去管理员化）见提交 `refactor(mirror)`；第二部分（移除 WebAuthn）见提交 `refactor(auth)`。
> 实施中对本文的两处修正已就地标注：§1.3 的后果描述（原文夸大为数据丢失）与 §1.4 #7（`RunAsAdmin` 不能删）。
> 相关文档：[`LINUX_COMPATIBILITY_PLAN.md`](./LINUX_COMPATIBILITY_PLAN.md)（§5.6 / §10.10 与本文第一部分有交集）、
> [`AUTH_LOGIN_DESIGN.md`](./AUTH_LOGIN_DESIGN.md)（第二部分要同步修订）。

---

# 第一部分：镜像去管理员化（真 NTFS junction）

## 1.1 现状与问题

`internal/mirror/mirror.go:462` 的 `createJunction` **名叫 junction，实现却是 `os.Symlink`**：

```go
func createJunction(linkPath, targetPath string) error {
	// ...
	if err := os.Symlink(absTarget, linkPath); err != nil {
		return fmt.Errorf("failed to create junction %s -> %s: %w", linkPath, absTarget, err)
	}
	// ...
}
```

在 Windows 上，`os.Symlink` 对目录目标创建的是**目录符号链接**，需要
`SeCreateSymbolicLinkPrivilege`（管理员，或开启开发者模式）。而真正的 **NTFS junction**
（`mklink /J` / `FSCTL_SET_REPARSE_POINT`）**普通用户就能创建**。

于是出现一个不对称：

| 条目类型 | 函数 | 失败时行为 |
|---|---|---|
| 文件 | `createFileSymlink`（`mirror.go:484`） | ✅ 回退 `fsutil.CopyFile`，功能正常 |
| **目录** | `createJunction`（`mirror.go:462`） | ❌ 直接返回 error |

目录侧的错误会一路上抛：
`processDirectory`（`mirror.go:374`）→ `filepath.Walk` 的回调 `return err`（`mirror.go:233-236`）
→ `createInstanceMirror` 失败 → `_ = CleanupInstanceMirror(instanceName)` 回滚整个镜像。

**结论**：非管理员且未开开发者模式时，实例**根本起不来**，
而不是 `main.go` 警告里说的「用文件复制模式起来」——那句话只对文件成立。

## 1.2 已实证的前提

在**非管理员** shell（Windows 11 / Go 1.27.0）下实测：

| 操作 | 结果 |
|---|---|
| `mklink /J`（真 NTFS junction） | ✅ 创建成功 |
| `mklink /D`（目录符号链接，等价于 `os.Symlink`） | ❌ 「您没有足够的权限执行此操作」 |

**换成真 junction 后，建目录链接不再需要任何特权。前提成立。**

配合业务侧的演进——可执行文件与 DLL 因启动期报错已改为**复制**而非链接
（`processFile` 里 `isUnderWin64(relPath)` → `fsutil.CopyFile`，`mirror.go:421-423`），
**现在需要建链接的实质上只剩目录**——管理员权限对镜像功能已不再必需。

## 1.3 ⚠️ 最大的连带风险：junction 的**识别**必须同步改

这是本次改造真正的难点，不是 `createJunction` 本身。

同一次实测（Go 1.27.0）：

| 类型 | `ModeSymlink` | `ModeIrregular` | `IsDir()` | `FILE_ATTRIBUTE_REPARSE_POINT` | `os.Readlink` |
|---|---|---|---|---|---|
| 真 junction（`mklink /J`） | **false** | **true** | false | true | ✅ 返回目标 |
| 目录 symlink（`os.Symlink`） | true | false | false | true | ✅ 返回目标 |
| 普通目录 | false | false | true | false | ✗ not a reparse point |

而 `isJunctionOrSymlink`（`mirror.go:430-437`）**只查 `ModeSymlink`**：

```go
// os.ModeSymlink 检测符号链接和 junction      ← 这句注释在 Go 1.23+ 上已经是错的
if fi.Mode()&os.ModeSymlink != 0 {
```

Go 1.23 起，Windows 上的 mount point（junction）不再被报告为 symlink，改报 `ModeIrregular`。
**所以只改 `createJunction` 而不改 `isJunctionOrSymlink`，同步逻辑会持续出错。**

> ⚠️ **本节初稿把后果写成了「会穿过 junction 把源文件删掉」，那是错的，已按实测更正。**
> 实测方式：把 `isJunctionOrSymlink` 改回只查 `ModeSymlink`，跑 `TestSyncDoesNotDeleteThroughJunctions`。

真实后果如下：

| 调用点 | 识别失效后的后果 |
|---|---|
| `collectMirrorEntries` | junction 被归成 `EntryTypeFile`（源侧意图是 `EntryTypeSymlink`），**每轮同步都判类型不匹配、把所有 junction 删掉重建** |
| `reconcileEntry` | 顺着上一条，对着一个目录调 `fsutil.CopyFile`，报 `Incorrect function`；同步返回错误后触发整个镜像重建 |
| `migrateExceptionJunctions`（`mirror.go:281`） | 该处另有 `!fi.IsDir()` 兜底（`os.Lstat` 对 junction 返回 `IsDir()==false`），**不会误迁移** |
| `CleanupInstanceMirror` / `removeMirrorEntry` | junction 落到 `os.Remove` 分支，**只删链接本身**，源目标安全 |

**为什么不会删到源**：`os.Lstat` 对 junction 返回 `IsDir()==false`，`filepath.Walk` 因此**不会递归进 junction**，
`migrateExceptionJunctions` 的 `!fi.IsDir()` 也拦得住。这层结构性保护与 `isJunctionOrSymlink` 无关，
所以识别失效表现为**性能与稳定性问题**，而不是数据丢失。

即便如此，**这两处仍应在同一个提交里一起改** —— 分开上线会留下一个每次同步都全量重建 junction、
且日志里刷满 `Incorrect function` 的中间版本。

**✅ 定案：用 `os.Readlink` 判定。**

| 方案 | 实现 | 结论 |
|---|---|---|
| **A** | `os.Readlink(path)` 成功即视为链接 | ✅ **采用**。跨平台、不依赖 `Mode` 语义在 Go 版本间的漂移、Linux 侧同样正确；开销与 `Lstat` 同量级 |
| B | `fi.Mode()&(os.ModeSymlink\|os.ModeIrregular) != 0` | ❌ `ModeIrregular` 语义偏宽，其他 reparse 点（如 OneDrive 占位文件、去重块）也会命中 |
| C | `windows.GetFileAttributes` 查 `FILE_ATTRIBUTE_REPARSE_POINT` | ❌ 判据最权威，但要为此拆 `_windows.go`/`_linux.go`，收益不抵成本 |

实测三种路径下 `os.Readlink` 的表现完全符合需要：真 junction ✅ 返回目标、目录 symlink ✅ 返回目标、
普通目录 ✗ 报 `not a reparse point`。实现：

```go
// isJunctionOrSymlink 判断路径是否是链接（NTFS junction / 符号链接）。
//
// 不用 os.ModeSymlink 判定：Go 1.23 起 Windows 的 mount point（junction）
// 报 ModeIrregular 而非 ModeSymlink，只查 ModeSymlink 会把真 junction 漏判成普通目录，
// 进而让增量同步穿过它删到源目录。Readlink 对两种链接都成功、对普通目录/文件都失败。
func isJunctionOrSymlink(path string) bool {
	_, err := os.Readlink(path)
	return err == nil
}
```

## 1.4 改造清单

| # | 位置 | 改动 | 备注 |
|---|---|---|---|
| 1 | `mirror.createJunction`（`mirror.go:462`） | 换成真 NTFS junction | 见下方实现选型 |
| 2 | `mirror.isJunctionOrSymlink`（`mirror.go:430`） | 改为能识别真 junction | **与 #1 同一提交**，见 §1.3 |
| 3 | `mirror.createFileSymlink`（`mirror.go:484`） | 简化为直接 `CopyFile`，并删掉 `reconcileEntry` 的 copy-fallback 特例（`mirror.go:1028-1045`） | 实测只影响 11 个文件 / 110 MB，见下 |
| 4 | `mirror.IsElevated()`（`mirror.go:95`）及 `elevated`/`elevatedErr`/`once` 全局变量 | 删除 | 唯一用途就是这套提权 |
| 5 | `main.go:273 ensureAdminElevation()` / `buildElevatedArgs()` / `quoteArg()` | 删除 | 连同 `main.go:194` 的调用 |
| 6 | `main.go` 的 `--no-admin` 标志与 `hasArgFlag` | 删除 | 提权没了，开关失去意义 |
| 7 | ~~`pkg/winproc.RunAsAdmin`~~ | ❌ **保留** | 实施时发现 `internal/certmgr/cli.go:67` 也在用它（`cert install --machine` 写 `LocalMachine\Root` 确实需要管理员）。本项计划有误，已放弃 |
| 8 | `certmgr.IsElevated()`（`store.go:210`） | **保留** | 写 `LocalMachine\Root` 证书存储仍需管理员，与镜像无关 |
| 9 | `CLAUDE.md` / `LINUX_COMPATIBILITY_PLAN.md` §5.6、§10.10 | 同步措辞 | 「uses NTFS junctions」到这时才名副其实 |

### #1 的实现：**✅ 定案用 `DeviceIoControl`**

| 方案 | 做法 | 结论 |
|---|---|---|
| **A** | `DeviceIoControl` + `FSCTL_SET_REPARSE_POINT` + `IO_REPARSE_TAG_MOUNT_POINT` | ✅ **采用**。纯 `golang.org/x/sys/windows`，无子进程、无 locale 依赖 |
| B | `cmd /c mklink /J` | ❌ 每条链接一个子进程；输出受系统语言影响（本次实测中文系统下报错信息即为 GBK 乱码）；参数需转义 |

步骤与注意点：

1. `os.MkdirAll(linkPath)` 建一个**空目录**（junction 必须建在空目录上）
2. `windows.CreateFile(linkPath, GENERIC_WRITE, ..., FILE_FLAG_BACKUP_SEMANTICS|FILE_FLAG_OPEN_REPARSE_POINT, ...)`
3. 构造 `REPARSE_DATA_BUFFER`：`ReparseTag = IO_REPARSE_TAG_MOUNT_POINT`，
   `SubstituteName` 用 NT 路径形式 **`\??\D:\path\to\target`**，`PrintName` 用显示路径 `D:\path\to\target`，
   两者均为 UTF-16 且各带 NUL 结尾
4. `windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, ...)`
5. **失败要 `os.Remove(linkPath)` 回滚**那个空目录，否则留下半成品会让后续同步误判

已核实 `golang.org/x/sys/windows@v0.47.0` 提供了所需常量：
`FSCTL_SET_REPARSE_POINT`(`types_windows.go:1983`)、`IO_REPARSE_TAG_MOUNT_POINT`、
`FILE_FLAG_OPEN_REPARSE_POINT`(`types_windows.go:136`)、`FILE_FLAG_BACKUP_SEMANTICS`。
但 `reparseDataBuffer` 结构体是**未导出**的（`types_windows.go:1933`），**需要自己定义一份**。

平台拆分：Linux 上 `os.Symlink` 本就免特权，所以 `createJunction` 要拆
`mirror_windows.go` / `mirror_linux.go` —— 与 `LINUX_COMPATIBILITY_PLAN.md` 的 P0 阶段合流，建议一起做。

### #3 文件符号链接：**不是「所有文件改成复制」**

先澄清一个容易误解的点：**镜像里绝大多数文件根本不参与「链接还是复制」的选择**。
`processFile`（`mirror.go:408`）把文件分成三类，只有第三类才走 `createFileSymlink`：

| 类 | 判据（`processFile` 分支） | 处理 | 在真实安装中的量 |
|---|---|---|---|
| ① **不进镜像** | 父目录已是 junction → `return nil`（`mirror.go:416`） | 不复制、不链接，**通过父目录的 junction 直接访问** | `Engine/`、`steamapps/`、`ShooterGame/Content/` 等，**约 11 GB，占绝大部分** |
| ② **完整复制** | `isUnderWin64(relPath)` → `fsutil.CopyFile`（`mirror.go:421`） | 真实副本，隔离启动期缓存 | `Win64/` 全部，**894 MB** —— exe 与 DLL 都在这里，**早已是复制，本次不变** |
| ③ **文件符号链接** | 其余 → `createFileSymlink`（`mirror.go:426`） | `os.Symlink`，失败回退 `CopyFile` | **只有 11 个文件，110 MB**（见下） |

在 `E:\asa_server_data\server-files`（12 GB，实测）上枚举第③类的全部成员 ——
它们全部落在 `server-files/` 根目录，`ShooterGame/`、`ShooterGame/Binaries/`、
`ShooterGame/Saved/`、`ShooterGame/Saved/Config/` 这几个「真实目录」里**一个散落文件都没有**：

```
  48.0 MB  Manifest_UFSFiles_Win64.txt
  23.2 MB  steamclient64.dll
  19.7 MB  steamclient.dll
  12.4 MB  steamwebrtc64.dll
   4.5 MB  steamwebrtc.dll
   0.6 MB  vstdlib_s.dll      0.5 MB  vstdlib_s64.dll
   0.4 MB  tier0_s64.dll      0.3 MB  tier0_s.dll
   0.0 MB  Manifest_DebugFiles_Win64.txt / Manifest_NonUFSFiles_Win64.txt
  ────────────────────────────────────────────
  合计 11 个文件 / 110 MB
```

**所以「文件都不链接了吗、全部使用复制吗」的答案是：**
① 类（约 11 GB）本来就既不链接也不复制，走 junction 访问，**不受影响**；
② 类（894 MB）本来就是复制，**不受影响**；
真正被这项改动波及的，**只有第 ③ 类这 11 个文件、110 MB**。

#### 为什么这 11 个也要改成复制

**因为 Windows 上文件符号链接同样需要 `SeCreateSymbolicLinkPrivilege`。**
去掉提权逻辑（#4–#7）之后，`os.Symlink` 对这 11 个文件**必然失败**，
每次建镜像都会白跑 11 次注定失败的系统调用、写 11 条 debug 日志，
最后仍旧回退到 `fsutil.CopyFile`。留着它就是一段**永远走不通的死代码**。

#### 代价与收益

| | 数值 |
|---|---|
| 每实例镜像增加 | **+110 MB**，在现有 894 MB（Win64 复制）基础上约 **+12%** |
| 5 个实例合计增加 | 约 550 MB |
| 增量同步增加 | 这 110 MB 的 MD5 计算（`reconcileEntry`，`mirror.go:1053`） |

> 上述代价**非管理员用户今天就已经在付**——他们本来就走 CopyFile 回退。
> 这项改动只是让管理员用户与之对齐，换来两种权限下行为完全一致。

附带收益：`reconcileEntry`（`mirror.go:1028-1045`）里那段
「source=Symlink 但 mirror=File，这是无权限时 fallback 到 copyFile 的合法结果」的特例分支
**可以整段删掉** —— 源侧意图类型不再有 `EntryTypeSymlink` 的文件，两边永远同为 `EntryTypeFile`，
走统一的 MD5 比对路径。这段特例正是当年为了兼容「有时链接、有时复制」而写的，
一旦行为统一它就没有存在理由了。

#### 被否的替代方案：NTFS 硬链接

`CreateHardLinkW` 同样免特权、且**不额外占空间**，镜像目录
（`{BaseDir}/server-files-tmp-<name>`，`mirror.go:117`）与 `server-files` 同在 `BaseDir` 下、
必然同卷，硬链接的技术前提是成立的。但仍然否掉：

1. **共享同一份内容**：任何进程写镜像侧的文件都会直接污染 `server-files` 源。
   这 11 个文件虽然运行期只读，但这是一条没有防护的路径。
2. **省下的空间会随更新流失**：SteamCMD 更新若以「删除+重建」方式替换源文件，
   硬链接会保留旧内容；`reconcileEntry` 的 MD5 比对发现不一致后会 `CopyFile` 覆盖，
   于是硬链接退化成普通副本 —— 空间收益不可持续。
3. 为 110 MB 引入一套额外机制与上述风险，不划算。

## 1.5 验收

1. **在普通（非管理员）账户下**：创建实例 → 启动 → 玩家可连入 → 停止，全流程通过。
2. 用 `fsutil reparsepoint query <镜像目录>` 确认建出来的是 **mount point**（`0xA0000003`）而非 symlink。
3. **回归重点（§1.3 的兜底）**：多实例并发同步 + 服务端更新后的增量同步，
   跑完确认 `server-files/` 下**没有任何文件被误删**。建议改造前先对源目录做一次全量快照用于比对。
4. **兼容旧镜像**：升级前用旧版本创建的镜像里是 `os.Symlink`，新代码必须能正确识别与清理，
   两种形态在过渡期并存。
5. 启动过程无 UAC 弹窗、无提权重启。

## 1.6 收益

- 去掉 Windows 提权重启：启动更快、无 UAC 弹窗、可在受限账户与 CI 环境下运行。
- 安装器与首次引导对管理员的依赖减半（只剩「注册服务」和「装本地 CA」两项）。
- 与 `LINUX_COMPATIBILITY_PLAN.md` §5.6 合流：**两个平台都免特权建链接**，行为终于一致。

---

# 第二部分：移除 WebAuthn

## 2.1 目标与保留边界

移除 WebAuthn / passkey，保留 **密码 + TOTP 两步验证 + 恢复码** 这套已经够用的组合。

已核实：`internal/auth/totp.go` 对 webauthn **零引用**，两者完全独立，移除不影响两步验证。

## 2.2 ✅ 零锁死风险（可以放心做的根本原因）

`internal/auth/user.go:34` 的注释写得很清楚：

> `PasswordHash` 恒非空：WebAuthn 只是补充，任何账户都必须能用密码登录。

**不存在「只有 passkey、没有密码」的账户**，所以移除后不会有任何用户被锁在门外。
这是这项改造风险可控的前提，实施前建议再用 `asa-server user list` 抽查确认。

## 2.3 改动清单

### 整文件删除（合计 1281 行）

| 文件 | 行数 |
|---|---|
| `internal/auth/webauthn.go` | 369 |
| `internal/auth/webauthn_domain.go` | 122 |
| `internal/auth/webauthn_domain_test.go` | 187 |
| `internal/auth/ceremony.go` | 87（纯 WebAuthn，直接 import `go-webauthn/webauthn`） |
| `internal/webapi/authapi/webauthn.go` | 77 |
| `internal/webapi/authapi/webauthn_handler.go` | 439 |

### 局部清理

| 文件 | 引用数 | 要点 |
|---|---|---|
| `internal/appconfig/config.go` | 14 | 删 `WebAuthnConfig` 结构与默认值 |
| `internal/appconfig/validate.go` | 11 | 删 `WebAuthnConfig.validate()`（`:138`）与 `NormalizeWebAuthnDomain`（`:173`），以及 `:66` 的调用 |
| `internal/appconfig/template.go` | 10 | 删 config.yaml 模板里的 `webauthn:` 段 |
| `internal/appconfig/config_test.go` | — | 同步 |
| `internal/webapi/authapi/handler.go` | 10 | 删 `registerWebAuthnRoutes`（`:44`）与登录响应的三个字段 `webauthn_available/reason/rp_id`（`:72-74`、`:134-136`） |
| `internal/webapi/authapi/middleware.go` | 5 | — |
| `internal/webapi/authapi/users.go` | 5 | 删 `POST /:username/webauthn/reset`（`handler.go:55`） |
| `internal/auth/user.go` | 9 | `webauthn_handle` 列的读写，见 §2.4 |
| `internal/auth/audit.go` | 3 | 见 §2.5 |
| `internal/auth/token.go` | 2 | — |
| `internal/auth/db.go` | 1 | — |

### 前端

| 文件 | 引用数 |
|---|---|
| `app/src/views/Profile.vue` | 32（最碎，passkey 管理界面主要在这里） |
| `app/src/views/Login.vue` | 16 |
| `app/src/views/UserManager.vue` | 5 |

### 依赖

`go.mod` 移除 `github.com/go-webauthn/webauthn v0.17.4`。
`go mod tidy` 后预期一并消失：`go-webauthn/x`、`fxamacker/cbor/v2`、`google/go-tpm`、`x448/float16`。
（`google/uuid` 可能被其他包使用，以 tidy 实际结果为准。）

## 2.4 ⚠️ 数据库迁移：**不要删除 m002**

`internal/auth/migrations.go:5` 明确约定：「migrations 必须按 Version 升序排列，且**只允许在末尾追加**」。
现有：`v1 initial_schema`、`v2 webauthn_credentials`。

**绝对不能删除 m002**——已部署的 `auth.db` 记录着 `version = 2`，删掉会让版本账目对不上；
且 `internal/auth/migrate.go:58` 有降级检测（报「这通常意味着 asa-server.exe 被降级了」）。

**✅ 定案：方案 A。**

| 方案 | 做法 | 结论 |
|---|---|---|
| **A** | 保留 m002 原样；**追加 m003**：`DROP TABLE webauthn_credentials` + 删 `idx_wa_credid`/`idx_wa_user` | ✅ **采用**。清掉无用数据、版本账目正确；旧版二进制会正确拒绝启动 |
| B | 什么都不动，表留着当孤儿 | ❌ 最安全但留脏数据 |
| C | 连 `users.webauthn_handle` 列一起删 | ❌ SQLite 删列要重建表，且该列上还挂着 `idx_users_handle` 唯一索引，风险远大于收益 |

选 A 时注意：**`users.webauthn_handle` 列保留在表上**，但
`userColumns`（`user.go:63`）与两处 `rows.Scan`（`user.go:298`、`user.go:316`）
**建议直接把该列从查询里去掉**——改动最小，且完全不碰 schema。

## 2.5 审计日志的历史数据

`audit_log` 表里会残留 WebAuthn 相关的 action 字符串（注册 / 认证 / 重置凭证）。移除代码后：

- **不要**在读取端做穷举枚举校验，否则历史行会渲染成错误甚至直接报错；
- 审计查询与展示要能容忍未知 action（原样显示即可）。

`asa-server user audit` 与前端审计页都要过一遍。

## 2.6 配置兼容

已有 `config.yaml` 里的 `auth.webauthn:` 段，在删掉对应结构体之后：

- viper 对**未知键**默认不报错、只是忽略 → 老配置文件**不会**导致启动失败；
- 相关校验一并删除后，写错的 webauthn 配置也不再有提示 —— 这是预期的。

**建议**：升级后首次启动时若检测到 `auth.webauthn` 键仍存在，
打一条 INFO 日志说明该功能已移除、可以删掉这一段，而不是完全静默地忽略。

## 2.7 文档同步

- `docs/AUTH_LOGIN_DESIGN.md` —— WebAuthn 章节整体删除，或保留一句「已于 vX 移除」的历史说明
- `CLAUDE.md` —— `internal/auth` 的描述、以及「WebAuthn 只是密码登录的**补充**」整段
- `docs/INTERNAL_LAYOUT_MIGRATION.md` —— 提及处

## 2.8 验收

1. `auth.enabled = false`（默认）：中间件短路、不打开 `auth.db` —— 行为不变。
2. `auth.enabled = true`：密码登录、TOTP 两步验证、恢复码、修改密码、
   全设备登出（`session_version`）、单设备吊销（`token_denylist`）全部正常。
3. **从已有 WebAuthn 凭证的 `auth.db` 升级**：能正常启动，老用户能用密码 + TOTP 登录。
4. `asa-server db verify` / `db migrate` / `user list` / `user audit` 均正常。
5. 前端：登录页无 passkey 入口，Profile 页无残留区块，UserManager 无重置凭证按钮。

---

# 第三部分：实施顺序与工作量

| 工作项 | 估算 | 风险 | 说明 |
|---|---|---|---|
| 第二部分（移除 WebAuthn） | 1.5–2 天 | **低** | 基本是纯删除；最碎的是 `Profile.vue` 的 32 处 |
| 第一部分（镜像去管理员化） | 2–3 天 | **中高** | §1.3 的识别改造与回归验证占大头，涉及**源目录数据安全** |

**建议先做第二部分**：纯删除、风险低、能快速把 CI 和验收流程跑通，
再带着这套验收习惯去做第一部分。

**例外**：如果 `LINUX_COMPATIBILITY_PLAN.md` 的安装器 / 首次引导（§10）是当前主线，
那第一部分应当优先——它直接决定引导程序对管理员权限的依赖程度（§1.6），
早做能少一次返工。

## 与其他计划的交叉

- 第一部分的 `createJunction` 平台拆分，与 `LINUX_COMPATIBILITY_PLAN.md` **P0 阶段**是同一块工作面，建议合并。
- 第一部分完成后，`LINUX_COMPATIBILITY_PLAN.md` §5.6 里
  「Linux 上 symlink 免特权、比 Windows 更省事」的论述可以简化为「两平台都免特权」。
- 第一部分完成后，§10.7.5.1「引导拒绝提权即退出」的理由随之收窄——
  引导仍需管理员（注册服务、装 CA），但**镜像不再需要**，提示文案要相应修改。
