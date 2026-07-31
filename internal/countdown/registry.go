package countdown

import (
	"asa-server/internal/realtime"
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrInProgress 表示该实例已经有一轮倒计时在跑。
	//
	// 拆分前这里是静默覆盖写：单实例倒计时与批量倒计时撞上同一个实例时，
	// 后者会顶掉前者的 cancel 函数，前一个 goroutine 就再也取消不掉了。
	ErrInProgress = errors.New("countdown already in progress for this instance")

	// ErrCancelled 表示倒计时被用户取消，目标动作不应执行。
	ErrCancelled = errors.New("countdown cancelled")

	// ErrNotRunning 表示实例并没有在运行，这一轮倒计时压根不该开始。
	// 与 ErrCancelled 区分开：它不是「用户反悔了」，而是「本来就没什么可通知、可停的」，
	// 调用方据此不应把实例状态回滚成 started。
	ErrNotRunning = errors.New("instance is not running")
)

// Status 是某个实例当前的倒计时状态，供 HTTP 查询。
type Status struct {
	InstanceName string `json:"instance_name"`
	Action       Action `json:"action"`
	Phase        string `json:"phase"`
	Remaining    int    `json:"remaining"`
}

type entry struct {
	action   Action
	phase    string
	deadline time.Time
	cancel   context.CancelFunc
}

var (
	mu     sync.RWMutex
	active = make(map[string]*entry)
)

// register 登记一轮倒计时。已有登记时返回 ErrInProgress，不覆盖。
func register(instanceName string, action Action, deadline time.Time, cancel context.CancelFunc) error {
	mu.Lock()
	defer mu.Unlock()

	if _, exists := active[instanceName]; exists {
		return ErrInProgress
	}

	active[instanceName] = &entry{
		action:   action,
		phase:    realtime.CountdownPhaseCounting,
		deadline: deadline,
		cancel:   cancel,
	}
	return nil
}

// release 清理实例的倒计时登记。对不存在的实例是空操作，可安全重复调用。
func release(instanceName string) {
	mu.Lock()
	delete(active, instanceName)
	mu.Unlock()
}

// Get 查询实例当前的倒计时状态。
func Get(instanceName string) (*Status, bool) {
	mu.RLock()
	defer mu.RUnlock()

	e, ok := active[instanceName]
	if !ok {
		return nil, false
	}

	remaining := 0
	if e.phase == realtime.CountdownPhaseCounting {
		if d := time.Until(e.deadline); d > 0 {
			remaining = int(d.Round(time.Second).Seconds())
		}
	}

	return &Status{
		InstanceName: instanceName,
		Action:       e.action,
		Phase:        e.phase,
		Remaining:    remaining,
	}, true
}

// Cancel 取消**该实例**正在进行的倒计时。
// 返回 false 表示该实例当前没有在倒计时，或已进入执行阶段（停止流程已开始，取消不了了）。
//
// 批量倒计时中每个实例持有独立的子 context，因此这里只影响这一个实例，
// 其余实例会照常走完倒计时；整批终止请走 batchmanage 的取消入口。
func Cancel(instanceName string) bool {
	mu.Lock()

	e, ok := active[instanceName]
	if !ok || e.phase != realtime.CountdownPhaseCounting {
		mu.Unlock()
		return false
	}
	cancel := e.cancel
	mu.Unlock()

	cancel()
	return true
}

// markPhase 更新登记表里的阶段并推送 WS。
func markPhase(instanceName string, fallbackAction Action, phase, message string) {
	mu.Lock()
	action := fallbackAction
	if e, ok := active[instanceName]; ok {
		e.phase = phase
		action = e.action
	}
	mu.Unlock()

	realtime.BroadcastCountdownEvent(instanceName, string(action), phase, message, 0)
}
