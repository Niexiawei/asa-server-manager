package schedule

import (
	"asa-server/internal/logger"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// logStore.load 的容错分支会调 logger.GetLogger()，未初始化时它返回 nil 并 panic。
// 统一初始化到临时目录，避免把日志写进仓库。
func TestMain(m *testing.M) {
	logger.InitLoggerWithBaseDir(os.TempDir())
	os.Exit(m.Run())
}

// newTempLogStore 返回一个写到临时目录的 logStore，避免污染 BaseDir。
func newTempLogStore(t *testing.T) *logStore {
	t.Helper()
	return &logStore{path: filepath.Join(t.TempDir(), logStoreFileName)}
}

func makeRecord(taskID, taskName string, success bool) *RunRecord {
	return &RunRecord{
		ID:         newRunRecordID(),
		TaskID:     taskID,
		TaskName:   taskName,
		TaskType:   TaskRestart,
		Trigger:    TriggerSchedule,
		StartedAt:  time.Now(),
		DurationMs: 1000,
		Success:    success,
		Message:    "ok",
	}
}

// 超出上限时丢弃最旧的，而不是清空——最新那条必须还在，
// 否则用户会在第 501 次执行后突然失去全部历史。
func TestAppendTrimsOldest(t *testing.T) {
	s := newTempLogStore(t)

	for i := range maxRunRecords + 50 {
		r := makeRecord("task-1", "任务", true)
		r.Message = string(rune('a' + i%26))
		r.ID = newRunRecordID()
		if err := s.append(r); err != nil {
			t.Fatalf("append 第 %d 条失败: %v", i, err)
		}
	}

	if got := s.count(); got != maxRunRecords {
		t.Errorf("记录数应恒为 %d，实际 %d", maxRunRecords, got)
	}

	// 最新的那条必须在，且排在最前
	logs, _ := s.list("", 1)
	if len(logs) != 1 {
		t.Fatalf("应返回 1 条，实际 %d", len(logs))
	}
	last := s.records[len(s.records)-1]
	if logs[0].ID != last.ID {
		t.Errorf("list 应把最新的排在最前：got %s, want %s", logs[0].ID, last.ID)
	}
}

// 上限被调小时（或载入了旧的超长文件），一次要能裁掉多条
func TestTrimOldestRemovesMultiple(t *testing.T) {
	records := make([]*RunRecord, 0, 10)
	for range 10 {
		records = append(records, makeRecord("t", "任务", true))
	}
	lastID := records[9].ID

	got := trimOldest(records, 3)

	if len(got) != 3 {
		t.Fatalf("应裁到 3 条，实际 %d", len(got))
	}
	if got[2].ID != lastID {
		t.Error("裁剪应保留最新的记录")
	}
}

func TestTrimOldestKeepsShortSlice(t *testing.T) {
	records := []*RunRecord{makeRecord("t", "任务", true)}
	if got := trimOldest(records, 500); len(got) != 1 {
		t.Errorf("未超上限时不应裁剪，实际长度 %d", len(got))
	}
	if got := trimOldest(nil, 500); got != nil {
		t.Errorf("空切片应原样返回，实际 %v", got)
	}
}

func TestListFilterAndLimit(t *testing.T) {
	s := newTempLogStore(t)

	for range 3 {
		if err := s.append(makeRecord("task-a", "任务 A", true)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := s.append(makeRecord("task-b", "任务 B", false)); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("不过滤返回全部", func(t *testing.T) {
		logs, total := s.list("", 100)
		if len(logs) != 5 || total != 5 {
			t.Errorf("got len=%d total=%d, want 5/5", len(logs), total)
		}
	})

	t.Run("按任务过滤", func(t *testing.T) {
		logs, total := s.list("task-a", 100)
		if len(logs) != 3 || total != 3 {
			t.Errorf("got len=%d total=%d, want 3/3", len(logs), total)
		}
		for _, r := range logs {
			if r.TaskID != "task-a" {
				t.Errorf("混入了其它任务的记录: %s", r.TaskID)
			}
		}
	})

	t.Run("limit 截断但 total 是截断前的数量", func(t *testing.T) {
		logs, total := s.list("", 2)
		if len(logs) != 2 {
			t.Errorf("应截断到 2 条，实际 %d", len(logs))
		}
		if total != 5 {
			t.Errorf("total 应为截断前的 5，实际 %d", total)
		}
	})

	t.Run("倒序：新的在前", func(t *testing.T) {
		logs, _ := s.list("", 100)
		for i := 1; i < len(logs); i++ {
			if logs[i-1].StartedAt.Before(logs[i].StartedAt) {
				t.Error("list 应按时间倒序返回")
				break
			}
		}
		if logs[0].TaskID != "task-b" {
			t.Errorf("最新的应是最后追加的 task-b，实际 %s", logs[0].TaskID)
		}
	})

	t.Run("不存在的任务返回空", func(t *testing.T) {
		logs, total := s.list("nope", 100)
		if len(logs) != 0 || total != 0 {
			t.Errorf("got len=%d total=%d, want 0/0", len(logs), total)
		}
	})
}

// list 返回的必须是副本，调用方改它不能影响存储
func TestListReturnsCopies(t *testing.T) {
	s := newTempLogStore(t)
	if err := s.append(makeRecord("t", "原名", true)); err != nil {
		t.Fatal(err)
	}

	logs, _ := s.list("", 10)
	logs[0].TaskName = "被改了"

	again, _ := s.list("", 10)
	if again[0].TaskName != "原名" {
		t.Error("list 应返回副本，调用方的修改不该写回存储")
	}
}

// 日志坏了不该让调度器起不来——任务定义坏了才值得报错
func TestLoadIsFaultTolerant(t *testing.T) {
	cases := []struct {
		name    string
		content string
		write   bool
	}{
		{name: "文件不存在", write: false},
		{name: "空文件", content: "", write: true},
		{name: "内容损坏", content: "{ this is not json", write: true},
		{name: "顶层不是数组", content: `{"foo":"bar"}`, write: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTempLogStore(t)
			if tc.write {
				if err := os.WriteFile(s.path, []byte(tc.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			s.load() // 不返回 error，坏了只记 WARN

			if s.count() != 0 {
				t.Errorf("应按空列表继续，实际 %d 条", s.count())
			}

			// 载入失败后仍然可以正常追加
			if err := s.append(makeRecord("t", "任务", true)); err != nil {
				t.Errorf("载入失败后应仍可追加: %v", err)
			}
		})
	}
}

func TestLoadRoundTrip(t *testing.T) {
	s := newTempLogStore(t)

	for range 3 {
		if err := s.append(makeRecord("task-a", "任务 A", true)); err != nil {
			t.Fatal(err)
		}
	}
	want := s.count()

	reloaded := &logStore{path: s.path}
	reloaded.load()

	if reloaded.count() != want {
		t.Errorf("重启后应恢复 %d 条，实际 %d", want, reloaded.count())
	}

	logs, _ := reloaded.list("", 10)
	if logs[0].TaskName != "任务 A" || !logs[0].Success {
		t.Errorf("字段未正确恢复: %+v", logs[0])
	}
}

// 载入时也要裁剪：上限被调小后，旧文件不该把内存撑着
func TestLoadTrimsOversizedFile(t *testing.T) {
	s := newTempLogStore(t)

	oversized := make([]*RunRecord, 0, maxRunRecords+20)
	for range maxRunRecords + 20 {
		oversized = append(oversized, makeRecord("t", "任务", true))
	}
	data, err := json.MarshalIndent(oversized, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	s.load()

	if s.count() != maxRunRecords {
		t.Errorf("载入时应裁到 %d 条，实际 %d", maxRunRecords, s.count())
	}
}

// clear 之后文件应是空数组而不是被删除，
// 否则下次 load 走的是「文件不存在」分支，语义上区分不了「清空过」和「从没跑过」
func TestClear(t *testing.T) {
	s := newTempLogStore(t)
	if err := s.append(makeRecord("t", "任务", true)); err != nil {
		t.Fatal(err)
	}

	if err := s.clear(); err != nil {
		t.Fatalf("clear 失败: %v", err)
	}
	if s.count() != 0 {
		t.Errorf("清空后应为 0 条，实际 %d", s.count())
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatalf("清空后文件应仍然存在: %v", err)
	}

	var records []*RunRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Errorf("清空后的文件应是合法的空数组: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("应为空数组，实际 %d 条", len(records))
	}
}

// 手动执行与定时触发可能几乎同时落记录，只靠纳秒时间戳不保险
func TestRunRecordIDIsUnique(t *testing.T) {
	const n = 1000
	seen := make(map[string]bool, n)

	for range n {
		id := newRunRecordID()
		if seen[id] {
			t.Fatalf("ID 重复: %s", id)
		}
		seen[id] = true
	}
}
