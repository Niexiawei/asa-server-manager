# 移除 WebAuthn：只保留密码 + TOTP + 恢复码（独立文档）

> 本文由 `MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md` 的第二部分于 2026-09-29 拆分为独立文档（该源文档自述「镜像去管理员化」与「移除 WebAuthn」是两块**互相独立**的工作项，故拆分）。
> 镜像部分见 [`MIRROR_STARTUP_PLAN.md`](./MIRROR_STARTUP_PLAN.md)。
> 📌 **实施顺序与工作量**（含本项的 1.5–2 天估算与「建议先做本项」的理由）见 `MIRROR_STARTUP_PLAN.md` 的「实施顺序与工作量」一节。

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

# 附录：拆分记录（2026-09-29）

本文件由 `docs/MIRROR_JUNCTION_AND_WEBAUTHN_REMOVAL_PLAN.md` 第二部分于 2026-09-29 原样拆出；正文未作任何改写。原文档的其余部分已并入 `docs/MIRROR_STARTUP_PLAN.md`。
