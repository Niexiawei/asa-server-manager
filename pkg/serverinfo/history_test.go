package serverinfo

import (
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

// memStore 是 HistoryStore 的内存实现，用来验证落盘/恢复的往返。
type memStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemStore() *memStore { return &memStore{data: make(map[string][]byte)} }

func (m *memStore) Put(key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(value))
	copy(cp, value)
	m.data[key] = cp
	return nil
}

func (m *memStore) Scan(prefix string) (map[string][]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]byte)
	for k, v := range m.data {
		if strings.HasPrefix(k, prefix) {
			out[k] = v
		}
	}
	return out, nil
}

func (m *memStore) Delete(keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.data, k)
	}
	return nil
}

func sampleAt(ts time.Time, cpu float64, insts map[string]float64) *Rates {
	byName := make(map[string]ProcRates, len(insts))
	for name, v := range insts {
		byName[name] = ProcRates{PID: 1, Name: name, CPUPercent: v, MemoryPercent: v / 2}
	}
	return &Rates{
		Timestamp: ts,
		Host:      HostRates{CPUUsedPercent: cpu, CoreCount: 4, MemUsedPercent: 50},
		ByName:    byName,
	}
}

func TestHistoryRingTrims(t *testing.T) {
	h := newHistory(nil, SampleInterval)
	base := time.Now().Add(-2 * HistoryWindow)

	total := h.max + 50
	for i := range total {
		h.append(sampleAt(base.Add(time.Duration(i)*SampleInterval), float64(i), map[string]float64{"a": float64(i)}))
	}

	if len(h.ts) != h.max {
		t.Fatalf("时间轴长度 = %d, 期望 %d", len(h.ts), h.max)
	}
	if len(h.host.CPU) != h.max {
		t.Fatalf("host 曲线长度 = %d, 期望 %d", len(h.host.CPU), h.max)
	}
	if got := h.host.CPU[h.max-1]; got != float64(total-1) {
		t.Fatalf("末点 = %v, 期望 %v（应当丢头不丢尾）", got, total-1)
	}
	if len(h.inst["a"].series.CPU) != h.max {
		t.Fatalf("实例曲线长度 = %d, 期望 %d", len(h.inst["a"].series.CPU), h.max)
	}
}

// 实例中途出现时，它的曲线必须被 NaN 补齐到与时间轴等长，否则下标全错位。
func TestHistoryLateInstanceIsPadded(t *testing.T) {
	h := newHistory(nil, SampleInterval)
	base := time.Now()

	h.append(sampleAt(base, 1, map[string]float64{"a": 10}))
	h.append(sampleAt(base.Add(SampleInterval), 2, map[string]float64{"a": 11}))
	h.append(sampleAt(base.Add(2*SampleInterval), 3, map[string]float64{"a": 12, "b": 99}))

	b := h.inst["b"].series
	if len(b.CPU) != len(h.ts) {
		t.Fatalf("后来的实例曲线长度 = %d, 时间轴 = %d", len(b.CPU), len(h.ts))
	}
	if !math.IsNaN(b.CPU[0]) || !math.IsNaN(b.CPU[1]) {
		t.Fatalf("补齐段应为 NaN, 实际 %v", b.CPU[:2])
	}
	if b.CPU[2] != 99 {
		t.Fatalf("末点 = %v, 期望 99", b.CPU[2])
	}
}

// 实例消失后要继续写 NaN 而不是跳过，曲线上才是可见的空洞。
func TestHistoryMissingInstanceGetsNaN(t *testing.T) {
	h := newHistory(nil, SampleInterval)
	base := time.Now()

	h.append(sampleAt(base, 1, map[string]float64{"a": 10}))
	h.append(sampleAt(base.Add(SampleInterval), 2, nil))

	a := h.inst["a"].series
	if len(a.CPU) != 2 {
		t.Fatalf("曲线长度 = %d, 期望 2", len(a.CPU))
	}
	if !math.IsNaN(a.CPU[1]) {
		t.Fatalf("实例消失后应写 NaN, 实际 %v", a.CPU[1])
	}

	s := h.query(HistoryWindow, "a")
	if s.Instance["cpu_percent"][1] != nil {
		t.Fatal("NaN 必须序列化成 null")
	}
	if v := s.Instance["cpu_percent"][0]; v == nil || *v != 10 {
		t.Fatalf("首点 = %v, 期望 10", v)
	}
}

func TestHistoryQueryWindow(t *testing.T) {
	h := newHistory(nil, SampleInterval)
	base := time.Now()
	for i := range 100 {
		h.append(sampleAt(base.Add(time.Duration(i)*SampleInterval), float64(i), nil))
	}

	s := h.query(10*SampleInterval, "")
	if len(s.Timestamps) != 10 {
		t.Fatalf("窗口内点数 = %d, 期望 10", len(s.Timestamps))
	}
	if v := s.Host["cpu_used_percent"][9]; v == nil || *v != 99 {
		t.Fatalf("窗口应取尾部, 末点 = %v", v)
	}
}

// 落盘 → 新实例恢复：时间轴、host 曲线、实例曲线都要按时间戳对齐还原。
func TestHistoryFlushRestoreRoundTrip(t *testing.T) {
	store := newMemStore()
	h := newHistory(store, SampleInterval)
	base := time.Now().Add(-time.Minute).Truncate(time.Second)

	for i := range 20 {
		h.append(sampleAt(base.Add(time.Duration(i)*SampleInterval), float64(i), map[string]float64{"srv": float64(i * 2)}))
	}
	h.flush()

	restored := newHistory(store, SampleInterval)
	restored.restore()

	if len(restored.ts) != 20 {
		t.Fatalf("恢复出的点数 = %d, 期望 20", len(restored.ts))
	}
	if restored.host.CPU[19] != 19 {
		t.Fatalf("host 末点 = %v, 期望 19", restored.host.CPU[19])
	}
	buf, ok := restored.inst["srv"]
	if !ok {
		t.Fatal("实例曲线没恢复出来")
	}
	if len(buf.series.CPU) != 20 {
		t.Fatalf("实例曲线长度 = %d, 期望 20", len(buf.series.CPU))
	}
	if buf.series.CPU[19] != 38 {
		t.Fatalf("实例末点 = %v, 期望 38", buf.series.CPU[19])
	}
	if restored.lastFlushTS != restored.ts[len(restored.ts)-1] {
		t.Fatal("恢复后 lastFlushTS 必须对齐到末点，否则下次刷盘会重写已落盘的段")
	}
}

// 过期的点在恢复时丢弃，避免长时间停机后画出一段远古曲线。
func TestHistoryRestoreDropsExpired(t *testing.T) {
	store := newMemStore()
	h := newHistory(store, SampleInterval)
	old := time.Now().Add(-2 * HistoryWindow)

	for i := range 5 {
		h.append(sampleAt(old.Add(time.Duration(i)*SampleInterval), float64(i), nil))
	}
	h.flush()

	restored := newHistory(store, SampleInterval)
	restored.restore()

	if len(restored.ts) != 0 {
		t.Fatalf("过期点应被丢弃, 实际恢复 %d 个", len(restored.ts))
	}
}

func TestRateOfGuardsCounterReset(t *testing.T) {
	if got := rateOf(5, 100, 2); got != 0 {
		t.Fatalf("计数回绕时应记 0, 实际 %v", got)
	}
	if got := rateOf(100, 0, 0); got != 0 {
		t.Fatalf("Δt 为 0 时应记 0, 实际 %v", got)
	}
	if got := rateOf(200, 100, 2); got != 50 {
		t.Fatalf("速率 = %v, 期望 50", got)
	}
}

func TestNameFromInstKey(t *testing.T) {
	cases := map[string]string{
		"metrics:i:server1:0000000123": "server1",
		"metrics:i:a:b:0000000123":     "a:b",
	}
	for key, want := range cases {
		got, ok := nameFromInstKey(key)
		if !ok || got != want {
			t.Fatalf("nameFromInstKey(%q) = %q,%v, 期望 %q", key, got, ok, want)
		}
	}
	if _, ok := nameFromInstKey("metrics:h:0000000123"); ok {
		t.Fatal("host 键不该被当成实例键")
	}
}
