package schedule

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPendingStore_EmptyDirNoPending(t *testing.T) {
	p := newPendingStoreAt(filepath.Join(t.TempDir(), "pending_restore.json"))
	p.load()

	if _, ok := p.Get(); ok {
		t.Fatalf("expected no pending restore in an empty directory")
	}
}

func TestPendingStore_MergeThenGet(t *testing.T) {
	p := newPendingStoreAt(filepath.Join(t.TempDir(), "pending_restore.json"))
	p.load()

	if err := p.Merge("t1", "更新任务", "更新过程中管理器退出", []string{"a", "b"}); err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	got, ok := p.Get()
	if !ok {
		t.Fatalf("expected pending restore after Merge")
	}
	if got.TaskID != "t1" || got.TaskName != "更新任务" {
		t.Errorf("unexpected task identity: %+v", got)
	}
	if len(got.Instances) != 2 || got.Instances[0] != "a" || got.Instances[1] != "b" {
		t.Errorf("unexpected instances: %v", got.Instances)
	}
}

func TestPendingStore_MergeIsUnionNotOverwrite(t *testing.T) {
	p := newPendingStoreAt(filepath.Join(t.TempDir(), "pending_restore.json"))
	p.load()

	if err := p.Merge("t1", "任务1", "原因1", []string{"a"}); err != nil {
		t.Fatalf("first Merge failed: %v", err)
	}
	if err := p.Merge("t2", "任务2", "原因2", []string{"b"}); err != nil {
		t.Fatalf("second Merge failed: %v", err)
	}

	got, ok := p.Get()
	if !ok {
		t.Fatalf("expected pending restore")
	}
	if len(got.Instances) != 2 || got.Instances[0] != "a" || got.Instances[1] != "b" {
		t.Errorf("expected union [a b], got %v", got.Instances)
	}
	// 最新一次的任务身份与原因应当覆盖
	if got.TaskID != "t2" || got.Reason != "原因2" {
		t.Errorf("expected latest task identity/reason, got %+v", got)
	}
}

func TestPendingStore_MergeDuplicateDoesNotAppend(t *testing.T) {
	p := newPendingStoreAt(filepath.Join(t.TempDir(), "pending_restore.json"))
	p.load()

	_ = p.Merge("t1", "任务1", "原因1", []string{"a", "b"})
	_ = p.Merge("t1", "任务1", "原因2", []string{"b", "c"})

	got, _ := p.Get()
	want := []string{"a", "b", "c"}
	if len(got.Instances) != len(want) {
		t.Fatalf("expected %v, got %v", want, got.Instances)
	}
	for i, name := range want {
		if got.Instances[i] != name {
			t.Errorf("expected %v, got %v", want, got.Instances)
			break
		}
	}
}

func TestPendingStore_ResolvePartialKeepsRemainder(t *testing.T) {
	p := newPendingStoreAt(filepath.Join(t.TempDir(), "pending_restore.json"))
	p.load()
	_ = p.Merge("t1", "任务1", "原因", []string{"a", "b", "c"})

	if err := p.Resolve([]string{"a", "c"}, "剩余原因"); err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	got, ok := p.Get()
	if !ok {
		t.Fatalf("expected pending restore to remain (b still unresolved)")
	}
	if len(got.Instances) != 1 || got.Instances[0] != "b" {
		t.Errorf("expected [b], got %v", got.Instances)
	}
	if got.Reason != "剩余原因" {
		t.Errorf("expected updated reason, got %q", got.Reason)
	}
}

func TestPendingStore_ResolveAllClearsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending_restore.json")
	p := newPendingStoreAt(path)
	p.load()
	_ = p.Merge("t1", "任务1", "原因", []string{"a", "b"})

	if err := p.Resolve([]string{"a", "b"}, ""); err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if _, ok := p.Get(); ok {
		t.Fatalf("expected no pending restore after resolving everything")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file to be removed, stat err = %v", err)
	}
}

func TestPendingStore_ClearRemovesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending_restore.json")
	p := newPendingStoreAt(path)
	p.load()
	_ = p.Merge("t1", "任务1", "原因", []string{"a"})

	if err := p.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}
	if _, ok := p.Get(); ok {
		t.Fatalf("expected no pending restore after Clear")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file to be removed, stat err = %v", err)
	}
}

func TestPendingStore_CorruptFileTreatedAsNoneButKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending_restore.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0644); err != nil {
		t.Fatalf("failed to seed corrupt file: %v", err)
	}

	p := newPendingStoreAt(path)
	p.load()

	if _, ok := p.Get(); ok {
		t.Fatalf("expected corrupt file to be treated as no pending restore")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected corrupt file to be left in place for inspection, got stat err: %v", err)
	}
}
