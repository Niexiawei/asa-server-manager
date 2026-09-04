package state

import (
	"fmt"

	"github.com/dgraph-io/badger/v4"
)

// 资源指标历史的持久化后端。
//
// 复用状态库那个 Badger 实例，不另开一个 LSM —— 键前缀 `metrics:` 与状态记录的
// `state:` 互不干扰（本文件之外的每一处迭代都按 `state:` 前缀取）。
// 注意 ClearStateDatabase() 是整目录删除，会连带清掉指标历史，这是预期行为。
//
// 这里只实现 serverinfo.HistoryStore 那三个搬字节的方法：分块、编码、过期裁剪
// 全在 pkg/serverinfo 侧，那边不认识 Badger，这边不认识指标。

// MetricsStore 返回一个绑定在状态库上的 KV 存储；状态管理器未初始化时返回 nil。
func MetricsStore() *BadgerKV {
	sm := GetStateManager()
	if sm == nil {
		return nil
	}
	return &BadgerKV{db: sm.db}
}

// BadgerKV 是一层极薄的字节读写封装。
type BadgerKV struct {
	db *badger.DB
}

func (s *BadgerKV) Put(key string, value []byte) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("状态数据库未初始化")
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), value)
	})
}

func (s *BadgerKV) Scan(prefix string) (map[string][]byte, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("状态数据库未初始化")
	}
	out := make(map[string][]byte)
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefix)
		it := txn.NewIterator(opts)
		defer it.Close()

		p := []byte(prefix)
		for it.Seek(p); it.ValidForPrefix(p); it.Next() {
			item := it.Item()
			val, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			out[string(item.Key())] = val
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *BadgerKV) Delete(keys []string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("状态数据库未初始化")
	}
	// 一个事务塞太多键会撞上 Badger 的事务大小上限，分批提交
	const batchSize = 200
	for start := 0; start < len(keys); start += batchSize {
		end := min(start+batchSize, len(keys))
		batch := keys[start:end]
		err := s.db.Update(func(txn *badger.Txn) error {
			for _, k := range batch {
				if err := txn.Delete([]byte(k)); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
