package appconfig

// defaultConfigTemplate 是首次运行时写出的 config.yaml。
//
// 刻意手写而不是用 viper.SafeWriteConfigAs 生成：这份文件的目标读者是人，
// 注释里的那些警告（尤其 lan_bypass 和 webauthn.domains）比字段本身更重要。
const defaultConfigTemplate = `# ASA Server Manager 应用配置
#
# 优先级：命令行 flag > 环境变量 ASA_* > 本文件 > 内置默认值
# 环境变量命名把点换成下划线并加 ASA_ 前缀，例如 auth.enabled -> ASA_AUTH_ENABLED
#
# 本文件管的是程序自身的运行参数。ARK 实例的配置在
# instances/<实例名>/instance_config.ini，与这里无关。

server:
  port: 19193

  tls:
    # 关掉 TLS 就等于退回 HTTP/1.1 的「每源 6 条连接」限制，常驻 SSE 会把 REST 请求饿死。
    # 浏览器只在 TLS 上通过 ALPN 协商 HTTP/2，没有主流浏览器支持明文 h2c。
    enabled: true
    # 把自签的本地 CA 写入 Windows 受信任根存储，浏览器打开 https://localhost:19193 无警告
    trust_local_ca: true
    # 有自备证书时填这两项（推荐有域名的场景），此时不再生成本地 CA
    cert_file: ""
    key_file: ""
    # 追加进证书 SAN 的域名（反代对外域名等）
    domains: []

  # 允许其设置 X-Forwarded-For 的来源。留空 = 谁都不信。
  # gin 默认信任所有代理，等于让任何客户端都能伪造来源 IP，必须收紧。
  trusted_proxies:
    - 127.0.0.1
    - ::1

  cors:
    # 留空 = 仅同源（生产部署的正常情况）。反代域名需要跨域时才填，
    # 形如 https://ark.example.com
    allowed_origins: []

auth:
  # 总开关。false 时鉴权中间件完全短路，也不会打开 auth.db，行为与未引入鉴权时一致。
  enabled: false

  database:
    # 留空 = {程序目录}/database_file/auth.db
    path: ""
    # 启动时自动应用待执行的数据库迁移。关掉的话升级后需要手动执行:
    #   asa-server.exe db migrate
    auto_migrate: true

  session:
    ttl: 168h            # 登录令牌有效期
    idle_timeout: 24h    # 空闲多久失效（滑动续期；0 = 不启用）
    cookie_name: asa_session
    cookie_path: /
    same_site: lax       # lax | strict | none（none 必须配合 TLS）

  # ⚠️ 内网免鉴权。开启前务必读完下面三条，否则可能让鉴权对公网完全失效。
  #
  # 1. 本项目典型部署是反代（frpc / Nginx）跑在同一台机器上转发到 127.0.0.1，
  #    此时公网请求和本机请求的来源 IP 完全一样。区分两者的唯一信号是
  #    反代有没有设置 X-Forwarded-For。
  # 2. 因此开启前必须确认反代会设置 XFF：
  #    Nginx  -> proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
  #    frpc   -> http 类型代理默认会加；【tcp 类型代理不会】
  #    用 frpc 的 tcp 类型穿透时，lan_bypass 必须保持关闭，否则等于没有鉴权。
  # 3. deny_if_forwarded 保持 true —— 它是上面那条规则的执行者。
  lan_bypass:
    enabled: false
    networks:
      - 127.0.0.0/8
      - ::1/128
      - 10.0.0.0/8
      - 172.16.0.0/12
      - 192.168.0.0/16
      - 169.254.0.0/16
      - fc00::/7
      - fe80::/10
    deny_if_forwarded: true
    # 额外自动信任本机「物理网卡」当前所在的子网（按 IP+子网掩码算出精确 CIDR），
    # 排除 Docker/Hyper-V/WSL2/VPN 等虚拟适配器。默认关闭，是对上面 networks
    # 列表的补充而非替换。启用前确认本机没有物理网卡直接暴露在公网。
    auto_detect_local_subnets: false

  # 两步验证（TOTP，兼容 Google Authenticator / Microsoft Authenticator 等）
  totp:
    enabled: true      # 是否允许用户绑定两步验证
    required: false    # true = 所有用户必须绑定
    issuer: "ASA Server Manager"
    skew: 1            # 允许 ±N 个 30 秒时间窗，应对服务器时钟偏差

  # WebAuthn / FIDO2（Passkey、YubiKey、Windows Hello）
  #
  # 它是密码登录的【补充】，永远不是替代 —— 每个账户恒有密码。
  # 任何条件不满足都会静默退回密码登录，不会把人锁在门外。
  webauthn:
    enabled: false

    # ★ 域名闸门：只有当前请求的域名命中这个列表，WebAuthn 才启用。
    #
    # 留空 = 不对任何请求生效（等同 enabled: false）。不做任何自动推导，
    # 本机使用必须显式写 localhost。
    #
    # 几条规范层面的硬约束（无法绕过）：
    #   - IP 地址不是合法的 RP ID。用 https://192.168.x.x:19193 访问时
    #     WebAuthn 一定不可用，此时自动退回密码登录。
    #   - 凭证不跨域名。在 localhost 注册的 Passkey 在 ark.example.com 上用不了，
    #     同一个人可能需要在两处各注册一次。
    #   - 写父域名（example.com）可让其子域名共享凭证。
    #
    # ⚠️ 改动这个列表会使已注册的凭证失效（RP ID 变了就匹配不上）。
    #    因为密码始终可用，这不会锁住任何人，但用户需要重新注册。
    domains: []
      # - localhost
      # - ark.example.com

    rp_display_name: "ASA Server Manager"
    extra_origins: []           # 额外允许的 Origin，一般不用填
    discoverable_login: true    # 登录页提供「使用 Passkey 登录」（无需输用户名）
    user_verification: required # discouraged | preferred | required
    satisfies_2fa: true         # 通过生物识别/PIN 的 Passkey 登录视为已完成两步验证
    clone_detection: warn       # off | warn | disable_credential

  password:
    min_length: 8
    bcrypt_cost: 12
    # 注意：没有「关闭密码登录」的开关。密码是 WebAuthn 不可用时的唯一兜底。

  # 登录失败限流。计数持久化在数据库里，重启服务不会清零。
  ratelimit:
    max_failures: 5
    window: 15m
    lockout: 15m

  audit:
    max_rows: 2000   # 审计日志滚动保留条数
`
