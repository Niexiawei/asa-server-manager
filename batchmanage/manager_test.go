package batchmanage

import (
	"asa-server/logger"
	"os"
	"sync"
	"testing"
)

// 生产代码在部分路径上直接调 logger.GetLogger()，未初始化时它返回 nil。
// 这里统一初始化到临时目录，避免把日志写进仓库。
func TestMain(m *testing.M) {
	logger.InitLoggerWithBaseDir(os.TempDir())
	os.Exit(m.Run())
}

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
