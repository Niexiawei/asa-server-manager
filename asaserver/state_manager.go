package asaserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
)

// InstanceStatus 定义实例状态枚举
type InstanceStatus string

const (
	StatusStartStartInitialization           InstanceStatus = "start_initialization"
	StatusStartStartInitializationSuccessful InstanceStatus = "start_initialization_successful"
	StatusStarting                           InstanceStatus = "starting"
	StatusStarted                            InstanceStatus = "started"
	StatusStopping                           InstanceStatus = "stopping"
	StatusStopped                            InstanceStatus = "stopped"
	StatusStartFailed                        InstanceStatus = "start_failed"
	StatusStopFailed                         InstanceStatus = "stop_failed"
	StatusRestartFailed                      InstanceStatus = "restart_failed"
	StatusRestart                            InstanceStatus = "restart"
)

// InstanceState 表示实例状态记录
type InstanceState struct {
	InstanceName  string         `json:"instance_name"`
	OperationTime time.Time      `json:"operation_time"`
	ErrorMessage  string         `json:"error_message,omitempty"`
	Status        InstanceStatus `json:"status"`
}

// StateManager 状态管理器
type StateManager struct {
	db                *badger.DB
	mu                sync.RWMutex
	path              string
	maxHistoryRecords int // 每个实例的最大历史记录数
}

const DefaultMaxHistoryRecords = 500 // 每个实例的最大历史记录数

// NewStateManager 创建新的状态管理器
func NewStateManager(baseDir string) (*StateManager, error) {
	dbPath := filepath.Join(baseDir, "database_file", "state_db")
	if err := os.MkdirAll(dbPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create state db directory: %w", err)
	}

	opts := badger.DefaultOptions(dbPath)
	opts.Logger = nil // 可根据需要启用日志

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db: %w", err)
	}

	sm := &StateManager{
		db:                db,
		path:              dbPath,
		maxHistoryRecords: DefaultMaxHistoryRecords,
	}

	return sm, nil
}

// Close 关闭状态管理器
func (sm *StateManager) Close() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.db != nil {
		return sm.db.Close()
	}
	return nil
}

// cleanupOldRecords 清理超出最大记录数的历史记录
func (sm *StateManager) cleanupOldRecords(instanceName string) error {
	// 获取该实例的所有状态记录
	allRecords, err := sm.getAllRecordsForInstance(instanceName)
	if err != nil {
		return err
	}

	// 如果记录数未超过限制，无需清理
	if len(allRecords) <= sm.maxHistoryRecords {
		return nil
	}

	// 按时间排序，保留最新的记录
	// 由于键是按时间戳排序的，我们获取最旧的记录并删除
	recordsToDelete := len(allRecords) - sm.maxHistoryRecords

	// 删除最旧的记录
	return sm.db.Update(func(txn *badger.Txn) error {
		for i := 0; i < recordsToDelete; i++ {
			err := txn.Delete([]byte(allRecords[i]))
			if err != nil {
				return fmt.Errorf("failed to delete old record: %w", err)
			}
		}
		return nil
	})
}

// getAllRecordsForInstance 获取实例的所有记录键（用于清理旧记录）
func (sm *StateManager) getAllRecordsForInstance(instanceName string) ([]string, error) {
	var keys []string

	err := sm.db.View(func(txn *badger.Txn) error {
		prefix := []byte(fmt.Sprintf("state:%s:", instanceName))

		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			keys = append(keys, string(it.Item().Key()))
		}
		return nil
	})

	return keys, err
}

// WriteState 写入实例状态
func (sm *StateManager) WriteState(state InstanceState) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// 生成键值，使用实例名+时间戳确保唯一性
	key := fmt.Sprintf("state:%s:%d", state.InstanceName, state.OperationTime.UnixNano())

	stateBytes, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	err = sm.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), stateBytes)
	})
	if err != nil {
		return err
	}

	// 检查并清理超出最大记录数的历史记录
	return sm.cleanupOldRecords(state.InstanceName)
}

// GetStateHistory 获取实例状态变更历史记录
func (sm *StateManager) GetStateHistory(instanceName string, limit int) ([]InstanceState, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var states = make([]InstanceState, 0)

	err := sm.db.View(func(txn *badger.Txn) error {
		// 创建前缀以查找特定实例的所有状态记录
		prefix := []byte(fmt.Sprintf("state:%s:", instanceName))

		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		// 从后往前遍历以获取最新的记录
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var state InstanceState
				if err := json.Unmarshal(val, &state); err != nil {
					return fmt.Errorf("failed to unmarshal state: %w", err)
				}
				states = append(states, state)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return states, err
	}

	// 按时间倒序排列（最新的在前）
	// 因为键是按时间戳排序的，所以这里需要重新排序
	for i, j := 0, len(states)-1; i < j; i, j = i+1, j-1 {
		states[i], states[j] = states[j], states[i]
	}

	// 如果指定了限制，则只返回最新的几条
	if limit > 0 && len(states) > limit {
		states = states[:limit]
	}

	return states, nil
}

// GetLatestState 获取实例最新状态
func (sm *StateManager) GetLatestState(instanceName string) (*InstanceState, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var latestState *InstanceState
	var latestTime time.Time

	err := sm.db.View(func(txn *badger.Txn) error {
		prefix := []byte(fmt.Sprintf("state:%s:", instanceName))

		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var state InstanceState
				if err := json.Unmarshal(val, &state); err != nil {
					return fmt.Errorf("failed to unmarshal state: %w", err)
				}

				if state.OperationTime.After(latestTime) {
					latestTime = state.OperationTime
					latestState = &state
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	if latestState == nil {
		return nil, fmt.Errorf("no state found for instance: %s", instanceName)
	}

	return latestState, nil
}

// GetAllInstances 获取所有有状态记录的实例名称
func (sm *StateManager) GetAllInstances() ([]string, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	instances := make(map[string]bool)
	var result []string

	err := sm.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		prefix := []byte("state:")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			key := string(it.Item().Key())
			// 解析实例名，格式为 "state:{instance_name}:{timestamp}"
			// 找到第一个冒号后的内容，再找下一个冒号
			if len(key) > len("state:") {
				afterPrefix := key[len("state:"):]
				// 找到第一个冒号（在时间戳之前）
				colonIndex := -1
				for i, ch := range afterPrefix {
					if ch == ':' {
						colonIndex = i
						break
					}
				}
				if colonIndex != -1 {
					instanceName := afterPrefix[:colonIndex]
					if !instances[instanceName] {
						instances[instanceName] = true
						result = append(result, instanceName)
					}
				}
			}
		}
		return nil
	})

	return result, err
}

// 以下是对外提供的操作方法

var (
	instanceStateManager *StateManager
	stateManagerOnce     sync.Once
)

// InitStateManager 初始化状态管理器
func InitStateManager(baseDir string) error {
	var err error
	stateManagerOnce.Do(func() {
		instanceStateManager, err = NewStateManager(baseDir)
	})
	return err
}

// GetStateManager 获取状态管理器实例
func GetStateManager() *StateManager {
	return instanceStateManager
}

// CloseStateManager 关闭状态管理器
func CloseStateManager() error {
	if instanceStateManager != nil {
		return instanceStateManager.Close()
	}
	return nil
}

// WriteInstanceState 写入实例状态
func WriteInstanceState(instanceName string, status InstanceStatus, errorMessage string) error {
	if instanceStateManager == nil {
		return fmt.Errorf("state manager not initialized")
	}

	state := InstanceState{
		InstanceName:  instanceName,
		OperationTime: time.Now(),
		ErrorMessage:  errorMessage,
		Status:        status,
	}

	return instanceStateManager.WriteState(state)
}

// GetInstanceStateHistory 获取实例状态变更历史记录
func GetInstanceStateHistory(instanceName string, limit int) ([]InstanceState, error) {
	if instanceStateManager == nil {
		return make([]InstanceState, 0), fmt.Errorf("state manager not initialized")
	}

	return instanceStateManager.GetStateHistory(instanceName, limit)
}

// GetLatestInstanceState 获取实例最新状态
func GetLatestInstanceState(instanceName string) (InstanceState, error) {
	state := InstanceState{
		InstanceName:  instanceName,
		OperationTime: time.Now(),
		ErrorMessage:  "",
		Status:        StatusStopped,
	}
	if instanceStateManager == nil {
		return state, fmt.Errorf("state manager not initialized")
	}

	status, err := instanceStateManager.GetLatestState(instanceName)
	if err != nil {
		return state, err
	}
	return *status, err
}

func GetInstanceStateIsStart(instanceName string) bool {
	state, err := GetLatestInstanceState(instanceName)
	if err != nil {
		return false
	}
	if state.Status == StatusStarted && state.Status == StatusStarting {
		return true
	}
	return false
}

// GetAllInstanceNames 获取所有有状态记录的实例名称
func GetAllInstanceNames() ([]string, error) {
	if instanceStateManager == nil {
		return nil, fmt.Errorf("state manager not initialized")
	}

	return instanceStateManager.GetAllInstances()
}
