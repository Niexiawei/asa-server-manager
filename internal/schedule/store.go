package schedule

import (
	cfgpkg "asa-server/internal/config"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const storeFileName = "schedules.json"

// store 是任务列表的内存副本 + JSON 落盘。
//
// 任务量是个位数，整份读写就够了，不需要 KV 库；
// 放成明文 JSON 也方便出问题时直接打开看、手改、备份。
type store struct {
	mu    sync.RWMutex
	path  string
	tasks []*Task
}

func newStore() *store {
	return &store{
		path: filepath.Join(cfgpkg.BaseDir, storeFileName),
	}
}

// load 读取配置文件。文件不存在视为空列表，不算错误（首次启动的正常情况）。
func (s *store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.tasks = nil
			return nil
		}
		return fmt.Errorf("failed to read %s: %w", s.path, err)
	}

	// 空文件同样按空列表处理，避免 Unmarshal 报 "unexpected end of JSON input"
	if len(data) == 0 {
		s.tasks = nil
		return nil
	}

	var tasks []*Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return fmt.Errorf("failed to parse %s: %w", s.path, err)
	}

	s.tasks = tasks
	return nil
}

// saveLocked 整份落盘。调用方必须已持有写锁。
func (s *store) saveLocked() error {
	return writeJSONAtomic(s.path, s.tasks)
}

// List 返回任务列表的深拷贝，避免调用方在锁外改到内部状态。
func (s *store) List() []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		clone := *t
		out = append(out, &clone)
	}
	return out
}

// Get 按 ID 取任务副本。
func (s *store) Get(id string) (*Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, t := range s.tasks {
		if t.ID == id {
			clone := *t
			return &clone, true
		}
	}
	return nil, false
}

// Add 追加任务并落盘。
func (s *store) Add(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tasks = append(s.tasks, t)
	return s.saveLocked()
}

// Update 用 t 覆盖同 ID 的任务并落盘。
func (s *store) Update(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.tasks {
		if existing.ID == t.ID {
			s.tasks[i] = t
			return s.saveLocked()
		}
	}
	return fmt.Errorf("task not found: %s", t.ID)
}

// Delete 删除任务并落盘。
func (s *store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, t := range s.tasks {
		if t.ID == id {
			s.tasks = append(s.tasks[:i], s.tasks[i+1:]...)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("task not found: %s", id)
}

// mutate 在锁内就地修改指定任务后落盘，用于调度器回写 LastRunAt / NextRunAt。
func (s *store) mutate(id string, fn func(*Task)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, t := range s.tasks {
		if t.ID == id {
			fn(t)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("task not found: %s", id)
}
