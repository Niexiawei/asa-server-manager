// Package filesyncmanage 在进程内运行 simple-file-sync 的同步节点，用它把
// {BaseDir}/clusters/<ClusterID> 同步到同一集群的其他机器。
//
// 形态照 frpmanage：结构化配置落 {BaseDir}/filesync/config.json，包级单例 Manager，
// Initialize / Start / Stop / Restart / SetConfig / Status。与 frpmanage 的不同之处
// 在于同步库自带身份：本机 enroll 得到的节点证书落 node.crt/node.key，与配置分开存
// （配置可能被备份、导出，身份私钥不应跟着走）。
// 见 docs/FILESYNC_REPLACE_SYNCTHING_PLAN.md §8 P2。
package filesyncmanage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Niexiawei/simple-file-sync/pkg/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"asa-server/internal/runner"
	"asa-server/pkg/logger"
)

const (
	nodeIDFileName   = "node-id"
	nodeCertFileName = "node.crt"
	nodeKeyFileName  = "node.key"

	// statsReportInterval 让协调端界面能看到本节点的下载进度（库默认不上报）。
	statsReportInterval = 5 * time.Second

	// maxRetryDelay 是暂时性失败后重建节点的退避上限。
	maxRetryDelay = 5 * time.Minute
)

// initialRetryDelay 是暂时性失败后第一次重试的等待时间，之后每次翻倍到 maxRetryDelay。
// 变量而非常量，测试里调短。
var initialRetryDelay = 10 * time.Second

// remoteCommands 是允许协调端对本机下发的远程指令（§10-5）：只开只读的 status
// 与无害的 rescan。request_backfill 有排序约束，list_conflict_copies 会把本机的
// 文件清单暴露给协调端，都不开。
var remoteCommands = []string{client.RemoteCommandStatus, client.RemoteCommandRescan}

// clusterRootConfig 是 clusters 这一种用法的同步参数（§5.3、§6.2）：
//   - 玩家站在方尖碑前等着，默认 10s 的静默期太慢，改成 1s / 5s；
//   - 显式打开 5 分钟一次的全量扫描，兜住 fsnotify 漏掉的事件（库默认关闭）；
//   - 文件是 KB 级且数量少，哈希缓存（每个根一个 SQLite 连接）不划算；
//   - 冲突副本数取库默认值（10 份），排除规则留空——它必须全组一致，不开放配置。
func clusterRootConfig(baseDir, clusterID string) client.RootConfig {
	return client.RootConfig{
		GroupID:      groupID(clusterID),
		Path:         clusterDir(baseDir, clusterID),
		WatchDelay:   time.Second,
		WatchTimeout: 5 * time.Second,
		ScanInterval: 5 * time.Minute,
	}
}

// Manager 管理进程内唯一的同步节点。
type Manager struct {
	baseDir string // {BaseDir}
	runDir  string // {BaseDir}/filesync

	mu      sync.Mutex
	cfg     *Config
	node    *client.Node
	roots   map[string]*client.Root // key: ClusterID
	rootErr map[string]string       // AddRoot 失败的集群及原因
	cancel  context.CancelFunc
	// generation 在每次启动与关闭时加一（只在持锁时改）。节点的 Run 协程退出、
	// 事件回调都拿它比对，已被 Stop/Restart 换掉的旧节点就不会再改动状态。
	// 用原子量是因为事件回调在同步库的协程里、不持锁读它：回调若要抢锁，
	// 而 Shutdown 恰好在持锁等待，就会互相卡住。
	generation atomic.Int64
	// failure 是节点停下的原因。终态错误（鉴权失败、node_id 冲突、凭据无法解析……）
	// 是配置问题，不重试；暂时性错误见 retry。
	failure error
	// retry 非 nil 表示节点因暂时性错误停下、已安排了重建（见 onRunExit）。
	retry      *time.Timer
	retryDelay time.Duration
}

var globalManager *Manager

// Initialize 准备 {BaseDir}/filesync 目录并读入配置。
func Initialize(baseDir string) (*Manager, error) {
	dir := filepath.Join(baseDir, "filesync")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建文件同步目录失败: %w", err)
	}
	m := &Manager{baseDir: baseDir, runDir: dir}
	if cfg, err := LoadConfig(dir); err != nil {
		if !errors.Is(err, ErrNotConfigured) {
			logger.Warnf("读取文件同步配置失败: %v", err)
		}
	} else {
		m.cfg = cfg
	}
	globalManager = m
	return m, nil
}

// GetGlobalManager 返回 Initialize 建好的单例；未初始化时为 nil。
func GetGlobalManager() *Manager { return globalManager }

// Start 按当前配置启动同步节点。
//
// 未配置返回 ErrNotConfigured（调用方记 INFO 即可）；配置里关闭了同步则什么也不做。
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.node != nil {
		return errors.New("文件同步已在运行")
	}
	cfg, err := LoadConfig(m.runDir)
	if err != nil {
		return err
	}
	m.cfg = cfg
	if !cfg.Enabled {
		return nil
	}
	return m.startLocked(cfg)
}

// startLocked 建节点、挂同步根、起 Run 协程。
func (m *Manager) startLocked(cfg *Config) error {
	m.cancelRetryLocked()
	m.failure = nil
	if err := cfg.Validate(); err != nil {
		m.failure = err
		return fmt.Errorf("文件同步配置无效: %w", err)
	}

	generation := m.generation.Add(1)
	clientCfg := client.Config{
		Address:                      cfg.Address,
		Label:                        cfg.Label,
		CAPEM:                        []byte(cfg.CAPEM),
		Identity:                     client.NewFileIdentityStore(filepath.Join(m.runDir, nodeIDFileName)),
		NodeCertificates:             client.NewFileNodeCertificateStore(m.nodeCertPath(), m.nodeKeyPath()),
		Logger:                       newLogger(),
		OnEvent:                      func(e client.Event) { m.onEvent(generation, e) },
		StatsReportInterval:          statsReportInterval,
		RemoteCommands:               remoteCommands,
		UploadRateLimitBytesPerSec:   cfg.UploadLimitKBps * 1024,
		DownloadRateLimitBytesPerSec: cfg.DownloadLimitKBps * 1024,
	}
	if cfg.hasBootstrap() {
		clientCfg.CertPEM, clientCfg.KeyPEM = []byte(cfg.BootstrapCertPEM), []byte(cfg.BootstrapKeyPEM)
	}
	node, err := client.New(clientCfg, nil)
	if err != nil {
		// 最常见的是：还没接入过（没有节点证书）却也没给引导凭据。
		if !cfg.hasBootstrap() && !m.hasNodeCertificate() {
			err = fmt.Errorf("本机尚未接入集群，需要引导凭据（接入字符串或证书文件）: %w", err)
		}
		m.failure = err
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.node, m.cancel = node, cancel
	m.roots, m.rootErr = make(map[string]*client.Root), make(map[string]string)
	for _, clusterID := range cfg.clusterIDs() {
		m.addRootLocked(ctx, clusterID)
	}
	go func() {
		err := node.Run(ctx)
		m.onRunExit(generation, err)
	}()
	logger.Infof("%s文件同步已启动：协调端 %s，集群 %s", logPrefix, cfg.Address, strings.Join(cfg.clusterIDs(), ", "))
	return nil
}

// addRootLocked 准备集群目录并挂上同步根。失败只记在 rootErr 里，不影响其他集群。
func (m *Manager) addRootLocked(ctx context.Context, clusterID string) {
	dir := clusterDir(m.baseDir, clusterID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.rootErr[clusterID] = fmt.Sprintf("创建集群目录失败: %v", err)
		return
	}
	// 与实例启动时的处理相同（internal/instance/server.go）：clusters 整棵交给
	// 降权运行时用户，否则游戏写不进它。Windows 上是空操作。
	if err := runner.ChownTreeForRuntime(filepath.Dir(dir)); err != nil {
		logger.Warnf("%s为运行时用户准备集群目录失败: %v", logPrefix, err)
	}
	root, err := m.node.AddRoot(ctx, clusterRootConfig(m.baseDir, clusterID))
	if err != nil {
		m.rootErr[clusterID] = err.Error()
		logger.Warnf("%s集群 %s 挂载失败: %v", logPrefix, clusterID, err)
		return
	}
	m.roots[clusterID] = root
	delete(m.rootErr, clusterID)
}

// onRunExit 处理 Run 协程的返回。Run 在 ctx 取消（正常停止）或出错时返回。
//
// 出错分两类。终态错误（证书被拒、node_id 冲突）是配置问题，停下等人处理。
// 暂时性错误——典型的是**还没接入过的机器在首次 Enroll 时连不上协调端**：
// 同步库只对已接入节点的连接做退避重连，接入这一步失败就直接从 Run 返回——
// 本程序开机时网络或协调端恰好不通，不能因此让同步永远停着，所以按退避重建节点。
func (m *Manager) onRunExit(generation int64, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if generation != m.generation.Load() || m.node == nil {
		return // 已被 Stop/Restart 换掉
	}
	m.shutdownLocked()
	if err == nil {
		return
	}
	if !retryable(err) {
		m.failure = err
		logger.Errorf("%s同步节点已停止: %v", logPrefix, err)
		return
	}
	m.retryDelay = min(max(m.retryDelay*2, initialRetryDelay), maxRetryDelay)
	delay := m.retryDelay
	m.failure = fmt.Errorf("暂时连不上协调端，%s 后重试: %w", delay, err)
	logger.Warnf("%s%v", logPrefix, m.failure)
	scheduled := m.generation.Load()
	m.retry = time.AfterFunc(delay, func() { m.retryStart(scheduled) })
}

// retryable 报告 Run 的错误是否值得重试：只认网络层面的暂时性失败。
func retryable(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	}
	return false
}

// retryStart 是退避到期后的重建。期间有人 Stop/Restart/改配置时，generation 已变，放弃。
func (m *Manager) retryStart(scheduled int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if scheduled != m.generation.Load() || m.retry == nil || m.node != nil {
		return
	}
	m.retry = nil
	if m.cfg == nil || !m.cfg.Enabled {
		return
	}
	delay := m.retryDelay // startLocked 会清掉它，重试路径要保留退避进度
	if err := m.startLocked(m.cfg); err != nil {
		logger.Warnf("%s重试启动失败: %v", logPrefix, err)
	}
	m.retryDelay = delay
}

// cancelRetryLocked 取消尚未触发的重建，并重置退避。
func (m *Manager) cancelRetryLocked() {
	if m.retry != nil {
		m.retry.Stop()
		m.retry = nil
	}
	m.retryDelay = 0
}

// shutdownLocked 关掉当前节点，容忍"本来就没在跑"。库约定 Shutdown 之后节点不可复用，
// 再启动一律新建。
func (m *Manager) shutdownLocked() {
	if m.node == nil {
		return
	}
	m.generation.Add(1) // 让旧节点的 Run 退出与事件回调失效
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if err := m.node.Shutdown(); err != nil {
		logger.Warnf("%s关闭同步节点时出错: %v", logPrefix, err)
	}
	m.node, m.roots = nil, nil
}

// Stop 停止同步节点。这是运行期操作；要让重启本程序后也不自动启动，把配置里的 Enabled 关掉。
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pendingRetry := m.retry != nil
	m.cancelRetryLocked()
	if m.node == nil {
		if pendingRetry {
			m.failure = nil
			logger.Infof("%s已取消待重试的文件同步", logPrefix)
			return nil
		}
		return errors.New("文件同步未在运行")
	}
	m.shutdownLocked()
	logger.Infof("%s文件同步已停止", logPrefix)
	return nil
}

// Restart 按当前配置重新建立节点。
func (m *Manager) Restart() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelRetryLocked()
	m.shutdownLocked()
	if m.cfg == nil {
		return ErrNotConfigured
	}
	if !m.cfg.Enabled {
		return errors.New("文件同步已在配置中关闭")
	}
	return m.startLocked(m.cfg)
}

// Config 返回当前配置的副本，未配置时为 nil。
func (m *Manager) Config() *Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg == nil {
		return nil
	}
	return m.cfg.clone()
}

// SetConfig 校验、落盘并让新配置生效。
//
// 生效方式分两条路（对照 frpmanage 的 UpdateConfigSource）：
//   - 连接层面的字段变了（地址、凭据、限速、开关）→ 只能重建节点；
//   - 只有集群列表变了 → 对差集热增删同步根，已建立的连接与在途传输不受影响。
//
// 节点没在跑、而配置是开启的，就按新配置启动：用户改好凭据点保存，期望的是它开始工作。
func (m *Manager) SetConfig(next *Config) error {
	if next == nil {
		return errors.New("配置不能为空")
	}
	next.Normalize()
	if err := next.Validate(); err != nil {
		return err
	}
	if err := SaveConfig(m.runDir, next); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	prev := m.cfg
	m.cfg = next.clone()

	switch {
	case !next.Enabled:
		m.cancelRetryLocked()
		m.shutdownLocked()
		m.failure = nil
		return nil
	case m.node == nil || next.connectionChanged(prev):
		m.shutdownLocked()
		return m.startLocked(m.cfg)
	}

	ctx := context.Background()
	for clusterID := range m.roots {
		if !slices.Contains(next.clusterIDs(), clusterID) {
			if err := m.node.RemoveRoot(ctx, groupID(clusterID)); err != nil {
				logger.Warnf("%s移除集群 %s 失败: %v", logPrefix, clusterID, err)
			}
			delete(m.roots, clusterID)
		}
	}
	for clusterID := range m.rootErr {
		if !slices.Contains(next.clusterIDs(), clusterID) {
			delete(m.rootErr, clusterID)
		}
	}
	for _, clusterID := range next.clusterIDs() {
		if _, ok := m.roots[clusterID]; !ok {
			m.addRootLocked(ctx, clusterID)
		}
	}
	return nil
}

// ResetIdentity 删掉本机的节点证书，下次启动用引导凭据重新接入。
//
// 用于换机、或怀疑节点私钥泄露。协调端那边要先对这台机器执行 `node reset`，
// 否则它会以"该 node_id 已绑定其他证书"拒绝这次接入——这是同步库有意不留的
// 身份抢占路径。node-id 保留不变。
func (m *Manager) ResetIdentity() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg == nil {
		return ErrNotConfigured
	}
	if !m.cfg.hasBootstrap() {
		return errors.New("重置身份后需要用引导凭据重新接入，请先提供接入字符串或证书文件")
	}
	m.cancelRetryLocked()
	m.shutdownLocked()
	for _, path := range []string{m.nodeCertPath(), m.nodeKeyPath()} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("删除 %s 失败: %w", path, err)
		}
	}
	logger.Warnf("%s已删除本机节点证书，将用引导凭据重新接入", logPrefix)
	if !m.cfg.Enabled {
		return nil
	}
	return m.startLocked(m.cfg)
}

func (m *Manager) nodeCertPath() string { return filepath.Join(m.runDir, nodeCertFileName) }
func (m *Manager) nodeKeyPath() string  { return filepath.Join(m.runDir, nodeKeyFileName) }

func (m *Manager) hasNodeCertificate() bool {
	_, err := os.Stat(m.nodeCertPath())
	return err == nil
}

// nodeID 读持久化的 node id（节点没在跑时也能展示）。
func (m *Manager) nodeID() string {
	data, err := os.ReadFile(filepath.Join(m.runDir, nodeIDFileName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
