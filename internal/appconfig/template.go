package appconfig

import (
	"fmt"
	"runtime"
)

// trustLocalCATemplateBlock 按平台渲染 trust_local_ca 那两行（注释 + 值）。
//
// Windows 上写进系统受信任根存储能让浏览器直接免警告；Linux 上系统信任库
// 不影响 Firefox/Chrome（它们用各自的 NSS db），默认装了也还是红锁，只会制造
// 困惑，所以默认关闭并把提示改成"按需手动安装"，见 docs/LINUX_COMPATIBILITY_PLAN.md §5.7。
func trustLocalCATemplateBlock() string {
	if runtime.GOOS == "linux" {
		return "    # Linux 上系统信任库不影响浏览器（Firefox/Chrome 用各自的 NSS db），装了也还是红锁。\n" +
			"    # 默认关闭；需要时执行 `asa-server cert install`（需 root）并按提示手动导入浏览器。\n" +
			"    trust_local_ca: false"
	}
	return "    # 把自签的本地 CA 写入 Windows 受信任根存储，浏览器打开 https://localhost:19193 无警告\n" +
		"    trust_local_ca: true"
}

// renderDefaultConfigTemplate 渲染首次运行时写出的 config.yaml。
//
// 刻意手写模板而不是用 viper.SafeWriteConfigAs 生成：这份文件的目标读者是人，
// 注释里的那些警告（尤其 lan_bypass）比字段本身更重要。trust_local_ca 那一段
// 按平台不同（见上），其余内容两平台通用。
func renderDefaultConfigTemplate() string {
	return fmt.Sprintf(defaultConfigTemplate, trustLocalCATemplateBlock())
}

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
%s
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

  password:
    min_length: 8
    bcrypt_cost: 12
    # 注意：没有「关闭密码登录」的开关。密码是唯一的登录手段。

  # 登录失败限流。计数持久化在数据库里，重启服务不会清零。
  ratelimit:
    max_failures: 5
    window: 15m
    lockout: 15m

  audit:
    max_rows: 2000   # 审计日志滚动保留条数

# 全局下载器（SteamCMD 等大文件下载走这里；将来 Linux 运行时的 umu/GE-Proton/Syncthing 下载也共用这份配置）
download:
  # GitHub 加速代理，前缀重写型（形如 https://ghproxy.example.com/），不是标准 HTTP CONNECT 代理。
  # 只对 github.com / raw.githubusercontent.com / objects.githubusercontent.com 生效，
  # 其余地址（如 Steam CDN）不受影响、始终直连。留空 = 直连 GitHub。
  github_proxy: ""
  # 标准 HTTP(S)_PROXY，对全部下载生效（含非 GitHub 的），留给只有通用出口代理的用户兜底。
  http_proxy: ""
  timeout: 30s   # 只约束连接建立与响应头等待，不含大文件传输本身
  retries: 3

# Linux 专属：Wine/Proton 运行时（用于在 Linux 上运行 Windows 版 ARK 服务端 exe）。
# 这整段在 Windows 上被忽略。
linux:
  # 运行时来源：umu（默认，程序自动下载 umu-launcher + GE-Proton 并管理）
  #           | custom（用户自备 PROTONPATH，程序只做只读检查，不下载不联网）
  runtime: umu
  # 必须是具体版本号，不能是 "latest"——通过 GitHub API 解析别名会撞限流，见文档。
  umu_version: "1.4.4"
  proton_version: "GE-Proton10-34"
  # prefix 模式：shared（默认，全部实例共用一个 Wine prefix，省盘）
  #           | per-instance（每实例独立 prefix，更隔离但更占盘）
  prefix_mode: shared
  # 留空 = {程序目录}/umu-prefix
  prefix_dir: ""
  # false 时完全不联网下载 umu/GE-Proton，缺失的组件只会在
  # GET /api/system/preflight 里报告，不会自动尝试修复
  auto_download: true
  gameid: "umu-default"
`
