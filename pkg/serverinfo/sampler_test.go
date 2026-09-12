package serverinfo

import (
	"os"
	"testing"
	"time"
)

// newTestSampler 直接造一个采样器，不碰 globalSampler——包级单例被 StartSampler
// 的 sync.Once 锁死，一个测试进程里只能启动一次。
func newTestSampler(src TargetSource) *Sampler {
	return &Sampler{
		interval:  SampleInterval,
		targetSrc: src,
		targets:   make(map[string]trackedTarget),
		procs:     make(map[int32]*procState),
		hist:      newHistory(nil, SampleInterval),
		done:      make(chan struct{}),
	}
}

// TestSamplerPullsTargetsWithoutClients 是本功能的核心回归：没有任何人调过
// 「登记目标」，仅靠注入的目标源，实例数据就要进快照与历史。
// 这条挂掉就意味着「没人开面板时实例级历史是空的」那个 bug 回来了。
func TestSamplerPullsTargetsWithoutClients(t *testing.T) {
	self := int32(os.Getpid())
	s := newTestSampler(func() []Target {
		return []Target{{Name: "inst-a", PID: self}}
	})

	// 两轮：第一轮建 CPU 基线，第二轮才有真实速率
	s.sample()
	s.sample()

	snap := s.latest.Load()
	if snap == nil {
		t.Fatal("采样器没有发布任何快照")
	}
	if _, ok := snap.ByName["inst-a"]; !ok {
		t.Fatalf("目标源给了 inst-a，快照里却没有；ByName=%v", snap.ByName)
	}

	series := s.hist.query(0, "inst-a")
	if len(series.Timestamps) != 2 {
		t.Fatalf("历史时间轴应有 2 个点，实际 %d", len(series.Timestamps))
	}
	if series.Instance == nil {
		t.Fatal("历史里没有 inst-a 的实例列")
	}
	if mem := series.Instance["memory_used"]; len(mem) != 2 || mem[1] == nil {
		t.Fatalf("实例内存序列应有值，实际 %v", mem)
	}
}

// TestSamplerNilTargetSourceStillSamplesHost 未注入目标源时不能 panic，
// 整机指标照常——组合根在某些路径上可能不接目标源。
func TestSamplerNilTargetSourceStillSamplesHost(t *testing.T) {
	s := newTestSampler(nil)
	s.sample()

	snap := s.latest.Load()
	if snap == nil {
		t.Fatal("采样器没有发布任何快照")
	}
	if len(snap.ByName) != 0 {
		t.Fatalf("没有目标源时不该有实例数据，实际 %v", snap.ByName)
	}
	if len(s.hist.query(0, "").Timestamps) != 1 {
		t.Fatal("host 历史没有落点")
	}
}

// TestSamplerTargetSurvivesMissedRound 目标源某一轮漏报（读 PID 文件失败之类）时，
// TTL 之内不能丢掉跟踪状态：丢了就要重建 CPU 基线，曲线上会多一个 0。
func TestSamplerTargetSurvivesMissedRound(t *testing.T) {
	self := int32(os.Getpid())
	report := true
	s := newTestSampler(func() []Target {
		if !report {
			return nil
		}
		return []Target{{Name: "inst-a", PID: self}}
	})

	s.sample()
	report = false
	s.sample()

	snap := s.latest.Load()
	if _, ok := snap.ByName["inst-a"]; !ok {
		t.Fatalf("TTL(%v) 之内漏报一轮就丢了目标；ByName=%v", targetTTL, snap.ByName)
	}
}

// TestSamplerTargetExpiresAfterTTL 目标源持续不再报某个目标时，跟踪状态要被回收，
// 否则已删除的实例会永远留在 procs / targets 里。
func TestSamplerTargetExpiresAfterTTL(t *testing.T) {
	self := int32(os.Getpid())
	s := newTestSampler(nil)
	s.applyTargets([]Target{{Name: "inst-a", PID: self}})

	// 把 lastSeen 拨到 TTL 之前，等价于「连续多轮没再报」
	s.mu.Lock()
	s.targets["inst-a"] = trackedTarget{pid: self, lastSeen: time.Now().Add(-2 * targetTTL)}
	s.mu.Unlock()

	s.sample()

	s.mu.Lock()
	_, tracked := s.targets["inst-a"]
	procCount := len(s.procs)
	s.mu.Unlock()
	if tracked {
		t.Fatal("超过 TTL 的目标没有被淘汰")
	}
	if procCount != 0 {
		t.Fatalf("目标淘汰后进程状态没跟着清理，procs=%d", procCount)
	}
}
