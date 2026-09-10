package frpmanage

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/fatedier/frp/pkg/config"
	v1 "github.com/fatedier/frp/pkg/config/v1"
)

const (
	// configFileName 是 FRP 配置的唯一真相，落在 {BaseDir}/frp/ 下。
	//
	// 用明文 JSON 而不是 BadgerDB：本仓库的分工是「Badger = 机器产生的实例状态，
	// SQLite = 仅鉴权，用户填的配置一律是可读文本」（schedules.json 是同类先例）。
	// 隧道配置还是「服务器进不去时用来救命」的东西，必须能备份、能手工放回、
	// 能肉眼核对 token。详见 docs/FRP_FORM_CONFIG_PLAN.md 决策 D6。
	configFileName = "frpc.json"

	// defaultServerPort 是 frps 的默认监听端口，与 frp 自己的默认值一致。
	defaultServerPort = 7000

	// maxPortsPerRule / maxProxies 是端口展开的硬上限。
	//
	// 每个端口都是一条**独立的 frp 代理注册**：一个手滑写成 1-65535 的范围会向
	// frps 发起六万多次注册，既打垮对端也让本进程日志彻底不可读。而且 frp 是
	// 库内调用（无崩溃隔离），配置面越宽、踩到 frp 边缘路径的面也越宽 ——
	// 这两个上限既保护 frps 也保护本进程。
	// ARK 一台实例实际只需要游戏端口 + 查询端口 + RCON，128 条足够十几个实例。
	maxPortsPerRule = 64
	maxProxies      = 128
)

// ErrNotConfigured 表示还没有配置过 FRP（frpc.json 不存在）。
//
// 这是个哨兵错误而不是普通错误：自动启动路径要靠它把「用户根本没配」
// 与「配了但起不来」区分开，前者只记一条 INFO，不该刷 ERROR。
var ErrNotConfigured = errors.New("frp 尚未配置")

// Protocol 是一条端口规则的协议选择。
type Protocol string

const (
	ProtocolTCP  Protocol = "tcp"
	ProtocolUDP  Protocol = "udp"
	ProtocolBoth Protocol = "tcp+udp"
)

// expand 把协议展开成实际要建的 frp 代理类型。
func (p Protocol) expand() []string {
	switch p {
	case ProtocolTCP:
		return []string{"tcp"}
	case ProtocolUDP:
		return []string{"udp"}
	case ProtocolBoth:
		return []string{"tcp", "udp"}
	default:
		return nil
	}
}

func (p Protocol) valid() bool { return len(p.expand()) > 0 }

// PortRule 是一条端口映射规则。
//
// remotePort 恒等于 localPort，不开放配置：现网配置一直如此，ARK 客户端也只认
// 同号端口，开放它等于在表单里再加一列、而大多数填错的组合根本无法工作。
type PortRule struct {
	Start    int      `json:"start"`            // 起始端口（含）
	End      int      `json:"end"`              // 结束端口（含），单端口时与 Start 相同
	Protocol Protocol `json:"protocol"`         // tcp | udp | tcp+udp
	Remark   string   `json:"remark,omitempty"` // 备注，仅供 UI 显示
}

// Config 是 FRP 配置的唯一真相。
//
// 字段刻意只有三组：地址、鉴权、端口映射。frp 的其余能力（http 代理、visitor、
// STUN、带宽限制…）本项目用不到，不开放也不落盘 —— 留一个「高级模式」等于留
// 两条真相路径，而第二条会在表单保存时被无声覆盖。
type Config struct {
	ServerAddr string     `json:"server_addr"`           // frps 地址，纯 host 或 host:port
	ServerPort int        `json:"server_port,omitempty"` // 省略/0 → 7000
	Token      string     `json:"token"`                 // auth.token，可为空（frps 未开鉴权）
	Rules      []PortRule `json:"rules"`
}

// Normalize 去掉用户从别处粘贴时常见的首尾空白。
//
// 单独一步而不是塞进 Validate：Validate 是只读判断，而这里会改数据 ——
// 保存路径要先规整再校验，读盘路径也要规整（老文件可能带空格）。
func (c *Config) Normalize() {
	c.ServerAddr = strings.TrimSpace(c.ServerAddr)
	c.Token = strings.TrimSpace(c.Token)
	for i := range c.Rules {
		c.Rules[i].Remark = strings.TrimSpace(c.Rules[i].Remark)
		c.Rules[i].Protocol = Protocol(strings.ToLower(strings.TrimSpace(string(c.Rules[i].Protocol))))
	}
}

// splitAddr 把 ServerAddr 拆成 host 与「地址里自带的端口」。
//
// 允许三种写法：host、host:port、[::1]:port。裸 IPv6（如 "::1"）会让
// SplitHostPort 报 "too many colons"，此时整串就是 host —— 这是正常输入，
// 不是错误。
func (c *Config) splitAddr() (host string, inlinePort int) {
	addr := c.ServerAddr
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 0
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return addr, 0
	}
	return h, n
}

// host 返回不带端口的 frps 主机名/IP。
func (c *Config) host() string {
	h, _ := c.splitAddr()
	return h
}

// port 返回最终生效的 frps 端口。
func (c *Config) port() int {
	_, inline := c.splitAddr()
	if inline > 0 {
		return inline
	}
	if c.ServerPort > 0 {
		return c.ServerPort
	}
	return defaultServerPort
}

// commonChanged 判断与 other 相比，frps 连接参数（而非代理清单）是否变了。
//
// 这是热更新的分流判据：连接参数变了必须重新登录（只能重启），
// 只有代理清单变时可以走 UpdateConfigSource 热更新，不断开已建立的隧道。
func (c *Config) commonChanged(other *Config) bool {
	if other == nil {
		return true
	}
	return c.host() != other.host() || c.port() != other.port() || c.Token != other.Token
}

// proxyCount 返回展开后的代理条数。
func (c *Config) proxyCount() int {
	n := 0
	for _, r := range c.Rules {
		if r.End < r.Start {
			continue
		}
		n += (r.End - r.Start + 1) * len(r.Protocol.expand())
	}
	return n
}

// proxyName 是代理在 frps 侧的登记名。
//
// 保持 {proto}-asaserver-{port}，与老的 frpc.toml 模板生成的名字逐字一致：
// frps 按名字登记代理，老用户切过来时对端看到的东西不变。这个名字还是查询
// 单条代理运行状态的键（Status 里 StatusExporter.GetProxyStatus 用），
// 所以**不能带随机成分**。
func proxyName(proto string, port int) string {
	return fmt.Sprintf("%s-asaserver-%d", proto, port)
}

// eachProxy 按 build 相同的顺序遍历展开后的每一条代理。
//
// Status 用它去 StatusExporter 里查每条代理的运行状态 —— 顺序与 build 一致，
// 所以 UI 上的排列与用户填的规则顺序对得上。
func (c *Config) eachProxy(fn func(proto string, port int, name string)) {
	for _, r := range c.Rules {
		for p := r.Start; p <= r.End; p++ {
			for _, proto := range r.Protocol.expand() {
				fn(proto, p, proxyName(proto, p))
			}
		}
	}
}

// Validate 校验配置，返回不阻塞保存的提示（warnings）与阻塞保存的错误。
//
// 保存接口与 Start() 两处都要调：前者给用户即时反馈，后者防住有人手改坏了
// frpc.json 之后才发现。
func (c *Config) Validate() (warnings []string, err error) {
	if c.ServerAddr == "" {
		return nil, errors.New("远程服务器地址不能为空")
	}

	host, inline := c.splitAddr()
	if host == "" {
		return nil, errors.New("远程服务器地址无效")
	}
	if inline > 0 && c.ServerPort > 0 && inline != c.ServerPort {
		return nil, fmt.Errorf("地址里已带端口 %d，与端口字段 %d 冲突", inline, c.ServerPort)
	}
	if p := c.port(); p < 1 || p > 65535 {
		return nil, fmt.Errorf("frps 端口 %d 无效，需在 1-65535 之间", p)
	}

	if len(c.Rules) == 0 {
		return nil, errors.New("至少需要一条端口映射规则")
	}

	// firstUse 记录每个 (协议, 端口) 头一次是被第几条规则占用的，用于重叠报错。
	//
	// 必须在保存时就挡住：重叠会展开出两个同名代理，而 frps 是按名字登记的，
	// 第二个会被拒绝 —— 但那个错误发生在启动后的异步登录流程里，用户在面板上
	// 只看到「启动了又停了」。同步挡掉才有可读的错误。
	firstUse := make(map[string]int)

	for i, r := range c.Rules {
		n := i + 1
		if !r.Protocol.valid() {
			return nil, fmt.Errorf("第 %d 条规则：协议只能是 tcp / udp / tcp+udp", n)
		}
		if r.Start < 1 || r.Start > 65535 {
			return nil, fmt.Errorf("第 %d 条规则：起始端口 %d 无效，需在 1-65535 之间", n, r.Start)
		}
		if r.End < 1 || r.End > 65535 {
			return nil, fmt.Errorf("第 %d 条规则：结束端口 %d 无效，需在 1-65535 之间", n, r.End)
		}
		if r.Start > r.End {
			return nil, fmt.Errorf("第 %d 条规则：起始端口 %d 不能大于结束端口 %d", n, r.Start, r.End)
		}
		if span := r.End - r.Start + 1; span > maxPortsPerRule {
			return nil, fmt.Errorf("第 %d 条规则跨越 %d 个端口，单条上限 %d", n, span, maxPortsPerRule)
		}

		for p := r.Start; p <= r.End; p++ {
			for _, proto := range r.Protocol.expand() {
				key := proxyName(proto, p)
				if prev, dup := firstUse[key]; dup {
					return nil, fmt.Errorf("第 %d 条规则的 %s %d 与第 %d 条重复", n, proto, p, prev)
				}
				firstUse[key] = n
			}
		}
	}

	if total := c.proxyCount(); total > maxProxies {
		return nil, fmt.Errorf("端口映射共展开 %d 条代理，上限 %d", total, maxProxies)
	}

	if c.Token == "" {
		warnings = append(warnings, "未设置验证密钥，仅在 frps 未开启鉴权时可用")
	}
	return warnings, nil
}

// ---------------------------------------------------------------------------
// 展开为 frp 的配置对象
// ---------------------------------------------------------------------------

// frpSchema 等一组类型是 frp v1 客户端配置的**子集**，字段名严格照 frp 文档化的
// 配置 schema 写（不是照 Go struct 的字段名抄）。
type frpSchema struct {
	ServerAddr string           `json:"serverAddr"`
	ServerPort int              `json:"serverPort"`
	Auth       frpAuthSchema    `json:"auth"`
	Proxies    []frpProxySchema `json:"proxies"`
}

type frpAuthSchema struct {
	Token string `json:"token,omitempty"`
}

type frpProxySchema struct {
	Name       string `json:"name"`
	Type       string `json:"type"` // tcp | udp
	LocalPort  int    `json:"localPort"`
	RemotePort int    `json:"remotePort"`
}

// build 把面板参数展开成 frp 的 Common 配置与代理清单。
//
// 走 config.LoadConfigure（喂内存字节）而不是直接拼 v1.TCPProxyConfig 这类结构体：
// frp 并不承诺 client / config/v1 这些 Go 包的 API 稳定（近期就有过
// ServiceOptions.ConfigSourceAggregator 变必填这样的破坏性变更，见
// docs/LINUX_COMPATIBILITY_PLAN.md §5.10.4 坑 #5），但**配置 schema 是它文档化
// 并保证兼容的**。多一次 marshal/unmarshal（每次启动一次，可忽略）换来的是：
// frp 改内部结构时我们不受影响，且白拿它自己的默认值填充与 schema 校验。
//
// ⚠️ 这不是「又回到写配置文件」：这段字节只存在于内存，磁盘上仍然只有我们自己的
// frpc.json，用户没有可手改的入口。
func (c *Config) build() (*v1.ClientCommonConfig, []v1.ProxyConfigurer, error) {
	s := frpSchema{
		ServerAddr: c.host(),
		ServerPort: c.port(),
		Auth:       frpAuthSchema{Token: c.Token},
	}
	for _, r := range c.Rules {
		for p := r.Start; p <= r.End; p++ {
			for _, proto := range r.Protocol.expand() {
				s.Proxies = append(s.Proxies, frpProxySchema{
					Name:       proxyName(proto, p),
					Type:       proto,
					LocalPort:  p,
					RemotePort: p,
				})
			}
		}
	}

	b, err := json.Marshal(&s)
	if err != nil {
		return nil, nil, fmt.Errorf("序列化 frp 配置失败: %w", err)
	}

	// LoadConfigure 认出 JSON buffer 后走 DecodeClientConfigJSON，proxies 会按
	// type 字段分派成具体的 TCPProxyConfig / UDPProxyConfig。
	// strict=false 与 frpc 命令行默认一致，也让 frp 将来加字段时我们不会炸。
	var all v1.ClientConfig
	if err := config.LoadConfigure(b, &all, false); err != nil {
		return nil, nil, fmt.Errorf("构建 frp 配置失败: %w", err)
	}

	// LoadConfigure 只解码不补默认值 —— Complete 是 LoadClientConfigResult 里
	// 单独调的一步，走内存字节这条路必须自己补上，否则 NatHoleSTUNServer、
	// LoginFailExit、UDPPacketSize 全是零值。
	common := &all.ClientCommonConfig
	if err := common.Complete(); err != nil {
		return nil, nil, fmt.Errorf("补全 frp 配置默认值失败: %w", err)
	}

	proxies := make([]v1.ProxyConfigurer, 0, len(all.Proxies))
	for _, p := range all.Proxies {
		proxies = append(proxies, p.ProxyConfigurer)
	}
	return common, proxies, nil
}

// ---------------------------------------------------------------------------
// 落盘
// ---------------------------------------------------------------------------

func configPath(dir string) string { return filepath.Join(dir, configFileName) }

// LoadConfig 读取 dir 下的 frpc.json。
//
// 文件不存在返回 ErrNotConfigured（首次运行的正常情况，不是故障）。
func LoadConfig(dir string) (*Config, error) {
	data, err := os.ReadFile(configPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotConfigured
		}
		return nil, fmt.Errorf("读取 %s 失败: %w", configFileName, err)
	}
	if len(data) == 0 {
		return nil, ErrNotConfigured
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", configFileName, err)
	}
	cfg.Normalize()
	return &cfg, nil
}

// SaveConfig 原子写入 frpc.json。
//
// 先写临时文件再 rename：中途掉电不会留下半截 JSON。权限 0600 —— 里面有 token，
// 与 auth/secret.key 同属机密文件。
func SaveConfig(dir string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 %s 失败: %w", configFileName, err)
	}

	path := configPath(dir)
	tmp, err := os.CreateTemp(dir, configFileName+".tmp*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	// CreateTemp 建出来是 0600，但显式设一次：Windows 上语义不同，且将来有人
	// 改成普通 Create 时这行仍然守着。
	if err := os.Chmod(tmpName, 0600); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("设置 %s 权限失败: %w", configFileName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("替换 %s 失败: %w", path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 从老的 frpc.toml 迁移
// ---------------------------------------------------------------------------

const legacyTOMLName = "frpc.toml"

// migrateFromTOML 把老的 frpc.toml 一次性转成 frpc.json。
//
// 用 frp 自己的 LoadClientConfigResult 解析而不是手写 TOML 解析器：现网那份配置
// 是 {{ parseNumberRangePair }} 的 Go 模板写法，只有 frp 的加载器会渲染它。
//
// 返回 (是否发生了迁移, 无法表达而被丢弃的代理名, error)。尽力而为：任何失败都
// 不该阻塞启动，调用方记一条 WARN 当作「未配置」即可。
func migrateFromTOML(dir string) (migrated bool, dropped []string, err error) {
	if _, statErr := os.Stat(configPath(dir)); statErr == nil {
		return false, nil, nil // 已有 JSON，不看 TOML
	}
	tomlPath := filepath.Join(dir, legacyTOMLName)
	if _, statErr := os.Stat(tomlPath); statErr != nil {
		return false, nil, nil // 没有老配置，全新安装
	}

	result, err := config.LoadClientConfigResult(tomlPath, false)
	if err != nil {
		return false, nil, fmt.Errorf("解析老配置 %s 失败: %w", legacyTOMLName, err)
	}

	cfg := &Config{
		ServerAddr: result.Common.ServerAddr,
		ServerPort: result.Common.ServerPort,
		Token:      result.Common.Auth.Token,
	}

	// ports[proto] = 该协议下所有 localPort==remotePort 的端口
	ports := map[string][]int{}
	for _, p := range result.Proxies {
		base := p.GetBaseConfig()
		var remote int
		switch typed := p.(type) {
		case *v1.TCPProxyConfig:
			remote = typed.RemotePort
		case *v1.UDPProxyConfig:
			remote = typed.RemotePort
		default:
			dropped = append(dropped, base.Name)
			continue
		}
		if remote != base.LocalPort || base.LocalPort == 0 {
			// remotePort != localPort 是本方案表达不了的，不静默丢弃
			dropped = append(dropped, base.Name)
			continue
		}
		ports[base.Type] = append(ports[base.Type], base.LocalPort)
	}
	for _, v := range result.Visitors {
		dropped = append(dropped, v.GetBaseConfig().Name)
	}

	cfg.Rules = rulesFromPorts(ports)

	if err := SaveConfig(dir, cfg); err != nil {
		return false, dropped, err
	}
	// 老文件改名保留：用户可回查，且再次启动不会重复迁移。
	if err := os.Rename(tomlPath, tomlPath+".migrated"); err != nil {
		return true, dropped, fmt.Errorf("重命名 %s 失败: %w", legacyTOMLName, err)
	}
	return true, dropped, nil
}

// rulesFromPorts 把「协议 → 端口集合」压回连续的 PortRule。
//
// 同一端口上 tcp 与 udp 都有时合并成 tcp+udp —— 否则一台实例的游戏端口会在
// 面板上裂成两行，与用户在表单里的填法对不上。
func rulesFromPorts(ports map[string][]int) []PortRule {
	tcp := toSet(ports["tcp"])
	udp := toSet(ports["udp"])

	byProto := map[Protocol][]int{}
	for p := range tcp {
		if udp[p] {
			byProto[ProtocolBoth] = append(byProto[ProtocolBoth], p)
		} else {
			byProto[ProtocolTCP] = append(byProto[ProtocolTCP], p)
		}
	}
	for p := range udp {
		if !tcp[p] {
			byProto[ProtocolUDP] = append(byProto[ProtocolUDP], p)
		}
	}

	var rules []PortRule
	// 固定顺序遍历，保证迁移结果可复现（map 遍历顺序随机）
	for _, proto := range []Protocol{ProtocolTCP, ProtocolUDP, ProtocolBoth} {
		list := byProto[proto]
		if len(list) == 0 {
			continue
		}
		sort.Ints(list)
		start, prev := list[0], list[0]
		for _, p := range list[1:] {
			if p == prev+1 {
				prev = p
				continue
			}
			rules = append(rules, PortRule{Start: start, End: prev, Protocol: proto})
			start, prev = p, p
		}
		rules = append(rules, PortRule{Start: start, End: prev, Protocol: proto})
	}
	return rules
}

func toSet(list []int) map[int]bool {
	set := make(map[int]bool, len(list))
	for _, v := range list {
		set[v] = true
	}
	return set
}
