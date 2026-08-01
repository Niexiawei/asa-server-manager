package parseserver

import (
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/logger"
	"asa-server/internal/realtime"
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/Niexiawei/go-arkparser/arkmonitor"
	"github.com/Niexiawei/go-arkparser/files"
)

// coalesceDelay 合并一次存档写入(ark/profile/tribe 可能连续多次写)产生的突发事件，
// 避免同一次存档触发大量重复重建与推送。
const coalesceDelay = 300 * time.Millisecond

// SaveDataManager 为每个实例维护一个 arkmonitor，缓存最新解析结果（内存），
// 并在存档变化时通过 WebSocket 推送玩家/部落列表。
type SaveDataManager struct {
	mu       sync.RWMutex
	monitors map[string]*arkmonitor.MonitorImpl
	current  map[string]*SaveData // 各实例最新解析结果（内存缓存）
	unsubs   []func()
	stops    []chan struct{} // 各实例去抖 worker 的停止信号
}

// NewSaveDataManager 创建管理器。返回 error 以兼容既有装配签名（当前恒为 nil）。
func NewSaveDataManager() (*SaveDataManager, error) {
	return &SaveDataManager{
		monitors: make(map[string]*arkmonitor.MonitorImpl),
		current:  make(map[string]*SaveData),
	}, nil
}

// GetCurrent 返回实例的内存缓存解析结果；未命中返回 (nil, false)。
func (m *SaveDataManager) GetCurrent(instanceName string) (*SaveData, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.current[instanceName]
	return data, ok
}

// Start 为所有实例启动存档监控。
func (m *SaveDataManager) Start(_ context.Context) {
	instances, err := cfgpkg.GetAvailableInstances()
	if err != nil {
		logger.GetLogger().Errorf("获取实例列表失败，无法启动存档监控: %v", err)
		return
	}

	for _, instanceName := range instances {
		config, err := cfgpkg.LoadInstanceConfig(instanceName)
		if err != nil {
			continue
		}

		// BobsMissions_WP 无存档解析意义，跳过
		if config.MapName == "BobsMissions_WP" {
			continue
		}

		arkPath := filepath.Join(cfgpkg.InstancesDir, instanceName, "Save",
			config.MapName, config.MapName+".ark")

		if err := m.startMonitor(instanceName, arkPath); err != nil {
			logger.GetLogger().Warnf("启动存档监控失败 %s: %v", instanceName, err)
		}
	}
}

// startMonitor 为单个实例创建并启动 arkmonitor 及其去抖推送 worker。
func (m *SaveDataManager) startMonitor(instanceName, arkPath string) error {
	cfg := arkmonitor.MonitorConfig{
		SavePath:    arkPath,
		WatchDir:    true,
		EventBuffer: 100,
		LazyMode:    true,
		LoadOptions: []files.LoadOption{files.WithMmap()},
	}

	mon, err := arkmonitor.NewMonitor(cfg)
	if err != nil {
		return fmt.Errorf("创建监控器: %w", err)
	}

	// trigger 缓冲为 1：多次事件合并为一次重建；stop 用于退出 worker
	trigger := make(chan struct{}, 1)
	stop := make(chan struct{})
	observer := &saveObserver{trigger: trigger}
	unsub := mon.Subscribe(observer)

	m.mu.Lock()
	m.unsubs = append(m.unsubs, unsub)
	m.stops = append(m.stops, stop)
	m.monitors[instanceName] = mon
	m.mu.Unlock()

	go m.debounceWorker(instanceName, mon, trigger, stop)

	// Start 会触发首次 Reload（首次无 diff 事件）
	if err := mon.Start(); err != nil {
		unsub()
		close(stop)
		return fmt.Errorf("启动监控器: %w", err)
	}

	logger.GetLogger().Infof("存档监控已启动: %s (%s)", instanceName, arkPath)

	// 首次加载不产生事件，主动缓存并推送一次
	if export := mon.Export(); export != nil {
		m.updateAndBroadcast(instanceName, export)
	}

	return nil
}

// debounceWorker 合并突发事件：收到触发后小睡片刻，取最新 Export 重建并推送一次。
func (m *SaveDataManager) debounceWorker(instanceName string, mon *arkmonitor.MonitorImpl, trigger <-chan struct{}, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-trigger:
			// 合并同一次存档写入产生的后续事件
			select {
			case <-time.After(coalesceDelay):
			case <-stop:
				return
			}
			// 清掉去抖窗口内累积的触发，避免紧接着再跑一次
			select {
			case <-trigger:
			default:
			}
			if export := mon.Export(); export != nil {
				m.updateAndBroadcast(instanceName, export)
			}
		}
	}
}

// updateAndBroadcast 从 Export 富数据构建 SaveData，更新内存缓存并 WS 推送。
func (m *SaveDataManager) updateAndBroadcast(instanceName string, export map[string][]map[string]any) {
	data := buildSaveData(export)

	m.mu.Lock()
	m.current[instanceName] = data
	m.mu.Unlock()

	// 玩家列表与部落列表分别以不同 event_type 全量推送
	realtime.BroadcastSavePlayers(instanceName, data.Players)
	realtime.BroadcastSaveTribes(instanceName, data.Tribes)
}

// Stop 停止所有监控与 worker。
func (m *SaveDataManager) Stop() {
	m.mu.Lock()
	for _, unsub := range m.unsubs {
		unsub()
	}
	for _, stop := range m.stops {
		close(stop)
	}
	for _, mon := range m.monitors {
		mon.Stop()
	}
	m.unsubs = nil
	m.stops = nil
	m.monitors = make(map[string]*arkmonitor.MonitorImpl)
	m.mu.Unlock()
}

// saveObserver 实现 arkmonitor.Observer，把变更事件汇聚到去抖 trigger。
type saveObserver struct {
	trigger chan<- struct{}
}

func (o *saveObserver) OnEvent(_ arkmonitor.Event) {
	select {
	case o.trigger <- struct{}{}:
	default: // 已有待处理触发，丢弃本次（合并）
	}
}
