package parseserver

import (
	"asa-server/asaserver"
	"asa-server/logger"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/Niexiawei/go-arkparser/arkmonitor"
	"github.com/Niexiawei/go-arkparser/files"
	"github.com/dgraph-io/badger/v4"
)

const (
	saveKeyAll = "save:%s:all"
)

// SaveDataManager manages BadgerDB cache and arkmonitor-based file monitoring for ARK save files
type SaveDataManager struct {
	db           *badger.DB
	mu           sync.RWMutex
	monitors     map[string]*arkmonitor.MonitorImpl
	broadcasters map[string]*SaveBroadcaster
	unsubs       []func()
}

// SaveBroadcaster fans out save events to SSE subscribers
type SaveBroadcaster struct {
	msgChan     chan []byte
	subscribers map[chan []byte]struct{}
	subMu       sync.Mutex
}

func NewSaveBroadcaster() *SaveBroadcaster {
	b := &SaveBroadcaster{
		msgChan:     make(chan []byte, 100),
		subscribers: make(map[chan []byte]struct{}),
	}
	go b.broadcast()
	return b
}

func (b *SaveBroadcaster) broadcast() {
	for msg := range b.msgChan {
		b.subMu.Lock()
		for ch := range b.subscribers {
			select {
			case ch <- msg:
			default:
				// subscriber too slow, skip
			}
		}
		b.subMu.Unlock()
	}
}

func (b *SaveBroadcaster) Subscribe() (chan []byte, func()) {
	ch := make(chan []byte, 50)
	b.subMu.Lock()
	b.subscribers[ch] = struct{}{}
	b.subMu.Unlock()

	unsubscribe := func() {
		b.subMu.Lock()
		delete(b.subscribers, ch)
		b.subMu.Unlock()
		close(ch)
	}
	return ch, unsubscribe
}

func (b *SaveBroadcaster) Broadcast(data []byte) {
	select {
	case b.msgChan <- data:
	default:
	}
}

func (b *SaveBroadcaster) Stop() {
	close(b.msgChan)
}

// NewSaveDataManager creates a new SaveDataManager with its own BadgerDB instance
func NewSaveDataManager() (*SaveDataManager, error) {
	dbPath := filepath.Join(asaserver.BaseDir, "database_file", "arkworldsave")
	opts := badger.DefaultOptions(dbPath).
		WithLogger(nil).
		WithNumVersionsToKeep(1)

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open arkworldsave badger db: %w", err)
	}

	return &SaveDataManager{
		db:           db,
		monitors:     make(map[string]*arkmonitor.MonitorImpl),
		broadcasters: make(map[string]*SaveBroadcaster),
	}, nil
}

// GetBroadcaster returns or creates a broadcaster for the given instance
func (m *SaveDataManager) GetBroadcaster(instanceName string) *SaveBroadcaster {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.broadcasters[instanceName]; ok {
		return b
	}
	b := NewSaveBroadcaster()
	m.broadcasters[instanceName] = b
	return b
}

// SetCached stores snapshot data in BadgerDB
func (m *SaveDataManager) SetCached(instanceName string, data *CachedSaveData) error {
	return m.db.Update(func(txn *badger.Txn) error {
		key := []byte(fmt.Sprintf(saveKeyAll, instanceName))
		jsonBytes, err := json.Marshal(data)
		if err != nil {
			return err
		}
		return txn.Set(key, jsonBytes)
	})
}

// GetCached retrieves cached snapshot data from BadgerDB
func (m *SaveDataManager) GetCached(instanceName string) (*CachedSaveData, error) {
	var data CachedSaveData
	err := m.db.View(func(txn *badger.Txn) error {
		key := []byte(fmt.Sprintf(saveKeyAll, instanceName))
		item, err := txn.Get(key)
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &data)
		})
	})
	if err != nil {
		return nil, err
	}
	return &data, nil
}

// Start begins monitoring all instance save directories for changes using arkmonitor
func (m *SaveDataManager) Start(_ context.Context) {
	instances, err := asaserver.GetAvailableInstances()
	if err != nil {
		logger.GetLogger().Errorf("Failed to get instances for save monitoring: %v", err)
		return
	}

	for _, instanceName := range instances {
		config, err := asaserver.LoadInstanceConfig(instanceName)
		if err != nil {
			continue
		}

		dirMapName := config.MapName
		if dirMapName == "BobsMissions_WP" {
			continue
		}

		arkPath := filepath.Join(asaserver.InstancesDir, instanceName, "Save",
			dirMapName, config.MapName+".ark")

		if err := m.startMonitor(instanceName, arkPath); err != nil {
			logger.GetLogger().Warnf("Failed to start save monitor for %s: %v", instanceName, err)
		}
	}
}

// startMonitor creates and starts an arkmonitor for a single instance
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
		return fmt.Errorf("create monitor: %w", err)
	}

	// Subscribe observer before starting
	observer := &saveObserver{
		manager:      m,
		instanceName: instanceName,
		monitor:      mon,
	}
	unsub := mon.Subscribe(observer)
	m.mu.Lock()
	m.unsubs = append(m.unsubs, unsub)
	m.monitors[instanceName] = mon
	m.mu.Unlock()

	// Start the monitor (triggers initial Reload)
	if err := mon.Start(); err != nil {
		unsub()
		return fmt.Errorf("start monitor: %w", err)
	}

	logger.GetLogger().Infof("Save monitor started for instance: %s (%s)", instanceName, arkPath)

	// Broadcast initial snapshot (Reload loaded it but no events are sent for first load)
	if snap := mon.Snapshot(); snap != nil {
		m.cacheAndBroadcast(instanceName, snap)
	}

	return nil
}

// cacheAndBroadcast caches the snapshot to BadgerDB and broadcasts via SSE
func (m *SaveDataManager) cacheAndBroadcast(instanceName string, snap *arkmonitor.WorldSnapshot) {
	broadcaster := m.GetBroadcaster(instanceName)

	cached := &CachedSaveData{
		Players:   snap.Players,
		Tribes:    snap.Tribes,
		Timestamp: snap.Timestamp,
	}

	// Cache to BadgerDB
	if err := m.SetCached(instanceName, cached); err != nil {
		logger.GetLogger().Errorf("Failed to cache save data for %s: %v", instanceName, err)
	}

	// Broadcast start event
	startMsg, _ := json.Marshal(map[string]any{
		"type":      "start",
		"map":       instanceName,
		"timestamp": time.Now().Unix(),
	})
	broadcaster.Broadcast(startMsg)

	// Broadcast complete event
	completeMsg, _ := json.Marshal(map[string]any{
		"type":    "complete",
		"map":     instanceName,
		"players": len(snap.Players),
		"tribes":  len(snap.Tribes),
	})
	broadcaster.Broadcast(completeMsg)

	// Broadcast full data
	dataMsg, _ := json.Marshal(map[string]any{
		"type": "data",
		"map":  instanceName,
		"data": cached,
	})
	broadcaster.Broadcast(dataMsg)

	logger.GetLogger().Infof("Save monitor updated %s: %d players, %d tribes",
		instanceName, len(snap.Players), len(snap.Tribes))
}

// Stop closes the BadgerDB and all monitors
func (m *SaveDataManager) Stop() {
	m.mu.Lock()
	for _, unsub := range m.unsubs {
		unsub()
	}
	for _, mon := range m.monitors {
		mon.Stop()
	}
	for _, b := range m.broadcasters {
		b.Stop()
	}
	m.mu.Unlock()

	if m.db != nil {
		m.db.Close()
	}
}

// saveObserver implements arkmonitor.Observer interface
type saveObserver struct {
	manager      *SaveDataManager
	instanceName string
	monitor      *arkmonitor.MonitorImpl
}

func (o *saveObserver) OnEvent(event arkmonitor.Event) {
	// After Reload(), the monitor has an updated snapshot.
	// We get the full snapshot and broadcast it.
	snap := o.monitor.Snapshot()
	if snap != nil {
		o.manager.cacheAndBroadcast(o.instanceName, snap)
	}
}
