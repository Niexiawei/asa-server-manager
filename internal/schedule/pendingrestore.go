package schedule

import (
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/logger"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const pendingRestoreFileName = "pending_restore.json"

// PendingRestore 是一份「被管理器停掉、但还没还回去」的现场。
//
// 文件存在本身就是语义：有实例欠着一次启动。内容只用来给用户看和执行恢复，
// 不参与任何状态判断——实例的权威状态始终在 BadgerDB 里。
type PendingRestore struct {
	// Instances 欠着启动的实例名，按首次记录的顺序
	Instances []string `json:"instances"`

	// 任务身份冗余存一份：任务可能已经被删了，提示里还要说得清是谁干的
	TaskID   string `json:"task_id"`
	TaskName string `json:"task_name"`

	// Reason 面向用户，直接展示在提示里
	Reason string `json:"reason"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// pendingStore 是待恢复现场的内存副本 + JSON 落盘。
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
func newPendingStoreAt(path string) *pendingStore {
	return &pendingStore{path: path}
}

// load 读取现场文件。文件不存在视为无现场，不算错误。
//
// JSON 解析失败也只记 WARN 并当作无现场，但不删文件——那份坏文件是排查现场的
// 唯一线索，静默删掉等于毁证。
func (p *pendingStore) load() {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, err := os.ReadFile(p.path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.GetLogger().Errorf("Failed to read %s: %v", p.path, err)
		}
		p.pending = nil
		return
	}
	if len(data) == 0 {
		p.pending = nil
		return
	}

	var pr PendingRestore
	if err := json.Unmarshal(data, &pr); err != nil {
		logger.GetLogger().Warnf("Failed to parse %s, treating as no pending restore (file left untouched for inspection): %v", p.path, err)
		p.pending = nil
		return
	}
	p.pending = &pr
}

func (p *pendingStore) saveLocked() error {
	return writeJSONAtomic(p.path, p.pending)
}

func (p *pendingStore) clearLocked() error {
	p.pending = nil
	if err := os.Remove(p.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Get 返回当前待恢复现场的副本。
func (p *pendingStore) Get() (*PendingRestore, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.pending == nil {
		return nil, false
	}
	clone := *p.pending
	clone.Instances = append([]string(nil), p.pending.Instances...)
	return &clone, true
}

// Merge 把 names 并入现场（并集，保持首次出现顺序），Reason/TaskID/TaskName/UpdatedAt
// 取本次调用的值，CreatedAt 保留最早那次。
//
// 用并集而不是覆盖：连续两个执行点都失败时，第二次不能把第一次的名单冲掉，
// 那样第一批实例就永远没人记得了。
func (p *pendingStore) Merge(taskID, taskName, reason string, names []string) error {
	if len(names) == 0 {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	if p.pending == nil {
		p.pending = &PendingRestore{
			Instances: append([]string(nil), names...),
			TaskID:    taskID,
			TaskName:  taskName,
			Reason:    reason,
			CreatedAt: now,
			UpdatedAt: now,
		}
		return p.saveLocked()
	}

	merged := p.pending.Instances
	for _, n := range names {
		merged = appendUnique(merged, n)
	}
	p.pending.Instances = merged
	p.pending.TaskID = taskID
	p.pending.TaskName = taskName
	p.pending.Reason = reason
	p.pending.UpdatedAt = now
	return p.saveLocked()
}

// Resolve 从现场里移掉已经处理完的实例（差集），reason 更新为本轮的说明。
// 移完之后如果一个都不剩，删掉文件。
//
// 用差集而不是整份覆盖：收尾时手里的名单是这一轮开始时的快照，期间另一条路径
// （定时更新、另一次恢复）完全可能往现场里加了新的实例。整份覆盖会把那些
// 新加的一并抹掉，而它们恰恰是还没人管过的。
func (p *pendingStore) Resolve(handled []string, reason string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.pending == nil {
		return nil
	}

	remaining := excludeNames(p.pending.Instances, handled)
	if len(remaining) == 0 {
		return p.clearLocked()
	}

	p.pending.Instances = remaining
	p.pending.Reason = reason
	p.pending.UpdatedAt = time.Now()
	return p.saveLocked()
}

// Clear 丢弃整个现场（用户点「忽略」）。
func (p *pendingStore) Clear() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.clearLocked()
}
