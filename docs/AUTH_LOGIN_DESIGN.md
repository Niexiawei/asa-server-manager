# 鉴权 + 登录页面 开发方案

> **状态：已实施**（六个阶段全部完成，见文末 §17 实施记录）
> 涉及包：新增 `appconfig/`、`auth/`、`webapi/authapi/`；改动 `webapi/actions.go`、`realtime/`、`main.go`、`actions/`、前端 `app/`
> 关联文档：`docs/HTTP2_CONNECTION_OPTIMIZATION.md`（TLS / h2 前提）、`docs/PACKAGE_RESTRUCTURE_PLAN.md`（分层约束）

---

## 0. 目标与约束

| 需求 | 落点 |
|------|------|
| 引入 viper + yaml 应用配置 | 新包 `appconfig`，配置文件 `{BaseDir}/config.yaml` |
| 鉴权可通过配置开关 | `auth.enabled: false`（默认关，保证升级不锁死存量用户） |
| 内网/本机免鉴权，仅反代出网需要鉴权 | `auth.lan_bypass.enabled`，默认 **false**（见 §4 的安全陷阱） |
| 两步验证（可选开启） | `github.com/pquerna/otp`，**按用户**开关，全局可强制 |
| **WebAuthn / FIDO2（可选开启，密码登录的补充）** | `github.com/go-webauthn/webauthn`；`auth.webauthn.domains` 命中当前请求域名才启用，任何条件不满足则**静默退回密码登录**，见 §7.1 |
| **只有用户模块用 SQLite，其余持久化不动** | `auth.db` 只放鉴权表；边界规则见 §3.4 |
| **CLI 数据库迁移命令** | `asa-server db migrate`，见 §11.1 |
| **CLI 重置用户密码** | `asa-server user passwd`，见 §11.2 |
| 前端登录页 + 用户管理页 | `app/src/views/Login.vue`、`UserManager.vue` 等 |
| 后端对应接口 | `webapi/authapi`，`/api/auth/*`、`/api/users/*` |
| WebSocket 鉴权失败断开会不会连接风暴 | **会**，按现有重连实现必然会。§9 给出完整规避方案 |

硬约束：

1. **不能破坏 SSE / WebSocket。** 项目大量依赖 `EventSource` 和浏览器 `WebSocket`，两者都**无法设置自定义请求头**。因此 `Authorization: Bearer` 方案在本项目不成立 —— 鉴权凭证必须走 **Cookie**（唯一在 REST / SSE / WS 三条链路上行为一致的载体）。
2. **不能破坏分层。** `appconfig` 是叶子包（只依赖 `pkg/*`），`auth` 依赖 `appconfig` + `pkg/*`，gin 相关的中间件与 handler 放 `webapi/authapi`。
3. **不能让服务起不来。** 配置文件缺失 / 损坏 / 无用户，都要有可用的降级路径（见 §10 首次启动引导）。
4. **SQLite 的引入范围严格限定在鉴权。** 见 §3.4。

---

## 1. 整体架构

```
appconfig/                 # viper + yaml，叶子包，无领域依赖
├── config.go              # Config 结构体 + Load/Get/Save + 默认值
├── watch.go               # WatchConfig 热重载（仅热重载可安全变更的字段）
└── config_test.go

auth/                      # 纯领域：用户、密码、令牌、TOTP、WebAuthn、限流、审计。不依赖 gin
├── db.go                  # SQLite 打开/关闭 + PRAGMA + 连接池设置
├── migrate.go             # 迁移引擎：schema_version + 有序迁移列表 + 降级保护
├── migrations.go          # 具体迁移函数（只追加，永不修改已发布的）
├── cache.go               # 用户/denylist 的内存副本（中间件热路径零 I/O）
├── user.go                # User 模型 + Create/Get/List/Update/Delete
├── password.go            # bcrypt 哈希与校验
├── token.go               # HMAC-SHA256 签名令牌：签发 / 校验 / 版本吊销
├── denylist.go            # jti 吊销表（单设备登出），过期行定期清理
├── totp.go                # pquerna/otp 封装：注册、校验、防重放、恢复码
├── webauthn.go            # go-webauthn 封装：RPID 解析、注册/登录仪式、凭证 CRUD
├── ceremony.go            # WebAuthn SessionData 的短时存储（内存 + TTL）
├── ratelimit.go           # 登录失败计数与锁定（持久化，重启不清零）
├── audit.go               # 登录/改密/凭证变更审计日志，滚动窗口
├── netmatch.go            # 内网 CIDR 判定（纯函数，无 DB）
└── *_test.go

webapi/authapi/            # gin 接入层
├── middleware.go          # Middleware()：鉴权闸门（REST/SSE/WS 共用）
├── handler.go             # /api/auth/* 路由
├── webauthn.go            # /api/auth/webauthn/* 路由
├── users.go               # /api/users/* 路由（用户管理）
└── setup.go               # 首次运行引导

actions/                   # CLI 子命令（沿用现有包）
├── auth_db.go             # asa-server db status|migrate|verify|vacuum|backup
└── auth_user.go           # asa-server user list|add|passwd|...

app/src/
├── views/Login.vue        # 登录页（密码 / Passkey / TOTP）
├── views/UserManager.vue  # 用户管理页
├── views/Profile.vue      # 个人：改密码、2FA、Passkey 管理
├── store/authStore.js     # 当前用户 / 鉴权状态 / 是否需要登录
├── apis/authApi.js
└── router/index.js        # 全局路由守卫
```

依赖方向（并入现有分层图）：

```
pkg/*            → appconfig → auth → webapi/authapi → webapi
                              ↑
                     realtime（WS 鉴权需要 auth.VerifyToken）
```

`realtime` 依赖 `auth` 不会成环：`auth` 只依赖 `appconfig` + `pkg`。

---

## 2. `appconfig` —— viper + yaml

### 2.1 依赖

```bash
go get github.com/spf13/viper
go get github.com/pquerna/otp
go get github.com/go-webauthn/webauthn
# bcrypt 来自 golang.org/x/crypto（已在 go.mod）
# golang.org/x/term 用于 CLI 无回显读密码（新增，很小，纯 Go）
go get golang.org/x/term
# modernc.org/sqlite 无需 go get —— 已在依赖图中，见 §3.3
```

### 2.2 配置文件位置与优先级

```
命令行 flag  >  环境变量 ASA_*  >  {BaseDir}/config.yaml  >  内置默认值
```

- 文件路径：`{BaseDir}/config.yaml`（与 `schedules.json` 同级，用户可手改）
- 环境变量：`viper.SetEnvPrefix("ASA")` + `SetEnvKeyReplacer(strings.NewReplacer(".", "_"))`，
  即 `auth.enabled` ⇄ `ASA_AUTH_ENABLED`
- flag 绑定：现有 `main.go` 的 `--api-port` / `--tls` 等继续用 `urfave/cli` 的 `Destination`，
  在 `appconfig.Load()` **之后**、`webapi.NewAPIServer()` **之前**做一次「flag 显式设置则覆盖 config」的合并。
  `urfave/cli` v3 可用 `cmd.IsSet("api-port")` 判断是否显式传入。

> **不要**把现有 `config`（cfgpkg，目录布局 + InstanceConfig）改成 viper。那是实例级 INI 配置，
> 与应用级 yaml 配置是两件事，混在一起会把 `config` 包重新变成神包。

### 2.3 配置结构

`{BaseDir}/config.yaml`：

```yaml
server:
  port: 19193
  tls:
    enabled: true
    trust_local_ca: true
    cert_file: ""
    key_file: ""
    domains: []            # 反代对外域名，写进证书 SAN；也是 WebAuthn RP ID 的默认来源
  trusted_proxies:         # 允许设置 X-Forwarded-For 的来源；空 = 谁都不信
    - 127.0.0.1
    - ::1
  cors:
    allowed_origins: []    # 空 = 仅同源。反代域名要写进来，例如 https://ark.example.com

auth:
  enabled: false           # 总开关。false 时中间件完全短路，行为与现在一致

  database:
    path: ""               # 空 = {BaseDir}/database_file/auth.db
    auto_migrate: true     # 启动时自动应用待执行迁移（见 §11.1 的失败处理）

  session:
    ttl: 168h              # 令牌有效期（7 天）
    idle_timeout: 24h      # 空闲多久失效（滑动续期，0 = 不启用）
    cookie_name: asa_session
    cookie_path: /
    # secure 不在这里配：TLS 开启时自动 Secure=true，关闭时自动 false
    same_site: lax         # lax | strict | none（none 必须配合 TLS）

  lan_bypass:
    enabled: false         # ⚠️ 默认关。开启前务必读 §4 的陷阱
    networks:              # 视为「内网」的网段；留空则用下面的内置默认集
      - 127.0.0.0/8
      - ::1/128
      - 10.0.0.0/8
      - 172.16.0.0/12
      - 192.168.0.0/16
      - 169.254.0.0/16
      - fc00::/7
      - fe80::/10
    deny_if_forwarded: true  # 带任何反代痕迹的请求一律不放行，见 §4.3

  totp:
    enabled: true          # 全局允许两步验证功能
    required: false        # true = 所有用户必须绑定
    issuer: "ASA Server Manager"
    skew: 1                # 允许 ±1 个 30s 时间窗

  # WebAuthn 是密码登录的补充，永远不是替代。任何条件不满足都静默退回密码登录。
  webauthn:
    enabled: false
    # ★ 域名闸门：只有当前请求的域名命中这个列表，WebAuthn 才启用。
    #   留空 = 不对任何请求生效（效果等同 enabled: false，启动时会给出 WARN）。
    #   本机使用必须显式写 localhost —— 不做任何自动推导。
    #   IP 地址不是合法 RP ID，写进来会在配置校验阶段直接报错。
    #   写父域名（example.com）可让其所有子域名共享同一套凭证；
    #   ⚠️ 改动这个列表会让已注册的凭证失效，见 §7.2。
    domains: []
      # - localhost
      # - ark.example.com
    rp_display_name: "ASA Server Manager"
    extra_origins: []          # 额外允许的 Origin（一般不填，默认由 domains + 端口推导）
    discoverable_login: true   # 登录页是否提供「使用 Passkey 登录」入口（需驻留密钥）
    user_verification: required  # discouraged | preferred | required
    satisfies_2fa: true        # 经过 UV 的 Passkey 登录视为已完成两步验证，不再要 TOTP
    clone_detection: warn      # off | warn | disable_credential

  password:
    min_length: 8
    bcrypt_cost: 12
    # 注意：没有「关闭密码登录」的开关。每个账户恒有密码，
    # 这是 WebAuthn 不可用（域名不匹配、IP 访问、认证器丢失）时的唯一兜底。

  ratelimit:
    max_failures: 5        # 连续失败次数
    window: 15m            # 统计窗口
    lockout: 15m           # 锁定时长

  audit:
    max_rows: 2000         # 审计日志滚动保留条数
```

Go 结构体用 `mapstructure` tag，`viper.Unmarshal(&cfg)`。

### 2.4 加载与热重载

```go
var current atomic.Pointer[Config]   // 读侧无锁，热重载整体换指针

func Load(baseDir string) error {
    v := viper.New()
    v.SetConfigName("config")
    v.SetConfigType("yaml")
    v.AddConfigPath(baseDir)
    setDefaults(v)
    v.SetEnvPrefix("ASA")
    v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
    v.AutomaticEnv()

    if err := v.ReadInConfig(); err != nil {
        var nf viper.ConfigFileNotFoundError
        if !errors.As(err, &nf) {
            return fmt.Errorf("读取 config.yaml 失败: %w", err)
        }
        if err := v.SafeWriteConfigAs(filepath.Join(baseDir, "config.yaml")); err != nil {
            logger.GetLogger().Warnf("写入默认 config.yaml 失败: %v", err)
        }
    }
    var cfg Config
    if err := v.Unmarshal(&cfg); err != nil {
        return fmt.Errorf("解析 config.yaml 失败: %w", err)
    }
    if err := cfg.Validate(); err != nil {
        return err
    }
    current.Store(&cfg)
    return nil
}

func Get() *Config { return current.Load() }
```

**热重载的边界**：`auth.*` 中除 `auth.database.*` 外全部可热重载（中间件每次请求都 `appconfig.Get()`）。
`server.port` / `server.tls.*` / `auth.database.*` 不可热重载，变更后日志提示「需重启生效」。

调用点：`main.go` 中 `cfgpkg.EnsureDirectories()`、`logger.InitLoggerWithBaseDir()` 之后立刻 `appconfig.Load()`。

---

## 3. 鉴权机制选型

### 3.1 为什么必须是 Cookie

| 链路 | 能否带自定义头 | 结论 |
|------|---------------|------|
| REST（axios） | 能 | Header 或 Cookie 都行 |
| SSE（`EventSource`） | **不能**（标准 API 无 headers 参数） | 只能 Cookie 或 URL query |
| WebSocket（浏览器 `WebSocket`） | **不能**（只能塞 subprotocol） | 只能 Cookie 或 URL query |

URL query 传令牌会进 access log、反代日志、浏览器历史。**不采用**，统一 Cookie：

```
Set-Cookie: asa_session=<token>; Path=/; HttpOnly; SameSite=Lax; Secure(仅 TLS 开启时); Max-Age=<ttl>
```

### 3.2 令牌格式：无状态签名令牌 + 版本吊销

不引入 JWT 库，自己签一个紧凑令牌（避开 JWT 的 alg 混淆类坑）：

```
token = base64url(payload) + "." + base64url(HMAC-SHA256(secret, base64url(payload)))
payload = {"u":"admin","v":3,"jti":"<16B random>","iat":...,"exp":...,"amr":["pwd","totp"]}
```

- `secret`：`{BaseDir}/auth/secret.key`（32 字节随机，0600，首次启动生成）。删掉它 = 全员登出。
- `v`（session_version）：存在 `users` 表。**改密码 / 管理员踢人 / 「登出全部设备」→ v++**。
- `jti` + `token_denylist` 表：支持「当前这一台设备登出」而不影响其他设备。
- `amr`（authentication methods references）：记录本次登录用了哪些手段（`pwd` / `totp` / `webauthn` / `recovery`）。
  前端可据此展示「当前会话通过 Passkey 登录」，审计也用得上。

**中间件的热路径不查库。** 校验令牌需要读该用户的 `session_version` 和 denylist，
如果每个请求都打一次 SQLite，长驻 SSE + 高频 REST 会把它变成瓶颈。做法是：
`auth/cache.go` 在内存里维护用户表和 denylist 的全量副本（各自也就几行到几十行），
**SQLite 是持久化真相，内存是读路径**；任何写操作在同一个函数里更新两者。
启动时加载一次，此后中间件是纯内存 + HMAC 计算，零 I/O。

**为什么仍然不用服务端 session store**（即便有了 SQLite）：
1. 无状态令牌下，服务重启不会把所有人踢下线 —— Windows 服务重启是常态。
2. 会话数量随标签页/设备增长，而 `session_version` + denylist 的行数通常是 0。
3. 吊销语义已完整：全设备登出走 `v++`，单设备登出走 denylist。

### 3.3 存储选型：SQLite

存储位置：`{BaseDir}/database_file/auth.db`（与 Badger 的 `state_db` 同级），文件权限 0600。

#### 为什么是 SQLite 而不是 JSON 文件

**驱动已经在二进制里了，边际成本为零。** ARK 的 `.ark` 存档本身就是 SQLite 数据库，
`go-arkparser/files` 用 `modernc.org/sqlite` 解析它，经 `parseserver` 进入主二进制：

```
$ go mod why modernc.org/sqlite
asa-server/parseserver → github.com/Niexiawei/go-arkparser/files → modernc.org/sqlite
```

`modernc.org/sqlite` 是**纯 Go 实现，不需要 cgo**，不影响现有构建方式。

如果鉴权只需要存「3 个用户」，一个 JSON 文件确实更简单。但鉴权子系统实际要持久化五类数据：

| 数据 | 用 JSON 的问题 |
|------|---------------|
| 用户表 | 尚可，但 `last_login_at` / `totp_last_step` 是**每次登录都写**，每次全文件重写 |
| jti denylist | 纯内存则重启失效 |
| **登录失败计数 / 锁定状态** | 纯内存则**重启即清零**。Windows 服务重启是常态，锁定机制形同虚设 —— 防暴力破解的真实漏洞 |
| **WebAuthn 凭证** | 每用户可有多个凭证 × 多个 RP ID，还有 `sign_count` 这种**每次登录都要更新**的字段，是典型的关系型数据 |
| **登录审计日志** | 需要按用户 / 时间范围 / IP 查询与滚动淘汰 |

#### 为什么不复用 Badger

1. **生命周期对不上**：`state` 的 Badger 在 `APIServer.Start()` 才 `InitStateManager`、`Stop()` 里关闭，
   而鉴权中间件在 `setupRoutes()` 就要能用。
2. **另开一个 Badger 实例代价不小**：独立 value log、memtable、compaction goroutine，几十 MB 内存，
   对一张 5 行的用户表不成比例。
3. **审计日志和 WebAuthn 凭证是查询型/关系型负载**，KV 存储要自己搭二级索引。

#### 打开方式（PRAGMA 与连接池）

```go
// auth/db.go
func Open(path string) (*sql.DB, error) {
    dsn := path +
        "?_pragma=journal_mode(WAL)" +      // 读写不互斥，服务重启后能自恢复
        "&_pragma=busy_timeout(5000)" +     // 遇锁等 5s 而不是立刻 SQLITE_BUSY
        "&_pragma=foreign_keys(ON)" +       // 让级联删除真正生效
        "&_pragma=synchronous(NORMAL)"      // WAL 下 NORMAL 足够

    db, err := sql.Open("sqlite", dsn)
    if err != nil {
        return nil, err
    }
    // 鉴权库并发量极低，单连接彻底规避 SQLITE_BUSY 与写锁竞争，
    // 也省掉自己处理 busy 重试。不要为了「性能」把这里调大。
    db.SetMaxOpenConns(1)
    db.SetMaxIdleConns(1)
    return db, nil     // 迁移不在这里做，见 §11.1
}
```

> `foreign_keys` 在 SQLite 里**默认关闭**，必须显式打开，否则 `ON DELETE CASCADE` 不生效，
> 删用户会留下孤儿凭证和孤儿恢复码。这是最常见的 SQLite 踩坑点。

#### 生命周期

SQLite 打开是瞬时且廉价的，不存在 Badger 那种「必须晚启动」的约束：

- 打开 + 迁移 + 加载内存副本：`InitializationBasicComponents()` 里，早于 `setupRoutes()`
- 关闭：`APIServer.Stop()` 中，与 `statepkg.CloseStateManager()` 相邻

### 3.4 存储边界：SQLite 只服务于鉴权

这是一条**架构规则，不是建议**。`auth.db` 里只允许存在鉴权相关的表：

| 数据 | 存储 | 本次是否变动 |
|------|------|-------------|
| 用户 / 凭证 / 会话吊销 / 失败计数 / 审计 | **SQLite `auth.db`** | 🆕 新增 |
| 实例状态历史 | Badger `database_file/state_db` | ❌ 不动 |
| 应用配置 | YAML `config.yaml` | 🆕 新增（本方案引入） |
| 实例配置 | INI `instances/*/instance_config.ini` | ❌ 不动 |
| 定时任务 / 执行日志 | JSON `schedules.json` / `schedule_logs.json` | ❌ 不动 |
| 日志映射 | JSON `log_mapping.json` | ❌ 不动 |
| FRP / Syncthing 配置 | 各自原有格式 | ❌ 不动 |
| ARK 存档解析 | SQLite（只读 `.ark`，`parseserver`） | ❌ 不动 |

明确的禁止项：

- **不要**把实例状态、定时任务、日志映射往 `auth.db` 里搬 —— 它们各自的存储都工作正常，
  迁移只会引入回归风险，且与本方案毫无关系。
- **不要**在 `auth.db` 里建任何非鉴权表。判定标准很简单：这张表如果在 `auth.enabled: false`
  时仍然需要被写入，它就不属于 `auth.db`。
- **不要**借机把 `state` 从 Badger 迁到 SQLite。那是独立议题。

好处是回滚极其干净：本方案出问题，删掉 `auth.db` + 把 `auth.enabled` 设回 false，
系统就完全回到改动前的状态，其他数据一个字节都没动过。

### 3.5 Schema

```sql
CREATE TABLE users (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    username        TEXT    NOT NULL,
    username_lower  TEXT    NOT NULL UNIQUE,   -- 大小写不敏感唯一约束交给 DB
    password_hash   TEXT    NOT NULL,           -- 恒非空：密码是 WebAuthn 不可用时的兜底
    role            TEXT    NOT NULL DEFAULT 'operator',  -- admin | operator
    session_version INTEGER NOT NULL DEFAULT 1,
    webauthn_handle BLOB    NOT NULL,          -- 32B 随机，WebAuthn user handle，非 PII
    totp_enabled    INTEGER NOT NULL DEFAULT 0,
    totp_secret     TEXT    NOT NULL DEFAULT '',
    totp_last_step  INTEGER NOT NULL DEFAULT 0,
    disabled        INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,          -- unix 秒
    last_login_at   INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX idx_users_handle ON users(webauthn_handle);

CREATE TABLE recovery_codes (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT    NOT NULL,
    used_at   INTEGER NOT NULL DEFAULT 0        -- 0 = 未使用
);
CREATE INDEX idx_recovery_user ON recovery_codes(user_id);

-- WebAuthn 凭证。注意 rp_id 列：同一用户在 localhost 和反代域名下是两套凭证，见 §7.1
CREATE TABLE webauthn_credentials (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    rp_id            TEXT    NOT NULL,
    credential_id    BLOB    NOT NULL,
    public_key       BLOB    NOT NULL,
    attestation_type TEXT    NOT NULL DEFAULT '',
    aaguid           BLOB,
    sign_count       INTEGER NOT NULL DEFAULT 0,
    transports       TEXT    NOT NULL DEFAULT '[]',   -- JSON 数组
    flags_uv         INTEGER NOT NULL DEFAULT 0,      -- 注册时是否做过 user verification
    flags_backup_eligible INTEGER NOT NULL DEFAULT 0, -- 是否可云端同步（passkey）
    flags_backup_state    INTEGER NOT NULL DEFAULT 0, -- 当前是否已同步
    attachment       TEXT    NOT NULL DEFAULT '',     -- platform | cross-platform
    name             TEXT    NOT NULL DEFAULT '',     -- 用户起的名字，如 "YubiKey 5C"
    clone_warned     INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL,
    last_used_at     INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX idx_wa_credid ON webauthn_credentials(rp_id, credential_id);
CREATE INDEX idx_wa_user ON webauthn_credentials(user_id);

CREATE TABLE token_denylist (
    jti        TEXT    PRIMARY KEY,
    expires_at INTEGER NOT NULL
);
CREATE INDEX idx_denylist_exp ON token_denylist(expires_at);

CREATE TABLE login_failures (
    scope        TEXT    NOT NULL,              -- 'ip' | 'user'
    key          TEXT    NOT NULL,              -- IP 字面量 或 username_lower
    fail_count   INTEGER NOT NULL DEFAULT 0,
    first_fail   INTEGER NOT NULL,
    locked_until INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (scope, key)
);

CREATE TABLE audit_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    ts         INTEGER NOT NULL,
    event      TEXT    NOT NULL,   -- login_ok | login_fail | totp_fail | webauthn_ok
                                   -- | webauthn_fail | webauthn_clone | logout | passwd_change
                                   -- | passwd_reset | user_create | user_delete | cred_add | cred_delete
    username   TEXT    NOT NULL DEFAULT '',
    actor      TEXT    NOT NULL DEFAULT '',   -- 操作发起者：用户名 或 'cli' 或 'system'
    client_ip  TEXT    NOT NULL DEFAULT '',
    user_agent TEXT    NOT NULL DEFAULT '',
    detail     TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX idx_audit_ts   ON audit_log(ts);
CREATE INDEX idx_audit_user ON audit_log(username, ts);

CREATE TABLE schema_version (version INTEGER NOT NULL);
```

#### 字段说明

- 角色只要两种：`admin`（可管理用户）、`operator`（可操作服务器）。别做复杂了 —— 这是单机管理面板。
- `password_hash` **恒非空**。没有「纯 Passkey 账户」这种东西 —— WebAuthn 只是补充，
  任何账户都必须能用密码登录，这是域名闸门未命中时的唯一兜底（§7.1）。
- `webauthn_handle` 是 32 字节随机值。规范要求 user handle **不得包含 PII**，
  所以不能用 username 或自增 id。
- `totp_secret` 明文存储。**这是有意的**：服务端必须读出明文才能校验，任何「加密」都要把密钥存在同一台机器上，
  只是把问题挪个位置。真正的防线是文件权限 0600 + 不入备份（`backup` 包只打包存档，天然不涉及）。
- `recovery_codes.code_hash` 存 bcrypt(cost 10)，用掉的置 `used_at` 而非删除，便于审计。

密码哈希用 `golang.org/x/crypto/bcrypt`，cost 12。

---

## 4. 内网免鉴权 —— 以及它最危险的那个坑

### 4.1 需求

`auth.lan_bypass.enabled: true` 时，来自内网/本机的请求跳过鉴权；只有经反代从公网进来的请求需要登录。

### 4.2 ⚠️ 核心陷阱：反代跑在同一台机器上

本项目的典型部署是 **frpc / Nginx 跑在同一台 Windows 上**，反代到 `127.0.0.1:19193`。
此时 `c.Request.RemoteAddr` 永远是 `127.0.0.1` —— **公网来的请求和本机来的请求，源 IP 完全一样**。
如果按朴素实现「RemoteAddr 是内网就放行」，结果是：**开了 lan_bypass = 整个鉴权对公网彻底失效**。

这不是理论风险，是这类方案最常见的实际事故。

### 4.3 判定规则（必须三条同时满足才放行）

```go
// authapi/middleware.go
func shouldBypass(c *gin.Context, cfg *appconfig.AuthConfig) bool {
    lb := cfg.LANBypass
    if !lb.Enabled {
        return false
    }

    // 规则 1：任何反代痕迹一律不放行。
    // 反代必须设置 XFF —— 这是让本机反代场景能被识别出来的唯一信号。
    if lb.DenyIfForwarded {
        for _, h := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded", "X-Forwarded-Host"} {
            if c.GetHeader(h) != "" {
                return false
            }
        }
    }

    // 规则 2：用 RemoteAddr，不用 c.ClientIP()。
    // ClientIP() 在可信代理场景下返回 XFF 里的值，语义是「最终客户端」；
    // 这里要判断的是「这个 TCP 连接从哪来」，必须用 RemoteAddr。
    host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
    if err != nil {
        return false
    }
    ip := net.ParseIP(host)
    if ip == nil {
        return false
    }

    // 规则 3：IP 落在配置的内网网段里
    return auth.IsInNetworks(ip, lb.Networks)
}
```

### 4.4 文档里必须对用户讲清楚的三句话

1. 开启 `lan_bypass` 前，**必须**确认反代配置了 `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`
   （Nginx）或等价配置。frpc 的 `http` 类型代理默认会加 XFF；**`tcp` 类型代理不会** —— 用 tcp 穿透时
   `lan_bypass` 必须关闭，否则等于没有鉴权。
2. `server.trusted_proxies` 要包含反代地址，否则 `c.ClientIP()`（日志、限流用）拿到的是反代 IP。
3. 默认值是 `enabled: false`。开启是显式选择，配套一条启动期 WARN 日志：
   > `[安全] lan_bypass 已开启：来自 127.0.0.0/8 等网段且不带 X-Forwarded-For 的请求将跳过鉴权。若反代未设置 XFF，公网访问将完全绕过鉴权。`

### 4.5 更保险的替代方案（后续增强）

真正无歧义的做法是**双监听**：`127.0.0.1:19193` 免鉴权、`0.0.0.0:19193` 强制鉴权。
但这要改 `APIServer.Start()` 起两个 `http.Server`，证书/端口都要重新规划。**本期不做**，
记在这里作为 lan_bypass 出问题时的升级路径。

### 4.6 `auto_detect_local_subnets`：自动补充本机物理网卡子网

`auth.lan_bypass.networks` 默认是写死的 RFC1918 大网段（`10.0.0.0/8` 等）。这类大网段
在企业内网里可能过宽——同属 `10.0.0.0/8` 但并非同一物理局域网段的机器也会被信任。

`auth.lan_bypass.auto_detect_local_subnets`（默认 `false`）开启后，会在上述
`networks` 列表之外，**追加**本机当前所在物理网卡的精确子网（按 IP + 子网掩码计算，
例如网卡是 `192.168.1.42/24` 就追加 `192.168.1.0/24`）。要点：

- **只是补充，不替换**：默认的 `networks` 列表始终保留。
- **只信任私有/链路本地地址**（`net.IP.IsPrivate()` / `IsLinkLocalUnicast()`）。
  物理网卡也可能直连公网（例如云主机），此时探测到的公网子网绝不会被加入信任列表，
  否则等于把免鉴权开放给整个公网子网段。
- **默认排除虚拟适配器**：Docker / Hyper-V / WSL2 / VPN / 隧道类网卡（按名称关键字
  识别，见 `appconfig/localnet.go` 的 `virtualAdapterKeywords`）一律不参与探测——
  这些虚拟子网里的进程理论上都能连到宿主机管理接口，纳入信任会把攻击面从
  "同一物理局域网" 扩大到 "本机上跑的任意容器"。名称启发式无法达到 WMI
  `Win32_NetworkAdapter.PhysicalAdapter` 那样的精确度，但 `appconfig` 是只依赖
  标准库 + viper 的叶子包（见包头注释），不为此引入 WMI 依赖；误判方向也刻意选择
  "宁可漏判物理网卡"而不是"误信虚拟网卡"。
- 探测只在配置 `Load()`/`reload` 时计算一次，不做运行时周期性重新探测
  （与其余配置项的生效模型一致，DHCP 续租导致子网变化需要手动 reload）。

---

## 5. 中间件与豁免清单

### 5.1 挂载位置

`webapi/actions.go` 的 `setupRoutes()` **最前面**，早于所有 `RegisterRouter`：

```go
func (s *APIServer) setupRoutes() {
    s.engine.Use(authapi.Middleware())     // ← 新增，必须在所有业务路由之前
    instanceapi.NewHandler().RegisterRouter(s.engine)
    // ... 其余不变
}
```

### 5.2 豁免路径

| 路径 | 原因 |
|------|------|
| `GET /health` | 健康检查，本来就不返回敏感信息 |
| `POST /api/auth/login` | 登录本身 |
| `POST /api/auth/login/totp` | 两步验证第二阶段（凭 pre-auth 令牌） |
| `POST /api/auth/webauthn/login/begin`、`/finish` | Passkey 登录 |
| `GET /api/auth/state` | 前端问「要不要登录 / 我是谁」，未登录返回 `{authenticated:false}` 而非 401 |
| `POST /api/auth/logout` | 幂等，未登录也返回 200 |
| `GET/POST /api/auth/setup*` | 首次引导，仅在「零用户」状态下开放，见 §10 |
| `POST /api/auth/reload` | CLI 改库后让服务重载内存副本；**仅接受 loopback 且无 XFF 的请求**，见 §10.2 |
| 非 `/api` 前缀的一切（SPA 静态资源、`index.html`） | 否则登录页自己都加载不出来 |

**静态资源不鉴权**是刻意的：SPA 的 JS/CSS 里没有数据，数据全在 API。给静态资源加鉴权只会造成
「未登录时白屏、连登录页都打不开」，没有任何安全收益。

### 5.3 中间件骨架

```go
func Middleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        cfg := appconfig.Get()
        if !cfg.Auth.Enabled {
            c.Next()
            return
        }
        if isExempt(c.Request.URL.Path) || !strings.HasPrefix(c.Request.URL.Path, "/api") {
            c.Next()
            return
        }
        if shouldBypass(c, &cfg.Auth) {
            c.Set(ctxUserKey, auth.LocalUser)   // 内网免鉴权用伪用户，便于审计日志
            c.Next()
            return
        }

        tok, err := c.Cookie(cfg.Auth.Session.CookieName)
        if err != nil {
            reject(c, "未登录")
            return
        }
        user, err := auth.VerifyToken(tok)     // 纯内存 + HMAC，零 I/O
        if err != nil {
            reject(c, "会话已失效")
            return
        }
        auth.MaybeRenew(c, user)   // 滑动续期：剩余寿命 < 1/2 TTL 时重发 Cookie
        c.Set(ctxUserKey, user)
        c.Next()
    }
}
```

### 5.4 `reject` 必须按链路类型区分响应 —— 这是避免风暴的关键

```go
func reject(c *gin.Context, msg string) {
    switch {
    case isWebSocketUpgrade(c.Request):
        // 关键：在 Upgrade 之前就 401，绝不「先升级再关闭」。详见 §9。
        c.Header("Connection", "close")
        c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": msg, "code": "unauthorized"})
    case isSSERequest(c.Request):
        // 关键：必须在写出任何 SSE 响应体之前返回非 200。
        // 一旦响应头是 200 + text/event-stream，浏览器 EventSource 会每 3 秒无限重连。
        c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": msg, "code": "unauthorized"})
    default:
        c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": msg, "code": "unauthorized"})
    }
}
```

`isSSERequest`：`strings.Contains(c.GetHeader("Accept"), "text/event-stream")`。
`isWebSocketUpgrade`：`strings.EqualFold(c.GetHeader("Upgrade"), "websocket")`。

因为中间件在所有路由之前跑，SSE handler 里 `c.Stream(...)` 还没被调用，天然满足「还没写响应体」。

---

## 6. 两步验证（pquerna/otp）

### 6.1 登录流程（两阶段）

```
POST /api/auth/login {username, password}
  ├─ 密码错 → 401，计入限流
  │   （密码登录对所有账户恒可用，不存在「该账户不支持密码」的分支）
  ├─ 密码对 && 未开 TOTP → 200 + Set-Cookie(会话令牌, amr=["pwd"])  → 完成
  └─ 密码对 && 已开 TOTP → 200 {"totp_required": true} + Set-Cookie(pre-auth 令牌, 5 分钟)
       └─ POST /api/auth/login/totp {code}
            ├─ 通过 → 200 + Set-Cookie(会话令牌, amr=["pwd","totp"])
            └─ 失败 → 401，计入限流
```

pre-auth 令牌用同一套签名机制，payload 加 `"stage":"pre"`，`VerifyToken` 对 `stage != "full"` 一律拒绝，
只有 `/api/auth/login/totp` 用专门的 `VerifyPreAuthToken` 接受它。**别用同一个校验函数**，
否则 pre-auth 令牌就成了完整凭证。

### 6.2 绑定流程

```
POST /api/auth/totp/setup      → 生成 secret（不落盘，暂存内存 5 分钟）
                                 返回 {secret, otpauth_url, qr_png_base64}
POST /api/auth/totp/confirm    → 提交验证码，通过才把 secret 写进 users 表，
                                 同时返回 10 个一次性恢复码（明文只在这一次返回）
POST /api/auth/totp/disable    → 需当前密码 + 一个有效验证码
```

二维码由**后端**生成（`key.Image(256,256)` → PNG → base64），前端直接
`<img :src="'data:image/png;base64,'+qr">`，不用新增 QR 库。

```go
key, err := totp.Generate(totp.GenerateOpts{
    Issuer:      cfg.Auth.TOTP.Issuer,
    AccountName: username,
    Period:      30,
    Digits:      otp.DigitsSix,
    Algorithm:   otp.AlgorithmSHA1,           // 兼容所有主流验证器 App，别改
})
img, _ := key.Image(256, 256)
```

### 6.3 校验：时钟偏差 + 防重放

```go
func ValidateTOTP(u *User, code string, skew uint) (bool, error) {
    valid, err := totp.ValidateCustom(code, u.TOTPSecret, time.Now().UTC(), totp.ValidateOpts{
        Period: 30, Skew: skew, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
    })
    if err != nil || !valid {
        return false, err
    }
    // 防重放：同一时间步只能用一次，否则 30 秒内抓到验证码可重放
    step := uint64(time.Now().UTC().Unix()) / 30
    if u.TOTPLastStep >= step {
        return false, ErrCodeReused
    }
    u.TOTPLastStep = step
    return true, nil
}
```

登录失败提示里带一句「若持续失败请检查服务器系统时间」。

### 6.4 恢复码

10 个 `XXXX-XXXX-XXXX` 格式的随机码，bcrypt 存储。`/api/auth/login/totp` 同时接受
6 位数字（TOTP）和恢复码格式；用掉的置 `used_at`。用完时前端提示重新生成。

---

## 7. WebAuthn / FIDO2（密码登录的补充）

### 7.1 定位：密码登录的补充，不可用就退回密码

三条铁律，贯穿整个 WebAuthn 设计：

1. **每个账户恒有密码。** 没有「纯 Passkey 账户」，也没有「关闭密码登录」的开关。
   Passkey 是登录方式之一，永远不是唯一一种。
2. **域名闸门。** 只有当前请求的域名命中 `auth.webauthn.domains`，WebAuthn 才启用。
3. **任何一步不满足 → 静默退回密码登录。** 不报错、不阻塞、不显示 Passkey 入口。

之所以必须这样设计，是因为 WebAuthn 的 RP ID 规则与本项目的访问方式存在**规范级**冲突，
没有任何绕过手段：

| 规范约束 | 对本项目的影响 |
|---------|--------------|
| **IP 地址不是合法 RP ID** | 通过 `https://192.168.2.26:19193` 访问时 WebAuthn 完全不可用 —— 而「局域网用 IP 访问面板」恰恰是本项目最常见的用法之一 |
| **`localhost` 是规范特例**（合法 RP ID，且视为安全上下文） | 本机访问 `https://localhost:19193` 可用 |
| **凭证不跨 RP ID** | 在 `localhost` 注册的 Passkey 在 `ark.example.com` 上用不了，反之亦然 |
| **要求安全上下文**（https 或 localhost） | `--tls=false` 且用域名访问时不可用。项目默认开 TLS 且 `certmgr` 会装本地 CA，正常满足 |

也就是说：**总有一部分访问路径注定用不了 WebAuthn。** 所以它只能是补充。
一旦允许「纯 Passkey 账户」，用户换个入口访问就把自己锁在门外了 ——
这条限制不是保守，是这个部署形态下唯一正确的选择。

顺带一提，这条约束把上一版设计里的一整类问题直接消掉了：
不再需要「删除最后一个凭证会不会自锁」的检查，不再需要「无密码 + 无凭证」的不变量，
清空 Passkey 永远是安全操作。

依赖：

```bash
go get github.com/go-webauthn/webauthn     # 用到 webauthn（主流程）与 protocol（选项常量）两个子包
```

### 7.2 域名闸门

#### 匹配规则

```go
// auth/webauthn.go
// MatchDomain 返回请求应使用的 RP ID。不命中返回 ok=false，调用方一律退回密码登录。
func MatchDomain(host string, domains []string) (rpID string, ok bool) {
    if h, _, err := net.SplitHostPort(host); err == nil {
        host = h
    }
    host = strings.TrimSuffix(strings.ToLower(host), ".")   // 去掉 FQDN 尾点
    if host == "" || net.ParseIP(host) != nil {
        return "", false                                     // IP 不是合法 RP ID
    }

    // 精确匹配优先（更具体的配置项应该胜出）
    if slices.Contains(domains, host) {
        return host, true
    }
    // 父域名匹配：配了 example.com 时，ark.example.com 也可用，共享同一套凭证。
    // 这正是 WebAuthn 允许的「RP ID 必须是 Origin 有效域名的可注册后缀」。
    // 取最长的那个父域名，行为才可预测。
    best := ""
    for _, d := range domains {
        if strings.HasSuffix(host, "."+d) && len(d) > len(best) {
            best = d
        }
    }
    if best != "" {
        return best, true
    }
    return "", false
}
```

`domains` **留空 = 不对任何请求生效**。不做任何自动推导（不从 `server.tls.domains` 继承）——
用户显式声明哪些域名参与 WebAuthn，比猜一个默认值安全，也更容易排查。
启动时若 `enabled: true` 而 `domains` 为空，记一条 WARN：

```
[鉴权] webauthn.enabled=true 但 webauthn.domains 为空，WebAuthn 不会对任何请求生效。
       如需本机使用请添加 "localhost"；反代场景请添加对外域名。
```

#### 配置校验（启动即失败，不要静默忽略）

`domains` 的每一项必须是纯域名。以下情况在 `Config.Validate()` 里**直接报错拒绝启动**：

| 非法输入 | 报错信息 |
|---------|---------|
| `192.168.1.10` | `webauthn.domains[0]: IP 地址不能作为 RP ID，请使用域名或 localhost` |
| `https://ark.example.com` | `webauthn.domains[0]: 不要带协议前缀，应为 ark.example.com` |
| `ark.example.com:19193` | `webauthn.domains[0]: 不要带端口，端口由 server.port 自动推导` |
| `ark.example.com/panel` | `webauthn.domains[0]: 不要带路径` |

之所以报错而不是过滤 + 告警：用户配错了却看到「WebAuthn 已启用」，然后发现按钮不出现，
排查成本远高于启动时直接告诉他哪一行写错了。

#### ⚠️ 改动 `domains` 会让已注册凭证失效

凭证绑定在具体的 RP ID 上。把 `ark.example.com` 改成 `example.com`（或反过来）之后，
**之前注册的 Passkey 全部失效** —— 它们的 `rp_id` 与新解析出的 RP ID 不再匹配，
登录时被过滤掉，表现为「Passkey 按钮在，但弹窗里没有可用凭证」。

处理方式：

- 这些行不自动删除。用户重新注册后，旧行仍留在库里但永远匹配不上。
- Profile 页的凭证列表按 `rp_id` 分组，对当前不生效的分组标注「当前域名下不可用」，并提供删除按钮。
- 因为密码永远可用，这个变更不会把任何人锁在外面 —— 这正是铁律 1 存在的价值。

### 7.3 可用性判定与退回密码登录

**判定统一在后端做**，前端只消费结果。这样规则只有一处实现，也避免前端自己猜。

```go
type Availability struct {
    Available bool   `json:"available"`
    Reason    string `json:"reason,omitempty"`
    RPID      string `json:"rp_id,omitempty"`
}

func AvailabilityFor(c *gin.Context) Availability {
    cfg := appconfig.Get().Auth.WebAuthn
    switch {
    case !cfg.Enabled:
        return Availability{Reason: "disabled"}
    case len(cfg.Domains) == 0:
        return Availability{Reason: "no_domains"}
    case !isSecureContext(c):          // 非 https 且非 localhost
        return Availability{Reason: "insecure_context"}
    }
    rpID, ok := MatchDomain(c.Request.Host, cfg.Domains)
    if !ok {
        // 包含 IP 访问、未列入 domains 的域名两种情况。
        // 对前端而言处理方式完全一样：退回密码登录。
        return Availability{Reason: "domain_not_allowed"}
    }
    return Availability{Available: true, RPID: rpID}
}
```

`reason` 取值与前端表现：

| reason | 含义 | 前端表现 |
|--------|------|---------|
| `disabled` | 配置未开启 | 隐藏 Passkey 入口，Profile 页也不显示注册按钮 |
| `no_domains` | 开了但 `domains` 为空 | 同上 |
| `insecure_context` | 非安全上下文 | 同上，Profile 页额外提示「请通过 HTTPS 访问以使用 Passkey」 |
| `domain_not_allowed` | 当前域名/IP 不在 `domains` 内 | 同上，Profile 页提示「当前访问地址不支持 Passkey，请通过已配置的域名访问」 |
| — （available） | 可用 | 显示 Passkey 入口 |

**「退回密码登录」的准确含义**是：登录页的密码表单**始终是主路径**，Passkey 只是可能出现的额外按钮。
不存在「先试 Passkey 失败再退回」的运行时切换 —— 判定在页面加载时就完成了，用户不会看到闪烁或报错。

除此之外还有三种运行时失败，同样退回密码，且**都不计入登录失败限流**：

| 情况 | 处理 |
|------|------|
| 浏览器不支持 WebAuthn（`!window.PublicKeyCredential`） | 前端特性检测，隐藏入口 |
| 用户取消系统弹窗（`NotAllowedError`） | 静默回到密码表单，不弹错误提示 |
| 断言校验失败（凭证不匹配、超时） | 提示「Passkey 验证失败，请使用密码登录」，焦点移到密码框 |

#### 每 RP ID 一个 WebAuthn 实例，缓存复用

`webauthn.Config` 的 `RPID` 是单值，而我们要支持多个，所以按 RP ID 建实例并缓存：

```go
var waCache sync.Map // rpID -> *webauthn.WebAuthn

func instanceFor(rpID string) (*webauthn.WebAuthn, error) {
    if v, ok := waCache.Load(rpID); ok {
        return v.(*webauthn.WebAuthn), nil
    }
    cfg := appconfig.Get()
    w, err := webauthn.New(&webauthn.Config{
        RPID:          rpID,
        RPDisplayName: cfg.Auth.WebAuthn.RPDisplayName,
        RPOrigins:     originsFor(rpID, cfg),   // 端口必须精确匹配，见下
    })
    if err != nil {
        return nil, err
    }
    waCache.Store(rpID, w)
    return w, nil
}
```

配置热重载时必须 **清空 `waCache`** —— 否则改了 `domains` 或 `rp_display_name` 后仍在用旧实例。

**Origin 的端口必须精确匹配**。`https://localhost` 和 `https://localhost:19193` 是不同的 Origin。
父域名匹配时更要注意：RP ID 是 `example.com`，但 Origin 是 `https://ark.example.com`，两者不同，
所以 `RPOrigins` 必须把实际访问的主机名也列进去：

```go
func originsFor(rpID string, cfg *appconfig.Config) []string {
    hosts := []string{rpID}
    // 父域名匹配场景：把配置里所有以 rpID 为后缀的域名都作为合法 Origin
    for _, d := range cfg.Auth.WebAuthn.Domains {
        if d != rpID && strings.HasSuffix(d, "."+rpID) {
            hosts = append(hosts, d)
        }
    }
    var out []string
    for _, h := range hosts {
        out = append(out, "https://"+h)
        if p := cfg.Server.Port; p != 443 {
            out = append(out, fmt.Sprintf("https://%s:%d", h, p))
        }
        if h == "localhost" && !cfg.Server.TLS.Enabled {
            out = append(out, fmt.Sprintf("http://localhost:%d", cfg.Server.Port))  // localhost 特例
        }
    }
    return append(out, cfg.Auth.WebAuthn.ExtraOrigins...)
}
```

> 父域名 + 子域名的组合是这套设计里最容易出错的地方。如果不确定，
> **就在 `domains` 里逐个列出实际访问的完整域名**，别用父域名简写。

### 7.4 `webauthn.User` 接口实现

```go
type webAuthnUser struct {
    u     *User
    creds []webauthn.Credential   // 仅当前 RPID 下的凭证
}

func (w *webAuthnUser) WebAuthnID() []byte                    { return w.u.WebAuthnHandle } // 32B 随机
func (w *webAuthnUser) WebAuthnName() string                  { return w.u.Username }
func (w *webAuthnUser) WebAuthnDisplayName() string           { return w.u.Username }
func (w *webAuthnUser) WebAuthnCredentials() []webauthn.Credential { return w.creds }
```

> `WebAuthnID` 必须是**稳定且不含 PII 的随机值**（规范要求）。用 username 或自增 id 都是错的：
> user handle 会被存进认证器并可能在 discoverable 登录时回传，等于把用户名泄露给认证器/同步云。
> 所以 `users` 表有专门的 `webauthn_handle` 列，用户创建时生成，永不变更。

### 7.5 注册流程

```
POST /api/auth/webauthn/register/begin    (需已登录)
   → 返回 CredentialCreation JSON，同时下发 asa_wa_ceremony Cookie（5 分钟）
POST /api/auth/webauthn/register/finish   (需已登录)
   → body: { credential: <浏览器返回的 attestation>, name: "YubiKey 5C" }
   → 落库
```

```go
func BeginRegistration(c *gin.Context, u *User) (*protocol.CredentialCreation, error) {
    av := AvailabilityFor(c)              // 域名闸门，见 §7.3
    if !av.Available {
        return nil, fmt.Errorf("%w: %s", ErrWebAuthnUnavailable, av.Reason)
    }
    wa, err := instanceFor(av.RPID)
    if err != nil {
        return nil, err
    }
    wu := newWebAuthnUser(u, credsFor(u.ID, av.RPID))

    opts, session, err := wa.BeginRegistration(wu,
        // 排除已注册的凭证，避免同一个认证器重复注册
        webauthn.WithExclusions(wu.credentialDescriptors()),
        webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
            // discoverable_login 需要驻留密钥，否则登录页的「使用 Passkey 登录」弹窗会是空的
            ResidentKey:      residentKeyRequirement(),   // required（默认）/ preferred
            UserVerification: userVerification(),
        }),
    )
    if err != nil {
        return nil, err
    }
    storeCeremony(c, av.RPID, u.ID, session)   // 见 §7.8
    return opts, nil
}
```

`FinishRegistration` 之后落库；`idx_wa_credid` 的 UNIQUE 约束天然阻止同一凭证被注册到两个账户，
命中冲突时返回「该认证器已被注册」。

注册接口本身也要挡一道：即便前端因为缓存或旧版本仍然发起注册，后端在域名闸门未命中时
直接返回 `409 {"code":"webauthn_unavailable","reason":"domain_not_allowed"}`，前端据此刷新可用性状态。

### 7.6 登录流程（两种）

两种流程都只在域名闸门命中时才可能被触发；未命中时登录页压根不渲染 Passkey 入口。

#### (a) Discoverable / 用户名无关（`discoverable_login: true` 时的默认入口）

用户点「使用 Passkey 登录」，不输任何东西：

```go
opts, session, err := wa.BeginDiscoverableLogin()
// ... 前端调 navigator.credentials.get(opts) ...
cred, err := wa.FinishDiscoverableLogin(
    func(rawID, userHandle []byte) (webauthn.User, error) {
        u, err := store.GetByWebAuthnHandle(userHandle)
        if err != nil { return nil, err }
        if u.Disabled { return nil, ErrUserDisabled }
        return newWebAuthnUser(u, credsFor(u.ID, rpID)), nil
    },
    session, c.Request)
```

要求凭证注册时是 `ResidentKey: required`。否则认证器里没有驻留密钥，浏览器弹窗会是空的。

#### (b) 用户名优先

用户先填用户名 → `BeginLogin(wu)` 带 allowCredentials 列表。用于认证器不支持驻留密钥的情况。

**一个隐私细节**：用户名不存在时**不要**直接返回 404 —— 那是用户名枚举漏洞。
返回一个用随机数据构造的假 challenge，让失败发生在 finish 阶段，与真实失败无法区分。

### 7.7 与 TOTP 的关系：UV 通过的 Passkey 就是两因素

一个经过 **user verification**（PIN / 指纹 / 面容）的 Passkey，本身已经是
「持有认证器」+「知道 PIN 或生物特征」两个因素。**登录成功后不应该再要 TOTP。**

```go
// satisfies_2fa: true（默认）时的判定
if cred.Flags.UserVerified && cfg.Auth.WebAuthn.Satisfies2FA {
    amr = []string{"webauthn", "uv"}
    // 直接签发完整会话令牌，跳过 TOTP 阶段
} else {
    amr = []string{"webauthn"}
    // UV 没通过（user_verification: discouraged 的场景）→ 若用户开了 TOTP，仍进第二阶段
}
```

`cred.Flags.UserVerified` 必须**从本次断言结果里读**，不能读注册时存的 `flags_uv` ——
同一个认证器这次有没有做 UV 是逐次变化的。

### 7.8 仪式（ceremony）状态存哪

`BeginRegistration` / `BeginLogin` 返回的 `*webauthn.SessionData` 必须在 begin 与 finish 之间保存。

**用内存 map + TTL，不用 Cookie**：SessionData 含 challenge 和 allowCredentials 列表，
凭证多时可能超过 4KB 的 Cookie 上限。

```go
type ceremony struct {
    data    *webauthn.SessionData
    rpID    string
    userID  int64      // 登录仪式为 0
    expires time.Time
}
var ceremonies sync.Map   // ceremonyID -> *ceremony，5 分钟 TTL，ticker 清理
```

ceremonyID 是 32 字节随机值，放在 `asa_wa_ceremony` 这个 HttpOnly + 短时 Cookie 里。
**finish 时必须校验 `rpID` 与当前请求一致**，防止跨 RPID 混用仪式。

服务重启会丢失进行中的仪式 —— 用户重试一次即可，可接受。

### 7.9 sign_count 与克隆检测

go-webauthn 在断言结果里会给出更新后的 `Authenticator.SignCount`，并在计数没有前进时设置
`CloneWarning`。处理要点：

- **很多认证器恒定返回 0**（尤其 iCloud / Google 同步的 passkey，按设计就不维护计数器）。
  `stored == 0 && new == 0` 要视为「不支持该特性」直接跳过，否则会误报到无法登录。
- 真正触发 `CloneWarning` 时按 `auth.webauthn.clone_detection` 配置处理：
  `off` 忽略 / `warn`（默认）记审计 + 日志 WARN 但放行 / `disable_credential` 禁用该凭证并要求重新注册。
- 每次成功登录都要写回 `sign_count` 和 `last_used_at`。这是 `webauthn_credentials` 表**每次登录都写**的原因，
  也是 §3.3 里「凭证是关系型数据、不适合 JSON」的具体体现。

### 7.10 凭证管理（没有自锁风险）

用户可在 Profile 页管理自己的凭证（改名、删除），管理员可在用户管理页重置某用户的全部凭证。

**因为每个账户恒有密码（§7.1 铁律 1），删除凭证永远是安全操作** —— 不需要「删除最后一个凭证」
的拦截，不需要「无密码 + 无凭证」的不变量检查，管理员也可以放心地一键清空某用户的全部 Passkey。
这是把 WebAuthn 限定为补充带来的直接简化。

唯一要注意的是**用户体验层面**的提示，不是安全拦截：

- 删除最后一个凭证时提示「删除后将只能使用密码登录，确认？」
- Profile 页的凭证列表按 `rp_id` 分组；当前域名下不生效的分组标注「当前域名下不可用」
  并说明原因（配置变更 / 换了访问入口），同样提供删除按钮

删除凭证要写审计（`event=cred_delete`，记录 `rp_id` 与凭证名）。

### 7.11 前端要点

```js
// 关键：challenge / user.id / allowCredentials[].id 是 base64url 字符串，
// 必须转成 ArrayBuffer 才能传给 navigator.credentials；返回值反向再转回 base64url。
// go-webauthn 的 protocol 包在 Go 侧已按 base64url 编解码，前端只需做 buffer 转换。
const opts = await authApi.webauthnLoginBegin();
const assertion = await navigator.credentials.get({
    publicKey: decodeOptions(opts.publicKey)
});
await authApi.webauthnLoginFinish(encodeAssertion(assertion));
```

**登录页的结构必须是「密码为主、Passkey 为辅」**，而不是两个并列的 Tab：

```
┌─────────────────────────────┐
│  用户名  [___________]      │   ← 始终存在，始终是默认焦点
│  密码    [___________]      │
│         [   登录   ]        │
│  ─────────  或  ─────────   │   ← 仅在 webauthn_available 时渲染
│    [ 🔑 使用 Passkey 登录 ]  │
└─────────────────────────────┘
```

这样「退回密码登录」不需要任何运行时切换逻辑 —— 密码表单本来就在那儿。

- 可用性判定**统一由后端给**：`/api/auth/state` 的 `webauthn_available` + `webauthn_reason`（§7.3），
  前端只负责渲染与否，不自己推导域名规则。
- 前端仍需做一次特性检测（`!window.PublicKeyCredential`）—— 后端不知道浏览器支不支持。
- 用 `PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable()` 决定是否把按钮文案
  换成「用本机生物识别登录」。
- 用户取消弹窗（`NotAllowedError`）不是错误：静默回到密码表单，不弹提示，**不计入失败限流**。
- 其它断言失败：提示「Passkey 验证失败，请使用密码登录」并把焦点移到密码框，
  **不要**把用户卡在一个只有重试按钮的页面上。

---

## 8. 接口设计

### 8.1 鉴权接口

| Method | Path | 鉴权 | 说明 |
|--------|------|------|------|
| GET | `/api/auth/state` | 豁免 | `{auth_enabled, authenticated, bypassed, setup_required, user:{...}, totp_required_global, webauthn_available, webauthn_reason, webauthn_rp_id}` —— 密码登录恒可用，故无对应字段 |
| POST | `/api/auth/login` | 豁免 | `{username,password}` → 会话 Cookie 或 `{totp_required:true}` |
| POST | `/api/auth/login/totp` | pre-auth | `{code}`（TOTP 或恢复码）→ 会话 Cookie |
| POST | `/api/auth/logout` | 豁免 | 清 Cookie + jti 加入 denylist |
| POST | `/api/auth/logout-all` | 需登录 | `session_version++`，踢掉所有设备 |
| POST | `/api/auth/password` | 需登录 | `{old_password,new_password}`，成功后 `version++` 并重签当前设备令牌 |
| POST | `/api/auth/totp/setup` \| `/confirm` \| `/disable` | 需登录 | 见 §6.2 |
| POST | `/api/auth/totp/recovery/regenerate` | 需登录 | 重新生成恢复码 |
| POST | `/api/auth/webauthn/register/begin` \| `/finish` | 需登录 | 注册 Passkey |
| POST | `/api/auth/webauthn/login/begin` \| `/finish` | 豁免 | Passkey 登录 |
| GET | `/api/auth/webauthn/credentials` | 需登录 | 自己的凭证列表（按 rp_id 分组） |
| PUT/DELETE | `/api/auth/webauthn/credentials/:id` | 需登录 | 改名 / 删除（§7.10 的自锁检查） |
| POST | `/api/auth/reload` | 豁免+loopback | 重载内存副本，供 CLI 调用，见 §10.2 |

### 8.2 用户管理接口（仅 admin）

| Method | Path | 说明 |
|--------|------|------|
| GET | `/api/users` | 列表（不含 hash / secret / 公钥） |
| POST | `/api/users` | `{username,password,role}`，**password 必填**（每个账户恒有密码，§7.1） |
| PUT | `/api/users/:username` | `{role?,disabled?}` |
| DELETE | `/api/users/:username` | 禁止删除最后一个 admin；禁止删除自己 |
| POST | `/api/users/:username/password` | 管理员重置密码，强制 `version++` |
| POST | `/api/users/:username/totp/reset` | 管理员解绑 TOTP（丢手机的救援路径） |
| POST | `/api/users/:username/webauthn/reset` | 管理员清空该用户全部 Passkey |
| POST | `/api/users/:username/unlock` | 清除该用户的登录失败锁定 |
| GET | `/api/auth/audit` | 审计日志分页查询，`?user=&event=&since=&limit=` |

**不变量**（在 `auth` 包内强制，不靠 handler 自觉；能用 DB 约束表达的就交给 DB）：
- 系统中至少保留一个未禁用的 `admin` —— 删除/禁用/降级前在**同一事务内**复核，
  避免并发下两个请求各自认为「还有另一个 admin」而把最后一个也删掉
- 用户名唯一、不区分大小写（`username_lower UNIQUE`）、只允许 `[a-zA-Z0-9_-]{3,32}`
- 每个账户恒有非空 `password_hash`；创建、更新接口都不提供「清空密码」的路径

### 8.3 CORS 与 CSRF

现有 `engine.Use(cors.Default())` 是 `AllowAllOrigins: true` + 不带凭证。上 Cookie 后必须改：

```go
corsCfg := cors.Config{
    AllowOrigins:     appconfig.Get().Server.CORS.AllowedOrigins,  // 空则不装 CORS 中间件
    AllowCredentials: true,
    AllowMethods:     []string{"GET","POST","PUT","DELETE","OPTIONS"},
    AllowHeaders:     []string{"Content-Type","X-Requested-With"},
}
```

`AllowCredentials: true` 与 `AllowAllOrigins: true` **不能共存**。生产同源部署时留空即可。

CSRF 双重保险：
1. `SameSite=Lax` Cookie —— 挡掉跨站表单 POST。
2. 所有非幂等 API 要求 `X-Requested-With: XMLHttpRequest` 头 —— 跨站 HTML 表单设不了这个头，
   会触发 CORS 预检。在 `app/src/utils/http.js` 请求拦截器里统一加。

---

## 9. WebSocket 鉴权失败会不会造成连接风暴？

### 9.1 结论

**按现有的重连实现，会。而且不止 WS，SSE 更严重。** 但风暴不是鉴权本身造成的，是「重连策略把认证失败
当成网络抖动」造成的。

### 9.2 现状分析

`app/src/workers/wsWorker.js` 当前行为：

- 固定间隔重连：`setInterval(..., 10000)`，`maxReconnectAttempts: null`（**无限次**）
- `onclose` / `onerror` 一律触发重连，**不区分原因**
- `wsManager.js` 还挂了 `useDocumentVisibility` 监听，页面切回前台时额外触发一次 `START_RECONNECT`

如果鉴权失败的实现是「先 `Upgrade()` 成功，再 `conn.Close()`」，那么：

| 影响 | 说明 |
|------|------|
| 每 10s 一次的永久热循环 | 每个标签页、每个浏览器、每台机器各一份，永不停止 |
| 每次重连 = 一次完整 TLS 握手 | 项目默认 HTTPS，握手是 WS 连接里最贵的部分；HTTP/2 多路复用在这里帮不上忙（WS 走 h1 Upgrade） |
| 日志淹没 | `asaServer.log` 被刷爆，lumberjack 疯狂切割，真正的错误被冲掉 |
| **惊群（thundering herd）** | 固定 TTL 意味着所有客户端会话在几乎同一时刻过期。N 个客户端同时被踢 → 同时进入 10s 固定间隔重连 → **相位锁定**，此后每 10s 一次 N 并发脉冲，永远不会自然错开 |
| 前端毫无反馈 | 用户只看到「连接中…」转圈，不知道该去登录 |

SSE 更糟：`EventSource` 的自动重连是**浏览器内置**的，JS 关不掉。规范规定：
- 响应状态非 200 或 MIME 不是 `text/event-stream` → 连接**失败**，浏览器**不重连**（这是我们要的）
- 响应 200 且 MIME 正确、之后流断开 → 浏览器按 `retry`（默认约 3 秒）**无限重连**

所以 **SSE 鉴权失败绝不能「先 200 再断」**。`logapi` / `serverapi` / `saveapi` 都有长驻 SSE，
中间件在 handler 之前拦截天然能返回 401 —— 但**必须审计一遍**，确保没有任何 handler 抢先写了
`Content-Type: text/event-stream` + 200。

### 9.3 规避方案（六条，缺一不可）

#### (a) 后端：升级前拒绝，不要升级后关闭

```go
// realtime/ws.go
func HandleServerEvents(c *gin.Context) {
    // 中间件已经拦过；这里是纵深防御，防止将来有人把路由挪出中间件覆盖范围
    if !authapi.IsAuthenticated(c) {
        c.AbortWithStatus(http.StatusUnauthorized)   // ← 不 Upgrade
        return
    }
    conn, err := WSUpgrader.Upgrade(...)
    ...
}
```

#### (b) 前端：连接前预检，未登录**根本不发起** WS

```js
export function connectWebSocket(onOpen, onError, onClose) {
    if (authStore.authRequired && !authStore.authenticated) {
        // 未登录：不连，也不重连。登录成功后由 authStore 主动调 forceReconnect()
        return;
    }
    ...
}
```

这一条消除了绝大部分无效连接。

#### (c) 会话中途失效：用应用级关闭码把「致命」和「可重试」分开

```go
const CloseAuthFailed = 4401   // 4000-4999 为应用私有区间

conn.WriteControl(websocket.CloseMessage,
    websocket.FormatCloseMessage(CloseAuthFailed, "session expired"),
    time.Now().Add(time.Second))
conn.Close()
```

```js
ws.onclose = (event) => {
    if (event.code === 4401) {
        stopReconnect();                                  // ← 彻底停，不是延后
        self.postMessage({ type: 'WS_AUTH_FAILED' });     // 主线程跳登录页
        return;
    }
    scheduleReconnect();   // 只有非鉴权原因才走退避重连
};
```

**重连只有在登录成功后才被显式重新启用**。

#### (d) 把固定间隔换成指数退避 + 全抖动

这是根治惊群的那一条，**即使不做鉴权也应该改**：

```js
const BACKOFF = { base: 1000, max: 30000 };
let attempt = 0;

function nextDelay() {
    const cap = Math.min(BACKOFF.max, BACKOFF.base * 2 ** attempt);
    attempt++;
    return Math.random() * cap;        // full jitter
}

function scheduleReconnect() {
    clearTimeout(reconnectTimer);       // 用递归 setTimeout，不用 setInterval
    reconnectTimer = setTimeout(doReconnect, nextDelay());
}
function onConnected() { attempt = 0; }
```

`setInterval` → 递归 `setTimeout` 同样重要：`setInterval` 在尝试耗时超过间隔时会**堆积**回调。

| 策略（100 客户端同时失效，10 分钟） | 尝试次数 | 峰值并发 |
|------|---------|---------|
| 固定 10s 无限重连 | 6000 | 100（相位锁定） |
| 指数退避 + 全抖动 + 4401 致命判定 | ≈100（各 1 次后停止） | ≈10 |

#### (e) 服务端限速兜底

```go
// 同一 IP 的鉴权失败：> 20 次/分钟 → 429 + Retry-After: 60
if authFailLimiter.Exceeded(clientIP) {
    c.Header("Retry-After", "60")
    c.AbortWithStatus(http.StatusTooManyRequests)
    return
}
```

这个限速器**只针对已建立会话的失效重连**，用内存计数即可（重启清零无所谓，它防的是流量而非爆破）。
**登录接口的失败计数是另一回事**，走 `login_failures` 表持久化 —— 那个防的是密码爆破。

配套**日志降噪**：同一 IP 的鉴权失败日志按分钟聚合，不要一次一条。审计表同理 ——
重连失败不写 `audit_log`，只有登录尝试才写，否则一次风暴能刷进几万行。

#### (f) 滑动续期，避免所有会话同时到期

`auth.session.idle_timeout` 生效时，中间件在令牌剩余寿命 < TTL/2 时重发 Cookie。
活跃用户的到期时间因此天然错开，从源头削掉惊群。

### 9.4 SSE 侧的对应改动

```js
es.onerror = (e) => {
    // readyState === CLOSED 表示浏览器已放弃（非 200 响应就是这种情况）
    if (es.readyState === EventSource.CLOSED) {
        authStore.recheck();   // 确认是不是鉴权失败，再决定跳登录还是提示网络错误
        return;
    }
    // CONNECTING 是浏览器在自动重连，交给它
};
```

**同时必须做的**：`resourceMonitorWorker.js` / `serverResourceWorker.js` / `sharedResourceWorker.js`
三个 SSE Worker 也要接同样判定，否则各自会维持一条无限重连的 SSE。

### 9.5 一句话总结

> 鉴权失败断开**本身**不造成风暴；「把鉴权失败当网络抖动、用固定间隔无限重连」才造成风暴。
> 做到「未登录不发起 + 4401 视为致命 + 指数退避全抖动 + 服务端限速兜底」，风暴就不存在。

---

## 10. 首次启动引导与救援路径

### 10.1 零用户状态

`auth.enabled: true` 但 `users` 表为空（含 `auth.db` 不存在、刚建完空表）时：

- 除 `/api/auth/setup*` 与静态资源外，所有 API 返回 `401 {"code":"setup_required"}`
- 前端 `authStore` 见到 `setup_required` → 跳 `/setup`，展示「创建管理员账号」表单
- `POST /api/auth/setup` 仅在零用户时可用，创建后立刻失效（再调返回 409）
- **该接口只接受来自 loopback 的请求**（`RemoteAddr` 是 127.0.0.1/::1 且无 XFF）。
  防止服务恰好暴露在公网时被人抢注管理员

启动日志与 GUI 里打印醒目提示：

```
[鉴权] 已启用鉴权但尚未创建任何账号。
       请在本机浏览器打开 https://localhost:19193/#/setup 创建管理员账号。
```

### 10.2 忘记密码 / 丢失 2FA 设备 / 丢失 Passkey

GUI 与 CLI 都跑在本机，等价于物理访问，所以本地 CLI 救援是合理的（不是安全漏洞）。
完整命令见 §11.2。

**这些 CLI 命令因为 SQLite 变成了必需品而不是便利品** —— `auth.db` 不像 JSON 那样能手改，
所以 CLI 是唯一的本地救援通道，必须在阶段二一并交付，不能推迟。

CLI 与运行中的 API 服务会**同时打开同一个 `auth.db`**。WAL + `busy_timeout(5000)` 使这是安全的
（SQLite 本身支持多进程访问），但 CLI 改完之后**运行中的服务不会知道** —— 它的内存副本（§3.2）还是旧的。因此：

- CLI 写操作完成后自动尝试调一次本地 `POST https://127.0.0.1:<port>/api/auth/reload`
  （仅 loopback 可访问、无需鉴权），让运行中的服务重新加载内存副本
- 调用失败（服务没在跑）则打印「改动已写入，将在服务下次启动时生效」

最后的核选项：删掉 `{BaseDir}/database_file/auth.db` → 回到零用户状态 → 走 §10.1 引导。
写进 README。**注意 WAL 模式下要连 `auth.db-wal` / `auth.db-shm` 一起删。**

---

## 11. CLI 工具集

沿用现有 `urfave/cli` 结构，在 `main.go` 的 `Commands` 里新增两个顶层命令。
它们与现有的 `state clear`（Badger 状态库维护）是同一层级的东西。

这些命令**不触发管理员提权**（`ensureAdminElevation` 只对 `api` 子命令生效），
但需要 `appconfig.Load()` 已执行 —— 现有 `main()` 的顺序天然满足。

### 11.1 `asa-server db` —— SQLite 迁移与维护

```bash
asa-server.exe db status                  # 当前 schema 版本 + 待执行迁移列表
asa-server.exe db migrate                 # 应用所有待执行迁移（默认先自动备份）
asa-server.exe db migrate --dry-run       # 只打印将要执行什么，不改库
asa-server.exe db migrate --no-backup     # 跳过自动备份
asa-server.exe db migrate --force         # 跳过「服务正在运行」检查
asa-server.exe db verify                  # PRAGMA integrity_check + foreign_key_check
asa-server.exe db backup [--out PATH]     # VACUUM INTO 一致性备份
asa-server.exe db vacuum                  # 回收空间（审计日志滚动淘汰后）
```

> 作用域说明：这些命令只操作 `auth.db`。项目里的其它持久化（Badger 状态库、
> `schedules.json`、实例 INI）不归它管，见 §3.4。

#### 迁移引擎

```go
// auth/migrate.go
type Migration struct {
    Version int
    Name    string
    Up      func(*sql.Tx) error
}

// 只追加，永不修改已发布的迁移函数
var migrations = []Migration{
    {1, "initial_schema", m001Initial},
    {2, "webauthn_credentials", m002WebAuthn},
    // ...
}
```

要点：

1. **每个迁移独立事务。** SQLite 的 DDL 是事务性的，所以一个迁移失败只会回滚它自己，
   数据库停留在上一个**完整**版本，不会半迁移。
2. **降级保护。** 如果 DB 里的 `schema_version` 大于二进制已知的最大版本，
   说明用户把 exe 降级了 —— **报错退出，不要尝试运行**，否则新表结构配旧代码会静默写坏数据：
   ```
   错误：数据库 schema 版本 (5) 高于本程序支持的版本 (3)。
        这通常意味着你把 asa-server.exe 降级了。请使用 v1.x 或更高版本，
        或从备份恢复 auth.db。
   ```
3. **迁移前自动备份。** 用 SQLite 原生的 `VACUUM INTO 'auth.db.bak-v2-20260728T143000'` ——
   这是在线一致性备份，比直接复制文件安全（复制文件时 WAL 里可能有未合并的事务）。
   成功后保留最近 5 份，更旧的删掉。
4. **运行中的服务检测。** 迁移时如果 API 服务正在跑，它的内存副本和 schema 都会失配。
   `db migrate` 先尝试 TCP 连一下 `127.0.0.1:<port>`，端口开着就拒绝：
   ```
   错误：检测到 API 服务正在 19193 端口运行。请先执行 asa-server service stop
        （或关闭 GUI）再迁移。确认无误可加 --force 跳过此检查。
   ```
5. **`--dry-run` 输出格式**：
   ```
   当前版本: 1
   待执行迁移:
     2  webauthn_credentials
     3  add_audit_actor_column
   共 2 个迁移待执行。未做任何改动（--dry-run）。
   ```

#### 启动时自动迁移

`auth.database.auto_migrate: true`（默认）时，`InitializationBasicComponents()` 里自动执行。
理由：这是自托管单机工具，用户换个 exe 就期望能用；只靠 CLI 会让一部分人换完 exe 服务起不来还不知道为什么。

**失败处理**必须谨慎 —— 分三种情况：

| 情况 | 处理 |
|------|------|
| `auth.enabled: false` | **完全不碰 `auth.db`**，不打开也不迁移。鉴权关着的时候数据库问题不该影响任何东西 |
| `auth.enabled: true` 且迁移失败 | **拒绝启动 API 服务**，日志给出明确指引（`asa-server db migrate --dry-run` 诊断 / 从备份恢复）。不能「静默降级为不鉴权」—— 那是把安全故障变成安全漏洞 |
| `auth.enabled: true` 且 `integrity_check` 失败（库损坏） | 同上拒绝启动，并提示核选项：删库回到零用户引导（§10.2） |

### 11.2 `asa-server user` —— 用户管理与救援

```bash
asa-server.exe user list                        # 表格：用户名 / 角色 / 2FA / Passkey 数 / 锁定状态 / 最后登录
asa-server.exe user add <name> [--role admin|operator]   # 密码交互式输入，不可省略
asa-server.exe user passwd <name>               # 重置密码（交互式，无回显）
asa-server.exe user passwd <name> --random      # 生成随机强密码，打印一次
asa-server.exe user passwd <name> --stdin       # 从 stdin 读，供脚本使用
asa-server.exe user role <name> <admin|operator>
asa-server.exe user disable <name> / enable <name>
asa-server.exe user delete <name>
asa-server.exe user unlock <name>               # 清除登录失败锁定
asa-server.exe user totp-reset <name>           # 解绑 2FA（丢手机）
asa-server.exe user webauthn-list <name>        # 列出该用户的 Passkey（含 rp_id）
asa-server.exe user webauthn-reset <name>       # 清空该用户全部 Passkey（丢设备）
asa-server.exe user audit [--user X] [--limit 50] [--event login_fail]
```

#### `user passwd` 的实现要点

```go
// 交互式读取，Windows 控制台同样有效
fmt.Print("新密码: ")
pw1, err := term.ReadPassword(int(syscall.Stdin))   // golang.org/x/term
fmt.Println()
fmt.Print("确认新密码: ")
pw2, _ := term.ReadPassword(int(syscall.Stdin))
fmt.Println()
```

**不要**提供 `--password <明文>` 参数 —— 明文密码会进 PowerShell 历史
（`ConsoleHost_history.txt`）和进程命令行，任何本机进程都能看到。`--stdin` 覆盖脚本化需求。

一次成功的重置必须在**同一个事务**里做完四件事：

1. 写入新 `password_hash`（校验长度符合 `auth.password.min_length`）
2. `session_version++` —— 踢掉该用户所有已登录设备
3. 清除 `login_failures` 中该用户的锁定记录（重置密码顺手解锁，符合直觉）
4. 写 `audit_log`，`event=passwd_reset`、`actor='cli'`

然后调 `/api/auth/reload`（§10.2），并打印：

```
已重置用户 admin 的密码。
所有已登录设备已被登出。
✓ 已通知运行中的服务重新加载（无需重启）
```

`--random` 的输出要明确「只显示这一次」：

```
已为用户 admin 生成新密码：

    Xk7#mQ2vLp9$wRt4Ns8B

请立即保存 —— 此密码不会再次显示。
```

随机密码用 `crypto/rand` 从一个排除易混淆字符（`0O1lI`）的字符集里取 20 位。

---

## 12. 前端改动清单

### 12.1 新增文件

| 文件 | 内容 |
|------|------|
| `src/views/Login.vue` | **密码表单为主路径且恒存在**，`webauthn_available` 时才在下方追加「使用 Passkey 登录」（§7.11 的布局）；TOTP 第二阶段；失败提示区分「密码错」「已锁定，剩余 X 分钟」「验证码错」 |
| `src/views/Setup.vue` | 首次创建管理员 |
| `src/views/UserManager.vue` | 用户表格（TDesign `t-table`）+ 新增/编辑/重置密码/重置 2FA/清空 Passkey 弹窗 + 审计日志抽屉 |
| `src/views/Profile.vue` | 个人：改密码、绑定/解绑 2FA（二维码 + 恢复码）、Passkey 列表（按 RP ID 分组）、登出所有设备 |
| `src/store/authStore.js` | `reactive({authEnabled, authenticated, bypassed, setupRequired, user, webauthnAvailable, ready})` + `login/logout/recheck` |
| `src/apis/authApi.js` | 上述接口封装 |
| `src/utils/webauthn.js` | base64url ⇄ ArrayBuffer 转换 + `navigator.credentials` 包装 |

### 12.2 修改文件

| 文件 | 改动 |
|------|------|
| `src/utils/http.js` | 请求拦截器加 `X-Requested-With`；`withCredentials: true`；响应拦截器捕获 401 → `authStore.setUnauthenticated()` + 跳 `/login`（**加去重锁，防止并发 401 触发多次跳转**） |
| `src/utils/wsManager.js` | 连接前检查 `authStore`；处理 `WS_AUTH_FAILED` |
| `src/workers/wsWorker.js` | 指数退避 + 全抖动；`setInterval` → 递归 `setTimeout`；close code 4401 判定为致命 |
| `src/workers/*ResourceWorker.js` | SSE `onerror` 的 CLOSED 判定 |
| `src/utils/utils.js` | **DEV 模式下 WS/SSE 改走 vite 代理同源地址**（见 §12.3） |
| `src/router/index.js` | 全局 `beforeEach` 守卫 + 新增 4 条路由 |
| `src/App.vue` | 顶栏用户菜单（用户名 / 个人设置 / 用户管理 / 退出）；`auth.enabled=false` 时整块隐藏 |

### 12.3 开发模式的跨站 Cookie 问题（必须解决）

当前 `utils.js` 里，DEV 模式的 WS 和 SSE **绕过 vite 代理直连** `https://localhost:19193`，
而页面在 `http://localhost:3000`。上 Cookie 后这就变成**跨站**请求：

- 端口不同不影响 same-site 判定，但 **scheme 不同（http vs https）就是 cross-site**
- 于是 `SameSite=Lax` 的 Cookie **不会**被带上 → 开发环境 WS/SSE 全部 401
- 改成 `SameSite=None` 又要求 `Secure`，而 dev 页面是 http，浏览器会拒绝写入

**解决办法：DEV 模式也走 vite 代理。** `vite.config.js` 的代理已配 `ws: true`，改 `utils.js` 即可：

```js
export function buildWebSocketUrl(url) {
    // DEV 与生产统一走同源，交给 vite 代理转发到后端。
    // 直连后端会造成 http(页面) → https(后端) 的跨站，Cookie 带不过去。
    const protocol = location.protocol === 'https:' ? 'wss://' : 'ws://';
    return urlJoin(protocol + location.host, url, `?clientId=${generateClientId()}`);
}

export function buildEventSourceUrl(url) {
    return urlJoin(location.origin, url);
}
```

顺带好处：dev 环境不再需要把本地 CA 装进系统信任存储才能用 `EventSource`。

> **WebAuthn 在 dev 模式下的额外注意**：页面是 `http://localhost:3000`，RP ID 会是 `localhost`，
> 而生产是 `https://localhost:19193` 或反代域名 —— RP ID 相同（都是 `localhost`）但 **Origin 不同**。
> 所以 `webauthn.extra_origins` 在开发时要加上 `http://localhost:3000`，或者干脆在 dev 环境不测 WebAuthn。

### 12.4 路由守卫

```js
router.beforeEach(async (to) => {
    if (!authStore.ready) await authStore.recheck();      // 首次进入拉一次 /api/auth/state
    if (!authStore.authEnabled || authStore.bypassed) return true;
    if (authStore.setupRequired && to.name !== 'Setup') return { name: 'Setup' };
    if (!authStore.authenticated && to.name !== 'Login') {
        return { name: 'Login', query: { redirect: to.fullPath } };
    }
    if (to.meta?.requiresAdmin && authStore.user?.role !== 'admin') return { name: 'ServerManager' };
    return true;
});
```

`authStore.recheck()` 内部要做**单飞（single-flight）**：多个路由/组件并发调用只发一次请求。

---

## 13. 实施顺序

分六个阶段，每阶段可独立编译、独立验证、独立回滚。

### 阶段一：`appconfig` 落地（不碰鉴权）
1. `go get github.com/spf13/viper`
2. `appconfig` 包 + `Config` 结构 + `Load` + 默认值 + `Validate`
3. `main.go` 接入；`webapi.ApiServerPort/EnableTLS/...` 的默认值来源改为 `appconfig`，CLI flag 显式设置时覆盖
4. 验证：`go build` + `go vet`，删掉 `config.yaml` 能自动生成，改端口能生效
5. **此阶段结束时功能行为与现在完全一致**，可先合入

### 阶段二：`auth` 领域包 + CLI（不接路由）
1. `go get github.com/pquerna/otp golang.org/x/term`
   （`modernc.org/sqlite` 无需 `go get`，`go mod tidy` 会把它从 `// indirect` 提升为直接依赖）
2. `db.go` + `migrate.go` + `migrations.go` 先行 —— schema 定下来后面才好写
3. `user.go` / `password.go` / `token.go` / `denylist.go` / `netmatch.go` / `ratelimit.go` / `totp.go` / `audit.go`
4. `cache.go` 内存副本层：加载、失效、与写操作的一致性
5. 单元测试（重点，见 §14）
6. **`asa-server db *` 与 `asa-server user *` CLI —— 本阶段必须交付**，
   它们是 SQLite 方案下唯一的本地救援通道
7. 此阶段结束时 HTTP 层零改动

### 阶段三：后端接入（不含 WebAuthn）
1. `webapi/authapi`：middleware + handler + users + setup
2. `actions.go` 挂中间件、改 CORS
3. `realtime/ws.go` 加 Upgrade 前鉴权 + 4401 关闭码 + 心跳循环里的令牌复检
4. 审计所有 SSE handler，确认 401 在写响应体之前
5. 用 curl / wscat 验证（此时前端会 401，属预期）

### 阶段四：前端接入（不含 WebAuthn）
1. `authStore` + `authApi` + `http.js` 拦截器
2. Login / Setup / UserManager / Profile 四个页面
3. 路由守卫 + App.vue 用户菜单
4. `utils.js` 的 DEV 同源改造（§12.3）

### 阶段五：WebAuthn
1. `go get github.com/go-webauthn/webauthn`
2. 迁移 `m002WebAuthn` 建 `webauthn_credentials` 表 + `users.webauthn_handle` 列
   （**存量用户要在迁移里补随机 handle**，不能留 NULL）
3. `auth/webauthn.go` + `ceremony.go`；`authapi/webauthn.go` 路由
4. 前端 `utils/webauthn.js` + Login/Profile 的 Passkey 入口（密码表单不动，只是多渲染一个按钮）
5. **必须在三种 Host 下各测一遍**：`https://localhost:19193`（在 `domains` 内）、
   反代域名（在 `domains` 内）、`https://192.168.x.x:19193`（不可能在 `domains` 内），
   验证「凭证不跨 RP ID」符合预期，且第三种情况下入口正确隐藏、密码登录照常可用

> WebAuthn 单独放在最后，因为它是四条链路里唯一有**外部环境依赖**的（域名、证书、认证器硬件）。
> 前四个阶段做完，系统已经是一个完整可用的鉴权方案；WebAuthn 做不完也不影响交付。

### 阶段六：连接风暴治理
1. `wsWorker.js` 指数退避 + 全抖动 + 4401 致命判定
2. 三个 SSE Worker 的 CLOSED 判定
3. 服务端鉴权失败限速 + 日志聚合
4. 压测验证（§14.3）

> 阶段六**可以提前到任何时候做** —— 指数退避本身就是现有代码的改进，与鉴权无关。
> 想降低风险的话，建议先单独做退避改造并观察一段时间。

---

## 14. 测试要点

### 14.1 单元测试（`auth` 包）

| 用例 | 断言 |
|------|------|
| `token`：签发→校验 | 往返一致；篡改 payload/签名任一字节 → 拒绝 |
| `token`：过期 | exp 已过 → 拒绝 |
| `token`：版本吊销 | `session_version` 从 1 → 2 后，旧令牌拒绝 |
| `token`：pre-auth 隔离 | `stage:"pre"` 的令牌过不了 `VerifyToken` |
| `netmatch` | 127.0.0.1/10.x/172.16.x/192.168.x/::1/fe80:: → true；8.8.8.8/2001:4860:: → false |
| `netmatch`：IPv4-mapped IPv6 | `::ffff:8.8.8.8` **不能**被误判为内网（易错点） |
| `totp` | `totp.GenerateCode` 造码校验通过；同一步重放 → `ErrCodeReused`；skew±1 边界 |
| `ratelimit` | 达阈值锁定；窗口滑过后解锁；不同 IP/用户互不影响 |
| **`ratelimit` 持久化** | 锁定后关闭 DB、重新 `Open` 同一文件 → **锁定仍然生效**（选 SQLite 的核心理由，必须有测试守住） |
| `user` | 最后一个 admin 不可删/不可禁用/不可降级；用户名大小写去重（靠 `username_lower UNIQUE`） |
| `user` | `password_hash` 恒非空：创建时空密码被拒，无任何路径可把它清空 |
| `migrate` | 空库 → 建表成功；重复调用幂等；`schema_version` 正确推进 |
| **`migrate` 降级保护** | DB version > 代码已知最大版本 → 报错，**不做任何写入** |
| `migrate` 失败回滚 | 故意让某个迁移返回 error → `schema_version` 停留在上一版本，表结构未半改 |
| `foreign_keys` | 删用户后其 `recovery_codes` 与 `webauthn_credentials` 一并消失（验证 PRAGMA 真的开了） |
| `denylist` | 加入后令牌被拒；过期行被清理 |
| `audit` | 超过 `max_rows` 时旧行被淘汰、新行保留 |
| **`webauthn`：`MatchDomain`** | 精确命中 → 返回该域名；IP、未列入的域名、空 `domains` → `ok=false`；带端口的 Host 正确剥离；FQDN 尾点 `example.com.` 正确归一 |
| **`webauthn`：父域名匹配** | 配 `example.com` 时 `ark.example.com` 命中且 RP ID 为 `example.com`；同时配了两者时 `ark.example.com` **精确匹配优先**；`notexample.com` **不得**被 `example.com` 命中（后缀判断必须带 `.`） |
| `webauthn`：`originsFor` | 父域名场景下实际访问的子域名也在 Origin 列表里；端口非 443 时同时给出带端口与不带端口两种形式 |
| `webauthn`：配置校验 | `domains` 含 IP / 带协议 / 带端口 / 带路径 → `Validate()` 报错，且错误信息指出是第几项 |
| `webauthn`：可用性判定 | 五种 `reason` 各自可复现；`available=true` 时 `rp_id` 非空 |
| **`webauthn`：凭证按 RPID 隔离** | 同一用户在 `localhost` 和 `example.com` 下的凭证互不可见 |
| `webauthn`：sign_count | `0/0` 视为不支持不告警；计数倒退触发 CloneWarning |
| `webauthn`：user handle | 每个用户唯一、32 字节、不含用户名 |

DB 测试用临时目录里的真实 `auth.db` 文件，**不要用 `:memory:`** —— WAL、`busy_timeout`、
多进程访问这些恰恰是文件模式才有的行为，内存库测不到。

### 14.2 中间件测试（`httptest`）

| 场景 | 期望 |
|------|------|
| `auth.enabled=false` | 一切放行，无 Cookie 也 200，且**完全不打开 `auth.db`** |
| 豁免路径 | 无 Cookie 200 |
| 非 `/api` 路径 | 无 Cookie 200（静态资源可达） |
| `lan_bypass=true` + RemoteAddr 127.0.0.1 + 无 XFF | 放行 |
| **`lan_bypass=true` + RemoteAddr 127.0.0.1 + 有 XFF** | **拒绝**（§4.2 的核心用例，必须有） |
| `lan_bypass=true` + RemoteAddr 公网 IP | 拒绝 |
| SSE 请求（`Accept: text/event-stream`）未登录 | 401，`Content-Type` **不是** `text/event-stream` |
| WS 请求（`Upgrade: websocket`）未登录 | 401，**无** `101` 响应 |
| `/api/auth/reload` 来自非 loopback | 拒绝 |

### 14.3 连接风暴回归测试

手工脚本即可，不必进 CI：

1. 开 10 个浏览器标签页，全部登录
2. 后端执行 `logout-all`（模拟全员会话失效）
3. 观察 60 秒内 `asaServer.log` 的 WS/SSE 请求条数
   - **期望**：每标签页 ≤ 2 次尝试后停止，总计 ≤ 20 条，且全部标签页跳到登录页
   - **失败信号**：条数持续增长不收敛 → 4401 判定或退避没生效
4. 重新登录 → 所有标签页 WS 自动恢复

### 14.4 手工验收清单

**基础**
- [ ] `auth.enabled=false` 时，行为与升级前 100% 一致（回归保护）
- [ ] 首次启用 → setup 页 → 创建 admin → 正常登录
- [ ] setup 接口从非 loopback 访问被拒
- [ ] operator 角色访问 `/api/users` 返回 403
- [ ] 改密码后其他设备立即被踢
- [ ] 连续 5 次密码错 → 锁定 15 分钟，提示剩余时间
- [ ] **锁定期间重启服务 → 仍然处于锁定状态**（JSON/内存方案在这里会失败）
- [ ] 反代（frpc http 类型）访问需登录；本机直连（lan_bypass 开启时）不需要
- [ ] 反代（frpc **tcp** 类型 + lan_bypass 开启）→ 确认能绕过 → 文档里必须警告这一点

**2FA**
- [ ] 绑定 2FA → 扫码 → 确认 → 恢复码可下载 → 退出 → 用 2FA 登录
- [ ] 用恢复码登录一次，该码失效
- [ ] `user totp-reset` CLI 能救回丢手机的账号

**WebAuthn**
- [ ] `https://localhost:19193` 下注册 Passkey → 退出 → Passkey 登录成功，且**不再要求 TOTP**
- [ ] 同一账户在反代域名下**必须重新注册**，且列表按 RP ID 正确分组显示
- [ ] 用 `https://192.168.x.x:19193` 访问 → Passkey 入口**隐藏**，密码登录照常可用；
      Profile 页提示「当前访问地址不支持 Passkey」
- [ ] 域名**不在** `domains` 内时同上（`reason=domain_not_allowed`）
- [ ] `domains: []` 但 `enabled: true` → 启动 WARN，全站行为等同未启用
- [ ] `domains` 写成 IP / 带 `https://` / 带端口 → **启动即报错**并指出是第几项
- [ ] 配 `example.com` 后从 `ark.example.com` 访问 → 可用，`rp_id` 显示为 `example.com`
- [ ] 把 `domains` 从 `ark.example.com` 改成 `example.com` → 旧凭证在列表里标注「当前域名下不可用」，
      密码登录不受影响
- [ ] 用户取消系统弹窗 → 静默返回密码表单，**不计入**失败限流
- [ ] 断言失败 → 提示改用密码并把焦点移到密码框，不卡在只能重试的页面
- [ ] 删除最后一个凭证 → **允许**，之后仍能用密码登录（验证补充定位）
- [ ] `user webauthn-reset` CLI 能清空凭证救回账号

**CLI**
- [ ] `db status` 在全新环境与已迁移环境下输出正确
- [ ] `db migrate --dry-run` 不产生任何写入
- [ ] `db migrate` 自动生成备份文件，且备份可用（换名后能被 `db verify` 通过）
- [ ] 服务运行中执行 `db migrate` → 被拒绝并提示先停服务；`--force` 可越过
- [ ] 人为把 `schema_version` 改大 → `db migrate` 报降级错误且不写入
- [ ] `user passwd` 交互式输入**不回显**，且不出现在 PowerShell 历史里
- [ ] `user passwd --random` 输出的密码可用于登录，且提示「只显示一次」
- [ ] 服务运行中用 CLI 改密码 → `/api/auth/reload` 后立即生效，无需重启
- [ ] `user audit` 能看到上述所有操作的记录，含来源 IP 与 `actor=cli`

---

## 15. 已知取舍与不做的事

| 决定 | 理由 |
|------|------|
| 用 SQLite 而非 JSON 文件存用户 | §3.3：驱动已在二进制中（ARK 存档就是 SQLite），且失败锁定/WebAuthn 凭证/审计日志用 JSON 做不好 |
| 用 SQLite 而非复用 Badger | §3.3：生命周期对不上，另开实例内存代价不成比例，凭证与审计是关系型/查询型负载 |
| **SQLite 只用于鉴权** | §3.4：其余持久化（Badger 状态、schedules.json、实例 INI）一律不动，回滚干净 |
| 不做服务端 session store（即便有了 SQLite） | §3.2：无状态令牌下服务重启不踢人，且吊销语义已完整 |
| WebAuthn 只作为密码登录的**补充**，永不替代 | §7.1：总有访问路径（IP、未列入的域名、非安全上下文）在规范层面用不了 WebAuthn。允许纯 Passkey 账户等于给用户留一个换入口就自锁的陷阱 |
| WebAuthn 由 `domains` 白名单显式开启，不自动推导 | §7.2：配错时启动即报错，比「按钮不出现」好排查；也避免从 `tls.domains` 继承出意料之外的 RP ID |
| 不做 attestation 校验（不验证认证器厂商） | 企业设备合规场景才需要；本项目验了只会平白挡掉一堆合法认证器 |
| 不做 OIDC / LDAP / OAuth | 单机管理面板，引入外部 IdP 的复杂度远超收益 |
| 不做细粒度 RBAC（按实例授权） | 两个角色够用；真需要时再加，`role` 字段已预留 |
| 不做双监听端口区分内外网 | §4.5：改动大，本期用 XFF 判定 + 默认关闭替代 |
| TOTP secret 明文存储 | §3.5：服务端必须能读明文才能校验，加密只是转移问题 |
| 不加密 `auth.db` | 同上；靠 0600 文件权限 + 不入备份 |
| 静态资源不鉴权 | §5.2：无安全收益，只会让登录页打不开 |
| GUI（Fyne）本地界面不加鉴权 | 能启动 GUI 的人已有物理/RDP 访问权 |
| 不把 `state` 从 Badger 迁到 SQLite | 独立议题，与鉴权无关，不顺手做 |

---

## 16. 风险登记

| 风险 | 等级 | 缓解 |
|------|------|------|
| lan_bypass + 本机反代无 XFF → 鉴权完全失效 | **高** | 默认关闭 + XFF 强制判定 + 启动 WARN + 文档三处警告 |
| WebAuthn 在部分访问路径下不可用 | **低**（已由设计降级） | 域名闸门 + 后端下发可用性 + 密码恒可用兜底。最坏情况只是「看不到 Passkey 按钮」，不是「登不上」 |
| `webauthn.domains` 配错导致功能静默不生效 | 中 | §7.2：非法项启动即报错；`domains` 为空但 `enabled=true` 时 WARN；Profile 页显示当前 `rp_id` 便于自查 |
| 迁移失败导致服务起不来 | 中 | 自动备份 + `--dry-run` 诊断 + 降级保护 + `auth.enabled=false` 时完全不碰库 |
| 迁移时服务正在运行造成 schema 与内存副本失配 | 中 | `db migrate` 检测端口占用并拒绝，需显式 `--force` |
| 前端重连风暴打爆 CPU / 日志 | 中 | §9 六条措施 + §14.3 回归测试 |
| 用户忘记密码且无 CLI 访问 | 中 | CLI 救援 + 删 `auth.db` 重置，写进 README |
| 服务器时钟漂移导致 2FA 全员登不上 | 中 | skew=1 + 恢复码 + CLI `totp-reset` + 错误提示带排查建议 |
| `auth.db` 损坏（断电、磁盘满） | 中 | WAL 可自恢复；`db verify` 诊断；损坏时拒绝启动并提示删库重建，**不静默降级为不鉴权** |
| 用户丢失所有认证器 | **低**（已由设计消除） | 密码恒可用，直接用密码登录即可；必要时 `user webauthn-reset` 清理残留凭证 |
| 改动 `webauthn.domains` 使已注册凭证失效 | 低 | §7.2：密码兜底不会锁人；Profile 页对失效分组标注并提供删除 |
| `config.yaml` 手改出语法错误导致起不来 | 中 | `Load` 失败时记 ERROR 并用默认值继续（`auth.enabled` 默认 false → 不会把人锁在外面） |
| CLI 改了库但运行中的服务用旧内存副本 | 低 | `/api/auth/reload`（仅 loopback）+ CLI 自动调用，见 §10.2 |
| CORS 改动影响现有 dev 流程 | 低 | §12.3 改为同源，反而简化 dev |
| 升级后老用户被意外锁定 | 低 | `auth.enabled` 默认 false，升级零影响 |

---

## 17. 实施记录

六个阶段全部完成。以下是实施过程中**偏离原设计**或**新发现**的地方，都已按下述方式解决。

### 17.1 Windows 服务模式下 config.yaml 会被静默忽略（已修）

原计划只把配置接到 CLI flag 的 `Value` 上。但 `main.go` 在检测到服务模式时会
`winservice.RunService(); return`，**`app.Run()` 根本不执行**，flag 的 `Destination`
永远不会被写入 —— 而「装成 Windows 服务」正是本项目最主要的部署方式。

解决：新增 `applyAppConfig()`，在服务分支之前直接把配置写进 `webapi` 的包级变量。
交互式运行时它随后会被 flag 解析覆盖成同样的值（未传参）或命令行值（传了参），两条路径都正确。

### 17.2 配置出错时「回落默认值继续跑」是个安全漏洞（已改为失败关闭）

§16 原本写的是「`Load` 失败时记 ERROR 并用默认值继续启动（`auth.enabled` 默认 false →
不会把人锁在外面）」。实施时发现这条规则有个危险的反面：用户明明写了
`auth.enabled: true`，只要 `webauthn.domains` 里有一个拼写错误，服务就会**静默地
不带鉴权启动** —— 而这台机器很可能正暴露在公网上。

解决：`appconfig` 新增 `ErrAuthConfigInvalid`。配置有错**且该配置明确要求开启鉴权**时，
`main.go` 直接 `log.Fatal` 拒绝启动；鉴权本来就关着的话，仍按原方案回落默认值继续。
配置错误应该表现为「起不来」，不该表现为「安全防护悄悄消失了」。

### 17.3 `user unlock` 只解一半锁（已修）

限流按用户名和来源 IP **两个维度独立计数**。单管理员的服务器上所有失败尝试都来自
同一台机器，两个维度必然同时被锁 —— 只解开用户维度，用户会发现「解锁了还是登不上」。

解决：`user unlock <name>` 默认同时清除 IP 维度的锁定并打印清了多少条，
`--keep-ip-locks` 可保留。Web 端的 `/api/users/:username/unlock` 保持只解用户维度
（那里管理员和被解锁的用户通常不在同一个 IP）。

### 17.4 审计日志缺来源 IP（已修）

`CreateUser`、`ChangePassword` 这些领域方法本身不接触 HTTP，写审计时拿不到来源 IP，
结果管理员操作的记录全是 `ip=-`。给每个方法加两个参数会污染整条调用链。

解决：`auth.WithAuditSource(ctx, ...)` 把来源挂在 context 上，中间件在请求入口设置一次，
`Manager.Audit` 在字段为空时从 context 补齐。

### 17.5 CORS 的向后兼容

`cors.Default()` 是 `AllowAllOrigins`，而浏览器禁止它与凭证共存。但直接删掉会影响
现有的跨域用法。最终规则：`server.cors.allowed_origins` 非空 → 用显式配置 + `AllowCredentials`；
留空且**鉴权关闭** → 保持 `cors.Default()`（行为与升级前一致）；留空且鉴权开启 → 不装 CORS（仅同源）。

### 17.6 前端路径前缀

项目约定是 API 路径里带 `/api`，dev 下由 vite 代理剥掉 `VITE_API_ROOT` 那一层。
`buildWebSocketUrl` / `buildEventSourceUrl` 改成同源之后必须补上这层前缀
（`basePrefix()`），生产下则用 `window.location.pathname` 以保留子路径部署支持。

### 17.7 测试覆盖

| 包 | 重点用例 |
|----|---------|
| `appconfig` | 模板自身可被解析（否则第二次启动就报错）、非法 domains 的四类写法、鉴权开启时的致命错误识别 |
| `auth` | 迁移降级保护与失败回滚、外键级联、**锁定跨重启仍生效**、令牌篡改/过期/版本吊销/pre-auth 隔离、TOTP 重放与 skew 边界、IPv4-mapped IPv6 不被误判为内网、域名闸门（含 `notexample.com` 不被 `example.com` 误命中） |
| `webapi/authapi` | 鉴权关闭时不创建 auth.db、静态资源放行、**SSE 拒绝响应不得是 `text/event-stream`**、WS 不得升级、**lan_bypass + XFF 必须拒绝** |

端到端还实测了：零用户引导 → setup（仅 loopback）→ 登录 → 会话 Cookie 属性 →
单设备登出隔离 → operator 403 → 管理员重置密码踢掉旧会话 → 限流锁定 → CLI 救援 →
`/api/auth/reload` 免重启生效 → WebAuthn 域名闸门在 10 种 Host 下的判定。

### 17.8 SSE Worker 的鉴权闸门

实际只有两个 SSE Worker（`serverResourceWorker` / `sharedResourceWorker`，
CLAUDE.md 里提到的 `resourceMonitorWorker` 已不存在）。两者原本都是固定 10 秒
`setInterval` 无限重连，和 `wsWorker` 一样存在相位锁定问题，已一并改为
指数退避 + 全抖动 + 递归 `setTimeout`。

鉴权判定上 SSE 比 WebSocket 麻烦：`EventSource` **拿不到 HTTP 状态码**，
服务端返回 401 时 Worker 只知道 `readyState` 变成了 `CLOSED`，
无法区分"会话过期"和"服务器挂了"。所以：

- Worker 在 CLOSED 时发 `SSE_CHECK_AUTH` 给主线程
- 主线程（`utils/sseAuthGate.js`）去问 `/api/auth/state`，多个 Worker 并发上报时合并成一次查询
- 结论以 `AUTH_BLOCKED` / `AUTH_RESUMED` 发回 Worker；登录成功后自动广播 `AUTH_RESUMED`

### 17.9 已知未做

- 前端 `Login.vue` 的 Passkey 按钮、`Profile.vue` 的凭证管理已接好，但**没有在真实认证器上跑过**
  （沙箱环境无法调用 `navigator.credentials`）。§14.4 的 WebAuthn 手工验收清单仍需在真机上过一遍。
- 前端未做单元测试（项目本身没有前端测试基建），前端逻辑靠端到端与人工验证。
