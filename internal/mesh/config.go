// Package mesh 是管理器侧的「管理器互控」运行时：本机身份、到协调节点的长连接、
// 选路（直连优先、中转兜底）、Peer gRPC 服务与客户端、配对与授权表、HTTP 隧道两端。
// 见 docs/REMOTE_MANAGER_MESH_PLAN.md §5～§7、§12 P1-6、P2、P3。
//
// 未配置时零副作用：不建目录、不发起连接、不监听端口（ErrNotConfigured 短路）。
// 本包不 import webapi——Gin engine 由组合根注入（SetHTTPHandler）。
package mesh

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"

	"asa-server/pkg/atomicfile"
	"asa-server/pkg/meshid"
	"asa-server/pkg/meshjoin"
)

// ConfigFileName 是 {BaseDir}/mesh/ 下的配置文件。
const ConfigFileName = "config.json"

// ErrNotConfigured 表示本机还没有接入协调节点（或已停用）。是常规状态，不是故障。
var ErrNotConfigured = errors.New("管理器互控尚未配置")

// DefaultPeerPort 是 Peer 端口的默认值（D7：默认监听，便于内网直连）。
const DefaultPeerPort = 19194

// 角色。与 internal/auth 的角色同名同义；本包不 import auth，由 authapi 负责对应。
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
)

// Config 是 {BaseDir}/mesh/config.json。含网络密钥，文件权限 0600。
// 身份（node.key / node.crt）不在这里：配置可能被导出，私钥不该跟着走（§10.2）。
type Config struct {
	Enabled bool   `json:"enabled"`
	Label   string `json:"label,omitempty"`
	// Coordinator 为空 = 无协调节点模式：只监听 Peer 端口、只按对端的手填地址直连（§12 P2-2）。
	Coordinator *CoordinatorConfig `json:"coordinator,omitempty"`
	// PeerPort 为 0 = DefaultPeerPort。
	PeerPort int `json:"peer_port,omitempty"`
	// NoListen 为 true 时不监听 Peer 端口：只能出、不能进（别人只能经中转连本机）。
	NoListen bool `json:"no_listen,omitempty"`
	// PublicAddrs 是手填的本机公网地址（端口映射 / DDNS），作为 CONFIGURED 候选上报。
	PublicAddrs []string `json:"public_addrs,omitempty"`
	// ControlRole 是本机上能使用远程控制的最低角色（D5），空 = admin。
	ControlRole string `json:"control_role,omitempty"`
	// UDPPort 是打洞用的 UDP 端口（§12 P6-1），0 = 与 Peer 端口同号。
	UDPPort int `json:"udp_port,omitempty"`
	// NoPunch 为 true 时不打洞：不开 UDP 端口，对端连本机只走 TCP 直连或中转。
	NoPunch bool `json:"no_punch,omitempty"`
}

// ListenPort 返回 Peer 端口。
func (c *Config) ListenPort() int {
	if c.PeerPort == 0 {
		return DefaultPeerPort
	}
	return c.PeerPort
}

// UDPListenPort 返回打洞用的 UDP 端口。
func (c *Config) UDPListenPort() int {
	if c.UDPPort == 0 {
		return c.ListenPort()
	}
	return c.UDPPort
}

// EffectiveControlRole 返回本机上能使用远程控制的最低角色。
func (c *Config) EffectiveControlRole() string {
	if c.ControlRole == "" {
		return RoleAdmin
	}
	return c.ControlRole
}

// Validate 检查各字段的取值。
func (c *Config) Validate() error {
	if c.PeerPort < 0 || c.PeerPort > 65535 {
		return fmt.Errorf("Peer 端口必须在 1-65535 之间，当前为 %d", c.PeerPort)
	}
	if c.UDPPort < 0 || c.UDPPort > 65535 {
		return fmt.Errorf("UDP 端口必须在 1-65535 之间，当前为 %d", c.UDPPort)
	}
	switch c.ControlRole {
	case "", RoleAdmin, RoleOperator:
	default:
		return fmt.Errorf("control_role 只能是 %s 或 %s", RoleAdmin, RoleOperator)
	}
	for _, a := range c.PublicAddrs {
		if _, port, err := net.SplitHostPort(a); err != nil || port == "" {
			return fmt.Errorf("公网地址 %q 不是 host:port", a)
		}
	}
	return nil
}

// CoordinatorConfig 来自 join blob。
type CoordinatorConfig struct {
	Addr          string    `json:"addr"`
	ID            meshid.ID `json:"id"`
	NetworkID     string    `json:"network_id"`
	NetworkSecret string    `json:"network_secret"`
}

// FromJoinBlob 由 join blob 生成协调节点配置。
func FromJoinBlob(j meshjoin.JoinBlob) *CoordinatorConfig {
	return &CoordinatorConfig{Addr: j.Addr, ID: j.Coordinator, NetworkID: j.NetworkID, NetworkSecret: j.NetworkSecret}
}

// Dir 返回 {baseDir}/mesh。
func Dir(baseDir string) string { return filepath.Join(baseDir, "mesh") }

// LoadConfig 读取配置。文件不存在或已停用时返回 ErrNotConfigured（同时返回读到的配置，
// 供页面显示备注名等）。没有协调节点但已启用是合法的（无协调节点模式）。
func LoadConfig(dir string) (*Config, error) {
	cfg, err := readConfig(dir)
	if err != nil {
		return cfg, err
	}
	if !cfg.Enabled {
		return cfg, fmt.Errorf("%w（已停用）", ErrNotConfigured)
	}
	return cfg, nil
}

// readConfig 只读文件，不看是否启用。文件不存在时返回空配置与 ErrNotConfigured。
func readConfig(dir string) (*Config, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, ErrNotConfigured
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", ConfigFileName, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", ConfigFileName, err)
	}
	return &cfg, nil
}

// editConfig 读出配置（不存在时从空配置开始）、交给 fn 修改、校验后写回。
func editConfig(dir string, fn func(*Config) error) (*Config, error) {
	cfg, err := readConfig(dir)
	if err != nil && !errors.Is(err, ErrNotConfigured) {
		return nil, err
	}
	if err := fn(cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := SaveConfig(dir, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// SaveConfig 原子写入配置（0600）。
func SaveConfig(dir string, cfg *Config) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, ConfigFileName), append(raw, '\n'), 0o600)
}

// Join 校验 join blob 并写入配置（启用）。不连协调节点：CLI 与正在运行的服务共用同一个身份，
// 一连就会把服务的会话踢掉——CLI 写完要重启服务才生效，页面则写完立即热应用。
func Join(dir, blob string) (*CoordinatorConfig, error) {
	return writeCoordinator(dir, blob, true)
}

// SetCoordinator 校验 join blob 并只写入协调节点信息，**不改启用开关**（页面用：协调节点只是一项配置，
// mesh 的启停由页面顶部的启动 / 停止单独控制）。
func SetCoordinator(dir, blob string) (*CoordinatorConfig, error) {
	return writeCoordinator(dir, blob, false)
}

func writeCoordinator(dir, blob string, enable bool) (*CoordinatorConfig, error) {
	j, err := meshjoin.ParseJoinBlob(blob)
	if err != nil {
		return nil, err
	}
	cfg, err := editConfig(dir, func(c *Config) error {
		c.Coordinator = FromJoinBlob(j)
		if enable {
			c.Enabled = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cfg.Coordinator, nil
}

// Leave 清除协调节点信息并停用。身份与配对关系保留：以后重新接入时对端还认得本机。
// 只想去掉协调节点、继续用直连，就在 Leave 之后 Enable（无协调节点模式）。
func Leave(dir string) error {
	cfg, err := readConfig(dir)
	if errors.Is(err, ErrNotConfigured) && !cfg.Enabled && cfg.Coordinator == nil {
		return nil
	}
	_, err = editConfig(dir, func(c *Config) error {
		c.Coordinator = nil
		c.Enabled = false
		return nil
	})
	return err
}

// SetEnabled 只拨开关。启用但没有协调节点 = 无协调节点模式。
func SetEnabled(dir string, enabled bool) (*Config, error) {
	return editConfig(dir, func(c *Config) error {
		c.Enabled = enabled
		return nil
	})
}

// ConfigPatch 是 PUT /api/mesh/config 的请求体：只改出现的字段。
type ConfigPatch struct {
	Label       *string   `json:"label"`
	PeerPort    *int      `json:"peer_port"`
	NoListen    *bool     `json:"no_listen"`
	PublicAddrs *[]string `json:"public_addrs"`
	ControlRole *string   `json:"control_role"`
	UDPPort     *int      `json:"udp_port"`
	NoPunch     *bool     `json:"no_punch"`
}

// UpdateConfig 按 patch 修改配置。
func UpdateConfig(dir string, p ConfigPatch) (*Config, error) {
	return editConfig(dir, func(c *Config) error {
		if p.Label != nil {
			c.Label = strings.TrimSpace(*p.Label)
		}
		if p.PeerPort != nil {
			c.PeerPort = *p.PeerPort
		}
		if p.NoListen != nil {
			c.NoListen = *p.NoListen
		}
		if p.PublicAddrs != nil {
			c.PublicAddrs = append([]string(nil), *p.PublicAddrs...)
		}
		if p.ControlRole != nil {
			c.ControlRole = *p.ControlRole
		}
		if p.UDPPort != nil {
			c.UDPPort = *p.UDPPort
		}
		if p.NoPunch != nil {
			c.NoPunch = *p.NoPunch
		}
		return nil
	})
}

// LoadIdentity 读取本机身份；没有就生成。
func LoadIdentity(dir string) (meshid.ID, error) {
	_, id, err := meshid.LoadOrCreate(dir)
	return id, err
}

// ExistingIdentity 只读：本机还没有身份时返回零值与 false，不生成（GET 接口用它，不该有副作用）。
func ExistingIdentity(dir string) (meshid.ID, bool) {
	if _, err := os.Stat(filepath.Join(dir, meshid.KeyFileName)); err != nil {
		return meshid.ID{}, false
	}
	_, id, err := meshid.LoadOrCreate(dir)
	if err != nil {
		return meshid.ID{}, false
	}
	return id, true
}
