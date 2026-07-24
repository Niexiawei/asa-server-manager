package countdown

import (
	instancepkg "asa-server/instance"
	"asa-server/logger"
	"asa-server/rconx"
	"asa-server/realtime"
	statepkg "asa-server/state"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

// Result 一轮倒计时的结果。
type Result struct {
	// Cancelled 是没有走完倒计时的实例，调用方应把它们排除在后续动作之外，
	// 其余实例照常执行。
	Cancelled []string

	// Reasons 记录每个 Cancelled 实例的原因：
	// 用户取消为 ErrCancelled，与另一轮倒计时撞车为 ErrInProgress。
	Reasons map[string]error
}

// IsCancelled 报告指定实例是否未走完倒计时。
func (r Result) IsCancelled(instanceName string) bool {
	return slices.Contains(r.Cancelled, instanceName)
}

// AllCancelled 报告是否所有实例都没走完。total 传本轮的实例总数。
func (r Result) AllCancelled(total int) bool {
	return total > 0 && len(r.Cancelled) >= total
}

// Reason 返回实例未走完的原因，走完了则返回 nil。
func (r Result) Reason(instanceName string) error {
	if err, ok := r.Reasons[instanceName]; ok {
		return err
	}
	return nil
}

// Wait 对一组实例执行一轮**对齐的**倒计时：同一时刻、同一文案，并发播报。
//
// 只倒计时，不执行任何动作。batchmanage 用它做阶段一——
// 它的阶段二有 CAS、逐实例结果、实例间延迟，不适合塞进本包。
//
// 为什么要对齐而不是逐个实例各走一轮：5 个实例 × 10 分钟就是 50 分钟，
// 而且最后一个实例的玩家要等到第 40 分钟才收到「还有 10 分钟」的通知。
//
// 每个实例持有独立的子 context：Cancel(name) 只掐断那一个，其余继续走完，
// 落在 Result.Cancelled 里。返回的 error 仅在**父 ctx** 被取消时非 nil
// （整批中止，例如进程退出或 batchmanage 的 CancelCurrent），或配置本身非法。
//
// 返回前会释放全部登记：Wait 的调用方随后要跑自己的阶段二，
// 登记表不该被这一轮倒计时继续占着。
func Wait(ctx context.Context, instances []string, action Action, cfg *Config) (Result, error) {
	if !cfg.Enabled() || len(instances) == 0 {
		return Result{}, nil
	}
	if err := cfg.Validate(); err != nil {
		return Result{}, err
	}

	norm := cfg.normalized()
	deadline := time.Now().Add(norm.Total)

	logger.GetLogger().Infof(
		"Countdown started for %d instance(s): %s until %s, %d notify point(s)",
		len(instances), norm.Total, deadline.Format(time.TimeOnly), len(norm.Points),
	)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		reasons = make(map[string]error)
	)

	for _, name := range instances {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()

			if err := runOne(ctx, name, action, &norm, deadline); err != nil {
				// 失败路径不碰登记表：被取消时 runOne 已经清理过，
				// 而 ErrInProgress 意味着那条登记属于**另一轮**倒计时，
				// 在这里 release 会把别人的 cancel 函数丢掉。
				mu.Lock()
				reasons[name] = err
				mu.Unlock()
				return
			}

			// 走完了。Wait 的调用方接下来要跑自己的阶段二，
			// 登记表不该被这一轮继续占着。
			release(name)
		}(name)
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		// 父 ctx 被取消：整批中止，逐实例的结果没有意义了
		return Result{}, err
	}

	// 保持入参顺序，便于调用方的日志与 UI 稳定
	res := Result{Reasons: reasons}
	for _, name := range instances {
		if _, ok := reasons[name]; ok {
			res.Cancelled = append(res.Cancelled, name)
		}
	}

	return res, nil
}

// Stop 倒计时结束后停止实例。cfg 为 nil 或未启用时等价于直接调用 instance.StopServer。
//
// 倒计时被取消时：不执行停止，把实例状态回滚到 started（倒计时期间服务器一直正常运行，
// 调用方在 CAS 时抢占的 stopping 状态要还回去），并返回取消原因。
//
// opts 透传给 instance.StopServer，调用方通常传 WithStatePreset。
func Stop(ctx context.Context, instanceName string, cfg *Config, opts ...instancepkg.StartServerOptionsFunc) error {
	if err := waitOne(ctx, instanceName, ActionStop, cfg); err != nil {
		_ = statepkg.WriteInstanceState(instanceName, statepkg.StatusStarted, "")
		return err
	}

	// 只有真的登记过才释放：没开倒计时时登记表里那条（如果有）属于别人
	if cfg.Enabled() {
		defer release(instanceName)
	}

	return instancepkg.StopServer(instanceName, opts...)
}

// Restart 倒计时结束后重启实例。语义与 Stop 一致，最终调用 instance.RestartServer。
func Restart(ctx context.Context, instanceName string, cfg *Config, opts ...instancepkg.StartServerOptionsFunc) error {
	if err := waitOne(ctx, instanceName, ActionRestart, cfg); err != nil {
		_ = statepkg.WriteInstanceState(instanceName, statepkg.StatusStarted, "")
		return err
	}

	if cfg.Enabled() {
		defer release(instanceName)
	}

	return instancepkg.RestartServer(instanceName, opts...)
}

// waitOne 为单个实例跑一轮倒计时。未启用倒计时时直接返回 nil。
//
// 与 Wait 不同，走完后**不**释放登记：接下来的停止/重启本身还要花时间，
// 前端靠 executing 阶段的登记显示「服务器关闭中…」，调用方负责在动作结束后 release。
func waitOne(ctx context.Context, instanceName string, action Action, cfg *Config) error {
	if !cfg.Enabled() {
		return nil
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	norm := cfg.normalized()
	deadline := time.Now().Add(norm.Total)

	logger.GetLogger().Infof(
		"Countdown started for instance %s: %s until %s, %d notify point(s)",
		instanceName, norm.Total, deadline.Format(time.TimeOnly), len(norm.Points),
	)

	return runOne(ctx, instanceName, action, &norm, deadline)
}

// runOne 跑单个实例的倒计时。cfg 必须已 normalized。
//
// 返回 nil 表示走完，此时登记仍在（阶段已切到 executing），由调用方释放；
// 返回 error 表示被取消，登记已由本函数清理。
func runOne(ctx context.Context, instanceName string, action Action, cfg *Config, deadline time.Time) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := register(instanceName, action, deadline, cancel); err != nil {
		logger.GetLogger().Warnf(
			"Skipping countdown for instance %s: %v", instanceName, err,
		)
		return err
	}

	if err := loop(ctx, instanceName, action, cfg, deadline); err != nil {
		logger.GetLogger().Infof("Countdown for instance %s cancelled", instanceName)
		markPhase(instanceName, action, realtime.CountdownPhaseCancelled,
			fmt.Sprintf("实例 %s 的%s已取消", instanceName, action.Label()))
		release(instanceName)
		return ErrCancelled
	}

	// 倒计时归零 ≠ 操作完成：接下来的停止本身还要花时间。
	// 切到 executing，让前端显示「服务器关闭中…」而不是继续卡在 0 秒。
	markPhase(instanceName, action, realtime.CountdownPhaseExecuting,
		fmt.Sprintf("服务器%s中…", action.Label()))

	return nil
}

// loop 每秒推一次 WS 进度，并在到达点位时发游戏内公告。
//
// 每秒 tick 是为了让前端能平滑逐秒递减，而不是只在点位跳变。
func loop(
	ctx context.Context,
	instanceName string,
	action Action,
	cfg *Config,
	deadline time.Time,
) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// 点位按剩余时间降序排列，用一个游标从前往后消费
	pending := slices.Clone(cfg.Points)

	// 公告发送失败不会中断倒计时——RCON 发不出去（玩家全下线、服务器卡住）
	// 只记 WARN。公告是尽力而为的通知，不是停止操作的前置条件。
	announce := func(remaining time.Duration) {
		msg := renderMessage(cfg.Template, instanceName, action, remaining)
		command := fmt.Sprintf("%s %s", cfg.Command, msg)
		if _, err := rconx.Execute(ctx, instanceName, command); err != nil {
			logger.GetLogger().Warnf(
				"Failed to announce countdown to instance %s (countdown continues): %v",
				instanceName, err,
			)
		}
	}

	// 剩余时间恰好等于总时长的点位要在开场立即播，等第一个 tick 就晚了 1 秒
	remaining := time.Until(deadline)
	for len(pending) > 0 && pending[0] >= remaining {
		announce(pending[0])
		pending = pending[1:]
	}
	pushProgress(instanceName, action, cfg.Template, remaining)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-ticker.C:
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return nil
			}

			// 一个 tick 内可能跨过多个点位（例如系统休眠后唤醒），全部补播
			for len(pending) > 0 && pending[0] >= remaining {
				announce(pending[0])
				pending = pending[1:]
			}

			pushProgress(instanceName, action, cfg.Template, remaining)
		}
	}
}

// pushProgress 推送一条 WS 倒计时进度。
func pushProgress(instanceName string, action Action, template string, remaining time.Duration) {
	if remaining < 0 {
		remaining = 0
	}

	realtime.BroadcastCountdownEvent(
		instanceName,
		string(action),
		realtime.CountdownPhaseCounting,
		renderMessage(template, instanceName, action, remaining),
		int(remaining.Round(time.Second).Seconds()),
	)
}
