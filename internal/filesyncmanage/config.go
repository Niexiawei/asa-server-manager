package filesyncmanage

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Niexiawei/simple-file-sync/pkg/joinblob"
)

// configFileName 是文件同步配置的唯一真相，落在 {BaseDir}/filesync/ 下。
// 与 frp/frpc.json 同理用明文 JSON：用户填的配置一律是可读文本，能备份、能手工放回。
// 里面有引导私钥，所以是 0600。
const configFileName = "config.json"

// ErrNotConfigured 表示还没有配置过文件同步（config.json 不存在）。
// 自动启动路径靠它把「用户根本没配」与「配了但起不来」区分开，前者只记 INFO。
var ErrNotConfigured = errors.New("文件同步尚未配置")

// Config 是文件同步的结构化配置。见 docs/FILESYNC_REPLACE_SYNCTHING_PLAN.md §8 P2-1。
//
// 同步参数（静默期、扫描间隔、排除规则……）刻意不在这里：它们是 clusters 这一种
// 用法的推荐值，写死在 clusterRootConfig 里。排除规则尤其必须全组一致，
// 让用户逐台配只会制造"一台拒收、任务进死信"的故障（同上 §6.2）。
type Config struct {
	// Enabled 为 false 时 Start 不连协调端，但配置保留。
	Enabled bool `json:"enabled"`
	// Address 是协调端的 host:port。
	Address string `json:"address"`
	// Label 只是给协调端界面看的显示名，不参与鉴权。
	Label string `json:"label,omitempty"`

	// 引导凭据：只用于首次接入换取节点证书（之后节点用自己的证书连接）。
	// 两种填法——粘贴 join blob、上传三个文件——在 API 层都落到这三个字段上。
	CAPEM            string `json:"ca_pem,omitempty"`
	BootstrapCertPEM string `json:"bootstrap_cert_pem,omitempty"`
	BootstrapKeyPEM  string `json:"bootstrap_key_pem,omitempty"`

	// UploadLimitKBps / DownloadLimitKBps 为 0 表示不限。同一台机器还在跑游戏服务器。
	UploadLimitKBps   int64 `json:"upload_limit_kbps,omitempty"`
	DownloadLimitKBps int64 `json:"download_limit_kbps,omitempty"`

	// Clusters 是要同步的集群。路径与同步组都由 ClusterID 推出，用户不填路径。
	Clusters []ClusterRoot `json:"clusters"`
}

// ClusterRoot 是一个要同步的集群传输目录。
type ClusterRoot struct {
	ClusterID string `json:"cluster_id"`
}

// groupID 是集群在同步库里的同步组名：同一个 ClusterID 的机器互相同步。
func groupID(clusterID string) string { return "cluster-" + clusterID }

// clusterDir 是实例启动时经 -ClusterDirOverride 交给游戏的那个目录
// （internal/instance/server.go），同机多实例本来就共用它。
func clusterDir(baseDir, clusterID string) string {
	return filepath.Join(baseDir, "clusters", clusterID)
}

// Normalize 去掉首尾空白，并按原顺序去重 Clusters。
func (c *Config) Normalize() {
	c.Address = strings.TrimSpace(c.Address)
	c.Label = strings.TrimSpace(c.Label)
	seen := make(map[string]bool, len(c.Clusters))
	clusters := make([]ClusterRoot, 0, len(c.Clusters))
	for _, cluster := range c.Clusters {
		cluster.ClusterID = strings.TrimSpace(cluster.ClusterID)
		if cluster.ClusterID == "" || seen[cluster.ClusterID] {
			continue
		}
		seen[cluster.ClusterID] = true
		clusters = append(clusters, cluster)
	}
	c.Clusters = clusters
}

// hasBootstrap 报告三份引导凭据是否都在。
func (c *Config) hasBootstrap() bool {
	return c.CAPEM != "" && c.BootstrapCertPEM != "" && c.BootstrapKeyPEM != ""
}

// Validate 校验配置。
//
// 引导凭据用 joinblob.Describe 校验：两种填法由此得到完全相同的检查与报错
// （地址格式、私钥与证书配对、证书由该 CA 签发、未过期）。
//
// 已经接入过的机器可以不带引导凭据（接入成功后它就没用了），只要有 CA——
// 节点证书的握手也要用 CA 验证协调端。所以这里只要求 CA 必填，
// 引导证书与私钥要么都有、要么都没有。
func (c *Config) Validate() error {
	if c.Address == "" {
		return errors.New("协调端地址不能为空")
	}
	// 没有引导凭据时 joinblob.Describe 不会运行，地址格式得在这里单独查。
	if host, port, err := net.SplitHostPort(c.Address); err != nil || host == "" || port == "" {
		return fmt.Errorf("协调端地址 %q 应为 主机:端口", c.Address)
	}
	if c.UploadLimitKBps < 0 || c.DownloadLimitKBps < 0 {
		return errors.New("限速不能为负数（0 表示不限）")
	}
	for _, cluster := range c.Clusters {
		if err := validateClusterID(cluster.ClusterID); err != nil {
			return err
		}
	}
	if c.CAPEM == "" {
		return errors.New("缺少集群 CA 证书")
	}
	if (c.BootstrapCertPEM == "") != (c.BootstrapKeyPEM == "") {
		return errors.New("引导证书与私钥必须同时提供")
	}
	if !c.hasBootstrap() {
		return nil
	}
	if _, err := joinblob.Describe(c.bootstrapInfo()); err != nil {
		return describeCredentialError(err)
	}
	return nil
}

func (c *Config) bootstrapInfo() joinblob.Info {
	return joinblob.Info{
		Address: c.Address,
		CAPEM:   []byte(c.CAPEM), CertPEM: []byte(c.BootstrapCertPEM), KeyPEM: []byte(c.BootstrapKeyPEM),
	}
}

// bootstrapSummary 返回引导证书的到期时间（接入前在状态里展示）。
func (c *Config) bootstrapSummary() (time.Time, error) {
	summary, err := joinblob.Describe(c.bootstrapInfo())
	if err != nil {
		return time.Time{}, err
	}
	return summary.CertNotAfter, nil
}

// describeCredentialError 把 joinblob 的三类错误翻成给用户看的话。
func describeCredentialError(err error) error {
	switch {
	case errors.Is(err, joinblob.ErrUnsupportedVersion):
		return fmt.Errorf("接入字符串来自更新版本的协调端，请升级本程序: %w", err)
	case errors.Is(err, joinblob.ErrInvalidCredential):
		return fmt.Errorf("引导凭据不可用（已过期、私钥与证书不配对，或不是这个 CA 签发的），请在协调端重新生成: %w", err)
	default:
		return fmt.Errorf("接入信息不完整或已损坏，请重新复制: %w", err)
	}
}

// validateClusterID 拒绝会让 clusters/<id> 逃出 clusters 目录、或在某个平台上建不出来的名字。
func validateClusterID(id string) error {
	if id == "" {
		return errors.New("集群 ID 不能为空")
	}
	if id == "." || id == ".." || strings.ContainsAny(id, `/\:*?"<>|`) {
		return fmt.Errorf("集群 ID %q 含有不能用作目录名的字符", id)
	}
	return nil
}

// connectionChanged 报告 next 相对 prev 是否改了"连接"层面的东西——这些只能重建节点才生效；
// 只改 Clusters 可以热增删同步根。
func (c *Config) connectionChanged(prev *Config) bool {
	if prev == nil {
		return true
	}
	return c.Enabled != prev.Enabled || c.Address != prev.Address || c.Label != prev.Label ||
		c.CAPEM != prev.CAPEM || c.BootstrapCertPEM != prev.BootstrapCertPEM || c.BootstrapKeyPEM != prev.BootstrapKeyPEM ||
		c.UploadLimitKBps != prev.UploadLimitKBps || c.DownloadLimitKBps != prev.DownloadLimitKBps
}

// clusterIDs 返回配置里的集群 ID 列表（保持顺序）。
func (c *Config) clusterIDs() []string {
	ids := make([]string, 0, len(c.Clusters))
	for _, cluster := range c.Clusters {
		ids = append(ids, cluster.ClusterID)
	}
	return ids
}

func (c *Config) clone() *Config {
	clone := *c
	clone.Clusters = slices.Clone(c.Clusters)
	return &clone
}

// ---------------------------------------------------------------------------
// 落盘
// ---------------------------------------------------------------------------

func configPath(dir string) string { return filepath.Join(dir, configFileName) }

// LoadConfig 读取 dir 下的 config.json。文件不存在返回 ErrNotConfigured。
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

// SaveConfig 原子写入 config.json（临时文件 + rename），权限 0600。
func SaveConfig(dir string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 %s 失败: %w", configFileName, err)
	}
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
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("设置 %s 权限失败: %w", configFileName, err)
	}
	if err := os.Rename(tmpName, configPath(dir)); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("替换 %s 失败: %w", configPath(dir), err)
	}
	return nil
}
