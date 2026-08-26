package frpmanage

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"asa-server/pkg/logger"

	"github.com/fatedier/frp/client"
	"github.com/fatedier/frp/pkg/config"
	"github.com/fatedier/frp/pkg/config/source"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	frplog "github.com/fatedier/frp/pkg/util/log"
	golibLog "github.com/fatedier/golib/log"
)

// FrpcManager manages the frpc client lifecycle in-process, via
// github.com/fatedier/frp/client — no more separate frpc.exe (see
// docs/LINUX_COMPATIBILITY_PLAN.md §5.10). This removes the last reason this
// package needed platform-specific binaries.
type FrpcManager struct {
	runDir string

	mu       sync.Mutex
	svr      *client.Service
	running  bool
	startErr error // Last start error
}

const (
	frpcConfigFileName = "frpc.toml"
)

var globalManager *FrpcManager
var frpConfigDir string // FRP 配置文件目录

// Initialize sets up the frp config directory. There is no binary to
// extract anymore — frpc now runs in-process.
func Initialize(basedir string) (string, error) {
	dir := filepath.Join(basedir, "frp")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create frp directory: %v", err)
	}

	// frp 有一个包级全局 logger（pkg/util/log.Logger）。绝不能调 frplog.InitLogger——
	// 它会按 frpc.toml 的 log.to 抢 stdout 或自己开一份轮转文件。正确做法是自己
	// New 一个写进 asaServer.log 的 Logger 塞进去。
	frplog.Logger = golibLog.New(golibLog.WithOutput(&LogWriter{tag: "[frpc]", logFunc: logger.Infof}))

	frpConfigDir = dir
	globalManager = &FrpcManager{runDir: dir}

	return dir, nil
}

// Start starts the frpc client asynchronously.
func (m *FrpcManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("frpc is already running")
	}

	configPath := filepath.Join(m.runDir, frpcConfigFileName)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := createDefaultFRPConfig(configPath); err != nil {
			return fmt.Errorf("failed to create default frpc config: %v", err)
		}
	}

	svr, err := buildService(configPath)
	if err != nil {
		m.startErr = err
		return fmt.Errorf("failed to build frp service: %w", err)
	}

	m.svr = svr
	m.startErr = nil
	m.running = true

	go m.asyncRun(svr)

	return nil
}

// buildService loads configPath and constructs a ready-to-run client.Service.
// Mirrors cmd/frpc/sub/root.go's runClientWithAggregator, verified against
// frp v0.71.0 (docs/LINUX_COMPATIBILITY_PLAN.md §5.10.2).
func buildService(configPath string) (*client.Service, error) {
	result, err := config.LoadClientConfigResult(configPath, false)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	configSource := source.NewConfigSource()
	if err := configSource.ReplaceAll(result.Proxies, result.Visitors); err != nil {
		return nil, fmt.Errorf("load proxies/visitors: %w", err)
	}
	aggregator := source.NewAggregator(configSource)

	proxyCfgs, visitorCfgs, err := aggregator.Load()
	if err != nil {
		return nil, fmt.Errorf("aggregate config: %w", err)
	}
	proxyCfgs, visitorCfgs = config.FilterClientConfigurers(result.Common, proxyCfgs, visitorCfgs)
	proxyCfgs = config.CompleteProxyConfigurers(proxyCfgs)
	visitorCfgs = config.CompleteVisitorConfigurers(visitorCfgs)

	if warn, err := validation.ValidateAllClientConfig(result.Common, proxyCfgs, visitorCfgs, nil); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	} else if warn != nil {
		logger.Warnf("[frpc] %v", warn)
	}

	return client.NewService(client.ServiceOptions{
		Common:                 result.Common,
		ConfigSourceAggregator: aggregator, // 必填，为空 NewService 直接报错
		ConfigFilePath:         configPath,
	})
}

// asyncRun runs svr and keeps m.running / m.startErr in sync with it for as
// long as it stays the manager's current service.
//
// svr.Run blocks until stopped. It returns nil on a normal Close/
// GracefulClose, and a non-nil error only when the initial login to frps
// fails (loginFailExit=true in the default config) — that error is the
// deterministic replacement for the old code's 500ms "did the process
// immediately exit" guess (see docs/LINUX_COMPATIBILITY_PLAN.md §5.10.4 #7).
func (m *FrpcManager) asyncRun(svr *client.Service) {
	err := svr.Run(context.Background())

	m.mu.Lock()
	defer m.mu.Unlock()
	// Only touch state if svr is still the current service — a Restart may
	// already have replaced it with a newer one by the time this returns.
	if m.svr != svr {
		return
	}
	m.running = false
	if err != nil {
		m.startErr = fmt.Errorf("frpc exited: %w", err)
		logger.Errorf("frpc exited with error: %v", err)
	} else {
		logger.Infof("frpc exited")
	}
}

// Stop stops the frpc client.
func (m *FrpcManager) Stop() error {
	logger.Infof("frp stoping ...")
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running || m.svr == nil {
		return fmt.Errorf("frpc is not running")
	}

	m.svr.GracefulClose(2 * time.Second)
	m.running = false
	logger.Infof("frp stoped")
	return nil
}

// Restart restarts the frpc client.
func (m *FrpcManager) Restart() error {
	if err := m.Stop(); err != nil {
		// If not running, just start it
		if m.IsRunning() {
			return err
		}
	}

	// Wait a bit before restarting
	time.Sleep(500 * time.Millisecond)

	return m.Start()
}

// IsRunning checks if frpc is running
func (m *FrpcManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// GetStartErr returns the last start error
func (m *FrpcManager) GetStartErr() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startErr
}

// CheckStatus checks the actual running status of the frpc client.
func (m *FrpcManager) CheckStatus() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// Cleanup removes the frp config directory.
func (m *FrpcManager) Cleanup() error {
	// C2 fix: Call Stop() before acquiring lock to avoid deadlock
	// (Stop() acquires mu internally, so we must not hold it here)
	if err := m.Stop(); err != nil {
		// M11 fix: Log warning but continue cleanup even if Stop fails
		logger.Warnf("frpc Cleanup: Stop failed (continuing cleanup): %v", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.runDir != "" {
		if err := os.RemoveAll(m.runDir); err != nil {
			return fmt.Errorf("failed to cleanup temp directory: %v", err)
		}
	}

	logger.Infof(" frpc manager cleanup ...")

	return nil
}

// GetGlobalManager returns the global frpc manager instance
func GetGlobalManager() *FrpcManager {
	return globalManager
}

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// LogWriter is an adapter that writes log messages to logger
type LogWriter struct {
	tag     string
	logFunc func(string, ...interface{})
}

func (w *LogWriter) Write(p []byte) (n int, err error) {
	text := string(p)
	// Remove ANSI color codes
	text = ansiRegex.ReplaceAllString(text, "")

	// Split by newline and log each line
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			w.logFunc("%s %s", w.tag, line)
		}
	}
	return len(p), nil
}

var _ io.Writer = (*LogWriter)(nil)

// createDefaultFRPConfig creates a default frpc configuration file
func createDefaultFRPConfig(configPath string) error {
	defaultConfig := `# FRP Client Configuration
# Generated automatically if not present
serverAddr = "127.0.0.1"
serverPort = 7000
auth.token = "your-token-here"

loginFailExit = true
protocol = "tcp"

# Client metadata
metaData.version = "0.57.0"

# Connection pool
connPoolCount = 1

# Log
log.to = "console"
log.level = "info"
log.maxDays = 3
`
	return os.WriteFile(configPath, []byte(defaultConfig), 0644)
}
