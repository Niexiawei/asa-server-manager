package frpmanage

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"asa-server/pkg/logger"

	"github.com/fatedier/frp/client"
	"github.com/fatedier/frp/pkg/config"
	"github.com/fatedier/frp/pkg/config/source"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	frplog "github.com/fatedier/frp/pkg/util/log"
	golibLog "github.com/fatedier/golib/log"
)

// FrpcManager manages the frpc client lifecycle in-process, via
// github.com/fatedier/frp/client — no more separate frpc.exe (see
// docs/LINUX_COMPATIBILITY_PLAN.md §5.10). This removes the last reason this
// package needed platform-specific binaries.
//
// 配置面是结构化参数（Config，落 {BaseDir}/frp/frpc.json），不再有 frpc.toml ——
// 见 docs/FRP_FORM_CONFIG_PLAN.md。
type FrpcManager struct {
	runDir string

	mu       sync.Mutex
	cfg      *Config
	svr      *client.Service
	src      *source.ConfigSource // 热更新要往这里塞新的代理清单
	cancel   context.CancelFunc   // 取消喂给 svr.Run 的 ctx，见 stopLocked
	running  bool
	startErr error // Last start error
}

var globalManager *FrpcManager

// Initialize sets up the frp config directory. There is no binary to
// extract anymore — frpc now runs in-process.
func Initialize(basedir string) (string, error) {
	dir := filepath.Join(basedir, "frp")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create frp directory: %v", err)
	}

	// frp 有一个包级全局 logger（pkg/util/log.Logger）。绝不能调 frplog.InitLogger——
	// 它会按配置抢 stdout 或自己开一份轮转文件。正确做法是自己 New 一个写进
	// asaServer.log 的 Logger 塞进去。
	//
	// 顺带一提：正因为 InitLogger 从不执行，frp 配置里的 log.* 字段对本项目
	// 完全惰性 —— 所以 Config 里没有、也不该有日志字段。
	frplog.Logger = golibLog.New(golibLog.WithOutput(&LogWriter{tag: "[frpc]"}))

	globalManager = &FrpcManager{runDir: dir}

	// 老的 frpc.toml 做一次性迁移。尽力而为：失败只记 WARN，当作「未配置」，
	// 用户在面板上重填三个参数即可，不该因此挡住整个服务启动。
	if migrated, dropped, err := migrateFromTOML(dir); err != nil {
		logger.Warnf("frp 老配置迁移失败（将按未配置处理）: %v", err)
	} else if migrated {
		logger.Infof("frp 老配置已迁移为 %s，原文件保留为 %s.migrated", configFileName, legacyTOMLName)
		if len(dropped) > 0 {
			logger.Warnf("frp 迁移丢弃了 %d 条无法用端口映射表达的代理: %s",
				len(dropped), strings.Join(dropped, ", "))
		}
	}

	if cfg, err := LoadConfig(dir); err != nil {
		if !errors.Is(err, ErrNotConfigured) {
			logger.Warnf("读取 frp 配置失败: %v", err)
		}
	} else {
		globalManager.cfg = cfg
	}

	return dir, nil
}

// Start starts the frpc client asynchronously.
func (m *FrpcManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startLocked()
}

// startLocked 是 Start 的无锁内核，供 Start / Restart / SetConfig 复用。
func (m *FrpcManager) startLocked() error {
	if m.running {
		return fmt.Errorf("frpc is already running")
	}

	cfg, err := LoadConfig(m.runDir)
	if err != nil {
		// ErrNotConfigured 原样上抛：调用方要靠它区分「没配」和「配了但起不来」。
		m.startErr = err
		return err
	}
	// 启动前再校验一次：防住有人绕过面板手改坏了 frpc.json。
	if _, err := cfg.Validate(); err != nil {
		m.startErr = err
		return fmt.Errorf("frp 配置无效: %w", err)
	}
	m.cfg = cfg

	svr, src, err := buildService(cfg)
	if err != nil {
		m.startErr = err
		return fmt.Errorf("failed to build frp service: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.svr = svr
	m.src = src
	m.cancel = cancel
	m.startErr = nil
	m.running = true

	go m.asyncRun(ctx, svr)

	return nil
}

// buildService constructs a ready-to-run client.Service from cfg.
// Mirrors cmd/frpc/sub/root.go's runClientWithAggregator, verified against
// frp v0.71.0 (docs/LINUX_COMPATIBILITY_PLAN.md §5.10.2).
//
// 返回的 *source.ConfigSource 要存进 manager：热更新（SetConfig）靠它把新的
// 代理清单换进去。
func buildService(cfg *Config) (*client.Service, *source.ConfigSource, error) {
	common, proxyCfgs, err := cfg.build()
	if err != nil {
		return nil, nil, err
	}
	return newService(common, proxyCfgs)
}

// newService 是 buildService 里「把 frp 配置对象接成一个可运行的 Service」那一半。
// 单独拆出来是为了让泄漏回归测试能喂一份改过 LoginFailExit 的 common 配置进来 ——
// 那条用例要的正是「登录失败后不退出、进退避重试循环」的那条路径。
func newService(common *v1.ClientCommonConfig, proxyCfgs []v1.ProxyConfigurer) (*client.Service, *source.ConfigSource, error) {
	src := source.NewConfigSource()
	if err := src.ReplaceAll(proxyCfgs, nil); err != nil {
		return nil, nil, fmt.Errorf("load proxies: %w", err)
	}
	aggregator := source.NewAggregator(src)

	proxyCfgs, visitorCfgs, err := aggregator.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("aggregate config: %w", err)
	}
	proxyCfgs, visitorCfgs = config.FilterClientConfigurers(common, proxyCfgs, visitorCfgs)
	proxyCfgs = config.CompleteProxyConfigurers(proxyCfgs)
	visitorCfgs = config.CompleteVisitorConfigurers(visitorCfgs)

	if warn, err := validation.ValidateAllClientConfig(common, proxyCfgs, visitorCfgs, nil); err != nil {
		return nil, nil, fmt.Errorf("validate config: %w", err)
	} else if warn != nil {
		logger.Warnf("[frpc] %v", warn)
	}

	svr, err := client.NewService(client.ServiceOptions{
		Common:                 common,
		ConfigSourceAggregator: aggregator, // 必填，为空 NewService 直接报错
		// ConfigFilePath 留空：它只被 client/config_manager.go 的 admin 热重载用，
		// 我们没开 webServer，也不再有配置文件。
	})
	if err != nil {
		return nil, nil, err
	}
	return svr, src, nil
}

// asyncRun runs svr and keeps m.running / m.startErr in sync with it for as
// long as it stays the manager's current service.
//
// svr.Run blocks until stopped. It returns nil on a normal Close/
// GracefulClose, and a non-nil error only when the initial login to frps
// fails (loginFailExit=true in the default config) — that error is the
// deterministic replacement for the old code's 500ms "did the process
// immediately exit" guess (see docs/LINUX_COMPATIBILITY_PLAN.md §5.10.4 #7).
func (m *FrpcManager) asyncRun(ctx context.Context, svr *client.Service) {
	err := svr.Run(ctx)

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
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running || m.svr == nil {
		return fmt.Errorf("frpc is not running")
	}
	m.stopLocked()
	return nil
}

// stopLocked 关掉当前 service，容忍「本来就没在跑」。
//
// ⚠️ 这里**不用 svr.GracefulClose**，而是取消我们自己喂给 Run 的 ctx。
// Run 的收尾是 `<-svr.ctx.Done(); svr.stop()`，而它的 svr.ctx 派生自我们传进去的
// ctx —— 两条路走的是同一个关闭流程，但自己持有 cancel 躲开了 GracefulClose 的
// 两个问题（frp v0.71.0）：
//
//  1. **nil cancel 的 panic 窗口**：svr.cancel 是 Run 开头才赋值的。Start 之后
//     立刻 Stop（Restart 的 stop→start→stop 尤其容易），Run 可能还没执行到那一行，
//     GracefulClose 里的 svr.cancel(nil) 就是在调一个 nil 函数 —— 而 frp 是库内
//     调用，没有崩溃隔离，这一下会带走整个 asa-server。
//  2. **数据竞争**：svr.cancel 的写（Run 里）与读（GracefulClose 里）之间没有任何
//     同步边，go test -race 会如实报出来。上游 cmd/frpc 的 handleTermSignal 是同一
//     个写法，所以这是 frp 的问题，不是我们用错了 API。
//
// 代价只有 gracefulShutdownDuration 归 0，即 ctl.GracefulClose 里那句 time.Sleep(d)
// 没了。对 tcp/udp 端口转发没有意义 —— 上游自己也只在 kcp/quic 协议下才在意优雅
// 关闭（cmd/frpc/sub/root.go 的 shouldGracefulClose）。
func (m *FrpcManager) stopLocked() {
	if !m.running || m.svr == nil {
		return
	}
	logger.Infof("frp stoping ...")
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.running = false
	logger.Infof("frp stoped")
}

// Restart restarts the frpc client.
func (m *FrpcManager) Restart() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	return m.startLocked()
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

// Config 返回当前配置的副本，未配置时返回 nil。
func (m *FrpcManager) Config() *Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg == nil {
		return nil
	}
	clone := *m.cfg
	clone.Rules = append([]PortRule(nil), m.cfg.Rules...)
	return &clone
}

// SetConfig 校验、落盘并让新配置生效，返回不阻塞保存的提示。
//
// 生效方式**分两条路**：
//   - frps 地址/端口/token 变了 → 必须重新登录，只能重启；
//   - 只有端口规则变了 → 走 UpdateConfigSource 热更新，已建立的隧道不断开
//     （用户加一条端口规则不该把正在游戏里的玩家踢下线）。
func (m *FrpcManager) SetConfig(next *Config) ([]string, error) {
	if next == nil {
		return nil, errors.New("配置不能为空")
	}
	next.Normalize()
	warnings, err := next.Validate()
	if err != nil {
		return nil, err
	}
	if err := SaveConfig(m.runDir, next); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	prev := m.cfg
	m.cfg = next

	if !m.running || m.svr == nil {
		return warnings, nil // 没在跑，下次 Start 自然生效
	}
	if next.commonChanged(prev) {
		m.stopLocked()
		return warnings, m.startLocked()
	}

	common, proxyCfgs, err := next.build()
	if err != nil {
		return warnings, err
	}
	// 用 UpdateConfigSource 而不是 UpdateAllConfigurer：前者会先把新配置写回
	// ConfigSource 再应用，后者只改 service 内部那份。只调后者的话，frp 内部
	// 任何一次 reloadConfigFromSources() 都会从旧 source 把配置**回滚回去**
	// （已核对 client/service.go:369 与 :385）。
	if err := m.svr.UpdateConfigSource(common, proxyCfgs, nil); err != nil {
		return warnings, fmt.Errorf("热更新 frp 代理失败: %w", err)
	}
	logger.Infof("frp 代理已热更新为 %d 条，未断开现有连接", next.proxyCount())
	return warnings, nil
}

// ProxyState 是单条代理的运行状态，来自 client/proxy.WorkingStatus 的投影。
//
// 只取 UI 需要的字段而不是把 frp 的类型直接塞进 HTTP 响应：那等于把
// v1.ProxyConfigurer 的内部形状变成我们的对外 API。
type ProxyState struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	LocalPort  int    `json:"local_port"`
	Phase      string `json:"phase"`
	Err        string `json:"err,omitempty"`
	RemoteAddr string `json:"remote_addr,omitempty"`
}

// FRPStatus 是 /api/frp/status 与 /api/frp/status/stream 的共同 payload。
//
// Message 与 Proxies[].Err 分工不同：前者是「连不上 frps」（整体失败），
// 后者是「连上了但这条代理没起来」（局部失败）。端口范围映射最常见的故障
// 恰恰是后者 —— 范围里某一个端口在 frps 侧已被别的客户端占用，整体看起来
// 一切正常，只有那一条静默失效。
type FRPStatus struct {
	Running    bool         `json:"running"`
	Configured bool         `json:"configured"`
	Message    string       `json:"message,omitempty"`
	ProxyCount int          `json:"proxy_count"`
	Proxies    []ProxyState `json:"proxies,omitempty"`
}

// Status 汇总当前状态。
func (m *FrpcManager) Status() FRPStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	st := FRPStatus{Running: m.running}
	if m.startErr != nil && !errors.Is(m.startErr, ErrNotConfigured) {
		st.Message = m.startErr.Error()
	}
	if m.cfg == nil {
		return st
	}
	if _, err := m.cfg.Validate(); err == nil {
		st.Configured = true
	}
	st.ProxyCount = m.cfg.proxyCount()

	if !m.running || m.svr == nil {
		return st
	}
	exporter := m.svr.StatusExporter()
	st.Proxies = make([]ProxyState, 0, st.ProxyCount)
	m.cfg.eachProxy(func(proto string, port int, name string) {
		ps := ProxyState{Name: name, Type: proto, LocalPort: port, Phase: "unknown"}
		if ws, ok := exporter.GetProxyStatus(name); ok {
			ps.Phase = ws.Phase
			ps.Err = ws.Err
			ps.RemoteAddr = ws.RemoteAddr
		}
		st.Proxies = append(st.Proxies, ps)
	})
	return st
}

// GetGlobalManager returns the global frpc manager instance
func GetGlobalManager() *FrpcManager {
	return globalManager
}

// LogWriter 把 frp 的日志转抄进本项目的 logger。
//
// 它同时实现 io.Writer 与 golib 的 log.Writer；后者让 golib 把**日志等级作为
// 参数**交过来，于是 frp 的 WARN/ERROR 在系统日志里也是 WARN/ERROR，而不是
// 全部塌成 INFO（前端 FRP 面板按等级配色，塌了就恒为蓝色）。
//
// 这里不再做 ANSI 剥离：golib 的颜色只在 ConsoleWriter.WriteLog 里加，走到这
// 一律是明文。子进程时代解析 frpc.exe 的 stdout 才需要剥离，那是迁移时正确
// 保留、迁移后应当回收的东西（docs/FRP_FORM_CONFIG_PLAN.md R4）。
type LogWriter struct {
	tag string
}

func (w *LogWriter) Write(p []byte) (n int, err error) {
	w.emit(golibLog.InfoLevel, p)
	return len(p), nil
}

func (w *LogWriter) WriteLog(p []byte, level golibLog.Level, _ time.Time) (n int, err error) {
	w.emit(level, p)
	return len(p), nil
}

func (w *LogWriter) emit(level golibLog.Level, p []byte) {
	logf := logger.Infof
	switch level {
	case golibLog.WarnLevel:
		logf = logger.Warnf
	case golibLog.ErrorLevel:
		logf = logger.Errorf
	}

	scanner := bufio.NewScanner(strings.NewReader(string(p)))
	for scanner.Scan() {
		line := trimGolibHeader(scanner.Text())
		if line != "" {
			logf("%s %s", w.tag, line)
		}
	}
}

// trimGolibHeader 去掉 golib 自己拼的 "2006-01-02 15:04:05.000 [I] " 前缀。
//
// 本项目的 logger 会再加一次时间与等级，不去掉的话每行都带两份。
// 形状对不上就原样返回 —— 宁可多一段前缀，也不要截掉正文。
func trimGolibHeader(line string) string {
	const tsLen = 23 // len("2006-01-02 15:04:05.000")
	if len(line) < tsLen+5 {
		return line
	}
	if line[tsLen] != ' ' || line[tsLen+1] != '[' || line[tsLen+3] != ']' || line[tsLen+4] != ' ' {
		return line
	}
	return line[tsLen+5:]
}

var (
	_ io.Writer       = (*LogWriter)(nil)
	_ golibLog.Writer = (*LogWriter)(nil)
)
