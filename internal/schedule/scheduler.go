package schedule

import (
	"asa-server/internal/batchmanage"
	instancepkg "asa-server/internal/instance"
	"asa-server/internal/logger"
	procpkg "asa-server/internal/process"
	"asa-server/internal/realtime"
	"asa-server/internal/updatemanage"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// tickInterval 调度循环的检查间隔。
// 规则最细的粒度是分钟（HH:mm），30s 足够保证不漏点，也不会白转太多圈。
const tickInterval = 30 * time.Second

const (
	// forceStopTimeout 是定时更新前强停实例后，等待进程真正退出的上限。
	// 给得比较宽是因为 ARK 收到关闭请求后还要存档；等不到就让本次更新失败，
	// 下一个执行点会重试，好过带着存活实例去更新然后被 installer 拒绝。
	forceStopTimeout = 2 * time.Minute
	stopPollInterval = 3 * time.Second

	// restoreStartDelaySeconds 是更新后恢复启动时，实例之间的间隔秒数。
	// 批量启动本来就是串行的（StartServer 要等到启动检测完成才返回），天然有间隔，
	// 所以默认不额外等；机器吃力时把这里调大即可。
	restoreStartDelaySeconds = 0
)

// Scheduler 定时任务调度器。
//
// 任务在**同一个 goroutine 里串行执行**：定时更新和定时重启因此天然不会重叠，
// 不需要额外的锁去防它们互相踩。代价是一个跑得久的任务会推迟后面的任务，
// 对「更新 / 重启」这种本来就该串行的操作而言这是想要的行为。
type Scheduler struct {
	store *store
	logs  *logStore

	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
}

var globalScheduler *Scheduler

// Initialize 初始化全局调度器并载入已保存的任务。
// 载入失败不致命（当成空列表继续），只记日志——配置坏了不该拖垮整个 API 服务。
func Initialize() error {
	s := &Scheduler{store: newStore(), logs: newLogStore()}

	if err := s.store.load(); err != nil {
		logger.GetLogger().Errorf("Failed to load schedules, starting with an empty list: %v", err)
	}
	// 日志载入自带容错，坏了只记 WARN 并按空列表继续
	s.logs.load()

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

		s.execute(ctx, t, TriggerSchedule)

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

	go s.execute(context.Background(), t, TriggerManual)
	return nil
}

// execute 执行一条任务，回写 LastRunAt / LastResult 并追加一条执行记录。
//
// LastRunAt / LastResult 保留而非从日志现算：它们是列表页「上次执行」列的数据源，
// 每次渲染都去扫一遍记录不值当。两者与日志在同一处写入，不会漂移。
func (s *Scheduler) execute(ctx context.Context, t *Task, trigger TriggerSource) {
	startedAt := time.Now()
	logger.GetLogger().Infof("Running scheduled task '%s' (%s, %s)", t.Name, t.Type, trigger)

	var (
		summary string
		err     error
	)
	switch t.Type {
	case TaskRestart:
		summary, err = s.runRestart(ctx, t)
	case TaskUpdate:
		summary, err = s.runUpdate(ctx, t)
	default:
		err = fmt.Errorf("未知的任务类型: %s", t.Type)
	}

	duration := time.Since(startedAt)

	result := "成功"
	message := summary
	if err != nil {
		result = "失败: " + err.Error()
		message = err.Error()
		logger.GetLogger().Errorf("Scheduled task '%s' failed: %v", t.Name, err)
	} else {
		logger.GetLogger().Infof("Scheduled task '%s' completed in %s", t.Name, duration.Round(time.Second))
	}

	if mErr := s.store.mutate(t.ID, func(stored *Task) {
		stored.LastRunAt = &startedAt
		stored.LastResult = result
	}); mErr != nil {
		logger.GetLogger().Errorf("Failed to persist run result for task '%s': %v", t.Name, mErr)
	}

	record := &RunRecord{
		ID:         newRunRecordID(),
		TaskID:     t.ID,
		TaskName:   t.Name,
		TaskType:   t.Type,
		Trigger:    trigger,
		StartedAt:  startedAt,
		DurationMs: duration.Milliseconds(),
		Success:    err == nil,
		Message:    message,
	}

	// 日志落盘失败不影响任务本身的成败，只记一条 ERROR
	if aErr := s.logs.append(record); aErr != nil {
		logger.GetLogger().Errorf("Failed to persist run log for task '%s': %v", t.Name, aErr)
	}

	realtime.BroadcastScheduleRun(record.TaskName, record.Success, map[string]any{
		"id":          record.ID,
		"task_id":     record.TaskID,
		"task_name":   record.TaskName,
		"task_type":   string(record.TaskType),
		"trigger":     string(record.Trigger),
		"started_at":  record.StartedAt.UnixMilli(),
		"duration_ms": record.DurationMs,
		"success":     record.Success,
		"message":     record.Message,
	})
}

// awaitBatch 等一次批量操作跑完，任务 ctx 先结束时把它一并取消。
//
// 不能只是 return：批量操作的 ctx 派生自 Background，任务退出后它会继续跑完整轮倒计时，
// 一直占着 batchmanage 的单例，把下一次调度顶成 ErrOperationInProgress。
// 取消后仍等 Done()，确保单例确实被释放了再返回。
func awaitBatch(ctx context.Context, op *batchmanage.BatchOperation) error {
	select {
	case <-op.Done():
		return nil
	case <-ctx.Done():
		op.Cancel()
		<-op.Done()
		return ctx.Err()
	}
}

// runRestart 批量重启。空实例列表由 batchmanage 解释为「全部实例」。
// 成功时返回一句摘要，供执行日志展示。
func (s *Scheduler) runRestart(ctx context.Context, t *Task) (string, error) {
	op, err := batchmanage.GetGlobalManager().StartOperation(
		batchmanage.BatchRestart, t.Instances, 0, t.CountdownConfig(),
	)
	if err != nil {
		return "", fmt.Errorf("failed to start batch restart: %w", err)
	}

	if err := awaitBatch(ctx, op); err != nil {
		return "", err
	}

	// 批量操作本身「完成」了不代表每个实例都重启成功。
	// 不看结果的话，一半实例起不来的任务照样记成「成功」。
	var failed []string
	succeeded := 0
	skipped := 0
	for _, r := range op.InstanceResults {
		switch r.Status {
		case batchmanage.InstanceFailed:
			failed = append(failed, r.InstanceName)
		case batchmanage.InstanceSuccess:
			succeeded++
		default:
			// skipped / cancelled：状态不允许、用户跳过、倒计时被取消
			skipped++
		}
	}
	if len(failed) > 0 {
		return "", fmt.Errorf("%d/%d 个实例重启失败：%s",
			len(failed), len(op.InstanceResults), strings.Join(failed, "、"))
	}

	summary := fmt.Sprintf("已重启 %d 个实例", succeeded)
	if skipped > 0 {
		summary += fmt.Sprintf("，跳过 %d 个", skipped)
	}
	return summary, nil
}

// runUpdate 先停全部实例，更新，再把本次任务亲手停掉的那批原样拉起来。
//
// 停服不是顺手做的好事，而是硬前提：installer 在有实例存活时会直接拒绝更新。
// 而停完不管才是真正的坑——实例状态被写成 stopped 之后，再挂一个定时重启任务也救不回来：
// 重启的预检（IsStoppable）和 CAS 都只接受 started，停着的实例会被整批 skip 掉。
// 所以「谁把它停的谁负责起回来」这件事只能由这里做。
func (s *Scheduler) runUpdate(ctx context.Context, t *Task) (string, error) {
	// stoppedByTask 记录本次任务真正停掉的实例，按停服顺序保存，更新后按同样顺序拉回来。
	// 只记「我们停的」而不是「所有实例」：任务开始前本来就停着的实例，
	// 不该被一次定时更新顺手拉起来。
	var stoppedByTask []string

	// 倒计时作用于这一步的停服：半夜自动更新同样要提前通知玩家
	op, err := batchmanage.GetGlobalManager().StartOperation(
		batchmanage.BatchStop, nil, 0, t.CountdownConfig(),
	)
	switch {
	case errors.Is(err, batchmanage.ErrNoInstances):
		// 一个实例都没有，没什么可停的，直接进入更新
		logger.GetLogger().Info("No instances to stop before scheduled update")
	case err != nil:
		// 尤其是 ErrOperationInProgress：此时实例多半正活着，硬着头皮更新必被拒
		return "", fmt.Errorf("更新前的批量停服未能启动: %w", err)
	default:
		if err := awaitBatch(ctx, op); err != nil {
			return "", err
		}
		for _, r := range op.InstanceResults {
			if r.Status == batchmanage.InstanceSuccess {
				stoppedByTask = append(stoppedByTask, r.InstanceName)
			}
		}
	}

	// 批量停服的 CAS 只接受 started 状态，处于 starting / start_initialization_successful /
	// stop_failed 等状态的实例会被 skipped——进程还活着，更新随后就会被 installer 拒绝。
	// 这里补一刀强停兜底。
	if alive := procpkg.ListAliveInstances(); len(alive) > 0 {
		logger.GetLogger().Warnf(
			"Instances still alive after batch stop (skipped by CAS): %s; force stopping",
			strings.Join(alive, "、"),
		)
		for _, name := range alive {
			if err := instancepkg.ForceStopServer(name); err != nil {
				logger.GetLogger().Errorf("Failed to force stop instance '%s': %v", name, err)
			}
			// 强停的同样算「被本次任务停掉」：它们进程活着就说明本来在跑，
			// 只是状态记录没跟上。漏掉的话更新完就再没人负责把它们起回来。
			// 去重是因为 StopServer 返回时进程可能仍在退出中，会与上面那批重叠。
			stoppedByTask = appendUnique(stoppedByTask, name)
		}
	}

	if alive := waitInstancesStopped(ctx, forceStopTimeout); len(alive) > 0 {
		// 更新做不成了，但已经被停下来的那批是被本次任务连累的，得还回去
		restored, restoreErr := restoreInstances(ctx, excludeNames(stoppedByTask, alive))
		return "", fmt.Errorf("以下实例无法停止，更新已取消：%s%s",
			strings.Join(alive, "、"), restoreNote(restored, restoreErr))
	}

	mgr := updatemanage.GetGlobalManager()
	done, started := mgr.Start()
	if !started {
		logger.GetLogger().Warn("An update was already running; waiting for it to finish")
	}

	select {
	case <-done:
		// 更新的失败只体现在 Result() 里：run() 内部把错误发给 SSE 订阅者后就正常收尾，
		// 只等 done 关闭会把每一次失败都记成「成功」
		updateErr := mgr.Result()

		// 更新失败也要恢复：把服务器一直停着比更新没做成更糟，
		// 下一个执行点可能是 24 小时之后
		restored, restoreErr := restoreInstances(ctx, stoppedByTask)

		switch {
		case updateErr != nil:
			return "", fmt.Errorf("%w%s", updateErr, restoreNote(restored, restoreErr))
		case restoreErr != nil:
			return "", fmt.Errorf("更新完成，但恢复启动失败：%w", restoreErr)
		case len(stoppedByTask) == 0:
			return "无实例需停止，更新完成", nil
		default:
			return fmt.Sprintf("已停止 %d 个实例并完成更新，已重新启动 %d 个",
				len(stoppedByTask), restored), nil
		}
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// restoreInstances 把 names 里的实例重新拉起来，返回成功启动的数量。
//
// 用 BatchStart 而不是 BatchRestart：重启的预检与 CAS 都只接受 started，
// 对着已经停掉的实例发重启，整批都会被 skip 掉（这正是「更新任务后面挂个重启任务」
// 救不回来的原因）。
func restoreInstances(ctx context.Context, names []string) (int, error) {
	if len(names) == 0 {
		return 0, nil
	}
	// 调度器正在停止（或任务已被取消）：这时候再拉起实例只会被 awaitBatch 立刻掐断，
	// 白留一批 start_initialization 的中间状态
	if ctx.Err() != nil {
		return 0, fmt.Errorf("任务已取消，%d 个实例未恢复启动：%s",
			len(names), strings.Join(names, "、"))
	}

	logger.GetLogger().Infof("Restoring %d instance(s) stopped by the scheduled update: %s",
		len(names), strings.Join(names, "、"))

	op, err := batchmanage.GetGlobalManager().StartOperation(
		batchmanage.BatchStart, names, restoreStartDelaySeconds, nil,
	)
	if err != nil {
		// 尤其是 ErrOperationInProgress：更新期间有人从 UI 发起了别的批量操作。
		// 不能吞——吞了就等于实例悄悄留在停止状态，和改动之前一样
		return 0, fmt.Errorf("恢复启动未能发起: %w", err)
	}
	if err := awaitBatch(ctx, op); err != nil {
		return 0, err
	}

	restored := 0
	var failed []string
	for _, r := range op.InstanceResults {
		switch r.Status {
		case batchmanage.InstanceSuccess:
			restored++
		case batchmanage.InstanceFailed:
			failed = append(failed, r.InstanceName)
		}
		// skipped：CAS 拒绝，通常是这台已经被别人启动了，既不算成功也不算失败
	}
	if len(failed) > 0 {
		return restored, fmt.Errorf("%d/%d 个实例启动失败：%s",
			len(failed), len(names), strings.Join(failed, "、"))
	}
	return restored, nil
}

// restoreNote 把恢复结果拼成一句可以挂在别的消息后面的补充说明，无事可说时返回空串。
func restoreNote(restored int, err error) string {
	switch {
	case err != nil:
		return fmt.Sprintf("（恢复启动失败：%v）", err)
	case restored > 0:
		return fmt.Sprintf("（已恢复启动 %d 个实例）", restored)
	default:
		return ""
	}
}

// appendUnique 追加一个尚未出现过的名字，保持首次出现的顺序。
// 实例数量在几十的量级，线性查找足够，不值得为它建一个 map。
func appendUnique(names []string, name string) []string {
	if slices.Contains(names, name) {
		return names
	}
	return append(names, name)
}

// excludeNames 从 names 中剔除 exclude 里出现过的名字，保持原顺序。
func excludeNames(names, exclude []string) []string {
	if len(exclude) == 0 {
		return names
	}
	drop := make(map[string]struct{}, len(exclude))
	for _, n := range exclude {
		drop[n] = struct{}{}
	}
	kept := make([]string, 0, len(names))
	for _, n := range names {
		if _, ok := drop[n]; !ok {
			kept = append(kept, n)
		}
	}
	return kept
}

// waitInstancesStopped 等所有实例进程真正消失，返回超时时仍存活的实例名。
//
// ForceStopServer 用的 taskkill 只是把关闭请求发出去就返回，ARK 收到后还要存档退出，
// 立刻复检必然误判成「停不掉」。轮询的判据复用 ListAliveInstances（端口 + PID 双重判断）。
func waitInstancesStopped(ctx context.Context, timeout time.Duration) []string {
	deadline := time.Now().Add(timeout)

	for {
		alive := procpkg.ListAliveInstances()
		if len(alive) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return alive
		}

		select {
		case <-ctx.Done():
			return alive
		case <-time.After(stopPollInterval):
		}
	}
}

// ---- 对外的任务增删改查，webapi 层使用 ----

// ListRunLogs 返回执行记录，新的在前。
// taskID 为空表示不过滤；limit <= 0 用默认条数。
// 第二个返回值是过滤后、截断前的总数。
func (s *Scheduler) ListRunLogs(taskID string, limit int) ([]*RunRecord, int) {
	return s.logs.list(taskID, limit)
}

// ClearRunLogs 清空全部执行记录。
func (s *Scheduler) ClearRunLogs() error { return s.logs.clear() }

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
