// Package mesh 是管理器侧的「管理器互控」运行时：本机身份、到协调节点的长连接、
// 选路（P1 只有中转）、Peer gRPC 服务与客户端。见 docs/REMOTE_MANAGER_MESH_PLAN.md §5、§12 P1-6。
//
// 未配置时零副作用：不建目录、不发起连接、不监听端口（ErrNotConfigured 短路）。
// 本包不 import webapi——Gin engine 由组合根注入（P3 的隧道才需要）。
package mesh

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"asa-server/pkg/atomicfile"
	"asa-server/pkg/meshid"
	"asa-server/pkg/meshjoin"
)

// ConfigFileName 是 {BaseDir}/mesh/ 下的配置文件。
const ConfigFileName = "config.json"

// ErrNotConfigured 表示本机还没有接入协调节点（或已停用）。是常规状态，不是故障。
var ErrNotConfigured = errors.New("管理器互控尚未配置")

// Config 是 {BaseDir}/mesh/config.json。含网络密钥，文件权限 0600。
// 身份（node.key / node.crt）不在这里：配置可能被导出，私钥不该跟着走（§10.2）。
type Config struct {
	Enabled     bool               `json:"enabled"`
	Label       string             `json:"label,omitempty"`
	Coordinator *CoordinatorConfig `json:"coordinator,omitempty"`
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

// LoadConfig 读取配置。文件不存在、没有协调节点信息、或已停用时返回 ErrNotConfigured
// （同时返回读到的配置，供页面显示备注名等）。
func LoadConfig(dir string) (*Config, error) {
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
	if cfg.Coordinator == nil {
		return &cfg, ErrNotConfigured
	}
	if !cfg.Enabled {
		return &cfg, fmt.Errorf("%w（已停用）", ErrNotConfigured)
	}
	return &cfg, nil
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
// 一连就会把服务的会话踢掉——生效要等服务重启。
func Join(dir, blob string) (*CoordinatorConfig, error) {
	j, err := meshjoin.ParseJoinBlob(blob)
	if err != nil {
		return nil, err
	}
	cfg, err := LoadConfig(dir)
	if err != nil && !errors.Is(err, ErrNotConfigured) {
		return nil, err
	}
	cfg.Coordinator = FromJoinBlob(j)
	cfg.Enabled = true
	if err := SaveConfig(dir, cfg); err != nil {
		return nil, err
	}
	return cfg.Coordinator, nil
}

// Leave 清除协调节点信息并停用。身份保留：以后重新接入时对端还认得本机。
func Leave(dir string) error {
	cfg, err := LoadConfig(dir)
	if err != nil && !errors.Is(err, ErrNotConfigured) {
		return err
	}
	if cfg.Coordinator == nil && !cfg.Enabled {
		return nil
	}
	cfg.Coordinator = nil
	cfg.Enabled = false
	return SaveConfig(dir, cfg)
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
