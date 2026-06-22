package asaserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"asa-server/logger"
	"asa-server/win32api"

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
	StatusRestarting                         InstanceStatus = "restarting"
	StatusRestarted                          InstanceStatus = "restarted"
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
	stateChange       *sync.Cond // 状态变更广播，替代轮询
	path              string
	maxHistoryRecords int                // 每个实例的最大历史记录数
	cancelFunc        context.CancelFunc // watcher 关闭
	seq               atomic.Uint64      // 进程内单调递增序列号，保证 key 有序
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
	sm.stateChange = sync.NewCond(&sm.mu)
	sm.seq.Store(1) // 序列号从 1 开始

	// 自动启动卡住状态恢复 watcher
	ctx, cancel := context.WithCancel(context.Background())
	sm.cancelFunc = cancel
	sm.startStuckStateWatcher(ctx)

	return sm, nil
}

// Close 关闭状态管理器
func (sm *StateManager) Close() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.cancelFunc != nil {
		sm.cancelFunc()
	}
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
	return sm.writeStateLocked(state)
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
	return sm.getLatestStateLocked(instanceName)
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
	stateManagerMu       sync.Mutex // H18: protects Once reset in Close/Init
)

// InitStateManager 初始化状态管理器
func InitStateManager(baseDir string) error {
	stateManagerMu.Lock()
	defer stateManagerMu.Unlock()
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
	stateManagerMu.Lock()
	defer stateManagerMu.Unlock()
	if instanceStateManager != nil {
		err := instanceStateManager.Close()
		instanceStateManager = nil
		stateManagerOnce = sync.Once{} // H18 fix: reset under mutex
		return err
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
	if state.Status == StatusStarted || state.Status == StatusStarting {
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

// ========== 操作类型定义 ==========

// OperationType 操作类型
type OperationType string

const (
	OpStart   OperationType = "start"
	OpStop    OperationType = "stop"
	OpRestart OperationType = "restart"
)

// ========== 内部无锁方法（假设 sm.mu 已被持有） ==========

// getLatestStateLocked 获取实例最新状态（无锁版本，调用方需持有 sm.mu）
func (sm *StateManager) getLatestStateLocked(instanceName string) (*InstanceState, error) {
	var latestState *InstanceState

	err := sm.db.View(func(txn *badger.Txn) error {
		prefix := []byte(fmt.Sprintf("state:%s:", instanceName))

		// M17 fix: 使用反向迭代器直接获取最新记录
		opts := badger.DefaultIteratorOptions
		opts.Reverse = true
		it := txn.NewIterator(opts)
		defer it.Close()

		seekKey := []byte(fmt.Sprintf("state:%s;", instanceName))
		it.Seek(seekKey)

		if it.ValidForPrefix(prefix) {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var state InstanceState
				if err := json.Unmarshal(val, &state); err != nil {
					return fmt.Errorf("failed to unmarshal state: %w", err)
				}
				latestState = &state
				return nil
			})
			return err
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

// getLatestStateOrDefaultLocked 获取实例最新状态，如果无记录则返回默认 stopped 状态（无锁版本）
func (sm *StateManager) getLatestStateOrDefaultLocked(instanceName string) InstanceState {
	state, err := sm.getLatestStateLocked(instanceName)
	if err != nil {
		// 无记录，视为 stopped
		return InstanceState{
			InstanceName:  instanceName,
			OperationTime: time.Now(),
			Status:        StatusStopped,
		}
	}
	return *state
}

// getAllInstancesLocked 获取所有有状态记录的实例名称（无锁版本）
func (sm *StateManager) getAllInstancesLocked() ([]string, error) {
	instances := make(map[string]bool)
	var result []string

	err := sm.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		prefix := []byte("state:")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			key := string(it.Item().Key())
			if len(key) > len("state:") {
				afterPrefix := key[len("state:"):]
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

// writeStateLocked 写入状态（无锁版本，调用方需持有 sm.mu）
func (sm *StateManager) writeStateLocked(state InstanceState) error {
	seq := sm.seq.Add(1)
	key := fmt.Sprintf("state:%s:%020d:%020d:%s", state.InstanceName, state.OperationTime.UnixNano(), seq, state.Status)

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

	sm.stateChange.Broadcast()
	return sm.cleanupOldRecords(state.InstanceName)
}

// ========== 广播等待机制 ==========

// WaitForCondition 等待条件满足（基于 sync.Cond 广播，不轮询）
// condition 函数在 sm.mu.Lock() 持有期间执行，禁止调用任何会获取 sm.mu 的公共方法
func (sm *StateManager) WaitForCondition(condition func() bool, timeout time.Duration) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	deadline := time.Now().Add(timeout)
	for !condition() {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("timeout waiting for condition")
		}
		timer := time.AfterFunc(remaining, func() {
			sm.stateChange.Broadcast()
		})
		sm.stateChange.Wait()
		timer.Stop()
	}
	return nil
}

// ========== 原子状态转换（CAS） ==========

// compareAndSwapState 原子状态转换（内部方法）
// 检查当前状态是否在 allowedStates 中，如果是则转换到 newStatus
// allowedStates 为空切片表示只允许无记录（默认 stopped）状态
func (sm *StateManager) compareAndSwapState(instanceName string, allowedStates []InstanceStatus, newStatus InstanceStatus) (bool, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	currentState := sm.getLatestStateOrDefaultLocked(instanceName)

	// 检查当前状态是否在允许列表中
	allowed := false
	for _, s := range allowedStates {
		if currentState.Status == s {
			allowed = true
			break
		}
	}

	if !allowed {
		return false, nil
	}

	// 写入新状态
	newState := InstanceState{
		InstanceName:  instanceName,
		OperationTime: time.Now(),
		Status:        newStatus,
	}
	if err := sm.writeStateLocked(newState); err != nil {
		return false, err
	}

	return true, nil
}

// ========== 操作权限检查 ==========

// isOperationAllowed 检查操作是否允许（内部方法）
// 根据目标实例当前状态判断操作是否允许
func (sm *StateManager) isOperationAllowed(instanceName string, op OperationType) (bool, string) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// 规则 2：检查目标实例当前状态
	currentState := sm.getLatestStateOrDefaultLocked(instanceName)

	switch op {
	case OpStart:
		switch currentState.Status {
		case StatusStopped, StatusStartFailed, StatusStopFailed, StatusRestartFailed, "":
			return true, ""
		}
		return false, fmt.Sprintf("instance '%s' is in '%s' state, start not allowed", instanceName, currentState.Status)

	case OpStop:
		switch currentState.Status {
		case StatusStarted:
			return true, ""
		}
		return false, fmt.Sprintf("instance '%s' is in '%s' state, stop not allowed (use ForceStop)", instanceName, currentState.Status)

	case OpRestart:
		switch currentState.Status {
		case StatusStarted:
			return true, ""
		}
		return false, fmt.Sprintf("instance '%s' is in '%s' state, restart not allowed", instanceName, currentState.Status)
	}

	return false, fmt.Sprintf("unknown operation type: %s", op)
}

// ========== 对外包级便捷函数 ==========

// CompareAndSwapInstanceState 原子状态转换（包级函数）
func CompareAndSwapInstanceState(instanceName string, allowedStates []InstanceStatus, newStatus InstanceStatus) (bool, error) {
	if instanceStateManager == nil {
		return false, fmt.Errorf("state manager not initialized")
	}
	return instanceStateManager.compareAndSwapState(instanceName, allowedStates, newStatus)
}

// IsOperationAllowed 检查操作是否允许（包级函数）
func IsOperationAllowed(instanceName string, op OperationType) (bool, string) {
	if instanceStateManager == nil {
		return false, "state manager not initialized"
	}
	return instanceStateManager.isOperationAllowed(instanceName, op)
}

// ========== 卡住状态自动恢复 ==========

const stuckThreshold = 10 * time.Minute

// startStuckStateWatcher 启动卡住状态自动恢复 watcher（私有方法，仅由 NewStateManager 调用）
func (sm *StateManager) startStuckStateWatcher(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sm.recoverStuckStates()
			}
		}
	}()
}

// recoverStuckStates 扫描所有实例，恢复卡住的中间态为 stopped
func (sm *StateManager) recoverStuckStates() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	instances, err := sm.getAllInstancesLocked()
	if err != nil {
		return
	}

	for _, name := range instances {
		state, err := sm.getLatestStateLocked(name)
		if err != nil || state == nil {
			continue
		}

		if time.Since(state.OperationTime) < stuckThreshold {
			continue
		}

		switch state.Status {
		case StatusStartStartInitialization, StatusStarting,
			StatusStartStartInitializationSuccessful, StatusStopping:
			if !isInstanceProcessAlive(name) {
				logger.GetLogger().Warnf(
					"Recovering stuck instance '%s' (state: %s, age: %v)",
					name, state.Status, time.Since(state.OperationTime))
				_ = sm.writeStateLocked(InstanceState{
					InstanceName:  name,
					OperationTime: time.Now(),
					Status:        StatusStopped,
					ErrorMessage:  fmt.Sprintf("auto-recovered from stuck state: %s", state.Status),
				})
			}
		}
	}
}

// isInstanceProcessAlive 检查实例进程是否存活
// 使用端口检测 + PID 检测双重验证，这些函数不依赖 sm.mu，在锁内调用安全
func isInstanceProcessAlive(instanceName string) bool {
	// 方法 1：检查端口是否被监听
	running, err := IsServerRunning(instanceName)
	if err == nil && running {
		return true
	}

	// 方法 2：检查进程是否存在
	pid, err := GetInstancePID(instanceName)
	if err != nil || pid <= 0 {
		return false
	}

	exited, err := win32api.IsProcessExited(uint32(pid))
	if err != nil {
		return false
	}
	return !exited
}

// WaitForInstanceState 等待实例离开指定状态（包级函数，基于广播不轮询）
func WaitForInstanceState(instanceName string, notInStates []InstanceStatus, timeout time.Duration) error {
	if instanceStateManager == nil {
		return fmt.Errorf("state manager not initialized")
	}
	return instanceStateManager.WaitForCondition(func() bool {
		currentState := instanceStateManager.getLatestStateOrDefaultLocked(instanceName)
		for _, s := range notInStates {
			if currentState.Status == s {
				return false
			}
		}
		return true
	}, timeout)
}

// ClearStateDatabase 删除状态数据库目录（用于 key 格式变更后清空旧数据）
func ClearStateDatabase() error {
	if instanceStateManager != nil {
		return fmt.Errorf("cannot clear state database while state manager is running")
	}
	dbPath := filepath.Join(BaseDir, "database_file", "state_db")
	if err := os.RemoveAll(dbPath); err != nil {
		return fmt.Errorf("failed to remove state database: %w", err)
	}
	return nil
}
