package batchmanage

import (
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/countdown"
	instancepkg "asa-server/internal/instance"
	"asa-server/internal/realtime"
	statepkg "asa-server/internal/state"
	"asa-server/pkg/logger"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// BatchOperationType 批量操作类型
type BatchOperationType string

// BatchOriginKind 说明这一轮批量是谁发起的。
//
// 加它的直接原因：schedule 会自动发起停服/启动批量，用户看到操作弹窗自己动起来时，
// 必须能一眼看出是谁干的，否则唯一合理的反应就是恐慌点取消。
type BatchOriginKind string

const (
	OriginUser              BatchOriginKind = "user"                // 用户在 UI 上发起
	OriginScheduleRestart   BatchOriginKind = "schedule_restart"    // 定时重启任务
	OriginScheduleUpdate    BatchOriginKind = "schedule_update"     // 定时更新任务的停服阶段
	OriginUpdateRestore     BatchOriginKind = "update_restore"      // 更新后自动恢复启动
	OriginManualRestore     BatchOriginKind = "manual_restore"      // 用户确认的待恢复启动
	OriginTaskCancelRestore BatchOriginKind = "task_cancel_restore" // 取消定时任务后的状态回滚
)

// BatchOrigin 批量操作的来源。Label 面向用户，直接显示在操作弹窗标题和日志里，
// 所以要带上具体是哪个任务——只说「定时任务」在有多个任务时等于没说。
type BatchOrigin struct {
	Kind  BatchOriginKind `json:"kind"`
	Label string          `json:"label"`
}

// UserOrigin 是最常见的来源：用户在 UI 上手动发起。
func UserOrigin() BatchOrigin {
	return BatchOrigin{Kind: OriginUser, Label: "手动批量操作"}
}

const (
	BatchStart   BatchOperationType = "start"
	BatchStop    BatchOperationType = "stop"
	BatchRestart BatchOperationType = "restart"
)

// StartOperation 拒绝启动的两种原因。做成哨兵是因为调用方需要区分它们：
// 「没有实例可操作」对定时更新而言无害（本来就没什么可停的），
// 「已有批量操作在跑」则意味着一堆实例还活着，继续往下走必然失败。
var (
	ErrOperationInProgress = errors.New("a batch operation is already running")
	ErrNoInstances         = errors.New("no instances to operate on")
)

// BatchOperationRequest POST 请求体
type BatchOperationRequest struct {
	Instances []string `json:"instances"`

	// DelaySeconds 是**实例之间**的间隔，防止同时拉起多个实例把机器压垮。
	// 与下面的 Countdown 含义不重叠，两者可以同时使用。
	DelaySeconds int `json:"delay_seconds"`

	// Countdown 是**操作开始前**给玩家的预告时间（秒），0 = 不倒计时。
	Countdown     int    `json:"countdown"`
	NotifyPoints  []int  `json:"notify_points"`
	NotifyMessage string `json:"notify_message"`
	NotifyCommand string `json:"notify_command"`
}

// CountdownConfig 把请求里的倒计时字段转成 countdown 包的配置。
// Countdown 为 0 时返回 nil，表示不倒计时。
func (r *BatchOperationRequest) CountdownConfig() *countdown.Config {
	return countdown.FromSeconds(r.Countdown, r.NotifyPoints, r.NotifyMessage, r.NotifyCommand)
}

// maxLogHistory 单次批量操作保留的日志条数上限。
// 前端刷新页面后依赖这份历史回放，20 实例的批量约产生 60+ 条日志，
// 上限过低会导致刷新后看不到开头部分。
const maxLogHistory = 500

// InstanceOpStatus 单实例操作结果状态
type InstanceOpStatus string

const (
	InstancePending InstanceOpStatus = "pending"
	// InstanceSkipRequested 用户已请求跳过，等待主循环轮到该实例后转为 InstanceSkipped
	InstanceSkipRequested InstanceOpStatus = "skip_requested"
	InstanceRunning       InstanceOpStatus = "running"
	InstanceSuccess       InstanceOpStatus = "success"
	InstanceFailed        InstanceOpStatus = "failed"
	InstanceSkipped       InstanceOpStatus = "skipped"
	InstanceCancelled     InstanceOpStatus = "cancelled"
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

// Subscribe 订阅日志流。
//
// ok 为 false 表示广播器已经停止（进程正在退出），此时不要拿这个通道去 select——
// 不会有任何东西到达，也不会有人来关它，handler 会一直挂到客户端断开。
// running 的读写都在 lb.mu 之下，所以「Stop 跑完之后才订阅」这个竞态在这里就被关掉了。
func (lb *LogBroadcaster) Subscribe() (chan BatchLogEntry, func(), bool) {
	subscriber := make(chan BatchLogEntry, 50)
	lb.mu.Lock()
	if !lb.running {
		lb.mu.Unlock()
		return nil, func() {}, false
	}
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
	return subscriber, unsubscribe, true
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
	Origin          BatchOrigin        `json:"origin"`
	Instances       []string           `json:"instances"`
	DelayBetween    time.Duration      `json:"-"`
	Status          string             `json:"status"` // running / completed / cancelled
	InstanceResults []*InstanceResult  `json:"instance_results"`

	// cancelledAll 记录这一轮批量是否被**整体**取消（op.Cancel() / CancelCurrent）。
	//
	// 不能靠扫 InstanceResults 里有没有 InstanceCancelled 来推断：单台实例的倒计时
	// 被取消时也会写这个状态（见 runCountdownPhase 对单台的处理），那属于「放过这一台」，
	// 整批仍在正常执行，两者必须分开，否则调用方会把「跳过一台」误判成「整批作废」。
	cancelledAll atomic.Bool

	ctx          context.Context
	cancel       context.CancelFunc
	skipChannels map[string]chan struct{}

	// skipOnce 保证每个实例的跳过通道只被 close 一次。
	// 用户主动跳过与倒计时被取消是两条路径，可能先后落在同一个实例上
	// （倒计时期间先点了跳过，随后又取消了这台的倒计时），裸 close 会 panic。
	skipOnce map[string]*sync.Once

	// done 在操作结束（完成/取消/panic）时关闭，供调用方等待批量操作跑完
	done chan struct{}

	// countdown 为 nil 表示不倒计时。见 runCountdownPhase 的编排说明。
	countdown *countdown.Config

	// countdownTargets 是通过预检、值得播报倒计时的实例。
	// 与 Instances 的差集在 StartOperation 阶段就被标成 skipped：
	// 对着已经停止的实例倒计时既发不出公告，也只会白占住单例锁。
	countdownTargets []string

	logBroadcaster *LogBroadcaster
	logHistory     []BatchLogEntry
	mu             sync.RWMutex
}

// BatchManager 全局单例管理器
type BatchManager struct {
	current *BatchOperation
	// last 是最近一次已结束的操作，仅供前端回放日志历史。
	// 下一次批量开始时被覆盖，常驻开销是一个 op 对象 + 至多 maxLogHistory 条日志。
	last *BatchOperation
	mu   sync.Mutex

	// logBroadcaster 全程存活，不随单次操作创建/销毁。
	// SSE 客户端（批量操作弹窗）连上后要跨多次批量操作持续收日志：
	// 广播器要是随操作一起拆掉，订阅者就会被踢下线，前端只能靠重连补救——
	// 而 EventSource 分不清「正常收尾」和「连接抖断」，那就成了每 3 秒一次的重连风暴。
	logBroadcaster *LogBroadcaster
}

var globalManager *BatchManager

// Initialize 初始化全局 BatchManager
func Initialize() {
	lb := NewLogBroadcaster()
	lb.Start()
	globalManager = &BatchManager{logBroadcaster: lb}
}

// GetLogBroadcaster 返回全程存活的日志广播器。
func (bm *BatchManager) GetLogBroadcaster() *LogBroadcaster {
	return bm.logBroadcaster
}

// GetGlobalManager 获取全局 BatchManager
func GetGlobalManager() *BatchManager {
	return globalManager
}

// StartOperation 创建并启动批量操作。
//
// origin 是位置参数而不是可选项：这样旧调用点不改也能编译的话，会留下一批
// Kind 为空的批量，而「不知道是谁发起的」正是引入 BatchOrigin 要消灭的状态。
// 位置参数强制每个调用点各自表态，编译器替我们查漏。
func (bm *BatchManager) StartOperation(
	opType BatchOperationType,
	instances []string,
	delaySeconds int,
	cdCfg *countdown.Config,
	origin BatchOrigin,
) (*BatchOperation, error) {
	// 廉价快路径：正在跑就别白做下面的预检。不是权威判断，锁内还会复查一次
	if bm.IsRunning() {
		return nil, ErrOperationInProgress
	}

	// —— 以下准备工作一律不持 bm.mu ——
	// 预检要跑 netstat，占着锁会把 /api/batch/status 的轮询一起拖住

	// 配错的倒计时要在这里就拦下，不能等阶段一跑起来才发现点位永远触发不到
	if err := cdCfg.Validate(); err != nil {
		return nil, err
	}

	// 启动不需要预告玩家：没人在线可通知
	if opType == BatchStart {
		cdCfg = nil
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
		return nil, ErrNoInstances
	}

	// 限制延迟范围
	if delaySeconds < 0 {
		delaySeconds = 0
	}
	if delaySeconds > 300 {
		delaySeconds = 300
	}

	// 初始化实例结果，并在倒计时之前就把不可操作的实例剔除。
	// 放在这里而不是留给阶段二的 CAS：CAS 在倒计时**之后**才跑，
	// 等它发现实例早就停了，一整轮倒计时已经白烧完，单例也白占了那么久。
	results := make([]*InstanceResult, len(instances))
	countdownTargets := make([]string, 0, len(instances))
	for i, inst := range instances {
		results[i] = &InstanceResult{
			InstanceName: inst,
			Status:       InstancePending,
		}
		ok, reason := operable(inst, opType)
		if !ok {
			results[i].Status = InstanceSkipped
			results[i].Error = reason
			continue
		}
		countdownTargets = append(countdownTargets, inst)
	}

	// —— 发布单例，以下需要持锁 ——
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bm.current != nil && bm.current.Status == "running" {
		return nil, ErrOperationInProgress
	}

	ctx, cancel := context.WithCancel(context.Background())

	// 创建 skip channels 及其一次性关闭守卫
	skipChannels := make(map[string]chan struct{})
	skipOnce := make(map[string]*sync.Once)
	for _, inst := range instances {
		skipChannels[inst] = make(chan struct{})
		skipOnce[inst] = new(sync.Once)
	}

	op := &BatchOperation{
		Type:             opType,
		Origin:           origin,
		Instances:        instances,
		DelayBetween:     time.Duration(delaySeconds) * time.Second,
		Status:           "running",
		InstanceResults:  results,
		ctx:              ctx,
		cancel:           cancel,
		skipChannels:     skipChannels,
		skipOnce:         skipOnce,
		done:             make(chan struct{}),
		countdown:        cdCfg,
		countdownTargets: countdownTargets,
		// 用 manager 的共享广播器，不新建：订阅者要跨操作存活
		logBroadcaster: bm.logBroadcaster,
		logHistory:     make([]BatchLogEntry, 0, 50),
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

// GetCurrentOrLast 返回进行中的操作，没有则返回最近一次已结束的操作。
// 仅用于日志回放，不要拿它做状态判断——那种场景请用 GetCurrent。
func (bm *BatchManager) GetCurrentOrLast() *BatchOperation {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if bm.current != nil {
		return bm.current
	}
	return bm.last
}

// IsRunning 该次操作是否仍在执行
func (op *BatchOperation) IsRunning() bool {
	op.mu.RLock()
	defer op.mu.RUnlock()
	return op.Status == "running"
}

// WasCancelled 报告这一轮批量是否被**整体**取消（而不是单台实例的倒计时被取消）。
// 只在 op 结束后（Done() 已关闭）读才有意义。
//
// 不能靠 op.Status == "cancelled" 判断：runBatchOperation 的收尾 defer 会把 Status
// 无条件改成 "completed"，"cancelled" 只在那一瞬间可见；cancelledAll 是一次性置位、
// 永不回退的标志，专为事后判断而设。
func (op *BatchOperation) WasCancelled() bool {
	return op.cancelledAll.Load()
}

// batchActionLabel 把操作类型翻成中文动词，供日志文案使用。
func batchActionLabel(opType BatchOperationType) string {
	switch opType {
	case BatchStart:
		return "启动"
	case BatchStop:
		return "停止"
	case BatchRestart:
		return "重启"
	default:
		return string(opType)
	}
}

// Cancel 取消这一次批量操作。
//
// 调用方自己的 ctx 结束时不能只是 return：批量操作的 ctx 派生自 Background，
// 不掐断的话它会继续跑完整轮倒计时，把单例一直占着，顶掉下一次调度。
// 与 BatchManager.CancelCurrent 的区别是它只作用于这一次操作，不会误伤已经换代的新操作。
func (op *BatchOperation) Cancel() {
	op.cancel()
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

// SkipInstance 请求跳过指定实例，返回是否成功及失败原因。
// 主循环只在轮到某个实例之前检查一次跳过信号，因此只有仍处于 pending 的实例可以跳过；
// 这里同时把意图写入 InstanceResults，使 /status 能立即反映出来。
func (bm *BatchManager) SkipInstance(instanceName string) (bool, string) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if bm.current == nil {
		return false, "当前没有进行中的批量操作"
	}
	op := bm.current

	// 注意：此处已持有 op.mu，不能调用 op.setResult（它会再次加锁），需内联修改
	op.mu.Lock()
	defer op.mu.Unlock()
	if op.Status != "running" {
		return false, "当前没有进行中的批量操作"
	}

	for _, r := range op.InstanceResults {
		if r.InstanceName != instanceName {
			continue
		}
		switch r.Status {
		case InstancePending:
			if _, ok := op.skipChannels[instanceName]; !ok {
				return false, "实例不在本次批量操作中"
			}
			op.signalSkipLocked(instanceName)
			r.Status = InstanceSkipRequested
			return true, ""
		case InstanceSkipRequested:
			return false, "该实例已请求跳过"
		case InstanceRunning:
			return false, "实例正在执行中，无法跳过"
		default:
			return false, "实例已处理完成，无法跳过"
		}
	}
	return false, "实例不在本次批量操作中"
}

// Shutdown 关闭时取消当前操作
func (bm *BatchManager) Shutdown() {
	bm.mu.Lock()
	if bm.current != nil && bm.current.Status == "running" {
		bm.current.cancel()
	}
	bm.mu.Unlock()

	// 广播器活到进程退出为止，收口只能在这里做。
	// 不关的话 SSE handler 会一直挂着，把 srv.Shutdown 的 30 秒宽限期整个占满——
	// Stop 会关掉所有订阅者通道，handler 随之退出。
	// 放在锁外：Stop 要等 broadcast goroutine 收尾，不该攥着 bm.mu 等。
	if bm.logBroadcaster != nil {
		bm.logBroadcaster.Stop()
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
	// 保留最近 maxLogHistory 条，供前端刷新页面后回放
	if len(op.logHistory) >= maxLogHistory {
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

// resultStatus 读取实例当前的结果状态，不在列表中返回空串。
func (op *BatchOperation) resultStatus(instanceName string) InstanceOpStatus {
	op.mu.RLock()
	defer op.mu.RUnlock()
	for _, r := range op.InstanceResults {
		if r.InstanceName == instanceName {
			return r.Status
		}
	}
	return ""
}

// skippedByPrecheck 快照出被预检剔除的实例。
// 此刻主循环还没开始跑，除了预检没人写过 InstanceSkipped，所以按状态筛选就够。
func (op *BatchOperation) skippedByPrecheck() []InstanceResult {
	op.mu.RLock()
	defer op.mu.RUnlock()
	var skipped []InstanceResult
	for _, r := range op.InstanceResults {
		if r.Status == InstanceSkipped {
			skipped = append(skipped, *r)
		}
	}
	return skipped
}

// signalSkip 让主循环跳过指定实例。
//
// 用户主动跳过与倒计时被取消都走这里：两条路径可能先后落在同一个实例上，
// Once 保证通道不会被 close 两次。
func (op *BatchOperation) signalSkip(instanceName string) {
	op.mu.Lock()
	defer op.mu.Unlock()
	op.signalSkipLocked(instanceName)
}

// signalSkipLocked 是 signalSkip 的已持锁版本，供 SkipInstance 在持锁期间调用。
func (op *BatchOperation) signalSkipLocked(instanceName string) {
	once, ok := op.skipOnce[instanceName]
	if !ok {
		return
	}
	ch := op.skipChannels[instanceName]
	once.Do(func() { close(ch) })
}

// runBatchOperation 执行批量操作主循环
func (bm *BatchManager) runBatchOperation(op *BatchOperation) {
	// 最先注册 = 最后执行：等状态、广播、单例都收拾干净了再放行等待方
	defer close(op.done)

	defer func() {
		op.mu.Lock()
		op.Status = "completed"
		op.mu.Unlock()

		// 注意：不要在这里 Stop 广播器。它归 manager 所有、全程存活，
		// 拆掉会把仍开着弹窗的 SSE 客户端一起踢下线

		bm.mu.Lock()
		if bm.current == op {
			// 存档而不是直接丢弃：前端在没有批量运行时也要能回放这一轮的日志
			bm.last = op
			bm.current = nil
		}
		bm.mu.Unlock()
	}()

	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("Batch operation panic: %v", r)
			op.sendLog("error", fmt.Sprintf("Batch operation panic: %v", r), "")
		}
	}()

	// 通知批量操作开始
	opTypeStr := string(op.Type)
	totalInstances := len(op.Instances)
	realtime.BroadcastBatchOperationStarted(opTypeStr, totalInstances, string(op.Origin.Kind), op.Origin.Label)
	startLog := fmt.Sprintf("批量%s开始，共 %d 个实例", batchActionLabel(op.Type), totalInstances)
	if op.Origin.Label != "" {
		startLog = fmt.Sprintf("[%s] %s", op.Origin.Label, startLog)
	}
	op.sendLog("info", startLog, "")

	// 预检剔除的实例在 StartOperation 里就标好了，这里补上日志：
	// 广播器那时还没启动，不补的话前端只看到实例凭空变成 skipped，没有原因
	for _, r := range op.skippedByPrecheck() {
		op.sendLog("warning", fmt.Sprintf("Instance '%s' skipped: %s", r.InstanceName, r.Error), r.InstanceName)
	}

	// 阶段一：倒计时统一前置
	if cancelled := op.runCountdownPhase(); cancelled {
		op.markRemainingCancelled()
		op.cancelledAll.Store(true)
		op.mu.Lock()
		op.Status = "cancelled"
		op.mu.Unlock()
		realtime.BroadcastBatchOperationCompleted(opTypeStr, 0, 0, totalInstances)
		op.sendLog("completed", "[COMPLETED] 倒计时被取消，批量操作未执行", "")
		return
	}

	// 阶段二：按原有串行逻辑逐个执行
	var succeeded, failed int

	for i, instanceName := range op.Instances {
		// 检查取消
		select {
		case <-op.ctx.Done():
			op.sendLog("warning", "批量操作已取消", "")
			op.markRemainingFrom(i, InstanceCancelled, "cancelled")
			op.cancelledAll.Store(true)
			op.mu.Lock()
			op.Status = "cancelled"
			op.mu.Unlock()
			realtime.BroadcastBatchOperationCompleted(opTypeStr, succeeded, failed, totalInstances)
			op.sendLog("completed", fmt.Sprintf("[COMPLETED] %d/%d succeeded, %d failed (cancelled)", succeeded, totalInstances, failed), "")
			return
		default:
		}

		// 预检已经判定不可操作的实例，原因比 CAS 的通用文案精确，别让下面把它盖掉
		if op.resultStatus(instanceName) == InstanceSkipped {
			continue
		}

		// 检查跳过
		op.mu.RLock()
		skipCh := op.skipChannels[instanceName]
		op.mu.RUnlock()
		select {
		case <-skipCh:
			// 倒计时被取消的实例已在阶段一标记为 cancelled，
			// 不要盖成「用户跳过」——那会丢掉真正的原因
			if op.resultStatus(instanceName) != InstanceCancelled {
				op.setResult(instanceName, InstanceSkipped, "skipped by user")
				op.sendLog("warning", fmt.Sprintf("Instance '%s' skipped by user", instanceName), instanceName)
			}
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
		switch op.resultStatus(instanceName) {
		case InstanceSuccess:
			succeeded++
		case InstanceFailed:
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

// runCountdownPhase 是批量操作的阶段一：向所有目标实例**并发**播报同一轮倒计时。
//
// 为什么不把倒计时塞进 executeInstance：那样 5 个实例 × 10 分钟就是 50 分钟，
// 而且最后一个实例的玩家要等到第 40 分钟才收到「还有 10 分钟」的通知。
// 统一前置之后总耗时 = 倒计时 + 原有串行耗时，且全服玩家的倒计时是对齐的。
//
// 单独取消某个实例的倒计时只放过那一台：它被接进已有的 skip 机制，
// 阶段二的主循环会像用户手动跳过一样把它略过，其余实例照常执行。
//
// 返回 true 表示整批中止（父 ctx 被取消，或所有实例都被逐个取消），
// 调用方不应继续执行阶段二。
func (op *BatchOperation) runCountdownPhase() bool {
	if !op.countdown.Enabled() {
		return false
	}

	// 目标全被预检剔除（例如所有实例本来就是停止的）：没人可通知，直接进阶段二。
	// 不早退的话这里会对着死实例空烧一整轮倒计时，公告发不出去、最后也停不掉，
	// 期间单例锁一直被占，下一次批量操作只会拿到 ErrOperationInProgress。
	if len(op.countdownTargets) == 0 {
		op.sendLog("info", "没有正在运行的实例，跳过倒计时", "")
		return false
	}

	action := countdown.ActionStop
	if op.Type == BatchRestart {
		action = countdown.ActionRestart
	}

	op.sendLog("info", fmt.Sprintf(
		"倒计时开始：%d 秒后%s %d 个实例",
		int(op.countdown.Total.Seconds()), action.Label(), len(op.countdownTargets),
	), "")

	res, err := countdown.Wait(op.ctx, op.countdownTargets, action, op.countdown)
	if err != nil {
		op.sendLog("warning", "倒计时被取消，批量操作未执行", "")
		return true
	}

	// 没走完倒计时的实例接进已有的 skip 机制，阶段二无需为此再加一条分支。
	// 「实例未运行」是 countdown 层的兜底命中（预检没拦住的漏网之鱼），
	// 与「用户取消」是两回事，标错了会把排查引到错误的方向
	notRunning := 0
	for _, name := range res.Cancelled {
		if errors.Is(res.Reason(name), countdown.ErrNotRunning) {
			op.setResult(name, InstanceSkipped, "实例未运行")
			op.signalSkip(name)
			op.sendLog("warning", fmt.Sprintf("实例 '%s' 未在运行，将跳过", name), name)
			notRunning++
			continue
		}
		op.setResult(name, InstanceCancelled, "倒计时被取消")
		op.signalSkip(name)
		op.sendLog("warning", fmt.Sprintf("实例 '%s' 的倒计时被取消，将跳过", name), name)
	}

	// 只有「用户把每一台都取消了」才算整批中止。
	// 兜底判出的「未运行」不算——那些实例本来就无事可做，让阶段二的 CAS 正常收场即可，
	// 否则会打出「倒计时被取消，批量操作未执行」这种把排查带偏的日志
	if res.AllCancelled(len(op.countdownTargets)) && notRunning == 0 {
		op.sendLog("warning", "所有实例的倒计时都被取消，批量操作未执行", "")
		return true
	}

	if len(res.Cancelled) > 0 {
		op.sendLog("info", fmt.Sprintf("倒计时结束，开始执行（跳过 %d 个已取消的实例）",
			len(res.Cancelled)), "")
	} else {
		op.sendLog("info", "倒计时结束，开始执行", "")
	}
	return false
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

// operable 是阶段一之前的预检：这台实例现在值不值得纳入本轮批量操作。
//
// 与阶段二的 batchDoCAS 不是重复——CAS 在倒计时之后才跑，只能事后把实例标成 skipped；
// 预检把判断提到倒计时之前，避免对着已停止的实例空播一轮公告。
// CAS 仍然保留：倒计时期间状态可能变化，那一层才是权威门禁。
//
// 启动一律放行：启动本来就没有倒计时，交给阶段二的 CAS 判即可。
func operable(instanceName string, opType BatchOperationType) (bool, string) {
	if opType == BatchStart {
		return true, ""
	}
	return instancepkg.IsStoppable(instanceName)
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
