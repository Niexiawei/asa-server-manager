//go:build windows

package winnetetw

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// 聚合参数与 pkg/procnet 对齐（那边叫 targetTTL / pruneInterval）。
const (
	trackedTTL    = 30 * time.Second // 30s 没被 Bytes() 问到即淘汰（采样器 2s 一问 = 15 轮容忍）
	pruneInterval = 10 * time.Second // 淘汰扫描的最小间隔
)

// netCounters 只有累计值，绝不存速率（速率由 serverinfo 采样器差分）。
type netCounters struct {
	rx, tx uint64
}

// aggregator 是 tracked-set + 计数的纯逻辑部分，不碰任何 ETW API——
// 单独拆出来是为了 TTL 语义可以注入时钟做单测（方案 §8）。
//
// 语义与 pkg/procnet 逐条对齐（方案 §2.2 / §4.5）：
//   - add 只累加**已登记**的 PID；未登记的事件直接丢弃（callback 取一次锁 + 一次
//     map miss），counters 条目数因此被限死在「被跟踪的实例数」，不会被系统全量进程撑爆；
//   - get 首次问到某 PID 时登记并返回 0 基线——此后内核事件才开始为其累计，
//     下一轮差分恰好是这两轮之间的流量（与 eBPF targets map 先登记后计数一致）。
type aggregator struct {
	mu       sync.Mutex
	counters map[uint32]*netCounters
	seen     map[uint32]time.Time

	now       func() time.Time // 测试注入；生产恒 time.Now
	lastPrune time.Time
}

func newAggregator(now func() time.Time) *aggregator {
	if now == nil {
		now = time.Now
	}
	return &aggregator{
		counters:  make(map[uint32]*netCounters),
		seen:      make(map[uint32]time.Time),
		now:       now,
		lastPrune: now(),
	}
}

// add 在 ETW callback 线程上被调。持锁更新两个 map 的 get/add——
// 单 session 的 ProcessTrace 回调串行，唯一的并发读者是采样器 2s 一次的
// Bytes()，锁竞争窗口可忽略（方案 §4.5 论证过不用 channel 的理由）。
//
// ⚠️ 查 counters 这一步**必须在锁内**：get 会在锁内插入新条目、pruneLocked 会在
// 锁内删除，锁外读同一张 map 撞上去就是 `fatal error: concurrent map read and
// map write`——那是 fatal 不是 panic，callback 里的 recover 拦不住，整个进程会被
// 带走。触发条件还恰恰是常规路径：某 PID 首次被 Bytes() 问到（= 插入）的同时
// 该 PID 有网络事件在流。见 docs/WINNET_ETW_TODO.md §2.1。
func (a *aggregator) add(pid uint32, k netKind, size uint32) {
	a.mu.Lock()
	if c, tracked := a.counters[pid]; tracked { // 未登记：丢弃（成本 = 一次锁 + 一次 map miss）
		if k == kindTCP_RX || k == kindUDP_RX {
			c.rx += uint64(size)
		} else {
			c.tx += uint64(size)
		}
	}
	a.mu.Unlock()
}

// get 即对外的 Bytes。首次问到登记零值条目并返回 0 基线。
func (a *aggregator) get(pid uint32) (rx, tx uint64, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.seen[pid] = now
	a.pruneLocked(now)
	if c, tracked := a.counters[pid]; tracked {
		return c.rx, c.tx, true
	}
	a.counters[pid] = &netCounters{}
	return 0, 0, true
}

// pruneLocked 淘汰一段时间没人问的 PID，两张 map 一起清。
// 条目数本就被限死在被跟踪的实例数，这里只是防止「实例删了、调用方却再没问过」
// 的条目常驻。
func (a *aggregator) pruneLocked(now time.Time) {
	if now.Sub(a.lastPrune) < pruneInterval {
		return
	}
	a.lastPrune = now
	for pid, last := range a.seen {
		if now.Sub(last) <= trackedTTL {
			continue
		}
		delete(a.seen, pid)
		delete(a.counters, pid)
	}
}

// ---- Collector ----

// Collector 是本包的全部对外 API，形状镜像 pkg/procnet
// （后续期的 procnet_windows.go 委托层按同一形状转发）。
//
// 线程模型：Bytes/Describe/Close 可从任意 goroutine 调；
// ETW 事件由 ProcessTrace 的单一回调线程串行送达。
type Collector struct {
	agg *aggregator
	// sess 在 Load 之后不再改写。Close 只翻 closed 标记，不把它置 nil——
	// 采样器可能正在另一个 goroutine 里读它（组合根先撤 NetSource 再 Close，
	// 但撤下的那一刻可能恰好有一次 Bytes 已经取到接口值了）。
	sess   *etwSession
	closed atomic.Bool

	schemas schemaCache // 仅回调线程访问（串行，无锁）

	eventsReceived atomic.Uint64 // callback 收到的本包关心的事件总数
	parseDropped   atomic.Uint64 // 解析失败被丢弃的事件数（schema 失败 / payload 越界）
	failedSchemas  uint32        // 解析失败的 Event ID 数（仅回调线程写，Describe 读近似值）
}

// eventCallback 是 syscall.NewCallback 的产物。每个进程最多 2000 个回调
// 且不可回收，必须全局只创建一次；Collector 实例经 EVENT_RECORD.UserContext
// （= EVENT_TRACE_LOGFILEW.Context）传回。
var (
	eventCallbackOnce sync.Once
	eventCallbackPtr  uintptr
)

func getEventCallback() uintptr {
	eventCallbackOnce.Do(func() {
		eventCallbackPtr = syscall.NewCallback(func(rec *eventRecord) uintptr {
			// NewCallback 的 panic 没有 recover 会带崩整个进程（方案 §10）。
			// defer+recover 在这里只兜「不该发生但发生了」的 bug，不是常态路径。
			defer func() { _ = recover() }()
			if rec == nil {
				return 0
			}
			c := (*Collector)(rec.UserContext)
			if c != nil {
				c.onEvent(rec)
			}
			return 0
		})
	})
	return eventCallbackPtr
}

// Load 建立 ETW 会话并返回 Collector。失败返回 error——调用方按
// 「失败即降级」处理（实例网络字段为 null，其余指标不受影响）。
//
// 调用方必须在放弃 Collector 前调用 Close：Context 里存的是裸指针，
// ETW 回调不会让它对 GC 可见。
func Load(opts Options) (*Collector, error) {
	c := &Collector{
		agg:     newAggregator(nil),
		schemas: make(schemaCache),
	}
	sess, err := startSession(unsafe.Pointer(c), getEventCallback())
	if err != nil {
		return nil, err
	}
	c.sess = sess
	return c, nil
}

// onEvent 是 ETW callback 的入口（原生线程 → Go）。保持轻：
// 分类 → 解析 → 查 map → 累加，不做任何 I/O、日志、分配（schema 首次解析除外）。
func (c *Collector) onEvent(rec *eventRecord) {
	k, ok := classifyEvent(rec.EventHeader.EventDescriptor.Id)
	if !ok {
		return // EnableTraceEx2 已在内核侧过滤，这里是防御
	}
	c.eventsReceived.Add(1)

	id := rec.EventHeader.EventDescriptor.Id
	s := c.schemas[id]
	if s == nil {
		s = buildEventSchema(rec)
		c.schemas[id] = s
		if s.state == schemaFailed {
			c.failedSchemas++
		}
	}
	pid, size, ok := parsePayload(rec, s)
	if !ok {
		c.parseDropped.Add(1)
		return
	}
	if pid == 0 {
		return // System/KERNEL 的网络活动（上游设计文档 §30），不硬映射成普通进程
	}
	c.agg.add(pid, k, size)
}

// Bytes 返回该 PID 的**累计**收发字节（自其被登记进跟踪集合起算），
// 速率由调用方按 Δt 差分。语义细节见方案 §2.2 / §4.6（PID 复用天然正确）。
// 会话已经不在了（被同机另一个进程抢走 session 名，或已 Close）时返回
// ok=false，而**不是**一个不再增长的累计值：后者会让采样器差分出恒 0，
// 前端画成贴着底边的实线，被读成「实例真的没流量」。采不到就得是 null，
// 这是 RESOURCE_RATE_CHART_PLAN §4.4 的全局约定。见 docs/WINNET_ETW_TODO.md §2.2。
func (c *Collector) Bytes(pid int32) (rx, tx uint64, ok bool) {
	if c == nil || c.sess == nil || pid <= 0 || c.closed.Load() || !c.sess.alive() {
		return 0, 0, false
	}
	return c.agg.get(uint32(pid))
}

// Describe 返回一行可读的运行状态，供上层记日志。
func (c *Collector) Describe() string {
	if c == nil || c.sess == nil {
		return ""
	}
	lost, rtLost := uint32(0), uint32(0)
	if eventsLost, buffersLost, err := c.sess.stats(); err == nil {
		lost, rtLost = eventsLost, buffersLost
	}
	dropped := c.parseDropped.Load()
	desc := fmt.Sprintf("ETW session=%s, 事件=%d, 解析丢弃=%d, 失败schema=%d, 丢事件=%d, 丢实时buffer=%d",
		sessionName, c.eventsReceived.Load(), dropped, c.failedSchemas, lost, rtLost)
	if dropped > 0 || c.failedSchemas > 0 {
		desc += "（部分 Event ID 的 schema 不兼容，对应流量未计入）"
	}
	// 「没流量」与「会话没了」在日志里必须能分开——后者的现象是计数停增，
	// 不写出来只会去查解析代码。
	if !c.closed.Load() && !c.sess.alive() {
		desc += "（⚠️ 会话已终止：多半被同机另一个消费进程抢走了 session 名，实例网络字段将回到 null）"
	}
	return desc
}

// Close 停止会话。顺序：CloseTrace → 等 ProcessTrace 返回 → ControlTraceW(STOP)。
// 必须在进程退出前调用，避免残留 ETW session。
func (c *Collector) Close() error {
	if c == nil || c.sess == nil {
		return nil
	}
	c.closed.Store(true) // 先断掉 Bytes/Describe，再动会话
	c.sess.stop()        // sync.Once：重复 Close 无害
	runtime.KeepAlive(c)
	return c.sess.stopErr()
}
