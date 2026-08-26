package batchmanage

import (
	"asa-server/internal/countdown"
	instancepkg "asa-server/internal/instance"
	"context"
	"sync"
	"testing"
	"time"
)

// newTestOperation 直接构造一个 running 状态的批量操作。
//
// 不走 StartOperation：那条路径会真的去跑实例的启停。
// 这里要验的只是跳过通道的关闭语义，与实例生命周期无关。
func newTestOperation(instances ...string) *BatchOperation {
	results := make([]*InstanceResult, len(instances))
	skipChannels := make(map[string]chan struct{}, len(instances))
	skipOnce := make(map[string]*sync.Once, len(instances))

	for i, name := range instances {
		results[i] = &InstanceResult{InstanceName: name, Status: InstancePending}
		skipChannels[name] = make(chan struct{})
		skipOnce[name] = new(sync.Once)
	}

	return &BatchOperation{
		Type:            BatchStop,
		Instances:       instances,
		Status:          "running",
		InstanceResults: results,
		skipChannels:    skipChannels,
		skipOnce:        skipOnce,
		done:            make(chan struct{}),
	}
}

// isSkipSignalled 报告实例的跳过通道是否已关闭。
func isSkipSignalled(op *BatchOperation, instanceName string) bool {
	select {
	case <-op.skipChannels[instanceName]:
		return true
	default:
		return false
	}
}

func TestSignalSkipIsIdempotent(t *testing.T) {
	const name = "inst-a"
	op := newTestOperation(name)

	// 裸 close 的话第二次就 panic 了
	op.signalSkip(name)
	op.signalSkip(name)
	op.signalSkip(name)

	if !isSkipSignalled(op, name) {
		t.Error("signalSkip 后跳过通道应已关闭")
	}
}

// 不在本次批量中的实例不应导致 panic（map 查不到时直接返回）
func TestSignalSkipUnknownInstance(t *testing.T) {
	op := newTestOperation("inst-a")
	op.signalSkip("not-in-this-batch")

	if isSkipSignalled(op, "inst-a") {
		t.Error("不应误伤本批中的其它实例")
	}
}

// 用户主动跳过与倒计时被取消是两条独立路径，可能先后落在同一个实例上。
// 这是 sync.Once 守卫要防的场景：用户在倒计时期间点了跳过，随后又取消了这台的倒计时。
func TestUserSkipThenCountdownCancel(t *testing.T) {
	const name = "inst-a"
	bm := &BatchManager{}
	op := newTestOperation(name)
	bm.current = op

	ok, reason := bm.SkipInstance(name)
	if !ok {
		t.Fatalf("pending 实例应可跳过，实际被拒: %s", reason)
	}
	if op.resultStatus(name) != InstanceSkipRequested {
		t.Errorf("状态应为 skip_requested，实际 %q", op.resultStatus(name))
	}

	// 倒计时随后被取消，走 runCountdownPhase 的那条路径
	op.setResult(name, InstanceCancelled, "倒计时被取消")
	op.signalSkip(name)

	if !isSkipSignalled(op, name) {
		t.Error("跳过通道应保持关闭")
	}
}

// 反向顺序：倒计时先被取消，用户随后又点了跳过。
// 此时状态已是 cancelled，SkipInstance 应直接拒绝而不是再关一次通道。
func TestCountdownCancelThenUserSkip(t *testing.T) {
	const name = "inst-a"
	bm := &BatchManager{}
	op := newTestOperation(name)
	bm.current = op

	// runCountdownPhase 的顺序：先写结果，再发跳过信号
	op.setResult(name, InstanceCancelled, "倒计时被取消")
	op.signalSkip(name)

	ok, reason := bm.SkipInstance(name)
	if ok {
		t.Error("已被倒计时取消的实例不应还能跳过")
	}
	if reason == "" {
		t.Error("拒绝时应给出原因")
	}

	// 阶段二的跳过分支据此判断不要把原因盖成「用户跳过」
	if got := op.resultStatus(name); got != InstanceCancelled {
		t.Errorf("状态应保持 cancelled，实际 %q", got)
	}
}

// 两条路径并发撞在同一个实例上。
//
// 没有 sync.Once 守卫时，signalSkip 与 SkipInstance 里的 close 会并发执行，
// 触发 "close of closed channel" panic —— 这正是本用例要钉住的。
// 建议配合 -race 运行。
func TestSkipChannelCloseIsConcurrencySafe(t *testing.T) {
	const name = "inst-a"
	bm := &BatchManager{}
	op := newTestOperation(name)
	bm.current = op

	const goroutines = 16
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // 让所有 goroutine 尽量同时进入临界区

			if i%2 == 0 {
				op.signalSkip(name) // 倒计时被取消
			} else {
				bm.SkipInstance(name) // 用户主动跳过
			}
		}(i)
	}

	close(start)
	wg.Wait()

	if !isSkipSignalled(op, name) {
		t.Error("跳过通道应已关闭")
	}
}

// 多实例时只影响目标实例，这是「取消一个不等于取消整批」的前提
func TestSignalSkipAffectsOnlyTarget(t *testing.T) {
	op := newTestOperation("inst-a", "inst-b", "inst-c")

	op.signalSkip("inst-b")

	if !isSkipSignalled(op, "inst-b") {
		t.Error("目标实例的跳过通道应已关闭")
	}
	for _, name := range []string{"inst-a", "inst-c"} {
		if isSkipSignalled(op, name) {
			t.Errorf("实例 %s 不应被波及", name)
		}
	}
}

func TestResultStatus(t *testing.T) {
	op := newTestOperation("inst-a", "inst-b")

	if got := op.resultStatus("inst-a"); got != InstancePending {
		t.Errorf("初始状态应为 pending，实际 %q", got)
	}

	op.setResult("inst-a", InstanceFailed, "boom")
	if got := op.resultStatus("inst-a"); got != InstanceFailed {
		t.Errorf("状态应为 failed，实际 %q", got)
	}
	if got := op.resultStatus("inst-b"); got != InstancePending {
		t.Errorf("不应波及其它实例，实际 %q", got)
	}

	if got := op.resultStatus("not-in-this-batch"); got != "" {
		t.Errorf("不在本批中的实例应返回空串，实际 %q", got)
	}
}

// newTestManager 构造一个带共享日志广播器的 manager，形态与 Initialize 一致。
// 广播器归 manager 所有、跨操作存活，所以它的生命周期挂在测试上而非单次操作上。
func newTestManager(t *testing.T) *BatchManager {
	t.Helper()
	lb := NewLogBroadcaster()
	lb.Start()
	t.Cleanup(lb.Stop)
	return &BatchManager{logBroadcaster: lb}
}

// newRunnableOperation 构造一个可以真的跑 runBatchOperation 的操作。
//
// 比 newTestOperation 多出 ctx 与日志广播器：主循环要读 ctx.Done()、要发日志，
// 缺一个就 nil 解引用。realtime 的广播在 hub 未初始化时是空操作，无需处理。
func newRunnableOperation(bm *BatchManager, cd *countdown.Config, instances ...string) *BatchOperation {
	op := newTestOperation(instances...)
	op.ctx, op.cancel = context.WithCancel(context.Background())
	op.countdown = cd
	op.logBroadcaster = bm.logBroadcaster
	return op
}

// 预检把实例全剔光时，阶段一必须直接退出。
//
// 这正是「所有服务器都已停止，却报 a batch operation is already running」的成因：
// 早退没了的话，这里会对着死实例空烧一整轮倒计时，期间单例锁一直被占着。
// 逻辑退化时本用例会卡满倒计时时长而超时，能直接抓到。
func TestCountdownPhaseSkippedWhenNoTargets(t *testing.T) {
	cd := countdown.FromSeconds(60, nil, "", "")
	if !cd.Enabled() {
		t.Fatal("测试前提：倒计时配置应为启用状态")
	}

	op := newRunnableOperation(newTestManager(t), cd, "inst-a", "inst-b")
	op.countdownTargets = nil

	done := make(chan bool, 1)
	go func() { done <- op.runCountdownPhase() }()

	select {
	case cancelled := <-done:
		if cancelled {
			t.Error("没有倒计时目标不等于整批被取消，阶段二应照常执行")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("阶段一没有早退，说明对着无目标的批次仍在跑倒计时")
	}
}

// 预检漏掉的实例由 countdown 层的兜底拦下（测试实例名不存在，判活必然为假）。
// 要点有二：整批不能因此中止（那些实例本来就无事可做），
// 且原因必须标成「实例未运行」而不是「倒计时被取消」——标错会把排查带偏。
func TestCountdownPhaseMarksNotRunningRatherThanCancelled(t *testing.T) {
	const name = "inst-a"
	op := newRunnableOperation(newTestManager(t), countdown.FromSeconds(60, nil, "", ""), name)
	op.countdownTargets = []string{name}

	done := make(chan bool, 1)
	go func() { done <- op.runCountdownPhase() }()

	select {
	case cancelled := <-done:
		if cancelled {
			t.Error("兜底判出的「未运行」不该让整批中止")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未运行的实例应被兜底立即拦下，不该真的跑倒计时")
	}

	if got := op.resultStatus(name); got != InstanceSkipped {
		t.Errorf("状态应为 skipped，实际 %q", got)
	}
	if got := op.InstanceResults[0].Error; got != "实例未运行" {
		t.Errorf("原因应为「实例未运行」，实际 %q", got)
	}
}

// 预检给出的原因比阶段二 CAS 的通用文案精确，主循环不能盖掉它。
// 全部实例都被预检剔除时，整批还应当立刻结束——单例锁不该被白占。
func TestPreSkippedResultKeepsReason(t *testing.T) {
	const name = "inst-a"

	bm := newTestManager(t)
	op := newRunnableOperation(bm, nil, name)
	op.setResult(name, InstanceSkipped, instancepkg.ReasonNotStarted)
	bm.current = op

	go bm.runBatchOperation(op)

	select {
	case <-op.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("全部实例都被预检剔除时，批量操作应立刻结束")
	}

	if got := op.resultStatus(name); got != InstanceSkipped {
		t.Errorf("状态应保持 skipped，实际 %q", got)
	}
	if got := op.InstanceResults[0].Error; got != instancepkg.ReasonNotStarted {
		t.Errorf("预检原因被覆盖了，实际 %q", got)
	}
	if bm.current != nil {
		t.Error("操作结束后应释放单例，否则下一次会拿到 ErrOperationInProgress")
	}
}

// 回归点：早期实现曾提议用「InstanceResults 里有没有 InstanceCancelled」来判断
// 整批是否被取消，但单台实例的倒计时被取消（runCountdownPhase 对单个目标的处理）
// 也会写这个状态，其余实例照常执行——那种判据会把「放过这一台」误报成「整批作废」。
// WasCancelled() 必须只反映 cancelledAll 这个显式标志，不能靠扫状态推断。
func TestWasCancelled_FalseAfterSingleInstanceCountdownCancel(t *testing.T) {
	op := newTestOperation("inst-a", "inst-b")
	op.setResult("inst-a", InstanceCancelled, "倒计时被取消")

	if op.WasCancelled() {
		t.Error("单台实例的倒计时被取消不应让 WasCancelled() 变为 true")
	}
}

// 批量操作因 ctx 被取消（对应 CancelCurrent / op.Cancel()）而整体中止时，
// WasCancelled() 必须为 true——这是 schedule 包据此判断「任务被取消」的唯一依据。
func TestWasCancelled_TrueWhenCtxCancelledMidLoop(t *testing.T) {
	bm := newTestManager(t)
	op := newRunnableOperation(bm, nil, "inst-a", "inst-b")
	op.cancel() // 模拟 CancelCurrent()：立刻让 op.ctx 结束
	bm.current = op

	go bm.runBatchOperation(op)

	select {
	case <-op.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("批量操作应当因 ctx 被取消而立刻收尾")
	}

	if !op.WasCancelled() {
		t.Error("op.ctx 被取消后 WasCancelled() 应为 true")
	}
}
