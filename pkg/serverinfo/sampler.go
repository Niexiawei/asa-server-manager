package serverinfo

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"asa-server/pkg/logger"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// 采样器：进程内单例，统一采集宿主机与目标进程的速率类指标。
//
// 为什么要有它：速率指标（磁盘 B/s、IOPS、网络 B/s）都需要「上一次累计计数 + 时间戳」，
// 而 SSE handler 是每个连接一个 goroutine，把状态放在 handler 局部会导致多客户端各自采样、
// 各自阻塞、状态随连接断开丢失。详见 docs/RESOURCE_RATE_CHART_PLAN.md §3.1。
const (
	// SampleInterval 采样周期。**必须与 SSE 推送周期一致**：若采样比推送快，
	// 曲线上每个点只代表最近一个采样窗口，中间的字节数被丢弃，那是抽样不是平均。
	SampleInterval = 2 * time.Second

	// targetTTL 目标进程的存活期。多个 SSE 连接会各自调用 SetTargets，
	// 取并集 + TTL 收敛，避免两个连接互相抹掉对方的目标。
	targetTTL = 3 * SampleInterval
)

// Target 是一个待采样的进程。Name 只是调用方给的标签（本项目里是实例名），
// 采样器不认识它的含义，只拿来做 key —— 按名字而非 PID 索引，
// 实例重启换了 PID 时曲线才是连续的。
type Target struct {
	Name string
	PID  int32
}

// HostRates 宿主机整机指标
type HostRates struct {
	CPUUsedPercent   float64
	CoreCount        int
	MemUsed          uint64
	MemTotal         uint64
	MemUsedPercent   float64
	DiskReadBytesPS  float64
	DiskWriteBytesPS float64
	DiskReadIOPS     float64
	DiskWriteIOPS    float64
	NetRecvBytesPS   float64
	NetSentBytesPS   float64
}

// ProcRates 单个目标进程的指标。指针字段为 nil 表示这一项采不到
// （权限不足、平台不支持等），与「速率真的是 0」是两回事，前端据此画断点。
type ProcRates struct {
	PID           int32
	Name          string
	CPUPercent    float64
	MemoryUsed    uint64
	MemoryPercent float64

	IOReadBytesPS  *float64
	IOWriteBytesPS *float64
	IOReadIOPS     *float64
	IOWriteIOPS    *float64

	// NetRxBytesPS / NetTxBytesPS 仅 Linux + eBPF 可用时非 nil（见 pkg/procnet）
	NetRxBytesPS *float64
	NetTxBytesPS *float64
}

// Rates 一次采样的完整结果。**发布后即只读**：ByName 不得再写入，
// 每轮构造新 map 整体替换（并发读写 map 会直接崩）。
type Rates struct {
	Timestamp time.Time
	Host      HostRates
	ByName    map[string]ProcRates
}

// Options 采样器的可选依赖。全部可为零值。
type Options struct {
	// History 为历史数据的持久化后端（Badger 实现在 internal/state）。
	// 为 nil 时只保留内存环形缓冲，进程重启后历史丢失。
	History HistoryStore

	// Interval 采样周期，零值取 SampleInterval。
	Interval time.Duration
}

type trackedTarget struct {
	pid      int32
	lastSeen time.Time
}

// procState 缓存单个进程的采样状态。
//
// createTime 用于识别 **PID 复用**：实例重启后系统可能把同一个 PID 分给新进程，
// 只按 PID 存 prev 计数会拿旧进程的累计值做差，`cur < prev` 那条兜底挡不住
// 「新进程计数恰好更大」的情形。
type procState struct {
	proc       *process.Process
	pid        int32
	createTime int64
	name       string

	prevIO *process.IOCountersStat
	prevAt time.Time

	prevNetRx  uint64
	prevNetTx  uint64
	prevNetAt  time.Time
	hasPrevNet bool
}

type diskCounters struct {
	readBytes  uint64
	writeBytes uint64
	readCount  uint64
	writeCount uint64
}

type netCounters struct {
	recvBytes uint64
	sentBytes uint64
}

type Sampler struct {
	interval time.Duration

	mu      sync.Mutex
	targets map[string]trackedTarget
	procs   map[int32]*procState

	prevDisk    diskCounters
	prevNet     netCounters
	prevAt      time.Time
	hasPrevHost bool

	coreCount int

	latest atomic.Pointer[Rates]
	hist   *history

	stopOnce sync.Once
	done     chan struct{}
}

var (
	globalSampler atomic.Pointer[Sampler]
	startOnce     sync.Once
)

// StartSampler 启动进程内单例采样器。重复调用只有第一次生效。
// 由 APIServer.Start() 调用一次；GUI 不接（它直接用 GetCPUInfo 显示托盘信息）。
func StartSampler(ctx context.Context, opts Options) *Sampler {
	startOnce.Do(func() {
		interval := opts.Interval
		if interval <= 0 {
			interval = SampleInterval
		}
		s := &Sampler{
			interval: interval,
			targets:  make(map[string]trackedTarget),
			procs:    make(map[int32]*procState),
			hist:     newHistory(opts.History, interval),
			done:     make(chan struct{}),
		}
		if n, err := cpu.Counts(true); err == nil {
			s.coreCount = n
		}
		s.hist.restore()
		globalSampler.Store(s)
		go s.run(ctx)
	})
	return globalSampler.Load()
}

// StopSampler 停止采样并把内存里的历史补刷一次。
// **必须排在 state.CloseStateManager() 之前**：刷盘用的就是那个 Badger 实例。
func StopSampler() {
	s := globalSampler.Load()
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.done)
	})
	s.hist.flush()
}

// Snapshot 无锁读取最新一份采样结果。采样器未启动或尚未采到第一帧时返回 nil。
func Snapshot() *Rates {
	s := globalSampler.Load()
	if s == nil {
		return nil
	}
	return s.latest.Load()
}

// SetTargets 告知采样器「现在需要盯着哪些进程」。
// 多个 SSE 连接会各自每轮调用，语义是并集 + TTL（见 targetTTL）。
func SetTargets(targets []Target) {
	s := globalSampler.Load()
	if s == nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range targets {
		if t.Name == "" || t.PID <= 0 {
			continue
		}
		s.targets[t.Name] = trackedTarget{pid: t.PID, lastSeen: now}
	}
}

func (s *Sampler) run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	flushTicker := time.NewTicker(FlushInterval)
	defer flushTicker.Stop()

	// 立刻采一次，让第一个接入的客户端不用等一个周期
	s.sample()

	for {
		select {
		case <-ctx.Done():
			s.hist.flush()
			return
		case <-s.done:
			return
		case <-ticker.C:
			s.sample()
		case <-flushTicker.C:
			s.hist.flush()
		}
	}
}

func (s *Sampler) sample() {
	now := time.Now()

	s.mu.Lock()
	dt := 0.0
	if s.hasPrevHost {
		dt = now.Sub(s.prevAt).Seconds()
	}
	host := s.sampleHostLocked(dt)
	byName := s.sampleTargetsLocked(now)
	s.prevAt = now
	s.hasPrevHost = true
	s.mu.Unlock()

	rates := &Rates{Timestamp: now, Host: host, ByName: byName}
	s.latest.Store(rates)
	s.hist.append(rates)
}

func (s *Sampler) sampleHostLocked(dt float64) HostRates {
	host := HostRates{CoreCount: s.coreCount}

	// interval<=0 走的是「距上次调用」的增量，不阻塞。
	// gopsutil 内部为此维护了一份包级状态；GUI 那边用的是 interval>0 的路径，
	// 不碰这份状态（cpu/cpu.go:154-176），两者互不干扰。
	if pct, err := cpu.Percent(0, false); err == nil && len(pct) > 0 {
		host.CPUUsedPercent = pct[0]
	} else if err != nil {
		logger.Debugf("采样 CPU 使用率失败: %v", err)
	}

	if v, err := mem.VirtualMemory(); err == nil {
		host.MemUsed = v.Used
		host.MemTotal = v.Total
		host.MemUsedPercent = v.UsedPercent
	} else {
		logger.Debugf("采样内存失败: %v", err)
	}

	if cur, err := collectDiskCounters(); err == nil {
		if dt > 0 {
			host.DiskReadBytesPS = rateOf(cur.readBytes, s.prevDisk.readBytes, dt)
			host.DiskWriteBytesPS = rateOf(cur.writeBytes, s.prevDisk.writeBytes, dt)
			host.DiskReadIOPS = rateOf(cur.readCount, s.prevDisk.readCount, dt)
			host.DiskWriteIOPS = rateOf(cur.writeCount, s.prevDisk.writeCount, dt)
		}
		s.prevDisk = cur
	} else {
		logger.Debugf("采样磁盘 IO 失败: %v", err)
	}

	if cur, err := collectNetCounters(); err == nil {
		if dt > 0 {
			host.NetRecvBytesPS = rateOf(cur.recvBytes, s.prevNet.recvBytes, dt)
			host.NetSentBytesPS = rateOf(cur.sentBytes, s.prevNet.sentBytes, dt)
		}
		s.prevNet = cur
	} else {
		logger.Debugf("采样网络 IO 失败: %v", err)
	}

	return host
}

func (s *Sampler) sampleTargetsLocked(now time.Time) map[string]ProcRates {
	byName := make(map[string]ProcRates, len(s.targets))
	alive := make(map[int32]struct{}, len(s.targets))

	for name, t := range s.targets {
		if now.Sub(t.lastSeen) > targetTTL {
			delete(s.targets, name)
			continue
		}
		st := s.procStateLocked(t.pid)
		if st == nil {
			continue
		}
		alive[t.pid] = struct{}{}
		if pr, ok := s.sampleProcLocked(st, now); ok {
			byName[name] = pr
		}
	}

	// 清理已经不再被跟踪的进程状态，避免 PID 无限堆积
	for pid := range s.procs {
		if _, ok := alive[pid]; !ok {
			delete(s.procs, pid)
		}
	}
	return byName
}

// procStateLocked 取出（或建立）某个 PID 的采样状态。
// 进程不存在、或 createTime 与缓存不符（PID 被复用）时重建。
func (s *Sampler) procStateLocked(pid int32) *procState {
	if st, ok := s.procs[pid]; ok {
		ct, err := st.proc.CreateTime()
		if err == nil && ct == st.createTime {
			return st
		}
		delete(s.procs, pid)
	}

	p, err := process.NewProcess(pid)
	if err != nil {
		return nil
	}
	ct, err := p.CreateTime()
	if err != nil {
		return nil
	}
	name, err := p.Name()
	if err != nil {
		name = "Unknown"
	}
	st := &procState{proc: p, pid: pid, createTime: ct, name: name}
	// 建立 CPU 基线：Percent(0) 首次调用恒返回 0，下一轮才有真实值
	_, _ = p.Percent(0)
	s.procs[pid] = st
	return st
}

func (s *Sampler) sampleProcLocked(st *procState, now time.Time) (ProcRates, bool) {
	pr := ProcRates{PID: st.pid, Name: st.name}

	// Percent(0) 才是「两次调用之间」的占用；CPUPercent() 是「创建至今的平均值」，
	// 复用 Process 对象也改变不了它的语义（process/process.go:365-383）。
	// 量纲是「单核 100%」，多核下可以超过 100。
	if v, err := st.proc.Percent(0); err == nil {
		pr.CPUPercent = v
	}

	memInfo, err := st.proc.MemoryInfo()
	if err != nil || memInfo == nil {
		// 内存都拿不到，多半是进程已经退出
		return pr, false
	}
	pr.MemoryUsed = memInfo.RSS
	if mp, err := st.proc.MemoryPercent(); err == nil {
		pr.MemoryPercent = float64(mp)
	}

	if io, err := st.proc.IOCounters(); err == nil && io != nil {
		if st.prevIO != nil {
			if dt := now.Sub(st.prevAt).Seconds(); dt > 0 {
				pr.IOReadBytesPS = ptr(rateOf(io.ReadBytes, st.prevIO.ReadBytes, dt))
				pr.IOWriteBytesPS = ptr(rateOf(io.WriteBytes, st.prevIO.WriteBytes, dt))
				pr.IOReadIOPS = ptr(rateOf(io.ReadCount, st.prevIO.ReadCount, dt))
				pr.IOWriteIOPS = ptr(rateOf(io.WriteCount, st.prevIO.WriteCount, dt))
			}
		}
		st.prevIO = io
		st.prevAt = now
	}

	// 按进程的网络计量只有 Linux + eBPF 能给（见 pkg/procnet），未注入时恒为 nil
	if src := netSource.Load(); src != nil {
		if rx, tx, ok := (*src).Bytes(st.pid); ok {
			if st.hasPrevNet {
				if dt := now.Sub(st.prevNetAt).Seconds(); dt > 0 {
					pr.NetRxBytesPS = ptr(rateOf(rx, st.prevNetRx, dt))
					pr.NetTxBytesPS = ptr(rateOf(tx, st.prevNetTx, dt))
				}
			}
			st.prevNetRx, st.prevNetTx, st.prevNetAt, st.hasPrevNet = rx, tx, now, true
		}
	}

	return pr, true
}

// rateOf 计算每秒速率。计数回绕/重置（cur < prev）时记 0，不产生负数尖峰。
func rateOf(cur, prev uint64, dt float64) float64 {
	if dt <= 0 || cur < prev {
		return 0
	}
	return float64(cur-prev) / dt
}

func ptr(v float64) *float64 { return &v }

func collectDiskCounters() (diskCounters, error) {
	var out diskCounters
	stats, err := disk.IOCounters()
	if err != nil {
		return out, err
	}
	for name, st := range stats {
		if !includeDisk(name) {
			continue
		}
		out.readBytes += st.ReadBytes
		out.writeBytes += st.WriteBytes
		out.readCount += st.ReadCount
		out.writeCount += st.WriteCount
	}
	return out, nil
}

// collectNetCounters 必须自己按网卡筛。net.IOCounters(false) 会把所有接口求和，
// 包含 lo / docker0 / veth*（net/net_linux.go:135-140 的 getIOCountersAll），
// 本机 SSE、RCON、反代的回环流量会被算进「网络速度」，数字明显虚高。
func collectNetCounters() (netCounters, error) {
	var out netCounters
	stats, err := net.IOCounters(true)
	if err != nil {
		return out, err
	}
	for _, st := range stats {
		if !includeNIC(st.Name) {
			continue
		}
		out.recvBytes += st.BytesRecv
		out.sentBytes += st.BytesSent
	}
	return out, nil
}

// nanToNil 把内部用的 NaN（表示「采不到」）转成 JSON 里的 null。
func nanToNil(v float64) *float64 {
	if math.IsNaN(v) {
		return nil
	}
	out := v
	return &out
}

func valueOr(p *float64) float64 {
	if p == nil {
		return math.NaN()
	}
	return *p
}
