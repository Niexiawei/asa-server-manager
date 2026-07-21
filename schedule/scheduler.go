package schedule

import (
	"asa-server/batchmanage"
	"asa-server/logger"
	"asa-server/updatemanage"
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// tickInterval 调度循环的检查间隔。
// 规则最细的粒度是分钟（HH:mm），30s 足够保证不漏点，也不会白转太多圈。
const tickInterval = 30 * time.Second

// Scheduler 定时任务调度器。
//
// 任务在**同一个 goroutine 里串行执行**：定时更新和定时重启因此天然不会重叠，
// 不需要额外的锁去防它们互相踩。代价是一个跑得久的任务会推迟后面的任务，
// 对「更新 / 重启」这种本来就该串行的操作而言这是想要的行为。
type Scheduler struct {
	store *store

	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
}

var globalScheduler *Scheduler

// Initialize 初始化全局调度器并载入已保存的任务。
// 载入失败不致命（当成空列表继续），只记日志——配置坏了不该拖垮整个 API 服务。
func Initialize() error {
	s := &Scheduler{store: newStore()}

	if err := s.store.load(); err != nil {
		logger.GetLogger().Errorf("Failed to load schedules, starting with an empty list: %v", err)
	}

	globalScheduler = s
	return nil
}

// GetGlobalScheduler 获取全局调度器。
func GetGlobalScheduler() *Scheduler { return globalScheduler }

// Start 启动调度循环。
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.running = true

	// 启动时把所有已启用任务的 NextRunAt 归位（含「不补跑」处理）
	s.realignAll()

	go s.loop(ctx)
	logger.GetLogger().Info("Schedule scheduler started")
}

// Stop 停止调度循环。正在执行的任务会跑完，不会被打断。
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}
	s.cancel()
	s.running = false
	logger.GetLogger().Info("Schedule scheduler stopped")
}

// realignAll 把所有已启用任务的 NextRunAt 校正到未来。
//
// 进程停机期间错过的执行**不补跑**：直接推进到下一个未来时刻。
// 补跑意味着管理器一启动就可能把所有实例重启一遍，这不是用户想要的惊喜。
func (s *Scheduler) realignAll() {
	now := time.Now()

	for _, t := range s.store.List() {
		if !t.Enabled {
			continue
		}
		if t.NextRunAt != nil && t.NextRunAt.After(now) {
			continue
		}

		missed := t.NextRunAt != nil
		next, err := t.NextRun(now)
		if err != nil {
			logger.GetLogger().Errorf("Task '%s' has an invalid rule, skipping: %v", t.Name, err)
			continue
		}

		if missed {
			logger.GetLogger().Warnf(
				"Task '%s' missed its scheduled run at %s (process was down); skipping to %s",
				t.Name, t.NextRunAt.Format(time.RFC3339), next.Format(time.RFC3339),
			)
		}

		if err := s.store.mutate(t.ID, func(stored *Task) { stored.NextRunAt = &next }); err != nil {
			logger.GetLogger().Errorf("Failed to persist next run time for task '%s': %v", t.Name, err)
		}
	}
}

func (s *Scheduler) loop(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// tick 扫描一遍任务，执行所有到点的。
func (s *Scheduler) tick(ctx context.Context) {
	now := time.Now()

	for _, t := range s.store.List() {
		if !t.Enabled || t.NextRunAt == nil || t.NextRunAt.After(now) {
			continue
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		s.execute(ctx, t)

		// 以「现在」而非原定时刻为基准推进：任务本身可能跑了很久，
		// 用原定时刻推进会导致刚跑完就立刻又到点
		next, err := t.NextRun(time.Now())
		if err != nil {
			logger.GetLogger().Errorf("Task '%s' has an invalid rule: %v", t.Name, err)
			continue
		}
		if err := s.store.mutate(t.ID, func(stored *Task) { stored.NextRunAt = &next }); err != nil {
			logger.GetLogger().Errorf("Failed to persist next run time for task '%s': %v", t.Name, err)
		}
	}
}

// RunNow 立即执行一次任务，不影响它的 NextRunAt。
func (s *Scheduler) RunNow(id string) error {
	t, ok := s.store.Get(id)
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}

	go s.execute(context.Background(), t)
	return nil
}

// execute 执行一条任务并回写 LastRunAt / LastResult。
func (s *Scheduler) execute(ctx context.Context, t *Task) {
	startedAt := time.Now()
	logger.GetLogger().Infof("Running scheduled task '%s' (%s)", t.Name, t.Type)

	var err error
	switch t.Type {
	case TaskRestart:
		err = s.runRestart(ctx, t)
	case TaskUpdate:
		err = s.runUpdate(ctx)
	default:
		err = fmt.Errorf("未知的任务类型: %s", t.Type)
	}

	result := "成功"
	if err != nil {
		result = "失败: " + err.Error()
		logger.GetLogger().Errorf("Scheduled task '%s' failed: %v", t.Name, err)
	} else {
		logger.GetLogger().Infof("Scheduled task '%s' completed in %s", t.Name, time.Since(startedAt).Round(time.Second))
	}

	if mErr := s.store.mutate(t.ID, func(stored *Task) {
		stored.LastRunAt = &startedAt
		stored.LastResult = result
	}); mErr != nil {
		logger.GetLogger().Errorf("Failed to persist run result for task '%s': %v", t.Name, mErr)
	}
}

// runRestart 批量重启。空实例列表由 batchmanage 解释为「全部实例」。
func (s *Scheduler) runRestart(ctx context.Context, t *Task) error {
	op, err := batchmanage.GetGlobalManager().StartOperation(batchmanage.BatchRestart, t.Instances, 0)
	if err != nil {
		return fmt.Errorf("failed to start batch restart: %w", err)
	}

	select {
	case <-op.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runUpdate 先停全部实例再更新。
//
// 停服不是顺手做的好事，而是硬前提：installer 在有实例存活时会直接拒绝更新，
// 不先停服的话定时更新到点必然失败。
func (s *Scheduler) runUpdate(ctx context.Context) error {
	op, err := batchmanage.GetGlobalManager().StartOperation(batchmanage.BatchStop, nil, 0)
	if err != nil {
		// 没有任何实例时 StartOperation 会报 "no instances to operate on"，
		// 这种情况下没什么可停的，直接进入更新
		logger.GetLogger().Warnf("Batch stop before scheduled update did not run: %v", err)
	} else {
		select {
		case <-op.Done():
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	done, started := updatemanage.GetGlobalManager().Start()
	if !started {
		logger.GetLogger().Warn("An update was already running; waiting for it to finish")
	}

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ---- 对外的任务增删改查，webapi 层使用 ----

func (s *Scheduler) ListTasks() []*Task { return s.store.List() }

func (s *Scheduler) GetTask(id string) (*Task, bool) { return s.store.Get(id) }

// AddTask 校验并新增任务，同时算好首次执行时间。
func (s *Scheduler) AddTask(t *Task) error {
	if err := t.Validate(); err != nil {
		return err
	}

	t.ID = newTaskID()

	if t.Enabled {
		next, err := t.NextRun(time.Now())
		if err != nil {
			return err
		}
		t.NextRunAt = &next
	}

	return s.store.Add(t)
}

// UpdateTask 校验并覆盖任务。规则可能变了，NextRunAt 需要重算。
func (s *Scheduler) UpdateTask(t *Task) error {
	if err := t.Validate(); err != nil {
		return err
	}

	existing, ok := s.store.Get(t.ID)
	if !ok {
		return fmt.Errorf("task not found: %s", t.ID)
	}

	// 执行历史属于任务的运行记录，不该被一次编辑抹掉
	t.LastRunAt = existing.LastRunAt
	t.LastResult = existing.LastResult

	if t.Enabled {
		next, err := t.NextRun(time.Now())
		if err != nil {
			return err
		}
		t.NextRunAt = &next
	} else {
		t.NextRunAt = nil
	}

	return s.store.Update(t)
}

func (s *Scheduler) DeleteTask(id string) error { return s.store.Delete(id) }

// ToggleTask 启用/停用任务。启用时重算 NextRunAt，停用时清空。
func (s *Scheduler) ToggleTask(id string, enabled bool) error {
	t, ok := s.store.Get(id)
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}

	var next *time.Time
	if enabled {
		n, err := t.NextRun(time.Now())
		if err != nil {
			return err
		}
		next = &n
	}

	return s.store.mutate(id, func(stored *Task) {
		stored.Enabled = enabled
		stored.NextRunAt = next
	})
}

// newTaskID 生成任务 ID。纳秒时间戳足够区分人工创建的任务，不值得引入 uuid 依赖。
func newTaskID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}
