package state

import (
	"fmt"
	"testing"

	"github.com/dgraph-io/badger/v4"
)

func newTestKV(t *testing.T) *BadgerKV {
	t.Helper()
	opts := badger.DefaultOptions(t.TempDir())
	opts.Logger = nil
	db, err := badger.Open(opts)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &BadgerKV{db: db}
}

func TestBadgerKVRoundTrip(t *testing.T) {
	kv := newTestKV(t)

	if err := kv.Put("metrics:h:0000000001", []byte("host-1")); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if err := kv.Put("metrics:i:server1:0000000001", []byte("inst-1")); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	// 状态记录用的是另一个前缀，扫描时不该被带出来
	if err := kv.Put("state:server1:0001", []byte("state")); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}

	got, err := kv.Scan("metrics:")
	if err != nil {
		t.Fatalf("Scan 失败: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("扫到 %d 个键, 期望 2（state: 前缀不该混进来）", len(got))
	}
	if string(got["metrics:h:0000000001"]) != "host-1" {
		t.Fatalf("值不对: %q", got["metrics:h:0000000001"])
	}

	hostOnly, err := kv.Scan(metricsHostPrefixForTest)
	if err != nil {
		t.Fatalf("Scan 失败: %v", err)
	}
	if len(hostOnly) != 1 {
		t.Fatalf("host 前缀扫到 %d 个键, 期望 1", len(hostOnly))
	}

	if err := kv.Delete([]string{"metrics:h:0000000001"}); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	got, _ = kv.Scan("metrics:")
	if len(got) != 1 {
		t.Fatalf("删除后剩 %d 个键, 期望 1", len(got))
	}
	if _, err := kv.Scan("state:"); err != nil {
		t.Fatalf("状态记录应当不受影响: %v", err)
	}
}

// 删除走分批提交，键数超过一批时也得全删干净。
func TestBadgerKVDeleteBatches(t *testing.T) {
	kv := newTestKV(t)

	const n = 450
	keys := make([]string, 0, n)
	for i := range n {
		key := fmt.Sprintf("metrics:h:%010d", i)
		if err := kv.Put(key, []byte("x")); err != nil {
			t.Fatalf("Put 失败: %v", err)
		}
		keys = append(keys, key)
	}
	if err := kv.Delete(keys); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	got, _ := kv.Scan("metrics:")
	if len(got) != 0 {
		t.Fatalf("删除后仍剩 %d 个键", len(got))
	}
}

func TestMetricsStoreNilWithoutManager(t *testing.T) {
	if MetricsStore() != nil {
		t.Skip("状态管理器已初始化，跳过")
	}
	// 未初始化时返回 nil，调用方（serverinfo）据此退化为纯内存历史
	var s *BadgerKV = MetricsStore()
	if err := s.Put("k", []byte("v")); err == nil {
		t.Fatal("nil 存储上的 Put 应当报错而不是 panic")
	}
}

const metricsHostPrefixForTest = "metrics:h:"
