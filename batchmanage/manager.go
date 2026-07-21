package batchmanage

import (
	cfgpkg "asa-server/config"
	instancepkg "asa-server/instance"
	"asa-server/logger"
	"asa-server/realtime"
	statepkg "asa-server/state"
	"context"
	"fmt"
	"sync"
	"time"
)

// BatchOperationType 批量操作类型
type BatchOperationType string

const (
	BatchStart   BatchOperationType = "start"
	BatchStop    BatchOperationType = "stop"
	BatchRestart BatchOperationType = "restart"
)

// BatchOperationRequest POST 请求体
type BatchOperationRequest struct {
	Instances    []string `json:"instances"`
	DelaySeconds int      `json:"delay_seconds"`
}

// InstanceOpStatus 单实例操作结果状态
type InstanceOpStatus string

const (
	InstancePending   InstanceOpStatus = "pending"
	InstanceRunning   InstanceOpStatus = "running"
	InstanceSuccess   InstanceOpStatus = "success"
	InstanceFailed    InstanceOpStatus = "failed"
	InstanceSkipped   InstanceOpStatus = "skipped"
	InstanceCancelled InstanceOpStatus = "cancelled"
)

// InstanceResult 单实例操作结果
type InstanceResult struct {
	InstanceName string           `json:"instance_name"`
	Status       InstanceOpStatus `json:"status"`
	Error        string           `json:"error,omitempty"`
}

// BatchLogEntry SSE 日志条目
type BatchLogEntry struct {
	Timestamp    int64  `json:"timestamp"`
	Level        string `json:"level"`
	Message      string `json:"message"`
	InstanceName string `json:"instance_name,omitempty"`
}

// LogBroadcaster SSE 日志广播器
type LogBroadcaster struct {
	msgChan     chan BatchLogEntry
	subscribers map[chan<- BatchLogEntry]bool
	mu          sync.RWMutex
	running     bool
	wg          sync.WaitGroup
}

// NewLogBroadcaster 创建日志广播器
func NewLogBroadcaster() *LogBroadcaster {
	return &LogBroadcaster{
		msgChan:     make(chan BatchLogEntry, 100),
		subscribers: make(map[chan<- BatchLogEntry]bool),
	}
}

// Start 启动广播器
func (lb *LogBroadcaster) Start() {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	if lb.running {
		return
	}
	lb.msgChan = make(chan BatchLogEntry, 100)
	lb.subscribers = make(map[chan<- BatchLogEntry]bool)
	lb.running = true
	lb.wg.Add(1)
	go lb.broadcast()
}

// Stop 停止广播器
func (lb *LogBroadcaster) Stop() {
	lb.mu.Lock()
	if !lb.running {
		lb.mu.Unlock()
		return
	}
	lb.running = false
	close(lb.msgChan)
	lb.mu.Unlock()

	lb.wg.Wait()

	lb.mu.Lock()
	for sub := range lb.subscribers {
		delete(lb.subscribers, sub)
		close(sub)
	}
	lb.subscribers = make(map[chan<- BatchLogEntry]bool)
	lb.mu.Unlock()
}

// Send 发送日志条目
func (lb *LogBroadcaster) Send(entry BatchLogEntry) {
	lb.mu.RLock()
	running := lb.running
	lb.mu.RUnlock()
	if !running {
		return
	}
	select {
	case lb.msgChan <- entry:
	default:
	}
}

// Subscribe 订阅日志流
func (lb *LogBroadcaster) Subscribe() (chan BatchLogEntry, func()) {
	subscriber := make(chan BatchLogEntry, 50)
	lb.mu.Lock()
	lb.subscribers[subscriber] = true
	lb.mu.Unlock()

	unsubscribe := func() {
		lb.mu.Lock()
		_, exists := lb.subscribers[subscriber]
		if exists {
			delete(lb.subscribers, subscriber)
			lb.mu.Unlock()
			close(subscriber)
		} else {
			lb.mu.Unlock()
		}
	}
	return subscriber, unsubscribe
}

func (lb *LogBroadcaster) broadcast() {
	defer lb.wg.Done()
	for entry := range lb.msgChan {
		lb.mu.RLock()
		subs := make([]chan<- BatchLogEntry, 0, len(lb.subscribers))
		for sub := range lb.subscribers {
			subs = append(subs, sub)
		}
		lb.mu.RUnlock()
		for _, sub := range subs {
			select {
			case sub <- entry:
			default:
			}
		}
	}
}

// BatchOperation 单次批量操作
type BatchOperation struct {
	Type            BatchOperationType `json:"type"`
	Instances       []string           `json:"instances"`
	DelayBetween    time.Duration      `json:"-"`
	Status          string             `json:"status"` // running / completed / cancelled
	InstanceResults []*InstanceResult  `json:"instance_results"`

	ctx          context.Context
	cancel       context.CancelFunc
	skipChannels map[string]chan struct{}

	// done 在操作结束（完成/取消/panic）时关闭，供调用方等待批量操作跑完
	done chan struct{}

	logBroadcaster *LogBroadcaster
	logHistory     []BatchLogEntry
	mu             sync.RWMutex
}

// BatchManager 全局单例管理器
type BatchManager struct {
	current *BatchOperation
	mu      sync.Mutex
}

var globalManager *BatchManager

// Initialize 初始化全局 BatchManager
func Initialize() {
	globalManager = &BatchManager{}
}

// GetGlobalManager 获取全局 BatchManager
func GetGlobalManager() *BatchManager {
	return globalManager
}

// StartOperation 创建并启动批量操作
func (bm *BatchManager) StartOperation(opType BatchOperationType, instances []string, delaySeconds int) (*BatchOperation, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bm.current != nil && bm.current.Status == "running" {
		return nil, fmt.Errorf("a batch operation is already running")
	}

	// 如果没有指定实例，获取所有可用实例
	if len(instances) == 0 {
		var err error
		instances, err = cfgpkg.GetAvailableInstances()
		if err != nil {
			return nil, fmt.Errorf("failed to get available instances: %w", err)
		}
	}

	if len(instances) == 0 {
		return nil, fmt.Errorf("no instances to operate on")
	}

	// 限制延迟范围
	if delaySeconds < 0 {
		delaySeconds = 0
	}
	if delaySeconds > 300 {
		delaySeconds = 300
	}

	ctx, cancel := context.WithCancel(context.Background())

	// 创建 skip channels
	skipChannels := make(map[string]chan struct{})
	for _, inst := range instances {
		skipChannels[inst] = make(chan struct{})
	}

	// 初始化实例结果
	results := make([]*InstanceResult, len(instances))
	for i, inst := range instances {
		results[i] = &InstanceResult{
			InstanceName: inst,
			Status:       InstancePending,
		}
	}

	logBroadcaster := NewLogBroadcaster()
	logBroadcaster.Start()

	op := &BatchOperation{
		Type:            opType,
		Instances:       instances,
		DelayBetween:    time.Duration(delaySeconds) * time.Second,
		Status:          "running",
		InstanceResults: results,
		ctx:             ctx,
		cancel:          cancel,
		skipChannels:    skipChannels,
		done:            make(chan struct{}),
		logBroadcaster:  logBroadcaster,
		logHistory:      make([]BatchLogEntry, 0, 50),
	}

	bm.current = op

	// 启动后台执行
	go bm.runBatchOperation(op)

	return op, nil
}

// IsRunning 检查是否有活跃操作
func (bm *BatchManager) IsRunning() bool {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	return bm.current != nil && bm.current.Status == "running"
}

// GetCurrent 获取当前操作
func (bm *BatchManager) GetCurrent() *BatchOperation {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	return bm.current
}

// CancelCurrent 取消当前操作
func (bm *BatchManager) CancelCurrent() bool {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if bm.current != nil && bm.current.Status == "running" {
		bm.current.cancel()
		return true
	}
	return false
}

// SkipInstance 跳过指定实例
func (bm *BatchManager) SkipInstance(instanceName string) bool {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if bm.current == nil || bm.current.Status != "running" {
		return false
	}
	bm.current.mu.Lock()
	defer bm.current.mu.Unlock()
	if ch, ok := bm.current.skipChannels[instanceName]; ok {
		select {
		case <-ch:
			// 已关闭（已跳过）
			return false
		default:
			close(ch)
			return true
		}
	}
	return false
}

// Shutdown 关闭时取消当前操作
func (bm *BatchManager) Shutdown() {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if bm.current != nil && bm.current.Status == "running" {
		bm.current.cancel()
	}
}

// sendLog 发送日志并保存历史
func (op *BatchOperation) sendLog(level, message, instanceName string) {
	entry := BatchLogEntry{
		Timestamp:    time.Now().UnixMilli(),
		Level:        level,
		Message:      message,
		InstanceName: instanceName,
	}
	op.mu.Lock()
	// 保留最近 50 条
	if len(op.logHistory) >= 50 {
		op.logHistory = op.logHistory[1:]
	}
	op.logHistory = append(op.logHistory, entry)
	op.mu.Unlock()

	op.logBroadcaster.Send(entry)
}

// setResult 更新实例结果状态
func (op *BatchOperation) setResult(instanceName string, status InstanceOpStatus, errMsg string) {
	op.mu.Lock()
	defer op.mu.Unlock()
	for _, r := range op.InstanceResults {
		if r.InstanceName == instanceName {
			r.Status = status
			r.Error = errMsg
			return
		}
	}
}

// runBatchOperation 执行批量操作主循环
func (bm *BatchManager) runBatchOperation(op *BatchOperation) {
	// 最先注册 = 最后执行：等状态、广播、单例都收拾干净了再放行等待方
	defer close(op.done)

	defer func() {
		op.mu.Lock()
		op.Status = "completed"
		op.mu.Unlock()
		op.logBroadcaster.Stop()

		bm.mu.Lock()
		if bm.current == op {
			bm.current = nil
		}
		bm.mu.Unlock()
	}()

	defer func() {
		if r := recover(); r != nil {
			logger.GetLogger().Errorf("Batch operation panic: %v", r)
			op.sendLog("error", fmt.Sprintf("Batch operation panic: %v", r), "")
		}
	}()

	// 通知批量操作开始
	opTypeStr := string(op.Type)
	totalInstances := len(op.Instances)
	realtime.BroadcastBatchOperationStarted(opTypeStr, totalInstances)
	op.sendLog("info", fmt.Sprintf("Batch %s started with %d instances", opTypeStr, totalInstances), "")

	var succeeded, failed int

	for i, instanceName := range op.Instances {
		// 检查取消
		select {
		case <-op.ctx.Done():
			op.sendLog("warning", "Batch operation cancelled", "")
			op.markRemainingFrom(i, InstanceCancelled, "cancelled")
			op.mu.Lock()
			op.Status = "cancelled"
			op.mu.Unlock()
			realtime.BroadcastBatchOperationCompleted(opTypeStr, succeeded, failed, totalInstances)
			op.sendLog("completed", fmt.Sprintf("[COMPLETED] %d/%d succeeded, %d failed (cancelled)", succeeded, totalInstances, failed), "")
			return
		default:
		}

		// 检查跳过
		op.mu.RLock()
		skipCh := op.skipChannels[instanceName]
		op.mu.RUnlock()
		select {
		case <-skipCh:
			op.setResult(instanceName, InstanceSkipped, "skipped by user")
			op.sendLog("warning", fmt.Sprintf("Instance '%s' skipped by user", instanceName), instanceName)
			continue
		default:
		}

		// 原子 CAS：预检并设置状态，失败时跳过该实例
		ok, casErr := batchDoCAS(instanceName, op.Type)
		if casErr != nil {
			op.setResult(instanceName, InstanceFailed, casErr.Error())
			op.sendLog("error", fmt.Sprintf("Instance '%s' CAS error: %v", instanceName, casErr), instanceName)
			failed++
			continue
		}
		if !ok {
			op.setResult(instanceName, InstanceSkipped, "operation not allowed in current state")
			op.sendLog("warning", fmt.Sprintf("Instance '%s' skipped: operation not allowed in current state", instanceName), instanceName)
			continue
		}

		// 执行操作
		op.setResult(instanceName, InstanceRunning, "")
		op.executeInstance(instanceName, op.Type)

		// 检查结果
		op.mu.RLock()
		var resultStatus InstanceOpStatus
		for _, r := range op.InstanceResults {
			if r.InstanceName == instanceName {
				resultStatus = r.Status
				break
			}
		}
		op.mu.RUnlock()

		if resultStatus == InstanceSuccess {
			succeeded++
		} else if resultStatus == InstanceFailed {
			failed++
		}

		done := succeeded + failed
		realtime.BroadcastBatchProgress(opTypeStr, done, totalInstances, instanceName)

		// 延迟等待（非最后一个）
		if op.DelayBetween > 0 && i < len(op.Instances)-1 {
			op.sendLog("info", fmt.Sprintf("Waiting %d seconds before next instance...", int(op.DelayBetween.Seconds())), "")
			select {
			case <-time.After(op.DelayBetween):
			case <-op.ctx.Done():
				// 下一轮循环会处理取消
			}
		}
	}

	// 完成通知
	realtime.BroadcastBatchOperationCompleted(opTypeStr, succeeded, failed, totalInstances)
	op.sendLog("completed", fmt.Sprintf("[COMPLETED] %d/%d succeeded, %d failed", succeeded, totalInstances, failed), "")
}

// executeInstance 执行单个实例操作（调用方已完成 CAS，此处传 WithStatePreset）
func (op *BatchOperation) executeInstance(instanceName string, opType BatchOperationType) {
	var err error
	actionVerb := ""

	switch opType {
	case BatchStart:
		actionVerb = "starting"
		op.sendLog("info", fmt.Sprintf("Starting instance '%s'...", instanceName), instanceName)
		err = instancepkg.StartServer(instanceName, instancepkg.WithStatePreset())

	case BatchStop:
		actionVerb = "stopping"
		op.sendLog("info", fmt.Sprintf("Stopping instance '%s'...", instanceName), instanceName)
		err = instancepkg.StopServer(instanceName, instancepkg.WithStatePreset())

	case BatchRestart:
		actionVerb = "restarting"
		op.sendLog("info", fmt.Sprintf("Restarting instance '%s'...", instanceName), instanceName)
		err = instancepkg.RestartServer(instanceName,
			instancepkg.WithStatePreset(),
			instancepkg.WithRestartStartupCompletion(func(string) {}), // 写 StatusRestarted 状态供 dispatcher 推送
		)
	}
	if err != nil {
		op.setResult(instanceName, InstanceFailed, err.Error())
		op.sendLog("error", fmt.Sprintf("Failed %s instance '%s': %v", actionVerb, instanceName, err), instanceName)
		return
	}

	op.setResult(instanceName, InstanceSuccess, "")
	op.sendLog("success", fmt.Sprintf("Instance '%s' %sed successfully", instanceName, actionVerb), instanceName)
}

// markRemainingCancelled 标记所有剩余实例为已取消
func (op *BatchOperation) markRemainingCancelled() {
	op.markRemainingFrom(0, InstanceCancelled, "cancelled")
}

// markRemainingFrom 从指定索引开始标记剩余实例
func (op *BatchOperation) markRemainingFrom(fromIndex int, status InstanceOpStatus, errMsg string) {
	op.mu.Lock()
	defer op.mu.Unlock()
	for i := fromIndex; i < len(op.InstanceResults); i++ {
		if op.InstanceResults[i].Status == InstancePending {
			op.InstanceResults[i].Status = status
			op.InstanceResults[i].Error = errMsg
		}
	}
}

// GetLogHistory 获取日志历史
func (op *BatchOperation) GetLogHistory() []BatchLogEntry {
	op.mu.RLock()
	defer op.mu.RUnlock()
	history := make([]BatchLogEntry, len(op.logHistory))
	copy(history, op.logHistory)
	return history
}

// GetLogBroadcaster 获取日志广播器
func (op *BatchOperation) GetLogBroadcaster() *LogBroadcaster {
	return op.logBroadcaster
}

// Done 返回一个在批量操作结束（完成/取消/panic）时关闭的 channel。
//
// 返回 channel 而非提供 Wait() 方法，是为了让调用方能把它和自己的 ctx 一起 select，
// 不至于在批量操作卡住时被无限期拖住。
func (op *BatchOperation) Done() <-chan struct{} {
	return op.done
}

// batchDoCAS 为批量操作做原子 CAS。成功（ok=true）时状态已设置，调用方应传 WithStatePreset。
func batchDoCAS(instanceName string, opType BatchOperationType) (bool, error) {
	switch opType {
	case BatchStart:
		return statepkg.CompareAndSwapInstanceState(instanceName,
			[]statepkg.InstanceStatus{
				statepkg.StatusStopped, statepkg.StatusStartFailed,
				statepkg.StatusStopFailed, statepkg.StatusRestartFailed, "",
			},
			statepkg.StatusStartStartInitialization)
	case BatchStop:
		return statepkg.CompareAndSwapInstanceState(instanceName,
			[]statepkg.InstanceStatus{statepkg.StatusStarted},
			statepkg.StatusStopping)
	case BatchRestart:
		return statepkg.CompareAndSwapInstanceState(instanceName,
			[]statepkg.InstanceStatus{statepkg.StatusStarted},
			statepkg.StatusRestarting)
	default:
		return false, fmt.Errorf("unknown batch operation type: %s", opType)
	}
}
