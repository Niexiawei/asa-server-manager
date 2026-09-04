package serverinfo

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"asa-server/pkg/logger"
)

// 历史数据：内存环形缓冲是真相，每 FlushInterval 分块落一次持久化后端。
//
// 为什么不是「每次采样写一行数据库」：那是每天 4 万多次写、LSM 持续 compaction，
// 而这份数据 30 分钟后必然丢弃；更讽刺的是本功能自己就在测磁盘 IOPS，会把自己写进曲线。
// 详见 docs/RESOURCE_RATE_CHART_PLAN.md §3.1.2。
const (
	// HistoryWindow 保留时长。前端最大渲染窗口是 15 分钟，这里留一倍余量。
	HistoryWindow = 30 * time.Minute

	// FlushInterval 刷盘周期。崩溃最多丢这么久的数据，表现为曲线上一段空洞。
	FlushInterval = 5 * time.Minute

	// historySchemaVersion 变了就直接丢弃旧块，不做迁移。
	historySchemaVersion = 1

	metricsHostPrefix = "metrics:h:"
	metricsInstPrefix = "metrics:i:"

	// maxChunkBytes 单值上限。Badger 的 ValueThreshold 是 1MB，超过就落 value log，
	// 而本仓库全局没有 RunValueLogGC 调用 —— 落了 vlog 就等于磁盘只增不减。
	// 实测一块（150 点）在十几 KB 量级，这里留足余量并做兜底检查。
	maxChunkBytes = 512 * 1024
)

// HistoryStore 是持久化后端。**只搬字节，不认识任何领域概念**——
// 这样 pkg/serverinfo 不必 import internal/state（Badger 实现在那边）。
type HistoryStore interface {
	Put(key string, value []byte) error
	Scan(prefix string) (map[string][]byte, error)
	Delete(keys []string) error
}

type hostSeries struct {
	CPU           []float64
	Mem           []float64
	DiskRead      []float64
	DiskWrite     []float64
	DiskReadIOPS  []float64
	DiskWriteIOPS []float64
	NetRecv       []float64
	NetSent       []float64
}

func (s *hostSeries) fields() []*[]float64 {
	return []*[]float64{&s.CPU, &s.Mem, &s.DiskRead, &s.DiskWrite,
		&s.DiskReadIOPS, &s.DiskWriteIOPS, &s.NetRecv, &s.NetSent}
}

func (s *hostSeries) columns() map[string][]float64 {
	return map[string][]float64{
		"cpu_used_percent":         s.CPU,
		"mem_used_percent":         s.Mem,
		"disk_read_bytes_per_sec":  s.DiskRead,
		"disk_write_bytes_per_sec": s.DiskWrite,
		"disk_read_iops":           s.DiskReadIOPS,
		"disk_write_iops":          s.DiskWriteIOPS,
		"net_recv_bytes_per_sec":   s.NetRecv,
		"net_sent_bytes_per_sec":   s.NetSent,
	}
}

type instSeries struct {
	CPU           []float64
	CPUTotal      []float64
	MemPercent    []float64
	MemUsed       []float64
	DiskRead      []float64
	DiskWrite     []float64
	DiskReadIOPS  []float64
	DiskWriteIOPS []float64
	NetRecv       []float64
	NetSent       []float64
}

func (s *instSeries) fields() []*[]float64 {
	return []*[]float64{&s.CPU, &s.CPUTotal, &s.MemPercent, &s.MemUsed,
		&s.DiskRead, &s.DiskWrite, &s.DiskReadIOPS, &s.DiskWriteIOPS, &s.NetRecv, &s.NetSent}
}

func (s *instSeries) columns() map[string][]float64 {
	return map[string][]float64{
		"cpu_percent":              s.CPU,
		"cpu_total_percent":        s.CPUTotal,
		"memory_percent":           s.MemPercent,
		"memory_used":              s.MemUsed,
		"disk_read_bytes_per_sec":  s.DiskRead,
		"disk_write_bytes_per_sec": s.DiskWrite,
		"disk_read_iops":           s.DiskReadIOPS,
		"disk_write_iops":          s.DiskWriteIOPS,
		"net_recv_bytes_per_sec":   s.NetRecv,
		"net_sent_bytes_per_sec":   s.NetSent,
	}
}

// hostChunk / instChunk 是落盘的单位。每块自带 TS，恢复时按时间戳对齐，
// 不依赖块之间的隐式顺序。
type hostChunk struct {
	Version int
	TS      []float64
	Series  hostSeries
}

type instChunk struct {
	Version int
	TS      []float64
	Series  instSeries
}

type instBuf struct {
	series   instSeries
	lastSeen time.Time
}

type history struct {
	mu       sync.RWMutex
	store    HistoryStore
	interval time.Duration
	max      int

	ts   []float64
	host hostSeries
	inst map[string]*instBuf

	lastFlushTS float64
}

func newHistory(store HistoryStore, interval time.Duration) *history {
	if interval <= 0 {
		interval = SampleInterval
	}
	max := int(HistoryWindow / interval)
	if max < 2 {
		max = 2
	}
	return &history{
		store:    store,
		interval: interval,
		max:      max,
		inst:     make(map[string]*instBuf),
	}
}

// Series 是对外的查询结果。列存而非对象数组：900 个点下 JSON 体积差 3~4 倍，
// 而且前端拿到就能直接喂 uPlot。nil 元素表示这一点采不到（前端画断点）。
type Series struct {
	Timestamps []float64             `json:"timestamps"`
	Host       map[string][]*float64 `json:"host"`
	Instance   map[string][]*float64 `json:"instance,omitempty"`
}

// GetHistory 取最近 window 时长的历史。instance 为空则只回 host。
func GetHistory(window time.Duration, instance string) Series {
	s := globalSampler.Load()
	if s == nil {
		return Series{Timestamps: []float64{}, Host: map[string][]*float64{}}
	}
	return s.hist.query(window, instance)
}

func (h *history) query(window time.Duration, instance string) Series {
	h.mu.RLock()
	defer h.mu.RUnlock()

	n := len(h.ts)
	want := n
	if window > 0 {
		if w := int(window / h.interval); w > 0 && w < n {
			want = w
		}
	}
	start := n - want

	out := Series{
		Timestamps: append([]float64(nil), h.ts[start:]...),
		Host:       make(map[string][]*float64, 8),
	}
	for name, col := range h.host.columns() {
		out.Host[name] = toNullable(col[start:])
	}
	if instance != "" {
		if buf, ok := h.inst[instance]; ok {
			out.Instance = make(map[string][]*float64, 10)
			for name, col := range buf.series.columns() {
				out.Instance[name] = toNullable(col[start:])
			}
		}
	}
	return out
}

func toNullable(col []float64) []*float64 {
	out := make([]*float64, len(col))
	for i, v := range col {
		out[i] = nanToNil(v)
	}
	return out
}

// append 把一次采样结果推进环形缓冲。
//
// 未运行的实例照样写 NaN 点而不是跳过：否则同一条实例曲线的下标与时间轴对不上，
// 前端还得再补一次；NaN 在输出时变成 null，图上是可见的空洞。
func (h *history) append(r *Rates) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ts := float64(r.Timestamp.UnixMilli()) / 1000
	h.ts = trimTail(append(h.ts, ts), h.max)

	h.pushHostLocked(r.Host)

	n := len(h.ts)
	for name, pr := range r.ByName {
		buf, ok := h.inst[name]
		if !ok {
			buf = &instBuf{}
			// 新出现的实例先用 NaN 补齐到与时间轴等长，保证下标对齐
			padTo(&buf.series, n-1)
			h.inst[name] = buf
		}
		buf.lastSeen = r.Timestamp
		pushInst(&buf.series, pr, float64(r.Host.CoreCount))
		trimInst(&buf.series, h.max)
	}

	for name, buf := range h.inst {
		if _, ok := r.ByName[name]; ok {
			continue
		}
		if r.Timestamp.Sub(buf.lastSeen) > HistoryWindow {
			delete(h.inst, name)
			continue
		}
		pushInstNaN(&buf.series)
		trimInst(&buf.series, h.max)
	}
}

func (h *history) pushHostLocked(hr HostRates) {
	h.host.CPU = trimTail(append(h.host.CPU, hr.CPUUsedPercent), h.max)
	h.host.Mem = trimTail(append(h.host.Mem, hr.MemUsedPercent), h.max)
	h.host.DiskRead = trimTail(append(h.host.DiskRead, hr.DiskReadBytesPS), h.max)
	h.host.DiskWrite = trimTail(append(h.host.DiskWrite, hr.DiskWriteBytesPS), h.max)
	h.host.DiskReadIOPS = trimTail(append(h.host.DiskReadIOPS, hr.DiskReadIOPS), h.max)
	h.host.DiskWriteIOPS = trimTail(append(h.host.DiskWriteIOPS, hr.DiskWriteIOPS), h.max)
	h.host.NetRecv = trimTail(append(h.host.NetRecv, hr.NetRecvBytesPS), h.max)
	h.host.NetSent = trimTail(append(h.host.NetSent, hr.NetSentBytesPS), h.max)
}

func pushInst(s *instSeries, pr ProcRates, cores float64) {
	cpuTotal := math.NaN()
	if cores > 0 {
		cpuTotal = pr.CPUPercent / cores
	}
	s.CPU = append(s.CPU, pr.CPUPercent)
	s.CPUTotal = append(s.CPUTotal, cpuTotal)
	s.MemPercent = append(s.MemPercent, pr.MemoryPercent)
	s.MemUsed = append(s.MemUsed, float64(pr.MemoryUsed))
	s.DiskRead = append(s.DiskRead, valueOr(pr.IOReadBytesPS))
	s.DiskWrite = append(s.DiskWrite, valueOr(pr.IOWriteBytesPS))
	s.DiskReadIOPS = append(s.DiskReadIOPS, valueOr(pr.IOReadIOPS))
	s.DiskWriteIOPS = append(s.DiskWriteIOPS, valueOr(pr.IOWriteIOPS))
	s.NetRecv = append(s.NetRecv, valueOr(pr.NetRxBytesPS))
	s.NetSent = append(s.NetSent, valueOr(pr.NetTxBytesPS))
}

func pushInstNaN(s *instSeries) {
	for _, f := range s.fields() {
		*f = append(*f, math.NaN())
	}
}

func padTo(s *instSeries, n int) {
	for _, f := range s.fields() {
		for len(*f) < n {
			*f = append(*f, math.NaN())
		}
	}
}

func trimInst(s *instSeries, max int) {
	for _, f := range s.fields() {
		*f = trimTail(*f, max)
	}
}

func trimTail(s []float64, max int) []float64 {
	if len(s) <= max {
		return s
	}
	drop := len(s) - max
	copy(s, s[drop:])
	return s[:max]
}

// flush 把上次刷盘之后的点分块写进持久化后端，并清掉过期的块。
func (h *history) flush() {
	if h.store == nil {
		return
	}

	h.mu.Lock()
	start := 0
	for start < len(h.ts) && h.ts[start] <= h.lastFlushTS {
		start++
	}
	if start >= len(h.ts) {
		h.mu.Unlock()
		h.cleanupExpired()
		return
	}
	chunkTS := append([]float64(nil), h.ts[start:]...)
	hostCopy := sliceHost(&h.host, start)
	instCopies := make(map[string]instSeries, len(h.inst))
	for name, buf := range h.inst {
		s := sliceInst(&buf.series, start)
		if allNaN(&s) {
			continue
		}
		instCopies[name] = s
	}
	last := chunkTS[len(chunkTS)-1]
	h.mu.Unlock()

	key := fmt.Sprintf("%s%010d", metricsHostPrefix, int64(chunkTS[0]))
	if err := h.put(key, hostChunk{Version: historySchemaVersion, TS: chunkTS, Series: hostCopy}); err != nil {
		logger.Warnf("指标历史落盘失败(host): %v", err)
		return
	}
	for name, s := range instCopies {
		k := fmt.Sprintf("%s%s:%010d", metricsInstPrefix, name, int64(chunkTS[0]))
		if err := h.put(k, instChunk{Version: historySchemaVersion, TS: chunkTS, Series: s}); err != nil {
			logger.Warnf("指标历史落盘失败(%s): %v", name, err)
		}
	}

	h.mu.Lock()
	h.lastFlushTS = last
	h.mu.Unlock()

	h.cleanupExpired()
}

func (h *history) put(key string, v any) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return err
	}
	if buf.Len() > maxChunkBytes {
		return fmt.Errorf("块过大(%d 字节)，跳过以免落进 value log", buf.Len())
	}
	return h.store.Put(key, buf.Bytes())
}

func (h *history) cleanupExpired() {
	cutoff := float64(time.Now().Add(-HistoryWindow - FlushInterval).Unix())
	var expired []string
	for _, prefix := range []string{metricsHostPrefix, metricsInstPrefix} {
		items, err := h.store.Scan(prefix)
		if err != nil {
			continue
		}
		for key := range items {
			if ts, ok := tsFromKey(key); ok && ts < cutoff {
				expired = append(expired, key)
			}
		}
	}
	if len(expired) == 0 {
		return
	}
	if err := h.store.Delete(expired); err != nil {
		logger.Warnf("清理过期指标历史失败: %v", err)
	}
}

// restore 从持久化后端恢复。任何一步失败都只降级为空缓冲，不阻断启动。
func (h *history) restore() {
	if h.store == nil {
		return
	}
	now := time.Now()
	cutoff := float64(now.Add(-HistoryWindow).Unix())
	// 时钟被往回调过的话，未来的点会把时间轴撑坏，直接丢掉
	future := float64(now.Add(time.Minute).Unix())

	hostItems, err := h.store.Scan(metricsHostPrefix)
	if err != nil {
		logger.Warnf("读取指标历史失败: %v", err)
		return
	}

	type decoded struct {
		ts     []float64
		series hostSeries
	}
	chunks := make([]decoded, 0, len(hostItems))
	for _, raw := range hostItems {
		var c hostChunk
		if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&c); err != nil || c.Version != historySchemaVersion {
			continue
		}
		chunks = append(chunks, decoded{ts: c.TS, series: c.Series})
	}
	if len(chunks) == 0 {
		return
	}
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].ts[0] < chunks[j].ts[0] })

	h.mu.Lock()
	defer h.mu.Unlock()

	index := make(map[int64]int)
	for _, c := range chunks {
		cols := c.series.columns()
		for i, ts := range c.ts {
			if ts < cutoff || ts > future {
				continue
			}
			key := int64(ts * 1000)
			if _, dup := index[key]; dup {
				continue
			}
			index[key] = len(h.ts)
			h.ts = append(h.ts, ts)
			appendAt(&h.host, cols, i)
		}
	}
	if len(h.ts) == 0 {
		return
	}
	h.lastFlushTS = h.ts[len(h.ts)-1]

	instItems, err := h.store.Scan(metricsInstPrefix)
	if err != nil {
		return
	}
	for key, raw := range instItems {
		name, ok := nameFromInstKey(key)
		if !ok {
			continue
		}
		var c instChunk
		if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&c); err != nil || c.Version != historySchemaVersion {
			continue
		}
		buf, ok := h.inst[name]
		if !ok {
			buf = &instBuf{lastSeen: now}
			padTo(&buf.series, len(h.ts))
			h.inst[name] = buf
		}
		cols := c.Series.columns()
		for i, ts := range c.TS {
			pos, ok := index[int64(ts*1000)]
			if !ok {
				continue
			}
			setAt(&buf.series, cols, i, pos)
		}
	}

	// 恢复来的点数可能超过窗口（比如跨了一次长时间停机），统一裁到上限
	h.ts = trimTail(h.ts, h.max)
	for _, f := range h.host.fields() {
		*f = trimTail(*f, h.max)
	}
	for _, buf := range h.inst {
		trimInst(&buf.series, h.max)
	}

	logger.Infof("已恢复 %d 个指标历史采样点（%d 个实例）", len(h.ts), len(h.inst))
}

func appendAt(dst *hostSeries, cols map[string][]float64, i int) {
	get := func(name string) float64 {
		col := cols[name]
		if i < len(col) {
			return col[i]
		}
		return math.NaN()
	}
	dst.CPU = append(dst.CPU, get("cpu_used_percent"))
	dst.Mem = append(dst.Mem, get("mem_used_percent"))
	dst.DiskRead = append(dst.DiskRead, get("disk_read_bytes_per_sec"))
	dst.DiskWrite = append(dst.DiskWrite, get("disk_write_bytes_per_sec"))
	dst.DiskReadIOPS = append(dst.DiskReadIOPS, get("disk_read_iops"))
	dst.DiskWriteIOPS = append(dst.DiskWriteIOPS, get("disk_write_iops"))
	dst.NetRecv = append(dst.NetRecv, get("net_recv_bytes_per_sec"))
	dst.NetSent = append(dst.NetSent, get("net_sent_bytes_per_sec"))
}

func setAt(dst *instSeries, cols map[string][]float64, src, pos int) {
	dstCols := dst.columns()
	for name, col := range cols {
		target := dstCols[name]
		if pos < len(target) && src < len(col) {
			target[pos] = col[src]
		}
	}
}

func sliceHost(s *hostSeries, start int) hostSeries {
	cut := func(col []float64) []float64 {
		if start >= len(col) {
			return nil
		}
		return append([]float64(nil), col[start:]...)
	}
	return hostSeries{
		CPU:           cut(s.CPU),
		Mem:           cut(s.Mem),
		DiskRead:      cut(s.DiskRead),
		DiskWrite:     cut(s.DiskWrite),
		DiskReadIOPS:  cut(s.DiskReadIOPS),
		DiskWriteIOPS: cut(s.DiskWriteIOPS),
		NetRecv:       cut(s.NetRecv),
		NetSent:       cut(s.NetSent),
	}
}

func sliceInst(s *instSeries, start int) instSeries {
	cut := func(col []float64) []float64 {
		if start >= len(col) {
			return nil
		}
		return append([]float64(nil), col[start:]...)
	}
	return instSeries{
		CPU:           cut(s.CPU),
		CPUTotal:      cut(s.CPUTotal),
		MemPercent:    cut(s.MemPercent),
		MemUsed:       cut(s.MemUsed),
		DiskRead:      cut(s.DiskRead),
		DiskWrite:     cut(s.DiskWrite),
		DiskReadIOPS:  cut(s.DiskReadIOPS),
		DiskWriteIOPS: cut(s.DiskWriteIOPS),
		NetRecv:       cut(s.NetRecv),
		NetSent:       cut(s.NetSent),
	}
}

func allNaN(s *instSeries) bool {
	for _, f := range s.fields() {
		for _, v := range *f {
			if !math.IsNaN(v) {
				return false
			}
		}
	}
	return true
}

func tsFromKey(key string) (float64, bool) {
	idx := strings.LastIndex(key, ":")
	if idx < 0 {
		return 0, false
	}
	v, err := strconv.ParseInt(key[idx+1:], 10, 64)
	if err != nil {
		return 0, false
	}
	return float64(v), true
}

// nameFromInstKey 解析 metrics:i:<实例名>:<ts>。实例名本身可能含冒号，
// 所以从右边切：最后一段是时间戳，中间全是名字。
func nameFromInstKey(key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, metricsInstPrefix)
	if !ok {
		return "", false
	}
	idx := strings.LastIndex(rest, ":")
	if idx <= 0 {
		return "", false
	}
	return rest[:idx], true
}
