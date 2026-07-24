package countdown

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// shortConfig 返回一个绕过 MinTotal 下界的配置，仅供直接驱动 runOne 的测试使用。
//
// Wait / Stop / Restart 会 Validate（下界 30s），跑满一轮太慢；
// 而「单实例取消互不影响」这条要验的是 runOne 的子 context 隔离，
// 用短时长直接测这一层，既准确又快。
func shortConfig(total time.Duration) (Config, time.Time) {
	cfg := (&Config{
		Total: total,
		// 显式给一个点位，避免走默认推导时在测试期间真的去连 RCON
		Points: []time.Duration{total},
	}).normalized()
	return cfg, time.Now().Add(total)
}

func TestConfigDisabled(t *testing.T) {
	var nilCfg *Config
	if nilCfg.Enabled() {
		t.Error("nil 配置的 Enabled() 应为 false")
	}
	if (&Config{}).Enabled() {
		t.Error("Total=0 时 Enabled() 应为 false")
	}
}

// 未启用倒计时时 Wait 必须立即返回，否则「不倒计时」会变成「卡住」
func TestWaitDisabledIsNoop(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, err := Wait(context.Background(), []string{"a", "b"}, ActionStop, &Config{})
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("未启用倒计时时 Wait 不应报错，实际: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未启用倒计时时 Wait 应立即返回")
	}
}

// 非法配置要在 Wait 入口就被挡下，不能等倒计时跑起来
func TestWaitRejectsInvalidConfig(t *testing.T) {
	cfg := &Config{Total: time.Minute, Points: []time.Duration{2 * time.Minute}}
	if _, err := Wait(context.Background(), []string{"a"}, ActionStop, cfg); err == nil {
		t.Error("点位超过总时长时 Wait 应返回错误")
	}
}

// 登记表不允许同一实例并存两轮倒计时。
// 拆分前这里是静默覆盖写，后来者会顶掉前者的 cancel，前一轮就再也取消不掉了。
func TestRegisterRejectsDuplicate(t *testing.T) {
	const name = "___countdown_dup_test"
	defer release(name)

	if err := register(name, ActionStop, time.Now().Add(time.Minute), func() {}); err != nil {
		t.Fatalf("首次登记不应失败: %v", err)
	}
	if err := register(name, ActionRestart, time.Now().Add(time.Minute), func() {}); !errors.Is(err, ErrInProgress) {
		t.Errorf("重复登记应返回 ErrInProgress，实际: %v", err)
	}

	release(name)

	if err := register(name, ActionStop, time.Now().Add(time.Minute), func() {}); err != nil {
		t.Errorf("释放后应可重新登记，实际: %v", err)
	}
}

// Cancel 走登记表，和父 ctx 取消是两条路，各测一次。
// 若这条失效，「取消关服」会变成「延迟关服」。
func TestCancelViaRegistry(t *testing.T) {
	const name = "___countdown_cancel_test"
	cfg, deadline := shortConfig(10 * time.Second)

	done := make(chan error, 1)
	go func() { done <- runOne(context.Background(), name, ActionRestart, &cfg, deadline) }()

	waitForRegistration(t, name)

	status, ok := Get(name)
	if !ok {
		t.Fatal("倒计时进行中，Get 应能查到")
	}
	if status.Action != ActionRestart {
		t.Errorf("action = %q, want %q", status.Action, ActionRestart)
	}
	if status.Remaining <= 0 {
		t.Errorf("倒计时进行中，剩余秒数应大于 0，实际 %d", status.Remaining)
	}

	if !Cancel(name) {
		t.Fatal("Cancel 应返回 true")
	}

	select {
	case err := <-done:
		if !errors.Is(err, ErrCancelled) {
			t.Errorf("被取消的倒计时应返回 ErrCancelled，实际: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runOne 未在取消后及时返回")
	}

	// 取消后登记表应被清理
	if _, ok := Get(name); ok {
		t.Error("倒计时取消后登记表未清理")
	}
	if Cancel(name) {
		t.Error("已结束的倒计时不应还能被取消")
	}
}

// 走完倒计时后登记**不**释放：接下来的停止本身还要花时间，
// 前端靠 executing 阶段的登记显示「服务器关闭中…」，提前清掉会让 UI 丢状态。
func TestRunOneKeepsRegistrationForExecutingPhase(t *testing.T) {
	const name = "___countdown_executing_test"
	defer release(name)

	cfg, deadline := shortConfig(1200 * time.Millisecond)

	if err := runOne(context.Background(), name, ActionStop, &cfg, deadline); err != nil {
		t.Fatalf("倒计时应正常走完: %v", err)
	}

	status, ok := Get(name)
	if !ok {
		t.Fatal("走完后登记应保留，供调用方在动作结束时释放")
	}
	if status.Phase != "executing" {
		t.Errorf("phase = %q, want executing", status.Phase)
	}
	if status.Remaining != 0 {
		t.Errorf("executing 阶段的 remaining 应为 0，实际 %d", status.Remaining)
	}
}

// 本次重构的核心行为变更：批量倒计时中取消一个实例只影响它自己，
// 其余实例照常走完。拆分前这里是「取消任一 = 取消整批」。
func TestCancelOneInstanceDoesNotAffectOthers(t *testing.T) {
	const (
		victim    = "___countdown_iso_victim"
		survivor1 = "___countdown_iso_survivor1"
		survivor2 = "___countdown_iso_survivor2"
	)
	names := []string{victim, survivor1, survivor2}
	for _, n := range names {
		defer release(n)
	}

	cfg, deadline := shortConfig(2 * time.Second)

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs = make(map[string]error)
	)
	for _, name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			err := runOne(context.Background(), name, ActionStop, &cfg, deadline)
			mu.Lock()
			errs[name] = err
			mu.Unlock()
		}(name)
	}

	waitForRegistration(t, victim)
	if !Cancel(victim) {
		t.Fatal("Cancel 应返回 true")
	}

	wg.Wait()

	if !errors.Is(errs[victim], ErrCancelled) {
		t.Errorf("被取消的实例应返回 ErrCancelled，实际: %v", errs[victim])
	}
	for _, n := range []string{survivor1, survivor2} {
		if errs[n] != nil {
			t.Errorf("实例 %s 未被取消，应正常走完，实际: %v", n, errs[n])
		}
	}
}

// 撞上另一轮倒计时的实例，其登记归**那一轮**所有：
// Wait 必须把它当作已取消跳过，且绝不能顺手把别人的登记清掉——
// 否则那一轮就再也取消不了了。
func TestWaitDoesNotReleaseForeignRegistration(t *testing.T) {
	const (
		occupied = "___countdown_foreign_occupied"
		free     = "___countdown_foreign_free"
	)
	defer release(occupied)
	defer release(free)

	// 模拟另一轮倒计时先占住了 occupied
	foreignCancelled := false
	if err := register(occupied, ActionRestart, time.Now().Add(time.Hour),
		func() { foreignCancelled = true }); err != nil {
		t.Fatalf("预置登记失败: %v", err)
	}

	cfg := &Config{Total: MinTotal, Points: []time.Duration{time.Second}}

	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := Wait(context.Background(), []string{occupied, free}, ActionStop, cfg)
		done <- outcome{res, err}
	}()

	waitForRegistration(t, free)
	if !Cancel(free) {
		t.Fatal("取消 free 失败")
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("不应整批报错: %v", got.err)
		}
		if !errors.Is(got.res.Reason(occupied), ErrInProgress) {
			t.Errorf("被占用的实例原因应为 ErrInProgress，实际: %v", got.res.Reason(occupied))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Wait 未及时返回")
	}

	// 关键断言：别人的登记还在，且仍然可取消
	status, ok := Get(occupied)
	if !ok {
		t.Fatal("另一轮倒计时的登记被误删了")
	}
	if status.Action != ActionRestart {
		t.Errorf("登记被顶替：action = %q, want %q", status.Action, ActionRestart)
	}
	if !Cancel(occupied) || !foreignCancelled {
		t.Error("另一轮倒计时的 cancel 函数丢失，已无法取消")
	}
}

// 父 ctx 取消 = 整批中止：Wait 返回错误，且不留下任何登记残余。
func TestWaitParentCtxCancelReturnsError(t *testing.T) {
	names := []string{"___countdown_parent_1", "___countdown_parent_2"}
	for _, n := range names {
		defer release(n)
	}

	cfg := &Config{Total: MinTotal, Points: []time.Duration{time.Second}}
	ctx, cancel := context.WithCancel(context.Background())

	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := Wait(ctx, names, ActionStop, cfg)
		done <- outcome{res, err}
	}()

	waitForRegistration(t, names[0])
	cancel()

	select {
	case got := <-done:
		if got.err == nil {
			t.Error("父 ctx 取消时 Wait 应返回错误（整批中止）")
		}
		if len(got.res.Cancelled) != 0 {
			t.Errorf("整批中止时逐实例结果无意义，应为空，实际: %v", got.res.Cancelled)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Wait 未在父 ctx 取消后及时返回")
	}

	for _, n := range names {
		if _, ok := Get(n); ok {
			t.Errorf("Wait 返回后实例 %s 的登记未释放", n)
		}
	}
}

// 全部实例被逐个取消：不是错误，而是 AllCancelled 为真，
// 调用方据此按「整批未执行」收尾。
func TestWaitAllCancelled(t *testing.T) {
	names := []string{"___countdown_all_1", "___countdown_all_2"}
	for _, n := range names {
		defer release(n)
	}

	cfg := &Config{Total: MinTotal, Points: []time.Duration{time.Second}}

	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := Wait(context.Background(), names, ActionStop, cfg)
		done <- outcome{res, err}
	}()

	for _, n := range names {
		waitForRegistration(t, n)
		if !Cancel(n) {
			t.Fatalf("取消实例 %s 失败", n)
		}
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Errorf("逐个取消不是整批中止，err 应为 nil，实际: %v", got.err)
		}
		if !got.res.AllCancelled(len(names)) {
			t.Errorf("AllCancelled 应为真，Cancelled = %v", got.res.Cancelled)
		}
		for _, n := range names {
			if !got.res.IsCancelled(n) {
				t.Errorf("实例 %s 应在 Cancelled 中", n)
			}
			if !errors.Is(got.res.Reason(n), ErrCancelled) {
				t.Errorf("实例 %s 的原因应为 ErrCancelled，实际: %v", n, got.res.Reason(n))
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Wait 未在全部取消后及时返回")
	}
}

// Wait 层面的端到端验证：一个实例被取消，其余跑满整轮后正常返回。
// 因为 Wait 会 Validate（下界 MinTotal），这条必然要跑满 30s，默认跳过。
func TestWaitCancelOneInstanceContinuesOthers(t *testing.T) {
	if testing.Short() {
		t.Skip("需要跑满 MinTotal(30s) 的真实倒计时")
	}

	names := []string{"___countdown_e2e_victim", "___countdown_e2e_survivor"}
	for _, n := range names {
		defer release(n)
	}

	cfg := &Config{Total: MinTotal, Points: []time.Duration{time.Second}}

	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := Wait(context.Background(), names, ActionStop, cfg)
		done <- outcome{res, err}
	}()

	waitForRegistration(t, names[0])
	if !Cancel(names[0]) {
		t.Fatal("取消受害实例失败")
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("单实例取消不应让 Wait 报错: %v", got.err)
		}
		if len(got.res.Cancelled) != 1 || got.res.Cancelled[0] != names[0] {
			t.Errorf("Cancelled 应恰好是被取消的那一个，实际: %v", got.res.Cancelled)
		}
		if got.res.IsCancelled(names[1]) {
			t.Error("未被取消的实例不应出现在 Cancelled 中")
		}
	case <-time.After(MinTotal + 20*time.Second):
		t.Fatal("Wait 未在倒计时结束后及时返回")
	}
}

// waitForRegistration 轮询等待实例进入登记表，避免用固定 sleep 制造 flaky。
func waitForRegistration(t *testing.T, instanceName string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := Get(instanceName); ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("实例 %s 未在预期时间内进入登记表", instanceName)
}
