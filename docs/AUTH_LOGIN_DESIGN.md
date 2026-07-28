# 鉴权 + 登录页面 开发方案

> 状态：设计稿（未实施）
> 涉及包：新增 `appconfig/`、`auth/`、`webapi/authapi/`；改动 `webapi/actions.go`、`realtime/`、各 SSE handler、`main.go`、前端 `app/`
> 关联文档：`docs/HTTP2_CONNECTION_OPTIMIZATION.md`（TLS / h2 前提）、`docs/PACKAGE_RESTRUCTURE_PLAN.md`（分层约束）

---

## 0. 目标与约束

| 需求 | 落点 |
|------|------|
| 引入 viper + yaml 应用配置 | 新包 `appconfig`，配置文件 `{BaseDir}/config.yaml` |
| 鉴权可通过配置开关 | `auth.enabled: false`（默认关，保证升级不锁死存量用户） |
| 内网/本机免鉴权，仅反代出网需要鉴权 | `auth.lan_bypass.enabled`，默认 **false**（见 §4 的安全陷阱） |
| 两步验证（可选开启） | `github.com/pquerna/otp`，**按用户**开关，全局可强制 |
| 前端登录页 + 用户管理页 | `app/src/views/Login.vue`、`app/src/views/UserManager.vue` |
| 后端对应接口 | `webapi/authapi`，`/api/auth/*`、`/api/users/*` |
| WebSocket 鉴权失败断开会不会连接风暴 | **会**，按现有重连实现必然会。§8 给出完整规避方案 |

硬约束：

1. **不能破坏 SSE / WebSocket。** 项目大量依赖 `EventSource` 和浏览器 `WebSocket`，两者都**无法设置自定义请求头**。因此 `Authorization: Bearer` 方案在本项目不成立 —— 鉴权凭证必须走 **Cookie**（唯一在 REST / SSE / WS 三条链路上行为一致的载体）。
2. **不能破坏分层。** `appconfig` 是叶子包（只依赖 `pkg/*`），`auth` 依赖 `appconfig` + `pkg/*`，gin 相关的中间件与 handler 放 `webapi/authapi`。
3. **不能让服务起不来。** 配置文件缺失 / 损坏 / 无用户，都要有可用的降级路径（见 §9 首次启动引导）。

---

## 1. 整体架构

```
appconfig/                 # viper + yaml，叶子包，无领域依赖
├── config.go              # Config 结构体 + Load/Get/Save + 默认值
├── watch.go               # WatchConfig 热重载（仅热重载可安全变更的字段）
└── config_test.go

auth/                      # 纯领域：用户、密码、令牌、TOTP、限流。不依赖 gin
├── user.go                # User 模型 + users.json 读写（原子写，0600）
├── store.go               # UserStore：Create/Get/List/Update/Delete，RWMutex
├── password.go            # bcrypt 哈希与校验
├── token.go               # HMAC-SHA256 签名令牌：签发 / 校验 / 版本吊销
├── totp.go                # pquerna/otp 封装：注册、校验、防重放、恢复码
├── ratelimit.go           # 登录失败计数与锁定（按 IP + 按用户名）
├── netmatch.go            # 内网 CIDR 判定
└── *_test.go

webapi/authapi/            # gin 接入层
├── middleware.go          # Middleware()：鉴权闸门（REST/SSE/WS 共用）
├── handler.go             # /api/auth/* 路由
├── users.go               # /api/users/* 路由（用户管理）
└── setup.go               # 首次运行引导

app/src/
├── views/Login.vue        # 登录页（密码 + 可选 TOTP 两步）
├── views/UserManager.vue  # 用户管理页
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
# bcrypt 来自 golang.org/x/crypto，go.mod 中已作为 indirect 存在，会自动提升为直接依赖
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
    domains: []            # 反代对外域名，写进证书 SAN
  trusted_proxies:         # 允许设置 X-Forwarded-For 的来源；空 = 谁都不信
    - 127.0.0.1
    - ::1
  cors:
    allowed_origins: []    # 空 = 仅同源。反代域名要写进来，例如 https://ark.example.com

auth:
  enabled: false           # 总开关。false 时中间件完全短路，行为与现在一致

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
    # 只要请求带 X-Forwarded-For / X-Real-IP / Forwarded 任一头，
    # 一律视为来自反代，绝不放行。防止「反代跑在 127.0.0.1 上」把鉴权整个绕过。
    deny_if_forwarded: true

  totp:
    enabled: true          # 全局允许两步验证功能
    required: false        # true = 所有用户必须绑定，未绑定的登录后强制进入绑定流程
    issuer: "ASA Server Manager"
    skew: 1                # 允许 ±1 个 30s 时间窗（应对时钟偏差）

  password:
    min_length: 8
    bcrypt_cost: 12

  ratelimit:
    max_failures: 5        # 连续失败次数
    window: 15m            # 统计窗口
    lockout: 15m           # 锁定时长
```

Go 结构体用 `mapstructure` tag，`viper.Unmarshal(&cfg)`：

```go
package appconfig

type Config struct {
    Server ServerConfig `mapstructure:"server"`
    Auth   AuthConfig   `mapstructure:"auth"`
}

type AuthConfig struct {
    Enabled   bool            `mapstructure:"enabled"`
    Session   SessionConfig   `mapstructure:"session"`
    LANBypass LANBypassConfig `mapstructure:"lan_bypass"`
    TOTP      TOTPConfig      `mapstructure:"totp"`
    Password  PasswordConfig  `mapstructure:"password"`
    RateLimit RateLimitConfig `mapstructure:"ratelimit"`
}

type LANBypassConfig struct {
    Enabled         bool     `mapstructure:"enabled"`
    Networks        []string `mapstructure:"networks"`
    DenyIfForwarded bool     `mapstructure:"deny_if_forwarded"`
}
```

### 2.4 加载与热重载

```go
var (
    current atomic.Pointer[Config]   // 读侧无锁，热重载整体换指针
)

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
        // 文件不存在：用默认值生成一份，方便用户后续手改
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

**热重载的边界**：`v.WatchConfig()` + `OnConfigChange` 只重载「读时生效」的字段 —— `auth.*` 全部可热重载（中间件每次请求都 `appconfig.Get()`）。`server.port` / `server.tls.*` 不可热重载，变更后日志提示「需重启生效」，不做进程内重启。

调用点：`main.go` 中 `cfgpkg.EnsureDirectories()` 之后、`logger.InitLoggerWithBaseDir()` 之后立刻 `appconfig.Load(cfgpkg.BaseDir)`。

---

## 3. 鉴权机制选型

### 3.1 为什么必须是 Cookie

| 链路 | 能否带自定义头 | 结论 |
|------|---------------|------|
| REST（axios） | 能 | Header 或 Cookie 都行 |
| SSE（`EventSource`） | **不能**（标准 API 无 headers 参数） | 只能 Cookie 或 URL query |
| WebSocket（浏览器 `WebSocket`） | **不能**（只能塞 subprotocol） | 只能 Cookie 或 URL query |

URL query 传令牌的问题：会进 access log、进反代日志、进浏览器历史，且 `buildWebSocketUrl` 目前已经把 `token=` 拼在 query 上（当前恒为空）。**不采用 query 传令牌**，统一 Cookie。

Cookie 属性：

```
Set-Cookie: asa_session=<token>; Path=/; HttpOnly; SameSite=Lax; Secure(仅 TLS 开启时); Max-Age=<ttl>
```

- `HttpOnly` —— 前端 JS 读不到令牌，XSS 偷不走。
- `Secure` —— 跟随 `server.tls.enabled`。项目默认开 TLS，正常情况恒为 true。
- `SameSite=Lax` —— 生产环境 SPA 与 API 同源，Lax 足够，同时天然挡掉大部分 CSRF。

### 3.2 令牌格式：无状态签名令牌 + 版本吊销

不引入 JWT 库，自己签一个紧凑令牌即可（避免 JWT 的 alg 混淆类坑）：

```
token = base64url(payload) + "." + base64url(HMAC-SHA256(secret, base64url(payload)))
payload = {"u":"admin","v":3,"jti":"<16B random>","iat":1690000000,"exp":1690604800}
```

- `secret`：`{BaseDir}/auth/secret.key`（32 字节随机，0600，首次启动生成）。删掉它 = 全员登出。
- `v`（session_version）：存在 `users.json` 里。**改密码 / 管理员踢人 / 「登出全部设备」→ v++**，
  所有旧令牌立即失效。这是唯一需要的持久化吊销手段。
- `jti` + 内存 denylist（`map[jti]expiry`，定期清理）：支持「当前这一台设备登出」而不影响其他设备。
  进程重启后 denylist 清空 —— 可接受，因为单设备登出的令牌本来也在客户端被删了。

**为什么不用服务端 session store**：BadgerDB 的 `state` 管理器在 `APIServer.Start()` 里才初始化、`Stop()` 里关闭，
而鉴权中间件在 `setupRoutes()` 就要可用，生命周期对不上；再开一个 Badger 实例又要多管一套开关。
无状态令牌 + 版本号把这些问题全绕开，且服务重启不会把所有人踢下线（Windows 服务重启是常态）。

### 3.3 用户存储

`{BaseDir}/auth/users.json`（原子写：写临时文件 → `os.Rename`；权限 0600）：

```json
[
  {
    "username": "admin",
    "password_hash": "$2a$12$...",
    "role": "admin",
    "session_version": 1,
    "totp_enabled": true,
    "totp_secret": "JBSWY3DPEHPK3PXP",
    "totp_last_step": 56333211,
    "recovery_codes": ["$2a$10$...", "..."],
    "created_at": "2026-07-28T10:00:00+08:00",
    "last_login_at": "2026-07-28T12:31:00+08:00",
    "disabled": false
  }
]
```

- 角色只要两种：`admin`（可管理用户、可做一切操作）、`operator`（可操作服务器，不能管用户）。
  权限模型别做复杂了 —— 这是单机管理面板，不是多租户 SaaS。
- `totp_secret` 明文存储。**这是有意的**：它必须能被服务端读出来做校验，任何「加密」都要把密钥存在同一台机器上，
  只是把问题挪了个位置。真正的防线是文件权限 0600 + 目录不入备份（`backup` 包只打包存档，天然不涉及）。
- `recovery_codes` 存 bcrypt 哈希，用一个删一个。

密码哈希用 `golang.org/x/crypto/bcrypt`，cost 12。（argon2id 更好，但 bcrypt 在 x/crypto 里 API 更简单、
无参数调优负担，对本项目的威胁模型够用。）

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
    // ClientIP() 会在可信代理场景下返回 XFF 里的值，语义是「最终客户端」；
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
2. `server.trusted_proxies` 要包含反代地址，否则 `c.ClientIP()`（日志、限流用）拿到的是反代 IP 而非真实客户端。
3. 默认值是 `enabled: false`。开启是用户的显式选择，配套一条启动期 WARN 日志：
   > `[安全] lan_bypass 已开启：来自 127.0.0.0/8 等网段且不带 X-Forwarded-For 的请求将跳过鉴权。若反代未设置 XFF，公网访问将完全绕过鉴权。`

### 4.5 更保险的替代方案（可作为后续增强）

真正无歧义的做法是**双监听**：`127.0.0.1:19193` 免鉴权、`0.0.0.0:19193` 强制鉴权，反代只准接第二个。
但这要改 `APIServer.Start()` 起两个 `http.Server` 并共享 handler，且证书/端口都要重新规划。
**本期不做**，记在这里作为 lan_bypass 出问题时的升级路径。

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

因为静态资源服务（`static.Serve` + `NoRoute`）也在同一 engine 上，中间件要能区分 API 与静态资源。

### 5.2 豁免路径

| 路径 | 原因 |
|------|------|
| `GET /health` | 健康检查（反代/监控用），本来就不返回敏感信息 |
| `POST /api/auth/login` | 登录本身 |
| `POST /api/auth/login/totp` | 两步验证第二阶段（凭 pre-auth 令牌） |
| `GET /api/auth/state` | 前端问「要不要登录 / 我是谁」，未登录返回 `{authenticated:false}` 而非 401 |
| `POST /api/auth/logout` | 幂等，未登录也返回 200 |
| `GET/POST /api/auth/setup*` | 首次引导，仅在「零用户」状态下开放，见 §9 |
| 非 `/api` 前缀的一切（SPA 静态资源、`index.html`） | 否则登录页自己都加载不出来 |

**静态资源不鉴权**是刻意的：SPA 的 JS/CSS 里没有数据，数据全在 API。给静态资源加鉴权只会造成
「未登录时白屏、连登录页都打不开」，而没有任何安全收益。

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
            c.Set(ctxUserKey, auth.LocalUser)   // 内网免鉴权用一个伪用户，便于审计日志
            c.Next()
            return
        }

        tok, err := c.Cookie(cfg.Auth.Session.CookieName)
        if err != nil {
            reject(c, "未登录")
            return
        }
        user, err := auth.VerifyToken(tok)
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
        // 关键：在 Upgrade 之前就 401，绝不「先升级再关闭」。
        // 详见 §8。
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
  ├─ 密码对 && 用户未开 TOTP → 200 + Set-Cookie(会话令牌)  → 完成
  └─ 密码对 && 用户已开 TOTP → 200 {"totp_required": true} + Set-Cookie(pre-auth 令牌, 有效期 5 分钟)
       └─ POST /api/auth/login/totp {code}
            ├─ 校验通过 → 200 + Set-Cookie(正式会话令牌)，清除 pre-auth
            └─ 失败 → 401，计入限流
```

pre-auth 令牌用同一套签名机制，payload 加 `"stage":"pre"`，`VerifyToken` 对 stage != "full" 的令牌一律拒绝，
只有 `/api/auth/login/totp` 用专门的 `VerifyPreAuthToken` 接受它。**别用同一个校验函数**，否则 pre-auth 令牌
就成了完整凭证。

### 6.2 绑定流程

```
POST /api/auth/totp/setup      → 生成 secret（不落盘，暂存内存 5 分钟，key=username）
                                 返回 {secret, otpauth_url, qr_png_base64}
POST /api/auth/totp/confirm    → 提交一个验证码，通过才把 secret 写进 users.json，
                                 同时返回 10 个一次性恢复码（明文只在这一次返回）
POST /api/auth/totp/disable    → 需当前密码 + 一个有效验证码
```

二维码由**后端**生成（`key.Image(256,256)` → PNG → base64），前端直接 `<img :src="'data:image/png;base64,'+qr">`。
这样前端不用新增 QR 库，也不用把 secret 渲染进 DOM。

```go
key, err := totp.Generate(totp.GenerateOpts{
    Issuer:      cfg.Auth.TOTP.Issuer,        // "ASA Server Manager"
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
        Period:    30,
        Skew:      skew,            // 1 → 接受前后各一个 30s 窗口
        Digits:    otp.DigitsSix,
        Algorithm: otp.AlgorithmSHA1,
    })
    if err != nil || !valid {
        return false, err
    }
    // 防重放：同一个时间步只能用一次，否则 30 秒内抓到验证码可重放
    step := uint64(time.Now().UTC().Unix()) / 30
    if u.TOTPLastStep >= step {
        return false, ErrCodeReused
    }
    u.TOTPLastStep = step
    return true, nil
}
```

Windows 上系统时间通常由 NTP 同步，`skew: 1` 够用。ASA 服务器机器如果时间漂移严重，登录会失败 —— 
在登录失败提示里带一句「若持续失败请检查服务器系统时间」。

### 6.4 恢复码

10 个 `XXXX-XXXX-XXXX` 格式的随机码，bcrypt(cost 10) 存储。`/api/auth/login/totp` 同时接受
6 位数字（TOTP）和恢复码格式；用掉的从数组里删除并落盘。用完时前端提示重新生成。

---

## 7. 接口设计

### 7.1 鉴权接口

| Method | Path | 鉴权 | 说明 |
|--------|------|------|------|
| GET | `/api/auth/state` | 豁免 | `{auth_enabled, authenticated, bypassed, user:{username,role,totp_enabled}, totp_required_global}` |
| POST | `/api/auth/login` | 豁免 | `{username,password}` → 会话 Cookie 或 `{totp_required:true}` |
| POST | `/api/auth/login/totp` | pre-auth | `{code}` → 会话 Cookie |
| POST | `/api/auth/logout` | 豁免 | 清 Cookie + jti 加入 denylist |
| POST | `/api/auth/logout-all` | 需登录 | `session_version++`，踢掉所有设备 |
| POST | `/api/auth/password` | 需登录 | `{old_password,new_password}`，成功后 `version++` 并重新签发当前设备令牌 |
| POST | `/api/auth/totp/setup` | 需登录 | 返回 secret + otpauth url + QR |
| POST | `/api/auth/totp/confirm` | 需登录 | `{code}` → 落盘 + 返回恢复码 |
| POST | `/api/auth/totp/disable` | 需登录 | `{password, code}` |
| POST | `/api/auth/totp/recovery/regenerate` | 需登录 | 重新生成恢复码 |

### 7.2 用户管理接口（仅 admin）

| Method | Path | 说明 |
|--------|------|------|
| GET | `/api/users` | 列表（不含 hash / secret） |
| POST | `/api/users` | `{username,password,role}` |
| PUT | `/api/users/:username` | `{role?,disabled?}` |
| DELETE | `/api/users/:username` | 禁止删除最后一个 admin；禁止删除自己 |
| POST | `/api/users/:username/password` | 管理员重置密码，强制 `version++` |
| POST | `/api/users/:username/totp/reset` | 管理员解绑 TOTP（用户丢手机时的救援路径） |
| POST | `/api/users/:username/unlock` | 清除该用户的登录失败锁定 |

**不变量**（在 `auth.UserStore` 里强制，不靠 handler 自觉）：
- 系统中至少保留一个未禁用的 `admin`
- 用户名唯一、不区分大小写、只允许 `[a-zA-Z0-9_-]{3,32}`

### 7.3 CORS 与 CSRF

现有 `engine.Use(cors.Default())` 是 `AllowAllOrigins: true` + 不带凭证。上 Cookie 后必须改：

```go
corsCfg := cors.Config{
    AllowOrigins:     appconfig.Get().Server.CORS.AllowedOrigins,  // 空则不装 CORS 中间件
    AllowCredentials: true,                                        // 关键：允许带 Cookie
    AllowMethods:     []string{"GET","POST","PUT","DELETE","OPTIONS"},
    AllowHeaders:     []string{"Content-Type","X-Requested-With"},
}
```

`AllowCredentials: true` 与 `AllowAllOrigins: true` **不能共存**（浏览器会直接拒绝 `Access-Control-Allow-Origin: *` + 凭证）。
生产同源部署时 `allowed_origins` 留空、不装 CORS 中间件即可。

CSRF 防护采用双重保险：
1. `SameSite=Lax` Cookie —— 挡掉跨站表单 POST。
2. 所有非幂等 API 要求 `X-Requested-With: XMLHttpRequest` 头 —— 该头无法被跨站 HTML 表单设置，
   会触发 CORS 预检。在 `app/src/utils/http.js` 的请求拦截器里统一加。

---

## 8. WebSocket 鉴权失败会不会造成连接风暴？

### 8.1 结论

**按现有的重连实现，会。而且不止 WS，SSE 更严重。** 但风暴不是鉴权本身造成的，是「重连策略把认证失败
当成网络抖动」造成的。下面逐条拆。

### 8.2 现状分析

`app/src/workers/wsWorker.js` 当前行为：

- 固定间隔重连：`setInterval(..., 10000)`，`maxReconnectAttempts: null`（**无限次**）
- `onclose` / `onerror` 一律触发重连，**不区分原因**
- `wsManager.js` 还挂了 `useDocumentVisibility` 监听，页面切回前台时额外触发一次 `START_RECONNECT`

如果鉴权失败的实现是「先 `Upgrade()` 成功，再 `conn.Close()`」，那么：

| 影响 | 说明 |
|------|------|
| 每 10s 一次的永久热循环 | 每个标签页、每个浏览器、每台机器各一份，永不停止 |
| 每次重连 = 一次完整 TLS 握手 | 项目默认 HTTPS，握手的 ECDHE + 证书链验证是 WS 连接里最贵的部分；HTTP/2 的多路复用在这里帮不上忙（WS 走的是 h1 Upgrade，见 `docs/HTTP2_CONNECTION_OPTIMIZATION.md`） |
| 日志淹没 | 每次失败一条 `logger.GetLogger().Warnf`，`asaServer.log` 被刷爆，lumberjack 疯狂切割，真正的错误被冲掉 |
| **惊群（thundering herd）** | 固定 TTL 意味着所有客户端的会话在几乎同一时刻过期。N 个客户端同时被踢 → 同时进入 10s 固定间隔重连 → **相位锁定**，此后每 10s 一次 N 并发脉冲，永远不会自然错开 |
| 前端毫无反馈 | 用户只看到「连接中…」转圈，不知道该去登录 |

SSE 的情况更糟：`EventSource` 的自动重连是**浏览器内置**的，JS 关不掉。规范规定：
- 响应状态非 200 或 MIME 不是 `text/event-stream` → 连接**失败**，浏览器**不重连**（这是我们要的）
- 响应 200 且 MIME 正确、之后流断开 → 浏览器按 `retry` 值（默认约 3 秒）**无限重连**

所以 **SSE 鉴权失败绝不能「先 200 再断」**。`webapi` 里 `logapi` / `serverapi` / `saveapi` 都有长驻 SSE，
中间件在 handler 之前拦截天然能返回 401 —— 但**必须审计一遍**，确保没有任何 handler 在鉴权检查之后、
业务失败之前就抢先写了 `c.Header("Content-Type","text/event-stream")` + `c.Status(200)`。

### 8.3 规避方案（六条，缺一不可）

#### (a) 后端：升级前拒绝，不要升级后关闭

```go
// realtime/ws.go —— 由中间件在 Upgrade 之前完成鉴权，handler 里只做二次确认
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

好处：浏览器侧 `WebSocket` 拿到的是握手失败（HTTP 401），而不是「连上又断」。虽然浏览器 JS 因安全限制
读不到具体状态码，但结合 (b) 的预检机制足以区分。

#### (b) 前端：连接前预检，未登录**根本不发起** WS

`wsManager.connectWebSocket()` 之前先看 `authStore.authenticated`：

```js
export function connectWebSocket(onOpen, onError, onClose) {
    if (authStore.authRequired && !authStore.authenticated) {
        // 未登录：不连，也不重连。登录成功后由 authStore 主动调用 forceReconnect()
        return;
    }
    ...
}
```

这一条消除了绝大部分无效连接 —— 未登录用户压根不会产生任何 WS 流量。

#### (c) 会话中途失效：用应用级关闭码把「致命」和「可重试」分开

如果连接已建立、途中会话过期（服务端在心跳循环里定期复检令牌），服务端主动发送应用级关闭帧：

```go
const CloseAuthFailed = 4401   // 4000-4999 为应用私有区间

conn.WriteControl(websocket.CloseMessage,
    websocket.FormatCloseMessage(CloseAuthFailed, "session expired"),
    time.Now().Add(time.Second))
conn.Close()
```

Worker 侧：

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

主线程收到 `WS_AUTH_FAILED` → `authStore.setUnauthenticated()` → 路由跳 `/login`。
**重连只有在登录成功后才被显式重新启用**（`authStore` 登录成功回调里调 `forceReconnect()`）。

#### (d) 把固定间隔换成指数退避 + 全抖动

这是根治惊群的那一条，**即使不做鉴权也应该改**：

```js
// wsWorker.js
const BACKOFF = { base: 1000, max: 30000 };
let attempt = 0;

function nextDelay() {
    const cap = Math.min(BACKOFF.max, BACKOFF.base * 2 ** attempt);
    attempt++;
    return Math.random() * cap;        // full jitter：均匀分布在 [0, cap)
}

function scheduleReconnect() {
    clearTimeout(reconnectTimer);       // 用 setTimeout 递归，不用 setInterval
    reconnectTimer = setTimeout(doReconnect, nextDelay());
}

// 连接成功时必须重置
function onConnected() { attempt = 0; }
```

`setInterval` → 递归 `setTimeout` 的改动同样重要：`setInterval` 在连接尝试耗时超过间隔时会**堆积**回调。

对比效果（100 个客户端同时会话过期，10 分钟内的连接尝试总数）：

| 策略 | 尝试次数 | 峰值并发 |
|------|---------|---------|
| 固定 10s 无限重连 | 6000 | 100（相位锁定，每 10s 一次脉冲） |
| 指数退避 + 全抖动 + 4401 致命判定 | ≈100（各 1 次后停止） | ≈10（抖动打散） |

#### (e) 服务端限速兜底

不能假设客户端一定是我们的前端（旧版本前端、脚本、恶意扫描）。在中间件里对**鉴权失败**做按 IP 限速：

```go
// 同一 IP 的鉴权失败：> 20 次/分钟 → 429 + Retry-After: 60
if authFailLimiter.Exceeded(clientIP) {
    c.Header("Retry-After", "60")
    c.AbortWithStatus(http.StatusTooManyRequests)
    return
}
```

配套**日志降噪**：同一 IP 的鉴权失败日志按分钟聚合（`「IP x.x.x.x 过去 1 分钟鉴权失败 137 次」`），
不要一次一条。否则风暴会变成日志风暴。

#### (f) 滑动续期，避免所有会话同时到期

`auth.session.idle_timeout` 生效时，中间件在令牌剩余寿命 < TTL/2 时重发 Cookie（新 exp）。
活跃用户的会话到期时间因此天然错开，从源头削掉惊群。

### 8.4 SSE 侧的对应改动

`sseApi.js` 里所有 `EventSource` 的 `onerror`：

```js
es.onerror = (e) => {
    // readyState === CLOSED 表示浏览器已放弃（非 200 响应就是这种情况）
    if (es.readyState === EventSource.CLOSED) {
        // 可能是鉴权失败。查一次 /api/auth/state 确认，再决定跳登录还是提示网络错误
        authStore.recheck();
        return;
    }
    // CONNECTING 状态是浏览器在自动重连，交给它
};
```

**同时必须做的**：`resourceMonitorWorker.js` / `serverResourceWorker.js` / `sharedResourceWorker.js`
这三个 SSE Worker 也要接同样的判定，否则它们会各自维持一条无限重连的 SSE。

### 8.5 一句话总结

> 鉴权失败断开**本身**不造成风暴；「把鉴权失败当网络抖动、用固定间隔无限重连」才造成风暴。
> 做到「未登录不发起 + 4401 视为致命 + 指数退避全抖动 + 服务端限速兜底」，风暴就不存在。

---

## 9. 首次启动引导与救援路径

### 9.1 零用户状态

`auth.enabled: true` 但 `users.json` 不存在或为空数组时：

- 除 `/api/auth/setup*` 与静态资源外，所有 API 返回 `401 {"code":"setup_required"}`
- 前端 `authStore` 见到 `setup_required` → 路由跳 `/setup`，展示「创建管理员账号」表单
- `POST /api/auth/setup` 仅在零用户时可用，创建后立刻失效（再调返回 409）
- **该接口只接受来自 loopback 的请求**（`RemoteAddr` 是 127.0.0.1/::1 且无 XFF）。
  防止服务恰好暴露在公网时被人抢注管理员

同时在启动日志与 GUI 里打印醒目提示：

```
[鉴权] 已启用鉴权但尚未创建任何账号。
       请在本机浏览器打开 https://localhost:19193/#/setup 创建管理员账号。
```

### 9.2 忘记密码 / 丢失 2FA 设备

GUI 跑在本机、CLI 也在本机，本来就等价于物理访问，所以提供本地 CLI 救援是合理的（不是安全漏洞）：

```bash
asa-server.exe user list
asa-server.exe user add <name> --role admin
asa-server.exe user passwd <name>          # 交互式输入，不走命令行参数（防止进 PowerShell 历史）
asa-server.exe user totp-reset <name>
asa-server.exe user unlock <name>
```

以及最后的核选项：删掉 `{BaseDir}/auth/users.json` → 回到零用户状态 → 走 §9.1 引导。
把这条写进 README，比让用户去改数据库强。

---

## 10. 前端改动清单

### 10.1 新增文件

| 文件 | 内容 |
|------|------|
| `src/views/Login.vue` | 用户名/密码 + 两步验证码两阶段表单；失败提示区分「密码错」「已锁定，剩余 X 分钟」「验证码错」 |
| `src/views/Setup.vue` | 首次创建管理员 |
| `src/views/UserManager.vue` | 用户表格（TDesign `t-table`）+ 新增/编辑/重置密码/重置 2FA 弹窗 |
| `src/views/Profile.vue` | 个人：改密码、绑定/解绑 2FA（含二维码与恢复码展示）、登出所有设备 |
| `src/store/authStore.js` | `reactive({authEnabled, authenticated, bypassed, user, ready})` + `login/logout/recheck` |
| `src/apis/authApi.js` | 上述接口封装 |

### 10.2 修改文件

| 文件 | 改动 |
|------|------|
| `src/utils/http.js` | 请求拦截器加 `X-Requested-With`；`withCredentials: true`；响应拦截器捕获 401 → `authStore.setUnauthenticated()` + 跳 `/login`（**加去重锁，防止并发 401 触发多次跳转**） |
| `src/utils/wsManager.js` | 连接前检查 `authStore`；处理 `WS_AUTH_FAILED` |
| `src/workers/wsWorker.js` | 指数退避 + 全抖动；`setInterval` → 递归 `setTimeout`；close code 4401 判定为致命 |
| `src/workers/*ResourceWorker.js` | SSE `onerror` 的 CLOSED 判定 |
| `src/utils/utils.js` | **DEV 模式下 WS/SSE 改走 vite 代理同源地址**（见下） |
| `src/router/index.js` | 全局 `beforeEach` 守卫 + 新增 4 条路由 |
| `src/App.vue` | 顶栏加用户菜单（当前用户名 / 个人设置 / 用户管理 / 退出登录）；`auth.enabled=false` 时整块隐藏 |
| `app/vite.config.js` | 代理加 `cookieDomainRewrite`（如需） |

### 10.3 开发模式的跨站 Cookie 问题（必须解决）

当前 `utils.js` 里，DEV 模式的 WS 和 SSE **绕过 vite 代理直连** `https://localhost:19193`，
而页面在 `http://localhost:3000`。上 Cookie 后这就变成**跨站**请求：

- 端口不同不影响 same-site 判定，但 **scheme 不同（http vs https）就是 cross-site**
- 于是 `SameSite=Lax` 的 Cookie **不会**被带上 → 开发环境 WS/SSE 全部 401
- 改成 `SameSite=None` 又要求 `Secure`，而 dev 页面是 http，浏览器会拒绝写入

**解决办法：DEV 模式也走 vite 代理。** `vite.config.js` 的代理已经配了 `ws: true`，改 `utils.js` 即可：

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

顺带好处：dev 环境不再需要把本地 CA 装进系统信任存储才能用 `EventSource`（原注释里提到的痛点）。
代理侧 `secure: false` 已经处理了自签证书。

### 10.4 路由守卫

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

`authStore.recheck()` 内部要做 **单飞（single-flight）**：多个路由/组件并发调用只发一次请求。

---

## 11. 实施顺序

分五个阶段，每阶段可独立编译、独立验证、独立回滚。

### 阶段一：`appconfig` 落地（不碰鉴权）
1. `go get github.com/spf13/viper`
2. 写 `appconfig` 包 + `Config` 结构 + `Load` + 默认值 + `Validate`
3. `main.go` 接入；把现有 `webapi.ApiServerPort/EnableTLS/...` 的默认值来源改为 `appconfig`，
   CLI flag 显式设置时覆盖
4. 验证：`go build` + `go vet`，删掉 `config.yaml` 能自动生成，改端口能生效
5. **此阶段结束时功能行为与现在完全一致**，可先合入

### 阶段二：`auth` 领域包（不接路由）
1. `go get github.com/pquerna/otp`
2. `user.go` / `store.go` / `password.go` / `token.go` / `netmatch.go` / `ratelimit.go` / `totp.go`
3. 单元测试（重点，见 §12）
4. `asa-server user *` CLI 子命令
5. 此阶段结束时 HTTP 层零改动

### 阶段三：后端接入
1. `webapi/authapi`：middleware + handler + users + setup
2. `actions.go` 挂中间件、改 CORS
3. `realtime/ws.go` 加 Upgrade 前鉴权 + 4401 关闭码 + 心跳循环里的令牌复检
4. 审计所有 SSE handler，确认 401 在写响应体之前
5. 用 curl / wscat 验证（`auth.enabled: true` + 无前端改动，此时前端会 401，属预期）

### 阶段四：前端接入
1. `authStore` + `authApi` + `http.js` 拦截器
2. Login / Setup / UserManager / Profile 四个页面
3. 路由守卫 + App.vue 用户菜单
4. `utils.js` 的 DEV 同源改造（§10.3）

### 阶段五：连接风暴治理
1. `wsWorker.js` 指数退避 + 全抖动 + 4401 致命判定
2. 三个 SSE Worker 的 CLOSED 判定
3. 服务端鉴权失败限速 + 日志聚合
4. 压测验证（§12.3）

> 阶段五**可以提前到阶段四之前甚至阶段一之前做** —— 指数退避本身就是现有代码的改进，
> 与鉴权无关。如果想降低风险，建议先单独做退避改造并观察一段时间。

---

## 12. 测试要点

### 12.1 单元测试（`auth` 包）

| 用例 | 断言 |
|------|------|
| `token`：签发→校验 | 往返一致；篡改 payload/签名任一字节 → 拒绝 |
| `token`：过期 | exp 已过 → 拒绝 |
| `token`：版本吊销 | `session_version` 从 1 → 2 后，旧令牌拒绝 |
| `token`：pre-auth 隔离 | `stage:"pre"` 的令牌过不了 `VerifyToken` |
| `netmatch` | 127.0.0.1/10.x/172.16.x/192.168.x/::1/fe80:: → true；8.8.8.8/2001:4860:: → false |
| `netmatch`：IPv4-mapped IPv6 | `::ffff:8.8.8.8` **不能**被误判为内网（易错点） |
| `totp` | 用 `totp.GenerateCode` 造码校验通过；同一步重放 → `ErrCodeReused`；skew±1 边界 |
| `ratelimit` | 达阈值锁定；窗口滑过后解锁；不同 IP/用户互不影响 |
| `store` | 最后一个 admin 不可删/不可禁用；用户名大小写去重 |

### 12.2 中间件测试（`httptest`）

| 场景 | 期望 |
|------|------|
| `auth.enabled=false` | 一切放行，无 Cookie 也 200 |
| 豁免路径 | 无 Cookie 200 |
| 非 `/api` 路径 | 无 Cookie 200（静态资源可达） |
| `lan_bypass=true` + RemoteAddr 127.0.0.1 + 无 XFF | 放行 |
| **`lan_bypass=true` + RemoteAddr 127.0.0.1 + 有 XFF** | **拒绝**（§4.2 的核心用例，必须有） |
| `lan_bypass=true` + RemoteAddr 公网 IP | 拒绝 |
| SSE 请求（`Accept: text/event-stream`）未登录 | 状态码 401，`Content-Type` **不是** `text/event-stream` |
| WS 请求（`Upgrade: websocket`）未登录 | 401，**无** `101` 响应 |

### 12.3 连接风暴回归测试

手工脚本即可，不必进 CI：

1. 开 10 个浏览器标签页，全部登录
2. 后端执行 `logout-all`（模拟全员会话失效）
3. 观察 60 秒内 `asaServer.log` 的 WS/SSE 请求条数
   - **期望**：每标签页 ≤ 2 次尝试后停止，总计 ≤ 20 条，且全部标签页跳到登录页
   - **失败信号**：条数持续增长不收敛 → 说明 4401 判定或退避没生效
4. 重新登录 → 所有标签页 WS 自动恢复（验证 `forceReconnect` 被正确触发）

### 12.4 手工验收清单

- [ ] `auth.enabled=false` 时，行为与升级前 100% 一致（回归保护）
- [ ] 首次启用 → setup 页 → 创建 admin → 正常登录
- [ ] setup 接口从非 loopback 访问被拒
- [ ] 绑定 2FA → 扫码 → 确认 → 恢复码可下载 → 退出 → 用 2FA 登录
- [ ] 用恢复码登录一次，该码失效
- [ ] `user totp-reset` CLI 能救回丢手机的账号
- [ ] 改密码后其他设备立即被踢
- [ ] operator 角色访问 `/api/users` 返回 403
- [ ] 连续 5 次密码错 → 锁定 15 分钟，提示剩余时间
- [ ] 反代（frpc http 类型）访问需登录；本机直连（lan_bypass 开启时）不需要
- [ ] 反代（frpc **tcp** 类型 + lan_bypass 开启）→ 确认能绕过 → 文档里必须警告这一点

---

## 13. 已知取舍与不做的事

| 决定 | 理由 |
|------|------|
| 不做 OIDC / LDAP / OAuth | 单机管理面板，引入外部 IdP 的复杂度远超收益 |
| 不做细粒度 RBAC（按实例授权） | 两个角色够用；真需要时再加，数据结构已预留 `role` 字段 |
| 不做服务端 session store | §3.2：生命周期与 Badger 冲突，且服务重启会踢光所有人 |
| 不做双监听端口区分内外网 | §4.5：改动大，本期用 XFF 判定 + 默认关闭替代 |
| TOTP secret 明文存储 | §3.3：服务端必须能读出明文才能校验，加密只是转移问题 |
| 不加密 `users.json` | 同上；靠 0600 文件权限 + 不入备份 |
| 静态资源不鉴权 | §5.2：无安全收益，只会让登录页打不开 |
| GUI（Fyne）本地界面不加鉴权 | 能启动 GUI 的人已有物理/RDP 访问权，加鉴权只是给自己添堵 |

---

## 14. 风险登记

| 风险 | 等级 | 缓解 |
|------|------|------|
| lan_bypass + 本机反代无 XFF → 鉴权完全失效 | **高** | 默认关闭 + XFF 强制判定 + 启动 WARN + 文档三处警告 |
| 前端重连风暴打爆 CPU / 日志 | 中 | §8 六条措施 + §12.3 回归测试 |
| 用户忘记密码且无 CLI 访问 | 中 | CLI 救援 + 删文件重置，写进 README |
| 服务器时钟漂移导致 2FA 全员登不上 | 中 | skew=1 + 恢复码 + CLI `totp-reset` + 错误提示带排查建议 |
| `config.yaml` 手改出语法错误导致起不来 | 中 | `Load` 失败时记 ERROR 并**用默认值继续启动**（`auth.enabled` 默认 false → 不会把人锁在外面） |
| CORS 改动影响现有 dev 流程 | 低 | §10.3 改为同源，反而简化 dev |
| 升级后老用户被意外锁定 | 低 | `auth.enabled` 默认 false，升级零影响 |
