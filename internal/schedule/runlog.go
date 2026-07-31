package schedule

import (
	cfgpkg "asa-server/config"
	"asa-server/logger"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	logStoreFileName = "schedule_logs.json"

	// maxRunRecords 是**全局**保留的执行记录条数上限。
	//
	// 超出后丢弃最旧的（滚动窗口），不是清空——字面意义的清空会让用户在第 501 次
	// 执行后突然失去全部历史，包括昨晚那次失败，恰好是最需要它的时候。
	//
	// 按每天 3 次执行估算，500 条约等于半年历史。
	maxRunRecords = 500

	// defaultLogLimit 是查询接口不传 limit 时返回的条数。
	defaultLogLimit = 100
)

// TriggerSource 区分执行是怎么被触发的。
//
// 定时触发与手动「立即执行」现在都走 Scheduler.execute，
// 不记这个字段的话事后完全分不出来。
type TriggerSource string

const (
	TriggerSchedule TriggerSource = "schedule" // 到点自动触发
	TriggerManual   TriggerSource = "manual"   // 用户点了「立即执行」
)

// RunRecord 一次任务执行的档案。
//
// 刻意不做逐步骤的流式日志：批量重启的过程日志 batchmanage 已有完整 SSE 流
// 与 500 条回放，更新过程也有 updatemanage 的流。这里记的是「这次跑了没有、
// 跑了多久、成没成、为什么没成」，过程细节留在各自的流里。
type RunRecord struct {
	ID string `json:"id"`

	// 任务身份冗余存一份：任务被删掉之后，历史记录仍然要能读懂
	TaskID   string   `json:"task_id"`
	TaskName string   `json:"task_name"`
	TaskType TaskType `json:"task_type"`

	Trigger TriggerSource `json:"trigger"`

	StartedAt  time.Time `json:"started_at"`
	DurationMs int64     `json:"duration_ms"`

	Success bool `json:"success"`

	// Message 成功时是摘要（「已重启 5 个实例」），失败时是原因
	// （「2/5 个实例重启失败：jibian、meijue」）。
	Message string `json:"message"`
}

// runRecordSeq 给同一纳秒内产生的记录去重。
// 手动执行与定时触发可能几乎同时落记录，只靠时间戳不保险。
var runRecordSeq atomic.Uint64

func newRunRecordID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36) +
		"-" + strconv.FormatUint(runRecordSeq.Add(1), 36)
}

// logStore 执行日志的内存副本 + JSON 落盘。
//
// 与任务定义分开存：日志会被裁剪、可能被清空、坏了丢掉就行；
// 任务定义必须保住，而且要保持「能直接打开手改」的可读性——
// 500 条日志会把几条任务定义彻底淹没。
type logStore struct {
	mu      sync.RWMutex
	path    string
	records []*RunRecord
}

func newLogStore() *logStore {
	return &logStore{
		path: filepath.Join(cfgpkg.BaseDir, logStoreFileName),
	}
}

// load 读取日志文件。
//
// 与 store.load 的容错级别不同：这里**任何**问题都只记日志并按空列表继续，
// 包括解析失败。日志坏了不该让调度器起不来——任务定义坏了才值得报错。
func (s *logStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.GetLogger().Warnf("Failed to read %s, starting with empty run logs: %v", s.path, err)
		}
		s.records = nil
		return
	}

	if len(data) == 0 {
		s.records = nil
		return
	}

	var records []*RunRecord
	if err := json.Unmarshal(data, &records); err != nil {
		logger.GetLogger().Warnf("Failed to parse %s, starting with empty run logs: %v", s.path, err)
		s.records = nil
		return
	}

	// 上限可能被调小，载入时先裁一次，避免旧文件把内存撑着
	s.records = trimOldest(records, maxRunRecords)
}

// append 追加一条记录并落盘。
func (s *logStore) append(r *RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 先给即将追加的这条腾出位置，再 append，结果恰好是 maxRunRecords
	s.records = append(trimOldest(s.records, maxRunRecords-1), r)
	return writeJSONAtomic(s.path, s.records)
}

// trimOldest 丢弃最旧的记录，使长度不超过 n。
//
// 用 slices.Delete 而非重新切片：它会把尾部让出的槽位清零，
// 否则被丢弃的记录会一直被底层数组引用着，回收不掉。
func trimOldest(records []*RunRecord, n int) []*RunRecord {
	if excess := len(records) - n; excess > 0 {
		return slices.Delete(records, 0, excess)
	}
	return records
}

// list 返回执行记录，**新的在前**。
//
// taskID 非空时只返回该任务的记录；limit <= 0 用 defaultLogLimit。
// 第二个返回值是过滤后、截断前的总数，供前端显示「共 N 条」。
func (s *logStore) list(taskID string, limit int) ([]*RunRecord, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = defaultLogLimit
	}
	if limit > maxRunRecords {
		limit = maxRunRecords
	}

	out := make([]*RunRecord, 0, min(limit, len(s.records)))
	total := 0

	// 倒序遍历：存储是追加序（旧→新），展示要新→旧
	for i := len(s.records) - 1; i >= 0; i-- {
		r := s.records[i]
		if taskID != "" && r.TaskID != taskID {
			continue
		}
		total++
		if len(out) < limit {
			clone := *r
			out = append(out, &clone)
		}
	}

	return out, total
}

// clear 清空全部记录并落盘。
func (s *logStore) clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.records = nil
	return writeJSONAtomic(s.path, []*RunRecord{})
}

// count 返回当前记录总数。
func (s *logStore) count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}
