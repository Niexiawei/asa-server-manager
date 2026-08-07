# 定时更新后恢复实例启动状态 — 执行方案

> 状态：**第一阶段（§1–§7）已实施**，`go build ./...` / `go vet ./...` / `go test ./internal/schedule/...` 通过；
> **§9–§11 待实施**。三者有依赖关系，按 §9 → §10 → §11 的顺序落；
> 其中 §10.2 / §10.3 是**第一阶段代码里的两个 bug**，可以先于其余部分单独修掉。
>
> | 阶段 | 内容 |
> |---|---|
> | §1–§7 | 更新任务自己把停掉的实例拉回来（已实施） |
> | §9 | 进程被杀也不丢：落盘待恢复现场 + 前端确认/忽略提示 |
> | §10 | 自动批量的可取消性修正 + `BatchOrigin` 来源标识 |
> | §11 | 运行中的定时任务可随时取消，并按快照回滚实例状态 |
>
> 目标：让 `schedule` 的**定时更新**任务在更新结束后，把「本次任务亲手停掉的实例」按原样重新拉起来，
> 而不是把整台机器丢在全停状态直到用户手动干预。
> 第二阶段进一步覆盖「进程没活到恢复那一步」的情况：落盘待恢复现场，由前端提示用户确认或忽略。
>
> 本文是**执行文档**：§4 的代码改动可以照着顺序落，改完 `go build ./...` / `go vet ./...` 即可通过；
> §3.5 专门核对了与 `schedule` 包现有编排的冲突面；§9.4 是第二阶段对第一阶段已实现代码的**修订**。

---

## 1. 现状与问题

### 1.1 `runUpdate` 现在做了什么

`internal/schedule/scheduler.go` 的 `runUpdate` 是一条单向的下坡路：

| 步骤 | 代码 | 结果 |
|---|---|---|
| ① 批量停服 | `batchmanage.StartOperation(BatchStop, nil, 0, cd)` | 状态是 `started` 的实例 → `stopped` |
| ② 强停兜底 | `procpkg.ListAliveInstances()` + `instancepkg.ForceStopServer` | CAS 没接住、但进程还活着的实例 → 被 `taskkill`，状态写 `stopped` |
| ③ 等待退出 | `waitInstancesStopped(ctx, 2min)` | 停不掉就整单放弃 |
| ④ 更新 | `updatemanage.GetGlobalManager().Start()` | 更新服务端文件 |
| ⑤ 收尾 | 返回 `"已停止 N 个实例并完成更新"` | **没有任何一步把实例拉回来** |

停服本身是硬前提（`installer` 在有实例存活时会拒绝更新，`updatemanage.run` 的 Step 0 也会先拦一道），
所以①②不能省。问题出在⑤：任务把「停」做完了，「起」没人负责。

### 1.2 为什么「更新任务后面再挂一个重启任务」救不回来

用户已经试过这条路，它在设计上就走不通，原因有两层，两层都会拦：

1. **预检拦一次**：`batchmanage.StartOperation` 在倒计时之前调 `operable()` → `instancepkg.IsStoppable()`，
   而 `IsStoppable` 的第一条判据就是 `state.Status != StatusStarted → false, ReasonNotStarted`。
   更新任务把实例写成了 `stopped`，重启任务在预检阶段就把它们全标成 `skipped`。
2. **CAS 再拦一次**：即使绕过预检，阶段二的 `batchDoCAS(BatchRestart)` 只接受
   `[]InstanceStatus{StatusStarted}`（`manager.go:879`）。`stopped` 的实例照样 `skipped`。

也就是说，**「重启」在本项目里语义上是「对着一台正在跑的实例做停+起」，它不是「拉起一台停着的实例」**。
后者对应的是 `BatchStart`（CAS 接受 `stopped / start_failed / stop_failed / restart_failed / ""`）。
所以修复方向只能是：更新任务自己记住停了谁，然后用 **`BatchStart`** 把它们拉回来。

### 1.3 影响面

- 定时更新一旦触发，全部实例停机时间 = 更新耗时 + **到用户下次手动点启动为止**。
  半夜跑的更新任务尤其致命：早上才有人发现服务器全停着。
- `runRestart` 不受影响，它操作的是活着的实例。
- 手动触发的更新（`POST /api/server/update`）本来就不负责停服（有实例活着会直接报错让用户先停），
  不在本次范围内。

---

## 2. 目标与非目标

### 目标

1. 定时更新在**停服之前**记住「这次是我停的」实例集合。
2. 更新结束后用 `batchmanage.BatchStart` 把这批实例按原顺序拉起来。
3. 更新**失败**、或因实例停不掉而放弃更新时，同样要把已经停掉的那批还回去——
   把服务器一直停着比更新没做成更糟，下一个执行点可能是 24 小时之后。
4. 恢复启动的成败要体现在任务的执行结果/执行日志里，不能悄悄吞掉。
5. 不引入新的全局状态；不改 `updatemanage` / `instance` / `state` 任何一行。

> 目标 5 原本还包含「不改 `batchmanage`」，因 §10 的 `BatchOrigin` 而作废——
> 「这轮批量是谁发起的」是批量操作自身的属性，只能落在 `batchmanage` 里。

### 非目标（本次不做，理由见 §8）

- 给 `Task` 加「是否恢复启动」的开关字段（含前端 DTO 改动）。
- 改动手动更新（HTTP）的行为。
- 修 `RunNow` 与 tick 可并发这一既有问题。

> 跨进程持久化「待恢复列表」原本列在这里，现已升级为**要实现**的第二阶段，
> 完整设计见 **§9**（落盘 + 前端确认/忽略提示）。

---

## 3. 设计

### 3.1 判据：谁该被拉起来

用「**本次任务真正停掉的实例**」，而不是「所有实例」，也不是「所有状态为 stopped 的实例」：

| 来源 | 是否纳入 | 理由 |
|---|---|---|
| 批量停服里 `InstanceSuccess` 的实例 | ✅ | 它们停之前必然是 `started`（CAS 只接受这一个状态） |
| 强停兜底时 `ListAliveInstances()` 命中的实例 | ✅ | 进程活着 = 它本来在跑，只是状态没跟上（`starting` / `stop_failed` 等）。漏掉的话更新完就再也没人管它们了 |
| 批量停服里 `InstanceSkipped`（预检判定「未启动」） | ❌ | 任务开始前它本来就是停的，不该被定时更新顺手拉起来 |
| 倒计时被用户单独取消的实例（`InstanceCancelled`） | ❌ | 它没被停，还在跑；除非随后被强停兜底命中，那时会从上一行进来 |
| ③ 超时后仍然活着的实例 | ❌ | 它们压根没停下来，本来就在跑 |

集合按**首次出现顺序**保存，恢复时就按这个顺序串行拉起（与停服顺序一致，便于对照日志）。
强停兜底那批可能与批量停服成功的那批重叠（`StopServer` 返回时进程可能还在退出中），
所以追加时要去重。

### 3.2 时机：四条退出路径的处理矩阵

`runUpdate` 有四个出口，每个都要明确「恢复还是不恢复」：

| 出口 | 现状 | 改后 |
|---|---|---|
| 批量停服未能发起（`ErrOperationInProgress` 等） | 直接返回错误 | 不变（一个都没停，没什么可恢复） |
| 批量停服被用户取消 | **无视取消**，强停兜底照杀，更新照跑（既有 bug） | 放弃本次更新，恢复已停的那批——详见 §10.2 |
| ③ 等待退出超时，更新取消 | 直接返回错误 | **恢复**已经停掉的那批（剔除仍然活着的），错误信息里附带恢复结果 |
| ④ 更新完成（成功或失败） | 只看 `mgr.Result()` | **无条件恢复**，再决定任务成败 |
| `ctx.Done()`（调度器 `Stop()` / 进程退出） | 返回 `ctx.Err()` | 不变，**不恢复**——这时候拉起实例只会被 `awaitBatch` 立刻掐断，白留一堆 `start_initialization` 中间状态 |

### 3.3 用什么拉起

`batchmanage.StartOperation(BatchStart, names, restoreStartDelaySeconds, nil)`：

- `BatchStart` 的 `operable()` 一律放行，CAS 接受 `stopped`（②强停后写的正是它，见 `ForceStopServer` 第 4 步），
  所以①②停下来的实例都能被接住。
- 第四个参数传 `nil`：启动不需要倒计时（`StartOperation` 里对 `BatchStart` 本来也会把 `cdCfg` 置 nil，
  这里显式传 nil 只是让调用点自解释）。
- `delaySeconds` 用一个包级常量，默认 **0**：批量启动本来就是串行的
  （`executeInstance` 同步调 `StartServer`，它要等启动检测完成才返回），天然有间隔；
  机器吃力时把这个常量调大即可，不用改结构。
- 等待复用现成的 `awaitBatch(ctx, op)`——它在任务 ctx 先结束时会 `op.Cancel()` 并等 `Done()`，
  保证 `batchmanage` 单例被释放，这一点对紧接着的下一次调度很关键。

结果统计口径：`InstanceSuccess` 计入 `restored`；`InstanceFailed` 收进失败名单让任务判失败；
`InstanceSkipped`（CAS 拒绝，通常意味着这台已经被别人启动了）不计成功也不算失败，只留在批量日志里。

### 3.4 结果文案

| 场景 | 任务结果 | Message |
|---|---|---|
| 无实例需停 + 更新成功 | 成功 | `无实例需停止，更新完成` |
| 停 N 台 + 更新成功 + 全部拉起 | 成功 | `已停止 3 个实例并完成更新，已重新启动 3 个` |
| 更新成功 + 部分拉不起来 | 失败 | `更新完成，但恢复启动失败：1/3 个实例启动失败：meijue` |
| 更新失败 | 失败 | `<更新错误原文>（已恢复启动 3 个实例）` |
| 停不掉，更新取消 | 失败 | `以下实例无法停止，更新已取消：ces99（已恢复启动 2 个实例）` |

`schedule` 的执行日志（`RunRecord.Message`）没有长度上限，附加这段补充说明是安全的。

### 3.5 与 `schedule` 包现有逻辑的冲突核对

逐条核对过，**没有需要额外加锁或改结构的冲突**，但下面几条是这次改动依赖的既有前提，实施时不能破坏：

1. **任务在同一个 goroutine 里串行执行**（`tick` → `execute` 同步调用）。
   定时更新与定时重启因此天然不重叠。本次改动只是让更新任务跑得更久（多了一段启动时间），
   后续任务被推迟——这正是 `Scheduler` 注释里写明的、想要的行为。
2. **`tick` 用 `time.Now()` 而不是原定时刻推进 `NextRunAt`**（`scheduler.go:166`）。
   任务变长不会导致「刚跑完立刻又到点」。
3. **`batchmanage` 单例的释放顺序**：`runBatchOperation` 里 `defer close(op.done)` 是**最先注册**的，
   所以它**最后执行**——`bm.current = nil` 一定发生在 `op.Done()` 关闭之前。
   于是 `awaitBatch` 返回之后紧接着发起的第二次 `StartOperation` 不会撞上 `ErrOperationInProgress`。
   本次改动把单次任务里的 `StartOperation` 从 1 次变成 2 次，完全依赖这个顺序。
4. **恢复阶段仍可能拿到 `ErrOperationInProgress`**：更新期间用户在 UI 上手动发起了别的批量操作。
   不能静默吞掉，要作为任务失败原因报出去（文案见 §3.4）。
5. **`RunNow` 是 `go s.execute(...)`**，可与 tick 里的任务并发——两个更新任务撞车时，
   第二个在停服阶段就会拿到 `ErrOperationInProgress` 而失败。这是既有行为，本次不改，也不会被放大：
   恢复阶段沿用同一个失败路径。
6. **`Scheduler.Stop()` → ctx cancel**：`restoreInstances` 入口处先查 `ctx.Err()`，
   保证进程退出途中不会再去拉起实例（见 §3.2 最后一行）。
7. **倒计时只作用于停服阶段**：`Task.CountdownConfig()` 传给 `BatchStop`，恢复阶段传 nil，
   不会再白等一轮倒计时。
8. **前端会看到一轮「批量启动」**：恢复阶段照常走 `realtime.BroadcastBatchOperationStarted`，
   批量操作弹窗会显示一次启动流程。这是预期行为（用户应该看得到管理器在替他拉服），
   不需要屏蔽。
9. `runRestart` 与 `store` / `logStore` / `realignAll` 均不受影响，不动。

---

## 4. 代码改动

只改 `internal/schedule/scheduler.go` 一个文件（外加一个新测试文件）。

### 4.1 新增常量

```go
const (
	forceStopTimeout = 2 * time.Minute
	stopPollInterval = 3 * time.Second

	// restoreStartDelaySeconds 是更新后恢复启动时，实例之间的间隔秒数。
	// 批量启动本来就是串行的（StartServer 要等到启动检测完成才返回），天然有间隔，
	// 所以默认不额外等；机器吃力时把这里调大即可。
	restoreStartDelaySeconds = 0
)
```

### 4.2 改写 `runUpdate`

```go
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
		logger.GetLogger().Info("No instances to stop before scheduled update")
	case err != nil:
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
```

### 4.3 新增 `restoreInstances` 与三个纯函数

```go
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
	for _, n := range names {
		if n == name {
			return names
		}
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
```

### 4.4 不需要改的地方（确认清单）

- `internal/schedule/task.go`：不加字段（见 §8.2），`Validate` 不变。
- `internal/batchmanage/*`：不动。恢复走的是已有的 `BatchStart` 通路。
- `internal/updatemanage/*`：不动。
- `internal/instance/*`、`internal/state/*`：不动。
- `internal/webapi/scheduleapi/*`、前端：不动（没有新增字段）。

---

## 5. 测试计划

`runUpdate` 依赖三个全局单例（`batchmanage` / `updatemanage` / BadgerDB 状态库），
不适合直接单测；所以把可测的部分拆成了纯函数。新增 `internal/schedule/restore_test.go`：

| 用例 | 覆盖 |
|---|---|
| `TestAppendUnique` | 追加新名字 / 重复名字不追加 / 空切片 |
| `TestExcludeNames` | 剔除交集 / `exclude` 为空时原样返回 / 全部被剔除返回空 |
| `TestRestoreNote` | 有错误 / 有成功数 / 两者都无（返回空串） |
| `TestCollectStoppedByTask`（可选） | 若把「从 `[]*batchmanage.InstanceResult` 汇总成功项」也提成纯函数，则覆盖 success/failed/skipped/cancelled 四种状态的筛选 |

`batchmanage.InstanceResult` 字段是导出的，测试里可以直接构造，不需要起真实批量操作。

手工验证（在有实例的机器上）：

1. 起 2 个实例，建一个「每 1 小时」的更新任务，`RunNow` 触发。
2. 观察：批量停服 → 更新 SSE → **批量启动**三段依次出现在批量操作弹窗里。
3. 更新结束后 `GET /api/instances` 确认两个实例回到 `started`。
4. 执行日志（`GET /api/schedule/logs`）里 Message 应为
   `已停止 2 个实例并完成更新，已重新启动 2 个`。
5. 断网重跑一次，验证「更新失败也会恢复启动」，且 Message 是
   `<更新错误>（已恢复启动 2 个实例）`、任务标记为失败。

---

## 6. 验证清单

```powershell
go build ./...
go vet ./...
gofmt -l internal/schedule          # 输出应为空
go test ./internal/schedule/...      # 含新增的 restore_test.go
```

---

## 7. 风险与回滚

| 风险 | 影响 | 缓解 |
|---|---|---|
| 更新耗时 + 启动耗时叠加，任务总时长变长 | 后续定时任务被推迟 | 既有的串行语义本就如此；`tick` 以 `time.Now()` 推进 `NextRunAt`，不会连锁触发 |
| 恢复阶段撞上用户手动发起的批量操作 | 恢复失败，实例留在停止状态 | 报为任务失败并写进执行日志，用户可见（改动前是**静默**留在停止状态，只会更好不会更差） |
| 进程在更新中途被杀 | 内存里的待恢复列表丢失，实例留在停止状态 | 与改动前行为一致，不构成回退；持久化方案见 §8.1 |
| 某台实例更新后起不来（存档损坏等） | 任务判失败 | 其余实例照常拉起，失败名单进执行日志 |

回滚：改动集中在一个文件、一个函数 + 四个新函数，`git revert` 单个 commit 即可。

---

## 8. 后续可选项（本次明确不做）

### 8.1 跨进程持久化「待恢复列表」

**已升级为要实现的第二阶段，见 §9。**

采用的正是原先设想的那个折中：**不静默自动拉起**，而是落盘一份待恢复现场，
由前端弹出「确认恢复 / 忽略」的提示，用户点确认才执行。这样既不违背 `realignAll`
「进程停机期间错过的执行不补跑，不给用户开机惊喜」的既有取舍，也不会让实例无声无息地停一整天。

### 8.2 给 `Task` 加「是否恢复启动」开关

**不做的理由**：两条。一是当前行为（停完不管）本身就是缺陷，没有值得保留成可选项的价值；
二是 Go 的 JSON 零值陷阱——`RestoreAfterUpdate bool` 加进去后，`schedules.json` 里所有存量任务
反序列化都会得到 `false`，等于默认关掉这个修复。真要加必须用 `*bool` 或反向命名
（`NoRestoreAfterUpdate`），并同步改 `scheduleapi` DTO 与前端表单——收益不足以支撑这些改动面。

### 8.3 恢复启动的间隔可配置

`restoreStartDelaySeconds` 目前是包级常量 0。若实测发现同时拉起多个实例压力过大，
先把常量调大即可；提成 `Task` 字段同样受 §8.2 的零值问题约束，届时再评估。

---

## 9. 第二阶段：待恢复现场持久化 + 前端确认/忽略

> §1–§7 是第一阶段（已实施）：进程活着的时候，更新任务自己把停掉的实例拉回来。
> 本节是第二阶段：**进程没活到那一步**（更新途中被杀、调度器被停、恢复启动被别的批量操作顶掉）
> 时，把「我停了谁」这件事落盘，等下次前端连上来时弹一个提示，由用户决定**恢复**还是**忽略**。

### 9.1 为什么必须在停服之后、更新之前就落盘

最初的动机场景是「进程在更新中途被杀」。这决定了落盘时机不能是「恢复失败的时候」——
那时候进程已经没了，没人来写这个文件。

所以采用**写前日志（write-ahead）**的顺序：

```
批量停服 + 强停兜底
        ↓
写 pending_restore.json ← 「我停了这些，还没还回去」
        ↓
更新（进程可能在这里被杀）
        ↓
恢复启动
        ↓
全部起回来了 → 删除 pending_restore.json
部分没起来   → 重写为剩下那几台
```

文件存在 = 「有一批实例是被管理器停掉且还没还回去的」。这个不变式在任何一个时间点被杀都成立。

### 9.2 数据结构与文件

`{BaseDir}/pending_restore.json`，与 `schedules.json` / `schedule_logs.json` 并列，
复用同一套 `writeJSONAtomic`（进程随时可能被杀，直接覆写会留下半截文件）。

```go
// PendingRestore 是一份「被管理器停掉、但还没还回去」的现场。
//
// 文件存在本身就是语义：有实例欠着一次启动。内容只用来给用户看和执行恢复，
// 不参与任何状态判断——实例的权威状态始终在 BadgerDB 里。
type PendingRestore struct {
	// Instances 欠着启动的实例名，按当初的停服顺序
	Instances []string `json:"instances"`

	// 任务身份冗余存一份：任务可能已经被删了，提示里还要说得清是谁干的
	TaskID   string `json:"task_id"`
	TaskName string `json:"task_name"`

	// Reason 面向用户，直接展示在提示里：
	// 「更新过程中管理器退出」/「恢复启动未能发起: ...」/「2/3 个实例启动失败：...」
	Reason string `json:"reason"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
```

新文件 `internal/schedule/pendingrestore.go`，结构对齐 `store.go`：

```go
const pendingRestoreFileName = "pending_restore.json"

type pendingStore struct {
	mu      sync.RWMutex
	path    string
	pending *PendingRestore // nil = 没有待恢复现场
}

func newPendingStore() *pendingStore {
	return newPendingStoreAt(filepath.Join(cfgpkg.BaseDir, pendingRestoreFileName))
}

// newPendingStoreAt 供测试注入临时目录——BaseDir 是包级变量，
// 直接改它会让并行跑的其它测试读到别人的路径。
func newPendingStoreAt(path string) *pendingStore { return &pendingStore{path: path} }

func (p *pendingStore) load()                                    // 文件不存在 = 无现场，不算错误
func (p *pendingStore) Get() (*PendingRestore, bool)             // 返回副本
func (p *pendingStore) Merge(taskID, taskName, reason string, names []string) error // 并集，见 9.3
func (p *pendingStore) Resolve(handled []string, reason string) error // 差集，见 9.3.1
func (p *pendingStore) Clear() error                             // 删文件（只给「忽略」用）
```

`load()` 的容错与 `store.load()` 一致：文件不存在 / 空文件 → 无现场；**JSON 解析失败也只记 WARN
并当作无现场，但不删文件**——那份坏文件是排查现场的唯一线索，静默删掉等于毁证。

### 9.3 `Merge` 而不是覆盖

连续两个执行点都失败时，第二次不能把第一次的名单冲掉——那样第一批实例就永远没人记得了。
`Merge` 用已有的 `appendUnique` 做并集，保持首次出现顺序；
`TaskID`/`TaskName`/`Reason`/`UpdatedAt` 取最新一次（提示里显示最近一次的原因，名单是累积的），
`CreatedAt` 保留最早那次。

### 9.3.1 收尾也必须是差集（`Resolve`），不能是全量覆盖

同样的道理反过来也成立：恢复启动跑完之后**不能** `Clear()` 或按自己的结果整份 `Replace()`。

初稿设计的 `Replace(names, reason)` 是全量覆盖语义，它和 `Merge` 之间存在 **lost update**：

```
T1  手动恢复的批量跑完（bm.current 已置 nil），准备写结果
T2  定时更新恰好到点，停掉实例 A，Merge([A]) 写进现场
T3  T1 那个 goroutine 才执行到 Clear() —— A 被抹掉，从此没人记得它
```

`pendingStore` 有锁，文件不会写坏，但**逻辑上的更新丢失**照样发生：T1 那次收尾持有的是 T0 时刻的旧名单。

所以收尾统一走差集：

```go
// Resolve 从现场里移掉已经处理完的实例；移完后如果一个都不剩，就删文件。
//
// 用差集而不是整份覆盖：收尾时手里的名单是这一轮开始时的快照，
// 期间另一条路径（定时更新、另一次恢复）完全可能往现场里加了新的实例。
// 整份覆盖会把那些新加的一并抹掉，而它们恰恰是还没人管过的。
func (p *pendingStore) Resolve(handled []string, reason string) error
```

- 恢复成功的（`Restored`）和 CAS 拒绝的（`Skipped`，说明已经在跑了）都算 `handled`，从名单里移掉。
- `Failed` 不传进去，它们继续欠着。
- `reason` 用本轮的失败原因；名单被清空时 `reason` 无意义，直接删文件。

`Clear()` 只保留给「用户点忽略」这一条路径——那是用户明确要求丢弃**整个**现场，全量语义正是想要的。

### 9.4 对 §4 已实现代码的修订

第一阶段的 `restoreInstances` 只返回 `(int, error)`，而落盘需要知道**具体哪几台没起来**，所以要改签名。
这是对已实现代码的修订，不是新增：

```go
// restoreOutcome 一次恢复启动的结果明细。
//
// 只返回数量不够用：pending_restore.json 要重写成「还欠着的那几台」，得知道名字。
type restoreOutcome struct {
	Restored []string // 启动成功
	Failed   []string // 启动失败，仍然欠着
	Skipped  []string // CAS 拒绝，通常是已经被别人启动了，不欠了
}

func restoreInstances(ctx context.Context, names []string) (restoreOutcome, error)
```

连带改动：

| 位置 | 改法 |
|---|---|
| `restoreNote(restored int, err error)` | 改成 `restoreNote(out restoreOutcome, err error)`，内部用 `len(out.Restored)` |
| `runUpdate` 里两处 `restored, restoreErr := restoreInstances(...)` | 变量改成 `out, restoreErr` |
| 成功分支的 `已重新启动 %d 个` | 改用 `len(out.Restored)` |
| `restore_test.go` 的 `TestRestoreNote` | 用例改成传 `restoreOutcome` |

`Skipped` 不计入「还欠着」：CAS 拒绝几乎总是因为这台已经在跑了（用户手动启动，或前一次恢复其实成功了），
再把它记进待恢复名单，只会让提示反复弹出一台根本不需要处理的实例。

### 9.5 落盘与清理的五个触点

全部集中在 `runUpdate` 里。`Scheduler` 新增 `pending *pendingStore` 字段，在 `Initialize()` 里建好并 `load()`：

| # | 位置 | 动作 |
|---|---|---|
| ① | 停服 + 强停兜底之后、`updatemanage.Start()` 之前 | `pending.Merge(t.ID, t.Name, "更新过程中管理器退出", stoppedByTask)`。Reason 写的是**假如进程死在这里**该显示的话；正常跑完会被后面的触点收掉 |
| ② | ③ 等待退出超时那条路径 | 恢复完后 `pending.Resolve(out.Restored ∪ out.Skipped, "实例无法停止，更新已取消")` |
| ③ | 更新结束、恢复完成（全成功或部分失败） | `pending.Resolve(out.Restored ∪ out.Skipped, reason)`——全部处理完时 `Resolve` 内部会删掉文件，不需要单独调 `Clear` |
| ④ | 更新结束、恢复**未能发起**（`ErrOperationInProgress` 等） | 一台都没处理，`handled` 为空；只更新 Reason 让提示说得清为什么还挂着 |
| ⑤ | `<-ctx.Done()`（调度器 `Stop()`／进程正在退出） | 先 `pending.Merge(..., "调度器停止，未恢复启动")` 再返回 `ctx.Err()`。这一条最容易漏——第一阶段这条路径是直接 return 的 |

落盘失败（磁盘满、权限）不改变任务本身的成败，只记一条 ERROR——与 `logs.append` 的既有处理一致。

### 9.6 手动恢复的入口：`Scheduler` 新增三个方法

```go
var (
	ErrNoPendingRestore  = errors.New("没有待恢复的实例")
	ErrRestoreInProgress = errors.New("恢复启动正在进行中")
)

// GetPendingRestore 返回待恢复现场，没有则第二个返回值为 false。
func (s *Scheduler) GetPendingRestore() (*PendingRestore, bool)

// ConfirmPendingRestore 后台执行一次恢复启动，立即返回。
// 进度走 batchmanage 既有的 SSE/WS，结果追加一条执行日志。
func (s *Scheduler) ConfirmPendingRestore() error

// IgnorePendingRestore 丢弃现场，之后不再提示。实例保持停止状态。
func (s *Scheduler) IgnorePendingRestore() error
```

`restoreInFlight atomic.Bool` 是**所有恢复启动路径的公共闸**，不只护住手动确认这一条：
`runUpdate` 里那两处 `restoreInstances` 调用同样要先拿到它（拿不到就跳过恢复、把实例留在现场里）。
只护手动路径的话，定时更新的自动恢复与用户点的手动恢复会同时在飞，两边各自持有一份旧名单去收尾。

不能只依赖 `batchmanage` 的 `ErrOperationInProgress`：那是**批量执行**的互斥，
而这里要互斥的是「读名单 → 跑 → 按名单收尾」这整段，范围比一次批量操作大。

```go
func (s *Scheduler) ConfirmPendingRestore() error {
	p, ok := s.pending.Get()
	if !ok {
		return ErrNoPendingRestore
	}

	// 同步预检：有批量在跑就当场回 409，别先回 200 再在后台悄悄失败。
	// 用户点了「恢复启动」却在几秒后看到提示框重新弹出来，比直接说「现在忙」更费解
	if batchmanage.GetGlobalManager().IsRunning() {
		return ErrRestoreInProgress
	}
	if !s.restoreInFlight.CompareAndSwap(false, true) {
		return ErrRestoreInProgress
	}

	// 后台跑：批量启动可能要好几分钟，HTTP 请求不能挂在这里等
	go func() {
		defer s.restoreInFlight.Store(false)

		startedAt := time.Now()
		// ctx 用 Background 而不是调度循环的 ctx：这是用户手动发起的动作，
		// 不该因为调度器恰好在这时被停掉而半途取消
		out, err := restoreInstances(context.Background(), p.Instances)

		s.finishPendingRestore(p, out, err, startedAt) // 落盘 + 执行日志 + WS 推送
	}()
	return nil
}
```

`finishPendingRestore` 做三件事：

1. `pending.Resolve(out.Restored ∪ out.Skipped, reason)`（差集语义，见 §9.3.1）。
2. 追加一条 `RunRecord`（`TaskType: TaskUpdate`、`Trigger: TriggerManual`、
   `TaskName: p.TaskName + "（恢复启动）"`），让这次手动恢复也出现在执行日志面板里——
   否则用户点完确认，界面上没有任何痕迹说明发生过什么。
3. 广播 WS（见 §9.7），让所有开着的页面同步收起提示。

### 9.7 WS 事件：`pending_restore`

只靠前端挂载时拉一次是不够的：现场也可能在**页面开着的时候**产生（定时更新恰好在用户看着的时候失败）。
`realtime/hub.go` 新增：

```go
// BroadcastPendingRestore 推送「待恢复现场」的变化。
//
// exists=false 表示现场已被恢复或忽略，前端据此收起提示——
// 多个页面同时开着时，一个人点了确认，其他人的提示也要跟着消失。
func BroadcastPendingRestore(exists bool, data map[string]any) {
	msg := "有实例在定时更新后未恢复启动"
	if !exists {
		msg = "待恢复实例已处理"
	}
	BroadcastServerEventWithData("pending_restore", "", msg, "warning", data)
}
```

`data` 是 `PendingRestore` 的扁平字段（`instances` / `task_name` / `reason` / `created_at`），
与 `BroadcastScheduleRun` 的做法一致：`realtime` 不能反向 import `schedule`（会成环），只接受 `map`。

触发点：§9.5 的 ①②④⑤（现场产生/变更，`exists=true`）；
③、`IgnorePendingRestore`、`finishPendingRestore` 清空时（`exists=false`）。

### 9.8 API：`scheduleapi` 新增三个路由

```go
s.GET("/pending-restore", h.getPendingRestore)              // 查询现场
s.POST("/pending-restore/confirm", h.confirmPendingRestore)  // 确认 → 后台恢复
s.DELETE("/pending-restore", h.ignorePendingRestore)         // 忽略 → 删文件
```

| 路由 | 返回 |
|---|---|
| `GET` | `{"success":true,"data":{"pending":{...}}}`，无现场时 `"pending":null`（**不是 404**：前端每次挂载都会调它，404 会在控制台刷一片红） |
| `POST .../confirm` | 成功 `{"success":true,"message":"已开始恢复启动"}`；`ErrNoPendingRestore` → 404；`ErrRestoreInProgress` → 409 |
| `DELETE` | `{"success":true,"message":"已忽略，实例保持停止状态"}`；无现场同样返回成功（幂等：两个页面同时点忽略不该报错） |

鉴权沿用 `authapi` 中间件，与其它 `/api/schedule/*` 一致，无需特殊处理。

### 9.9 前端

#### 9.9.1 API 封装 `app/src/apis/api.js`

```js
// ==================== 待恢复实例 ====================

// 查询「定时更新后未恢复启动」的现场，无现场时 data.pending 为 null
export function getPendingRestore() {
    return apiClient.get('/api/schedule/pending-restore')
}

// 确认恢复：后台批量启动，接口立即返回
export function confirmPendingRestore() {
    return apiClient.post('/api/schedule/pending-restore/confirm')
}

// 忽略：删除现场记录，不再提示（实例保持停止状态）
export function ignorePendingRestore() {
    return apiClient.delete('/api/schedule/pending-restore')
}
```

#### 9.9.2 新组件 `app/src/components/PendingRestoreDialog.vue`

TDesign `t-dialog`，参照 `CountdownConfirmDialog.vue` 的写法（`attach="body"`、自管 `visible`）：

- **内容**：任务名 + 发生时间 + `reason` + 实例名列表（`t-tag` 平铺）。
- **确认按钮**（`theme="primary"`，文案「恢复启动」）：调 `confirmPendingRestore()` →
  成功后关闭对话框 + `MessagePlugin.success('已开始恢复启动，可在批量操作面板查看进度')`。
- **忽略按钮**（`variant="outline"`，文案「忽略」）：**二次确认**（`DialogPlugin.confirm`），
  文案要说清后果——「忽略后不再提示，这些实例将保持停止状态，需要时请手动启动」。
  不可撤销的操作不能一键完成。确认后调 `ignorePendingRestore()`。
- **不提供右上角 × 静默关闭**：`:close-btn="false"` + `:close-on-overlay-click="false"`。
  这个提示的价值就在于「不处理就一直在」，随手点掉等于回到改动前的状态。
  用户想暂时不处理，刷新页面它会再出现——这是刻意的。

#### 9.9.3 挂载点：`App.vue`

放在 `header-tools` 里 `WSEventNotification` 旁边，与它同级：

```vue
<WSEventNotification/>
<PendingRestoreDialog/>
```

- 放 `App.vue` 而不是 `ScheduleManager.vue`：用户不一定会点进定时任务页，
  而这个提示的全部意义就是「一进来就看到」。
- `App.vue` 在登录页/首次引导时走 `isStandalone` 分支、不渲染主框架，
  所以未登录状态下不会弹——不需要额外判断。
- 组件内 `onMounted` 调一次 `getPendingRestore()`，有现场就 `visible = true`。

#### 9.9.4 WS 接入 `app/src/store/serverStore.js`

`handleServerEvent` 的 `switch` 里加一个分支，照 `schedule_run` 的回调模式：

```js
// 待恢复现场发生变化（产生 / 已恢复 / 已忽略）。
// 多个页面开着时，一个人处理完，其他人的对话框也得跟着收起
case 'pending_restore':
    serverStore.pendingRestoreCallbacks.forEach(cb => cb('pending_restore', event))
    break
```

`serverStore` 加 `pendingRestoreCallbacks: []` 及对应的注册/注销函数（照 `scheduleCallbacks` 抄）。
`PendingRestoreDialog` 在 `onMounted` 注册、`onUnmounted` 注销；回调里**重新拉一次 `getPendingRestore()`**，
不直接信 WS 里的 data——以服务端的文件为准，避免两条信息源打架。

#### 9.9.5 与批量操作弹窗的关系

确认恢复后，后端走的是常规 `batchmanage.BatchStart`，会广播 `batch_operation_started`。
`ServerManager.vue` 的 `BatchOperationDialog` 是否会**自动弹出**需要在实施时核实：
若不会自动弹，就只靠 `MessagePlugin` 提示用户去批量面板看——**不要**为此改动批量弹窗的既有触发逻辑，
那会波及所有批量场景。

### 9.10 边界情况

| 情况 | 行为 |
|---|---|
| 现场里的实例已被用户手动启动 | CAS 拒绝 → 计入 `Skipped`，不算失败，也不再欠着 |
| 现场里的实例已被删除 / 改名 | `operable` 对 `BatchStart` 一律放行，CAS 读不到状态记录当 stopped → 会尝试启动，`StartServer` 因读不到 `instance_config.ini` 失败 → 计入 `Failed` 留在名单里。用户在提示里看到一个不存在的实例名，点忽略即可 |
| 两个页面同时点「确认」 | 第二个拿到 409 `ErrRestoreInProgress`，前端提示「恢复启动正在进行中」 |
| 确认恢复时有别的批量在跑 | `StartOperation` 返回 `ErrOperationInProgress` → `restoreInstances` 报错 → 现场**保留**，提示继续在，用户可稍后重试 |
| `pending_restore.json` 被手工改坏 | `load()` 记 WARN 当作无现场，不删文件 |
| 磁盘满导致落盘失败 | 记 ERROR，任务成败不受影响；退化成第一阶段的行为（进程活着就能恢复，被杀就丢） |

### 9.11 实施顺序

每一步都能单独 `go build ./...` 通过：

1. **`pendingrestore.go`**：`PendingRestore` + `pendingStore`（`load`/`Get`/`Merge`/`Replace`/`Clear`）+ 单测。
2. **`restoreInstances` 改签名**（§9.4）：`restoreOutcome`、`restoreNote`、`runUpdate` 两处调用、`restore_test.go`。
3. **落盘触点**（§9.5）：`Scheduler` 加 `pending` 字段，`Initialize()` 里 `load()`，`runUpdate` 五个触点接上。
4. **`realtime.BroadcastPendingRestore`**（§9.7）+ 在触点处调用。
5. **`Scheduler` 三个方法**（§9.6）+ `restoreInFlight`。
6. **`scheduleapi` 三个路由**（§9.8）。
7. **前端**：`api.js` 三个封装 → `serverStore` 事件分支与回调 → `PendingRestoreDialog.vue` → `App.vue` 挂载。
8. **文档**：`CLAUDE.md` 运行时目录树加 `pending_restore.json`（本地文件，不入库）；
   `docs/API_REFERENCE.md` 补三个端点；本文档状态行改为「已实施」。

### 9.12 测试

| 层 | 用例 |
|---|---|
| `pendingStore`（`pendingrestore_test.go`，用 `t.TempDir()` + `newPendingStoreAt`） | 空目录 → 无现场；`Merge` 后 `Get` 拿得到；二次 `Merge` 做并集且不丢第一批；`Replace` 覆盖名单；`Clear` 删文件后 `Get` 为空；坏 JSON → 无现场且文件仍在 |
| `restoreNote` / `restoreOutcome` | 沿用 `restore_test.go`，改成新签名 |
| 手工端到端 | ① 建更新任务 → `RunNow` → 停服完成后**杀掉进程** → 重启管理器 → 前端应弹提示，实例名与停掉的一致；② 点「忽略」→ 提示消失、刷新不再出现、`pending_restore.json` 已删除；③ 重复 ①，改点「确认」→ 批量启动跑起来，结束后提示消失，执行日志多一条「恢复启动」记录；④ 两个浏览器标签同开，一个点确认，另一个的提示应自动收起 |

### 9.13 风险

| 风险 | 缓解 |
|---|---|
| 提示不可关闭（只能确认/忽略）可能让用户觉得被打扰 | 这是刻意的（§9.9.2）；「忽略」一步即可永久消除，成本足够低 |
| 用户长期不处理，现场一直挂着并累积 | `Merge` 只增不减，但记的都是真实欠着启动的实例，不是噪音 |
| 手动确认恢复用 `context.Background()`，进程退出时不会被取消 | 与 `batchmanage` 既有行为一致（`BatchManager.Shutdown()` 会取消当前操作），不额外引入泄漏 |

### 9.14 恢复启动与其它操作撞车

手动确认恢复是一段能跑几分钟的后台操作，期间调度器不会停下来等它。下面把三类碰撞逐一定死。

#### 9.14.1 恢复启动进行中，定时**更新**任务到点

| 时刻 | 发生什么 |
|---|---|
| T0 | 用户点确认 → `restoreInFlight` 置位 → `StartOperation(BatchStart)` 成为 `bm.current` |
| T1 | `tick` 到点，`runUpdate` 调 `StartOperation(BatchStop)` → **`ErrOperationInProgress`** |
| T2 | `runUpdate` 走 `case err != nil` 分支，任务判失败，`NextRunAt` 照常推进 |

结论：**安全但会跳过一轮**。实例状态不会错乱——更新压根没开始，也就不会在实例正被拉起时去停它。
代价是这次更新要等到下一个执行点（可能 24 小时后）。

两处要改：

1. **错误文案要说清是被谁顶掉的**。现在只会记一句 `a batch operation is already running`，
   用户在执行日志里看不出为什么。改成先判一次：

   ```go
   if errors.Is(err, batchmanage.ErrOperationInProgress) {
       return "", fmt.Errorf("更新前的批量停服未能启动：有批量操作正在进行（可能是待恢复实例的启动），本次更新已跳过")
   }
   ```

2. **不做自动重试**。等一会儿再试听着更聪明，但恢复启动可能要跑十几分钟，
   在 `tick` 的串行 goroutine 里空等会把后面所有任务一起堵死；而定时更新本来就是低频操作，
   跳一轮的代价远小于堵住调度循环。用户看到日志后手动触发一次即可。

#### 9.14.2 恢复启动进行中，定时**重启**任务到点

同样撞在 `batchmanage` 单例上，`runRestart` 拿到 `ErrOperationInProgress` 后任务判失败。

**这恰恰是想要的结果**：待恢复的实例此刻正处于 `start_initialization`，
重启的预检（`IsStoppable`）和 CAS 都只认 `started`，就算放它进去也是整批 skip。
与其让一次「重启」在半路上和「启动」抢同一批实例，不如干脆不跑。

文案同 §9.14.1，要指明是被恢复启动顶掉的。

#### 9.14.3 反向：定时任务正在跑，用户点「确认恢复」

同步预检（§9.6）会先看 `batchmanage.GetGlobalManager().IsRunning()`，直接回 **409**，
前端提示「当前有批量操作正在进行，请稍后重试」。提示框**保持不动**，用户稍后可以再点。

如果没有这道预检，就会退化成：接口先回 200「已开始恢复启动」，几秒后后台失败，
提示框又被 WS 推回来——用户完全看不懂发生了什么。

预检存在 TOCTOU（检查完到真正 `StartOperation` 之间批量可能刚好开始），
但那条路径已经被 `restoreInstances` 的错误处理兜住了（现场保留、提示重弹），
预检只是把绝大多数情况变成一个干脆的同步错误。

#### 9.14.4 用户手动启动了现场里的某个实例

分两种时机，都不会出问题：

| 时机 | 结果 |
|---|---|
| 恢复启动**之前**手动启动 | 确认恢复时该实例状态是 `started` / `start_initialization`，`batchDoCAS(BatchStart)` 只接受 `stopped / start_failed / stop_failed / restart_failed / ""` → 拒绝 → 记为 `Skipped` → 计入 `handled` 从现场移除。**不会被启动第二次** |
| 恢复启动**进行中**手动启动 | 批量是串行的，轮到这台时 CAS 同样拒绝 → `Skipped`。反过来，如果批量先到，用户的单实例启动接口会拿到 CAS 失败并返回「当前状态不允许启动」 |

这里的底座是**每个实例的状态 CAS**，不是任何一把全局锁：单实例启停走
`serverapi` → `statepkg.CompareAndSwapInstanceState`，批量走 `batchDoCAS`，两条路径改的是同一条状态记录，
BadgerDB 的 CAS 保证只有一个赢家。所以「批量启动」与「单实例启动」天然互斥到实例粒度，
不需要额外加锁。

> 注：`CLAUDE.md` 里「The API server uses a mutex (`serverActionsLock`)」的说法已经过时，
> 代码里不存在这个符号了，实际靠的就是上面这套 per-instance CAS。实施本节时顺手把那句话修掉。

如果那台被手动启动的实例**启动失败**（状态落到 `start_failed`），CAS 是接受这个状态的，
恢复启动会再试一次——这是合理的，用户的手动尝试失败了不代表这台不该被拉起来。

#### 9.14.5 汇总

| 碰撞 | 谁赢 | 现场文件 | 用户看到 |
|---|---|---|---|
| 恢复中 → 定时更新到点 | 恢复 | 不动 | 执行日志一条失败：本次更新被跳过 |
| 恢复中 → 定时重启到点 | 恢复 | 不动 | 执行日志一条失败：本次重启被跳过 |
| 定时任务中 → 点确认恢复 | 定时任务 | 不动 | 409，提示稍后重试，提示框保留 |
| 恢复中 → 手动启动某实例 | 先到者 | 该实例计入 `handled` 移除 | 后到的一方提示「当前状态不允许启动」 |
| 恢复收尾 与 定时更新落盘 并发 | 两者都保留 | `Merge`/`Resolve` 都是集合运算，无覆盖 | 提示里的名单是并集，不丢实例 |

---

## 10. 批量操作的可取消性与来源标识

> 本节回答两个问题：这些自动发起的批量能不能被用户取消（能，但取消后的行为有两个 bug 要修），
> 以及用户怎么知道眼前这轮批量是谁发起的（新增 `BatchOrigin`）。

### 10.1 是的，三条路径都走 `batchmanage`，因此都可取消

| 批量 | 发起处 | 类型 |
|---|---|---|
| 更新前的停服 | `schedule.runUpdate` | `BatchStop` |
| 更新后的恢复启动（自动） | `schedule.restoreInstances`（`runUpdate` 调用） | `BatchStart` |
| 待恢复现场的恢复启动（手动确认） | `schedule.restoreInstances`（`ConfirmPendingRestore` 调用） | `BatchStart` |

三者都是 `batchmanage.StartOperation`，所以 `POST /api/server/batch/cancel`（`CancelCurrent`）
和 `POST /api/server/batch/skip`（单实例跳过）对它们**同样有效**。

**这是对的，不要去堵。** 用户必须随时能叫停管理器自动动他的服务器——尤其是他没亲手发起的那些操作。
问题不在「能不能取消」，而在「取消之后发生了什么」，下面两条都是错的。

### 10.2 Bug 1（既有）：取消定时更新的停服批量，并不会中止更新

`runUpdate` 里 `awaitBatch` 之后的代码是**无条件**往下走的：

```go
if err := awaitBatch(ctx, op); err != nil { return "", err }   // 取消时这里返回 nil！
...
if alive := procpkg.ListAliveInstances(); len(alive) > 0 {
    for _, name := range alive { instancepkg.ForceStopServer(name) }   // 照杀不误
}
```

`awaitBatch` 只在**任务自己的 ctx** 结束时返回错误；用户取消的是**批量操作的 ctx**，
此时 `op.Done()` 正常关闭，`awaitBatch` 返回 `nil`。于是：

> 用户点「取消」→ 批量停服停下了 → 紧接着强停兜底把所有还活着的实例 `taskkill` 一遍 → 更新照常进行。

比不给取消按钮还糟：用户的意图被无视，而且服务器死得更难看（跳过了 RCON `saveworld` 的优雅停止）。
倒计时阶段被取消也走同一条路——玩家还在倒计时里，管理器已经把进程杀了。

**这是第一阶段之前就存在的行为**，不是本次改动引入的，但既然要在这条链路上加自动恢复，必须一并修掉。

修法：批量结束后先判是否被取消，被取消就放弃本次更新。

> ⚠️ **本小节最初提议的判据已作废，见 §12.2。**
> 原方案是「`InstanceResults` 里出现 `InstanceCancelled` 就算整批被取消」，
> 但用户取消**单台**实例的倒计时也会写这个状态（`manager.go:755`），会被误报成整批取消。
> 正确做法是让 `batchmanage` 用 `cancelledAll atomic.Bool` 显式记账，
> 通过 `op.WasCancelled()` 读取。下面的中止分支照写，判据换成 `op.WasCancelled()`。

```go
if err := awaitBatch(ctx, op); err != nil {
	return "", err
}
if op.WasCancelled() {
	// 用户中途叫停：不要再往下走强停兜底，那等于无视他的取消把服务器硬杀一遍。
	// 已经停掉的那批要还回去——它们是被这次半途而废的任务连累的
	out, restoreErr := restoreInstances(ctx, stoppedByTask)
	return "", fmt.Errorf("批量停服被取消，本次更新已放弃%s", restoreNote(out, restoreErr))
}
```

放在强停兜底**之前**。此时 `stoppedByTask` 里已经装着倒计时结束前就停掉的那几台，照样要恢复。

### 10.3 Bug 2（第一阶段代码）：恢复启动被取消时，任务会报成功

已实现的 `restoreInstances` 只认两种结果：

```go
switch r.Status {
case batchmanage.InstanceSuccess: restored++
case batchmanage.InstanceFailed:  failed = append(failed, r.InstanceName)
}
```

用户取消恢复启动后，剩下的实例是 `InstanceCancelled`，两个分支都不命中，被静默丢弃。
于是 `failed` 为空 → 返回 `nil` 错误 → 任务判**成功**，日志写「已停止 5 个实例并完成更新，已重新启动 2 个」。
5 停 2 起，却算成功。

修法：`restoreOutcome`（§9.4）再加一个字段，并让它参与成败判定：

```go
type restoreOutcome struct {
	Restored  []string // 启动成功
	Failed    []string // 启动失败，仍然欠着
	Skipped   []string // CAS 拒绝，通常是已经在跑了，不欠了
	Cancelled []string // 用户取消/未轮到，仍然欠着
}
```

- 成败判定：`len(Failed) > 0 || len(Cancelled) > 0` → 返回错误。
  文案分开写，别把「被取消」混进「启动失败」里——前者是用户主动的，后者是故障。
- `handled`（§9.3.1）保持 `Restored ∪ Skipped`，`Cancelled` 继续留在待恢复现场里，
  提示框会重新弹出来让用户再决定一次。这正是想要的：他取消的是「现在别启动」，不是「以后也别管了」。
- `InstancePending` / `InstanceSkipRequested` 这类没轮到的残留状态归入 `Cancelled` 同类处理
  （批量已经结束却还是 pending，只可能是被整体取消了）。

### 10.4 `BatchOrigin`：这一轮批量是谁发起的

#### 10.4.1 为什么必须加

第一阶段之前，`batchmanage` 只有一个入口（用户在 UI 上点）。现在多了三个自动入口（§10.1），
用户会看到批量操作弹窗凭空开始跑，而弹窗标题永远是写死的「服务器批量操作」，
日志第一行是 `Batch stop started with 3 instances`。他无从判断这是自己刚点的、
定时任务触发的、还是待恢复现场的恢复启动——而这三者的正确反应完全不同。

#### 10.4.2 类型

`batchmanage/manager.go`：

```go
// BatchOriginKind 说明这一轮批量是谁发起的。
//
// 加它的直接原因：schedule 现在会自动发起停服/启动，用户看到弹窗自己动起来时，
// 必须能一眼看出是谁干的，否则唯一的合理反应就是恐慌点取消。
type BatchOriginKind string

const (
	OriginUser            BatchOriginKind = "user"             // 用户在 UI 上发起
	OriginScheduleRestart BatchOriginKind = "schedule_restart" // 定时重启任务
	OriginScheduleUpdate  BatchOriginKind = "schedule_update"  // 定时更新任务的停服阶段
	OriginUpdateRestore   BatchOriginKind = "update_restore"   // 更新后自动恢复启动
	OriginManualRestore   BatchOriginKind = "manual_restore"   // 用户确认的待恢复启动
)

// BatchOrigin 批量操作的来源。Label 面向用户，直接显示在弹窗标题和日志里，
// 所以要带上具体是哪个任务——只说「定时任务」在有多个任务时等于没说。
type BatchOrigin struct {
	Kind  BatchOriginKind `json:"kind"`
	Label string          `json:"label"`
}
```

Label 约定：

| Kind | Label |
|---|---|
| `user` | `手动批量操作` |
| `schedule_restart` | `定时任务「每日重启」` |
| `schedule_update` | `定时任务「每日更新」· 更新前停服` |
| `update_restore` | `定时任务「每日更新」· 更新后恢复启动` |
| `manual_restore` | `恢复更新前停止的实例` |

#### 10.4.3 签名改动：位置参数，不用可选项

```go
func (bm *BatchManager) StartOperation(
	opType BatchOperationType,
	instances []string,
	delaySeconds int,
	cdCfg *countdown.Config,
	origin BatchOrigin,      // 新增
) (*BatchOperation, error)
```

刻意**不用**函数式选项或变参：那样旧调用点不改也能编译，会留下一批 Kind 为空的批量，
而「不知道是谁发起的」正是这次要消灭的状态。位置参数强制四个调用点各自表态，编译器替我们查漏。

四个调用点：

| 文件 | 传什么 |
|---|---|
| `batchmanage/api.go` `handleBatchOperation` | `BatchOrigin{OriginUser, "手动批量操作"}` |
| `schedule/scheduler.go` `runRestart` | `BatchOrigin{OriginScheduleRestart, fmt.Sprintf("定时任务「%s」", t.Name)}` |
| `schedule/scheduler.go` `runUpdate` 停服 | `BatchOrigin{OriginScheduleUpdate, fmt.Sprintf("定时任务「%s」· 更新前停服", t.Name)}` |
| `schedule/scheduler.go` `restoreInstances` | 由调用方传入：自动路径给 `OriginUpdateRestore` + 任务名，手动确认给 `OriginManualRestore`。**`restoreInstances` 要多收一个 `origin BatchOrigin` 参数**——这是它区分两种调用场景的唯一途径 |

#### 10.4.4 暴露到哪里

1. **`BatchOperation.Origin` 字段**（带 json tag），随 `GET /api/server/batch/status` 一起返回。
2. **首条日志**：`runBatchOperation` 里那句 `Batch %s started with %d instances` 改成带 Label，
   例如 `[定时任务「每日更新」· 更新前停服] 批量停止开始，共 3 个实例`。
   日志历史会回放给后连上的客户端，这是最省事的传达途径。
3. **WS**：`realtime.BroadcastBatchOperationStarted(opType string, totalInstances int)` 加两个参数
   `originKind, originLabel string`，塞进 `data`。只有 `manager.go` 一个调用点。
   前端 `serverStore` 的 `batch_started` 分支把它们存进 `batchOrigin` 供弹窗读取。
   > `realtime` 不能 import `batchmanage`（会成环，`batchmanage` 已经 import 了 `realtime`），
   > 所以这里传的是两个 string 而不是 `BatchOrigin` 结构体——与 `BroadcastScheduleRun` 用 `map` 的理由相同。
4. **前端 `BatchOperationDialog.vue`**：写死的 `header="服务器批量操作"` 改成动态——
   有 Label 就显示 `服务器批量操作 · {label}`。
   另外在弹窗顶部状态条上，非 `user` 来源的批量加一行提示：
   `本次操作由「{label}」自动发起`，让「我没点过为什么在跑」当场有答案。
   弹窗打开时也要从 `GET /api/server/batch/status` 读一次 origin（用户可能是批量跑起来之后才打开弹窗的）。

#### 10.4.5 顺带修掉的日志噪音

`BroadcastBatchOperationStarted` / `Progress` / `Completed` 现在发的是英文（`Batch stop started with 3 instances`），
而前端其它提示都是中文。加 Origin 时一并改成中文，`WSEventNotification` 里就不会中英夹杂。
这是纯文案改动，不影响 `event_type` 与 `data` 的字段名（前端按字段名取值，不解析文案）。

### 10.5 对前面章节的修订

| 章节 | 修订 |
|---|---|
| §2 目标 5「不改 `batchmanage` 任何一行」 | **作废**。`BatchOrigin` 必须落在 `batchmanage` 里——来源是批量操作自身的属性，塞在 `schedule` 侧无法传达给 `/status` 和 WS 的订阅者。改为：`updatemanage` / `instance` / `state` 不动 |
| §3.2 退出路径矩阵 | 新增一行「批量停服被用户取消」→ 放弃更新 + 恢复已停的（§10.2） |
| §9.4 `restoreOutcome` | 加 `Cancelled` 字段（§10.3） |
| §9.6 `restoreInstances` 签名 | 多收一个 `origin BatchOrigin` |

### 10.6 实施顺序（接在 §9.11 之后）

1. `batchmanage`：`BatchOriginKind` / `BatchOrigin` / `StartOperation` 签名 / `BatchOperation.Origin` / 首条日志。
2. `realtime`：`BroadcastBatchOperationStarted` 加 origin 参数 + 三条批量广播文案改中文。
3. 四个调用点补 origin（编译器会一个个报出来）。
4. `schedule`：`op.WasCancelled()`（§12.2）+ §10.2 的中止分支 + §10.3 的 `Cancelled` 字段与成败判定。
5. 前端：`serverStore` 存 origin → `BatchOperationDialog` 标题与提示条 → `/status` 兜底读取。
6. 测试：`batchmanage/manager_test.go` 补一条「取消后 `InstanceResults` 里存在 `InstanceCancelled`」的断言
   （见 §12.2：判据改用 `cancelledAll` 显式标志，测试要钉住「单实例倒计时取消不算整批取消」）。

### 10.7 验证

```powershell
go build ./... ; go vet ./... ; go test ./internal/schedule/... ./internal/batchmanage/...
```

手工：① 定时更新任务设 60 秒倒计时 → `RunNow` → 倒计时期间点「取消」→ 断言实例**没有**被 taskkill、
更新**没有**开始、已停的实例被拉回、执行日志写「批量停服被取消，本次更新已放弃」。
② 恢复启动跑到一半点「取消」→ 任务判失败、未启动的实例仍留在待恢复现场、提示框重新弹出。
③ 三种自动批量各触发一次，确认弹窗标题与提示条显示的来源正确。

---

## 11. 运行中的定时任务可随时取消，并回滚实例状态

> 新增能力：任务跑到一半（倒计时中、停服中、更新中、恢复启动中）时，用户可以一键取消，
> 管理器负责把实例恢复到**任务开始之前**的运行状态。

### 11.1 现状：没有「取消一个任务」这回事

今天能取消的只有任务的**某个子步骤**，而且入口分散：

| 想取消的东西 | 现有入口 | 效果 |
|---|---|---|
| 批量停服/重启 | 批量弹窗的「取消」 | 只停这一轮批量；任务本身照跑（§10.2 的 bug） |
| 单个实例的倒计时 | `POST /api/server/:name/countdown/cancel` | 只放过那一台 |
| 服务端更新 | `POST /api/server/update/cancel` | 只停更新；任务不知情 |
| 整个任务 | **没有** | 只能停整个调度器（`Scheduler.Stop()`），且不回滚任何东西 |

用户想表达的是「这次别跑了，把服务器还给我」，现在得分别去三个地方点，还点不全。

### 11.2 判据：恢复到什么状态

在 `execute()` 的最开头、任何动作之前拍一张快照：

```go
// snapshot 是任务开始前「正在跑」的实例名。
//
// 判据用 procpkg.ListAliveInstances()（端口 + PID 双重判断）而不是读 BadgerDB 状态：
// 状态记录会因为崩溃而滞留在 started，照着它恢复会去启动一台本来就死着的实例。
snapshot := procpkg.ListAliveInstances()
```

取消时：

```
应该在跑的 = snapshot
现在在跑的 = ListAliveInstances()
需要拉起的 = snapshot - 现在在跑的
```

**只启动，不停止。** `现在在跑的 - snapshot` 那一部分（任务开始后才跑起来的实例）一律不碰——
它可能是用户在任务执行期间自己手动启动的，回滚时把它停掉是**用户绝不会预期的破坏性动作**。
恢复的语义是「把我弄停的还回来」，不是「把世界恢复成一模一样」。

### 11.3 快照取代 `stoppedByTask`

第一阶段用的是行为日志（记下「我停了谁」），快照是它的严格泛化：

| 场景 | `stoppedByTask` | 快照 | 是否一致 |
|---|---|---|---|
| 任务停掉的实例 | 记录 | 执行前在跑 → 需拉起 | ✅ 同一批 |
| 任务开始前就停着的 | 不记 | 不在快照里 | ✅ 都不碰 |
| 任务想停但没停掉的 | 不记 | 在快照里，且现在还在跑 → 差集为空 | ✅ 都不碰 |
| **定时重启任务停到一半** | 覆盖不到（第一阶段只给更新任务用） | 在快照里，现在停着 → 需拉起 | 快照胜 |

所以 **`stoppedByTask` 退休，快照成为「该恢复谁」的唯一真相**。
这会简化 §4.2（不用再一路攒名单）、§9.5（落盘的名单直接来自快照差集），
代价是要改一遍第一阶段已经落地的代码——值得，两套并存才是真正的隐患。

`appendUnique` / `excludeNames` 保留，快照差集正好用得上。

### 11.4 取消的传导：ctx 关不掉更新

`Scheduler` 新增运行中任务的登记表：

```go
// taskRun 一次正在执行的任务。按 runID 而不是 taskID 登记：
// RunNow 是 `go s.execute(...)`，同一个任务完全可能有两次执行同时在飞（§3.5 第 5 条），
// 用 taskID 做键会让后一次把前一次挤掉，取消时也分不清取消的是哪一次。
type taskRun struct {
	RunID     string    `json:"run_id"`
	TaskID    string    `json:"task_id"`
	TaskName  string    `json:"task_name"`
	TaskType  TaskType  `json:"task_type"`
	Trigger   TriggerSource `json:"trigger"`
	StartedAt time.Time `json:"started_at"`

	// Phase 面向用户：countdown / stopping / updating / restoring / restarting
	Phase atomic.Value `json:"-"`

	snapshot  []string
	cancel    context.CancelFunc
	cancelled atomic.Bool
}
```

`execute()` 开头 `ctx, cancel := context.WithCancel(ctx)` + 登记，`defer` 注销。

**光 cancel ctx 是不够的**，三个子操作各有各的取消方式：

| 子操作 | ctx 取消后会怎样 | 要补什么 |
|---|---|---|
| 批量操作（含倒计时） | `awaitBatch` 已经会 `op.Cancel()` 并等 `Done()` | ✅ 不用补 |
| 服务端更新 | `runUpdate` 从 `<-ctx.Done()` 返回，但 **`updatemanage` 的 ctx 派生自 `Background`，更新会继续跑完** | ❌ 必须显式调 `updatemanage.GetGlobalManager().Cancel()` |
| 恢复启动 | 同批量 | ✅ |

所以 `CancelRun` 是这样：

```go
func (s *Scheduler) CancelRun(runID string) error {
	run, ok := s.runs.Get(runID)
	if !ok {
		return ErrRunNotFound
	}
	// 重复点取消只生效一次：第二次进来时回滚可能已经跑了一半，
	// 再取消一次会把回滚自己的 ctx 也掐掉
	if !run.cancelled.CompareAndSwap(false, true) {
		return ErrRunAlreadyCancelling
	}

	run.cancel()

	// 更新的 ctx 派生自 Background，任务 ctx 关不掉它——不显式取消的话，
	// 用户看到任务已取消，SteamCMD 还在后台默默下载，回滚拉起的实例
	// 跑的还是正在被覆写的服务端文件
	if run.TaskType == TaskUpdate {
		updatemanage.GetGlobalManager().Cancel()
	}
	return nil
}
```

### 11.5 回滚由任务自己的 goroutine 做

不在 `CancelRun` 里同步回滚——那会让 HTTP 请求挂几分钟。回滚放在 `execute` 的收尾：

```go
// execute 尾部，拿到 summary/err 之后、写 RunRecord 之前
if run.cancelled.Load() {
	// 关键：用新的 ctx，不能用 run 的那个——它已经被取消了，
	// restoreInstances 入口的 ctx.Err() 检查会当场把回滚挡回去
	out, rErr := restoreInstances(context.Background(), missingFrom(run.snapshot), BatchOrigin{
		Kind:  OriginTaskCancelRestore,
		Label: fmt.Sprintf("取消定时任务「%s」· 状态回滚", t.Name),
	})
	summary, err = cancelSummary(out, rErr)
}
```

- `missingFrom(snapshot)` = `excludeNames(snapshot, procpkg.ListAliveInstances())`，即 §11.2 的差集。
- 回滚失败的实例进 `pending_restore.json`（§9），Reason 写「定时任务被取消后未能恢复启动」，
  提示框会弹出来让用户再决定一次。**不要在这里重试**——重试是提示框那条路径的事。
- `RunRecord`：`Success=false`，Message 形如
  `已取消（已恢复 2 个实例）` / `已取消，但 1 个实例恢复失败：meijue`。
  取消是用户主动行为，不算故障，但也不能记成成功——执行日志里必须能一眼看出这次没跑完。

### 11.6 §10.2 与本节的关系

两条取消入口最终汇到同一段回滚代码：

| 入口 | 用户的意图 | 处理 |
|---|---|---|
| 批量弹窗点「取消」 | 「别动这批服务器」 | `op.WasCancelled()` 检出（§12.2）→ 视同取消本次任务 → 走 §11.5 的回滚 |
| 定时任务页点「取消任务」 | 「这次任务整个别跑了」 | `CancelRun` → 走 §11.5 的回滚 |

§10.2 原先写的是「恢复 `stoppedByTask`」，现在统一改成「按快照回滚」，两处不再各写一套。

### 11.7 各阶段被取消时的具体后果

| 取消发生在 | 立即效果 | 回滚动作 |
|---|---|---|
| 倒计时中 | 倒计时中止，公告停发，一台都还没停 | 差集为空，无事可做 |
| 批量停服中 | 当前实例停完就收手，剩下的标 `cancelled` | 已停的拉回来 |
| 强停兜底 / `waitInstancesStopped` 中 | 轮询循环 `select ctx.Done()` 退出 | 已停的拉回来。**已经 `taskkill` 出去的请求收不回来**，那些实例会继续退出，随后被回滚拉起 |
| 更新中 | `updatemanage.Cancel()` → SteamCMD 进程被掐 | 全部拉回来。⚠️ 见 §11.8 |
| 恢复启动中 | 剩下的实例标 `cancelled` | 差集里还缺的继续拉——**这是唯一一个「取消之后还要继续启动」的分支**，因为回滚的方向和被取消的操作方向一致。实现上直接跳过回滚，把未启动的交给 `pending_restore.json` 更诚实 |

最后一行值得单独说：在恢复启动阶段点取消，用户的意思是「别再启动了」，
此时再跑一轮回滚（也是启动）等于无视他。所以**取消发生在 `restoring` 阶段时不执行回滚**，
直接把没起来的写进待恢复现场。`taskRun.Phase` 就是为这个判断存在的。

### 11.8 取消更新的风险：服务端文件停在中间态

`updatemanage.Cancel()` 掐掉的是 SteamCMD 进程，`server-files/` 可能停在下载/解压到一半的状态。
回滚随后把实例拉起来，跑的就是这份不完整的文件。

处理方式：**照做，但说清楚**。

- SteamCMD 本身支持断点续传，下一次更新（或 `asa-server update`）会把它补齐并 `VerifyServerInstallation`。
- 取消更新阶段的任务时，`RunRecord.Message` 追加一句
  `⚠️ 更新被中断，服务端文件可能不完整，建议重新执行一次更新`。
- 前端的取消二次确认框在 `phase === 'updating'` 时把这句话显示出来，让用户带着这个认知点确认。

不做「取消后自动回滚文件」——那需要更新前完整备份 `server-files/`（几十 GB），代价完全不成比例。

### 11.9 API 与前端

```go
s.GET("/runs", h.listRunning)                 // 正在跑的任务（含 phase / started_at / run_id）
s.POST("/runs/:run_id/cancel", h.cancelRun)   // 取消
```

| 路由 | 返回 |
|---|---|
| `GET /api/schedule/runs` | `{"runs":[{run_id,task_id,task_name,task_type,trigger,phase,started_at}]}`，空数组而非 null |
| `POST /api/schedule/runs/:run_id/cancel` | 成功 `{"success":true,"message":"已取消，正在回滚实例状态"}`；`ErrRunNotFound` → 404；`ErrRunAlreadyCancelling` → 409 |

前端 `ScheduleManager.vue`：

- 表格「操作」列：任务正在跑时，「立即执行」换成 **「取消」**（`theme="danger"`），
  旁边显示当前阶段的中文标签（倒计时中 / 停服中 / 更新中 / 恢复启动中）。
- 运行中状态的来源：进页面拉一次 `GET /runs`，之后靠 WS 的 `schedule_run`（任务结束）
  和新增的 `schedule_run_started` 事件维持；再加一个 15s 的兜底轮询，
  因为任务可能在页面没开着的时候就开始了。
- 取消要二次确认，文案说清会做什么：
  「取消后将停止本次任务，并把执行前正在运行的实例重新启动。」
  `phase === 'updating'` 时追加 §11.8 的那句警告。
- 回滚进度不另外做 UI：它就是一轮 `BatchStart`，批量弹窗里带着
  `取消定时任务「x」· 状态回滚` 的来源标签（§10.4）自然可见。

### 11.10 边界情况

| 情况 | 行为 |
|---|---|
| 任务刚好在用户点取消的瞬间自己跑完了 | `CancelRun` 找不到 runID → 404「该任务已执行完毕」 |
| 取消后回滚期间，用户又点了别的批量操作 | 回滚的 `StartOperation` 拿到 `ErrOperationInProgress` → 全部进待恢复现场，提示框弹出 |
| 快照拍摄时 `ListAliveInstances()` 返回 nil（读实例列表失败） | 快照为空 → 取消时无可回滚。记一条 WARN；这与「一个实例都没在跑」不可区分，但两者的正确动作都是「什么都不做」 |
| 调度器整体 `Stop()`（服务停止） | 走的是各任务 ctx 被取消的既有路径，`cancelled` 标志**没有**被置位 → **不触发回滚**。进程都要退出了，这时候拉起实例只会留下一堆中间状态；未恢复的实例由 §9.5 触点 ⑤ 写进待恢复现场，下次启动后提示 |
| 同一任务两次执行同时在飞 | 按 runID 各自独立取消，互不影响 |
| 取消一个**重启**任务 | 快照回滚同样适用：重启到一半被取消，停着的那几台会被拉起来 |

### 11.11 实施顺序（接在 §10.6 之后）

1. `schedule`：`taskRun` + `runs` 登记表（`sync.Map` 或 `map` + `mu`）+ `newRunID()`。
2. `execute()`：拍快照、派生可取消 ctx、登记/注销、按阶段更新 `Phase`。
3. `CancelRun` + `updatemanage.Cancel()` 传导 + 幂等 CAS。
4. §11.5 的收尾回滚（含 `restoring` 阶段不回滚的分支）+ RunRecord 文案。
5. 用快照替换 `stoppedByTask`（§11.3），同步改 §4.2 / §9.5 / §10.2 三处的名单来源。
6. `BatchOrigin` 新增 `OriginTaskCancelRestore`。
7. `realtime`：新增 `schedule_run_started` 广播（任务开始时发，带 run_id / phase）。
8. `scheduleapi`：两个新路由。
9. 前端：`api.js` 两个封装 → `ScheduleManager` 的「取消」按钮与阶段标签 → 二次确认文案。

### 11.12 测试

| 层 | 用例 |
|---|---|
| 纯函数 | `missingFrom(snapshot, alive)` 的差集：全在跑 → 空；全停 → 全量；部分重叠；快照为空 |
| `runs` 登记表 | 登记/注销/按 runID 取消；同一 taskID 两个 run 互不干扰；重复 cancel 第二次返回 `ErrRunAlreadyCancelling` |
| 手工端到端 | ① 60 秒倒计时的更新任务 → 倒计时中取消 → 实例一台没停、无回滚、日志记「已取消」；② 停服完成、更新进行中取消 → SteamCMD 进程消失、实例全部被拉回、日志带 ⚠️ 文件不完整提示；③ 恢复启动中取消 → 不再继续启动、未启动的进待恢复提示框；④ 取消一个重启任务；⑤ 回滚期间抢跑一个手动批量 → 全部进待恢复现场 |

---

## 12. 任务的状态流转：子操作在批量面板里被取消时

> 本节回答：用户在**批量操作面板**（而不是定时任务页）点了取消，
> 把这次定时任务的「停止所有服务器」或「恢复实例」那一轮批量掐掉之后，
> 这个任务的状态是怎么走完的。

### 12.1 三个取消入口，必须归一到同一个标记

| 入口 | 用户在哪点的 | 它直接掐掉的东西 |
|---|---|---|
| A | 定时任务页「取消任务」（§11） | 任务的 ctx（+ `updatemanage.Cancel()`） |
| B | 批量操作面板「取消」 | 当前那一轮 `BatchOperation` |
| C | 实例卡片上的「取消倒计时」 | **单台**实例的倒计时 |

A 会置位 `taskRun.cancelled`，从而触发 §11.5 的快照回滚。
**B 不会**——它掐的是 `op.ctx`，任务的 ctx 毫发无损，`taskRun.cancelled` 还是 false。
§11.6 说两条入口「汇到同一段回滚代码」，但没说清怎么汇。这里定死：

```go
// runUpdate / runRestart 里，每次 awaitBatch 返回之后立刻判一次。
// 把「批量被取消」翻译成「任务被取消」，是让 B 和 A 走同一条收尾路径的唯一接缝——
// 少了这一步，B 只会让任务默默失败，快照回滚一次都不会跑。
if op.WasCancelled() {
	run.markCancelled("批量操作在操作面板中被取消")
	return "", errRunCancelled
}
```

`markCancelled(reason)` 内部就是 `cancelled.CompareAndSwap(false, true)` + 记录原因，
与 `CancelRun`（入口 A）调的是同一个方法。收尾时 `execute` 只看 `run.cancelled`，
不关心是谁置的位——回滚、RunRecord 文案、待恢复现场落盘因此天然一致。

C 不影响任务：它只放过一台实例，其余照常执行，语义上等同于「批量里跳过一个」。

### 12.2 修正：§10.2 的 `wasCancelled` 判据是错的

§10.2 提议「`InstanceResults` 里出现 `InstanceCancelled` 就算整批被取消」。
查了一遍写入点，这个判据会误报：

| `manager.go` 位置 | 写入场景 | 是否整批取消 |
|---|---|---|
| `:627` `markRemainingFrom(i, InstanceCancelled, "cancelled")` | 主循环发现 `op.ctx` 被取消 | ✅ 是 |
| `:813` `markRemainingCancelled()` | 倒计时阶段整体中止 | ✅ 是 |
| **`:755` `setResult(name, InstanceCancelled, "倒计时被取消")`** | **用户取消了单台实例的倒计时（入口 C）** | ❌ **不是**，其余实例照常执行 |

按原判据，用户取消**一台**服务器的倒计时，会被误判成「整批被取消」，
进而中止整个定时更新并触发回滚——把一次「放过这一台」放大成「整个任务作废」。

靠 `Error` 字段的文案（`"cancelled"` vs `"倒计时被取消"`）区分太脆，改一行文案就坏。
正解是让 `batchmanage` 显式记账：

```go
// BatchOperation 新增字段
// cancelledAll 记录这一轮批量是否被**整体**取消（op.Cancel() / CancelCurrent）。
//
// 不能靠扫 InstanceResults 里有没有 InstanceCancelled 来推断：单台实例的倒计时
// 被取消时也会写这个状态（见 runCountdownPhase），那属于「放过这一台」，
// 整批仍在正常执行，两者必须分开。
cancelledAll atomic.Bool

// WasCancelled 报告这一轮批量是否被整体取消。
// 只在 op 结束后（Done() 已关闭）读才有意义。
func (op *BatchOperation) WasCancelled() bool { return op.cancelledAll.Load() }
```

置位点正好两处，与上表的 ✅ 一一对应：主循环 `ctx.Done()` 分支、`runCountdownPhase` 返回 true 的分支。

> 顺带说明为什么不用 `op.Status == "cancelled"`：`runBatchOperation` 的收尾 defer 会把 Status
> 无条件改成 `"completed"`，`"cancelled"` 只在那一瞬间可见（§10.2 已记录）。
> `cancelledAll` 是一次性置位、永不回退的标志，专为「事后判断」而设。

### 12.3 状态机：取消发生在哪一步，任务怎么收场

以**定时更新**任务为例（重启任务是它的子集）。纵轴是取消发生的阶段，横轴是任务的收场：

| 取消发生在 | 任务结局 | 回滚动作 | 更新做了吗 | 待恢复现场 |
|---|---|---|---|---|
| 停服批量的**倒计时**阶段 | `cancelled` | 无（一台都没停） | 否 | 不写 |
| 停服批量的**执行**阶段 | `cancelled` | 拉回已停的 | 否 | 回滚失败的才写 |
| 强停兜底 / 等待退出 | `cancelled` | 拉回已停的 | 否 | 同上 |
| **更新**阶段 | `cancelled` | 拉回全部 | 中断（§11.8 警告） | 同上 |
| **恢复启动**批量 | `cancelled` | **不回滚**（§11.7） | 是，已完成 | 未起来的全写进去 |

三条贯穿全表的规则：

1. **一旦 `run.cancelled` 置位，任务结局就是 `cancelled`，不再是 `failed`。**
   取消是用户主动行为，不是故障；混在一起会让执行日志里的失败率失去意义。
2. **`cancelled` 也不是 `success`。** 界面上不能出现「绿色成功」而服务器还停着三台。
3. **`NextRunAt` 照常推进**（`tick` 在 `execute` 返回后按 `time.Now()` 重算）。
   取消的是**这一次**执行，不是这个任务——不会重跑，也不会提前跑。
   想让任务别再自动跑，用的是「停用」而不是「取消」，两者语义不重叠。

### 12.4 `RunRecord` 要能表达「已取消」

现在只有 `Success bool`，两分法装不下三种结局。新增一个字段：

```go
// RunOutcome 一次执行的结局。
//
// 取消必须与失败分开：前者是用户主动叫停，后者是任务没干成。
// 混成一个 Success=false 会让「最近 7 天失败 5 次」这种统计彻底失真。
type RunOutcome string

const (
	OutcomeSuccess   RunOutcome = "success"
	OutcomeFailed    RunOutcome = "failed"
	OutcomeCancelled RunOutcome = "cancelled"
)

type RunRecord struct {
	...
	Success bool       `json:"success"`            // 保留：存量记录与旧前端还在读
	Outcome RunOutcome `json:"outcome,omitempty"`  // 新增
}
```

**向后兼容**：`schedule_logs.json` 里的存量记录没有 `outcome`，反序列化后是空串。
读取侧统一走一个派生函数，不要在各处 `if r.Outcome == ""` 散判：

```go
// outcomeOf 兼容没有 outcome 字段的存量记录：它们只有 Success，
// 而当时还不存在「已取消」这种结局，二分法推导是准确的。
func outcomeOf(r *RunRecord) RunOutcome {
	if r.Outcome != "" {
		return r.Outcome
	}
	if r.Success {
		return OutcomeSuccess
	}
	return OutcomeFailed
}
```

写入侧 `Success` 与 `Outcome` 同时写（`Success = (Outcome == OutcomeSuccess)`），
保证两个字段永远不打架。

Message 文案：

| 结局 | Message 示例 |
|---|---|
| 停服阶段被取消 | `已取消：批量操作在操作面板中被取消（已恢复 2 个实例）` |
| 更新阶段被取消 | `已取消：更新已中断（已恢复 3 个实例）⚠️ 服务端文件可能不完整，建议重新执行一次更新` |
| 恢复启动被取消 | `更新已完成，但恢复启动被取消，2 个实例仍处于停止状态（已记入待恢复，可在提示中恢复）` |

最后一行刻意不说「已取消」开头：更新那件事**确实做完了**，
说成「已取消」会让用户以为更新也没生效，下次又手动更新一遍。

### 12.5 前端

- `ScheduleRunLog.vue` 的结果标签从两态改三态：
  成功（`theme="success"`）/ 失败（`danger`）/ **已取消**（`warning`），按 `outcome` 取值，
  空值走 §12.4 的派生规则。
- `schedule_run` 的 WS 广播 `data` 里补上 `outcome`（`BroadcastScheduleRun` 现在只传 `success`）。
- 批量面板点取消之后，**定时任务页的「取消」按钮会自己消失**——任务收尾时
  `schedule_run` 事件到达，运行中列表被刷新。不需要为入口 B 单独做 UI 联动。

### 12.6 边界

| 情况 | 行为 |
|---|---|
| 批量面板点的是**单实例 skip** 而非整批取消 | `InstanceSkipped` + `"skipped by user"`，`cancelledAll` 不置位，任务照常跑完。跳过的那台不在「本次停掉」的集合里，也就不会被回滚拉起 |
| 整批取消恰好与批量自然结束同时发生 | `cancelledAll` 只在两个取消分支置位；批量正常跑完时不置位，任务照常收场 |
| 用户先在批量面板取消，紧接着又在任务页点「取消任务」 | `markCancelled` 的 CAS 保证只生效一次，第二次返回 `ErrRunAlreadyCancelling`（409） |
| 停服批量被取消，但此时**所有实例其实都已经停完了** | 回滚差集 = 快照 - 现在在跑的 = 全部，照样拉回来。用户的意图是「别停」，那就还他一个都在跑的状态 |
| 恢复启动批量被取消后，用户又点了待恢复提示里的「恢复启动」 | 正常路径，`ConfirmPendingRestore` 重新拉一轮（§9.6） |

### 12.7 实施补充（并入 §10.6 / §11.11）

1. `batchmanage`：`cancelledAll atomic.Bool` + `WasCancelled()` + 两处置位（替换 §10.2 的 `wasCancelled` 辅助函数，那个判据作废）。
2. `schedule`：`taskRun.markCancelled(reason)`，`CancelRun` 与 `awaitBatch` 后的检查共用它。
3. `schedule`：`RunOutcome` + `RunRecord.Outcome` + `outcomeOf()` + 三处 Message 文案。
4. `realtime`：`BroadcastScheduleRun` 的 `data` 补 `outcome`。
5. 前端：`ScheduleRunLog` 三态标签。
6. 测试：`batchmanage` 补两条——「单实例倒计时取消后 `WasCancelled()` 仍为 false」
   和「`CancelCurrent()` 后为 true」。第一条正是 §12.2 那个误报的回归钉。
