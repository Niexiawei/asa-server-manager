// Package appconfig 提供应用级配置（viper + YAML）。
//
// 它与 config 包（cfgpkg）是两件不同的事：cfgpkg 管的是目录布局和每个 ARK 实例的
// INI 配置，appconfig 管的是本程序自身的运行参数（端口、TLS、鉴权）。两者不要混。
//
// 依赖上 appconfig 是叶子包：只用标准库、viper 和 pkg/logger，不引入任何领域包，
// 这样 auth / webapi / certmgr 都能安全地依赖它而不成环。引入 pkg/logger 只是为了
// Load 在 BaseDir 解析异常兜底时能打一条启动警告——pkg/logger 本身也是零
// internal/ 依赖的叶子包，不会引入环，见 docs/APPCONFIG_BASEDIR_PLAN.md。
package appconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/viper"

	"asa-server/pkg/logger"
)

// ConfigFileName 是 BaseDir 下的配置文件名
const ConfigFileName = "config.yaml"

// Config 是整份应用配置
type Config struct {
	// BaseDir 是 §10.3 的数据目录字段，本次改造里数据目录的最高权威：非空时 Load 直接
	// 用它作为最终 BaseDir，优先级高于 ASA_BASEDIR 环境变量。留空 = 与本文件同目录
	// （绿色部署的默认行为，兼容现有全部安装，无需迁移），此时才轮到 ASA_BASEDIR 兜底。
	// 见 Load 的文档。
	BaseDir  string         `mapstructure:"basedir"`
	Server   ServerConfig   `mapstructure:"server"`
	Auth     AuthConfig     `mapstructure:"auth"`
	Download DownloadConfig `mapstructure:"download"`
	Linux    LinuxConfig    `mapstructure:"linux"`
}

// ServerConfig 对应 HTTP 服务本身
type ServerConfig struct {
	Port           int        `mapstructure:"port"`
	TLS            TLSConfig  `mapstructure:"tls"`
	TrustedProxies []string   `mapstructure:"trusted_proxies"`
	CORS           CORSConfig `mapstructure:"cors"`
}

type TLSConfig struct {
	Enabled      bool     `mapstructure:"enabled"`
	TrustLocalCA bool     `mapstructure:"trust_local_ca"`
	CertFile     string   `mapstructure:"cert_file"`
	KeyFile      string   `mapstructure:"key_file"`
	Domains      []string `mapstructure:"domains"`
}

type CORSConfig struct {
	AllowedOrigins []string `mapstructure:"allowed_origins"`
}

// AuthConfig 是鉴权总配置。Enabled 为 false 时其余字段都不生效，
// 且完全不会打开 auth.db。
type AuthConfig struct {
	Enabled   bool            `mapstructure:"enabled"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Session   SessionConfig   `mapstructure:"session"`
	LANBypass LANBypassConfig `mapstructure:"lan_bypass"`
	TOTP      TOTPConfig      `mapstructure:"totp"`
	Password  PasswordConfig  `mapstructure:"password"`
	RateLimit RateLimitConfig `mapstructure:"ratelimit"`
	Audit     AuditConfig     `mapstructure:"audit"`
}

type DatabaseConfig struct {
	// Path 为空时用 {BaseDir}/database_file/auth.db
	Path        string `mapstructure:"path"`
	AutoMigrate bool   `mapstructure:"auto_migrate"`
}

type SessionConfig struct {
	TTL         time.Duration `mapstructure:"ttl"`
	IdleTimeout time.Duration `mapstructure:"idle_timeout"`
	CookieName  string        `mapstructure:"cookie_name"`
	CookiePath  string        `mapstructure:"cookie_path"`
	SameSite    string        `mapstructure:"same_site"` // lax | strict | none
}

// LANBypassConfig 控制内网免鉴权。
//
// 默认关闭是刻意的：本项目的典型部署是反代跑在同一台机器上转发到 127.0.0.1，
// 此时公网请求和本机请求的 RemoteAddr 完全一样。DenyIfForwarded 是把两者区分开的
// 唯一信号，所以它也默认为 true。
type LANBypassConfig struct {
	Enabled         bool     `mapstructure:"enabled"`
	Networks        []string `mapstructure:"networks"`
	DenyIfForwarded bool     `mapstructure:"deny_if_forwarded"`
	// AutoDetectLocalSubnets 额外把本机「物理网卡」当前所在的子网追加进 Networks，
	// 排除 Docker/Hyper-V/WSL2/VPN 等虚拟适配器，且只信任私有/链路本地地址。
	// 默认关闭：是对 Networks 的补充，不是替换，不能在升级后悄悄扩大信任范围。
	AutoDetectLocalSubnets bool `mapstructure:"auto_detect_local_subnets"`
}

type TOTPConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Required bool   `mapstructure:"required"`
	Issuer   string `mapstructure:"issuer"`
	Skew     uint   `mapstructure:"skew"`
}

type PasswordConfig struct {
	MinLength  int `mapstructure:"min_length"`
	BcryptCost int `mapstructure:"bcrypt_cost"`
}

type RateLimitConfig struct {
	MaxFailures int           `mapstructure:"max_failures"`
	Window      time.Duration `mapstructure:"window"`
	Lockout     time.Duration `mapstructure:"lockout"`
}

type AuditConfig struct {
	MaxRows int `mapstructure:"max_rows"`
}

// DownloadConfig 是 pkg/download 的全局下载器配置，两平台都读
// （Syncthing/SteamCMD 等在 Windows 上同样要下载，不只是 Linux 运行时）。
type DownloadConfig struct {
	// GithubProxy 是前缀重写型代理，形如 "https://ghproxy.example.com/"；
	// 命中 github.com / raw.githubusercontent.com / objects.githubusercontent.com
	// 时把原始 URL 整串拼接在其后代理下载。留空 = 直连。
	GithubProxy string `mapstructure:"github_proxy"`
	// HTTPProxy 是标准 HTTP(S)_PROXY 语义，作用于全部下载（含非 GitHub 的），
	// 供只有通用出口代理、没有专用 GitHub 加速服务的用户兜底。留空 = 不使用。
	HTTPProxy string `mapstructure:"http_proxy"`
	// Timeout 只约束连接建立与响应头等待，不含大文件传输本身。
	Timeout time.Duration `mapstructure:"timeout"`
	Retries int           `mapstructure:"retries"`
}

// LinuxConfig 是 docs/LINUX_COMPATIBILITY_PLAN.md §7 描述的 Linux 专属运行时配置。
// 这整段在 Windows 上被忽略——没有 Wine/Proton 运行时的概念。
type LinuxConfig struct {
	// Runtime 选择 Proton 运行时的来源："umu"（默认，程序自动下载与管理）
	// 或 "custom"（用户自备 PROTONPATH，程序只做只读的 Preflight 检查）。
	Runtime string `mapstructure:"runtime"`
	// UmuVersion / ProtonVersion 必须是具体的 release tag，绝不能是 "latest"——
	// 通过 api.github.com 解析 latest/别名正是本项目要绕开的限流坑，见 §4.3。
	UmuVersion    string `mapstructure:"umu_version"`
	ProtonVersion string `mapstructure:"proton_version"`
	// PrefixMode：实例之间怎么分 Wine prefix。默认 "shared"。
	//
	// 一个 prefix 目录 = 一个 wineserver = 一个 Wine 会话，这条 Wine 实现细节
	// 是三种模式全部差异的来源（wineserver 是按 WINEPREFIX 目录的 dev/ino 选的）。
	//
	//   shared       全部实例共用 {BaseDir}/umu-prefix。省盘；但实例之间在注册表、
	//                命名内核对象上互相可见，**启动必须串行**
	//                （internal/instance/launchgate.go 会自动排队，等上一台到达
	//                start_initialization_successful 再放行下一台），而且
	//                **同时只能有一个启用 ArkApi 的实例**（第二个会卡在加载器
	//                启动前直到超时，见 docs/SHARED_PREFIX_MULTI_ARKAPI_PLAN.md）。
	//   per-instance 每实例一个 {BaseDir}/umu-prefix-<实例名>。完全隔离，启动可并发
	//                （与 Windows 一致），ArkApi 想开几个开几个；代价是每实例多占
	//                一个完整 prefix，且该实例首次启动会多花约一分钟创建它
	//                （GE-Proton 与 Steam Linux Runtime 仍全局共享，不重复下载）。
	//   overlay      共用一份只读底层 prefix + 每实例一个 overlayfs 可写层，落在
	//                {BaseDir}/umu-prefix-overlay/<实例名>/。隔离性与 per-instance
	//                逐条等价（各自独立的 wineserver），磁盘与首启开销接近 shared。
	//                需要 root 与内核 overlayfs；挂不上时自动降级成「从底层复制一份」
	//                （功能正确，只是多占盘）并响亮告警。
	//                见 docs/UMU_PREFIX_OVERLAY_PLAN.md。
	//
	// 切回 shared 后，另两种模式留下的目录不会自动消失，
	// 用 `asa-server prefix status | gc` 查看与清理。
	PrefixMode string `mapstructure:"prefix_mode"`
	// PrefixDir 留空 = {BaseDir}/umu-prefix。
	// 注意：per-instance 模式下这个值是**前缀**而不是目录本身，
	// 实际路径为 "<prefix_dir>-<实例名>"。
	// overlay 模式下它只决定**底层**在哪；每实例的可写层固定落在
	// {BaseDir}/umu-prefix-overlay/ 下，不跟着这个值走 —— 让一个配置项同时
	// 控制两套布局只会更难解释（docs/UMU_PREFIX_OVERLAY_PLAN.md §11.4）。
	PrefixDir string `mapstructure:"prefix_dir"`
	// UmuPythonBin：执行 umu-launcher zipapp 的 Python 解释器。
	// 留空 = 自动探测系统 python3 / python3.10…python3.N，多个取最高版本（不动系统默认 python3，
	// 也不会自动发现 venv/pyenv）。非空 = 只用这一个、不再自动探测，可填裸名字（python3.14）、
	// 绝对路径，或 venv / pyenv 的解释器路径（~ 会展开）。要求 Python >= 3.10。
	// 见 docs/UMU_PYTHON_DISCOVERY_PLAN.md。
	UmuPythonBin string `mapstructure:"umu_python_bin"`
	// AutoDownload 为 false 时 EnsureRuntime 完全不联网，缺失的运行时组件
	// 只会体现为 Preflight 报告里的问题，不会自动尝试修复。
	AutoDownload bool `mapstructure:"auto_download"`
	// SteamRTPrefetch：umu 初始化前，是否先用 pkg/download 把 Steam Linux Runtime
	// 归档下好塞进 umu 自己的下载缓存。默认 true —— umu 内部那次 150~190MB 的下载
	// 走它自带的 urllib3，没有重试、也读不到 download.http_proxy，是首次安装最常见的
	// 失败点。关掉即完全回到「让 umu 自己下」，排障用。见 docs/STEAMRT_PREFETCH_PLAN.md。
	SteamRTPrefetch bool `mapstructure:"steamrt_prefetch"`
	// InstallVCRedist：把微软 VC++ 运行时装进 Wine prefix。ArkApi 的 AsaApiLoader.exe
	// 依赖它，而 Wine/GE-Proton 的 prefix 里只有 Wine 自己的同名实现。默认 true。
	// false = 不下载不安装；启用 ArkApi 的实例启动时只多一条告警，不阻断。
	InstallVCRedist bool `mapstructure:"install_vcredist"`
	// VCRedistURL 留空 = 微软官方短链。最终地址的路径里自带文件 sha256，会自动提取
	// 校验；自建镜像若没有那一段，用 VCRedistSHA256 显式指定（小写十六进制）。
	VCRedistURL    string `mapstructure:"vcredist_url"`
	VCRedistSHA256 string `mapstructure:"vcredist_sha256"`
	// Display / XvfbBin / XvfbScreen：给 Wine 进程提供图形显示的三个旋钮。
	// AsaApiLoader.exe（ArkApi）与微软 VC++ 安装器都会创建 Win32 窗口，Wine 连不上
	// X 就直接失败，见 docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md。
	//
	// Display：显式点名要用的显示（":0"）。留空 = 读 DISPLAY 环境变量。后台服务进程
	// 没有 DISPLAY（真机 /proc/<pid>/environ 里只有 HOME=/root），这是把机器上现成的
	// X 服务告诉它的唯一办法。
	Display string `mapstructure:"display"`
	// XvfbBin：Xvfb 服务端二进制的路径，留空 = PATH + 几个常见位置。注意是
	// **Xvfb** 而不是 Debian 的 xvfb-run 脚本（Fedora/RHEL/Arch 不提供后者）。
	XvfbBin string `mapstructure:"xvfb_bin"`
	// XvfbScreen：自管 Xvfb 的 -screen 规格，留空 = 1280x1024x24。排障用。
	XvfbScreen string `mapstructure:"xvfb_screen"`
	// AllowX11Remount：/tmp/.X11-unix 是只读挂载时，允不允许把它重新挂载为可写。
	// 默认 true。X 的 socket 路径写死在 xtrans 里改不了，所以这是 WSL/WSLg（把该目录
	// 挂成只读 tmpfs）上唯一能用上自管 Xvfb 的办法；关掉就退回用 WSLg 自己的 :0。
	// 仅在 asa-server 以 root 运行、且真的是只读挂载时才动手，动手留日志，退出时还原。
	AllowX11Remount bool `mapstructure:"allow_x11_remount"`
	// WineDLLOverrides 原样追加到游戏进程的 WINEDLLOVERRIDES，留空 = 不设。
	// VC++ 那组 override 已在安装时写进 prefix 注册表，不必在这里重复；排障用。
	WineDLLOverrides string `mapstructure:"wine_dll_overrides"`
	GameID           string `mapstructure:"gameid"`

	// 以下为「游戏实例以专用非 root 用户运行」相关配置，见
	// docs/UMU_RUNTIME_USER_PLAN.md。仅当 asa-server 自身以 root 运行时生效。
	//
	// UmuRuntimeUser：降权运行游戏进程的专用账号；不存在则自动 useradd -r。
	UmuRuntimeUser string `mapstructure:"umu_runtime_user"`
	// UmuRuntimeUID / UmuRuntimeGID：非 0 时固定数值 uid/gid（BaseDir 跨机迁移时保持属主稳定）。
	UmuRuntimeUID int `mapstructure:"umu_runtime_uid"`
	UmuRuntimeGID int `mapstructure:"umu_runtime_gid"`
	// UmuRunAsRoot：true = 有意以 root 运行游戏进程，不降权、跳过全部自检。
	// 这是「降权环境不满足时 asa-server 拒绝启动」的唯一绕过开关。
	UmuRunAsRoot bool `mapstructure:"umu_run_as_root"`
	// UmuRuntimeDeepProbe：asa-server 启动自检时是否 fork 降权子进程做真实写探测。
	// 实例启动门禁处恒为开，此项只管 asa-server 启动那一次。
	UmuRuntimeDeepProbe bool `mapstructure:"umu_runtime_deep_probe"`
}

// current 让读侧无锁：热重载时整体换指针即可。
var current atomic.Pointer[Config]

// configAutoGenerated 记录最近一次 Load 是否生成了一份全新的 config.yaml——
// 见 WasConfigAutoGenerated 的文档。
var configAutoGenerated atomic.Bool

func init() {
	// 保证 Get() 在 Load() 之前（或 Load 失败后）也永远返回一份可用的配置。
	// 默认值里 auth.enabled 是 false，所以配置出问题时最坏结果是"不鉴权"，
	// 而不是"把所有人锁在门外"。
	def := defaultConfig()
	current.Store(&def)
}

// Get 返回当前生效的配置，永不返回 nil
func Get() *Config { return current.Load() }

// WasConfigAutoGenerated 报告最近一次 Load() 有没有生成一份全新的 config.yaml，
// 而不是读到一份已经存在的文件（哪怕是没有 basedir 字段的旧文件）。
//
// 首次启动向导（Windows Fyne 首次启动对话框 / Linux `asa-server setup`）靠它判断
// 要不要弹出来：§10.4 的规则是"任一级已有 config.yaml 就维持现状，不弹窗"——只有
// 真正的全新安装（三级查找哪儿都没有、纯靠 Load 兜底生成）才需要向导介入帮用户
// 选数据目录。见 docs/LINUX_COMPATIBILITY_PLAN.md §10.4。
func WasConfigAutoGenerated() bool { return configAutoGenerated.Load() }

// DatabasePath 返回鉴权数据库的绝对路径。baseDir 用于 Path 未显式配置时的回落。
func (c *Config) DatabasePath(baseDir string) string {
	if c.Auth.Database.Path != "" {
		return c.Auth.Database.Path
	}
	return filepath.Join(baseDir, "database_file", "auth.db")
}

// Load 定位并加载 config.yaml，返回解析出的 BaseDir。完整算法见
// docs/APPCONFIG_BASEDIR_PLAN.md §2 第 3 条，不接收任何目录参数——查找规则是这个
// 函数自己的职责，不是调用方传进来的外部输入。
//
// 第一步，确定读哪一份 config.yaml（完整覆盖，不做任何跨档的字段级合并，三档里
// 只有一档会被真正读取）：
//  1. 环境变量 ASA_CFG 指定的目录
//  2. 可执行文件同级目录
//  3. 系统固定目录（Windows %ProgramData%\ASAServerManager，Linux /etc/asa-server）
//  4. 都没有 → 在可执行文件同级目录生成一份默认模板
//
// 第二步，只看第一步选中的那一份文件的 basedir 字段（本次改造的核心目的：把
// "数据目录到底在哪"的权威从环境变量搬进配置文件，ASA_BASEDIR 从"能让文件整个
// 失效的最高优先级"降级为"文件没写这个字段时的兜底"，不会因为字段为空就回头去看
// 其他档位的文件）：
//  1. 字段非空 → BaseDir = 字段值
//  2. 字段为空 → 环境变量 ASA_BASEDIR 非空则用它，否则用这份 config.yaml 所在的目录
//
// 第三步，纯防御性兜底（正常输入下不会触发——第二步最后一档恒不为空）：上面两步
// 因为异常（比如 os.Executable() 报错）拿不到可用的 BaseDir 时，回落到可执行文件
// 同级目录，连这个都拿不到时再退到当前工作目录，并立刻打一条包含最终选中目录的
// Warn 级别启动警告。
//
// 文件不存在时会写出一份带注释的模板再继续（不算错误）。
// 返回错误时调用方应记录日志并继续运行 —— 此时 Get() 仍返回默认配置，返回的 BaseDir
// 仍按上面同一条算法给出最佳可用值，不会是空字符串，调用方可以安全地继续建目录/
// 写日志。
func Load() (string, error) {
	dir, locateErr := locateConfigDir()
	if locateErr != nil {
		// 第一步本身就失败了（os.Executable() 报错）：没有 dir 可用，直接走第三步兜底。
		configAutoGenerated.Store(false)
		return fallbackBaseDir(""), fmt.Errorf("定位 %s 失败: %w", ConfigFileName, locateErr)
	}
	// 记录这次 Load 有没有生成一份全新的 config.yaml（三档都没有、只能兜底生成）——
	// 首次启动向导（Fyne 对话框 / Linux setup CLI）靠 WasConfigAutoGenerated 判断
	// 要不要弹出来：任一档已有旧文件（哪怕没有 basedir 字段）都不该弹，只有"三档
	// 都是空的，纯靠这次 Load 兜底生成"才是真正的全新安装。
	configAutoGenerated.Store(!fileExists(filepath.Join(dir, ConfigFileName)))
	baseDir := resolveBaseDirValue("", dir) // 任何后续错误都回落到这个值

	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(dir)
	setDefaults(v)

	v.SetEnvPrefix("ASA")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return baseDir, fmt.Errorf("读取 %s 失败: %w", ConfigFileName, err)
		}
		// 首次运行：写出带注释的模板，方便用户后续手改。
		// 写失败不阻断启动 —— 内存里的默认值一样能跑。
		if writeErr := writeDefaultConfig(filepath.Join(dir, ConfigFileName)); writeErr != nil {
			return baseDir, fmt.Errorf("生成默认 %s 失败: %w", ConfigFileName, writeErr)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return baseDir, wrapIfAuthWanted(v, fmt.Errorf("解析 %s 失败: %w", ConfigFileName, err))
	}
	// basedir 字段单独用一个不开 AutomaticEnv 的 viper 实例重读，只反映文件内容：
	// 它的 key 名字面上拼出来正好是 ASA_BASEDIR，会被上面那个开了 AutomaticEnv 的
	// v 撞见同名的 ASA_BASEDIR 环境变量，把"文件里写了什么"和"环境变量设了什么"
	// 这两件现在优先级不同的事混在一起。
	cfg.BaseDir = fileOnlyBaseDir(dir)
	if err := cfg.Validate(); err != nil {
		return baseDir, wrapIfAuthWanted(v, err)
	}
	current.Store(&cfg)

	result := resolveBaseDirValue(cfg.BaseDir, dir)
	if result == "" {
		// 第三步防御性兜底：正常输入下不会到这里（resolveBaseDirValue 最后一档
		// dir 恒非空），保留这道闸门纯粹是防未来的意外。
		return fallbackBaseDir(dir), nil
	}
	return result, nil
}

// resolveBaseDirValue 按"文件字段 > ASA_BASEDIR 环境变量 > config.yaml 所在目录"
// 的优先级给出最终 BaseDir。fileBaseDir 传空串表示"文件字段不可用/未知"（比如文件还
// 没解析成功），此时只在环境变量与目录之间选。
func resolveBaseDirValue(fileBaseDir, dir string) string {
	if fileBaseDir != "" {
		return fileBaseDir
	}
	if env := os.Getenv("ASA_BASEDIR"); env != "" {
		return env
	}
	return dir
}

// fallbackBaseDir 是第三步防御性兜底：回落到可执行文件同级目录，连这个都拿不到
// 时退到当前工作目录，并立刻打一条包含最终目录的 Warn 级别启动警告——用户看到
// 警告要能马上知道数据落在哪，不用去翻代码或者猜。
//
// 直接在这里打日志，而不是让 Load 返回一个额外的"是否触发兜底"标志交给调用方
// 处理，是因为 pkg/logger 是零依赖、全项目唯一日志入口，其 init() 已经准备了一个
// "InitLoggerWithBaseDir 调用之前也能安全用"的纯控制台兜底 logger——这正是这个
// 场景：警告发生在 BaseDir 还没解析出来、文件日志系统根本没法初始化的最早期，
// WithConsole() 保证它不看 SetLevel 阈值、一定能在控制台露出来。
func fallbackBaseDir(attempted string) string {
	fallback := attempted
	if fallback == "" {
		var err error
		fallback, err = executableDir()
		if err != nil || fallback == "" {
			fallback, err = os.Getwd()
			if err != nil || fallback == "" {
				fallback = "."
			}
		}
	}
	logger.WithConsole().Warnf(
		"BaseDir 未能从 %s / 环境变量解析出来，已回落到 %s，数据将存放在这个目录，"+
			"请检查 %s 的 basedir 字段或 ASA_BASEDIR 环境变量是否配置正确",
		ConfigFileName, fallback, ConfigFileName)
	return fallback
}

// fileOnlyBaseDir 读取 dir/config.yaml 的 basedir 字段，不受任何环境变量影响。
// 文件不存在或解析失败时返回空串，调用方按"字段留空"处理——不是错误：
// 主 Load 流程里已经处理过"文件不存在就生成默认模板"的情况，这里只是重复获取
// 同一份文件的一个字段，不需要重复报错。
func fileOnlyBaseDir(dir string) string {
	fv := viper.New()
	fv.SetConfigName("config")
	fv.SetConfigType("yaml")
	fv.AddConfigPath(dir)
	if err := fv.ReadInConfig(); err != nil {
		return ""
	}
	return fv.GetString("basedir")
}

// locateConfigDir 定位要读的 config.yaml **所在目录**（不是 BaseDir 本身，见 Load
// 的文档），三级查找，完整覆盖语义。
func locateConfigDir() (string, error) {
	if cfgEnv := os.Getenv("ASA_CFG"); cfgEnv != "" {
		return cfgEnv, nil
	}
	exeDir, err := executableDir()
	if err != nil {
		return "", fmt.Errorf("解析可执行文件目录失败: %w", err)
	}
	if fileExists(filepath.Join(exeDir, ConfigFileName)) {
		return exeDir, nil
	}
	if sysDir := systemConfigDir(); sysDir != "" && fileExists(filepath.Join(sysDir, ConfigFileName)) {
		return sysDir, nil
	}
	// 三档都没有：落回 exe 同级，Load 会在这里生成默认模板。
	return exeDir, nil
}

// executableDirFn / systemConfigDirFn 是可在测试里替换的查找函数变量（生产代码
// 不改变行为）：os.Executable() 返回的是测试二进制自己的临时路径，测试没法把
// config.yaml 摆在那儿去验证"exe 同级"这一级，必须能整体换掉。
var (
	executableDirFn   = defaultExecutableDir
	systemConfigDirFn = defaultSystemConfigDir
)

func executableDir() (string, error) { return executableDirFn() }

func defaultExecutableDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func systemConfigDir() string { return systemConfigDirFn() }

// defaultSystemConfigDir 是三级查找的第 3 档，主要给开发/调试用：本机固定放
// 一份，不管当前跑的是哪次临时构建出的二进制。取不到 %ProgramData% 时返回空串，
// 调用方按"没有这一级"处理，不当作错误。
func defaultSystemConfigDir() string {
	if runtime.GOOS == "windows" {
		pd := os.Getenv("ProgramData")
		if pd == "" {
			return ""
		}
		return filepath.Join(pd, "ASAServerManager")
	}
	return "/etc/asa-server"
}

// OverrideSearchDirsForTest 仅供测试使用：临时把"可执行文件同级目录"与"系统固定
// 目录"这两级查找指向给定目录，测试结束时通过 t.Cleanup 自动还原。生产代码不会、
// 也不应该调用它。ASA_CFG 那一级不需要它，测试直接 t.Setenv("ASA_CFG", dir) 即可。
func OverrideSearchDirsForTest(t testing.TB, exeDir, systemDir string) {
	t.Helper()
	origExe, origSys := executableDirFn, systemConfigDirFn
	executableDirFn = func() (string, error) { return exeDir, nil }
	systemConfigDirFn = func() string { return systemDir }
	t.Cleanup(func() {
		executableDirFn, systemConfigDirFn = origExe, origSys
	})
}

// ErrAuthConfigInvalid 表示配置有错，而且这份配置**明确要求开启鉴权**。
//
// 调用方必须让程序停下来，不能像普通配置错误那样"回落默认值继续跑"：
// 默认值里 auth.enabled 是 false，一个 domains 的拼写错误就会让服务
// 静默地不带鉴权启动 —— 而这台机器很可能正暴露在公网上。
// 配置错误应该表现为"起不来"，不该表现为"安全防护悄悄消失了"。
var ErrAuthConfigInvalid = errors.New("鉴权配置无效")

func wrapIfAuthWanted(v *viper.Viper, err error) error {
	if v.GetBool("auth.enabled") {
		return fmt.Errorf("%w: %w", ErrAuthConfigInvalid, err)
	}
	return err
}

// writeDefaultConfig 写出带注释的模板。
// 用手写模板而不是 viper.SafeWriteConfigAs，是因为后者会丢掉所有注释，
// 而这份文件是给用户手改的，注释比什么都重要。
//
// 先 MkdirAll 目标目录：老版本这里能省略是因为 main.go 在调用 Load 之前已经把
// BaseDir 的子目录建过一轮，目标目录顺带存在；现在 BaseDir 解析本身要靠这份文件
// 才能定出来，顺序反过来了，所以这里不能再假设目录已经存在（比如 ASA_CFG 指向
// 一个全新、尚未创建的目录）。
func writeDefaultConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // 已存在就不覆盖
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	return os.WriteFile(path, []byte(renderDefaultConfigTemplate()), 0o644)
}

// DefaultPrivateNetworks 是 lan_bypass 未配置 networks 时使用的内网网段集合
var DefaultPrivateNetworks = []string{
	"127.0.0.0/8",
	"::1/128",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"169.254.0.0/16",
	"fc00::/7",
	"fe80::/10",
}

func defaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Port: 19193,
			// Linux 上系统信任库不影响浏览器（Firefox/Chrome 用各自的 NSS db），
			// 默认装了也还是红锁，只会制造困惑——所以默认关闭，见 docs/LINUX_COMPATIBILITY_PLAN.md §5.7。
			TLS:            TLSConfig{Enabled: true, TrustLocalCA: runtime.GOOS != "linux"},
			TrustedProxies: []string{"127.0.0.1", "::1"},
		},
		Auth: AuthConfig{
			Enabled:  false,
			Database: DatabaseConfig{AutoMigrate: true},
			Session: SessionConfig{
				TTL:         7 * 24 * time.Hour,
				IdleTimeout: 24 * time.Hour,
				CookieName:  "asa_session",
				CookiePath:  "/",
				SameSite:    "lax",
			},
			LANBypass: LANBypassConfig{
				Enabled:                false,
				Networks:               DefaultPrivateNetworks,
				DenyIfForwarded:        true,
				AutoDetectLocalSubnets: false,
			},
			TOTP: TOTPConfig{
				Enabled: true,
				Issuer:  "ASA Server Manager",
				Skew:    1,
			},
			Password:  PasswordConfig{MinLength: 8, BcryptCost: 12},
			RateLimit: RateLimitConfig{MaxFailures: 5, Window: 15 * time.Minute, Lockout: 15 * time.Minute},
			Audit:     AuditConfig{MaxRows: 2000},
		},
		Download: DownloadConfig{
			Timeout: 30 * time.Second,
			Retries: 3,
		},
		Linux: LinuxConfig{
			Runtime:         "umu",
			UmuVersion:      "1.4.4",
			ProtonVersion:   "GE-Proton10-34",
			PrefixMode:      "shared",
			AutoDownload:    true,
			SteamRTPrefetch: true,
			InstallVCRedist: true,
			AllowX11Remount: true,
			GameID:          "umu-default",
			UmuRuntimeUser:  "asa-umu-runtime",
		},
	}
}

func setDefaults(v *viper.Viper) {
	d := defaultConfig()

	v.SetDefault("basedir", d.BaseDir)

	v.SetDefault("server.port", d.Server.Port)
	v.SetDefault("server.tls.enabled", d.Server.TLS.Enabled)
	v.SetDefault("server.tls.trust_local_ca", d.Server.TLS.TrustLocalCA)
	v.SetDefault("server.tls.cert_file", "")
	v.SetDefault("server.tls.key_file", "")
	v.SetDefault("server.tls.domains", []string{})
	v.SetDefault("server.trusted_proxies", d.Server.TrustedProxies)
	v.SetDefault("server.cors.allowed_origins", []string{})

	v.SetDefault("auth.enabled", d.Auth.Enabled)
	v.SetDefault("auth.database.path", "")
	v.SetDefault("auth.database.auto_migrate", d.Auth.Database.AutoMigrate)

	v.SetDefault("auth.session.ttl", d.Auth.Session.TTL)
	v.SetDefault("auth.session.idle_timeout", d.Auth.Session.IdleTimeout)
	v.SetDefault("auth.session.cookie_name", d.Auth.Session.CookieName)
	v.SetDefault("auth.session.cookie_path", d.Auth.Session.CookiePath)
	v.SetDefault("auth.session.same_site", d.Auth.Session.SameSite)

	v.SetDefault("auth.lan_bypass.enabled", d.Auth.LANBypass.Enabled)
	v.SetDefault("auth.lan_bypass.networks", d.Auth.LANBypass.Networks)
	v.SetDefault("auth.lan_bypass.deny_if_forwarded", d.Auth.LANBypass.DenyIfForwarded)
	v.SetDefault("auth.lan_bypass.auto_detect_local_subnets", d.Auth.LANBypass.AutoDetectLocalSubnets)

	v.SetDefault("auth.totp.enabled", d.Auth.TOTP.Enabled)
	v.SetDefault("auth.totp.required", d.Auth.TOTP.Required)
	v.SetDefault("auth.totp.issuer", d.Auth.TOTP.Issuer)
	v.SetDefault("auth.totp.skew", d.Auth.TOTP.Skew)

	v.SetDefault("auth.password.min_length", d.Auth.Password.MinLength)
	v.SetDefault("auth.password.bcrypt_cost", d.Auth.Password.BcryptCost)

	v.SetDefault("auth.ratelimit.max_failures", d.Auth.RateLimit.MaxFailures)
	v.SetDefault("auth.ratelimit.window", d.Auth.RateLimit.Window)
	v.SetDefault("auth.ratelimit.lockout", d.Auth.RateLimit.Lockout)

	v.SetDefault("auth.audit.max_rows", d.Auth.Audit.MaxRows)

	v.SetDefault("download.github_proxy", "")
	v.SetDefault("download.http_proxy", "")
	v.SetDefault("download.timeout", d.Download.Timeout)
	v.SetDefault("download.retries", d.Download.Retries)

	v.SetDefault("linux.runtime", d.Linux.Runtime)
	v.SetDefault("linux.umu_version", d.Linux.UmuVersion)
	v.SetDefault("linux.proton_version", d.Linux.ProtonVersion)
	v.SetDefault("linux.prefix_mode", d.Linux.PrefixMode)
	v.SetDefault("linux.prefix_dir", "")
	v.SetDefault("linux.umu_python_bin", "")
	v.SetDefault("linux.auto_download", d.Linux.AutoDownload)
	v.SetDefault("linux.steamrt_prefetch", d.Linux.SteamRTPrefetch)
	v.SetDefault("linux.install_vcredist", d.Linux.InstallVCRedist)
	v.SetDefault("linux.vcredist_url", "")
	v.SetDefault("linux.vcredist_sha256", "")
	v.SetDefault("linux.wine_dll_overrides", "")
	v.SetDefault("linux.display", "")
	v.SetDefault("linux.xvfb_bin", "")
	v.SetDefault("linux.xvfb_screen", "")
	v.SetDefault("linux.allow_x11_remount", d.Linux.AllowX11Remount)
	v.SetDefault("linux.gameid", d.Linux.GameID)
	v.SetDefault("linux.umu_runtime_user", d.Linux.UmuRuntimeUser)
	v.SetDefault("linux.umu_runtime_uid", d.Linux.UmuRuntimeUID)
	v.SetDefault("linux.umu_runtime_gid", d.Linux.UmuRuntimeGID)
	v.SetDefault("linux.umu_run_as_root", d.Linux.UmuRunAsRoot)
	v.SetDefault("linux.umu_runtime_deep_probe", d.Linux.UmuRuntimeDeepProbe)
}
