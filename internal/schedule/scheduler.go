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
	"sync/atomic"
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

	// restoreStartDelaySeconds 是恢复启动时，实例之间的间隔秒数。
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
	store   *store
	logs    *logStore
	pending *pendingStore

	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool

	// runs 是正在执行的任务登记表，按 runID（不是 taskID）索引：RunNow 是
	// `go s.execute(...)`，同一个任务完全可能有两次执行同时在飞，用 taskID
	// 做键会让后一次把前一次挤掉，取消时也分不清取消的是哪一次。
	runsMu sync.Mutex
	runs   map[string]*taskRun

	// restoreInFlight 是所有「恢复启动」路径（定时更新的自动恢复、任务取消后的
	// 状态回滚、用户手动确认待恢复现场）共用的一把闸。不能只依赖 batchmanage 的
	// ErrOperationInProgress——那只保护批量执行本身，保护不了「读名单 → 跑 →
	// 按名单收尾」这一整段：两个恢复动作各自读到同一份旧名单，前一个刚收尾、
	// 后一个又跑一遍，用户会看到批量日志把同一批实例又走了一轮（虽然per-instance
	// CAS 会让第二轮全部 skip，但足够让人心里一惊）。
	restoreInFlight atomic.Bool
}

var globalScheduler *Scheduler

// Initialize 初始化全局调度器并载入已保存的任务与待恢复现场。
// 载入失败不致命（当成空列表继续），只记日志——配置坏了不该拖垮整个 API 服务。
func Initialize() error {
	s := &Scheduler{
		store:   newStore(),
		logs:    newLogStore(),
		pending: newPendingStore(),
		runs:    make(map[string]*taskRun),
	}

	if err := s.store.load(); err != nil {
		logger.GetLogger().Errorf("Failed to load schedules, starting with an empty list: %v", err)
	}
	// 日志与待恢复现场载入都自带容错，坏了只记 WARN/ERROR 并按空状态继续
	s.logs.load()
	s.pending.load()

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

// Stop 停止调度循环。正在执行的任务会跑完，不会被打断——
// 各任务 ctx 的取消不会置位 taskRun.cancelled，因此不会触发状态回滚
// （见 runUpdate/runRestart 里对 run.cancelled 的判断）；已经停掉但还没
// 来得及恢复的实例会落进待恢复现场，下次启动后提示。
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
	run := newTaskRun(ctx, t, trigger)
	s.registerRun(run)
	defer s.unregisterRun(run.RunID)

	startedAt := run.StartedAt
	logger.GetLogger().Infof("Running scheduled task '%s' (%s, %s)", t.Name, t.Type, trigger)
	realtime.BroadcastScheduleRunStarted(run.RunID, t.ID, t.Name, string(t.Type), string(trigger))

	var (
		summary string
		err     error
	)
	switch t.Type {
	case TaskRestart:
		summary, err = s.runRestart(run, t)
	case TaskUpdate:
		summary, err = s.runUpdate(run, t)
	default:
		err = fmt.Errorf("未知的任务类型: %s", t.Type)
	}

	duration := time.Since(startedAt)

	// 取消必须与失败分开：前者是用户主动叫停，后者是任务没干成。
	// 混成一个 Success=false 会让「最近 7 天失败 5 次」这种统计彻底失真。
	outcome := OutcomeSuccess
	result := "成功"
	message := summary
	switch {
	case run.cancelled.Load():
		outcome = OutcomeCancelled
		if err != nil {
			message = err.Error()
		} else {
			message = run.CancelReason()
		}
		result = "已取消: " + message
		logger.GetLogger().Warnf("Scheduled task '%s' was cancelled: %s", t.Name, message)
	case err != nil:
		outcome = OutcomeFailed
		result = "失败: " + err.Error()
		message = err.Error()
		logger.GetLogger().Errorf("Scheduled task '%s' failed: %v", t.Name, err)
	default:
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
		Success:    outcome == OutcomeSuccess,
		Outcome:    outcome,
		Message:    message,
	}
	s.recordAndBroadcast(record)
}

// recordAndBroadcast 落盘一条执行记录并广播给前端。
// 日志落盘失败不影响任务本身的成败，只记一条 ERROR。
func (s *Scheduler) recordAndBroadcast(record *RunRecord) {
	if aErr := s.logs.append(record); aErr != nil {
		logger.GetLogger().Errorf("Failed to persist run log for task '%s': %v", record.TaskName, aErr)
	}

	realtime.BroadcastScheduleRun(record.TaskName, string(record.Outcome), map[string]any{
		"id":          record.ID,
		"task_id":     record.TaskID,
		"task_name":   record.TaskName,
		"task_type":   string(record.TaskType),
		"trigger":     string(record.Trigger),
		"started_at":  record.StartedAt.UnixMilli(),
		"duration_ms": record.DurationMs,
		"success":     record.Success,
		"outcome":     string(record.Outcome),
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
//
// 取消（无论是任务本身被取消，还是这一轮批量在操作面板里被取消）都会触发
// 状态回滚：把执行前正在跑、此刻还没重启回来的实例重新拉起。
func (s *Scheduler) runRestart(run *taskRun, t *Task) (string, error) {
	ctx := run.ctx
	snapshot := run.snapshot

	run.setPhase(PhaseRestarting)
	op, err := batchmanage.GetGlobalManager().StartOperation(
		batchmanage.BatchRestart, t.Instances, 0, t.CountdownConfig(),
		batchmanage.BatchOrigin{
			Kind:  batchmanage.OriginScheduleRestart,
			Label: fmt.Sprintf("定时任务「%s」", t.Name),
		},
	)
	if errors.Is(err, batchmanage.ErrOperationInProgress) {
		return "", fmt.Errorf("批量重启未能发起：有批量操作正在进行（可能是待恢复实例的启动），本次重启已跳过")
	}
	if err != nil {
		return "", fmt.Errorf("failed to start batch restart: %w", err)
	}

	awaitErr := awaitBatch(ctx, op)
	if awaitErr == nil && op.WasCancelled() {
		// 把「这一轮批量在操作面板里被取消」翻译成「任务被取消」——
		// 这是让两个取消入口走同一条收尾路径的唯一接缝
		run.markCancelled("批量操作在操作面板中被取消")
	}
	if run.cancelled.Load() {
		return s.rollbackAfterCancel(t, snapshot, "批量重启被取消")
	}
	if awaitErr != nil {
		// 调度器整体停止 / 进程退出：不回滚，落盘待恢复现场，下次启动后提示
		s.mergeShutdownPending(t, snapshot, "调度器停止，未恢复启动")
		return "", awaitErr
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

// runUpdate 先停全部实例，更新，再把执行前正在跑、此刻还没起来的实例原样拉起来。
//
// 停服不是顺手做的好事，而是硬前提：installer 在有实例存活时会直接拒绝更新。
// 而停完不管才是真正的坑——实例状态被写成 stopped 之后，再挂一个定时重启任务也救不回来：
// 重启的预检（IsStoppable）和 CAS 都只接受 started，停着的实例会被整批 skip 掉。
// 所以「谁把它停的谁负责起回来」这件事只能由这里做。
//
// 判据用「执行前的存活快照」而不是「行为日志（记住我停了谁）」：前者能同时覆盖
// 「批量停服正常完成」「强停兜底」「批量在中途被取消」「进程被杀后重启」等所有场景，
// 后者只能覆盖第一种。回滚时只启动，不停止——快照之外新出现的存活实例
// （用户在任务执行期间自己手动启动的）一律不碰。
func (s *Scheduler) runUpdate(run *taskRun, t *Task) (string, error) {
	ctx := run.ctx
	snapshot := run.snapshot

	run.setPhase(PhaseCountdown)
	op, err := batchmanage.GetGlobalManager().StartOperation(
		batchmanage.BatchStop, nil, 0, t.CountdownConfig(),
		batchmanage.BatchOrigin{
			Kind:  batchmanage.OriginScheduleUpdate,
			Label: fmt.Sprintf("定时任务「%s」· 更新前停服", t.Name),
		},
	)
	switch {
	case errors.Is(err, batchmanage.ErrNoInstances):
		// 一个实例都没有，没什么可停的，直接进入更新
		logger.GetLogger().Info("No instances to stop before scheduled update")
	case errors.Is(err, batchmanage.ErrOperationInProgress):
		return "", fmt.Errorf("更新前的批量停服未能启动：有批量操作正在进行（可能是待恢复实例的启动），本次更新已跳过")
	case err != nil:
		return "", fmt.Errorf("更新前的批量停服未能启动: %w", err)
	default:
		run.setPhase(PhaseStopping)
		awaitErr := awaitBatch(ctx, op)
		if awaitErr == nil && op.WasCancelled() {
			run.markCancelled("批量操作在操作面板中被取消")
		}
		if run.cancelled.Load() {
			// 用户中途叫停：不能再往下走强停兜底，那等于无视他的取消把服务器硬杀一遍。
			// 已经停掉的那批要还回去——它们是被这次半途而废的任务连累的
			return s.rollbackAfterCancel(t, snapshot, "批量停服被取消，本次更新已放弃")
		}
		if awaitErr != nil {
			s.mergeShutdownPending(t, snapshot, "调度器停止，未恢复启动")
			return "", awaitErr
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
		}
	}

	if alive := waitInstancesStopped(ctx, forceStopTimeout); len(alive) > 0 {
		// 更新做不成了，但已经被停下来的那批是被本次任务连累的，得还回去
		toRestore := excludeNames(snapshot, alive)
		out, restoreErr := s.guardedRestore(ctx, toRestore, batchmanage.BatchOrigin{
			Kind:  batchmanage.OriginUpdateRestore,
			Label: fmt.Sprintf("定时任务「%s」· 更新后恢复启动", t.Name),
		})
		s.settlePendingAfterRestore(t.ID, t.Name, out, restoreErr)
		return "", fmt.Errorf("以下实例无法停止，更新已取消：%s%s",
			strings.Join(alive, "、"), restoreNote(out, restoreErr))
	}

	// 写前日志：假如进程死在更新这一步，至少有落盘的记录说明「这批是我停的」。
	// 正常跑完会被下面的 settlePendingAfterRestore 收掉。
	if stoppedNow := excludeNames(snapshot, procpkg.ListAliveInstances()); len(stoppedNow) > 0 {
		if err := s.pending.Merge(t.ID, t.Name, "更新过程中管理器退出", stoppedNow); err != nil {
			logger.GetLogger().Errorf("Failed to persist pending restore state: %v", err)
		}
		s.broadcastPendingState()
	}

	run.setPhase(PhaseUpdating)
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

		run.setPhase(PhaseRestoring)
		toRestore := excludeNames(snapshot, procpkg.ListAliveInstances())
		out, restoreErr := s.guardedRestore(ctx, toRestore, batchmanage.BatchOrigin{
			Kind:  batchmanage.OriginUpdateRestore,
			Label: fmt.Sprintf("定时任务「%s」· 更新后恢复启动", t.Name),
		})
		s.settlePendingAfterRestore(t.ID, t.Name, out, restoreErr)

		switch {
		case updateErr != nil:
			return "", fmt.Errorf("%v%s", updateErr, restoreNote(out, restoreErr))
		case restoreErr != nil:
			return "", fmt.Errorf("更新完成，但恢复启动失败：%w", restoreErr)
		case len(snapshot) == 0:
			return "无实例需停止，更新完成", nil
		default:
			return fmt.Sprintf("已停止 %d 个实例并完成更新，已重新启动 %d 个",
				len(snapshot), len(out.Restored)), nil
		}
	case <-ctx.Done():
		if run.cancelled.Load() {
			// updatemanage.Cancel() 已经被 CancelRun 调用；SteamCMD 进程会被掐掉，
			// server-files/ 可能停在下载/解压到一半的状态。照做但说清楚：
			// SteamCMD 支持断点续传，下次更新会补齐并重新校验。
			_, rollErr := s.rollbackAfterCancel(t, snapshot, "更新已中断")
			return "", fmt.Errorf("%v；⚠️ 更新被中断，服务端文件可能不完整，建议重新执行一次更新", rollErr)
		}
		s.mergeShutdownPending(t, snapshot, "调度器停止，未恢复启动")
		return "", ctx.Err()
	}
}

// rollbackAfterCancel 是「任务被取消」的统一收尾：把执行前活着、此刻还没恢复的实例拉回来。
//
// 用 context.Background() 而不是 run.ctx——它已经被取消了，restoreInstances
// 入口的 ctx.Err() 检查会当场把回滚挡回去。
func (s *Scheduler) rollbackAfterCancel(t *Task, snapshot []string, note string) (string, error) {
	toRestore := excludeNames(snapshot, procpkg.ListAliveInstances())
	origin := batchmanage.BatchOrigin{
		Kind:  batchmanage.OriginTaskCancelRestore,
		Label: fmt.Sprintf("取消定时任务「%s」· 状态回滚", t.Name),
	}
	out, err := s.guardedRestore(context.Background(), toRestore, origin)
	s.settlePendingAfterRestore(t.ID, t.Name, out, err)
	return "", fmt.Errorf("%s%s", note, restoreNote(out, err))
}

// mergeShutdownPending 是调度器整体停止 / 进程退出时的收尾：不回滚（拉起实例只会被
// awaitBatch 立刻掐断，白留一批 start_initialization 中间状态），只落盘待恢复现场。
func (s *Scheduler) mergeShutdownPending(t *Task, snapshot []string, reason string) {
	stoppedNow := excludeNames(snapshot, procpkg.ListAliveInstances())
	if len(stoppedNow) == 0 {
		return
	}
	if err := s.pending.Merge(t.ID, t.Name, reason, stoppedNow); err != nil {
		logger.GetLogger().Errorf("Failed to persist pending restore state: %v", err)
	}
	s.broadcastPendingState()
}

// restoreOutcome 一次恢复启动的结果明细。
//
// 只返回数量不够用：待恢复现场要重写成「还欠着的那几台」，得知道名字；
// Cancelled 与 Failed 分开是为了在文案里说清「启动失败」和「没轮到就被取消」的区别，
// 但两者都不计入「处理完了」，都要继续留在待恢复现场里。
type restoreOutcome struct {
	Restored  []string // 启动成功
	Failed    []string // 启动失败，仍然欠着
	Skipped   []string // CAS 拒绝，通常是已经在跑了，不欠了
	Cancelled []string // 用户取消/未轮到，仍然欠着
}

// handled 返回「已经处理完，可以从待恢复现场移除」的实例：启动成功的，
// 以及 CAS 拒绝的（几乎总是因为已经在跑了）。
func (o restoreOutcome) handled() []string {
	if len(o.Restored) == 0 && len(o.Skipped) == 0 {
		return nil
	}
	out := make([]string, 0, len(o.Restored)+len(o.Skipped))
	out = append(out, o.Restored...)
	out = append(out, o.Skipped...)
	return out
}

// stillOwed 返回「还欠着启动，需要留在待恢复现场」的实例。
func (o restoreOutcome) stillOwed() []string {
	if len(o.Failed) == 0 && len(o.Cancelled) == 0 {
		return nil
	}
	out := make([]string, 0, len(o.Failed)+len(o.Cancelled))
	out = append(out, o.Failed...)
	out = append(out, o.Cancelled...)
	return out
}

// guardedRestore 是 restoreInstances 的加闸版本：拿不到 restoreInFlight 就直接失败，
// 而不是让两个恢复动作同时跑（各自读到同一份旧名单，一个刚收尾另一个又拉一遍）。
func (s *Scheduler) guardedRestore(ctx context.Context, names []string, origin batchmanage.BatchOrigin) (restoreOutcome, error) {
	if len(names) == 0 {
		return restoreOutcome{}, nil
	}
	if !s.tryAcquireRestore() {
		return restoreOutcome{}, fmt.Errorf(
			"恢复启动被占用（可能有另一次恢复正在进行），%d 个实例暂未恢复：%s",
			len(names), strings.Join(names, "、"))
	}
	defer s.releaseRestore()
	return restoreInstances(ctx, names, origin)
}

func (s *Scheduler) tryAcquireRestore() bool { return s.restoreInFlight.CompareAndSwap(false, true) }
func (s *Scheduler) releaseRestore()         { s.restoreInFlight.Store(false) }

// restoreInstances 把 names 里的实例重新拉起来。不做并发保护——调用方（guardedRestore /
// ConfirmPendingRestore）负责持有 restoreInFlight。
//
// 用 BatchStart 而不是 BatchRestart：重启的预检与 CAS 都只接受 started，
// 对着已经停掉的实例发重启，整批都会被 skip 掉（这正是「更新任务后面挂个重启任务」
// 救不回来的原因）。
func restoreInstances(ctx context.Context, names []string, origin batchmanage.BatchOrigin) (restoreOutcome, error) {
	if len(names) == 0 {
		return restoreOutcome{}, nil
	}
	if ctx.Err() != nil {
		return restoreOutcome{}, fmt.Errorf("任务已取消，%d 个实例未恢复启动：%s",
			len(names), strings.Join(names, "、"))
	}

	logger.GetLogger().Infof("Restoring %d instance(s): %s", len(names), strings.Join(names, "、"))

	op, err := batchmanage.GetGlobalManager().StartOperation(
		batchmanage.BatchStart, names, restoreStartDelaySeconds, nil, origin,
	)
	if err != nil {
		// 尤其是 ErrOperationInProgress：有人从 UI 发起了别的批量操作。
		// 不能吞——吞了就等于实例悄悄留在停止状态
		return restoreOutcome{}, fmt.Errorf("恢复启动未能发起: %w", err)
	}

	// 即使 ctx 被取消导致 awaitBatch 主动 Cancel 了这轮批量，op 此刻也已经结束，
	// InstanceResults 是最终态——不能因为 awaitBatch 返回了错误就丢弃这些信息，
	// 那会把「已经成功启动的几台」也一并算作没处理
	_ = awaitBatch(ctx, op)

	out := collectRestoreOutcome(op)
	if len(out.Failed) > 0 || len(out.Cancelled) > 0 {
		return out, buildRestoreErr(out)
	}
	return out, nil
}

func collectRestoreOutcome(op *batchmanage.BatchOperation) restoreOutcome {
	var out restoreOutcome
	for _, r := range op.InstanceResults {
		switch r.Status {
		case batchmanage.InstanceSuccess:
			out.Restored = append(out.Restored, r.InstanceName)
		case batchmanage.InstanceFailed:
			out.Failed = append(out.Failed, r.InstanceName)
		case batchmanage.InstanceSkipped:
			out.Skipped = append(out.Skipped, r.InstanceName)
		default:
			// InstancePending / InstanceSkipRequested / InstanceRunning / InstanceCancelled：
			// 批量已经结束却还是这些状态，只可能是这一轮恢复被整体取消了
			out.Cancelled = append(out.Cancelled, r.InstanceName)
		}
	}
	return out
}

func buildRestoreErr(out restoreOutcome) error {
	var parts []string
	if len(out.Failed) > 0 {
		parts = append(parts, fmt.Sprintf("%d 个实例启动失败：%s", len(out.Failed), strings.Join(out.Failed, "、")))
	}
	if len(out.Cancelled) > 0 {
		parts = append(parts, fmt.Sprintf("%d 个实例未及启动（已取消）：%s", len(out.Cancelled), strings.Join(out.Cancelled, "、")))
	}
	return errors.New(strings.Join(parts, "；"))
}

// restoreNote 把恢复结果拼成一句可以挂在别的消息后面的补充说明，无事可说时返回空串。
func restoreNote(out restoreOutcome, err error) string {
	switch {
	case err != nil:
		return fmt.Sprintf("（恢复启动失败：%v）", err)
	case len(out.Restored) > 0:
		return fmt.Sprintf("（已恢复启动 %d 个实例）", len(out.Restored))
	default:
		return ""
	}
}

// settlePendingAfterRestore 把一次恢复启动的结果同步进待恢复现场：
// 处理完的（成功 / 已在跑）从现场移除，还欠着的（失败 / 被取消）写入，
// 供前端提示继续弹出让用户再决定一次。
func (s *Scheduler) settlePendingAfterRestore(taskID, taskName string, out restoreOutcome, err error) {
	handled := out.handled()
	if len(handled) > 0 {
		if e := s.pending.Resolve(handled, ""); e != nil {
			logger.GetLogger().Errorf("Failed to update pending restore state: %v", e)
		}
	}

	owed := out.stillOwed()
	if len(owed) > 0 {
		reason := ""
		if err != nil {
			reason = err.Error()
		}
		if e := s.pending.Merge(taskID, taskName, reason, owed); e != nil {
			logger.GetLogger().Errorf("Failed to persist pending restore state: %v", e)
		}
	}

	if len(handled) > 0 || len(owed) > 0 {
		s.broadcastPendingState()
	}
}

// broadcastPendingState 读取当前待恢复现场并广播给前端，多个页面开着时
// 保持同步（一个人处理完，其他人的提示要跟着消失或更新）。
func (s *Scheduler) broadcastPendingState() {
	p, ok := s.pending.Get()
	if !ok {
		realtime.BroadcastPendingRestore(false, nil)
		return
	}
	realtime.BroadcastPendingRestore(true, map[string]any{
		"instances":  p.Instances,
		"task_id":    p.TaskID,
		"task_name":  p.TaskName,
		"reason":     p.Reason,
		"created_at": p.CreatedAt.UnixMilli(),
		"updated_at": p.UpdatedAt.UnixMilli(),
	})
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

// ---- 运行中任务的登记表：取消能力 ----

// taskPhase 面向用户，描述一次执行此刻走到哪一步。
type taskPhase string

const (
	PhaseCountdown  taskPhase = "countdown"  // 倒计时中
	PhaseStopping   taskPhase = "stopping"   // 停服中
	PhaseUpdating   taskPhase = "updating"   // 更新中
	PhaseRestoring  taskPhase = "restoring"  // 恢复启动中
	PhaseRestarting taskPhase = "restarting" // 批量重启中
)

// taskRun 一次正在执行的任务。
type taskRun struct {
	RunID     string
	TaskID    string
	TaskName  string
	TaskType  TaskType
	Trigger   TriggerSource
	StartedAt time.Time

	// snapshot 是任务开始前「正在跑」的实例名，取消/需要回滚时据此计算差集。
	//
	// 判据用 procpkg.ListAliveInstances()（端口 + PID 双重判断）而不是读 BadgerDB
	// 状态：状态记录会因为崩溃而滞留在 started，照着它恢复会去启动一台本来就死着的实例。
	snapshot []string

	phase atomic.Value // taskPhase

	ctx    context.Context
	cancel context.CancelFunc

	cancelled    atomic.Bool
	cancelReason atomic.Value // string
}

func newTaskRun(parentCtx context.Context, t *Task, trigger TriggerSource) *taskRun {
	ctx, cancel := context.WithCancel(parentCtx)
	run := &taskRun{
		RunID:     newRunID(),
		TaskID:    t.ID,
		TaskName:  t.Name,
		TaskType:  t.Type,
		Trigger:   trigger,
		StartedAt: time.Now(),
		snapshot:  procpkg.ListAliveInstances(),
		ctx:       ctx,
		cancel:    cancel,
	}
	run.setPhase(PhaseCountdown)
	return run
}

func (r *taskRun) setPhase(p taskPhase) { r.phase.Store(p) }

func (r *taskRun) Phase() taskPhase {
	if v, ok := r.phase.Load().(taskPhase); ok {
		return v
	}
	return ""
}

// markCancelled 幂等地把这次执行标记为已取消，返回是否是本次调用真正生效的。
// 两个取消入口（任务页「取消任务」、批量面板「取消」）都调它，CAS 保证只生效一次——
// 第二次进来时回滚可能已经跑了一半，重复触发会把回滚自己的 ctx 也一并搅乱。
func (r *taskRun) markCancelled(reason string) bool {
	if !r.cancelled.CompareAndSwap(false, true) {
		return false
	}
	r.cancelReason.Store(reason)
	return true
}

func (r *taskRun) CancelReason() string {
	if v, ok := r.cancelReason.Load().(string); ok {
		return v
	}
	return ""
}

var runSeq atomic.Uint64

func newRunID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatUint(runSeq.Add(1), 36)
}

func (s *Scheduler) registerRun(run *taskRun) {
	s.runsMu.Lock()
	s.runs[run.RunID] = run
	s.runsMu.Unlock()
}

func (s *Scheduler) unregisterRun(runID string) {
	s.runsMu.Lock()
	delete(s.runs, runID)
	s.runsMu.Unlock()
}

func (s *Scheduler) getRun(runID string) (*taskRun, bool) {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	r, ok := s.runs[runID]
	return r, ok
}

// RunInfo 是 taskRun 面向 API 的只读视图。
type RunInfo struct {
	RunID     string        `json:"run_id"`
	TaskID    string        `json:"task_id"`
	TaskName  string        `json:"task_name"`
	TaskType  TaskType      `json:"task_type"`
	Trigger   TriggerSource `json:"trigger"`
	Phase     string        `json:"phase"`
	StartedAt time.Time     `json:"started_at"`
}

// ListRuns 返回当前正在执行的任务。
func (s *Scheduler) ListRuns() []RunInfo {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()

	out := make([]RunInfo, 0, len(s.runs))
	for _, r := range s.runs {
		out = append(out, RunInfo{
			RunID:     r.RunID,
			TaskID:    r.TaskID,
			TaskName:  r.TaskName,
			TaskType:  r.TaskType,
			Trigger:   r.Trigger,
			Phase:     string(r.Phase()),
			StartedAt: r.StartedAt,
		})
	}
	return out
}

var (
	ErrRunNotFound          = errors.New("该任务已执行完毕或不存在")
	ErrRunAlreadyCancelling = errors.New("该任务正在取消中")
)

// CancelRun 取消一次正在执行的任务。回滚（把执行前活着的实例拉回来）在
// runUpdate/runRestart 里各自完成，这里只负责传导取消信号。
//
// 光取消 ctx 是不够的：批量操作（含倒计时）的取消 awaitBatch 已经处理好了，
// 但 updatemanage 的 ctx 派生自 context.Background()，任务 ctx 关不掉它——
// 不显式调 Cancel() 的话，用户看到任务已取消，SteamCMD 还在后台默默下载。
func (s *Scheduler) CancelRun(runID string) error {
	run, ok := s.getRun(runID)
	if !ok {
		return ErrRunNotFound
	}
	if !run.markCancelled("用户取消了本次执行") {
		return ErrRunAlreadyCancelling
	}

	run.cancel()
	if run.TaskType == TaskUpdate {
		updatemanage.GetGlobalManager().Cancel()
	}
	return nil
}

// ---- 待恢复现场：查询 / 确认 / 忽略 ----

var (
	ErrNoPendingRestore  = errors.New("没有待恢复的实例")
	ErrRestoreInProgress = errors.New("恢复启动正在进行中")
)

// GetPendingRestore 返回待恢复现场，没有则第二个返回值为 false。
func (s *Scheduler) GetPendingRestore() (*PendingRestore, bool) {
	return s.pending.Get()
}

// ConfirmPendingRestore 后台执行一次恢复启动，立即返回。进度走 batchmanage
// 既有的 SSE/WS，结果追加一条执行日志。
//
// 同步预检有批量在跑就当场回 409，别先回 200 再在后台悄悄失败——用户点了
// 「恢复启动」却在几秒后看到提示框重新弹出来，比直接说「现在忙」更费解。
func (s *Scheduler) ConfirmPendingRestore() error {
	p, ok := s.pending.Get()
	if !ok {
		return ErrNoPendingRestore
	}
	if batchmanage.GetGlobalManager().IsRunning() {
		return ErrRestoreInProgress
	}
	if !s.tryAcquireRestore() {
		return ErrRestoreInProgress
	}

	go func() {
		defer s.releaseRestore()

		startedAt := time.Now()
		// ctx 用 Background 而不是调度循环的 ctx：这是用户手动发起的动作，
		// 不该因为调度器恰好在这时被停掉而半途取消
		out, err := restoreInstances(context.Background(), p.Instances, batchmanage.BatchOrigin{
			Kind:  batchmanage.OriginManualRestore,
			Label: "恢复更新前停止的实例",
		})
		s.finishPendingRestore(p, out, err, startedAt)
	}()
	return nil
}

// finishPendingRestore 收尾一次手动确认的恢复：更新现场文件，并追加一条执行日志——
// 否则用户点完确认，界面上没有任何痕迹说明发生过什么。
func (s *Scheduler) finishPendingRestore(p *PendingRestore, out restoreOutcome, err error, startedAt time.Time) {
	s.settlePendingAfterRestore(p.TaskID, p.TaskName, out, err)

	outcome := OutcomeSuccess
	message := fmt.Sprintf("已恢复启动 %d 个实例", len(out.Restored))
	if err != nil {
		outcome = OutcomeFailed
		message = err.Error()
	}

	record := &RunRecord{
		ID:         newRunRecordID(),
		TaskID:     p.TaskID,
		TaskName:   p.TaskName + "（恢复启动）",
		TaskType:   TaskUpdate,
		Trigger:    TriggerManual,
		StartedAt:  startedAt,
		DurationMs: time.Since(startedAt).Milliseconds(),
		Success:    outcome == OutcomeSuccess,
		Outcome:    outcome,
		Message:    message,
	}
	s.recordAndBroadcast(record)
}

// IgnorePendingRestore 丢弃现场，之后不再提示。实例保持停止状态。
func (s *Scheduler) IgnorePendingRestore() error {
	if err := s.pending.Clear(); err != nil {
		return err
	}
	s.broadcastPendingState()
	return nil
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
