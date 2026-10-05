// Package meshcoord 是协调节点的服务实现：在线登记、交换候选地址、字节中转、STUN 端点。
// 它不理解业务，也不签发任何身份（docs/REMOTE_MANAGER_MESH_PLAN.md §4、§8、§12 P1-5）。
//
// 入口在 cmd/asa-coordinator。本包刻意不依赖 asa-server 的应用配置、Badger、Fyne、frp、gin——
// 协调节点是独立部署的小二进制（守卫测试见 deps_test.go）。
package meshcoord

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"
)

// ConfigFileName 是协调节点配置文件的文件名。
const ConfigFileName = "coordinator.yaml"

// systemConfigPath 是 Linux 上的系统级配置位置（查找顺序的第三位）。
const systemConfigPath = "/etc/asa-coordinator/" + ConfigFileName

// Config 是 coordinator.yaml 的内容。
type Config struct {
	// Listen 是 gRPC（TLS）监听地址，如 ":443"。
	Listen string `yaml:"listen"`
	// PublicAddr 是管理器连接用的地址（host:port），写进 join blob。
	PublicAddr string `yaml:"public_addr"`
	// DataDir 存数据库与自身身份；相对路径按配置文件所在目录解析。
	DataDir string `yaml:"data_dir"`

	TLS    TLSConfig    `yaml:"tls"`
	Limits LimitsConfig `yaml:"limits"`
	STUN   STUNConfig   `yaml:"stun"`

	// path 是加载自的文件（绝对路径），不来自 YAML。
	path string
}

// TLSConfig：两者都给出时用正式证书，否则用 data_dir 里的自签证书。
type TLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// LimitsConfig 是中转的闸门——中转是协调节点唯一的成本项（§5.4）。
type LimitsConfig struct {
	// MaxRelaysPerNode：每个节点同时参与的中转会话上限（含尚未配对的）。
	MaxRelaysPerNode int `yaml:"max_relays_per_node"`
	// RelayIdleTimeout：中转会话多久没有数据就关闭。
	RelayIdleTimeout time.Duration `yaml:"relay_idle_timeout"`
	// RelayRateKBps：每个节点经中转发出的速率上限（KiB/s），0 = 不限。
	RelayRateKBps int `yaml:"relay_rate_kbps"`
}

// STUNConfig 见 §12「P1-8」。listen 为空 = 关闭。
type STUNConfig struct {
	Listen         []string `yaml:"listen"`
	Advertise      []string `yaml:"advertise"`
	RatePerSource  float64  `yaml:"rate_per_source"`
	BurstPerSource int      `yaml:"burst_per_source"`
	RateGlobal     float64  `yaml:"rate_global"`
}

const (
	defaultDataDir          = "data"
	defaultMaxRelaysPerNode = 8
	defaultRelayIdleTimeout = 5 * time.Minute
)

// Path 返回配置文件的绝对路径。
func (c *Config) Path() string { return c.path }

// applyDefaults 填充零值字段。STUN 的速率默认值由 pkg/stun 自己给。
func (c *Config) applyDefaults() {
	if c.DataDir == "" {
		c.DataDir = defaultDataDir
	}
	if c.Limits.MaxRelaysPerNode == 0 {
		c.Limits.MaxRelaysPerNode = defaultMaxRelaysPerNode
	}
	if c.Limits.RelayIdleTimeout == 0 {
		c.Limits.RelayIdleTimeout = defaultRelayIdleTimeout
	}
}

// resolvePaths 把相对路径按配置文件所在目录解析——作为服务运行时工作目录不可控，
// 相对 cwd 会把数据库写到意想不到的地方。
func (c *Config) resolvePaths(base string) {
	abs := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	c.DataDir = abs(c.DataDir)
	c.TLS.CertFile = abs(c.TLS.CertFile)
	c.TLS.KeyFile = abs(c.TLS.KeyFile)
}

// Validate 校验配置。返回的 warnings 不阻止启动，但应当打印出来。
func (c *Config) Validate() (warnings []string, err error) {
	var errs []error
	if c.Listen == "" {
		errs = append(errs, errors.New("listen 不能为空"))
	} else if _, _, e := net.SplitHostPort(c.Listen); e != nil {
		errs = append(errs, fmt.Errorf("listen %q 不是 host:port", c.Listen))
	}
	if c.PublicAddr == "" {
		errs = append(errs, errors.New("public_addr 不能为空（它会写进 join blob，管理器按它来连）"))
	} else if host, port, e := net.SplitHostPort(c.PublicAddr); e != nil || host == "" || port == "" {
		errs = append(errs, fmt.Errorf("public_addr %q 不是 host:port", c.PublicAddr))
	} else if ip, e := netip.ParseAddr(host); e == nil && isDocumentationAddr(ip) {
		warnings = append(warnings, fmt.Sprintf("public_addr %s 还是模板里的示例地址，管理器用这份 join blob 连不上，请改成本机的公网 IP 或域名", c.PublicAddr))
	}
	if (c.TLS.CertFile == "") != (c.TLS.KeyFile == "") {
		errs = append(errs, errors.New("tls.cert_file 与 tls.key_file 必须同时给出或同时留空"))
	}
	if c.Limits.MaxRelaysPerNode < 0 || c.Limits.RelayIdleTimeout < 0 || c.Limits.RelayRateKBps < 0 {
		errs = append(errs, errors.New("limits 的各项不能为负数"))
	}

	switch n := len(c.STUN.Listen); {
	case n > 2:
		errs = append(errs, fmt.Errorf("stun.listen 最多 2 个，实际 %d 个", n))
	case n == 1:
		warnings = append(warnings, "stun.listen 只有 1 个端口：STUN 可用，但管理器分辨不出对称型 NAT（NAT4），建议配两个端口")
	}
	seen := map[int]bool{}
	for _, l := range c.STUN.Listen {
		_, portStr, e := net.SplitHostPort(l)
		port, perr := strconv.Atoi(portStr)
		if e != nil || perr != nil || port <= 0 || port > 65535 {
			errs = append(errs, fmt.Errorf("stun.listen %q 不是带端口号的 host:port", l))
			continue
		}
		if seen[port] {
			errs = append(errs, fmt.Errorf("stun.listen 的两个端口不能相同（%d）", port))
		}
		seen[port] = true
	}
	for _, a := range c.STUN.Advertise {
		if host, port, e := net.SplitHostPort(a); e != nil || host == "" || port == "" {
			errs = append(errs, fmt.Errorf("stun.advertise %q 不是 host:port", a))
		}
	}
	if c.STUN.RatePerSource < 0 || c.STUN.BurstPerSource < 0 || c.STUN.RateGlobal < 0 {
		errs = append(errs, errors.New("stun 的限流参数不能为负数"))
	}
	return warnings, errors.Join(errs...)
}

// STUNAdvertise 返回下发给管理器的 STUN 地址：显式的 stun.advertise，
// 否则是 public_addr 的主机名 + 各 listen 的端口。未开 STUN 时为空。
//
// 主机名不在这里解析，交给管理器解析——管理器可能走 IPv6。
func (c *Config) STUNAdvertise() []string {
	if len(c.STUN.Listen) == 0 {
		return nil
	}
	if len(c.STUN.Advertise) > 0 {
		return append([]string(nil), c.STUN.Advertise...)
	}
	host, _, err := net.SplitHostPort(c.PublicAddr)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(c.STUN.Listen))
	for _, l := range c.STUN.Listen {
		if _, port, err := net.SplitHostPort(l); err == nil {
			out = append(out, net.JoinHostPort(host, port))
		}
	}
	return out
}

// ErrConfigNotFound 表示按查找顺序哪里都没有配置文件。
type ErrConfigNotFound struct {
	Searched []string
}

func (e *ErrConfigNotFound) Error() string {
	var b bytes.Buffer
	b.WriteString("未找到协调节点配置文件 " + ConfigFileName + "，已查找（按顺序）：\n")
	for i, p := range e.Searched {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, p)
	}
	b.WriteString("请先运行 asa-coordinator config init 生成配置，编辑后再启动。")
	return b.String()
}

// SearchPaths 返回查找顺序：-c 给出的路径 > 二进制同目录 > /etc/asa-coordinator（仅 Linux）。
// 不支持环境变量覆盖。
func SearchPaths(flagPath string) []string {
	if flagPath != "" {
		if abs, err := filepath.Abs(flagPath); err == nil {
			flagPath = abs
		}
		return []string{flagPath}
	}
	var paths []string
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), ConfigFileName))
	}
	if runtime.GOOS == "linux" {
		paths = append(paths, systemConfigPath)
	}
	return paths
}

// Locate 按查找顺序找到配置文件。显式给了 -c 时只看那一个。
func Locate(flagPath string) (string, error) {
	paths := SearchPaths(flagPath)
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", &ErrConfigNotFound{Searched: paths}
}

// Load 读取并校验 path。不认识的字段报错——拼错的 stun.listn 不该被静默忽略、变成「STUN 没开」。
func Load(path string) (*Config, []string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, &ErrConfigNotFound{Searched: []string{abs}}
		}
		return nil, nil, err
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, fmt.Errorf("%s 是空文件", abs)
		}
		return nil, nil, fmt.Errorf("解析 %s: %w", abs, err)
	}
	cfg.path = abs
	cfg.applyDefaults()
	cfg.resolvePaths(filepath.Dir(abs))
	warnings, err := cfg.Validate()
	if err != nil {
		return nil, warnings, fmt.Errorf("%s 校验失败：\n%w", abs, err)
	}
	return &cfg, warnings, nil
}

// LocateAndLoad = Locate + Load。
func LocateAndLoad(flagPath string) (*Config, []string, error) {
	p, err := Locate(flagPath)
	if err != nil {
		return nil, nil, err
	}
	return Load(p)
}

// isDocumentationAddr 报告 ip 是否在 RFC 5737 / RFC 3849 的文档示例段（模板用的就是它们）。
func isDocumentationAddr(ip netip.Addr) bool {
	for _, p := range docPrefixes {
		if p.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}

var docPrefixes = []netip.Prefix{
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
}
